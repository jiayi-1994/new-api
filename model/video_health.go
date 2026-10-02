package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	VideoHealthRetention   = 30 * 24 * time.Hour
	VideoHealthRecoveryTTL = 7 * 24 * time.Hour
)

var ErrVideoHealthStateChanged = errors.New("video health state changed before admission")

// Activation starts a fresh verification epoch. Shadow/off collection is
// observational and can fail open, so its evidence cannot establish that the
// next takeover has a complete transport journal. Existing faults stay blocked.
func BeginVideoHealthActivation(tx *gorm.DB) error {
	if err := tx.Model(&VideoHealthState{}).Where("state IN ?", []string{videosched.HealthNormal, videosched.HealthUnverified}).Updates(map[string]any{
		"state": videosched.HealthUnverified, "reason": gorm.Expr("CASE WHEN qualification_json IS NULL OR qualification_json = '' THEN 'new_channel' ELSE 'activation_validation' END"), "version": gorm.Expr("version + 1"), "revision": gorm.Expr("revision + 1"), "validation_round": gorm.Expr("validation_round + 1"), "validation_started": 0, "validation_expires": 0, "validation_source": gorm.Expr("CASE WHEN qualification_json IS NULL OR qualification_json = '' THEN 'cold_start' ELSE 'revalidation' END"), "cohort_end": 0, "current_json": "",
	}).Error; err != nil {
		return err
	}
	return tx.Model(&VideoHealthState{}).Where("state = ?", videosched.HealthRecovering).Updates(map[string]any{"state": videosched.HealthBlocked, "reason": "activation requires recovery verification", "version": gorm.Expr("version + 1"), "revision": gorm.Expr("revision + 1"), "validation_round": gorm.Expr("validation_round + 1"), "recovery_started": 0, "recovery_expires": 0, "recovery_json": "", "cohort_end": 0}).Error
}

// The registration survives state loss: a missing state for a registered pair
// is an integrity error, never evidence that the channel is new.
type VideoHealthRegistration struct {
	ID        int64  `gorm:"primaryKey"`
	ChannelID int    `gorm:"uniqueIndex:idx_vh_registration,priority:1"`
	ModelName string `gorm:"type:varchar(191);uniqueIndex:idx_vh_registration,priority:2"`
	CreatedAt int64
}

// VideoHealthAttempt is a small durable journal of actual transport attempts.
// It contains no prompt, upstream response, credentials, or free-form errors.
type VideoHealthAttempt struct {
	ValidationLimit    int    `json:"-" gorm:"-"`
	RequestStartedAt   int64  `json:"-" gorm:"-"`
	Mode               string `json:"mode" gorm:"type:varchar(16)"`
	SelectionPolicy    string `json:"selection_policy" gorm:"type:varchar(32)"`
	ID                 int64  `json:"id" gorm:"primaryKey"`
	RequestID          string `json:"request_id" gorm:"type:varchar(64);uniqueIndex:idx_vh_attempt,priority:1"`
	AttemptSeq         int    `json:"attempt_seq" gorm:"uniqueIndex:idx_vh_attempt,priority:2"`
	ChannelID          int    `json:"channel_id" gorm:"index:idx_vh_cohort,priority:1;index:idx_vh_round,priority:1"`
	ModelName          string `json:"model" gorm:"type:varchar(191);index:idx_vh_cohort,priority:2;index:idx_vh_round,priority:2"`
	ActualGroup        string `json:"group" gorm:"type:varchar(64)"`
	StartedAt          int64  `json:"started_at" gorm:"index;index:idx_vh_round,priority:4"`
	BatchStart         int64  `json:"batch_start" gorm:"index:idx_vh_cohort,priority:3"`
	WindowSeconds      int    `json:"window_seconds"`
	StateVersion       int64  `json:"state_version"`
	ConfigIdentity     string `json:"-" gorm:"type:varchar(64)"`
	ValidationRound    int64  `json:"validation_round" gorm:"index:idx_vh_round,priority:3"`
	Flow               string `json:"flow" gorm:"type:varchar(16)"`                                          // normal | explore | probe | shadow | weighted
	SubmitOutcome      string `json:"submit_outcome" gorm:"type:varchar(16)"`                                // dispatching | accepted | rejected | unknown
	FinalOutcome       string `json:"final_outcome" gorm:"type:varchar(16);index:idx_vh_pending,priority:1"` // success | upstream | user | cancelled | unknown
	Attribution        string `json:"attribution" gorm:"type:varchar(32)"`
	TaskPK             *int64 `json:"task_pk" gorm:"index:idx_vh_pending,priority:2"`
	FinishedAt         int64  `json:"finished_at"`
	Missing            bool   `json:"missing"`
	ReviewedAt         int64  `json:"reviewed_at"`
	ReviewedBy         int    `json:"reviewed_by"`
	ReviewNote         string `json:"review_note" gorm:"type:text"`
	SlotKey            string `json:"-" gorm:"type:varchar(128)"`
	SlotToken          string `json:"-" gorm:"type:varchar(64)"`
	SlotExpires        int64  `json:"-" gorm:"index"`
	SubmitOwner        string `json:"-" gorm:"type:varchar(64)"`
	SubmitLeaseExpires int64  `json:"-"`
}

// VideoHealthRequest keeps the separate user-facing request denominator,
// including requests that exhaust candidates without any transport attempt.
type VideoHealthRequest struct {
	RequestID       string `json:"request_id" gorm:"type:varchar(64);primaryKey"`
	LastAttemptSeq  int    `json:"-"`
	StartedAt       int64  `json:"started_at" gorm:"index"`
	ModelName       string `json:"model" gorm:"type:varchar(191);index"`
	Mode            string `json:"mode" gorm:"type:varchar(16)"`
	SelectionPolicy string `json:"selection_policy" gorm:"type:varchar(32)"`
	ActualGroup     string `json:"group" gorm:"type:varchar(64)"`
	ChannelID       int    `json:"channel_id"`
	Outcome         string `json:"outcome" gorm:"type:varchar(32)"`
	FinishedAt      int64  `json:"finished_at"`
	Missing         bool   `json:"missing"`
}

