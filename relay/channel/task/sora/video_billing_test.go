package sora

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func soraVideoBillingContext(t *testing.T, request relaycommon.TaskSubmitReq) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := common.Marshal(request)
	require.NoError(t, err)
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(string(body)))
	httpRequest.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httpRequest
	c.Set("task_request", request)
	info := &relaycommon.RelayInfo{
		OriginModelName: request.Model,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: request.Model},
	}
	return c, info
}

func TestSoraResolveVideoBillingDefaultsTo720pAndFourSeconds(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Model: "sora-2", Prompt: "animate"})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	require.Nil(t, taskErr)
	assert.Equal(t, "720p", selection.EffectiveResolution)
	assert.Equal(t, 4, selection.EffectiveDurationSeconds)
}

func TestSoraResolveVideoBillingMapsHighDimensionsTo1024p(t *testing.T) {
	for _, size := range []string{"1024x1792", "1792x1024"} {
		t.Run(size, func(t *testing.T) {
			c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
				Model:   "sora-2-pro",
				Prompt:  "animate",
				Size:    size,
				Seconds: "8",
			})

			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

			require.Nil(t, taskErr)
			assert.Equal(t, "1024p", selection.EffectiveResolution)
			assert.Equal(t, 8, selection.EffectiveDurationSeconds)
		})
	}
}

func TestSoraResolveVideoBillingRejectsUnsupportedDimensions(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
		Model:   "sora-2-pro",
		Prompt:  "animate",
		Size:    "1920x1080",
		Seconds: "8",
	})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
}

func TestSoraResolveVideoBillingUsesMappedUpstreamModelRules(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
		Model:   "client-model",
		Prompt:  "animate",
		Size:    "1792x1024",
		Seconds: "8",
	})
	info.UpstreamModelName = "sora-2"

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Contains(t, taskErr.Message, "1024p")
	assert.NotContains(t, taskErr.Message, "unknown")
}

func TestSoraResolveVideoBillingRejectsDurationAboveBound(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
		Model:    "sora-2",
		Prompt:   "animate",
		Size:     "720x1280",
		Duration: relaycommon.MaxTaskDurationSeconds + 1,
	})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
}

func TestSoraBuildRequestBodyUsesResolvedBillingPayload(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Model: "client-model", Prompt: "animate"})
	info.UpstreamModelName = "sora-2-pro"
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(payload, &decoded))

	assert.Equal(t, "720p", selection.EffectiveResolution)
	assert.Equal(t, 4, selection.EffectiveDurationSeconds)
	assert.Equal(t, "sora-2-pro", decoded["model"])
	assert.Equal(t, "720x1280", decoded["size"])
	assert.Equal(t, "4", decoded["seconds"])
	assert.NotContains(t, decoded, "duration")
	assert.Equal(t, selection.EffectiveResolution, decoded["resolution"])
}

func TestMegabyaiResolveAndBuildRequestBodyPreserveExplicitResolution(t *testing.T) {
	for _, resolution := range []string{"480p", "720p", "864p", "1080p", "2160p"} {
		t.Run(resolution, func(t *testing.T) {
			c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
				Model:      "videos-mini",
				Prompt:     "animate",
				Resolution: resolution,
				Duration:   6,
			})
			adaptor := &TaskAdaptor{baseURL: "https://megabyai.cc"}

			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			assert.Equal(t, resolution, selection.EffectiveResolution)
			assert.Equal(t, 6, selection.EffectiveDurationSeconds)

			body, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			payload, err := io.ReadAll(body)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, common.Unmarshal(payload, &decoded))

			assert.Equal(t, "videos-mini", decoded["model"])
			assert.Equal(t, resolution, decoded["resolution"])
			assert.Equal(t, float64(6), decoded["duration"])
		})
	}
}

func TestSoraOpenAIResolveAndBuildRequestBodyPreserveExplicitResolution(t *testing.T) {
	for _, resolution := range []string{"480p", "720p", "1080p", "2160p"} {
		t.Run(resolution, func(t *testing.T) {
			c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
				Model:      "videos-mini",
				Prompt:     "animate",
				Resolution: resolution,
				Duration:   6,
			})
			adaptor := &TaskAdaptor{baseURL: "https://videos.example.com"}

			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			body, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			payload, err := io.ReadAll(body)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, common.Unmarshal(payload, &decoded))

			assert.Equal(t, resolution, selection.EffectiveResolution)
			assert.Equal(t, 6, selection.EffectiveDurationSeconds)
			assert.Equal(t, selection.EffectiveResolution, decoded["resolution"])
			assert.NotContains(t, decoded, "size")
		})
	}
}

