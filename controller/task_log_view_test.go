package controller

import (
	"errors"
	"fmt"
	"math"
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

func TestTaskLogChannelNamesAreAdminOnlyAndFollowChannelUpdates(t *testing.T) {
	task := setupGenericTaskTest(t)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", task.ChannelId).Update("name", "破晓站").Error)
	other := &model.Channel{Id: 2, Name: "备用渠道", Key: "private-channel-key"}
	require.NoError(t, model.DB.Create(other).Error)
	tasks := []*model.Task{task, {ChannelId: other.Id}, {ChannelId: task.ChannelId}, {ChannelId: 404}, {}}
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		views := tasksToDto(tasks, false, role)
		require.Len(t, views, len(tasks))
		for i, view := range views {
			encoded, err := common.Marshal(view)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private-channel-key")
			if role < common.RoleAdminUser {
				assert.NotContains(t, string(encoded), "channel_name")
			} else {
				assert.Equal(t, []string{"破晓站", "备用渠道", "破晓站", "", ""}[i], view.ChannelName)
				assert.Equal(t, tasks[i].ChannelId, view.ChannelId)
			}
		}
	}
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", task.ChannelId).Update("name", "破晓站新名称").Error)
	assert.Equal(t, "破晓站新名称", tasksToDto(tasks, false, common.RoleAdminUser)[0].ChannelName)
	require.NoError(t, model.DB.Delete(other).Error)
	deleted := tasksToDto(tasks, false, common.RoleAdminUser)[1]
	assert.Empty(t, deleted.ChannelName)
	assert.Equal(t, other.Id, deleted.ChannelId)
}

func TestTaskLogOrdinaryVideoRequestDisplayFacts(t *testing.T) {
	seed := setupGenericTaskTest(t)
	var tasks []*model.Task
	for index, tc := range []struct {
		name, body, contentType, action string
		seconds                         float64
		resolution, mode                string
	}{
		{"per-call JSON", `{"model":"public-video","prompt":"private prompt","seconds":"15","resolution":"720p"}`, "application/json", constant.TaskActionTextToVideo, 15, "720p", "text_to_video"},
		{"canvas metadata and image", `{"duration":5,"metadata":{"resolution":"1080p","content":[{"type":"image_url","image_url":{"url":"https://private.example/image.png"}}]}}`, "application/json", constant.TaskActionReferenceToVideo, 5, "1080p", "image_to_video"},
		{"video reference takes precedence", `{"seconds":8,"size":"720x1280","images":["https://private.example/image.png"],"videos":["https://private.example/video.mp4"]}`, "application/json", constant.TaskActionReferenceToVideo, 8, "720p", "reference_to_video"},
		{"multipart saved fields", "model=public-video\nprompt=private prompt\nseconds=10\nresolution=2160p\ninput_reference=[file image.png, 200 bytes]\n", "multipart/form-data; boundary=original", constant.TaskActionImageToVideo, 10, "4k", "image_to_video"},
		{"nested parameters", `{"parameters":{"duration":6,"resolution":"480p"},"input":{"first_image_url":"https://private.example/image.png"}}`, "application/json", constant.TaskActionReferenceToVideo, 6, "480p", "image_to_video"},
		{"missing saved request", "", "application/json", constant.TaskActionTextToVideo, 0, "", ""},
		{"truncated saved request", `{"seconds":15,"resolution":`, "application/json", constant.TaskActionTextToVideo, 0, "", ""},
		{"invalid display quantities", `{"seconds":18446744073686646784,"resolution":"https://private.example/leak"}`, "application/json", constant.TaskActionTextToVideo, 0, "", "text_to_video"},
		{"conflicting scalar aliases", `{"seconds":5,"duration":10,"resolution":"720p","metadata":{"resolution":"1080p"}}`, "application/json", constant.TaskActionTextToVideo, 0, "", "text_to_video"},
		{"non-video task is not interpreted", `{"seconds":15,"resolution":"720p"}`, "application/json", "MUSIC", 0, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{TaskID: fmt.Sprintf("display-%d", index), UserId: seed.UserId, Action: tc.action, Platform: "seedance-hjmie", Quota: 123,
				PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{PerCallBilling: true, OtherRatios: map[string]float64{"requests": 1}}}}
			require.NoError(t, model.DB.Create(task).Error)
			tasks = append(tasks, task)
			if tc.body != "" {
				require.NoError(t, model.DB.Create(&model.TaskRequestBody{TaskRowID: task.ID, ContentType: tc.contentType, Body: tc.body}).Error)
			}
			for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser} {
				view := tasksToDto([]*model.Task{task}, false, role)[0]
				assert.Equal(t, tc.seconds, view.Seconds)
				assert.Equal(t, tc.resolution, view.Resolution)
				assert.Equal(t, tc.mode, view.VideoMode)
				assert.Nil(t, view.ReferenceSeconds, "request fields cannot invent measured reference seconds")
				assert.Equal(t, 123, view.Quota)
				encoded, err := common.Marshal(view)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), "private prompt")
				assert.NotContains(t, string(encoded), "private.example")
			}
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, map[string]float64{"requests": 1}, persisted.PrivateData.BillingContext.OtherRatios)
			assert.Nil(t, persisted.PrivateData.BillingContext.TieredSnapshot)
		})
	}
	// A mixed page must match request bodies by task row, including missing bodies.
	views := tasksToDto(tasks, false, common.RoleCommonUser)
	require.Len(t, views, len(tasks))
	assert.Equal(t, 15.0, views[0].Seconds)
	assert.Equal(t, "1080p", views[1].Resolution)
	assert.Equal(t, "reference_to_video", views[2].VideoMode)
	assert.Empty(t, views[5].Resolution)
	assert.Zero(t, views[len(views)-1].Seconds, "non-video tasks must remain excluded in a mixed page")

	// Existing snapshots remain authoritative, even when the saved client body differs.
	seed.Action = constant.TaskActionTextToVideo
	seed.PrivateData.BillingContext = &model.TaskBillingContext{TieredSnapshot: &billingexpr.BillingSnapshot{
		UsageFacts: map[string]any{"seconds": 7.0, "resolution": "480p", "input_video_seconds": 2.5},
	}}
	require.NoError(t, model.DB.Create(&model.TaskRequestBody{TaskRowID: seed.ID, ContentType: "application/json", Body: `{"seconds":15,"resolution":"1080p"}`}).Error)
	view := tasksToDto([]*model.Task{seed}, false, common.RoleCommonUser)[0]
	assert.Equal(t, 7.0, view.Seconds)
	assert.Equal(t, "480p", view.Resolution)
	assert.Equal(t, "reference_to_video", view.VideoMode)
	require.NotNil(t, view.ReferenceSeconds)
	assert.Equal(t, 2.5, *view.ReferenceSeconds)
}

