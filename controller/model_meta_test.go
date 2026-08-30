package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelMetaControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	originalModes := ratio_setting.TaskBillingMode2JSONString()
	originalBillingModes, originalBillingExpressions := billing_setting.PricingDocumentsJSON()
	originalProtectedValues := map[string]string{
		"AudioCompletionRatio": ratio_setting.AudioCompletionRatio2JSONString(),
		"AudioRatio":           ratio_setting.AudioRatio2JSONString(),
		"CacheRatio":           ratio_setting.CacheRatio2JSONString(),
		"CompletionRatio":      ratio_setting.CompletionRatio2JSONString(),
		"CreateCacheRatio":     ratio_setting.CreateCacheRatio2JSONString(),
		"ImageRatio":           ratio_setting.ImageRatio2JSONString(),
		"ModelPrice":           ratio_setting.ModelPrice2JSONString(),
		"ModelRatio":           ratio_setting.ModelRatio2JSONString(),
		"TaskBillingMode":      originalModes,
		ratio_setting.VideoResolutionPriceOptionKey: originalPrices,
		"billing_setting.billing_expr":              originalBillingExpressions,
		"billing_setting.billing_mode":              originalBillingModes,
	}
	originalDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Model{}, &model.Option{}, &model.Channel{}, &model.Ability{}, &model.Vendor{}))
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		_, _ = model.ExecuteModelPricingCommand(model.ModelPricingCommand{
			Kind:   model.PricingCommandReplaceDocuments,
			Values: originalProtectedValues,
		})
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices))
		require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(originalModes))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
		common.SetDatabaseTypes(originalDatabaseType, originalLogDatabaseType)
		model.InvalidatePricingCache()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func pricingDocumentsForControllerModel(source string) map[string]string {
	numeric := fmt.Sprintf(`{"%s":1.25,"untouched":2.5}`, source)
	return map[string]string{
		"AudioCompletionRatio": numeric,
		"AudioRatio":           numeric,
		"CacheRatio":           numeric,
		"CompletionRatio":      numeric,
		"CreateCacheRatio":     numeric,
		"ImageRatio":           numeric,
		"ModelPrice":           numeric,
		"ModelRatio":           numeric,
		"TaskBillingMode":      fmt.Sprintf(`{"%s":"per_call","untouched":"per_second"}`, source),
		ratio_setting.VideoResolutionPriceOptionKey: fmt.Sprintf(`{"%s":{"720p":0.1},"untouched":{"1080p":0.2}}`, source),
		"billing_setting.billing_expr":              fmt.Sprintf(`{"%s":"tier(\"base\", p * 1 + c * 2)","untouched":"tier(\"base\", p * 3 + c * 4)"}`, source),
		"billing_setting.billing_mode":              fmt.Sprintf(`{"%s":"tiered_expr","untouched":"tiered_expr"}`, source),
	}
}

func seedControllerPricingDocuments(t *testing.T, source string) map[string]string {
	t.Helper()
	values := pricingDocumentsForControllerModel(source)
	result, err := model.ExecuteModelPricingCommand(model.ModelPricingCommand{
		Kind:   model.PricingCommandReplaceDocuments,
		Values: values,
	})
	require.NoError(t, err)
	require.True(t, result.Committed)
	return values
}

func assertControllerPricingName(t *testing.T, db *gorm.DB, source, target string) {
	t.Helper()
	for key := range pricingDocumentsForControllerModel(source) {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		var document map[string]any
		require.NoError(t, common.UnmarshalJsonStr(option.Value, &document))
		assert.NotContains(t, document, source, key)
		if target != "" {
			assert.Contains(t, document, target, key)
		}
		assert.Contains(t, document, "untouched", key)
	}
}

func modelMetaContext(method, path, body string, role int) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Set("role", role)
	return c, recorder
}

type modelMutationResponseEnvelope struct {
	Success          bool              `json:"success"`
	Data             *model.Model      `json:"data"`
	PricingDocuments map[string]string `json:"pricing_documents"`
}

