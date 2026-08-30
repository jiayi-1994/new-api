package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDistributeRejectsResolutionRequestWithIncompatibleSpecificChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	t.Cleanup(func() { model.DB = previousDB })

	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices)) })
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"resolution-model":{"720p":0.1}}`))
	require.NoError(t, db.Create(&model.Channel{
		Id:     992001,
		Type:   constant.ChannelTypeKling,
		Key:    "kling-key",
		Name:   "incompatible-kling",
		Status: common.ChannelStatusEnabled,
	}).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{"model":"resolution-model"}`))
	c.Request.Header.Set("Content-Type", gin.MIMEJSON)
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "992001")

	Distribute()(c)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"video_resolution_not_supported"`)
}

func TestDistributeSelectsResolverCapableChannelForResolutionPlan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := model.DB
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.MemoryCacheEnabled = true
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices)) })
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"resolution-selection-model":{"720p":0.1}}`))
	highPriority, lowPriority := int64(100), int64(1)
	require.NoError(t, db.Create(&[]model.Channel{
		{Id: 992011, Type: constant.ChannelTypeKling, Key: "kling-key", Name: "incompatible-kling", Status: common.ChannelStatusEnabled, Group: "default", Models: "resolution-selection-model", Priority: &highPriority},
		{Id: 992012, Type: constant.ChannelTypeSora, Key: "sora-key", Name: "compatible-sora", Status: common.ChannelStatusEnabled, Group: "default", Models: "resolution-selection-model", Priority: &lowPriority},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "resolution-selection-model", ChannelId: 992011, Enabled: true, Priority: &highPriority},
		{Group: "default", Model: "resolution-selection-model", ChannelId: 992012, Enabled: true, Priority: &lowPriority},
	}).Error)
	model.InitChannelCache()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{"model":"resolution-selection-model"}`))
	c.Request.Header.Set("Content-Type", gin.MIMEJSON)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")

	Distribute()(c)

	assert.Equal(t, constant.ChannelTypeSora, c.GetInt(string(constant.ContextKeyChannelType)))
}

func TestDistributeRejectsResolutionRequestWhenNoCompatibleChannelExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := model.DB
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.MemoryCacheEnabled = true
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices)) })
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"resolution-no-channel-model":{"720p":0.1}}`))
	priority := int64(100)
	require.NoError(t, db.Create(&model.Channel{
		Id: 992021, Type: constant.ChannelTypeKling, Key: "kling-key", Name: "incompatible-kling", Status: common.ChannelStatusEnabled,
		Group: "default", Models: "resolution-no-channel-model", Priority: &priority,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "resolution-no-channel-model", ChannelId: 992021, Enabled: true, Priority: &priority,
	}).Error)
	model.InitChannelCache()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{"model":"resolution-no-channel-model"}`))
	c.Request.Header.Set("Content-Type", gin.MIMEJSON)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")

	Distribute()(c)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"video_resolution_not_supported"`)
}
