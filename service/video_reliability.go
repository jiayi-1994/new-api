package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/videosched"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

const videoHealthAttemptKey = "video_health_attempt"
const videoHealthAdmissionKey = "video_health_admission"
const videoValidationLeaseKey = "video_validation_lease"
const videoSubmissionOwnerKey = "video_submission_owner"
const videoSubmissionLeaseTTL = 120 * time.Second

var ErrVideoHealthAdmission = errors.New("video health admission unavailable")
var videoReliabilityCache sync.Map
var videoReliabilityFailures atomic.Int64
var videoReliabilityReconcileCursor atomic.Int64

func VideoReliabilityCollectionFailures() int64 { return videoReliabilityFailures.Load() }

type videoReliabilityEntry struct {
	Snapshot videosched.ReliabilitySnapshot
	Expires  int64
}
type videoHealthAdmission struct {
	ChannelID int
	Version   int64
	Flow      string
	Model     string
	Identity  string
}

// BindVideoHealthChannel freezes the same configuration used to build the
// outgoing request, including shadow/weighted traffic and retry selections.
func BindVideoHealthChannel(c *gin.Context, channel *model.Channel, modelName string) {
	d := VideoSchedDecisionFrom(c)
	if d.Takeover || d.Shadow {
		c.Set("video_health_channel", videoHealthAdmission{ChannelID: channel.Id, Model: videoHealthModelName(channel, modelName), Identity: channel.VideoHealthIdentity()})
	}
}

type videoValidationLease struct {
	ChannelID  int
	Key, Token string
	Expires    int64
	Retained   bool
}

func frozenVideoSetting(c *gin.Context) *operation_setting.VideoSchedulingSetting {
	if value, ok := common.GetContextKeyType[*operation_setting.VideoSchedulingSetting](c, constant.ContextKeyVideoSchedSetting); ok && value != nil {
		return value
	}
	return operation_setting.GetVideoSchedulingSetting()
}

func videoReliabilityPolicy(s *operation_setting.VideoSchedulingSetting) videosched.Policy {
	return videosched.Policy{MinSamples: max(1, s.MinSamples), MinGenRate: max(.8, s.MinGenRate), MinOverallRate: max(.6, s.MinOverallRate), QualificationTTLSeconds: s.QualificationTTLSeconds, ValidationPeriodSeconds: s.ValidationPeriodSeconds, WindowSeconds: s.WindowSeconds}
}

func videoReliabilityKey(channelID int, modelName string) string {
	return fmt.Sprintf("%sreliability:%d:%s", videoSchedKeyPrefix, channelID, modelName)
}

// Online selection only reads these bounded snapshots, never the journal.
func GetVideoReliability(channelID int, modelName string) *videosched.ReliabilitySnapshot {
	if common.RedisEnabled && common.RDB == nil {
		return nil
	}
	key := videoReliabilityKey(channelID, modelName)
	var entry videoReliabilityEntry
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		data, err := common.RDB.Get(ctx, key).Bytes()
		if err != nil || common.Unmarshal(data, &entry) != nil {
			return nil
		}
	} else if value, ok := videoReliabilityCache.Load(key); ok {
		entry = value.(videoReliabilityEntry)
	}
	if entry.Expires <= time.Now().Unix() || entry.Snapshot.Model != modelName {
		return nil
	}
	return &entry.Snapshot
}

func publishVideoReliability(channelID int, view videosched.ReliabilitySnapshot) error {
	if common.RedisEnabled && common.RDB == nil {
		return ErrVideoHealthAdmission
	}
	key := videoReliabilityKey(channelID, view.Model)
	entry := videoReliabilityEntry{Snapshot: view, Expires: time.Now().Unix() + 180}
	if common.RedisEnabled && common.RDB != nil {
		data, err := common.Marshal(entry)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		// A delayed publisher cannot overwrite a newer blocked epoch.
		return common.RDB.Eval(ctx, `local old=redis.call('GET',KEYS[1]); if old then local v=cjson.decode(old); if v.Snapshot.state_version>tonumber(ARGV[2]) or (v.Snapshot.state_version==tonumber(ARGV[2]) and (v.Snapshot.state_revision or 0)>tonumber(ARGV[3])) then return 0 end end; redis.call('SET',KEYS[1],ARGV[1],'EX',180); return 1`, []string{key}, string(data), view.StateVersion, view.StateRevision).Err()
	}
	for {
		old, ok := videoReliabilityCache.Load(key)
		if !ok {
			if _, loaded := videoReliabilityCache.LoadOrStore(key, entry); !loaded {
				return nil
			}
			continue
		}
		previous := old.(videoReliabilityEntry).Snapshot
		if previous.StateVersion > view.StateVersion || previous.StateVersion == view.StateVersion && previous.StateRevision > view.StateRevision {
			return nil
		}
		if videoReliabilityCache.CompareAndSwap(key, old, entry) {
			return nil
		}
	}
}

