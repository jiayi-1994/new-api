package dto

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertArkVideoRequest(t *testing.T) {
	const img, img2, vid, aud = "https://cdn.example/a.png", "https://cdn.example/b.png", "https://cdn.example/r.mp4", "https://cdn.example/r.mp3"
	testCases := []struct {
		name     string
		body     string
		expected map[string]any
		err      string
	}{
		{
			name:     "text to video with Ark defaults dropped",
			body:     `{"model":"seedance-2.0","content":[{"type":"text","text":"a cat"},{"type":"text","text":"on a piano"}],"duration":5,"resolution":"720p","ratio":"16:9","generate_audio":true,"watermark":false,"seed":-1,"camera_fixed":false,"return_last_frame":false,"draft":false,"service_tier":"default","callback_url":null}`,
			expected: map[string]any{"model": "seedance-2.0", "prompt": "a cat\non a piano", "seconds": 5, "resolution": "720p", "ratio": "16:9", "generate_audio": true},
		},
		{
			name:     "frames and references map onto OpenAI video fields",
			body:     `{"model":"m","content":[{"type":"text","text":"x"},{"type":"image_url","role":"first_frame","image_url":{"url":"` + img + `"}},{"type":"image_url","role":"last_frame","image_url":"` + img2 + `"}]}`,
			expected: map[string]any{"model": "m", "prompt": "x", "first_image_url": img, "last_image_url": img2},
		},
		{
			name:     "role-less image is a reference image",
			body:     `{"model":"m","content":[{"type":"image_url","image_url":{"url":"` + img + `"}},{"type":"image_url","role":"reference_image","image_url":{"url":"` + img2 + `"}},{"type":"video_url","role":"reference_video","video_url":{"url":"` + vid + `"}},{"type":"audio_url","audio_url":{"url":"` + aud + `"}}]}`,
			expected: map[string]any{"model": "m", "images": []string{img, img2}, "videos": []string{vid}, "audios": []string{aud}},
		},
		{
			name:     "non-default options pass through",
			body:     `{"model":"m","watermark":true,"seed":42,"camera_fixed":true}`,
			expected: map[string]any{"model": "m", "watermark": true, "seed": int64(42), "camera_fixed": true},
		},
		{name: "duration above host bound", body: `{"model":"m","duration":3601}`, err: "duration must be an integer from 1 to 3600 seconds"},
		{name: "adaptive duration", body: `{"model":"m","duration":-1}`, err: "duration must be an integer from 1 to 3600 seconds"},
		{name: "fractional duration", body: `{"model":"m","duration":5.5}`, err: "duration must be an integer from 1 to 3600 seconds"},
		{name: "billing alias outside Ark", body: `{"model":"m","seconds":5}`, err: "unsupported parameter: seconds"},
		{name: "frames", body: `{"model":"m","frames":121}`, err: "unsupported parameter: frames"},
		{name: "prompt command", body: `{"model":"m","content":[{"type":"text","text":"a cat --dur 10"}]}`, err: "prompt parameters such as --dur or --rs are not supported; use the JSON fields instead"},
		{name: "callback", body: `{"model":"m","callback_url":"https://hook.example"}`, err: "callback_url is not supported; poll the task instead"},
		{name: "last frame output", body: `{"model":"m","return_last_frame":true}`, err: "return_last_frame is not supported"},
		{name: "flex tier", body: `{"model":"m","service_tier":"flex"}`, err: "service_tier is not supported; omit it or use default"},
		{name: "draft task", body: `{"model":"m","content":[{"type":"draft_task","draft_task":{"id":"cgt-1"}}]}`, err: "draft_task is not supported; resubmit the draft as a new request"},
		{name: "wrong role", body: `{"model":"m","content":[{"type":"video_url","role":"first_frame","video_url":{"url":"` + vid + `"}}]}`, err: `content video_url does not support role "first_frame"`},
		{name: "two first frames", body: `{"model":"m","content":[{"type":"image_url","role":"first_frame","image_url":"` + img + `"},{"type":"image_url","role":"first_frame","image_url":"` + img2 + `"}]}`, err: "content accepts one first_frame image"},
		{name: "missing model", body: `{"content":[{"type":"text","text":"x"}]}`, err: "model is required"},
		{name: "not an object", body: `[1]`, err: "request body must be a JSON object"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			converted, err := ConvertArkVideoRequest([]byte(tc.body), 3600)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, converted)
		})
	}
}

