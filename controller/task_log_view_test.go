package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedVideoTaskViewsHideUpstreamDetails(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Properties = model.Properties{OriginModelName: "public-video", UpstreamModelName: "private-upstream-video"}
	task.Platform = "private-plugin"
	task.Data = []byte(`{"provider":"private-provider","url":"https://private-upstream.invalid/result"}`)
	task.PrivateData.ResultURL = "https://private-upstream.invalid/result"
	for _, unified := range []bool{false, true} {
		if unified {
			task.PrivateData.BillingContext = &model.TaskBillingContext{TieredSnapshot: &billingexpr.BillingSnapshot{
				SalesSource: billingexpr.SalesSourceVideoRequest,
				UsageFacts:  map[string]any{"seconds": 7.0, "resolution": "720p"},
			}}
		}
		require.NoError(t, model.DB.Save(task).Error)
		for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
			item := tasksToDto([]*model.Task{task}, false, role)[0]
			properties, ok := item.Properties.(model.Properties)
			require.True(t, ok)
			assert.Equal(t, "public-video", properties.OriginModelName)
			if unified && role < common.RoleAdminUser {
				assert.Empty(t, properties.UpstreamModelName)
				assert.Equal(t, "video", item.Platform)
				assert.Zero(t, item.ChannelId)
				assert.Empty(t, item.Data)
			} else {
				assert.Equal(t, "private-upstream-video", properties.UpstreamModelName)
				assert.Equal(t, "private-plugin", item.Platform)
				assert.Equal(t, task.ChannelId, item.ChannelId)
				assert.Equal(t, task.Data, item.Data)
			}
		}
		assert.Equal(t, "private-upstream-video", task.Properties.UpstreamModelName, "views must not mutate the persisted task")
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", task.UserId)
		c.Set("role", common.RoleRootUser)
		c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/video/generations/"+task.TaskID, nil)
		require.Nil(t, relay.RelayTaskFetch(c, relayconstant.RelayModeVideoFetchByID))
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Contains(t, recorder.Body.String(), "public-video")
		if unified {
			assert.NotContains(t, recorder.Body.String(), "private-upstream-video", "token responses omit upstream names even for an administrator's token")
			assert.NotContains(t, recorder.Body.String(), "private-plugin")
			assert.NotContains(t, recorder.Body.String(), "private-provider")
			assert.NotContains(t, recorder.Body.String(), "private-upstream.invalid")
		} else {
			assert.Contains(t, recorder.Body.String(), "private-upstream-video")
		}
	}
	// Public retrieval does not depend on an installed upstream plugin and
	// never exposes provider errors or raw response extensions.
	for _, status := range []model.TaskStatus{model.TaskStatusInProgress, model.TaskStatusSuccess, model.TaskStatusFailure} {
		task.Status = status
		task.FailReason = "private-plugin: private-provider at private-upstream.invalid"
		require.NoError(t, model.DB.Save(task).Error)
		for _, endpoint := range []string{"/v1/tasks/", "/v1/videos/", "/v1/video/generations/"} {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("id", task.UserId)
			c.Set("role", common.RoleRootUser)
			c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}, {Key: "key", Value: task.TaskID}}
			c.Request = httptest.NewRequest(http.MethodGet, endpoint+task.TaskID, nil)
			if endpoint == "/v1/tasks/" {
				GetTask(c)
			} else {
				require.Nil(t, relay.RelayTaskFetch(c, relayconstant.RelayModeVideoFetchByID))
			}
			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "private-", endpoint)
			assert.Contains(t, recorder.Body.String(), task.TaskID)
			if status == model.TaskStatusFailure {
				assert.Contains(t, recorder.Body.String(), "Video generation failed")
			}
			if endpoint == "/v1/videos/" {
				assert.Contains(t, recorder.Body.String(), `"seconds":"7"`)
				assert.Contains(t, recorder.Body.String(), `"resolution":"720p"`)
			}
		}
		view := tasksToDto([]*model.Task{task}, false, common.RoleCommonUser)[0]
		encoded, err := common.Marshal(view)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "private-")
		adminView := tasksToDto([]*model.Task{task}, false, common.RoleAdminUser)[0]
		assert.Equal(t, task.FailReason, adminView.FailReason)
		assert.Equal(t, "private-plugin", string(task.Platform))
		assert.Contains(t, string(task.Data), "private-provider")
	}
}

