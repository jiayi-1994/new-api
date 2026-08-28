package doubao

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

func doubaoBillingContext(t *testing.T, req relaycommon.TaskSubmitReq, upstreamModel string) (*gin.Context, *relaycommon.RelayInfo) {
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

func decodeDoubaoPayload(t *testing.T, adaptor *TaskAdaptor, c *gin.Context, info *relaycommon.RelayInfo) requestPayload {
	t.Helper()
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	return payload
}

func TestDoubaoVideoBillingUsesMappedModelCapabilitiesAndOriginPriceKey(t *testing.T) {
	c, info := doubaoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate",
		Metadata: map[string]any{
			"resolution": "1080p",
			"duration":   5,
			"content": []any{
				map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://example.com/a.mp4"}},
			},
		},
	}, "doubao-seedance-2-0-260128")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeDoubaoPayload(t, adaptor, c, info)

	assert.Equal(t, "client-price-key", info.OriginModelName)
	assert.Equal(t, "doubao-seedance-2-0-260128", payload.Model)
	assert.Equal(t, payload.Resolution, selection.EffectiveResolution)
	require.NotNil(t, payload.Duration)
	assert.Equal(t, int(*payload.Duration), selection.EffectiveDurationSeconds)
	assert.InDelta(t, 31.0/51.0, selection.IndependentRatios["video_input"], 1e-9)
	assert.NotContains(t, selection.IndependentRatios, "resolution")
}

func TestDoubaoVideoBillingUsesPerModelDocumentedDefaults(t *testing.T) {
	tests := []struct {
		model      string
		duration   int
		resolution string
	}{
		{"doubao-seedance-1-0-pro-250528", 4, "1080p"},
		{"doubao-seedance-1-0-pro-fast-250528", 4, "1080p"},
		{"doubao-seedance-1-0-lite-t2v", 4, "720p"},
		{"doubao-seedance-1-0-lite-i2v", 4, "720p"},
		{"doubao-seedance-1-5-pro-251215", 4, "720p"},
		{"doubao-seedance-2-0-260128", 4, "720p"},
		{"doubao-seedance-2-0-fast-260128", 4, "720p"},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			c, info := doubaoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Metadata: map[string]any{"duration": tc.duration},
			}, tc.model)
			adaptor := &TaskAdaptor{}
			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			payload := decodeDoubaoPayload(t, adaptor, c, info)
			assert.Equal(t, tc.resolution, selection.EffectiveResolution)
			assert.Equal(t, tc.resolution, payload.Resolution)
		})
	}
}

func TestDoubaoVideoBillingRejects1080pForLiteI2VAndSeedance20Fast(t *testing.T) {
	for _, model := range []string{"doubao-seedance-1-0-lite-i2v", "doubao-seedance-2-0-fast-260128"} {
		t.Run(model, func(t *testing.T) {
			c, info := doubaoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate",
				Metadata: map[string]any{"resolution": "1080p", "duration": 4},
			}, model)
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			assert.Zero(t, selection)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestDoubaoVideoBillingRejectsUnsupportedTierAndUnknownDuration(t *testing.T) {
	t.Run("unsupported tier", func(t *testing.T) {
		c, info := doubaoBillingContext(t, relaycommon.TaskSubmitReq{
			Model: "alias", Prompt: "animate",
			Metadata: map[string]any{"resolution": "4k", "duration": 5},
		}, "doubao-seedance-2-0-260128")
		selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
		assert.Zero(t, selection)
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	})

	t.Run("unknown duration", func(t *testing.T) {
		c, info := doubaoBillingContext(t, relaycommon.TaskSubmitReq{
			Model: "alias", Prompt: "animate", Metadata: map[string]any{"resolution": "720p"},
		}, "doubao-seedance-2-0-260128")
		selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
		assert.Zero(t, selection)
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	})
}

func TestDoubaoVideoBillingRejectsUndocumentedModelVariant(t *testing.T) {
	c, info := doubaoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate",
		Metadata: map[string]any{"resolution": "720p", "duration": 5},
	}, "proxy-doubao-seedance-2-0-260128-untrusted")
	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestDoubaoVideoInputIndependentRatioUsesSelectedTier(t *testing.T) {
	withoutVideo, ok := GetVideoInputIndependentRatio("doubao-seedance-2-0-260128", "1080p", false)
	require.True(t, ok)
	withVideo, ok := GetVideoInputIndependentRatio("doubao-seedance-2-0-260128", "1080p", true)
	require.True(t, ok)
	assert.Equal(t, 1.0, withoutVideo)
	assert.InDelta(t, 31.0/51.0, withVideo, 1e-9)

	legacy, ok := GetVideoInputRatio("doubao-seedance-2-0-260128", "1080p", true)
	require.True(t, ok)
	assert.InDelta(t, 31.0/46.0, legacy, 1e-9)
}

func TestDoubaoParseTaskResultUsesOnlyBoundedActualDuration(t *testing.T) {
	adaptor := &TaskAdaptor{}
	bounded, err := adaptor.ParseTaskResult([]byte(`{"status":"succeeded","duration":"9"}`))
	require.NoError(t, err)
	assert.Equal(t, 9, bounded.EffectiveDurationSeconds)

	oversized, err := adaptor.ParseTaskResult([]byte(`{"status":"succeeded","duration":999999}`))
	require.NoError(t, err)
	assert.Zero(t, oversized.EffectiveDurationSeconds)

	absent, err := adaptor.ParseTaskResult([]byte(`{"status":"succeeded"}`))
	require.NoError(t, err)
	assert.Zero(t, absent.EffectiveDurationSeconds)
}
