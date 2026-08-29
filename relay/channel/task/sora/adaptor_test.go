package sora

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
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

func TestParseTaskResultAcceptsFloatProgress(t *testing.T) {
	// Python-backed video upstreams serialize progress as a float
	// (`"progress":0.0`, `"progress":42.5`) where the official API uses an
	// integer — polling must not get stuck on parse errors
	body := []byte(`{"id":"67ea6f2c","object":"video","status":"queued","model":"MiniMaxAI/MiniMax-H3","progress":0.0,"created_at":1787751034,"completed_at":null,"error":null}`)

	taskInfo, err := (&TaskAdaptor{}).ParseTaskResult(body)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusQueued), taskInfo.Status)

	midBody := []byte(`{"id":"67ea6f2c","object":"video","status":"processing","progress":42.5}`)
	taskInfo, err = (&TaskAdaptor{}).ParseTaskResult(midBody)
	require.NoError(t, err)
	assert.Equal(t, string(model.TaskStatusInProgress), taskInfo.Status)
	assert.Equal(t, "42%", taskInfo.Progress)
}

func TestDoResponseAcceptsFloatProgressSubmitPayload(t *testing.T) {
	// real submit response from a MiniMax-style upstream: float progress,
	// null completed_at/error, plus non-standard billing fields to ignore
	body := `{"id":"67ea6f2c-5948-4bcf-ba93-8aeee654928f","object":"video","status":"queued","model":"MiniMaxAI/MiniMax-H3","progress":0.0,"created_at":1787751034,"completed_at":null,"error":null,"project_id":"949368fc","credits_held":450,"queue_status":{"state":"status_unavailable"},"held_microcredits":450000000}`

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public456"}}

	upstreamID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "67ea6f2c-5948-4bcf-ba93-8aeee654928f", upstreamID)
	assert.Equal(t, body, string(taskData))

	// the client-facing response must carry the public task ID, not the upstream one
	assert.Equal(t, "task_public456", gjson.Get(recorder.Body.String(), "id").String())
}

func TestConvertToOpenAIVideoRewritesOutputURL(t *testing.T) {
	// MiniMax-style upstreams deliver the finished video via `output_url`;
	// it must be hidden behind the signed proxy URL like url/result_url.
	upstreamURL := "https://upstream.example.com/files/final.mp4?signature=abc"
	data, err := common.Marshal(map[string]any{
		"id":         "67ea6f2c",
		"object":     "video",
		"status":     "completed",
		"output_url": upstreamURL,
	})
	require.NoError(t, err)

	prevSecret := common.SessionSecret
	common.SessionSecret = "sora-adaptor-test-secret"
	t.Cleanup(func() { common.SessionSecret = prevSecret })

	task := &model.Task{ID: 99, UserId: 42, TaskID: "task_public456", Status: model.TaskStatusSuccess, Data: data}
	out, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)

	assert.NotContains(t, string(out), "upstream.example.com")
	rewritten := gjson.GetBytes(out, "output_url").String()
	parsed, err := url.Parse(rewritten)
	require.NoError(t, err)
	assert.Equal(t, taskcommon.BuildProxyURL(task.TaskID), parsed.Scheme+"://"+parsed.Host+parsed.Path)
	assert.NotEmpty(t, parsed.Query().Get("video_token"))
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

