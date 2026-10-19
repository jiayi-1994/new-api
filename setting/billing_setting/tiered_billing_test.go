package billing_setting

import (
	"fmt"
	"math"
	"strings"
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
		{name: "2160p is written 4k", value: `{"m":{"resolutions":{"2160p":{"usd_per_second":0.1,"seconds":[5]}}}}`},
		{name: "upper case tier", value: `{"m":{"resolutions":{"720P":{"usd_per_second":0.1,"seconds":[5]}}}}`},
		{name: "duplicate canonical tier", value: `{"m":{"resolutions":{"2160p":{"usd_per_second":0.1,"seconds":[5]},"4k":{"usd_per_second":0.2,"seconds":[10]}}}}`, wantErr: "duplicate resolution"},
		{name: "unknown tier", value: `{"m":{"resolutions":{"hd":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: "<height>p or 4k"},
		{name: "zero price", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0,"seconds":[5]}}}}`, wantErr: "positive number"},
		{name: "free input video", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"input_video_usd_per_second":0,"seconds":[5]}}}}`},
		{name: "priced input video", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"input_video_usd_per_second":0.2,"seconds":[5]}}}}`},
		{name: "negative input video price", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"input_video_usd_per_second":-0.01,"seconds":[5]}}}}`, wantErr: "input_video_usd_per_second must be a finite number >= 0"},
		{name: "huge input video price overflows", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"input_video_usd_per_second":1e400,"seconds":[5]}}}}`, wantErr: "invalid video_sales"},
		{name: "text input video price", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"input_video_usd_per_second":"0.2","seconds":[5]}}}}`, wantErr: "invalid video_sales"},
		{name: "no seconds", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"seconds":[]}}}}`, wantErr: "sellable duration"},
		{name: "seconds above cap", value: `{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"seconds":[3601]}}}}`, wantErr: "seconds must be within"},
		{name: "no resolutions", value: `{"m":{"resolutions":{}}}`, wantErr: "at least one resolution"},
		{name: "untrimmed name", value: `{" m":{"resolutions":{"720p":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: "trimmed"},
		{name: "token pricing needs no per-second price", value: `{"m":{"pricing_unit":"token","resolutions":{"720p":{"usd_per_second":0,"usd_per_million_tokens":46.3,"with_video_usd_per_million_tokens":0,"seconds":[5]}}}}`},
		{name: "unknown pricing unit", value: `{"m":{"pricing_unit":"frame","resolutions":{"720p":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: "pricing_unit must be second or token"},
		{name: "zero token price", value: `{"m":{"pricing_unit":"token","resolutions":{"720p":{"usd_per_million_tokens":0,"seconds":[5]}}}}`, wantErr: "usd_per_million_tokens must be a positive number"},
		{name: "negative token price", value: `{"m":{"pricing_unit":"token","resolutions":{"720p":{"usd_per_million_tokens":-1,"seconds":[5]}}}}`, wantErr: "usd_per_million_tokens must be a positive number"},
		{name: "negative with-video token price", value: `{"m":{"pricing_unit":"token","resolutions":{"720p":{"usd_per_million_tokens":46.3,"with_video_usd_per_million_tokens":-1,"seconds":[5]}}}}`, wantErr: "with_video_usd_per_million_tokens must be a finite number >= 0"},
		{name: "official token billing needs a with-video price", value: `{"m":{"pricing_unit":"token","official_reference_billing":true,"resolutions":{"720p":{"usd_per_million_tokens":46.3,"seconds":[5]}}}}`, wantErr: "positive with_video_usd_per_million_tokens"},
		{name: "token tier outside the size table", value: `{"m":{"pricing_unit":"token","resolutions":{"540p":{"usd_per_million_tokens":46.3,"seconds":[5]}}}}`, wantErr: "needs an official output size"},
		{name: "seedance-2.5 has no 4k", value: `{"m":{"dimension_profile":"seedance-2.5","resolutions":{"4k":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: "seedance-2.5 has no 4k"},
		{name: "unknown dimension profile", value: `{"m":{"dimension_profile":"seedance-1.0","resolutions":{"720p":{"usd_per_second":0.1,"seconds":[5]}}}}`, wantErr: "dimension_profile must be"},
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

	// An absent input price is free, and the parsed table always carries it.
	legacy, err := ParseVideoSales(`{"m":{"resolutions":{"720p":{"usd_per_second":0.1,"seconds":[5]}}}}`)
	require.NoError(t, err)
	assert.Zero(t, legacy["m"].Resolutions["720p"].InputVideoUSDPerSecond)
	encoded, err := common.Marshal(legacy)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"input_video_usd_per_second":0`)
	assert.NotContains(t, string(encoded), "official_reference_billing", "unset official billing is omitted")
	official, err := ParseVideoSales(`{"m":{"official_reference_billing":true,"resolutions":{"720p":{"usd_per_second":1,"input_video_usd_per_second":0.6048,"seconds":[5]}}}}`)
	require.NoError(t, err)
	assert.True(t, official["m"].OfficialReferenceBilling)
	encoded, err = common.Marshal(official)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"official_reference_billing":true`)
	// The with-video price prices the whole order, so it can never be free.
	_, err = ParseVideoSales(`{"m":{"official_reference_billing":true,"resolutions":{"720p":{"usd_per_second":1,"input_video_usd_per_second":0.6048,"seconds":[5]},"1080p":{"usd_per_second":2,"seconds":[5]}}}}`)
	assert.ErrorContains(t, err, "official reference billing needs a positive input_video_usd_per_second")
	for _, price := range []float64{-1, math.NaN(), math.Inf(1)} {
		assert.False(t, ValidVideoInputPrice(price), price)
	}
	for _, price := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		assert.False(t, ValidVideoTokenPrice(price), price)
	}
	// Defaults normalize to empty, so per-second documents serialize unchanged.
	defaults, err := ParseVideoSales(`{"m":{"pricing_unit":"second","dimension_profile":"seedance-2.0","resolutions":{"720p":{"usd_per_second":0.1,"seconds":[5]}}}}`)
	require.NoError(t, err)
	encoded, err = common.Marshal(defaults)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "pricing_unit")
	assert.NotContains(t, string(encoded), "dimension_profile")
	assert.NotContains(t, string(encoded), "per_million_tokens")

	for input, want := range map[string]string{"720P": "720p", " 1080p ": "1080p", "2160p": "4k", "4K": "4k"} {
		got, ok := CanonicalVideoTier(input)
		assert.True(t, ok, input)
		assert.Equal(t, want, got, input)
	}
	for _, input := range []string{"", "p", "0720p", "-720p", "720", "hd"} {
		_, ok := CanonicalVideoTier(input)
		assert.False(t, ok, input)
	}

	// The official size tables, cell for cell; the web pricing tests assert
	// the same cells against the TypeScript copy.
	ratios := []string{"16:9", "4:3", "1:1", "3:4", "9:16", "21:9"}
	shared := map[string]string{
		"720p":  "1280x720 1112x834 960x960 834x1112 720x1280 1470x630",
		"1080p": "1920x1080 1664x1248 1440x1440 1248x1664 1080x1920 2206x946",
	}
	for profile, rows := range map[string]map[string]string{
		"": {"480p": "864x496 752x560 640x640 560x752 496x864 992x432", "720p": shared["720p"], "1080p": shared["1080p"],
			"4k": "3840x2160 3326x2494 2880x2880 2494x3326 2160x3840 4398x1886"},
		VideoDimensionProfile25: {"480p": "854x480 752x560 640x640 560x752 480x854 992x432", "720p": shared["720p"], "1080p": shared["1080p"]},
	} {
		for tier, row := range rows {
			for i, want := range strings.Fields(row) {
				width, height, ok := VideoTokenDimensions(profile, tier, ratios[i])
				require.True(t, ok, profile, tier, ratios[i])
				assert.Equal(t, want, fmt.Sprintf("%dx%d", width, height), profile, tier, ratios[i])
			}
		}
	}
	for _, ratio := range []string{"", "adaptive"} {
		width, height, ok := VideoTokenDimensions("", "720p", ratio)
		require.True(t, ok)
		assert.Equal(t, [2]int{1280, 720}, [2]int{width, height}, "a missing or adaptive ratio takes 16:9")
	}
	_, _, ok := VideoTokenDimensions("", "720p", "2:1")
	assert.False(t, ok)
	_, ok = VideoTokenMaxPixels(VideoDimensionProfile25, "4k")
	assert.False(t, ok)
	pixels, ok := VideoTokenMaxPixels("", "720p")
	require.True(t, ok)
	assert.Equal(t, 1112*834, pixels, "4:3 is the largest 720p frame")

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
