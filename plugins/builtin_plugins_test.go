package plugins

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/pkg/videosched/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expectedKeys = []string{"aggc", "alibaba", "apipod", "bytefor", "cangyuan", "doubao", "elevenlabs-pool", "google", "hailuo", "ijm2api", "jimeng", "kling", "meaicc", "megabyai", "onmi", "paipu", "pidoi", "seedance-hjmie", "sora", "sudashui", "sunoapi", "token6688", "vertex-ai", "vidu", "vobilemax", "zongheng"}

// responsesKeys are the vendor plugins that also serve OpenAI Responses.
var responsesKeys = []string{"alibaba", "doubao", "google", "hailuo", "jimeng", "kling", "sora", "sunoapi", "vertex-ai", "vidu"}

// videoOnlyKeys are third-party relays that only speak the OpenAI video protocol.
var videoOnlyKeys = []string{"aggc", "apipod", "bytefor", "cangyuan", "elevenlabs-pool", "ijm2api", "meaicc", "megabyai", "onmi", "paipu", "pidoi", "seedance-hjmie", "sudashui", "token6688", "vobilemax", "zongheng"}

func TestElevenLabsPoolPlugin(t *testing.T) {
	source, err := Source("elevenlabs-pool")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "elevenlabs-pool"})
	require.NoError(t, err)
	call := func(hook string, args ...any) map[string]any {
		t.Helper()
		value, callErr := plugin.Engine.Call(t.Context(), hook, args...)
		require.NoError(t, callErr, hook)
		return value.(map[string]any)
	}
	decode := func(input map[string]any, model string) map[string]any {
		t.Helper()
		encoded, marshalErr := common.Marshal(input)
		require.NoError(t, marshalErr)
		var httpInput map[string]any
		require.NoError(t, common.Unmarshal(encoded, &httpInput))
		ctx := map[string]any{"model": input["model"], "upstreamModel": model, "body": map[string]any{"kind": "json", "value": httpInput}}
		value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, ctx)
		require.NoError(t, callErr)
		repeated, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, ctx)
		require.NoError(t, callErr)
		assert.Equal(t, value, repeated)
		unchanged, marshalErr := common.Marshal(httpInput)
		require.NoError(t, marshalErr)
		assert.JSONEq(t, string(encoded), string(unchanged))
		return map[string]any{"model": input["model"], "upstreamModel": model, "requestBody": value.(map[string]any)["requestBody"], "baseUrl": "https://pool.example/", "apiKey": "fixture-only-key", "publicTaskId": "task_fixture_1"}
	}
	t.Run("catalog defaults bounds and final mapped model", func(t *testing.T) {
		for _, tc := range []struct {
			alias, model string
			min, max     int
			tiers        []string
			audio, video bool
		}{
			{"seedance-1.0-pro", "bytedance-seedance-v1-pro", 3, 12, []string{"480p", "720p", "1080p"}, false, false},
			{"seedance-1.5-pro", "bytedance-seedance-v1.5-pro", 4, 12, []string{"480p", "720p"}, true, false},
			{"seedance-2.0", "bytedance-seedance-v2", 4, 15, []string{"480p", "720p", "1080p", "4k"}, true, false},
			{"seedance-2.0-fast", "bytedance-seedance-v2-fast", 4, 15, []string{"480p", "720p"}, true, false},
			{"seedance-2.0-mini", "bytedance-seedance-v2-mini", 4, 15, []string{"480p", "720p"}, true, false},
			{"seedance-2.5", "bytedance-seedance-v2.5", 4, 30, []string{"480p", "720p", "1080p"}, true, false},
			{"seedance-2.5-edit", "bytedance-seedance-v2.5-edit", -1, -1, []string{"480p", "720p", "1080p"}, true, true},
			{"seedance-2.5-extend", "bytedance-seedance-v2.5-extend", 4, 30, []string{"480p", "720p", "1080p"}, true, true},
		} {
			t.Run(tc.alias, func(t *testing.T) {
				for _, model := range []string{tc.alias, tc.model} {
					binding, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", model)
					require.True(t, found)
					assert.Same(t, plugin, binding.Plugin)
					for _, tier := range tc.tiers {
						for _, seconds := range []int{tc.min, tc.max} {
							input := map[string]any{"model": "public-video", "prompt": "cat", "seconds": seconds, "resolution": tier, "metadata": map[string]any{}, "n": 1}
							if tc.video {
								input["videos"] = []string{"https://cdn.example/input.mp4"}
							}
							ctx := decode(input, model)
							built := call("buildSubmitRequest", ctx)
							assert.NotContains(t, built, "model")
							wire := built["body"].(map[string]any)
							assert.Equal(t, tc.model, wire["model"])
							assert.EqualValues(t, seconds, wire["duration_secs"])
							assert.Equal(t, tier, wire["resolution"])
							assert.NotContains(t, wire, "generate_audio")
							usage := call("extractUsage", ctx)
							_, usageErr := plugin.Meta.ValidateUsageFacts(usage, model, "public-video")
							require.NoError(t, usageErr)
							require.NoError(t, plugin.Meta.ValidateResolvedUsageRequest(ctx["requestBody"], model, "public-video"))
							assert.Equal(t, tc.audio, usage["generate_audio"])
							assert.Equal(t, tier, usage["resolution"])
							if tc.min == -1 {
								assert.EqualValues(t, 1, usage["video_count"])
								assert.NotContains(t, usage, "seconds")
								assert.Equal(t, map[string]any{"unsupported": true}, call("describeSpec", ctx))
							} else {
								assert.EqualValues(t, seconds, usage["seconds"])
								got, _, specErr := spec.Parse(call("describeSpec", ctx))
								require.NoError(t, specErr)
								assert.EqualValues(t, seconds, *got.OutputSeconds)
								assert.Equal(t, tier, got.Tier)
							}
							assert.Empty(t, call("extractUsageOnComplete", ctx, map[string]any{"status": "SUCCESS"}, map[string]any{"quote": 1706.3, "reserved": 1707, "timing": map[string]any{"total_secs": 121}}))
						}
					}
				}
				input := map[string]any{"model": tc.alias, "prompt": "cat"}
				if tc.video {
					input["videos"] = []string{"https://cdn.example/input.mp4"}
				}
				wire := call("buildSubmitRequest", decode(input, tc.model))["body"].(map[string]any)
				defaultSeconds := 4
				if tc.min == -1 {
					defaultSeconds = -1
				}
				assert.EqualValues(t, defaultSeconds, wire["duration_secs"])
				assert.Equal(t, "480p", wire["resolution"])
			})
		}
	})
	t.Run("ordered media mirrors frames and audio", func(t *testing.T) {
		for _, tc := range []struct {
			model string
			input string
			refs  map[string]int
			ratio string
		}{
			{"seedance-2.0-mini", `{"images":["https://cdn.example/a","https://cdn.example/a"],"videos":["https://cdn.example/v"],"audios":["https://cdn.example/aud"],"metadata":{"image_urls":["https://cdn.example/a","https://cdn.example/a"]}}`, map[string]int{"image": 2, "video": 1, "audio": 1, "frame": 0}, "auto"},
			{"seedance-2.5", `{"audios":["https://cdn.example/audio"]}`, map[string]int{"image": 0, "video": 0, "audio": 1, "frame": 0}, "16:9"},
			{"seedance-1.0-pro", `{"first_image_url":"https://cdn.example/first","last_image_url":"https://cdn.example/last","ratio":"auto"}`, map[string]int{"image": 0, "video": 0, "audio": 0, "frame": 2}, "auto"},
			{"seedance-1.5-pro", `{"first_frame_url":"https://cdn.example/first"}`, map[string]int{"image": 0, "video": 0, "audio": 0, "frame": 1}, "16:9"},
			{"seedance-2.5", `{"start_frame_url":"https://cdn.example/first","metadata":{"first_image_url":"https://cdn.example/first"}}`, map[string]int{"image": 0, "video": 0, "audio": 0, "frame": 1}, "auto"},
			{"seedance-2.5", `{"start_frame_url":"https://cdn.example/first","ratio":"16:9"}`, map[string]int{"image": 0, "video": 0, "audio": 0, "frame": 1}, "auto"},
			{"seedance-2.5", `{"videos":["https://cdn.example/v"],"aspect_ratio":"9:16"}`, map[string]int{"image": 0, "video": 1, "audio": 0, "frame": 0}, "auto"},
			{"seedance-2.5", `{"videos":["https://cdn.example/v"],"size":"1280x720"}`, map[string]int{"image": 0, "video": 1, "audio": 0, "frame": 0}, "auto"},
			{"seedance-2.5", `{"images":["https://cdn.example/image"],"ratio":"9:16"}`, map[string]int{"image": 1, "video": 0, "audio": 0, "frame": 0}, "9:16"},
			{"seedance-2.0", `{"image_url":"https://cdn.example/single","size":"3840x2160"}`, map[string]int{"image": 1, "video": 0, "audio": 0, "frame": 0}, "16:9"},
		} {
			var input map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.input, &input))
			input["model"], input["prompt"], input["generate_audio"] = "public-video", "cat", false
			ctx := decode(input, tc.model)
			wire := call("buildSubmitRequest", ctx)["body"].(map[string]any)
			assert.Equal(t, false, wire["generate_audio"])
			assert.Equal(t, tc.ratio, wire["aspect_ratio"])
			got, _, specErr := spec.Parse(call("describeSpec", ctx))
			require.NoError(t, specErr)
			assert.Equal(t, tc.refs, got.References)
			assert.Len(t, got.ReferenceVideoURLs, tc.refs["video"])
			if tc.refs["image"] == 2 {
				assert.Equal(t, []any{"https://cdn.example/a", "https://cdn.example/a"}, wire["image_urls"])
			}
			framePrice := 0.1
			quote := videosched.Quote(videosched.CostConfig{Mode: videosched.ModePerSecond, Prices: map[string]float64{got.Tier: 0.5}, References: map[string]map[string]videosched.ReferenceCost{
				"image": {"*": {Mode: videosched.RefIncluded}}, "video": {"*": {Mode: videosched.RefIncluded}}, "audio": {"*": {Mode: videosched.RefIncluded}}, "frame": {"*": {Mode: videosched.RefPerInput, Value: &framePrice}},
			}}, got)
			assert.Empty(t, quote.Reason)
			assert.InDelta(t, 2+float64(tc.refs["frame"])*0.1, quote.TotalUSD, 1e-9)
		}
		for _, audio := range []bool{true, false} {
			ctx := decode(map[string]any{"model": "seedance-2.0-mini", "prompt": "cat", "generate_audio": audio, "metadata": map[string]any{"generate_audio": audio}}, "seedance-2.0-mini")
			assert.Equal(t, audio, call("buildSubmitRequest", ctx)["body"].(map[string]any)["generate_audio"])
			assert.Equal(t, audio, call("extractUsage", ctx)["generate_audio"])
		}
		ctx := decode(map[string]any{"model": "seedance-2.0", "prompt": "cat", "images": []string{"https://cdn.example?signature=fixture:value"}}, "seedance-2.0")
		assert.Equal(t, []any{"https://cdn.example?signature=fixture:value"}, call("buildSubmitRequest", ctx)["body"].(map[string]any)["image_urls"])
		for _, tc := range []struct {
			model  string
			counts []int
		}{{"seedance-2.0-mini", []int{6, 3, 3}}, {"seedance-2.5", []int{30, 10, 10}}} {
			input := map[string]any{"model": tc.model, "prompt": "cat"}
			for i, field := range []string{"images", "videos", "audios"} {
				input[field] = slices.Repeat([]string{"https://cdn.example/" + field}, tc.counts[i])
			}
			ctx := decode(input, tc.model)
			got, _, specErr := spec.Parse(call("describeSpec", ctx))
			require.NoError(t, specErr)
			assert.Equal(t, map[string]int{"image": tc.counts[0], "video": tc.counts[1], "audio": tc.counts[2], "frame": 0}, got.References)
		}
	})
	t.Run("invalid inputs cannot bypass model or billing validation", func(t *testing.T) {
		for _, tc := range []struct{ model, input, message string }{
			{"seedance-2.0-mini", `{"seconds":0}`, "integer"},
			{"seedance-2.0-mini", `{"seconds":null}`, "integer"},
			{"seedance-2.0-mini", `{"seconds":4.5}`, "integer"},
			{"seedance-2.0-mini", `{"metadata":{"duration_secs":18446744073686646784}}`, "integer"},
			{"seedance-2.0-mini", `{"seconds":-1}`, "supports duration"},
			{"seedance-2.0-mini", `{"seconds":16}`, "supports duration"},
			{"seedance-2.0-mini", `{"seconds":4,"metadata":{"duration_secs":5}}`, "conflicting"},
			{"seedance-2.5", `{"seconds":31}`, "integer"},
			{"seedance-1.0-pro", `{"seconds":13}`, "supports duration"},
			{"seedance-1.5-pro", `{"seconds":3}`, "supports duration"},
			{"seedance-2.0-mini", `{"resolution":"1080p"}`, "supports only"},
			{"seedance-2.5", `{"resolution":"4k"}`, "supports only"},
			{"seedance-2.0", `{"size":"1280x720","resolution":"480p"}`, "conflicts"},
			{"seedance-2.0", `{"size":"1280x720","ratio":"9:16"}`, "conflicts"},
			{"seedance-2.0", `{"metadata":{"n":2}}`, "n must"},
			{"seedance-2.0", `{"n":"1"}`, "n must"},
			{"seedance-2.0", `{"generate_audio":"false"}`, "boolean"},
			{"seedance-2.0", `{"generate_audio":0}`, "boolean"},
			{"seedance-2.0", `{"generate_audio":null}`, "boolean"},
			{"seedance-2.0", `{"generate_audio":false,"metadata":{"generate_audio":true}}`, "conflicting"},
			{"seedance-1.0-pro", `{"generate_audio":true}`, "does not generate audio"},
			{"seedance-2.0-mini", `{"audios":["https://cdn.example/a"]}`, "requires an image or video"},
			{"seedance-2.0", `{"images":"https://cdn.example/a"}`, "array"},
			{"seedance-2.0", `{"images":[null]}`, "HTTP(S)"},
			{"seedance-2.0", `{"images":["file:///tmp/a"]}`, "HTTP(S)"},
			{"seedance-2.0", `{"images":["https://user:secret@cdn.example/a"]}`, "without credentials"},
			{"seedance-2.0", `{"images":["https://cdn.example/a#fragment"]}`, "fragments"},
			{"seedance-2.0", `{"images":["https://cdn.example:8080/a"]}`, "ports"},
			{"seedance-2.0", `{"image_url":"https://cdn.example/a","images":["https://cdn.example/a"]}`, "not both"},
			{"seedance-2.0", `{"images":["https://cdn.example/a"],"metadata":{"image_urls":["https://cdn.example/b"]}}`, "conflicting"},
			{"seedance-2.0", `{"last_image_url":"https://cdn.example/a"}`, "requires a start frame"},
			{"seedance-2.0", `{"first_frame_url":"https://cdn.example/a","images":["https://cdn.example/b"]}`, "cannot be combined"},
			{"seedance-1.0-pro", `{"images":["https://cdn.example/a"]}`, "at most 0"},
			{"seedance-1.5-pro", `{"start_frame_url":"https://cdn.example/a","ratio":"auto"}`, "auto aspect ratio"},
			{"seedance-2.5-edit", `{}`, "requires a reference video"},
			{"seedance-2.5-edit", `{"videos":["https://cdn.example/v"],"seconds":4}`, "must be -1"},
			{"seedance-2.5-extend", `{}`, "requires a reference video"},
			{"seedance-2.0", `{"metadata":null}`, "must be an object"},
			{"seedance-2.0", `{"metadata":{"reference_seconds":0}}`, "unsupported metadata"},
			{"seedance-2.0", `{"account_id":"pool-account"}`, "unsupported video"},
			{"unknown-model", `{}`, "unsupported ElevenLabs"},
		} {
			t.Run(tc.model+tc.input, func(t *testing.T) {
				var input map[string]any
				require.NoError(t, common.UnmarshalJsonStr(tc.input, &input))
				input["model"], input["prompt"] = "public-video", "cat"
				_, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "public-video", "upstreamModel": tc.model, "body": map[string]any{"kind": "json", "value": input}})
				require.ErrorContains(t, callErr, tc.message)
				for _, hook := range []string{"buildSubmitRequest", "extractUsage", "describeSpec"} {
					_, callErr = plugin.Engine.Call(t.Context(), hook, map[string]any{"model": "public-video", "upstreamModel": tc.model, "requestBody": input})
					require.ErrorContains(t, callErr, tc.message, hook)
				}
			})
		}
		for _, tc := range []struct {
			model, field string
			count        int
			message      string
		}{
			{"seedance-2.0-mini", "images", 10, "at most 9"}, {"seedance-2.0-mini", "videos", 4, "at most 3"}, {"seedance-2.0-mini", "audios", 4, "at most 3"}, {"seedance-2.5", "images", 31, "at most 30"},
		} {
			input := map[string]any{"prompt": "cat", tc.field: slices.Repeat([]string{"https://cdn.example/media"}, tc.count)}
			_, callErr := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"model": tc.model, "requestBody": input})
			require.ErrorContains(t, callErr, tc.message)
		}
		_, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"model": "seedance-2.0-mini", "requestBody": map[string]any{"prompt": "cat", "images": slices.Repeat([]string{"https://cdn.example/image"}, 9), "videos": slices.Repeat([]string{"https://cdn.example/video"}, 3), "audios": []string{"https://cdn.example/audio"}}})
		require.ErrorContains(t, err, "12 total references")
		_, err = plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"model": "seedance-2.0-mini", "upstreamModel": "seedance-2.5-edit", "requestBody": map[string]any{"prompt": "cat"}})
		require.ErrorContains(t, err, "cannot be mapped")
		_, err = plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"model": "seedance-1.0-pro", "requestBody": map[string]any{"prompt": strings.Repeat("a", 12001)}})
		require.ErrorContains(t, err, "prompt exceeds")
	})
	t.Run("multipart URL inputs and repeatable idempotency", func(t *testing.T) {
		value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "seedance-2.0-mini", "body": map[string]any{"kind": "multipart", "fields": map[string][]string{
			"model": {"seedance-2.0-mini"}, "prompt": {"cat"}, "seconds": {"4"}, "generate_audio": {"false"}, "images[]": {"https://cdn.example/a", "https://cdn.example/a"}, "metadata": {`{"duration_secs":4}`}, "n": {"1"},
		}}})
		require.NoError(t, callErr)
		ctx := map[string]any{"model": "seedance-2.0-mini", "requestBody": value.(map[string]any)["requestBody"], "baseUrl": "https://pool.example/", "authHeader": "Bearer fixture-only-key", "publicTaskId": "task_fixture_1"}
		built := call("buildSubmitRequest", ctx)
		assert.Equal(t, "https://pool.example/v1/videos", built["url"])
		assert.Equal(t, map[string]any{"Authorization": "Bearer fixture-only-key", "Content-Type": "application/json", "Idempotency-Key": "task_fixture_1"}, built["headers"])
		assert.Equal(t, false, built["body"].(map[string]any)["generate_audio"])
		assert.Equal(t, built, call("buildSubmitRequest", ctx))
		ctx["publicTaskId"] = "task_fixture_2"
		assert.NotEqual(t, built["headers"], call("buildSubmitRequest", ctx)["headers"])
		delete(ctx, "publicTaskId")
		_, callErr = plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
		require.ErrorContains(t, callErr, "idempotent")
		_, callErr = plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"body": map[string]any{"kind": "multipart", "files": []any{map[string]any{"ref": "request_file:input_reference"}}}})
		require.ErrorContains(t, callErr, "local file uploads are unsupported")
	})
	t.Run("poll state survives snapshots and result URL is required", func(t *testing.T) {
		ctx := map[string]any{"taskId": "el_task", "baseUrl": "https://pool.example", "apiKey": "fixture-only-key"}
		for status, expected := range map[string]string{"queued": "QUEUED", "preparing": "IN_PROGRESS", "waiting_captcha": "IN_PROGRESS", "submitting": "IN_PROGRESS", "submission_unknown": "IN_PROGRESS", "processing": "IN_PROGRESS", "failed": "FAILURE", "cancelled": "FAILURE", "new_status": "UNKNOWN"} {
			assert.Equal(t, expected, call("parseTaskResult", ctx, map[string]any{"id": "el_task", "status": status, "error": "needs_attention"})["status"])
		}
		assert.Equal(t, "UNKNOWN", call("parseTaskResult", ctx, map[string]any{"id": "other", "status": "completed"})["status"])
		_, callErr := plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx, map[string]any{"body": map[string]any{"error": "unknown"}})
		require.ErrorContains(t, callErr, "missing a task id")
		completed := map[string]any{"id": "el_task", "status": "completed", "account_id": "private", "workspace_id": "private"}
		accepted := call("parseSubmitResponse", ctx, map[string]any{"body": completed})
		assert.NotContains(t, accepted, "immediate")
		assert.Equal(t, map[string]any{"fetchResult": true}, accepted["state"])
		assert.Equal(t, "https://pool.example/v1/tasks/el_task", call("buildQueryRequest", ctx)["url"])
		result := call("parseTaskResult", ctx, completed)
		assert.Equal(t, "IN_PROGRESS", result["status"])
		encoded, marshalErr := common.Marshal(result["state"])
		require.NoError(t, marshalErr)
		var persistedState any
		require.NoError(t, common.Unmarshal(encoded, &persistedState))
		ctx["state"] = persistedState
		assert.Equal(t, "https://pool.example/v1/tasks/el_task/result", call("buildQueryRequest", ctx)["url"])
		for _, body := range []map[string]any{{"id": "el_task"}, {"id": "el_task", "content_url": "https://user:secret@cdn.example/v"}, {"id": "wrong", "content_url": "https://cdn.example/v"}, {"id": "el_task", "content_url": "https://cdn.example/v", "status": "failed"}} {
			assert.Equal(t, "UNKNOWN", call("parseTaskResult", ctx, body)["status"])
		}
		data := map[string]any{"id": "el_task", "content_url": "https://cdn.example/v.mp4?sig=fixture%2Bvalue", "upstream_id": "private", "mode": "mock"}
		assert.Equal(t, "SUCCESS", call("parseTaskResult", ctx, data)["status"])
		task := map[string]any{"status": "SUCCESS", "data": data}
		artifacts, callErr := plugin.Engine.Call(t.Context(), "listArtifacts", task)
		require.NoError(t, callErr)
		assert.Len(t, artifacts, 1)
		content := call("buildContentRequest", map[string]any{"artifactKey": "video", "data": data, "clientRequest": map[string]any{"method": "HEAD", "headers": map[string]any{"Authorization": "private-client-token"}}})
		assert.Equal(t, map[string]any{"url": data["content_url"], "method": "HEAD", "credentialless": true}, content)
		rendered, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "render"}, map[string]any{}, task)
		require.NoError(t, callErr)
		assert.Equal(t, map[string]any{"url": data["content_url"]}, rendered)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, r.Header.Get("Authorization"))
			assert.Equal(t, "/video.mp4", r.URL.Path)
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = io.WriteString(w, "synthetic-video-bytes")
		}))
		defer server.Close()
		data["content_url"] = server.URL + "/video.mp4"
		content = call("buildContentRequest", map[string]any{"artifactKey": "video", "data": data, "apiKey": "fixture-only-key", "clientRequest": map[string]any{"method": "GET", "headers": map[string]any{"Authorization": "private-client-token"}}})
		assert.True(t, content["credentialless"].(bool))
		assert.NotContains(t, content, "headers")
		resp, requestErr := server.Client().Get(content["url"].(string))
		require.NoError(t, requestErr)
		payload, readErr := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, readErr)
		assert.Equal(t, "synthetic-video-bytes", string(payload))
		for reason, expected := range map[string]string{"internal_moderation": "user", "cancelled_by_operator": "cancelled", "quota_exhausted": "upstream"} {
			value, callErr := plugin.Engine.Call(t.Context(), "classifyFailure", reason)
			require.NoError(t, callErr)
			assert.Equal(t, expected, value)
		}
	})
}

