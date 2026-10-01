package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

const (
	videoAuditContextKey  = "video_schedule_audit_state"
	videoAuditDetailLimit = 256 * 1024
	videoAuditQueueLimit  = 1024
	videoAuditByteLimit   = 32 * 1024 * 1024
)

// This state contains only host-authored facts, never an error message or a
// task/channel object. It lives in the request until one final batch is built.
type videoAuditState struct {
	Enabled        bool
	Enqueued       bool
	AssemblyError  string
	Attempts       map[int]videoAuditAttempt
	TaskPK         *int64
	TaskID         string
	Platform       string
	TaskStatus     string
	TerminalClass  string
	TerminalHealth string
	TerminalAt     *int64
}

type videoAuditAttempt struct {
	Attempt     int    `json:"attempt"`
	Channel     int    `json:"channel"`
	Group       string `json:"group"`
	Outcome     string `json:"outcome"`
	ErrorSource string `json:"error_source"`
	Status      int    `json:"status"`
	Health      string `json:"health"`
}

func freezeVideoScheduleAudit(c *gin.Context, decision VideoSchedDecision, enabled bool) {
	if decision.Takeover || decision.Shadow {
		c.Set(videoAuditContextKey, &videoAuditState{Enabled: enabled, Attempts: map[int]videoAuditAttempt{}})
	}
}

func videoScheduleAuditState(c *gin.Context) *videoAuditState {
	if c == nil {
		return nil
	}
	value, ok := c.Get(videoAuditContextKey)
	if !ok {
		return nil
	}
	state, _ := value.(*videoAuditState)
	if state == nil || !state.Enabled {
		return nil
	}
	return state
}

// captureVideoAuditSubmit supplements PolicyEvent with the two facts that its
// public error projection loses: accepted-before-insert and the unknown sentinel.
// It is called from the existing submit observer, without an event queue write.
func captureVideoAuditSubmit(c *gin.Context, channelID int, taskErr *taskdto.TaskError) {
	state := videoScheduleAuditState(c)
	if state == nil {
		return
	}
	policy := RequestPolicy(c)
	attempt := videoAuditAttempt{Attempt: policy.Attempts, Channel: channelID, Group: policy.SelectedGroup, Outcome: "accepted", Health: "success"}
	if taskErr != nil {
		attempt.Status = taskErr.StatusCode
		attempt.Outcome, attempt.ErrorSource = "rejected", "upstream"
		if taskErr.LocalError {
			attempt.Outcome, attempt.ErrorSource = "local", "local"
		}
		if errors.Is(taskErr.Error, relaycommon.ErrTaskSubmitOutcomeUnknown) {
			attempt.Outcome, attempt.ErrorSource = "outcome_unknown", "transport"
		}
		attempt.Health = "ignored"
		if videoSubmitOutcome(taskErr) == VideoOutcomeFail {
			attempt.Health = "fail"
		}
	}
	state.Attempts[attempt.Attempt] = attempt
}

func recordVideoAuditAssemblyError(c *gin.Context) {
	if state := videoScheduleAuditState(c); state != nil {
		state.AssemblyError = "candidate_snapshot_failed"
	}
}

// Only free-form diagnostic errors need redaction. Their text can originate
// in a plugin exception, model mapping, or spec validation of user input.
func videoAuditExclusion(reason string) string {
	if strings.HasPrefix(reason, "spec invalid:") {
		return "spec invalid"
	}
	if strings.HasPrefix(reason, "not schedulable:") {
		switch reason {
		case "not schedulable: no cost table", "not schedulable: model not priced", "not schedulable: no execution plugin", "not schedulable: model opt-out":
			return reason
		default:
			return "not schedulable: invalid configuration"
		}
	}
	if strings.Contains(reason, "://") || len(reason) > 160 {
		return "invalid candidate"
	}
	return reason
}

// Resolution is the only free-form request string inside the normalized spec.
// Keep short product/tier identifiers; a URL or prose is not audit metadata.
func videoAuditResolution(tier string) string {
	if len(tier) > 64 {
		return ""
	}
	for _, r := range tier {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-*+", r)) {
			return ""
		}
	}
	return tier
}

