package ali

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
}

func TestConvertToAliRequestWan27I2VBuildsMediaFromImage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:    "wan2.7-i2v",
		Prompt:   "animate the first frame",
		Image:    "https://example.com/first.png",
		Size:     "720p",
		Duration: 10,
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "wan2.7-i2v", aliReq.Model)
	require.Equal(t, "720P", aliReq.Parameters.Resolution)
	require.Equal(t, 10, aliReq.Parameters.Duration)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
	}, aliReq.Input.Media)
	require.Empty(t, aliReq.Input.ImgURL)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestConvertToAliRequestWan27I2VBuildsFirstAndLastFrameFromImages(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "interpolate between frames",
		Images: []string{
			"https://example.com/first.png",
			"https://example.com/last.png",
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VPrefersImageBeforeImagesAndInputReference(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "wan2.7-i2v",
		Prompt:         "use the direct image",
		Image:          " https://example.com/direct.png ",
		Images:         []string{"https://example.com/images-first.png", " https://example.com/images-last.png "},
		InputReference: "https://example.com/input-reference.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/direct.png"},
		{Type: "last_frame", URL: "https://example.com/images-last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VFallsBackToFirstNonEmptyImage(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "skip blank images",
		Image:  " ",
		Images: []string{
			" ",
			" https://example.com/first.png ",
			" https://example.com/last.png ",
		},
		InputReference: "https://example.com/input-reference.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
}

func TestConvertToAliRequestWan27I2VKeepsExplicitMetadataMedia(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:          "wan2.7-i2v",
		Prompt:         "continue the clip",
		Image:          "https://example.com/direct.png",
		Images:         []string{"https://example.com/images-first.png", "https://example.com/images-last.png"},
		InputReference: "https://example.com/input-reference.png",
		Metadata: map[string]interface{}{
			"input": map[string]interface{}{
				"media": []interface{}{
					map[string]interface{}{
						"type": "first_clip",
						"url":  "https://example.com/input.mp4",
					},
				},
			},
		},
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_clip", URL: "https://example.com/input.mp4"},
	}, aliReq.Input.Media)
	require.Empty(t, aliReq.Input.ImgURL)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestConvertToAliRequestWan27I2VRequiresMedia(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Prompt: "animate without a frame",
	}

	_, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "requires image"))
}