func TestIJM2APIVideoPlugin(t *testing.T) {
	source, err := Source("ijm2api")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "ijm2api"})
	require.NoError(t, err)

	t.Run("unified sales select the matching vendor resolution", func(t *testing.T) {
		for _, tc := range []struct{ model, tier, vendor string }{
			{"seedance-2.0", "720p", "sd-2-0"},
			{"seedance-2.0", "1080p", "sd-2-0-1080p"},
			{"seedance-2.0", "4k", "sd-2-0-4k"},
			{"seedance-2.0-fast", "720p", "sd-2-0-fast"},
			{"seedance-2.0-mini", "720p", "sd-2-0-mini"},
			{"seedance-2.5", "480p", "sd-2-5-480p"},
			{"seedance-2.5", "720p", "sd-2-5"},
			{"seedance-2.5", "1080p", "sd-2-5-1080p"},
		} {
			t.Run(tc.model+"/"+tc.tier, func(t *testing.T) {
				binding, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", tc.model)
				require.True(t, found)
				assert.Same(t, plugin, binding.Plugin)
				input := map[string]any{"model": tc.model, "prompt": "cat", "seconds": 15, "resolution": tc.tier, "generate_audio": false,
					"first_image_url": "https://cdn.example/first.png", "last_frame_url": "https://cdn.example/last.png"}
				fields := map[string][]string{"model": {tc.model}, "prompt": {"cat"}, "seconds": {"15"}, "resolution": {tc.tier}, "generate_audio": {"false"},
					"first_image_url": {"https://cdn.example/first.png"}, "last_frame_url": {"https://cdn.example/last.png"}}
				var firstIntent any
				for _, body := range []map[string]any{{"kind": "json", "value": input}, {"kind": "multipart", "fields": fields}} {
					decoded, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": tc.model, "body": body})
					require.NoError(t, callErr)
					if firstIntent != nil {
						assert.Equal(t, firstIntent, decoded)
					}
					firstIntent = decoded
					intent := decoded.(map[string]any)
					assert.Equal(t, tc.model, intent["model"])
					assert.Equal(t, "reference_to_video", intent["action"])
					ctx := map[string]any{"model": tc.model, "requestBody": intent["requestBody"], "baseUrl": "https://ijm.example", "apiKey": "fixture-only-key"}
					built, callErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
					require.NoError(t, callErr)
					assert.NotContains(t, built, "model", "preserve the host's billing/display model")
					wire := built.(map[string]any)["body"].(map[string]any)
					assert.Equal(t, tc.vendor, wire["model"])
					assert.Equal(t, tc.tier, wire["resolution"])
					assert.Equal(t, false, wire["generate_audio"])
					assert.Equal(t, "https://cdn.example/first.png", wire["first_frame_url"])
					assert.Equal(t, "https://cdn.example/last.png", wire["last_frame_url"])
					assert.NotContains(t, wire, "first_image_url")
					value, callErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
					require.NoError(t, callErr)
					got, _, parseErr := spec.Parse(value.(map[string]any))
					require.NoError(t, parseErr)
					assert.Equal(t, tc.tier, got.Tier)
					assert.EqualValues(t, 15, *got.OutputSeconds)
					assert.Equal(t, map[string]int{"image": 0, "video": 0, "audio": 0, "frame": 2}, got.References)
					usage, callErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
					require.NoError(t, callErr)
					assert.EqualValues(t, 15, usage.(map[string]any)["seconds"])
					_, callErr = plugin.Meta.ValidateUsageFacts(usage.(map[string]any), tc.model)
					require.NoError(t, callErr)
				}
			})
		}
		for _, model := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini"} {
			for _, hook := range []string{"buildSubmitRequest", "extractUsage", "describeSpec"} {
				_, callErr := plugin.Engine.Call(t.Context(), hook, map[string]any{"model": model, "requestBody": map[string]any{"prompt": "cat", "resolution": "480p"}})
				require.ErrorContains(t, callErr, "supports only", model+"/"+hook)
			}
		}
	})

	t.Run("family defaults and channel mapping preserve the client model", func(t *testing.T) {
		for _, tc := range []struct{ model, upstream, fields, vendor, tier string }{
			{"seedance-2.0", "", `{}`, "sd-2-0", "720p"},
			{"seedance-2.0-fast", "", `{}`, "sd-2-0-fast", "720p"},
			{"seedance-2.0-mini", "", `{}`, "sd-2-0-mini", "720p"},
			{"seedance-2.5", "", `{}`, "sd-2-5", "720p"},
			{"public-video", "seedance-2.0", `{"size":"3840x2160"}`, "sd-2-0-4k", "4k"},
			{"public-video", "seedance-2.5", `{"resolution":"480p","seconds":30}`, "sd-2-5-480p", "480p"},
			{"public-video", "seedance-2.0-fast", `{}`, "sd-2-0-fast", "720p"},
			{"public-video", "seedance-2.0-mini", `{}`, "sd-2-0-mini", "720p"},
			{"seedance-2.0", "sd-2-5-1080p", `{"seconds":30}`, "sd-2-5-1080p", "1080p"},
		} {
			t.Run(tc.model+"/"+tc.upstream+"/"+tc.tier, func(t *testing.T) {
				input := map[string]any{"model": tc.model, "prompt": "cat"}
				require.NoError(t, common.UnmarshalJsonStr(tc.fields, &input))
				decoded, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
					"model": tc.model, "body": map[string]any{"kind": "json", "value": input},
				})
				require.NoError(t, callErr)
				intent := decoded.(map[string]any)
				assert.Equal(t, tc.model, intent["model"])
				if _, hasTier := input["resolution"]; !hasTier && input["size"] == nil {
					assert.NotContains(t, intent["requestBody"], "resolution", "defer defaults until channel mapping")
				}
				ctx := map[string]any{"model": tc.model, "upstreamModel": tc.upstream, "requestBody": intent["requestBody"], "baseUrl": "https://ijm.example", "apiKey": "fixture-only-key"}
				before, marshalErr := common.Marshal(ctx)
				require.NoError(t, marshalErr)
				built, callErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, callErr)
				assert.NotContains(t, built, "model")
				wire := built.(map[string]any)["body"].(map[string]any)
				assert.Equal(t, tc.vendor, wire["model"])
				assert.Equal(t, tc.tier, wire["resolution"])
				assert.NotContains(t, wire, "generate_audio")
				value, callErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
				require.NoError(t, callErr)
				assert.Equal(t, tc.tier, value.(map[string]any)["resolution"])
				usage, callErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
				require.NoError(t, callErr)
				assert.Equal(t, wire["duration"], usage.(map[string]any)["seconds"])
				after, marshalErr := common.Marshal(ctx)
				require.NoError(t, marshalErr)
				assert.JSONEq(t, string(before), string(after))
			})
		}
	})

	t.Run("mapped model limits defaults and scheduling agree", func(t *testing.T) {
		for _, tc := range []struct {
			model, tier                        string
			maxSeconds, images, videos, audios int
		}{
			{"sd-2-0", "720p", 15, 9, 3, 3},
			{"sd-2-0-1080p", "1080p", 15, 9, 3, 3},
			{"sd-2-0-4k", "4k", 15, 9, 3, 3},
			{"sd-2-0-fast", "720p", 15, 9, 3, 3},
			{"sd-2-0-mini", "720p", 15, 9, 3, 3},
			{"sd-2-5", "720p", 30, 30, 10, 10},
			{"sd-2-5-480p", "480p", 30, 30, 10, 10},
			{"sd-2-5-480p-x", "480p", 30, 30, 10, 10},
			{"sd-2-5-1080p", "1080p", 30, 30, 10, 10},
			{"sd-2-5-480p-preview", "480p", 30, 30, 10, 10},
		} {
			t.Run(tc.model, func(t *testing.T) {
				binding, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", tc.model)
				require.True(t, found)
				assert.Same(t, plugin, binding.Plugin)
				for _, maximum := range []bool{false, true} {
					input := map[string]any{"model": "public-video", "prompt": "海边日出"}
					seconds := 4
					counts := map[string]int{"image": 0, "video": 0, "audio": 0, "frame": 0}
					if maximum {
						seconds = tc.maxSeconds
						input["seconds"], input["duration"], input["n"] = fmt.Sprint(seconds), seconds, 1
						input["generate_audio"] = false
						for field, count := range map[string]int{"images": tc.images, "videos": tc.videos, "audios": tc.audios} {
							urls := make([]string, count)
							for i := range urls {
								urls[i] = "https://cdn.example/" + field
							}
							input[field] = urls
							counts[strings.TrimSuffix(field, "s")] = count
						}
						input["image_urls"] = input["images"]
					}
					before, marshalErr := common.Marshal(input)
					require.NoError(t, marshalErr)
					// Decode before mapping, exactly as candidate filtering does.
					decodeCtx := map[string]any{"model": "public-video", "body": map[string]any{"kind": "json", "value": input}}
					decoded, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, decodeCtx)
					require.NoError(t, decodeErr)
					repeated, repeatErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, decodeCtx)
					require.NoError(t, repeatErr)
					assert.Equal(t, decoded, repeated)
					after, marshalErr := common.Marshal(input)
					require.NoError(t, marshalErr)
					assert.JSONEq(t, string(before), string(after))
					intent := decoded.(map[string]any)
					assert.Equal(t, "public-video", intent["model"])
					assert.NotContains(t, intent["requestBody"], "resolution", "mapping must select the default tier")
					ctx := map[string]any{"model": "public-video", "upstreamModel": tc.model, "requestBody": intent["requestBody"], "baseUrl": "https://ijm.example/", "apiKey": "fixture-only-key"}
					built, buildErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
					require.NoError(t, buildErr)
					request := built.(map[string]any)
					wire := request["body"].(map[string]any)
					assert.Equal(t, "https://ijm.example/v1/videos", request["url"])
					assert.NotContains(t, request, "model")
					assert.Equal(t, tc.model, wire["model"])
					assert.Equal(t, tc.tier, wire["resolution"])
					assert.EqualValues(t, seconds, wire["duration"])
					assert.Equal(t, "16:9", wire["aspect_ratio"])
					if maximum {
						assert.Equal(t, false, wire["generate_audio"])
					} else {
						assert.NotContains(t, wire, "generate_audio")
					}
					usage, usageErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
					require.NoError(t, usageErr)
					usageJSON, marshalErr := common.Marshal(usage)
					require.NoError(t, marshalErr)
					assert.JSONEq(t, fmt.Sprintf(`{"seconds":%d}`, seconds), string(usageJSON))
					_, usageErr = plugin.Meta.ValidateUsageFacts(usage.(map[string]any), tc.model)
					require.NoError(t, usageErr)
					value, specErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
					require.NoError(t, specErr)
					got, ignored, specErr := spec.Parse(value.(map[string]any))
					require.NoError(t, specErr)
					assert.Empty(t, ignored)
					assert.EqualValues(t, seconds, *got.OutputSeconds)
					wantTier := tc.tier
					assert.Equal(t, wantTier, got.Tier)
					assert.Equal(t, counts, got.References)
					assert.Len(t, got.ReferenceVideoURLs, counts["video"])
					for _, url := range got.ReferenceVideoURLs {
						assert.Equal(t, "https://cdn.example/videos", url)
					}
					// Synthetic prices exercise quotation only; no vendor prices are implied.
					quote := videosched.Quote(videosched.CostConfig{Mode: videosched.ModePerSecond, Prices: map[string]float64{wantTier: 0.5}, References: map[string]map[string]videosched.ReferenceCost{
						"image": {"*": {Mode: videosched.RefIncluded}}, "video": {"*": {Mode: videosched.RefIncluded}}, "audio": {"*": {Mode: videosched.RefIncluded}},
					}}, got)
					assert.Empty(t, quote.Reason)
					assert.Equal(t, float64(seconds)*0.5, quote.TotalUSD)
				}
			})
		}
	})

	t.Run("frame roles survive JSON multipart mapping and scheduling", func(t *testing.T) {
		for _, model := range []string{"sd-2-0", "sd-2-0-1080p", "sd-2-0-4k", "sd-2-0-fast", "sd-2-0-mini", "sd-2-5", "sd-2-5-480p", "sd-2-5-480p-x", "sd-2-5-1080p", "sd-2-5-480p-preview"} {
			for _, count := range []int{1, 2} {
				t.Run(fmt.Sprintf("%s/%d", model, count), func(t *testing.T) {
					input := map[string]any{"model": "public-video", "prompt": "First to last", "first_image_url": "https://cdn.example/first.png", "first_frame_url": "https://cdn.example/first.png", "generate_audio": false}
					fields := map[string][]string{"model": {"public-video"}, "prompt": {"First to last"}, "first_image_url": {"https://cdn.example/first.png"}, "first_frame_url": {"https://cdn.example/first.png"}, "generate_audio": {"false"}}
					if count == 2 {
						input["last_image_url"] = "https://cdn.example/last.png"
						fields["last_image_url"] = []string{"https://cdn.example/last.png"}
					}
					var firstIntent any
					for _, body := range []map[string]any{{"kind": "json", "value": input}, {"kind": "multipart", "fields": fields, "files": []any{}}} {
						decoded, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "public-video", "body": body})
						require.NoError(t, callErr)
						if firstIntent != nil {
							assert.Equal(t, firstIntent, decoded)
						}
						firstIntent = decoded
						intent := decoded.(map[string]any)
						assert.Equal(t, "reference_to_video", intent["action"])
						ctx := map[string]any{"model": "public-video", "upstreamModel": model, "requestBody": intent["requestBody"], "baseUrl": "https://ijm.example", "apiKey": "fixture-only-key"}
						built, callErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
						require.NoError(t, callErr)
						wire := built.(map[string]any)["body"].(map[string]any)
						assert.Equal(t, model, wire["model"])
						assert.Equal(t, "https://cdn.example/first.png", wire["first_frame_url"])
						assert.NotContains(t, wire, "first_image_url")
						assert.NotContains(t, wire, "image_urls")
						assert.Equal(t, false, wire["generate_audio"])
						if count == 2 {
							assert.Equal(t, "https://cdn.example/last.png", wire["last_frame_url"])
						} else {
							assert.NotContains(t, wire, "last_frame_url")
						}
						value, callErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
						require.NoError(t, callErr)
						got, _, parseErr := spec.Parse(value.(map[string]any))
						require.NoError(t, parseErr)
						assert.Equal(t, map[string]int{"image": 0, "video": 0, "audio": 0, "frame": count}, got.References)
						usage, callErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
						require.NoError(t, callErr)
						assert.EqualValues(t, 4, usage.(map[string]any)["seconds"])
					}
				})
			}
		}
	})

	t.Run("invalid inputs fail decoding billing scheduling and overrides", func(t *testing.T) {
		for _, tc := range []struct{ name, model, fields, message string }{
			{"below minimum", "sd-2-5", `{"seconds":3}`, "between 4 and 30"},
			{"above family maximum", "sd-2-0", `{"seconds":16}`, "4 to 15"},
			{"above maximum", "sd-2-5", `{"duration":31}`, "between 4 and 30"},
			{"overflow", "sd-2-5", `{"seconds":18446744073686646784}`, "between 4 and 30"},
			{"fraction", "sd-2-5", `{"seconds":4.5}`, "integer"},
			{"null seconds", "sd-2-5", `{"seconds":null}`, "integer"},
			{"boolean seconds", "sd-2-5", `{"seconds":true}`, "integer"},
			{"seconds conflict", "sd-2-5", `{"seconds":4,"duration":5}`, "conflicting values"},
			{"fixed resolution", "sd-2-0-mini", `{"resolution":"480p"}`, "uses 720p"},
			{"explicit native pin stays fixed", "sd-2-0", `{"resolution":"1080p"}`, "uses 720p"},
			{"unified mini unsupported tier", "seedance-2.0-mini", `{"resolution":"1080p"}`, "supports only 720p"},
			{"unified fast unsupported tier", "seedance-2.0-fast", `{"resolution":"4k"}`, "supports only 720p"},
			{"unified 2.5 unsupported tier", "seedance-2.5", `{"resolution":"4k"}`, "supports only"},
			{"unified fast family duration", "seedance-2.0-fast", `{"seconds":16}`, "4 to 15"},
			{"unified mini family duration", "seedance-2.0-mini", `{"seconds":16}`, "4 to 15"},
			{"resolution conflict", "sd-2-5", `{"resolution":"720p","size":"1920x1080"}`, "size conflicts with resolution"},
			{"ratio conflict", "sd-2-5", `{"ratio":"16:9","aspect_ratio":"9:16"}`, "conflicting values"},
			{"size ratio conflict", "sd-2-5", `{"size":"720x1280","ratio":"16:9"}`, "size conflicts with ratio"},
			{"unknown resolution", "sd-2-5", `{"size":"1440p"}`, "resolution must be"},
			{"invalid ratio", "sd-2-5", `{"ratio":"auto"}`, "unsupported aspect ratio"},
			{"batch", "sd-2-5", `{"n":2}`, "n must be 1"},
			{"zero batch", "sd-2-5", `{"n":0}`, "n must be 1"},
			{"string batch", "sd-2-5", `{"n":"1"}`, "n must be 1"},
			{"string audio", "sd-2-5", `{"generate_audio":"false"}`, "must be a boolean"},
			{"numeric audio", "sd-2-5", `{"generate_audio":0}`, "must be a boolean"},
			{"null audio", "sd-2-5", `{"generate_audio":null}`, "must be a boolean"},
			{"invalid first frame", "sd-2-5", `{"first_image_url":"file:///tmp/image.png"}`, "HTTP(S) image URL"},
			{"last frame without first", "sd-2-5", `{"last_image_url":"https://cdn.example/i"}`, "requires first_frame_url"},
			{"frame aliases conflict", "sd-2-5", `{"first_frame_url":"https://cdn.example/a","first_image_url":"https://cdn.example/b"}`, "conflicting values"},
			{"mixed frame and reference", "sd-2-5", `{"first_frame_url":"https://cdn.example/a","image_urls":["https://cdn.example/b"]}`, "cannot be combined"},
			{"mixed frame and video", "sd-2-5", `{"first_frame_url":"https://cdn.example/a","video_urls":["https://cdn.example/b"]}`, "cannot be combined"},
			{"mixed frame and audio", "sd-2-5", `{"first_frame_url":"https://cdn.example/a","audio_urls":["https://cdn.example/b"]}`, "cannot be combined"},
			{"null frame", "sd-2-5", `{"first_frame_url":null}`, "HTTP(S) image URL"},
			{"nested multiplier", "sd-2-5", `{"metadata":{"duration":99999}}`, "unsupported video parameter"},
			{"blank prompt", "sd-2-5", `{"prompt":" "}`, "prompt is required"},
			{"scalar reference", "sd-2-5", `{"images":"https://cdn.example/i"}`, "must be an array"},
			{"invalid reference type", "sd-2-5", `{"videos":[1]}`, "HTTP(S) URLs"},
			{"credential reference", "sd-2-5", `{"audios":["https://user:pass@cdn.example/a"]}`, "without credentials"},
			{"file reference", "sd-2-5", `{"images":["file:///tmp/i"]}`, "HTTP(S) URLs"},
			{"alias conflict", "sd-2-5", `{"images":[],"image_urls":["https://cdn.example/i"]}`, "conflicting reference fields"},
			{"unknown model", "unknown-model", `{}`, "unsupported IJM2API model"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				input := map[string]any{"model": "public-video", "prompt": "cat"}
				require.NoError(t, common.UnmarshalJsonStr(tc.fields, &input))
				ctx := map[string]any{"model": "public-video", "upstreamModel": tc.model, "body": map[string]any{"kind": "json", "value": input}, "requestBody": input, "baseUrl": "https://ijm.example", "apiKey": "fixture-only-key"}
				_, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, ctx)
				require.ErrorContains(t, callErr, tc.message)
				for _, hook := range []string{"extractUsage", "describeSpec", "buildSubmitRequest"} {
					_, callErr = plugin.Engine.Call(t.Context(), hook, ctx)
					require.ErrorContains(t, callErr, tc.message, hook)
				}
			})
		}
		for model, limits := range map[string]map[string]int{"sd-2-0-mini": {"images": 9, "videos": 3, "audios": 3}, "sd-2-5": {"images": 30, "videos": 10, "audios": 10}} {
			for field, limit := range limits {
				urls := make([]string, limit+1)
				for i := range urls {
					urls[i] = "https://cdn.example/media"
				}
				ctx := map[string]any{"model": model, "requestBody": map[string]any{"prompt": "cat", field: urls}}
				for _, hook := range []string{"extractUsage", "describeSpec", "buildSubmitRequest"} {
					_, callErr := plugin.Engine.Call(t.Context(), hook, ctx)
					require.ErrorContains(t, callErr, fmt.Sprint("at most ", limit), model+"/"+field+"/"+hook)
				}
			}
		}
	})

	_, err = jsplugin.ReplayFixture(t.Context(), source, []byte(`{"cases":[
		{"name":"4k dimensions and true audio","hook":"buildSubmitRequest","args":[{"model":"public","upstreamModel":"sd-2-0-4k","baseUrl":"https://ijm.example/","authHeader":"Bearer fixture-only-key","requestBody":{"prompt":"cat","seconds":"15","duration":15,"size":"3840x2160","resolution":"2160p","ratio":"16:9","generate_audio":true}}],"expected":{"url":"https://ijm.example/v1/videos","method":"POST","headers":{"Authorization":"Bearer fixture-only-key","Content-Type":"application/json"},"body":{"model":"sd-2-0-4k","prompt":"cat","duration":15,"resolution":"4k","aspect_ratio":"16:9","generate_audio":true}}},
		{"name":"multipart canvas","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"model":"sd-2-5","body":{"kind":"multipart","fields":{"model":["sd-2-5"],"prompt":["cat"],"seconds":["30"],"generate_audio":["false"],"n":["1"],"videos[]":["https://cdn.example/v","https://cdn.example/v"],"images":["[\"https://cdn.example/i\"]"]},"files":[]}}],"expected":{"kind":"submit","model":"sd-2-5","action":"reference_to_video","requestBody":{"prompt":"cat","duration":30,"aspect_ratio":"16:9","generate_audio":false,"video_urls":["https://cdn.example/v","https://cdn.example/v"],"image_urls":["https://cdn.example/i"]}}},
		{"name":"file uploads rejected","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","fields":{},"files":[{"ref":"request_file:input_reference"}]}}],"expectedError":"local file uploads are unsupported"},
		{"name":"duplicate scalar rejected","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","fields":{"seconds":["4","30"]}}}],"expectedError":"must be provided once"},
		{"name":"multipart invalid boolean","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","fields":{"generate_audio":["0"]}}}],"expectedError":"must be a boolean"},
		{"name":"query id encoding","hook":"buildQueryRequest","args":[{"taskId":"ijm/a?b","baseUrl":"https://ijm.example/","apiKey":"fixture-only-key"}],"expected":{"url":"https://ijm.example/v1/videos/ijm%2Fa%3Fb","method":"GET","headers":{"Authorization":"Bearer fixture-only-key"}}},
		{"name":"queued submit","hook":"parseSubmitResponse","args":[{},{"body":{"id":"ijm_test","status":"queued","progress":0,"data":[]}}],"expected":{"taskId":"ijm_test","taskData":{"id":"ijm_test","status":"queued","progress":0,"data":[]}}},
		{"name":"ambiguous submission","hook":"parseSubmitResponse","args":[{},{"body":{"error":"retry later"}}],"expectedError":"missing a task id"},
		{"name":"immediate failure is accepted task","hook":"parseSubmitResponse","args":[{},{"body":{"id":"ijm_test","status":"failed","error":{"code":"CONTENT_MODERATION_FAILED","message":"内容违规"}}}],"expected":{"taskId":"ijm_test","taskData":{"id":"ijm_test","status":"failed","error":{"code":"CONTENT_MODERATION_FAILED","message":"内容违规"}},"immediate":{"status":"FAILURE","reason":"CONTENT_MODERATION_FAILED: 内容违规"}}},
		{"name":"immediate completion","hook":"parseSubmitResponse","args":[{},{"body":{"id":"ijm_test","status":"succeeded","data":[{"url":"https://cdn.example/v.mp4"}]}}],"expected":{"taskId":"ijm_test","taskData":{"id":"ijm_test","status":"succeeded","data":[{"url":"https://cdn.example/v.mp4"}]},"immediate":{"status":"SUCCESS","progress":"100%"}}},
		{"name":"queued zero progress","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"queued","progress":0}],"expected":{"status":"QUEUED","progress":"0%"}},
		{"name":"preparing uploads","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"preparing","progress":12}],"expected":{"status":"IN_PROGRESS","progress":"12%"}},
		{"name":"submitted","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"submitted","progress":40}],"expected":{"status":"SUBMITTED","progress":"40%"}},
		{"name":"running","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"running","progress":45}],"expected":{"status":"IN_PROGRESS","progress":"45%"}},
		{"name":"invalid progress ignored","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"running","progress":1000}],"expected":{"status":"IN_PROGRESS"}},
		{"name":"expired","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"expired"}],"expected":{"status":"FAILURE","reason":"UPSTREAM_TIMEOUT: video generation failed"}},
		{"name":"id mismatch cannot settle","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_other","status":"succeeded","data":[{"url":"https://cdn.example/v"}]}],"expected":{"status":"UNKNOWN","reason":"invalid or mismatched IJM2API task id"}},
		{"name":"unknown state","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"invented"}],"expected":{"status":"UNKNOWN","reason":"unrecognized task status or missing video URL"}},
		{"name":"success needs URL","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"succeeded","data":[]}],"expected":{"status":"UNKNOWN","reason":"unrecognized task status or missing video URL"}},
		{"name":"conflicting error","hook":"parseTaskResult","args":[{"taskId":"ijm_test"},{"id":"ijm_test","status":"succeeded","error":{"code":"UPSTREAM_FAILED"},"data":[{"url":"https://cdn.example/v"}]}],"expected":{"status":"UNKNOWN","reason":"conflicting IJM2API task response"}},
		{"name":"moderation attribution","hook":"classifyFailure","args":["CONTENT_MODERATION_FAILED: localized text"],"expected":"user"},
		{"name":"reference constraint attribution","hook":"classifyFailure","args":["VIDEO_EDIT_CONSTRAINT: localized text"],"expected":"user"},
		{"name":"upload failure is upstream","hook":"classifyFailure","args":["MEDIA_UPLOAD_FAILED: localized text"],"expected":"upstream"},
		{"name":"timeout remains upstream","hook":"classifyFailure","args":["UPSTREAM_TIMEOUT: 内容违规"],"expected":"upstream"},
		{"name":"completion cannot infer credits or elapsed time","hook":"extractUsageOnComplete","args":[{}, {"status":"SUCCESS"},{"created_at":100,"completed_at":500,"actual_cost":20,"duration":99999}],"expected":{}},
		{"name":"content has no credentials","hook":"buildContentRequest","args":[{"artifactKey":"video","apiKey":"fixture-only-key","clientRequest":{"method":"HEAD","headers":{"Authorization":"client-secret"}},"data":{"status":"succeeded","data":[{"url":"https://cdn.example/v.mp4?sig=fixture%2Bvalue"}]}}],"expected":{"url":"https://cdn.example/v.mp4?sig=fixture%2Bvalue","method":"HEAD","credentialless":true}},
		{"name":"invalid artifact URL","hook":"buildContentRequest","args":[{"artifactKey":"video","data":{"status":"succeeded","data":[{"url":"https://user:pass@cdn.example/v"}]}}],"expectedError":"artifact_not_found"},
		{"name":"no unfinished artifacts","hook":"listArtifacts","args":[{"status":"IN_PROGRESS","data":{"status":"succeeded","data":[{"url":"https://cdn.example/v"}]}}],"expected":[]},
		{"name":"video artifact","hook":"listArtifacts","args":[{"status":"SUCCESS","data":{"status":"succeeded","data":[{"url":"https://cdn.example/v"}]}}],"expected":[{"key":"video","type":"video","mimeType":"video/mp4"}]},
		{"name":"renderer excludes private account and task","hook":"protocols","path":["openai_video","render"],"args":[{},{"status":"SUCCESS","data":{"id":"ijm_private","account_id":42,"status":"succeeded","data":[{"url":"https://cdn.example/v"}]}}],"expected":{"url":"https://cdn.example/v"}}
	]}`))
	require.NoError(t, err)
}