// VideoHealthState outlives all sample windows. Only explicit versioned
// transitions can clear a block; deleting old facts never changes this row.
type VideoHealthState struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	ChannelID         int    `json:"channel_id" gorm:"uniqueIndex:idx_vh_state,priority:1"`
	ModelName         string `json:"model" gorm:"type:varchar(191);uniqueIndex:idx_vh_state,priority:2"`
	Version           int64  `json:"version"`
	Revision          int64  `json:"revision"`
	ValidationRound   int64  `json:"validation_round"`
	ConfigIdentity    string `json:"-" gorm:"type:varchar(64)"`
	ConfigVersion     int64  `json:"-"`
	ProbeFailures     int    `json:"probe_failures"`
	WindowSeconds     int    `json:"window_seconds"`
	State             string `json:"state" gorm:"type:varchar(16)"`
	Reason            string `json:"reason" gorm:"type:varchar(64)"`
	Integrity         string `json:"integrity" gorm:"type:varchar(16)"`
	BlockedAt         int64  `json:"blocked_at"`
	RecoveryStarted   int64  `json:"recovery_started"`
	RecoveryExpires   int64  `json:"recovery_expires"`
	LastBatchEnd      int64  `json:"last_batch_end"`
	LastValidatedEnd  int64  `json:"last_validated_end"`
	NextRefreshAt     int64  `json:"next_refresh_at" gorm:"index"`
	ValidationStarted int64  `json:"validation_started"`
	ValidationExpires int64  `json:"validation_expires"`
	ValidationSource  string `json:"validation_source" gorm:"type:varchar(16)"`
	LastValidationAt  int64  `json:"last_validation_at"`
	CohortEnd         int64  `json:"cohort_end"`
	QualificationJSON string `json:"-" gorm:"type:text"`
	CurrentJSON       string `json:"-" gorm:"type:text"`
	RecoveryJSON      string `json:"-" gorm:"type:text"`
}

func (s VideoHealthState) Snapshot() (videosched.ReliabilitySnapshot, error) {
	view := videosched.ReliabilitySnapshot{Version: videosched.ReliabilityVersion, Model: s.ModelName, ConfigIdentity: s.ConfigIdentity,
		State: s.State, StateVersion: s.Version, StateRevision: s.Revision, ValidationRound: s.ValidationRound, ProbeFailures: s.ProbeFailures, Reason: s.Reason, Integrity: s.Integrity,
		BlockedAt: s.BlockedAt, RecoveryStarted: s.RecoveryStarted, RecoveryExpires: s.RecoveryExpires,
		ValidationStarted: s.ValidationStarted, ValidationExpires: s.ValidationExpires, LastValidationAt: s.LastValidationAt}
	for _, field := range []struct {
		value  string
		target **videosched.ReliabilityEvidence
	}{
		{s.QualificationJSON, &view.Qualification}, {s.CurrentJSON, &view.Current}, {s.RecoveryJSON, &view.Recovery},
	} {
		if field.value == "" {
			continue
		}
		if err := common.UnmarshalJsonStr(field.value, field.target); err != nil {
			return view, err
		}
		if *field.target != nil {
			if err := (*field.target).Validate(); err != nil {
				return view, err
			}
		}
	}
	return view, view.Validate()
}

// VideoHealthIdentity binds evidence to the upstream configuration. Operational
// counters, prices, priorities and capacities do not change this identity.
// Only a digest is persisted; credentials and headers never enter the journal.
func (channel *Channel) VideoHealthIdentity() string {
	settings := channel.GetOtherSettings()
	settings.VideoScheduling = nil
	settings.UpstreamModelUpdateLastCheckTime = 0
	settings.UpstreamModelUpdateLastDetectedModels = nil
	settings.UpstreamModelUpdateLastRemovedModels = nil
	encoded, err := common.Marshal([]any{channel.Type, channel.Key, channel.BaseURL, channel.OpenAIOrganization,
		channel.Other, channel.Models, channel.ModelMapping, channel.Setting, channel.ParamOverride, channel.HeaderOverride, settings})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func EnsureVideoHealthState(ctx context.Context, channelID int, modelName string, identity ...string) error {
	if DB == nil {
		return errors.New("video health database unavailable")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(identity) > 0 && identity[0] != "" {
			var channel Channel
			if err := lockForUpdate(tx).First(&channel, channelID).Error; err != nil {
				return err
			}
			if channel.VideoHealthIdentity() != identity[0] {
				return ErrVideoHealthStateChanged
			}
		}
		registration := VideoHealthRegistration{ChannelID: channelID, ModelName: modelName, CreatedAt: time.Now().Unix()}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&registration)
		if result.Error != nil {
			return result.Error
		}
		configIdentity := ""
		if len(identity) > 0 {
			configIdentity = identity[0]
		}
		if result.RowsAffected == 0 {
			var state VideoHealthState
			if err := lockForUpdate(tx).Where("channel_id = ? AND model_name = ?", channelID, modelName).First(&state).Error; err != nil {
				return err
			}
			if configIdentity == "" || state.ConfigIdentity == configIdentity {
				return nil
			}
			version := state.Version
			state = VideoHealthState{ID: state.ID, ChannelID: channelID, ModelName: modelName,
				ConfigIdentity: configIdentity, ConfigVersion: version + 1, Version: version + 1, Revision: state.Revision, ValidationRound: state.ValidationRound + 1,
				State: videosched.HealthUnverified, Reason: "upstream_configuration_changed", Integrity: "complete", ValidationSource: "cold_start"}
			return SaveVideoHealthState(tx, &state, version)
		}
		state := VideoHealthState{ChannelID: channelID, ModelName: modelName, ConfigIdentity: configIdentity, Version: 1, Revision: 1, ValidationRound: 1, State: videosched.HealthUnverified, Reason: "new_channel", Integrity: "complete", ValidationSource: "cold_start"}
		return tx.Create(&state).Error
	})
}

