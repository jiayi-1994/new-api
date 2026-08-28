package vidu

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

func viduBillingContext(t *testing.T, req relaycommon.TaskSubmitReq, upstreamModel string) (*gin.Context, *relaycommon.RelayInfo) {
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

func decodeViduPayload(t *testing.T, adaptor *TaskAdaptor, c *gin.Context, info *relaycommon.RelayInfo) requestPayload {
	t.Helper()
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	return payload
}

func TestViduVideoBillingDefaultsViduQ1To1080p(t *testing.T) {
	c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate",
	}, "viduq1")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeViduPayload(t, adaptor, c, info)
	assert.Equal(t, "client-price-key", info.OriginModelName)
	assert.Equal(t, "viduq1", payload.Model)
	assert.Equal(t, "1080p", payload.Resolution)
	assert.Equal(t, payload.Resolution, selection.EffectiveResolution)
	assert.Equal(t, payload.Duration, selection.EffectiveDurationSeconds)
	assert.Empty(t, selection.IndependentRatios)
}

func TestViduVideoBillingDefaultsViduQ2To720p(t *testing.T) {
	c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate",
	}, "viduq2")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeViduPayload(t, adaptor, c, info)
	assert.Equal(t, "720p", payload.Resolution)
	assert.Equal(t, payload.Resolution, selection.EffectiveResolution)
	assert.Equal(t, payload.Duration, selection.EffectiveDurationSeconds)
}

func TestViduVideoBillingMetadataOverrideMatchesPayloadAndBoundsDuration(t *testing.T) {
	c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Size: "540p", Duration: 4,
		Metadata: map[string]any{"resolution": "1080P", "duration": 9},
	}, "viduq2")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeViduPayload(t, adaptor, c, info)
	assert.Equal(t, "1080p", selection.EffectiveResolution)
	assert.Equal(t, 9, selection.EffectiveDurationSeconds)
	assert.Equal(t, selection.EffectiveResolution, payload.Resolution)
	assert.Equal(t, selection.EffectiveDurationSeconds, payload.Duration)

	badC, badInfo := viduBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate",
		Metadata: map[string]any{"duration": relaycommon.MaxTaskDurationSeconds + 1},
	}, "viduq2")
	badSelection, badTaskErr := adaptor.ResolveVideoBilling(badC, badInfo)
	assert.Zero(t, badSelection)
	require.NotNil(t, badTaskErr)
	assert.Equal(t, http.StatusBadRequest, badTaskErr.StatusCode)
}

func TestVidu20VideoBillingRejectsEightSecondsOutside720p(t *testing.T) {
	for _, resolution := range []string{"360p", "1080p"} {
		t.Run(resolution, func(t *testing.T) {
			c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Duration: 8, Size: resolution,
			}, "vidu2.0")
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			assert.Zero(t, selection)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestVidu20VideoBillingUsesDurationSpecificResolutionCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		duration   int
		size       string
		resolution string
	}{
		{"four seconds 360p", 4, "360p", "360p"},
		{"four seconds 720p", 4, "720p", "720p"},
		{"four seconds 1080p", 4, "1080p", "1080p"},
		{"eight seconds 720p", 8, "720p", "720p"},
		{"eight seconds default", 8, "", "720p"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Duration: tc.duration, Size: tc.size,
			}, "vidu2.0")
			adaptor := &TaskAdaptor{}
			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			payload := decodeViduPayload(t, adaptor, c, info)
			assert.Equal(t, tc.resolution, selection.EffectiveResolution)
			assert.Equal(t, tc.resolution, payload.Resolution)
			assert.Equal(t, tc.duration, payload.Duration)
		})
	}
}

func TestViduParseTaskResultUsesOnlyBoundedActualDuration(t *testing.T) {
	adaptor := &TaskAdaptor{}
	bounded, err := adaptor.ParseTaskResult([]byte(`{"state":"success","creations":[{"url":"https://example.com/v.mp4","video":{"duration":8}}]}`))
	require.NoError(t, err)
	assert.Equal(t, 8, bounded.EffectiveDurationSeconds)

	oversized, err := adaptor.ParseTaskResult([]byte(`{"state":"success","creations":[{"video":{"duration":999999}}]}`))
	require.NoError(t, err)
	assert.Zero(t, oversized.EffectiveDurationSeconds)

	absent, err := adaptor.ParseTaskResult([]byte(`{"state":"success","creations":[{}]}`))
	require.NoError(t, err)
	assert.Zero(t, absent.EffectiveDurationSeconds)
}