// Real local HTTP transport with a simulated IJM2API response, without paid work.
func TestIJM2APIHTTPRoundTrip(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/videos":
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "Bearer fixture-only-key", r.Header.Get("Authorization"))
			var body map[string]any
			if !assert.NoError(t, common.DecodeJson(r.Body, &body)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.Equal(t, "sd-2-5-480p", body["model"])
			assert.Equal(t, "480p", body["resolution"])
			assert.EqualValues(t, 30, body["duration"])
			assert.Equal(t, false, body["generate_audio"])
			assert.Equal(t, "https://cdn.example/first.png", body["first_frame_url"])
			assert.Equal(t, "https://cdn.example/last.png", body["last_frame_url"])
			assert.NotContains(t, body, "first_image_url")
			assert.NotContains(t, body, "last_image_url")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"ijm_http","status":"queued","data":[]}`)
		case "/v1/videos/ijm_http":
			assert.Equal(t, "GET", r.Method)
			assert.Equal(t, "Bearer fixture-only-key", r.Header.Get("Authorization"))
			_, _ = fmt.Fprintf(w, `{"id":"ijm_http","status":"succeeded","data":[{"url":"http://%s/media.mp4"}],"account_id":42}`, r.Host)
		case "/media.mp4":
			assert.Empty(t, r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = io.WriteString(w, "synthetic-video-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	source, err := Source("ijm2api")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "ijm2api"})
	require.NoError(t, err)
	decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
		"model": "public-video", "body": map[string]any{"kind": "json", "value": map[string]any{"model": "public-video", "prompt": "test", "seconds": 30, "resolution": "480p", "generate_audio": false,
			"first_image_url": "https://cdn.example/first.png", "last_image_url": "https://cdn.example/last.png"}},
	})
	require.NoError(t, err)
	ctx := map[string]any{"model": "public-video", "upstreamModel": "seedance-2.5", "baseUrl": server.URL, "apiKey": "fixture-only-key", "requestBody": decoded.(map[string]any)["requestBody"]}
	var data map[string]any
	for _, stage := range []string{"submit", "query", "content"} {
		hook := map[string]string{"submit": "buildSubmitRequest", "query": "buildQueryRequest", "content": "buildContentRequest"}[stage]
		value, callErr := plugin.Engine.Call(t.Context(), hook, ctx)
		require.NoError(t, callErr)
		encoded, marshalErr := common.Marshal(value)
		require.NoError(t, marshalErr)
		var descriptor struct {
			URL            string            `json:"url"`
			Method         string            `json:"method"`
			Headers        map[string]string `json:"headers"`
			Body           map[string]any    `json:"body"`
			Credentialless bool              `json:"credentialless"`
		}
		require.NoError(t, common.Unmarshal(encoded, &descriptor))
		var body []byte
		if descriptor.Body != nil {
			body, err = common.Marshal(descriptor.Body)
			require.NoError(t, err)
		}
		req, requestErr := http.NewRequestWithContext(t.Context(), descriptor.Method, descriptor.URL, bytes.NewReader(body))
		require.NoError(t, requestErr)
		for name, value := range descriptor.Headers {
			req.Header.Set(name, value)
		}
		response, requestErr := server.Client().Do(req)
		require.NoError(t, requestErr)
		payload, readErr := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, readErr)
		if stage == "content" {
			assert.True(t, descriptor.Credentialless)
			assert.Equal(t, http.StatusOK, response.StatusCode)
			assert.Equal(t, "synthetic-video-bytes", string(payload))
			continue
		}
		require.NoError(t, common.Unmarshal(payload, &data))
		if stage == "submit" {
			assert.Equal(t, http.StatusAccepted, response.StatusCode)
			parsed, parseErr := plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx, map[string]any{"statusCode": response.StatusCode, "body": data})
			require.NoError(t, parseErr)
			ctx["taskId"] = parsed.(map[string]any)["taskId"]
		} else {
			result, parseErr := plugin.Engine.Call(t.Context(), "parseTaskResult", ctx, data)
			require.NoError(t, parseErr)
			assert.Equal(t, "SUCCESS", result.(map[string]any)["status"])
			ctx["data"], ctx["artifactKey"], ctx["clientRequest"] = data, "video", map[string]any{"method": "GET"}
		}
	}
	assert.Equal(t, []string{"POST /v1/videos", "GET /v1/videos/ijm_http", "GET /media.mp4"}, requests)
}

func TestBuiltInVendorPluginsDeclareNativeRoutesAndLegacyChannelTypes(t *testing.T) {
	generation := jsplugin.DefaultRegistry.Generation()
	require.NotNil(t, generation)

	routes := []struct {
		method    string
		path      string
		key       string
		routeType jsplugin.RouteType
		action    string
		renderer  string
	}{
		{"POST", "/kling/v1/videos/text2video", "kling", jsplugin.RouteTypeSubmit, "text_to_video", "taskCreated"},
		{"POST", "/kling/v1/videos/image2video", "kling", jsplugin.RouteTypeSubmit, "image_to_video", "taskCreated"},
		{"GET", "/kling/v1/videos/text2video/:task_id", "kling", jsplugin.RouteTypeQuery, "", "taskStatus"},
		{"GET", "/kling/v1/videos/image2video/:task_id", "kling", jsplugin.RouteTypeQuery, "", "taskStatus"},
		{"POST", "/jimeng/", "jimeng", jsplugin.RouteTypeDynamic, "", "renderTask"},
		{"POST", "/suno/submit/:action", "sunoapi", jsplugin.RouteTypeSubmit, "", "renderSubmit"},
		{"POST", "/suno/fetch", "sunoapi", jsplugin.RouteTypeDynamic, "", "renderTasks"},
		{"GET", "/suno/fetch/:task_id", "sunoapi", jsplugin.RouteTypeQuery, "", "renderTask"},
		{"POST", "/doubao/api/v3/contents/generations/tasks", "doubao", jsplugin.RouteTypeSubmit, "", "taskCreated"},
		{"GET", "/doubao/api/v3/contents/generations/tasks/:task_id", "doubao", jsplugin.RouteTypeQuery, "", "taskStatus"},
		{"POST", "/doubao/api/v3/images/generations", "doubao", jsplugin.RouteTypeSubmit, "", "imageCreated"},
	}
	for _, expected := range routes {
		t.Run(expected.method+" "+expected.path, func(t *testing.T) {
			binding, found := generation.LookupDeclaredRoute(expected.method, expected.path)
			require.True(t, found)
			require.Equal(t, expected.key, binding.Plugin.Meta.Key)
			require.Equal(t, expected.routeType, binding.Route.Type)
			require.Equal(t, expected.action, binding.Route.Action)
			require.Equal(t, expected.renderer, binding.Route.Render)
		})
	}

	// The legacy Wan edit route serves both the wan2.5 image-list body and the
	// wanx2.1-imageedit function API.
	imageEdit, found := generation.LookupDeclaredRoute("POST", "/ali/api/v1/services/aigc/image2image/image-synthesis")
	require.True(t, found)
	assert.Equal(t, "alibaba", imageEdit.Plugin.Meta.Key)
	assert.ElementsMatch(t, []string{"wan2.5-i2i-preview", "wanx2.1-imageedit"}, imageEdit.Route.Models)

	channelTypes := []struct {
		value int
		key   string
	}{
		{1, "sora"},
		{36, "sunoapi"},
		{45, "doubao"},
		{50, "kling"},
		{51, "jimeng"},
		{54, "doubao"},
		{55, "sora"},
	}
	for _, channelType := range channelTypes {
		plugin, found := generation.GetByChannelType(channelType.value)
		require.True(t, found)
		require.Equal(t, channelType.key, plugin.Meta.Key)
	}
}

func TestBuiltInTaskPluginResponsesAndUsageContracts(t *testing.T) {
	generation := jsplugin.DefaultRegistry.Generation()
	require.NotNil(t, generation)

	entries, err := fs.ReadDir(taskPlugins, "tasks")
	require.NoError(t, err)
	actualKeys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			actualKeys = append(actualKeys, entry.Name())
		}
	}
	assert.Equal(t, expectedKeys, actualKeys)

	for _, key := range expectedKeys {
		t.Run(key, func(t *testing.T) {
			_, found := generation.Get(key)
			require.True(t, found, "factory plugin was excluded from the active generation")

			source, sourceErr := Source(key)
			require.NoError(t, sourceErr)
			registry := jsplugin.NewRegistry()
			plugin, registerErr := registry.RegisterFactory(source, jsplugin.Options{Key: key})
			require.NoError(t, registerErr)

			if slices.Contains(videoOnlyKeys, key) {
				claims := make([]string, 0, len(plugin.Meta.Protocols))
				for _, claim := range plugin.Meta.Protocols {
					claims = append(claims, claim.Name)
				}
				assert.Equal(t, []string{"openai_video"}, claims)
			} else {
				require.Contains(t, responsesKeys, key)
				var responsesClaim jsplugin.ProtocolClaim
				foundResponses := false
				for _, claim := range plugin.Meta.Protocols {
					if claim.Name == "openai_responses" {
						responsesClaim = claim
						foundResponses = true
						break
					}
				}
				require.True(t, foundResponses, "openai_responses claim must be present")
				assert.Equal(t, []string{"stream", "sync", "background"}, responsesClaim.Supports)
				for _, model := range plugin.Meta.Models {
					binding, claimed := registry.Generation().LookupEndpoint("POST", "/v1/responses", model)
					require.True(t, claimed, model)
					assert.Same(t, plugin, binding.Plugin)
				}
				for _, hook := range []string{"decodeRequest", "renderEvents", "renderFinal"} {
					callable, callableErr := plugin.Engine.HasCallablePath(t.Context(), "protocols", "openai_responses", hook)
					require.NoError(t, callableErr)
					assert.True(t, callable, hook)
				}
			}
			for _, hook := range []string{"extractUsage", "extractUsageOnComplete"} {
				callable, callableErr := plugin.Engine.HasExport(t.Context(), hook)
				require.NoError(t, callableErr)
				assert.True(t, callable, hook)
			}
			// An icon sidecar on disk must be embedded; test and data files must not.
			for _, name := range []string{"icon.svg", "icon.png"} {
				if _, statErr := os.Stat("tasks/" + key + "/" + name); statErr == nil {
					_, _, embedded := Icon(key)
					assert.True(t, embedded, "%s/%s is not in the go:embed pattern", key, name)
				}
			}
			for _, name := range []string{"fixture.json", "plugin.test.mjs", "test.mjs", "README.md"} {
				_, openErr := fs.Stat(taskPlugins, "tasks/"+key+"/"+name)
				assert.Error(t, openErr, "%s/%s must not ship in the binary", key, name)
			}
			// Only the third-party relays are schedulable; a pool holding an
			// official plugin is never taken over.
			callable, callableErr := plugin.Engine.HasCallablePath(t.Context(), "describeSpec")
			require.NoError(t, callableErr)
			assert.Equal(t, slices.Contains(videoOnlyKeys, key), callable, "describeSpec")
			// Schedulable plugins attribute terminal failures for channel health.
			if callable {
				for reason, class := range map[string]string{
					"upstream timeout":                       "upstream",
					"content moderation rejected the prompt": "user",
					"内容违规":                                   "user",
					"task cancelled by user":                 "cancelled",
				} {
					value, callErr := plugin.Engine.Call(t.Context(), "classifyFailure", reason)
					require.NoError(t, callErr)
					assert.Equal(t, class, value, reason)
				}
			}
			require.NotEmpty(t, plugin.Meta.UsageSchema)
			for usageKey, schema := range plugin.Meta.UsageSchema {
				assert.NotEmpty(t, schema.Description, usageKey)
			}
		})
	}
}

func TestBuiltInResponsesDecodersEchoChannelMappedAlias(t *testing.T) {
	bodyOverrides := map[string]map[string]any{}
	for _, key := range responsesKeys {
		t.Run(key, func(t *testing.T) {
			source, sourceErr := Source(key)
			require.NoError(t, sourceErr)
			registry := jsplugin.NewRegistry()
			plugin, registerErr := registry.RegisterFactory(source, jsplugin.Options{Key: key})
			require.NoError(t, registerErr)
			require.NotEmpty(t, plugin.Meta.Models)

			alias := "alias-under-test"
			upstreamModel := plugin.Meta.Models[0]
			body := map[string]any{"model": alias, "input": "a cat walking on the beach"}
			if override, ok := bodyOverrides[key]; ok {
				body = override
			}
			value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_responses", "decodeRequest"}, map[string]any{
				"model": alias, "upstreamModel": upstreamModel, "stream": false,
				"body": map[string]any{"kind": "json", "value": body},
			})
			require.NoError(t, callErr)
			encoded, marshalErr := common.Marshal(value)
			require.NoError(t, marshalErr)
			var decoded map[string]any
			require.NoError(t, common.Unmarshal(encoded, &decoded))
			assert.Equal(t, "submit", decoded["kind"])
			assert.Equal(t, alias, decoded["model"])
		})
	}
}

// The describeSpec value crossing the JS runtime parses on the host: decode
// with the plugin itself, call the hook without credentials, then spec.Parse.
func TestBuiltInVideoRelaysDescribeParsableSpecs(t *testing.T) {
	images := []any{"https://cdn.example/1.png", "https://cdn.example/2.png"}
	video := []any{"https://cdn.example/1.mp4"}
	cases := []struct {
		key, model string
		body       map[string]any
		seconds    float64
		want       videosched.Spec
	}{
		{"aggc", "sd2.5-op", map[string]any{"prompt": "cat", "seconds": 30, "resolution": "1080p", "images": images, "videos": video}, 30,
			videosched.Spec{Tier: "1080p", References: map[string]int{"video": 1, "image": 2, "audio": 0, "frame": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"bytefor", "bytefor-2.5", map[string]any{"prompt": "cat", "seconds": 30, "resolution": "720p", "images": images, "videos": video, "audios": []any{"https://cdn.example/1.mp3"}}, 30,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 2, "audio": 1, "frame": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"cangyuan", "sd10-seedance-2.0", map[string]any{"prompt": "cat", "seconds": 10, "resolution": "720p", "images": images, "videos": video, "audios": []any{"https://cdn.example/1.wav"}}, 10,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 2, "audio": 1, "frame": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"cangyuan", "cangyuan-doubao-seedance-2-0-260128", map[string]any{"prompt": "cat", "seconds": 6, "resolution": "720p", "first_image_url": "https://cdn.example/first.png", "last_image_url": "asset://asset-last"}, 6,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 0, "image": 0, "audio": 0, "frame": 2}}},
		{"meaicc", "w3-c1", map[string]any{"input": map[string]any{"prompt": "cat", "media": []any{
			map[string]any{"type": "first_frame", "url": "https://cdn.example/f.png"},
			map[string]any{"type": "reference_video", "url": "https://cdn.example/v.mp4"},
		}}, "parameters": map[string]any{"duration": 12, "resolution": "1080p"}}, 12,
			videosched.Spec{Tier: "1080p", References: map[string]int{"video": 1, "image": 0, "audio": 0, "frame": 1}, ReferenceVideoURLs: []string{"https://cdn.example/v.mp4"}}},
		{"megabyai", "videos-fast", map[string]any{"prompt": "cat", "seconds": "8", "size": "1280x720", "referenceImages": images}, 8,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 0, "image": 2, "audio": 0, "frame": 0}}},
		{"onmi", "e组-sd2.0mini", map[string]any{"prompt": "cat", "seconds": 10, "resolution": "480p", "ratio": "16:9", "n": 1, "generate_audio": false, "images": images, "videos": video, "audios": []any{"https://cdn.example/1.mp3"}}, 10,
			videosched.Spec{Tier: "480p", References: map[string]int{"video": 1, "image": 2, "audio": 1, "frame": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"seedance-hjmie", "videos-fast", map[string]any{"prompt": "cat", "duration": 8, "resolution": "4k", "videos": video}, 8,
			videosched.Spec{Tier: "4k", References: map[string]int{"video": 1, "image": 0, "audio": 0, "frame": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"sudashui", "sdas-mg-sd2.5-720p", map[string]any{"prompt": "cat", "duration": 30, "resolution": "720p", "images": images, "videos": video}, 30,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 2, "audio": 0, "frame": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"paipu", "lec-seed-2-0-900", map[string]any{"prompt": "cat", "images": images}, 15,
			videosched.Spec{Tier: "*", SecondsKind: videosched.KindFixed, References: map[string]int{"video": 0, "image": 2, "audio": 0, "frame": 0}}},
		{"pidoi", "tejiasd-mini-720p", map[string]any{"prompt": "cat", "seconds": 8, "resolution": "480p", "images": images}, 8,
			videosched.Spec{Tier: "480p", References: map[string]int{"video": 0, "image": 2, "audio": 0, "frame": 0}}},
		{"vobilemax", "vobilemax-seedance-2.0", map[string]any{"prompt": "cat", "seconds": 4, "size": "720x1280", "images": images, "videos": video, "audios": []any{"https://cdn.example/1.wav"}}, 4,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 2, "audio": 1, "frame": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"zongheng", "XXseedacn2.5", map[string]any{"prompt": "cat", "seconds": 10, "size": "1280x720", "images": images, "videos": video, "start_frame": "https://cdn.example/start.png", "end_frame": "https://cdn.example/end.png"}, 10,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 2, "audio": 0, "frame": 2}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			source, sourceErr := Source(tc.key)
			require.NoError(t, sourceErr)
			plugin, registerErr := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: tc.key})
			require.NoError(t, registerErr)
			tc.body["model"] = tc.model
			decoded, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
				"model": tc.model, "upstreamModel": tc.model, "body": map[string]any{"kind": "json", "value": tc.body},
			})
			require.NoError(t, decodeErr)
			value, callErr := plugin.Engine.Call(t.Context(), "describeSpec", map[string]any{
				"model": tc.model, "upstreamModel": tc.model, "usagePurpose": "spec",
				"requestBody": decoded.(map[string]any)["requestBody"],
			})
			require.NoError(t, callErr)
			raw, ok := value.(map[string]any)
			require.True(t, ok, "describeSpec must return an object")
			got, ignored, parseErr := spec.Parse(raw)
			require.NoError(t, parseErr)
			assert.Empty(t, ignored, "built-in plugins return only contract fields")
			want := tc.want
			want.OutputSeconds = &tc.seconds
			if want.SecondsKind == "" {
				want.SecondsKind = videosched.KindExact
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestMegaVideoModelCatalog(t *testing.T) {
	source, err := Source("megabyai")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "megabyai"})
	require.NoError(t, err)
	for _, model := range []string{"videos-mini", "videos-fast", "videos-standard", "sd-2-720p"} {
		binding, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", model)
		require.True(t, found, "legacy model %s must remain routable", model)
		assert.Same(t, plugin, binding.Plugin)
	}
	for _, tc := range []struct {
		model                  string
		tiers                  []string
		images, videos, audios int
	}{
		{"seedance-2.0", []string{"480p", "720p", "1080p", "4k"}, 9, 3, 3},
		{"seedance-2.0-fast", []string{"480p", "720p"}, 9, 3, 3},
		{"seedance-2.0-mini", []string{"480p", "720p"}, 9, 3, 3},
		{"seedance-2.5", []string{"480p", "720p", "1080p"}, 30, 10, 10},
		{"sd-2.0-480p", []string{"480p"}, 9, 3, 3},
		{"sd-2.0-720p", []string{"720p"}, 9, 3, 3},
		{"sd-2.0-1080p", []string{"1080p"}, 9, 3, 3},
		{"sd-2.0-4k", []string{"4k"}, 9, 3, 3},
		{"sd-mini-480p", []string{"480p"}, 9, 3, 3},
		{"sd-mini-720p", []string{"720p"}, 9, 3, 3},
		{"sd-2.5-720p", []string{"720p"}, 30, 10, 10},
	} {
		t.Run(tc.model, func(t *testing.T) {
			binding, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", tc.model)
			require.True(t, found)
			assert.Same(t, plugin, binding.Plugin)
			references := map[string][]string{}
			for field, count := range map[string]int{"images": tc.images, "videos": tc.videos, "audios": tc.audios} {
				for i := range count {
					references[field] = append(references[field], fmt.Sprintf("https://cdn.example/%s/%d", field, i))
				}
			}
			for _, tier := range tc.tiers {
				for _, kind := range []string{"json", "multipart"} {
					t.Run(tier+"/"+kind, func(t *testing.T) {
						input := map[string]any{
							"model": "public-video", "prompt": "cat", "seconds": "8", "resolution": tier,
							"images": references["images"], "videos": references["videos"], "audios": references["audios"],
						}
						// Exercise fixed-tier inference as well as explicit resolution, including the 4k alias.
						if len(tc.tiers) == 1 && kind == "multipart" {
							delete(input, "resolution")
						} else if tier == "4k" {
							input["resolution"] = "2160p"
						}
						body := map[string]any{"kind": "json", "value": input}
						if kind == "multipart" {
							fields := map[string][]string{}
							for field, value := range input {
								if urls, ok := value.([]string); ok {
									encoded, marshalErr := common.Marshal(urls)
									require.NoError(t, marshalErr)
									fields[field] = []string{string(encoded)}
								} else {
									fields[field] = []string{fmt.Sprint(value)}
								}
							}
							body = map[string]any{"kind": kind, "fields": fields}
						}
						decoded, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
							"model": "public-video", "upstreamModel": tc.model, "body": body,
						})
						require.NoError(t, decodeErr)
						intent := decoded.(map[string]any)
						assert.Equal(t, "public-video", intent["model"])
						ctx := map[string]any{"model": "public-video", "upstreamModel": tc.model, "requestBody": intent["requestBody"]}
						value, specErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
						require.NoError(t, specErr)
						got, ignored, parseErr := spec.Parse(value.(map[string]any))
						require.NoError(t, parseErr)
						assert.Empty(t, ignored)
						assert.Equal(t, tier, got.Tier)
						require.NotNil(t, got.OutputSeconds)
						assert.EqualValues(t, 8, *got.OutputSeconds)
						assert.Equal(t, map[string]int{"image": tc.images, "video": tc.videos, "audio": tc.audios, "frame": 0}, got.References)
						assert.Equal(t, references["videos"], got.ReferenceVideoURLs)
						usage, usageErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
						require.NoError(t, usageErr)
						usageJSON, marshalErr := common.Marshal(usage)
						require.NoError(t, marshalErr)
						assert.JSONEq(t, fmt.Sprintf(`{"seconds":8,"surcharge_seconds":8,"resolution":%q}`, tier), string(usageJSON))
						ctx["baseUrl"], ctx["apiKey"] = "https://newapi.megabyai.cc", "fixture-only-key"
						built, buildErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
						require.NoError(t, buildErr)
						request := built.(map[string]any)
						assert.Equal(t, "https://newapi.megabyai.cc/v1/videos", request["url"])
						assert.Equal(t, "POST", request["method"])
						wireJSON, marshalErr := common.Marshal(request["body"])
						require.NoError(t, marshalErr)
						wantJSON, marshalErr := common.Marshal(map[string]any{
							"model": tc.model, "prompt": "cat", "duration": 8, "resolution": tier,
							"referenceImages": references["images"], "referenceVideos": references["videos"], "referenceAudios": references["audios"],
						})
						require.NoError(t, marshalErr)
						assert.JSONEq(t, string(wantJSON), string(wireJSON))
					})
				}
			}
			invalid := map[string]map[string]any{}
			for _, tier := range []string{"480p", "720p", "1080p", "4k"} {
				if !slices.Contains(tc.tiers, tier) {
					invalid["unsupported "+tier] = map[string]any{"resolution": tier}
				}
			}
			for field, urls := range references {
				invalid["too many "+field] = map[string]any{"resolution": tc.tiers[0], field: append(slices.Clone(urls), "https://cdn.example/extra")}
			}
			for name, input := range invalid {
				t.Run(name, func(t *testing.T) {
					input["model"], input["prompt"], input["duration"] = "public-video", "cat", 8
					_, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
						"model": "public-video", "upstreamModel": tc.model, "body": map[string]any{"kind": "json", "value": input},
					})
					require.Error(t, decodeErr)
					for _, hook := range []string{"extractUsage", "describeSpec", "buildSubmitRequest"} {
						_, hookErr := plugin.Engine.Call(t.Context(), hook, map[string]any{
							"model": "public-video", "upstreamModel": tc.model, "requestBody": input,
							"baseUrl": "https://newapi.megabyai.cc", "apiKey": "fixture-only-key",
						})
						require.ErrorContains(t, hookErr, tc.model, hook+" must apply the mapped model's limits")
					}
				})
			}
		})
	}
}

func TestSeedanceHjmieCanvasCompatibility(t *testing.T) {
	source, err := Source("seedance-hjmie")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "seedance-hjmie"})
	require.NoError(t, err)

	// Same field shape as the reported canvas request, with synthetic media URLs.
	const canvas = `{"model":"seedance2.0特价1","prompt":"餐馆内两人吃面，男生说：천천히 먹어.","seconds":"15","duration":15,"resolution":"720p","aspect_ratio":"16:9","images":["https://cdn.example/1.webp","https://cdn.example/2.webp"],"videos":["https://cdn.example/1.mp4"],"image_urls":["https://cdn.example/1.webp","https://cdn.example/2.webp"],"video_urls":["https://cdn.example/1.mp4"],"generate_audio":true,"metadata":{"content":[{"type":"image_url","image_url":{"url":"https://cdn.example/1.webp"},"role":"reference_image"},{"type":"image_url","image_url":{"url":"https://cdn.example/2.webp"},"role":"reference_image"},{"type":"video_url","video_url":{"url":"https://cdn.example/1.mp4"},"role":"reference_video"}],"ratio":"16:9","resolution":"720p"}}`
	for _, tc := range []struct {
		name, mode string
		audio      any
	}{
		{"original canvas", "canvas", true},
		{"explicit false", "canvas", false},
		{"omitted audio", "canvas", nil},
		{"URL aliases only", "aliases", true},
		{"metadata only", "metadata", false},
		{"multipart mirrors", "multipart", false},
		{"repeated references", "duplicates", true},
		{"equivalent scalar mirrors", "scalars", false},
		{"reference audio mirrors", "audio", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input map[string]any
			require.NoError(t, common.UnmarshalJsonStr(canvas, &input))
			if tc.audio == nil {
				delete(input, "generate_audio")
			} else {
				input["generate_audio"] = tc.audio
			}
			metadata := input["metadata"].(map[string]any)
			wantImages := []any{"https://cdn.example/1.webp", "https://cdn.example/2.webp"}
			wantVideos := []any{"https://cdn.example/1.mp4"}
			var wantAudios []any
			switch tc.mode {
			case "aliases":
				delete(input, "images")
				delete(input, "videos")
				delete(input, "metadata")
			case "metadata":
				metadata["duration"], metadata["generate_audio"] = 15, tc.audio
				for _, name := range []string{"seconds", "duration", "resolution", "aspect_ratio", "images", "videos", "image_urls", "video_urls", "generate_audio"} {
					delete(input, name)
				}
			case "duplicates":
				wantVideos = append(wantVideos, "https://cdn.example/1.mp4")
				input["videos"], input["video_urls"] = wantVideos, wantVideos
				content := metadata["content"].([]any)
				metadata["content"] = append(content, content[2])
			case "scalars":
				metadata["seconds"], metadata["resolution"], metadata["generate_audio"] = 15, "720P", tc.audio
				metadata["size"] = "1280x720"
			case "audio":
				wantAudios = []any{"https://cdn.example/a.wav"}
				input["audios"], input["audio_urls"] = wantAudios, wantAudios
				metadata["content"] = append(metadata["content"].([]any), map[string]any{"type": "audio_url", "audio_url": map[string]any{"url": wantAudios[0]}, "role": "reference_audio"})
			}
			body := map[string]any{"kind": "json", "value": input}
			if tc.mode == "multipart" {
				fields := map[string][]string{}
				for name, value := range input {
					encoded := fmt.Sprint(value)
					switch value.(type) {
					case map[string]any, []any:
						raw, err := common.Marshal(value)
						require.NoError(t, err)
						encoded = string(raw)
					}
					fields[name] = []string{encoded}
				}
				fields["image_urls[]"] = []string{"https://cdn.example/1.webp", "https://cdn.example/2.webp"}
				delete(fields, "image_urls")
				body = map[string]any{"kind": "multipart", "fields": fields}
			}
			before, err := common.Marshal(body)
			require.NoError(t, err)
			want := map[string]any{"prompt": input["prompt"], "duration": float64(15), "resolution": "720p", "ratio": "16:9", "images": wantImages, "videos": wantVideos}
			if tc.audio != nil {
				want["generate_audio"] = tc.audio
			}
			if len(wantAudios) > 0 {
				want["audios"] = wantAudios
			}
			wantJSON, err := common.Marshal(want)
			require.NoError(t, err)
			for range 2 {
				decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": input["model"], "body": body})
				require.NoError(t, err)
				intent := decoded.(map[string]any)
				assert.Equal(t, "reference_to_video", intent["action"])
				assert.Equal(t, input["model"], intent["model"])
				actualJSON, err := common.Marshal(intent["requestBody"])
				require.NoError(t, err)
				assert.JSONEq(t, string(wantJSON), string(actualJSON))
				driver := map[string]any{"model": input["model"], "upstreamModel": "sd-2-c5", "requestBody": intent["requestBody"]}
				value, err := plugin.Engine.Call(t.Context(), "describeSpec", driver)
				require.NoError(t, err)
				gotSpec, ignored, err := spec.Parse(value.(map[string]any))
				require.NoError(t, err)
				assert.Empty(t, ignored)
				assert.Equal(t, map[string]int{"image": 2, "video": len(wantVideos), "audio": len(wantAudios), "frame": 0}, gotSpec.References)
				videoURLs := make([]string, len(wantVideos))
				for i, url := range wantVideos {
					videoURLs[i] = url.(string)
				}
				assert.Equal(t, videoURLs, gotSpec.ReferenceVideoURLs)
				assert.Equal(t, float64(15), *gotSpec.OutputSeconds)
				assert.Equal(t, "720p", gotSpec.Tier)
				value, err = plugin.Engine.Call(t.Context(), "extractUsage", driver)
				require.NoError(t, err)
				actualJSON, err = common.Marshal(value)
				require.NoError(t, err)
				assert.JSONEq(t, `{"seconds":15,"resolution":"720p","requests":1}`, string(actualJSON))
				driver["baseUrl"], driver["apiKey"] = "https://gateway.example", "fixture-only-key"
				value, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", driver)
				require.NoError(t, err)
				request := value.(map[string]any)
				assert.Equal(t, "https://gateway.example/v1/videos", request["url"])
				wire := maps.Clone(want)
				wire["model"] = "sd-2-c5"
				wireJSON, err := common.Marshal(wire)
				require.NoError(t, err)
				actualJSON, err = common.Marshal(request["body"])
				require.NoError(t, err)
				assert.JSONEq(t, string(wireJSON), string(actualJSON))
			}
			after, err := common.Marshal(body)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "decoding must not mutate host-owned input")
		})
	}

	for _, tc := range []struct{ name, extra, message string }{
		{"string audio", `{"generate_audio":"false"}`, "generate_audio must be a boolean"},
		{"numeric audio", `{"generate_audio":0}`, "generate_audio must be a boolean"},
		{"null audio", `{"generate_audio":null}`, "generate_audio must be a boolean"},
		{"audio conflict", `{"generate_audio":true,"metadata":{"generate_audio":false}}`, "conflicting values for generate_audio"},
		{"metadata string audio", `{"metadata":{"generate_audio":"false"}}`, "generate_audio must be a boolean"},
		{"image conflict", `{"images":["https://cdn.example/a"],"image_urls":["https://cdn.example/b"]}`, "conflicting image reference fields"},
		{"explicit empty conflict", `{"videos":[],"video_urls":["https://cdn.example/v"]}`, "conflicting video reference fields"},
		{"content conflict", `{"videos":["https://cdn.example/a"],"metadata":{"content":[{"type":"video_url","video_url":{"url":"https://cdn.example/b"}}]}}`, "conflicting video reference fields"},
		{"resolution conflict", `{"metadata":{"resolution":"1080p"}}`, "conflicting values for resolution"},
		{"duration conflict", `{"metadata":{"duration":10}}`, "conflicting values for seconds/duration"},
		{"invalid metadata", `{"metadata":null}`, "metadata must be an object"},
		{"unknown metadata", `{"metadata":{"unknown":true}}`, "unsupported metadata parameter"},
		{"invalid content", `{"metadata":{"content":{}}}`, "metadata.content must be an array"},
		{"invalid URL", `{"image_urls":[42]}`, "public http(s) URLs"},
		{"content credentials", `{"metadata":{"content":[{"type":"image_url","image_url":{"url":"https://user:pass@cdn.example/a"}}]}}`, "public http(s) URLs"},
		{"content URL cannot encode an array", `{"metadata":{"content":[{"type":"image_url","image_url":{"url":"[\"https://cdn.example/a\"]"}}]}}`, "public http(s) URLs"},
		{"unknown content option", `{"metadata":{"content":[{"type":"video_url","video_url":{"url":"https://cdn.example/v"},"duration":5}]}}`, "unsupported metadata.content parameter"},
		{"unknown URL option", `{"metadata":{"content":[{"type":"video_url","video_url":{"url":"https://cdn.example/v","duration":5}}]}}`, "must contain only url"},
		{"frame role", `{"metadata":{"content":[{"type":"image_url","image_url":{"url":"https://cdn.example/a"},"role":"first_frame"}]}}`, "unsupported metadata.content role"},
		{"unknown content type", `{"metadata":{"content":[{"type":"text","text":"other prompt"}]}}`, "unsupported metadata.content type"},
		{"oversized metadata duration", `{"metadata":{"duration":3601}}`, "between 1 and 3600"},
		{"unknown field", `{"unknown":true}`, "unsupported video parameter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{"model": "seedance2.0特价1", "prompt": "cat", "seconds": 15, "resolution": "720p"}
			require.NoError(t, common.UnmarshalJsonStr(tc.extra, &input))
			_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": input["model"], "body": map[string]any{"kind": "json", "value": input}})
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestSeedanceHjmiePerRequestBilling(t *testing.T) {
	source, err := Source("seedance-hjmie")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "seedance-hjmie"})
	require.NoError(t, err)

	for _, model := range []string{"seedance-2.0", "seedance-2.5", "videos-mini", "videos-fast", "videos-standard"} {
		schema, _ := plugin.Meta.UsageForModel(model)
		assert.NotContains(t, schema, "requests", model)
		value, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{
			"model": model, "requestBody": map[string]any{"duration": 5, "resolution": "720p"},
		})
		require.NoError(t, err)
		facts := value.(map[string]any)
		_, err = plugin.Meta.ValidateUsageFacts(facts, model)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"seconds": float64(5), "resolution": "720p"}, facts)
	}

	for _, tc := range []struct{ model, upstream string }{
		{"sd-2-c5", "sd-2-c5"},
		{"sd-2-c4", "sd-2-c4"},
		{"public-video", "sd-2-c5"},
		{"public-video", "sd-2-c4"},
		{"sd-2-c4", "videos-standard"},
	} {
		for _, duration := range []int{5, 15} {
			t.Run(fmt.Sprintf("%s/%s/%ds", tc.model, tc.upstream, duration), func(t *testing.T) {
				schema, examples := plugin.Meta.UsageForModels(tc.upstream, tc.model)
				require.Contains(t, schema, "requests")
				assert.Equal(t, "count", schema["requests"].Unit)
				assert.Equal(t, "次", schema["requests"].UnitLabel["zh"])
				for _, example := range examples {
					assert.EqualValues(t, 1, example.Facts["requests"])
				}
				body := map[string]any{"model": tc.model, "prompt": "cat", "seconds": duration, "resolution": "720p", "n": 1}
				decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
					"model": tc.model, "upstreamModel": tc.upstream, "body": map[string]any{"kind": "json", "value": body},
				})
				require.NoError(t, err)
				driver := map[string]any{"model": tc.model, "upstreamModel": tc.upstream, "requestBody": decoded.(map[string]any)["requestBody"]}
				value, err := plugin.Engine.Call(t.Context(), "extractUsage", driver)
				require.NoError(t, err)
				reserved := value.(map[string]any)
				_, err = plugin.Meta.ValidateUsageFacts(reserved, tc.upstream, tc.model)
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"requests": float64(1), "seconds": float64(duration), "resolution": "720p"}, reserved)
				legacyDriver := maps.Clone(driver)
				legacyDriver["usagePurpose"] = "billing_ratios"
				value, err = plugin.Engine.Call(t.Context(), "extractUsage", legacyDriver)
				require.NoError(t, err)
				ratios, err := plugin.Meta.ValidateUsageFacts(value.(map[string]any), tc.upstream, tc.model)
				require.NoError(t, err)
				assert.Equal(t, map[string]float64{"requests": 1}, ratios, "legacy per-call prices must not multiply by duration")

				// Test prices only: duration can change while one request remains one charge.
				for _, data := range []any{nil, map[string]any{"seconds": "10", "resolution": "1080p", "requests": 99}} {
					value, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", driver, map[string]any{"status": "SUCCESS"}, data)
					require.NoError(t, err)
					completed, err := plugin.Meta.ValidateCompletionUsageFacts(value, tc.upstream, tc.model)
					require.NoError(t, err)
					facts := maps.Clone(reserved)
					maps.Copy(facts, completed)
					cost, _, err := billingexpr.RunExprWithRequest(`tier("request", u("requests") * 0.25)`, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
					require.NoError(t, err)
					assert.Equal(t, 0.25, cost)
					if data != nil {
						assert.Equal(t, float64(10), facts["seconds"])
						cost, _, err = billingexpr.RunExprWithRequest(`tier("seconds", u("seconds") * 0.25)`, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
						require.NoError(t, err)
						assert.Equal(t, 2.5, cost, "existing per-second expressions remain supported")
					}
				}
				for key, value := range map[string]any{"n": 2, "requests": 0} {
					invalid := maps.Clone(body)
					invalid[key] = value
					_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
						"model": tc.model, "upstreamModel": tc.upstream, "body": map[string]any{"kind": "json", "value": invalid},
					})
					assert.Error(t, err, "clients cannot override the single-output count via %s", key)
				}
			})
		}
	}
}

func TestToken6688VideoPluginLifecycle(t *testing.T) {
	source, err := Source("token6688")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "token6688"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"seedance-2-0-official", "seedance-2-0-promo", "seedance-2-0-special"}, plugin.Meta.Models)
	for _, tc := range []struct {
		model, quality string
		tiers          []string
		seconds        float64
	}{
		{"seedance-2-0-official", "标准", []string{"480p", "720p", "1080p", "4k"}, 15},
		{"seedance-2-0-official", "fast", []string{"480p", "720p", "1080p"}, 4},
		{"seedance-2-0-official", "mini", []string{"480p", "720p", "1080p", "4k"}, 11},
		{"seedance-2-0-promo", "标准", []string{"480p", "720p", "1080p", "2k", "4k"}, 12},
		{"seedance-2-0-promo", "fast", []string{"480p", "720p", "1080p"}, 4},
		{"seedance-2-0-promo", "mini", []string{"480p", "720p", "4k"}, 15},
		{"seedance-2-0-special", "高清", []string{"480p", "720p"}, 15},
		{"seedance-2-0-special", "快速", []string{"480p", "720p"}, 15},
		{"seedance-2-0-special", "标准", []string{"480p", "720p"}, 15},
	} {
		for _, tier := range tc.tiers {
			t.Run(tc.model+"/"+tc.quality+"/"+tier, func(t *testing.T) {
				binding, found := jsplugin.DefaultRegistry.Generation().LookupEndpoint("POST", "/v1/videos", tc.model)
				require.True(t, found)
				assert.Equal(t, "token6688", binding.Plugin.Meta.Key)
				body := map[string]any{
					"model": "public-video", "prompt": "cat", "seconds": tc.seconds, "resolution": tier, "quality": tc.quality,
					"lane_id": tc.model + "@TEST:fixture",
					"ratio":   "16:9", "n": float64(1), "images": []any{"https://cdn.example/i.png"},
					"videos": []any{"https://cdn.example/v.mp4", "https://cdn.example/v.mp4"}, "audios": []any{"https://cdn.example/a.wav"},
				}
				before, err := common.Marshal(body)
				require.NoError(t, err)
				decodeCtx := map[string]any{"model": "public-video", "body": map[string]any{"kind": "json", "value": body}}
				// Public aliases decode before the host knows the channel mapping.
				initial, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, decodeCtx)
				require.NoError(t, err)
				decodeCtx["upstreamModel"] = tc.model
				decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, decodeCtx)
				require.NoError(t, err)
				wantQuality := tc.quality
				if tc.model == "seedance-2-0-official" {
					wantQuality = "标准"
				} else {
					assert.Equal(t, initial, decoded, "explicit parameters must survive mapping unchanged")
				}
				intent := decoded.(map[string]any)
				assert.Equal(t, "public-video", intent["model"])
				driver := map[string]any{"model": "public-video", "upstreamModel": tc.model, "requestBody": intent["requestBody"], "baseUrl": "https://freeai.token6688.com/", "apiKey": "fixture-only-key", "publicTaskId": "task_fixture"}
				value, err := plugin.Engine.Call(t.Context(), "describeSpec", driver)
				require.NoError(t, err)
				got, ignored, err := spec.Parse(value.(map[string]any))
				require.NoError(t, err)
				assert.Empty(t, ignored)
				assert.Equal(t, tier, got.Tier)
				assert.Equal(t, &tc.seconds, got.OutputSeconds)
				assert.Equal(t, map[string]int{"image": 1, "video": 2, "audio": 1, "frame": 0}, got.References)
				assert.Equal(t, []string{"https://cdn.example/v.mp4", "https://cdn.example/v.mp4"}, got.ReferenceVideoURLs)
				kind := videosched.KindExact
				if tc.model == "seedance-2-0-special" {
					kind = videosched.KindFixed
				}
				assert.Equal(t, kind, got.SecondsKind)
				value, err = plugin.Engine.Call(t.Context(), "extractUsage", driver)
				require.NoError(t, err)
				facts := value.(map[string]any)
				ratios, err := plugin.Meta.ValidateUsageFacts(facts, tc.model, "public-video")
				require.NoError(t, err)
				if kind == videosched.KindFixed {
					assert.Equal(t, map[string]float64{"requests": 1}, ratios)
				} else {
					assert.Equal(t, map[string]float64{"seconds": tc.seconds}, ratios)
				}
				assert.Equal(t, wantQuality, facts["quality"])
				value, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", driver)
				require.NoError(t, err)
				wire, err := common.Marshal(value)
				require.NoError(t, err)
				var request map[string]any
				require.NoError(t, common.Unmarshal(wire, &request))
				assert.Equal(t, "https://freeai.token6688.com/v1/videos/generations", request["url"])
				assert.Equal(t, "POST", request["method"])
				assert.Equal(t, map[string]any{"Authorization": "Bearer fixture-only-key", "Content-Type": "application/json"}, request["headers"])
				assert.Equal(t, map[string]any{
					"model": tc.model, "prompt": "cat", "duration": tc.seconds, "resolution": tier, "quality": wantQuality,
					"lane_id":      tc.model + "@TEST:fixture",
					"aspect_ratio": "16:9", "mode": "reference", "client_request_id": "task_fixture",
					"images": body["images"], "videos": body["videos"], "audios": body["audios"],
				}, request["body"])
				after, err := common.Marshal(body)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after), "hooks must not mutate the caller's references")
			})
		}
	}
	for _, tc := range []struct {
		name, form string
		audio      any
	}{
		{"canvas audio enabled", "json", true},
		{"canvas audio disabled", "json", false},
		{"canvas audio omitted", "json", nil},
		{"nested audio enabled", "params", true},
		{"equal audio mirrors", "metadata", false},
		{"multipart audio enabled", "multipart", true},
		{"multipart audio disabled", "multipart", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{
				"model": "seedance-2.0-nsfw", "prompt": "A landscape video",
				"seconds": "10", "duration": 10, "resolution": "720p", "aspect_ratio": "9:16",
			}
			body := map[string]any{"kind": "json", "value": input}
			switch tc.form {
			case "params":
				input["params"] = map[string]any{"generate_audio": tc.audio}
			case "metadata":
				input["generate_audio"] = tc.audio
				input["metadata"] = map[string]any{"generate_audio": tc.audio}
			case "multipart":
				fields := make(map[string]any, len(input)+1)
				for key, value := range input {
					fields[key] = []string{fmt.Sprint(value)}
				}
				fields["generate_audio"] = []string{fmt.Sprint(tc.audio)}
				body = map[string]any{"kind": "multipart", "fields": fields}
			default:
				if tc.audio != nil {
					input["generate_audio"] = tc.audio
				}
			}
			before, err := common.Marshal(body)
			require.NoError(t, err)
			decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": input["model"], "body": body})
			require.NoError(t, err)
			intent := decoded.(map[string]any)
			assert.Equal(t, input["model"], intent["model"])
			driver := map[string]any{"model": input["model"], "upstreamModel": "seedance-2-0-official", "requestBody": intent["requestBody"], "baseUrl": "https://freeai.token6688.com", "apiKey": "fixture-only-key", "publicTaskId": "task_canvas"}
			value, err := plugin.Engine.Call(t.Context(), "describeSpec", driver)
			require.NoError(t, err)
			got, _, err := spec.Parse(value.(map[string]any))
			require.NoError(t, err)
			require.NotNil(t, got.OutputSeconds)
			assert.Equal(t, float64(10), *got.OutputSeconds)
			assert.Equal(t, "720p", got.Tier)
			facts, err := plugin.Engine.Call(t.Context(), "extractUsage", driver)
			require.NoError(t, err)
			ratios, err := plugin.Meta.ValidateUsageFacts(facts.(map[string]any), "seedance-2-0-official", "seedance-2.0-nsfw")
			require.NoError(t, err)
			assert.Equal(t, map[string]float64{"seconds": 10}, ratios)
			value, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", driver)
			require.NoError(t, err)
			wire := value.(map[string]any)["body"].(map[string]any)
			assert.Equal(t, "seedance-2-0-official", wire["model"])
			assert.Equal(t, "标准", wire["quality"])
			if tc.audio == nil {
				assert.NotContains(t, wire, "generate_audio")
			} else {
				assert.Equal(t, tc.audio, wire["generate_audio"])
			}
			after, err := common.Marshal(body)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after))
		})
	}
	for _, tc := range []struct {
		name, model, body, want string
	}{
		{"string audio switch", "official", `{"generate_audio":"false"}`, "generate_audio must be a boolean"},
		{"numeric audio switch", "official", `{"generate_audio":1}`, "generate_audio must be a boolean"},
		{"null audio switch", "official", `{"generate_audio":null}`, "generate_audio must be a boolean"},
		{"conflicting audio mirrors", "official", `{"generate_audio":true,"params":{"generate_audio":false}}`, "conflicting video parameter: generate_audio"},
		{"seconds overflow", "official", `{"seconds":18446744073686646784}`, "duration must be an integer"},
		{"negative duration", "official", `{"duration":-1}`, "duration must be an integer"},
		{"fractional duration", "official", `{"duration":4.5}`, "duration must be an integer"},
		{"promo duration gap", "promo", `{"seconds":11}`, "duration must be one of"},
		{"fixed special duration", "special", `{"seconds":10}`, "duration must be one of 15"},
		{"unsupported resolution", "special", `{"resolution":"1080p"}`, "quality/resolution"},
		{"official unsupported 2k", "official", `{"resolution":"2k"}`, "quality/resolution"},
		{"promo fast 4k", "promo", `{"resolution":"4k","quality":"fast"}`, "quality/resolution"},
		{"multiple outputs", "official", `{"n":2}`, "n must be 1"},
		{"hidden count", "official", `{"params":{"count":2}}`, "count must be 1"},
		{"string json n", "official", `{"n":"1"}`, "n must be 1"},
		{"null reference", "official", `{"images":null}`, "images must be"},
		{"scalar references", "official", `{"images":"https://cdn.example/a.png"}`, "images must be"},
		{"object reference", "official", `{"videos":[{"url":"https://cdn.example/a.mp4"}]}`, "videos must be"},
		{"credentials in reference URL", "official", `{"videos":["https://user:pass@cdn.example/a.mp4"]}`, "videos must be"},
		{"too many videos", "official", `{"videos":["https://cdn.example/1.mp4","https://cdn.example/2.mp4","https://cdn.example/3.mp4","https://cdn.example/4.mp4"]}`, "at most 3"},
		{"conflicting duration", "official", `{"seconds":4,"duration":5}`, "conflicting values"},
		{"hidden duration conflict", "official", `{"duration":4,"metadata":{"duration":5}}`, "conflicting video parameter"},
		{"hidden resolution conflict", "official", `{"resolution":"480p","params":{"size":"720p"}}`, "size conflicts"},
		{"last frame alone", "official", `{"last_image_url":"https://cdn.example/a.png"}`, "requires first_image_url"},
		{"frame reference mixing", "official", `{"first_image_url":"https://cdn.example/a.png","videos":["https://cdn.example/a.mp4"]}`, "no other reference media"},
		{"extra frame images", "official", `{"mode":"first-frame","images":["https://cdn.example/1.png","https://cdn.example/2.png"]}`, "exactly 1"},
		{"audio alone", "promo", `{"audios":["https://cdn.example/a.wav"]}`, "requires an image or video"},
		{"official audio without image", "official", `{"videos":["https://cdn.example/v.mp4"],"audios":["https://cdn.example/a.wav"]}`, "require a reference image"},
		{"special mini last frame", "special", `{"mode":"first-last","images":["https://cdn.example/1.png","https://cdn.example/2.png"]}`, "quality/mode"},
		{"unsupported adaptive", "special", `{"ratio":"adaptive","images":["https://cdn.example/a.png"]}`, "adaptive ratio"},
		{"variable duration edit", "official", `{"mode":"edit"}`, "do not preserve exact output duration"},
		{"variable duration extend", "official", `{"mode":"extend"}`, "do not preserve exact output duration"},
		{"spoofed reference seconds", "official", `{"video_total_duration_sec":0}`, "unsupported video parameter"},
		{"unsupported switch", "promo", `{"return_last_frame":false}`, "only by official"},
		{"empty lane", "official", `{"lane_id":""}`, "lane_id must be a non-empty lane identifier"},
		{"numeric lane", "official", `{"lane_id":42}`, "lane_id must be a non-empty lane identifier"},
		{"lane whitespace", "official", `{"lane_id":"model@TEST:fixture\n"}`, "lane_id must be a non-empty lane identifier"},
		{"conflicting lane mirrors", "official", `{"lane_id":"model@TEST:one","params":{"lane_id":"model@TEST:two"}}`, "conflicting video parameter: lane_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &body))
			body["model"] = "seedance-2-0-" + tc.model
			body["prompt"] = "cat"
			_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"body": map[string]any{"kind": "json", "value": body}})
			if tc.want == "quality/resolution" || tc.want == "quality/mode" {
				require.NoError(t, err, "channel overrides select quality after decoding")
			} else {
				require.ErrorContains(t, err, tc.want)
			}
			// Driver entry points must independently validate quantities and media
			// semantics, including callers that supply a body without decoding.
			for _, hook := range []string{"buildSubmitRequest", "describeSpec", "extractUsage"} {
				_, err := plugin.Engine.Call(t.Context(), hook, map[string]any{"model": body["model"], "requestBody": body})
				require.ErrorContains(t, err, tc.want, hook)
			}
		})
	}
	_, err = jsplugin.ReplayFixture(t.Context(), source, []byte(`{"cases":[
		{"name":"official defaults","hook":"extractUsage","args":[{"model":"seedance-2-0-official","requestBody":{"prompt":"cat"}}],"expected":{"seconds":4,"resolution":"480p","quality":"标准","mode":"text-to-video","has_video":false}},
		{"name":"official 1080p needs no channel override","hook":"describeSpec","args":[{"model":"seedance-2-0-official","requestBody":{"prompt":"cat","seconds":4,"resolution":"1080p"}}],"expected":{"spec_version":3,"output_seconds":4,"seconds_kind":"exact","resolution":"1080p","references":{"frame":0,"image":0,"video":0,"audio":0},"reference_video_urls":[]}},
		{"name":"equal numeric and tier mirrors are normalized","hook":"extractUsage","args":[{"model":"seedance-2-0-official","requestBody":{"prompt":"cat","duration":"4","resolution":"720P","params":{"duration":4,"resolution":"720p"}}}],"expected":{"seconds":4,"resolution":"720p","quality":"标准","mode":"text-to-video","has_video":false}},
		{"name":"special defaults remain per request","hook":"extractUsage","args":[{"model":"seedance-2-0-special","requestBody":{"prompt":"cat"}}],"expected":{"requests":1,"resolution":"480p","quality":"标准","mode":"text-to-video","has_video":false}},
		{"name":"frames are distinct from reference images","hook":"describeSpec","args":[{"model":"seedance-2-0-special","requestBody":{"prompt":"cat","quality":"快速","first_image_url":"https://cdn.example/1.png","last_image_url":"https://cdn.example/2.png"}}],"expected":{"spec_version":3,"output_seconds":15,"seconds_kind":"fixed","resolution":"480p","references":{"frame":2,"image":0,"video":0,"audio":0},"reference_video_urls":[]}},
		{"name":"native frame images preserve their roles","hook":"describeSpec","args":[{"model":"seedance-2-0-official","requestBody":{"prompt":"cat","mode":"first-frame","images":["https://cdn.example/1.png"]}}],"expected":{"spec_version":3,"output_seconds":4,"seconds_kind":"exact","resolution":"480p","references":{"frame":1,"image":0,"video":0,"audio":0},"reference_video_urls":[]}},
		{"name":"multipart and mirrored nested parameters","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"model":"public-video","upstreamModel":"seedance-2-0-official","body":{"kind":"multipart","fields":{"model":["public-video"],"prompt":["cat"],"seconds":["4"],"n":["1"],"metadata":["{\"duration\":4,\"resolution\":\"720p\"}"],"size":["1280x720"],"return_last_frame":["false"],"images[]":["https://cdn.example/1.png","https://cdn.example/1.png"]}}}],"expected":{"kind":"submit","model":"public-video","action":"image_to_video","requestBody":{"prompt":"cat","duration":4,"resolution":"720p","aspect_ratio":"16:9","quality":"标准","mode":"reference","images":["https://cdn.example/1.png","https://cdn.example/1.png"],"return_last_frame":false}}},
		{"name":"size maps 4k with standard quality","hook":"describeSpec","args":[{"model":"seedance-2-0-official","requestBody":{"prompt":"cat","quality":"标准","size":"3840x2160"}}],"expected":{"spec_version":3,"output_seconds":4,"seconds_kind":"exact","resolution":"4k","references":{"frame":0,"image":0,"video":0,"audio":0},"reference_video_urls":[]}},
		{"name":"missing idempotency id cannot submit","hook":"buildSubmitRequest","args":[{"model":"seedance-2-0-official","requestBody":{"prompt":"cat"}}],"expectedError":"public task id is required"},
		{"name":"mapped unknown model cannot submit","hook":"buildSubmitRequest","args":[{"model":"seedance-2-0-official","upstreamModel":"other","requestBody":{"prompt":"cat"}}],"expectedError":"unsupported Token6688 model"},
		{"name":"documented pending create","hook":"parseSubmitResponse","args":[{}, {"statusCode":202,"body":{"id":"task-42","object":"video","task_id":"task-42","status":"pending"}}],"expected":{"taskId":"task-42","taskData":{"id":"task-42","object":"video","task_id":"task-42","status":"pending"}}},
		{"name":"accepted immediate failure refunds","hook":"parseSubmitResponse","args":[{}, {"body":{"id":"task-42","is_final":true,"status":"failed","error":{"message":"content moderation rejected the prompt"}}}],"expected":{"taskId":"task-42","taskData":{"id":"task-42","is_final":true,"status":"failed","error":{"message":"content moderation rejected the prompt"}},"immediate":{"status":"FAILURE","reason":"content moderation rejected the prompt"}}},
		{"name":"immediate completed response","hook":"parseSubmitResponse","args":[{}, {"body":{"id":"task-42","is_final":true,"status":"completed","output_url":"https://cdn.example/v.mp4"}}],"expected":{"taskId":"task-42","taskData":{"id":"task-42","is_final":true,"status":"completed","output_url":"https://cdn.example/v.mp4"},"immediate":{"status":"SUCCESS","progress":"100%"}}},
		{"name":"ambiguous create cannot trigger retry","hook":"parseSubmitResponse","args":[{}, {"body":{"status":"pending"}}],"expectedError":"no unambiguous Token6688 task id"},
		{"name":"conflicting task ids","hook":"parseSubmitResponse","args":[{}, {"body":{"id":"task-42","task_id":"task-43","status":"pending"}}],"expectedError":"no unambiguous Token6688 task id"},
		{"name":"contradictory rejection and acceptance","hook":"parseSubmitResponse","args":[{}, {"body":{"id":"task-42","status":"pending","error":"quota exhausted"}}],"expectedError":"no unambiguous Token6688 task id"},
		{"name":"query uses escaped persisted id","hook":"buildQueryRequest","args":[{"baseUrl":"https://freeai.token6688.com/","taskId":"task/a?b","authHeader":"Bearer fixture-only-key"}],"expected":{"url":"https://freeai.token6688.com/v1/tasks/task%2Fa%3Fb","method":"GET","headers":{"Authorization":"Bearer fixture-only-key"}}},
		{"name":"pending without fake progress","hook":"parseTaskResult","args":[{"taskId":"task-42"},{"id":"task-42","is_final":false,"status":"pending","progress_available":false,"progress":0}],"expected":{"status":"QUEUED"}},
		{"name":"processing with real progress","hook":"parseTaskResult","args":[{"taskId":"task-42"},{"id":"task-42","is_final":false,"status":"processing","progress_available":true,"progress":42}],"expected":{"status":"IN_PROGRESS","progress":"42%"}},
		{"name":"manual review is nonterminal","hook":"parseTaskResult","args":[{"taskId":"task-42"},{"id":"task-42","is_final":false,"status":"needs_review"}],"expected":{"status":"IN_PROGRESS"}},
		{"name":"unknown status stays unknown","hook":"parseTaskResult","args":[{}, {"id":"task-42","is_final":false,"status":"new_state"}],"expected":{"status":"UNKNOWN","reason":"unrecognized or inconsistent Token6688 task status or missing video URL"}},
		{"name":"completed needs final flag","hook":"parseTaskResult","args":[{}, {"id":"task-42","is_final":false,"status":"completed","output_url":"https://cdn.example/v.mp4"}],"expected":{"status":"UNKNOWN","reason":"unrecognized or inconsistent Token6688 task status or missing video URL"}},
		{"name":"completed needs downloadable artifact","hook":"parseTaskResult","args":[{}, {"id":"task-42","is_final":true,"status":"completed"}],"expected":{"status":"UNKNOWN","reason":"unrecognized or inconsistent Token6688 task status or missing video URL"}},
		{"name":"task id mismatch cannot settle","hook":"parseTaskResult","args":[{"taskId":"task-42"},{"id":"task-43","is_final":true,"status":"completed","output_url":"https://cdn.example/v.mp4"}],"expected":{"status":"UNKNOWN","reason":"invalid or mismatched Token6688 task id"}},
		{"name":"legacy result URL","hook":"parseTaskResult","args":[{}, {"id":"task-42","is_final":true,"status":"completed","result_url":"https://cdn.example/v.mp4"}],"expected":{"status":"SUCCESS","progress":"100%"}},
		{"name":"download never forwards credentials","hook":"buildContentRequest","args":[{"artifactKey":"video","apiKey":"fixture-only-key","data":{"is_final":true,"status":"completed","output_url":"https://cdn.example/v.mp4?sig=original%2Bvalue"},"clientRequest":{"method":"HEAD","headers":{"Authorization":"client-secret"}}}],"expected":{"url":"https://cdn.example/v.mp4?sig=original%2Bvalue","method":"HEAD","credentialless":true}},
		{"name":"render excludes upstream account and id","hook":"protocols","path":["openai_video","render"],"args":[{}, {"status":"SUCCESS","data":{"id":"task-private","is_final":true,"status":"completed","output_url":"https://cdn.example/v.mp4","actual_cost_micro_usd":99999999}}],"expected":{"url":"https://cdn.example/v.mp4"}},
		{"name":"video artifact listed","hook":"listArtifacts","args":[{"status":"SUCCESS","data":{"is_final":true,"status":"completed","output_url":"https://cdn.example/v.mp4"}}],"expected":[{"key":"video","type":"video","mimeType":"video/mp4"}]},
		{"name":"elapsed time and vendor money never become usage","hook":"extractUsageOnComplete","args":[{}, {"status":"SUCCESS"},{"duration_seconds":84,"actual_cost_micro_usd":99999999}],"expected":{}}
	]}`))
	require.NoError(t, err)
}

// Verify APIPod's wire protocol and fixed per-video usage through the host runtime.
func TestAPIPodVideoPluginLifecycle(t *testing.T) {
	source, err := Source("apipod")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "apipod"})
	require.NoError(t, err)
	for _, model := range []string{"seedance-2.0-x-r2v", "seedance-2.0-x-t2v", "seedance-2.0-spicy-r2v", "seedance-2.0-spicy-t2v"} {
		binding, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", model)
		require.True(t, found, model)
		assert.Same(t, plugin, binding.Plugin)
	}

	t.Run("mapped standard requests preserve wire spec and fixed billing", func(t *testing.T) {
		for _, tc := range []struct {
			name, model, fields, aspect, tier string
			seconds, images, videos, audios   int
		}{
			{"text defaults", "seedance-2.0-x-t2v", `{}`, "16:9", "720p", 5, 0, 0, 0},
			{"text maximum", "seedance-2.0-x-t2v", `{"seconds":15,"size":"720x1280","n":1}`, "9:16", "720p", 15, 0, 0, 0},
			{"image reference", "seedance-2.0-x-r2v", `{"images":["https://cdn.example/i.png"]}`, "16:9", "720p", 5, 1, 0, 0},
			{"video reference", "seedance-2.0-x-r2v", `{"videos":["https://cdn.example/v.mp4"],"ratio":"21:9"}`, "21:9", "720p", 5, 0, 1, 0},
			{"all references and mirrored aliases", "seedance-2.0-x-r2v", `{"seconds":"15","duration":15,"size":"960x720","resolution":"720P","aspect_ratio":"4:3","images":["https://cdn.example/i.png"],"image_urls":["https://cdn.example/i.png"],"videos":["https://cdn.example/v.mp4","https://cdn.example/v.mp4"],"audios":["https://cdn.example/a.mp3"],"n":1}`, "4:3", "720p", 15, 1, 2, 1},
			{"native maximum reference counts", "seedance-2.0-x-r2v", `{"image_urls":["https://cdn.example/1","https://cdn.example/2","https://cdn.example/3","https://cdn.example/4","https://cdn.example/5","https://cdn.example/6","https://cdn.example/7","https://cdn.example/8","https://cdn.example/9"],"video_urls":["https://cdn.example/v1","https://cdn.example/v2","https://cdn.example/v3"],"audio_urls":["https://cdn.example/a1","https://cdn.example/a2","https://cdn.example/a3"],"duration":7,"ratio":"1:1"}`, "1:1", "720p", 7, 9, 3, 3},
			{"spicy text 480p canvas", "seedance-2.0-spicy-t2v", `{"seconds":"10","duration":10,"resolution":"480p","aspect_ratio":"9:16","generate_audio":true}`, "9:16", "480p", 10, 0, 0, 0},
			{"spicy reference 480p dimensions", "seedance-2.0-spicy-r2v", `{"size":"480x854","images":["https://cdn.example/i.png"],"videos":["https://cdn.example/v.mp4"],"generate_audio":false}`, "9:16", "480p", 5, 1, 1, 0},
			{"spicy audio reference", "seedance-2.0-spicy-r2v", `{"audios":["https://cdn.example/a.mp3"]}`, "16:9", "720p", 5, 0, 0, 1},
			{"spicy 480p tier aliases", "seedance-2.0-spicy-t2v", `{"size":"480p","resolution":"480P"}`, "16:9", "480p", 5, 0, 0, 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var input map[string]any
				require.NoError(t, common.UnmarshalJsonStr(tc.fields, &input))
				input["model"], input["prompt"] = "public-video", "海边日出"
				before, marshalErr := common.Marshal(input)
				require.NoError(t, marshalErr)
				decodeCtx := map[string]any{"model": "public-video", "upstreamModel": tc.model, "body": map[string]any{"kind": "json", "value": input}}
				decoded, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, decodeCtx)
				require.NoError(t, decodeErr)
				repeated, repeatErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, decodeCtx)
				require.NoError(t, repeatErr)
				assert.Equal(t, decoded, repeated)
				after, marshalErr := common.Marshal(input)
				require.NoError(t, marshalErr)
				assert.JSONEq(t, string(before), string(after), "decoding must not mutate host inputs")
				intent := decoded.(map[string]any)
				assert.Equal(t, "public-video", intent["model"])
				ctx := map[string]any{"model": "public-video", "upstreamModel": tc.model, "requestBody": intent["requestBody"]}
				value, specErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
				require.NoError(t, specErr)
				got, ignored, parseErr := spec.Parse(value.(map[string]any))
				require.NoError(t, parseErr)
				assert.Empty(t, ignored)
				assert.Equal(t, tc.tier, got.Tier)
				assert.EqualValues(t, tc.seconds, *got.OutputSeconds)
				assert.Equal(t, map[string]int{"image": tc.images, "video": tc.videos, "audio": tc.audios, "frame": 0}, got.References)
				usage, usageErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
				require.NoError(t, usageErr)
				usageJSON, marshalErr := common.Marshal(usage)
				require.NoError(t, marshalErr)
				assert.JSONEq(t, `{"video_count":1}`, string(usageJSON), "duration and references must not multiply the per-video charge")
				ctx["baseUrl"], ctx["apiKey"], ctx["publicTaskId"] = "https://api.apipod.ai/", "fixture-only-key", "task_stable"
				built, buildErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, buildErr)
				request := built.(map[string]any)
				wire := request["body"].(map[string]any)
				assert.Equal(t, "https://api.apipod.ai/v1/videos/generations", request["url"])
				assert.Equal(t, "POST", request["method"])
				assert.Equal(t, tc.model, wire["model"])
				assert.EqualValues(t, tc.seconds, wire["duration"])
				assert.Equal(t, got.Tier, wire["resolution"])
				assert.Equal(t, tc.aspect, wire["aspect_ratio"])
				if audio, present := input["generate_audio"]; present {
					assert.Equal(t, audio, wire["generate_audio"])
				} else {
					assert.NotContains(t, wire, "generate_audio")
				}
				for _, field := range []string{"seconds", "size", "ratio", "n", "images", "videos", "audios"} {
					assert.NotContains(t, wire, field)
				}
				if tc.videos > 0 {
					urls := wire["video_urls"].([]any)
					require.Len(t, urls, len(got.ReferenceVideoURLs))
					for i, url := range got.ReferenceVideoURLs {
						assert.Equal(t, url, urls[i], "reference order and repeats must match the wire")
					}
				}
				assert.NotContains(t, request, "model")
				assert.Equal(t, "task_stable", request["headers"].(map[string]any)["Idempotency-Key"])
				assert.Equal(t, "Bearer fixture-only-key", request["headers"].(map[string]any)["Authorization"])
				retried, retryErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, retryErr)
				assert.Equal(t, built, retried)
			})
		}
	})

	t.Run("canvas audio compatibility follows the mapped model", func(t *testing.T) {
		for _, kind := range []string{"json", "multipart"} {
			for _, tc := range []struct {
				name, model, tier string
				audio             any
			}{
				{"X enabled", "seedance-2.0-x-t2v", "720p", true},
				{"X disabled", "seedance-2.0-x-t2v", "720p", false},
				{"X omitted", "seedance-2.0-x-t2v", "720p", nil},
				{"spicy enabled", "seedance-2.0-spicy-t2v", "480p", true},
				{"spicy disabled", "seedance-2.0-spicy-t2v", "480p", false},
				{"spicy omitted", "seedance-2.0-spicy-t2v", "480p", nil},
			} {
				t.Run(kind+"/"+tc.name, func(t *testing.T) {
					input := map[string]any{"model": "seedance-2.0", "prompt": "海边日出", "seconds": "10", "duration": 10, "resolution": tc.tier, "aspect_ratio": "9:16"}
					fields := map[string][]string{"model": {"seedance-2.0"}, "prompt": {"海边日出"}, "seconds": {"10"}, "duration": {"10"}, "resolution": {tc.tier}, "aspect_ratio": {"9:16"}}
					if tc.audio != nil {
						input["generate_audio"] = tc.audio
						fields["generate_audio"] = []string{fmt.Sprint(tc.audio)}
					}
					body := map[string]any{"kind": kind, "value": input, "fields": fields}
					decoded, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "seedance-2.0", "body": body})
					require.NoError(t, decodeErr)
					intent := decoded.(map[string]any)
					assert.Equal(t, "text_to_video", intent["action"])
					requestBody := intent["requestBody"].(map[string]any)
					if tc.audio == nil {
						assert.NotContains(t, requestBody, "generate_audio")
					} else {
						assert.Equal(t, tc.audio, requestBody["generate_audio"])
					}
					before, marshalErr := common.Marshal(requestBody)
					require.NoError(t, marshalErr)
					ctx := map[string]any{"model": "seedance-2.0", "upstreamModel": tc.model, "requestBody": requestBody, "baseUrl": "https://api.apipod.ai", "apiKey": "fixture-only-key", "publicTaskId": "task_audio"}
					value, specErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
					require.NoError(t, specErr)
					got, _, parseErr := spec.Parse(value.(map[string]any))
					require.NoError(t, parseErr)
					assert.Equal(t, tc.tier, got.Tier)
					assert.EqualValues(t, 10, *got.OutputSeconds)
					assert.Equal(t, map[string]int{"image": 0, "video": 0, "audio": 0, "frame": 0}, got.References)
					usage, usageErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
					require.NoError(t, usageErr)
					usageJSON, marshalErr := common.Marshal(usage)
					require.NoError(t, marshalErr)
					assert.JSONEq(t, `{"video_count":1}`, string(usageJSON))
					built, buildErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
					require.NoError(t, buildErr)
					wire, marshalErr := common.Marshal(built.(map[string]any)["body"])
					require.NoError(t, marshalErr)
					want := map[string]any{"model": tc.model, "prompt": "海边日出", "duration": 10, "resolution": tc.tier, "aspect_ratio": "9:16"}
					if tc.model == "seedance-2.0-spicy-t2v" && tc.audio != nil {
						want["generate_audio"] = tc.audio
					}
					wantJSON, marshalErr := common.Marshal(want)
					require.NoError(t, marshalErr)
					assert.JSONEq(t, string(wantJSON), string(wire))
					after, marshalErr := common.Marshal(requestBody)
					require.NoError(t, marshalErr)
					assert.JSONEq(t, string(before), string(after), "driver hooks must not remove the audio flag from the normalized input")
				})
			}
		}
	})

	t.Run("decoder and driver reject invalid or overridden parameters", func(t *testing.T) {
		for _, tc := range []struct{ name, model, fields, reason string }{
			{"too short", "seedance-2.0-x-t2v", `{"seconds":4}`, "between 5 and 15"},
			{"too long", "seedance-2.0-x-t2v", `{"duration":16}`, "between 5 and 15"},
			{"zero", "seedance-2.0-x-t2v", `{"seconds":0}`, "between 5 and 15"},
			{"negative", "seedance-2.0-x-t2v", `{"seconds":-1}`, "between 5 and 15"},
			{"fraction", "seedance-2.0-x-t2v", `{"seconds":5.5}`, "between 5 and 15"},
			{"overflow", "seedance-2.0-x-t2v", `{"seconds":18446744073686646784}`, "between 5 and 15"},
			{"null", "seedance-2.0-x-t2v", `{"duration":null}`, "between 5 and 15"},
			{"boolean", "seedance-2.0-x-t2v", `{"duration":true}`, "between 5 and 15"},
			{"seconds conflict", "seedance-2.0-x-t2v", `{"seconds":5,"duration":10}`, "conflicting values"},
			{"resolution", "seedance-2.0-x-t2v", `{"resolution":"1080p"}`, "480p or 720p"},
			{"size bypass", "seedance-2.0-x-t2v", `{"resolution":"720p","size":"1920x1080"}`, "480p or 720p"},
			{"resolution alias conflict", "seedance-2.0-spicy-t2v", `{"resolution":"480p","size":"1280x720"}`, "size conflicts with resolution"},
			{"size ratio conflict", "seedance-2.0-x-t2v", `{"size":"1280x720","ratio":"9:16"}`, "conflicts with ratio"},
			{"ratio conflict", "seedance-2.0-x-t2v", `{"ratio":"16:9","aspect_ratio":"9:16"}`, "conflicting values"},
			{"unsupported ratio", "seedance-2.0-x-t2v", `{"ratio":"adaptive"}`, "unsupported aspect ratio"},
			{"multiple outputs", "seedance-2.0-x-t2v", `{"n":2}`, "n must be 1"},
			{"zero outputs", "seedance-2.0-x-t2v", `{"n":0}`, "n must be 1"},
			{"string outputs", "seedance-2.0-x-t2v", `{"n":"1"}`, "n must be 1"},
			{"audio string", "seedance-2.0-x-t2v", `{"generate_audio":"false"}`, "generate_audio must be a boolean"},
			{"audio number", "seedance-2.0-x-t2v", `{"generate_audio":0}`, "generate_audio must be a boolean"},
			{"audio null", "seedance-2.0-x-t2v", `{"generate_audio":null}`, "generate_audio must be a boolean"},
			{"original canvas resolution", "seedance-2.0-x-t2v", `{"seconds":"10","duration":10,"resolution":"480p","aspect_ratio":"9:16","generate_audio":true}`, "only 720p"},
			{"seed unsupported", "seedance-2.0-x-t2v", `{"seed":0}`, "unsupported video parameter"},
			{"watermark unsupported", "seedance-2.0-x-t2v", `{"watermark":false}`, "unsupported video parameter"},
			{"nested multiplier", "seedance-2.0-x-t2v", `{"metadata":{"duration":9999}}`, "unsupported video parameter"},
			{"blank prompt", "seedance-2.0-x-t2v", `{"prompt":" "}`, "prompt must contain"},
			{"first frame", "seedance-2.0-x-r2v", `{"first_image_url":"https://cdn.example/i.png"}`, "does not support first or last frames"},
			{"last frame", "seedance-2.0-x-r2v", `{"last_image_url":"https://cdn.example/i.png"}`, "does not support first or last frames"},
			{"missing reference", "seedance-2.0-x-r2v", `{}`, "requires at least one image or video"},
			{"spicy missing reference", "seedance-2.0-spicy-r2v", `{}`, "requires at least one image, video or audio reference"},
			{"spicy output audio is not a reference", "seedance-2.0-spicy-r2v", `{"seconds":"10","duration":10,"resolution":"720p","aspect_ratio":"9:16","generate_audio":true}`, "requires at least one image, video or audio reference"},
			{"spicy empty references", "seedance-2.0-spicy-r2v", `{"images":[],"videos":[],"audios":[]}`, "requires at least one image, video or audio reference"},
			{"spicy text with reference", "seedance-2.0-spicy-t2v", `{"images":["https://cdn.example/i.png"]}`, "does not support reference media"},
			{"audio alone", "seedance-2.0-x-r2v", `{"audios":["https://cdn.example/a.mp3"]}`, "audio cannot be used alone"},
			{"text with reference", "seedance-2.0-x-t2v", `{"images":["https://cdn.example/i.png"]}`, "does not support reference media"},
			{"scalar reference", "seedance-2.0-x-r2v", `{"images":"https://cdn.example/i.png"}`, "must be an array"},
			{"typed reference", "seedance-2.0-x-r2v", `{"images":[123]}`, "public HTTP(S) URLs"},
			{"local reference", "seedance-2.0-x-r2v", `{"images":["file:///tmp/a.png"]}`, "public HTTP(S) URLs"},
			{"URL credential", "seedance-2.0-x-r2v", `{"images":["https://user:pass@cdn.example/a.png"]}`, "without credentials"},
			{"media conflict", "seedance-2.0-x-r2v", `{"images":["https://cdn.example/a"],"image_urls":["https://cdn.example/b"]}`, "conflicting reference fields"},
			{"too many images", "seedance-2.0-x-r2v", `{"images":["https://cdn.example/1","https://cdn.example/2","https://cdn.example/3","https://cdn.example/4","https://cdn.example/5","https://cdn.example/6","https://cdn.example/7","https://cdn.example/8","https://cdn.example/9","https://cdn.example/10"]}`, "at most 9"},
			{"too many videos", "seedance-2.0-x-r2v", `{"videos":["https://cdn.example/1","https://cdn.example/2","https://cdn.example/3","https://cdn.example/4"]}`, "at most 3"},
			{"too many audios", "seedance-2.0-x-r2v", `{"audios":["https://cdn.example/1","https://cdn.example/2","https://cdn.example/3","https://cdn.example/4"]}`, "at most 3"},
			{"unknown mapped model", "other-model", `{}`, "unsupported APIPod model"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				input := map[string]any{"model": "public-video", "prompt": "cat"}
				require.NoError(t, common.UnmarshalJsonStr(tc.fields, &input))
				_, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
					"model": "public-video", "upstreamModel": tc.model, "body": map[string]any{"kind": "json", "value": input},
				})
				require.ErrorContains(t, decodeErr, tc.reason)
				ctx := map[string]any{"model": "public-video", "upstreamModel": tc.model, "requestBody": input}
				for _, hook := range []string{"describeSpec", "extractUsage", "buildSubmitRequest"} {
					_, callErr := plugin.Engine.Call(t.Context(), hook, ctx)
					require.ErrorContains(t, callErr, tc.reason, hook)
				}
			})
		}
	})

	_, err = jsplugin.ReplayFixture(t.Context(), source, []byte(`{"cases":[
		{"name":"multipart standard references","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"model":"seedance-2.0-x-r2v","body":{"kind":"multipart","fields":{"model":["seedance-2.0-x-r2v"],"prompt":["cat"],"seconds":["6"],"size":["1280x720"],"n":["1"],"images[]":["https://cdn.example/a.png"],"videos":["[\"https://cdn.example/v.mp4\"]"]}}}],"expected":{"kind":"submit","model":"seedance-2.0-x-r2v","action":"reference_to_video","requestBody":{"prompt":"cat","duration":6,"resolution":"720p","aspect_ratio":"16:9","image_urls":["https://cdn.example/a.png"],"video_urls":["https://cdn.example/v.mp4"]}}},
		{"name":"duplicate multipart scalar","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","fields":{"seconds":["5","15"]}}}],"expectedError":"must be provided once"},
		{"name":"invalid multipart audio","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","fields":{"model":["seedance-2.0"],"prompt":["cat"],"generate_audio":["0"]}}}],"expectedError":"generate_audio must be a boolean"},
		{"name":"duplicate multipart audio","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","fields":{"generate_audio":["true","false"]}}}],"expectedError":"must be provided once"},
		{"name":"local files rejected","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","files":[{"ref":"request_file:input_reference"}]}}],"expectedError":"local file uploads are unsupported"},
		{"name":"submission envelope","hook":"parseSubmitResponse","args":[{},{"statusCode":200,"body":{"code":200,"message":"success","data":{"task_id":"vid_test"}}}],"expected":{"taskId":"vid_test","taskData":{"code":200,"message":"success","data":{"task_id":"vid_test"}}}},
		{"name":"missing id is ambiguous","hook":"parseSubmitResponse","args":[{},{"body":{"code":200,"data":{}}}],"expectedError":"no unambiguous task id"},
		{"name":"2xx error is ambiguous","hook":"parseSubmitResponse","args":[{},{"body":{"code":500,"message":"error","data":{"task_id":"vid_test"}}}],"expectedError":"no unambiguous task id"},
		{"name":"accepted immediate failure","hook":"parseSubmitResponse","args":[{},{"body":{"code":200,"data":{"task_id":"vid_test","status":"failed","error":"capacity exhausted"}}}],"expected":{"taskId":"vid_test","taskData":{"code":200,"data":{"task_id":"vid_test","status":"failed","error":"capacity exhausted"}},"immediate":{"status":"FAILURE","reason":"capacity exhausted"}}},
		{"name":"query uses persisted upstream id","hook":"buildQueryRequest","args":[{"taskId":"vid/a?b","baseUrl":"https://api.apipod.ai/","authHeader":"Bearer fixture-only-key"}],"expected":{"url":"https://api.apipod.ai/v1/videos/status/vid%2Fa%3Fb","method":"GET","headers":{"Authorization":"Bearer fixture-only-key"}}},
		{"name":"pending","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_test","status":"pending","progress":0}}],"expected":{"status":"QUEUED","progress":"0%"}},
		{"name":"processing","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_test","status":"processing","progress":50}}],"expected":{"status":"IN_PROGRESS","progress":"50%"}},
		{"name":"completed","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_test","status":"completed","result":["https://cdn.example/v.mp4"],"usage":{"cost":0.2}}}],"expected":{"status":"SUCCESS","progress":"100%"}},
		{"name":"failed","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_test","status":"failed","error":"content moderation rejected the prompt"}}],"expected":{"status":"FAILURE","reason":"content moderation rejected the prompt"}},
		{"name":"cancelled","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_test","status":"cancelled"}}],"expected":{"status":"FAILURE","reason":"apipod_task_cancelled: video generation failed"}},
		{"name":"cancelled attribution","hook":"classifyFailure","args":["apipod_task_cancelled: video generation failed"],"expected":"cancelled"},
		{"name":"unknown state","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_test","status":"constructor"}}],"expected":{"status":"UNKNOWN","reason":"unrecognized task status or missing video URL"}},
		{"name":"empty result cannot complete","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_test","status":"completed","result":[]}}],"expected":{"status":"UNKNOWN","reason":"unrecognized task status or missing video URL"}},
		{"name":"wrong task cannot settle","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_other","status":"completed","result":["https://cdn.example/v.mp4"]}}],"expected":{"status":"UNKNOWN","reason":"upstream task id does not match the queried task"}},
		{"name":"poll error envelope cannot complete","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":500,"data":{"task_id":"vid_test","status":"completed","result":["https://cdn.example/v.mp4"]}}],"expected":{"status":"UNKNOWN","reason":"invalid APIPod task response"}},
		{"name":"contradictory success","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"code":200,"data":{"task_id":"vid_test","status":"completed","result":["https://cdn.example/v.mp4"],"error":"failed"}}],"expected":{"status":"UNKNOWN","reason":"conflicting APIPod task response"}},
		{"name":"upstream amount is not a usage quantity","hook":"extractUsageOnComplete","args":[{},{"status":"SUCCESS"},{"code":200,"data":{"usage":{"cost":999999,"completion_tokens":123}}}],"expected":{}},
		{"name":"video artifact","hook":"listArtifacts","args":[{"status":"SUCCESS","data":{"code":200,"data":{"task_id":"vid_test","status":"completed","result":["https://cdn.example/v.mp4"]}}}],"expected":[{"key":"video","type":"video","mimeType":"video/mp4"}]},
		{"name":"unfinished media hidden","hook":"listArtifacts","args":[{"status":"IN_PROGRESS","data":{"code":200,"data":{"status":"processing","result":["https://cdn.example/v.mp4"]}}}],"expected":[]},
		{"name":"credentialless content","hook":"buildContentRequest","args":[{"artifactKey":"video","apiKey":"fixture-only-key","clientRequest":{"method":"HEAD","headers":{"Authorization":"client-secret"}},"data":{"code":200,"data":{"status":"completed","result":["https://cdn.example/v.mp4?sig=original%2Bvalue"]}}}],"expected":{"url":"https://cdn.example/v.mp4?sig=original%2Bvalue","method":"HEAD","credentialless":true}},
		{"name":"credential URL rejected","hook":"buildContentRequest","args":[{"artifactKey":"video","data":{"code":200,"data":{"status":"completed","result":["https://user:pass@cdn.example/v.mp4"]}}}],"expectedError":"artifact_not_found"},
		{"name":"renderer omits provider costs and ids","hook":"protocols","path":["openai_video","render"],"args":[{},{"status":"SUCCESS","data":{"code":200,"data":{"task_id":"private_id","status":"completed","result":["https://cdn.example/v.mp4"],"usage":{"cost":123}}}}],"expected":{"url":"https://cdn.example/v.mp4"}}
	]}`))
	require.NoError(t, err)
}

// Replay through Sobek as well as the JS tests: HTTP JSON numbers, immutable
// host-backed input maps and nested result envelopes must cross the runtime intact.
func TestOnmiVideoPluginLifecycle(t *testing.T) {
	source, err := Source("onmi")
	require.NoError(t, err)
	_, err = jsplugin.ReplayFixture(t.Context(), source, []byte(`{"cases":[
		{"name":"mapped E request preserves false and typed references","hook":"buildSubmitRequest","args":[{"model":"public-video","upstreamModel":"e组-sd2.0mini","publicTaskId":"task_fixture","baseUrl":"https://api.onmi.eu.cc/v1/","authHeader":"Bearer fixture-only-key","requestBody":{"prompt":"海边 @Video1","seconds":10,"resolution":"480p","generate_audio":false,"references":[{"type":"video","url":"https://cdn.example/v.mp4","name":"运镜","duration":2.5}]}}],"expected":{"url":"https://api.onmi.eu.cc/v1/videos","method":"POST","headers":{"Authorization":"Bearer fixture-only-key","Content-Type":"application/json","Idempotency-Key":"task_fixture"},"body":{"model":"e组-sd2.0mini","prompt":"海边 @Video1","seconds":10,"resolution":"480p","aspect_ratio":"16:9","generate_audio":false,"references":[{"type":"video","url":"https://cdn.example/v.mp4","name":"运镜","duration":2.5}]}}},
		{"name":"reference metadata cannot replace output usage","hook":"extractUsage","args":[{"model":"e组-sd2.0mini","requestBody":{"prompt":"cat","seconds":10,"resolution":"480p","references":[{"type":"video","url":"https://cdn.example/v.mp4","duration":2.5}]}}],"expected":{"output_seconds":10,"resolution":"480p","video_input":true}},
		{"name":"completion keeps frozen local sales facts","hook":"extractUsageOnComplete","args":[{},{"status":"SUCCESS"},{"seconds":99999,"usage":{"seconds":2}}],"expected":{}},
		{"name":"accepted terminal failure is persisted","hook":"parseSubmitResponse","args":[{},{"body":{"id":"upstream_fixture","status":"failed","error":{"code":"video_request_rejected","message":"Video generation failed"}}}],"expected":{"taskId":"upstream_fixture","taskData":{"id":"upstream_fixture","status":"failed","error":{"code":"video_request_rejected","message":"Video generation failed"}},"immediate":{"status":"FAILURE","reason":"video request rejected"}}},
		{"name":"unknown status stays unknown","hook":"parseTaskResult","args":[{},{"status":"unexpected"}],"expected":{"status":"UNKNOWN","reason":"unrecognized upstream task status"}},
		{"name":"content ignores untrusted response URL","hook":"buildContentRequest","args":[{"baseUrl":"https://api.onmi.eu.cc","apiKey":"fixture-only-key","upstreamTaskId":"up/a","artifactKey":"video","data":{"url":"https://untrusted.example/a"},"clientRequest":{"method":"HEAD"}}],"expected":{"url":"https://api.onmi.eu.cc/v1/videos/up%2Fa/content","method":"HEAD","headers":{"Authorization":"Bearer fixture-only-key"}}}
	]}`))
	require.NoError(t, err)
}

func TestAggcVideoPluginLifecycle(t *testing.T) {
	// Adding this relay must not replace the official MiniMax billing metadata
	// or add a non-schedulable official provider to its scheduling pool.
	generation := jsplugin.DefaultRegistry.Generation()
	official, found := generation.LookupEndpoint("POST", "/v1/videos", "MiniMax-H3")
	require.True(t, found)
	assert.Equal(t, "hailuo", official.Plugin.Meta.Key)
	relay, found := generation.LookupEndpoint("POST", "/v1/videos", "aggc-minimax-h3")
	require.True(t, found)
	assert.Equal(t, "aggc", relay.Plugin.Meta.Key)

	source, err := Source("aggc")
	require.NoError(t, err)
	_, err = jsplugin.ReplayFixture(t.Context(), source, []byte(`{"cases":[
		{
			"name":"mapped alias builds the AGGC wire request with raw API key",
			"hook":"buildSubmitRequest",
			"args":[{"model":"public-video","upstreamModel":"sd2.5-op","baseUrl":"https://aggc.site/","authHeader":"fixture-only-key","requestBody":{
				"prompt":"cat","params":{"duration":30,"resolution":"1080p","imageUrls":["https://cdn.example/a.png"],"videoUrls":["https://cdn.example/a.mp4","https://cdn.example/a.mp4"]}
			}}],
			"expected":{"url":"https://aggc.site/api/v1/prot/generate","method":"POST","headers":{"x-api-key":"fixture-only-key","Content-Type":"application/json"},"body":{
				"model_id":"sd2.5-op","type":"video","prompt":"cat","params":{"duration":30,"aspectRatio":"16:9","resolution":"1080p","imageUrls":["https://cdn.example/a.png"],"videoUrls":["https://cdn.example/a.mp4","https://cdn.example/a.mp4"]}
			}}
		},
		{
			"name":"nested duration cannot bypass quoting limits",
			"hook":"describeSpec",
			"args":[{"model":"public-video","upstreamModel":"sd2.0-933-op","requestBody":{"prompt":"cat","params":{"duration":30,"resolution":"720p"}}}],
			"expectedError":"duration must be between 4 and 15 seconds"
		},
		{
			"name":"submission accepts a numeric job id without premature completion",
			"hook":"parseSubmitResponse",
			"args":[{},{"statusCode":200,"body":{"code":0,"message":"OK","data":{"job_id":123,"status":"success","credits_frozen":10}}}],
			"expected":{"taskId":"123","taskData":{"code":0,"message":"OK","data":{"job_id":123,"status":"success","credits_frozen":10}}}
		},
		{
			"name":"poll context has no request body",
			"hook":"buildQueryRequest",
			"args":[{"taskId":"123","baseUrl":"https://aggc.site","apiKey":"fixture-only-key"}],
			"expected":{"url":"https://aggc.site/api/v1/prot/query/123","method":"GET","headers":{"x-api-key":"fixture-only-key"}}
		},
		{
			"name":"poll completes from the nested video result",
			"hook":"parseTaskResult",
			"args":[{"taskId":"123"},{"code":0,"data":{"job_id":123,"status":"success","video_url":"https://cdn.example/video.mp4"}}],
			"expected":{"status":"SUCCESS"}
		},
		{
			"name":"documented generation failure remains an accepted failed task",
			"hook":"parseSubmitResponse",
			"args":[{},{"statusCode":200,"body":{"code":3001,"data":{"job_id":123,"status":"failed","error_message":"invalid video"}}}],
			"expected":{"taskId":"123","taskData":{"code":3001,"data":{"job_id":123,"status":"failed","error_message":"invalid video"}},"immediate":{"status":"FAILURE","reason":"aggc:3001: invalid video"}}
		},
		{
			"name":"video download never receives the API key",
			"hook":"buildContentRequest",
			"args":[{"artifactKey":"video","apiKey":"fixture-only-key","data":{"code":0,"data":{"job_id":123,"status":"success","video_url":"https://cdn.example/video.mp4"}},"clientRequest":{"method":"HEAD","headers":{"Authorization":"private-client-token"}}}],
			"expected":{"url":"https://cdn.example/video.mp4","method":"HEAD","credentialless":true}
		}
	]}`))
	require.NoError(t, err)
}

func TestVobilemaxVideoPluginLifecycle(t *testing.T) {
	source, err := Source("vobilemax")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "vobilemax"})
	require.NoError(t, err)
	for _, model := range []string{"vobilemax-seedance-2.0", "vobilemax-seedance-2.5", "vobilemax-kling-v3"} {
		binding, found := jsplugin.DefaultRegistry.Generation().LookupEndpoint("POST", "/v1/videos", model)
		require.True(t, found, model)
		assert.Equal(t, "vobilemax", binding.Plugin.Meta.Key)
	}

	t.Run("decoded wire request matches billed and scheduled facts", func(t *testing.T) {
		for _, tc := range []struct {
			name, fields, normalized string
		}{
			{"defaults", `{}`, `{"prompt":"海边日出","seconds":15,"resolution":"720p","ratio":"16:9","images":[],"videos":[],"audios":[]}`},
			{"canvas audio enabled", `{"generate_audio":true}`, `{"prompt":"海边日出","seconds":15,"resolution":"720p","ratio":"16:9","images":[],"videos":[],"audios":[],"generate_audio":true}`},
			{"canvas audio disabled", `{"generate_audio":false}`, `{"prompt":"海边日出","seconds":15,"resolution":"720p","ratio":"16:9","images":[],"videos":[],"audios":[],"generate_audio":false}`},
			{"standard portrait", `{"seconds":"4","size":"720x1280","n":1,"images":["https://cdn.example/a.png"],"videos":["https://cdn.example/v.mp4","https://cdn.example/v.mp4"],"audios":["https://cdn.example/a.wav"]}`, `{"prompt":"海边日出","seconds":4,"resolution":"720p","ratio":"9:16","images":["https://cdn.example/a.png"],"videos":["https://cdn.example/v.mp4","https://cdn.example/v.mp4"],"audios":["https://cdn.example/a.wav"]}`},
			{"native options and equivalent aliases", `{"seconds":3,"duration":"3","resolution":"480p","options":{"duration":3,"durationSeconds":3,"resolution":"480p","quality":"480p","ratio":"4:3","aspectRatio":"4:3","count":1},"imageUrls":["https://cdn.example/a.png"],"image_urls":["https://cdn.example/a.png"],"mode":"allReference","sync":false}`, `{"prompt":"海边日出","seconds":3,"resolution":"480p","ratio":"4:3","images":["https://cdn.example/a.png"],"videos":[],"audios":[],"mode":"allReference"}`},
			{"ultrawide size", `{"seconds":4,"size":"2560x1080","video_urls":["https://cdn.example/v.mp4"],"audioUrls":["https://cdn.example/a.wav"]}`, `{"prompt":"海边日出","seconds":4,"resolution":"1080p","ratio":"21:9","images":[],"videos":["https://cdn.example/v.mp4"],"audios":["https://cdn.example/a.wav"]}`},
			{"1080p aliases", `{"seconds":4,"size":"1920x1080","resolution":"1080p"}`, `{"prompt":"海边日出","seconds":4,"resolution":"1080p","ratio":"16:9","images":[],"videos":[],"audios":[]}`},
			{"4k aliases and landscape pixels", `{"seconds":4,"size":"3840x2160","resolution":"4K","options":{"quality":"2160p"}}`, `{"prompt":"海边日出","seconds":4,"resolution":"4k","ratio":"16:9","images":[],"videos":[],"audios":[]}`},
			{"4k portrait pixels", `{"seconds":4,"size":"2160x3840"}`, `{"prompt":"海边日出","seconds":4,"resolution":"4k","ratio":"9:16","images":[],"videos":[],"audios":[]}`},
			{"4k size tier", `{"seconds":4,"size":"2160p","resolution":"4k"}`, `{"prompt":"海边日出","seconds":4,"resolution":"4k","ratio":"16:9","images":[],"videos":[],"audios":[]}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var input, normalized map[string]any
				require.NoError(t, common.UnmarshalJsonStr(tc.fields, &input))
				require.NoError(t, common.UnmarshalJsonStr(tc.normalized, &normalized))
				input["model"], input["prompt"] = "public-video", "海边日出"
				before, marshalErr := common.Marshal(input)
				require.NoError(t, marshalErr)
				decoded, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
					"model": "public-video", "upstreamModel": "public-video", "body": map[string]any{"kind": "json", "value": input},
				})
				require.NoError(t, decodeErr)
				intent := decoded.(map[string]any)
				assert.Equal(t, "public-video", intent["model"])
				encoded, marshalErr := common.Marshal(intent["requestBody"])
				require.NoError(t, marshalErr)
				assert.JSONEq(t, tc.normalized, string(encoded))
				ctx := map[string]any{"model": "public-video", "upstreamModel": "vobilemax-seedance-2.0", "requestBody": intent["requestBody"], "baseUrl": "https://vobile.example/", "authHeader": "Bearer fixture-only-key"}
				built, buildErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, buildErr)
				body := map[string]any{
					"model": "doubao-seedance-2-0-260128", "prompt": "海边日出", "seconds": normalized["seconds"], "sync": false, "mode": "allReference",
					"imageUrls": normalized["images"], "videoUrls": normalized["videos"], "audioUrls": normalized["audios"],
					"options": map[string]any{"duration": normalized["seconds"], "durationSeconds": normalized["seconds"], "count": 1,
						"resolution": normalized["resolution"], "quality": normalized["resolution"], "ratio": normalized["ratio"], "aspectRatio": normalized["ratio"]},
				}
				if audio, present := normalized["generate_audio"]; present {
					body["generate_audio"] = audio
				}
				want, marshalErr := common.Marshal(map[string]any{"url": "https://vobile.example/v1/videos", "method": "POST", "headers": map[string]string{
					"Authorization": "Bearer fixture-only-key", "Content-Type": "application/json; charset=utf-8",
				}, "body": body})
				require.NoError(t, marshalErr)
				got, marshalErr := common.Marshal(built)
				require.NoError(t, marshalErr)
				assert.JSONEq(t, string(want), string(got))
				usage, usageErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
				require.NoError(t, usageErr)
				got, marshalErr = common.Marshal(usage)
				require.NoError(t, marshalErr)
				want, marshalErr = common.Marshal(map[string]any{"seconds": normalized["seconds"], "resolution": normalized["resolution"], "video_input": len(normalized["videos"].([]any)) > 0})
				require.NoError(t, marshalErr)
				assert.JSONEq(t, string(want), string(got))
				value, specErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
				require.NoError(t, specErr)
				parsed, _, parseErr := spec.Parse(value.(map[string]any))
				require.NoError(t, parseErr)
				assert.Equal(t, normalized["resolution"], parsed.Tier)
				assert.Equal(t, normalized["seconds"], *parsed.OutputSeconds)
				assert.Equal(t, len(normalized["images"].([]any)), parsed.References["image"])
				assert.Equal(t, len(normalized["audios"].([]any)), parsed.References["audio"])
				urls := normalized["videos"].([]any)
				assert.Equal(t, len(urls), parsed.References["video"])
				require.Len(t, parsed.ReferenceVideoURLs, len(urls))
				for i, url := range urls {
					assert.Equal(t, url, parsed.ReferenceVideoURLs[i])
				}
				retried, retryErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, retryErr)
				assert.Equal(t, built, retried)
				after, marshalErr := common.Marshal(input)
				require.NoError(t, marshalErr)
				assert.JSONEq(t, string(before), string(after), "hooks must not mutate host inputs or accumulate references")
			})
		}
	})

	t.Run("new models preserve mapped wire parameters and scheduling facts", func(t *testing.T) {
		for _, tc := range []struct {
			name, model, upstream, fields, tier, mode string
			seconds                                   float64
			images, videos, audios, frames            int
			ratio                                     string
		}{
			{"seedance25 minimum", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":4,"resolution":"480p"}`, "480p", "allReference", 4, 0, 0, 0, 0, "16:9"},
			{"seedance25 mixed references", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":15,"resolution":"720p","images":["https://cdn.example/1.png"],"videos":["https://cdn.example/1.mp4","https://cdn.example/1.mp4"],"audios":["https://cdn.example/1.wav"]}`, "720p", "allReference", 15, 1, 2, 1, 0, "16:9"},
			{"seedance25 maximum", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":30,"size":"1920x1080"}`, "1080p", "allReference", 30, 0, 0, 0, 0, "16:9"},
			{"seedance20 first frame", "vobilemax-seedance-2.0", "doubao-seedance-2-0-260128", `{"seconds":4,"resolution":"1080p","first_image_url":"https://cdn.example/first.png"}`, "1080p", "image2video", 4, 0, 0, 0, 1, "16:9"},
			{"seedance20 standard frames 4k", "vobilemax-seedance-2.0", "doubao-seedance-2-0-260128", `{"seconds":5,"size":"2160x3840","first_image_url":"https://cdn.example/first.png","last_image_url":"https://cdn.example/last.png"}`, "4k", "firstLastFrame", 5, 0, 0, 0, 2, "9:16"},
			{"seedance20 native frames", "vobilemax-seedance-2.0", "doubao-seedance-2-0-260128", `{"seconds":5,"resolution":"720p","mode":"firstLastFrame","imageUrls":["https://cdn.example/first.png","https://cdn.example/last.png"],"ratio":"adaptive"}`, "720p", "firstLastFrame", 5, 0, 0, 0, 2, "adaptive"},
			{"seedance25 first frame default ratio", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":4,"resolution":"480p","first_image_url":"https://cdn.example/first.png"}`, "480p", "image2video", 4, 0, 0, 0, 1, "adaptive"},
			{"seedance25 standard frames default ratio", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":30,"size":"1080p","first_image_url":"https://cdn.example/first.png","last_image_url":"https://cdn.example/last.png"}`, "1080p", "firstLastFrame", 30, 0, 0, 0, 2, "adaptive"},
			{"seedance25 native frames explicit ratio", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":15,"resolution":"720p","mode":"firstLastFrame","image_urls":["https://cdn.example/first.png","https://cdn.example/last.png"],"options":{"ratio":"adaptive","aspectRatio":"adaptive"}}`, "720p", "firstLastFrame", 15, 0, 0, 0, 2, "adaptive"},
			{"seedance25 optional last frame", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":5,"mode":"firstLastFrame","images":["https://cdn.example/first.png"]}`, "720p", "firstLastFrame", 5, 0, 0, 0, 1, "adaptive"},
			{"seedance images keep reference meaning", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":5,"images":["https://cdn.example/first.png","https://cdn.example/last.png"]}`, "720p", "allReference", 5, 2, 0, 0, 0, "16:9"},
			{"kling first frame", "vobilemax-kling-v3", "kling-v3", `{"seconds":3,"resolution":"720p","first_image_url":"https://cdn.example/first.png"}`, "720p", "image2video", 3, 0, 0, 0, 1, "16:9"},
			{"kling native frames", "vobilemax-kling-v3", "kling-v3", `{"seconds":5,"resolution":"1080p","mode":"firstLastFrame","imageUrls":["https://cdn.example/first.png","https://cdn.example/last.png"]}`, "1080p", "firstLastFrame", 5, 0, 0, 0, 2, "16:9"},
			{"kling standard frames 4k", "vobilemax-kling-v3", "kling-v3", `{"seconds":10,"size":"2160x3840","resolution":"4K","first_image_url":"https://cdn.example/first.png","last_image_url":"https://cdn.example/last.png"}`, "4k", "firstLastFrame", 10, 0, 0, 0, 2, "9:16"},
			{"kling inferred frames after mapping", "vobilemax-kling-v3", "kling-v3", `{"seconds":15,"size":"4k","images":["https://cdn.example/first.png","https://cdn.example/last.png"]}`, "4k", "firstLastFrame", 15, 0, 0, 0, 2, "16:9"},
			{"kling canvas single image audio disabled", "vobilemax-kling-v3", "kling-v3", `{"seconds":15,"resolution":"1080p","ratio":"16:9","images":["https://cdn.example/first.png"],"generate_audio":false}`, "1080p", "image2video", 15, 0, 0, 0, 1, "16:9"},
			{"kling canvas frames audio enabled", "vobilemax-kling-v3", "kling-v3", `{"seconds":15,"resolution":"1080p","ratio":"16:9","images":["https://cdn.example/first.png","https://cdn.example/last.png"],"generate_audio":true}`, "1080p", "firstLastFrame", 15, 0, 0, 0, 2, "16:9"},
			{"seedance25 canvas audio disabled", "vobilemax-seedance-2.5", "doubao-seedance-2-5-260628", `{"seconds":15,"resolution":"1080p","generate_audio":false}`, "1080p", "allReference", 15, 0, 0, 0, 0, "16:9"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var input map[string]any
				require.NoError(t, common.UnmarshalJsonStr(tc.fields, &input))
				input["model"], input["prompt"] = "public-video", "cat"
				before, err := common.Marshal(input)
				require.NoError(t, err)
				// Exercise both initial alias decoding and a selected upstream pin.
				for _, decodeModel := range []string{"public-video", tc.model, tc.upstream} {
					decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
						"model": "public-video", "upstreamModel": decodeModel, "body": map[string]any{"kind": "json", "value": input},
					})
					require.NoError(t, err)
					intent := decoded.(map[string]any)
					assert.Equal(t, "public-video", intent["model"])
					for _, mappedModel := range []string{tc.model, tc.upstream} {
						ctx := map[string]any{"model": "public-video", "upstreamModel": mappedModel, "requestBody": intent["requestBody"], "baseUrl": "https://vobile.example", "apiKey": "fixture-only-key"}
						built, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
						require.NoError(t, err)
						body := built.(map[string]any)["body"].(map[string]any)
						assert.Equal(t, tc.upstream, body["model"])
						assert.Equal(t, tc.mode, body["mode"])
						assert.EqualValues(t, tc.seconds, body["seconds"])
						if audio, present := input["generate_audio"]; present {
							assert.Equal(t, audio, body["generate_audio"])
						} else {
							assert.NotContains(t, body, "generate_audio")
						}
						options := body["options"].(map[string]any)
						assert.Equal(t, tc.tier, options["resolution"])
						assert.Equal(t, tc.tier, options["quality"])
						assert.Equal(t, tc.ratio, options["ratio"])
						assert.Equal(t, tc.ratio, options["aspectRatio"])
						assert.EqualValues(t, tc.seconds, options["duration"])
						assert.EqualValues(t, tc.seconds, options["durationSeconds"])
						if tc.frames > 0 {
							frames := []any{"https://cdn.example/first.png"}
							if tc.frames == 2 {
								frames = append(frames, "https://cdn.example/last.png")
							}
							assert.Equal(t, frames, body["imageUrls"])
						}
						usage, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
						require.NoError(t, err)
						encoded, err := common.Marshal(usage)
						require.NoError(t, err)
						want, err := common.Marshal(map[string]any{"seconds": tc.seconds, "resolution": tc.tier, "video_input": tc.videos > 0})
						require.NoError(t, err)
						assert.JSONEq(t, string(want), string(encoded))
						value, err := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
						require.NoError(t, err)
						parsed, _, err := spec.Parse(value.(map[string]any))
						require.NoError(t, err)
						assert.Equal(t, tc.seconds, *parsed.OutputSeconds)
						assert.Equal(t, tc.tier, parsed.Tier)
						assert.Equal(t, map[string]int{"image": tc.images, "video": tc.videos, "audio": tc.audios, "frame": tc.frames}, parsed.References)
						require.Len(t, parsed.ReferenceVideoURLs, tc.videos)
						for i, url := range parsed.ReferenceVideoURLs {
							assert.Equal(t, body["videoUrls"].([]any)[i], url)
						}
						retried, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
						require.NoError(t, err)
						assert.Equal(t, built, retried)
					}
				}
				after, err := common.Marshal(input)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after))
			})
		}
	})

	t.Run("mapped model restrictions cannot be bypassed through aliases or options", func(t *testing.T) {
		for _, tc := range []struct{ name, model, fields, message string }{
			{"seedance25 lower bound", "vobilemax-seedance-2.5", `{"seconds":3}`, "between 4 and 30"},
			{"seedance25 upper bound", "vobilemax-seedance-2.5", `{"options":{"durationSeconds":31}}`, "between 4 and 30"},
			{"seedance25 4k", "vobilemax-seedance-2.5", `{"size":"3840x2160"}`, "resolution must be 480p, 720p, 1080p"},
			{"seedance20 too many frames", "vobilemax-seedance-2.0", `{"mode":"firstLastFrame","images":["https://cdn.example/a.png","https://cdn.example/b.png","https://cdn.example/c.png"]}`, "requires one or two images"},
			{"seedance20 mixed video and frames", "vobilemax-seedance-2.0", `{"first_image_url":"https://cdn.example/a.png","videos":["https://cdn.example/a.mp4"]}`, "do not support reference videos or audio"},
			{"seedance25 mixed audio and frames", "vobilemax-seedance-2.5", `{"first_image_url":"https://cdn.example/a.png","audios":["https://cdn.example/a.wav"]}`, "do not support reference videos or audio"},
			{"seedance25 fixed frame ratio", "vobilemax-seedance-2.5", `{"first_image_url":"https://cdn.example/a.png","ratio":"16:9"}`, "frame modes require ratio adaptive"},
			{"seedance25 nested fixed frame ratio", "vobilemax-seedance-2.5", `{"mode":"firstLastFrame","imageUrls":["https://cdn.example/a.png"],"options":{"aspectRatio":"9:16"}}`, "frame modes require ratio adaptive"},
			{"seedance25 pixel frame size", "vobilemax-seedance-2.5", `{"first_image_url":"https://cdn.example/a.png","size":"1280x720"}`, "frame modes require ratio adaptive"},
			{"kling no first frame", "vobilemax-kling-v3", `{}`, "requires one image"},
			{"kling unsupported seconds", "vobilemax-kling-v3", `{"images":["https://cdn.example/a.png"],"options":{"duration":4}}`, "seconds must be 3, 5, 10 or 15"},
			{"kling 480p", "vobilemax-kling-v3", `{"images":["https://cdn.example/a.png"],"resolution":"480p"}`, "resolution must be 720p, 1080p, 4k"},
			{"kling reference video", "vobilemax-kling-v3", `{"images":["https://cdn.example/a.png"],"videos":["https://cdn.example/a.mp4"]}`, "do not support reference videos or audio"},
			{"kling reference audio", "vobilemax-kling-v3", `{"images":["https://cdn.example/a.png"],"audios":["https://cdn.example/a.wav"]}`, "do not support reference videos or audio"},
			{"kling all reference mode", "vobilemax-kling-v3", `{"images":["https://cdn.example/a.png"],"mode":"allReference"}`, "Kling requires"},
			{"kling too many frames", "vobilemax-kling-v3", `{"images":["https://cdn.example/a.png","https://cdn.example/b.png","https://cdn.example/c.png"]}`, "requires one or two images"},
			{"kling mixed frame sources", "vobilemax-kling-v3", `{"images":["https://cdn.example/a.png"],"first_image_url":"https://cdn.example/b.png"}`, "conflict with images"},
			{"kling last without first", "vobilemax-kling-v3", `{"last_image_url":"https://cdn.example/b.png"}`, "requires first_image_url"},
			{"kling invalid frame URL", "vobilemax-kling-v3", `{"first_image_url":"file:///a.png"}`, "public http(s) URL"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var input map[string]any
				require.NoError(t, common.UnmarshalJsonStr(tc.fields, &input))
				input["model"], input["prompt"] = "public-video", "cat"
				_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
					"model": "public-video", "upstreamModel": tc.model, "body": map[string]any{"kind": "json", "value": input},
				})
				require.ErrorContains(t, err, tc.message)
				for _, hook := range []string{"extractUsage", "describeSpec", "buildSubmitRequest"} {
					_, err := plugin.Engine.Call(t.Context(), hook, map[string]any{"model": "public-video", "upstreamModel": tc.model,
						"requestBody": input, "baseUrl": "https://vobile.example", "apiKey": "fixture-only-key"})
					require.ErrorContains(t, err, tc.message, hook)
				}
			})
		}
	})

	t.Run("invalid input and channel overrides fail before quoting or submitting", func(t *testing.T) {
		for _, tc := range []struct {
			name, fields, message string
		}{
			{"zero", `{"seconds":0}`, "between 1 and 3600"},
			{"negative", `{"seconds":-1}`, "between 1 and 3600"},
			{"fraction", `{"seconds":1.5}`, "integer seconds"},
			{"boolean", `{"seconds":true}`, "integer seconds"},
			{"null", `{"seconds":null}`, "integer seconds"},
			{"overflow", `{"seconds":18446744073686646784}`, "between 1 and 3600"},
			{"nested duration", `{"options":{"duration":3601}}`, "between 1 and 3600"},
			{"nested alias", `{"options":{"durationSeconds":0}}`, "between 1 and 3600"},
			{"duration conflict", `{"seconds":4,"options":{"duration":8}}`, "conflicting values"},
			{"multiple outputs", `{"n":2}`, "n must be 1"},
			{"nested output count", `{"options":{"count":2}}`, "count must be 1"},
			{"unavailable tier", `{"resolution":"8k"}`, "480p, 720p, 1080p or 4k"},
			{"4k size conflict", `{"size":"3840x2160","resolution":"1080p"}`, "size conflicts with resolution"},
			{"size conflict", `{"size":"1280x720","resolution":"1080p"}`, "size conflicts with resolution"},
			{"ratio conflict", `{"size":"720x1280","ratio":"16:9"}`, "size conflicts with ratio"},
			{"nested tier conflict", `{"resolution":"480p","options":{"quality":"720p"}}`, "conflicting values"},
			{"unrecognized dimensions", `{"size":"1000x720"}`, "unsupported aspect ratio"},
			{"malformed references", `{"images":[42]}`, "public http(s) URLs"},
			{"reference object", `{"videos":{"url":"https://cdn.example/v.mp4"}}`, "array"},
			{"reference disagreement", `{"videos":["https://cdn.example/a.mp4"],"videoUrls":["https://cdn.example/b.mp4"]}`, "conflicting values"},
			{"URL credentials", `{"audios":["https://user:password@cdn.example/a.wav"]}`, "without credentials"},
			{"URL scheme", `{"images":["data:image/png;base64,AA=="]}`, "public http(s) URLs"},
			{"blank prompt", `{"prompt":" "}`, "non-empty string"},
			{"conflicting frame mode", `{"first_image_url":"https://cdn.example/a.png","mode":"allReference"}`, "require image2video or firstLastFrame mode"},
			{"last frame", `{"last_image_url":"https://cdn.example/a.png"}`, "last_image_url requires first_image_url"},
			{"sync request", `{"sync":true}`, "sync must be false"},
			{"string audio switch", `{"generate_audio":"false"}`, "generate_audio must be a boolean"},
			{"numeric audio switch", `{"generate_audio":0}`, "generate_audio must be a boolean"},
			{"null audio switch", `{"generate_audio":null}`, "generate_audio must be a boolean"},
			{"unverified mode", `{"mode":"videoExtend"}`, "mode must be"},
			{"client reference duration", `{"ref_video_seconds":0}`, "reference duration hints are not accepted"},
			{"hidden multiplier", `{"metadata":{"duration":99999}}`, "unsupported video parameter"},
			{"unknown option", `{"options":{"seed":42}}`, "unsupported options parameter"},
			{"null options", `{"options":null}`, "options must be an object"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var input map[string]any
				require.NoError(t, common.UnmarshalJsonStr(tc.fields, &input))
				input["model"] = "vobilemax-seedance-2.0"
				if _, exists := input["prompt"]; !exists {
					input["prompt"] = "cat"
				}
				_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": input["model"], "body": map[string]any{"kind": "json", "value": input}})
				require.ErrorContains(t, err, tc.message)
				for _, hook := range []string{"extractUsage", "describeSpec", "buildSubmitRequest"} {
					_, err = plugin.Engine.Call(t.Context(), hook, map[string]any{"model": input["model"], "requestBody": input, "baseUrl": "https://vobile.example", "apiKey": "fixture-only-key"})
					require.ErrorContains(t, err, tc.message, hook)
				}
			})
		}
	})

	_, err = jsplugin.ReplayFixture(t.Context(), source, []byte(`{"cases":[
		{"name":"quoted queued task is accepted without treating zero cost as free","hook":"parseSubmitResponse","args":[{},{"statusCode":200,"body":{"id":"cgt_fixture","status":"queued","seconds":null,"progress":0,"vobile":{"creditCost":"0.000000","quote":{"payable_amount":"4000","currency":"CREDIT"}}}}],"expected":{"taskId":"cgt_fixture","taskData":{"id":"cgt_fixture","status":"queued","seconds":null,"progress":0,"vobile":{"creditCost":"0.000000","quote":{"payable_amount":"4000","currency":"CREDIT"}}}}},
		{"name":"explicit rejection","hook":"parseSubmitResponse","args":[{},{"body":{"error":{"code":"no_capacity","message":"busy"}}}],"expected":{"rejected":{"reason":"no_capacity: busy"}}},
		{"name":"ambiguous missing id does not authorize retry","hook":"parseSubmitResponse","args":[{},{"body":{"status":"failed"}}],"expectedError":"no task id"},
		{"name":"numeric id is not silently converted","hook":"parseSubmitResponse","args":[{},{"body":{"id":42}}],"expectedError":"invalid task id"},
		{"name":"failed accepted task settles through immediate failure","hook":"parseSubmitResponse","args":[{},{"body":{"id":"cgt_fixture","status":"failed","error":{"code":"invalid_prompt","message":"invalid prompt"}}}],"expected":{"taskId":"cgt_fixture","taskData":{"id":"cgt_fixture","status":"failed","error":{"code":"invalid_prompt","message":"invalid prompt"}},"immediate":{"status":"FAILURE","reason":"invalid_prompt: invalid prompt"}}},
		{"name":"immediate success retains artifact","hook":"parseSubmitResponse","args":[{},{"body":{"id":"cgt_fixture","status":"completed","url":"https://cdn.example/v.mp4"}}],"expected":{"taskId":"cgt_fixture","taskData":{"id":"cgt_fixture","status":"completed","url":"https://cdn.example/v.mp4"},"immediate":{"status":"SUCCESS","progress":"100%"}}},
		{"name":"query uses persisted upstream id","hook":"buildQueryRequest","args":[{"baseUrl":"https://vobile.example/","apiKey":"fixture-only-key","taskId":"cgt/a"}],"expected":{"url":"https://vobile.example/v1/videos/cgt%2Fa","method":"GET","headers":{"Authorization":"Bearer fixture-only-key"}}},
		{"name":"queued progress","hook":"parseTaskResult","args":[{},{"status":"queued","progress":0}],"expected":{"status":"QUEUED","progress":"0%"}},
		{"name":"archiving remains in progress even with zero progress","hook":"parseTaskResult","args":[{},{"status":"in_progress","progress":0,"vobile":{"stage":"archiving"}}],"expected":{"status":"IN_PROGRESS","progress":"0%"}},
		{"name":"completed","hook":"parseTaskResult","args":[{},{"status":"completed","url":"https://cdn.example/v.mp4"}],"expected":{"status":"SUCCESS","progress":"100%"}},
		{"name":"missing video cannot be charged as success","hook":"parseTaskResult","args":[{},{"status":"completed"}],"expected":{"status":"UNKNOWN","reason":"completed task has no downloadable video URL"}},
		{"name":"unknown state does not run forever","hook":"parseTaskResult","args":[{},{"status":"constructor"}],"expected":{"status":"UNKNOWN","reason":"unrecognized upstream task status"}},
		{"name":"invalid body","hook":"parseTaskResult","args":[{},null],"expected":{"status":"UNKNOWN","reason":"invalid upstream task response"}},
		{"name":"credit cost is not output seconds or host currency","hook":"extractUsageOnComplete","args":[{},{"status":"SUCCESS"},{"seconds":null,"vobile":{"creditCost":"4000.000000","balanceAfter":"18000.000000"}}],"expected":{}},
		{"name":"artifact available","hook":"listArtifacts","args":[{"status":"SUCCESS","data":{"url":"https://cdn.example/v.mp4"}}],"expected":[{"key":"video","type":"video","mimeType":"video/mp4"}]},
		{"name":"unfinished artifact unavailable","hook":"listArtifacts","args":[{"status":"IN_PROGRESS","data":{"url":"https://cdn.example/v.mp4"}}],"expected":[]},
		{"name":"OSS download is credentialless","hook":"buildContentRequest","args":[{"artifactKey":"video","apiKey":"fixture-only-key","data":{"url":"https://cdn.example/v.mp4?fixture=1"},"clientRequest":{"method":"HEAD","headers":{"Authorization":"private-client-key"}}}],"expected":{"url":"https://cdn.example/v.mp4?fixture=1","method":"HEAD","credentialless":true}},
		{"name":"credentials in download URL rejected","hook":"buildContentRequest","args":[{"artifactKey":"video","data":{"url":"https://user:pass@cdn.example/v.mp4"}}],"expectedError":"no downloadable video URL"},
		{"name":"unrecognized artifact","hook":"buildContentRequest","args":[{"artifactKey":"thumbnail","data":{"url":"https://cdn.example/v.mp4"}}],"expectedError":"artifact_not_found"},
		{"name":"presenter excludes account and credit details","hook":"protocols","path":["openai_video","render"],"args":[{},{"status":"SUCCESS","data":{"id":"private-id","size":"1280x720","url":"https://cdn.example/v.mp4","thumbnail_url":"https://cdn.example/a.png","vobile":{"account_id":"private-account","creditCost":"4000","balanceAfter":"18000"}}}],"expected":{"size":"1280x720","url":"https://cdn.example/v.mp4","thumbnail_url":"https://cdn.example/a.png"}},
		{"name":"unsupported mapped model","hook":"describeSpec","args":[{"model":"vobilemax-seedance-2.0","upstreamModel":"wrong-model","requestBody":{"prompt":"cat"}}],"expectedError":"unsupported Vobilemax upstream model"},
		{"name":"multipart files rejected","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","fields":{},"files":[]}}],"expectedError":"file uploads are not supported"},
		{"name":"moderation infrastructure failure remains upstream","hook":"classifyFailure","args":["content_moderation: service unavailable"],"expected":"upstream"}
	]}`))
	require.NoError(t, err)
}

