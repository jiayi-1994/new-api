package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

// Video scheduling health tracks, per channel x client model, a submit
// acceptance window and a generation success window, plus in-flight gauges
// and probe leases. State lives in Redis when enabled (shared by every
// instance) and in process memory otherwise (single instance). Only channels
// with a video_scheduling config are tracked, and only while the mode is not
// off; tasks remember that decision in PrivateData.SchedulingSummary.

const (
	videoSchedKeyPrefix        = "video_sched:"
	videoSchedAllModels        = "*"
	videoInFlightPrefix        = videoSchedKeyPrefix + "inflight:"
	videoSchedCalibrationKey   = videoSchedKeyPrefix + "calib"
	videoSchedCalibratedKey    = videoSchedKeyPrefix + "capacity_owners_ready"
	videoCapacityRevisionsKey  = videoSchedKeyPrefix + "capacity_revisions"
	videoCapacityOwnersPrefix  = videoSchedKeyPrefix + "capacity_owners:"
	videoCapacityClosedPrefix  = videoSchedKeyPrefix + "capacity_closed:"
	videoSchedCalibrationTick  = time.Minute
	videoSchedProbeCooldownMax = 24 * time.Hour
)

// Health sample fields; each bucket i stores <field>_i plus ts_i (its minute).
const (
	videoSubmitOK   = "s_ok"
	videoSubmitFail = "s_fail"
	videoGenOK      = "g_ok"
	videoGenFail    = "g_fail"
)

var videoSampleFields = []string{videoSubmitOK, videoSubmitFail, videoGenOK, videoGenFail}

type VideoOutcome int

const (
	VideoOutcomeIgnored VideoOutcome = iota
	VideoOutcomeSuccess
	VideoOutcomeFail
)

type VideoProbeState struct {
	LastProbeAt      int64 `json:"last_probe_at"`
	ConsecutiveFails int   `json:"consecutive_fails"`
}

type VideoChannelHealth struct {
	Submit      videosched.HealthStat           `json:"submit"`
	Gen         videosched.HealthStat           `json:"gen"`
	InFlight    int                             `json:"in_flight"`
	Probe       VideoProbeState                 `json:"probe"`
	Reliability *videosched.ReliabilitySnapshot `json:"reliability,omitempty"`
}

// videoProbeLease is a probe slot owned by the request until its task is
// persisted; afterwards the task's SchedulingSummary owns it.
type videoProbeLease struct {
	ChannelID  int
	Key, Token string
}

// VideoSchedulingTracks reports whether a channel's traffic feeds channel
// health: scheduling is not off and the channel has a cost table. A channel
// built from request context carries no settings; its cached copy is read.
func VideoSchedulingTracks(channel *model.Channel) (*dto.VideoSchedulingConfig, bool) {
	if operation_setting.GetVideoSchedulingSetting().Mode == operation_setting.VideoSchedulingModeOff {
		return nil, false
	}
	return VideoSchedulingConfigOf(channel)
}

// VideoSchedulingConfigOf returns a channel's video_scheduling config whatever
// the live mode: a request whose takeover was frozen keeps its candidates even
// if scheduling is switched off mid-request.
func VideoSchedulingConfigOf(channel *model.Channel) (*dto.VideoSchedulingConfig, bool) {
	if channel == nil {
		return nil, false
	}
	if channel.OtherSettings == "" && channel.Id > 0 {
		full, err := model.CacheGetChannel(channel.Id)
		if err != nil {
			return nil, false
		}
		channel = full
	}
	var settings dto.ChannelOtherSettings
	if channel.OtherSettings == "" || common.UnmarshalJsonStr(channel.OtherSettings, &settings) != nil || settings.VideoScheduling == nil {
		return nil, false
	}
	return settings.VideoScheduling, true
}

func videoHealthKey(channelID int, modelName string) string {
	return fmt.Sprintf("%sh:%d:%s", videoSchedKeyPrefix, channelID, modelName)
}

func videoInFlightKey(channelID int) string {
	return fmt.Sprintf("%sinflight:%d", videoSchedKeyPrefix, channelID)
}

func videoGroupInFlightKey(group string) string {
	return videoSchedKeyPrefix + "inflight:g:" + group
}

const (
	videoCapacityReservationKey = "video_capacity_reservation"
	videoCapacityOwnershipKey   = "video_capacity_ownership"
	// videoRateLimitCooldown keeps a channel out of selection after its upstream
	// answered a submit with 429. ponytail: fixed; honor Retry-After if
	// upstreams start sending one.
	videoRateLimitCooldown = time.Minute
	videoSchedRateLimited  = "rate_limited_cooldown"
)

func videoCooldownKey(channelID int) string {
	return fmt.Sprintf("%scooldown:%d", videoSchedKeyPrefix, channelID)
}

// videoCapacityReservation counts a request in the channel's in-flight gauges
// from admission on. Counting only once the task was persisted let a burst
// read the same free capacity and overshoot it by the whole burst.
type videoCapacityReservation struct {
	ChannelID   int
	Keys        []string
	Token       string
	Group       string
	HoldSeconds int64
	store       videoHealthBackend
	stop        func()
	once        sync.Once
}

type videoCapacityOwnership struct {
	ChannelID    int
	Group, Token string
}

// Admission freezes ownership even for unlimited/shadow channels: later
// channel edits cannot move a task to another account or remove its tracking.
func freezeVideoCapacityOwnership(c *gin.Context, channel *model.Channel) *videoCapacityOwnership {
	if frozen, ok := common.GetContextKeyType[*videoCapacityOwnership](c, videoCapacityOwnershipKey); ok && frozen != nil && frozen.ChannelID == channel.Id {
		return frozen
	}
	cfg, ok := VideoSchedulingConfigOf(channel)
	if !ok {
		return nil
	}
	owner := &videoCapacityOwnership{ChannelID: channel.Id, Group: cfg.CapacityGroup, Token: common.GetRandomString(24)}
	c.Set(videoCapacityOwnershipKey, owner)
	return owner
}

// reserveVideoCapacity replaces the request's previous reservation, if any.
// An unlimited channel reserves nothing, so a store outage cannot refuse it;
// its task is counted once persisted.
func reserveVideoCapacity(c *gin.Context, channel *model.Channel, groupQuotas map[string]int) (bool, error) {
	releaseVideoCapacityReservation(c)
	c.Set(videoCapacityOwnershipKey, nil)
	cfg, ok := VideoSchedulingConfigOf(channel)
	if !ok {
		return true, nil
	}
	owner := freezeVideoCapacityOwnership(c, channel)
	if cfg.Capacity <= 0 && groupQuotas[cfg.CapacityGroup] <= 0 {
		return true, nil
	}
	if common.RedisEnabled && common.RDB == nil {
		return false, ErrVideoHealthAdmission
	}
	keys, limits := []string{videoInFlightKey(channel.Id)}, []int64{int64(cfg.Capacity)}
	if cfg.CapacityGroup != "" {
		keys, limits = append(keys, videoGroupInFlightKey(cfg.CapacityGroup)), append(limits, int64(groupQuotas[cfg.CapacityGroup]))
	}
	store, token := videoHealthStore(), owner.Token
	reserved, err := store.reserve(keys, limits, token, time.Now().Add(videoSubmissionLeaseTTL).UnixMilli())
	if reserved {
		holdSeconds := int64(24 * time.Hour / time.Second)
		if constant.TaskTimeoutMinutes > 0 {
			holdSeconds = int64(constant.TaskTimeoutMinutes) * 60
		}
		reservation := &videoCapacityReservation{ChannelID: channel.Id, Keys: keys, Token: token, Group: owner.Group, HoldSeconds: holdSeconds, store: store}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		reservation.stop = func() { cancel(); <-done }
		go func() {
			defer close(done)
			ticker := time.NewTicker(videoSubmissionLeaseTTL / 4)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					renewCtx, renewCancel := context.WithTimeout(ctx, time.Second)
					err := store.renewCapacity(renewCtx, keys, token, time.Now().Add(videoSubmissionLeaseTTL).UnixMilli())
					renewCancel()
					if err != nil && ctx.Err() == nil {
						common.SysError(fmt.Sprintf("video scheduling capacity renewal failed: channel=%d error=%v", channel.Id, err))
					}
				}
			}
		}()
		c.Set(videoCapacityReservationKey, reservation)
	}
	return reserved, err
}

