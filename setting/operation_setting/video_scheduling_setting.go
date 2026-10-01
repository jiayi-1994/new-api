package operation_setting

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	VideoSchedulingModeOff    = "off"
	VideoSchedulingModeShadow = "shadow"
	VideoSchedulingModeOn     = "on"

	videoSchedulingSettingName = "video_scheduling_setting"
)

// VideoSchedulingSetting is the global video task scheduling configuration.
// Channel cost tables live in each channel's video_scheduling settings.
type VideoSchedulingSetting struct {
	Mode               string         `json:"mode"`   // off | shadow | on
	Models             []string       `json:"models"` // allow list; empty = every model of plugins exporting describeSpec
	PriceWeight        float64        `json:"price_weight"`
	QualityWeight      float64        `json:"quality_weight"`
	ServiceWeight      float64        `json:"service_weight"`
	MinSubmitRate      float64        `json:"min_submit_rate"`
	MinGenRate         float64        `json:"min_gen_rate"`
	MinSamples         int            `json:"min_samples"`
	WindowSeconds      int            `json:"window_seconds"`
	ExploreShare       float64        `json:"explore_share"`         // target probability for unproven channels, not a hard cap
	ExploreMaxInFlight int            `json:"explore_max_in_flight"` // per unproven channel
	ProbeRatio         float64        `json:"probe_ratio"`           // probe probability for gated channels
	ProbeCooldownSec   int            `json:"probe_cooldown_sec"`    // doubles on consecutive failures, capped at one day
	ProbeMaxInFlight   int            `json:"probe_max_in_flight"`
	UnknownSellPolicy  string         `json:"unknown_sell_policy"`    // exclude | relative
	MaxCostToSellRatio float64        `json:"max_cost_to_sell_ratio"` // 0 = no loss threshold
	TieEpsilon         float64        `json:"tie_epsilon"`
	CapacityGroups     map[string]int `json:"capacity_groups"` // shared upstream account group -> quota; channels reference the name only
}

var videoSchedulingSetting = VideoSchedulingSetting{
	Mode:               VideoSchedulingModeOff,
	Models:             []string{},
	PriceWeight:        0.5,
	QualityWeight:      0.3,
	ServiceWeight:      0.2,
	MinSubmitRate:      0.8,
	MinGenRate:         0.5,
	MinSamples:         20,
	WindowSeconds:      1800,
	ExploreShare:       0.10,
	ExploreMaxInFlight: 2,
	ProbeRatio:         0.02,
	ProbeCooldownSec:   300,
	ProbeMaxInFlight:   1,
	UnknownSellPolicy:  "exclude",
	CapacityGroups:     map[string]int{},
}

func init() {
	config.GlobalConfig.Register(videoSchedulingSettingName, &videoSchedulingSetting)
}

func GetVideoSchedulingSetting() *VideoSchedulingSetting {
	return &videoSchedulingSetting
}

// VideoSchedMaxCostUSD is the purchase cost bound: the largest wallet quota
// expressed in USD. QuotaPerUnit can change at runtime, so callers evaluate it
// on every save and every scheduling decision instead of caching it.
func VideoSchedMaxCostUSD() (float64, error) {
	perUnit := common.QuotaPerUnit
	if !(perUnit > 0) || math.IsInf(perUnit, 1) {
		return 0, fmt.Errorf("video scheduling: invalid QuotaPerUnit %v", perUnit)
	}
	bound := float64(common.MaxWalletQuota) / perUnit
	if !(bound > 0) || math.IsInf(bound, 1) {
		return 0, fmt.Errorf("video scheduling: QuotaPerUnit %v yields an invalid cost bound", perUnit)
	}
	return bound, nil
}

const (
	maxVideoProbeSlots        = 16
	maxVideoSchedulingSeconds = 86400
)

