package sora

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
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
		{name: "completed is unchanged", upstreamStatus: "completed", expectedStatus: model.TaskStatusSuccess},
		{name: "failed is unchanged", upstreamStatus: "failed", expectedStatus: model.TaskStatusFailure},
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

	task := &model.Task{TaskID: "task_public456", Data: data}
	out, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)

	assert.NotContains(t, string(out), "upstream.example.com")
	assert.NotContains(t, string(out), "task_upstream123")

	proxyURL := taskcommon.BuildProxyURL(task.TaskID)
	assert.Equal(t, "task_public456", gjson.GetBytes(out, "id").String())
	assert.Equal(t, "task_public456", gjson.GetBytes(out, "task_id").String())
	assert.Equal(t, proxyURL, gjson.GetBytes(out, "url").String())
	assert.Equal(t, proxyURL, gjson.GetBytes(out, "result_url").String())
	assert.Equal(t, proxyURL, gjson.GetBytes(out, "metadata.url").String())
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