func takeVideoCapacityReservation(c *gin.Context) *videoCapacityReservation {
	if c == nil {
		return nil
	}
	value, _ := c.Get(videoCapacityReservationKey)
	c.Set(videoCapacityReservationKey, nil)
	reservation, _ := value.(*videoCapacityReservation)
	return reservation
}

func (r *videoCapacityReservation) release() {
	r.finish(false)
}

func (r *videoCapacityReservation) finish(persisted bool) {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.stop != nil {
			r.stop()
		}
		if err := r.store.finishCapacity(r.Keys, r.Token, persisted); err != nil {
			common.SysError(fmt.Sprintf("video scheduling capacity handoff failed: channel=%d error=%v", r.ChannelID, err))
		}
	})
}

func (r *videoCapacityReservation) retainUnknown() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.stop != nil {
			r.stop()
		}
		if err := r.store.retainCapacity(r.Keys, r.Token, time.Now().Add(time.Duration(r.HoldSeconds)*time.Second).UnixMilli()); err != nil {
			common.SysError(fmt.Sprintf("video scheduling unknown capacity hold failed: channel=%d error=%v", r.ChannelID, err))
		}
	})
}

func releaseVideoCapacityReservation(c *gin.Context) {
	reservation := takeVideoCapacityReservation(c)
	if reservation == nil {
		return
	}
	if attempt, ok := common.GetContextKeyType[*model.VideoHealthAttempt](c, videoHealthAttemptKey); ok && attempt != nil && attempt.CapacityToken == reservation.Token && (attempt.FinalOutcome == "" || attempt.FinalOutcome == "unknown") {
		reservation.retainUnknown()
		return
	}
	reservation.release()
}

func videoProbeStateKeys(channelID int) (last, fails string) {
	prefix := fmt.Sprintf("%sprobe_state:%d:", videoSchedKeyPrefix, channelID)
	return prefix + "last", prefix + "fails"
}

func videoWindowBuckets() (int, int64) {
	window := operation_setting.GetVideoSchedulingSetting().WindowSeconds
	return max(window/60, 1), time.Now().Unix() / 60
}

func recordVideoSamples(channelID int, modelName, field string) {
	buckets, minute := videoWindowBuckets()
	store := videoHealthStore()
	for _, key := range []string{videoHealthKey(channelID, modelName), videoHealthKey(channelID, videoSchedAllModels)} {
		if err := store.addSample(key, field, minute, buckets); err != nil {
			common.SysError(fmt.Sprintf("video scheduling health write failed: key=%s error=%v", key, err))
		}
	}
}

// ObserveVideoSubmit records one submission attempt. A probe lease that the
// attempt did not turn into an accepted task is released at once, and a
// failure attributed to the upstream extends the probe cooldown.
func ObserveVideoSubmit(c *gin.Context, channel *model.Channel, modelName string, taskErr *taskdto.TaskError) {
	ObserveVideoReliabilitySubmit(c, taskErr, false)
	captureVideoAuditSubmit(c, channel.Id, taskErr)
	outcome := videoSubmitOutcome(taskErr)
	if taskErr != nil {
		if errors.Is(taskErr.Error, relaycommon.ErrTaskSubmitOutcomeUnknown) {
			takeVideoCapacityReservation(c).retainUnknown()
		} else {
			releaseVideoCapacityReservation(c)
		}
		if taskErr.StatusCode == http.StatusTooManyRequests && !taskErr.LocalError {
			if _, err := videoHealthStore().acquire(videoCooldownKey(channel.Id), "429", videoRateLimitCooldown); err != nil {
				common.SysError(fmt.Sprintf("video scheduling cooldown write failed: channel=%d error=%v", channel.Id, err))
			}
		}
		if lease, ok := takeVideoProbeLease(c, channel); ok {
			releaseVideoProbeSlot(lease.Key, lease.Token)
			recordVideoProbeOutcome(lease.ChannelID, outcome)
		}
	}
	if _, ok := VideoSchedulingTracks(channel); !ok || outcome == VideoOutcomeIgnored {
		return
	}
	field := videoSubmitOK
	if outcome == VideoOutcomeFail {
		field = videoSubmitFail
	}
	recordVideoSamples(channel.Id, modelName, field)
}

// NewVideoSchedulingSummary is written into the task before insert. It moves
// the request's probe lease for this channel into the task, but the request
// keeps ownership until VideoTaskPersisted confirms the insert.
func NewVideoSchedulingSummary(c *gin.Context, channel *model.Channel, modelName string) *model.TaskSchedulingSummary {
	decision := VideoSchedDecisionFrom(c)
	if !decision.Takeover && !decision.Shadow && operation_setting.GetVideoSchedulingSetting().Mode == operation_setting.VideoSchedulingModeOff {
		return nil
	}
	owner := freezeVideoCapacityOwnership(c, channel)
	if owner == nil {
		return nil
	}
	summary := &model.TaskSchedulingSummary{Model: modelName, CapacityGroup: owner.Group, CapacityToken: owner.Token}
	if lease, ok := peekVideoProbeLease(c); ok && lease.ChannelID == channel.Id {
		summary.ProbeSlot = &model.TaskProbeSlot{Key: lease.Key, Token: lease.Token}
	}
	// The selection that fed the submitting attempt, so terminal logs can be
	// matched to the consumption log's attempts by task ID.
	if record, row, ok := videoScheduleSelection(c, channel.Id); ok {
		summary.Selected, summary.SelectionSeq, summary.AttemptSeq, summary.Probe, summary.Spec = channel.Id, record.SelectionSeq, record.AttemptSeq, record.Probe, row.Spec
	}
	return summary
}