// ValidateVideoSchedulingSnapshot applies the option checks to a whole
// setting supplied outside the option table (the simulator's config_snapshot).
// Mode is only checked to be known: the snapshot is never put into effect.
func ValidateVideoSchedulingSnapshot(setting *VideoSchedulingSetting) error {
	switch setting.Mode {
	case VideoSchedulingModeOff, VideoSchedulingModeShadow, VideoSchedulingModeOn:
	default:
		return fmt.Errorf("invalid video scheduling mode %q", setting.Mode)
	}
	options, err := config.ConfigToMap(setting)
	if err != nil {
		return err
	}
	for field, value := range options {
		if field == "mode" {
			continue
		}
		if err := ValidateVideoSchedulingOption(videoSchedulingSettingName+"."+field, value); err != nil {
			return err
		}
	}
	return nil
}

// ValidateVideoSchedulingOption validates one video_scheduling_setting.* option
// so a saved value can never make the runtime policy invalid.
func ValidateVideoSchedulingOption(key, value string) error {
	field, ok := strings.CutPrefix(key, videoSchedulingSettingName+".")
	if !ok {
		return nil
	}
	switch field {
	case "mode":
		switch value {
		case VideoSchedulingModeOff:
			return nil
		case VideoSchedulingModeShadow, VideoSchedulingModeOn:
			// Candidates are read from the in-memory channel cache.
			if !common.MemoryCacheEnabled {
				return fmt.Errorf("video scheduling mode %s requires MEMORY_CACHE_ENABLED", value)
			}
			if value == VideoSchedulingModeShadow {
				return nil
			}
			// ponytail: no single-instance detector; a slave node without Redis
			// is the only multi-instance proof available. Add an instance
			// registry check if one appears.
			if !common.RedisEnabled && !common.IsMasterNode {
				return fmt.Errorf("video scheduling mode on requires Redis in multi-instance deployments")
			}
			return nil
		}
		return fmt.Errorf("invalid video scheduling mode %q", value)
	case "unknown_sell_policy":
		if value != "exclude" && value != "relative" {
			return fmt.Errorf("invalid unknown_sell_policy %q", value)
		}
		return nil
	case "models":
		var models []string
		if err := common.UnmarshalJsonStr(value, &models); err != nil {
			return fmt.Errorf("invalid video scheduling models: %w", err)
		}
		for _, model := range models {
			if model == "" || model != strings.TrimSpace(model) {
				return fmt.Errorf("invalid video scheduling model %q", model)
			}
		}
		return nil
	case "capacity_groups":
		var groups map[string]int
		if err := common.UnmarshalJsonStr(value, &groups); err != nil {
			return fmt.Errorf("invalid capacity_groups: %w", err)
		}
		for name, quota := range groups {
			if name == "" || name != strings.TrimSpace(name) || len(name) > dto.MaxVideoCapacityGroupLength || quota <= 0 {
				return fmt.Errorf("capacity group %q needs a trimmed name of at most %d bytes and a quota > 0", name, dto.MaxVideoCapacityGroupLength)
			}
		}
		return nil
	case "min_samples", "explore_max_in_flight":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("%s must be a non-negative integer", field)
		}
		return nil
	case "probe_max_in_flight":
		// Every selection reads each candidate's slots one key per slot.
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > maxVideoProbeSlots {
			return fmt.Errorf("%s must be an integer within [0,%d]", field, maxVideoProbeSlots)
		}
		return nil
	case "window_seconds", "probe_cooldown_sec":
		// A window is summed bucket by bucket on every health read, and the
		// probe cooldown is capped at one day anyway.
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 || n > maxVideoSchedulingSeconds {
			return fmt.Errorf("%s must be an integer within [1,%d]", field, maxVideoSchedulingSeconds)
		}
		return nil
	case "min_submit_rate", "min_gen_rate", "explore_share", "probe_ratio":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || !(f >= 0 && f <= 1) {
			return fmt.Errorf("%s must be within [0,1]", field)
		}
		return nil
	case "price_weight", "quality_weight", "service_weight", "max_cost_to_sell_ratio", "tie_epsilon":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || !(f >= 0) || math.IsInf(f, 1) {
			return fmt.Errorf("%s must be a finite non-negative number", field)
		}
		return nil
	}
	return fmt.Errorf("unknown video scheduling option %q", field)
}
