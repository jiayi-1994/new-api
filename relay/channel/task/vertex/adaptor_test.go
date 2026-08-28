package vertex

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	geminitask "github.com/QuantumNous/new-api/relay/channel/task/gemini"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func vertexBillingContext(t *testing.T, req relaycommon.TaskSubmitReq, upstreamModel string) (*gin.Context, *relaycommon.RelayInfo) {
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

func TestVertexVideoBillingMatchesSharedVeoPayload(t *testing.T) {
	c, info := vertexBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate", Size: "2160x3840", Seconds: "8",
	}, "veo-3.1-fast-generate-preview")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload geminitask.VeoRequestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	require.NotNil(t, payload.Parameters)

	assert.Equal(t, "client-price-key", info.OriginModelName)
	assert.Equal(t, "4k", selection.EffectiveResolution)
	assert.Equal(t, "9:16", payload.Parameters.AspectRatio)
	assert.Equal(t, payload.Parameters.Resolution, selection.EffectiveResolution)
	assert.Equal(t, payload.Parameters.DurationSeconds, selection.EffectiveDurationSeconds)
	assert.Empty(t, selection.IndependentRatios)
}

func TestVertexVideoBillingRejectsDimensionHeuristicAliases(t *testing.T) {
	c, info := vertexBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate", Size: "2000x1000", Duration: 4,
	}, "veo-3.0-generate-001")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}
