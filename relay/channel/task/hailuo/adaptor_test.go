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
			c, info := hailuoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "client-price-key", Prompt: "animate", Size: tc.resolution, Duration: tc.duration,
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
