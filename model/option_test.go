package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupVideoResolutionOptionTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.NoError(t, DB.Where("key IN ?", []string{
		ratio_setting.VideoResolutionPriceOptionKey,
		"TaskBillingMode",
	}).Delete(&Option{}).Error)

	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	originalPublisher := publishVideoResolutionPriceOption
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString("{}"))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString("{}"))
	InvalidatePricingCache()
	ratio_setting.InvalidateExposedDataCache()

	t.Cleanup(func() {
		publishVideoResolutionPriceOption = originalPublisher
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString("{}"))
		require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString("{}"))
		require.NoError(t, DB.Where("key IN ?", []string{
			ratio_setting.VideoResolutionPriceOptionKey,
			"TaskBillingMode",
		}).Delete(&Option{}).Error)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
		InvalidatePricingCache()
		ratio_setting.InvalidateExposedDataCache()
	})
}

func TestUpdateOptionRejectsInvalidVideoResolutionPriceWithoutPersisting(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	require.NoError(t, UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"sora-2":{"720p":0.1}}`))

	err := UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"sora-2":{"720p":0}}`)
	assert.Error(t, err)

	var stored Option
	require.NoError(t, DB.First(&stored, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, `{"sora-2":{"720p":0.1}}`, stored.Value)
	price, ok := ratio_setting.GetVideoResolutionPrice("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
}

func TestVideoResolutionPriceUpdatePublishesSingleProtectedSnapshot(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	const liveMode = `{"sora-2":"per_second"}`
	const storedMode = `{"sora-2":"per_call"}`
	require.NoError(t, DB.Create(&Option{Key: "TaskBillingMode", Value: storedMode}).Error)
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(liveMode))
	common.OptionMapRWMutex.Lock()
	common.OptionMap["TaskBillingMode"] = liveMode
	common.OptionMapRWMutex.Unlock()

	require.NoError(t, UpdateOption(
		ratio_setting.VideoResolutionPriceOptionKey,
		`{"sora-2":{"720p":0.2}}`,
	))

	var taskOption Option
	require.NoError(t, DB.First(&taskOption, "key = ?", "TaskBillingMode").Error)
	assert.Equal(t, storedMode, taskOption.Value)
	assert.Equal(t, map[string]string{"sora-2": ratio_setting.TaskBillingModePerCall}, ratio_setting.GetTaskBillingModeMap())
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, storedMode, common.OptionMap["TaskBillingMode"])
	common.OptionMapRWMutex.RUnlock()
}

func TestTaskBillingModeUpdatePublishesVideoResolutionPriceLast(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	const livePrice = `{"sora-2":{"720p":0.1}}`
	const storedPrice = `{"sora-2":{"720p":0.9}}`
	require.NoError(t, DB.Create(&Option{Key: ratio_setting.VideoResolutionPriceOptionKey, Value: storedPrice}).Error)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(livePrice))
	common.OptionMapRWMutex.Lock()
	common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey] = livePrice
	common.OptionMapRWMutex.Unlock()

	require.NoError(t, UpdateOption("TaskBillingMode", `{"sora-2":"per_call"}`))

	var priceOption Option
	require.NoError(t, DB.First(&priceOption, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, storedPrice, priceOption.Value)
	assert.Equal(t, map[string]map[string]float64{"sora-2": {"720p": 0.9}}, ratio_setting.GetVideoResolutionPriceMap())
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, storedPrice, common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey])
	common.OptionMapRWMutex.RUnlock()
}

func TestLoadOptionsFromDatabasePublishesVideoResolutionPriceIndependently(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	const liveMode = `{"sora-2":"per_call"}`
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(liveMode))
	common.OptionMapRWMutex.Lock()
	common.OptionMap["TaskBillingMode"] = liveMode
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, DB.Create(&Option{
		Key:   ratio_setting.VideoResolutionPriceOptionKey,
		Value: `{"sora-2":{"720p":0.2}}`,
	}).Error)

	loadOptionsFromDatabase()

	price, ok := ratio_setting.GetVideoResolutionPrice("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.2, price)
	assert.Equal(t, map[string]string{"sora-2": ratio_setting.TaskBillingModePerCall}, ratio_setting.GetTaskBillingModeMap())
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, liveMode, common.OptionMap["TaskBillingMode"])
	common.OptionMapRWMutex.RUnlock()
}