// VideoTaskPersisted runs right after the task insert succeeds, before billing
// settlement. The task now owns any probe slot. An immediate terminal result
// is observed once and never counted in flight; any other task is counted
// until one of the terminal paths releases it.
func VideoTaskPersisted(c *gin.Context, task *model.Task) {
	linkVideoReliabilityTask(c, task)
	reservation := takeVideoCapacityReservation(c)
	summary := task.PrivateData.SchedulingSummary
	audit := videoScheduleAuditState(c)
	if audit != nil {
		id := task.ID
		audit.TaskPK, audit.TaskID, audit.Platform = &id, task.TaskID, string(task.Platform)
	}
	if summary != nil && summary.ProbeSlot != nil {
		c.Set(string(constant.ContextKeyVideoSchedProbeLease), nil)
	}
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		reservation.release()
		if summary == nil && audit == nil && task.PrivateData.VideoHealth == nil {
			return
		}
		outcome, attribution := videoTerminalAttribution(task, false)
		observeVideoReliabilityTerminal(task, outcome, attribution)
		if audit != nil {
			observedAt := time.Now().UnixMilli()
			audit.TaskStatus, audit.TerminalClass, audit.TerminalAt = string(task.Status), attribution, &observedAt
			audit.TerminalHealth = "ignored"
			if outcome == VideoOutcomeSuccess {
				audit.TerminalHealth = "success"
			} else if outcome == VideoOutcomeFail {
				audit.TerminalHealth = "fail"
			}
		}
		observeVideoTerminal(task, false, outcome)
		return
	}
	if summary == nil {
		reservation.release()
		return
	}
	keys := videoInFlightKeys(task.ChannelId, summary)
	if reservation != nil && reservation.ChannelID == task.ChannelId && slices.Equal(reservation.Keys, keys) {
		reservation.finish(true)
		return // the reservation already counts this task until its terminal release
	}
	reservation.release()
	if err := videoHealthStore().finishCapacity(keys, videoTaskCapacityToken(task), true); err != nil {
		common.SysError(fmt.Sprintf("video scheduling task capacity handoff failed: channel=%d error=%v", task.ChannelId, err))
	}
}

// ObserveVideoTerminal is called exactly once per polled task by the
// state-transition winner: finalizeTerminalTask, the timeout sweep or the
// realtime fetch. It releases the task's in-flight count. hostFailure marks a
// failure the host detected itself (timeout, poll failure escalation).
func ObserveVideoTerminal(task *model.Task, hostFailure bool) {
	outcome, attribution := videoTerminalAttribution(task, hostFailure)
	observeVideoReliabilityTerminal(task, outcome, attribution)
	ObserveVideoScheduleAuditTerminal(task, attribution, outcome)
	observeVideoTerminal(task, true, outcome)
}

// observeVideoTerminal records the generation outcome; counted tells whether
// the task sits in the in-flight gauges (immediate results never do).
func observeVideoTerminal(task *model.Task, counted bool, outcome VideoOutcome) {
	summary := task.PrivateData.SchedulingSummary
	if summary == nil {
		return
	}
	if outcome != VideoOutcomeIgnored {
		field := videoGenOK
		if outcome == VideoOutcomeFail {
			field = videoGenFail
		}
		recordVideoSamples(task.ChannelId, summary.Model, field)
	}
	if counted {
		if err := videoHealthStore().finishCapacity(videoInFlightKeys(task.ChannelId, summary), videoTaskCapacityToken(task), false); err != nil {
			common.SysError(fmt.Sprintf("video scheduling task capacity release failed: channel=%d error=%v", task.ChannelId, err))
		}
	}
	if slot := summary.ProbeSlot; slot != nil {
		releaseVideoProbeSlot(slot.Key, slot.Token)
		recordVideoProbeOutcome(task.ChannelId, outcome)
	}
}

func videoTaskCapacityToken(task *model.Task) string {
	if task.PrivateData.SchedulingSummary != nil && task.PrivateData.SchedulingSummary.CapacityToken != "" {
		return task.PrivateData.SchedulingSummary.CapacityToken
	}
	return fmt.Sprintf("task:%d", task.ID)
}

func videoInFlightKeys(channelID int, summary *model.TaskSchedulingSummary) []string {
	keys := []string{videoInFlightKey(channelID)}
	if summary.CapacityGroup != "" {
		keys = append(keys, videoGroupInFlightKey(summary.CapacityGroup))
	}
	return keys
}

// GetVideoChannelHealth reads both windows for a channel and model. Each
// metric uses the model-level window when it has minSamples, else the
// channel-level window when that one does, else the model-level window.
func GetVideoChannelHealth(channelID int, modelName string, minSamples int) (VideoChannelHealth, error) {
	buckets, minute := videoWindowBuckets()
	store := videoHealthStore()
	modelCounts, err := store.window(videoHealthKey(channelID, modelName), minute, buckets)
	if err != nil {
		return VideoChannelHealth{}, err
	}
	channelCounts, err := store.window(videoHealthKey(channelID, videoSchedAllModels), minute, buckets)
	if err != nil {
		return VideoChannelHealth{}, err
	}
	pick := func(ok, fail string) videosched.HealthStat {
		stat := videoHealthStat(modelCounts[ok], modelCounts[fail])
		if stat.Samples < minSamples {
			if wide := videoHealthStat(channelCounts[ok], channelCounts[fail]); wide.Samples >= minSamples {
				return wide
			}
		}
		return stat
	}
	health := VideoChannelHealth{Submit: pick(videoSubmitOK, videoSubmitFail), Gen: pick(videoGenOK, videoGenFail), Reliability: GetVideoReliability(channelID, modelName)}
	lastKey, failsKey := videoProbeStateKeys(channelID)
	values, err := store.get([]string{videoInFlightKey(channelID), lastKey, failsKey})
	if err != nil {
		return VideoChannelHealth{}, err
	}
	health.InFlight = int(values[0])
	health.Probe = VideoProbeState{LastProbeAt: values[1], ConsecutiveFails: int(values[2])}
	return health, nil
}

// VideoHealthView is an operator view of one scheduled channel: its
// channel-wide windows, in-flight count and probe state, whether a health gate
// holds it back under the current setting, its capacity, held probe slots and,
// on request, the windows of each model it prices.
type VideoHealthView struct {
	VideoChannelHealth
	SelectionPolicy     string                        `json:"selection_policy"`
	AsOf                int64                         `json:"as_of"`
	ValidationSlotsHeld int                           `json:"validation_slots_held"`
	ExploreLimit        int                           `json:"explore_limit"`
	RecoveryLimit       int                           `json:"recovery_limit"`
	Gated               bool                          `json:"gated"`
	Capacity            int                           `json:"capacity"`
	CapacityGroup       string                        `json:"capacity_group,omitempty"`
	GroupCapacity       int                           `json:"group_capacity,omitempty"`
	GroupInFlight       int                           `json:"group_in_flight,omitempty"`
	ProbeSlotsHeld      int                           `json:"probe_slots_held"`
	ProbeCooldownUntil  int64                         `json:"probe_cooldown_until,omitempty"` // unix seconds after the last probe
	Models              map[string]VideoChannelHealth `json:"models,omitempty"`
}

