package model

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func pricingCommandFixture() map[string]string {
	return map[string]string{
		"AudioCompletionRatio": `{"owned":1.1,"target":2.1,"unrelated":3.1}`,
		"AudioRatio":           `{"owned":1.2,"target":2.2,"unrelated":3.2}`,
		"CacheRatio":           `{"owned":1.3,"target":2.3,"unrelated":3.3}`,
		"CompletionRatio":      `{"owned":1.4,"target":2.4,"unrelated":3.4}`,
		"CreateCacheRatio":     `{"owned":1.5,"target":2.5,"unrelated":3.5}`,
		"ImageRatio":           `{"owned":1.6,"target":2.6,"unrelated":3.6}`,
		"ModelPrice":           `{"owned":1.7,"target":2.7,"unrelated":3.7}`,
		"ModelRatio":           `{"owned":1.8,"target":2.8,"unrelated":3.8}`,
		"TaskBillingMode":      `{"owned":"per_call","target":"per_second","unrelated":"per_call"}`,
		ratio_setting.VideoResolutionPriceOptionKey: `{"owned":{"720p":0.1},"target":{"1080p":0.2},"unrelated":{"4k":0.3}}`,
		"billing_setting.billing_expr":              `{"owned":"tier(\"owned\", p * 1)","unrelated":"tier(\"unrelated\", p * 3)"}`,
		"billing_setting.billing_mode":              `{"owned":"tiered_expr","target":"ratio","unrelated":"tiered_expr"}`,
	}
}

func setupPricingCommandTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Option{}, &Model{}))
	require.NoError(t, DB.Where(commonKeyCol+" IN ?", modelPricingOptionKeys).Delete(&Option{}).Error)
	require.NoError(t, DB.Unscoped().Where("1 = 1").Delete(&Model{}).Error)
	originalPricingValues := currentPricingOptionDefaults()

	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	originalPublisher := publishPricingDocumentsAfterCommit

	t.Cleanup(func() {
		publishPricingDocumentsAfterCommit = originalPublisher
		require.NoError(t, publishPricingDocumentsLowLevel(originalPricingValues))
		require.NoError(t, DB.Where(commonKeyCol+" IN ?", modelPricingOptionKeys).Delete(&Option{}).Error)
		require.NoError(t, DB.Unscoped().Where("1 = 1").Delete(&Model{}).Error)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})
}

func seedPricingDocuments(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range modelPricingOptionKeys {
		value, ok := values[key]
		require.True(t, ok, "fixture missing %s", key)
		require.NoError(t, DB.Create(&Option{Key: key, Value: value}).Error)
	}
	require.NoError(t, publishPricingDocumentsLowLevel(values))
}

func storedPricingDocuments(t *testing.T) map[string]string {
	t.Helper()
	var options []Option
	require.NoError(t, DB.Where(commonKeyCol+" IN ?", modelPricingOptionKeys).Find(&options).Error)
	values := make(map[string]string, len(options))
	for _, option := range options {
		values[option.Key] = option.Value
	}
	return values
}

func numericPricingDocument(t *testing.T, values map[string]string, key string) map[string]float64 {
	t.Helper()
	var document map[string]float64
	require.NoError(t, common.Unmarshal([]byte(values[key]), &document))
	return document
}

func stringPricingDocument(t *testing.T, values map[string]string, key string) map[string]string {
	t.Helper()
	var document map[string]string
	require.NoError(t, common.Unmarshal([]byte(values[key]), &document))
	return document
}

func resolutionPricingDocument(t *testing.T, values map[string]string) map[string]map[string]float64 {
	t.Helper()
	var document map[string]map[string]float64
	require.NoError(t, common.Unmarshal([]byte(values[ratio_setting.VideoResolutionPriceOptionKey]), &document))
	return document
}

func TestExecuteModelPricingCommandResolutionSavePreservesLegacy(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)

	result, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "target",
		Selection: &ModelPricingSelection{
			Mode:             PricingModeVideoResolution,
			ResolutionPrices: map[string]float64{"720p": 0.4},
		},
	})
	require.NoError(t, err)
	assert.True(t, result.Committed)

	stored := storedPricingDocuments(t)
	for _, key := range modelPricingOptionKeys {
		if key == ratio_setting.VideoResolutionPriceOptionKey {
			continue
		}
		assert.JSONEq(t, fixture[key], stored[key], key)
	}
	assert.Equal(t, map[string]float64{"720p": 0.4}, resolutionPricingDocument(t, stored)["target"])
	assert.Equal(t, map[string]float64{"720p": 0.1}, resolutionPricingDocument(t, stored)["owned"])
	assert.Equal(t, map[string]float64{"4k": 0.3}, resolutionPricingDocument(t, stored)["unrelated"])
}

