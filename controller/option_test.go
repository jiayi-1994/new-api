package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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
	require.NoError(t, ratio_setting.UpdateVideoResolutionPricingSnapshotByJSONString("{}", "{}"))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateVideoResolutionPricingSnapshotByJSONString("{}", "{}"))
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
		`{"key":"VideoResolutionPrice","value":"{\"sora-2\":{\"720p\":0}}"}`,
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

func TestUpdateOptionsBulkRejectsDuplicateKeys(t *testing.T) {
	setupOptionControllerTest(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/bulk", strings.NewReader(`[
		{"key":"TaskBillingMode","value":"{\"sora-2\":\"per_call\"}"},
		{"key":"TaskBillingMode","value":"{\"sora-2\":\"per_second\"}"}
	]`))

	UpdateOptionsBulk(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "duplicate option key")
}

func TestUpdateOptionsBulkRequiresExactVideoPricingPair(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing task billing mode",
			body: `[{"key":"VideoResolutionPrice","value":"{\"sora-2\":{\"720p\":0.1}}"}]`,
		},
		{
			name: "missing video resolution price",
			body: `[{"key":"TaskBillingMode","value":"{\"sora-2\":\"per_call\"}"}]`,
		},
		{
			name: "includes unrelated option",
			body: `[
				{"key":"VideoResolutionPrice","value":"{\"sora-2\":{\"720p\":0.1}}"},
				{"key":"TaskBillingMode","value":"{\"sora-2\":\"per_call\"}"},
				{"key":"QuotaForInviter","value":"100"}
			]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupOptionControllerTest(t)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/bulk", strings.NewReader(tc.body))

			UpdateOptionsBulk(ctx)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			var count int64
			require.NoError(t, db.Model(&model.Option{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestUpdateOptionsBulkAcceptsExactVideoPricingPair(t *testing.T) {
	db := setupOptionControllerTest(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/bulk", strings.NewReader(`[
		{"key":"VideoResolutionPrice","value":"{\"sora-2\":{\"720p\":0.1}}"},
		{"key":"TaskBillingMode","value":"{\"sora-2\":\"per_call\"}"}
	]`))

	UpdateOptionsBulk(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var count int64
	require.NoError(t, db.Model(&model.Option{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
	price, mode, ok := ratio_setting.GetVideoResolutionBillingConfig("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
	assert.Equal(t, ratio_setting.TaskBillingModePerCall, mode)
}

func TestBuildCompletionRatioMetaIncludesVideoResolutionPriceModels(t *testing.T) {
	metaJSON := buildCompletionRatioMetaValue(map[string]string{
		ratio_setting.VideoResolutionPriceOptionKey: `{"sora-video":{"720p":0.1}}`,
	})
	var meta map[string]ratio_setting.CompletionRatioInfo
	require.NoError(t, common.UnmarshalJsonStr(metaJSON, &meta))
	assert.Contains(t, meta, "sora-video")
}