func TestSudashuiVideoPluginLifecycle(t *testing.T) {
	source, err := Source("sudashui")
	require.NoError(t, err)
	fixture, err := os.ReadFile("tasks/sudashui/fixture.json")
	require.NoError(t, err)
	report, err := jsplugin.ReplayFixture(t.Context(), source, fixture)
	require.NoError(t, err)
	assert.Equal(t, report.Total, report.Passed)
}

func TestZonghengVideoPluginLifecycle(t *testing.T) {
	source, err := Source("zongheng")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "zongheng"})
	require.NoError(t, err)

	t.Run("mapped models keep scheduling billing and wire parameters consistent", func(t *testing.T) {
		for _, model := range plugin.Meta.Models {
			t.Run(model, func(t *testing.T) {
				decoded, decodeErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
					"model": "unified-video", "upstreamModel": model,
					"body": map[string]any{"kind": "json", "value": map[string]any{
						"model": "unified-video", "prompt": "海边日出", "seconds": "6", "size": "1280x720", "generate_audio": false,
					}},
				})
				require.NoError(t, decodeErr)
				intent := decoded.(map[string]any)
				assert.Equal(t, "unified-video", intent["model"])
				ctx := map[string]any{"model": "unified-video", "upstreamModel": model, "requestBody": intent["requestBody"]}
				value, specErr := plugin.Engine.Call(t.Context(), "describeSpec", ctx)
				require.NoError(t, specErr)
				got, _, parseErr := spec.Parse(value.(map[string]any))
				require.NoError(t, parseErr)
				usage, usageErr := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
				require.NoError(t, usageErr)
				encodedUsage, marshalErr := common.Marshal(usage)
				require.NoError(t, marshalErr)
				assert.JSONEq(t, `{"seconds":6,"resolution":"720p"}`, string(encodedUsage))
				ctx["publicTaskId"] = "task_stable"
				ctx["baseUrl"] = "https://cnd-coo-new.pages.dev/"
				ctx["authHeader"] = "Bearer fixture-only-key"
				built, buildErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, buildErr)
				request := built.(map[string]any)
				wire := request["body"].(map[string]any)
				assert.Equal(t, "https://cnd-coo-new.pages.dev/v1/videos", request["url"])
				assert.Equal(t, model, wire["model"])
				assert.NotContains(t, request, "model", "descriptor must not replace the pinned alias")
				assert.Equal(t, false, wire["generate_audio"])
				assert.Equal(t, got.Tier, wire["resolution"])
				assert.EqualValues(t, *got.OutputSeconds, wire["duration"])
				assert.NotContains(t, wire, "seconds")
				assert.NotContains(t, wire, "size")
				assert.Equal(t, "task_stable", request["headers"].(map[string]any)["Idempotency-Key"])
				retried, retryErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, retryErr)
				assert.Equal(t, built, retried)
			})
		}
	})

	t.Run("channel overrides cannot bypass validation", func(t *testing.T) {
		for _, tc := range []struct {
			name, field string
			value       any
		}{
			{"zero duration", "duration", 0}, {"negative duration", "duration", -1},
			{"oversized duration", "duration", 3601}, {"wrapped unsigned duration", "duration", "18446744073686646784"},
			{"boolean duration", "duration", true}, {"conflicting seconds", "seconds", 12},
			{"hidden multiplier", "extra", map[string]any{"duration": 999999}},
			{"hidden metadata", "metadata", map[string]any{"duration": 999999}},
			{"multiple outputs", "n", 2}, {"string boolean", "generate_audio", "false"},
			{"conflicting resolution", "size", "1920x1080"}, {"unsupported resolution", "resolution", "2k"},
			{"unauthenticated public media required", "images", []any{"https://user:pass@example.com/a.png"}},
			{"local media rejected", "images", []any{"data:image/png;base64,AA=="}},
			{"HTTPS required", "reference_videos", []any{"http://cdn.example/a.mp4"}},
			{"tail frame requires head", "end_frame", "https://cdn.example/end.png"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body := map[string]any{"prompt": "cat", "duration": 6, "resolution": "720p"}
				body[tc.field] = tc.value
				ctx := map[string]any{"model": "XXseedacn2.5", "requestBody": body, "publicTaskId": "task_test", "baseUrl": "https://cnd-coo-new.pages.dev", "apiKey": "fixture-only-key"}
				for _, hook := range []string{"describeSpec", "extractUsage", "buildSubmitRequest"} {
					_, callErr := plugin.Engine.Call(t.Context(), hook, ctx)
					require.Error(t, callErr, hook)
				}
			})
		}
	})

	// Exercise the real Sobek boundary with host-backed arrays and the documented
	// response envelopes; no live paid generation is needed for these regressions.
	_, err = jsplugin.ReplayFixture(t.Context(), source, []byte(`{"cases":[
		{"name":"multipart false and repeated images","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"model":"XXseedacn2.5","body":{"kind":"multipart","fields":{"model":["XXseedacn2.5"],"prompt":["cat"],"seconds":["6"],"size":["1280x720"],"generate_audio":["false"],"images[]":["https://cdn.example/a.png","https://cdn.example/b.png"]}}}],"expected":{"kind":"submit","model":"XXseedacn2.5","action":"reference_to_video","requestBody":{"prompt":"cat","duration":6,"resolution":"720p","ratio":"16:9","generate_audio":false,"images":["https://cdn.example/a.png","https://cdn.example/b.png"]}}},
		{"name":"local upload guidance","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"body":{"kind":"multipart","files":[{"ref":"request_file:input_reference"}]}}],"expectedError":"/v1/media"},
		{"name":"fractional seconds remain exact","hook":"describeSpec","args":[{"model":"XXseedacn2.5","requestBody":{"prompt":"cat","duration":6.5,"resolution":"720p"}}],"expected":{"spec_version":3,"output_seconds":6.5,"seconds_kind":"exact","resolution":"720p","references":{"image":0,"video":0,"audio":0,"frame":0},"reference_video_urls":[]}},
		{"name":"references include frames and repeated video inputs","hook":"describeSpec","args":[{"model":"XXseedacn2.5","requestBody":{"prompt":"cat","duration":6,"resolution":"720p","images":["https://cdn.example/a.png"],"referenceImages":["https://cdn.example/a.png"],"start_frame":"https://cdn.example/start.png","end_frame":"https://cdn.example/end.png","videos":["https://cdn.example/a.mp4","https://cdn.example/a.mp4"],"audios":["https://cdn.example/a.wav"]}}],"expected":{"spec_version":3,"output_seconds":6,"seconds_kind":"exact","resolution":"720p","references":{"image":1,"video":2,"audio":1,"frame":2},"reference_video_urls":["https://cdn.example/a.mp4","https://cdn.example/a.mp4"]}},
		{"name":"conflicting media mirrors rejected","hook":"describeSpec","args":[{"model":"XXseedacn2.5","requestBody":{"prompt":"cat","duration":6,"resolution":"720p","images":["https://cdn.example/a.png"],"referenceImages":["https://cdn.example/b.png"]}}],"expectedError":"conflicting reference fields"},
		{"name":"mapped Grok rejects reference video","hook":"describeSpec","args":[{"model":"unified-video","upstreamModel":"TTP-grok","requestBody":{"prompt":"cat","duration":6,"resolution":"720p","videos":["https://cdn.example/a.mp4"]}}],"expectedError":"Grok does not support"},
		{"name":"public creation envelope","hook":"parseSubmitResponse","args":[{},{"statusCode":202,"body":{"code":0,"success":true,"task_id":"vid_test","status":"processing","next_poll_seconds":10}}],"expected":{"taskId":"vid_test","taskData":{"code":0,"success":true,"task_id":"vid_test","status":"processing","next_poll_seconds":10}}},
		{"name":"ambiguous acceptance must not resubmit","hook":"parseSubmitResponse","args":[{},{"body":{"error":{"type":"invalid_prompt","message":"rejected"}}}],"expectedError":"no valid task id"},
		{"name":"contradictory acceptance must not resubmit","hook":"parseSubmitResponse","args":[{},{"body":{"success":false,"task_id":"vid_test","status":"processing"}}],"expectedError":"conflicting"},
		{"name":"accepted terminal failure refunds","hook":"parseSubmitResponse","args":[{},{"body":{"task_id":"vid_test","status":"failed","error_code":"invalid_prompt","error_detail":"提示词未通过内容审核"}}],"expected":{"taskId":"vid_test","taskData":{"task_id":"vid_test","status":"failed","error_code":"invalid_prompt","error_detail":"提示词未通过内容审核"},"immediate":{"status":"FAILURE","reason":"invalid_prompt: 提示词未通过内容审核"}}},
		{"name":"query encoded task id using persisted context","hook":"buildQueryRequest","args":[{"taskId":"vid/a?b","baseUrl":"https://cnd-coo-new.pages.dev/","apiKey":"fixture-only-key"}],"expected":{"url":"https://cnd-coo-new.pages.dev/v1/tasks/vid%2Fa%3Fb","method":"GET","headers":{"Authorization":"Bearer fixture-only-key"}}},
		{"name":"processing poll","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"task_id":"vid_test","status":"processing","next_poll_seconds":10}],"expected":{"status":"IN_PROGRESS"}},
		{"name":"successful public poll","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"task_id":"vid_test","status":"succeeded","video_url":"https://cnd-coo-new.pages.dev/api/video-content/vid_test"}],"expected":{"status":"SUCCESS","progress":"100%"}},
		{"name":"unknown status does not become processing","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"task_id":"vid_test","status":"unexpected"}],"expected":{"status":"UNKNOWN","reason":"unrecognized task status or missing video URL"}},
		{"name":"success requires video content","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"task_id":"vid_test","status":"succeeded"}],"expected":{"status":"UNKNOWN","reason":"unrecognized task status or missing video URL"}},
		{"name":"wrong task cannot settle","hook":"parseTaskResult","args":[{"taskId":"vid_test"},{"task_id":"vid_other","status":"succeeded","url":"https://cdn.example/v.mp4"}],"expected":{"status":"UNKNOWN","reason":"upstream task id does not match the queried task"}},
		{"name":"credits cannot replace reserved seconds","hook":"extractUsageOnComplete","args":[{},{"status":"SUCCESS"},{"credits":999999,"duration":-1}],"expected":{}},
		{"name":"credentialless content preserves vendor URL","hook":"buildContentRequest","args":[{"artifactKey":"video","apiKey":"fixture-only-key","data":{"status":"succeeded","video_url":"https://cnd-coo-new.pages.dev/api/video-content/vid_test?sig=original"},"clientRequest":{"method":"HEAD","headers":{"Authorization":"client-secret","Range":"bytes=0-99"}}}],"expected":{"url":"https://cnd-coo-new.pages.dev/api/video-content/vid_test?sig=original","method":"HEAD","credentialless":true}},
		{"name":"non-success cannot expose media","hook":"listArtifacts","args":[{"status":"FAILURE","data":{"status":"processing","video_url":"https://cdn.example/v.mp4"}}],"expected":[]},
		{"name":"video artifact","hook":"listArtifacts","args":[{"status":"SUCCESS","data":{"status":"succeeded","result_url":"https://cdn.example/v.mp4"}}],"expected":[{"key":"video","type":"video","mimeType":"video/mp4"}]},
		{"name":"renderer omits private metadata","hook":"protocols","path":["openai_video","render"],"args":[{},{"status":"SUCCESS","data":{"task_id":"vid_private","status":"succeeded","url":"https://cdn.example/v.mp4","credits":5}}],"expected":{"url":"https://cdn.example/v.mp4"}},
		{"name":"moderation does not penalize channel health","hook":"classifyFailure","args":["invalid_prompt: 提示词未通过内容审核"],"expected":"user"},
		{"name":"quota failure penalizes channel health","hook":"classifyFailure","args":["insufficient balance"],"expected":"upstream"}
	]}`))
	require.NoError(t, err)
}