func TestConvertToAliRequestWan25I2VKeepsLegacyImgURL(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.5-i2v-preview",
		Prompt: "animate the first frame",
		Image:  "https://example.com/first.png",
	}

	aliReq, err := adaptor.convertToAliRequest(testRelayInfo(), req)

	require.NoError(t, err)
	require.Equal(t, "https://example.com/first.png", aliReq.Input.ImgURL)
	require.Empty(t, aliReq.Input.Media)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"img_url"`)
	require.NotContains(t, string(body), `"media"`)
}

func aliBillingContext(t *testing.T, req relaycommon.TaskSubmitReq, upstreamModel string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	body, err := common.Marshal(req)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("task_request", req)
	return c, &relaycommon.RelayInfo{
		OriginModelName: req.Model,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: upstreamModel},
	}
}

func decodeAliPayload(t *testing.T, adaptor *TaskAdaptor, c *gin.Context, info *relaycommon.RelayInfo) AliVideoRequest {
	t.Helper()
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload AliVideoRequest
	require.NoError(t, common.Unmarshal(data, &payload))
	require.NotNil(t, payload.Parameters)
	return payload
}

func TestAliVideoBillingUsesMappedUpstreamModelDefaultAndOriginPriceKey(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate", Duration: 5,
	}, "wan2.7-t2v")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeAliPayload(t, adaptor, c, info)

	assert.Equal(t, "client-price-key", info.OriginModelName)
	assert.Equal(t, "wan2.7-t2v", payload.Model)
	assert.Equal(t, "1080p", selection.EffectiveResolution)
	assert.Equal(t, 5, selection.EffectiveDurationSeconds)
	assert.Empty(t, payload.Parameters.Size)
	assert.Equal(t, "1080P", payload.Parameters.Resolution)
	assert.Empty(t, selection.IndependentRatios)
}

func TestAliVideoBillingRejectsConflictingSizeAndResolution(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate", Size: "1920*1080", Duration: 5,
		Metadata: map[string]any{"parameters": map[string]any{"resolution": "720P"}},
	}, "wan2.7-t2v")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
}

func TestAliVideoBillingCollapsesEquivalentSizeAndResolutionToOneField(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate", Size: "1920*1080", Duration: 5,
		Metadata: map[string]any{"parameters": map[string]any{"resolution": "1080p"}},
	}, "wan2.7-t2v")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeAliPayload(t, adaptor, c, info)
	assert.Equal(t, "1080p", selection.EffectiveResolution)
	assert.Empty(t, payload.Parameters.Size)
	assert.Equal(t, "1080P", payload.Parameters.Resolution)
}

func TestAliVideoBillingUsesWan27TextToVideo1080pDefault(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Seconds: "7",
	}, "wan2.7-t2v")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeAliPayload(t, adaptor, c, info)
	assert.Equal(t, "1080p", selection.EffectiveResolution)
	assert.Equal(t, 7, selection.EffectiveDurationSeconds)
	assert.Empty(t, payload.Parameters.Size)
	assert.Equal(t, "1080P", payload.Parameters.Resolution)
	assert.Equal(t, 7, payload.Parameters.Duration)
}

func TestAliVideoBillingRejectsUnknownDefault(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Image: "https://example.com/a.png", Duration: 5,
	}, "wan2.6-i2v")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Contains(t, taskErr.Message, "unknown")
}

func TestAliVideoBillingUsesExactModelDefaults(t *testing.T) {
	tests := []struct {
		model      string
		resolution string
		duration   int
	}{
		{"wan2.7-i2v", "1080p", 5},
		{"wan2.5-i2v-preview", "1080p", 5},
		{"wan2.2-i2v-flash", "720p", 5},
		{"wan2.2-i2v-plus", "1080p", 5},
		{"wanx2.1-i2v-plus", "720p", 5},
		{"wanx2.1-i2v-turbo", "720p", 5},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Image: "https://example.com/frame.png",
			}, tc.model)
			adaptor := &TaskAdaptor{}
			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			payload := decodeAliPayload(t, adaptor, c, info)
			assert.Equal(t, tc.resolution, selection.EffectiveResolution)
			assert.Equal(t, tc.duration, selection.EffectiveDurationSeconds)
			assert.Equal(t, strings.ToUpper(tc.resolution), payload.Parameters.Resolution)
			assert.Equal(t, tc.duration, payload.Parameters.Duration)
		})
	}
}

func TestAliVideoBillingRejectsUnsupportedModelTuples(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		resolution string
		duration   int
	}{
		{"wan27 i2v 480p", "wan2.7-i2v", "480p", 5},
		{"wan25 duration six", "wan2.5-i2v-preview", "1080p", 6},
		{"wan22 flash cross-region 1080p", "wan2.2-i2v-flash", "1080p", 5},
		{"wan22 plus 720p ten seconds", "wan2.2-i2v-plus", "720p", 10},
		{"wan21 plus 480p", "wanx2.1-i2v-plus", "480p", 5},
		{"wan21 turbo 1080p", "wanx2.1-i2v-turbo", "1080p", 5},
		{"wan21 turbo ten seconds", "wanx2.1-i2v-turbo", "720p", 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Image: "https://example.com/frame.png",
				Resolution: tc.resolution, Duration: tc.duration,
			}, tc.model)
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			assert.Zero(t, selection)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestAliVideoBillingRejectsUnboundedMetadataDuration(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "1920*1080",
		Metadata: map[string]any{"parameters": map[string]any{"duration": relaycommon.MaxTaskDurationSeconds + 1}},
	}, "wan2.7-t2v")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestAliWan27TextToVideoUsesResolutionProtocolAndPreservesAspectRatio(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "720*1280", Duration: 5,
	}, "wan2.7-t2v")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeAliPayload(t, adaptor, c, info)
	assert.Equal(t, "720p", selection.EffectiveResolution)
	assert.Empty(t, payload.Parameters.Size)
	assert.Equal(t, "720P", payload.Parameters.Resolution)
	assert.Equal(t, "9:16", payload.Parameters.Ratio)
}

func TestAliVideoBillingRejectsLegacyOnlyModelWithoutVerifiedCapability(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "720*1280", Duration: 5,
	}, "wan2.5-t2v-preview")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestAliWan27MetadataSizeControlsFinalAspectRatio(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "1280*720", Duration: 5,
		Metadata: map[string]any{"parameters": map[string]any{"size": "720*1280"}},
	}, "wan2.7-t2v")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeAliPayload(t, adaptor, c, info)
	assert.Equal(t, "720p", selection.EffectiveResolution)
	assert.Empty(t, payload.Parameters.Size)
	assert.Equal(t, "720P", payload.Parameters.Resolution)
	assert.Equal(t, "9:16", payload.Parameters.Ratio)
}

func TestAliWan27VideoBillingRejectsUnsupportedMetadataRatio(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Duration: 5,
		Metadata: map[string]any{"parameters": map[string]any{"resolution": "1080P", "ratio": "2:1"}},
	}, "wan2.7-t2v")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestAliWan27ImageToVideoRejectsTextOnlyRatioSelector(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Images: []string{"https://example.com/frame.png"}, Duration: 5,
		Metadata: map[string]any{"parameters": map[string]any{"resolution": "1080P", "ratio": "16:9"}},
	}, "wan2.7-i2v")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestAliWan27TextToVideoRejects480p(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "832*480", Duration: 5,
	}, "wan2.7-t2v")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestAliParseTaskResultUsesOnlyBoundedActualDuration(t *testing.T) {
	adaptor := &TaskAdaptor{}
	bounded, err := adaptor.ParseTaskResult([]byte(`{"output":{"task_status":"SUCCEEDED"},"usage":{"duration":"9"}}`))
	require.NoError(t, err)
	assert.Equal(t, 9, bounded.EffectiveDurationSeconds)

	oversized, err := adaptor.ParseTaskResult([]byte(`{"output":{"task_status":"SUCCEEDED"},"usage":{"duration":999999}}`))
	require.NoError(t, err)
	assert.Zero(t, oversized.EffectiveDurationSeconds)

	absent, err := adaptor.ParseTaskResult([]byte(`{"output":{"task_status":"SUCCEEDED"}}`))
	require.NoError(t, err)
	assert.Zero(t, absent.EffectiveDurationSeconds)
}

func TestAliParseTaskResultAcceptsWan21VideoDurationSchema(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"output":{"task_status":"SUCCEEDED"},"usage":{"video_duration":"5"}}`))
	require.NoError(t, err)
	assert.Equal(t, 5, result.EffectiveDurationSeconds)
}

func TestAliParseTaskResultAcceptsWan26DurationSchema(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"output":{"task_status":"SUCCEEDED"},"usage":{"duration":9.0}}`))
	require.NoError(t, err)
	assert.Equal(t, 9, result.EffectiveDurationSeconds)
}

func TestAliParseTaskResultIgnoresInvalidActualDurationWithoutFailingPolling(t *testing.T) {
	for _, usage := range []string{
		`{"duration":9.5}`,
		`{"duration":"9.5"}`,
		`{"duration":-1}`,
		`{"duration":0}`,
		`{"duration":999999}`,
		`{"video_duration":"not-a-number"}`,
	} {
		t.Run(usage, func(t *testing.T) {
			response := `{"output":{"task_status":"SUCCEEDED"},"usage":` + usage + `}`
			result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(response))
			require.NoError(t, err)
			assert.Zero(t, result.EffectiveDurationSeconds)
		})
	}
}
