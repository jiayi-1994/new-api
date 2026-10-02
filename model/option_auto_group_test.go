package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOptionValueRejectsInvalidMaxTokenAutoGroups(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "invalid"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("MaxTokenAutoGroups", value))
		})
	}
	require.NoError(t, validateOptionValue("MaxTokenAutoGroups", "999999"))
}

// A unified model leaves video_sales only after no channel lists its public
// name; otherwise that name would route as an alias priced by upstream models.
func TestValidateVideoSalesOptionGuardsRemovalAndAllowsPluginNames(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))
	const previous = `{"video-unified":{"resolutions":{"720p":{"usd_per_second":0.02,"seconds":[15]}}},"retired-video":{"resolutions":{"720p":{"usd_per_second":0.02,"seconds":[15]}}}}`
	require.NoError(t, DB.Save(&Option{Key: billing_setting.VideoSalesOption, Value: previous}).Error)
	channel := &Channel{Name: "unified", Key: "sk-unified", Models: "other-model, Video-Unified", Group: "default"}
	require.NoError(t, DB.Create(channel).Error)
	const pluginKey = "video-sales-option-test"
	_, err := jsplugin.DefaultRegistry.Register(`
export const meta = {apiVersion: 1, key: "video-sales-option-test", name: "Video Sales Option", version: "1.0.0", author: {name: "Test"}, models: ["declared-video"], fetchMode: "per_task"};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() {
		DB.Delete(channel)
		DB.Where(&Option{Key: billing_setting.VideoSalesOption}).Delete(&Option{})
		require.NoError(t, jsplugin.DefaultRegistry.Unregister(pluginKey))
	})

	const kept = `{"video-unified":{"resolutions":{"720p":{"usd_per_second":0.03,"seconds":[15]}}}}`
	require.NoError(t, validateOptionValue(billing_setting.VideoSalesOption, kept), "an unlisted model may be removed and a listed one repriced")
	require.NoError(t, validateOptionValue(billing_setting.VideoSalesOption, `{"Video-Unified":{"disabled":true,"resolutions":{"720p":{"usd_per_second":0.02,"seconds":[15]}}}}`), "disabling keeps the entry")

	err = validateOptionValue(billing_setting.VideoSalesOption, `{}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still listed by a channel")

	err = validateOptionValue(billing_setting.VideoSalesOption, `{"video-unified":{"resolutions":{"720p":{"usd_per_second":0.02,"seconds":[15]}}},"DECLARED-VIDEO":{"resolutions":{"720p":{"usd_per_second":0.02,"seconds":[15]}}}}`)
	require.NoError(t, err, "a real upstream model name may also be the public unified model")

	assert.Error(t, validateOptionValue(billing_setting.VideoSalesOption, `{"video-unified":"not an object"}`))
}

func TestVideoSalesSaveNormalizesResolutionAliases(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))
	previousMap := common.OptionMap
	previousSales := config.GlobalConfig.ExportAllConfigs()
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.OptionMap = previousMap
		require.NoError(t, config.GlobalConfig.LoadFromDB(previousSales))
		DB.Where(&Option{Key: billing_setting.VideoSalesOption}).Delete(&Option{})
	})
	const input = `{"normalized-video":{"resolutions":{"2160p":{"usd_per_second":0.1,"seconds":[5]}}}}`
	const expected = `{"normalized-video":{"resolutions":{"4k":{"usd_per_second":0.1,"seconds":[5]}}}}`
	for _, bulk := range []bool{false, true} {
		var err error
		if bulk {
			err = UpdateOptionsBulk(map[string]string{billing_setting.VideoSalesOption: input})
		} else {
			err = UpdateOption(billing_setting.VideoSalesOption, input)
		}
		require.NoError(t, err)
		var stored Option
		require.NoError(t, DB.Where(&Option{Key: billing_setting.VideoSalesOption}).First(&stored).Error)
		assert.JSONEq(t, expected, stored.Value)
		assert.JSONEq(t, expected, common.OptionMap[billing_setting.VideoSalesOption])
		_, sale, ok := billing_setting.GetVideoSales("normalized-video")
		require.True(t, ok)
		assert.Contains(t, sale.Resolutions, "4k")
		assert.NotContains(t, sale.Resolutions, "2160p")
	}
}
