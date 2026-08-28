package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func veoBillingContext(t *testing.T, req relaycommon.TaskSubmitReq, upstreamModel string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
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

func decodeVeoPayload(t *testing.T, adaptor *TaskAdaptor, c *gin.Context, info *relaycommon.RelayInfo) VeoRequestPayload {
	t.Helper()
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload VeoRequestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	require.NotNil(t, payload.Parameters)
	return payload
}

func TestVeoVideoBillingMatchesPayload(t *testing.T) {
	c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
		Model:    "client-model",
		Prompt:   "animate",
		Size:     "1920x1080",
		Duration: 8,
	}, "veo-3.0-generate-001")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeVeoPayload(t, adaptor, c, info)

	assert.Equal(t, payload.Parameters.Resolution, selection.EffectiveResolution)
	assert.Equal(t, payload.Parameters.DurationSeconds, selection.EffectiveDurationSeconds)
	assert.Equal(t, "1080p", selection.EffectiveResolution)
	assert.Empty(t, selection.IndependentRatios)
}

func TestVeo30VideoBillingAllows720pAnd1080pButRejects4k(t *testing.T) {
	for _, resolution := range []string{"720p", "1080p"} {
		t.Run(resolution, func(t *testing.T) {
			duration := 4
			if resolution == "1080p" {
				duration = 8
			}
			c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "client-model", Prompt: "animate", Duration: duration,
				Metadata: map[string]any{"resolution": resolution},
			}, "veo-3.0-fast-generate-001")
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			assert.Equal(t, resolution, selection.EffectiveResolution)
		})
	}

	c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-model", Prompt: "animate", Duration: 4,
		Metadata: map[string]any{"resolution": "4k"},
	}, "veo-3.0-generate-001")
	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
}

func TestVeo31PreviewVideoBillingAllows4k(t *testing.T) {
	c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-model", Prompt: "animate", Duration: 8,
		Metadata: map[string]any{"resolution": "4K"},
	}, "veo-3.1-generate-preview")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeVeoPayload(t, adaptor, c, info)
	assert.Equal(t, "4k", selection.EffectiveResolution)
	assert.Equal(t, "4k", payload.Parameters.Resolution)
}

func TestVeoVideoBillingDefaultsTo720p(t *testing.T) {
	c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-model", Prompt: "animate",
	}, "veo-3.1-fast-generate-preview")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeVeoPayload(t, adaptor, c, info)
	assert.Equal(t, "720p", selection.EffectiveResolution)
	assert.Equal(t, 8, selection.EffectiveDurationSeconds)
	assert.Equal(t, "720p", payload.Parameters.Resolution)
	assert.Equal(t, 8, payload.Parameters.DurationSeconds)
}

func TestProviderVideoBillingMetadataOverrideMatchesPayload(t *testing.T) {
	c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-price-key", Prompt: "animate", Size: "1280x720", Duration: 4,
		Metadata: map[string]any{"resolution": "1080P", "durationSeconds": 8},
	}, "veo-3.0-generate-001")
	adaptor := &TaskAdaptor{}

	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	payload := decodeVeoPayload(t, adaptor, c, info)
	assert.Equal(t, "client-price-key", info.OriginModelName)
	assert.Equal(t, "veo-3.0-generate-001", info.UpstreamModelName)
	assert.Equal(t, "1080p", selection.EffectiveResolution)
	assert.Equal(t, 8, selection.EffectiveDurationSeconds)
	assert.Equal(t, selection.EffectiveResolution, payload.Parameters.Resolution)
	assert.Equal(t, selection.EffectiveDurationSeconds, payload.Parameters.DurationSeconds)
}

func TestVeoVideoBillingRejectsUnboundedMetadataDuration(t *testing.T) {
	c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "client-model", Prompt: "animate",
		Metadata: map[string]any{"durationSeconds": relaycommon.MaxTaskDurationSeconds + 1},
	}, "veo-3.0-generate-001")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestVeoVideoBillingAcceptsOnlyProviderDurations(t *testing.T) {
	for _, duration := range []int{4, 6, 8} {
		t.Run("valid-"+strconv.Itoa(duration), func(t *testing.T) {
			c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Metadata: map[string]any{"durationSeconds": duration},
			}, "veo-3.0-generate-001")
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			assert.Equal(t, duration, selection.EffectiveDurationSeconds)
		})
	}
	for _, duration := range []int{1, 5, 9} {
		t.Run("invalid-"+strconv.Itoa(duration), func(t *testing.T) {
			c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Metadata: map[string]any{"durationSeconds": duration},
			}, "veo-3.0-generate-001")
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			assert.Zero(t, selection)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestVeoVideoBillingRequiresEightSecondsForHighResolution(t *testing.T) {
	for _, resolution := range []string{"1080p", "4k"} {
		for _, duration := range []int{4, 6} {
			t.Run(resolution+"-"+strconv.Itoa(duration), func(t *testing.T) {
				c, info := veoBillingContext(t, relaycommon.TaskSubmitReq{
					Model: "alias", Prompt: "animate",
					Metadata: map[string]any{"resolution": resolution, "durationSeconds": duration},
				}, "veo-3.1-generate-preview")
				selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
				assert.Zero(t, selection)
				require.NotNil(t, taskErr)
				assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			})
		}
	}
}
