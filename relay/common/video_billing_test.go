package common

import (
	"math"
	"testing"

	rootcommon "github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateVideoResolutionQuotaAlwaysMultipliesDurationOnce(t *testing.T) {
	originalQuotaPerUnit := rootcommon.QuotaPerUnit
	rootcommon.QuotaPerUnit = 500
	t.Cleanup(func() { rootcommon.QuotaPerUnit = originalQuotaPerUnit })

	quota, clamp, err := CalculateVideoResolutionQuota(
		0.1,
		8,
		1,
		map[string]float64{"video_input": 1.5},
	)

	require.NoError(t, err)
	assert.Nil(t, clamp)
	assert.Equal(t, 600, quota)
}

func TestCalculateVideoResolutionQuotaAtUnitUsesExplicitSnapshot(t *testing.T) {
	originalQuotaPerUnit := rootcommon.QuotaPerUnit
	rootcommon.QuotaPerUnit = 1_000
	t.Cleanup(func() { rootcommon.QuotaPerUnit = originalQuotaPerUnit })

	quota, clamp, err := CalculateVideoResolutionQuotaAtUnit(0.1, 4, 1.25, map[string]float64{"video_input": 1.2}, 500)

	require.NoError(t, err)
	assert.Nil(t, clamp)
	assert.Equal(t, 300, quota)

	quota, clamp, err = CalculateVideoResolutionQuotaAtUnit(0.1, 4, 1.25, nil, 0)
	assert.Error(t, err)
	assert.Zero(t, quota)
	assert.Nil(t, clamp)
}

func TestCalculateVideoResolutionQuotaAllowsZeroGroupRatio(t *testing.T) {
	quota, clamp, err := CalculateVideoResolutionQuotaAtUnit(0.1, 5, 0, nil, 500)
	require.NoError(t, err)
	assert.Zero(t, quota)
	assert.Nil(t, clamp)
}

func TestCalculateVideoResolutionQuotaRejectsUnsafeInputs(t *testing.T) {
	tests := []struct {
		name       string
		price      float64
		duration   int
		groupRatio float64
		ratios     map[string]float64
		wantClamp  bool
	}{
		{name: "zero price", price: 0, duration: 1, groupRatio: 1},
		{name: "negative price", price: -0.1, duration: 1, groupRatio: 1},
		{name: "nan price", price: math.NaN(), duration: 1, groupRatio: 1},
		{name: "positive infinite price", price: math.Inf(1), duration: 1, groupRatio: 1},
		{name: "zero duration", price: 0.1, duration: 0, groupRatio: 1},
		{name: "negative duration", price: 0.1, duration: -1, groupRatio: 1},
		{name: "duration above maximum", price: 0.1, duration: MaxTaskDurationSeconds + 1, groupRatio: 1},
		{name: "negative group ratio", price: 0.1, duration: 1, groupRatio: -1},
		{name: "nan group ratio", price: 0.1, duration: 1, groupRatio: math.NaN()},
		{name: "positive infinite group ratio", price: 0.1, duration: 1, groupRatio: math.Inf(1)},
		{name: "unknown independent ratio", price: 0.1, duration: 1, groupRatio: 1, ratios: map[string]float64{"size": 2}},
		{name: "zero independent ratio", price: 0.1, duration: 1, groupRatio: 1, ratios: map[string]float64{"video_input": 0}},
		{name: "negative independent ratio", price: 0.1, duration: 1, groupRatio: 1, ratios: map[string]float64{"video_input": -1}},
		{name: "nan independent ratio", price: 0.1, duration: 1, groupRatio: 1, ratios: map[string]float64{"video_input": math.NaN()}},
		{name: "positive infinite independent ratio", price: 0.1, duration: 1, groupRatio: 1, ratios: map[string]float64{"video_input": math.Inf(1)}},
		{name: "saturated product", price: math.MaxFloat64, duration: 1, groupRatio: 1, wantClamp: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			quota, clamp, err := CalculateVideoResolutionQuota(tc.price, tc.duration, tc.groupRatio, tc.ratios)
			if tc.wantClamp {
				require.NoError(t, err)
				require.NotNil(t, clamp)
				assert.Equal(t, rootcommon.MaxQuota, quota)
				assert.Equal(t, rootcommon.QuotaClampOverflow, clamp.Kind)
				return
			}
			assert.Error(t, err)
			assert.Zero(t, quota)
			assert.Nil(t, clamp)
		})
	}
}

func TestNewResolvedVideoBillingDefensivelyClonesSelection(t *testing.T) {
	selection := VideoBillingSelection{
		EffectiveResolution:      "1080p",
		EffectiveDurationSeconds: 8,
		IndependentRatios:        map[string]float64{"video_input": 1.2},
	}

	resolved, err := NewResolvedVideoBilling(selection, 0.18)
	require.NoError(t, err)

	selection.EffectiveResolution = "720p"
	selection.IndependentRatios["video_input"] = 9
	selection.IndependentRatios["size"] = 2

	assert.Equal(t, "1080p", resolved.Selection.EffectiveResolution)
	assert.Equal(t, map[string]float64{"video_input": 1.2}, resolved.Selection.IndependentRatios)
	assert.Equal(t, 0.18, resolved.SelectedResolutionPrice)
}

func TestNewResolvedVideoBillingRejectsInvalidSelection(t *testing.T) {
	tests := []struct {
		name      string
		selection VideoBillingSelection
		price     float64
	}{
		{name: "empty resolution", selection: VideoBillingSelection{EffectiveDurationSeconds: 1}, price: 0.1},
		{name: "non canonical resolution", selection: VideoBillingSelection{EffectiveResolution: "1920x1080", EffectiveDurationSeconds: 1}, price: 0.1},
		{name: "zero duration", selection: VideoBillingSelection{EffectiveResolution: "1080p"}, price: 0.1},
		{name: "duration above maximum", selection: VideoBillingSelection{EffectiveResolution: "1080p", EffectiveDurationSeconds: MaxTaskDurationSeconds + 1}, price: 0.1},
		{name: "unknown ratio", selection: VideoBillingSelection{EffectiveResolution: "1080p", EffectiveDurationSeconds: 1, IndependentRatios: map[string]float64{"size": 2}}, price: 0.1},
		{name: "invalid ratio", selection: VideoBillingSelection{EffectiveResolution: "1080p", EffectiveDurationSeconds: 1, IndependentRatios: map[string]float64{"video_input": math.NaN()}}, price: 0.1},
		{name: "invalid selected price", selection: VideoBillingSelection{EffectiveResolution: "1080p", EffectiveDurationSeconds: 1}, price: math.Inf(1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := NewResolvedVideoBilling(tc.selection, tc.price)
			assert.Error(t, err)
			assert.Nil(t, resolved)
		})
	}
}

func TestTaskInfoEffectiveDurationSecondsRoundTrip(t *testing.T) {
	want := TaskInfo{Status: "SUCCESS", EffectiveDurationSeconds: 8}

	raw, err := rootcommon.Marshal(want)
	require.NoError(t, err)
	assert.JSONEq(t, `{"code":0,"task_id":"","status":"SUCCESS","effective_duration_seconds":8}`, string(raw))

	var got TaskInfo
	require.NoError(t, rootcommon.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}
