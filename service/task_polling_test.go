package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type taskPollingFetchAdaptor struct {
	mu           sync.Mutex
	taskIDs      []string
	fetched      chan string
	blockTaskID  string
	blockStarted chan struct{}
	releaseBlock chan struct{}
	blockOnce    sync.Once
}

type resolutionSuccessPollingAdaptor struct {
	adjustCalls int
}

func (a *resolutionSuccessPollingAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *resolutionSuccessPollingAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(`{"provider_status":"complete"}`)),
	}, nil
}

func (a *resolutionSuccessPollingAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		Url:                      "https://example.invalid/video.mp4",
		EffectiveDurationSeconds: 4,
		TotalTokens:              999999,
	}, nil
}

func (a *resolutionSuccessPollingAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	a.adjustCalls++
	return 123456
}

type sunoFailurePollingAdaptor struct {
	failReason string
}

func (a *sunoFailurePollingAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *sunoFailurePollingAdaptor) FetchTask(_ string, _ string, body map[string]any, _ string) (*http.Response, error) {
	taskIDs, _ := body["ids"].([]string)
	items := make([]taskdto.SunoDataResponse, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		items = append(items, taskdto.SunoDataResponse{
			TaskID:     taskID,
			Status:     string(model.TaskStatusFailure),
			FailReason: a.failReason,
			FinishTime: time.Now().Unix(),
		})
	}

	responseBody, err := common.Marshal(taskdto.TaskResponse[[]taskdto.SunoDataResponse]{
		Code: taskdto.TaskSuccessCode,
		Data: items,
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *sunoFailurePollingAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, nil
}

func (a *sunoFailurePollingAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *taskPollingFetchAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *taskPollingFetchAdaptor) FetchTask(_ string, _ string, body map[string]any, _ string) (*http.Response, error) {
	taskID, _ := body["task_id"].(string)
	if taskID == a.blockTaskID && a.releaseBlock != nil {
		a.blockOnce.Do(func() {
			if a.blockStarted != nil {
				close(a.blockStarted)
			}
		})
		<-a.releaseBlock
	}

	a.mu.Lock()
	a.taskIDs = append(a.taskIDs, taskID)
	a.mu.Unlock()
	if a.fetched != nil {
		select {
		case a.fetched <- taskID:
		default:
		}
	}

	response := taskdto.TaskResponse[model.Task]{
		Code: taskdto.TaskSuccessCode,
		Data: model.Task{
			TaskID:   taskID,
			Status:   model.TaskStatusInProgress,
			Progress: "30%",
		},
	}
	responseBody, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *taskPollingFetchAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return &relaycommon.TaskInfo{Status: model.TaskStatusInProgress}, nil
}

func (a *taskPollingFetchAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *taskPollingFetchAdaptor) fetchCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.taskIDs)
}

func (a *taskPollingFetchAdaptor) fetchedTaskIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.taskIDs...)
}

func seedTaskPollingChannel(t *testing.T, id int, disableSleep bool) {
	t.Helper()
	ch := &model.Channel{
		Id:     id,
		Type:   constant.ChannelTypeKling,
		Name:   "polling_channel",
		Key:    "sk-test",
		Status: common.ChannelStatusEnabled,
	}
	if disableSleep {
		ch.SetOtherSettings(dto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	}
	require.NoError(t, model.DB.Create(ch).Error)
}

func seedPollingTask(t *testing.T, channelID int, publicID string, upstreamID string) *model.Task {
	t.Helper()
	task := &model.Task{
		TaskID:    publicID,
		Platform:  constant.TaskPlatform("kling"),
		UserId:    1,
		ChannelId: channelID,
		Action:    constant.TaskActionGenerate,
		Status:    model.TaskStatusInProgress,
		Progress:  "30%",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: upstreamID,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)
	return task
}

func TestResolutionPricedTaskPollingSettlesPerSecondDifference(t *testing.T) {
	truncate(t)
	const userID, channelID = 120, 120
	seedUser(t, userID, 1_000_000)
	seedTaskPollingChannel(t, channelID, true)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.TaskID = "task_resolution_polling"
	task.Platform = constant.TaskPlatform("kling")
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.Progress = "30%"
	task.PrivateData.UpstreamTaskID = "upstream_resolution_polling"
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	adaptor := &resolutionSuccessPollingAdaptor{}

	err = updateVideoSingleTask(context.Background(), adaptor, &channel, task.GetUpstreamTaskID(), map[string]*model.Task{
		task.GetUpstreamTaskID(): task,
	})
	require.NoError(t, err)

	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 4, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, task.Quota)
	assert.Zero(t, adaptor.adjustCalls)
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	require.NotNil(t, reloaded.PrivateData.BillingContext)
	assert.Equal(t, 4, reloaded.PrivateData.BillingContext.SettledDurationSeconds)
}