func TestArkVideoTaskFromOpenAI(t *testing.T) {
	tokens := 108900.0
	completed := OpenAIVideo{
		ID: "task_1", Model: "seedance-2.0", Status: VideoStatusCompleted, CreatedAt: 100, CompletedAt: 160, Seconds: "5",
		Metadata:    map[string]any{"resolution": "720p", "url": "https://upstream.cdn/v.mp4", "volcesVideoUrl": "https://upstream.cdn/v.mp4"},
		VideoTokens: &tokens,
	}
	five := 5
	assert.Equal(t, ArkVideoTask{
		ID: "task_1", Model: "seedance-2.0", Status: ArkVideoStatusSucceeded,
		Content:  &ArkVideoContent{VideoURL: "https://gateway.example/v1/tasks/task_1/artifacts/video/content?access=sig"},
		Usage:    &ArkVideoUsage{CompletionTokens: 108900, TotalTokens: 108900},
		Duration: &five, Resolution: "720p", CreatedAt: 100, UpdatedAt: 160,
	}, ArkVideoTaskFromOpenAI(completed, "https://gateway.example/v1/tasks/task_1/artifacts/video/content?access=sig"))

	statusCases := []struct {
		status   string
		expected string
	}{
		{VideoStatusQueued, ArkVideoStatusQueued},
		{VideoStatusInProgress, ArkVideoStatusRunning},
		{VideoStatusUnknown, ArkVideoStatusQueued},
	}
	for _, tc := range statusCases {
		task := ArkVideoTaskFromOpenAI(OpenAIVideo{ID: "t", Status: tc.status, CreatedAt: 1}, "")
		assert.Equal(t, tc.expected, task.Status, tc.status)
		assert.Nil(t, task.Content, tc.status)
	}

	failed := ArkVideoTaskFromOpenAI(OpenAIVideo{ID: "t", Status: VideoStatusFailed, Error: &OpenAIVideoError{Message: "content rejected"}}, "")
	assert.Equal(t, ArkVideoStatusFailed, failed.Status)
	assert.Equal(t, &ArkVideoError{Code: ArkVideoErrorInternal, Message: "content rejected"}, failed.Error)
}

func TestArkVideoErrorFromBody(t *testing.T) {
	testCases := []struct {
		name     string
		status   int
		body     string
		expected ArkVideoError
	}{
		{"OpenAI envelope", http.StatusUnauthorized, `{"error":{"message":"invalid token","type":"new_api_error","code":""}}`, ArkVideoError{Code: ArkVideoErrorAuthentication, Message: "invalid token"}},
		{"OpenAI envelope with code", http.StatusTooManyRequests, `{"error":{"message":"slow down","code":"rate_limited"}}`, ArkVideoError{Code: "rate_limited", Message: "slow down"}},
		{"task error", http.StatusBadRequest, `{"code":"task_not_exist","message":"task_not_exist","data":null}`, ArkVideoError{Code: "task_not_exist", Message: "task_not_exist"}},
		{"server error without code", http.StatusBadGateway, `{"message":"upstream failed"}`, ArkVideoError{Code: ArkVideoErrorInternal, Message: "upstream failed"}},
		{"plain text", http.StatusServiceUnavailable, `busy`, ArkVideoError{Code: ArkVideoErrorInternal, Message: "busy"}},
		{"opaque JSON", http.StatusBadRequest, `{"detail":"x"}`, ArkVideoError{Code: ArkVideoErrorInvalidParameter, Message: "request failed"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, ArkVideoErrorResponse{Error: tc.expected}, ArkVideoErrorFromBody(tc.status, []byte(tc.body)))
		})
	}
}
