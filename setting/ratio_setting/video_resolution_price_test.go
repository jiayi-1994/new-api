package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvalidResolutionPriceDoesNotReplaceLiveConfig(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720p":0.1}}`))
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

	require.Error(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720P":0.1,"720p":0.2}}`))
	price, ok := GetVideoResolutionPrice("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
}

func TestUpdateVideoResolutionPriceRejectsNonPositiveAndNonFinitePrices(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "zero", value: `{"sora-2":{"720p":0}}`},
		{name: "negative", value: `{"sora-2":{"720p":-0.1}}`},
		{name: "string", value: `{"sora-2":{"720p":"0.1"}}`},
		{name: "nan", value: `{"sora-2":{"720p":NaN}}`},
		{name: "infinity", value: `{"sora-2":{"720p":Infinity}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720p":0.1}}`))
			t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

			assert.Error(t, UpdateVideoResolutionPriceByJSONString(tc.value))
			assert.Equal(t, map[string]map[string]float64{
				"sora-2": {"720p": 0.1},
			}, GetVideoResolutionPriceMap())
		})
	}
}

func TestGetVideoResolutionPriceUsesCompactWildcardModel(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"*-openai-compact":{"720p":0.25}}`))
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

	price, ok := GetVideoResolutionPrice("sora-2-openai-compact", " 720P ")
	assert.True(t, ok)
	assert.Equal(t, 0.25, price)
}

func TestGetVideoResolutionPriceMapReturnsDeepCopy(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720p":0.1}}`))
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

	prices := GetVideoResolutionPriceMap()
	prices["sora-2"]["720p"] = 99
	prices["other"] = map[string]float64{"4k": 88}
	delete(prices, "sora-2")

	assert.Equal(t, map[string]map[string]float64{
		"sora-2": {"720p": 0.1},
	}, GetVideoResolutionPriceMap())
}

func TestVideoResolutionPriceJSONCanonicalizesKeys(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{" 1080P ":0.2,"4K":0.4}}`))
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

	assert.Equal(t, map[string]map[string]float64{
		"sora-2": {"1080p": 0.2, "4k": 0.4},
	}, GetVideoResolutionPriceMap())
	assert.True(t, HasVideoResolutionPrice("sora-2"))
	assert.False(t, HasVideoResolutionPrice("missing"))
}

func TestExposedDataIncludesVideoResolutionPriceCopy(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720p":0.1}}`))
	t.Cleanup(func() {
		require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}"))
		InvalidateExposedDataCache()
	})
	InvalidateExposedDataCache()

	exposed := GetExposedData()
	prices, ok := exposed["video_resolution_price"].(map[string]map[string]float64)
	require.True(t, ok)
	prices["sora-2"]["720p"] = 99
	prices["other"] = map[string]float64{"4k": 88}

	assert.Equal(t, 0.1, GetVideoResolutionPriceMap()["sora-2"]["720p"])
	next := GetExposedData()["video_resolution_price"].(map[string]map[string]float64)
	assert.Equal(t, 0.1, next["sora-2"]["720p"])
	assert.NotContains(t, next, "other")
}