// BeginVideoHealthAttempt runs immediately before HTTP transport execution.
// strict requires the state version selected by v2 to remain admissible.
func BeginVideoHealthAttempt(ctx context.Context, attempt *VideoHealthAttempt, strict bool) error {
	if attempt.RequestID == "" || attempt.AttemptSeq <= 0 || attempt.ChannelID <= 0 || attempt.ModelName == "" || attempt.WindowSeconds <= 0 {
		return errors.New("invalid video health attempt")
	}
	if DB == nil {
		return errors.New("video health database unavailable")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if attempt.ConfigIdentity != "" {
			var channel Channel
			if err := lockForUpdate(tx).First(&channel, attempt.ChannelID).Error; err != nil {
				return err
			}
			if channel.VideoHealthIdentity() != attempt.ConfigIdentity {
				return ErrVideoHealthStateChanged
			}
		}
		if strict && attempt.Flow != "normal" {
			if attempt.ValidationLimit < 1 || attempt.ValidationLimit > 16 || attempt.SlotKey == "" || attempt.SlotToken == "" || attempt.SlotExpires <= attempt.StartedAt {
				return ErrVideoHealthStateChanged
			}
			// All model states share this channel's oldest registration lock.
			// Redis is the fast reservation path, but its loss must not erase
			// the outstanding owners already journaled before transmission.
			var registration VideoHealthRegistration
			if err := lockForUpdate(tx).Where("channel_id = ?", attempt.ChannelID).Order("id ASC").First(&registration).Error; err != nil {
				return err
			}
			var slots struct{ Held, Collision int64 }
			if err := activeVideoValidationAttempts(tx, attempt.StartedAt).Where("channel_id = ?", attempt.ChannelID).
				Select("COUNT(*) AS held, COALESCE(SUM(CASE WHEN slot_key = ? THEN 1 ELSE 0 END),0) AS collision", attempt.SlotKey).Scan(&slots).Error; err != nil {
				return err
			}
			if slots.Held >= int64(attempt.ValidationLimit) || slots.Collision > 0 {
				return ErrVideoHealthStateChanged
			}
		}
		var state VideoHealthState
		if err := lockForUpdate(tx).Where("channel_id = ? AND model_name = ?", attempt.ChannelID, attempt.ModelName).First(&state).Error; err != nil {
			return err
		}
		if attempt.ConfigIdentity != state.ConfigIdentity {
			return ErrVideoHealthStateChanged
		}
		if strict {
			if state.Version != attempt.StateVersion || state.Integrity == "unavailable" || state.WindowSeconds != 0 && state.WindowSeconds != attempt.WindowSeconds {
				return ErrVideoHealthStateChanged
			}
			switch attempt.Flow {
			case "normal":
				view, err := state.Snapshot()
				if err != nil || state.State != videosched.HealthNormal || state.Integrity != "complete" || view.Qualification == nil || view.Qualification.ExpiresAt <= attempt.StartedAt || !view.Qualification.Mature(attempt.StartedAt) {
					return ErrVideoHealthStateChanged
				}
			case "explore", "revalidate":
				if state.State != videosched.HealthUnverified {
					return ErrVideoHealthStateChanged
				}
			case "recover":
				if state.State != videosched.HealthRecovering || state.RecoveryExpires <= attempt.StartedAt {
					return ErrVideoHealthStateChanged
				}
			default:
				return ErrVideoHealthStateChanged
			}
		}
		attempt.StateVersion = state.Version
		attempt.ValidationRound = state.ValidationRound
		attempt.BatchStart = attempt.StartedAt / int64(attempt.WindowSeconds) * int64(attempt.WindowSeconds)
		attempt.SubmitOutcome = "dispatching"
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(attempt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("duplicate video health transport attempt")
		}
		requestStarted := attempt.RequestStartedAt
		if requestStarted == 0 {
			requestStarted = attempt.StartedAt
		}
		request := VideoHealthRequest{RequestID: attempt.RequestID, LastAttemptSeq: attempt.AttemptSeq, StartedAt: requestStarted, ModelName: attempt.ModelName, Mode: attempt.Mode, SelectionPolicy: attempt.SelectionPolicy, ActualGroup: attempt.ActualGroup, ChannelID: attempt.ChannelID, Outcome: "pending"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&request).Error; err != nil {
			return err
		}
		if err := tx.Model(&VideoHealthRequest{}).Where("request_id = ? AND outcome IN ? AND COALESCE(last_attempt_seq, 0) <= ?", attempt.RequestID, []string{"pending", "unknown"}, attempt.AttemptSeq).Updates(map[string]any{"actual_group": attempt.ActualGroup, "channel_id": attempt.ChannelID, "last_attempt_seq": attempt.AttemptSeq, "outcome": "pending", "missing": false, "finished_at": 0}).Error; err != nil {
			return err
		}
		if attempt.Flow == "explore" || attempt.Flow == "revalidate" || attempt.Flow == "recover" {
			return tx.Model(&state).Updates(map[string]any{"last_validation_at": attempt.StartedAt, "revision": gorm.Expr("revision + 1")}).Error
		}
		return nil
	})
}

// A cache TTL limits abandoned unknown submissions, not a durable task's
// execution lifetime. Task timeouts start after submit response parsing and may
// be disabled, so an unfinished task retains its validation slot until terminal.
func activeVideoValidationAttempts(tx *gorm.DB, now int64) *gorm.DB {
	activeTasks := tx.Model(&Task{}).Select("video_health_attempt_id").
		Where("video_health_attempt_id IS NOT NULL AND status NOT IN ?", []TaskStatus{TaskStatusSuccess, TaskStatusFailure})
	return tx.Model(&VideoHealthAttempt{}).
		Where("slot_key <> ? AND final_outcome IN ?", "", []string{"", "unknown"}).
		Where("slot_expires > ? OR submit_lease_expires > ? OR id IN (?)", now, now, activeTasks)
}

func ListVideoValidationOwners(ctx context.Context, channelID int, now int64) ([]VideoHealthAttempt, error) {
	var owners []VideoHealthAttempt
	err := activeVideoValidationAttempts(DB.WithContext(ctx), now).
		Select("slot_key", "slot_token", "slot_expires").Where("channel_id = ?", channelID).
		Order("id DESC").Limit(512).Find(&owners).Error
	return owners, err
}

// ObserveVideoHealthAttempt is idempotent for both the submit observation and
// the terminal observation. A terminal callback can arrive before task linkage.
func ObserveVideoHealthAttempt(ctx context.Context, requestID string, attemptSeq int, taskPK *int64, submitOutcome, finalOutcome, attribution string, now int64) error {
	if DB == nil {
		return errors.New("video health database unavailable")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return observeVideoHealthAttempt(tx, requestID, attemptSeq, taskPK, submitOutcome, finalOutcome, attribution, now, false)
	})
}