func TestSoraMultipartBuildRequestBodyPreservesResolvedBillingSelection(t *testing.T) {
	for _, channel := range []struct {
		name    string
		baseURL string
	}{
		{name: "openai", baseURL: "https://videos.example.com"},
		{name: "megabyai", baseURL: "https://megabyai.cc"},
	} {
		for _, resolution := range []string{"1080p", "2160p"} {
			t.Run(channel.name+"/"+resolution, func(t *testing.T) {
				var requestBody bytes.Buffer
				writer := multipart.NewWriter(&requestBody)
				require.NoError(t, writer.WriteField("model", "videos-mini"))
				require.NoError(t, writer.WriteField("prompt", "animate"))
				require.NoError(t, writer.WriteField("resolution", resolution))
				require.NoError(t, writer.WriteField("duration", "6"))
				require.NoError(t, writer.Close())

				request := httptest.NewRequest(http.MethodPost, "/v1/videos", &requestBody)
				request.Header.Set("Content-Type", writer.FormDataContentType())
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = request
				c.Set("task_request", relaycommon.TaskSubmitReq{
					Model:      "videos-mini",
					Prompt:     "animate",
					Resolution: resolution,
					Duration:   6,
				})
				info := &relaycommon.RelayInfo{
					OriginModelName: "videos-mini",
					TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
					ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "videos-mini"},
				}
				adaptor := &TaskAdaptor{baseURL: channel.baseURL}

				selection, taskErr := adaptor.ResolveVideoBilling(c, info)
				require.Nil(t, taskErr)
				body, err := adaptor.BuildRequestBody(c, info)
				require.NoError(t, err)
				upstreamRequest := httptest.NewRequest(http.MethodPost, "/v1/videos", body)
				upstreamRequest.Header.Set("Content-Type", c.GetHeader("Content-Type"))
				require.NoError(t, upstreamRequest.ParseMultipartForm(32<<20))

				assert.Equal(t, resolution, selection.EffectiveResolution)
				assert.Equal(t, 6, selection.EffectiveDurationSeconds)
				assert.Equal(t, selection.EffectiveResolution, upstreamRequest.FormValue("resolution"))
				assert.Equal(t, "6", upstreamRequest.FormValue("seconds"))
				assert.Empty(t, upstreamRequest.FormValue("size"))
			})
		}
	}
}

func TestSoraAcceptsCanonicalPrewrappedDashScopeBillingParameters(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Model: "client-model", Prompt: "animate"})
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(`{"model":"client-model","prompt":"animate","input":{"prompt":"wrapped"},"parameters":{"resolution":"1080P","duration":6}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	info.UpstreamModelName = "sora-2-pro"
	info.ChannelSetting.VideoPayloadFormat = "dashscope"
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(payload, &decoded))
	parameters, ok := decoded["parameters"].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "1080p", selection.EffectiveResolution)
	assert.Equal(t, 6, selection.EffectiveDurationSeconds)
	assert.Equal(t, "1080P", parameters["resolution"])
	assert.Equal(t, float64(6), parameters["duration"])
}

func TestSoraDashScopeNestedParametersSelect1024pAndEightSeconds(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Model: "client-model", Prompt: "animate"})
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(`{"model":"client-model","prompt":"animate","input":{"prompt":"wrapped","negative_prompt":"blur"},"parameters":{"resolution":"1024P","duration":8,"seed":42}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	info.UpstreamModelName = "sora-2-pro"
	info.ChannelSetting.VideoPayloadFormat = "dashscope"
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(payload, &decoded))
	parameters, ok := decoded["parameters"].(map[string]any)
	require.True(t, ok)
	input, ok := decoded["input"].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "1024p", selection.EffectiveResolution)
	assert.Equal(t, 8, selection.EffectiveDurationSeconds)
	assert.Equal(t, "1024P", parameters["resolution"])
	assert.Equal(t, float64(8), parameters["duration"])
	assert.Equal(t, float64(42), parameters["seed"])
	assert.Equal(t, "wrapped", input["prompt"])
	assert.Equal(t, "blur", input["negative_prompt"])
	assert.NotContains(t, decoded, "size")
	assert.Equal(t, "8", decoded["seconds"])
}

