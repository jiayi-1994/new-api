package billing_setting

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmokeTestTaskExprValidatesDeclaredUsageVectors(t *testing.T) {
	videoSchema := map[string]jsplugin.UsageFieldSchema{
		"seconds": {Type: "number", Unit: "second"},
		"mode":    {Enum: []string{"std", "pro"}},
		"quality": {Enum: []string{"sd", "hd"}},
	}

	tests := []struct {
		name          string
		schema        map[string]jsplugin.UsageFieldSchema
		expression    string
		expectedError string
	}{
		{
			name:          "fixed prices are not task usage prices",
			schema:        videoSchema,
			expression:    `true ? tier("normal", u("seconds") * 0.4) : tier("fixed", fixed(0.01))`,
			expectedError: "fixed pricing is not supported for task usage expressions",
		},
		{
			name:       "declared numeric and enum facts",
			schema:     videoSchema,
			expression: `u("mode") == "pro" ? tier("pro", u("seconds") * 0.8) : tier("std", u("seconds") * 0.4)`,
		},
		{
			name:          "undeclared literal key",
			schema:        videoSchema,
			expression:    `tier("base", u("clips") * 0.1)`,
			expectedError: `usage key "clips" is not declared`,
		},
		{
			name:          "negative duration boundary",
			schema:        videoSchema,
			expression:    fmt.Sprintf(`u("seconds") == %d ? -1 : 0`, relaycommon.MaxTaskDurationSeconds),
			expectedError: "result must be finite and non-negative",
		},
		{
			name:          "negative count boundary",
			schema:        map[string]jsplugin.UsageFieldSchema{"clips": {Type: "number", Unit: "count"}},
			expression:    fmt.Sprintf(`u("clips") == %d ? -1 : 0`, dto.MaxImageN),
			expectedError: "result must be finite and non-negative",
		},
		{
			name:          "negative token boundary",
			schema:        map[string]jsplugin.UsageFieldSchema{"tokens": {Type: "number", Unit: "token"}},
			expression:    fmt.Sprintf(`u("tokens") == %d ? -1 : 0`, common.MaxQuota),
			expectedError: "result must be finite and non-negative",
		},
		{
			name:          "negative credit boundary",
			schema:        map[string]jsplugin.UsageFieldSchema{"units": {Type: "number", Unit: "credit"}},
			expression:    fmt.Sprintf(`u("units") == %d ? -1 : 0`, common.MaxQuota),
			expectedError: "result must be finite and non-negative",
		},
		{
			name:          "negative enum combination",
			schema:        videoSchema,
			expression:    `u("mode") == "pro" && u("quality") == "hd" ? -1 : 0`,
			expectedError: "result must be finite and non-negative",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := SmokeTestTaskExpr(testCase.expression, testCase.schema)
			if testCase.expectedError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, testCase.expectedError)
		})
	}
}

func TestSmokeTestTaskExprCapsOversizedEnumProductsAtLastCombination(t *testing.T) {
	schema := make(map[string]jsplugin.UsageFieldSchema, 7)
	condition := ""
	for index := range 7 {
		schema[fmt.Sprintf("enum_%d", index)] = jsplugin.UsageFieldSchema{Enum: []string{"first", "middle", "last"}}
		if condition != "" {
			condition += " && "
		}
		condition += fmt.Sprintf(`u("enum_%d") == "last"`, index)
	}

	err := SmokeTestTaskExpr(condition+" ? -1 : 0", schema)
	require.ErrorContains(t, err, "result must be finite and non-negative")
}

func TestSmokeTestExprRejectsTaskUsageWithoutSchema(t *testing.T) {
	err := SmokeTestExpr(`u("mode") == "std" ? 1 : 2`)
	require.Error(t, err)
	assert.ErrorContains(t, err, "mode")
	assert.ErrorContains(t, err, "no task plugin usage schema")

	require.NoError(t, SmokeTestExpr(`tier("base", p * 2 + c * 8)`))
}

func TestVideoSalesValidationAndFoldedLookup(t *testing.T) {
	for _, tc := range []struct{ name, value, wantErr string }{
		{name: "valid", value: `{"video-unified":{"resolutions":{"720p":{"usd_per_second":0.02,"seconds":[5,15]},"4k":{"usd_per_second":0.1,"seconds":[5]}}}}`},
		{name: "case duplicate", value: `{"video-unified":{"resolutions":{"720p":{"usd_per_second":0.02,"seconds":[5]}}},"Video-Unified":{"resolutions":{"720p":{"usd_per_second":0.01,"seconds":[5]}}}}`, wantErr: "differ only in letter case"},
		{name: "2160p is written 4k", value: `{"m":{"resolutions":{"2160p":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: `as "4k"`},
		{name: "upper case tier", value: `{"m":{"resolutions":{"720P":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: `as "720p"`},
		{name: "unknown tier", value: `{"m":{"resolutions":{"hd":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: "<height>p or 4k"},
		{name: "zero price", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0,"seconds":[5]}}}}`, wantErr: "positive number"},
		{name: "no seconds", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"seconds":[]}}}}`, wantErr: "sellable duration"},
		{name: "seconds above cap", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"seconds":[3601]}}}}`, wantErr: "seconds must be within"},
		{name: "no resolutions", value: `{"m":{"resolutions":{}}}`, wantErr: "at least one resolution"},
		{name: "untrimmed name", value: `{" m":{"resolutions":{"720p":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: "trimmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseVideoSales(tc.value)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	for input, want := range map[string]string{"720P": "720p", " 1080p ": "1080p", "2160p": "4k", "4K": "4k"} {
		got, ok := CanonicalVideoTier(input)
		assert.True(t, ok, input)
		assert.Equal(t, want, got, input)
	}
	for _, input := range []string{"", "p", "0720p", "-720p", "720", "hd"} {
		_, ok := CanonicalVideoTier(input)
		assert.False(t, ok, input)
	}

	saved := billingSetting.VideoSales
	t.Cleanup(func() { billingSetting.VideoSales = saved })
	billingSetting.VideoSales = map[string]VideoSalesModel{"Video-Unified": {Disabled: true}}
	name, sales, ok := GetVideoSales("video-unified")
	require.True(t, ok, "a channel spelling differing in case still finds the entry")
	assert.Equal(t, "Video-Unified", name)
	assert.True(t, sales.Disabled)
	_, _, ok = GetVideoSales("other")
	assert.False(t, ok)
}
