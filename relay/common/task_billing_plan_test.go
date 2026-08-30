package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoResolutionTaskBillingPlanClonesPrices(t *testing.T) {
	prices := map[string]float64{"720p": 0.1}
	plan, err := NewVideoResolutionTaskBillingPlan("client-model", "req-frozen", prices)
	require.NoError(t, err)
	prices["720p"] = 9

	price, ok := plan.ResolutionPrice(" 720P ")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
	assert.Equal(t, TaskBillingKindVideoResolution, plan.Kind())
	assert.Equal(t, "client-model", plan.OriginModelName())
	assert.Equal(t, "req-frozen", plan.RequestID())
}

func TestLegacyTaskBillingPlanHasNoResolutionPrice(t *testing.T) {
	plan := NewLegacyTaskBillingPlan("legacy-model", "req-legacy")
	_, ok := plan.ResolutionPrice("720p")
	assert.False(t, ok)
	assert.Equal(t, TaskBillingKindLegacy, plan.Kind())
}