// GetVideoHealthView reads a channel's scheduling state from Redis or memory
// only, never the database for a channel that carries its settings. It
// returns nil for a channel without a video_scheduling config.
func GetVideoHealthView(channel *model.Channel, perModel bool) (*VideoHealthView, error) {
	cfg, ok := VideoSchedulingConfigOf(channel)
	if !ok {
		return nil, nil
	}
	setting := operation_setting.GetVideoSchedulingSetting()
	health, err := GetVideoChannelHealth(channel.Id, videoSchedAllModels, setting.MinSamples)
	if err != nil {
		return nil, err
	}
	view := &VideoHealthView{
		VideoChannelHealth: health,
		SelectionPolicy:    setting.SelectionPolicy, AsOf: time.Now().Unix(), ExploreLimit: setting.ExploreMaxInFlight, RecoveryLimit: setting.ProbeMaxInFlight,
		Gated:         videoGated(health.Submit, health.Gen, setting.MinSubmitRate, setting.MinGenRate, setting.MinSamples),
		Capacity:      cfg.Capacity,
		CapacityGroup: cfg.CapacityGroup,
		GroupCapacity: setting.CapacityGroups[cfg.CapacityGroup],
	}
	if setting.SelectionPolicy == videosched.PolicyStabilityCostV2 {
		if view.ValidationSlotsHeld, err = videoHealthStore().held(videoValidationKeys(channel.Id)); err != nil {
			return nil, err
		}
	}
	if view.GroupCapacity > 0 {
		if view.GroupInFlight, err = GetVideoGroupInFlight(cfg.CapacityGroup); err != nil {
			return nil, err
		}
	}
	if view.ProbeSlotsHeld, err = VideoProbeSlotsHeld(channel.Id, setting.ProbeMaxInFlight); err != nil {
		return nil, err
	}
	if last := health.Probe.LastProbeAt; last > 0 {
		view.ProbeCooldownUntil = last + int64(VideoProbeCooldown(setting.ProbeCooldownSec, health.Probe).Seconds())
	}
	if perModel || setting.SelectionPolicy == videosched.PolicyStabilityCostV2 {
		view.Models = make(map[string]VideoChannelHealth, len(cfg.Models))
		for modelName := range strings.SplitSeq(channel.Models, ",") {
			if _, priced := videoModelCost(cfg.Models, modelName); modelName == "" || !priced {
				continue
			}
			if view.Models[modelName], err = GetVideoChannelHealth(channel.Id, modelName, setting.MinSamples); err != nil {
				return nil, err
			}
		}
	}
	return view, nil
}

// videoGated reports a health gate holding a channel back: a metric with at
// least minSamples samples below its enabled minimum rate.
func videoGated(submit, gen videosched.HealthStat, minSubmit, minGen float64, minSamples int) bool {
	return (minSubmit > 0 && submit.Samples >= minSamples && submit.Rate < minSubmit) ||
		(minGen > 0 && gen.Samples >= minSamples && gen.Rate < minGen)
}

// VideoChannelOverview lists each channel x model that is scheduled now or
// served requests in the audit window. It reports no weighted total: the price
// score depends on each request's spec and sell price, so only the simulator
// can rank channels for a concrete request.
type VideoChannelOverview struct {
	SelectionPolicy string `json:"selection_policy"`
	// Weights are normalized as the weighted_v1 total uses them.
	PriceWeight   float64                   `json:"price_weight"`
	QualityWeight float64                   `json:"quality_weight"`
	ServiceWeight float64                   `json:"service_weight"`
	MinSamples    int                       `json:"min_samples"`
	AsOf          int64                     `json:"as_of"`
	Rows          []VideoChannelOverviewRow `json:"rows"`
}

type VideoChannelOverviewRow struct {
	ChannelID int    `json:"channel_id"`
	Name      string `json:"name"`
	Status    int    `json:"status"` // 0: the channel no longer exists
	Group     string `json:"group"`
	Priority  int64  `json:"priority"`
	Weight    int    `json:"weight"`
	Model     string `json:"model"`
	// Scheduled reports that the channel prices this model now and is in the
	// filtered group; the live fields below are empty otherwise.
	Scheduled     bool                        `json:"scheduled"`
	Quality       float64                     `json:"quality"`
	CostMode      string                      `json:"cost_mode,omitempty"`
	Prices        map[string]float64          `json:"prices,omitempty"`
	Capacity      int                         `json:"capacity"`
	CapacityGroup string                      `json:"capacity_group,omitempty"`
	GroupCapacity int                         `json:"group_capacity,omitempty"`
	GroupInFlight int                         `json:"group_in_flight,omitempty"`
	Health        *VideoChannelHealth         `json:"health,omitempty"` // nil when unreadable
	Gated         bool                        `json:"gated"`
	Unproven      bool                        `json:"unproven"`
	Service       *float64                    `json:"service,omitempty"` // weighted_v1 only
	Usage         model.VideoAuditChannelStat `json:"usage"`
}

// GetVideoChannelOverview joins live config and health with the filtered audit
// window. Model, group and channel filters also narrow the live rows; the other
// filters only describe requests.
func GetVideoChannelOverview(ctx context.Context, filter model.VideoScheduleAuditFilter) (*VideoChannelOverview, error) {
	usage, err := model.GetVideoScheduleChannelStats(ctx, filter)
	if err != nil {
		return nil, err
	}
	served := make([]int, 0, len(usage))
	for _, stat := range usage {
		served = append(served, stat.SelectedChannel)
	}
	var channels []model.Channel
	query := model.DB.WithContext(ctx).Where("settings LIKE ? OR id IN ?", `%"video_scheduling"%`, served)
	if filter.Channel != 0 {
		query = query.Where("id = ?", filter.Channel)
	}
	if err := query.Order("id ASC").Find(&channels).Error; err != nil {
		return nil, err
	}

	setting := operation_setting.GetVideoSchedulingSetting()
	overview := &VideoChannelOverview{SelectionPolicy: setting.SelectionPolicy, MinSamples: setting.MinSamples, AsOf: time.Now().UnixMilli(), Rows: []VideoChannelOverviewRow{}}
	overview.PriceWeight, overview.QualityWeight, overview.ServiceWeight = videosched.NormalizeWeights(videosched.Weights{Price: setting.PriceWeight, Quality: setting.QualityWeight, Service: setting.ServiceWeight})
	type rowKey struct {
		channel int
		model   string
	}
	index := map[rowKey]int{}
	byID := make(map[int]*model.Channel, len(channels))
	for i := range channels {
		channel := &channels[i]
		byID[channel.Id] = channel
		cfg, ok := VideoSchedulingConfigOf(channel)
		if !ok || filter.Group != "" && !slices.Contains(channel.GetGroups(), filter.Group) {
			continue
		}
		view, err := GetVideoHealthView(channel, true)
		if err != nil {
			common.SysError("video scheduling health read failed: channel=" + strconv.Itoa(channel.Id) + " error=" + err.Error())
			view = &VideoHealthView{} // rows stay listed without health
		}
		for modelName := range strings.SplitSeq(channel.Models, ",") {
			cost, priced := videoModelCost(cfg.Models, modelName)
			key := rowKey{channel.Id, modelName}
			if _, seen := index[key]; modelName == "" || !priced || seen || filter.Model != "" && modelName != filter.Model {
				continue
			}
			row := VideoChannelOverviewRow{ChannelID: channel.Id, Name: channel.Name, Status: channel.Status, Group: channel.Group, Priority: channel.GetPriority(), Weight: channel.GetWeight(),
				Model: modelName, Scheduled: true, Quality: cfg.Quality, CostMode: cost.Mode, Prices: cost.Prices, Capacity: cfg.Capacity, CapacityGroup: cfg.CapacityGroup}
			if health, ok := view.Models[modelName]; ok {
				row.Health, row.GroupCapacity, row.GroupInFlight = &health, view.GroupCapacity, view.GroupInFlight
				row.Gated = videoGated(health.Submit, health.Gen, setting.MinSubmitRate, setting.MinGenRate, setting.MinSamples)
				row.Unproven = health.Submit.Samples < setting.MinSamples || health.Gen.Samples < setting.MinSamples
				if setting.SelectionPolicy != videosched.PolicyStabilityCostV2 {
					score := videosched.ServiceScore(&videosched.Candidate{Submit: health.Submit, Gen: health.Gen, Capacity: cfg.Capacity, InFlight: health.InFlight,
						GroupCapacity: view.GroupCapacity, GroupInFlight: view.GroupInFlight}, setting.MinSamples)
					row.Service = &score
				}
			}
			index[key] = len(overview.Rows)
			overview.Rows = append(overview.Rows, row)
		}
	}
	for _, stat := range usage {
		if i, ok := index[rowKey{stat.SelectedChannel, stat.ModelName}]; ok {
			overview.Rows[i].Usage = stat
			continue
		}
		row := VideoChannelOverviewRow{ChannelID: stat.SelectedChannel, Model: stat.ModelName, Usage: stat}
		if channel := byID[stat.SelectedChannel]; channel != nil {
			row.Name, row.Status, row.Group, row.Priority, row.Weight = channel.Name, channel.Status, channel.Group, channel.GetPriority(), channel.GetWeight()
		}
		overview.Rows = append(overview.Rows, row)
	}
	slices.SortStableFunc(overview.Rows, func(a, b VideoChannelOverviewRow) int {
		return cmp.Or(strings.Compare(a.Model, b.Model), cmp.Compare(b.Usage.Requests, a.Usage.Requests), cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.ChannelID, b.ChannelID))
	})
	return overview, nil
}

