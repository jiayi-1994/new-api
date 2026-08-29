package helper

import (
	"math"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func videoPriceTestContext(t *testing.T) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	originalQuotaPerUnit := common.QuotaPerUnit
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalSpecialRatios := ratio_setting.GroupGroupRatio2JSONString()
	common.QuotaPerUnit = 500
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(originalSpecialRatios))
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "video-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}
	return c, info
}

func TestHasModelBillingConfigIncludesResolutionOnlyModel(t *testing.T) {
	original := ratio_setting.VideoResolutionPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"resolution-only-model":{"720p":0.1}}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(original))
	})

	assert.True(t, HasModelBillingConfig("resolution-only-model"))
	assert.False(t, HasModelBillingConfig("missing-resolution-model"))
}

func TestBuildVideoResolutionPriceDataAlwaysMultipliesDuration(t *testing.T) {
	c, info := videoPriceTestContext(t)
	selection := relaycommon.VideoBillingSelection{
		EffectiveResolution:      "1080p",
		EffectiveDurationSeconds: 8,
		IndependentRatios:        map[string]float64{"video_input": 1.5},
	}

	priceData, clamp, err := BuildVideoResolutionPriceData(c, info, 0.1, selection)

	require.NoError(t, err)
	assert.Nil(t, clamp)
	assert.Equal(t, 600, priceData.Quota)
	assert.Equal(t, 0.1, priceData.ModelPrice)
	assert.True(t, priceData.UsePrice)
	assert.Equal(t, 1.0, priceData.GroupRatioInfo.GroupRatio)
	assert.Equal(t, map[string]float64{"video_input": 1.5}, priceData.OtherRatios())
	require.NotNil(t, info.ResolvedVideoBilling)
	assert.Equal(t, 500.0, info.ResolvedVideoBilling.QuotaPerUnit)
	selection.IndependentRatios["video_input"] = 9
	assert.Equal(t, map[string]float64{"video_input": 1.5}, info.ResolvedVideoBilling.Selection.IndependentRatios)
	assert.Equal(t, 0.1, info.ResolvedVideoBilling.SelectedResolutionPrice)
	common.QuotaPerUnit = 1_000
	assert.Equal(t, 600, priceData.Quota)
}

func TestBuildVideoResolutionPriceDataIgnoresLegacyPerCallMode(t *testing.T) {
	c, info := videoPriceTestContext(t)
	originalModes := ratio_setting.TaskBillingMode2JSONString()
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"video-test-model":"per_call"}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(originalModes)) })

	priceData, clamp, err := BuildVideoResolutionPriceData(c, info, 0.1, relaycommon.VideoBillingSelection{
		EffectiveResolution:      "1080p",
		EffectiveDurationSeconds: 8,
	})

	require.NoError(t, err)
	assert.Nil(t, clamp)
	assert.Equal(t, 400, priceData.Quota)
}

func TestBuildVideoResolutionPriceDataRejectsUnsafeInputs(t *testing.T) {
	tests := []struct {
		name       string
		price      float64
		resolution string
		seconds    int
		ratios     map[string]float64
		wantClamp  bool
	}{
		{name: "empty resolution", price: 0.1, seconds: 1},
		{name: "non canonical resolution", price: 0.1, resolution: "1920x1080", seconds: 1},
		{name: "zero duration", price: 0.1, resolution: "1080p", seconds: 0},
		{name: "negative duration", price: 0.1, resolution: "1080p", seconds: -1},
		{name: "duration above maximum", price: 0.1, resolution: "1080p", seconds: relaycommon.MaxTaskDurationSeconds + 1},
		{name: "unknown independent ratio", price: 0.1, resolution: "1080p", seconds: 1, ratios: map[string]float64{"size": 2}},
		{name: "invalid independent ratio", price: 0.1, resolution: "1080p", seconds: 1, ratios: map[string]float64{"video_input": math.NaN()}},
		{name: "zero price", price: 0, resolution: "1080p", seconds: 1},
		{name: "negative price", price: -0.1, resolution: "1080p", seconds: 1},
		{name: "nan price", price: math.NaN(), resolution: "1080p", seconds: 1},
		{name: "positive infinite price", price: math.Inf(1), resolution: "1080p", seconds: 1},
		{name: "saturated product", price: math.MaxFloat64, resolution: "1080p", seconds: 1, wantClamp: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, info := videoPriceTestContext(t)
			selection := relaycommon.VideoBillingSelection{
				EffectiveResolution:      tc.resolution,
				EffectiveDurationSeconds: tc.seconds,
				IndependentRatios:        tc.ratios,
			}
			priceData, clamp, err := BuildVideoResolutionPriceData(c, info, tc.price, selection)
			if tc.wantClamp {
				require.NoError(t, err)
				require.NotNil(t, clamp)
				assert.Equal(t, common.MaxQuota, priceData.Quota)
				return
			}
			assert.Error(t, err)
			assert.Nil(t, clamp)
			assert.Zero(t, priceData.Quota)
		})
	}
}
