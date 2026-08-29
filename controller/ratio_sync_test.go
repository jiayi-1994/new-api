package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRatioSyncLeavesNestedResolutionPricesUnchanged(t *testing.T) {
	db := setupModelMetaControllerTest(t)
	const localDocument = `{"video-model":{"720p":0.1}}`
	require.NoError(t, model.UpdateOption(ratio_setting.VideoResolutionPriceOptionKey, localDocument))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[{"model_name":"video-model","quota_type":1,"model_price":0.9,"resolution_prices":{"720p":9.9,"1080p":19.9}}]}`))
	}))
	t.Cleanup(server.Close)

	requestBody, err := common.Marshal(map[string]any{
		"upstreams": []map[string]any{{
			"name":     "resolution-source",
			"base_url": server.URL,
			"endpoint": "/api/pricing",
		}},
		"timeout": 2,
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/ratio/sync", strings.NewReader(string(requestBody)))
	c.Request.Header.Set("Content-Type", "application/json")

	FetchUpstreamRatios(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var stored model.Option
	require.NoError(t, db.First(&stored, "key = ?", ratio_setting.VideoResolutionPriceOptionKey).Error)
	assert.Equal(t, localDocument, stored.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, localDocument, common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey])
	common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, map[string]map[string]float64{"video-model": {"720p": 0.1}}, ratio_setting.GetVideoResolutionPriceMap())
}
