package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newVideoSchedTestContext(t *testing.T) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func setVideoSchedulingForTest(t *testing.T, mode string, models ...string) {
	t.Helper()
	setting := operation_setting.GetVideoSchedulingSetting()
	saved := *setting
	t.Cleanup(func() { *setting = saved })
	setting.Mode = mode
	setting.Models = models
}

// megabyai and seedance-hjmie are real built-in plugins sharing the model
// names videos-mini/fast/standard and exporting describeSpec; sora does not.
func builtinVideoPlugin(t *testing.T, key string) (*jsplugin.RoutingGeneration, *jsplugin.LoadedPlugin) {
	t.Helper()
	generation := jsplugin.DefaultRegistry.Generation()
	plugin, ok := generation.Get(key)
	require.True(t, ok, key)
	return generation, plugin
}

func TestDecideVideoSchedFreezesOneRequestLevelAnswer(t *testing.T) {
	generation, megabyai := builtinVideoPlugin(t, "megabyai")
	_, seedance := builtinVideoPlugin(t, "seedance-hjmie")
	_, sora := builtinVideoPlugin(t, "sora")
	protocol := func(plugins ...*jsplugin.LoadedPlugin) func(*gin.Context) {
		return func(c *gin.Context) {
			endpoint := jsplugin.PinnedEndpoint{Generation: generation, Plugin: plugins[0], Model: "videos-fast"}
			for _, plugin := range plugins {
				endpoint.Candidates = append(endpoint.Candidates, jsplugin.ProtocolBinding{Plugin: plugin, Protocol: "openai_video", Model: "videos-fast"})
			}
			c.Set(jsplugin.ContextKeyPinnedEndpoint, endpoint)
			c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: plugins[0]})
		}
	}
	native := func(c *gin.Context) {
		c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: seedance})
	}
	originTask := func(c *gin.Context) {
		protocol(megabyai, seedance)(c)
		common.SetContextKey(c, constant.ContextKeyOriginTasks, []*model.Task{{TaskID: "task_origin"}})
	}

	for _, tc := range []struct {
		name   string
		mode   string
		models []string
		entry  func(*gin.Context)
		want   VideoSchedDecision
	}{
		{"off never inspects candidates", operation_setting.VideoSchedulingModeOff, nil, protocol(megabyai, sora), VideoSchedDecision{Reason: VideoSchedReasonModeOff}},
		{"shadow observes a hooked pool", operation_setting.VideoSchedulingModeShadow, nil, protocol(megabyai, seedance), VideoSchedDecision{Shadow: true}},
		{"on takes over a listed hooked pool", operation_setting.VideoSchedulingModeOn, []string{"videos-fast"}, protocol(megabyai, seedance), VideoSchedDecision{Takeover: true}},
		{"on takes over the native entry", operation_setting.VideoSchedulingModeOn, nil, native, VideoSchedDecision{Takeover: true}},
		{"model outside the allow list", operation_setting.VideoSchedulingModeOn, []string{"videos-mini"}, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonModelNotListed}},
		{"one hook-less candidate blocks the pool", operation_setting.VideoSchedulingModeOn, nil, protocol(megabyai, sora), VideoSchedDecision{Reason: VideoSchedReasonMixedPool}},
		{"shadow is blocked by a mixed pool too", operation_setting.VideoSchedulingModeShadow, nil, protocol(sora, seedance), VideoSchedDecision{Reason: VideoSchedReasonMixedPool}},
		{"origin task continuation is not a submission", operation_setting.VideoSchedulingModeOn, nil, originTask, VideoSchedDecision{Reason: VideoSchedReasonNotSubmit}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setVideoSchedulingForTest(t, tc.mode, tc.models...)
			c := newVideoSchedTestContext(t)
			c.Set("resolved_task_model", "videos-fast")
			tc.entry(c)

			assert.Equal(t, tc.want, DecideVideoSched(c))
			// Changing the global mode afterwards does not move the frozen answer.
			operation_setting.GetVideoSchedulingSetting().Mode = operation_setting.VideoSchedulingModeOff
			assert.Equal(t, tc.want, VideoSchedDecisionFrom(c))
		})
	}

	t.Run("entries without a decision never schedule", func(t *testing.T) {
		assert.Equal(t, VideoSchedDecision{}, VideoSchedDecisionFrom(newVideoSchedTestContext(t)))
		assert.Equal(t, VideoSchedDecision{}, VideoSchedDecisionFrom(nil))
	})
}

func TestVideoSchedStaticBlockersNameHooklessPluginsSharingAModel(t *testing.T) {
	registry := jsplugin.NewRegistry()
	source, err := plugins.Source("seedance-hjmie")
	require.NoError(t, err)
	_, err = registry.RegisterFactory(source, jsplugin.Options{Key: "seedance-hjmie"})
	require.NoError(t, err)
	_, err = registry.Register(`
export const meta = {apiVersion: 1, key: "hookless-video", name: "hookless-video", version: "1.0.0", author: {name: "Test"},
  models: ["videos-fast"], fetchMode: "per_task", protocols: ["openai_video"]};
export function buildSubmitRequest() { return {url: "https://example.com"}; }
export function parseSubmitResponse() { return {taskId: "one"}; }
export function buildQueryRequest() { return {url: "https://example.com"}; }
export function parseTaskResult() { return {status: "SUCCESS"}; }
export function listArtifacts() { return []; }
export function buildContentRequest() { throw new Error("artifact_not_found"); }
export const protocols = {openai_video: {
  decodeRequest: function(ctx) { return {kind: "submit", model: ctx.model}; },
  render: function() { return {}; },
}};
`, jsplugin.Options{})
	require.NoError(t, err)
	generation := registry.Generation()

	assert.Equal(t, []string{"hookless-video"}, VideoSchedStaticBlockers(t.Context(), generation, "videos-fast"))
	assert.Empty(t, VideoSchedStaticBlockers(t.Context(), generation, "videos-mini"))
	// An empty allow list schedules every model of a hooked plugin; only the
	// shared one is reported.
	assert.Contains(t, videoSchedBlockerWarning(t.Context(), generation, nil), " videos-fast=[hookless-video]")
	assert.NotContains(t, videoSchedBlockerWarning(t.Context(), generation, nil), "videos-mini")
	assert.Empty(t, videoSchedBlockerWarning(t.Context(), generation, []string{"videos-mini"}))
}

