package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSystemTaskRegistry swaps the package registry for the given handlers for
// the duration of a test and restores the original registry afterward.
func withSystemTaskRegistry(t *testing.T, handlers ...SystemTaskHandler) {
	t.Helper()
	systemTaskHandlersMu.Lock()
	saved := systemTaskHandlers
	systemTaskHandlers = map[string]SystemTaskHandler{}
	for _, h := range handlers {
		systemTaskHandlers[h.Type()] = h
	}
	systemTaskHandlersMu.Unlock()
	t.Cleanup(func() {
		systemTaskHandlersMu.Lock()
		systemTaskHandlers = saved
		systemTaskHandlersMu.Unlock()
	})
}

type stubScheduledHandler struct {
	taskType string
	enabled  bool
	interval time.Duration
	onRun    func(ctx context.Context, task *model.SystemTask, runnerID string)
}

type stubSystemTaskRunResult struct {
	taskID   string
	taskType string
	err      error
}

func (h *stubScheduledHandler) Type() string { return h.taskType }

func (h *stubScheduledHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if h.onRun != nil {
		h.onRun(ctx, task, runnerID)
	}
}

func (h *stubScheduledHandler) Enabled() bool           { return h.enabled }
func (h *stubScheduledHandler) Interval() time.Duration { return h.interval }
func (h *stubScheduledHandler) NewPayload() any         { return nil }

func countSystemTasks(t *testing.T, taskType string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("type = ?", taskType).Count(&count).Error)
	return count
}