func TestConvertToOpenAIVideoRewritesURLValuedObjectField(t *testing.T) {
	// meaicc-style relays return the signed upstream video link in the
	// non-standard `object` field; it must be hidden like url/result_url.
	// A literal `"object":"video"` must stay untouched.
	upstreamURL := "https://plcdn.example.com/gen_video/atomic_1.mp4?x-oss-signature=sig"
	data, err := common.Marshal(map[string]any{
		"id":     "wr_upstream1",
		"object": upstreamURL,
		"status": "SUCCEEDED",
	})
	require.NoError(t, err)

	prevSecret := common.SessionSecret
	common.SessionSecret = "sora-adaptor-test-secret"
	t.Cleanup(func() { common.SessionSecret = prevSecret })

	out, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(&model.Task{ID: 99, UserId: 42, TaskID: "task_public456", Data: data})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "plcdn.example.com")
	rewritten := gjson.GetBytes(out, "object").String()
	parsed, err := url.Parse(rewritten)
	require.NoError(t, err)
	assert.Equal(t, taskcommon.BuildProxyURL("task_public456"), parsed.Scheme+"://"+parsed.Host+parsed.Path)
	assert.NotEmpty(t, parsed.Query().Get("video_token"))

	literalData, err := common.Marshal(map[string]any{"id": "wr_upstream1", "object": "video", "status": "completed"})
	require.NoError(t, err)
	out, err = (&TaskAdaptor{}).ConvertToOpenAIVideo(&model.Task{ID: 99, UserId: 42, TaskID: "task_public456", Data: literalData})
	require.NoError(t, err)
	assert.Equal(t, "video", gjson.GetBytes(out, "object").String())
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

func TestConvertToOpenAIVideoKeepsEmptyURLPlaceholdersWhilePending(t *testing.T) {
	// Some relay upstreams include empty-string url/result_url/video_url keys
	// while the task is still queued; they must stay empty instead of being
	// rewritten into a signed proxy URL, or clients treat the task as done.
	data, err := common.Marshal(map[string]any{
		"id":         "task_upstream123",
		"task_id":    "task_upstream123",
		"object":     "video",
		"status":     "queued",
		"url":        "",
		"video_url":  "",
		"result_url": "",
	})
	require.NoError(t, err)

	task := &model.Task{ID: 99, UserId: 42, TaskID: "task_public456", Status: model.TaskStatusQueued, Data: data}
	out, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)

	assert.Equal(t, "", gjson.GetBytes(out, "url").String())
	assert.Equal(t, "", gjson.GetBytes(out, "video_url").String())
	assert.Equal(t, "", gjson.GetBytes(out, "result_url").String())
	assert.NotContains(t, string(out), "video_token")

	// once the task succeeds the same empty placeholders must be rewritten
	prevSecret := common.SessionSecret
	common.SessionSecret = "sora-adaptor-test-secret"
	t.Cleanup(func() { common.SessionSecret = prevSecret })

	task.Status = model.TaskStatusSuccess
	out, err = (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(gjson.GetBytes(out, "result_url").String(), taskcommon.BuildProxyURL("task_public456")+"?video_token="))
}

func TestWrapDashScopeVideoPayloadWrapsPromptImagesAndKnobs(t *testing.T) {
	bodyMap := map[string]interface{}{
		"model":        "sd-2-c5",
		"prompt":       "咖啡店交接第一杯",
		"seconds":      "15",
		"duration":     float64(15),
		"resolution":   "720p",
		"aspect_ratio": "16:9",
		"images":       []interface{}{"https://cdn.example.com/a.png", "https://cdn.example.com/b.png"},
	}

	wrapDashScopeVideoPayload(bodyMap)

	input, ok := bodyMap["input"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "咖啡店交接第一杯", input["prompt"])
	media, ok := input["media"].([]interface{})
	require.True(t, ok)
	require.Len(t, media, 2)
	assert.Equal(t, map[string]interface{}{"type": "reference_image", "url": "https://cdn.example.com/a.png"}, media[0])

	parameters, ok := bodyMap["parameters"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "720P", parameters["resolution"])
	assert.Equal(t, 15, parameters["duration"])
	assert.Equal(t, "16:9", parameters["ratio"])
	assert.Equal(t, false, parameters["prompt_extend"])
	assert.Equal(t, false, parameters["watermark"])

	// top-level fields stay for gateway validation/billing
	assert.Equal(t, "咖啡店交接第一杯", bodyMap["prompt"])
	assert.Equal(t, "15", bodyMap["seconds"])
}