// GetVideoGroupInFlight reads the in-flight gauge of a capacity group.
func GetVideoGroupInFlight(group string) (int, error) {
	values, err := videoHealthStore().get([]string{videoGroupInFlightKey(group)})
	if err != nil {
		return 0, err
	}
	return int(values[0]), nil
}

// videoHealthStat reports no evidence as a perfect rate; the scheduler marks
// under-sampled metrics Unproven instead of trusting them.
func videoHealthStat(ok, fail int64) videosched.HealthStat {
	samples := ok + fail
	if samples == 0 {
		return videosched.HealthStat{Rate: 1}
	}
	return videosched.HealthStat{Rate: float64(ok) / float64(samples), Samples: int(samples)}
}

// VideoProbeCooldown doubles the base cooldown per consecutive failure, capped
// at one day.
func VideoProbeCooldown(baseSeconds int, state VideoProbeState) time.Duration {
	cooldown := time.Duration(baseSeconds) * time.Second
	for range min(state.ConsecutiveFails, 32) {
		cooldown *= 2
		if cooldown >= videoSchedProbeCooldownMax {
			return videoSchedProbeCooldownMax
		}
	}
	return min(cooldown, videoSchedProbeCooldownMax)
}

func videoProbeSlotKey(channelID, n int) string {
	return fmt.Sprintf("%sprobe:%d:%d", videoSchedKeyPrefix, channelID, n)
}

// VideoProbeSlotsHeld counts the held probe slots 0..slots-1 of a channel.
func VideoProbeSlotsHeld(channelID, slots int) (int, error) {
	if slots <= 0 {
		return 0, nil
	}
	keys := make([]string, slots)
	for n := range slots {
		keys[n] = videoProbeSlotKey(channelID, n)
	}
	return videoHealthStore().held(keys)
}

// AcquireVideoProbeSlot claims probe slot n of a channel for this request.
// The request owns the lease until its task is persisted; slot TTL is only a
// backstop. LastProbeAt is stamped on success so the cooldown starts now. A
// request holds at most one lease: an earlier unpersisted one is released.
func AcquireVideoProbeSlot(c *gin.Context, channelID, n int, ttl time.Duration) (bool, error) {
	if lease, ok := takeVideoProbeLease(c, nil); ok {
		releaseVideoProbeSlot(lease.Key, lease.Token)
	}
	key := videoProbeSlotKey(channelID, n)
	token := common.GetRandomString(24)
	ok, err := videoHealthStore().acquire(key, token, ttl)
	if err != nil || !ok {
		return false, err
	}
	c.Set(string(constant.ContextKeyVideoSchedProbeLease), &videoProbeLease{ChannelID: channelID, Key: key, Token: token})
	lastKey, _ := videoProbeStateKeys(channelID)
	if err := videoHealthStore().set(lastKey, time.Now().Unix()); err != nil {
		common.SysError(fmt.Sprintf("video scheduling probe state write failed: channel=%d error=%v", channelID, err))
	}
	return true, nil
}

// ReleaseUnpersistedVideoProbeLease releases a lease the request never handed
// to a persisted task. It must run on every request exit.
// ReleaseUnpersistedVideoProbeLease also gives back the capacity reservation
// no task took over.
func ReleaseUnpersistedVideoProbeLease(c *gin.Context) {
	if lease, ok := takeVideoProbeLease(c, nil); ok {
		releaseVideoProbeSlot(lease.Key, lease.Token)
	}
	releaseVideoCapacityReservation(c)
}

func peekVideoProbeLease(c *gin.Context) (*videoProbeLease, bool) {
	if c == nil {
		return nil, false
	}
	value, _ := c.Get(string(constant.ContextKeyVideoSchedProbeLease))
	lease, ok := value.(*videoProbeLease)
	return lease, ok && lease != nil
}

// takeVideoProbeLease removes the request's lease from the context; with a
// channel it only takes a lease held for that channel.
func takeVideoProbeLease(c *gin.Context, channel *model.Channel) (*videoProbeLease, bool) {
	lease, ok := peekVideoProbeLease(c)
	if !ok || (channel != nil && lease.ChannelID != channel.Id) {
		return nil, false
	}
	c.Set(string(constant.ContextKeyVideoSchedProbeLease), nil)
	return lease, true
}

// releaseVideoProbeSlot deletes the slot only if this token still holds it:
// an expired slot may already belong to another request.
func releaseVideoProbeSlot(key, token string) {
	if err := videoHealthStore().release(key, token); err != nil {
		common.SysError(fmt.Sprintf("video scheduling probe release failed: key=%s error=%v", key, err))
	}
}

// recordVideoProbeOutcome resets the failure streak on success and extends it
// on an attributed failure; ignored outcomes (cancellations, user errors)
// release without penalty.
func recordVideoProbeOutcome(channelID int, outcome VideoOutcome) {
	_, failsKey := videoProbeStateKeys(channelID)
	var err error
	switch outcome {
	case VideoOutcomeSuccess:
		err = videoHealthStore().set(failsKey, 0)
	case VideoOutcomeFail:
		_, err = videoHealthStore().add(failsKey, 1)
	}
	if err != nil {
		common.SysError(fmt.Sprintf("video scheduling probe state write failed: channel=%d error=%v", channelID, err))
	}
}

var videoHealthReady atomic.Bool

