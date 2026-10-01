package service

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

	previousReady, previousMemoryCache, previousMaster := videoHealthReady.Load(), common.MemoryCacheEnabled, common.IsMasterNode
	t.Cleanup(func() {
		videoHealthReady.Store(previousReady)
		common.MemoryCacheEnabled, common.IsMasterNode = previousMemoryCache, previousMaster
	})
	common.IsMasterNode = true // a single instance needs no Redis

	for _, tc := range []struct {
		name          string
		mode          string
		models        []string
		entry         func(*gin.Context)
		want          VideoSchedDecision
		uncalibrated  bool
		noMemoryCache bool
	}{
		{"off never inspects candidates", operation_setting.VideoSchedulingModeOff, nil, protocol(megabyai, sora), VideoSchedDecision{Reason: VideoSchedReasonModeOff}, false, false},
		{"shadow observes a hooked pool", operation_setting.VideoSchedulingModeShadow, nil, protocol(megabyai, seedance), VideoSchedDecision{Shadow: true}, false, false},
		{"on takes over a listed hooked pool", operation_setting.VideoSchedulingModeOn, []string{"videos-fast"}, protocol(megabyai, seedance), VideoSchedDecision{Takeover: true}, false, false},
		{"on takes over the native entry", operation_setting.VideoSchedulingModeOn, nil, native, VideoSchedDecision{Takeover: true}, false, false},
		{"model outside the allow list", operation_setting.VideoSchedulingModeOn, []string{"videos-mini"}, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonModelNotListed}, false, false},
		{"one hook-less candidate blocks the pool", operation_setting.VideoSchedulingModeOn, nil, protocol(megabyai, sora), VideoSchedDecision{Reason: VideoSchedReasonMixedPool}, false, false},
		{"shadow is blocked by a mixed pool too", operation_setting.VideoSchedulingModeShadow, nil, protocol(sora, seedance), VideoSchedDecision{Reason: VideoSchedReasonMixedPool}, false, false},
		{"origin task continuation is not a submission", operation_setting.VideoSchedulingModeOn, nil, originTask, VideoSchedDecision{Reason: VideoSchedReasonNotSubmit}, false, false},
		// Mode on reaches a request only after in-flight gauges were calibrated
		// once and while its runtime prerequisites hold; shadow needs neither.
		{"on before the first calibration", operation_setting.VideoSchedulingModeOn, nil, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonNotReady}, true, false},
		{"on without the memory cache", operation_setting.VideoSchedulingModeOn, nil, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonNotReady}, false, true},
		{"shadow before the first calibration", operation_setting.VideoSchedulingModeShadow, nil, protocol(megabyai, seedance), VideoSchedDecision{Shadow: true}, true, false},
		// Shadow reads candidates from the memory cache like on does; without it
		// the candidate snapshot would silently be empty.
		{"shadow without the memory cache", operation_setting.VideoSchedulingModeShadow, nil, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonNotReady}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setVideoSchedulingForTest(t, tc.mode, tc.models...)
			videoHealthReady.Store(!tc.uncalibrated)
			common.MemoryCacheEnabled = !tc.noMemoryCache
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
		// 8 seconds x $1000 is past the int32 single-request quota, which
		// submission pre-consume rejects, so it can never be sold.
		{"sale past the single-request quota ceiling is unknown", func(c *gin.Context) {
			pinned(c)
			setExpr(`"u(\"seconds\") * 1000"`)
			t.Cleanup(func() { setExpr(`"u(\"seconds\") * 0.05"`) })
		}, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", seedance, "videos-fast", "videos-fast", seedanceBody, "")
		}, videosched.SellPrice{Kind: videosched.SellUnknown, Estimated: true}},
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

// createVideoSchedChannel stores an enabled task plugin channel serving
// videos-fast in group and bound to plugin, with the given settings JSON
// ("" = no video_scheduling) and model mapping ("" = none).
func createVideoSchedChannel(t *testing.T, db *gorm.DB, id int, group, plugin string, priority int64, settings, mapping string) {
	t.Helper()
	weight, autoBan := uint(100), 0
	setting := fmt.Sprintf(`{"task_plugin_key":%q}`, plugin)
	channel := &model.Channel{Id: id, Type: constant.ChannelTypeTaskPlugin, Key: "k", Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("video-%d", id),
		Weight: &weight, Models: "videos-fast", Group: group, Priority: &priority, Setting: &setting, OtherSettings: settings, AutoBan: &autoBan}
	if mapping != "" {
		channel.ModelMapping = &mapping
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(db))
}