func TestEstimateVideoSellMirrorsSubmissionPricing(t *testing.T) {
	generation, seedance := builtinVideoPlugin(t, "seedance-hjmie")
	_, megabyai := builtinVideoPlugin(t, "megabyai")
	billingConfig := config.GlobalConfig.Get("billing_setting")
	savedExpr := config.GlobalConfig.ExportAllConfigs()[billing_setting.PluginBillingExprOption]
	savedPrices := ratio_setting.ModelPrice2JSONString()
	savedGroups := ratio_setting.GroupRatio2JSONString()
	savedGroupGroups := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(billingConfig, map[string]string{"plugin_billing_expr": savedExpr}))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(savedGroupGroups))
	})
	setExpr := func(expr string) {
		require.NoError(t, config.UpdateConfigFromMap(billingConfig, map[string]string{"plugin_billing_expr": `{"seedance-hjmie::videos-fast":` + expr + `}`}))
	}
	setExpr(`"u(\"seconds\") * 0.05"`)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"videos-mini":0.1,"videos-standard":0}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"gift":0}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"svip":{"vip":0.5}}`))

	seedanceBody := map[string]any{"prompt": "cat", "duration": 8, "resolution": "720p"}
	megabyaiBody := map[string]any{"prompt": "cat", "duration": 8, "resolution": "720p"}
	pinned := func(c *gin.Context) {
		c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: seedance})
	}

	t.Run("expression price is cached before the group ratio", func(t *testing.T) {
		c := newVideoSchedTestContext(t)
		pinned(c)
		sell := EstimateVideoSell(c, "default", seedance, "videos-fast", "videos-fast", seedanceBody, "text_to_video")
		assert.Equal(t, videosched.SellKnown, sell.Kind)
		assert.InDelta(t, 0.4, sell.USD, 1e-9)
		assert.True(t, sell.Estimated)

		// The request keeps its cached pre-group price even if pricing changes,
		// while every call applies the ratio of the group it is asked about.
		setExpr(`"u(\"seconds\") * 1"`)
		t.Cleanup(func() { setExpr(`"u(\"seconds\") * 0.05"`) })
		assert.InDelta(t, 0.8, EstimateVideoSell(c, "vip", seedance, "videos-fast", "videos-fast", seedanceBody, "").USD, 1e-9)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "svip")
		assert.InDelta(t, 0.2, EstimateVideoSell(c, "vip", seedance, "videos-fast", "videos-fast", seedanceBody, "").USD, 1e-9)
		assert.Equal(t, videosched.SellPrice{Kind: videosched.SellFree, Estimated: true},
			EstimateVideoSell(c, "gift", seedance, "videos-fast", "videos-fast", seedanceBody, ""))
	})

	for _, tc := range []struct {
		name  string
		setup func(*gin.Context)
		call  func(*gin.Context) videosched.SellPrice
		want  videosched.SellPrice
	}{
		{"usage hook rejecting the body is unknown", pinned, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", seedance, "videos-fast", "videos-fast", map[string]any{"prompt": "cat", "resolution": "720p"}, "")
		}, videosched.SellPrice{Kind: videosched.SellUnknown}},
		{"shared-model expression outside the plugin schema is unknown", func(c *gin.Context) {
			c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: megabyai})
			setExpr(`"u(\"frames\") * 0.05"`)
			t.Cleanup(func() { setExpr(`"u(\"seconds\") * 0.05"`) })
		}, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", seedance, "videos-fast", "videos-fast", seedanceBody, "")
		}, videosched.SellPrice{Kind: videosched.SellUnknown}},
		// Legacy per-call pricing multiplies every positive usage fact, exactly
		// as submission does: 0.1 x seconds 8 x surcharge_seconds 8.
		{"per-call price times billing ratios", pinned, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "vip", megabyai, "videos-mini", "videos-mini", megabyaiBody, "")
		}, videosched.SellPrice{Kind: videosched.SellKnown, USD: 12.8, Estimated: true}},
		{"zero per-call price is free", pinned, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", megabyai, "videos-standard", "videos-standard", megabyaiBody, "")
		}, videosched.SellPrice{Kind: videosched.SellFree, Estimated: true}},
		{"no price configured is unknown", pinned, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", megabyai, "videos-fast", "videos-fast", megabyaiBody, "")
		}, videosched.SellPrice{Kind: videosched.SellUnknown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			tc.setup(c)
			got := tc.call(c)
			assert.Equal(t, tc.want.Kind, got.Kind)
			assert.InDelta(t, tc.want.USD, got.USD, 1e-9)
			assert.Equal(t, tc.want.Estimated, got.Estimated)
		})
	}
}
