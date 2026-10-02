package model

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
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
func TestValidateVideoSalesOptionGuardsRemovalAndPluginNames(t *testing.T) {
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
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plugin model name")

	assert.Error(t, validateOptionValue(billing_setting.VideoSalesOption, `{"video-unified":"not an object"}`))
}
