package model

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelMetaResolutionPriceTest(t *testing.T) {
	t.Helper()
	setupVideoResolutionOptionTest(t)
	require.NoError(t, DB.AutoMigrate(&Model{}))
	require.NoError(t, DB.Exec("DELETE FROM models").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM models").Error)
	})
}

func seedResolutionPricedModel(t *testing.T, name string) *Model {
	t.Helper()
	item := &Model{ModelName: name, Status: 1, SyncOfficial: 1, NameRule: NameRuleExact}
	require.NoError(t, item.Insert())
	return item
}

func assertTaskBillingModeUnchanged(t *testing.T, expected string) {
	t.Helper()
	var option Option
	require.NoError(t, DB.First(&option, "key = ?", "TaskBillingMode").Error)
	assert.Equal(t, expected, option.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, expected, common.OptionMap["TaskBillingMode"])
	common.OptionMapRWMutex.RUnlock()
}

func TestModelMetaRenameMovesOnlyVideoResolutionPriceAtomically(t *testing.T) {
	setupModelMetaResolutionPriceTest(t)
	modelMeta := seedResolutionPricedModel(t, "video-old")
	const taskMode = ` { "legacy-model" : "per_call" } `
	require.NoError(t, DB.Create(&Option{Key: "TaskBillingMode", Value: taskMode}).Error)
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(taskMode))
	common.OptionMapRWMutex.Lock()
	common.OptionMap["TaskBillingMode"] = taskMode
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"video-old":{"720p":0.1},"untouched":{"1080p":0.2}}`))

	modelMeta.ModelName = "video-new"
	require.NoError(t, modelMeta.Update())

	var storedModel Model
	require.NoError(t, DB.First(&storedModel, modelMeta.Id).Error)
	assert.Equal(t, "video-new", storedModel.ModelName)
	var storedPrices Option
	require.NoError(t, DB.First(&storedPrices, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.JSONEq(t, `{"video-new":{"720p":0.1},"untouched":{"1080p":0.2}}`, storedPrices.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, storedPrices.Value, common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey])
	common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, map[string]map[string]float64{
		"video-new": {"720p": 0.1},
		"untouched": {"1080p": 0.2},
	}, ratio_setting.GetVideoResolutionPriceMap())
	assertTaskBillingModeUnchanged(t, taskMode)
}

func TestModelMetaDeleteRemovesOnlyVideoResolutionPriceAtomically(t *testing.T) {
	setupModelMetaResolutionPriceTest(t)
	modelMeta := seedResolutionPricedModel(t, "video-delete")
	const taskMode = `{"video-delete":"per_call","legacy":"per_second"}`
	require.NoError(t, DB.Create(&Option{Key: "TaskBillingMode", Value: taskMode}).Error)
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(taskMode))
	common.OptionMapRWMutex.Lock()
	common.OptionMap["TaskBillingMode"] = taskMode
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"video-delete":{"720p":0.1},"untouched":{"1080p":0.2}}`))

	require.NoError(t, DeleteModelMetaByID(modelMeta.Id))

	var count int64
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", modelMeta.Id).Count(&count).Error)
	assert.Zero(t, count)
	var storedPrices Option
	require.NoError(t, DB.First(&storedPrices, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.JSONEq(t, `{"untouched":{"1080p":0.2}}`, storedPrices.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, storedPrices.Value, common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey])
	common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, map[string]map[string]float64{
		"untouched": {"1080p": 0.2},
	}, ratio_setting.GetVideoResolutionPriceMap())
	assertTaskBillingModeUnchanged(t, taskMode)
}

func TestModelMetaResolutionPriceMutationRollsBackWithModelWrite(t *testing.T) {
	setupModelMetaResolutionPriceTest(t)
	modelMeta := seedResolutionPricedModel(t, "video-original")
	const priceDocument = `{"video-original":{"720p":0.1},"untouched":{"1080p":0.2}}`
	require.NoError(t, UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, priceDocument))
	const callbackName = "test:model_meta_update_failure"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Model" {
			tx.AddError(errors.New("forced model update failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callbackName)) })

	modelMeta.ModelName = "video-renamed"
	err := modelMeta.Update()
	require.Error(t, err)

	var storedModel Model
	require.NoError(t, DB.First(&storedModel, modelMeta.Id).Error)
	assert.Equal(t, "video-original", storedModel.ModelName)
	var storedOption Option
	require.NoError(t, DB.First(&storedOption, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, priceDocument, storedOption.Value)
	assert.Equal(t, map[string]map[string]float64{
		"video-original": {"720p": 0.1},
		"untouched":      {"1080p": 0.2},
	}, ratio_setting.GetVideoResolutionPriceMap())
}

func TestModelMetaLifecycleDoesNotOverwriteConcurrentVideoResolutionPriceUpdate(t *testing.T) {
	setupModelMetaResolutionPriceTest(t)
	modelMeta := seedResolutionPricedModel(t, "video-old")
	require.NoError(t, UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"video-old":{"720p":0.1}}`))
	pricingMap = []Pricing{{ModelName: "stale-price-cache"}}
	vendorsList = []PricingVendor{{Name: "stale-vendor-cache"}}
	lastGetPricingTime = time.Now()

	lifecyclePublishEntered := make(chan struct{})
	releaseLifecyclePublish := make(chan struct{})
	normalPublishEntered := make(chan struct{})
	realPublisher := publishVideoResolutionPriceOption
	publishVideoResolutionPriceOption = func(value string) error {
		switch value {
		case `{"video-new":{"720p":0.1}}`:
			close(lifecyclePublishEntered)
			<-releaseLifecyclePublish
		case `{"video-new":{"720p":0.3}}`:
			close(normalPublishEntered)
		}
		return realPublisher(value)
	}

	modelMeta.ModelName = "video-new"
	renameDone := make(chan error, 1)
	go func() { renameDone <- modelMeta.Update() }()
	<-lifecyclePublishEntered

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"video-new":{"720p":0.3}}`)
	}()
	select {
	case <-normalPublishEntered:
		t.Fatal("normal price update published while model lifecycle held the price-option lock")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseLifecyclePublish)
	require.NoError(t, <-renameDone)
	require.NoError(t, <-updateDone)

	var stored Option
	require.NoError(t, DB.First(&stored, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, `{"video-new":{"720p":0.3}}`, stored.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, stored.Value, common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey])
	common.OptionMapRWMutex.RUnlock()
	price, ok := ratio_setting.GetVideoResolutionPrice("video-new", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.3, price)
	exposed := ratio_setting.GetExposedData()["video_resolution_price"].(map[string]map[string]float64)
	assert.Equal(t, 0.3, exposed["video-new"]["720p"])
	assert.Empty(t, pricingMap)
	assert.Empty(t, vendorsList)
	assert.True(t, lastGetPricingTime.IsZero())
}

func TestModelMetaLifecycleCreatesMissingVideoResolutionPriceOption(t *testing.T) {
	setupModelMetaResolutionPriceTest(t)
	modelMeta := seedResolutionPricedModel(t, "video-without-option")

	modelMeta.ModelName = "video-renamed-without-option"
	require.NoError(t, modelMeta.Update())

	var stored Option
	require.NoError(t, DB.First(&stored, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, "{}", stored.Value)
	assert.Empty(t, ratio_setting.GetVideoResolutionPriceMap())
}
