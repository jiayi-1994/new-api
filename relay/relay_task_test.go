package relay

import (
	"bytes"
	"errors"
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
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskSubmitTestAdaptor struct {
	taskcommon.BaseBilling
	selection       relaycommon.VideoBillingSelection
	resolveErr      *dto.TaskError
	estimateRatios  map[string]float64
	adjustRatios    map[string]float64
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
	if a.estimateRatios != nil {
		return a.estimateRatios
	}
	return map[string]float64{"seconds": 99, "size": 99}
}

func (a *taskSubmitTestAdaptor) AdjustBillingOnSubmit(*relaycommon.RelayInfo, []byte) map[string]float64 {
	a.didAdjust = true
	if a.adjustRatios != nil {
		return a.adjustRatios
	}
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

type retryVideoTaskSubmitTestAdaptor struct {
	*videoTaskSubmitTestAdaptor
}

// failFirstTaskSubmitRequest 让第一次上游提交失败以触发重试，第二次成功。
func failFirstTaskSubmitRequest(a *taskSubmitTestAdaptor) (*http.Response, error) {
	a.didRequest = true
	a.requestCalls++
	if a.requestCalls == 1 {
		return nil, errors.New("forced first attempt failure")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(`{"id":"upstream-task"}`)),
	}, nil
}

func (a *retryVideoTaskSubmitTestAdaptor) DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (*http.Response, error) {
	return failFirstTaskSubmitRequest(a.taskSubmitTestAdaptor)
}

// retryLegacyTaskSubmitTestAdaptor 故意不实现 ResolveVideoBilling：
// 旧版重试路径不得依赖任何分辨率解析能力。
type retryLegacyTaskSubmitTestAdaptor struct {
	*taskSubmitTestAdaptor
}

func (a *retryLegacyTaskSubmitTestAdaptor) DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (*http.Response, error) {
	return failFirstTaskSubmitRequest(a.taskSubmitTestAdaptor)
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

// taskSubmitRejectingBilling 模拟重试时补扣失败（例如钱包余额不够补差价）
type taskSubmitRejectingBilling struct {
	taskSubmitTestBilling
	reserveTargets []int
}

func (b *taskSubmitRejectingBilling) Reserve(target int) error {
	b.reserveTargets = append(b.reserveTargets, target)
	return relaytypes.NewErrorWithStatusCode(
		errors.New("insufficient user quota"),
		relaytypes.ErrorCodeInsufficientUserQuota,
		http.StatusForbidden,
	)
}

type taskSubmitTestState struct {
	preConsumeCalls  int
	preConsumedQuota int
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
		preConsume: func(_ *gin.Context, quota int, relayInfo *relaycommon.RelayInfo) *dto.TaskError {
			state.preConsumeCalls++
			state.preConsumedQuota = quota
			relayInfo.Billing = &taskSubmitTestBilling{}
			relayInfo.BillingSource = service.BillingSourceWallet
			return nil
		},
	}
	return c, info, deps, state
}

func TestRelayTaskSubmitToppingUpFailsBeforeReachingUpstream(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "1080p", EffectiveDurationSeconds: 8}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"1080p":0.5}}`))
	// 模拟重试：会话已存在（首次尝试已预扣），这次解析出更贵的额度需要补扣
	billing := &taskSubmitRejectingBilling{}
	info.Billing = billing

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.NotNil(t, taskErr)
	assert.Nil(t, result)
	assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
	// 补扣发生在提交之前：上游没有被调用，也就不会留下无法追踪的孤儿任务
	assert.False(t, base.didBuildRequest)
	assert.False(t, base.didRequest)
	assert.Zero(t, base.requestCalls)
	// 已经存在会话时不会重复预扣
	assert.Zero(t, state.preConsumeCalls)
	assert.Equal(t, []int{2000}, billing.reserveTargets)
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

func TestRelayTaskSubmitAttachesMissingFrozenBillingPlan(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "720p", EffectiveDurationSeconds: 4}}
	c, info, deps, _ := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	info.RequestId = "direct-task-plan"
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	require.NotNil(t, info.TaskRelayInfo.BillingPlan)
	assert.Equal(t, relaycommon.TaskBillingKindVideoResolution, info.TaskRelayInfo.BillingPlan.Kind())
	prepared, err := PrepareTaskBillingPlan(c, info.OriginModelName, info.RequestId)
	require.NoError(t, err)
	assert.Same(t, prepared, info.TaskRelayInfo.BillingPlan)
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

func TestRelayTaskSubmitUnconfiguredVideoModelUsesLegacyPrice(t *testing.T) {
	base := &taskSubmitTestAdaptor{}
	c, info, deps, state := taskSubmitVideoTestContext(t, base)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.2}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.True(t, base.didEstimate)
	assert.True(t, base.didAdjust)
	assert.Equal(t, 980100, state.preConsumedQuota)
	assert.Nil(t, info.ResolvedVideoBilling)
}

func TestRelayTaskSubmitLegacyPerCallIgnoresDurationMultiplier(t *testing.T) {
	base := &taskSubmitTestAdaptor{}
	c, info, deps, state := taskSubmitVideoTestContext(t, base)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.2}`))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"client-model":"per_call"}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, 100, state.preConsumedQuota)
	assert.Equal(t, 100, result.Quota)
}