func TestLoadOptionsFromDatabaseRejectsInvalidVideoResolutionPriceWithoutPartialPublication(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	const validPrice = `{"sora-2":{"720p":0.1}}`
	require.NoError(t, UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, validPrice))
	validExposed := ratio_setting.GetExposedData()["video_resolution_price"].(map[string]map[string]float64)
	pricingMap = []Pricing{{ModelName: "cached-pricing"}}
	vendorsList = []PricingVendor{{Name: "cached-vendor"}}
	lastGetPricingTime = time.Now()

	require.NoError(t, DB.Model(&Option{}).
		Where(commonKeyCol+" = ?", ratio_setting.VideoResolutionPriceOptionKey).
		Update("value", `{"sora-2":{"720p":0}}`).Error)

	loadOptionsFromDatabase()

	common.OptionMapRWMutex.RLock()
	assert.Equal(t, validPrice, common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey])
	common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, map[string]map[string]float64{"sora-2": {"720p": 0.1}}, ratio_setting.GetVideoResolutionPriceMap())
	assert.Equal(t, validExposed, ratio_setting.GetExposedData()["video_resolution_price"])
	assert.Equal(t, []Pricing{{ModelName: "cached-pricing"}}, pricingMap)
	assert.Equal(t, []PricingVendor{{Name: "cached-vendor"}}, vendorsList)
	assert.False(t, lastGetPricingTime.IsZero())
}

func TestConcurrentVideoResolutionPriceUpdatesPublishLatestDatabaseValue(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	require.NoError(t, UpdateOption(
		ratio_setting.VideoResolutionPriceOptionKey,
		`{"sora-2":{"720p":0.1}}`,
	))
	pricingMap = []Pricing{{ModelName: "stale-price-cache"}}
	vendorsList = []PricingVendor{{Name: "stale-vendor-cache"}}
	lastGetPricingTime = time.Now()

	firstPublishEntered := make(chan struct{})
	releaseFirstPublish := make(chan struct{})
	secondPublishEntered := make(chan struct{})
	realPublisher := publishVideoResolutionPriceOption
	publishVideoResolutionPriceOption = func(priceJSON string) error {
		switch priceJSON {
		case `{"sora-2":{"720p":0.2}}`:
			close(firstPublishEntered)
			<-releaseFirstPublish
		case `{"sora-2":{"720p":0.3}}`:
			close(secondPublishEntered)
		}
		return realPublisher(priceJSON)
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- UpdateOption(
			ratio_setting.VideoResolutionPriceOptionKey,
			`{"sora-2":{"720p":0.2}}`,
		)
	}()
	<-firstPublishEntered

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondDone <- UpdateOptionsBulk(map[string]string{
			ratio_setting.VideoResolutionPriceOptionKey: `{"sora-2":{"720p":0.3}}`,
		})
	}()
	<-secondStarted
	select {
	case <-secondPublishEntered:
		t.Fatal("second price update published while the first publisher held the price-option lock")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirstPublish)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)

	var stored Option
	require.NoError(t, DB.First(&stored, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, `{"sora-2":{"720p":0.3}}`, stored.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, stored.Value, common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey])
	common.OptionMapRWMutex.RUnlock()
	price, ok := ratio_setting.GetVideoResolutionPrice("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.3, price)
	exposed := ratio_setting.GetExposedData()["video_resolution_price"].(map[string]map[string]float64)
	assert.Equal(t, 0.3, exposed["sora-2"]["720p"])
	assert.Empty(t, pricingMap)
	assert.Empty(t, vendorsList)
	assert.True(t, lastGetPricingTime.IsZero())
}

func TestUpdateOptionsBulkRejectsInvalidProtectedDocumentWithoutPersisting(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	require.NoError(t, UpdateOption(
		ratio_setting.VideoResolutionPriceOptionKey,
		`{"sora-2":{"720p":0.1}}`,
	))

	err := UpdateOptionsBulk(map[string]string{
		"TaskBillingMode": `{invalid`,
		ratio_setting.VideoResolutionPriceOptionKey: `{"sora-2":{"720p":0.2}}`,
	})
	assert.Error(t, err)

	var stored Option
	require.NoError(t, DB.First(&stored, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, `{"sora-2":{"720p":0.1}}`, stored.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, stored.Value, common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey])
	common.OptionMapRWMutex.RUnlock()
	price, ok := ratio_setting.GetVideoResolutionPrice("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
	exposed := ratio_setting.GetExposedData()["video_resolution_price"].(map[string]map[string]float64)
	assert.Equal(t, 0.1, exposed["sora-2"]["720p"])
}