func TestExecuteModelPricingCommandReactivatesResolutionAfterFixedSave(t *testing.T) {
	setupPricingCommandTest(t)
	seedPricingDocuments(t, pricingCommandFixture())
	fixedPrice := 0.75
	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "target",
		Selection:  &ModelPricingSelection{Mode: PricingModeFixed, ModelPrice: &fixedPrice},
	})
	require.NoError(t, err)
	assert.NotContains(t, resolutionPricingDocument(t, storedPricingDocuments(t)), "target")

	_, err = ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "target",
		Selection: &ModelPricingSelection{
			Mode:             PricingModeVideoResolution,
			ResolutionPrices: map[string]float64{"1080p": 0.8},
		},
	})
	require.NoError(t, err)
	stored := storedPricingDocuments(t)
	assert.Equal(t, 0.75, numericPricingDocument(t, stored, "ModelPrice")["target"])
	assert.Equal(t, map[string]float64{"1080p": 0.8}, resolutionPricingDocument(t, stored)["target"])
}

func TestExecuteModelPricingCommandRenameMovesOwnedEntriesOnly(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)

	result, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandRename,
		SourceName: "owned",
		TargetName: "target",
	})
	require.NoError(t, err)
	assert.True(t, result.Committed)
	stored := storedPricingDocuments(t)
	for _, key := range []string{
		"AudioCompletionRatio", "AudioRatio", "CacheRatio", "CompletionRatio",
		"CreateCacheRatio", "ImageRatio", "ModelPrice", "ModelRatio",
	} {
		document := numericPricingDocument(t, stored, key)
		assert.NotContains(t, document, "owned", key)
		assert.Equal(t, numericPricingDocument(t, fixture, key)["owned"], document["target"], key)
		assert.Equal(t, numericPricingDocument(t, fixture, key)["unrelated"], document["unrelated"], key)
	}
	for _, key := range []string{"TaskBillingMode", "billing_setting.billing_mode"} {
		document := stringPricingDocument(t, stored, key)
		assert.NotContains(t, document, "owned", key)
		assert.Equal(t, stringPricingDocument(t, fixture, key)["owned"], document["target"], key)
	}
	expressions := stringPricingDocument(t, stored, "billing_setting.billing_expr")
	assert.NotContains(t, expressions, "owned")
	assert.Equal(t, stringPricingDocument(t, fixture, "billing_setting.billing_expr")["owned"], expressions["target"])
	assert.Contains(t, expressions, "unrelated")
	resolutions := resolutionPricingDocument(t, stored)
	assert.NotContains(t, resolutions, "owned")
	assert.Equal(t, map[string]float64{"720p": 0.1}, resolutions["target"])
}

func TestExecuteModelPricingCommandRenameKeepsTargetWhereSourceIsAbsent(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	fixture["billing_setting.billing_expr"] = `{"target":"tier(\"target\", p * 2)","unrelated":"tier(\"unrelated\", p * 3)"}`
	fixture["billing_setting.billing_mode"] = `{"owned":"ratio","target":"ratio","unrelated":"tiered_expr"}`
	seedPricingDocuments(t, fixture)

	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandRename,
		SourceName: "owned",
		TargetName: "target",
	})
	require.NoError(t, err)
	assert.Equal(t, "tier(\"target\", p * 2)", stringPricingDocument(t, storedPricingDocuments(t), "billing_setting.billing_expr")["target"])
}

func TestExecuteModelPricingCommandCopyReplacesTargetAcrossAllDocuments(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)

	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandCopy,
		SourceName: "owned",
		TargetName: "target",
	})
	require.NoError(t, err)
	stored := storedPricingDocuments(t)
	for _, key := range []string{
		"AudioCompletionRatio", "AudioRatio", "CacheRatio", "CompletionRatio",
		"CreateCacheRatio", "ImageRatio", "ModelPrice", "ModelRatio",
	} {
		document := numericPricingDocument(t, stored, key)
		assert.Equal(t, document["owned"], document["target"], key)
	}
	assert.Equal(t, stringPricingDocument(t, stored, "TaskBillingMode")["owned"], stringPricingDocument(t, stored, "TaskBillingMode")["target"])
	assert.Equal(t, stringPricingDocument(t, stored, "billing_setting.billing_mode")["owned"], stringPricingDocument(t, stored, "billing_setting.billing_mode")["target"])
	assert.Equal(t, stringPricingDocument(t, stored, "billing_setting.billing_expr")["owned"], stringPricingDocument(t, stored, "billing_setting.billing_expr")["target"])
	assert.Equal(t, resolutionPricingDocument(t, stored)["owned"], resolutionPricingDocument(t, stored)["target"])
}