// videoSchedProtocolRequest is a protocol-entry request for videos-fast that
// megabyai and seedance-hjmie both accepted, each with its own decoded body.
func videoSchedProtocolRequest(t *testing.T, body map[string]any, decision VideoSchedDecision) *gin.Context {
	t.Helper()
	generation, megabyai := builtinVideoPlugin(t, "megabyai")
	_, seedance := builtinVideoPlugin(t, "seedance-hjmie")
	c := newVideoSchedTestContext(t)
	endpoint := jsplugin.PinnedEndpoint{Generation: generation, Plugin: megabyai, Model: "videos-fast"}
	for _, plugin := range []*jsplugin.LoadedPlugin{megabyai, seedance} {
		endpoint.Candidates = append(endpoint.Candidates, jsplugin.ProtocolBinding{Plugin: plugin, Protocol: "openai_video", Model: "videos-fast", DecodedBody: body})
	}
	c.Set(jsplugin.ContextKeyPinnedEndpoint, endpoint)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: megabyai})
	c.Set("expected_task_plugin_key", "megabyai")
	c.Set("task_request", body)
	common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
	return c
}

func setVideoSellExprForTest(t *testing.T, variants string) {
	t.Helper()
	billingConfig := config.GlobalConfig.Get("billing_setting")
	saved := config.GlobalConfig.ExportAllConfigs()[billing_setting.PluginBillingExprOption]
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(billingConfig, map[string]string{"plugin_billing_expr": saved}))
	})
	require.NoError(t, config.UpdateConfigFromMap(billingConfig, map[string]string{"plugin_billing_expr": variants}))
}

// megabyai and seedance-hjmie share videos-fast; each channel is quoted with
// the plugin that would execute on it, from that plugin's own decoded body.
func TestVideoSchedulerQuotesEachCandidateWithItsOwnPlugin(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	useVideoHealthBackend(t, "memory")
	operation_setting.GetVideoSchedulingSetting().MaxCostToSellRatio = 1
	setVideoSellExprForTest(t, `{"megabyai::videos-fast":"u(\"seconds\") * 0.1","seedance-hjmie::videos-fast":"u(\"seconds\") * 0.1"}`)
	// megabyai is cheaper per second but buys each reference video for $1;
	// seedance-hjmie's price already includes them.
	createVideoSchedChannel(t, db, 3101, "default", "megabyai", 0,
		`{"video_scheduling":{"models":{"videos-fast":{"mode":"per_second","prices":{"720p":0.05},"references":{"video":{"*":{"mode":"per_input","value":1}}}}}}}`, "")
	createVideoSchedChannel(t, db, 3102, "default", "seedance-hjmie", 0,
		`{"video_scheduling":{"models":{"videos-fast":{"mode":"per_second","prices":{"720p":0.08},"references":{"video":{"*":{"mode":"included"}}}}}}}`, "")
	model.InitChannelCache()
	plugins := map[int]string{3101: "megabyai", 3102: "seedance-hjmie"}

	for _, tc := range []struct {
		name     string
		videos   []any
		want     int
		costs    map[int]float64
		excluded map[int]string
	}{
		{"the base price decides without references", nil, 3101, map[int]float64{3101: 0.4, 3102: 0.64}, nil},
		// 0.4 + 1 against a 0.8 sale is a 1.75 cost/sell ratio.
		{"a reference video reverses it and trips the loss ceiling", []any{"https://example.com/ref.mp4"}, 3102,
			map[int]float64{3101: 1.4, 3102: 0.64}, map[int]string{3101: "cost/sell 1.75 > 1.00"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"prompt": "cat", "duration": 8, "resolution": "720p"}
			if tc.videos != nil {
				body["videos"] = tc.videos
			}
			c := videoSchedProtocolRequest(t, body, VideoSchedDecision{Takeover: true})
			channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, tc.want, channel.Id)

			records := VideoScheduleRecords(c)
			require.Len(t, records, 1)
			assert.Equal(t, VideoScheduleRecord{SelectionSeq: 1, AttemptSeq: 1, Mode: "on", Group: "default", Recommended: tc.want}, VideoScheduleRecord{
				SelectionSeq: records[0].SelectionSeq, AttemptSeq: records[0].AttemptSeq, Mode: records[0].Mode, Group: records[0].Group, Recommended: records[0].Recommended})
			require.Len(t, records[0].Candidates, 2)
			for _, row := range records[0].Candidates {
				assert.Equal(t, plugins[row.ID], row.Plugin)
				assert.Equal(t, "videos-fast", row.MappedModel)
				require.NotNil(t, row.Spec)
				assert.Equal(t, len(tc.videos), row.Spec.References["video"])
				assert.Equal(t, videosched.SellKnown, row.SellKind)
				assert.InDelta(t, 0.8, row.SellUSD, 1e-9)
				require.NotNil(t, row.CostUSD)
				assert.InDelta(t, tc.costs[row.ID], *row.CostUSD, 1e-9)
				assert.Equal(t, tc.excluded[row.ID], row.Excluded)
			}
		})
	}
}

