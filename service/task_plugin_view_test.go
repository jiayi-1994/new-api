package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
			assert.Equal(t, "Video generation failed", video.Error.Message)
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
