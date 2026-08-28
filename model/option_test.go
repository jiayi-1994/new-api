package model

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
	originalPublisher := publishVideoResolutionPricingSnapshot
	require.NoError(t, ratio_setting.UpdateVideoResolutionPricingSnapshotByJSONString("{}", "{}"))

	t.Cleanup(func() {
		publishVideoResolutionPricingSnapshot = originalPublisher
		require.NoError(t, ratio_setting.UpdateVideoResolutionPricingSnapshotByJSONString("{}", "{}"))
		require.NoError(t, DB.Where("key IN ?", []string{
			ratio_setting.VideoResolutionPriceOptionKey,
			"TaskBillingMode",
		}).Delete(&Option{}).Error)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
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

func TestUpdateTaskBillingModeRejectsInvalidExplicitUnit(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		ratio_setting.VideoResolutionPriceOptionKey: `{"sora-2":{"720p":0.1},"defaulted":{"720p":0.2}}`,
		"TaskBillingMode": `{"sora-2":"per_call"}`,
	}))

	err := UpdateOption("TaskBillingMode", `{"sora-2":"per_minute"}`)
	assert.Error(t, err)

	var stored Option
	require.NoError(t, DB.First(&stored, "key = ?", "TaskBillingMode").Error)
	assert.Equal(t, `{"sora-2":"per_call"}`, stored.Value)
	_, mode, ok := ratio_setting.GetVideoResolutionBillingConfig("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, ratio_setting.TaskBillingModePerCall, mode)
	_, mode, ok = ratio_setting.GetVideoResolutionBillingConfig("defaulted", "720p")
	assert.True(t, ok)
	assert.Equal(t, ratio_setting.TaskBillingModePerSecond, mode)
}

func TestUpdateOptionsBulkPublishesVideoPriceAndUnitTogether(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPricingSnapshotByJSONString(
		`{"sora-2":{"720p":0.1}}`,
		`{"sora-2":"per_second"}`,
	))

	require.NoError(t, UpdateOptionsBulk(map[string]string{
		ratio_setting.VideoResolutionPriceOptionKey: `{"sora-2":{"720p":0.2}}`,
		"TaskBillingMode": `{"sora-2":"per_call"}`,
	}))

	price, mode, ok := ratio_setting.GetVideoResolutionBillingConfig("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.2, price)
	assert.Equal(t, ratio_setting.TaskBillingModePerCall, mode)
	for key, want := range map[string]string{
		ratio_setting.VideoResolutionPriceOptionKey: `{"sora-2":{"720p":0.2}}`,
		"TaskBillingMode": `{"sora-2":"per_call"}`,
	} {
		var stored Option
		require.NoError(t, DB.First(&stored, "key = ?", key).Error)
		assert.Equal(t, want, stored.Value)
	}
}

func TestUpdateOptionsBulkRollsBackVideoPriceAndUnitOnSecondWriteFailure(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	oldPrice := `{"sora-2":{"720p":0.1}}`
	oldMode := `{"sora-2":"per_second"}`
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		ratio_setting.VideoResolutionPriceOptionKey: oldPrice,
		"TaskBillingMode": oldMode,
	}))

	callbackName := "test:fail_second_video_pricing_option_update"
	var writes atomic.Int32
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "options" {
			return
		}
		if writes.Add(1) == 2 {
			tx.AddError(errors.New("forced second option write failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callbackName)) })

	err := UpdateOptionsBulk(map[string]string{
		ratio_setting.VideoResolutionPriceOptionKey: `{"sora-2":{"720p":0.2}}`,
		"TaskBillingMode": `{"sora-2":"per_call"}`,
	})
	assert.ErrorContains(t, err, "forced second option write failure")

	for key, want := range map[string]string{
		ratio_setting.VideoResolutionPriceOptionKey: oldPrice,
		"TaskBillingMode": oldMode,
	} {
		var stored Option
		require.NoError(t, DB.First(&stored, "key = ?", key).Error)
		assert.Equal(t, want, stored.Value)
	}
	price, mode, ok := ratio_setting.GetVideoResolutionBillingConfig("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
	assert.Equal(t, ratio_setting.TaskBillingModePerSecond, mode)
}

func TestLoadOptionsFromDatabasePublishesVideoPriceAndUnitTogether(t *testing.T) {
	setupVideoResolutionOptionTest(t)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPricingSnapshotByJSONString(
		`{"sora-2":{"720p":0.1}}`,
		`{"sora-2":"per_second"}`,
	))
	require.NoError(t, DB.Create(&Option{Key: ratio_setting.VideoResolutionPriceOptionKey, Value: `{"sora-2":{"720p":0.2}}`}).Error)
	require.NoError(t, DB.Create(&Option{Key: "TaskBillingMode", Value: `{"sora-2":"per_call"}`}).Error)

	publishEntered := make(chan struct{})
	releasePublish := make(chan struct{})
	realPublisher := publishVideoResolutionPricingSnapshot
	publishVideoResolutionPricingSnapshot = func(priceJSON, modeJSON string) error {
		close(publishEntered)
		<-releasePublish
		return realPublisher(priceJSON, modeJSON)
	}

	loadDone := make(chan struct{})
	go func() {
		loadOptionsFromDatabase()
		close(loadDone)
	}()
	<-publishEntered

	type observed struct {
		price float64
		mode  string
		ok    bool
	}
	readConcurrently := func() []observed {
		const readers = 16
		results := make([]observed, readers)
		var wg sync.WaitGroup
		wg.Add(readers)
		for i := range results {
			go func(index int) {
				defer wg.Done()
				results[index].price, results[index].mode, results[index].ok = ratio_setting.GetVideoResolutionBillingConfig("sora-2", "720p")
			}(i)
		}
		wg.Wait()
		return results
	}

	for _, got := range readConcurrently() {
		assert.Equal(t, observed{price: 0.1, mode: ratio_setting.TaskBillingModePerSecond, ok: true}, got)
	}
	close(releasePublish)
	<-loadDone
	for _, got := range readConcurrently() {
		assert.Equal(t, observed{price: 0.2, mode: ratio_setting.TaskBillingModePerCall, ok: true}, got)
	}
}
