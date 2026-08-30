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

func setupOptionControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Log{}))

	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalOptionMap := common.OptionMap
	originalRedisEnabled := common.RedisEnabled
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
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
		"TaskBillingMode":      ratio_setting.TaskBillingMode2JSONString(),
		ratio_setting.VideoResolutionPriceOptionKey: ratio_setting.VideoResolutionPrice2JSONString(),
		"billing_setting.billing_expr":              originalBillingExpressions,
		"billing_setting.billing_mode":              originalBillingModes,
	}
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString("{}"))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString("{}"))
	t.Cleanup(func() {
		_, _ = model.ExecuteModelPricingCommand(model.ModelPricingCommand{
			Kind:   model.PricingCommandReplaceDocuments,
			Values: originalProtectedValues,
		})
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.RedisEnabled = originalRedisEnabled
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func TestUpdateOptionRejectsInvalidVideoResolutionPriceWithoutPersisting(t *testing.T) {
	db := setupOptionControllerTest(t)
	require.NoError(t, model.UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"sora-2":{"720p":0.1}}`))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(
		`{"key":"VideoResolutionPrice","value":"{\"sora-2\":{\"720p\":0}}","expected_value":"{\"sora-2\":{\"720p\":0.1}}"}`,
	))
	UpdateOption(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	var stored model.Option
	require.NoError(t, db.First(&stored, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, `{"sora-2":{"720p":0.1}}`, stored.Value)
	price, ok := ratio_setting.GetVideoResolutionPrice("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
}

func TestBuildCompletionRatioMetaIncludesVideoResolutionPriceModels(t *testing.T) {
	metaJSON := buildCompletionRatioMetaValue(map[string]string{
		ratio_setting.VideoResolutionPriceOptionKey: `{"sora-video":{"720p":0.1}}`,
	})
	var meta map[string]ratio_setting.CompletionRatioInfo
	require.NoError(t, common.UnmarshalJsonStr(metaJSON, &meta))
	assert.Contains(t, meta, "sora-video")
}

func optionControllerContext(body string, role int) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(body))
	ctx.Set("role", role)
	return ctx, recorder
}

func TestUpdateOptionCASRequiresExpectedValueForProtectedDocument(t *testing.T) {
	db := setupOptionControllerTest(t)
	seedControllerPricingDocuments(t, "cas-protected")
	ctx, recorder := optionControllerContext(
		`{"key":"ModelPrice","value":"{\"cas-protected\":9}"}`,
		common.RoleRootUser,
	)

	UpdateOption(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "expected_value")
	var stored model.Option
	require.NoError(t, db.First(&stored, "key = ?", "ModelPrice").Error)
	assert.JSONEq(t, `{"cas-protected":1.25,"untouched":2.5}`, stored.Value)
}

func TestUpdateOptionCASReturnsConflictWithCurrentRawValue(t *testing.T) {
	db := setupOptionControllerTest(t)
	seedControllerPricingDocuments(t, "cas-stale")
	ctx, recorder := optionControllerContext(
		`{"key":"ModelPrice","value":"{\"cas-stale\":9}","expected_value":"{}"}`,
		common.RoleRootUser,
	)

	UpdateOption(ctx)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Key          string `json:"key"`
			CurrentValue string `json:"current_value"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "ModelPrice", response.Data.Key)
	assert.JSONEq(t, `{"cas-stale":1.25,"untouched":2.5}`, response.Data.CurrentValue)
	var stored model.Option
	require.NoError(t, db.First(&stored, "key = ?", "ModelPrice").Error)
	assert.JSONEq(t, response.Data.CurrentValue, stored.Value)
}

func TestUpdateOptionCASAcceptsExactRawExpectedValue(t *testing.T) {
	db := setupOptionControllerTest(t)
	seedControllerPricingDocuments(t, "cas-current")
	const current = `{"cas-current":1.25,"untouched":2.5}`
	ctx, recorder := optionControllerContext(
		`{"key":"ModelPrice","value":"{\"cas-current\":9}","expected_value":"{\"cas-current\":1.25,\"untouched\":2.5}"}`,
		common.RoleRootUser,
	)

	UpdateOption(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var stored model.Option
	require.NoError(t, db.First(&stored, "key = ?", "ModelPrice").Error)
	assert.JSONEq(t, `{"cas-current":9}`, stored.Value)
	assert.NotEqual(t, current, stored.Value)
}

func TestUpdateOptionCASKeepsLegacyContractForUnprotectedKey(t *testing.T) {
	db := setupOptionControllerTest(t)
	ctx, recorder := optionControllerContext(
		`{"key":"TopUpLink","value":"https://example.test/topup"}`,
		common.RoleRootUser,
	)

	UpdateOption(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var stored model.Option
	require.NoError(t, db.First(&stored, "key = ?", "TopUpLink").Error)
	assert.Equal(t, "https://example.test/topup", stored.Value)
}

func TestPricingCommandRouteHandlerRequiresRoot(t *testing.T) {
	setupOptionControllerTest(t)
	ctx, recorder := optionControllerContext(
		`{"kind":"save","target_name":"admin-semantic-save","pricing":{"mode":"per_request","price":2.5}}`,
		common.RoleAdminUser,
	)

	UpdatePricingOption(ctx)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestPricingCommandRouteSaveCopyDeleteAreAtomic(t *testing.T) {
	db := setupOptionControllerTest(t)

	ctx, recorder := optionControllerContext(
		`{"kind":"save","target_name":"semantic-source","pricing":{"mode":"per_request","price":2.5,"task_billing_mode":"per_call"}}`,
		common.RoleRootUser,
	)
	UpdatePricingOption(ctx)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"committed":true`)
	seedControllerPricingDocuments(t, "semantic-source")

	ctx, recorder = optionControllerContext(
		`{"kind":"copy","source_name":"semantic-source","target_name":"semantic-copy"}`,
		common.RoleRootUser,
	)
	UpdatePricingOption(ctx)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	copiedValues := make(map[string]any)
	for key := range pricingDocumentsForControllerModel("semantic-source") {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		var document map[string]any
		require.NoError(t, common.UnmarshalJsonStr(option.Value, &document))
		assert.Equal(t, document["semantic-source"], document["semantic-copy"], key)
		assert.Contains(t, document, "untouched", key)
		copiedValues[key] = document["semantic-copy"]
	}

	ctx, recorder = optionControllerContext(
		`{"kind":"delete","target_name":"semantic-source"}`,
		common.RoleRootUser,
	)
	UpdatePricingOption(ctx)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	for key, expectedCopy := range copiedValues {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		var document map[string]any
		require.NoError(t, common.UnmarshalJsonStr(option.Value, &document))
		assert.NotContains(t, document, "semantic-source", key)
		assert.Equal(t, expectedCopy, document["semantic-copy"], key)
		assert.Contains(t, document, "untouched", key)
	}
}

func TestPricingCommandRouteInvalidSaveRollsBack(t *testing.T) {
	db := setupOptionControllerTest(t)
	expected := seedControllerPricingDocuments(t, "atomic-source")
	ctx, recorder := optionControllerContext(
		`{"kind":"save","target_name":"atomic-source","pricing":{"mode":"per_request"}}`,
		common.RoleRootUser,
	)

	UpdatePricingOption(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	for key, value := range expected {
		var stored model.Option
		require.NoError(t, db.First(&stored, "key = ?", key).Error)
		assert.JSONEq(t, value, stored.Value, key)
	}
}

func TestPricingCommandRouteTypedNumericValidationUsesHTTP400(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "missing per-token ratio",
			body: `{"kind":"save","target_name":"atomic-source","pricing":{"mode":"per_token"}}`,
		},
		{
			name: "negative fixed price",
			body: `{"kind":"save","target_name":"atomic-source","pricing":{"mode":"per_request","price":-1}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := setupOptionControllerTest(t)
			expected := seedControllerPricingDocuments(t, "atomic-source")
			ctx, recorder := optionControllerContext(test.body, common.RoleRootUser)

			UpdatePricingOption(ctx)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			for key, value := range expected {
				var stored model.Option
				require.NoError(t, db.First(&stored, "key = ?", key).Error)
				assert.JSONEq(t, value, stored.Value, key)
			}
		})
	}
}

func TestPricingCommandRoutePreservesValidZeroPricing(t *testing.T) {
	for _, test := range []struct {
		name        string
		body        string
		documentKey string
	}{
		{
			name:        "fixed price",
			body:        `{"kind":"save","target_name":"zero-target","pricing":{"mode":"per_request","price":0}}`,
			documentKey: "ModelPrice",
		},
		{
			name:        "per-token ratio",
			body:        `{"kind":"save","target_name":"zero-target","pricing":{"mode":"per_token","ratio":0}}`,
			documentKey: "ModelRatio",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := setupOptionControllerTest(t)
			seedControllerPricingDocuments(t, "existing")
			ctx, recorder := optionControllerContext(test.body, common.RoleRootUser)

			UpdatePricingOption(ctx)

			assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), `"success":true`)
			var stored model.Option
			require.NoError(t, db.First(&stored, "key = ?", test.documentKey).Error)
			var document map[string]float64
			require.NoError(t, common.UnmarshalJsonStr(stored.Value, &document))
			value, exists := document["zero-target"]
			assert.True(t, exists)
			assert.Zero(t, value)
		})
	}
}