// VideoHealthReady reports that in-flight gauges have been calibrated against
// the database since startup: by this instance, or by another instance whose
// success marker is still fresh while it holds the calibration lock.
func VideoHealthReady() bool {
	return videoHealthReady.Load()
}

// RunVideoHealthCalibration recalibrates in-flight gauges every minute while
// scheduling is not off. One instance at a time does the work.
func RunVideoHealthCalibration(ctx context.Context) {
	ticker := time.NewTicker(videoSchedCalibrationTick)
	defer ticker.Stop()
	lastBlockerWarning := ""
	for {
		setting := operation_setting.GetVideoSchedulingSetting()
		RefreshVideoReliability(ctx)
		if setting.Mode != operation_setting.VideoSchedulingModeOff {
			calibrateVideoInFlight()
			// Logged when it changes, so startup and later setting or plugin
			// changes each report the static mixed-pool blockers once.
			if warning := videoSchedBlockerWarning(ctx, jsplugin.DefaultRegistry.Generation(), setting.Models); warning != lastBlockerWarning {
				if warning != "" {
					logger.LogWarn(ctx, "%s", warning)
				}
				lastBlockerWarning = warning
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func calibrateVideoInFlight() {
	store := videoHealthStore()
	lockToken := common.GetRandomString(16)
	locked, err := store.acquire(videoSchedCalibrationKey, lockToken, videoSchedCalibrationTick-5*time.Second)
	if err != nil {
		common.SysError("video scheduling calibration lock failed: " + err.Error())
		return
	}
	_, shared := store.(redisVideoHealth)
	if !locked {
		// Another instance holds the lock and may still be counting or may
		// have failed; only its published success makes the gauges trusted.
		// Without Redis the lock holder is this instance itself.
		if shared {
			marked, err := common.RDB.Exists(context.Background(), videoSchedCalibratedKey).Result()
			if err != nil {
				common.SysError("video scheduling calibration marker read failed: " + err.Error())
			} else if marked > 0 {
				videoHealthReady.Store(true)
			}
		}
		return
	}
	trusted := videoHealthReady.Load()
	if shared {
		marked, err := common.RDB.Exists(context.Background(), videoSchedCalibratedKey).Result()
		if err != nil {
			common.SysError("video scheduling calibration marker read failed: " + err.Error())
			return
		}
		trusted = marked > 0
	}
	configs, err := model.GetVideoScheduledChannels()
	if err != nil {
		common.SysError("video scheduling calibration channel scan failed: " + err.Error())
		return
	}
	// Read revisions before the database count. A reservation handed to a
	// task during that query must fence a reset based on the older task set.
	revisions, err := store.capacityRevisions()
	if err != nil {
		common.SysError("video scheduling calibration revision read failed: " + err.Error())
		return
	}
	durable, err := model.ListVideoCapacityOwners(context.Background(), time.Now().Unix())
	if err != nil {
		// Keep the last gauge values: a failed read must not zero them.
		common.SysError("video scheduling calibration count failed: " + err.Error())
		return
	}
	desired := map[string]map[string]int64{}
	for key := range revisions {
		if strings.HasPrefix(key, videoInFlightPrefix) {
			desired[key] = map[string]int64{}
		}
	}
	for id, cfg := range configs {
		desired[videoInFlightKey(id)] = map[string]int64{}
		if cfg.CapacityGroup != "" {
			desired[videoGroupInFlightKey(cfg.CapacityGroup)] = map[string]int64{}
		}
	}
	for _, owner := range durable {
		keys := []string{videoInFlightKey(owner.ChannelID)}
		if owner.Group != "" {
			keys = append(keys, videoGroupInFlightKey(owner.Group))
		}
		for _, key := range keys {
			if desired[key] == nil {
				desired[key] = map[string]int64{}
			}
			// Zero marks a durable active owner, negative milliseconds a
			// durable unknown hold. Positive scores are unsubmitted cache leases.
			desired[key][owner.Token] = -owner.ExpiresAt * 1000
		}
	}
	complete := true
	for key, owners := range desired {
		applied, err := calibrateVideoGauge(store, key, owners, revisions[key])
		if err != nil || !applied && !trusted {
			complete = false
		}
	}
	if !complete {
		return
	}
	if shared {
		// Publishing and checking the lock are one operation: a cache flush
		// during the database scan invalidates this whole recovery round.
		marked, err := common.RDB.Eval(context.Background(), `if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end; redis.call('SET',KEYS[2],1,'EX',ARGV[2]); return 1`, []string{videoSchedCalibrationKey, videoSchedCalibratedKey}, lockToken, int64(3*videoSchedCalibrationTick/time.Second)).Int()
		if err != nil {
			common.SysError("video scheduling calibration marker write failed: " + err.Error())
			return
		}
		if marked == 0 {
			return
		}
	}
	videoHealthReady.Store(true)
}

// Reconcile a union of durable owners and unexpired, not-yet-submitted local
// leases. Shared tokens make handoff/late terminal callbacks idempotent.
// The revision was captured before the database reads, so new writes win.
func calibrateVideoGauge(store videoHealthBackend, key string, owners map[string]int64, revision int64) (bool, error) {
	changed, err := store.calibrateCapacity(key, owners, revision, time.Now().UnixMilli())
	if err != nil {
		common.SysError(fmt.Sprintf("video scheduling calibration write failed: key=%s error=%v", key, err))
	}
	return changed, err
}

type videoHealthBackend interface {
	// addSample increments field in bucket minute%buckets of a health hash,
	// clearing the bucket first when it holds an older minute.
	addSample(key, field string, minute int64, buckets int) error
	// window sums each sample field over the buckets of the last buckets minutes.
	window(key string, minute int64, buckets int) (map[string]int64, error)
	// add changes a gauge by delta, flooring the result at zero atomically.
	add(key string, delta int64) (int64, error)
	get(keys []string) ([]int64, error)
	set(key string, value int64) error
	// acquire sets key to token only if absent; release deletes key only
	// while it still holds token.
	acquire(key, token string, ttl time.Duration) (bool, error)
	release(key, token string) error
	// held counts the keys that currently exist.
	held(keys []string) (int, error)
	// reserve increments every gauge by one only if each stays within its
	// limit (0 = unlimited), all or nothing in one atomic step.
	reserve(keys []string, limits []int64, token string, expires int64) (bool, error)
	renewCapacity(ctx context.Context, keys []string, token string, expires int64) error
	finishCapacity(keys []string, token string, persisted bool) error
	retainCapacity(keys []string, token string, expires int64) error
	capacityRevisions() (map[string]int64, error)
	calibrateCapacity(key string, owners map[string]int64, revision, now int64) (bool, error)
}

func videoHealthStore() videoHealthBackend {
	if common.RedisEnabled && common.RDB != nil {
		return redisVideoHealth{client: common.RDB}
	}
	return memoryVideoHealth
}

// sumVideoWindow adds up the buckets whose minute lies within the window.
func sumVideoWindow(fields map[string]int64, minute int64, buckets int) map[string]int64 {
	sums := make(map[string]int64, len(videoSampleFields))
	for i := range buckets {
		suffix := "_" + strconv.Itoa(i)
		ts, ok := fields["ts"+suffix]
		if !ok || ts <= minute-int64(buckets) || ts > minute {
			continue
		}
		for _, field := range videoSampleFields {
			sums[field] += fields[field+suffix]
		}
	}
	return sums
}

const videoAddSampleScript = `
local ts = 'ts_' .. ARGV[1]
if redis.call('HGET', KEYS[1], ts) ~= ARGV[2] then
  redis.call('HSET', KEYS[1], ts, ARGV[2], 's_ok_' .. ARGV[1], 0, 's_fail_' .. ARGV[1], 0, 'g_ok_' .. ARGV[1], 0, 'g_fail_' .. ARGV[1], 0)
end
redis.call('HINCRBY', KEYS[1], ARGV[3] .. '_' .. ARGV[1], 1)
redis.call('EXPIRE', KEYS[1], ARGV[4])
return 1`

// videoAddFloorScript never lets a gauge go below zero in one atomic step; a
// DECR followed by a separate SET 0 could erase increments made in between.
const videoAddFloorScript = `
local value = tonumber(redis.call('GET', KEYS[1]) or '0') + tonumber(ARGV[1])
if value < 0 then value = 0 end
redis.call('SET', KEYS[1], value)
redis.call('HINCRBY', KEYS[2], KEYS[1], 1)
return value`

const videoReserveScript = `
if redis.call('EXISTS', '` + videoSchedCalibratedKey + `') == 0 then return -1 end
for i=2,#KEYS do
  local key = KEYS[i]
  local limit = tonumber(ARGV[i])
  if limit > 0 and redis.call('ZCARD', '` + videoCapacityOwnersPrefix + `' .. key) >= limit then
    return 0
  end
end
for i=2,#KEYS do
  local key = KEYS[i]
  redis.call('ZADD', '` + videoCapacityOwnersPrefix + `' .. key, ARGV[1], ARGV[#ARGV])
  redis.call('HINCRBY', KEYS[1], key, 1)
end
return 1`

const videoFinishCapacityScript = `
local closed = '` + videoCapacityClosedPrefix + `' .. ARGV[1]
if ARGV[2] == '1' and redis.call('EXISTS', closed) == 1 then return 0 end
if ARGV[2] == '0' then redis.call('SET', closed, 1, 'EX', ARGV[3]) end
for i=2,#KEYS do
  local key = KEYS[i]
  if ARGV[2] == '1' then redis.call('ZADD', '` + videoCapacityOwnersPrefix + `' .. key, 0, ARGV[1])
  else redis.call('ZREM', '` + videoCapacityOwnersPrefix + `' .. key, ARGV[1]) end
  redis.call('HINCRBY', KEYS[1], key, 1)
end
return 1`

const videoCalibrateCapacityScript = `
if tonumber(redis.call('HGET', KEYS[2], KEYS[1]) or '0') ~= tonumber(ARGV[2]) then return 0 end
local key = '` + videoCapacityOwnersPrefix + `' .. KEYS[1]
local desired = cjson.decode(ARGV[1])
local owners = redis.call('ZRANGE', key, 0, -1, 'WITHSCORES')
for i=1,#owners,2 do
  local token, score = owners[i], tonumber(owners[i+1])
  if not desired[token] and (score <= 0 or score <= tonumber(ARGV[3])) then redis.call('ZREM', key, token) end
end
for token, score in pairs(desired) do redis.call('ZADD', key, score, token) end
redis.call('HINCRBY', KEYS[2], KEYS[1], 1)
return 1`

const videoReleaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`

type redisVideoHealth struct{ client *redis.Client }

func (r redisVideoHealth) held(keys []string) (int, error) {
	n, err := r.client.Exists(context.Background(), keys...).Result()
	return int(n), err
}

func (r redisVideoHealth) addSample(key, field string, minute int64, buckets int) error {
	ttl := int64(buckets) * 60 * 2
	return r.client.Eval(context.Background(), videoAddSampleScript, []string{key}, minute%int64(buckets), minute, field, ttl).Err()
}

func (r redisVideoHealth) window(key string, minute int64, buckets int) (map[string]int64, error) {
	raw, err := r.client.HGetAll(context.Background(), key).Result()
	if err != nil {
		return nil, err
	}
	fields := make(map[string]int64, len(raw))
	for field, value := range raw {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			fields[field] = n
		}
	}
	return sumVideoWindow(fields, minute, buckets), nil
}

func (r redisVideoHealth) add(key string, delta int64) (int64, error) {
	return r.client.Eval(context.Background(), videoAddFloorScript, []string{key, videoCapacityRevisionsKey}, delta).Int64()
}

func (r redisVideoHealth) get(keys []string) ([]int64, error) {
	return r.client.Eval(context.Background(), `local values={}; for i,key in ipairs(KEYS) do if string.sub(key,1,string.len(ARGV[1]))==ARGV[1] then values[i]=redis.call('ZCARD',ARGV[2]..key) else values[i]=tonumber(redis.call('GET',key) or '0') end end; return values`, keys, videoInFlightPrefix, videoCapacityOwnersPrefix).Int64Slice()
}

func (r redisVideoHealth) set(key string, value int64) error {
	return r.client.Eval(context.Background(), `redis.call('SET',KEYS[1],ARGV[1]); redis.call('HINCRBY',KEYS[2],KEYS[1],1); return 1`, []string{key, videoCapacityRevisionsKey}, value).Err()
}

func (r redisVideoHealth) acquire(key, token string, ttl time.Duration) (bool, error) {
	return r.client.SetNX(context.Background(), key, token, ttl).Result()
}

func (r redisVideoHealth) release(key, token string) error {
	return r.client.Eval(context.Background(), videoReleaseScript, []string{key}, token).Err()
}

func (r redisVideoHealth) reserve(keys []string, limits []int64, token string, expires int64) (bool, error) {
	args := make([]any, len(limits)+2)
	args[0], args[len(args)-1] = expires, token
	for i, limit := range limits {
		args[i+1] = limit
	}
	n, err := r.client.Eval(context.Background(), videoReserveScript, append([]string{videoCapacityRevisionsKey}, keys...), args...).Int()
	if err == nil && n == -1 {
		return false, ErrVideoHealthAdmission
	}
	return n == 1, err
}

func (r redisVideoHealth) renewCapacity(ctx context.Context, keys []string, token string, expires int64) error {
	n, err := r.client.Eval(ctx, `for _,key in ipairs(KEYS) do local score=tonumber(redis.call('ZSCORE','`+videoCapacityOwnersPrefix+`' .. key,ARGV[1])); if not score or (score>0 and score<=tonumber(ARGV[3])) then return 0 end end; for _,key in ipairs(KEYS) do local owner='`+videoCapacityOwnersPrefix+`' .. key; if tonumber(redis.call('ZSCORE',owner,ARGV[1]))>0 then redis.call('ZADD',owner,ARGV[2],ARGV[1]) end end; return 1`, keys, token, expires, time.Now().UnixMilli()).Int()
	if err == nil && n != 1 {
		return fmt.Errorf("capacity reservation owner expired")
	}
	return err
}

func (r redisVideoHealth) finishCapacity(keys []string, token string, persisted bool) error {
	keep := 0
	if persisted {
		keep = 1
	}
	return r.client.Eval(context.Background(), videoFinishCapacityScript, append([]string{videoCapacityRevisionsKey}, keys...), token, keep, int64(videoSubmissionLeaseTTL/time.Second)).Err()
}

func (r redisVideoHealth) retainCapacity(keys []string, token string, expires int64) error {
	return r.client.Eval(context.Background(), `for i=2,#KEYS do redis.call('ZADD',ARGV[3]..KEYS[i],-tonumber(ARGV[2]),ARGV[1]); redis.call('HINCRBY',KEYS[1],KEYS[i],1) end; return 1`, append([]string{videoCapacityRevisionsKey}, keys...), token, expires, videoCapacityOwnersPrefix).Err()
}

func (r redisVideoHealth) capacityRevisions() (map[string]int64, error) {
	values, err := r.client.HGetAll(context.Background(), videoCapacityRevisionsKey).Result()
	if err != nil {
		return nil, err
	}
	revisions := make(map[string]int64, len(values))
	for key, value := range values {
		revisions[key], err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
	}
	return revisions, nil
}

func (r redisVideoHealth) calibrateCapacity(key string, owners map[string]int64, revision, now int64) (bool, error) {
	if owners == nil {
		owners = map[string]int64{}
	}
	data, err := common.Marshal(owners)
	if err != nil {
		return false, err
	}
	n, err := r.client.Eval(context.Background(), videoCalibrateCapacityScript, []string{key, videoCapacityRevisionsKey}, string(data), revision, now).Int()
	return n == 1, err
}

// memoryVideoHealth is the single-instance store used without Redis.
var memoryVideoHealth = &memoryVideoHealthStore{
	hashes: map[string]map[string]int64{},
	gauges: map[string]int64{},
	slots:  map[string]memoryVideoSlot{},
}

type memoryVideoSlot struct {
	token   string
	expires time.Time
}

type memoryVideoHealthStore struct {
	mu           sync.Mutex
	hashes       map[string]map[string]int64
	gauges       map[string]int64
	slots        map[string]memoryVideoSlot
	reservations map[string]map[string]int64
	revisions    map[string]int64
	closed       map[string]int64
}

// Called with mu held, keeping revisions separate from user-named group keys.
func (m *memoryVideoHealthStore) advanceCapacityRevision(key string) {
	if m.revisions == nil {
		m.revisions = map[string]int64{}
	}
	m.revisions[key]++
}

func (m *memoryVideoHealthStore) addSample(key, field string, minute int64, buckets int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	hash := m.hashes[key]
	if hash == nil {
		hash = map[string]int64{}
		m.hashes[key] = hash
	}
	suffix := "_" + strconv.FormatInt(minute%int64(buckets), 10)
	if ts, ok := hash["ts"+suffix]; !ok || ts != minute {
		hash["ts"+suffix] = minute
		for _, name := range videoSampleFields {
			hash[name+suffix] = 0
		}
	}
	hash[field+suffix]++
	return nil
}

func (m *memoryVideoHealthStore) window(key string, minute int64, buckets int) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sumVideoWindow(m.hashes[key], minute, buckets), nil
}

func (m *memoryVideoHealthStore) add(key string, delta int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[key] = max(m.gauges[key]+delta, 0)
	m.advanceCapacityRevision(key)
	return m.gauges[key], nil
}

func (m *memoryVideoHealthStore) get(keys []string) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	values := make([]int64, len(keys))
	for i, key := range keys {
		if strings.HasPrefix(key, videoInFlightPrefix) {
			values[i] = int64(len(m.reservations[key]))
		} else {
			values[i] = m.gauges[key]
		}
	}
	return values, nil
}

func (m *memoryVideoHealthStore) set(key string, value int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[key] = value
	m.advanceCapacityRevision(key)
	return nil
}

func (m *memoryVideoHealthStore) acquire(key, token string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slot, ok := m.slots[key]; ok && time.Now().Before(slot.expires) {
		return false, nil
	}
	m.slots[key] = memoryVideoSlot{token: token, expires: time.Now().Add(ttl)}
	return true, nil
}

func (m *memoryVideoHealthStore) release(key, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slot, ok := m.slots[key]; ok && slot.token == token {
		delete(m.slots, key)
	}
	return nil
}

func (m *memoryVideoHealthStore) reserve(keys []string, limits []int64, token string, expires int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, key := range keys {
		if limits[i] > 0 && int64(len(m.reservations[key])) >= limits[i] {
			return false, nil
		}
	}
	for _, key := range keys {
		m.advanceCapacityRevision(key)
		if m.reservations == nil {
			m.reservations = map[string]map[string]int64{}
		}
		if m.reservations[key] == nil {
			m.reservations[key] = map[string]int64{}
		}
		m.reservations[key][token] = expires
	}
	return true, nil
}

func (m *memoryVideoHealthStore) renewCapacity(ctx context.Context, keys []string, token string, expires int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		if score, held := m.reservations[key][token]; !held || score > 0 && score <= time.Now().UnixMilli() {
			return fmt.Errorf("capacity reservation owner expired")
		}
	}
	for _, key := range keys {
		if m.reservations[key][token] > 0 {
			m.reservations[key][token] = expires
		}
	}
	return nil
}