func TestBuildMegabyaiVideoPayloadMapsOpenAIVideoBody(t *testing.T) {
	// megabyai rejects unknown keys, so the OpenAI-style body must be replaced,
	// not extended: size becomes ratio + resolution, seconds becomes duration,
	// and the reference aliases collapse onto the camelCase keys.
	bodyMap := map[string]any{
		"model":           "videos-standard",
		"prompt":          "让参考角色在樱花树下挥手，镜头缓慢推进",
		"seconds":         "8",
		"size":            "720x1280",
		"input_reference": "https://assets.example.com/character-front.jpg",
		"response_format": "url",
		"metadata": map[string]any{
			"reference_videos": []any{"https://assets.example.com/motion-reference.mp4"},
			"audios":           "https://assets.example.com/music.mp3",
		},
	}

	payload := buildMegabyaiVideoPayload(bodyMap)

	assert.Equal(t, map[string]any{
		"model":           "videos-standard",
		"prompt":          "让参考角色在樱花树下挥手，镜头缓慢推进",
		"duration":        8,
		"ratio":           "9:16",
		"resolution":      "720p",
		"referenceImages": []string{"https://assets.example.com/character-front.jpg"},
		"referenceVideos": []string{"https://assets.example.com/motion-reference.mp4"},
		"referenceAudios": []string{"https://assets.example.com/music.mp3"},
	}, payload)
}

func TestBuildMegabyaiVideoPayloadKeepsClientDialectFields(t *testing.T) {
	// a client that already speaks megabyai must pass through unchanged
	bodyMap := map[string]any{
		"model":           "videos-pro",
		"prompt":          "一只猫",
		"duration":        float64(4),
		"ratio":           "1:1",
		"resolution":      "1080p",
		"referenceImages": []any{"https://assets.example.com/a.jpg", "https://assets.example.com/b.jpg"},
	}

	payload := buildMegabyaiVideoPayload(bodyMap)

	assert.Equal(t, 4, payload["duration"])
	assert.Equal(t, "1:1", payload["ratio"])
	assert.Equal(t, "1080p", payload["resolution"])
	assert.Equal(t, []string{"https://assets.example.com/a.jpg", "https://assets.example.com/b.jpg"}, payload["referenceImages"])
}

func TestBuildMegabyaiVideoPayloadSnapsUnsupportedSize(t *testing.T) {
	// 1792x1024 reduces to 7:4, which megabyai rejects — it must snap to 16:9,
	// and a text-to-video request must not carry empty reference arrays
	payload := buildMegabyaiVideoPayload(map[string]any{
		"model":  "videos-standard",
		"prompt": "城市夜景",
		"size":   "1792x1024",
	})

	assert.Equal(t, "16:9", payload["ratio"])
	assert.Equal(t, "1080p", payload["resolution"])
	assert.NotContains(t, payload, "referenceImages")
	assert.NotContains(t, payload, "referenceVideos")
	assert.NotContains(t, payload, "referenceAudios")
	assert.NotContains(t, payload, "duration")
}

func TestWrapDashScopeVideoPayloadPreservesDialectInputAndRebuildsParameters(t *testing.T) {
	existingInput := map[string]interface{}{"prompt": "already wrapped", "media": []interface{}{}}
	bodyMap := map[string]interface{}{
		"model":  "sd-2-c5",
		"prompt": "outer",
		"input":  existingInput,
	}

	wrapDashScopeVideoPayload(bodyMap)

	assert.Equal(t, existingInput, bodyMap["input"])
	assert.Equal(t, map[string]interface{}{
		"prompt_extend": false,
		"watermark":     false,
	}, bodyMap["parameters"])
}

func TestWrapDashScopeVideoPayloadTextToVideoOmitsMedia(t *testing.T) {
	bodyMap := map[string]interface{}{"model": "sd-2-c5", "prompt": "一只猫", "duration": float64(5)}

	wrapDashScopeVideoPayload(bodyMap)

	input, ok := bodyMap["input"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "一只猫", input["prompt"])
	_, hasMedia := input["media"]
	assert.False(t, hasMedia)
	parameters := bodyMap["parameters"].(map[string]interface{})
	assert.Equal(t, 5, parameters["duration"])
}
