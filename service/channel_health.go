package service

import (
	"context"
	"fmt"
	"strconv"
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
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
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
	videoSchedCalibrationKey   = videoSchedKeyPrefix + "calib"
	videoSchedCalibratedKey    = videoSchedKeyPrefix + "calib_ok"
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
	Submit   videosched.HealthStat `json:"submit"`
	Gen      videosched.HealthStat `json:"gen"`
	InFlight int                   `json:"in_flight"`
	Probe    VideoProbeState       `json:"probe"`
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
	captureVideoAuditSubmit(c, channel.Id, taskErr)
	outcome := videoSubmitOutcome(taskErr)
	if taskErr != nil {
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
	cfg, ok := VideoSchedulingTracks(channel)
	if !ok {
		return nil
	}
	summary := &model.TaskSchedulingSummary{Model: modelName, CapacityGroup: cfg.CapacityGroup}
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
		if summary == nil && audit == nil {
			return
		}
		outcome, attribution := videoTerminalAttribution(task, false)
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
		return
	}
	store := videoHealthStore()
	for _, key := range videoInFlightKeys(task.ChannelId, summary) {
		if _, err := store.add(key, 1); err != nil {
			common.SysError(fmt.Sprintf("video scheduling in-flight increment failed: key=%s error=%v", key, err))
		}
	}
}

