package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskSubmitTestAdaptor struct {
	taskcommon.BaseBilling
	selection       relaycommon.VideoBillingSelection
	resolveErr      *dto.TaskError
	didEstimate     bool
	didAdjust       bool
	didBuildRequest bool
	didRequest      bool
	requestCalls    int
}

func (a *taskSubmitTestAdaptor) Init(*relaycommon.RelayInfo) {}

func (a *taskSubmitTestAdaptor) ValidateRequestAndSetAction(_ *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	info.Action = constant.TaskActionTextGenerate
	return nil
}

func (a *taskSubmitTestAdaptor) EstimateBilling(*gin.Context, *relaycommon.RelayInfo) map[string]float64 {
	a.didEstimate = true
	return map[string]float64{"seconds": 99, "size": 99}
}

func (a *taskSubmitTestAdaptor) AdjustBillingOnSubmit(*relaycommon.RelayInfo, []byte) map[string]float64 {
	a.didAdjust = true
	return map[string]float64{"seconds": 99, "size": 99}
}

func (a *taskSubmitTestAdaptor) BuildRequestURL(*relaycommon.RelayInfo) (string, error) {
	return "https://example.test/v1/videos", nil
}

func (a *taskSubmitTestAdaptor) BuildRequestHeader(*gin.Context, *http.Request, *relaycommon.RelayInfo) error {
	return nil
}

func (a *taskSubmitTestAdaptor) BuildRequestBody(*gin.Context, *relaycommon.RelayInfo) (io.Reader, error) {
	a.didBuildRequest = true
	return bytes.NewBufferString(`{}`), nil
}

func (a *taskSubmitTestAdaptor) DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (*http.Response, error) {
	a.didRequest = true
	a.requestCalls++
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(`{"id":"upstream-task"}`)),
	}, nil
}

func (a *taskSubmitTestAdaptor) DoResponse(*gin.Context, *http.Response, *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	return "upstream-task", []byte(`{"id":"upstream-task"}`), nil
}

func (a *taskSubmitTestAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return nil, nil
}

func (a *taskSubmitTestAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, nil
}

func (a *taskSubmitTestAdaptor) GetModelList() []string { return nil }
func (a *taskSubmitTestAdaptor) GetChannelName() string { return "test" }

type videoTaskSubmitTestAdaptor struct {
	*taskSubmitTestAdaptor
}

type unsupportedVideoAdaptorSpy struct {
	channel.TaskAdaptor
	buildCalls   int
	requestCalls int
	builtBody    []byte
	responseBody string
}

func (a *unsupportedVideoAdaptorSpy) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	a.buildCalls++
	body, err := a.TaskAdaptor.BuildRequestBody(c, info)
	if err != nil {
		return nil, err
	}
	a.builtBody, err = io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(a.builtBody), nil
}

func (a *unsupportedVideoAdaptorSpy) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	a.requestCalls++
	if a.responseBody != "" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(a.responseBody)),
		}, nil
	}
	return a.TaskAdaptor.DoRequest(c, info, body)
}

func (a *unsupportedVideoAdaptorSpy) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (relaycommon.VideoBillingSelection, *dto.TaskError) {
	resolver, ok := a.TaskAdaptor.(channel.VideoBillingResolver)
	if !ok {
		return relaycommon.VideoBillingSelection{}, nil
	}
	return resolver.ResolveVideoBilling(c, info)
}

func (a *videoTaskSubmitTestAdaptor) ResolveVideoBilling(*gin.Context, *relaycommon.RelayInfo) (relaycommon.VideoBillingSelection, *dto.TaskError) {
	return a.selection, a.resolveErr
}

type taskSubmitTestBilling struct{}

func (taskSubmitTestBilling) Settle(int) error         { return nil }
func (taskSubmitTestBilling) Refund(*gin.Context)      {}
func (taskSubmitTestBilling) NeedsRefund() bool        { return false }
func (taskSubmitTestBilling) GetPreConsumedQuota() int { return 0 }
func (taskSubmitTestBilling) Reserve(int) error        { return nil }

type taskSubmitTestState struct {
	preConsumeCalls int
}