const videoSpecProbePlugin = `
export const meta = {apiVersion: 1, key: "spec-probe", name: "spec-probe", version: "1.0.0", author: {name: "Test"}, models: ["videos-fast"], fetchMode: "per_task"};
export function buildSubmitRequest() { return {url: "https://example.com"}; }
export function parseSubmitResponse() { return {taskId: "one"}; }
export function buildQueryRequest() { return {url: "https://example.com"}; }
export function parseTaskResult() { return {status: "SUCCESS"}; }
export function describeSpec(ctx) {
  if (ctx.upstreamModel === "future") return {spec_version: 2, references: {video: 0, image: 0, audio: 0}};
  if (ctx.upstreamModel === "opt-out") return {unsupported: true};
  if (ctx.upstreamModel === "broken") throw new Error("bad body");
  return {spec_version: 1, output_seconds: 5, resolution: "*", references: {video: 0, image: 0, audio: 0}};
}
`

func TestVideoSchedulerExclusionsLayersAndFallback(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	useVideoHealthBackend(t, "memory")
	setting := operation_setting.GetVideoSchedulingSetting()
	setting.MinSamples, setting.MinSubmitRate, setting.UnknownSellPolicy = 1, 0.8, "relative"
	setting.ProbeRatio, setting.ProbeMaxInFlight = 0, 1 // subtests turn probing on
	setting.CapacityGroups = map[string]int{"acct": 2}
	plugin, err := jsplugin.NewRegistry().Register(videoSpecProbePlugin, jsplugin.Options{})
	require.NoError(t, err)

	scheduled := func(capacity int, group string) string {
		return fmt.Sprintf(`{"video_scheduling":{"capacity":%d,"capacity_group":%q,"models":{"videos-fast":{"mode":"per_video","prices":{"*":1}}}}}`, capacity, group)
	}
	want := map[int]string{
		// Priority 10: every channel fails a gate the algorithm applies.
		3201: "submit rate 0.00 < 0.80",
		3202: "at capacity", // channel capacity
		3203: "at capacity", // capacity group
		// Priority 5: one eligible channel and every hard exclusion.
		3204: "",
		3205: "not schedulable: no cost table",
		3206: "not schedulable: model not priced",
		3207: "tried",
		3208: "spec version unsupported: 2",
		3209: "not schedulable: model opt-out",
		3210: "spec invalid: ",
	}
	createVideoSchedChannel(t, db, 3201, "default", "spec-probe", 10, scheduled(0, ""), "")
	createVideoSchedChannel(t, db, 3202, "default", "spec-probe", 10, scheduled(1, ""), "")
	createVideoSchedChannel(t, db, 3203, "default", "spec-probe", 10, scheduled(0, "acct"), "")
	createVideoSchedChannel(t, db, 3204, "default", "spec-probe", 5, scheduled(0, "unregistered"), "")
	createVideoSchedChannel(t, db, 3205, "default", "spec-probe", 5, "", "")
	createVideoSchedChannel(t, db, 3206, "default", "spec-probe", 5, `{"video_scheduling":{"models":{"videos-mini":{"mode":"per_video","prices":{"*":1}}}}}`, "")
	createVideoSchedChannel(t, db, 3207, "default", "spec-probe", 5, scheduled(0, ""), "")
	createVideoSchedChannel(t, db, 3208, "default", "spec-probe", 5, scheduled(0, ""), `{"videos-fast":"future"}`)
	createVideoSchedChannel(t, db, 3209, "default", "spec-probe", 5, scheduled(0, ""), `{"videos-fast":"opt-out"}`)
	createVideoSchedChannel(t, db, 3210, "default", "spec-probe", 5, scheduled(0, ""), `{"videos-fast":"broken"}`)
	model.InitChannelCache()
	recordVideoSamples(3201, "videos-fast", videoSubmitFail)
	store := videoHealthStore()
	_, err = store.add(videoInFlightKey(3202), 1)
	require.NoError(t, err)
	_, err = store.add(videoGroupInFlightKey("acct"), 2)
	require.NoError(t, err)

	request := func(decision VideoSchedDecision, tried ...int) *gin.Context {
		c := newVideoSchedTestContext(t)
		c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Plugin: plugin})
		c.Set("task_request", map[string]any{"prompt": "cat"})
		common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
		for _, id := range tried {
			RequestPolicy(c).BeginAttempt(&model.Channel{Id: id}, "default")
		}
		return c
	}

	t.Run("the highest layer with an eligible candidate wins", func(t *testing.T) {
		c := request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id, "an unregistered capacity group is no group")
		records := VideoScheduleRecords(c)
		require.Len(t, records, 1)
		assert.Equal(t, 2, records[0].AttemptSeq, "the selection feeds the attempt after the tried one")
		got := map[int]string{}
		for _, row := range records[0].Candidates {
			got[row.ID] = row.Excluded
			if strings.HasPrefix(row.Excluded, "spec invalid: ") {
				got[row.ID] = "spec invalid: "
			}
		}
		assert.Equal(t, want, got)
	})

	t.Run("a frozen takeover keeps its candidates after scheduling is switched off", func(t *testing.T) {
		c := request(VideoSchedDecision{Takeover: true}, 3207)
		setting.Mode = operation_setting.VideoSchedulingModeOff
		t.Cleanup(func() { setting.Mode = operation_setting.VideoSchedulingModeShadow })
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id)
	})

	t.Run("a gated channel is probed and a lost slot is decided again", func(t *testing.T) {
		setting.ProbeRatio = 1
		lastProbeKey, _ := videoProbeStateKeys(3201)
		restartCooldown := func() { require.NoError(t, videoHealthStore().set(lastProbeKey, 0)) }
		t.Cleanup(func() {
			setting.ProbeRatio = 0
			restartCooldown()
		})

		// 3201 fails the submit gate in the top layer; with the gates relaxed it
		// is the only eligible channel there, so it is probed.
		c := request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3201, channel.Id)
		records := VideoScheduleRecords(c)
		require.Len(t, records, 1)
		assert.True(t, records[0].Probe)
		assert.NotEmpty(t, records[0].Fingerprint)
		lease, leased := peekVideoProbeLease(c)
		require.True(t, leased)
		assert.Equal(t, 3201, lease.ChannelID)
		health, err := GetVideoChannelHealth(3201, "videos-fast", 1)
		require.NoError(t, err)
		assert.Positive(t, health.Probe.LastProbeAt, "the cooldown starts at acquisition")

		// The task inherits the selection, so terminal logs correlate by task.
		RequestPolicy(c).BeginAttempt(channel, "default")
		summary := NewVideoSchedulingSummary(c, channel, "videos-fast")
		require.NotNil(t, summary)
		require.NotNil(t, summary.ProbeSlot)
		assert.Equal(t, lease.Token, summary.ProbeSlot.Token)
		assert.Equal(t, 3201, summary.Selected)
		assert.Equal(t, 1, summary.SelectionSeq)
		assert.Equal(t, 2, summary.AttemptSeq, "3207 was attempted first")
		assert.True(t, summary.Probe)
		require.NotNil(t, summary.Spec)
		assert.Equal(t, map[string]int{"video": 0, "image": 0, "audio": 0}, summary.Spec.References)
		other := model.NewLogOther()
		AppendVideoScheduleConsumeLog(c, &model.Task{ChannelId: 3201}, other)
		adminInfo := other.Snapshot()["admin_info"].(map[string]any)
		schedule := adminInfo["video_schedule"].(map[string]any)
		assert.Equal(t, "on", schedule["mode"])
		assert.Equal(t, 3201, schedule["selected"])
		assert.Equal(t, true, schedule["probe"])
		assert.Len(t, schedule["attempts"], 1)
		assert.NotContains(t, other.Snapshot(), "video_schedule")
		ReleaseUnpersistedVideoProbeLease(c)
		restartCooldown()

		// Another request takes the slot between this one's decision and its
		// admission: the lost choice is recorded, and a fresh snapshot that
		// sees the held slot decides again under a new selection_seq.
		previous := acquireVideoProbeSlot
		t.Cleanup(func() { acquireVideoProbeSlot = previous })
		rival := newVideoSchedTestContext(t)
		acquireVideoProbeSlot = func(_ *gin.Context, channelID, n int, ttl time.Duration) (bool, error) {
			won, err := AcquireVideoProbeSlot(rival, channelID, n, ttl)
			require.NoError(t, err)
			require.True(t, won)
			return false, nil
		}
		c = request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err = model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id)
		records = VideoScheduleRecords(c)
		require.Len(t, records, 2)
		assert.Equal(t, []int{1, 2}, []int{records[0].SelectionSeq, records[1].SelectionSeq})
		assert.Equal(t, []int{2, 2}, []int{records[0].AttemptSeq, records[1].AttemptSeq})
		assert.Equal(t, 3201, records[0].Recommended)
		assert.Equal(t, VideoSchedAdmissionProbeTaken, records[0].Admission)
		assert.Equal(t, 3204, records[1].Recommended)
		assert.False(t, records[1].Probe)
		assert.NotEqual(t, records[0].Fingerprint, records[1].Fingerprint)
		_, leased = peekVideoProbeLease(c)
		assert.False(t, leased)
		ReleaseUnpersistedVideoProbeLease(rival)
	})

	t.Run("a channel disabled while it was scored is decided again", func(t *testing.T) {
		setting.ProbeRatio = 1
		lastProbeKey, _ := videoProbeStateKeys(3201)
		previous := acquireVideoProbeSlot
		t.Cleanup(func() {
			setting.ProbeRatio = 0
			acquireVideoProbeSlot = previous
			require.NoError(t, videoHealthStore().set(lastProbeKey, 0))
			// Re-enabling only flips the status; a rebuild restores the index.
			model.InitChannelCache()
		})
		// 3201 is probed; it is disabled after scoring, before submission.
		acquireVideoProbeSlot = func(c *gin.Context, channelID, n int, ttl time.Duration) (bool, error) {
			model.CacheUpdateChannelStatus(channelID, common.ChannelStatusAutoDisabled)
			return AcquireVideoProbeSlot(c, channelID, n, ttl)
		}
		c := request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id)
		assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
		records := VideoScheduleRecords(c)
		require.Len(t, records, 2)
		assert.Equal(t, 3201, records[0].Recommended)
		assert.Equal(t, VideoSchedAdmissionChannelUnavailable, records[0].Admission)
		assert.Equal(t, 3204, records[1].Recommended)
		assert.Empty(t, records[1].Admission)
		for _, row := range records[1].Candidates {
			assert.NotEqual(t, 3201, row.ID, "the fresh snapshot no longer holds the disabled channel")
		}
		_, leased := peekVideoProbeLease(c)
		assert.False(t, leased, "the disabled channel's probe slot is released")
	})

	t.Run("no eligible candidate never falls back to ordinary selection", func(t *testing.T) {
		channel, err := model.GetRandomSatisfiedChannelWithContext(request(VideoSchedDecision{Takeover: true}, 3204, 3207), "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		assert.Nil(t, channel)
	})

	t.Run("a request not taken over keeps ordinary selection", func(t *testing.T) {
		c := request(VideoSchedDecision{Reason: VideoSchedReasonMixedPool}, 3207)
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Contains(t, []int{3201, 3202, 3203}, channel.Id, "the priority retry index picks the top layer")
		assert.Empty(t, VideoScheduleRecords(c))
	})

	t.Run("shadow scores after ordinary selection and writes no state", func(t *testing.T) {
		// Shadow recommends the probe but never acquires its slot.
		setting.ProbeRatio = 1
		t.Cleanup(func() { setting.ProbeRatio = 0 })
		// fmt prints maps in key order, so equal text is equal state.
		snapshot := func() string {
			memoryVideoHealth.mu.Lock()
			defer memoryVideoHealth.mu.Unlock()
			return fmt.Sprint(memoryVideoHealth.hashes, memoryVideoHealth.gauges, memoryVideoHealth.slots)
		}
		before := snapshot()
		c := request(VideoSchedDecision{Shadow: true})
		channel, selectGroup, selectErr := SelectChannelForRequest(c, "videos-fast", &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "videos-fast", Retry: common.GetPointer(0)})
		require.Nil(t, selectErr)
		require.NotNil(t, channel)
		assert.Contains(t, []int{3201, 3202, 3203}, channel.Id)
		records := VideoScheduleRecords(c)
		require.Len(t, records, 1)
		assert.Equal(t, "shadow", records[0].Mode)
		assert.Equal(t, selectGroup, records[0].Group)
		assert.Equal(t, channel.Id, records[0].Selected)
		assert.Equal(t, 3201, records[0].Recommended)
		assert.True(t, records[0].Probe)
		assert.False(t, records[0].AffinityHit)
		assert.Equal(t, before, snapshot())
		_, leased := peekVideoProbeLease(c)
		assert.False(t, leased)

		// The probe is hypothetical: whichever channel ordinary selection
		// submitted to, its task is no probe, while the recommendation keeps
		// the flag.
		if RequestPolicy(c).Attempts == 0 {
			RequestPolicy(c).BeginAttempt(channel, selectGroup)
		}
		for _, id := range []int{3201, 3202, 3203} {
			submitted, err := model.CacheGetChannel(id)
			require.NoError(t, err)
			summary := NewVideoSchedulingSummary(c, submitted, "videos-fast")
			require.NotNil(t, summary)
			assert.Equal(t, id, summary.Selected)
			assert.False(t, summary.Probe, id)
			assert.Nil(t, summary.ProbeSlot, id)
			other := model.NewLogOther()
			AppendVideoScheduleConsumeLog(c, &model.Task{ChannelId: id}, other)
			schedule := other.Snapshot()["admin_info"].(map[string]any)["video_schedule"].(map[string]any)
			assert.Equal(t, false, schedule["probe"], id)
			attempts := schedule["attempts"].([]VideoScheduleRecord)
			require.Len(t, attempts, 1)
			assert.True(t, attempts[0].Probe, "the recommendation keeps its probe flag")
		}
	})

	t.Run("takeover skips session affinity and shadow keeps it", func(t *testing.T) {
		affinity := operation_setting.GetChannelAffinitySetting()
		previousAffinity := *affinity
		t.Cleanup(func() { *affinity = previousAffinity })
		snapshot, err := model.BuildRequestPolicy(map[string]string{
			"channel_affinity_setting.enabled": "true",
			"channel_affinity_setting.rules":   `[{"name":"session","model_regex":[".*"],"key_sources":[{"type":"request_header","key":"X-Session"}]}]`,
		})
		require.NoError(t, err)
		*affinity = snapshot.Affinity
		session := func(decision VideoSchedDecision) *gin.Context {
			c := request(decision)
			c.Request.Header.Set("X-Session", t.Name())
			return c
		}
		seed := session(VideoSchedDecision{})
		_, found := GetPreferredChannelByAffinity(seed, "videos-fast", "default")
		require.False(t, found)
		seed.Set("channel_id", 3201)
		RecordChannelAffinity(seed, 3201)
		t.Cleanup(func() { ClearCurrentChannelAffinityCache(seed) })

		shadow := session(VideoSchedDecision{Shadow: true})
		channel, _, selectErr := SelectChannelForRequest(shadow, "videos-fast", &RetryParam{Ctx: shadow, TokenGroup: "default", ModelName: "videos-fast", Retry: common.GetPointer(0)})
		require.Nil(t, selectErr)
		assert.Equal(t, 3201, channel.Id)
		records := VideoScheduleRecords(shadow)
		require.Len(t, records, 1)
		assert.True(t, records[0].AffinityHit)
		assert.Equal(t, 3204, records[0].Recommended)

		takeover := session(VideoSchedDecision{Takeover: true})
		channel, _, selectErr = SelectChannelForRequest(takeover, "videos-fast", &RetryParam{Ctx: takeover, TokenGroup: "default", ModelName: "videos-fast", Retry: common.GetPointer(0)})
		require.Nil(t, selectErr)
		assert.Equal(t, 3204, channel.Id)
		bound, found := GetPreferredChannelByAffinity(seed, "videos-fast", "default")
		assert.True(t, found, "the scheduler neither reads nor clears the binding")
		assert.Equal(t, 3201, bound)
	})
}