func TestRelayTaskSubmitLegacyPerSecondUsesEstimateThenSubmitAdjustment(t *testing.T) {
	base := &taskSubmitTestAdaptor{
		estimateRatios: map[string]float64{"seconds": 2, "size": 3},
		adjustRatios:   map[string]float64{"seconds": 4, "size": 5},
	}
	c, info, deps, state := taskSubmitVideoTestContext(t, base)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.2}`))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"client-model":"per_second"}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, 600, state.preConsumedQuota)
	assert.Equal(t, 2000, result.Quota)
	assert.Equal(t, map[string]float64{"seconds": 4, "size": 5}, info.PriceData.OtherRatios())
}

func TestRelayTaskSubmitResolutionPlanUsesFrozenTableAfterLiveRemoval(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "720p", EffectiveDurationSeconds: 5}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan("client-model", "req-frozen", map[string]float64{"720p": 0.1})
	require.NoError(t, err)
	info.TaskRelayInfo.BillingPlan = plan
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, 250, state.preConsumedQuota)
	assert.Equal(t, 250, result.Quota)
}

func TestRelayTaskSubmitResolutionPlanDoesNotFallbackForMissingTier(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "1080p", EffectiveDurationSeconds: 5}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan("client-model", "req-frozen", map[string]float64{"720p": 0.1})
	require.NoError(t, err)
	info.TaskRelayInfo.BillingPlan = plan
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.2}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Zero(t, state.preConsumeCalls)
}

func TestRelayTaskSubmitRetryKeepsFrozenPlanFundingAndRequestIdentity(t *testing.T) {
	base := &taskSubmitTestAdaptor{
		selection: relaycommon.VideoBillingSelection{
			EffectiveResolution:      "720p",
			EffectiveDurationSeconds: 5,
		},
	}
	adaptor := &retryVideoTaskSubmitTestAdaptor{&videoTaskSubmitTestAdaptor{base}}
	c, info, deps, state := taskSubmitVideoTestContext(t, adaptor)
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan(
		"client-model", "req-frozen", map[string]float64{"720p": 0.1},
	)
	require.NoError(t, err)
	info.RequestId = "req-frozen"
	info.TaskRelayInfo.BillingPlan = plan
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	require.NotNil(t, info.Billing)
	frozenBilling := info.Billing
	frozenFunding := info.BillingSource
	assert.Equal(t, 1, state.preConsumeCalls)
	assert.Equal(t, 250, state.preConsumedQuota)

	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.9}}`))
	info.RequestId = "req-live-mutated"
	result, taskErr = relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, 250, result.Quota)
	assert.Equal(t, 1, state.preConsumeCalls)
	assert.Same(t, frozenBilling, info.Billing)
	assert.Equal(t, service.BillingSourceWallet, frozenFunding)
	assert.Equal(t, frozenFunding, info.BillingSource)
	assert.Same(t, plan, info.TaskRelayInfo.BillingPlan)
	assert.Equal(t, "req-frozen", info.TaskRelayInfo.BillingPlan.RequestID())
	frozenPrice, ok := info.TaskRelayInfo.BillingPlan.ResolutionPrice("720p")
	require.True(t, ok)
	assert.Equal(t, 0.1, frozenPrice)
	assert.Equal(t, 2, base.requestCalls)
}