// ReviewVideoHealthUnknown closes an abandoned unknown for future health
// verification only. The original outcome remains unknown in the journal and
// user statistics; no task, billing, refund or retry is performed. Fresh recovery
// evidence is required, so review can never manufacture a healthy certificate.
func ReviewVideoHealthUnknown(ctx context.Context, attemptID int64, actor int, note string, now int64) (*VideoHealthAttempt, error) {
	note = strings.TrimSpace(note)
	if attemptID <= 0 || actor <= 0 || note == "" || len(note) > 1000 {
		return nil, errors.New("review requires an operator and an evidence note of at most 1000 bytes")
	}
	var attempt VideoHealthAttempt
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&attempt, attemptID).Error; err != nil {
			return err
		}
		if attempt.ReviewedAt > 0 {
			return nil // idempotent: preserve the first operator and their evidence
		}
		if attempt.FinalOutcome != "unknown" || attempt.TaskPK != nil || attempt.SubmitLeaseExpires > now || attempt.SlotExpires > now || attempt.StartedAt >= now-120 {
			return errors.New("only an abandoned unknown submission without a task can be reviewed")
		}
		var tasks int64
		if err := tx.Model(&Task{}).Where("video_health_attempt_id = ?", attempt.ID).Count(&tasks).Error; err != nil {
			return err
		}
		if tasks > 0 {
			return errors.New("submission has a task; wait for automatic reconciliation")
		}
		attempt.ReviewedAt, attempt.ReviewedBy, attempt.ReviewNote = now, actor, note
		if err := tx.Model(&attempt).Updates(map[string]any{"reviewed_at": now, "reviewed_by": actor, "review_note": note}).Error; err != nil {
			return err
		}
		var state VideoHealthState
		if err := lockForUpdate(tx).Where("channel_id = ? AND model_name = ?", attempt.ChannelID, attempt.ModelName).First(&state).Error; err != nil {
			return err
		}
		if attempt.ConfigIdentity != state.ConfigIdentity || attempt.StateVersion < state.ConfigVersion {
			return nil
		}
		version := state.Version
		state.State, state.Reason, state.BlockedAt = videosched.HealthBlocked, "unknown_submission_reviewed", now
		state.Version++
		state.ValidationRound++
		state.RecoveryStarted, state.RecoveryExpires, state.ValidationStarted, state.ValidationExpires, state.CohortEnd = 0, 0, 0, 0, 0
		state.CurrentJSON, state.RecoveryJSON = "", ""
		return SaveVideoHealthState(tx, &state, version)
	})
	return &attempt, err
}

// RenewVideoHealthSubmission keeps a live request distinct from a process that
// disappeared before durable task handoff. The token fences old request owners.
func RenewVideoHealthSubmission(ctx context.Context, attemptID int64, owner string, expires int64) error {
	if DB == nil || owner == "" {
		return errors.New("video health submission owner unavailable")
	}
	return DB.WithContext(ctx).Model(&VideoHealthAttempt{}).
		Where("id = ? AND submit_owner = ? AND task_pk IS NULL AND final_outcome = ?", attemptID, owner, "").
		Update("submit_lease_expires", expires).Error
}

// ExpireVideoHealthSubmission rechecks the owner and task inside the same
// transaction as the missing observation. A late terminal result cannot be
// overwritten by a reconciler that read an older unlinked attempt.
func ExpireVideoHealthSubmission(ctx context.Context, requestID string, attemptSeq int, now int64) error {
	if DB == nil {
		return errors.New("video health database unavailable")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return observeVideoHealthAttempt(tx, requestID, attemptSeq, nil, "unknown", "unknown", "missing", now, true)
	})
}