// A request video scheduling took over leaves 429 and 5xx to the health gate;
// the disable decision and the policy audit event read the same answer.
func TestShouldDisableChannelForRequestYieldsTransientFailuresToScheduling(t *testing.T) {
	previousEnabled, previousRanges := common.AutomaticDisableChannelEnabled, operation_setting.AutomaticDisableStatusCodeRanges
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled, operation_setting.AutomaticDisableStatusCodeRanges = previousEnabled, previousRanges
	})
	common.AutomaticDisableChannelEnabled = true
	operation_setting.AutomaticDisableStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 401, End: 401}, {Start: 429, End: 429}, {Start: 500, End: 599}}

	for _, tc := range []struct {
		name     string
		takeover bool
		status   int
		message  string
		want     bool
	}{
		{"takeover 502", true, http.StatusBadGateway, "bad gateway", false},
		{"takeover 429", true, http.StatusTooManyRequests, "slow down", false},
		{"takeover quota exhausted", true, http.StatusTooManyRequests, "You exceeded your current quota", true},
		{"takeover credential failure", true, http.StatusUnauthorized, "unauthorized", true},
		{"ordinary 502", false, http.StatusBadGateway, "bad gateway", true},
		{"ordinary 429", false, http.StatusTooManyRequests, "slow down", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, VideoSchedDecision{Takeover: tc.takeover, Shadow: !tc.takeover})
			c.Set("auto_ban", true)
			apiErr := types.NewOpenAIError(errors.New(tc.message), types.ErrorCodeBadResponseStatusCode, tc.status) // as task submission reports upstream failures
			assert.Equal(t, tc.want, ShouldDisableChannelForRequest(c, apiErr))

			RecordPolicyFailure(c, 1, apiErr, PolicyDecision{Action: "retry"})
			events := RequestPolicy(c).Events()
			health := "unchanged"
			if tc.want {
				health = "channel_disable_requested"
			}
			assert.Equal(t, health, events[len(events)-1].Health)
		})
	}
}