func TestCangyuanVideoPluginLifecycle(t *testing.T) {
	generation := jsplugin.DefaultRegistry.Generation()
	official, found := generation.LookupEndpoint("POST", "/v1/videos", "doubao-seedance-2-0-260128")
	require.True(t, found)
	assert.Equal(t, "doubao", official.Plugin.Meta.Key, "relay must not replace official token pricing or join its scheduling pool")
	plugin, found := generation.Get("cangyuan")
	require.True(t, found)
	cases := []struct {
		model       string
		seconds     float64
		resolutions []string
	}{
		{"sd10-seedance-2.0", 15, []string{"720p"}},
		{"sd10-seedance-2.0-fast", 15, []string{"720p"}},
		{"sd10-seedance-2.0-mini", 10, []string{"720p"}},
		{"sd10-seedance-2.5", 30, []string{"720p"}},
		{"sd11-seedance-2.0", 15, []string{"480p", "720p", "1080p"}},
		{"sd11-seedance-2.0-fast", 15, []string{"480p", "720p"}},
		{"sd11-seedance-2.0-mini", 15, []string{"480p", "720p"}},
		{"sd11-seedance-2.5", 30, []string{"480p", "720p", "1080p"}},
		{"sd13-seedance-2.0", 15, []string{"480p", "720p", "1080p", "4k"}},
		{"sd13-seedance-2.0-fast", 15, []string{"480p", "720p"}},
		{"sd13-seedance-2.0-mini", 15, []string{"480p", "720p"}},
		{"sd13-seedance-2.5", 30, []string{"480p", "720p"}},
		{"sd14-seedance-2.0", 15, []string{"720p"}},
		{"sd15-seedance-2.0", 15, []string{"480p", "720p"}},
		{"sd15-seedance-2.5", 30, []string{"480p", "720p"}},
		{"sd7-seedance-2.0-1080p", 15, []string{"1080p"}},
		{"sd7-seedance-2.0-720p", 15, []string{"720p"}},
		{"sd8-seedance-2.5", 30, nil},
		{"cangyuan-doubao-seedance-2-0-260128", 15, []string{"720p"}},
		{"cangyuan-doubao-seedance-2-0-fast-260128", 15, []string{"720p"}},
		{"cangyuan-doubao-seedance-2-5-260628", 30, []string{"720p"}},
	}
	require.Len(t, plugin.Meta.Models, len(cases))
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			binding, found := generation.LookupEndpoint("POST", "/v1/videos", tc.model)
			require.True(t, found)
			assert.Equal(t, "cangyuan", binding.Plugin.Meta.Key)
			body := map[string]any{"prompt": "cat", "duration": tc.seconds}
			tier := "*"
			if len(tc.resolutions) > 0 {
				tier = tc.resolutions[len(tc.resolutions)-1]
				body["resolution"] = tier
			}
			driver := map[string]any{"model": "public-alias", "upstreamModel": tc.model, "requestBody": body, "salesSource": "video_request"}
			value, err := plugin.Engine.Call(t.Context(), "describeSpec", driver)
			require.NoError(t, err)
			got, ignored, err := spec.Parse(value.(map[string]any))
			require.NoError(t, err)
			assert.Empty(t, ignored)
			assert.Equal(t, &tc.seconds, got.OutputSeconds)
			assert.Equal(t, tier, got.Tier)
			kind := videosched.KindExact
			if tc.model == "sd10-seedance-2.5" || tc.model == "sd8-seedance-2.5" {
				kind = videosched.KindFixed
			}
			assert.Equal(t, kind, got.SecondsKind)
			value, err = plugin.Engine.Call(t.Context(), "extractUsage", driver)
			require.NoError(t, err)
			facts := value.(map[string]any)
			ratios, err := plugin.Meta.ValidateUsageFacts(facts, tc.model, "public-alias")
			require.NoError(t, err)
			schema, _ := plugin.Meta.UsageForModels(tc.model, "public-alias")
			if strings.HasPrefix(tc.model, "cangyuan-") {
				assert.Empty(t, facts)
				assert.Empty(t, schema, "token-priced models must not inherit per-generation pricing")
				return
			}
			assert.Equal(t, map[string]float64{"requests": 1}, ratios)
			if len(tc.resolutions) > 0 {
				assert.Equal(t, tier, facts["resolution"])
				assert.Equal(t, tc.resolutions, schema["resolution"].Enum)
			} else {
				assert.NotContains(t, facts, "resolution")
				assert.NotContains(t, schema, "resolution")
			}
		})
	}
	source, err := Source("cangyuan")
	require.NoError(t, err)
	// Host-backed JSON maps/arrays and numeric fields must behave identically in
	// Sobek and the standalone JS regressions. No network or database is involved.
	_, err = jsplugin.ReplayFixture(t.Context(), source, []byte(`{"cases":[
		{"name":"mapped public alias preserves SD10 references","hook":"buildSubmitRequest","args":[{"model":"unified-seedance","upstreamModel":"sd10-seedance-2.0","baseUrl":"https://ai.cangyuansuanli.cn/","authHeader":"Bearer fixture-only-key","requestBody":{"prompt":"cat","seconds":10,"size":"1280x720","images":["https://cdn.example/i.png"],"videos":["https://cdn.example/v.mp4"],"audios":["https://cdn.example/a.wav"]}}],"expected":{"url":"https://ai.cangyuansuanli.cn/v1/videos","method":"POST","headers":{"Authorization":"Bearer fixture-only-key","Content-Type":"application/json"},"body":{"model":"sd10-seedance-2.0","prompt":"cat","duration":10,"resolution":"720p","aspect_ratio":"16:9","reference_image_urls":["https://cdn.example/i.png"],"reference_videos":["https://cdn.example/v.mp4"],"reference_audios":["https://cdn.example/a.wav"]}}},
		{"name":"SD10 billing remains per generation","hook":"extractUsage","args":[{"model":"sd10-seedance-2.0","requestBody":{"prompt":"cat","duration":15}}],"expected":{"requests":1,"resolution":"720p"}},
		{"name":"official relay requires host-owned sale","hook":"buildSubmitRequest","args":[{"model":"cangyuan-doubao-seedance-2-0-260128","requestBody":{"prompt":"cat","duration":6}}],"expectedError":"requires host-configured unified video sales"},
		{"name":"official alias is removed only on the wire","hook":"buildSubmitRequest","args":[{"model":"unified-seedance","upstreamModel":"cangyuan-doubao-seedance-2-0-260128","salesSource":"video_request","baseUrl":"https://ai.cangyuansuanli.cn","apiKey":"fixture-only-key","requestBody":{"prompt":"cat","duration":6,"generate_audio":false,"seed":0}}],"expected":{"url":"https://ai.cangyuansuanli.cn/v1/videos","method":"POST","headers":{"Authorization":"Bearer fixture-only-key","Content-Type":"application/json"},"body":{"model":"doubao-seedance-2-0-260128","prompt":"cat","duration":6,"resolution":"720p","aspect_ratio":"16:9","generate_audio":false,"seed":0}}},
		{"name":"multipart arrays retain zero and false","hook":"protocols","path":["openai_video","decodeRequest"],"args":[{"model":"cangyuan-doubao-seedance-2-0-260128","body":{"kind":"multipart","fields":{"model":["cangyuan-doubao-seedance-2-0-260128"],"prompt":["cat"],"seconds":["6"],"seed":["0"],"generate_audio":["false"],"reference_image_urls[]":["https://cdn.example/a.png","https://cdn.example/b.png"]}}}],"expected":{"kind":"submit","model":"cangyuan-doubao-seedance-2-0-260128","action":"reference_to_video","requestBody":{"prompt":"cat","duration":6,"resolution":"720p","aspect_ratio":"16:9","reference_image_urls":["https://cdn.example/a.png","https://cdn.example/b.png"],"generate_audio":false,"seed":0}}},
		{"name":"asset videos cannot become partial duration sources","hook":"describeSpec","args":[{"model":"sd10-seedance-2.0","requestBody":{"prompt":"cat","duration":10,"reference_videos":["https://cdn.example/v.mp4","asset://asset-video"]}}],"expected":{"spec_version":3,"output_seconds":10,"seconds_kind":"exact","resolution":"720p","references":{"image":0,"video":2,"audio":0,"frame":0}}},
		{"name":"overrides cannot bypass duration bounds","hook":"describeSpec","args":[{"model":"sd10-seedance-2.0","requestBody":{"prompt":"cat","duration":"18446744073686646784"}}],"expectedError":"duration must be an integer from 1 to 3600 seconds"},
		{"name":"hidden multipliers are rejected","hook":"buildSubmitRequest","args":[{"model":"sd10-seedance-2.0","requestBody":{"prompt":"cat","duration":10,"metadata":{"duration":9999}}}],"expectedError":"unsupported video parameter: metadata"},
		{"name":"accept creation with id","hook":"parseSubmitResponse","args":[{},{"statusCode":202,"body":{"id":"video_42","status":"queued"}}],"expected":{"taskId":"video_42","taskData":{"id":"video_42","status":"queued"}}},
		{"name":"accepted terminal failure refunds","hook":"parseSubmitResponse","args":[{},{"body":{"id":"video_42","status":"failed","error":{"message":"content moderation rejected the prompt"}}}],"expected":{"taskId":"video_42","taskData":{"id":"video_42","status":"failed","error":{"message":"content moderation rejected the prompt"}},"immediate":{"status":"FAILURE","reason":"content moderation rejected the prompt"}}},
		{"name":"ambiguous acceptance cannot trigger retry","hook":"parseSubmitResponse","args":[{},{"body":{"id":"video_42","error":"quota exhausted"}}],"expectedError":"conflicting acceptance and rejection"},
		{"name":"query uses persisted id","hook":"buildQueryRequest","args":[{"taskId":"video/a?b","baseUrl":"https://ai.cangyuansuanli.cn","apiKey":"fixture-only-key"}],"expected":{"url":"https://ai.cangyuansuanli.cn/v1/videos/video%2Fa%3Fb","method":"GET","headers":{"Authorization":"Bearer fixture-only-key"}}},
		{"name":"completion supports documented array result","hook":"parseTaskResult","args":[{"taskId":"video_42"},{"id":"video_42","status":"completed","data":[{"url":"https://cdn.example/v.mp4"}]}],"expected":{"status":"SUCCESS"}},
		{"name":"unknown status stays unknown","hook":"parseTaskResult","args":[{}, {"status":"unexpected"}],"expected":{"status":"UNKNOWN","reason":"unrecognized Cangyuan task status"}},
		{"name":"success without video cannot settle","hook":"parseTaskResult","args":[{}, {"status":"completed"}],"expected":{"status":"UNKNOWN","reason":"completed task has no valid video URL"}},
		{"name":"download excludes all credentials","hook":"buildContentRequest","args":[{"artifactKey":"video","apiKey":"fixture-only-key","data":{"video_url":"https://cdn.example/v.mp4?sig=original%2Bvalue"},"clientRequest":{"method":"HEAD","headers":{"Authorization":"client-secret"}}}],"expected":{"url":"https://cdn.example/v.mp4?sig=original%2Bvalue","method":"HEAD","credentialless":true}},
		{"name":"upstream cost never changes usage","hook":"extractUsageOnComplete","args":[{}, {"status":"SUCCESS"},{"usage":{"cost":9999},"duration":0}],"expected":{}}
	]}`))
	require.NoError(t, err)
}