func TestResolutionPollingKeepsTerminalTaskRetryableWhenSettlementPersistenceFails(t *testing.T) {
	truncate(t)
	const userID, channelID = 392, 392
	seedUser(t, userID, 1_000_000)
	seedTaskPollingChannel(t, channelID, true)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.TaskID = "task_resolution_terminal_retry"
	task.Platform = constant.TaskPlatform("kling")
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.Progress = "30%"
	task.PrivateData.UpstreamTaskID = "upstream_resolution_terminal_retry"
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)

	const callbackName = "test:fail_resolution_polling_settlement_task_update"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Task" {
			return
		}
		values, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok {
			return
		}
		if _, hasQuota := values["quota"]; hasQuota {
			tx.AddError(errors.New("forced resolution settlement persistence failure"))
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
		}
	})

	adaptor := &resolutionSuccessPollingAdaptor{}
	require.NoError(t, updateVideoSingleTask(context.Background(), adaptor, &channel, task.GetUpstreamTaskID(), map[string]*model.Task{
		task.GetUpstreamTaskID(): task,
	}))
	var afterFailure model.Task
	require.NoError(t, model.DB.First(&afterFailure, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), afterFailure.Status)
	assert.Equal(t, preConsumed, afterFailure.Quota)
	assert.True(t, model.HasUnfinishedSyncTasks())
	assert.Zero(t, countLogs(t))

	require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	callbackRegistered = false
	require.NoError(t, model.DB.First(&afterFailure, task.ID).Error)
	require.NoError(t, updateVideoSingleTask(context.Background(), adaptor, &channel, afterFailure.GetUpstreamTaskID(), map[string]*model.Task{
		afterFailure.GetUpstreamTaskID(): &afterFailure,
	}))
	var afterRetry model.Task
	require.NoError(t, model.DB.First(&afterRetry, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), afterRetry.Status)
	assert.False(t, afterRetry.PrivateData.BillingContext.SettlementPending)
	assert.False(t, model.HasUnfinishedSyncTasks())
	assert.Equal(t, int64(1), countLogs(t))
}

