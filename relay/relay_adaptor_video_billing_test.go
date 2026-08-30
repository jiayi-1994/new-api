package relay

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompatibleTaskChannelTypesAreDerivedFromResolverInterface(t *testing.T) {
	allowed := CompatibleTaskChannelTypes(relaycommon.TaskBillingKindVideoResolution)
	require.NotEmpty(t, allowed)

	for _, channelType := range allowed {
		adaptor := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channelType)))
		require.NotNil(t, adaptor)
		_, ok := adaptor.(channel.VideoBillingResolver)
		assert.True(t, ok, "channel type %d", channelType)
	}

	assert.False(t, TaskChannelTypeSupportsBilling(relaycommon.TaskBillingKindVideoResolution, constant.ChannelTypeKling))
	assert.True(t, TaskChannelTypeSupportsBilling(relaycommon.TaskBillingKindLegacy, constant.ChannelTypeKling))
}

func TestPrepareTaskBillingPlanFreezesResolutionPlanBeforeChannelSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices))
	})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"resolver-model":{"720p":0.1}}`))

	c, _ := gin.CreateTestContext(nil)
	c.Set(common.RequestIdKey, "request-frozen")
	plan := PrepareTaskBillingPlan(c, "resolver-model", "")
	require.Equal(t, relaycommon.TaskBillingKindVideoResolution, plan.Kind())
	assert.Equal(t, "resolver-model", plan.OriginModelName())
	assert.Equal(t, "request-frozen", plan.RequestID())
	assert.Equal(t, 0.1, mustResolutionPrice(t, plan, "720p"))

	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"resolver-model":{"720p":9}}`))
	assert.Same(t, plan, PrepareTaskBillingPlan(c, "resolver-model", "other-request"))
	assert.Equal(t, 0.1, mustResolutionPrice(t, plan, "720p"))
}

func TestPrepareTaskBillingPlanKeepsSunoModelsLegacy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices))
	})
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"suno_music":{"720p":0.1}}`))

	c, _ := gin.CreateTestContext(nil)
	plan := PrepareTaskBillingPlan(c, "suno_music", "suno-request")

	assert.Equal(t, relaycommon.TaskBillingKindLegacy, plan.Kind())
}

func mustResolutionPrice(t *testing.T, plan *relaycommon.TaskBillingPlan, resolution string) float64 {
	t.Helper()
	price, ok := plan.ResolutionPrice(resolution)
	require.True(t, ok)
	return price
}
