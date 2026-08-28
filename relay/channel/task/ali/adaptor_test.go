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

func TestAliVideoBillingPreservesLegacyPortraitPayloadSize(t *testing.T) {
	c, info := aliBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "720*1280", Duration: 5,
	}, "wan2.5-t2v-preview")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeAliPayload(t, adaptor, c, info)
	assert.Equal(t, "720p", selection.EffectiveResolution)
	assert.Equal(t, "720*1280", payload.Parameters.Size)
	assert.Empty(t, payload.Parameters.Resolution)
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