func observeVideoHealthAttempt(tx *gorm.DB, requestID string, attemptSeq int, taskPK *int64, submitOutcome, finalOutcome, attribution string, now int64, expireOnly bool) error {
	var attempt VideoHealthAttempt
	if err := lockForUpdate(tx).Where("request_id = ? AND attempt_seq = ?", requestID, attemptSeq).First(&attempt).Error; err != nil {
		return err
	}
	if expireOnly {
		if attempt.FinalOutcome != "" || attempt.TaskPK != nil || attempt.SubmitLeaseExpires > now || attempt.StartedAt >= now-120 {
			return nil
		}
		var tasks int64
		if err := tx.Model(&Task{}).Where("video_health_attempt_id = ?", attempt.ID).Count(&tasks).Error; err != nil {
			return err
		}
		if tasks > 0 {
			return nil
		}
	}
	updates := map[string]any{}
	if taskPK != nil && attempt.TaskPK == nil {
		updates["task_pk"] = *taskPK
	}
	if attempt.SubmitOutcome == "dispatching" && submitOutcome != "" {
		updates["submit_outcome"] = submitOutcome
	}
	changed := finalOutcome != "" && (attempt.FinalOutcome == "" || attempt.FinalOutcome == "unknown" && taskPK != nil && submitOutcome == "accepted" && finalOutcome != "unknown")
	if changed {
		updates["final_outcome"], updates["attribution"], updates["finished_at"] = finalOutcome, attribution, now
		if expireOnly {
			updates["missing"] = true
		}
		if submitOutcome == "accepted" {
			updates["submit_outcome"] = submitOutcome
			updates["missing"] = false
		}
	}
	if len(updates) > 0 {
		if err := tx.Model(&attempt).Updates(updates).Error; err != nil {
			return err
		}
	}
	fault := finalOutcome == "upstream" || finalOutcome == "unknown"
	if changed && (fault || attempt.Flow == "recover") {
		var state VideoHealthState
		if err := lockForUpdate(tx).Where("channel_id = ? AND model_name = ?", attempt.ChannelID, attempt.ModelName).First(&state).Error; err != nil {
			return err
		}
		version, dirty := state.Version, false
		if attempt.ConfigIdentity == state.ConfigIdentity && attempt.StateVersion >= state.ConfigVersion && attempt.Flow == "recover" && attempt.ValidationRound == state.ValidationRound {
			if fault {
				state.ProbeFailures = min(32, state.ProbeFailures+1)
				dirty = true
			} else if finalOutcome == "success" {
				state.ProbeFailures = 0
				dirty = true
			}
		}
		// New faults revoke a normal certificate immediately. Probe failures
		// belong to their recovery round and are assessed against both gates.
		if fault && attempt.ConfigIdentity == state.ConfigIdentity && attempt.StateVersion >= state.ConfigVersion && (state.State == videosched.HealthNormal || state.State == videosched.HealthUnverified) {
			state.State, state.Reason, state.BlockedAt = videosched.HealthBlocked, "new upstream failure", now
			if finalOutcome == "unknown" {
				state.Reason = "submit outcome unknown"
			}
			state.Version++
			dirty = true
		}
		if dirty {
			if err := SaveVideoHealthState(tx, &state, version); err != nil {
				return err
			}
		}
	}
	if submitOutcome == "accepted" || changed && finalOutcome == "unknown" {
		outcome := finalOutcome
		if outcome == "upstream" {
			outcome = "failure"
		}
		finishedAt := now
		if outcome == "" {
			outcome, finishedAt = "pending", 0
		}
		request := VideoHealthRequest{RequestID: requestID, LastAttemptSeq: attempt.AttemptSeq, ModelName: attempt.ModelName, StartedAt: attempt.StartedAt, ChannelID: attempt.ChannelID, ActualGroup: attempt.ActualGroup, Mode: attempt.Mode, SelectionPolicy: attempt.SelectionPolicy, Outcome: outcome, FinishedAt: finishedAt, Missing: expireOnly}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&request).Error; err != nil {
			return err
		}
		if err := tx.Model(&VideoHealthRequest{}).Where("request_id = ? AND outcome IN ? AND COALESCE(last_attempt_seq, 0) <= ?", requestID, []string{"pending", "unknown"}, attempt.AttemptSeq).Updates(map[string]any{
			"outcome": outcome, "finished_at": finishedAt, "missing": expireOnly, "last_attempt_seq": attempt.AttemptSeq,
			"channel_id": attempt.ChannelID, "actual_group": attempt.ActualGroup, "model_name": attempt.ModelName,
			"mode": attempt.Mode, "selection_policy": attempt.SelectionPolicy,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func FinishVideoHealthRequest(ctx context.Context, request *VideoHealthRequest) error {
	if DB == nil {
		return errors.New("video health database unavailable")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(request).Error; err != nil {
			return err
		}
		// A terminal result may already have won. Fill provenance without
		// replacing that result with the request's older pending observation.
		if err := tx.Model(&VideoHealthRequest{}).Where("request_id = ?", request.RequestID).Updates(map[string]any{
			"started_at": request.StartedAt, "mode": request.Mode, "selection_policy": request.SelectionPolicy,
			"actual_group": request.ActualGroup, "channel_id": request.ChannelID,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&VideoHealthRequest{}).Where("request_id = ? AND outcome IN ?", request.RequestID, []string{"pending", "unknown"}).Updates(map[string]any{"outcome": request.Outcome, "finished_at": request.FinishedAt, "missing": request.Missing}).Error
	})
}

// A request may disappear after a definite rejection, before another retry or
// its HTTP response. Those attempts are already terminal and do not appear in
// the pending-attempt reconciler. Sequence fencing keeps a newer retry safe.
func ReconcileAbandonedVideoHealthRequests(ctx context.Context, now int64) error {
	stale := DB.WithContext(ctx).Model(&VideoHealthAttempt{}).
		Select("request_id", "attempt_seq").Where("submit_outcome = ? AND final_outcome IN ? AND COALESCE(submit_lease_expires, 0) <= ? AND started_at < ?", "rejected", []string{"upstream", "user", "cancelled"}, now, now-120)
	pending := DB.WithContext(ctx).Model(&VideoHealthRequest{}).Where("outcome = ?", "pending")
	var requests []VideoHealthRequest
	if err := DB.WithContext(ctx).Table("(?) AS requests", pending).
		Select("requests.request_id", "requests.last_attempt_seq").
		Joins("JOIN (?) AS attempts ON attempts.request_id = requests.request_id AND attempts.attempt_seq = requests.last_attempt_seq", stale).
		Order("requests.started_at ASC").Limit(500).Find(&requests).Error; err != nil {
		return err
	}
	for _, request := range requests {
		if err := DB.WithContext(ctx).Model(&VideoHealthRequest{}).
			Where("request_id = ? AND outcome = ? AND last_attempt_seq = ?", request.RequestID, "pending", request.LastAttemptSeq).
			Updates(map[string]any{"outcome": "unknown", "missing": true, "finished_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}

// StartVideoHealthRecovery admits a real probe after the shared slot has been
// acquired. The caller uses the returned version on its attempt and task.
func StartVideoHealthRecovery(ctx context.Context, channelID int, modelName string, expectedVersion, now int64, periodSeconds int) (int64, error) {
	if DB == nil {
		return 0, errors.New("video health database unavailable")
	}
	if periodSeconds <= 0 || periodSeconds > int(VideoHealthRetention.Seconds()) {
		return 0, errors.New("invalid video health validation period")
	}
	var version int64
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state VideoHealthState
		if err := lockForUpdate(tx).Where("channel_id = ? AND model_name = ?", channelID, modelName).First(&state).Error; err != nil {
			return err
		}
		if state.Version != expectedVersion || state.State != videosched.HealthBlocked && state.State != videosched.HealthRecovering {
			return ErrVideoHealthStateChanged
		}
		if state.State == videosched.HealthBlocked || state.RecoveryExpires <= now {
			state.State, state.Reason = videosched.HealthRecovering, "recovery verification"
			state.Version++
			state.ValidationRound++
			state.RecoveryStarted, state.RecoveryExpires = now, now+int64(periodSeconds)
			state.ValidationStarted, state.ValidationExpires, state.ValidationSource = now, state.RecoveryExpires, "recovery"
			state.CohortEnd = 0
			state.RecoveryJSON = ""
			if err := SaveVideoHealthState(tx, &state, expectedVersion); err != nil {
				return err
			}
		}
		version = state.Version
		return nil
	})
	return version, err
}

func StartVideoHealthValidation(ctx context.Context, channelID int, modelName string, expectedVersion, now int64, p videosched.Policy, flow string) (int64, error) {
	if flow == "recover" {
		return StartVideoHealthRecovery(ctx, channelID, modelName, expectedVersion, now, p.ValidationPeriodSeconds)
	}
	if DB == nil || p.ValidationPeriodSeconds <= 0 || p.ValidationPeriodSeconds > 30*86400 {
		return 0, ErrVideoHealthStateChanged
	}
	var version int64
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state VideoHealthState
		if err := lockForUpdate(tx).Where("channel_id = ? AND model_name = ?", channelID, modelName).First(&state).Error; err != nil {
			return err
		}
		if state.Version != expectedVersion || state.Integrity == "unavailable" {
			return ErrVideoHealthStateChanged
		}
		if err := revalidateVideoHealthQualification(&state, p, now); err != nil {
			return err
		}
		if state.State != videosched.HealthUnverified {
			return ErrVideoHealthStateChanged
		}
		if state.ValidationStarted == 0 || state.ValidationExpires <= now {
			if state.ValidationStarted > 0 {
				state.Version++
				state.ValidationRound++
			}
			state.ValidationStarted, state.ValidationExpires, state.CohortEnd = now, now+int64(p.ValidationPeriodSeconds), 0
		}
		if err := SaveVideoHealthState(tx, &state, expectedVersion); err != nil {
			return err
		}
		version = state.Version
		return nil
	})
	return version, err
}

// Policy changes can revoke a still-unexpired certificate. A larger sample
// requirement needs more verification; a measured rate below the new gate is a
// known fault and must take the recovery path, including its cooldown.
func revalidateVideoHealthQualification(state *VideoHealthState, p videosched.Policy, now int64) error {
	if state.State != videosched.HealthNormal {
		return nil
	}
	view, err := state.Snapshot()
	if err != nil {
		return err
	}
	reason := view.NormalReason(p, now)
	if reason == "" {
		return nil
	}
	state.Version++
	if reason == "generation rate below minimum" || reason == "overall completion below minimum" {
		state.State, state.Reason, state.BlockedAt = videosched.HealthBlocked, reason, now
		return nil
	}
	state.State, state.Reason = videosched.HealthUnverified, "insufficient_samples"
	if reason == "health qualification expired" {
		state.Reason = "evidence_expired"
	}
	state.ValidationRound++
	state.ValidationStarted, state.ValidationExpires, state.ValidationSource = now, now+int64(p.ValidationPeriodSeconds), "revalidation"
	state.CohortEnd, state.CurrentJSON = 0, ""
	return nil
}

func ListVideoHealthAttempts(ctx context.Context, requestID string) ([]VideoHealthAttempt, error) {
	var attempts []VideoHealthAttempt
	err := DB.WithContext(ctx).Where("request_id = ?", requestID).Order("attempt_seq ASC").Limit(512).Find(&attempts).Error
	return attempts, err
}

// AggregateVideoHealthEvidence uses scalar SQL only. The cohort is fixed by
// submit time, never by which tasks happened to complete first.
func AggregateVideoHealthEvidence(tx *gorm.DB) (videosched.ReliabilityEvidence, error) {
	var e videosched.ReliabilityEvidence
	err := tx.Model(&VideoHealthAttempt{}).Select(`COUNT(*) AS submitted,
COALESCE(SUM(CASE WHEN submit_outcome = 'accepted' THEN 1 ELSE 0 END),0) AS accepted,
COALESCE(SUM(CASE WHEN final_outcome = 'success' THEN 1 ELSE 0 END),0) AS succeeded,
COALESCE(SUM(CASE WHEN submit_outcome = 'rejected' AND final_outcome = 'upstream' THEN 1 ELSE 0 END),0) AS rejected,
COALESCE(SUM(CASE WHEN submit_outcome = 'accepted' AND final_outcome = 'upstream' THEN 1 ELSE 0 END),0) AS generation_failed,
COALESCE(SUM(CASE WHEN final_outcome = 'user' THEN 1 ELSE 0 END),0) AS user,
COALESCE(SUM(CASE WHEN final_outcome = 'cancelled' THEN 1 ELSE 0 END),0) AS cancelled,
COALESCE(SUM(CASE WHEN final_outcome = '' THEN 1 ELSE 0 END),0) AS pending,
COALESCE(SUM(CASE WHEN final_outcome = 'unknown' THEN 1 ELSE 0 END),0) AS unknown,
COALESCE(SUM(CASE WHEN missing = ? THEN 1 ELSE 0 END),0) AS missing`, true).Scan(&e).Error
	return e, err
}

// SaveVideoHealthState also compares the version on SQLite, whose transaction
// locking replaces SELECT FOR UPDATE. A stale writer must never win a reset.
func SaveVideoHealthState(tx *gorm.DB, state *VideoHealthState, expectedVersion int64) error {
	previousRevision := state.Revision
	state.Revision++
	result := tx.Model(&VideoHealthState{}).Where("id = ? AND version = ? AND revision = ?", state.ID, expectedVersion, previousRevision).
		Select("*").Omit("id").Updates(state)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVideoHealthStateChanged
	}
	return nil
}

// RefreshVideoHealthState seals contiguous submit cohorts. It never cherry
// picks fast completions, and a sealed cohort's certificate is issued once.
func RefreshVideoHealthState(ctx context.Context, channelID int, modelName string, p videosched.Policy, now int64) (videosched.ReliabilitySnapshot, error) {
	var view videosched.ReliabilitySnapshot
	if DB == nil {
		return view, errors.New("video health database unavailable")
	}
	if p.MinSamples < 1 || p.MinGenRate < .8 || p.MinGenRate > 1 || p.MinOverallRate < .6 || p.MinOverallRate > 1 ||
		p.QualificationTTLSeconds <= 0 || p.QualificationTTLSeconds > 7*86400 || p.ValidationPeriodSeconds <= 0 || p.ValidationPeriodSeconds > 30*86400 {
		return view, errors.New("invalid video health policy")
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state VideoHealthState
		if err := lockForUpdate(tx).Where("channel_id = ? AND model_name = ?", channelID, modelName).First(&state).Error; err != nil {
			return err
		}
		version := state.Version
		if p.WindowSeconds > 0 {
			if state.WindowSeconds != 0 && state.WindowSeconds != p.WindowSeconds {
				state.Version++
				state.ValidationRound++
				state.ValidationStarted, state.ValidationExpires, state.CohortEnd = now, now+int64(p.ValidationPeriodSeconds), 0
				state.CurrentJSON = ""
				if state.State == videosched.HealthRecovering {
					state.RecoveryStarted, state.RecoveryExpires, state.RecoveryJSON = now, state.ValidationExpires, ""
				}
			}
			state.WindowSeconds = p.WindowSeconds
		}
		var err error
		if view, err = state.Snapshot(); err != nil {
			return err
		}
		var incomplete int64
		if err = tx.Model(&VideoHealthAttempt{}).Where("channel_id = ? AND model_name = ? AND COALESCE(config_identity, '') = ? AND state_version >= ? AND COALESCE(reviewed_at, 0) = 0 AND (final_outcome = ? OR missing = ?)", channelID, modelName, state.ConfigIdentity, state.ConfigVersion, "unknown", true).Count(&incomplete).Error; err != nil {
			return err
		}
		state.Integrity = "complete"
		if incomplete > 0 {
			state.Integrity = "uncertain"
		}
		if err := revalidateVideoHealthQualification(&state, p, now); err != nil {
			return err
		}
		// Expiry starts another bounded verification round, not a certificate.
		// Unresolved unknown/missing facts above survive every round reset.
		if state.State == videosched.HealthUnverified && (state.ValidationStarted == 0 || state.ValidationExpires <= now) {
			if state.ValidationStarted > 0 {
				state.Version++
				state.ValidationRound++
				state.Reason = "insufficient_samples"
			}
			state.ValidationStarted, state.ValidationExpires = now, now+int64(p.ValidationPeriodSeconds)
			state.CohortEnd = 0
		}
		if state.State == videosched.HealthRecovering && state.RecoveryExpires <= now {
			state.State, state.Reason = videosched.HealthBlocked, "recovery verification expired"
			state.Version++
		}
		{
			query := tx.Where("channel_id = ? AND model_name = ? AND validation_round = ? AND started_at >= ? AND started_at <= ?", channelID, modelName, state.ValidationRound, state.ValidationStarted, now)
			if state.State == videosched.HealthRecovering {
				query = query.Where("flow = ?", "recover")
			}
			var batches []struct {
				BatchStart    int64
				WindowSeconds int
				Generation    int64
				Overall       int64
			}
			if err = query.Session(&gorm.Session{}).Model(&VideoHealthAttempt{}).Select(`batch_start, window_seconds,
SUM(CASE WHEN submit_outcome = 'accepted' AND final_outcome IN ('success','upstream') THEN 1 ELSE 0 END) AS generation,
SUM(CASE WHEN final_outcome IN ('success','upstream') THEN 1 ELSE 0 END) AS overall`).Group("batch_start, window_seconds").Order("batch_start ASC").Find(&batches).Error; err != nil {
				return err
			}
			// Display the current round, including its open/pending batch. Only
			// the separate sealed interval below may create a qualification.
			state.CurrentJSON = ""
			if len(batches) > 0 {
				current, currentErr := AggregateVideoHealthEvidence(query.Session(&gorm.Session{}))
				if currentErr != nil {
					return currentErr
				}
				current.Version, current.Source, current.BatchStart, current.WindowSeconds, current.AsOf = videosched.ReliabilityVersion, state.ValidationSource, state.ValidationStarted, batches[0].WindowSeconds, now
				if current.Source == "" {
					current.Source = "window"
				}
				for _, batch := range batches {
					current.BatchEnd = max(current.BatchEnd, batch.BatchStart+int64(batch.WindowSeconds))
				}
				state.CurrentJSON, err = marshalVideoHealthEvidence(current)
				if err != nil {
					return err
				}
				if state.State == videosched.HealthRecovering {
					state.RecoveryJSON = state.CurrentJSON
				}
			}
			end, window, generation, overall := int64(0), 0, int64(0), int64(0)
			for _, batch := range batches {
				batchEnd := batch.BatchStart + int64(batch.WindowSeconds)
				if batchEnd > now || state.CohortEnd > 0 && batchEnd > state.CohortEnd {
					break
				}
				if window != 0 && window != batch.WindowSeconds {
					break
				}
				end, window = batchEnd, batch.WindowSeconds
				generation += batch.Generation
				overall += batch.Overall
				if state.CohortEnd == 0 && generation >= int64(p.MinSamples) && overall >= int64(p.MinSamples) {
					state.CohortEnd = end
					break
				}
			}
			if state.State != videosched.HealthBlocked && end > state.ValidationStarted {
				e, aggregateErr := AggregateVideoHealthEvidence(query.Session(&gorm.Session{}).Where("started_at < ?", end))
				if aggregateErr != nil {
					return aggregateErr
				}
				e.Version, e.Source, e.BatchStart, e.BatchEnd, e.WindowSeconds, e.AsOf = videosched.ReliabilityVersion, state.ValidationSource, state.ValidationStarted, end, window, now
				if e.Source == "" {
					e.Source = "window"
				}
				reason := e.QualificationReason(p.MinSamples, p.MinGenRate, p.MinOverallRate, now)
				if reason == "" && state.Integrity == "complete" && end > state.LastValidatedEnd {
					e.ValidatedAt, e.ExpiresAt = now, now+int64(p.QualificationTTLSeconds)
					state.QualificationJSON, err = marshalVideoHealthEvidence(e)
					if err != nil {
						return err
					}
					state.State, state.Reason, state.LastValidatedEnd = videosched.HealthNormal, "qualified", end
					// A new state version fences stale admissions. Keep the evidence
					// round and the next submit boundary: already-open later batches
					// must retain both their slow failures and their successes.
					state.Version++
					state.ValidationStarted, state.ValidationExpires, state.ValidationSource, state.CohortEnd = end, now+int64(p.ValidationPeriodSeconds), "window", 0
				} else if e.Mature(now) && generation >= int64(p.MinSamples) && overall >= int64(p.MinSamples) && reason != "" {
					state.State, state.Reason, state.BlockedAt = videosched.HealthBlocked, reason, now
					state.Version++
				} else if state.State == videosched.HealthUnverified && state.Reason != "evidence_expired" {
					state.Reason = "insufficient_samples"
				}
				state.LastBatchEnd = end
			}
		}
		state.NextRefreshAt = now + 30
		if err = SaveVideoHealthState(tx, &state, version); err != nil {
			return err
		}
		view, err = state.Snapshot()
		return err
	})
	return view, err
}

func marshalVideoHealthEvidence(e videosched.ReliabilityEvidence) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	b, err := common.Marshal(e)
	return string(b), err
}

