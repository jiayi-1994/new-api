package plugins

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/pkg/videosched/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expectedKeys = []string{"aggc", "alibaba", "bytefor", "cangyuan", "doubao", "google", "hailuo", "jimeng", "kling", "meaicc", "megabyai", "paipu", "pidoi", "seedance-hjmie", "sora", "sudashui", "sunoapi", "vertex-ai", "vidu", "zongheng"}

// responsesKeys are the vendor plugins that also serve OpenAI Responses.
var responsesKeys = []string{"alibaba", "doubao", "google", "hailuo", "jimeng", "kling", "sora", "sunoapi", "vertex-ai", "vidu"}

// videoOnlyKeys are third-party relays that only speak the OpenAI video protocol.
var videoOnlyKeys = []string{"aggc", "bytefor", "cangyuan", "meaicc", "megabyai", "paipu", "pidoi", "seedance-hjmie", "sudashui", "zongheng"}

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
			videosched.Spec{Tier: "1080p", References: map[string]int{"video": 1, "image": 2, "audio": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"bytefor", "bytefor-2.5", map[string]any{"prompt": "cat", "seconds": 30, "resolution": "720p", "images": images, "videos": video, "audios": []any{"https://cdn.example/1.mp3"}}, 30,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 2, "audio": 1}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"cangyuan", "sd10-seedance-2.0", map[string]any{"prompt": "cat", "seconds": 10, "resolution": "720p", "images": images, "videos": video, "audios": []any{"https://cdn.example/1.wav"}}, 10,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 2, "audio": 1}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"cangyuan", "cangyuan-doubao-seedance-2-0-260128", map[string]any{"prompt": "cat", "seconds": 6, "resolution": "720p", "first_image_url": "https://cdn.example/first.png", "last_image_url": "asset://asset-last"}, 6,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 0, "image": 2, "audio": 0}}},
		{"meaicc", "w3-c1", map[string]any{"input": map[string]any{"prompt": "cat", "media": []any{
			map[string]any{"type": "first_frame", "url": "https://cdn.example/f.png"},
			map[string]any{"type": "reference_video", "url": "https://cdn.example/v.mp4"},
		}}, "parameters": map[string]any{"duration": 12, "resolution": "1080p"}}, 12,
			videosched.Spec{Tier: "1080p", References: map[string]int{"video": 1, "image": 1, "audio": 0}, ReferenceVideoURLs: []string{"https://cdn.example/v.mp4"}}},
		{"megabyai", "videos-fast", map[string]any{"prompt": "cat", "seconds": "8", "size": "1280x720", "referenceImages": images}, 8,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 0, "image": 2, "audio": 0}}},
		{"seedance-hjmie", "videos-fast", map[string]any{"prompt": "cat", "duration": 8, "resolution": "4k", "videos": video}, 8,
			videosched.Spec{Tier: "4k", References: map[string]int{"video": 1, "image": 0, "audio": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"sudashui", "sdas-mg-sd2.5-720p", map[string]any{"prompt": "cat", "duration": 30, "resolution": "720p", "images": images, "videos": video}, 30,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 2, "audio": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
		{"paipu", "lec-seed-2-0-900", map[string]any{"prompt": "cat", "images": images}, 15,
			videosched.Spec{Tier: "*", SecondsKind: videosched.KindFixed, References: map[string]int{"video": 0, "image": 2, "audio": 0}}},
		{"pidoi", "tejiasd-mini-720p", map[string]any{"prompt": "cat", "seconds": 8, "resolution": "480p", "images": images}, 8,
			videosched.Spec{Tier: "480p", References: map[string]int{"video": 0, "image": 2, "audio": 0}}},
		{"zongheng", "XXseedacn2.5", map[string]any{"prompt": "cat", "seconds": 10, "size": "1280x720", "images": images, "videos": video, "start_frame": "https://cdn.example/start.png", "end_frame": "https://cdn.example/end.png"}, 10,
			videosched.Spec{Tier: "720p", References: map[string]int{"video": 1, "image": 4, "audio": 0}, ReferenceVideoURLs: []string{"https://cdn.example/1.mp4"}}},
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

// Replay through Sobek as well as the JS tests: HTTP JSON numbers, immutable
// host-backed input maps and nested result envelopes must cross the runtime intact.
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
		{"name":"fractional seconds remain exact","hook":"describeSpec","args":[{"model":"XXseedacn2.5","requestBody":{"prompt":"cat","duration":6.5,"resolution":"720p"}}],"expected":{"spec_version":2,"output_seconds":6.5,"seconds_kind":"exact","resolution":"720p","references":{"image":0,"video":0,"audio":0},"reference_video_urls":[]}},
		{"name":"references include frames and repeated video inputs","hook":"describeSpec","args":[{"model":"XXseedacn2.5","requestBody":{"prompt":"cat","duration":6,"resolution":"720p","images":["https://cdn.example/a.png"],"referenceImages":["https://cdn.example/a.png"],"start_frame":"https://cdn.example/start.png","end_frame":"https://cdn.example/end.png","videos":["https://cdn.example/a.mp4","https://cdn.example/a.mp4"],"audios":["https://cdn.example/a.wav"]}}],"expected":{"spec_version":2,"output_seconds":6,"seconds_kind":"exact","resolution":"720p","references":{"image":3,"video":2,"audio":1},"reference_video_urls":["https://cdn.example/a.mp4","https://cdn.example/a.mp4"]}},
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
		{"name":"asset videos cannot become partial duration sources","hook":"describeSpec","args":[{"model":"sd10-seedance-2.0","requestBody":{"prompt":"cat","duration":10,"reference_videos":["https://cdn.example/v.mp4","asset://asset-video"]}}],"expected":{"spec_version":2,"output_seconds":10,"seconds_kind":"exact","resolution":"720p","references":{"image":0,"video":2,"audio":0}}},
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