func (m *memoryVideoHealthStore) finishCapacity(keys []string, token string, persisted bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if persisted && m.closed[token] > time.Now().UnixMilli() {
		return nil
	}
	if !persisted {
		if m.closed == nil {
			m.closed = map[string]int64{}
		}
		m.closed[token] = time.Now().Add(videoSubmissionLeaseTTL).UnixMilli()
	}
	for _, key := range keys {
		if persisted {
			if m.reservations == nil {
				m.reservations = map[string]map[string]int64{}
			}
			if m.reservations[key] == nil {
				m.reservations[key] = map[string]int64{}
			}
			m.reservations[key][token] = 0
		} else {
			delete(m.reservations[key], token)
		}
		m.advanceCapacityRevision(key)
	}
	return nil
}

func (m *memoryVideoHealthStore) retainCapacity(keys []string, token string, expires int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reservations == nil {
		m.reservations = map[string]map[string]int64{}
	}
	for _, key := range keys {
		if m.reservations[key] == nil {
			m.reservations[key] = map[string]int64{}
		}
		m.reservations[key][token] = -expires
		m.advanceCapacityRevision(key)
	}
	return nil
}

func (m *memoryVideoHealthStore) capacityRevisions() (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for token, expires := range m.closed {
		if expires <= time.Now().UnixMilli() {
			delete(m.closed, token)
		}
	}
	return maps.Clone(m.revisions), nil
}

func (m *memoryVideoHealthStore) calibrateCapacity(key string, owners map[string]int64, revision, now int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revisions[key] != revision {
		return false, nil
	}
	for token, score := range m.reservations[key] {
		if _, durable := owners[token]; !durable && (score <= 0 || score <= now) {
			delete(m.reservations[key], token)
		}
	}
	if m.reservations == nil {
		m.reservations = map[string]map[string]int64{}
	}
	if m.reservations[key] == nil {
		m.reservations[key] = map[string]int64{}
	}
	maps.Copy(m.reservations[key], owners)
	m.advanceCapacityRevision(key)
	return true, nil
}

func (m *memoryVideoHealthStore) held(keys []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, key := range keys {
		if slot, ok := m.slots[key]; ok && time.Now().Before(slot.expires) {
			n++
		}
	}
	return n, nil
}