// CleanupVideoHealthFacts keeps unresolved facts, registrations and state.
// It is bounded so retention maintenance does not hold a large write lock.
func CleanupVideoHealthFacts(ctx context.Context, now int64) error {
	cutoff := now - int64(VideoHealthRetention.Seconds())
	var ids []int64
	if err := DB.WithContext(ctx).Model(&VideoHealthAttempt{}).Where("started_at < ? AND final_outcome IN ? AND missing = ?", cutoff, []string{"success", "upstream", "user", "cancelled"}, false).Limit(500).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := DB.WithContext(ctx).Where("id IN ?", ids).Delete(&VideoHealthAttempt{}).Error; err != nil {
			return err
		}
	}
	var requests []string
	if err := DB.WithContext(ctx).Model(&VideoHealthRequest{}).Where("started_at < ? AND outcome NOT IN ? AND missing = ?", cutoff, []string{"pending", "unknown"}, false).Limit(500).Pluck("request_id", &requests).Error; err != nil {
		return err
	}
	if len(requests) == 0 {
		return nil
	}
	return DB.WithContext(ctx).Where("request_id IN ?", requests).Delete(&VideoHealthRequest{}).Error
}

type VideoReliabilityStats struct {
	Supported               bool                           `json:"supported"`
	Reason                  string                         `json:"reason,omitempty"`
	AsOf                    int64                          `json:"as_of"`
	Attempts                videosched.ReliabilityEvidence `json:"attempts"`
	GenerationSuccess       VideoAuditRate                 `json:"generation_success"`
	ChannelCompletion       VideoAuditRate                 `json:"channel_completion"`
	RequestCompletion       VideoAuditRate                 `json:"request_completion"`
	LimitedShare            VideoAuditRate                 `json:"limited_share"`
	Requests                int64                          `json:"requests"`
	RequestPending          int64                          `json:"request_pending"`
	RequestUnknown          int64                          `json:"request_unknown"`
	RequestMissing          int64                          `json:"request_missing"`
	RequestUser             int64                          `json:"request_user"`
	RequestCancelled        int64                          `json:"request_cancelled"`
	CollectionWriteFailures int64                          `json:"collection_write_failures"`
}

