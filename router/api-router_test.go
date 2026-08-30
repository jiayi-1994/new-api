package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTask5RouteAuthTest(t *testing.T) (*gin.Engine, string, *gorm.DB, <-chan struct{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Log{}, &model.Model{}, &model.Option{},
		&model.Channel{}, &model.Ability{}, &model.Vendor{},
	))
	auditWritten := make(chan struct{}, 1)
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("task5:audit-written", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Log" {
			select {
			case auditWritten <- struct{}{}:
			default:
			}
		}
	}))

	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalRedis := common.RedisEnabled
	originalMainType := common.MainDatabaseType()
	originalLogType := common.LogDatabaseType()
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.RedisEnabled = originalRedis
		common.SetDatabaseTypes(originalMainType, originalLogType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	token := "task5-admin-route-token-opaque"
	user := &model.User{
		Username:    "task5-route-admin",
		Password:    "password-placeholder",
		Role:        common.RoleAdminUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AccessToken: &token,
		AuthVersion: 1,
		AffCode:     "task5-route-admin-aff",
	}
	require.NoError(t, db.Create(user).Error)

	engine := gin.New()
	SetApiRouter(engine)
	return engine, token, db, auditWritten
}

func performTask5AdminRouteRequest(engine *gin.Engine, token, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestPricingCommandRouteRequiresRootAuthentication(t *testing.T) {
	engine, token, _, _ := setupTask5RouteAuthTest(t)

	response := performTask5AdminRouteRequest(
		engine,
		token,
		http.MethodPut,
		"/api/option/pricing",
		`{"kind":"delete","target_name":"protected-model"}`,
	)

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "AUTH_INSUFFICIENT_PRIVILEGE")
}

func TestModelMetaPricingRenameRouteRejectsAdmin(t *testing.T) {
	engine, token, db, auditWritten := setupTask5RouteAuthTest(t)
	item := &model.Model{ModelName: "route-rename-source", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())

	response := performTask5AdminRouteRequest(
		engine,
		token,
		http.MethodPut,
		"/api/models/",
		fmt.Sprintf(`{"id":%d,"model_name":"route-rename-target","status":1,"sync_official":1}`, item.Id),
	)

	assert.Equal(t, http.StatusForbidden, response.Code)
	select {
	case <-auditWritten:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for denied rename audit")
	}
	var stored model.Model
	require.NoError(t, db.First(&stored, item.Id).Error)
	assert.Equal(t, "route-rename-source", stored.ModelName)
}

func TestModelMetaPricingDeleteRouteRejectsAdmin(t *testing.T) {
	engine, token, db, _ := setupTask5RouteAuthTest(t)
	item := &model.Model{ModelName: "route-delete", Status: 1, SyncOfficial: 1}
	require.NoError(t, item.Insert())

	response := performTask5AdminRouteRequest(
		engine,
		token,
		http.MethodDelete,
		fmt.Sprintf("/api/models/%d", item.Id),
		"",
	)

	assert.Equal(t, http.StatusForbidden, response.Code)
	var count int64
	require.NoError(t, db.Model(&model.Model{}).Where("id = ?", item.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestModelMetaPricingRoutesKeepLegacyAdminMetadataWrites(t *testing.T) {
	engine, token, db, auditWritten := setupTask5RouteAuthTest(t)

	response := performTask5AdminRouteRequest(
		engine,
		token,
		http.MethodPost,
		"/api/models/",
		`{"model_name":"route-admin-metadata","description":"before","status":1,"sync_official":1}`,
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"success":true`)
	select {
	case <-auditWritten:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for legacy create audit")
	}

	var item model.Model
	require.NoError(t, db.First(&item, "model_name = ?", "route-admin-metadata").Error)
	response = performTask5AdminRouteRequest(
		engine,
		token,
		http.MethodPut,
		"/api/models/",
		fmt.Sprintf(`{"id":%d,"model_name":"route-admin-metadata","description":"after","status":1,"sync_official":1}`, item.Id),
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"success":true`)
	select {
	case <-auditWritten:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for legacy update audit")
	}

	require.NoError(t, db.First(&item, item.Id).Error)
	assert.Equal(t, "after", item.Description)
}
