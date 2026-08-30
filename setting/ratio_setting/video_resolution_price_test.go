package ratio_setting

import (
	"sync/atomic"
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

func TestVideoResolutionPriceRejectsEmptyPerModelTable(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"kept":{"720p":0.1}}`))
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

	err := UpdateVideoResolutionPriceByJSONString(`{"invalid":{}}`)
	require.Error(t, err)
	assert.Equal(t, map[string]map[string]float64{"kept": {"720p": 0.1}}, GetVideoResolutionPriceMap())
}

func TestVideoResolutionPriceRejectsIdenticalRawJSONKeys(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720p":0.1}}`))
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

	err := UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720p":0.2,"720p":0.3}}`)
	assert.Error(t, err)
	assert.Equal(t, map[string]map[string]float64{
		"sora-2": {"720p": 0.1},
	}, GetVideoResolutionPriceMap())
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

func TestVideoResolutionPriceMatchesOnlyExactModelsAndCompactWildcard(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{
		"gemini-2.5-flash-thinking-*":{"720p":0.1},
		"gpt-4o-gizmo-*":{"720p":0.2},
		"*":{"720p":0.25},
		"exact-model":{"720p":0.3},
		"*-openai-compact":{"720p":0.4}
	}`))
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

	assert.False(t, HasVideoResolutionPrice("gemini-2.5-flash-thinking-32768"))
	assert.False(t, HasVideoResolutionPrice("gpt-4o-gizmo-preview"))
	assert.False(t, HasVideoResolutionPrice("ordinary-model"))

	exactPrice, ok := GetVideoResolutionPrice("exact-model", "720p")
	require.True(t, ok)
	assert.Equal(t, 0.3, exactPrice)

	compactPrice, ok := GetVideoResolutionPrice("uncatalogued-openai-compact", "720p")
	require.True(t, ok)
	assert.Equal(t, 0.4, compactPrice)
}

func TestVideoResolutionPriceRejectsBlankModelKeysWithoutReplacingLiveConfig(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"kept":{"720p":0.1}}`))
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}")) })

	for _, value := range []string{
		`{"":{"720p":0.2}}`,
		`{"   ":{"720p":0.2}}`,
	} {
		assert.Error(t, UpdateVideoResolutionPriceByJSONString(value))
		assert.Equal(t, map[string]map[string]float64{"kept": {"720p": 0.1}}, GetVideoResolutionPriceMap())
	}
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

func TestExposedDataInvalidationPreventsStaleRebuildPublication(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720p":0.1}}`))
	InvalidateExposedDataCache()
	realBuilder := buildExposedDataSnapshot
	firstSnapshotBuilt := make(chan struct{})
	releaseFirstSnapshot := make(chan struct{})
	var calls atomic.Int32
	buildExposedDataSnapshot = func() map[string]any {
		data := realBuilder()
		if calls.Add(1) == 1 {
			close(firstSnapshotBuilt)
			<-releaseFirstSnapshot
		}
		return data
	}
	t.Cleanup(func() {
		buildExposedDataSnapshot = realBuilder
		require.NoError(t, UpdateVideoResolutionPriceByJSONString("{}"))
		InvalidateExposedDataCache()
	})

	result := make(chan map[string]any, 1)
	go func() {
		result <- GetExposedData()
	}()
	<-firstSnapshotBuilt
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"sora-2":{"720p":0.2}}`))
	close(releaseFirstSnapshot)

	got := <-result
	prices := got["video_resolution_price"].(map[string]map[string]float64)
	assert.Equal(t, 0.2, prices["sora-2"]["720p"])
	next := GetExposedData()["video_resolution_price"].(map[string]map[string]float64)
	assert.Equal(t, 0.2, next["sora-2"]["720p"])
}
