package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoResolutionBillingUnitValidationPreservesConfiguration(t *testing.T) {
	original := VideoResolutionBillingUnit2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionBillingUnitByJSONString(original)) })
	require.NoError(t, UpdateVideoResolutionBillingUnitByJSONString(`{"video":"per_call"}`))
	for _, value := range []string{`null`, `[]`, `"per_call"`, `{" ":"per_call"}`, `{"video":""}`, `{"video":"per_token"}`, `{"video":null}`, `{"video":1}`, `{"video":"per_second","video":"per_call"}`} {
		t.Run(value, func(t *testing.T) {
			require.Error(t, UpdateVideoResolutionBillingUnitByJSONString(value))
			assert.Equal(t, TaskBillingModePerCall, GetVideoResolutionBillingUnit("video"))
		})
	}
}

func TestVideoResolutionBillingUnitExactCompactAndDefault(t *testing.T) {
	original := VideoResolutionBillingUnit2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateVideoResolutionBillingUnitByJSONString(original)) })
	require.NoError(t, UpdateVideoResolutionBillingUnitByJSONString(`{"video":"per_call","*-openai-compact":"per_call","exact-openai-compact":"per_second"}`))
	assert.Equal(t, TaskBillingModePerCall, GetVideoResolutionBillingUnit("video"))
	assert.Equal(t, TaskBillingModePerCall, GetVideoResolutionBillingUnit("other-openai-compact"))
	assert.Equal(t, TaskBillingModePerSecond, GetVideoResolutionBillingUnit("exact-openai-compact"))
	assert.Equal(t, TaskBillingModePerSecond, GetVideoResolutionBillingUnit("missing"))
}