func TestResolutionPollingDeduplicatesLogWhenPendingClearFails(t *testing.T) {
	truncate(t)
	originalLogDB := model.LOG_DB
	logDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	model.LOG_DB = logDB
	t.Cleanup(func() { model.LOG_DB = originalLogDB })

	const userID, channelID = 393, 393
	seedUser(t, userID, 1_000_000)
	seedTaskPollingChannel(t, channelID, true)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.TaskID = "task_resolution_log_dedupe"
	task.Platform = constant.TaskPlatform("kling")
	task.Action = constant.TaskActionGenerate
	task.Status = model.TaskStatusInProgress
	task.Progress = "30%"
	task.PrivateData.UpstreamTaskID = "upstream_resolution_log_dedupe"
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)

	const callbackName = "test:fail_resolution_pending_clear"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Task" {
			return
		}
		values, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok {
			return
		}
		if _, hasPrivateData := values["private_data"]; hasPrivateData {
			if _, hasQuota := values["quota"]; !hasQuota {
				tx.AddError(errors.New("forced settlement pending clear failure"))
			}
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
		}
	})

	adaptor := &resolutionSuccessPollingAdaptor{}
	require.NoError(t, updateVideoSingleTask(context.Background(), adaptor, &channel, task.GetUpstreamTaskID(), map[string]*model.Task{
		task.GetUpstreamTaskID(): task,
	}))
	var pending model.Task
	require.NoError(t, model.DB.First(&pending, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), pending.Status)
	require.NotNil(t, pending.PrivateData.BillingContext)
	assert.True(t, pending.PrivateData.BillingContext.SettlementPending)
	assert.Equal(t, int64(1), countLogs(t))
	var firstLog model.Log
	require.NoError(t, model.LOG_DB.First(&firstLog).Error)
	assert.Equal(t, "task-resolution-settlement-"+strconv.FormatInt(task.ID, 10), firstLog.RequestId)

	require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	callbackRegistered = false
	require.NoError(t, model.DB.First(&pending, task.ID).Error)
	require.NoError(t, updateVideoSingleTask(context.Background(), adaptor, &channel, pending.GetUpstreamTaskID(), map[string]*model.Task{
		pending.GetUpstreamTaskID(): &pending,
	}))
	var afterRetry model.Task
	require.NoError(t, model.DB.First(&afterRetry, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), afterRetry.Status)
	assert.False(t, afterRetry.PrivateData.BillingContext.SettlementPending)
	assert.Equal(t, int64(1), countLogs(t))
}

func TestResolutionPollingSerializesSeparateLogDatabasePublicationAcrossSQLiteSessions(t *testing.T) {
	originalDB, originalLogDB := model.DB, model.LOG_DB
	tempDir := t.TempDir()
	mainDSN := "file:" + filepath.ToSlash(filepath.Join(tempDir, "main.db")) + "?_busy_timeout=5000"
	logDSN := "file:" + filepath.ToSlash(filepath.Join(tempDir, "log.db")) + "?_busy_timeout=5000"
	mainDB, err := gorm.Open(sqlite.Open(mainDSN), &gorm.Config{})
	require.NoError(t, err)
	logDB, err := gorm.Open(sqlite.Open(logDSN), &gorm.Config{})
	require.NoError(t, err)
	mainSQLDB, err := mainDB.DB()
	require.NoError(t, err)
	mainSQLDB.SetMaxOpenConns(4)
	logSQLDB, err := logDB.DB()
	require.NoError(t, err)
	logSQLDB.SetMaxOpenConns(4)
	require.NoError(t, mainDB.AutoMigrate(&model.Task{}, &model.User{}, &model.Channel{}))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	model.DB, model.LOG_DB = mainDB, logDB
	t.Cleanup(func() {
		model.DB, model.LOG_DB = originalDB, originalLogDB
		require.NoError(t, mainSQLDB.Close())
		require.NoError(t, logSQLDB.Close())
	})

	const userID, channelID = 402, 402
	seedUser(t, userID, 1_000_000)
	seedTaskPollingChannel(t, channelID, true)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.TaskID = "task_resolution_cross_database_concurrency"
	task.Platform = constant.TaskPlatform("kling")
	task.Action = constant.TaskActionGenerate
	task.PrivateData.UpstreamTaskID = "upstream_resolution_cross_database_concurrency"
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	var firstPollerTask, secondPollerTask model.Task
	require.NoError(t, model.DB.First(&firstPollerTask, task.ID).Error)
	require.NoError(t, model.DB.First(&secondPollerTask, task.ID).Error)

	start := make(chan struct{})
	errorsByPoller := make(chan error, 2)
	var pollers sync.WaitGroup
	for _, pollerTask := range []*model.Task{&firstPollerTask, &secondPollerTask} {
		pollerTask := pollerTask
		pollers.Add(1)
		go func() {
			defer pollers.Done()
			<-start
			errorsByPoller <- updateVideoSingleTask(context.Background(), &resolutionSuccessPollingAdaptor{}, &channel, pollerTask.GetUpstreamTaskID(), map[string]*model.Task{
				pollerTask.GetUpstreamTaskID(): pollerTask,
			})
		}()
	}
	close(start)
	pollers.Wait()
	close(errorsByPoller)
	for pollerErr := range errorsByPoller {
		require.NoError(t, pollerErr)
	}

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	require.NotNil(t, persisted.PrivateData.BillingContext)
	assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), persisted.Status)
	assert.False(t, persisted.PrivateData.BillingContext.SettlementPending)
	assert.True(t, persisted.PrivateData.BillingContext.SettlementCompleted)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, "task-resolution-settlement-"+strconv.FormatInt(task.ID, 10), logs[0].RequestId)
}