// 旧版镜像：两次尝试之间上线了匹配的分辨率表，冻结的旧版计划必须继续走
// 历史估算/预扣路径，并复用第一次尝试的计费会话与资金来源。
func TestRelayTaskSubmitRetryKeepsLegacyPlanWhenResolutionTableAppears(t *testing.T) {
	base := &taskSubmitTestAdaptor{}
	adaptor := &retryLegacyTaskSubmitTestAdaptor{base}
	c, info, deps, state := taskSubmitVideoTestContext(t, adaptor)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.2}`))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"client-model":"per_call"}`))
	plan := relaycommon.NewLegacyTaskBillingPlan("client-model", "req-legacy-frozen")
	info.RequestId = "req-legacy-frozen"
	info.TaskRelayInfo.BillingPlan = plan

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	require.NotNil(t, info.Billing)
	frozenBilling := info.Billing
	frozenFunding := info.BillingSource
	assert.Equal(t, 1, state.preConsumeCalls)
	assert.Equal(t, 100, state.preConsumedQuota)

	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.1}}`))
	info.RequestId = "req-live-mutated"
	result, taskErr = relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, 100, result.Quota)
	assert.Equal(t, 1, state.preConsumeCalls)
	assert.Same(t, frozenBilling, info.Billing)
	assert.Equal(t, frozenFunding, info.BillingSource)
	assert.Same(t, plan, info.TaskRelayInfo.BillingPlan)
	assert.Equal(t, relaycommon.TaskBillingKindLegacy, info.TaskRelayInfo.BillingPlan.Kind())
	assert.Equal(t, "req-legacy-frozen", info.TaskRelayInfo.BillingPlan.RequestID())
	assert.Nil(t, info.ResolvedVideoBilling)
	assert.True(t, base.didEstimate)
	assert.True(t, base.didAdjust)
	assert.Equal(t, 2, base.requestCalls)
}

// 一张紧凑通配符表独立激活多个具体的非 Suno 模型；Suno 请求命中同一张表仍保持旧版；
// 激活与冻结均不改动存储的表文档。
func TestPrepareTaskBillingPlanCompactWildcardActivatesConcreteModelsIndependently(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"*-openai-compact":{"720p":0.1,"1080p":0.2}}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices))
	})
	storedBefore := ratio_setting.VideoResolutionPrice2JSONString()
	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		return c
	}

	planA, err := PrepareTaskBillingPlan(newContext(), "model-a-openai-compact", "req-wildcard-a")
	require.NoError(t, err)
	planB, err := PrepareTaskBillingPlan(newContext(), "model-b-openai-compact", "req-wildcard-b")
	require.NoError(t, err)
	require.Equal(t, relaycommon.TaskBillingKindVideoResolution, planA.Kind())
	require.Equal(t, relaycommon.TaskBillingKindVideoResolution, planB.Kind())
	assert.Equal(t, "model-a-openai-compact", planA.OriginModelName())
	assert.Equal(t, "model-b-openai-compact", planB.OriginModelName())
	assert.Equal(t, "req-wildcard-a", planA.RequestID())
	assert.Equal(t, "req-wildcard-b", planB.RequestID())
	assert.Equal(t, 0.1, mustResolutionPrice(t, planA, "720p"))
	assert.Equal(t, 0.2, mustResolutionPrice(t, planB, "1080p"))

	plainPlan, err := PrepareTaskBillingPlan(newContext(), "model-a", "req-wildcard-plain")
	require.NoError(t, err)
	assert.Equal(t, relaycommon.TaskBillingKindLegacy, plainPlan.Kind())

	sunoContext := newContext()
	sunoContext.Set("platform", string(constant.TaskPlatformSuno))
	sunoPlan, err := PrepareTaskBillingPlan(sunoContext, "model-c-openai-compact", "req-wildcard-suno")
	require.NoError(t, err)
	assert.Equal(t, relaycommon.TaskBillingKindLegacy, sunoPlan.Kind())

	assert.Equal(t, storedBefore, ratio_setting.VideoResolutionPrice2JSONString())
}

// 渠道能力决定分辨率计划的可路由渠道集合，但查询/切换能力不得改动存储的表
// 或已冻结的计划快照；旧版计划保持不受限的历史路由。
func TestTaskChannelCapabilityGatesRoutingWithoutMutatingFrozenPricingState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"*-openai-compact":{"720p":0.1}}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices))
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	plan, err := PrepareTaskBillingPlan(c, "model-capability-openai-compact", "req-capability")
	require.NoError(t, err)
	require.Equal(t, relaycommon.TaskBillingKindVideoResolution, plan.Kind())
	storedBefore := ratio_setting.VideoResolutionPrice2JSONString()

	compatible := CompatibleTaskChannelTypes(plan.Kind())
	assert.Contains(t, compatible, constant.ChannelTypeSora)
	assert.NotContains(t, compatible, constant.ChannelTypeKling)
	assert.Nil(t, CompatibleTaskChannelTypes(relaycommon.TaskBillingKindLegacy))

	assert.Equal(t, storedBefore, ratio_setting.VideoResolutionPrice2JSONString())
	assert.Equal(t, 0.1, mustResolutionPrice(t, plan, "720p"))
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

func TestRelayTaskSubmitSunoUsesLegacyPriceWhenResolutionTableMatches(t *testing.T) {
	base := &taskSubmitTestAdaptor{}
	c, info, deps, state := taskSubmitVideoTestContext(t, base)
	c.Set("platform", string(constant.TaskPlatformSuno))
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"client-model":{"720p":0.9}}`))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.2}`))

	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)

	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.True(t, base.didEstimate)
	assert.True(t, base.didAdjust)
	assert.Equal(t, 980100, state.preConsumedQuota)
	assert.Equal(t, 980100, result.Quota)
	assert.Nil(t, info.ResolvedVideoBilling)
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
