package operation_setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoSchedMaxCostUSD(t *testing.T) {
	orig := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = orig })

	common.QuotaPerUnit = 500 * 1000.0
	bound, err := VideoSchedMaxCostUSD()
	require.NoError(t, err)
	assert.Equal(t, float64(common.MaxWalletQuota)/500000, bound)

	for _, perUnit := range []float64{0, -1, 1e-300} {
		common.QuotaPerUnit = perUnit
		_, err := VideoSchedMaxCostUSD()
		assert.Error(t, err, "QuotaPerUnit=%v", perUnit)
	}
}

func TestValidateVideoSchedulingOption(t *testing.T) {
	origMemory, origRedis, origMaster := common.MemoryCacheEnabled, common.RedisEnabled, common.IsMasterNode
	t.Cleanup(func() {
		common.MemoryCacheEnabled, common.RedisEnabled, common.IsMasterNode = origMemory, origRedis, origMaster
	})
	common.MemoryCacheEnabled, common.RedisEnabled, common.IsMasterNode = true, true, true

	t.Run("reliability_policy_bounds_apply_to_saved_and_simulated_settings", func(t *testing.T) {
		original := videoSchedulingSetting
		t.Cleanup(func() { videoSchedulingSetting = original })
		videoSchedulingSetting.SelectionPolicy = videosched.PolicyStabilityCostV2
		videoSchedulingSetting.Mode = VideoSchedulingModeOn
		videoSchedulingSetting.MinGenRate, videoSchedulingSetting.MinOverallRate = .8, .6
		videoSchedulingSetting.MinSamples, videoSchedulingSetting.ExploreMaxInFlight = 20, 2
		videoSchedulingSetting.QualificationTTLSeconds, videoSchedulingSetting.ValidationPeriodSeconds = 86400, 604800
		require.NoError(t, ValidateVideoSchedulingSnapshot(&videoSchedulingSetting))
		for field, value := range map[string]string{"min_gen_rate": "0.799", "min_overall_rate": "0.599", "min_samples": "0", "explore_max_in_flight": "0", "qualification_ttl_seconds": "604801", "validation_period_seconds": "2592001", "min_margin_rate": "1", "selection_policy": videosched.PolicyWeightedV1} {
			assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting."+field, value), field)
		}
		for _, change := range []func(*VideoSchedulingSetting){
			func(s *VideoSchedulingSetting) { s.MinGenRate = .799 },
			func(s *VideoSchedulingSetting) { s.MinOverallRate = .599 },
			func(s *VideoSchedulingSetting) { s.MinSamples = 0 },
			func(s *VideoSchedulingSetting) { s.ExploreMaxInFlight = 17 },
			func(s *VideoSchedulingSetting) { s.QualificationTTLSeconds = 0 },
			func(s *VideoSchedulingSetting) { s.ValidationPeriodSeconds = 30*86400 + 1 },
		} {
			snapshot := videoSchedulingSetting
			change(&snapshot)
			assert.Error(t, ValidateVideoSchedulingSnapshot(&snapshot))
		}
		videoSchedulingSetting.Mode = VideoSchedulingModeShadow
		require.NoError(t, ValidateVideoSchedulingOption("video_scheduling_setting.selection_policy", videosched.PolicyWeightedV1))
		videoSchedulingSetting.SelectionPolicy = videosched.PolicyWeightedV1
		videoSchedulingSetting.MinSamples, videoSchedulingSetting.ExploreMaxInFlight = 0, 0
		require.NoError(t, ValidateVideoSchedulingSnapshot(&videoSchedulingSetting), "legacy zero semantics remain valid")
	})

	valid := map[string]string{
		"audit_enabled":          "true",
		"audit_retention_days":   "30",
		"mode":                   "shadow",
		"models":                 `["videos-mini"]`,
		"capacity_groups":        `{"acct-a":20}`,
		"min_submit_rate":        "0.8",
		"min_samples":            "0",
		"window_seconds":         "86400",
		"probe_max_in_flight":    "16",
		"probe_cooldown_sec":     "86400",
		"price_weight":           "0",
		"max_cost_to_sell_ratio": "0",
		"unknown_sell_policy":    "relative",
	}
	for field, value := range valid {
		require.NoError(t, ValidateVideoSchedulingOption("video_scheduling_setting."+field, value), field)
	}
	invalid := map[string]string{
		"audit_enabled":        "yes",
		"audit_retention_days": "6",
		"mode":                 "auto",
		"models":               `[""]`,
		"capacity_groups":      `{"acct-a":0}`,
		"min_gen_rate":         "1.5",
		"min_samples":          "-1",
		"window_seconds":       "0",
		"probe_max_in_flight":  "17",
		"probe_cooldown_sec":   "86401",
		"tie_epsilon":          "NaN",
		"quality_weight":       "Inf",
		"unknown_sell_policy":  "guess",
		"no_such_field":        "1",
	}
	for field, value := range invalid {
		assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting."+field, value), field)
	}
	assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting.capacity_groups", `{"`+strings.Repeat("g", 65)+`":1}`), "group names follow the channel-side length limit")
	assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting.window_seconds", "86401"), "a window is summed bucket by bucket on every read")

	// The simulator's config_snapshot goes through the same checks.
	require.NoError(t, ValidateVideoSchedulingOption("video_scheduling_setting.audit_enabled", "false"))
	assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting.audit_retention_days", "181"))
	snapshot := *GetVideoSchedulingSetting()
	require.NoError(t, ValidateVideoSchedulingSnapshot(&snapshot))
	snapshot.ProbeMaxInFlight = 100000000
	assert.Error(t, ValidateVideoSchedulingSnapshot(&snapshot))
	snapshot = *GetVideoSchedulingSetting()
	snapshot.Mode = "auto"
	assert.Error(t, ValidateVideoSchedulingSnapshot(&snapshot))
	require.NoError(t, ValidateVideoSchedulingOption("other_setting.mode", "anything"))

	require.NoError(t, ValidateVideoSchedulingOption("video_scheduling_setting.mode", "on"))
	common.RedisEnabled = false
	require.NoError(t, ValidateVideoSchedulingOption("video_scheduling_setting.mode", "on"), "single master without Redis")
	common.IsMasterNode = false
	assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting.mode", "on"), "slave without Redis")
	common.RedisEnabled, common.IsMasterNode, common.MemoryCacheEnabled = true, true, false
	assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting.mode", "on"), "memory cache off")
	assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting.mode", "shadow"), "shadow reads the memory cache too")
	require.NoError(t, ValidateVideoSchedulingOption("video_scheduling_setting.mode", "off"))
}