func TestTaskLogVideoInputsPreserveKnownAndUnknownFacts(t *testing.T) {
	measured, billed, zero, invalid := 2.25, 10.0, 0.0, math.NaN()
	for _, tc := range []struct {
		name    string
		spec    *model.VideoSpecView
		facts   map[string]any
		mode    string
		seconds any
	}{
		{name: "historical task stays unknown"},
		{name: "text video without references", spec: &model.VideoSpecView{References: map[string]int{}}, mode: "text_to_video", seconds: zero},
		{name: "image video has no input video duration", spec: &model.VideoSpecView{References: map[string]int{"image": 1}}, mode: "image_to_video", seconds: zero},
		{name: "first and last frames are image inputs", spec: &model.VideoSpecView{References: map[string]int{"frame": 2}}, mode: "image_to_video", seconds: zero},
		{name: "audio alone does not mean reference video", spec: &model.VideoSpecView{References: map[string]int{"audio": 1}}, mode: "text_to_video", seconds: zero},
		{name: "video reference with no measured duration", spec: &model.VideoSpecView{References: map[string]int{"video": 1}}, mode: "reference_to_video"},
		{name: "image and video references prioritize video even without measured duration", spec: &model.VideoSpecView{References: map[string]int{"image": 1, "video": 1}}, mode: "reference_to_video"},
		{name: "measured input duration from scheduling", spec: &model.VideoSpecView{References: map[string]int{"video": 1}, InputVideoSeconds: &measured}, mode: "reference_to_video", seconds: measured},
		{name: "frozen measured duration wins over scheduling and billable duration", spec: &model.VideoSpecView{References: map[string]int{"image": 1, "video": 1}, InputVideoSeconds: &billed}, facts: map[string]any{"input_video_seconds": measured, "input_video_billable_seconds": billed}, mode: "reference_to_video", seconds: measured},
		{name: "measured video duration overrides an incomplete image spec", spec: &model.VideoSpecView{References: map[string]int{"image": 1}}, facts: map[string]any{"input_video_seconds": measured}, mode: "reference_to_video", seconds: measured},
		{name: "billing facts recover reference video without scheduling", facts: map[string]any{"input_video_seconds": measured}, mode: "reference_to_video", seconds: measured},
		{name: "zero video duration does not imply no reference images", facts: map[string]any{"input_video_seconds": zero}, seconds: zero},
		{name: "invalid duration stays unknown", spec: &model.VideoSpecView{References: map[string]int{"video": 1}, InputVideoSeconds: &invalid}, mode: "reference_to_video"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{TaskID: "public-video", Action: "GENERATE"}
			if tc.spec != nil {
				task.PrivateData.SchedulingSummary = &model.TaskSchedulingSummary{Spec: tc.spec}
			}
			if tc.facts != nil {
				task.PrivateData.BillingContext = &model.TaskBillingContext{TieredSnapshot: &billingexpr.BillingSnapshot{UsageFacts: tc.facts}}
			}
			for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser} {
				view := tasksToDto([]*model.Task{task}, false, role)[0]
				encoded, err := common.Marshal(view)
				require.NoError(t, err)
				var payload map[string]any
				require.NoError(t, common.Unmarshal(encoded, &payload))
				assert.Equal(t, tc.mode, view.VideoMode)
				assert.NotContains(t, payload, "has_reference")
				assert.Equal(t, tc.seconds, payload["reference_seconds"])
				assert.NotContains(t, payload, "private_data")
			}
		})
	}
}

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
