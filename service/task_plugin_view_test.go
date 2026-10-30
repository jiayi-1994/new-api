package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
)

type taskViewFailureClassifier struct {
	TaskPollingAdaptor
	class string
	valid bool
}

func (a *taskViewFailureClassifier) ClassifyFailure(string) (string, bool) {
	return a.class, a.valid
}

type taskViewOriginRenderer struct {
	TaskPollingAdaptor
	rendered string
	err      error
}

func (a *taskViewOriginRenderer) ConvertToOpenAIVideo(*model.Task) ([]byte, error) {
	return []byte(a.rendered), a.err
}

func TestUnifiedVideoResponseExposesOnlyPluginMappedOriginFields(t *testing.T) {
	previousFactory := GetTaskAdaptorFunc
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
	unified := model.TaskPrivateData{BillingContext: &model.TaskBillingContext{TieredSnapshot: &billingexpr.BillingSnapshot{
		SalesSource: billingexpr.SalesSourceVideoRequest, UsageFacts: map[string]any{"seconds": 5},
	}}}
	for _, tc := range []struct {
		name     string
		status   model.TaskStatus
		adaptor  TaskPollingAdaptor
		rendered string
		err      error
		want     map[string]any
	}{
		{name: "mapped origin fields are lifted, other renderer keys are not", status: model.TaskStatusSuccess,
			rendered: `{"url":"https://vendor.example/v.mp4","volcesVideoUrl":"https://ark.tos.volces.com/v.mp4?sig=1","volcesCgt":"cgt-1","credits":9}`,
			want:     map[string]any{"volcesVideoUrl": "https://ark.tos.volces.com/v.mp4?sig=1", "volcesCgt": "cgt-1"}},
		{name: "empty and non-string values are omitted", status: model.TaskStatusSuccess,
			rendered: `{"volcesVideoUrl":"","volcesCgt":{"id":1}}`},
		{name: "renderer failure leaves the sale intact", status: model.TaskStatusSuccess, err: errors.New("render failed")},
		{name: "adaptor without a renderer", status: model.TaskStatusSuccess, adaptor: &taskViewFailureClassifier{}},
		{name: "unfinished tasks expose nothing", status: model.TaskStatusInProgress,
			rendered: `{"volcesVideoUrl":"https://ark.tos.volces.com/v.mp4"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
				if tc.adaptor != nil {
					return tc.adaptor
				}
				return &taskViewOriginRenderer{rendered: tc.rendered, err: tc.err}
			}
			task := &model.Task{TaskID: "task_public", Status: tc.status, PrivateData: unified, Data: []byte(`{"data":{"volcesVideoUrl":"private"}}`)}
			video := BuildUnifiedVideoResponse(task)
			want := map[string]any{}
			if tc.status == model.TaskStatusSuccess {
				want["url"] = "/v1/videos/task_public/content"
			}
			for key, value := range tc.want {
				want[key] = value
			}
			if len(want) == 0 {
				assert.Empty(t, video.Metadata)
				return
			}
			assert.Equal(t, want, video.Metadata)
		})
	}
}

func TestUnifiedVideoFailureCodesPreserveAttributionWithoutPrivateDetails(t *testing.T) {
	previousFactory := GetTaskAdaptorFunc
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
	for _, tc := range []struct {
		name        string
		class       string
		valid       bool
		attribution string
		reason      string
		noFactory   bool
		noPlugin    bool
		wantCode    string
	}{
		{name: "user rejection", class: VideoFailureUser, valid: true, wantCode: "video_request_rejected"},
		{name: "cancellation", class: VideoFailureCancelled, valid: true, wantCode: "video_generation_cancelled"},
		{name: "upstream failure", class: VideoFailureUpstream, valid: true, wantCode: "video_generation_failed"},
		{name: "unknown class", class: "private-vendor-class", valid: true, wantCode: "video_generation_failed"},
		{name: "invalid classification", class: VideoFailureUser, wantCode: "video_generation_failed"},
		{name: "missing factory", noFactory: true, wantCode: "video_generation_failed"},
		{name: "missing plugin", noPlugin: true, wantCode: "video_generation_failed"},
		{name: "host failure", class: VideoFailureUser, valid: true, attribution: "host", wantCode: "video_generation_failed"},
		{name: "host lag", class: VideoFailureCancelled, valid: true, attribution: taskAttributionHostLag, wantCode: "video_generation_failed"},
		{name: "legacy poll failure", class: VideoFailureUser, valid: true, reason: "poll failed: unrecognized_status; body=content policy violation", wantCode: "video_generation_failed"},
		{name: "legacy missing task", class: VideoFailureUser, valid: true, reason: "upstream task not found (HTTP 404)", wantCode: "video_generation_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
				if tc.noPlugin {
					return nil
				}
				return &taskViewFailureClassifier{class: tc.class, valid: tc.valid}
			}
			if tc.noFactory {
				GetTaskAdaptorFunc = nil
			}
			reason := tc.reason
			if reason == "" {
				reason = "private-vendor moderation detail, credential=private-secret"
			}
			task := &model.Task{
				TaskID: "task_public_failure", Status: model.TaskStatusFailure,
				FailReason: reason, VideoHealthAttribution: tc.attribution,
				Properties: model.Properties{OriginModelName: "seedance-2.0", UpstreamModelName: "private-vendor-model"},
				PrivateData: model.TaskPrivateData{
					UpstreamTaskID: "private-task-id", Key: "private-key",
					BillingContext: &model.TaskBillingContext{TieredSnapshot: &billingexpr.BillingSnapshot{
						SalesSource: billingexpr.SalesSourceVideoRequest,
						UsageFacts:  map[string]any{"seconds": 5, "resolution": "720p"},
					}},
				},
				Data: []byte(`{"error":{"message":"private-upstream-message"}}`),
			}
			video := BuildUnifiedVideoResponse(task)
			require.NotNil(t, video.Error)
			assert.Equal(t, tc.wantCode, video.Error.Code)
			wantMessage := "❌ 本次视频生成未能完成，请稍后重新提交；如持续失败，请联系管理员协助处理。"
			if tc.wantCode == "video_request_rejected" {
				wantMessage = "❌ 本次视频生成未能完成，提示词或参考素材可能未通过安全审核，或不符合视频生成要求，请调整后重新提交。"
			}
			assert.Equal(t, wantMessage, video.Error.Message)
			item := &dto.TaskDto{FailReason: reason}
			RedactUnifiedVideoTaskDTO(task, item)
			assert.Equal(t, wantMessage, item.FailReason)
			assert.Equal(t, "failed", video.Status)
			assert.Equal(t, "5", video.Seconds)
			encoded, err := common.Marshal(video)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private-")
			assert.NotContains(t, string(encoded), reason)
			assert.Equal(t, reason, task.FailReason, "presenting a failure must preserve the internal task")
		})
	}
}

func TestBuildTaskPluginViewRewritesOnlyStructuredTaskIDFields(t *testing.T) {
	const (
		privateTaskID = "upstream-task-123"
		publicTaskID  = "task_public_123"
		resultURL     = "https://cdn.example.com/results/upstream-task-123/video.mp4"
	)

	taskData, err := common.Marshal(map[string]any{
		"task_id": privateTaskID,
		"id":      privateTaskID,
		"taskId":  privateTaskID,
		"url":     resultURL,
		"message": "completed upstream-task-123",
		"nested": []any{
			map[string]any{
				"task_id": privateTaskID,
				"url":     resultURL,
			},
			privateTaskID,
		},
		privateTaskID: "opaque map key",
	})
	require.NoError(t, err)
	task := &model.Task{
		TaskID: publicTaskID,
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: privateTaskID,
		},
		Data: taskData,
	}

	view, err := BuildTaskPluginView(task)
	require.NoError(t, err)

	data, ok := view.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, publicTaskID, data["task_id"])
	assert.Equal(t, publicTaskID, data["id"])
	assert.Equal(t, publicTaskID, data["taskId"])
	assert.Equal(t, resultURL, data["url"])
	assert.Equal(t, "completed upstream-task-123", data["message"])
	assert.Equal(t, "opaque map key", data[privateTaskID])

	nested, ok := data["nested"].([]any)
	require.True(t, ok)
	nestedData, ok := nested[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, publicTaskID, nestedData["task_id"])
	assert.Equal(t, resultURL, nestedData["url"])
	assert.Equal(t, privateTaskID, nested[1])

}

func TestBuildTaskPluginViewOmitsPrivatePollState(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public_view",
		Data:   []byte(`{"ok":true}`),
		PrivateData: model.TaskPrivateData{
			PluginState:  []byte(`{"req_key":"secret"}`),
			PollFailures: 7,
		},
	}

	view, err := BuildTaskPluginView(task)
	require.NoError(t, err)
	encoded, err := common.Marshal(view)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.NotContains(t, payload, "plugin_state")
	assert.NotContains(t, payload, "poll_failures")
	assert.NotContains(t, payload, "private_data")
}

func TestTaskVideoFactsPrefersBillingFactsThenSchedulingSpec(t *testing.T) {
	five, fifteen := 5.0, 15.0
	spec := &model.TaskSchedulingSummary{Spec: &model.VideoSpecView{OutputSeconds: &fifteen, Tier: "1080p"}}
	for _, tc := range []struct {
		name           string
		private        model.TaskPrivateData
		wantSeconds    float64
		wantResolution string
	}{
		{name: "usage facts win", private: model.TaskPrivateData{
			BillingContext:    &model.TaskBillingContext{TieredSnapshot: &billingexpr.BillingSnapshot{UsageFacts: map[string]any{"seconds": five, "resolution": "720p"}}},
			SchedulingSummary: spec,
		}, wantSeconds: 5, wantResolution: "720p"},
		{name: "scheduling spec fills gaps", private: model.TaskPrivateData{SchedulingSummary: spec}, wantSeconds: 15, wantResolution: "1080p"},
		{name: "legacy other ratio seconds", private: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{OtherRatios: map[string]float64{"seconds": 8}},
		}, wantSeconds: 8},
		{name: "unsafe resolution is dropped", private: model.TaskPrivateData{
			SchedulingSummary: &model.TaskSchedulingSummary{Spec: &model.VideoSpecView{Tier: "https://x.invalid/a"}},
		}},
		{name: "non-video task has no facts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seconds, resolution := TaskVideoFacts(&model.Task{PrivateData: tc.private})
			assert.Equal(t, tc.wantSeconds, seconds)
			assert.Equal(t, tc.wantResolution, resolution)
		})
	}
}
