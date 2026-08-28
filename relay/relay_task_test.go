package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
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