// ObserveVideoTerminal is called exactly once per polled task by the
// state-transition winner: finalizeTerminalTask, the timeout sweep or the
// realtime fetch. It releases the task's in-flight count. hostFailure marks a
// failure the host detected itself (timeout, poll failure escalation).
func ObserveVideoTerminal(task *model.Task, hostFailure bool) {
	outcome, attribution := videoTerminalAttribution(task, hostFailure)
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
		store := videoHealthStore()
		for _, key := range videoInFlightKeys(task.ChannelId, summary) {
			if _, err := store.add(key, -1); err != nil {
				common.SysError(fmt.Sprintf("video scheduling in-flight decrement failed: key=%s error=%v", key, err))
			}
		}
	}
	if slot := summary.ProbeSlot; slot != nil {
		releaseVideoProbeSlot(slot.Key, slot.Token)
		recordVideoProbeOutcome(task.ChannelId, outcome)
	}
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
	health := VideoChannelHealth{Submit: pick(videoSubmitOK, videoSubmitFail), Gen: pick(videoGenOK, videoGenFail)}
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
	Gated              bool                          `json:"gated"`
	Capacity           int                           `json:"capacity"`
	CapacityGroup      string                        `json:"capacity_group,omitempty"`
	GroupCapacity      int                           `json:"group_capacity,omitempty"`
	GroupInFlight      int                           `json:"group_in_flight,omitempty"`
	ProbeSlotsHeld     int                           `json:"probe_slots_held"`
	ProbeCooldownUntil int64                         `json:"probe_cooldown_until,omitempty"` // unix seconds after the last probe
	Models             map[string]VideoChannelHealth `json:"models,omitempty"`
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
		Gated:              videoGated(health.Submit, health.Gen, setting.MinSubmitRate, setting.MinGenRate, setting.MinSamples),
		Capacity:           cfg.Capacity,
		CapacityGroup:      cfg.CapacityGroup,
		GroupCapacity:      setting.CapacityGroups[cfg.CapacityGroup],
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
	if perModel {
		view.Models = make(map[string]VideoChannelHealth, len(cfg.Models))
		for name := range cfg.Models {
			if view.Models[name], err = GetVideoChannelHealth(channel.Id, name, setting.MinSamples); err != nil {
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
	ReleaseUnpersistedVideoProbeLease(c)
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
func ReleaseUnpersistedVideoProbeLease(c *gin.Context) {
	if lease, ok := takeVideoProbeLease(c, nil); ok {
		releaseVideoProbeSlot(lease.Key, lease.Token)
	}
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

var (
	videoHealthReady atomic.Bool
	// videoCalibrationDrift remembers each gauge's last small drift; only the
	// instance holding the calibration lock uses it.
	videoCalibrationDrift sync.Map
)

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
	locked, err := store.acquire(videoSchedCalibrationKey, common.GetRandomString(16), videoSchedCalibrationTick-5*time.Second)
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
	configs, err := model.GetVideoScheduledChannels()
	if err != nil {
		common.SysError("video scheduling calibration channel scan failed: " + err.Error())
		return
	}
	channels, groups, err := model.CountActiveScheduledTasks()
	if err != nil {
		// Keep the last gauge values: a failed read must not zero them.
		common.SysError("video scheduling calibration count failed: " + err.Error())
		return
	}
	for id, cfg := range configs {
		calibrateVideoGauge(store, videoInFlightKey(id), channels[id])
		if _, ok := groups[cfg.CapacityGroup]; !ok && cfg.CapacityGroup != "" {
			groups[cfg.CapacityGroup] = 0 // a configured group whose tasks all ended drains to zero
		}
	}
	for group, count := range groups {
		calibrateVideoGauge(store, videoGroupInFlightKey(group), count)
	}
	if shared {
		if err := common.RDB.Set(context.Background(), videoSchedCalibratedKey, 1, 3*videoSchedCalibrationTick).Err(); err != nil {
			common.SysError("video scheduling calibration marker write failed: " + err.Error())
		}
	}
	videoHealthReady.Store(true)
}

// calibrateVideoGauge resets a gauge to the database count when they differ by
// more than max(2, 10%), or when the same non-zero drift persists for two
// rounds so a quiet zombie count (capacity 1, redis 1, db 0) cannot pin a
// channel at capacity forever. This is a heuristic: steady load can repeat a
// drift from feedback still in flight, and the reset then errs by that much
// until later feedback and calibration converge.
func calibrateVideoGauge(store videoHealthBackend, key string, db int64) {
	values, err := store.get([]string{key})
	if err != nil {
		common.SysError(fmt.Sprintf("video scheduling calibration read failed: key=%s error=%v", key, err))
		return
	}
	drift := values[0] - db
	previous, _ := videoCalibrationDrift.Load(key)
	reset := max(-drift, drift) > max(2, db/10) || (drift != 0 && previous == drift)
	if !reset {
		videoCalibrationDrift.Store(key, drift)
		return
	}
	videoCalibrationDrift.Delete(key)
	if err := store.set(key, db); err != nil {
		common.SysError(fmt.Sprintf("video scheduling calibration write failed: key=%s error=%v", key, err))
		return
	}
	logger.LogInfo(context.Background(), fmt.Sprintf("video scheduling calibrated %s: %d -> %d", key, values[0], db))
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
}

func videoHealthStore() videoHealthBackend {
	if common.RedisEnabled && common.RDB != nil {
		return redisVideoHealth{}
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
return value`

const videoReleaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`

type redisVideoHealth struct{}

func (redisVideoHealth) held(keys []string) (int, error) {
	n, err := common.RDB.Exists(context.Background(), keys...).Result()
	return int(n), err
}

func (redisVideoHealth) addSample(key, field string, minute int64, buckets int) error {
	ttl := int64(buckets) * 60 * 2
	return common.RDB.Eval(context.Background(), videoAddSampleScript, []string{key}, minute%int64(buckets), minute, field, ttl).Err()
}

func (redisVideoHealth) window(key string, minute int64, buckets int) (map[string]int64, error) {
	raw, err := common.RDB.HGetAll(context.Background(), key).Result()
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

func (redisVideoHealth) add(key string, delta int64) (int64, error) {
	return common.RDB.Eval(context.Background(), videoAddFloorScript, []string{key}, delta).Int64()
}

func (redisVideoHealth) get(keys []string) ([]int64, error) {
	raw, err := common.RDB.MGet(context.Background(), keys...).Result()
	if err != nil {
		return nil, err
	}
	values := make([]int64, len(keys))
	for i, value := range raw {
		if text, ok := value.(string); ok {
			values[i], _ = strconv.ParseInt(text, 10, 64)
		}
	}
	return values, nil
}

func (redisVideoHealth) set(key string, value int64) error {
	return common.RDB.Set(context.Background(), key, value, 0).Err()
}

func (redisVideoHealth) acquire(key, token string, ttl time.Duration) (bool, error) {
	return common.RDB.SetNX(context.Background(), key, token, ttl).Result()
}

func (redisVideoHealth) release(key, token string) error {
	return common.RDB.Eval(context.Background(), videoReleaseScript, []string{key}, token).Err()
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
	mu     sync.Mutex
	hashes map[string]map[string]int64
	gauges map[string]int64
	slots  map[string]memoryVideoSlot
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
	return m.gauges[key], nil
}

func (m *memoryVideoHealthStore) get(keys []string) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	values := make([]int64, len(keys))
	for i, key := range keys {
		values[i] = m.gauges[key]
	}
	return values, nil
}

func (m *memoryVideoHealthStore) set(key string, value int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[key] = value
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