func taskSubmitVideoTestContext(
	t *testing.T,
	adaptor channel.TaskAdaptor,
) (*gin.Context, *relaycommon.RelayInfo, relayTaskSubmitDeps, *taskSubmitTestState) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	originalModelPrices := ratio_setting.ModelPrice2JSONString()
	originalModes := ratio_setting.TaskBillingMode2JSONString()
	originalGroups := ratio_setting.GroupRatio2JSONString()
	originalSpecialGroups := ratio_setting.GroupGroupRatio2JSONString()
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(originalModelPrices))
		require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(originalModes))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(originalSpecialGroups))
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = request
	c.Set("platform", "video-test")
	c.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "client-model",
		UsingGroup:      "default",
		UserGroup:       "default",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}
	state := &taskSubmitTestState{}
	deps := relayTaskSubmitDeps{
		getTaskAdaptor: func(constant.TaskPlatform) channel.TaskAdaptor { return adaptor },
		preConsume: func(_ *gin.Context, _ int, relayInfo *relaycommon.RelayInfo) *dto.TaskError {
			state.preConsumeCalls++
			relayInfo.Billing = taskSubmitTestBilling{}
			return nil
		},
	}
	return c, info, deps, state
}

func TestRelayTaskSubmitUsesOriginalModelForResolutionPrice(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "720p", EffectiveDurationSeconds: 4}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	c.Set("model_mapping", `{"client-model":"upstream-model"}`)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1},"upstream-model":{"720p":0.9}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, 200, result.Quota)
	assert.Equal(t, "client-model", info.OriginModelName)
	assert.Equal(t, "upstream-model", info.UpstreamModelName)
	assert.False(t, base.didEstimate)
	assert.False(t, base.didAdjust)
	assert.True(t, base.didBuildRequest)
	assert.True(t, base.didRequest)
	assert.Equal(t, 1, state.preConsumeCalls)
	assert.Equal(t, 1, base.requestCalls)
}

func TestRelayTaskSubmitResolutionPriceAlwaysMultipliesDuration(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "720p", EffectiveDurationSeconds: 8}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, 400, result.Quota)
	assert.Empty(t, info.PriceData.OtherRatios())
	assert.Equal(t, 1, state.preConsumeCalls)
	assert.Equal(t, 1, base.requestCalls)
}

func TestRelayTaskSubmitResolutionPricingIgnoresLegacyPerCallMode(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "720p", EffectiveDurationSeconds: 8}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"client-model":"per_call"}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, 400, result.Quota)
	assert.Equal(t, 1, state.preConsumeCalls)
	assert.Equal(t, 1, base.requestCalls)
}

func TestRelayTaskSubmitRejectsUnconfiguredResolutionBeforeRequest(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "1080p", EffectiveDurationSeconds: 5}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.001}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Contains(t, taskErr.Message, "1080p")
	assert.False(t, info.ForcePreConsume)
	assert.False(t, base.didEstimate)
	assert.False(t, base.didBuildRequest)
	assert.False(t, base.didRequest)
	assert.Zero(t, state.preConsumeCalls)
	assert.Zero(t, base.requestCalls)
}

func TestRelayTaskSubmitRejectsUnconfiguredModelBeforePreConsume(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "720p", EffectiveDurationSeconds: 5}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.001}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Zero(t, state.preConsumeCalls)
	assert.False(t, base.didBuildRequest)
	assert.False(t, base.didRequest)
	assert.Zero(t, base.requestCalls)
}

func TestRelayTaskSubmitRejectsUnknownResolutionBeforePreConsume(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveDurationSeconds: 5}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Contains(t, taskErr.Message, "unknown")
	assert.Zero(t, state.preConsumeCalls)
	assert.False(t, base.didBuildRequest)
	assert.False(t, base.didRequest)
	assert.Zero(t, base.requestCalls)
}

func TestRelayTaskSubmitRejectsVideoAdaptorWithoutResolver(t *testing.T) {
	base := &taskSubmitTestAdaptor{}
	c, info, deps, state := taskSubmitVideoTestContext(t, base)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Contains(t, taskErr.Message, "unknown")
	assert.False(t, info.ForcePreConsume)
	assert.False(t, base.didEstimate)
	assert.False(t, base.didBuildRequest)
	assert.False(t, base.didRequest)
	assert.Zero(t, state.preConsumeCalls)
	assert.Zero(t, base.requestCalls)
}

func TestRelayTaskSubmitResolverRejectionStopsBeforePreConsumeAndRequest(t *testing.T) {
	base := &taskSubmitTestAdaptor{resolveErr: &dto.TaskError{
		Code:       "video_resolution_not_supported",
		Message:    "provider resolution conflict",
		StatusCode: http.StatusBadRequest,
	}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Zero(t, state.preConsumeCalls)
	assert.False(t, base.didBuildRequest)
	assert.Zero(t, base.requestCalls)
}

func TestRelayTaskSubmitKeepsLegacySunoPath(t *testing.T) {
	base := &taskSubmitTestAdaptor{}
	c, info, deps, state := taskSubmitVideoTestContext(t, base)
	c.Set("platform", string(constant.TaskPlatformSuno))
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.1}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.True(t, base.didEstimate)
	assert.True(t, base.didRequest)
	assert.Equal(t, 1, state.preConsumeCalls)
	assert.Equal(t, 1, base.requestCalls)
}