func TestExecuteModelPricingCommandCopyDeletesTargetWhereSourceIsAbsent(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	fixture["billing_setting.billing_expr"] = `{"target":"tier(\"target\", p * 2)","unrelated":"tier(\"unrelated\", p * 3)"}`
	fixture["billing_setting.billing_mode"] = `{"owned":"ratio","target":"ratio","unrelated":"tiered_expr"}`
	seedPricingDocuments(t, fixture)

	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandCopy,
		SourceName: "owned",
		TargetName: "target",
	})
	require.NoError(t, err)
	assert.NotContains(t, stringPricingDocument(t, storedPricingDocuments(t), "billing_setting.billing_expr"), "target")
}

func TestExecuteModelPricingCommandDeleteRemovesEveryEntry(t *testing.T) {
	setupPricingCommandTest(t)
	seedPricingDocuments(t, pricingCommandFixture())

	_, err := ExecuteModelPricingCommand(ModelPricingCommand{Kind: PricingCommandDelete, TargetName: "owned"})
	require.NoError(t, err)
	stored := storedPricingDocuments(t)
	for _, key := range modelPricingOptionKeys {
		if key == ratio_setting.VideoResolutionPriceOptionKey {
			assert.NotContains(t, resolutionPricingDocument(t, stored), "owned", key)
		} else if key == "TaskBillingMode" || key == "billing_setting.billing_expr" || key == "billing_setting.billing_mode" {
			assert.NotContains(t, stringPricingDocument(t, stored, key), "owned", key)
		} else {
			assert.NotContains(t, numericPricingDocument(t, stored, key), "owned", key)
		}
	}
}

func TestExecuteModelPricingCommandMaterializesMissingDocumentsFromLiveDefaults(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	require.NoError(t, publishPricingDocumentsLowLevel(fixture))

	price := 9.5
	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "new-model",
		Selection:  &ModelPricingSelection{Mode: PricingModeFixed, ModelPrice: &price},
	})
	require.NoError(t, err)
	stored := storedPricingDocuments(t)
	assert.Len(t, stored, len(modelPricingOptionKeys))
	assert.Equal(t, 9.5, numericPricingDocument(t, stored, "ModelPrice")["new-model"])
	assert.Equal(t, 3.8, numericPricingDocument(t, stored, "ModelRatio")["unrelated"])
	assert.Equal(t, map[string]float64{"720p": 0.1}, resolutionPricingDocument(t, stored)["owned"])
}

func TestExecuteModelPricingCommandRollsBackDocumentsWhenModelMutationFails(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	price := 0.5
	callbackName := "pricing-command-model-create-failure-" + t.Name()
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Model" {
			tx.AddError(errors.New("injected model mutation failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Create().Remove(callbackName)) })

	result, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "owned",
		Selection:  &ModelPricingSelection{Mode: PricingModeFixed, ModelPrice: &price},
		ModelMutation: &ModelRowMutation{
			Kind:  "create",
			Model: &Model{ModelName: "owned"},
		},
	})
	assert.Error(t, err)
	assert.False(t, result.Committed)
	stored := storedPricingDocuments(t)
	for _, key := range modelPricingOptionKeys {
		assert.JSONEq(t, fixture[key], stored[key], key)
	}
}

func TestExecuteModelPricingCommandUpdatesModelRowInRenameTransaction(t *testing.T) {
	setupPricingCommandTest(t)
	seedPricingDocuments(t, pricingCommandFixture())
	row := Model{ModelName: "owned", Status: 1, SyncOfficial: 1}
	require.NoError(t, DB.Create(&row).Error)
	row.ModelName = "target"
	row.Description = "renamed"

	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:          PricingCommandRename,
		SourceName:    "owned",
		TargetName:    "target",
		ModelMutation: &ModelRowMutation{Kind: "update", Model: &row, ID: row.Id},
	})
	require.NoError(t, err)
	var storedModel Model
	require.NoError(t, DB.First(&storedModel, row.Id).Error)
	assert.Equal(t, "target", storedModel.ModelName)
	assert.Equal(t, "renamed", storedModel.Description)
	assert.NotContains(t, numericPricingDocument(t, storedPricingDocuments(t), "ModelPrice"), "owned")
}

func TestExecuteModelPricingCommandRejectsDeleteMutationForDifferentModel(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	owned := Model{ModelName: "owned", Status: 1, SyncOfficial: 1}
	other := Model{ModelName: "other", Status: 1, SyncOfficial: 1}
	require.NoError(t, DB.Create(&owned).Error)
	require.NoError(t, DB.Create(&other).Error)

	result, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:          PricingCommandDelete,
		TargetName:    "owned",
		ModelMutation: &ModelRowMutation{Kind: "delete", ID: other.Id},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "other")
	assert.False(t, result.Committed)
	var count int64
	require.NoError(t, DB.Model(&Model{}).Where("id IN ?", []int{owned.Id, other.Id}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	for _, key := range modelPricingOptionKeys {
		assert.JSONEq(t, fixture[key], storedPricingDocuments(t)[key], key)
	}
}

func TestExecuteModelPricingCommandRejectsRenameMutationWithDifferentTarget(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	row := Model{ModelName: "owned", Status: 1, SyncOfficial: 1}
	require.NoError(t, DB.Create(&row).Error)
	row.ModelName = "different-target"

	result, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:          PricingCommandRename,
		SourceName:    "owned",
		TargetName:    "target",
		ModelMutation: &ModelRowMutation{Kind: "update", Model: &row, ID: row.Id},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "different-target")
	assert.False(t, result.Committed)
	var stored Model
	require.NoError(t, DB.First(&stored, row.Id).Error)
	assert.Equal(t, "owned", stored.ModelName)
	for _, key := range modelPricingOptionKeys {
		assert.JSONEq(t, fixture[key], storedPricingDocuments(t)[key], key)
	}
}

