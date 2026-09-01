package common

import (
	"fmt"
	"math"

	rootcommon "github.com/QuantumNous/new-api/common"
	hosttypes "github.com/QuantumNous/new-api/types"
)

const videoInputRatio = "video_input"

// MaxInputReferenceVideoSeconds caps a single probed input reference video.
// MaxInputReferenceTotalSeconds caps the sum across all reference videos in
// one request. Both bound a billing multiplier; out-of-range values must be
// rejected at request validation, never silently clamped.
const (
	MaxInputReferenceVideoSeconds = 300
	MaxInputReferenceTotalSeconds = 900
)

// VideoBillingSelection is the provider-resolved set of inputs that may
// affect resolution-based video billing.
type VideoBillingSelection struct {
	EffectiveResolution      string
	EffectiveDurationSeconds int
	IndependentRatios        map[string]float64
	// InputVideoSeconds is the gateway-probed total duration of input
	// reference videos; InputVideoPricePerSecond is the surcharge price
	// snapshot taken at submit time. Both are zero when no surcharge applies,
	// and both must be positive together (an additive flat fee, never scaled
	// by IndependentRatios).
	InputVideoSeconds        int
	InputVideoPricePerSecond float64
}

// ResolvedVideoBilling freezes the provider selection and its selected
// per-second price for pre-consume, settlement, and audit snapshots.
type ResolvedVideoBilling struct {
	Selection               VideoBillingSelection
	SelectedResolutionPrice float64
	QuotaPerUnit            float64
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
	if err := validateInputVideoSurcharge(selection.InputVideoSeconds, selection.InputVideoPricePerSecond); err != nil {
		return nil, err
	}

	return &ResolvedVideoBilling{
		Selection: VideoBillingSelection{
			EffectiveResolution:      resolution,
			EffectiveDurationSeconds: selection.EffectiveDurationSeconds,
			IndependentRatios:        ratios,
			InputVideoSeconds:        selection.InputVideoSeconds,
			InputVideoPricePerSecond: selection.InputVideoPricePerSecond,
		},
		SelectedResolutionPrice: selectedResolutionPrice,
	}, nil
}

// validateInputVideoSurcharge enforces that the probed seconds and the
// per-second surcharge price are either both absent or both positive, with
// seconds bounded because they are a billing multiplier.
func validateInputVideoSurcharge(seconds int, pricePerSecond float64) error {
	if seconds == 0 && pricePerSecond == 0 {
		return nil
	}
	if seconds <= 0 || seconds > MaxInputReferenceTotalSeconds {
		return fmt.Errorf("input video seconds must be between 1 and %d", MaxInputReferenceTotalSeconds)
	}
	return validatePositiveFinite("input video price per second", pricePerSecond)
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
	return CalculateVideoResolutionQuotaAtUnit(
		resolutionPrice,
		durationSeconds,
		groupRatio,
		independentRatios,
		rootcommon.QuotaPerUnit,
		0,
		0,
	)
}

// CalculateVideoResolutionQuotaAtUnit calculates against an explicit quota
// conversion basis so asynchronous settlement can use the submission-time
// snapshot even when the live system option changes while a task is running.
func CalculateVideoResolutionQuotaAtUnit(
	resolutionPrice float64,
	durationSeconds int,
	groupRatio float64,
	independentRatios map[string]float64,
	quotaPerUnit float64,
	inputVideoSeconds int,
	inputVideoPricePerSecond float64,
) (int, *rootcommon.QuotaClamp, error) {
	if err := validatePositiveFinite("resolution price", resolutionPrice); err != nil {
		return 0, nil, err
	}
	if err := validateVideoDuration(durationSeconds); err != nil {
		return 0, nil, err
	}
	if err := validateNonNegativeFinite("group ratio", groupRatio); err != nil {
		return 0, nil, err
	}
	if err := validatePositiveFinite("quota per unit", quotaPerUnit); err != nil {
		return 0, nil, err
	}
	if err := validateInputVideoSurcharge(inputVideoSeconds, inputVideoPricePerSecond); err != nil {
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

	quotaValue := resolutionPrice * quotaPerUnit * groupRatio * float64(durationSeconds)
	quotaValue = priceData.ApplyOtherRatiosToFloat(quotaValue)
	// Additive per-second surcharge for probed input reference videos. It is a
	// constant per submission: independent ratios never scale it, and async
	// settlement re-runs it with the same snapshot values.
	quotaValue += inputVideoPricePerSecond * quotaPerUnit * groupRatio * float64(inputVideoSeconds)
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

func validateNonNegativeFinite(name string, value float64) error {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be non-negative and finite", name)
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