func TestPricingCommandRouteBulkValidationErrorsUseHTTP400(t *testing.T) {
	for _, test := range []struct {
		name string
		body func(map[string]string) string
	}{
		{
			name: "unknown option",
			body: func(_ map[string]string) string {
				return `{
					"kind":"replace_documents",
					"values":{"Unknown":"{}"},
					"expected_documents":{"Unknown":"{}"}
				}`
			},
		},
		{
			name: "invalid raw document",
			body: func(expected map[string]string) string {
				return fmt.Sprintf(`{
					"kind":"replace_documents",
					"values":{"ModelPrice":"not-json"},
					"expected_documents":{"ModelPrice":%q}
				}`, expected["ModelPrice"])
			},
		},
		{
			name: "negative raw document",
			body: func(expected map[string]string) string {
				return fmt.Sprintf(`{
					"kind":"replace_documents",
					"values":{"ModelPrice":"{\"bulk-invalid\":-1}"},
					"expected_documents":{"ModelPrice":%q}
				}`, expected["ModelPrice"])
			},
		},
		{
			name: "NaN raw document",
			body: func(expected map[string]string) string {
				return fmt.Sprintf(`{
					"kind":"replace_documents",
					"values":{"ModelPrice":"{\"bulk-invalid\":NaN}"},
					"expected_documents":{"ModelPrice":%q}
				}`, expected["ModelPrice"])
			},
		},
		{
			name: "infinite raw document",
			body: func(expected map[string]string) string {
				return fmt.Sprintf(`{
					"kind":"replace_documents",
					"values":{"ModelPrice":"{\"bulk-invalid\":Infinity}"},
					"expected_documents":{"ModelPrice":%q}
				}`, expected["ModelPrice"])
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := setupOptionControllerTest(t)
			expected := seedControllerPricingDocuments(t, "bulk-invalid")
			ctx, recorder := optionControllerContext(test.body(expected), common.RoleRootUser)

			UpdatePricingOption(ctx)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			for key, value := range expected {
				var stored model.Option
				require.NoError(t, db.First(&stored, "key = ?", key).Error)
				assert.JSONEq(t, value, stored.Value, key)
			}
		})
	}
}