func TestUpdateVideoTasksDefaultSleepWaitsBetweenTasks(t *testing.T) {
	truncate(t)

	const channelID = 101
	seedTaskPollingChannel(t, channelID, false)
	first := seedPollingTask(t, channelID, "task_public_1", "upstream_1")
	second := seedPollingTask(t, channelID, "task_public_2", "upstream_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		channelID: {
			first.GetUpstreamTaskID(),
			second.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		first.GetUpstreamTaskID():  first,
		second.GetUpstreamTaskID(): second,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, adaptor.fetchCount())
}

func TestUpdateVideoTasksCanSkipPollingSleepPerChannel(t *testing.T) {
	truncate(t)

	const channelID = 102
	seedTaskPollingChannel(t, channelID, true)
	first := seedPollingTask(t, channelID, "task_public_3", "upstream_3")
	second := seedPollingTask(t, channelID, "task_public_4", "upstream_4")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		channelID: {
			first.GetUpstreamTaskID(),
			second.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		first.GetUpstreamTaskID():  first,
		second.GetUpstreamTaskID(): second,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, adaptor.fetchCount())
}

func TestUpdateVideoTasksDefaultSleepDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const firstChannelID = 201
	const secondChannelID = 202
	seedTaskPollingChannel(t, firstChannelID, false)
	seedTaskPollingChannel(t, secondChannelID, false)
	firstChannelFirst := seedPollingTask(t, firstChannelID, "task_public_5", "upstream_a_1")
	firstChannelSecond := seedPollingTask(t, firstChannelID, "task_public_6", "upstream_a_2")
	secondChannelFirst := seedPollingTask(t, secondChannelID, "task_public_7", "upstream_b_1")
	secondChannelSecond := seedPollingTask(t, secondChannelID, "task_public_8", "upstream_b_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		firstChannelID: {
			firstChannelFirst.GetUpstreamTaskID(),
			firstChannelSecond.GetUpstreamTaskID(),
		},
		secondChannelID: {
			secondChannelFirst.GetUpstreamTaskID(),
			secondChannelSecond.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		firstChannelFirst.GetUpstreamTaskID():   firstChannelFirst,
		firstChannelSecond.GetUpstreamTaskID():  firstChannelSecond,
		secondChannelFirst.GetUpstreamTaskID():  secondChannelFirst,
		secondChannelSecond.GetUpstreamTaskID(): secondChannelSecond,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_a_1", "upstream_b_1"}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksSlowChannelDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const slowChannelID = 251
	const fastChannelID = 252
	seedTaskPollingChannel(t, slowChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	slowTask := seedPollingTask(t, slowChannelID, "task_public_slow", "upstream_slow_1")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_fast_1", "upstream_fast_parallel_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_fast_2", "upstream_fast_parallel_2")

	adaptor := &taskPollingFetchAdaptor{
		fetched:      make(chan string, 4),
		blockTaskID:  slowTask.GetUpstreamTaskID(),
		blockStarted: make(chan struct{}),
		releaseBlock: make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseBlockedTask := func() {
		releaseOnce.Do(func() {
			close(adaptor.releaseBlock)
		})
	}
	t.Cleanup(releaseBlockedTask)
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	errCh := make(chan error, 1)
	gopool.Go(func() {
		errCh <- UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"), map[int][]string{
			slowChannelID: {
				slowTask.GetUpstreamTaskID(),
			},
			fastChannelID: {
				fastFirst.GetUpstreamTaskID(),
				fastSecond.GetUpstreamTaskID(),
			},
		}, map[string]*model.Task{
			slowTask.GetUpstreamTaskID():   slowTask,
			fastFirst.GetUpstreamTaskID():  fastFirst,
			fastSecond.GetUpstreamTaskID(): fastSecond,
		})
	})

	select {
	case <-adaptor.blockStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("slow channel did not start blocking")
	}

	require.Eventually(t, func() bool {
		fetchedTaskIDs := adaptor.fetchedTaskIDs()
		return len(fetchedTaskIDs) == 2 &&
			fetchedTaskIDs[0] == fastFirst.GetUpstreamTaskID() &&
			fetchedTaskIDs[1] == fastSecond.GetUpstreamTaskID()
	}, 500*time.Millisecond, 10*time.Millisecond)

	releaseBlockedTask()
	require.NoError(t, <-errCh)
	assert.ElementsMatch(t, []string{
		slowTask.GetUpstreamTaskID(),
		fastFirst.GetUpstreamTaskID(),
		fastSecond.GetUpstreamTaskID(),
	}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksMixedChannelSleepSettings(t *testing.T) {
	truncate(t)

	const sleepyChannelID = 301
	const fastChannelID = 302
	seedTaskPollingChannel(t, sleepyChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	sleepyFirst := seedPollingTask(t, sleepyChannelID, "task_public_9", "upstream_sleepy_1")
	sleepySecond := seedPollingTask(t, sleepyChannelID, "task_public_10", "upstream_sleepy_2")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_11", "upstream_fast_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_12", "upstream_fast_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		sleepyChannelID: {
			sleepyFirst.GetUpstreamTaskID(),
			sleepySecond.GetUpstreamTaskID(),
		},
		fastChannelID: {
			fastFirst.GetUpstreamTaskID(),
			fastSecond.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		sleepyFirst.GetUpstreamTaskID():  sleepyFirst,
		sleepySecond.GetUpstreamTaskID(): sleepySecond,
		fastFirst.GetUpstreamTaskID():    fastFirst,
		fastSecond.GetUpstreamTaskID():   fastSecond,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_sleepy_1", "upstream_fast_1", "upstream_fast_2"}, adaptor.fetchedTaskIDs())
}

func TestUpdateSunoTasksStalePollsRefundExactlyOnce(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 401, 401, 401
	const initialUserQuota, initialTokenQuota, taskQuota = 10_000, 6_000, 2_500
	const publicTaskID, upstreamTaskID = "suno_public_refund_once", "suno_upstream_refund_once"

	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-suno-refund-once", initialTokenQuota)
	baseURL := "https://suno.invalid"
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeSunoAPI,
		Name:    "suno_refund_once",
		Key:     "sk-suno-channel",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}).Error)

	task := makeTask(userID, channelID, taskQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = publicTaskID
	task.Platform = constant.TaskPlatformSuno
	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix()
	task.PrivateData.UpstreamTaskID = upstreamTaskID
	require.NoError(t, model.DB.Create(task).Error)

	var firstPollTask model.Task
	var staleSecondPollTask model.Task
	require.NoError(t, model.DB.First(&firstPollTask, task.ID).Error)
	require.NoError(t, model.DB.First(&staleSecondPollTask, task.ID).Error)

	adaptor := &sunoFailurePollingAdaptor{failReason: "upstream failed"}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	require.NoError(t, updateSunoTasks(context.Background(), channelID, []string{upstreamTaskID}, map[string]*model.Task{
		upstreamTaskID: &firstPollTask,
	}))
	require.NoError(t, updateSunoTasks(context.Background(), channelID, []string{upstreamTaskID}, map[string]*model.Task{
		upstreamTaskID: &staleSecondPollTask,
	}))

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Zero(t, reloaded.Quota)
	assert.Equal(t, initialUserQuota+taskQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota+taskQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestRunTaskPollingOnceDoesNotRefundHistoricalFailedTask(t *testing.T) {
	truncate(t)

	const userID, initialQuota, taskQuota = 402, 10_000, 1_200
	seedUser(t, userID, initialQuota)

	task := makeTask(userID, 0, taskQuota, 0, BillingSourceWallet, 0)
	task.TaskID = "historical_failed_already_refunded"
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.SubmitTime = time.Now().Add(-90 * 24 * time.Hour).Unix()
	task.UpdatedAt = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, model.DB.Create(task).Error)

	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return &taskPollingFetchAdaptor{}
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	summary := RunTaskPollingOnce(context.Background(), nil)

	assert.Zero(t, summary.UnfinishedTasks)
	assert.Equal(t, initialQuota, getUserQuota(t, userID))
	assert.Equal(t, taskQuota, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSweepTimedOutTasksHonorsRefundRolloutBoundary(t *testing.T) {
	truncate(t)

	const (
		userID          = 403
		initialQuota    = 10_000
		legacyTaskQuota = 1_800
		modernTaskQuota = 1_200
	)
	seedUser(t, userID, initialQuota)

	legacyTask := makeTask(userID, 0, legacyTaskQuota, 0, BillingSourceWallet, 0)
	legacyTask.TaskID = "legacy_timeout_without_refund"
	legacyTask.Progress = "50%"
	legacyTask.SubmitTime = 1771718399 // 2026-02-21 23:59:59 UTC
	require.NoError(t, model.DB.Create(legacyTask).Error)

	modernTask := makeTask(userID, 0, modernTaskQuota, 0, BillingSourceWallet, 0)
	modernTask.TaskID = "modern_timeout_with_refund"
	modernTask.Progress = "50%"
	modernTask.SubmitTime = 1771718400 // 2026-02-22 00:00:00 UTC
	require.NoError(t, model.DB.Create(modernTask).Error)

	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })

	sweepTimedOutTasks(context.Background())

	var reloadedLegacy model.Task
	var reloadedModern model.Task
	require.NoError(t, model.DB.First(&reloadedLegacy, legacyTask.ID).Error)
	require.NoError(t, model.DB.First(&reloadedModern, modernTask.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloadedLegacy.Status)
	assert.EqualValues(t, model.TaskStatusFailure, reloadedModern.Status)
	assert.Zero(t, reloadedLegacy.Quota)
	assert.Zero(t, reloadedModern.Quota)
	assert.Contains(t, reloadedLegacy.FailReason, "旧系统遗留任务")
	assert.Contains(t, reloadedModern.FailReason, "任务超时")
	assert.Equal(t, initialQuota+modernTaskQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestSweepTimedOutTasksDoesNotCrossResolutionSettlementMarkers(t *testing.T) {
	truncate(t)
	const userID = 404
	seedUser(t, userID, 10_000)
	for index, state := range []struct {
		name      string
		pending   bool
		completed bool
	}{
		{name: "pending", pending: true},
		{name: "completed", completed: true},
	} {
		task := makeTask(userID, 0, 1_200+index, 0, BillingSourceWallet, 0)
		task.TaskID = "resolution_timeout_guard_" + state.name
		task.Progress = "50%"
		task.SubmitTime = time.Now().Add(-2 * time.Minute).Unix()
		task.PrivateData.BillingContext = resolutionBillingContext(8)
		task.PrivateData.BillingContext.SettlementPending = state.pending
		task.PrivateData.BillingContext.SettlementCompleted = state.completed
		if state.pending {
			task.PrivateData.BillingContext.SettlementPreConsumed = 1_500
			task.PrivateData.BillingContext.SettlementActualQuota = task.Quota
		}
		require.NoError(t, model.DB.Create(task).Error)
	}

	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })
	sweepTimedOutTasks(context.Background())

	var tasks []model.Task
	require.NoError(t, model.DB.Where("task_id LIKE ?", "resolution_timeout_guard_%").Order("id").Find(&tasks).Error)
	require.Len(t, tasks, 2)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), tasks[0].Status)
	assert.True(t, tasks[0].PrivateData.BillingContext.SettlementPending)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), tasks[1].Status)
	assert.True(t, tasks[1].PrivateData.BillingContext.SettlementCompleted)
	assert.Equal(t, 10_000, getUserQuota(t, userID))
	assert.Zero(t, countLogs(t))
}

