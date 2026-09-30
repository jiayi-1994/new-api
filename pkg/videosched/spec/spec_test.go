package spec_test

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/pkg/videosched/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// valid is a minimal describeSpec result as the plugin runtime exports it:
// JavaScript integers arrive as int64, fractions as float64.
func valid(overrides map[string]any) map[string]any {
	raw := map[string]any{
		"spec_version":   int64(1),
		"output_seconds": int64(10),
		"seconds_kind":   "exact",
		"resolution":     "720p",
		"references":     map[string]any{"video": int64(0), "image": int64(2), "audio": int64(0)},
	}
	for key, value := range overrides {
		if value == nil {
			delete(raw, key)
			continue
		}
		raw[key] = value
	}
	return raw
}

func refs(video, image, audio any) map[string]any {
	return map[string]any{"video": video, "image": image, "audio": audio}
}

func TestParseAccepts(t *testing.T) {
	cases := []struct {
		name    string
		raw     map[string]any
		seconds *float64
		kind    string
		tier    string
		missing []string
	}{
		{"full spec", valid(nil), ptr(10), "exact", "720p", nil},
		{"fractional seconds", valid(map[string]any{"output_seconds": 4.5}), ptr(4.5), "exact", "720p", nil},
		{"fixed seconds", valid(map[string]any{"seconds_kind": "fixed"}), ptr(10), "fixed", "720p", nil},
		{"seconds kind defaults to exact", valid(map[string]any{"seconds_kind": nil}), ptr(10), "exact", "720p", nil},
		{"tier is lowercased and trimmed", valid(map[string]any{"resolution": " 1080P "}), ptr(10), "exact", "1080p", nil},
		{"pixel size becomes the short side", valid(map[string]any{"resolution": "1280x720"}), ptr(10), "exact", "720p", nil},
		{"portrait star size", valid(map[string]any{"resolution": "1080*1920"}), ptr(10), "exact", "1080p", nil},
		{"any product key is a tier", valid(map[string]any{"resolution": "Pro"}), ptr(10), "exact", "pro", nil},
		{"untiered model", valid(map[string]any{"resolution": "*"}), ptr(10), "exact", "*", nil},
		{"unknown tier stays empty", valid(map[string]any{"resolution": nil}), ptr(10), "exact", "", []string{"resolution"}},
		{"unknown seconds stay nil, not zero", valid(map[string]any{"output_seconds": nil, "seconds_kind": nil}), nil, "exact", "720p", []string{"output_seconds"}},
		{"seconds at the host bound", valid(map[string]any{"output_seconds": int64(spec.MaxOutputSeconds)}), ptr(spec.MaxOutputSeconds), "exact", "720p", nil},
		{"integral float version", valid(map[string]any{"spec_version": 1.0}), ptr(10), "exact", "720p", nil},
		{"unsupported false", valid(map[string]any{"unsupported": false}), ptr(10), "exact", "720p", nil},
		{"unknown descriptive key", valid(map[string]any{"note": "hd"}), ptr(10), "exact", "720p", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := spec.Parse(tc.raw)
			require.NoError(t, err)
			assert.Equal(t, tc.seconds, s.OutputSeconds)
			assert.Equal(t, tc.kind, s.SecondsKind)
			assert.Equal(t, tc.tier, s.Tier)
			assert.Equal(t, tc.missing, s.Missing)
		})
	}

	s, err := spec.Parse(valid(map[string]any{"references": refs(int64(1), 0.0, int64(3))}))
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"video": 1, "image": 0, "audio": 3}, s.References, "explicit zeros are kept")
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{"nil object", nil, "spec must be an object"},
		{"missing version", valid(map[string]any{"spec_version": nil}), "spec_version is required"},
		{"string version", valid(map[string]any{"spec_version": "1"}), "spec_version must be a positive integer"},
		{"zero version", valid(map[string]any{"spec_version": int64(0)}), "spec_version must be a positive integer"},
		{"fractional version", valid(map[string]any{"spec_version": 1.5}), "spec_version must be a positive integer"},
		{"missing references", valid(map[string]any{"references": nil}), "references must be an object"},
		{"references not an object", valid(map[string]any{"references": []any{}}), "references must be an object"},
		{"missing kind", valid(map[string]any{"references": map[string]any{"video": int64(0), "image": int64(0)}}), "references.audio is required"},
		{"unknown kind", valid(map[string]any{"references": map[string]any{"video": int64(0), "image": int64(0), "audio": int64(0), "mask": int64(1)}}), `unknown reference kind "mask"`},
		{"string count", valid(map[string]any{"references": refs("1", int64(0), int64(0))}), "references.video must be an integer"},
		{"null count", valid(map[string]any{"references": refs(nil, int64(0), int64(0))}), "references.video must be an integer"},
		{"fractional count", valid(map[string]any{"references": refs(int64(0), 1.5, int64(0))}), "references.image must be an integer"},
		{"negative count", valid(map[string]any{"references": refs(int64(0), int64(0), int64(-1))}), "references.audio must be an integer"},
		{"nan count", valid(map[string]any{"references": refs(math.NaN(), int64(0), int64(0))}), "references.video must be an integer"},
		{"infinite count", valid(map[string]any{"references": refs(math.Inf(1), int64(0), int64(0))}), "references.video must be an integer"},
		{"count above the host bound", valid(map[string]any{"references": refs(int64(spec.MaxReferenceCount+1), int64(0), int64(0))}), "references.video must be an integer"},
		{"string seconds", valid(map[string]any{"output_seconds": "10"}), "output_seconds must be a number"},
		{"null seconds", map[string]any{"spec_version": int64(1), "output_seconds": nil, "references": refs(int64(0), int64(0), int64(0))}, "output_seconds must be a number"},
		{"zero seconds", valid(map[string]any{"output_seconds": int64(0)}), "output_seconds must be a number"},
		{"negative seconds", valid(map[string]any{"output_seconds": -1.0}), "output_seconds must be a number"},
		{"nan seconds", valid(map[string]any{"output_seconds": math.NaN()}), "output_seconds must be a number"},
		{"infinite seconds", valid(map[string]any{"output_seconds": math.Inf(1)}), "output_seconds must be a number"},
		{"seconds above the host bound", valid(map[string]any{"output_seconds": spec.MaxOutputSeconds + 0.5}), "output_seconds must be a number"},
		{"estimate seconds kind was removed", valid(map[string]any{"seconds_kind": "estimate"}), "seconds_kind must be exact or fixed"},
		{"fixed without seconds", valid(map[string]any{"seconds_kind": "fixed", "output_seconds": nil}), "fixed seconds_kind requires output_seconds"},
		{"empty resolution", valid(map[string]any{"resolution": " "}), "resolution must be a non-empty string"},
		{"numeric resolution", valid(map[string]any{"resolution": int64(720)}), "resolution must be a non-empty string"},
		{"non-boolean opt-out", valid(map[string]any{"unsupported": "yes"}), "unsupported must be a boolean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := spec.Parse(tc.raw)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestParseVersionAndOptOut(t *testing.T) {
	_, err := spec.Parse(valid(map[string]any{"spec_version": int64(2)}))
	require.ErrorIs(t, err, spec.ErrVersion)
	assert.Contains(t, err.Error(), "2")

	_, err = spec.Parse(map[string]any{"unsupported": true})
	require.ErrorIs(t, err, spec.ErrOptOut, "an opt-out needs no other field")
	_, err = spec.Parse(valid(map[string]any{"unsupported": true, "spec_version": int64(9)}))
	require.ErrorIs(t, err, spec.ErrOptOut)
}

func TestUnknownTierSpecIsNotQuotedAtTheCheapestTier(t *testing.T) {
	s, err := spec.Parse(valid(map[string]any{"resolution": nil}))
	require.NoError(t, err)
	cost := videosched.CostConfig{Mode: videosched.ModePerSecond, Prices: map[string]float64{"480p": 0.01, "1080p": 0.1},
		References: map[string]map[string]videosched.ReferenceCost{"image": {"*": {Mode: videosched.RefIncluded}}}}
	assert.Equal(t, "tier unknown", videosched.Quote(cost, s).Reason)

	s, err = spec.Parse(valid(map[string]any{"resolution": "1920x1080"}))
	require.NoError(t, err)
	q := videosched.Quote(cost, s)
	require.Empty(t, q.Reason)
	assert.Equal(t, "1080p", q.Tier)
	assert.InDelta(t, 1.0, q.TotalUSD, 1e-9)
}

func ptr(v float64) *float64 { return &v }