func TestPricingCommandRouteRejectsNullInEveryNumericDocumentWithHTTP400(t *testing.T) {
	for _, key := range []string{
		"AudioCompletionRatio",
		"AudioRatio",
		"CacheRatio",
		"CompletionRatio",
		"CreateCacheRatio",
		"ImageRatio",
		"ModelPrice",
		"ModelRatio",
	} {
		t.Run(key, func(t *testing.T) {
			db := setupOptionControllerTest(t)
			expected := seedControllerPricingDocuments(t, "null-invalid")
			body := fmt.Sprintf(`{
				"kind":"replace_documents",
				"values":{%q:"{\"null-invalid\":null}"},
				"expected_documents":{%q:%q}
			}`, key, key, expected[key])
			ctx, recorder := optionControllerContext(body, common.RoleRootUser)

			UpdatePricingOption(ctx)

			assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			for documentKey, value := range expected {
				var stored model.Option
				require.NoError(t, db.First(&stored, "key = ?", documentKey).Error)
				assert.JSONEq(t, value, stored.Value, documentKey)
			}
		})
	}
}

func TestPricingCommandRouteStoredDocumentErrorUsesHTTP500(t *testing.T) {
	db := setupOptionControllerTest(t)
	seedControllerPricingDocuments(t, "stored-invalid")
	require.NoError(t, db.Model(&model.Option{}).
		Where("key = ?", "ModelPrice").
		Update("value", "not-json").Error)
	ctx, recorder := optionControllerContext(
		`{"kind":"delete","target_name":"stored-invalid"}`,
		common.RoleRootUser,
	)

	UpdatePricingOption(ctx)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"success":false`)
}

func TestPricingCommandRouteBulkCASRequiresEveryExpectedDocument(t *testing.T) {
	setupOptionControllerTest(t)
	seedControllerPricingDocuments(t, "bulk-required")
	ctx, recorder := optionControllerContext(`{
		"kind":"replace_documents",
		"values":{"ModelPrice":"{\"bulk-required\":9}"},
		"expected_documents":{}
	}`, common.RoleRootUser)

	UpdatePricingOption(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "expected_documents")
	assert.Contains(t, recorder.Body.String(), "ModelPrice")
}

func TestPricingCommandRouteBulkCASReturnsConflictWithoutPartialWrite(t *testing.T) {
	db := setupOptionControllerTest(t)
	expected := seedControllerPricingDocuments(t, "bulk-stale")
	ctx, recorder := optionControllerContext(`{
		"kind":"replace_documents",
		"values":{
			"ModelPrice":"{\"bulk-stale\":9}",
			"TaskBillingMode":"{\"bulk-stale\":\"per_second\"}"
		},
		"expected_documents":{
			"ModelPrice":"{}",
			"TaskBillingMode":"{\"bulk-stale\":\"per_call\",\"untouched\":\"per_second\"}"
		}
	}`, common.RoleRootUser)

	UpdatePricingOption(ctx)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"key":"ModelPrice"`)
	assert.Contains(t, recorder.Body.String(), `"current_value":"{\"bulk-stale\":1.25,\"untouched\":2.5}"`)
	for key, value := range expected {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		assert.JSONEq(t, value, option.Value, key)
	}
}