func videoReliabilityWriteFailed(channelID int, modelName string, err error) {
	videoReliabilityFailures.Add(1)
	common.SysError(fmt.Sprintf("video reliability persistence failed: channel=%d model=%q error=%v", channelID, modelName, err))
	videoReliabilityCache.Delete(videoReliabilityKey(channelID, modelName))
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = common.RDB.Del(ctx, videoReliabilityKey(channelID, modelName)).Err()
	}
}

func publishPersistedVideoReliability(ctx context.Context, channelID int, modelName string) {
	var state model.VideoHealthState
	if err := model.DB.WithContext(ctx).Where("channel_id = ? AND model_name = ?", channelID, modelName).First(&state).Error; err != nil {
		videoReliabilityWriteFailed(channelID, modelName, err)
		return
	}
	view, err := state.Snapshot()
	if err == nil {
		err = publishVideoReliability(channelID, view)
	}
	if err != nil {
		videoReliabilityWriteFailed(channelID, modelName, err)
	}
}

func RefreshReviewedVideoHealth(ctx context.Context, attempt *model.VideoHealthAttempt) {
	view, err := model.RefreshVideoHealthState(ctx, attempt.ChannelID, attempt.ModelName, videoReliabilityPolicy(operation_setting.GetVideoSchedulingSetting()), time.Now().Unix())
	if err == nil {
		err = publishVideoReliability(attempt.ChannelID, view)
	}
	if err != nil {
		videoReliabilityWriteFailed(attempt.ChannelID, attempt.ModelName, err)
	}
}