func TestSystemTaskSchedulerCreatesWhenDueAndDedups(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_scheduled", enabled: true, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// An active (pending) row already exists, so a second pass must not create
	// another row.
	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Finish the run; with a fresh updated_at the next run is not due yet.
	latest, err := model.GetLatestSystemTask(handler.taskType)
	require.NoError(t, err)
	require.NotNil(t, latest)
	_, claimed, err := model.ClaimSystemTask(latest.ID, handler.taskType, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(latest.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Backdate the finished row beyond the interval -> the job becomes due again.
	require.NoError(t, model.DB.Model(&model.SystemTask{}).
		Where("task_id = ?", latest.TaskID).
		Update("updated_at", common.GetTimestamp()-120).Error)

	runSystemTaskScheduler()
	require.Equal(t, int64(2), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskSchedulerSkipsDisabled(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_disabled", enabled: false, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	assert.Equal(t, int64(0), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskAsyncPollUsesCompletionAndReloadedInterval(t *testing.T) {
	truncate(t)
	h := &stubScheduledHandler{taskType: model.SystemTaskTypeAsyncTaskPoll, enabled: true, interval: 5 * time.Second}
	withSystemTaskRegistry(t, h)
	start := time.Unix(1000, 0)
	// Simulate a slow 20-second round; waiting starts at completion, not start.
	last := &model.SystemTask{TaskID: "slow-poll", Type: h.Type(), Status: model.SystemTaskStatusSucceeded, CreatedAt: 1000, UpdatedAt: 1020}
	require.NoError(t, model.DB.Create(last).Error)
	s := asyncTaskPollSchedule{}
	s.refresh(start.Add(21 * time.Second)) // also represents a restarted node
	assert.True(t, start.Add(24*time.Second).Before(s.nextCheck()))
	// Account for persisted timestamps being rounded down to seconds.
	assert.Equal(t, start.Add(26*time.Second), s.nextCheck())
	s.refresh(start.Add(26 * time.Second))
	assert.EqualValues(t, 2, countSystemTasks(t, h.Type()))
	assert.Equal(t, model.SystemTaskStatusPending, s.latest.Status)

	// No catch-up or overlapping row, even after a much later wakeup.
	s.refresh(start.Add(time.Hour))
	assert.EqualValues(t, 2, countSystemTasks(t, h.Type()))

	// A second node holding an older terminal snapshot must reread before create.
	second := asyncTaskPollSchedule{latest: last}
	second.refresh(start.Add(26 * time.Second))
	assert.Equal(t, s.latest.TaskID, second.latest.TaskID)

	// Dynamic configuration changes are evaluated on each memory tick.
	s.latest = last
	h.interval = 15 * time.Second
	assert.Equal(t, start.Add(36*time.Second), s.nextCheck())
	h.interval = 5 * time.Second
	assert.Equal(t, start.Add(26*time.Second), s.nextCheck())
	h.interval = 30 * time.Second
	assert.Equal(t, start.Add(51*time.Second), s.nextCheck())
}

func TestSystemTaskAsyncPollIdleDisabledAndGlobalCadence(t *testing.T) {
	truncate(t)
	poll := &stubScheduledHandler{taskType: model.SystemTaskTypeAsyncTaskPoll, interval: 3 * time.Second}
	other := &stubScheduledHandler{taskType: model.SystemTaskTypeMidjourneyPoll, enabled: true, interval: 15 * time.Second}
	withSystemTaskRegistry(t, poll, other)
	now := time.Unix(1000, 0)
	s := asyncTaskPollSchedule{}
	s.refresh(now)
	assert.Equal(t, now.Add(15*time.Second), s.nextCheck(), "idle discovery stays at 15 seconds")
	assert.Zero(t, countSystemTasks(t, poll.Type()))

	poll.enabled = true
	runSystemTaskScheduler()
	assert.Zero(t, countSystemTasks(t, poll.Type()), "global scheduler does not bypass targeted cadence")
	assert.EqualValues(t, 1, countSystemTasks(t, other.Type()))
	s.refresh(now.Add(15 * time.Second))
	assert.EqualValues(t, 1, countSystemTasks(t, poll.Type()))
	assert.Equal(t, now.Add(18*time.Second), s.nextCheck(), "active rows are not queried every second")
	poll.enabled = false
	s.refresh(now.Add(18 * time.Second))
	assert.Nil(t, s.latest)
	assert.EqualValues(t, 1, countSystemTasks(t, poll.Type()), "disable leaves queued work intact")
}

func TestSystemTaskPollingIntervalConcurrentReads(t *testing.T) {
	previous := operation_setting.TaskPollingInterval()
	t.Cleanup(func() { _ = operation_setting.LoadTaskPollingInterval(fmt.Sprint(int(previous.Seconds()))) })
	values := []string{"3", "5", "15", "300"}
	var wg sync.WaitGroup
	for _, value := range values {
		wg.Go(func() {
			assert.NoError(t, operation_setting.LoadTaskPollingInterval(value))
			assert.Contains(t, []time.Duration{3 * time.Second, 5 * time.Second, 15 * time.Second, 300 * time.Second}, operation_setting.TaskPollingInterval())
		})
	}
	wg.Wait()
}

func TestSystemTaskClaimPassDispatchesByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 1)
	handler := &stubScheduledHandler{
		taskType: "test_dispatch",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskType: task.Type,
				err:      model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handler)

	_, err := model.CreateSystemTask(handler.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	select {
	case got := <-ran:
		require.NoError(t, got.err)
		assert.Equal(t, handler.taskType, got.taskType)
	case <-time.After(2 * time.Second):
		t.Fatal("claimed task was not dispatched to its handler")
	}

	require.Eventually(t, func() bool {
		latest, err := model.GetLatestSystemTask(handler.taskType)
		return err == nil && latest != nil && latest.Status == model.SystemTaskStatusSucceeded
	}, 2*time.Second, 20*time.Millisecond)
}

func TestSystemTaskClaimPassDispatchesEarliestPendingByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 2)
	handlerA := &stubScheduledHandler{
		taskType: "test_dispatch_a",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	handlerB := &stubScheduledHandler{
		taskType: "test_dispatch_b",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handlerA, handlerB)

	firstA, err := model.CreateSystemTask(handlerA.taskType, nil, nil)
	require.NoError(t, err)
	secondTaskID, err := model.GenerateSystemTaskID()
	require.NoError(t, err)
	secondA := &model.SystemTask{
		TaskID: secondTaskID,
		Type:   handlerA.taskType,
		Status: model.SystemTaskStatusPending,
	}
	require.NoError(t, model.DB.Create(secondA).Error)
	firstB, err := model.CreateSystemTask(handlerB.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	got := map[string]bool{}
	for range 2 {
		select {
		case result := <-ran:
			require.NoError(t, result.err)
			got[result.taskID] = true
		case <-time.After(2 * time.Second):
			t.Fatal("claimed tasks were not dispatched to their handlers")
		}
	}

	assert.True(t, got[firstA.TaskID])
	assert.True(t, got[firstB.TaskID])
	assert.False(t, got[secondA.TaskID])

	require.Eventually(t, func() bool {
		reloaded, err := model.GetSystemTaskByTaskID(secondA.TaskID)
		return err == nil && reloaded != nil && reloaded.Status == model.SystemTaskStatusPending
	}, 2*time.Second, 20*time.Millisecond)
}

func TestEnqueueSystemTaskReportsCreatedAndExistingActive(t *testing.T) {
	truncate(t)

	first, created, err := EnqueueSystemTask("test_enqueue", map[string]bool{"manual": true})
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, first)

	existing, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.False(t, created)
	require.NotNil(t, existing)
	assert.Equal(t, first.TaskID, existing.TaskID)

	_, claimed, err := model.ClaimSystemTask(first.ID, first.Type, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(first.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	second, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, second)
	assert.NotEqual(t, first.TaskID, second.TaskID)
}
