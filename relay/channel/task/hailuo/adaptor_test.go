package hailuo

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

func hailuoBillingContext(t *testing.T, req relaycommon.TaskSubmitReq, upstreamModel string) (*gin.Context, *relaycommon.RelayInfo) {
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

func decodeHailuoPayload(t *testing.T, adaptor *TaskAdaptor, c *gin.Context, info *relaycommon.RelayInfo) VideoRequest {
	t.Helper()
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload VideoRequest
	require.NoError(t, common.Unmarshal(data, &payload))
	return payload
}

func TestHailuoVideoBillingMatches512p720p768pAnd1080pPayloads(t *testing.T) {
	tests := []struct {
		model      string
		resolution string
		duration   int
	}{
		{"MiniMax-Hailuo-02", "512p", 6},
		{"T2V-01", "720p", 6},
		{"MiniMax-Hailuo-2.3", "768p", 10},
		{"MiniMax-Hailuo-2.3", "1080p", 6},
	}
	for _, tc := range tests {
		t.Run(tc.model+"-"+tc.resolution, func(t *testing.T) {
			metadata := map[string]any(nil)
			if tc.model == "MiniMax-Hailuo-02" && tc.resolution == "512p" {
				metadata = map[string]any{"first_frame_image": "https://example.com/frame.png"}
			}
			c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "client-price-key", Prompt: "animate", Size: tc.resolution, Duration: tc.duration, Metadata: metadata,
			}, tc.model)
			adaptor := &TaskAdaptor{}
			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			payload := decodeHailuoPayload(t, adaptor, c, info)
			require.NotNil(t, payload.Duration)
			assert.Equal(t, tc.model, payload.Model)
			assert.Equal(t, strings.ToUpper(tc.resolution), payload.Resolution)
			assert.Equal(t, strings.ToLower(payload.Resolution), selection.EffectiveResolution)
			assert.Equal(t, *payload.Duration, selection.EffectiveDurationSeconds)
			assert.Empty(t, selection.IndependentRatios)
			assert.Equal(t, "client-price-key", info.OriginModelName)
		})
	}
}

func TestHailuoVideoBillingRejectsUnsupportedResolutionInsteadOfDefaulting(t *testing.T) {
	c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "900p", Duration: 6,
	}, "MiniMax-Hailuo-02")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestHailuoVideoBillingValidatesMetadataOverrideAgainstModelConfig(t *testing.T) {
	c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "720p", Duration: 6,
		Metadata: map[string]any{"resolution": "1080P", "duration": 10},
	}, "T2V-01")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestHailuoVideoBillingRejects1080pTenSecondCartesianProduct(t *testing.T) {
	for _, model := range []string{
		"MiniMax-Hailuo-2.3",
		"MiniMax-Hailuo-2.3-Fast",
		"MiniMax-Hailuo-02",
	} {
		t.Run(model, func(t *testing.T) {
			metadata := map[string]any(nil)
			if model == "MiniMax-Hailuo-2.3-Fast" || model == "MiniMax-Hailuo-02" {
				metadata = map[string]any{"first_frame_image": "https://example.com/frame.png"}
			}
			c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Size: "1080p", Duration: 10, Metadata: metadata,
			}, model)
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			assert.Zero(t, selection)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestHailuoVideoBillingKeepsDocumentedNon1080Tuples(t *testing.T) {
	tests := []struct {
		model      string
		resolution string
		duration   int
	}{
		{"MiniMax-Hailuo-2.3", "768p", 10},
		{"MiniMax-Hailuo-2.3-Fast", "768p", 10},
		{"MiniMax-Hailuo-02", "512p", 10},
		{"T2V-01", "720p", 6},
	}
	for _, tc := range tests {
		t.Run(tc.model+"-"+tc.resolution, func(t *testing.T) {
			metadata := map[string]any(nil)
			if tc.model == "MiniMax-Hailuo-2.3-Fast" || (tc.model == "MiniMax-Hailuo-02" && tc.resolution == "512p") {
				metadata = map[string]any{"first_frame_image": "https://example.com/frame.png"}
			}
			c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Size: tc.resolution, Duration: tc.duration, Metadata: metadata,
			}, tc.model)
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			assert.Equal(t, tc.resolution, selection.EffectiveResolution)
			assert.Equal(t, tc.duration, selection.EffectiveDurationSeconds)
		})
	}
}

func TestHailuoVideoBillingSeparatesTextAndImageCapabilityTuples(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		metadata   map[string]any
		resolution string
		duration   int
		accepted   bool
	}{
		{"hailuo02 text rejects 512p", "MiniMax-Hailuo-02", nil, "512p", 6, false},
		{"hailuo02 image accepts 512p", "MiniMax-Hailuo-02", map[string]any{"first_frame_image": "https://example.com/frame.png"}, "512p", 10, true},
		{"hailuo23 fast rejects text mode", "MiniMax-Hailuo-2.3-Fast", nil, "768p", 6, false},
		{"hailuo23 fast accepts image mode", "MiniMax-Hailuo-2.3-Fast", map[string]any{"first_frame_image": "https://example.com/frame.png"}, "768p", 10, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Size: tc.resolution, Duration: tc.duration, Metadata: tc.metadata,
			}, tc.model)
			adaptor := &TaskAdaptor{}
			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			if !tc.accepted {
				assert.Zero(t, selection)
				require.NotNil(t, taskErr)
				assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				return
			}
			require.Nil(t, taskErr)
			payload := decodeHailuoPayload(t, adaptor, c, info)
			assert.NotEmpty(t, payload.FirstFrameImage)
			assert.Equal(t, tc.resolution, selection.EffectiveResolution)
			assert.Equal(t, tc.duration, selection.EffectiveDurationSeconds)
		})
	}
}

func TestHailuoLegacyVideoBillingUsesConservativeOfficialMatrix(t *testing.T) {
	t.Run("director defaults to 720p six seconds", func(t *testing.T) {
		c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
			Model: "alias", Prompt: "animate",
		}, "T2V-01-Director")
		adaptor := &TaskAdaptor{}
		selection, taskErr := adaptor.ResolveVideoBilling(c, info)
		require.Nil(t, taskErr)
		payload := decodeHailuoPayload(t, adaptor, c, info)
		assert.Equal(t, "720p", selection.EffectiveResolution)
		assert.Equal(t, 6, selection.EffectiveDurationSeconds)
		assert.Equal(t, "720P", payload.Resolution)
	})

	t.Run("legacy i2v rejects conflicted 1080p capability", func(t *testing.T) {
		c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
			Model: "alias", Prompt: "animate", Size: "1080p", Duration: 6,
			Metadata: map[string]any{"first_frame_image": "https://example.com/frame.png"},
		}, "I2V-01-Director")
		selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
		assert.Zero(t, selection)
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	})

	t.Run("s2v stays strict unknown", func(t *testing.T) {
		c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
			Model: "alias", Prompt: "animate", Size: "720p", Duration: 6,
		}, "S2V-01")
		selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
		assert.Zero(t, selection)
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	})
}
