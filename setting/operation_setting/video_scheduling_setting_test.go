package operation_setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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

	valid := map[string]string{
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
		"mode":                "auto",
		"models":              `[""]`,
		"capacity_groups":     `{"acct-a":0}`,
		"min_gen_rate":        "1.5",
		"min_samples":         "-1",
		"window_seconds":      "0",
		"probe_max_in_flight": "17",
		"probe_cooldown_sec":  "86401",
		"tie_epsilon":         "NaN",
		"quality_weight":      "Inf",
		"unknown_sell_policy": "guess",
		"no_such_field":       "1",
	}
	for field, value := range invalid {
		assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting."+field, value), field)
	}
	assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting.capacity_groups", `{"`+strings.Repeat("g", 65)+`":1}`), "group names follow the channel-side length limit")
	assert.Error(t, ValidateVideoSchedulingOption("video_scheduling_setting.window_seconds", "86401"), "a window is summed bucket by bucket on every read")

	// The simulator's config_snapshot goes through the same checks.
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
