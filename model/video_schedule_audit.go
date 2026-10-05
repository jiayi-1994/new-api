package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const VideoScheduleAuditVersion = "1"

// VideoAuditSnapshot stores bounded JSON as text in all three databases.
// MySQL TEXT is only 64 KiB; the decision limit is 256 KiB.
type VideoAuditSnapshot string

func (VideoAuditSnapshot) GormDataType() string { return "string" }

func (VideoAuditSnapshot) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "MEDIUMTEXT"
	}
	return "TEXT"
}

// VideoScheduleRun contains one request's immutable submission summary and its
// separately updated terminal outcome. All timestamps use Unix milliseconds.
// These operational records live only in DB, never in LOG_DB.
type VideoScheduleRun struct {
	ID                 int64              `json:"id" gorm:"primaryKey;index:idx_vs_run_time,priority:2"`
	RequestID          string             `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	StartedAt          int64              `json:"started_at" gorm:"index:idx_vs_run_time,priority:1;index:idx_vs_run_model_time,priority:2;index:idx_vs_run_channel_time,priority:2"`
	EndedAt            int64              `json:"ended_at" gorm:"index"`
	Mode               string             `json:"mode" gorm:"type:varchar(16)"`
	SchedulerVersion   string             `json:"scheduler_version" gorm:"type:varchar(64)"`
	ConfigVersion      string             `json:"config_version" gorm:"type:varchar(64)"`
	ModelName          string             `json:"model_name" gorm:"type:varchar(191);index:idx_vs_run_model_time,priority:1"`
	RequestGroup       string             `json:"request_group" gorm:"type:varchar(191)"`
	ActualGroup        string             `json:"actual_group" gorm:"type:varchar(191)"`
	SelectedChannel    int                `json:"selected_channel" gorm:"index:idx_vs_run_channel_time,priority:1"`
	Platform           string             `json:"platform" gorm:"type:varchar(64)"`
	TaskPK             *int64             `json:"task_pk" gorm:"index;index:idx_vs_run_pending,priority:2"`
	TaskID             string             `json:"task_id" gorm:"type:varchar(191)"`
	SubmitAttempts     int                `json:"submit_attempts"`
	SubmitAccepted     int                `json:"submit_accepted"`
	SubmitRejected     int                `json:"submit_rejected"`
	SubmitUnknown      int                `json:"submit_unknown"`
	SubmitCancelled    int                `json:"submit_cancelled"`
	SubmitLocal        int                `json:"submit_local"`
	HealthIgnored      int                `json:"health_ignored"`
	RequestOutcome     string             `json:"request_outcome" gorm:"type:varchar(32)"`
	AssemblyError      string             `json:"assembly_error" gorm:"type:varchar(64)"`
	TaskStatus         string             `json:"task_status" gorm:"type:varchar(24)"`
	TerminalClass      string             `json:"terminal_class" gorm:"type:varchar(24)"`
	TerminalHealth     string             `json:"terminal_health" gorm:"type:varchar(24)"`
	TimedOut           bool               `json:"timed_out"`
	TerminalObservedAt *int64             `json:"terminal_observed_at" gorm:"index:idx_vs_run_pending,priority:1"`
	DurationMS         *int64             `json:"duration_ms"`
	CostUSD            *float64           `json:"cost_usd"`
	OutputSeconds      *float64           `json:"output_seconds"`
	Resolution         string             `json:"resolution" gorm:"type:varchar(64)"`
	ReferenceVideo     *int               `json:"reference_video"`
	ReferenceImage     *int               `json:"reference_image"`
	ReferenceAudio     *int               `json:"reference_audio"`
	SnapshotComplete   bool               `json:"snapshot_complete"`
	DataIssue          string             `json:"data_issue" gorm:"type:varchar(128)"`
	AttemptSummary     VideoAuditSnapshot `json:"-"`
}

// VideoScheduleDecision preserves the input and board as separate TEXT JSON
// documents: a historical board must remain readable across algorithm changes.
type VideoScheduleDecision struct {
	ID               int64              `json:"id" gorm:"primaryKey"`
	RequestID        string             `json:"request_id" gorm:"type:varchar(64);uniqueIndex:idx_vs_decision_sequence,priority:1;index:idx_vs_decision_attempt,priority:1"`
	SelectionSeq     int                `json:"selection_seq" gorm:"uniqueIndex:idx_vs_decision_sequence,priority:2"`
	AttemptSeq       int                `json:"attempt_seq" gorm:"index:idx_vs_decision_attempt,priority:2"`
	SelectedAt       int64              `json:"selected_at"`
	ActualGroup      string             `json:"actual_group" gorm:"type:varchar(191)"`
	Recommended      int                `json:"recommended"`
	Selected         int                `json:"selected"`
	ChoiceKind       string             `json:"choice_kind" gorm:"type:varchar(24)"`
	SelectionReason  string             `json:"selection_reason,omitempty" gorm:"type:varchar(32)"`
	AffinityHit      bool               `json:"affinity_hit"`
	Admission        string             `json:"admission" gorm:"type:varchar(32)"`
	SubmitOutcome    string             `json:"submit_outcome" gorm:"type:varchar(32)"`
	ErrorSource      string             `json:"error_source" gorm:"type:varchar(24)"`
	StatusCode       int                `json:"status_code"`
	HealthOutcome    string             `json:"health_outcome" gorm:"type:varchar(24)"`
	CandidateCount   int                `json:"candidate_count"`
	SchemaVersion    string             `json:"schema_version" gorm:"type:varchar(16)"`
	SchedulerVersion string             `json:"scheduler_version" gorm:"type:varchar(64)"`
	BuildVersion     string             `json:"build_version" gorm:"type:varchar(128)"`
	ConfigVersion    string             `json:"config_version" gorm:"type:varchar(64)"`
	Fingerprint      string             `json:"fingerprint" gorm:"type:varchar(64)"`
	SnapshotComplete bool               `json:"snapshot_complete"`
	InputJSON        VideoAuditSnapshot `json:"input_json"`
	BoardJSON        VideoAuditSnapshot `json:"board_json"`
	PluginsJSON      VideoAuditSnapshot `json:"plugins_json"`
	ExclusionsJSON   VideoAuditSnapshot `json:"exclusions_json"`
	ShadowComparable bool               `json:"shadow_comparable"`
	ShadowDifferent  bool               `json:"shadow_different"`
	ShadowCostDelta  *float64           `json:"shadow_cost_delta"`
}

type VideoScheduleAudit struct {
	Run       VideoScheduleRun        `json:"run"`
	Decisions []VideoScheduleDecision `json:"decisions"`
}

// InsertVideoScheduleAudit commits one completed request atomically. A retry
// cannot replace a historical decision or a terminal update already received.
func InsertVideoScheduleAudit(ctx context.Context, audit *VideoScheduleAudit) error {
	if audit.Run.RequestID == "" || len(audit.Run.RequestID) > 64 {
		return errors.New("invalid scheduling audit request ID")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		run := audit.Run
		run.ID = 0
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&run)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		if len(audit.Decisions) == 0 {
			return nil
		}
		decisions := append([]VideoScheduleDecision(nil), audit.Decisions...)
		for i := range decisions {
			decisions[i].ID = 0
			decisions[i].RequestID = run.RequestID
		}
		return tx.CreateInBatches(&decisions, 50).Error
	})
}

// CompleteVideoScheduleAudit never inserts. A terminal observation preceding
// the request batch is repaired by the indexed reconciliation pass.
type VideoAuditTerminal struct {
	Status     string
	Class      string
	Health     string
	TimedOut   bool
	ObservedAt int64
}

func CompleteVideoScheduleAudit(ctx context.Context, requestID string, terminal VideoAuditTerminal) error {
	if terminal.Status != TaskStatusSuccess && terminal.Status != TaskStatusFailure {
		return nil
	}
	return DB.WithContext(ctx).Model(&VideoScheduleRun{}).
		Where("request_id = ? AND terminal_observed_at IS NULL", requestID).
		Updates(map[string]any{
			"task_status": terminal.Status, "terminal_class": terminal.Class, "terminal_health": terminal.Health, "timed_out": terminal.TimedOut, "terminal_observed_at": terminal.ObservedAt,
			"duration_ms": gorm.Expr("CASE WHEN started_at <= ? THEN ? - started_at ELSE NULL END", terminal.ObservedAt, terminal.ObservedAt),
		}).Error
}

type VideoScheduleAuditFilter struct {
	Start          int64    `json:"start"`
	End            int64    `json:"end"`
	Model          string   `json:"model"`
	Mode           string   `json:"mode"`
	Channel        int      `json:"channel"`
	Group          string   `json:"group"`
	Version        string   `json:"version"`
	Outcome        string   `json:"outcome"`
	Resolution     string   `json:"resolution"`
	Seconds        *float64 `json:"seconds"`
	ReferenceVideo *int     `json:"reference_video"`
	ReferenceImage *int     `json:"reference_image"`
	ReferenceAudio *int     `json:"reference_audio"`
	RequestID      string   `json:"request_id"`
	Page           int      `json:"page"`
	PageSize       int      `json:"page_size"`
}

func (f *VideoScheduleAuditFilter) Validate(now time.Time) error {
	if f.End == 0 {
		f.End = now.UnixMilli()
	}
	if f.Start == 0 {
		f.Start = f.End - int64(24*time.Hour/time.Millisecond)
	}
	if f.Start < 0 || f.End <= f.Start || f.End-f.Start > int64(31*24*time.Hour/time.Millisecond) {
		return errors.New("scheduling audit time range must be within 31 days")
	}
	if f.Page == 0 {
		f.Page = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 25
	}
	if f.Page < 1 || f.Page > 1000000 || f.PageSize < 1 || f.PageSize > 100 || f.Channel < 0 {
		return errors.New("invalid scheduling audit pagination or channel")
	}
	if f.Mode != "" && f.Mode != "on" && f.Mode != "shadow" {
		return errors.New("invalid scheduling audit mode")
	}
	if !slices.Contains([]string{"", "success", "failure", "pending", "cancelled", "no_candidate", "rejected", "local_failure", "internal_failure", "outcome_unknown", "persistence_failure", "submitted"}, f.Outcome) {
		return errors.New("invalid scheduling audit outcome")
	}
	if f.Seconds != nil && !(*f.Seconds >= 0 && *f.Seconds <= 86400) {
		return errors.New("invalid output seconds")
	}
	for _, n := range []*int{f.ReferenceVideo, f.ReferenceImage, f.ReferenceAudio} {
		if n != nil && (*n < 0 || *n > 1000000) {
			return errors.New("invalid reference count")
		}
	}
	for _, value := range []string{f.Model, f.Group, f.Version, f.Outcome, f.Resolution, f.RequestID} {
		if len(value) > 191 {
			return errors.New("scheduling audit filter is too long")
		}
	}
	return nil
}

func (f VideoScheduleAuditFilter) query(ctx context.Context) *gorm.DB {
	query := DB.WithContext(ctx).Model(&VideoScheduleRun{}).Where("started_at >= ? AND started_at < ?", f.Start, f.End)
	for column, value := range map[string]string{"model_name": f.Model, "mode": f.Mode, "actual_group": f.Group, "scheduler_version": f.Version, "resolution": f.Resolution, "request_id": f.RequestID} {
		if value != "" {
			query = query.Where(column+" = ?", value)
		}
	}
	if f.Channel != 0 {
		query = query.Where("selected_channel = ?", f.Channel)
	}
	if f.Seconds != nil {
		query = query.Where("output_seconds = ?", *f.Seconds)
	}
	for column, n := range map[string]*int{"reference_video": f.ReferenceVideo, "reference_image": f.ReferenceImage, "reference_audio": f.ReferenceAudio} {
		if n != nil {
			query = query.Where(column+" = ?", *n)
		}
	}
	switch f.Outcome {
	case "success":
		query = query.Where("task_status = ?", TaskStatusSuccess)
	case "failure":
		query = query.Where("(task_status = ? AND terminal_class <> ?) OR (task_pk IS NULL AND request_outcome IN ?)", TaskStatusFailure, "cancelled", []string{"no_candidate", "rejected", "local_failure", "internal_failure"})
	case "pending":
		query = query.Where("task_pk IS NOT NULL AND terminal_observed_at IS NULL")
	case "cancelled":
		query = query.Where("request_outcome = ? OR terminal_class = ?", "cancelled", "cancelled")
	case "":
	default:
		query = query.Where("request_outcome = ?", f.Outcome)
	}
	return query
}

func ListVideoScheduleAudits(ctx context.Context, filter VideoScheduleAuditFilter) ([]VideoScheduleRun, int64, error) {
	var total int64
	if err := filter.Validate(time.Now()); err != nil {
		return nil, 0, err
	}
	if err := filter.query(ctx).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	runs := []VideoScheduleRun{}
	err := filter.query(ctx).Omit("attempt_summary").Order("started_at DESC, id DESC").Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&runs).Error
	return runs, total, err
}

func GetVideoScheduleAudit(ctx context.Context, requestID string) (*VideoScheduleAudit, error) {
	audit := &VideoScheduleAudit{Decisions: []VideoScheduleDecision{}}
	if err := DB.WithContext(ctx).Where("request_id = ?", requestID).First(&audit.Run).Error; err != nil {
		return nil, err
	}
	err := DB.WithContext(ctx).Where("request_id = ?", requestID).Order("selection_seq ASC").Find(&audit.Decisions).Error
	return audit, err
}

// ReconcileVideoScheduleAudits scans the indexed audit rows, then fetches only
// terminal columns of their tasks by primary key. It never scans private_data.
// A missed live attribution stays unknown rather than guessing from host text.
func ReconcileVideoScheduleAudits(ctx context.Context, now time.Time) (int, error) {
	var after int64
	repaired := 0
	for {
		var runs []VideoScheduleRun
		err := DB.WithContext(ctx).Select("id", "request_id", "task_pk").Where("id > ? AND task_pk IS NOT NULL AND terminal_observed_at IS NULL", after).Order("id ASC").Limit(100).Find(&runs).Error
		if err != nil || len(runs) == 0 {
			return repaired, err
		}
		ids := make([]int64, 0, len(runs))
		for _, run := range runs {
			ids = append(ids, *run.TaskPK)
		}
		var tasks []Task
		if err := DB.WithContext(ctx).Select("id", "status").Where("id IN ? AND status IN ?", ids, []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).Find(&tasks).Error; err != nil {
			return repaired, err
		}
		byID := make(map[int64]TaskStatus, len(tasks))
		for _, task := range tasks {
			byID[task.ID] = task.Status
		}
		for _, run := range runs {
			status, ok := byID[*run.TaskPK]
			if !ok {
				continue
			}
			attribution := "unknown"
			if status == TaskStatusSuccess {
				attribution = "success"
			}
			if err := CompleteVideoScheduleAudit(ctx, run.RequestID, VideoAuditTerminal{Status: string(status), Class: attribution, Health: attribution, ObservedAt: now.UnixMilli()}); err != nil {
				return repaired, err
			}
			repaired++
		}
		after = runs[len(runs)-1].ID
	}
}

// DeleteExpiredVideoScheduleAudits keeps pending tasks and removes complete
// request/detail batches atomically. A later terminal UPDATE cannot revive them.
func DeleteExpiredVideoScheduleAudits(ctx context.Context, cutoff int64) (int64, error) {
	var deleted int64
	for {
		var ids []string
		err := DB.WithContext(ctx).Model(&VideoScheduleRun{}).Where("ended_at > 0 AND ended_at < ? AND (task_pk IS NULL OR (terminal_observed_at IS NOT NULL AND terminal_observed_at < ?))", cutoff, cutoff).Order("id ASC").Limit(100).Pluck("request_id", &ids).Error
		if err != nil || len(ids) == 0 {
			return deleted, err
		}
		err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("request_id IN ?", ids).Delete(&VideoScheduleDecision{}).Error; err != nil {
				return err
			}
			return tx.Where("request_id IN ?", ids).Delete(&VideoScheduleRun{}).Error
		})
		if err != nil {
			return deleted, err
		}
		deleted += int64(len(ids))
	}
}

// VideoScheduleAuditAttempts decodes only the safe host-authored attempt facts.
func (r *VideoScheduleRun) VideoScheduleAuditAttempts() ([]map[string]any, error) {
	var attempts []map[string]any
	if r.AttemptSummary == "" {
		return []map[string]any{}, nil
	}
	if err := common.UnmarshalJsonStr(string(r.AttemptSummary), &attempts); err != nil {
		return nil, fmt.Errorf("invalid scheduling attempt summary: %w", err)
	}
	return attempts, nil
}

type VideoAuditRate struct {
	Numerator   int64    `json:"numerator"`
	Denominator int64    `json:"denominator"`
	Value       *float64 `json:"value"`
}

func videoAuditRate(n, d int64) VideoAuditRate {
	rate := VideoAuditRate{Numerator: n, Denominator: d}
	if d > 0 {
		value := float64(n) / float64(d)
		rate.Value = &value
	}
	return rate
}

type VideoScheduleAuditStats struct {
	Reliability         *VideoReliabilityStats `json:"reliability,omitempty"`
	AsOf                int64                  `json:"as_of"`
	Total               int64                  `json:"total"`
	Pending             int64                  `json:"pending"`
	Unknown             int64                  `json:"unknown"`
	Cancelled           int64                  `json:"cancelled"`
	Missing             int64                  `json:"missing"`
	SubmitUnknown       int64                  `json:"submit_unknown"`
	SubmitCancelled     int64                  `json:"submit_cancelled"`
	SubmitLocal         int64                  `json:"submit_local"`
	HealthIgnored       int64                  `json:"health_ignored"`
	HealthUnknown       int64                  `json:"health_unknown"`
	Timeouts            int64                  `json:"timeouts"`
	MaturityWaitMS      int64                  `json:"maturity_wait_ms"`
	RequestSuccess      VideoAuditRate         `json:"request_success"`
	SubmitAcceptance    VideoAuditRate         `json:"submit_acceptance"`
	GenerationSuccess   VideoAuditRate         `json:"generation_success"`
	HealthSuccess       VideoAuditRate         `json:"health_success"`
	Retry               VideoAuditRate         `json:"retry"`
	NoCandidate         VideoAuditRate         `json:"no_candidate"`
	P50MS               *int64                 `json:"p50_ms"`
	P95MS               *int64                 `json:"p95_ms"`
	DurationSamples     int                    `json:"duration_samples"`
	OldestPendingMS     *int64                 `json:"oldest_pending_ms"`
	CostSamples         int64                  `json:"cost_samples"`
	CostMissing         int64                  `json:"cost_missing"`
	MeanCostUSD         *float64               `json:"mean_cost_usd"`
	Selections          int64                  `json:"selections"`
	Candidates          int64                  `json:"candidates"`
	ShadowDifference    VideoAuditRate         `json:"shadow_difference"`
	ShadowCostSamples   int64                  `json:"shadow_cost_samples"`
	MeanShadowCostDelta *float64               `json:"mean_shadow_cost_delta"`
	Exclusions          map[string]int64       `json:"exclusions"`
}

// Exact aggregates use scalar columns only. The bounded Go percentile pass
// reads only successful durations; no input/board/attempt TEXT is selected.
func GetVideoScheduleAuditStats(ctx context.Context, filter VideoScheduleAuditFilter) (*VideoScheduleAuditStats, error) {
	if err := filter.Validate(time.Now()); err != nil {
		return nil, err
	}
	var count int64
	if err := filter.query(ctx).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 100000 {
		return nil, errors.New("more than 100,000 scheduling requests; narrow the time range")
	}
	var sums struct {
		Total, Pending, Unknown, Cancelled, Missing, Success, TaskFailure, HealthOK, HealthFailure, HealthUnknown, Timeouts, RequestFailure, Ended, Retry, NoCandidate int64
		Accepted, Rejected, SubmitUnknown, SubmitCancelled, SubmitLocal, HealthIgnored, CostSamples                                                                    int64
		MeanCostUSD                                                                                                                                                    *float64
		OldestPending                                                                                                                                                  *int64
	}
	selection := `COUNT(*) AS total,
	COALESCE(SUM(CASE WHEN task_pk IS NOT NULL AND terminal_observed_at IS NULL THEN 1 ELSE 0 END),0) AS pending,
	COALESCE(SUM(CASE WHEN request_outcome IN ('outcome_unknown','persistence_failure') THEN 1 ELSE 0 END),0) AS unknown,
	COALESCE(SUM(CASE WHEN request_outcome = 'cancelled' OR terminal_class = 'cancelled' THEN 1 ELSE 0 END),0) AS cancelled,
	COALESCE(SUM(CASE WHEN snapshot_complete = ? OR data_issue <> '' OR assembly_error <> '' THEN 1 ELSE 0 END),0) AS missing,
	COALESCE(SUM(CASE WHEN task_status = 'SUCCESS' THEN 1 ELSE 0 END),0) AS success,
	COALESCE(SUM(CASE WHEN task_status = 'FAILURE' AND terminal_class <> 'cancelled' THEN 1 ELSE 0 END),0) AS task_failure,
	COALESCE(SUM(CASE WHEN terminal_health = 'success' THEN 1 ELSE 0 END),0) AS health_ok,
	COALESCE(SUM(CASE WHEN terminal_health = 'fail' THEN 1 ELSE 0 END),0) AS health_failure,
	COALESCE(SUM(CASE WHEN terminal_observed_at IS NOT NULL AND terminal_health NOT IN ('success','fail','ignored') THEN 1 ELSE 0 END),0) AS health_unknown,
	COALESCE(SUM(CASE WHEN timed_out = ? THEN 1 ELSE 0 END),0) AS timeouts,
	COALESCE(SUM(CASE WHEN task_pk IS NULL AND request_outcome IN ('no_candidate','rejected','local_failure','internal_failure') THEN 1 ELSE 0 END),0) AS request_failure,
	COALESCE(SUM(CASE WHEN ended_at > 0 THEN 1 ELSE 0 END),0) AS ended,
	COALESCE(SUM(CASE WHEN ended_at > 0 AND submit_attempts > 1 THEN 1 ELSE 0 END),0) AS retry,
	COALESCE(SUM(CASE WHEN ended_at > 0 AND request_outcome = 'no_candidate' THEN 1 ELSE 0 END),0) AS no_candidate,
	COALESCE(SUM(submit_accepted),0) AS accepted, COALESCE(SUM(submit_rejected),0) AS rejected,
	COALESCE(SUM(submit_unknown),0) AS submit_unknown, COALESCE(SUM(submit_cancelled),0) AS submit_cancelled,
	COALESCE(SUM(submit_local),0) AS submit_local, COALESCE(SUM(health_ignored),0) AS health_ignored,
	COUNT(cost_usd) AS cost_samples, AVG(cost_usd) AS mean_cost_usd,
	MIN(CASE WHEN task_pk IS NOT NULL AND terminal_observed_at IS NULL THEN started_at ELSE NULL END) AS oldest_pending`
	if err := filter.query(ctx).Select(selection, false, true).Scan(&sums).Error; err != nil {
		return nil, err
	}
	// Detect growth across the count and aggregate queries instead of returning
	// a truncated result as if it covered the whole cohort.
	if sums.Total > 100000 {
		return nil, errors.New("more than 100,000 scheduling requests; narrow the time range")
	}
	stats := &VideoScheduleAuditStats{AsOf: time.Now().UnixMilli(), Total: sums.Total, Pending: sums.Pending, Unknown: sums.Unknown, Cancelled: sums.Cancelled, Missing: sums.Missing,
		SubmitUnknown: sums.SubmitUnknown, SubmitCancelled: sums.SubmitCancelled, SubmitLocal: sums.SubmitLocal, HealthIgnored: sums.HealthIgnored,
		RequestSuccess: videoAuditRate(sums.Success, sums.Success+sums.TaskFailure+sums.RequestFailure), SubmitAcceptance: videoAuditRate(sums.Accepted, sums.Accepted+sums.Rejected),
		GenerationSuccess: videoAuditRate(sums.Success, sums.Success+sums.TaskFailure), HealthSuccess: videoAuditRate(sums.HealthOK, sums.HealthOK+sums.HealthFailure), HealthUnknown: sums.HealthUnknown, Timeouts: sums.Timeouts,
		Retry: videoAuditRate(sums.Retry, sums.Ended), NoCandidate: videoAuditRate(sums.NoCandidate, sums.Ended), CostSamples: sums.CostSamples, CostMissing: sums.Total - sums.CostSamples, MeanCostUSD: sums.MeanCostUSD, Exclusions: map[string]int64{}}
	if sums.OldestPending != nil {
		age := max(int64(0), stats.AsOf-*sums.OldestPending)
		stats.OldestPendingMS = &age
	}
	var durations []int64
	if err := filter.query(ctx).Where("task_status = ? AND duration_ms >= 0", TaskStatusSuccess).Limit(100001).Pluck("duration_ms", &durations).Error; err != nil {
		return nil, err
	}
	if len(durations) > 100000 {
		return nil, errors.New("more than 100,000 duration samples; narrow the time range")
	}
	slices.Sort(durations)
	stats.DurationSamples = len(durations)
	if len(durations) > 0 {
		p50, p95 := durations[int(math.Ceil(float64(len(durations))*.5))-1], durations[int(math.Ceil(float64(len(durations))*.95))-1]
		stats.P50MS, stats.P95MS = &p50, &p95
	}
	var decisions struct {
		Selections, Candidates, Comparable, Different, CostSamples int64
		CostDelta                                                  *float64
	}
	dq := DB.WithContext(ctx).Model(&VideoScheduleDecision{}).Where("request_id IN (?)", filter.query(ctx).Select("request_id"))
	if err := dq.Select(`COUNT(*) AS selections, COALESCE(SUM(candidate_count),0) AS candidates,
	COALESCE(SUM(CASE WHEN shadow_comparable = ? THEN 1 ELSE 0 END),0) AS comparable,
	COALESCE(SUM(CASE WHEN shadow_different = ? THEN 1 ELSE 0 END),0) AS different,
	COUNT(shadow_cost_delta) AS cost_samples, AVG(shadow_cost_delta) AS cost_delta`, true, true).Scan(&decisions).Error; err != nil {
		return nil, err
	}
	stats.Selections, stats.Candidates, stats.ShadowDifference, stats.ShadowCostSamples, stats.MeanShadowCostDelta = decisions.Selections, decisions.Candidates, videoAuditRate(decisions.Different, decisions.Comparable), decisions.CostSamples, decisions.CostDelta
	rows, err := DB.WithContext(ctx).Model(&VideoScheduleDecision{}).Where("request_id IN (?)", filter.query(ctx).Select("request_id")).Select("exclusions_json").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if raw == "" {
			continue
		}
		var counts map[string]int64
		if err := common.UnmarshalJsonStr(raw, &counts); err != nil {
			return nil, err
		}
		for key, n := range counts {
			stats.Exclusions[key] += n
		}
	}
	return stats, rows.Err()
}

// VideoAuditChannelStat summarizes the requests one channel served for one
// model. Failures exclude cancellations, matching GenerationSuccess; the mean
// duration covers successful tasks only, matching the percentiles.
type VideoAuditChannelStat struct {
	SelectedChannel int      `json:"channel"`
	ModelName       string   `json:"model"`
	Requests        int64    `json:"requests"`
	Success         int64    `json:"success"`
	Failure         int64    `json:"failure"`
	MeanCostUSD     *float64 `json:"mean_cost_usd"`
	MeanDurationMS  *float64 `json:"mean_duration_ms"`
}

// GetVideoScheduleChannelStats groups the filtered cohort by its final
// channel. Requests that never reached a channel are left out.
func GetVideoScheduleChannelStats(ctx context.Context, filter VideoScheduleAuditFilter) ([]VideoAuditChannelStat, error) {
	if err := filter.Validate(time.Now()); err != nil {
		return nil, err
	}
	stats := []VideoAuditChannelStat{}
	err := filter.query(ctx).Where("selected_channel > 0").Select(`selected_channel, model_name, COUNT(*) AS requests,
	COALESCE(SUM(CASE WHEN task_status = 'SUCCESS' THEN 1 ELSE 0 END),0) AS success,
	COALESCE(SUM(CASE WHEN task_status = 'FAILURE' AND terminal_class <> 'cancelled' THEN 1 ELSE 0 END),0) AS failure,
	AVG(cost_usd) AS mean_cost_usd,
	AVG(CASE WHEN task_status = 'SUCCESS' AND duration_ms >= 0 THEN duration_ms ELSE NULL END) AS mean_duration_ms`).
		Group("selected_channel, model_name").Order("requests DESC, selected_channel ASC, model_name ASC").Scan(&stats).Error
	return stats, err
}

type VideoAuditExportCursor struct {
	Version       int    `json:"v"`
	FilterHash    string `json:"filter"`
	Start         int64  `json:"start"`
	End           int64  `json:"end"`
	ThroughID     int64  `json:"through_id"`
	StartedAt     int64  `json:"started_at"`
	ID            int64  `json:"id"`
	NextSelection int    `json:"next_selection"` // -1: finished request, 0: summary, >=1: next decision
}

// ExportVideoScheduleAudits uses a high-water mark for cohort membership and
// resumes within a request. Terminal fields remain live observations, as the
// versioned header states; this is not a database snapshot held across downloads.
func ExportVideoScheduleAudits(ctx context.Context, filter VideoScheduleAuditFilter, token string) ([]byte, error) {
	return exportVideoScheduleAudits(ctx, filter, token, 10*1024*1024)
}

func exportVideoScheduleAudits(ctx context.Context, filter VideoScheduleAuditFilter, token string, byteLimit int) ([]byte, error) {
	cursor := VideoAuditExportCursor{Version: 1, NextSelection: -1}
	if token != "" {
		if len(token) > 2048 {
			return nil, errors.New("invalid export continuation")
		}
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return nil, errors.New("invalid export continuation")
		}
		if err := common.Unmarshal(raw, &cursor); err != nil || cursor.Version != 1 || cursor.ID < 0 || cursor.ThroughID < cursor.ID || cursor.NextSelection < -1 || cursor.NextSelection > 1000000 {
			return nil, errors.New("invalid export continuation")
		}
		if filter.Start == 0 {
			filter.Start = cursor.Start
		}
		if filter.End == 0 {
			filter.End = cursor.End
		}
	}
	if err := filter.Validate(time.Now()); err != nil {
		return nil, err
	}
	filter.Page, filter.PageSize = 0, 0
	encoded, err := common.Marshal(filter)
	if err != nil {
		return nil, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(encoded))
	if token != "" && cursor.FilterHash != fingerprint {
		return nil, errors.New("export continuation does not match filters")
	}
	if token == "" {
		if err := filter.query(ctx).Select("COALESCE(MAX(id),0)").Scan(&cursor.ThroughID).Error; err != nil {
			return nil, err
		}
		cursor.FilterHash, cursor.Start, cursor.End = fingerprint, filter.Start, filter.End
	}
	var output bytes.Buffer
	header, _ := common.Marshal(map[string]any{"type": "header", "schema_version": 1, "as_of": time.Now().UnixMilli(), "filter": filter, "through_id": cursor.ThroughID, "terminal_results": "live", "coverage": "unknown"})
	output.Write(header)
	output.WriteByte('\n')
	requests, partial := 0, false
	for {
		var run VideoScheduleRun
		q := filter.query(ctx).Where("id <= ?", cursor.ThroughID)
		if cursor.NextSelection >= 0 {
			q = q.Where("id = ? AND started_at = ?", cursor.ID, cursor.StartedAt)
		} else {
			q = q.Where("started_at > ? OR (started_at = ? AND id > ?)", cursor.StartedAt, cursor.StartedAt, cursor.ID)
		}
		err := q.Order("started_at ASC, id ASC").First(&run).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if cursor.NextSelection >= 0 {
				// Retention or a live terminal filter may remove the partially
				// exported request. Continue with later requests in this cohort.
				cursor.NextSelection = -1
				continue
			}
			break
		}
		if err != nil {
			return nil, err
		}
		if cursor.NextSelection < 0 {
			if requests >= 1000 {
				partial = true
				break
			}
			cursor.StartedAt, cursor.ID, cursor.NextSelection = run.StartedAt, run.ID, 0
		} else if cursor.NextSelection > 0 {
			requests++ // a resumed request also counts against this segment's cap
		}
		if cursor.NextSelection == 0 {
			attempts, err := run.VideoScheduleAuditAttempts()
			if err != nil {
				return nil, err
			}
			line, err := common.Marshal(map[string]any{"type": "request", "run": run, "attempts": attempts})
			if err != nil {
				return nil, err
			}
			if output.Len()+len(line)+1+2048 > byteLimit {
				partial = true
				break
			}
			output.Write(line)
			output.WriteByte('\n')
			requests++
			cursor.NextSelection = 1
		}
		for {
			var decision VideoScheduleDecision
			err := DB.WithContext(ctx).Where("request_id = ? AND selection_seq >= ?", run.RequestID, cursor.NextSelection).Order("selection_seq ASC").First(&decision).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				cursor.NextSelection = -1
				break
			}
			if err != nil {
				return nil, err
			}
			line, err := common.Marshal(map[string]any{"type": "decision", "decision": decision})
			if err != nil {
				return nil, err
			}
			if output.Len()+len(line)+1+2048 > byteLimit {
				partial = true
				break
			}
			output.Write(line)
			output.WriteByte('\n')
			cursor.NextSelection = decision.SelectionSeq + 1
		}
		if partial {
			break
		}
	}
	continuation := ""
	if partial {
		raw, err := common.Marshal(cursor)
		if err != nil {
			return nil, err
		}
		continuation = base64.RawURLEncoding.EncodeToString(raw)
	}
	footer, _ := common.Marshal(map[string]any{"type": "footer", "partial": partial, "continuation": continuation, "requests": requests})
	output.Write(footer)
	output.WriteByte('\n')
	return output.Bytes(), nil
}