// Health denominators remain separate from the original operational audit.
// Unsupported old/spec filters must not silently broaden the health query.
func GetVideoReliabilityStats(ctx context.Context, f VideoScheduleAuditFilter) (VideoReliabilityStats, error) {
	result := VideoReliabilityStats{AsOf: time.Now().UnixMilli()}
	if err := f.Validate(time.Now()); err != nil {
		return result, err
	}
	if f.Resolution != "" || f.Seconds != nil || f.ReferenceVideo != nil || f.ReferenceImage != nil || f.ReferenceAudio != nil || f.Version != "" || f.Outcome != "" {
		result.Reason = "unsupported_health_filter"
		return result, nil
	}
	query := DB.WithContext(ctx).Where("started_at >= ? AND started_at <= ?", f.Start/1000, f.End/1000)
	for _, field := range []struct {
		name  string
		value string
	}{{"model_name", f.Model}, {"mode", f.Mode}, {"actual_group", f.Group}, {"request_id", f.RequestID}} {
		if field.value != "" {
			query = query.Where(field.name+" = ?", field.value)
		}
	}
	if f.Channel > 0 {
		query = query.Where("channel_id = ?", f.Channel)
	}
	e, err := AggregateVideoHealthEvidence(query.Session(&gorm.Session{}))
	if err != nil {
		return result, err
	}
	e.Version, e.Source, e.BatchStart, e.BatchEnd, e.WindowSeconds, e.AsOf = 1, "window", f.Start/1000, f.End/1000, max(1, int((f.End-f.Start)/1000)), time.Now().Unix()
	result.Attempts = e
	result.GenerationSuccess = videoAuditRate(e.Succeeded, e.GenerationSamples())
	result.ChannelCompletion = videoAuditRate(e.Succeeded, e.OverallSamples())
	var requests struct{ Total, Success, Failure, Pending, Unknown, Missing, User, Cancelled int64 }
	err = query.Session(&gorm.Session{}).Model(&VideoHealthRequest{}).Select(`COUNT(*) AS total,
COALESCE(SUM(CASE WHEN outcome='success' THEN 1 ELSE 0 END),0) AS success,
COALESCE(SUM(CASE WHEN outcome='failure' THEN 1 ELSE 0 END),0) AS failure,
COALESCE(SUM(CASE WHEN outcome='pending' THEN 1 ELSE 0 END),0) AS pending,
COALESCE(SUM(CASE WHEN outcome='unknown' THEN 1 ELSE 0 END),0) AS unknown,
COALESCE(SUM(CASE WHEN missing=? THEN 1 ELSE 0 END),0) AS missing,
COALESCE(SUM(CASE WHEN outcome='user' THEN 1 ELSE 0 END),0) AS user,
COALESCE(SUM(CASE WHEN outcome='cancelled' THEN 1 ELSE 0 END),0) AS cancelled`, true).Scan(&requests).Error
	if err != nil {
		return result, err
	}
	result.Requests, result.RequestPending, result.RequestUnknown, result.RequestMissing, result.RequestUser, result.RequestCancelled = requests.Total, requests.Pending, requests.Unknown, requests.Missing, requests.User, requests.Cancelled
	result.RequestCompletion = videoAuditRate(requests.Success, requests.Success+requests.Failure)
	var limited int64
	if err := query.Session(&gorm.Session{}).Model(&VideoHealthAttempt{}).Where("flow IN ?", []string{"explore", "revalidate", "recover"}).Count(&limited).Error; err != nil {
		return result, err
	}
	result.LimitedShare = videoAuditRate(limited, e.Submitted)
	result.Supported = requests.Total > 0 || e.Submitted > 0
	if !result.Supported {
		result.Reason = "no_health_facts"
	}
	return result, nil
}
