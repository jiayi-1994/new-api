package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoEffectiveGroupRatioUsesGroupModelRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedGroup := ratio_setting.GroupRatio2JSONString()
	savedGroupModel := ratio_setting.GroupModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroup))
		require.NoError(t, ratio_setting.UpdateGroupModelRatioByJSONString(savedGroupModel))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"sd73/80":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelRatioByJSONString(`{"sd73/80":{"seedance-2.0":0.73,"seedance-2.5":0.8}}`))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUserGroup, "sd73/80")

	assert.Equal(t, 0.73, VideoEffectiveGroupRatio(c, "sd73/80", "seedance-2.0"))
	assert.Equal(t, 0.8, VideoEffectiveGroupRatio(c, "sd73/80", "seedance-2.5"))
	assert.Equal(t, 1.0, VideoEffectiveGroupRatio(c, "sd73/80", "other-video"))

	sales := VideoSalesFacts{Seconds: 10, USDPerSecond: 0.5}
	assert.InDelta(t, 5*0.73, unifiedVideoSell(c, "sd73/80", "seedance-2.0", sales).USD, 1e-9,
		"the scheduler's sell price uses the same ratio submission charges")
}