func TestExecuteModelPricingCommandLocksModelBeforePricingDocuments(t *testing.T) {
	setupPricingCommandTest(t)
	seedPricingDocuments(t, pricingCommandFixture())
	row := Model{ModelName: "owned", Status: 1, SyncOfficial: 1}
	require.NoError(t, DB.Create(&row).Error)
	row.ModelName = "target"

	callbackName := "pricing-command-query-order-" + t.Name()
	tables := make([]string, 0, len(modelPricingOptionKeys)+1)
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		tables = append(tables, tx.Statement.Table)
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Query().Remove(callbackName)) })

	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:          PricingCommandRename,
		SourceName:    "owned",
		TargetName:    "target",
		ModelMutation: &ModelRowMutation{Kind: "update", Model: &row, ID: row.Id},
	})
	require.NoError(t, err)
	modelQuery := -1
	optionQuery := -1
	for index, table := range tables {
		if table == "models" && modelQuery == -1 {
			modelQuery = index
		}
		if table == "options" && optionQuery == -1 {
			optionQuery = index
		}
	}
	require.NotEqual(t, -1, modelQuery)
	require.NotEqual(t, -1, optionQuery)
	assert.Less(t, modelQuery, optionQuery, "model row must be locked before option rows to match retained lifecycle writers")
}

func TestExecuteModelPricingCommandRejectsActiveTargetNameConflict(t *testing.T) {
	tests := []struct {
		name          string
		command       func(source Model) ModelPricingCommand
		expectsSource bool
	}{
		{
			name: "rename",
			command: func(source Model) ModelPricingCommand {
				updated := source
				updated.ModelName = "target"
				return ModelPricingCommand{
					Kind:          PricingCommandRename,
					SourceName:    "owned",
					TargetName:    "target",
					ModelMutation: &ModelRowMutation{Kind: "update", ID: source.Id, Model: &updated},
				}
			},
			expectsSource: true,
		},
		{
			name: "create save",
			command: func(Model) ModelPricingCommand {
				price := 4.25
				return ModelPricingCommand{
					Kind:       PricingCommandSave,
					TargetName: "target",
					Selection:  &ModelPricingSelection{Mode: PricingModeFixed, ModelPrice: &price},
					ModelMutation: &ModelRowMutation{
						Kind:  "create",
						Model: &Model{ModelName: "target", Status: 1, SyncOfficial: 1},
					},
				}
			},
		},
		{
			name: "create copy",
			command: func(Model) ModelPricingCommand {
				return ModelPricingCommand{
					Kind:       PricingCommandCopy,
					SourceName: "owned",
					TargetName: "target",
					ModelMutation: &ModelRowMutation{
						Kind:  "create",
						Model: &Model{ModelName: "target", Status: 1, SyncOfficial: 1},
					},
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupPricingCommandTest(t)
			fixture := pricingCommandFixture()
			seedPricingDocuments(t, fixture)

			source := Model{ModelName: "owned", Status: 1, SyncOfficial: 1}
			if test.expectsSource {
				require.NoError(t, DB.Create(&source).Error)
			}
			target := Model{ModelName: "target", Status: 1, SyncOfficial: 1}
			require.NoError(t, DB.Create(&target).Error)

			result, err := ExecuteModelPricingCommand(test.command(source))
			var conflict *ModelNameConflictError
			require.ErrorAs(t, err, &conflict)
			assert.Equal(t, target.Id, conflict.ExistingID)
			assert.False(t, result.Committed)

			var targetCount int64
			require.NoError(t, DB.Model(&Model{}).Where("model_name = ?", "target").Count(&targetCount).Error)
			assert.EqualValues(t, 1, targetCount)
			if test.expectsSource {
				var storedSource Model
				require.NoError(t, DB.First(&storedSource, source.Id).Error)
				assert.Equal(t, "owned", storedSource.ModelName)
			}
			stored := storedPricingDocuments(t)
			for _, key := range modelPricingOptionKeys {
				assert.JSONEq(t, fixture[key], stored[key], key)
			}
		})
	}
}

func TestExecuteModelPricingCommandCreatesModelBeforeLockingPricingDocuments(t *testing.T) {
	setupPricingCommandTest(t)
	seedPricingDocuments(t, pricingCommandFixture())
	price := 4.25

	events := make([]string, 0, len(modelPricingOptionKeys)+1)
	queryCallback := "pricing-command-create-query-order-" + t.Name()
	createCallback := "pricing-command-create-row-order-" + t.Name()
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "options" {
			events = append(events, "option-lock")
		}
	}))
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(createCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "models" {
			events = append(events, "model-create")
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(queryCallback))
		require.NoError(t, DB.Callback().Create().Remove(createCallback))
	})

	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "new-model",
		Selection:  &ModelPricingSelection{Mode: PricingModeFixed, ModelPrice: &price},
		ModelMutation: &ModelRowMutation{
			Kind:  "create",
			Model: &Model{ModelName: "new-model", Status: 1, SyncOfficial: 1},
		},
	})
	require.NoError(t, err)

	modelCreate := -1
	optionLock := -1
	for index, event := range events {
		if event == "model-create" && modelCreate == -1 {
			modelCreate = index
		}
		if event == "option-lock" && optionLock == -1 {
			optionLock = index
		}
	}
	require.NotEqual(t, -1, modelCreate)
	require.NotEqual(t, -1, optionLock)
	assert.Less(t, modelCreate, optionLock, "create must reserve the model name before pricing option locks")
}

