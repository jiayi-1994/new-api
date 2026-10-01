package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type taskSubmissionTestBilling struct {
	events     *[]string
	settleErr  error
	reserveErr error
	onSettle   func()
	refunds    int
}

func (b *taskSubmissionTestBilling) Settle(int) error {
	*b.events = append(*b.events, "settle")
	if b.onSettle != nil {
		b.onSettle()
	}
	return b.settleErr
}

func (b *taskSubmissionTestBilling) Refund(*gin.Context) {
	*b.events = append(*b.events, "refund")
	b.refunds++
}

func (b *taskSubmissionTestBilling) NeedsRefund() bool        { return b.refunds == 0 }
func (b *taskSubmissionTestBilling) GetPreConsumedQuota() int { return 0 }
func (b *taskSubmissionTestBilling) Reserve(int) error {
	*b.events = append(*b.events, "reserve")
	return b.reserveErr
}

func TestPresentTaskSubmissionUsesNativePresenterAfterPersistence(t *testing.T) {
	plugin, err := pluginruntime.CompilePlugin(`
export const meta = {apiVersion:1,key:"presenter-test",name:"Presenter",version:"1.0.0",author:{name:"Test"},models:["model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native = {decode:function(ctx){return {kind:"submit",model:"model",requestBody:ctx.body.value};},created:function(ctx,task){return {data:{task_id:task.task_id},upstream:task.data};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	priceData := types.PriceData{}
	priceData.AddOtherRatio("seconds", 5)
	task := &model.Task{TaskID: "task_public", SubmitTime: 123}
	task.SetData(map[string]any{"task_id": "upstream_private"})
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      task,
		RelayInfo: &relaycommon.RelayInfo{PriceData: priceData},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{"model":"model"}`))
	c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
	c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{"model": "model"}}})

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"data":{"task_id":"task_public"},
		"upstream":{"task_id":"upstream_private"}
	}`, recorder.Body.String())
	assert.JSONEq(t, `{"seconds":5}`, recorder.Header().Get("X-New-Api-Other-Ratios"))
}

func TestPresentTaskSubmissionFallbackUsesPersistedPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	outcome := &taskSubmissionOutcome{
		Result:    &relay.TaskSubmitResult{},
		Task:      &model.Task{TaskID: "task_persisted", SubmitTime: 456},
		RelayInfo: &relaycommon.RelayInfo{OriginModelName: "video-model"},
	}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{
		"id":"task_persisted",
		"task_id":"task_persisted",
		"status":"queued",
		"model":"video-model",
		"created_at":456
	}`, recorder.Body.String())
}