func TestUnifiedVideoErrorsKeepDiagnosticsOutOfUserViews(t *testing.T) {
	task := setupGenericTaskTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Log{}))
	t.Cleanup(func() { require.NoError(t, model.DB.Migrator().DropTable(&model.Log{})) })
	previousLogDB, previousLogType := model.LOG_DB, common.LogDatabaseType()
	previousEnabled := constant.ErrorLogEnabled
	model.LOG_DB = model.DB
	common.SetLogDatabaseType(common.MainDatabaseType())
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		model.LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousLogType)
		constant.ErrorLogEnabled = previousEnabled
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	c.Set("id", task.UserId)
	c.Set("token_id", 99)
	c.Set("original_model", "public-video")
	c.Set("group", "default")
	c.Set(common.UpstreamRequestIdKey, "private-upstream-request")
	service.SetVideoSalesFacts(c, service.VideoSalesFacts{Model: "public-video", Seconds: 7, Resolution: "720p", USDPerSecond: 1})
	err := errors.New("private-plugin private-provider https://private-upstream.invalid")
	apiErr := relaytypes.NewOpenAIError(err, "private-provider-error", http.StatusBadGateway)
	service.ProcessChannelError(c, relaytypes.ChannelError{ChannelId: task.ChannelId}, apiErr, nil)

	userLogs, queryErr := model.GetLogByTokenId(99)
	require.NoError(t, queryErr)
	require.Len(t, userLogs, 1)
	assert.Equal(t, "Video request failed", userLogs[0].Content)
	userJSON, marshalErr := common.Marshal(userLogs)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(userJSON), "private-")
	assert.Zero(t, userLogs[0].ChannelId)
	var adminLogs []*model.Log
	require.NoError(t, model.LOG_DB.Find(&adminLogs).Error)
	model.FormatAdminLogs(adminLogs)
	require.Len(t, adminLogs, 1)
	assert.Contains(t, adminLogs[0].Content, "private-provider")
	assert.Contains(t, adminLogs[0].Other, "private-provider-error")
	assert.Equal(t, task.ChannelId, adminLogs[0].ChannelId)

	taskErr := service.TaskErrorWrapper(err, "private-plugin-error", http.StatusBadGateway)
	taskErr.Data = map[string]any{"provider": "private-provider"}
	respondTaskSubmissionError(c, taskErr)
	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "private-")
	assert.Contains(t, recorder.Body.String(), "Video request failed")
	assert.Equal(t, "private-plugin-error", taskErr.Code, "public projection must not mutate diagnostics")
	assert.Contains(t, taskErr.Message, "private-provider")
}

func TestUnifiedVideoCreateReceiptUsesOnlyPublicFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{
		Protocol: "openai_video", Operation: pluginruntime.HostProtocolOperation{Name: "create"},
	})
	task := &model.Task{
		TaskID: "task_public", Platform: "private-plugin", Status: model.TaskStatusSubmitted, CreatedAt: 456,
		Properties: model.Properties{OriginModelName: "public-video", UpstreamModelName: "private-model"},
		Data:       []byte(`{"provider":"private-provider"}`),
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{TieredSnapshot: &billingexpr.BillingSnapshot{
			SalesSource: billingexpr.SalesSourceVideoRequest, UsageFacts: map[string]any{"seconds": 7, "resolution": "720p"},
		}}},
	}
	presentTaskSubmission(c, &taskSubmissionOutcome{Result: &relay.TaskSubmitResult{}, Task: task, RelayInfo: &relaycommon.RelayInfo{}})
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "private-")
	assert.Contains(t, recorder.Body.String(), `"seconds":"7"`)
	assert.Contains(t, recorder.Body.String(), `"resolution":"720p"`)
	assert.Contains(t, recorder.Body.String(), `"model":"public-video"`)
}

