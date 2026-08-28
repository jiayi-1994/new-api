package common

import (
	"fmt"
	"math"

	rootcommon "github.com/QuantumNous/new-api/common"
	hosttypes "github.com/QuantumNous/new-api/types"
)

const videoInputRatio = "video_input"

// VideoBillingSelection is the provider-resolved set of inputs that may
// affect resolution-based video billing.
type VideoBillingSelection struct {
	EffectiveResolution      string
	EffectiveDurationSeconds int
	IndependentRatios        map[string]float64
}

// ResolvedVideoBilling freezes the provider selection and its selected
// per-second price for pre-consume, settlement, and audit snapshots.
type ResolvedVideoBilling struct {
	Selection               VideoBillingSelection
	SelectedResolutionPrice float64
}

// NewResolvedVideoBilling validates and defensively copies billing inputs so
// later adapter mutations cannot alter the values used for charging.
func NewResolvedVideoBilling(selection VideoBillingSelection, selectedResolutionPrice float64) (*ResolvedVideoBilling, error) {
	resolution, err := rootcommon.NormalizeVideoResolutionKey(selection.EffectiveResolution)
	if err != nil {
		return nil, err
	}
	if err := validateVideoDuration(selection.EffectiveDurationSeconds); err != nil {
		return nil, err
	}
	if err := validatePositiveFinite("resolution price", selectedResolutionPrice); err != nil {
		return nil, err
	}

	ratios, err := cloneVideoIndependentRatios(selection.IndependentRatios)
	if err != nil {
		return nil, err
	}

	return &ResolvedVideoBilling{
		Selection: VideoBillingSelection{
			EffectiveResolution:      resolution,
			EffectiveDurationSeconds: selection.EffectiveDurationSeconds,
			IndependentRatios:        ratios,
		},
		SelectedResolutionPrice: selectedResolutionPrice,
	}, nil
}

// CalculateVideoResolutionQuota calculates a resolution price exactly once
// per bounded second and applies only explicitly allowlisted independent
// multipliers. Saturation is returned to the caller for billing audit logs.
func CalculateVideoResolutionQuota(
	resolutionPrice float64,
	durationSeconds int,
	groupRatio float64,
	independentRatios map[string]float64,
) (int, *rootcommon.QuotaClamp, error) {
	if err := validatePositiveFinite("resolution price", resolutionPrice); err != nil {
		return 0, nil, err
	}
	if err := validateVideoDuration(durationSeconds); err != nil {
		return 0, nil, err
	}
	if err := validatePositiveFinite("group ratio", groupRatio); err != nil {
		return 0, nil, err
	}

	priceData := hosttypes.PriceData{}
	for name, ratio := range independentRatios {
		if name != videoInputRatio {
			return 0, nil, fmt.Errorf("unsupported video independent ratio %q", name)
		}
		if err := validatePositiveFinite("video independent ratio "+name, ratio); err != nil {
			return 0, nil, err
		}
		priceData.AddOtherRatio(name, ratio)
	}

	quotaValue := resolutionPrice * rootcommon.QuotaPerUnit * groupRatio * float64(durationSeconds)
	quotaValue = priceData.ApplyOtherRatiosToFloat(quotaValue)
	quota, clamp := rootcommon.QuotaFromFloatChecked(quotaValue)
	return quota, clamp, nil
}

func validateVideoDuration(durationSeconds int) error {
	if durationSeconds <= 0 || durationSeconds > MaxTaskDurationSeconds {
		return fmt.Errorf("video duration must be between 1 and %d seconds", MaxTaskDurationSeconds)
	}
	return nil
}

func validatePositiveFinite(name string, value float64) error {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be positive and finite", name)
	}
	return nil
}

func cloneVideoIndependentRatios(ratios map[string]float64) (map[string]float64, error) {
	if len(ratios) == 0 {
		return nil, nil
	}
	clone := make(map[string]float64, len(ratios))
	for name, ratio := range ratios {
		if name != videoInputRatio {
			return nil, fmt.Errorf("unsupported video independent ratio %q", name)
		}
		if err := validatePositiveFinite("video independent ratio "+name, ratio); err != nil {
			return nil, err
		}
		clone[name] = ratio
	}
	return clone, nil
}
