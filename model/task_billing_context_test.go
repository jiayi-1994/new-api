package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskBillingContextRoundTripsFrozenResolutionSelection(t *testing.T) {
	want := TaskBillingContext{
		PricingKind:              "video_resolution",
		EffectiveResolution:      "1080p",
		SelectedResolutionPrice:  0.18,
		EffectiveDurationSeconds: 8,
		QuotaPerUnit:             500_000,
		IndependentRatios:        map[string]float64{"video_input": 1.2},
	}

	raw, err := common.Marshal(want)
	require.NoError(t, err)

	var got TaskBillingContext
	require.NoError(t, common.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
	assert.NotContains(t, string(raw), "billing_unit")
}