func TestTaskLogDTOSeparatesUserAdminAndRootDetails(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_public",
		Platform: "document-parser",
		PrivateData: model.TaskPrivateData{
			Key:            "channel-secret-canary",
			UpstreamTaskID: "upstream-private",
			NodeName:       "node-a",
			Execution: &model.TaskExecutionSnapshot{
				RequestID:   "request-public",
				RequestPath: "/v1/documents",
				TaskPlugin: &model.TaskPluginSnapshot{
					Key:     "document-parser",
					Name:    "Document Parser",
					Version: "1.2.3",
					Author: &model.TaskPluginAuthorSnapshot{
						Name: "Community Author",
						URL:  "https://plugins.example/author",
					},
					APIVersion: 1,
					Generation: 42,
				},
			},
		},
	}

	userView := tasksToDto([]*model.Task{task}, false, common.RoleCommonUser)[0]
	assert.Nil(t, userView.AdminInfo)
	assert.Nil(t, userView.RootInfo)

	adminView := tasksToDto([]*model.Task{task}, false, common.RoleAdminUser)[0]
	require.NotNil(t, adminView.AdminInfo)
	require.NotNil(t, adminView.AdminInfo.TaskPlugin)
	assert.Equal(t, "document-parser", adminView.AdminInfo.TaskPlugin.Key)
	assert.Equal(t, "Document Parser", adminView.AdminInfo.TaskPlugin.Name)
	assert.Equal(t, "1.2.3", adminView.AdminInfo.TaskPlugin.Version)
	require.NotNil(t, adminView.AdminInfo.TaskPlugin.Author)
	assert.Equal(t, "Community Author", adminView.AdminInfo.TaskPlugin.Author.Name)
	assert.Equal(t, "https://plugins.example/author", adminView.AdminInfo.TaskPlugin.Author.URL)
	assert.Equal(t, "request-public", adminView.AdminInfo.RequestID)
	assert.Equal(t, "/v1/documents", adminView.AdminInfo.RequestPath)
	assert.Nil(t, adminView.RootInfo)

	rootView := tasksToDto([]*model.Task{task}, false, common.RoleRootUser)[0]
	require.NotNil(t, rootView.AdminInfo)
	require.NotNil(t, rootView.RootInfo)
	require.NotNil(t, rootView.RootInfo.TaskPlugin)
	assert.Equal(t, 1, rootView.RootInfo.TaskPlugin.APIVersion)
	assert.Equal(t, uint64(42), rootView.RootInfo.TaskPlugin.Generation)
	assert.Equal(t, "upstream-private", rootView.RootInfo.UpstreamTaskID)
	assert.Equal(t, "node-a", rootView.RootInfo.NodeName)

	adminJSON, err := common.Marshal(adminView)
	require.NoError(t, err)
	assert.NotContains(t, string(adminJSON), "channel-secret-canary")
	assert.NotContains(t, string(adminJSON), "upstream-private")

	rootJSON, err := common.Marshal(rootView)
	require.NoError(t, err)
	assert.NotContains(t, string(rootJSON), "channel-secret-canary")
	assert.Contains(t, string(rootJSON), "upstream-private")
}

func TestTaskLogDTODoesNotInventHistoricalPluginProvenance(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_without_snapshot",
		Platform: "document-parser",
	}

	adminView := tasksToDto([]*model.Task{task}, false, common.RoleAdminUser)[0]

	assert.Nil(t, adminView.AdminInfo)
	assert.Nil(t, adminView.RootInfo)
}

func TestTaskLogDTOReplacesLegacyVideoURLWithAvailabilityFlag(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_legacy_video",
		Platform:   "jimeng",
		Action:     constant.TaskActionTextToVideo,
		Status:     model.TaskStatusSuccess,
		FailReason: "https://private-upstream.invalid/video.mp4?signature=secret",
	}

	view := tasksToDto([]*model.Task{task}, false, common.RoleCommonUser)[0]
	assert.True(t, view.LegacyVideoAvailable)
	assert.Empty(t, view.ResultURL)
	assert.Empty(t, view.FailReason)
	encoded, err := common.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private-upstream.invalid")
	assert.NotContains(t, string(encoded), "result_url")
	assert.Contains(t, string(encoded), "legacy_video_available")
}

func TestTaskLogDTOKeepsFailureReasonAndDoesNotMarkPluginTaskLegacy(t *testing.T) {
	failed := &model.Task{
		TaskID:     "task_failed",
		Platform:   "jimeng",
		Action:     constant.TaskActionTextToVideo,
		Status:     model.TaskStatusFailure,
		FailReason: "provider rejected the request",
	}
	failedView := tasksToDto([]*model.Task{failed}, false, common.RoleCommonUser)[0]
	assert.Equal(t, "provider rejected the request", failedView.FailReason)
	assert.False(t, failedView.LegacyVideoAvailable)

	pluginTask := &model.Task{
		TaskID:     "task_plugin_video",
		Platform:   "community-video",
		Action:     constant.TaskActionTextToVideo,
		Status:     model.TaskStatusSuccess,
		FailReason: "https://stale-upstream.invalid/plugin-video.mp4",
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://private-upstream.invalid/plugin-video.mp4",
			Execution: &model.TaskExecutionSnapshot{
				TaskPlugin: &model.TaskPluginSnapshot{Key: "community-video"},
			},
		},
	}
	pluginView := tasksToDto([]*model.Task{pluginTask}, false, common.RoleCommonUser)[0]
	assert.False(t, pluginView.LegacyVideoAvailable)
	assert.Empty(t, pluginView.ResultURL)
	assert.Empty(t, pluginView.FailReason)
}