func TestBuiltInPluginsAddressNewAPIUpstreamOnNativeRoutes(t *testing.T) {
	generation := jsplugin.DefaultRegistry.Generation()
	require.NotNil(t, generation)
	for _, key := range []string{"hailuo", "google", "vidu", "vertex-ai"} {
		plugin, found := generation.Get(key)
		require.True(t, found, key)
		assert.False(t, plugin.Meta.SupportsUpstream(jsplugin.UpstreamKindNewAPI), "%s has no native routes and must not be bindable to a New API channel", key)
	}

	const base = "https://upstream.example"
	cases := []struct {
		key                string
		driver             map[string]any
		vendorKey          string
		vendorAuthPrefix   string
		vendorSubmit       string
		gatewaySubmit      string
		vendorQuery        string
		gatewayQuery       string
		content            string
		legacyKeyHeuristic bool
	}{
		{
			key:              "doubao",
			driver:           map[string]any{"action": "text_to_video", "model": "doubao-seedance-1-0-pro-250528", "upstreamModel": "doubao-seedance-1-0-pro-250528", "requestBody": map[string]any{"model": "doubao-seedance-1-0-pro-250528", "prompt": "a cat"}},
			vendorKey:        "vendor-key",
			vendorAuthPrefix: "Bearer vendor-key",
			vendorSubmit:     "/api/v3/contents/generations/tasks",
			gatewaySubmit:    "/doubao/api/v3/contents/generations/tasks",
			vendorQuery:      "/api/v3/contents/generations/tasks/tid",
			gatewayQuery:     "/doubao/api/v3/contents/generations/tasks/tid",
		},
		{
			key:              "alibaba",
			driver:           map[string]any{"action": "text_to_video", "model": "wan2.2-t2v-plus", "upstreamModel": "wan2.2-t2v-plus", "requestBody": map[string]any{"model": "wan2.2-t2v-plus", "prompt": "a cat"}},
			vendorKey:        "vendor-key",
			vendorAuthPrefix: "Bearer vendor-key",
			vendorSubmit:     "/api/v1/services/aigc/video-generation/video-synthesis",
			gatewaySubmit:    "/ali/api/v1/services/aigc/video-generation/video-synthesis",
			vendorQuery:      "/api/v1/tasks/tid",
			gatewayQuery:     "/ali/api/v1/tasks/tid",
		},
		{
			key:                "kling",
			driver:             map[string]any{"action": "text_to_video", "model": "kling-v1", "upstreamModel": "kling-v1", "requestBody": map[string]any{"prompt": "a cat"}},
			vendorKey:          "ak|sk",
			vendorAuthPrefix:   "Bearer ey",
			vendorSubmit:       "/v1/videos/text2video",
			gatewaySubmit:      "/kling/v1/videos/text2video",
			vendorQuery:        "/v1/videos/text2video/tid",
			gatewayQuery:       "/kling/v1/videos/text2video/tid",
			legacyKeyHeuristic: true,
		},
		{
			key:                "jimeng",
			driver:             map[string]any{"action": "text_to_video", "model": "jimeng_vgfm_t2v_l20", "upstreamModel": "jimeng_vgfm_t2v_l20", "requestBody": map[string]any{"prompt": "a cat"}},
			vendorKey:          "ak|sk",
			vendorAuthPrefix:   "HMAC-SHA256",
			vendorSubmit:       "/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31",
			gatewaySubmit:      "/jimeng/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31",
			vendorQuery:        "/?Action=CVSync2AsyncGetResult&Version=2022-08-31",
			gatewayQuery:       "/jimeng/?Action=CVSync2AsyncGetResult&Version=2022-08-31",
			legacyKeyHeuristic: true,
		},
		{
			key:              "sunoapi",
			driver:           map[string]any{"action": "MUSIC", "model": "suno_music", "upstreamModel": "suno_music", "requestBody": map[string]any{"prompt": "a song"}},
			vendorKey:        "vendor-key",
			vendorAuthPrefix: "Bearer vendor-key",
			vendorSubmit:     "/suno/submit/MUSIC",
			gatewaySubmit:    "/suno/submit/MUSIC",
			vendorQuery:      "/suno/fetch",
			gatewayQuery:     "/suno/fetch",
		},
		{
			key:              "sora",
			driver:           map[string]any{"action": "text_to_video", "model": "sora-2", "upstreamModel": "sora-2", "requestBody": map[string]any{"prompt": "a cat"}},
			vendorKey:        "vendor-key",
			vendorAuthPrefix: "Bearer vendor-key",
			vendorSubmit:     "/v1/videos",
			gatewaySubmit:    "/v1/videos",
			vendorQuery:      "/v1/videos/tid",
			gatewayQuery:     "/v1/videos/tid",
			content:          "/v1/videos/tid/content",
		},
	}
	variants := []struct {
		name      string
		upstream  map[string]any
		gateway   bool
		legacyKey bool
	}{
		{"vendor signal", map[string]any{"kind": "vendor"}, false, false},
		{"no signal from an older host", nil, false, false},
		{"new_api signal", map[string]any{"kind": "new_api"}, true, false},
		{"legacy sk- key without a signal", nil, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			plugin, found := generation.Get(tc.key)
			require.True(t, found)
			require.True(t, plugin.Meta.SupportsUpstream(jsplugin.UpstreamKindNewAPI), "%s must declare new_api to be bindable to a New API channel", tc.key)
			call := func(hook string, args ...any) map[string]any {
				value, err := plugin.Engine.Call(t.Context(), hook, args...)
				require.NoError(t, err, hook)
				encoded, err := common.Marshal(value)
				require.NoError(t, err)
				var descriptor map[string]any
				require.NoError(t, common.Unmarshal(encoded, &descriptor))
				return descriptor
			}
			for _, variant := range variants {
				if variant.legacyKey && !tc.legacyKeyHeuristic {
					continue
				}
				t.Run(variant.name, func(t *testing.T) {
					apiKey := tc.vendorKey
					if variant.legacyKey {
						apiKey = "sk-legacy"
					} else if variant.gateway {
						apiKey = "sk-gateway"
					}
					authHeader := apiKey
					if variant.gateway && !variant.legacyKey {
						authHeader = "Bearer " + apiKey
					}
					ctx := map[string]any{"baseUrl": base, "apiKey": apiKey, "authHeader": authHeader, "publicTaskId": "task_pub", "requestHeaders": map[string]any{}, "files": []any{}}
					for key, value := range tc.driver {
						ctx[key] = value
					}
					if variant.upstream != nil {
						ctx["upstream"] = variant.upstream
					}
					wantSubmit, wantQuery := tc.vendorSubmit, tc.vendorQuery
					if variant.gateway {
						wantSubmit, wantQuery = tc.gatewaySubmit, tc.gatewayQuery
					}

					submit := call("buildSubmitRequest", ctx)
					assert.Equal(t, base+wantSubmit, submit["url"])
					headers, _ := submit["headers"].(map[string]any)
					authorization, _ := headers["Authorization"].(string)
					if variant.gateway {
						assert.Equal(t, "Bearer "+apiKey, authorization, "a gateway receives its own token as a Bearer header")
					} else {
						assert.True(t, strings.HasPrefix(authorization, tc.vendorAuthPrefix), "vendor Authorization %q", authorization)
					}

					queryCtx := map[string]any{"taskId": "tid", "publicTaskId": "task_pub", "action": tc.driver["action"], "model": tc.driver["model"], "upstreamModel": tc.driver["upstreamModel"], "baseUrl": base, "apiKey": apiKey, "authHeader": authHeader, "data": nil, "state": nil}
					if variant.upstream != nil {
						queryCtx["upstream"] = variant.upstream
					}
					var query map[string]any
					if plugin.Meta.FetchMode == "batch" {
						query = call("buildBatchQueryRequest", queryCtx, []any{queryCtx})
					} else {
						query = call("buildQueryRequest", queryCtx)
					}
					assert.Equal(t, base+wantQuery, query["url"])

					if tc.content == "" {
						return
					}
					contentCtx := map[string]any{"baseUrl": base, "apiKey": apiKey, "authHeader": authHeader, "artifactKey": "video", "upstreamTaskId": "tid", "data": map[string]any{}, "clientRequest": map[string]any{"method": "GET", "headers": map[string]any{}}}
					if variant.upstream != nil {
						contentCtx["upstream"] = variant.upstream
					}
					assert.Equal(t, base+tc.content, call("buildContentRequest", contentCtx)["url"])
				})
			}
		})
	}

	t.Run("alibaba asks only DashScope for an interleaved image stream", func(t *testing.T) {
		plugin, found := generation.Get("alibaba")
		require.True(t, found)
		for _, variant := range []struct {
			name             string
			upstream         map[string]any
			wantResponseType string
			wantSSEHeader    bool
		}{
			{"vendor", map[string]any{"kind": "vendor"}, "sse", true},
			{"new_api", map[string]any{"kind": "new_api"}, "json", false},
		} {
			t.Run(variant.name, func(t *testing.T) {
				value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
					"baseUrl": base, "apiKey": "key", "authHeader": "key", "publicTaskId": "task_pub", "upstream": variant.upstream,
					"model": "wan2.6-image", "upstreamModel": "wan2.6-image",
					"requestBody": map[string]any{"model": "wan2.6-image", "prompt": "a cat", "metadata": map[string]any{"upstream_mode": "sync", "parameters": map[string]any{"enable_interleave": true}}},
				})
				require.NoError(t, err)
				encoded, err := common.Marshal(value)
				require.NoError(t, err)
				var descriptor struct {
					URL          string            `json:"url"`
					ResponseType string            `json:"responseType"`
					Headers      map[string]string `json:"headers"`
				}
				require.NoError(t, common.Unmarshal(encoded, &descriptor))
				assert.Equal(t, variant.wantResponseType, descriptor.ResponseType, "a gateway native route aggregates the vendor stream and answers with JSON")
				_, hasSSEHeader := descriptor.Headers["X-DashScope-Sse"]
				assert.Equal(t, variant.wantSSEHeader, hasSSEHeader)
			})
		}
	})
}
