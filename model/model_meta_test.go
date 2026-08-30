package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedLifecycleModel(t *testing.T, name string) *Model {
	t.Helper()
	item := &Model{ModelName: name, Status: 1, SyncOfficial: 1, NameRule: NameRuleExact}
	require.NoError(t, item.Insert())
	return item
}

func assertLifecyclePricingName(t *testing.T, values map[string]string, source, target string) {
	t.Helper()
	fixture := pricingCommandFixture()
	for _, key := range modelPricingNumericOptionKeys {
		document := numericPricingDocument(t, values, key)
		assert.NotContains(t, document, source, key)
		if target != "" {
			expected := numericPricingDocument(t, fixture, key)[source]
			assert.Equal(t, expected, document[target], key)
		}
		assert.Contains(t, document, "unrelated", key)
	}
	for _, key := range modelPricingStringOptionKeys {
		document := stringPricingDocument(t, values, key)
		assert.NotContains(t, document, source, key)
		if target != "" {
			sourceDocument := stringPricingDocument(t, fixture, key)
			if expected, ok := sourceDocument[source]; ok {
				assert.Equal(t, expected, document[target], key)
			} else {
				assert.NotContains(t, document, target, key)
			}
		}
		assert.Contains(t, document, "unrelated", key)
	}
	resolution := resolutionPricingDocument(t, values)
	assert.NotContains(t, resolution, source)
	if target != "" {
		assert.Equal(t, map[string]float64{"720p": 0.1}, resolution[target])
	}
	assert.Contains(t, resolution, "unrelated")
}

func TestModelMetaRenameMovesEveryPricingDocumentAtomically(t *testing.T) {
	setupPricingCommandTest(t)
	seedPricingDocuments(t, pricingCommandFixture())
	item := seedLifecycleModel(t, "owned")

	item.ModelName = "renamed"
	require.NoError(t, item.Update())

	var stored Model
	require.NoError(t, DB.First(&stored, item.Id).Error)
	assert.Equal(t, "renamed", stored.ModelName)
	assertLifecyclePricingName(t, storedPricingDocuments(t), "owned", "renamed")
}

func TestModelMetaSameNameMetadataUpdateDoesNotTouchPricing(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	item := seedLifecycleModel(t, "owned")
	item.Description = "updated metadata"

	require.NoError(t, item.Update())

	assert.Equal(t, fixture, storedPricingDocuments(t))
	var stored Model
	require.NoError(t, DB.First(&stored, item.Id).Error)
	assert.Equal(t, "updated metadata", stored.Description)
}

func TestModelMetaDuplicateRenameRollsBackModelAndEveryPricingDocument(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	source := seedLifecycleModel(t, "owned")
	target := seedLifecycleModel(t, "target")

	source.ModelName = target.ModelName
	err := source.Update()

	var conflict *ModelNameConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, target.Id, conflict.ExistingID)
	var stored Model
	require.NoError(t, DB.First(&stored, source.Id).Error)
	assert.Equal(t, "owned", stored.ModelName)
	assert.Equal(t, fixture, storedPricingDocuments(t))
}

func TestModelMetaDeleteRemovesEveryPricingDocumentAtomically(t *testing.T) {
	setupPricingCommandTest(t)
	seedPricingDocuments(t, pricingCommandFixture())
	item := seedLifecycleModel(t, "owned")

	require.NoError(t, DeleteModelMetaByID(item.Id))

	var count int64
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", item.Id).Count(&count).Error)
	assert.Zero(t, count)
	assertLifecyclePricingName(t, storedPricingDocuments(t), "owned", "")
}

func TestModelMetaRenameRollsBackEveryPricingDocumentWhenModelWriteFails(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	item := seedLifecycleModel(t, "owned")
	const callbackName = "test:model_meta_atomic_update_failure"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Model" {
			tx.AddError(errors.New("forced model update failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callbackName)) })

	item.ModelName = "renamed"
	err := item.Update()

	require.Error(t, err)
	var stored Model
	require.NoError(t, DB.First(&stored, item.Id).Error)
	assert.Equal(t, "owned", stored.ModelName)
	assert.Equal(t, fixture, storedPricingDocuments(t))
}

func TestModelMetaLifecycleMaterializesAllMissingPricingDocuments(t *testing.T) {
	setupPricingCommandTest(t)
	item := seedLifecycleModel(t, "without-options")

	item.ModelName = "renamed-without-options"
	require.NoError(t, item.Update())

	values := storedPricingDocuments(t)
	assert.Len(t, values, len(modelPricingOptionKeys))
	assert.Equal(t, "{}", values[ratio_setting.VideoResolutionPriceOptionKey])
}