// buildVideoScheduleAudit makes a detached, credential-free batch. It never
// re-runs a plugin, estimator, health read, random draw, or scheduling decision.
func buildVideoScheduleAudit(c *gin.Context, state *videoAuditState, panicked bool) (*model.VideoScheduleAudit, int, error) {
	policy := RequestPolicy(c)
	decision := VideoSchedDecisionFrom(c)
	mode := operation_setting.VideoSchedulingModeShadow
	if decision.Takeover {
		mode = operation_setting.VideoSchedulingModeOn
	}
	run := model.VideoScheduleRun{
		RequestID: c.GetString(common.RequestIdKey), StartedAt: policy.StartedAt.UnixMilli(), EndedAt: time.Now().UnixMilli(),
		Mode: mode, SchedulerVersion: model.VideoScheduleAuditVersion, ModelName: c.GetString("resolved_task_model"),
		RequestGroup: c.GetString("group"), ActualGroup: policy.SelectedGroup, SelectedChannel: c.GetInt("channel_id"),
		TaskPK: state.TaskPK, TaskID: state.TaskID, Platform: state.Platform, TaskStatus: state.TaskStatus,
		TerminalClass: state.TerminalClass, TerminalHealth: state.TerminalHealth, TerminalObservedAt: state.TerminalAt, AssemblyError: state.AssemblyError, SnapshotComplete: true,
	}
	if run.RequestID == "" {
		return nil, 0, errors.New("missing server request ID")
	}
	if run.TerminalObservedAt != nil && *run.TerminalObservedAt >= run.StartedAt {
		duration := *run.TerminalObservedAt - run.StartedAt
		run.DurationMS = &duration
	}
	events := policy.Events()
	for _, event := range events {
		attempt, exists := state.Attempts[event.Attempt]
		switch event.Decision.Action {
		case "attempt":
			if !exists {
				state.Attempts[event.Attempt] = videoAuditAttempt{Attempt: event.Attempt, Channel: event.ChannelID, Group: event.Group, Outcome: "local", ErrorSource: "local", Health: "ignored"}
			}
		case "failure":
			if exists {
				// Source classification is safe host metadata. The error code or
				// message may be provider-controlled and is not copied.
				if attempt.Outcome != "outcome_unknown" {
					attempt.ErrorSource = event.ErrorSource
				}
				state.Attempts[event.Attempt] = attempt
			}
		case "cancelled":
			state.Attempts[event.Attempt] = videoAuditAttempt{Attempt: event.Attempt, Channel: event.ChannelID, Group: event.Group, Outcome: "cancelled", ErrorSource: "local", Health: "ignored"}
		}
	}
	keys := make([]int, 0, len(state.Attempts))
	for key := range state.Attempts {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	attempts := make([]videoAuditAttempt, 0, len(keys))
	for _, key := range keys {
		attempt := state.Attempts[key]
		attempts = append(attempts, attempt)
		if attempt.Outcome != "local" {
			run.SubmitAttempts++
		}
		switch attempt.Outcome {
		case "accepted":
			run.SubmitAccepted++
		case "rejected":
			run.SubmitRejected++
		case "outcome_unknown":
			run.SubmitUnknown++
		case "cancelled":
			run.SubmitCancelled++
		case "local":
			run.SubmitLocal++
		}
		if attempt.Health == "ignored" {
			run.HealthIgnored++
		}
	}
	attemptJSON, err := common.Marshal(attempts)
	if err != nil {
		return nil, 0, err
	}
	run.AttemptSummary = model.VideoAuditSnapshot(attemptJSON)
	run.RequestOutcome = "no_candidate"
	switch {
	case state.TaskPK != nil:
		run.RequestOutcome = "submitted"
	case run.SubmitAccepted > 0:
		run.RequestOutcome = "persistence_failure"
	case run.SubmitUnknown > 0:
		run.RequestOutcome = "outcome_unknown"
	case run.SubmitCancelled > 0 || (c.Request != nil && c.Request.Context().Err() != nil):
		run.RequestOutcome = "cancelled"
	case state.AssemblyError != "" || panicked:
		run.RequestOutcome = "internal_failure"
	case len(attempts) > 0:
		run.RequestOutcome = "rejected"
		if attempts[len(attempts)-1].Outcome == "local" {
			run.RequestOutcome = "local_failure"
		}
	}
	if panicked && state.TaskPK == nil && run.SubmitAccepted == 0 {
		run.RequestOutcome = "internal_failure"
	}
	if len(events) == 512 {
		run.DataIssue = "policy_events_truncated"
	}
	records := VideoScheduleRecords(c)
	if decision.Takeover && len(records) > 0 && state.TaskPK == nil && run.SubmitAccepted == 0 && run.SubmitUnknown == 0 && !panicked && state.AssemblyError == "" {
		last := records[len(records)-1]
		if last.AttemptSeq > policy.Attempts || last.Admission != "" {
			run.RequestOutcome = "no_candidate"
		}
	}
	if len(records) == 0 && state.AssemblyError == "" {
		run.DataIssue = "missing_decisions"
	}
	audit := &model.VideoScheduleAudit{Run: run, Decisions: make([]model.VideoScheduleDecision, 0, len(records))}
	var generation *jsplugin.RoutingGeneration
	if value, ok := c.Get(jsplugin.ContextKeyPinnedEndpoint); ok {
		pinned, _ := value.(jsplugin.PinnedEndpoint)
		generation = pinned.Generation
	} else if value, ok := c.Get(jsplugin.ContextKeyPinnedPlugin); ok {
		pinned, _ := value.(jsplugin.PinnedPlugin)
		generation = pinned.Generation
	}
	// Only the last admitted selection of an actual attempt was executed.
	used := map[int]int{}
	for _, record := range records {
		attempt, exists := state.Attempts[record.AttemptSeq]
		selected := record.Recommended
		if mode == operation_setting.VideoSchedulingModeShadow {
			selected = record.Selected
		}
		if exists && record.Admission == "" && selected == attempt.Channel && selected != 0 {
			used[record.AttemptSeq] = record.SelectionSeq
		}
	}
	bytes := 4096 + len(run.AttemptSummary)
	for _, record := range records {
		row := model.VideoScheduleDecision{
			RequestID: run.RequestID, SelectionSeq: record.SelectionSeq, AttemptSeq: record.AttemptSeq, ActualGroup: record.Group,
			Recommended: record.Recommended, ChoiceKind: "normal", AffinityHit: record.AffinityHit, Admission: record.Admission,
			Fingerprint: record.Fingerprint, CandidateCount: len(record.Candidates), SchemaVersion: "1", SchedulerVersion: model.VideoScheduleAuditVersion,
			BuildVersion: common.Version, SnapshotComplete: record.Input != nil,
		}
		if record.Probe {
			row.ChoiceKind = "probe"
		} else if record.Explore {
			row.ChoiceKind = "explore"
		}
		if used[record.AttemptSeq] == record.SelectionSeq {
			attempt := state.Attempts[record.AttemptSeq]
			row.Selected, row.SubmitOutcome, row.ErrorSource, row.StatusCode, row.HealthOutcome = attempt.Channel, attempt.Outcome, attempt.ErrorSource, attempt.Status, attempt.Health
		}
		row.ShadowComparable = mode == operation_setting.VideoSchedulingModeShadow && record.Selected != 0 && record.Recommended != 0
		row.ShadowDifferent = row.ShadowComparable && record.Selected != record.Recommended
		board := slices.Clone(record.Candidates)
		exclusions := map[string]int{}
		plugins := map[string]map[string]any{}
		var selectedCost, recommendedCost *float64
		for i := range board {
			if spec := board[i].Spec; spec != nil && videoAuditResolution(spec.Tier) != spec.Tier {
				copy := *spec
				copy.Tier = ""
				board[i].Spec, row.SnapshotComplete = &copy, false
			}
			if board[i].Excluded != "" {
				exclusions[videoAuditExclusion(board[i].Excluded)]++
			}
			if safe := videoAuditExclusion(board[i].Excluded); safe != board[i].Excluded {
				board[i].Excluded, row.SnapshotComplete = safe, false
			}
			if generation != nil && board[i].Plugin != "" {
				if plugin, ok := generation.Get(board[i].Plugin); ok {
					plugins[plugin.Meta.Key] = map[string]any{"version": plugin.Meta.Version, "generation": generation.Number, "api_version": plugin.Meta.APIVersion}
				}
			}
			if board[i].ID == record.Recommended {
				recommendedCost = board[i].CostUSD
			}
			if board[i].ID == record.Selected {
				selectedCost = board[i].CostUSD
			}
			if row.Selected == board[i].ID && record.AttemptSeq == policy.Attempts {
				audit.Run.CostUSD = board[i].CostUSD
				if spec := board[i].Spec; spec != nil {
					audit.Run.OutputSeconds, audit.Run.Resolution = spec.OutputSeconds, spec.Tier
					if spec.References != nil {
						v, i, a := spec.References["video"], spec.References["image"], spec.References["audio"]
						audit.Run.ReferenceVideo, audit.Run.ReferenceImage, audit.Run.ReferenceAudio = &v, &i, &a
					}
				}
			}
		}
		if row.ShadowComparable && selectedCost != nil && recommendedCost != nil {
			delta := *recommendedCost - *selectedCost
			row.ShadowCostDelta = &delta
		}
		if record.Input != nil {
			input := *record.Input
			input.Candidates = slices.Clone(input.Candidates)
			for i := range input.Candidates {
				if safe := videoAuditResolution(input.Candidates[i].Spec.Tier); safe != input.Candidates[i].Spec.Tier {
					input.Candidates[i].Spec.Tier = safe
					row.SnapshotComplete = false
				}
				if safe := videoAuditExclusion(input.Candidates[i].Excluded); safe != input.Candidates[i].Excluded {
					input.Candidates[i].Excluded = safe
					row.SnapshotComplete = false
				}
			}
			row.SelectedAt = input.Now.UnixMilli()
			fingerprint, sections, hashErr := VideoDecisionFingerprint(input)
			if hashErr != nil {
				row.SnapshotComplete = false
			} else {
				row.ConfigVersion = sections["settings"]
				if fingerprint != record.Fingerprint {
					row.SnapshotComplete = false
				}
			}
			inputJSON, inputErr := common.Marshal(input)
			if inputErr == nil {
				row.InputJSON = model.VideoAuditSnapshot(inputJSON)
			} else {
				row.SnapshotComplete = false
			}
		}
		boardJSON, boardErr := common.Marshal(board)
		if boardErr == nil {
			row.BoardJSON = model.VideoAuditSnapshot(boardJSON)
		} else {
			row.SnapshotComplete = false
		}
		pluginJSON, _ := common.Marshal(plugins)
		exclusionJSON, _ := common.Marshal(exclusions)
		row.PluginsJSON, row.ExclusionsJSON = model.VideoAuditSnapshot(pluginJSON), model.VideoAuditSnapshot(exclusionJSON)
		if len(row.InputJSON)+len(row.BoardJSON)+len(row.PluginsJSON)+len(row.ExclusionsJSON) > videoAuditDetailLimit {
			row.InputJSON, row.BoardJSON, row.PluginsJSON, row.ExclusionsJSON = "", "", "", ""
			row.SnapshotComplete = false
			videoAuditQueue.noteTruncated()
		}
		if !row.SnapshotComplete {
			audit.Run.SnapshotComplete, audit.Run.DataIssue = false, "incomplete_snapshot"
		}
		audit.Run.ConfigVersion = row.ConfigVersion
		bytes += 2048 + len(row.InputJSON) + len(row.BoardJSON) + len(row.PluginsJSON) + len(row.ExclusionsJSON)
		audit.Decisions = append(audit.Decisions, row)
	}
	return audit, bytes, nil
}

// EnqueueVideoScheduleAudit is independent of error/consumption log switches.
// It cannot replace an in-flight panic or a successful business response.
func EnqueueVideoScheduleAudit(c *gin.Context, panicked bool) {
	defer func() {
		if recover() != nil {
			videoAuditQueue.noteFailure("capture_panic")
		}
	}()
	state := videoScheduleAuditState(c)
	if state == nil || state.Enqueued {
		return
	}
	state.Enqueued = true
	audit, size, err := buildVideoScheduleAudit(c, state, panicked)
	// No input pointers or gin context cross the worker boundary.
	for i := range VideoScheduleRecords(c) {
		VideoScheduleRecords(c)[i].Input = nil
	}
	if err != nil {
		videoAuditQueue.noteFailure("capture_failed")
		return
	}
	videoAuditQueue.enqueue(audit, size)
}

// ObserveVideoScheduleAuditTerminal is called outside the health summary guard.
// No current setting is consulted: an enrolled request can finish after off.
func ObserveVideoScheduleAuditTerminal(task *model.Task, attribution string, outcome VideoOutcome) {
	if task.PrivateData.Execution == nil || task.PrivateData.Execution.RequestID == "" || model.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	health := "ignored"
	if outcome == VideoOutcomeSuccess {
		health = "success"
	} else if outcome == VideoOutcomeFail {
		health = "fail"
	}
	terminal := model.VideoAuditTerminal{Status: string(task.Status), Class: attribution, Health: health, TimedOut: attribution == "host" && strings.HasPrefix(task.FailReason, "任务超时"), ObservedAt: time.Now().UnixMilli()}
	if err := model.CompleteVideoScheduleAudit(ctx, task.PrivateData.Execution.RequestID, terminal); err != nil {
		videoAuditQueue.noteFailure("terminal_write_failed")
	}
}

type VideoAuditCollectionStatus struct {
	Node          string `json:"node"`
	StartedAt     int64  `json:"started_at"`
	Running       bool   `json:"running"`
	Pending       int    `json:"pending"`
	PendingBytes  int    `json:"pending_bytes"`
	OldestAt      int64  `json:"oldest_at"`
	Written       int64  `json:"written"`
	Dropped       int64  `json:"dropped"`
	WriteFailures int64  `json:"write_failures"`
	Truncated     int64  `json:"truncated"`
	LastWriteAt   int64  `json:"last_write_at"`
	FirstIssueAt  int64  `json:"first_issue_at"`
	LastIssueAt   int64  `json:"last_issue_at"`
	LastIssue     string `json:"last_issue"`
	Coverage      string `json:"coverage"` // local process counters cannot prove historical cluster completeness
}

type videoAuditBatch struct {
	audit    *model.VideoScheduleAudit
	bytes    int
	queuedAt int64
}
type videoAuditWriter struct {
	mu      sync.Mutex
	queue   []videoAuditBatch
	status  VideoAuditCollectionStatus
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc
	closing bool
}

func newVideoAuditWriter() *videoAuditWriter {
	return &videoAuditWriter{status: VideoAuditCollectionStatus{StartedAt: time.Now().UnixMilli(), Coverage: "unknown"}, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
}

var videoAuditQueue = newVideoAuditWriter()

func VideoScheduleAuditCollectionStatus() VideoAuditCollectionStatus {
	videoAuditQueue.mu.Lock()
	defer videoAuditQueue.mu.Unlock()
	status := videoAuditQueue.status
	status.Node = common.GetNodeIdentity().Name
	return status
}

func (w *videoAuditWriter) enqueue(audit *model.VideoScheduleAudit, size int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing || w.status.Pending >= videoAuditQueueLimit || size > videoAuditByteLimit-w.status.PendingBytes {
		w.status.Dropped++
		w.status.LastIssue, w.status.LastIssueAt = "queue_full_or_stopped", time.Now().UnixMilli()
		if w.status.FirstIssueAt == 0 {
			w.status.FirstIssueAt = w.status.LastIssueAt
		}
		return false
	}
	now := time.Now().UnixMilli()
	w.queue = append(w.queue, videoAuditBatch{audit, size, now})
	w.status.Pending++
	w.status.PendingBytes += size
	if w.status.OldestAt == 0 {
		w.status.OldestAt = now
	}
	if len(w.queue) >= 100 {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	return true
}

func (w *videoAuditWriter) noteFailure(reason string) {
	w.mu.Lock()
	w.status.WriteFailures++
	w.status.LastIssue, w.status.LastIssueAt = reason, time.Now().UnixMilli()
	if w.status.FirstIssueAt == 0 {
		w.status.FirstIssueAt = w.status.LastIssueAt
	}
	w.mu.Unlock()
	common.SysError("video scheduling audit: " + reason)
}

func (w *videoAuditWriter) noteTruncated() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status.Truncated++
	w.status.LastIssue, w.status.LastIssueAt = "snapshot_limit", time.Now().UnixMilli()
	if w.status.FirstIssueAt == 0 {
		w.status.FirstIssueAt = w.status.LastIssueAt
	}
}

func (w *videoAuditWriter) flush(ctx context.Context) {
	for {
		w.mu.Lock()
		n := min(len(w.queue), 100)
		batch := append([]videoAuditBatch(nil), w.queue[:n]...)
		clear(w.queue[:n])
		w.queue = w.queue[n:]
		w.mu.Unlock()
		if len(batch) == 0 {
			return
		}
		for _, entry := range batch {
			var err error
			for range 3 {
				writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err = model.InsertVideoScheduleAudit(writeCtx, entry.audit)
				cancel()
				if err == nil || ctx.Err() != nil {
					break
				}
			}
			if err != nil {
				w.noteFailure("request_write_failed")
			}
			w.mu.Lock()
			w.status.Pending--
			w.status.PendingBytes -= entry.bytes
			if err == nil {
				w.status.Written++
				w.status.LastWriteAt = time.Now().UnixMilli()
			} else {
				w.status.Dropped++
			}
			w.mu.Unlock()
		}
		w.mu.Lock()
		w.status.OldestAt = 0
		if len(w.queue) > 0 {
			w.status.OldestAt = w.queue[0].queuedAt
		}
		w.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
	}
}

func StartVideoScheduleAuditWriter() {
	videoAuditQueue.start()
}

func (w *videoAuditWriter) start() {
	w.mu.Lock()
	if w.status.Running || w.closing {
		w.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel, w.status.Running = cancel, true
	w.mu.Unlock()
	go func() {
		defer close(w.done)
		defer func() {
			if recover() != nil {
				w.noteFailure("writer_panic")
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			w.status.Running, w.closing = false, true
			w.status.Dropped += int64(w.status.Pending)
			w.status.Pending, w.status.PendingBytes, w.status.OldestAt = 0, 0, 0
			w.queue = nil
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.flush(ctx)
			case <-w.wake:
				w.flush(ctx)
			case <-w.stop:
				w.flush(ctx)
				return
			}
		}
	}()
}

func StopVideoScheduleAuditWriter(ctx context.Context) {
	videoAuditQueue.stopAndFlush(ctx)
}

func (w *videoAuditWriter) stopAndFlush(ctx context.Context) {
	w.mu.Lock()
	if !w.status.Running {
		w.mu.Unlock()
		return
	}
	if !w.closing {
		w.closing = true
		close(w.stop)
	}
	cancel := w.cancel
	w.mu.Unlock()
	defer cancel()
	select {
	case <-w.done:
	case <-ctx.Done():
		w.noteFailure("shutdown_timeout")
		cancel()
	}
}

// MarkVideoScheduleAuditMaintenanceFailure also feeds the existing per-node
// system information reporter when the leased maintenance task fails.
func MarkVideoScheduleAuditMaintenanceFailure(err error) {
	if err != nil {
		videoAuditQueue.noteFailure("maintenance_failed")
	}
}