func TestPricingCommandRouteBulkCASCommitsChangedDocumentsOnce(t *testing.T) {
	db := setupOptionControllerTest(t)
	expected := seedControllerPricingDocuments(t, "bulk-current")
	ctx, recorder := optionControllerContext(`{
		"kind":"replace_documents",
		"values":{
			"ModelPrice":"{\"bulk-current\":9,\"untouched\":2.5}",
			"TaskBillingMode":"{\"bulk-current\":\"per_second\",\"untouched\":\"per_second\"}"
		},
		"expected_documents":{
			"ModelPrice":"{\"bulk-current\":1.25,\"untouched\":2.5}",
			"TaskBillingMode":"{\"bulk-current\":\"per_call\",\"untouched\":\"per_second\"}"
		}
	}`, common.RoleRootUser)

	UpdatePricingOption(ctx)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		Success   bool              `json:"success"`
		Committed bool              `json:"committed"`
		Data      map[string]string `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.True(t, response.Committed)
	assert.Len(t, response.Data, 12)
	assert.JSONEq(t, `{"bulk-current":9,"untouched":2.5}`, response.Data["ModelPrice"])
	assert.JSONEq(t, `{"bulk-current":"per_second","untouched":"per_second"}`, response.Data["TaskBillingMode"])
	for key, oldValue := range expected {
		var option model.Option
		require.NoError(t, db.First(&option, "key = ?", key).Error)
		assert.Equal(t, response.Data[key], option.Value, key)
		if key != "ModelPrice" && key != "TaskBillingMode" {
			assert.Equal(t, oldValue, option.Value, key)
		}
	}
}

func TestPricingCommandHTTPResultDoesNotInviteRetryAfterCommit(t *testing.T) {
	for _, test := range []struct {
		name   string
		result model.ModelPricingCommandResult
		text   string
	}{
		{
			name: "recovered",
			result: model.ModelPricingCommandResult{
				Committed:            true,
				PublicationRecovered: true,
			},
			text: "publication recovered",
		},
		{
			name: "pending",
			result: model.ModelPricingCommandResult{
				Committed:          true,
				PublicationPending: true,
			},
			text: "do not retry",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)

			writePricingCommandSuccess(ctx, test.result, nil)

			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":true`)
			assert.Contains(t, recorder.Body.String(), test.text)
			assert.NotContains(t, recorder.Body.String(), `"success":false`)
		})
	}
}
