package sora

import (
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestTaskAdaptorParseTaskResultMapsNonTerminalStatuses(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus string
		expectedStatus model.TaskStatus
	}{
		{name: "unknown remains queued", upstreamStatus: "unknown", expectedStatus: model.TaskStatusQueued},
		{name: "submitted remains submitted", upstreamStatus: "submitted", expectedStatus: model.TaskStatusSubmitted},
		{name: "created remains submitted", upstreamStatus: "created", expectedStatus: model.TaskStatusSubmitted},
		{name: "status is normalized", upstreamStatus: "  UNKNOWN  ", expectedStatus: model.TaskStatusQueued},
		{name: "processing is unchanged", upstreamStatus: "processing", expectedStatus: model.TaskStatusInProgress},
		{name: "running maps to in progress", upstreamStatus: "RUNNING", expectedStatus: model.TaskStatusInProgress},
		{name: "completed is unchanged", upstreamStatus: "completed", expectedStatus: model.TaskStatusSuccess},
		{name: "succeeded maps to success", upstreamStatus: "succeeded", expectedStatus: model.TaskStatusSuccess},
		{name: "success maps to success", upstreamStatus: "success", expectedStatus: model.TaskStatusSuccess},
		{name: "failed is unchanged", upstreamStatus: "failed", expectedStatus: model.TaskStatusFailure},
		{name: "canceled single-l maps to failure", upstreamStatus: "canceled", expectedStatus: model.TaskStatusFailure},
	}

	adaptor := &TaskAdaptor{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{"status": test.upstreamStatus})
			require.NoError(t, err)

			taskInfo, err := adaptor.ParseTaskResult(body)
			require.NoError(t, err)
			assert.Equal(t, string(test.expectedStatus), taskInfo.Status)
		})
	}
}

func TestParseTaskResultAcceptsNumericSeconds(t *testing.T) {
	// some OpenAI-compatible relays send `"seconds": 15` as a number where the
	// official API uses a string — polling must not get stuck on parse errors
	body := []byte(`{"id":"task_up1","object":"video","status":"completed","progress":100,"seconds":15,"size":"1280x720"}`)

	taskInfo, err := (&TaskAdaptor{}).ParseTaskResult(body)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusSuccess), taskInfo.Status)

	stringBody := []byte(`{"id":"task_up1","object":"video","status":"completed","progress":100,"seconds":"15"}`)
	taskInfo, err = (&TaskAdaptor{}).ParseTaskResult(stringBody)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusSuccess), taskInfo.Status)
}

func TestConvertToOpenAIVideoHidesUpstreamURLsAndTaskID(t *testing.T) {
	upstreamURL := "https://upstream.example.com/v1/videos/task_upstream123/content?signature=abc"
	data, err := common.Marshal(map[string]any{
		"id":         "task_upstream123",
		"task_id":    "task_upstream123",
		"status":     "completed",
		"url":        upstreamURL,
		"result_url": upstreamURL,
		"metadata": map[string]any{
			"url":  upstreamURL,
			"note": "keep-me",
		},
	})
	require.NoError(t, err)

	prevSecret := common.SessionSecret
	common.SessionSecret = "sora-adaptor-test-secret"
	t.Cleanup(func() { common.SessionSecret = prevSecret })

	task := &model.Task{ID: 99, UserId: 42, TaskID: "task_public456", Data: data}
	out, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)

	assert.NotContains(t, string(out), "upstream.example.com")
	assert.NotContains(t, string(out), "task_upstream123")

	// browser <video> cannot send Authorization — rewritten links must carry a video_token capability
	rewritten := gjson.GetBytes(out, "url").String()
	parsed, err := url.Parse(rewritten)
	require.NoError(t, err)
	assert.Equal(t, taskcommon.BuildProxyURL(task.TaskID), parsed.Scheme+"://"+parsed.Host+parsed.Path)
	grant, err := service.ParseVideoContentToken(parsed.Query().Get("video_token"), task.TaskID)
	require.NoError(t, err)
	assert.Equal(t, 42, grant.OwnerUserID)
	assert.Equal(t, int64(99), grant.TaskRecordID)

	assert.Equal(t, "task_public456", gjson.GetBytes(out, "id").String())
	assert.Equal(t, "task_public456", gjson.GetBytes(out, "task_id").String())
	assert.Equal(t, rewritten, gjson.GetBytes(out, "result_url").String())
	assert.Equal(t, rewritten, gjson.GetBytes(out, "metadata.url").String())
	assert.Equal(t, "keep-me", gjson.GetBytes(out, "metadata.note").String())
	// stored task data must stay untouched for the /content proxy to resolve upstream
	assert.Equal(t, string(data), string(task.Data))
}

func TestConvertToOpenAIVideoLeavesPendingTaskWithoutURLs(t *testing.T) {
	data, err := common.Marshal(map[string]any{
		"id":     "task_upstream123",
		"status": "in_progress",
	})
	require.NoError(t, err)

	out, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(&model.Task{TaskID: "task_public456", Data: data})
	require.NoError(t, err)

	assert.Equal(t, "task_public456", gjson.GetBytes(out, "id").String())
	assert.False(t, gjson.GetBytes(out, "url").Exists())
	assert.False(t, gjson.GetBytes(out, "task_id").Exists())
}