// BeginVideoHealthTransmission is called only after local URL/header/client
// setup, immediately before the actual HTTP transport. Storage failures stop
// locally; a confirmed state conflict permits bounded admission reselection.
// Neither failure sends upstream bytes.
func BeginVideoHealthTransmission(c *gin.Context, info *relaycommon.RelayInfo) error {
	decision := VideoSchedDecisionFrom(c)
	if !decision.Takeover && !decision.Shadow {
		return nil
	}
	stopVideoHealthSubmissionOwner(c)
	s := frozenVideoSetting(c)
	strict := decision.Takeover && s.SelectionPolicy == videosched.PolicyStabilityCostV2
	channel, bound := common.GetContextKeyType[videoHealthAdmission](c, "video_health_channel")
	if !bound || channel.ChannelID != info.GetChannelID() || channel.Identity == "" {
		if strict {
			return ErrVideoHealthAdmission
		}
		return nil
	}
	flow, version := "weighted", int64(0)
	if decision.Shadow {
		flow = "shadow"
	}
	if strict {
		admission, ok := common.GetContextKeyType[videoHealthAdmission](c, videoHealthAdmissionKey)
		if !ok || admission.ChannelID != info.GetChannelID() || admission.Identity != channel.Identity || admission.Model != channel.Model {
			return ErrVideoHealthAdmission
		}
		flow, version = admission.Flow, admission.Version
	}
	attempt := &model.VideoHealthAttempt{RequestID: c.GetString(common.RequestIdKey), AttemptSeq: RequestPolicy(c).Attempts, ChannelID: info.GetChannelID(), ModelName: channel.Model, ConfigIdentity: channel.Identity, ActualGroup: RequestPolicy(c).SelectedGroup, StartedAt: time.Now().Unix(), WindowSeconds: s.WindowSeconds, StateVersion: version, Flow: flow}
	attempt.RequestStartedAt = RequestPolicy(c).StartedAt.Unix()
	attempt.SubmitOwner, attempt.SubmitLeaseExpires = common.GetRandomString(24), time.Now().Add(videoSubmissionLeaseTTL).Unix()
	attempt.Mode, attempt.SelectionPolicy = s.Mode, s.SelectionPolicy
	if strict && flow != "normal" {
		attempt.ValidationLimit = s.ExploreMaxInFlight
		if flow == "recover" {
			attempt.ValidationLimit = s.ProbeMaxInFlight
		}
	}
	if value, ok := c.Get(videoValidationLeaseKey); ok {
		if lease, ok := value.(*videoValidationLease); ok && lease != nil && lease.ChannelID == attempt.ChannelID {
			attempt.SlotKey, attempt.SlotToken, attempt.SlotExpires = lease.Key, lease.Token, lease.Expires
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()
	if strict && flow != "normal" {
		owned := false
		if attempt.SlotKey != "" && attempt.SlotExpires > time.Now().Unix() {
			if common.RedisEnabled {
				if common.RDB != nil {
					owner, err := common.RDB.Get(ctx, attempt.SlotKey).Result()
					owned = err == nil && owner == attempt.SlotToken
				}
			} else {
				m := memoryVideoHealth
				m.mu.Lock()
				slot, ok := m.slots[attempt.SlotKey]
				owned = ok && slot.token == attempt.SlotToken && time.Now().Before(slot.expires)
				m.mu.Unlock()
			}
		}
		if !owned {
			return ErrVideoHealthAdmission
		}
	}
	if !strict {
		if err := model.EnsureVideoHealthState(ctx, attempt.ChannelID, attempt.ModelName, attempt.ConfigIdentity); err != nil {
			videoReliabilityWriteFailed(attempt.ChannelID, attempt.ModelName, err)
			return nil
		}
	}
	err := model.BeginVideoHealthAttempt(ctx, attempt, strict)
	if err != nil {
		if errors.Is(err, model.ErrVideoHealthStateChanged) {
			// A concurrent transition is an expected admission conflict. Keep
			// reselection available with the latest state, including any block.
			publishPersistedVideoReliability(ctx, attempt.ChannelID, attempt.ModelName)
		} else {
			videoReliabilityWriteFailed(attempt.ChannelID, attempt.ModelName, err)
		}
		if strict {
			return fmt.Errorf("%w: %w", ErrVideoHealthAdmission, err)
		}
		return nil
	}
	c.Set(videoHealthAttemptKey, attempt)
	startVideoHealthSubmissionOwner(c, attempt)
	publishPersistedVideoReliability(ctx, attempt.ChannelID, attempt.ModelName)
	return nil
}

// The request owns its submission through response parsing and the durable task
// barrier, including a long upstream response or a client cancellation. Other
// nodes can expire the durable lease after this process disappears.
func startVideoHealthSubmissionOwner(c *gin.Context, attempt *model.VideoHealthAttempt) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.Set(videoSubmissionOwnerKey, func() {
		cancel()
		<-done
	})
	go func() {
		defer close(done)
		ticker := time.NewTicker(videoSubmissionLeaseTTL / 4)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(ctx, 2*time.Second)
				err := model.RenewVideoHealthSubmission(renewCtx, attempt.ID, attempt.SubmitOwner, time.Now().Add(videoSubmissionLeaseTTL).Unix())
				renewCancel()
				if err != nil && ctx.Err() == nil {
					videoReliabilityWriteFailed(attempt.ChannelID, attempt.ModelName, err)
				}
			}
		}
	}()
}

func stopVideoHealthSubmissionOwner(c *gin.Context) {
	if stop, ok := common.GetContextKeyType[func()](c, videoSubmissionOwnerKey); ok && stop != nil {
		stop()
		c.Set(videoSubmissionOwnerKey, nil)
	}
}

func VideoHealthReference(c *gin.Context) *model.TaskVideoHealthReference {
	attempt, ok := common.GetContextKeyType[*model.VideoHealthAttempt](c, videoHealthAttemptKey)
	if !ok || attempt == nil || attempt.AttemptSeq != RequestPolicy(c).Attempts {
		return nil
	}
	ref := &model.TaskVideoHealthReference{AttemptID: attempt.ID, RequestID: attempt.RequestID, AttemptSeq: attempt.AttemptSeq, ChannelID: attempt.ChannelID, Model: attempt.ModelName, StartedAt: attempt.StartedAt, StateVersion: attempt.StateVersion, Flow: attempt.Flow}
	if attempt.SlotKey != "" {
		ref.Slot = &model.TaskProbeSlot{Key: attempt.SlotKey, Token: attempt.SlotToken}
	}
	return ref
}