// videoDecisionTestInput is a hand-built decision input: channel 1 is healthy
// and proven, every other channel is added by the case. Probing and
// exploration are off unless a case turns them on.
func videoDecisionTestInput(candidates ...videosched.Candidate) VideoDecisionInput {
	healthy := videosched.HealthStat{Rate: 1, Samples: 20}
	return VideoDecisionInput{
		Candidates: append([]videosched.Candidate{videoDecisionTestCandidate(1, 0, healthy, healthy)}, candidates...),
		Policy: videosched.Policy{
			Weights:       videosched.DefaultWeights,
			MinSubmitRate: 0.8, MinGenRate: 0.5, MinSamples: 10,
			UnknownSellPolicy: videosched.UnknownSellExclude,
			MaxCostUSD:        1e6,
		},
		Explore:       VideoExploreSettings{ProbeCooldownSec: 300, ProbeMaxInFlight: 1, ExploreMaxInFlight: 2},
		Probe:         map[int]VideoProbeState{},
		SlotOccupancy: map[int]int{},
		Now:           time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Seed:          7,
	}
}

func videoDecisionTestCandidate(id int, priority int64, submit, gen videosched.HealthStat) videosched.Candidate {
	return videosched.Candidate{
		ID: id, Name: fmt.Sprintf("video-%d", id), Priority: priority, Weight: 1, Quality: 0.5,
		Cost:   videosched.CostConfig{Mode: videosched.ModePerVideo, Prices: map[string]float64{"*": 1}},
		Spec:   videosched.Spec{Tier: "*", References: map[string]int{"video": 0, "image": 0, "audio": 0}},
		Sell:   videosched.SellPrice{Kind: videosched.SellKnown, USD: 10},
		Submit: submit, Gen: gen,
	}
}

