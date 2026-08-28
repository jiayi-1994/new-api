package vertex

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		Model: "client-price-key", Prompt: "animate", Size: "1080x1920", Seconds: "6",
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
	assert.Equal(t, "1080p", selection.EffectiveResolution)
	assert.Equal(t, "9:16", payload.Parameters.AspectRatio)
	assert.Equal(t, payload.Parameters.Resolution, selection.EffectiveResolution)
	assert.Equal(t, payload.Parameters.DurationSeconds, selection.EffectiveDurationSeconds)
	assert.Empty(t, selection.IndependentRatios)
}

func TestVertexVeoVideoBillingUsesVertexDurationAndResolutionMatrix(t *testing.T) {
	for _, duration := range []int{4, 6, 8} {
		t.Run("1080p-portrait-"+strconv.Itoa(duration), func(t *testing.T) {
			c, info := vertexBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Size: "1080x1920", Duration: duration,
			}, "veo-3.0-generate-001")
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
			assert.Equal(t, "1080p", selection.EffectiveResolution)
			assert.Equal(t, duration, selection.EffectiveDurationSeconds)
			assert.Equal(t, "9:16", payload.Parameters.AspectRatio)
			assert.Equal(t, selection.EffectiveResolution, payload.Parameters.Resolution)
			assert.Equal(t, selection.EffectiveDurationSeconds, payload.Parameters.DurationSeconds)
		})
	}

	t.Run("reject-4k", func(t *testing.T) {
		c, info := vertexBillingContext(t, relaycommon.TaskSubmitReq{
			Model: "alias", Prompt: "animate", Size: "3840x2160", Duration: 8,
		}, "veo-3.1-generate-preview")
		selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
		assert.Zero(t, selection)
		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	})
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

func TestVertexVideoBillingSupportsCurrentVeo31GAModels(t *testing.T) {
	for _, model := range []string{"veo-3.1-generate-001", "veo-3.1-fast-generate-001"} {
		t.Run(model, func(t *testing.T) {
			c, info := vertexBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Size: "1080x1920", Duration: 6,
			}, model)
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
			assert.Equal(t, "1080p", selection.EffectiveResolution)
			assert.Equal(t, 6, selection.EffectiveDurationSeconds)
			assert.Equal(t, "9:16", payload.Parameters.AspectRatio)
		})
	}
}
