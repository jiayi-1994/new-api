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

func setupModelMetaControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	originalModes := ratio_setting.TaskBillingMode2JSONString()
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

func TestDeleteModelMetaUsesResolutionPriceLifecycle(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	item := &model.Model{ModelName: "controller-video-delete", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())
	const taskMode = `{"controller-video-delete":"per_call"}`
	require.NoError(t, model.UpdateOption("TaskBillingMode", taskMode))
	require.NoError(t, model.UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, `{"controller-video-delete":{"720p":0.1},"untouched":{"1080p":0.2}}`))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/models/1", nil)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", item.Id)}}
	DeleteModelMeta(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var count int64
	require.NoError(t, db.Model(&model.Model{}).Where("id = ?", item.Id).Count(&count).Error)
	assert.Zero(t, count)
	assert.Equal(t, map[string]map[string]float64{"untouched": {"1080p": 0.2}}, ratio_setting.GetVideoResolutionPriceMap())
	var storedMode model.Option
	require.NoError(t, db.First(&storedMode, "key = ?", "TaskBillingMode").Error)
	assert.Equal(t, taskMode, storedMode.Value)
}