func TestDecideVideoScheduleProbesAndExplores(t *testing.T) {
	healthy := videosched.HealthStat{Rate: 1, Samples: 20}
	failing := videosched.HealthStat{Rate: 0, Samples: 20}
	gated := func(id int, priority int64) videosched.Candidate {
		return videoDecisionTestCandidate(id, priority, failing, healthy)
	}
	unproven := func(id, samples int) videosched.Candidate {
		return videoDecisionTestCandidate(id, 0, videosched.HealthStat{Rate: 1, Samples: samples}, videosched.HealthStat{Rate: 1, Samples: samples})
	}
	probing := func(input *VideoDecisionInput) { input.Explore.ProbeRatio = 1 }

	for _, tc := range []struct {
		name     string
		extra    []videosched.Candidate
		setup    func(*VideoDecisionInput)
		want     int
		probe    bool
		explore  bool
		excluded map[int]string
	}{
		{"a gated channel gets a probe despite a lower score", []videosched.Candidate{gated(2, 0)}, probing, 2, true, false, nil},
		{"no probe draw keeps it gated", []videosched.Candidate{gated(2, 0)}, nil, 1, false, false, map[int]string{2: "submit rate 0.00 < 0.80"}},
		{"a disabled channel is never probed", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Candidates[1].Excluded = "disabled"
		}, 1, false, false, map[int]string{2: "disabled"}},
		{"a tried channel is never probed", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Candidates[1].Excluded = "tried"
		}, 1, false, false, nil},
		{"an at-capacity channel is never probed", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Candidates[1].Capacity, input.Candidates[1].InFlight = 1, 1
		}, 1, false, false, nil},
		{"an unpriced channel is never probed", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Candidates[1].Cost.Prices = nil
		}, 1, false, false, nil},
		{"the cooldown doubles per consecutive failure", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Probe[2] = VideoProbeState{LastProbeAt: input.Now.Add(-9 * time.Minute).Unix(), ConsecutiveFails: 1}
		}, 1, false, false, nil},
		{"an elapsed cooldown probes again", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Probe[2] = VideoProbeState{LastProbeAt: input.Now.Add(-10 * time.Minute).Unix(), ConsecutiveFails: 1}
		}, 2, true, false, nil},
		{"held probe slots block the probe", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.SlotOccupancy[2] = 1
		}, 1, false, false, nil},
		{"a lower layer is never probed across a live higher layer", []videosched.Candidate{gated(2, -1)}, probing, 1, false, false, nil},
		{"a higher layer holding only gated channels is probed", []videosched.Candidate{gated(2, 5)}, probing, 2, true, false, nil},
		{"with proven channels the fewest-samples unproven one is explored", []videosched.Candidate{unproven(3, 5), unproven(4, 1)}, func(input *VideoDecisionInput) {
			input.Explore.ExploreShare = 1
		}, 4, false, true, nil},
		{"without an explore draw unproven channels stay out of the argmax", []videosched.Candidate{unproven(3, 5), unproven(4, 1)}, nil, 1, false, false,
			map[int]string{3: "unproven", 4: "unproven"}},
		{"without proven channels unproven ones are capped per channel", []videosched.Candidate{unproven(3, 0), unproven(4, 0)}, func(input *VideoDecisionInput) {
			input.Candidates = input.Candidates[1:]
			input.Candidates[0].Quality, input.Candidates[0].InFlight = 1, 2
		}, 4, false, false, map[int]string{3: "explore limit"}},
		{"a zero explore cap leaves unproven channels uncapped", []videosched.Candidate{unproven(3, 0), unproven(4, 0)}, func(input *VideoDecisionInput) {
			input.Candidates = input.Candidates[1:]
			input.Candidates[0].Quality, input.Candidates[0].InFlight = 1, 2
			input.Explore.ExploreMaxInFlight = 0
		}, 3, false, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := videoDecisionTestInput(tc.extra...)
			if tc.setup != nil {
				tc.setup(&input)
			}
			choice := DecideVideoSchedule(input)
			require.NotNil(t, choice.Best)
			assert.Equal(t, tc.want, choice.Best.ID)
			assert.Equal(t, tc.probe, choice.Probe)
			assert.Equal(t, tc.explore, choice.Explore)
			for id, reason := range tc.excluded {
				for _, score := range choice.Board {
					if score.Candidate.ID == id {
						assert.Equal(t, reason, score.Reason, "channel %d", id)
					}
				}
			}
			assert.Equal(t, choice, DecideVideoSchedule(input), "the same input decides the same")
		})
	}
}