func TestUnsupportedVideoAdaptorRejectsKlingAndJimengBeforeRequest(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeKling, constant.ChannelTypeJimeng} {
		t.Run(strconv.Itoa(channelType), func(t *testing.T) {
			actual := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channelType)))
			require.NotNil(t, actual)
			spy := &unsupportedVideoAdaptorSpy{TaskAdaptor: actual}
			c, info, deps, state := taskSubmitVideoTestContext(t, spy)
			c.Request = httptest.NewRequest(
				http.MethodPost,
				"/v1/videos",
				bytes.NewBufferString(`{"model":"client-model","prompt":"animate"}`),
			)
			c.Request.Header.Set("Content-Type", "application/json")
			require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

			result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

			assert.Nil(t, result)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
			assert.Contains(t, taskErr.Message, "unknown")
			assert.Zero(t, state.preConsumeCalls)
			assert.Zero(t, spy.buildCalls)
			assert.Zero(t, spy.requestCalls)
		})
	}
}

func TestVeoProviderCapabilitiesApplyBeforePreConsumeAndMatchBuiltPayload(t *testing.T) {
	tests := []struct {
		name           string
		channelType    int
		upstreamModel  string
		requestBody    string
		wantAccepted   bool
		wantResolution string
		wantDuration   int
	}{
		{
			name: "gemini rejects 720p four seconds", channelType: constant.ChannelTypeGemini,
			upstreamModel: "veo-3.0-generate-001",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1280x720","duration":4}`,
		},
		{
			name: "gemini rejects 1080p portrait", channelType: constant.ChannelTypeGemini,
			upstreamModel: "veo-3.0-generate-001",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1080x1920","duration":8}`,
		},
		{
			name: "gemini rejects retired standard model", channelType: constant.ChannelTypeGemini,
			upstreamModel: "veo-3.0-generate-001",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1280x720","duration":8}`,
		},
		{
			name: "gemini rejects retired fast model", channelType: constant.ChannelTypeGemini,
			upstreamModel: "veo-3.0-fast-generate-001",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1280x720","duration":8}`,
		},
		{
			name: "gemini accepts advertised preview model", channelType: constant.ChannelTypeGemini,
			upstreamModel: "veo-3.1-generate-preview",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1280x720","duration":4}`,
			wantAccepted:  true, wantResolution: "720p", wantDuration: 4,
		},
		{
			name: "vertex rejects retired veo30 standard", channelType: constant.ChannelTypeVertexAi,
			upstreamModel: "veo-3.0-generate-001",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1280x720","duration":8}`,
		},
		{
			name: "vertex rejects retired veo30 fast", channelType: constant.ChannelTypeVertexAi,
			upstreamModel: "veo-3.0-fast-generate-001",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1280x720","duration":8}`,
		},
		{
			name: "vertex rejects retired veo31 preview", channelType: constant.ChannelTypeVertexAi,
			upstreamModel: "veo-3.1-generate-preview",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1280x720","duration":8}`,
		},
		{
			name: "vertex rejects retired veo31 fast preview", channelType: constant.ChannelTypeVertexAi,
			upstreamModel: "veo-3.1-fast-generate-preview",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1280x720","duration":8}`,
		},
		{
			name: "vertex accepts current ga portrait tuple", channelType: constant.ChannelTypeVertexAi,
			upstreamModel: "veo-3.1-generate-001",
			requestBody:   `{"model":"client-model","prompt":"animate","size":"1080x1920","duration":4}`,
			wantAccepted:  true, wantResolution: "1080p", wantDuration: 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(tc.channelType)))
			require.NotNil(t, actual)
			spy := &unsupportedVideoAdaptorSpy{TaskAdaptor: actual, responseBody: `{"name":"operations/provider-test"}`}
			c, info, deps, state := taskSubmitVideoTestContext(t, spy)
			c.Set("platform", strconv.Itoa(tc.channelType))
			common.SetContextKey(c, constant.ContextKeyChannelType, tc.channelType)
			c.Set("model_mapping", `{"client-model":"`+tc.upstreamModel+`"}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(tc.requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(
				`{"client-model":{"720p":0.1,"1080p":0.2}}`,
			))

			result, taskErr := relayTaskSubmitWithDeps(c, info, deps)
			if !tc.wantAccepted {
				assert.Nil(t, result)
				require.NotNil(t, taskErr)
				assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
				assert.Zero(t, state.preConsumeCalls)
				assert.Zero(t, spy.buildCalls)
				assert.Zero(t, spy.requestCalls)
				return
			}

			require.Nil(t, taskErr)
			require.NotNil(t, result)
			assert.Equal(t, 1, state.preConsumeCalls)
			assert.Equal(t, 1, spy.buildCalls)
			assert.Equal(t, 1, spy.requestCalls)
			var payload struct {
				Parameters struct {
					Resolution      string `json:"resolution"`
					DurationSeconds int    `json:"durationSeconds"`
				} `json:"parameters"`
			}
			require.NoError(t, common.Unmarshal(spy.builtBody, &payload))
			assert.Equal(t, tc.wantResolution, payload.Parameters.Resolution)
			assert.Equal(t, tc.wantDuration, payload.Parameters.DurationSeconds)
		})
	}
}

func TestViduReferenceImageLimitAppliesBeforePreConsume(t *testing.T) {
	actual := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeVidu)))
	require.NotNil(t, actual)
	spy := &unsupportedVideoAdaptorSpy{TaskAdaptor: actual}
	c, info, deps, state := taskSubmitVideoTestContext(t, spy)
	c.Set("platform", strconv.Itoa(constant.ChannelTypeVidu))
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeVidu)
	c.Set("model_mapping", `{"client-model":"viduq2"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(
		`{"model":"client-model","prompt":"animate","duration":5,"resolution":"720p","images":["1","2","3","4","5","6","7","8"]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Zero(t, state.preConsumeCalls)
	assert.Zero(t, spy.buildCalls)
	assert.Zero(t, spy.requestCalls)
}

func TestViduReferenceSubjectLimitAppliesBeforePreConsume(t *testing.T) {
	actual := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeVidu)))
	require.NotNil(t, actual)
	spy := &unsupportedVideoAdaptorSpy{TaskAdaptor: actual}
	c, info, deps, state := taskSubmitVideoTestContext(t, spy)
	c.Set("platform", strconv.Itoa(constant.ChannelTypeVidu))
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeVidu)
	c.Set("model_mapping", `{"client-model":"viduq2"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(
		`{"model":"client-model","prompt":"animate","duration":5,"resolution":"720p","metadata":{"action":"referenceGenerate","subjects":[{"name":"subject1","images":["1","2","3","4"]}]}}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Zero(t, state.preConsumeCalls)
	assert.Zero(t, spy.buildCalls)
	assert.Zero(t, spy.requestCalls)
}

func TestViduQ2FlatImagesBuildReferencePayloadBeforeUpstream(t *testing.T) {
	for _, imageCount := range []int{1, 2, 3, 4, 7} {
		t.Run(strconv.Itoa(imageCount), func(t *testing.T) {
			images := make([]string, imageCount)
			for index := range images {
				images[index] = strconv.Itoa(index + 1)
			}
			body, err := common.Marshal(relaycommon.TaskSubmitReq{
				Model: "client-model", Prompt: "animate", Duration: 5, Resolution: "720p", Images: images,
			})
			require.NoError(t, err)
			actual := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeVidu)))
			require.NotNil(t, actual)
			spy := &unsupportedVideoAdaptorSpy{
				TaskAdaptor:  actual,
				responseBody: `{"task_id":"upstream-task","state":"created"}`,
			}
			c, info, deps, state := taskSubmitVideoTestContext(t, spy)
			c.Set("platform", strconv.Itoa(constant.ChannelTypeVidu))
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeVidu)
			c.Set("model_mapping", `{"client-model":"viduq2"}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

			result, taskErr := relayTaskSubmitWithDeps(c, info, deps)
			require.Nil(t, taskErr)
			require.NotNil(t, result)
			assert.Equal(t, constant.TaskActionReferenceGenerate, info.Action)
			assert.Equal(t, 1, state.preConsumeCalls)
			assert.Equal(t, 1, spy.buildCalls)
			assert.Equal(t, 1, spy.requestCalls)
			var payload struct {
				Images   []string `json:"images"`
				Subjects []struct {
					Name   string   `json:"name"`
					Images []string `json:"images"`
				} `json:"subjects"`
			}
			require.NoError(t, common.Unmarshal(spy.builtBody, &payload))
			assert.Empty(t, payload.Images)
			flattened := make([]string, 0, imageCount)
			for index, subject := range payload.Subjects {
				assert.Equal(t, "subject"+strconv.Itoa(index+1), subject.Name)
				assert.NotEmpty(t, subject.Images)
				assert.LessOrEqual(t, len(subject.Images), 3)
				flattened = append(flattened, subject.Images...)
			}
			assert.Equal(t, images, flattened)
		})
	}
}