func ObserveVideoReliabilitySubmit(c *gin.Context, taskErr *taskdto.TaskError, cancelled bool) {
	attempt, ok := common.GetContextKeyType[*model.VideoHealthAttempt](c, videoHealthAttemptKey)
	if !ok || attempt == nil || attempt.AttemptSeq != RequestPolicy(c).Attempts {
		return
	}
	if taskErr != nil {
		defer stopVideoHealthSubmissionOwner(c)
	}
	submit, final, attribution := "accepted", "", ""
	if taskErr != nil {
		submit, final, attribution = "rejected", "upstream", "upstream"
		switch {
		case errors.Is(taskErr.Error, relaycommon.ErrTaskSubmitOutcomeUnknown):
			submit, final, attribution = "unknown", "unknown", "transport"
		case cancelled:
			final, attribution = "cancelled", "cancelled"
		case taskErr.SubmitFailureClass == VideoFailureUser || taskErr.SubmitFailureClass == VideoFailureCancelled:
			final, attribution = taskErr.SubmitFailureClass, taskErr.SubmitFailureClass
		case taskErr.StatusCode/100 == 5 || taskErr.StatusCode == 429 || taskErr.StatusCode == 401 || taskErr.StatusCode == 403:
			// Transport/service/auth failures remain upstream even when their
			// messages mention moderation or input validation infrastructure.
		default:
			// Only explicit plugin classifications exempt a request. An HTTP
			// 400 alone does not prove user fault for the new health metrics.
			if GetTaskAdaptorFunc != nil {
				if classifier, ok := GetTaskAdaptorFunc(constant.TaskPlatform(c.GetString("platform"))).(VideoFailureClassifier); ok && taskErr.Error != nil {
					if class, valid := classifier.ClassifyFailure(taskErr.Error.Error()); valid && (class == VideoFailureUser || class == VideoFailureCancelled) {
						final, attribution = class, class
					}
				}
			}
		}
	}
	attempt.SubmitOutcome, attempt.FinalOutcome = submit, final
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := model.ObserveVideoHealthAttempt(ctx, attempt.RequestID, attempt.AttemptSeq, nil, submit, final, attribution, time.Now().Unix()); err != nil {
		videoReliabilityWriteFailed(attempt.ChannelID, attempt.ModelName, err)
	} else {
		publishPersistedVideoReliability(ctx, attempt.ChannelID, attempt.ModelName)
	}
	if final == "unknown" || submit == "accepted" {
		retainVideoValidationLease(c)
	} else {
		releaseVideoValidationLease(c)
	}
}

