package helper

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleGroupRatioPrefersGroupModelRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedGroup := ratio_setting.GroupRatio2JSONString()
	savedGroupGroup := ratio_setting.GroupGroupRatio2JSONString()
	savedGroupModel := ratio_setting.GroupModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroup))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(savedGroupGroup))
		require.NoError(t, ratio_setting.UpdateGroupModelRatioByJSONString(savedGroupModel))
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"sd73/80":0.9}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"sd73/80":{"sd73/80":0.95}}`))
	require.NoError(t, ratio_setting.UpdateGroupModelRatioByJSONString(`{"sd73/80":{"seedance-2.0":0.73,"seedance-2.5":0.8}}`))

	ratioFor := func(userGroup, usingGroup, modelName string) (float64, bool) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		info := &relaycommon.RelayInfo{UserGroup: userGroup, UsingGroup: usingGroup, OriginModelName: modelName}
		got := HandleGroupRatio(ctx, info)
		return got.GroupRatio, got.HasModelRatio
	}

	ratio, fromModel := ratioFor("sd73/80", "sd73/80", "seedance-2.0")
	assert.Equal(t, 0.73, ratio)
	assert.True(t, fromModel)
	ratio, _ = ratioFor("sd73/80", "sd73/80", "seedance-2.5")
	assert.Equal(t, 0.8, ratio)

	ratio, fromModel = ratioFor("sd73/80", "sd73/80", "gpt-4o")
	assert.Equal(t, 0.95, ratio, "without a model entry the user group special ratio still applies")
	assert.False(t, fromModel)
	ratio, _ = ratioFor("default", "sd73/80", "gpt-4o")
	assert.Equal(t, 0.9, ratio, "then the plain group ratio")
	ratio, _ = ratioFor("default", "default", "seedance-2.0")
	assert.Equal(t, 1.0, ratio, "other groups are not affected")
}