// The fingerprint covers every input of the decision, probe and explore
// included, and the segment hashes name the part that changed.
func TestVideoDecisionFingerprintPinsEveryInput(t *testing.T) {
	encoded, err := common.Marshal(map[string]int{"b": 1, "a": 2})
	require.NoError(t, err)
	require.Equal(t, `{"a":2,"b":1}`, string(encoded), "canonical JSON relies on common.Marshal sorting map keys")

	base := func() VideoDecisionInput {
		input := videoDecisionTestInput(videoDecisionTestCandidate(2, 0, videosched.HealthStat{Rate: 0, Samples: 20}, videosched.HealthStat{Rate: 1, Samples: 20}))
		input.Probe[2] = VideoProbeState{LastProbeAt: 100, ConsecutiveFails: 1}
		return input
	}
	whole, segments, err := VideoDecisionFingerprint(base())
	require.NoError(t, err)
	again, againSegments, err := VideoDecisionFingerprint(base())
	require.NoError(t, err)
	assert.Equal(t, whole, again)
	assert.Equal(t, segments, againSegments)
	assert.ElementsMatch(t, []string{"candidates", "settings", "probe", "slots", "now", "seed"}, slices.Collect(maps.Keys(segments)))

	for _, tc := range []struct {
		name    string
		segment string
		change  func(*VideoDecisionInput)
	}{
		{"last probe time", "probe", func(input *VideoDecisionInput) {
			input.Probe[2] = VideoProbeState{LastProbeAt: 101, ConsecutiveFails: 1}
		}},
		{"consecutive failures", "probe", func(input *VideoDecisionInput) {
			input.Probe[2] = VideoProbeState{LastProbeAt: 100, ConsecutiveFails: 2}
		}},
		{"probe cooldown", "settings", func(input *VideoDecisionInput) { input.Explore.ProbeCooldownSec = 301 }},
		{"probe ratio", "settings", func(input *VideoDecisionInput) { input.Explore.ProbeRatio = 0.5 }},
		{"probe slots", "settings", func(input *VideoDecisionInput) { input.Explore.ProbeMaxInFlight = 2 }},
		{"explore share", "settings", func(input *VideoDecisionInput) { input.Explore.ExploreShare = 0.2 }},
		{"explore cap", "settings", func(input *VideoDecisionInput) { input.Explore.ExploreMaxInFlight = 3 }},
		{"cost bound", "settings", func(input *VideoDecisionInput) { input.Policy.MaxCostUSD = 2e6 }},
		{"slot occupancy", "slots", func(input *VideoDecisionInput) { input.SlotOccupancy[2] = 1 }},
		{"clock", "now", func(input *VideoDecisionInput) { input.Now = input.Now.Add(time.Second) }},
		{"seed", "seed", func(input *VideoDecisionInput) { input.Seed = 8 }},
		{"in-flight count", "candidates", func(input *VideoDecisionInput) { input.Candidates[1].InFlight = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base()
			tc.change(&input)
			changed, changedSegments, err := VideoDecisionFingerprint(input)
			require.NoError(t, err)
			assert.NotEqual(t, whole, changed)
			for name, hash := range segments {
				if name == tc.segment {
					assert.NotEqual(t, hash, changedSegments[name], name)
				} else {
					assert.Equal(t, hash, changedSegments[name], name)
				}
			}
		})
	}
}