func observeVideoReliabilityTerminal(task *model.Task, outcome VideoOutcome, attribution string) {
	ref := task.PrivateData.VideoHealth
	if ref == nil {
		return
	}
	final := "upstream"
	if outcome == VideoOutcomeSuccess {
		final = "success"
	} else if outcome == VideoOutcomeIgnored {
		final = attribution
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := model.ObserveVideoHealthAttempt(ctx, ref.RequestID, ref.AttemptSeq, &task.ID, "accepted", final, attribution, time.Now().Unix()); err != nil {
		videoReliabilityWriteFailed(ref.ChannelID, ref.Model, err)
	} else {
		publishPersistedVideoReliability(ctx, ref.ChannelID, ref.Model)
	}
	if ref.Slot != nil {
		releaseVideoProbeSlot(ref.Slot.Key, ref.Slot.Token)
	}
}

func linkVideoReliabilityTask(c *gin.Context, task *model.Task) {
	ref := task.PrivateData.VideoHealth
	if ref == nil {
		return
	}
	defer stopVideoHealthSubmissionOwner(c)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := model.ObserveVideoHealthAttempt(ctx, ref.RequestID, ref.AttemptSeq, &task.ID, "accepted", "", "", time.Now().Unix()); err != nil {
		videoReliabilityWriteFailed(ref.ChannelID, ref.Model, err)
	}
	if a, ok := common.GetContextKeyType[*model.VideoHealthAttempt](c, videoHealthAttemptKey); ok && a != nil {
		a.TaskPK = &task.ID
	}
	c.Set(videoValidationLeaseKey, nil) // the durable task owns its slot
}

// FinishVideoReliabilityRequest includes no-candidate requests but never
// attributes those to an unsubmitted channel. A terminal callback may win first.
func FinishVideoReliabilityRequest(c *gin.Context, panicked bool) {
	defer stopVideoHealthSubmissionOwner(c)
	defer releaseVideoValidationLease(c)
	d := VideoSchedDecisionFrom(c)
	if !d.Takeover && !d.Shadow {
		return
	}
	s := frozenVideoSetting(c)
	policy := RequestPolicy(c)
	r := &model.VideoHealthRequest{RequestID: c.GetString(common.RequestIdKey), StartedAt: policy.StartedAt.Unix(), ModelName: c.GetString("resolved_task_model"), Mode: s.Mode, SelectionPolicy: s.SelectionPolicy, ActualGroup: policy.SelectedGroup, Outcome: "failure", FinishedAt: time.Now().Unix()}
	if a, ok := common.GetContextKeyType[*model.VideoHealthAttempt](c, videoHealthAttemptKey); ok && a != nil {
		r.ChannelID = a.ChannelID
		if a.SubmitOutcome == "accepted" {
			r.Outcome, r.FinishedAt = "pending", 0
			if a.TaskPK == nil {
				r.Outcome, r.Missing = "unknown", true
			}
		} else if a.FinalOutcome == "unknown" {
			r.Outcome = "unknown"
		} else if a.FinalOutcome == "user" || a.FinalOutcome == "cancelled" {
			r.Outcome = a.FinalOutcome
		}
	}
	if panicked {
		r.Missing = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := model.FinishVideoHealthRequest(ctx, r); err != nil {
		videoReliabilityWriteFailed(r.ChannelID, r.ModelName, err)
	}
}

func retainVideoValidationLease(c *gin.Context) {
	if value, ok := c.Get(videoValidationLeaseKey); ok {
		if lease, ok := value.(*videoValidationLease); ok && lease != nil {
			lease.Retained = true
		}
	}
}

func releaseVideoValidationLease(c *gin.Context) {
	if value, ok := c.Get(videoValidationLeaseKey); ok {
		if lease, ok := value.(*videoValidationLease); ok && lease != nil && !lease.Retained {
			releaseVideoProbeSlot(lease.Key, lease.Token)
			c.Set(videoValidationLeaseKey, nil)
		}
	}
}

// Restore first, then check ownership on the primary database. A terminal
// callback can release its lease between the background snapshot and SET NX;
// checking only before SET NX would resurrect that completed owner's slot.
func restoreVideoValidationLease(ctx context.Context, attempt model.VideoHealthAttempt) error {
	store := videoHealthStore()
	if _, err := store.acquire(attempt.SlotKey, attempt.SlotToken, max(videoSubmissionLeaseTTL, time.Until(time.Unix(attempt.SlotExpires, 0)))); err != nil {
		return err
	}
	active, err := model.VideoValidationOwnerActive(ctx, attempt.ID, time.Now().Unix())
	if err != nil {
		return err
	}
	if !active {
		// Compare-and-delete cannot release a newer owner's reservation.
		return store.release(attempt.SlotKey, attempt.SlotToken)
	}
	return nil
}

// refreshVideoReliability runs off the request path and recovers state from the
// primary database. Missing Redis keys never initialize a new health state.
func RefreshVideoReliability(ctx context.Context) {
	if common.RedisEnabled && common.RDB == nil {
		videoReliabilityWriteFailed(0, "", ErrVideoHealthAdmission)
		return
	}
	if err := reconcileVideoReliability(ctx); err != nil {
		videoReliabilityWriteFailed(0, "", err)
		return
	}
	var channels []model.Channel
	if err := model.DB.WithContext(ctx).Where("settings LIKE ?", `%"video_scheduling"%`).Find(&channels).Error; err != nil {
		videoReliabilityWriteFailed(0, "", err)
		return
	}
	p := videoReliabilityPolicy(operation_setting.GetVideoSchedulingSetting())
	for _, channel := range channels {
		config, ok := VideoSchedulingConfigOf(&channel)
		if !ok {
			continue
		}
		channelID := channel.Id
		// Restore all unexpired owners for this channel before publishing any
		// readable state after a cache flush or process restart.
		leases, err := model.ListVideoValidationOwners(ctx, channelID, time.Now().Unix())
		if err != nil {
			videoReliabilityWriteFailed(channelID, "", err)
			continue
		}
		seen := map[string]bool{}
		restored := true
		for _, lease := range leases {
			if lease.SlotKey == "" || seen[lease.SlotKey] {
				continue
			}
			seen[lease.SlotKey] = true
			if err := restoreVideoValidationLease(ctx, lease); err != nil {
				restored = false
				videoReliabilityWriteFailed(channelID, "", err)
				break
			}
		}
		if !restored {
			continue
		}
		for modelName := range strings.SplitSeq(channel.Models, ",") {
			if _, priced := videoModelCost(config.Models, modelName); modelName == "" || !priced {
				continue
			}
			if err := model.EnsureVideoHealthState(ctx, channelID, modelName, channel.VideoHealthIdentity()); err != nil {
				videoReliabilityWriteFailed(channelID, modelName, err)
				continue
			}
			view, err := model.RefreshVideoHealthState(ctx, channelID, modelName, p, time.Now().Unix())
			if err == nil {
				err = publishVideoReliability(channelID, view)
			}
			if err != nil {
				videoReliabilityWriteFailed(channelID, modelName, err)
			}
		}
	}
	if err := model.CleanupVideoHealthFacts(ctx, time.Now().Unix()); err != nil {
		videoReliabilityWriteFailed(0, "", err)
	}
}

// Indexed scalar linkage repairs a lost post-insert callback. No audit or
// task JSON sweep is required, and pending tasks keep their host timeout.
func reconcileVideoReliability(ctx context.Context) error {
	if model.DB == nil {
		return ErrVideoHealthAdmission
	}
	if err := model.ReconcileAbandonedVideoHealthRequests(ctx, time.Now().Unix()); err != nil {
		return err
	}
	var attempts []model.VideoHealthAttempt
	if err := model.DB.WithContext(ctx).Where("id > ? AND final_outcome IN ?", videoReliabilityReconcileCursor.Load(), []string{"", "unknown"}).Order("id ASC").Limit(500).Find(&attempts).Error; err != nil {
		return err
	}
	if len(attempts) == 0 {
		videoReliabilityReconcileCursor.Store(0)
		return nil
	}
	ids := make([]int64, 0, len(attempts))
	for _, a := range attempts {
		ids = append(ids, a.ID)
	}
	var tasks []model.Task
	if err := model.DB.WithContext(ctx).Select("id", "video_health_attempt_id", "video_health_attribution", "status", "platform", "channel_id", "fail_reason", "finish_time").Where("video_health_attempt_id IN ?", ids).Find(&tasks).Error; err != nil {
		return err
	}
	byAttempt := make(map[int64]*model.Task, len(tasks))
	for i := range tasks {
		if tasks[i].VideoHealthAttemptID != nil {
			byAttempt[*tasks[i].VideoHealthAttemptID] = &tasks[i]
		}
	}
	now := time.Now().Unix()
	for _, a := range attempts {
		liveOwner := a.SubmitLeaseExpires > now
		if task := byAttempt[a.ID]; task != nil {
			final, attribution := "", ""
			if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
				outcome, class := videoTerminalAttribution(task, task.VideoHealthAttribution == "host")
				attribution = class
				final = "upstream"
				if outcome == VideoOutcomeSuccess {
					final = "success"
				} else if outcome == VideoOutcomeIgnored {
					final = class
				}
			}
			if err := model.ObserveVideoHealthAttempt(ctx, a.RequestID, a.AttemptSeq, &task.ID, "accepted", final, attribution, now); err != nil {
				return err
			}
			if final != "" && a.SlotKey != "" {
				releaseVideoProbeSlot(a.SlotKey, a.SlotToken)
				continue
			}
			liveOwner = final == ""
		} else if a.FinalOutcome == "" && a.StartedAt < now-120 && a.SubmitLeaseExpires <= now {
			// A response may legitimately take several minutes. Only an expired
			// submission owner permits reconciliation to declare it missing.
			if err := model.ExpireVideoHealthSubmission(ctx, a.RequestID, a.AttemptSeq, now); err != nil {
				return err
			}
		}
		if a.SlotKey != "" && (a.SlotExpires > now || liveOwner) {
			// NX restores a lost cache without stealing a newer owner's token.
			if err := restoreVideoValidationLease(ctx, a); err != nil {
				return err
			}
		}
	}
	videoReliabilityReconcileCursor.Store(attempts[len(attempts)-1].ID)
	return nil
}