func TestPresentTaskSubmissionUsesHostOpenAIVideoCreateReceipt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol:  "openai_video",
		Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusSubmitted,
		Progress:   "0%",
		CreatedAt:  456,
		Properties: model.Properties{OriginModelName: "video-model"},
	}
	outcome := &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}}

	presentTaskSubmission(c, outcome)

	assert.JSONEq(t, `{"id":"task_public","object":"video","model":"video-model","status":"queued","progress":0,"created_at":456}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "task_id")
}

func TestExecuteTaskSubmissionRefundsWhenInsertFails(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, false, &events)
	_ = database
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_insert_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionSettlementFailureStaysDurableAndWritesNothing(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, settleErr: errors.New("settlement failed")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "task_billing_settlement_failed", taskErr.Code)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionPersistsPinnedPluginProvenance(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

	c := taskSubmissionTestContext()
	c.Set(common.RequestIdKey, "request-public")
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{
		Generation: &pluginruntime.RoutingGeneration{Number: 42},
		Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{
			Key:        "document-parser",
			Name:       "Document Parser",
			Version:    "1.2.3",
			APIVersion: 1,
			Author: pluginruntime.AuthorMeta{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
		}},
	})
	billing := &taskSubmissionTestBilling{events: &events}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream-private",
			Platform:       constant.TaskPlatform("document-parser"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	require.NotNil(t, outcome.Task.PrivateData.Execution)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "request-public", outcome.Task.PrivateData.Execution.RequestID)
	assert.Equal(t, "/plugin/submit", outcome.Task.PrivateData.Execution.RequestPath)
	assert.Equal(t, "1.2.3", outcome.Task.PrivateData.Execution.TaskPlugin.Version)
	assert.Equal(t, uint64(42), outcome.Task.PrivateData.Execution.TaskPlugin.Generation)
	require.NotNil(t, outcome.Task.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", outcome.Task.PrivateData.Execution.TaskPlugin.Author.URL)

	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.Execution)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin)
	assert.Equal(t, "document-parser", stored.PrivateData.Execution.TaskPlugin.Key)
	require.NotNil(t, stored.PrivateData.Execution.TaskPlugin.Author)
	assert.Equal(t, "Community Author", stored.PrivateData.Execution.TaskPlugin.Author.Name)
	assert.Equal(t, "upstream-private", stored.PrivateData.UpstreamTaskID)
}

// A submit route declaring retainResult: false persists the task row for
// billing but never writes the upstream snapshot for an immediate terminal
// result, while the in-memory task still carries it for the presenter. An
// asynchronous result on the same route is retained because polling and
// retrieval depend on it.
func TestExecuteTaskSubmissionHonorsRouteRetainResult(t *testing.T) {
	retainFalse := false
	for _, tc := range []struct {
		name          string
		immediate     *relaycommon.TaskInfo
		wantDiscarded bool
	}{
		{"immediate success is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, Progress: "100%"}, true},
		{"immediate failure is discarded", &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "rejected"}, true},
		{"asynchronous result is retained", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make([]string, 0, 3)
			database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.Channel{})
			previousDB := model.DB
			model.DB = database
			t.Cleanup(func() { model.DB = previousDB })
			previousLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })

			c := taskSubmissionTestContext()
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{
				Plugin: &pluginruntime.LoadedPlugin{Meta: pluginruntime.Meta{Key: "sync-images"}},
				Route:  pluginruntime.Route{Method: http.MethodPost, Path: "/sync/images", Type: pluginruntime.RouteTypeSubmit, RetainResult: &retainFalse},
			})
			info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
			upstream := []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)

			outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "task_public",
					Platform:       constant.TaskPlatform("sync-images"),
					TaskData:       upstream,
					Immediate:      tc.immediate,
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			assert.JSONEq(t, string(upstream), string(outcome.Task.Data), "presenter keeps the in-memory snapshot")
			assert.Equal(t, tc.wantDiscarded, outcome.Task.PrivateData.ResultDiscarded)
			assert.Equal(t, !tc.wantDiscarded, outcome.Task.ResultRetrievable())

			var stored model.Task
			require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
			assert.Equal(t, tc.wantDiscarded, stored.PrivateData.ResultDiscarded)
			var nullCount int64
			require.NoError(t, database.Model(&model.Task{}).Where("task_id = ? AND data IS NULL", "task_public").Count(&nullCount).Error)
			if tc.wantDiscarded {
				assert.Equal(t, int64(1), nullCount, "snapshot column must stay NULL")
				artifacts, err := projectTaskArtifacts(&stored)
				require.NoError(t, err)
				assert.Empty(t, artifacts)
			} else {
				assert.Equal(t, int64(0), nullCount)
				assert.JSONEq(t, string(upstream), string(stored.Data))
			}
			listed := model.TaskGetAllUserTask(1, 0, 10, model.SyncTaskQueryParams{})
			require.Len(t, listed, 1)
			assert.Empty(t, listed[0].Data, "task lists never select the snapshot column")
		})
	}
}

// A first attempt uses the channel the distributor selected, which the relay
// rebuilds from request context without its settings; channel health must
// still see the channel's scheduling config.
func TestExecuteTaskSubmissionFirstAttemptFeedsVideoScheduling(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.User{}))
	t.Cleanup(func() { require.NoError(t, database.Migrator().DropTable(&model.Channel{}, &model.User{})) })
	previousRedis, previousMemory, previousConsume := common.RedisEnabled, common.MemoryCacheEnabled, common.LogConsumeEnabled
	previousSetting := *operation_setting.GetVideoSchedulingSetting()
	common.RedisEnabled, common.MemoryCacheEnabled, common.LogConsumeEnabled = false, false, false
	operation_setting.GetVideoSchedulingSetting().Mode = operation_setting.VideoSchedulingModeShadow
	t.Cleanup(func() {
		common.RedisEnabled, common.MemoryCacheEnabled, common.LogConsumeEnabled = previousRedis, previousMemory, previousConsume
		*operation_setting.GetVideoSchedulingSetting() = previousSetting
	})
	channel := &model.Channel{Id: 10000 + int(time.Now().UnixNano()%1_000_000), Type: constant.ChannelTypeTaskPlugin, Name: "scheduled", Key: "k",
		OtherSettings: `{"video_scheduling":{"capacity_group":"acct","models":{"plugin-model":{"mode":"per_video","prices":{"*":1}}}}}`}
	require.NoError(t, database.Create(channel).Error)

	c := taskSubmissionTestContext()
	c.Set("channel_id", channel.Id)
	c.Set("channel_type", channel.Type)
	info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
	info.ChannelMeta, info.LockedChannel = nil, nil
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(_ *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: channel.Type} // as the real submit does
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream", Platform: constant.TaskPlatform("plugin")}, nil
	})
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)

	health, err := service.GetVideoChannelHealth(channel.Id, "plugin-model", 0)
	require.NoError(t, err)
	assert.Equal(t, 1, health.Submit.Samples, "the accepted submission is a sample")
	assert.Equal(t, 1, health.InFlight)
	var stored model.Task
	require.NoError(t, database.Where("task_id = ?", "task_public").First(&stored).Error)
	require.NotNil(t, stored.PrivateData.SchedulingSummary)
	assert.Equal(t, model.TaskSchedulingSummary{Model: "plugin-model", CapacityGroup: "acct"}, *stored.PrivateData.SchedulingSummary)
	service.ObserveVideoTerminal(&stored, true)

	// A submission failed by the client leaving is no sample against the upstream.
	c = taskSubmissionTestContext()
	c.Set("channel_id", channel.Id)
	c.Set("channel_type", channel.Type)
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info = taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
	info.ChannelMeta, info.LockedChannel = nil, nil
	_, taskErr = executeTaskSubmissionWith(c, info, func(_ *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: channel.Type}
		cancel()
		return nil, service.TaskErrorWrapper(fmt.Errorf("do request failed: %w", context.Canceled), "do_request_failed", http.StatusInternalServerError)
	})
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	health, err = service.GetVideoChannelHealth(channel.Id, "plugin-model", 0)
	require.NoError(t, err)
	assert.Equal(t, videosched.HealthStat{Rate: 1, Samples: 1}, health.Submit)

	// A channel locked by the origin task is never scheduled, but its real
	// upstream load still feeds health and the in-flight count.
	c = taskSubmissionTestContext()
	info = taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
	info.LockedChannel = channel
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id, ChannelType: channel.Type}
	_, taskErr = executeTaskSubmissionWith(c, info, func(_ *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream-locked", Platform: constant.TaskPlatform("plugin")}, nil
	})
	require.Nil(t, taskErr)
	health, err = service.GetVideoChannelHealth(channel.Id, "plugin-model", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, health.Submit.Samples)
	assert.Equal(t, 1, health.InFlight)
	stored = model.Task{}
	require.NoError(t, database.Where("channel_id = ? AND status <> ?", channel.Id, model.TaskStatusFailure).Last(&stored).Error)
	require.NotNil(t, stored.PrivateData.SchedulingSummary)
	service.ObserveVideoTerminal(&stored, true)
}

func TestExecuteTaskSubmissionRefundsCancellationBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 2)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		cancel()
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectBeforeUpstreamAcceptanceSkipsSubmitAndRefunds(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitted := false

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		submitted = true
		return nil, nil
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.False(t, submitted)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionCallerCancellationDuringSubmitRefundsBeforeDurableBarrier(t *testing.T) {
	events := make([]string, 0, 1)
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	info := taskSubmissionRelayInfo(billing)
	submitStarted := make(chan struct{})
	done := make(chan struct{})
	var outcome *taskSubmissionOutcome
	var taskErr *dto.TaskError

	go func() {
		defer close(done)
		outcome, taskErr = executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
			close(submitStarted)
			<-c.Request.Context().Done()
			return nil, service.TaskErrorWrapperLocal(c.Request.Context().Err(), "do_request_failed", http.StatusInternalServerError)
		})
	}()
	select {
	case <-submitStarted:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "submission did not stop after disconnect")
	}

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "request_cancelled", taskErr.Code)
	assert.Equal(t, []string{"refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

func TestExecuteTaskSubmissionDisconnectAfterDurableInsertDoesNotRefund(t *testing.T) {
	events := make([]string, 0, 3)
	database := setupTaskSubmissionDatabase(t, true, &events)
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
	c := taskSubmissionTestContext()
	requestContext, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestContext)
	billing := &taskSubmissionTestBilling{
		events:   &events,
		onSettle: cancel,
	}
	info := taskSubmissionRelayInfo(billing)

	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{
			UpstreamTaskID: "upstream_private",
			Platform:       constant.TaskPlatform("plugin"),
		}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, "task_public", outcome.Task.TaskID)
	assert.Equal(t, []string{"reserve", "insert", "settle"}, events)
	assert.Zero(t, billing.refunds)
	var count int64
	require.NoError(t, database.Model(&model.Task{}).Where("task_id = ?", "task_public").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	assert.False(t, c.Writer.Written())
}

// setupTaskSubmissionDatabase opens the dialect selected by
// TEST_TASK_DB_DIALECT (SQLite in memory by default) and records every task
// INSERT in events so tests can assert the reserve → insert → settle order.
// Without migrate the task table does not exist and inserts fail.
func setupTaskSubmissionDatabase(t *testing.T, migrate bool, events *[]string) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	var models []any
	if migrate {
		models = append(models, &model.Task{})
	}
	database, _ := openTaskDialectDatabase(t, models...)
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:task-submit-order", func(*gorm.DB) {
		*events = append(*events, "insert")
	}))
	model.DB = database
	t.Cleanup(func() { model.DB = previousDB })
	return database
}

func taskSubmissionTestContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/plugin/submit", strings.NewReader(`{}`))
	return c
}

func taskSubmissionRelayInfo(billing relaycommon.BillingSettler) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          1,
		UsingGroup:      "default",
		OriginModelName: "plugin-model",
		Billing:         billing,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID:  "task_public",
			LockedChannel: &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "plugin"},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin},
	}
}

// Uses the real submit adaptor, expression evaluator, BillingSession, task row
// and consume log. Set TEST_TASK_DB_DIALECT plus TEST_MYSQL_DSN or
// TEST_POSTGRES_DSN to exercise the same contract on an external test database.
// Unique table prefixes keep the fixture isolated from all existing tables.
// openTaskDialectDatabase opens the engine selected by TEST_TASK_DB_DIALECT
// (default SQLite in memory; MySQL and PostgreSQL through TEST_MYSQL_DSN and
// TEST_POSTGRES_DSN) with a unique table prefix, migrates the given models and
// drops them on cleanup. It logs the engine version so database verification
// runs leave a record.
func openTaskDialectDatabase(t *testing.T, models ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(":memory:")
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tsubmit_%d_", time.Now().UnixNano())}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(models...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
	var version string
	if dialect == common.DatabaseTypeSQLite {
		require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	} else {
		require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	}
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestImmediateTaskSettlementDatabase(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume, oldExport := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = false, false, false, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.DataExportEnabled = oldRedis, oldMemory, oldBatch, oldConsume, oldExport
	})

	const expression = `u("units") == 7 ? tier("missing", u("missing") * 1.0) : u("units") == 8 ? tier("invalid", -1.0) : u("units") > 3 ? tier("bulk", u("units") * 0.01) : tier("small", u("units") * 0.01)`
	withTieredBillingConfig(t, map[string]string{"document-model": "tiered_expr"}, map[string]string{"document-model": expression})
	const source = `
export const meta={apiVersion:1,key:"generic-settlement",name:"Generic settlement",version:"1.0.0",author:{name:"Test"},models:["document-model"],fetchMode:"per_task",usageSchema:{units:{type:"number",unit:"count"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/compile",body:ctx.requestBody};}
export function parseSubmitResponse(ctx,resp){return {taskId:"vendor-job",taskData:resp.body,immediate:{status:resp.body.status,reason:"provider rejected job"}};}
export function extractUsage(){return {units:4};}
export function extractUsageOnComplete(ctx,result,body){return body.usage;}
export function parseTaskResult(){throw new Error("completed submissions must not poll");}
export function buildQueryRequest(){throw new Error("completed submissions must not poll");}
`
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	for index, tc := range []struct {
		name, status string
		actual       any
		count        float64
	}{
		{"partial", "SUCCESS", 2, 2}, {"zero", "SUCCESS", 0, 0}, {"larger", "SUCCESS", 6, 6},
		{"invalid usage", "SUCCESS", -1, 4}, {"expression failure", "SUCCESS", 7, 4}, {"negative result", "SUCCESS", 8, 4}, {"missing usage", "SUCCESS", nil, 4}, {"failed", "FAILURE", 9, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"status": tc.status}
				if tc.actual != nil {
					body["usage"] = map[string]any{"units": tc.actual}
				}
				encoded, err := common.Marshal(body)
				if err != nil {
					panic(err)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			initial := int(20 * common.QuotaPerUnit)
			user := model.User{Username: fmt.Sprintf("task_user_%d", index), AffCode: fmt.Sprintf("task_aff_%d", index), Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			ch := model.Channel{Name: "test provider", Type: constant.ChannelTypeTaskPlugin}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Set("group", "default")
			c.Set("username", user.Username)
			c.Set("task_request", map[string]any{"model": "document-model"})
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "document-model")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
			info := taskSubmissionRelayInfo(nil)
			info.UserId = user.Id
			info.OriginModelName = "document-model"
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID = model.GenerateTaskID()
			info.LockedChannel = &ch
			outcome, taskErr := executeTaskSubmission(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			want := common.QuotaRound(tc.count * 0.01 * common.QuotaPerUnit)
			assert.Equal(t, want, outcome.Result.Quota)
			assert.Equal(t, want, info.PriceData.Quota)
			var stored model.Task
			require.NoError(t, db.Where("task_id = ?", info.PublicTaskID).First(&stored).Error)
			assert.Equal(t, want, stored.Quota)
			assert.Equal(t, model.TaskStatus(tc.status), stored.Status)
			assert.Positive(t, stored.FinishTime)
			assert.Equal(t, float64(4), info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup/(0.01*common.QuotaPerUnit))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, stored.PrivateData.BillingContext.TieredSnapshot.UsageFacts["units"])
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota)
			assert.Equal(t, want, updated.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, want, logs[0].Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			if tc.status == "SUCCESS" {
				assert.Equal(t, tc.count, other["usage_facts"].(map[string]any)["units"])
			}
			assert.False(t, c.Writer.Written(), "presentation must follow persistence and settlement")
			require.NoError(t, info.Billing.Settle(want))
			info.Billing.Refund(c)
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-want, updated.Quota, "terminal settlement is idempotent")
		})
	}
}

func TestAcceptedSubmitStreamNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "task_accepted", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: 502, LocalError: true, NoRetry: true}, 3))
}

func TestSubmitWithUnknownOutcomeNeverRetries(t *testing.T) {
	c := taskSubmissionTestContext()
	sent := service.TaskErrorWrapper(fmt.Errorf("do request failed: %w: %w", relaycommon.ErrTaskSubmitOutcomeUnknown, io.ErrUnexpectedEOF), "do_request_failed", http.StatusInternalServerError)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "submit_outcome_unknown", Source: "system"}, decideTaskRetry(c, sent, 3))
	unsent := service.TaskErrorWrapper(fmt.Errorf("do request failed: %w", io.ErrUnexpectedEOF), "do_request_failed", http.StatusInternalServerError)
	assert.Equal(t, "retry", decideTaskRetry(c, unsent, 3).Action, "connection-phase failures still retry")
}

// Local task rejections carry a message but no cause; the response and the
// decision record must still be produced.
func TestRespondTaskSubmissionErrorWithoutCause(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	c := taskSubmissionTestContext()
	taskErr := &dto.TaskError{Code: "get_channel_failed", Message: "no channel", StatusCode: http.StatusServiceUnavailable, LocalError: true}
	require.NotPanics(t, func() { respondTaskSubmissionError(c, taskErr) })
	assert.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
	events := service.RequestPolicy(c).Events()
	require.Len(t, events, 1)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "request_failed", Source: "system"}, events[0].Decision)
	assert.Equal(t, http.StatusServiceUnavailable, events[0].Status)
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "system"}, decideTaskRetry(c, &dto.TaskError{StatusCode: http.StatusForbidden, LocalError: true, Message: "billing"}, 2), "local errors stop once the status rules do not force a retry")
}

func TestExecuteTaskSubmissionRefundsWhenFinalReserveFails(t *testing.T) {
	events := []string{}
	setupTaskSubmissionDatabase(t, true, &events)
	billing := &taskSubmissionTestBilling{events: &events, reserveErr: errors.New("insufficient funds")}
	c := taskSubmissionTestContext()
	info := taskSubmissionRelayInfo(billing)
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{Platform: "plugin", Quota: 600, Immediate: &relaycommon.TaskInfo{Status: "SUCCESS"}}, nil
	})
	require.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	assert.Equal(t, []string{"reserve", "refund"}, events)
	assert.Equal(t, 1, billing.refunds)
	assert.False(t, c.Writer.Written())
}

// A request video scheduling took over leaves an auto group only after
// exhausting it, and its attempt budget does not restart in the next group.
// Any other request keeps the retry-index flow, where each group switch resets
// the index and the budget.
func TestExecuteTaskSubmissionVideoSchedulingAutoGroupBudget(t *testing.T) {
	events := make([]string, 0)
	database := setupTaskSubmissionDatabase(t, true, &events)
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}))
	previousMemory, previousRetry, previousRedis, previousErrorLog := common.MemoryCacheEnabled, common.RetryTimes, common.RedisEnabled, constant.ErrorLogEnabled
	previousSetting := *operation_setting.GetVideoSchedulingSetting()
	previousGroups, previousMaxAuto := setting.UserUsableGroups2JSONString(), setting.GetMaxTokenAutoGroups()
	previousRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		common.MemoryCacheEnabled, common.RetryTimes, common.RedisEnabled, constant.ErrorLogEnabled = previousMemory, previousRetry, previousRedis, previousErrorLog
		*operation_setting.GetVideoSchedulingSetting() = previousSetting
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprint(previousMaxAuto)))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		require.NoError(t, database.Migrator().DropTable(&model.Channel{}, &model.Ability{}))
	})
	common.MemoryCacheEnabled, common.RetryTimes, common.RedisEnabled, constant.ErrorLogEnabled = true, 1, false, false
	operation_setting.GetVideoSchedulingSetting().Mode = operation_setting.VideoSchedulingModeShadow
	operation_setting.GetVideoSchedulingSetting().UnknownSellPolicy = "relative"
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	// The auto groups are vip then default; vip has one channel, default two.
	base := 20000 + int(time.Now().UnixNano()%1_000_000)
	for i, group := range []string{"vip", "default", "default"} {
		priority, weight, autoBan := int64(0), uint(100), 0
		binding := `{"task_plugin_key":"seedance-hjmie"}`
		channel := &model.Channel{Id: base + i, Type: constant.ChannelTypeTaskPlugin, Key: "k", Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("video-%d", i),
			Weight: &weight, Priority: &priority, AutoBan: &autoBan, Models: "videos-fast", Group: group, Setting: &binding,
			OtherSettings: `{"video_scheduling":{"models":{"videos-fast":{"mode":"per_video","prices":{"*":1}}}}}`}
		require.NoError(t, database.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(database))
	}
	model.InitChannelCache()
	generation := pluginruntime.DefaultRegistry.Generation()
	seedance, ok := generation.Get("seedance-hjmie")
	require.True(t, ok)

	for _, tc := range []struct {
		name       string
		decision   service.VideoSchedDecision
		crossGroup bool
		wantGroups []string
		wantStop   string
	}{
		{"takeover crosses only an exhausted group within one budget", service.VideoSchedDecision{Takeover: true}, true, []string{"vip", "default"}, "attempt_budget_exhausted"},
		{"takeover without cross-group retry stops in its group", service.VideoSchedDecision{Takeover: true}, false, []string{"vip"}, "candidates_exhausted"},
		{"shadow keeps the retry-index flow", service.VideoSchedDecision{Shadow: true}, true, []string{"auto", "vip", "default", "default"}, "retry_status_matched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := taskSubmissionTestContext()
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "auto")
			common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
			common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, tc.crossGroup)
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, tc.decision)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: generation, Plugin: seedance})
			c.Set("expected_task_plugin_key", "seedance-hjmie")
			c.Set("task_request", map[string]any{"prompt": "cat", "duration": 5, "resolution": "720p"})
			// The distributor's first selection.
			first, _, selectErr := service.SelectChannelForRequest(c, "videos-fast", &service.RetryParam{Ctx: c, TokenGroup: "auto", ModelName: "videos-fast", Retry: common.GetPointer(0)})
			require.Nil(t, selectErr)
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, first, "videos-fast"))

			info := taskSubmissionRelayInfo(&taskSubmissionTestBilling{events: &events})
			info.ChannelMeta, info.LockedChannel = nil, nil
			info.TokenGroup, info.UsingGroup, info.OriginModelName = "auto", "auto", "videos-fast"
			_, taskErr := executeTaskSubmissionWith(c, info, func(_ *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				info.ChannelMeta = &relaycommon.ChannelMeta{} // as the real submit does
				return nil, service.TaskErrorWrapper(errors.New("bad gateway"), "fail_to_fetch_task", http.StatusBadGateway)
			})
			require.NotNil(t, taskErr)
			if tc.decision.Takeover {
				// Running out of candidates returns the last upstream error.
				assert.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
				assert.Equal(t, "fail_to_fetch_task", taskErr.Code)
			}

			var groups []string
			tried := map[int]bool{}
			lastDecision := ""
			for _, event := range service.RequestPolicy(c).Events() {
				switch event.Decision.Action {
				case "attempt":
					groups = append(groups, event.Group)
					if tc.decision.Takeover {
						assert.False(t, tried[event.ChannelID], "a taken-over request never retries a tried channel")
					}
					tried[event.ChannelID] = true
				case "retry", "stop":
					lastDecision = event.Decision.Reason
				}
			}
			assert.Equal(t, tc.wantGroups, groups)
			assert.Equal(t, tc.wantStop, lastDecision)
		})
	}
}

func TestExecuteTaskSubmissionVideoHealthAdmissionReselection(t *testing.T) {
	service.InitHttpClient()
	database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.User{}, &model.Channel{}, &model.Ability{}, &model.VideoHealthRegistration{}, &model.VideoHealthState{}, &model.VideoHealthAttempt{}, &model.VideoHealthRequest{})
	oldDB, oldMemory, oldRedis, oldRetry := model.DB, common.MemoryCacheEnabled, common.RedisEnabled, common.RetryTimes
	oldConsume, oldErrorLog := common.LogConsumeEnabled, constant.ErrorLogEnabled
	oldSetting, oldPrices := *operation_setting.GetVideoSchedulingSetting(), ratio_setting.ModelPrice2JSONString()
	oldGroupRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled, common.RedisEnabled, common.RetryTimes = oldDB, oldMemory, oldRedis, oldRetry
		common.LogConsumeEnabled, constant.ErrorLogEnabled = oldConsume, oldErrorLog
		*operation_setting.GetVideoSchedulingSetting() = oldSetting
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios))
	})
	model.DB, common.MemoryCacheEnabled, common.RedisEnabled, common.RetryTimes = database, true, false, 0
	common.LogConsumeEnabled, constant.ErrorLogEnabled = false, false
	s := operation_setting.GetVideoSchedulingSetting()
	s.Mode, s.SelectionPolicy, s.WindowSeconds = "on", videosched.PolicyStabilityCostV2, 1800
	s.MinSamples, s.MinGenRate, s.MinOverallRate, s.MinMarginRate = 20, .8, .6, .1
	s.QualificationTTLSeconds, s.ValidationPeriodSeconds = 86400, 604800
	s.ExploreMaxInFlight, s.ExploreShare, s.ProbeRatio = 1, 0, 0
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"admission-model":2}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"conflict_then_success":1,"conflict_budget":1,"storage_error":1,"outcome_unknown":1}`))
	require.NoError(t, database.Create(&model.User{Id: 1, Username: "admission-fixture", Quota: int(20 * common.QuotaPerUnit)}).Error)
	plugin, err := pluginruntime.DefaultRegistry.Register(`
export const meta = {apiVersion:1,key:"admission-loop-test",name:"Admission loop",version:"1.0.0",author:{name:"Test"},models:["admission-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) { return {url:ctx.baseUrl+"/submit",body:ctx.requestBody}; }
export function parseSubmitResponse() { return {taskId:"upstream-task",immediate:{status:"SUCCESS",url:"https://example.com/result.mp4"}}; }
export function buildQueryRequest() { throw new Error("immediate fixture must not poll"); }
export function parseTaskResult() { throw new Error("immediate fixture must not poll"); }
export function describeSpec() { return {spec_version:1,output_seconds:5,resolution:"*",references:{video:0,image:0,audio:0}}; }
`, pluginruntime.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pluginruntime.DefaultRegistry.Unregister(plugin.Meta.Key)) })

	for caseIndex, tc := range []struct {
		name      string
		conflicts int
		wantCalls int
		wantSent  int64
		wantCode  string
	}{
		{"conflict_then_success", 1, 2, 1, ""},
		{"conflict_budget", 4, 3, 0, "video_health_admission_conflict"},
		{"storage_error", 0, 1, 0, "video_health_unavailable"},
		{"outcome_unknown", 0, 1, 1, "do_request_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				sent.Add(1)
				if tc.name == "outcome_unknown" {
					conn, _, hijackErr := w.(http.Hijacker).Hijack()
					if hijackErr == nil {
						_ = conn.Close()
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"upstream-task"}`))
			}))
			defer server.Close()
			base := 31000 + caseIndex*10
			for i := range 4 {
				priority, weight, autoBan := int64(4-i), uint(100), 0
				binding := `{"task_plugin_key":"admission-loop-test"}`
				channel := &model.Channel{Id: base + i, Type: constant.ChannelTypeTaskPlugin, Key: "test", BaseURL: &server.URL,
					Status: common.ChannelStatusEnabled, Name: tc.name, Weight: &weight, Priority: &priority, AutoBan: &autoBan,
					Models: "admission-model", Group: tc.name, Setting: &binding,
					OtherSettings: `{"video_scheduling":{"quality":0.8,"models":{"admission-model":{"mode":"per_video","prices":{"*":1}}}}}`}
				require.NoError(t, database.Create(channel).Error)
				require.NoError(t, channel.AddAbilities(database))
			}
			model.InitChannelCache()
			service.RefreshVideoReliability(t.Context())
			if tc.name == "storage_error" {
				require.NoError(t, database.Callback().Create().Before("gorm:create").Register("test:health-journal-unavailable", func(tx *gorm.DB) {
					if _, ok := tx.Statement.Dest.(*model.VideoHealthAttempt); ok {
						tx.AddError(errors.New("journal unavailable"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, database.Callback().Create().Remove("test:health-journal-unavailable")) })
			}
			c := taskSubmissionTestContext()
			c.Set(common.RequestIdKey, tc.name)
			c.Set("resolved_task_model", "admission-model")
			c.Set("task_request", map[string]any{"prompt": "fixture"})
			c.Set("expected_task_plugin_key", plugin.Meta.Key)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: pluginruntime.DefaultRegistry.Generation(), Plugin: plugin})
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, service.VideoSchedDecision{Takeover: true})
			common.SetContextKey(c, constant.ContextKeyVideoSchedSetting, s)
			t.Cleanup(func() { service.FinishVideoReliabilityRequest(c, false) })
			first, _, selectErr := service.SelectChannelForRequest(c, "admission-model", &service.RetryParam{Ctx: c, TokenGroup: tc.name, ModelName: "admission-model", Retry: common.GetPointer(0)})
			require.Nil(t, selectErr)
			require.NotNil(t, first)
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, first, "admission-model"))
			events := []string{}
			billing := &taskSubmissionTestBilling{events: &events}
			info := taskSubmissionRelayInfo(billing)
			info.ChannelMeta, info.LockedChannel = nil, nil
			info.OriginModelName, info.TokenGroup, info.UsingGroup, info.UserGroup = "admission-model", tc.name, tc.name, "default"
			info.PublicTaskID = "task_" + tc.name
			info.UserQuota = int(20 * common.QuotaPerUnit)
			calls := 0
			outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				calls++
				if calls <= tc.conflicts {
					// Selection succeeded, but another node changed its state
					// before the final durable fence and before HTTP transport.
					require.NoError(t, database.Model(&model.VideoHealthState{}).Where("channel_id = ?", c.GetInt("channel_id")).Update("version", gorm.Expr("version + 1")).Error)
				}
				return relay.RelayTaskSubmit(c, info)
			})
			service.FinishVideoReliabilityRequest(c, false)
			assert.Equal(t, tc.wantCalls, calls)
			assert.Equal(t, tc.wantSent, sent.Load())
			facts, err := model.ListVideoHealthAttempts(t.Context(), tc.name)
			require.NoError(t, err)
			assert.Len(t, facts, int(tc.wantSent), "only real transmissions enter the health denominator")
			if tc.wantCode == "" {
				require.Nil(t, taskErr)
				require.NotNil(t, outcome)
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), outcome.Task.Status)
				require.Len(t, facts, 1)
				assert.Equal(t, 2, facts[0].AttemptSeq, "the rejected selection retains its sequence without a transport fact")
				assert.Equal(t, "success", facts[0].FinalOutcome)
				assert.Equal(t, []string{"reserve", "settle"}, events)
				assert.Zero(t, billing.refunds)
			} else {
				require.NotNil(t, taskErr)
				assert.Equal(t, tc.wantCode, taskErr.Code)
				assert.Nil(t, outcome)
				assert.Equal(t, []string{"refund"}, events)
				if tc.name == "outcome_unknown" {
					assert.ErrorIs(t, taskErr.Error, relaycommon.ErrTaskSubmitOutcomeUnknown)
				}
			}
			records := service.VideoScheduleRecords(c)
			require.Len(t, records, tc.wantCalls)
			for i := range min(tc.conflicts, tc.wantCalls) {
				assert.Equal(t, "admission_conflict", records[i].Admission)
				assert.Equal(t, i+1, records[i].AttemptSeq)
				channel, err := model.CacheGetChannel(records[i].Recommended)
				require.NoError(t, err)
				health, err := service.GetVideoHealthView(channel, false)
				require.NoError(t, err)
				assert.Zero(t, health.ValidationSlotsHeld, "an unsent conflict releases its validation reservation")
			}
			if tc.name == "outcome_unknown" {
				channel, err := model.CacheGetChannel(records[0].Recommended)
				require.NoError(t, err)
				health, err := service.GetVideoHealthView(channel, false)
				require.NoError(t, err)
				assert.Equal(t, 1, health.ValidationSlotsHeld, "an unknown upstream receipt retains its reservation")
			}
			for _, event := range service.RequestPolicy(c).Events() {
				if event.Attempt <= tc.conflicts && event.Decision.Action == "failure" {
					assert.Equal(t, "local", event.ErrorSource)
				}
			}
		})
	}
}
