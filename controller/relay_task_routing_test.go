package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRelayTaskRemixRejectsIncompatibleLockedChannelBeforeSubmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Channel{}))
	t.Cleanup(func() { model.DB = previousDB })

	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices)) })
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"resolution-model":{"720p":0.1}}`))
	require.NoError(t, db.Create(&model.Channel{
		Id:     993001,
		Type:   constant.ChannelTypeKling,
		Key:    "kling-key",
		Name:   "incompatible-kling",
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&model.Task{
		TaskID:     "origin-task",
		UserId:     42,
		ChannelId:  993001,
		Properties: model.Properties{OriginModelName: "resolution-model"},
	}).Error)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUserId, 42)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	})
	router.POST("/v1/videos/:video_id/remix", middleware.Distribute(), RelayTask)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/videos/origin-task/remix", nil))

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"video_resolution_not_supported"`)
}