func TestSoraDashScopePreservesPrewrappedInputWithoutNestedPrompt(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Model: "client-model", Prompt: "animate"})
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(`{"model":"client-model","prompt":"animate","input":{"negative_prompt":"blur","media":[{"type":"reference_image","url":"https://example.test/a.png"}]},"parameters":{"resolution":"720P","duration":4,"seed":42}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	info.UpstreamModelName = "sora-2-pro"
	info.ChannelSetting.VideoPayloadFormat = "dashscope"
	adaptor := &TaskAdaptor{}

	_, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(payload, &decoded))
	input, ok := decoded["input"].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "animate", input["prompt"])
	assert.Equal(t, "blur", input["negative_prompt"])
	media, ok := input["media"].([]any)
	require.True(t, ok)
	require.Len(t, media, 1)
	assert.Equal(t, "https://example.test/a.png", media[0].(map[string]any)["url"])
}

func TestSoraDashScopeEquivalentTopLevelAndNestedParametersAreMerged(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Model:      "sora-2-pro",
		Prompt:     "animate",
		Size:       "1792x1024",
		Resolution: "1024p",
		Seconds:    "8",
		Duration:   8,
	}
	c, info := soraVideoBillingContext(t, req)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(`{"model":"sora-2-pro","prompt":"animate","size":"1792x1024","resolution":"1024p","seconds":"8","duration":8,"input":{"prompt":"wrapped"},"parameters":{"resolution":"1024P","duration":8,"seed":42}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	info.ChannelSetting.VideoPayloadFormat = "dashscope"
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(payload, &decoded))
	parameters := decoded["parameters"].(map[string]any)

	assert.Equal(t, "1024p", selection.EffectiveResolution)
	assert.Equal(t, 8, selection.EffectiveDurationSeconds)
	assert.Equal(t, float64(42), parameters["seed"])
}