func TestExecuteModelPricingCommandRollsBackEarlyModelCreateWhenPricingValidationFails(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)

	result, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "invalid-new-model",
		Selection:  &ModelPricingSelection{Mode: PricingModeFixed},
		ModelMutation: &ModelRowMutation{
			Kind:  "create",
			Model: &Model{ModelName: "invalid-new-model", Status: 1, SyncOfficial: 1},
		},
	})
	require.Error(t, err)
	assert.False(t, result.Committed)
	var count int64
	require.NoError(t, DB.Model(&Model{}).Where("model_name = ?", "invalid-new-model").Count(&count).Error)
	assert.Zero(t, count)
	stored := storedPricingDocuments(t)
	for _, key := range modelPricingOptionKeys {
		assert.JSONEq(t, fixture[key], stored[key], key)
	}
}

func TestInsertModelWithActiveNameGuardSerializesAbsentTargetAcrossConnections(t *testing.T) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "models.db")) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	firstDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	secondDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, firstDB.AutoMigrate(&Model{}))
	for _, db := range []*gorm.DB{firstDB, secondDB} {
		sqlDB, sqlErr := db.DB()
		require.NoError(t, sqlErr)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	for index, db := range []*gorm.DB{firstDB, secondDB} {
		callbackName := fmt.Sprintf("active-name-read-%d-%s", index, t.Name())
		require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "models" {
				select {
				case <-release:
					return
				default:
				}
				ready <- struct{}{}
				<-release
			}
		}))
		t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })
	}

	results := make(chan error, 2)
	go func() {
		results <- insertModelWithActiveNameGuard(firstDB, &Model{ModelName: "concurrent", Status: 1, SyncOfficial: 1})
	}()
	go func() {
		results <- insertModelWithActiveNameGuard(secondDB, &Model{ModelName: "concurrent", Status: 1, SyncOfficial: 1})
	}()
	<-ready
	<-ready
	close(release)

	firstErr := <-results
	secondErr := <-results
	successes := 0
	if firstErr == nil {
		successes++
	}
	if secondErr == nil {
		successes++
	}
	assert.Equal(t, 1, successes, "serializable absent-name reads must allow at most one creator to commit")
	var count int64
	require.NoError(t, firstDB.Model(&Model{}).Where("model_name = ?", "concurrent").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestModelNameMutationLocksExistingRowsInStableIDOrder(t *testing.T) {
	setupPricingCommandTest(t)
	low := Model{ModelName: "lock-low", Status: 1, SyncOfficial: 1}
	high := Model{ModelName: "lock-high", Status: 1, SyncOfficial: 1}
	require.NoError(t, DB.Create(&low).Error)
	require.NoError(t, DB.Create(&high).Error)
	require.Less(t, low.Id, high.Id)

	callbackName := "model-name-stable-lock-order-" + t.Name()
	type queryPhase struct {
		insideTransaction bool
		kind              string
		rowID             int
	}
	queryPhases := make([]queryPhase, 0, 6)
	lockedIDs := make([]int, 0, 4)
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "models" {
			return
		}
		_, insideTransaction := tx.Statement.ConnPool.(*sql.Tx)
		switch destination := tx.Statement.Dest.(type) {
		case *[]int:
			queryPhases = append(queryPhases, queryPhase{insideTransaction: insideTransaction, kind: "candidate-discovery"})
		case *Model:
			if destination.Id != low.Id && destination.Id != high.Id {
				return
			}
			queryPhases = append(queryPhases, queryPhase{insideTransaction: insideTransaction, kind: "point-lock", rowID: destination.Id})
			if insideTransaction {
				lockedIDs = append(lockedIDs, destination.Id)
			}
		default:
			queryPhases = append(queryPhases, queryPhase{insideTransaction: insideTransaction, kind: "other-model-read"})
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Query().Remove(callbackName)) })

	for _, mutation := range []struct {
		sourceID   int
		sourceName string
		targetName string
		command    bool
	}{
		{sourceID: high.Id, sourceName: high.ModelName, targetName: low.ModelName},
		{sourceID: low.Id, sourceName: low.ModelName, targetName: high.ModelName, command: true},
	} {
		updated := Model{Id: mutation.sourceID, ModelName: mutation.targetName, Status: 1, SyncOfficial: 1}
		var err error
		if mutation.command {
			_, err = ExecuteModelPricingCommand(ModelPricingCommand{
				Kind:          PricingCommandRename,
				SourceName:    mutation.sourceName,
				TargetName:    mutation.targetName,
				ModelMutation: &ModelRowMutation{Kind: "update", ID: mutation.sourceID, Model: &updated},
			})
		} else {
			err = updated.Update()
		}
		var conflict *ModelNameConflictError
		require.ErrorAs(t, err, &conflict)
	}

	discoveries := 0
	for _, phase := range queryPhases {
		if phase.kind == "candidate-discovery" {
			discoveries++
			assert.False(t, phase.insideTransaction, "candidate discovery must precede the serializable transaction")
		}
		if phase.insideTransaction {
			assert.Equal(t, "point-lock", phase.kind, "the transaction may not read the model table before stable point locks")
		}
	}
	assert.Equal(t, 2, discoveries, "each transaction attempt must resolve target candidates afresh")
	assert.Equal(t, []int{low.Id, high.Id, low.Id, high.Id}, lockedIDs,
		"opposite renames must issue primary-key locking reads in the same explicit order")
}

