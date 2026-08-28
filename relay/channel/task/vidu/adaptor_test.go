package vidu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
	info := &relaycommon.RelayInfo{
		OriginModelName: req.Model,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeVidu, UpstreamModelName: upstreamModel,
		},
	}
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
	return c, info
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
				Model: "alias", Prompt: "animate", Images: []string{"https://example.com/frame.png"}, Duration: 8, Size: resolution,
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
				Model: "alias", Prompt: "animate", Images: []string{"https://example.com/frame.png"}, Duration: tc.duration, Size: tc.size,
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

func TestVidu20TextToVideoRejectsBeforePayloadBuild(t *testing.T) {
	c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Duration: 4, Size: "360p",
	}, "vidu2.0")
	adaptor := &TaskAdaptor{}
	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	_, err := adaptor.BuildRequestBody(c, info)
	require.Error(t, err)
}

func TestViduVideoBillingUsesActionModelCapabilityAndMatchingURL(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		images     []string
		duration   int
		resolution string
		path       string
	}{
		{"q2 text", "viduq2", nil, 9, "1080p", "/ent/v2/text2video"},
		{"q2 reference", "viduq2", []string{"a", "b", "c"}, 9, "1080p", "/ent/v2/reference2video"},
		{"q1 text", "viduq1", nil, 5, "1080p", "/ent/v2/text2video"},
		{"q1 image", "viduq1", []string{"a"}, 5, "1080p", "/ent/v2/img2video"},
		{"q1 start-end", "viduq1", []string{"a", "b"}, 5, "1080p", "/ent/v2/start-end2video"},
		{"q1 reference", "viduq1", []string{"a", "b", "c"}, 5, "1080p", "/ent/v2/reference2video"},
		{"vidu20 image", "vidu2.0", []string{"a"}, 4, "360p", "/ent/v2/img2video"},
		{"vidu20 start-end", "vidu2.0", []string{"a", "b"}, 8, "720p", "/ent/v2/start-end2video"},
		{"vidu20 reference", "vidu2.0", []string{"a", "b", "c"}, 4, "720p", "/ent/v2/reference2video"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Images: tc.images,
				Duration: tc.duration, Resolution: tc.resolution,
			}, tc.model)
			adaptor := &TaskAdaptor{baseURL: "https://vidu.example"}
			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			payload := decodeViduPayload(t, adaptor, c, info)
			url, err := adaptor.BuildRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, "https://vidu.example"+tc.path, url)
			assert.Equal(t, tc.model, payload.Model)
			assert.Equal(t, tc.resolution, payload.Resolution)
			assert.Equal(t, tc.duration, payload.Duration)
			assert.Equal(t, payload.Resolution, selection.EffectiveResolution)
			assert.Equal(t, payload.Duration, selection.EffectiveDurationSeconds)
			if info.Action == constant.TaskActionReferenceGenerate {
				assert.Empty(t, payload.Images)
				require.Len(t, payload.Subjects, 1)
				assert.Equal(t, "subject1", payload.Subjects[0].Name)
				assert.Equal(t, tc.images, payload.Subjects[0].Images)
			}
		})
	}
}

func TestViduVideoBillingValidatesFinalPayloadInputsBeforeBilling(t *testing.T) {
	tests := []relaycommon.TaskSubmitReq{
		{
			Model: "alias", Prompt: "animate", Duration: 5, Resolution: "1080p",
			Metadata: map[string]any{"action": constant.TaskActionGenerate},
		},
		{
			Model: "alias", Prompt: "animate", Images: []string{"https://example.com/frame.png"}, Duration: 5, Resolution: "1080p",
			Metadata: map[string]any{"images": []string{}},
		},
	}
	for index, req := range tests {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			c, info := viduBillingContext(t, req, "viduq1")
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			assert.Zero(t, selection)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
		})
	}
}

func TestViduReferenceVideoBillingRejectsMoreThanSevenImages(t *testing.T) {
	c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Duration: 5, Resolution: "720p",
		Images: []string{"1", "2", "3", "4", "5", "6", "7", "8"},
	}, "viduq2")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestViduQ2FlatImagesUseStableReferenceSubjects(t *testing.T) {
	tests := []struct {
		name       string
		images     []string
		metadata   map[string]any
		groupSizes []int
	}{
		{"one", []string{"1"}, nil, []int{1}},
		{"two", []string{"1", "2"}, nil, []int{2}},
		{"three", []string{"1", "2", "3"}, nil, []int{3}},
		{"four", []string{"1", "2", "3", "4"}, nil, []int{3, 1}},
		{"seven", []string{"1", "2", "3", "4", "5", "6", "7"}, nil, []int{3, 3, 1}},
		{"explicit reference", []string{"1", "2"}, map[string]any{"action": constant.TaskActionReferenceGenerate}, []int{2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Duration: 5, Resolution: "720p",
				Images: tc.images, Metadata: tc.metadata,
			}, "viduq2")
			adaptor := &TaskAdaptor{baseURL: "https://vidu.example"}

			selection, taskErr := adaptor.ResolveVideoBilling(c, info)
			require.Nil(t, taskErr)
			payload := decodeViduPayload(t, adaptor, c, info)
			url, err := adaptor.BuildRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, constant.TaskActionReferenceGenerate, info.Action)
			assert.Equal(t, "https://vidu.example/ent/v2/reference2video", url)
			assert.Equal(t, "720p", selection.EffectiveResolution)
			assert.Empty(t, payload.Images)
			require.Len(t, payload.Subjects, len(tc.groupSizes))
			flattened := make([]string, 0, len(tc.images))
			for index, groupSize := range tc.groupSizes {
				assert.Equal(t, "subject"+strconv.Itoa(index+1), payload.Subjects[index].Name)
				assert.Len(t, payload.Subjects[index].Images, groupSize)
				flattened = append(flattened, payload.Subjects[index].Images...)
			}
			assert.Equal(t, tc.images, flattened)
		})
	}
}

func TestViduReferenceMetadataRejectsSubjectWithMoreThanThreeImages(t *testing.T) {
	c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
		Model: "alias", Prompt: "animate", Duration: 5, Resolution: "720p",
		Metadata: map[string]any{
			"action":   constant.TaskActionReferenceGenerate,
			"subjects": []map[string]any{{"name": "subject1", "images": []string{"1", "2", "3", "4"}}},
		},
	}, "viduq2")

	selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
	assert.Zero(t, selection)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
}

func TestViduVideoBillingRejectsUnsupportedActionModelTuples(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		images     []string
		duration   int
		resolution string
	}{
		{"vidu20 text", "vidu2.0", nil, 4, "360p"},
		{"vidu20 reference eight seconds", "vidu2.0", []string{"a", "b", "c"}, 8, "720p"},
		{"vidu20 reference 1080p", "vidu2.0", []string{"a", "b", "c"}, 4, "1080p"},
		{"vidu15 unknown", "vidu1.5", []string{"a"}, 5, "720p"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, info := viduBillingContext(t, relaycommon.TaskSubmitReq{
				Model: "alias", Prompt: "animate", Images: tc.images,
				Duration: tc.duration, Resolution: tc.resolution,
			}, tc.model)
			selection, taskErr := (&TaskAdaptor{}).ResolveVideoBilling(c, info)
			assert.Zero(t, selection)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
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