func TestChannelLookupFailureDoesNotTerminalizeResolutionSettlementMarkers(t *testing.T) {
	truncate(t)
	const userID, missingChannelID = 405, 405
	seedUser(t, userID, 10_000)
	taskIDs := make([]string, 0, 2)
	tasksByUpstreamID := make(map[string]*model.Task, 2)
	for index, state := range []struct {
		name      string
		pending   bool
		completed bool
	}{
		{name: "pending", pending: true},
		{name: "completed", completed: true},
	} {
		task := makeTask(userID, missingChannelID, 1_200+index, 0, BillingSourceWallet, 0)
		task.TaskID = "resolution_missing_channel_" + state.name
		task.Platform = constant.TaskPlatform("kling")
		task.Action = constant.TaskActionGenerate
		task.PrivateData.UpstreamTaskID = "upstream_resolution_missing_channel_" + state.name
		task.PrivateData.BillingContext = resolutionBillingContext(8)
		task.PrivateData.BillingContext.SettlementPending = state.pending
		task.PrivateData.BillingContext.SettlementCompleted = state.completed
		if state.pending {
			task.PrivateData.BillingContext.SettlementPreConsumed = 1_500
			task.PrivateData.BillingContext.SettlementActualQuota = task.Quota
		}
		require.NoError(t, model.DB.Create(task).Error)
		taskIDs = append(taskIDs, task.GetUpstreamTaskID())
		tasksByUpstreamID[task.GetUpstreamTaskID()] = task
	}
	legacyTask := makeTask(userID, missingChannelID, 900, 0, BillingSourceWallet, 0)
	legacyTask.TaskID = "resolution_missing_channel_legacy"
	legacyTask.Platform = constant.TaskPlatform("kling")
	legacyTask.Action = constant.TaskActionGenerate
	legacyTask.PrivateData.UpstreamTaskID = "upstream_resolution_missing_channel_legacy"
	legacyTask.PrivateData.BillingContext = nil
	require.NoError(t, model.DB.Create(legacyTask).Error)
	taskIDs = append(taskIDs, legacyTask.GetUpstreamTaskID())
	tasksByUpstreamID[legacyTask.GetUpstreamTaskID()] = legacyTask

	require.Error(t, updateVideoTasks(context.Background(), constant.TaskPlatform("kling"), missingChannelID, taskIDs, tasksByUpstreamID))
	var tasks []model.Task
	require.NoError(t, model.DB.Where("task_id LIKE ?", "resolution_missing_channel_%").Order("id").Find(&tasks).Error)
	require.Len(t, tasks, 3)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), tasks[0].Status)
	assert.True(t, tasks[0].PrivateData.BillingContext.SettlementPending)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), tasks[1].Status)
	assert.True(t, tasks[1].PrivateData.BillingContext.SettlementCompleted)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), tasks[2].Status)
}