func decodeModelMutationResponse(t *testing.T, recorder *httptest.ResponseRecorder) modelMutationResponseEnvelope {
	t.Helper()
	var response modelMutationResponseEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestModelMetaPricingLegacyCreateWithoutPricing(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	c, recorder := modelMetaContext(http.MethodPost, "/api/models/", `{"model_name":"legacy-create","description":"legacy"}`, common.RoleAdminUser)

	CreateModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var stored model.Model
	require.NoError(t, db.First(&stored, "model_name = ?", "legacy-create").Error)
	assert.Equal(t, "legacy", stored.Description)
}

func TestModelMetaPricingValidationUsesHTTP400(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "negative fixed price",
			body: `{"model_name":"invalid-negative","pricing":{"mode":"per_request","price":-1}}`,
		},
		{
			name: "missing per-token ratio",
			body: `{"model_name":"invalid-ratio","pricing":{"mode":"per_token"}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := setupModelMetaControllerTest(t)
			expected := seedControllerPricingDocuments(t, "existing")
			c, recorder := modelMetaContext(http.MethodPost, "/api/models/", test.body, common.RoleRootUser)

			CreateModelMeta(c)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			var count int64
			require.NoError(t, db.Model(&model.Model{}).Count(&count).Error)
			assert.Zero(t, count)
			for key, value := range expected {
				var option model.Option
				require.NoError(t, db.First(&option, "key = ?", key).Error)
				assert.JSONEq(t, value, option.Value, key)
			}
		})
	}
}

func TestModelMetaPricingMetadataUpdateDoesNotTouchPricing(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "metadata-only", Description: "before", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	expected := seedControllerPricingDocuments(t, item.ModelName)
	c, recorder := modelMetaContext(http.MethodPut, "/api/models/", fmt.Sprintf(
		`{"id":%d,"model_name":"metadata-only","description":"after","status":1,"sync_official":1}`,
		item.Id,
	), common.RoleAdminUser)

	UpdateModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	for key, value := range expected {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		assert.JSONEq(t, value, option.Value, key)
	}
}

func TestModelMetaPricingStatusOnlyUpdateDoesNotTouchPricing(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "status-only", Description: "preserved", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	expected := seedControllerPricingDocuments(t, item.ModelName)
	c, recorder := modelMetaContext(http.MethodPut, "/api/models/?status_only=true", fmt.Sprintf(
		`{"id":%d,"status":0}`,
		item.Id,
	), common.RoleAdminUser)
	c.Request.URL.RawQuery = "status_only=true"

	UpdateModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var stored model.Model
	require.NoError(t, db.First(&stored, item.Id).Error)
	assert.Zero(t, stored.Status)
	assert.Equal(t, "status-only", stored.ModelName)
	assert.Equal(t, "preserved", stored.Description)
	for key, value := range expected {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		assert.JSONEq(t, value, option.Value, key)
	}
}

func TestModelMetaPricingCreateRequiresRoot(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	c, recorder := modelMetaContext(http.MethodPost, "/api/models/", `{
		"model_name":"admin-priced-create",
		"pricing":{"mode":"per_request","price":1.25}
	}`, common.RoleAdminUser)

	CreateModelMeta(c)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	var count int64
	require.NoError(t, db.Model(&model.Model{}).Where("model_name = ?", "admin-priced-create").Count(&count).Error)
	assert.Zero(t, count)
}

func TestModelMetaPricingCreateReturnsModelAndCommittedPricingDocuments(t *testing.T) {
	setupModelMetaControllerTest(t)
	c, recorder := modelMetaContext(http.MethodPost, "/api/models/", `{
		"model_name":"root-priced-create",
		"pricing":{"mode":"per_request","price":1.25}
	}`, common.RoleRootUser)

	CreateModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	response := decodeModelMutationResponse(t, recorder)
	require.True(t, response.Success)
	require.NotNil(t, response.Data)
	assert.Equal(t, "root-priced-create", response.Data.ModelName)
	require.Len(t, response.PricingDocuments, len(pricingDocumentsForControllerModel("unused")))
	assert.JSONEq(t, `{"root-priced-create":1.25}`, response.PricingDocuments["ModelPrice"])
}

func TestModelMetaPricingSameNameSaveRequiresRoot(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "admin-priced-save", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	seedControllerPricingDocuments(t, item.ModelName)
	c, recorder := modelMetaContext(http.MethodPut, "/api/models/", fmt.Sprintf(`{
		"id":%d,
		"model_name":"admin-priced-save",
		"status":1,
		"sync_official":1,
		"pricing":{"mode":"per_request","price":9}
	}`, item.Id), common.RoleAdminUser)

	UpdateModelMeta(c)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	var option model.Option
	require.NoError(t, db.First(&option, "key = ?", "ModelPrice").Error)
	assert.JSONEq(t, `{"admin-priced-save":1.25,"untouched":2.5}`, option.Value)
}

func TestModelMetaPricingSameNameSaveReturnsModelAndCommittedPricingDocuments(t *testing.T) {
	setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "root-priced-save", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	seedControllerPricingDocuments(t, item.ModelName)
	c, recorder := modelMetaContext(http.MethodPut, "/api/models/", fmt.Sprintf(`{
		"id":%d,
		"model_name":"root-priced-save",
		"status":1,
		"sync_official":1,
		"pricing":{"mode":"per_request","price":9}
	}`, item.Id), common.RoleRootUser)

	UpdateModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	response := decodeModelMutationResponse(t, recorder)
	require.True(t, response.Success)
	require.NotNil(t, response.Data)
	assert.Equal(t, item.Id, response.Data.Id)
	require.Len(t, response.PricingDocuments, len(pricingDocumentsForControllerModel("unused")))
	assert.JSONEq(t, `{"root-priced-save":9,"untouched":2.5}`, response.PricingDocuments["ModelPrice"])
}

func TestModelMetaPricingRenameRequiresRoot(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "admin-rename-source", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	expected := seedControllerPricingDocuments(t, item.ModelName)
	c, recorder := modelMetaContext(http.MethodPut, "/api/models/", fmt.Sprintf(
		`{"id":%d,"model_name":"admin-rename-target","status":1,"sync_official":1}`,
		item.Id,
	), common.RoleAdminUser)

	UpdateModelMeta(c)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	var stored model.Model
	require.NoError(t, db.First(&stored, item.Id).Error)
	assert.Equal(t, "admin-rename-source", stored.ModelName)
	for key, value := range expected {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		assert.JSONEq(t, value, option.Value, key)
	}
}

func TestModelMetaPricingRenameWithoutSelectionMovesAllDocuments(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "root-rename-source", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	seedControllerPricingDocuments(t, item.ModelName)
	c, recorder := modelMetaContext(http.MethodPut, "/api/models/", fmt.Sprintf(
		`{"id":%d,"model_name":"root-rename-target","status":1,"sync_official":1}`,
		item.Id,
	), common.RoleRootUser)

	UpdateModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var stored model.Model
	require.NoError(t, db.First(&stored, item.Id).Error)
	assert.Equal(t, "root-rename-target", stored.ModelName)
	assertControllerPricingName(t, db, "root-rename-source", "root-rename-target")
	response := decodeModelMutationResponse(t, recorder)
	require.Len(t, response.PricingDocuments, len(pricingDocumentsForControllerModel("unused")))
	for key, raw := range response.PricingDocuments {
		var document map[string]any
		require.NoError(t, common.UnmarshalJsonStr(raw, &document), key)
		assert.NotContains(t, document, "root-rename-source", key)
		assert.Contains(t, document, "root-rename-target", key)
	}
}

func TestModelMetaPricingDuplicateRenameRollsBack(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	source := &model.Model{ModelName: "duplicate-source", Status: 1, SyncOfficial: 1}
	target := &model.Model{ModelName: "duplicate-target", Status: 1, SyncOfficial: 1}
	require.NoError(t, source.Insert())
	require.NoError(t, target.Insert())
	expected := seedControllerPricingDocuments(t, source.ModelName)
	c, recorder := modelMetaContext(http.MethodPut, "/api/models/", fmt.Sprintf(
		`{"id":%d,"model_name":"duplicate-target","status":1,"sync_official":1}`,
		source.Id,
	), common.RoleRootUser)

	UpdateModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	var stored model.Model
	require.NoError(t, db.First(&stored, source.Id).Error)
	assert.Equal(t, "duplicate-source", stored.ModelName)
	for key, value := range expected {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		assert.JSONEq(t, value, option.Value, key)
	}
}

func TestModelMetaPricingDeleteRequiresRoot(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "admin-delete", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	seedControllerPricingDocuments(t, item.ModelName)
	c, recorder := modelMetaContext(http.MethodDelete, "/api/models/1", "", common.RoleAdminUser)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", item.Id)}}

	DeleteModelMeta(c)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	var count int64
	require.NoError(t, db.Model(&model.Model{}).Where("id = ?", item.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestModelMetaPricingDeleteRemovesAllDocuments(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "controller-model-delete", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	seedControllerPricingDocuments(t, item.ModelName)

	c, recorder := modelMetaContext(http.MethodDelete, "/api/models/1", "", common.RoleRootUser)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", item.Id)}}
	DeleteModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var count int64
	require.NoError(t, db.Model(&model.Model{}).Where("id = ?", item.Id).Count(&count).Error)
	assert.Zero(t, count)
	assertControllerPricingName(t, db, item.ModelName, "")
	response := decodeModelMutationResponse(t, recorder)
	require.Len(t, response.PricingDocuments, len(pricingDocumentsForControllerModel("unused")))
	for key, raw := range response.PricingDocuments {
		var document map[string]any
		require.NoError(t, common.UnmarshalJsonStr(raw, &document), key)
		assert.NotContains(t, document, item.ModelName, key)
	}
}
