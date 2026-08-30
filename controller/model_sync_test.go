package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestApplyOfficialModelOverwritePreservesConcurrentIdentityAndPricingRename(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	originalModes, originalExpressions := billing_setting.PricingDocumentsJSON()
	originalDocuments := map[string]string{
		"AudioCompletionRatio":         ratio_setting.AudioCompletionRatio2JSONString(),
		"AudioRatio":                   ratio_setting.AudioRatio2JSONString(),
		"CacheRatio":                   ratio_setting.CacheRatio2JSONString(),
		"CompletionRatio":              ratio_setting.CompletionRatio2JSONString(),
		"CreateCacheRatio":             ratio_setting.CreateCacheRatio2JSONString(),
		"ImageRatio":                   ratio_setting.ImageRatio2JSONString(),
		"ModelPrice":                   ratio_setting.ModelPrice2JSONString(),
		"ModelRatio":                   ratio_setting.ModelRatio2JSONString(),
		"TaskBillingMode":              ratio_setting.TaskBillingMode2JSONString(),
		"VideoResolutionPrice":         ratio_setting.VideoResolutionPrice2JSONString(),
		"billing_setting.billing_expr": originalExpressions,
		"billing_setting.billing_mode": originalModes,
	}
	t.Cleanup(func() { require.NoError(t, model.UpdateOptionsBulk(originalDocuments)) })
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"AudioCompletionRatio":         `{}`,
		"AudioRatio":                   `{}`,
		"CacheRatio":                   `{}`,
		"CompletionRatio":              `{}`,
		"CreateCacheRatio":             `{}`,
		"ImageRatio":                   `{}`,
		"ModelPrice":                   `{"sync-old":1.25}`,
		"ModelRatio":                   `{}`,
		"TaskBillingMode":              `{}`,
		"VideoResolutionPrice":         `{"sync-old":{"720p":0.5}}`,
		"billing_setting.billing_expr": `{}`,
		"billing_setting.billing_mode": `{}`,
	}))

	item := model.Model{ModelName: "sync-old", Description: "local", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	var stale model.Model
	require.NoError(t, db.Where("id = ?", item.Id).First(&stale).Error)

	renamed := stale
	renamed.ModelName = "sync-new"
	result, err := model.ExecuteModelPricingCommand(model.ModelPricingCommand{
		Kind:          model.PricingCommandRename,
		SourceName:    "sync-old",
		TargetName:    "sync-new",
		ModelMutation: &model.ModelRowMutation{Kind: "update", ID: stale.Id, Model: &renamed},
	})
	require.NoError(t, err)
	assert.True(t, result.Committed)
	require.NoError(t, db.Model(&model.Model{}).Where("id = ?", item.Id).Update("status", 0).Error)

	// Poison the transaction-external snapshot to prove the overwrite path
	// cannot persist identity or soft-delete fields from a stale struct.
	stale.DeletedAt = gorm.DeletedAt{Time: time.Unix(123, 0), Valid: true}
	var updated bool
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var overwriteErr error
		updated, overwriteErr = applyOfficialModelOverwrite(tx, &stale, upstreamModel{
			Description: "official",
			ModelName:   "sync-old",
		}, []string{"description", "status"}, 0)
		return overwriteErr
	}))
	assert.True(t, updated)

	var stored model.Model
	require.NoError(t, db.Unscoped().Where("id = ?", item.Id).First(&stored).Error)
	assert.Equal(t, "sync-new", stored.ModelName)
	assert.False(t, stored.DeletedAt.Valid)
	assert.Equal(t, "official", stored.Description)
	assert.Zero(t, stored.Status)

	var priceOption model.Option
	require.NoError(t, db.Where("key = ?", "ModelPrice").First(&priceOption).Error)
	var prices map[string]float64
	require.NoError(t, common.Unmarshal([]byte(priceOption.Value), &prices))
	assert.NotContains(t, prices, "sync-old")
	assert.Equal(t, 1.25, prices["sync-new"])

	var resolutionOption model.Option
	require.NoError(t, db.Where("key = ?", "VideoResolutionPrice").First(&resolutionOption).Error)
	var resolutions map[string]map[string]float64
	require.NoError(t, common.Unmarshal([]byte(resolutionOption.Value), &resolutions))
	assert.NotContains(t, resolutions, "sync-old")
	assert.Equal(t, map[string]float64{"720p": 0.5}, resolutions["sync-new"])
}