func TestModelUpdateRejectsBlankNameWithoutMovingResolutionPricing(t *testing.T) {
	for _, invalidName := range []string{"", "   "} {
		t.Run(fmt.Sprintf("name_%q", invalidName), func(t *testing.T) {
			setupPricingCommandTest(t)
			item := Model{ModelName: "legacy-update-name", Description: "before", Status: 1, SyncOfficial: 1}
			require.NoError(t, item.Insert())
			require.NoError(t, UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"legacy-update-name":{"720p":0.5}}`))

			item.ModelName = invalidName
			item.Description = "after"
			err := item.Update()
			require.Error(t, err)
			assert.ErrorContains(t, err, "model name is required")

			var stored Model
			require.NoError(t, DB.Where("id = ?", item.Id).First(&stored).Error)
			assert.Equal(t, "legacy-update-name", stored.ModelName)
			assert.Equal(t, "before", stored.Description)

			var option Option
			require.NoError(t, DB.Where("key = ?", ratio_setting.VideoResolutionPriceOptionKey).First(&option).Error)
			var prices map[string]map[string]float64
			require.NoError(t, common.Unmarshal([]byte(option.Value), &prices))
			assert.Equal(t, map[string]float64{"720p": 0.5}, prices["legacy-update-name"])
			assert.NotContains(t, prices, invalidName)
		})
	}
}

func TestExecuteModelPricingCommandRejectsBlankMutationNamesAndRollsBack(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		setupPricingCommandTest(t)
		fixture := pricingCommandFixture()
		seedPricingDocuments(t, fixture)
		price := 1.25
		created := Model{ModelName: "   ", Status: 1, SyncOfficial: 1}

		_, err := ExecuteModelPricingCommand(ModelPricingCommand{
			Kind:          PricingCommandSave,
			TargetName:    "   ",
			Selection:     &ModelPricingSelection{Mode: PricingModeFixed, ModelPrice: &price},
			ModelMutation: &ModelRowMutation{Kind: "create", Model: &created},
		})
		require.Error(t, err)
		assert.ErrorContains(t, err, "model name is required")
		var count int64
		require.NoError(t, DB.Model(&Model{}).Count(&count).Error)
		assert.Zero(t, count)
		stored := storedPricingDocuments(t)
		for key, expected := range fixture {
			assert.JSONEq(t, expected, stored[key], key)
		}
	})

	t.Run("rename", func(t *testing.T) {
		setupPricingCommandTest(t)
		fixture := pricingCommandFixture()
		seedPricingDocuments(t, fixture)
		item := Model{ModelName: "owned", Status: 1, SyncOfficial: 1}
		require.NoError(t, item.Insert())
		renamed := item
		renamed.ModelName = "   "

		_, err := ExecuteModelPricingCommand(ModelPricingCommand{
			Kind:          PricingCommandRename,
			SourceName:    "owned",
			TargetName:    "   ",
			ModelMutation: &ModelRowMutation{Kind: "update", ID: item.Id, Model: &renamed},
		})
		require.Error(t, err)
		assert.ErrorContains(t, err, "model name is required")
		var stored Model
		require.NoError(t, DB.Where("id = ?", item.Id).First(&stored).Error)
		assert.Equal(t, "owned", stored.ModelName)
		storedDocuments := storedPricingDocuments(t)
		for key, expected := range fixture {
			assert.JSONEq(t, expected, storedDocuments[key], key)
		}
	})
}

func TestPricingOptionMaterializationDefaultsUsePairedBillingSnapshot(t *testing.T) {
	setupPricingCommandTest(t)

	oldModes := `{"paired-default":"ratio"}`
	oldExpressions := `{"paired-default":"tier(\"old\", p * 1)"}`
	newModes := `{"paired-default":"tiered_expr"}`
	newExpressions := `{"paired-default":"tier(\"new\", p * 2)"}`
	require.NoError(t, billing_setting.UpdatePricingDocuments(oldModes, oldExpressions))

	callbackName := "publish-between-billing-option-materialization-" + t.Name()
	var publishErr error
	require.NoError(t, DB.Callback().Create().After("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		option, ok := tx.Statement.Dest.(*Option)
		if ok && option.Key == "billing_setting.billing_expr" {
			publishErr = billing_setting.UpdatePricingDocuments(newModes, newExpressions)
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Create().Remove(callbackName)) })

	var documents *PricingDocuments
	require.NoError(t, modelNamespaceTransaction(DB, func(tx *gorm.DB) error {
		var err error
		documents, err = lockPricingDocuments(tx)
		return err
	}))
	require.NoError(t, publishErr)

	var modes map[string]string
	var expressions map[string]string
	require.NoError(t, common.Unmarshal([]byte(documents.Raw["billing_setting.billing_mode"]), &modes))
	require.NoError(t, common.Unmarshal([]byte(documents.Raw["billing_setting.billing_expr"]), &expressions))
	assert.Equal(t, billing_setting.BillingModeRatio, modes["paired-default"])
	assert.Equal(t, `tier("old", p * 1)`, expressions["paired-default"])
}

func TestUpdateOptionCASUsesExactRawDocument(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	fixture["ModelPrice"] = "{\n  \"owned\": 1.7\n}"
	seedPricingDocuments(t, fixture)

	err := UpdateOptionCAS("ModelPrice", `{"owned":2}`, `{"owned":1.7}`)
	var conflict *OptionConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "ModelPrice", conflict.Key)
	assert.Equal(t, fixture["ModelPrice"], conflict.CurrentValue)

	require.NoError(t, UpdateOptionCAS("ModelPrice", `{"owned":2}`, fixture["ModelPrice"]))
	assert.Equal(t, `{"owned":2}`, storedPricingDocuments(t)["ModelPrice"])
}

func TestUpdateOptionsBulkCASRollsBackOnConflict(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)

	err := UpdateOptionsBulkCAS(
		map[string]string{"ModelPrice": `{"owned":9}`, "ModelRatio": `{"owned":8}`},
		map[string]string{"ModelPrice": fixture["ModelPrice"], "ModelRatio": `{"stale":1}`},
	)
	var conflict *OptionConflictError
	require.ErrorAs(t, err, &conflict)
	stored := storedPricingDocuments(t)
	assert.JSONEq(t, fixture["ModelPrice"], stored["ModelPrice"])
	assert.JSONEq(t, fixture["ModelRatio"], stored["ModelRatio"])
}

func TestUpdateOptionCASRejectsTieredModeWithoutExpression(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)

	err := UpdateOptionCAS(
		"billing_setting.billing_mode",
		`{"owned":"tiered_expr","missing":"tiered_expr"}`,
		fixture["billing_setting.billing_mode"],
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
	assert.JSONEq(t, fixture["billing_setting.billing_mode"], storedPricingDocuments(t)["billing_setting.billing_mode"])
}

func TestPricingPublicationRecoversAfterCommittedPublisherFailure(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	price := 8.25
	publishPricingDocumentsAfterCommit = func(map[string]string) error {
		return errors.New("injected post-commit failure")
	}

	result, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "owned",
		Selection:  &ModelPricingSelection{Mode: PricingModeFixed, ModelPrice: &price},
	})
	require.NoError(t, err)
	assert.True(t, result.Committed)
	assert.True(t, result.PublicationRecovered)
	assert.False(t, result.PublicationPending)
	stored := storedPricingDocuments(t)
	assert.Equal(t, 8.25, numericPricingDocument(t, stored, "ModelPrice")["owned"])
	assert.Equal(t, 8.25, ratio_setting.GetModelPriceCopy()["owned"])
}

func TestPricingPublicationPlanKeepsEveryTransitionBillable(t *testing.T) {
	current := make(map[string]string, len(modelPricingOptionKeys))
	final := make(map[string]string, len(modelPricingOptionKeys))
	for _, key := range modelPricingOptionKeys {
		current[key] = `{}`
		final[key] = `{}`
	}
	current["ModelPrice"] = `{"fixed-to-ratio":1,"fixed-to-tiered":2}`
	current["ModelRatio"] = `{"ratio-to-fixed":3}`
	current["billing_setting.billing_mode"] = `{"tiered-to-fixed":"tiered_expr"}`
	current["billing_setting.billing_expr"] = `{"tiered-to-fixed":"tier(\"old\", p * 1)"}`
	final["ModelPrice"] = `{"ratio-to-fixed":4,"tiered-to-fixed":5}`
	final["ModelRatio"] = `{"fixed-to-ratio":6}`
	final["billing_setting.billing_mode"] = `{"fixed-to-tiered":"tiered_expr"}`
	final["billing_setting.billing_expr"] = `{"fixed-to-tiered":"tier(\"new\", p * 2)"}`

	steps, err := pricingPublicationSteps(current, final)
	require.NoError(t, err)
	live := clonePricingValues(current)
	models := []string{"fixed-to-ratio", "ratio-to-fixed", "tiered-to-fixed", "fixed-to-tiered"}
	for _, step := range steps {
		published := step.Key
		if step.BillingMode != "" {
			live["billing_setting.billing_mode"] = step.BillingMode
			live["billing_setting.billing_expr"] = step.BillingExpr
			published = "billing pricing snapshot"
		} else {
			live[step.Key] = step.Value
		}
		modes := stringPricingDocument(t, live, "billing_setting.billing_mode")
		expressions := stringPricingDocument(t, live, "billing_setting.billing_expr")
		prices := numericPricingDocument(t, live, "ModelPrice")
		ratios := numericPricingDocument(t, live, "ModelRatio")
		for _, modelName := range models {
			if modes[modelName] == "tiered_expr" {
				assert.NotEmpty(t, expressions[modelName], "%s after publishing %s", modelName, published)
				continue
			}
			_, hasPrice := prices[modelName]
			_, hasRatio := ratios[modelName]
			assert.True(t, hasPrice || hasRatio, "%s became unpriced after publishing %s", modelName, published)
		}
	}
	for _, key := range modelPricingOptionKeys {
		assert.JSONEq(t, final[key], live[key], key)
	}
	require.NotEmpty(t, steps)
	assert.Equal(t, ratio_setting.VideoResolutionPriceOptionKey, steps[len(steps)-1].Key)
}

func TestPricingPublicationPendingConvergesOnNextDatabaseLoad(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	price := 6.5
	callbackName := "pricing-command-recovery-failure-" + t.Name()
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callbackName) })
	publishPricingDocumentsAfterCommit = func(map[string]string) error {
		require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(
			callbackName,
			func(tx *gorm.DB) { tx.AddError(fmt.Errorf("injected recovery reload failure")) },
		))
		return errors.New("injected post-commit failure")
	}

	result, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind:       PricingCommandSave,
		TargetName: "owned",
		Selection:  &ModelPricingSelection{Mode: PricingModeFixed, ModelPrice: &price},
	})
	require.NoError(t, err)
	assert.True(t, result.Committed)
	assert.False(t, result.PublicationRecovered)
	assert.True(t, result.PublicationPending)
	assert.Equal(t, 1.7, ratio_setting.GetModelPriceCopy()["owned"], "failed publication must not pretend the new value is live")

	require.NoError(t, DB.Callback().Query().Remove(callbackName))
	loadOptionsFromDatabase()
	assert.Equal(t, 6.5, ratio_setting.GetModelPriceCopy()["owned"])
}

func TestPricingPublicationLoadRejectsTieredModeWithoutExpression(t *testing.T) {
	setupPricingCommandTest(t)
	fixture := pricingCommandFixture()
	seedPricingDocuments(t, fixture)
	require.NoError(t, DB.Model(&Option{}).
		Where(&Option{Key: "ModelPrice"}).
		Update("value", `{"owned":99}`).Error)
	require.NoError(t, DB.Model(&Option{}).
		Where(&Option{Key: "billing_setting.billing_mode"}).
		Update("value", `{"owned":"tiered_expr","missing":"tiered_expr"}`).Error)

	loadOptionsFromDatabase()

	assert.Equal(t, 1.7, ratio_setting.GetModelPriceCopy()["owned"])
	assert.Equal(t, "ratio", stringPricingDocument(t, fixture, "billing_setting.billing_mode")["target"])
	common.OptionMapRWMutex.RLock()
	assert.JSONEq(t, fixture["billing_setting.billing_mode"], common.OptionMap["billing_setting.billing_mode"])
	common.OptionMapRWMutex.RUnlock()
}