func TestSoraDashScopeRejectsConflictingTopLevelAndNestedResolution(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
		Model:   "sora-2-pro",
		Prompt:  "animate",
		Size:    "720x1280",
		Seconds: "8",
	})
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(`{"model":"sora-2-pro","prompt":"animate","size":"720x1280","seconds":"8","input":{"prompt":"wrapped"},"parameters":{"resolution":"1024P","duration":8}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	info.ChannelSetting.VideoPayloadFormat = "dashscope"

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
}

func TestSoraDashScopeRejectsConflictingTopLevelAndNestedDuration(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
		Model:   "sora-2-pro",
		Prompt:  "animate",
		Size:    "1024x1792",
		Seconds: "8",
	})
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(`{"model":"sora-2-pro","prompt":"animate","size":"1024x1792","seconds":"8","input":{"prompt":"wrapped"},"parameters":{"resolution":"1024P","duration":7}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	info.ChannelSetting.VideoPayloadFormat = "dashscope"

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
}

func TestSoraDashScopeRejectsOversizedNestedDuration(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Model: "sora-2-pro", Prompt: "animate"})
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		strings.NewReader(`{"model":"sora-2-pro","prompt":"animate","input":{"prompt":"wrapped"},"parameters":{"resolution":"1024P","duration":3601,"seed":42}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	info.ChannelSetting.VideoPayloadFormat = "dashscope"

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
}

func TestSoraRemixVideoBillingRestoresSavedSelection(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Prompt: "remix"})
	info.Action = constant.TaskActionRemix
	c.Set("origin_task", &model.Task{
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
			PricingKind:              "video_resolution",
			EffectiveResolution:      "1024p",
			EffectiveDurationSeconds: 12,
		}},
		Data: []byte(`{"size":"720x1280","seconds":"4"}`),
	})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	require.Nil(t, taskErr)
	assert.Equal(t, "1024p", selection.EffectiveResolution)
	assert.Equal(t, 12, selection.EffectiveDurationSeconds)
}

func TestSoraRemixVideoBillingRestoresCanonicalSavedResolution(t *testing.T) {
	for _, channel := range []struct {
		name    string
		baseURL string
	}{
		{name: "openai", baseURL: "https://videos.example.com"},
		{name: "megabyai", baseURL: "https://megabyai.cc"},
	} {
		for _, resolution := range []string{"1080p", "2160p"} {
			t.Run(channel.name+"/"+resolution, func(t *testing.T) {
				c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Prompt: "remix"})
				info.Action = constant.TaskActionRemix
				c.Set("origin_task", &model.Task{
					PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
						PricingKind:              "video_resolution",
						EffectiveResolution:      resolution,
						EffectiveDurationSeconds: 12,
					}},
					Data: []byte(`{"size":"720x1280","seconds":"4"}`),
				})
				adaptor := &TaskAdaptor{baseURL: channel.baseURL}

				selection, taskErr := adaptor.ResolveVideoBilling(c, info)

				require.Nil(t, taskErr)
				assert.Equal(t, resolution, selection.EffectiveResolution)
				assert.Equal(t, 12, selection.EffectiveDurationSeconds)
			})
		}
	}
}

func TestSoraRemixVideoBillingRejectsInvalidSavedSelectionWithoutLegacyFallback(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Prompt: "remix"})
	info.Action = constant.TaskActionRemix
	c.Set("origin_task", &model.Task{
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
			PricingKind:              "video_resolution",
			EffectiveResolution:      "1080p",
			EffectiveDurationSeconds: relaycommon.MaxTaskDurationSeconds + 1,
		}},
		Data: []byte(`{"size":"720x1280","seconds":"4"}`),
	})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Contains(t, taskErr.Message, "1080p")
}

func TestSoraRemixVideoBillingRecovers720pFromLegacyTaskData(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Prompt: "remix"})
	info.Action = constant.TaskActionRemix
	c.Set("origin_task", &model.Task{Data: []byte(`{"size":"1280x720","seconds":"8"}`)})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	require.Nil(t, taskErr)
	assert.Equal(t, "720p", selection.EffectiveResolution)
	assert.Equal(t, 8, selection.EffectiveDurationSeconds)
}

func TestSoraRemixVideoBillingRecovers1024pFromLegacyTaskData(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Prompt: "remix"})
	info.Action = constant.TaskActionRemix
	c.Set("origin_task", &model.Task{Data: []byte(`{"size":"1792x1024","seconds":"12"}`)})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	require.Nil(t, taskErr)
	assert.Equal(t, "1024p", selection.EffectiveResolution)
	assert.Equal(t, 12, selection.EffectiveDurationSeconds)
}

func TestSoraRemixVideoBillingRejectsWhenSnapshotAndTaskDataHaveNoResolution(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Prompt: "remix"})
	info.Action = constant.TaskActionRemix
	c.Set("origin_task", &model.Task{
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{OtherRatios: map[string]float64{"seconds": 4}}},
		Data:        []byte(`{"seconds":"4"}`),
	})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Contains(t, taskErr.Message, "unknown")
}

func TestSoraRemixVideoBillingRejectsUnboundedLegacyDuration(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{Prompt: "remix"})
	info.Action = constant.TaskActionRemix
	c.Set("origin_task", &model.Task{Data: []byte(`{"size":"1280x720","seconds":"999999"}`)})

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)

	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
}

func TestSoraRemixBuildRequestBodyDropsResolutionOverrides(t *testing.T) {
	c, info := soraVideoBillingContext(t, relaycommon.TaskSubmitReq{
		Prompt:     "remix",
		Size:       "1792x1024",
		Resolution: "1024p",
		Seconds:    "99",
		Duration:   99,
	})
	info.Action = constant.TaskActionRemix
	info.UpstreamModelName = "sora-2-pro"
	info.ChannelSetting.VideoPayloadFormat = "dashscope"
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos/origin/remix",
		strings.NewReader(`{"prompt":"remix","size":"1792x1024","resolution":"1024p","seconds":"99","duration":99,"input":{"prompt":"wrapped"},"parameters":{"resolution":"1080P","duration":3600}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")

	body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(payload, &decoded))

	assert.NotContains(t, decoded, "size")
	assert.NotContains(t, decoded, "resolution")
	assert.NotContains(t, decoded, "seconds")
	assert.NotContains(t, decoded, "duration")
	parameters, ok := decoded["parameters"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, parameters, "resolution")
	assert.NotContains(t, parameters, "duration")
}
