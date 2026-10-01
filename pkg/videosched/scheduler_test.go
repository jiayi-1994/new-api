package videosched

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Migrated from the video-scheduler prototype. Semantics that changed are
// asserted explicitly: an unknown tier is rejected instead of taking the
// cheapest tier, the price score is the margin 1-cost/sell instead of
// cheapest/cost, health is split into submit and generation rates, and model
// matching moved to candidate assembly. Log-only, random, fuzz, goroutine and
// CLI tests were not migrated; their invariants are explicit rows below.
//
// The prototype's surcharge conditions became per-kind reference media rules.
// A prototype per-unit surcharge equals per_request on a per-video channel and
// per_output_second on a per-second channel, so its independently computed
// numbers carry over. Fixture "audio" now means a submitted reference audio,
// not a generated sound track; "video" was the prototype's video_input.

const eps = 1e-9

// bound is a MaxCostUSD far above every fixture price.
const bound = 1e6

var gatePolicy = Policy{Weights: DefaultWeights, MinSubmitRate: 0.5, MinGenRate: 0.5, MaxCostUSD: bound}

func stabilityTestCandidate(id int, cost float64, successes int64) Candidate {
	e := &ReliabilityEvidence{Version: 1, Source: "window", BatchStart: 1000, BatchEnd: 2800, WindowSeconds: 1800, AsOf: 3000, ValidatedAt: 3000, ExpiresAt: 89400, Submitted: 1000, Accepted: 1000, Succeeded: successes, GenerationFailed: 1000 - successes}
	return Candidate{ID: id, Priority: 1, Weight: 1, Quality: .8, Spec: Spec{Tier: "*"}, Cost: CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"*": cost}}, Sell: SellPrice{Kind: SellKnown, USD: 1}, Reliability: &ReliabilitySnapshot{Version: 1, Model: "video", State: HealthNormal, StateVersion: 2, Integrity: "complete", Qualification: e}}
}

func TestStabilityCostOrderAndMargin(t *testing.T) {
	p := Policy{SelectionPolicy: PolicyStabilityCostV2, MinMarginRate: .1, MinGenRate: .8, MinOverallRate: .6, StabilityTolerance: .01, MinSamples: 20, Now: 4000, MaxCostUSD: bound, QualificationTTLSeconds: 86400, ValidationPeriodSeconds: 604800, TieEpsilon: 1}
	for _, tc := range []struct {
		name   string
		change func([]Candidate)
		want   int
		reason string
	}{
		{"99_and_98_choose_cheaper", func(c []Candidate) {}, 2, ""},
		{"97_9_is_not_close", func(c []Candidate) {
			c[1].Reliability.Qualification.Succeeded = 979
			c[1].Reliability.Qualification.GenerationFailed = 21
		}, 1, "stability gap too large"},
		{"tolerance_does_not_chain", func(c []Candidate) { c[2] = stabilityTestCandidate(3, .1, 970) }, 2, "stability gap too large"},
		{"priority_before_cost", func(c []Candidate) { c[0].Priority = 2 }, 1, "lower priority"},
		{"quality_before_cost", func(c []Candidate) { c[0].Quality = .9 }, 1, "lower quality tier"},
		{"capacity_filters_without_scoring", func(c []Candidate) { c[0].Capacity = 10; c[0].InFlight = 9 }, 2, ""},
		{"full_shared_group", func(c []Candidate) { c[1].GroupCapacity = 1; c[1].GroupInFlight = 1 }, 1, "at capacity"},
		{"exact_ten_percent", func(c []Candidate) { c[1].Cost.Prices["*"] = .9; c[0].Excluded = "disabled" }, 2, ""},
		{"below_ten_percent", func(c []Candidate) { c[1].Cost.Prices["*"] = .9001 }, 1, "margin below minimum"},
		{"reference_cost_included", func(c []Candidate) {
			c[1].Spec.References = map[string]int{"image": 1}
			c[1].Cost.References = map[string]map[string]ReferenceCost{"image": {"*": rule(RefPerRequest, .401)}}
		}, 1, "margin below minimum"},
		{"effective_group_sell", func(c []Candidate) { c[1].Sell.USD = .5 }, 1, "margin below minimum"},
		{"free_sell_rejected", func(c []Candidate) { c[1].Sell.Kind = SellFree; c[1].Sell.USD = 0 }, 1, "positive sell price required"},
		{"unknown_sell_rejected", func(c []Candidate) { c[1].Sell.Kind = SellUnknown }, 1, "positive sell price required"},
		{"invalid_sell_rejected", func(c []Candidate) { c[1].Sell.USD = math.Inf(1) }, 1, "invalid sell price"},
		{"unknown_evidence", func(c []Candidate) { c[1].Reliability = nil }, 1, "health_state_unavailable"},
		{"incomplete_cohort", func(c []Candidate) { q := c[1].Reliability.Qualification; q.Succeeded--; q.Pending++ }, 1, "health evidence incomplete"},
		{"independent_overall_gate", func(c []Candidate) {
			q := c[1].Reliability.Qualification
			q.Accepted = 700
			q.Succeeded = 560
			q.GenerationFailed = 140
			q.Rejected = 300
		}, 1, "overall completion below minimum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := []Candidate{stabilityTestCandidate(1, .8, 990), stabilityTestCandidate(2, .5, 980), {ID: 3, Excluded: "disabled"}}
			tc.change(c)
			got, board, err := Select(c, p, rand.New(rand.NewPCG(19, 1)))
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.ID)
			if tc.reason != "" {
				assert.True(t, slices.ContainsFunc(board, func(s Score) bool { return s.Reason == tc.reason }), "missing reason %s: %+v", tc.reason, board)
			}
		})
	}
}

func secs(v float64) *float64 { return &v }

func healthy(rate float64) (HealthStat, HealthStat) {
	return HealthStat{Rate: rate, Samples: 100}, HealthStat{Rate: 1, Samples: 100}
}

// spec builds an exact-seconds spec carrying one reference per listed kind.
func spec(tier string, seconds float64, kinds ...string) Spec {
	s := Spec{Tier: tier, OutputSeconds: &seconds, SecondsKind: KindExact, References: map[string]int{"video": 0, "image": 0, "audio": 0}}
	for _, kind := range kinds {
		s.References[kind]++
	}
	return s
}

func rule(mode string, value float64) ReferenceCost { return ReferenceCost{Mode: mode, Value: &value} }

// flatReferences prices each kind like the prototype's per-unit surcharge.
func flatReferences(mode string, extras map[string]float64) map[string]map[string]ReferenceCost {
	if len(extras) == 0 {
		return nil
	}
	refMode := RefPerRequest
	if mode == ModePerSecond {
		refMode = RefPerOutputSecond
	}
	out := map[string]map[string]ReferenceCost{}
	for kind, value := range extras {
		out[kind] = map[string]ReferenceCost{"*": rule(refMode, value)}
	}
	return out
}

// candidate builds a known-sell candidate with healthy generation stats.
func candidate(id int, mode string, prices, extras map[string]float64, quality, submitRate float64, inFlight, capacity int, sell float64) Candidate {
	c := Candidate{ID: id, Name: fmt.Sprintf("ch%d", id), Quality: quality, InFlight: inFlight, Capacity: capacity,
		Cost: CostConfig{Mode: mode, Prices: prices, References: flatReferences(mode, extras)},
		Sell: SellPrice{Kind: SellKnown, USD: sell}}
	c.Submit, c.Gen = healthy(submitRate)
	return c
}

// demoCandidates mirrors the prototype's five billing shapes plus a disabled channel.
func demoCandidates(s Spec, sell float64) []Candidate {
	cands := []Candidate{
		candidate(1, ModePerVideo, map[string]float64{"720p": 1.20}, nil, 0.70, 0.98, 2, 10, sell),
		candidate(2, ModePerVideo, map[string]float64{"480p": 0.60, "720p": 1.00, "1080p": 1.80}, nil, 0.80, 0.95, 5, 10, sell),
		candidate(3, ModePerSecond, map[string]float64{"1080p": 0.30}, nil, 0.90, 0.99, 0, 5, sell),
		candidate(4, ModePerSecond, map[string]float64{"480p": 0.08, "720p": 0.15, "1080p": 0.28}, nil, 0.75, 0.90, 8, 10, sell),
		candidate(5, ModePerSecond, map[string]float64{"720p": 0.14, "1080p": 0.26, "4k": 0.60},
			map[string]float64{"audio": 0.04, "video": 0.10}, 0.95, 0.97, 1, 20, sell),
		candidate(6, ModePerVideo, map[string]float64{"720p": 0.10}, nil, 1, 1, 0, 0, sell),
	}
	cands[5].Excluded = "disabled"
	for i := range cands {
		cands[i].Spec = s
	}
	return cands
}

// edgeCandidate is a per-second 720p channel with an audio reference surcharge.
func edgeCandidate(id int) Candidate {
	c := candidate(id, ModePerSecond, map[string]float64{"720p": 1}, map[string]float64{"audio": 0.25}, 1, 1, 0, 10, 100)
	c.Spec = spec("720p", 5)
	return c
}

func byID(t *testing.T, scores []Score, id int) Score {
	t.Helper()
	for _, s := range scores {
		if s.Candidate.ID == id {
			return s
		}
	}
	require.FailNow(t, "candidate not in scores", "id %d", id)
	return Score{}
}

func eligibleCount(scores []Score) int {
	n := 0
	for _, s := range scores {
		if s.Reason == "" {
			n++
		}
	}
	return n
}

// ---- Quote -----------------------------------------------------------------

func TestQuoteBillingShapes(t *testing.T) {
	cases := []struct {
		name       string
		id         int
		spec       Spec
		wantTier   string
		wantUSD    float64
		wantRef    float64
		wantReason string
	}{
		{"per-video single tier 5s", 1, spec("720p", 5), "720p", 1.20, 0, ""},
		{"per-video same price at 15s", 1, spec("720p", 15), "720p", 1.20, 0, ""},
		{"per-video unpriced tier", 1, spec("1080p", 5), "", 0, 0, "tier 1080p not priced"},
		{"per-video multi tier 480p", 2, spec("480p", 30), "480p", 0.60, 0, ""},
		{"per-video multi tier 1080p", 2, spec("1080p", 3), "1080p", 1.80, 0, ""},
		{"unknown tier is rejected, not the cheapest", 2, spec("", 5), "", 0, 0, "tier unknown"},
		{"tier matched case-insensitively", 2, spec("1080P", 3), "1080p", 1.80, 0, ""},
		{"per-second single tier", 3, spec("1080p", 10), "1080p", 3.00, 0, ""},
		{"per-second fractional seconds", 3, spec("1080p", 4.5), "1080p", 1.35, 0, ""},
		{"per-second unpriced tier", 3, spec("720p", 10), "", 0, 0, "tier 720p not priced"},
		{"per-second multi tier 480p", 4, spec("480p", 10), "480p", 0.80, 0, ""},
		{"per-second multi tier 1080p", 4, spec("1080p", 10), "1080p", 2.80, 0, ""},
		{"reference without a rule", 4, spec("720p", 10, "audio"), "720p", 0, 0, "reference audio not priced"},
		{"surcharged base only", 5, spec("1080p", 10), "1080p", 2.60, 0, ""},
		{"surcharged +audio", 5, spec("1080p", 10, "audio"), "1080p", 3.00, 0.40, ""},
		{"surcharged +audio +video", 5, spec("720p", 10, "audio", "video"), "720p", 2.80, 1.40, ""},
		{"surcharged unpriced image", 5, spec("720p", 10, "image"), "720p", 0, 0, "reference image not priced"},
		{"zero seconds", 3, spec("1080p", 0), "", 0, 0, "invalid seconds"},
	}
	cands := demoCandidates(Spec{}, 10)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := Quote(cands[tc.id-1].Cost, tc.spec)
			require.Equal(t, tc.wantReason, q.Reason)
			assert.Equal(t, tc.wantTier, q.Tier)
			assert.InDelta(t, tc.wantUSD, q.TotalUSD, eps)
			assert.InDelta(t, tc.wantRef, q.ReferenceUSD, eps)
			assert.InDelta(t, q.TotalUSD, q.BaseUSD+q.ReferenceUSD, eps)
		})
	}
}

func TestQuotePerRequestReferenceIsFlat(t *testing.T) {
	cost := CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": 1}, References: flatReferences(ModePerVideo, map[string]float64{"audio": 0.5})}
	for _, seconds := range []float64{1, 10, 60} {
		q := Quote(cost, spec("720p", seconds, "audio"))
		require.Empty(t, q.Reason)
		assert.InDelta(t, 1.5, q.TotalUSD, eps, "seconds=%v", seconds)
	}
}

func TestQuoteUnknownTierNeverGuesses(t *testing.T) {
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cost := CostConfig{Mode: ModePerSecond, Prices: map[string]float64{"bad": bad, "720p": 2, "480p": 1}}
		assert.Equal(t, "tier unknown", Quote(cost, spec("", 5)).Reason, "bad=%v", bad)
		assert.Equal(t, "invalid price", Quote(cost, spec("bad", 5)).Reason, "bad=%v", bad)
		assert.Equal(t, "invalid price", Quote(CostConfig{Mode: ModePerSecond, Prices: map[string]float64{"*": bad}}, spec("", 5)).Reason, "bad=%v", bad)
	}

	q := Quote(CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"pro": 3, "lite": 1}}, Spec{Tier: "Pro"})
	require.Empty(t, q.Reason)
	assert.Equal(t, "pro", q.Tier, "any product key is a tier")
	assert.InDelta(t, 3, q.TotalUSD, eps)

	wildcard := CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"*": 0.8}}
	for _, s := range []Spec{{}, {Tier: "720p"}, {Tier: "*"}} {
		q = Quote(wildcard, s)
		require.Empty(t, q.Reason, s.Tier)
		assert.InDelta(t, 0.8, q.TotalUSD, eps)
		assert.Equal(t, "*", q.Tier)
	}
	assert.Equal(t, "tier unknown", Quote(CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"*": 0.8, "1080p": 2}}, Spec{}).Reason,
		"a wildcard beside other tiers must not quote an unknown tier")
	assert.Equal(t, "tier unknown", Quote(CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": 1}}, Spec{}).Reason,
		"a lone named tier is not a guess either")
	assert.Equal(t, "tier * not priced", Quote(CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": 1}}, Spec{Tier: "*"}).Reason,
		"an untiered model needs a wildcard price")
}

func TestQuoteSecondsRules(t *testing.T) {
	perVideo := CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": 1}}
	withRange := CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": 1}, MinSeconds: 4, MaxSeconds: 10}
	discrete := CostConfig{Mode: ModePerSecond, Prices: map[string]float64{"720p": 1}, AllowedSeconds: []int{5, 10}}
	noSeconds := Spec{Tier: "720p"}
	fixed := spec("720p", 6)
	fixed.SecondsKind = KindFixed

	cases := []struct {
		name   string
		cost   CostConfig
		spec   Spec
		reason string
		usd    float64
	}{
		{"unknown seconds allowed for unconstrained per-video", perVideo, noSeconds, "", 1},
		{"unknown seconds rejected for per-second", discrete, noSeconds, "seconds unknown", 0},
		{"unknown seconds rejected when constrained", withRange, noSeconds, "seconds unknown", 0},
		{"below minimum", withRange, spec("720p", 3), "seconds 3 out of range", 0},
		{"above maximum", withRange, spec("720p", 11), "seconds 11 out of range", 0},
		{"inside range", withRange, spec("720p", 10), "", 1},
		{"allowed discrete value", discrete, spec("720p", 10), "", 10},
		{"disallowed discrete value", discrete, spec("720p", 6), "seconds 6 not allowed", 0},
		{"fixed seconds still checked", discrete, fixed, "seconds 6 not allowed", 0},
		{"negative seconds", perVideo, spec("720p", -1), "invalid seconds", 0},
		{"nan seconds", perVideo, spec("720p", math.NaN()), "invalid seconds", 0},
		{"inf seconds", perVideo, spec("720p", math.Inf(1)), "invalid seconds", 0},
		{"negative inf seconds", perVideo, spec("720p", math.Inf(-1)), "invalid seconds", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := Quote(tc.cost, tc.spec)
			require.Equal(t, tc.reason, q.Reason)
			assert.InDelta(t, tc.usd, q.TotalUSD, eps)
		})
	}
}

// TestQuoteReferences covers the six reference rules and the §14 quote rows
// (hypothetical prices, base cost 1.00 unless stated).
func TestQuoteReferences(t *testing.T) {
	base := func(refs map[string]map[string]ReferenceCost) CostConfig {
		return CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"*": 1}, References: refs}
	}
	star := func(r ReferenceCost) map[string]ReferenceCost { return map[string]ReferenceCost{"*": r} }
	with := func(tier string, seconds *float64, video, image, audio int) Spec {
		return Spec{Tier: tier, OutputSeconds: seconds, References: map[string]int{"video": video, "image": image, "audio": audio}}
	}

	cases := []struct {
		name   string
		cost   CostConfig
		spec   Spec
		total  float64
		reason string
	}{
		{"per_request ignores the count", base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerRequest, 0.5))}), with("720p", secs(5), 2, 0, 0), 1.5, ""},
		{"per_input charges each item", base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerInput, 0.5))}), with("720p", secs(5), 2, 0, 0), 2.0, ""},
		{"multiplier adds base times v-1, not base times v", base(map[string]map[string]ReferenceCost{"video": star(rule(RefMultiplier, 1.5))}), with("720p", secs(5), 1, 0, 0), 1.5, ""},
		{"multipliers of two kinds add up", base(map[string]map[string]ReferenceCost{"video": star(rule(RefMultiplier, 1.5)), "image": star(rule(RefMultiplier, 1.2))}), with("720p", secs(5), 1, 1, 0), 1.7, ""},
		{"multiplier of one is free", base(map[string]map[string]ReferenceCost{"video": star(rule(RefMultiplier, 1))}), with("720p", secs(5), 1, 0, 0), 1, ""},
		{"per_output_second uses the output length", base(map[string]map[string]ReferenceCost{"audio": star(rule(RefPerOutputSecond, 0.02))}), with("720p", secs(10), 0, 0, 1), 1.2, ""},
		{"per_output_second needs known seconds", base(map[string]map[string]ReferenceCost{"audio": star(rule(RefPerOutputSecond, 0.02))}), with("720p", nil, 0, 0, 1), 0, "seconds unknown"},
		{"known tier beats the default rule", base(map[string]map[string]ReferenceCost{"video": {"720p": rule(RefPerRequest, 0.5), "*": {Mode: RefIncluded}}}), with("720p", secs(5), 1, 0, 0), 1.5, ""},
		{"other tiers fall back to the default rule", base(map[string]map[string]ReferenceCost{"video": {"720p": rule(RefPerRequest, 0.5), "*": {Mode: RefIncluded}}}), with("480p", secs(5), 1, 0, 0), 1, ""},
		{"unknown tier only uses the default rule", base(map[string]map[string]ReferenceCost{"video": {"720p": rule(RefPerRequest, 0.5)}}), with("", secs(5), 1, 0, 0), 0, "reference video not priced"},
		{"missing rule is not free", base(nil), with("720p", secs(5), 1, 0, 0), 0, "reference video not priced"},
		{"included", base(map[string]map[string]ReferenceCost{"video": star(ReferenceCost{Mode: RefIncluded})}), with("720p", secs(5), 1, 0, 0), 1, ""},
		{"explicit zero price", base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerRequest, 0))}), with("720p", secs(5), 1, 0, 0), 1, ""},
		{"unsupported", base(map[string]map[string]ReferenceCost{"video": star(ReferenceCost{Mode: RefUnsupported})}), with("720p", secs(5), 1, 0, 0), 0, "reference video unsupported"},
		{"three kinds sum", base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerRequest, 0.5)), "image": star(rule(RefPerInput, 0.1))}), with("720p", secs(5), 1, 2, 0), 1.7, ""},
		{"only a video rule while images are sent", base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerRequest, 0.5))}), with("720p", secs(5), 1, 2, 0), 0, "reference image not priced"},
		{"images included", base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerRequest, 0.5)), "image": star(ReferenceCost{Mode: RefIncluded})}), with("720p", secs(5), 1, 2, 0), 1.5, ""},
		{"zero counts never look up rules", base(nil), with("720p", secs(5), 0, 0, 0), 1, ""},
		{"nil counts are zero", base(nil), Spec{Tier: "720p"}, 1, ""},
		{"negative count", base(nil), with("720p", secs(5), -1, 0, 0), 0, "invalid reference video count"},
		{"charging rule without a value", base(map[string]map[string]ReferenceCost{"video": star(ReferenceCost{Mode: RefPerRequest})}), with("720p", secs(5), 1, 0, 0), 0, "invalid reference video price"},
		{"multiplier below one", base(map[string]map[string]ReferenceCost{"video": star(rule(RefMultiplier, 0.6))}), with("720p", secs(5), 1, 0, 0), 0, "invalid reference video price"},
		{"negative price", base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerInput, -1))}), with("720p", secs(5), 1, 0, 0), 0, "invalid reference video price"},
		{"nan price", base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerInput, math.NaN()))}), with("720p", secs(5), 1, 0, 0), 0, "invalid reference video price"},
		{"inf multiplier", base(map[string]map[string]ReferenceCost{"video": star(rule(RefMultiplier, math.Inf(1)))}), with("720p", secs(5), 1, 0, 0), 0, "invalid reference video price"},
		{"unknown mode", base(map[string]map[string]ReferenceCost{"video": star(rule("percent", 1))}), with("720p", secs(5), 1, 0, 0), 0, "invalid reference video rule"},
		{"first failure in kind order", base(map[string]map[string]ReferenceCost{"video": star(ReferenceCost{Mode: RefUnsupported})}), with("720p", secs(5), 1, 1, 1), 0, "reference video unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := Quote(tc.cost, tc.spec)
			require.Equal(t, tc.reason, q.Reason)
			assert.InDelta(t, tc.total, q.TotalUSD, eps)
			if tc.reason != "" {
				assert.Zero(t, q.BaseUSD, "an invalid quote carries no amounts")
				assert.Zero(t, q.ReferenceUSD)
			}
		})
	}

	t.Run("lines are detailed in kind order", func(t *testing.T) {
		q := Quote(base(map[string]map[string]ReferenceCost{
			"audio": star(rule(RefPerOutputSecond, 0.01)),
			"image": {"720p": rule(RefPerInput, 0.1)},
			"video": star(rule(RefMultiplier, 1.5)),
		}), with("720p", secs(10), 1, 2, 1))
		require.Empty(t, q.Reason)
		require.Len(t, q.References, 3)
		assert.Equal(t, []string{"video", "image", "audio"}, []string{q.References[0].Kind, q.References[1].Kind, q.References[2].Kind})
		video, image, audio := q.References[0], q.References[1], q.References[2]
		assert.Equal(t, RefMultiplier, video.Mode)
		assert.Nil(t, video.Quantity)
		assert.InDelta(t, 1.5, *video.Value, eps)
		assert.InDelta(t, 0.5, video.USD, eps)
		assert.Equal(t, "720p", image.Tier)
		assert.InDelta(t, 2, *image.Quantity, eps)
		assert.InDelta(t, 0.2, image.USD, eps)
		assert.Equal(t, "*", audio.Tier)
		assert.InDelta(t, 10, *audio.Quantity, eps)
		assert.InDelta(t, 0.1, audio.USD, eps)
		assert.InDelta(t, 1, q.BaseUSD, eps)
		assert.InDelta(t, 0.8, q.ReferenceUSD, eps)
		assert.InDelta(t, 1.8, q.TotalUSD, eps)
	})
	t.Run("one failing line invalidates the whole quote", func(t *testing.T) {
		q := Quote(base(map[string]map[string]ReferenceCost{"video": star(rule(RefPerRequest, 0.5))}), with("720p", secs(5), 1, 1, 0))
		assert.Equal(t, "reference image not priced", q.Reason)
		require.Len(t, q.References, 2)
		assert.Empty(t, q.References[0].Reason)
		assert.Equal(t, "reference image not priced", q.References[1].Reason)
		assert.Zero(t, q.TotalUSD)
	})
	t.Run("a base failure has no lines; a free quote has no reason", func(t *testing.T) {
		failed := Quote(CostConfig{Mode: ModePerSecond, Prices: map[string]float64{"*": 1}}, Spec{})
		assert.Equal(t, "seconds unknown", failed.Reason)
		assert.Empty(t, failed.References)
		free := Quote(CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"*": 0}}, Spec{})
		assert.Empty(t, free.Reason)
		assert.Zero(t, free.TotalUSD)
	})
}

// TestReferenceSurchargesChangeTheChoice is §14's ranking rows: output 10s; A
// 0.10/s with a 0.50 per-request video surcharge, B 0.12/s with video included.
func TestReferenceSurchargesChangeTheChoice(t *testing.T) {
	pool := func(sell float64, kinds ...string) []Candidate {
		a := candidate(1, ModePerSecond, map[string]float64{"*": 0.10}, nil, 0.5, 1, 0, 0, sell)
		b := candidate(2, ModePerSecond, map[string]float64{"*": 0.12}, nil, 0.5, 1, 0, 0, sell)
		a.Cost.References = map[string]map[string]ReferenceCost{"video": {"*": rule(RefPerRequest, 0.5)}}
		b.Cost.References = map[string]map[string]ReferenceCost{"video": {"*": {Mode: RefIncluded}}}
		a.Spec, b.Spec = spec("720p", 10, kinds...), spec("720p", 10, kinds...)
		return []Candidate{a, b}
	}

	best, scores, err := Select(pool(2), gatePolicy, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, best.ID)
	assert.InDelta(t, 1.00, byID(t, scores, 1).Quote.TotalUSD, eps)
	assert.InDelta(t, 1.20, byID(t, scores, 2).Quote.TotalUSD, eps)

	best, scores, err = Select(pool(2, "video"), gatePolicy, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, best.ID, "a reference video reverses the order")
	assert.InDelta(t, 1.50, byID(t, scores, 1).Quote.TotalUSD, eps)
	assert.InDelta(t, 0.50, byID(t, scores, 1).Quote.ReferenceUSD, eps)
	assert.InDelta(t, 1.20, byID(t, scores, 2).Quote.TotalUSD, eps)

	p := gatePolicy
	p.MaxCostToSellRatio = 1
	best, scores, err = Select(pool(1.3, "video"), p, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, best.ID)
	assert.Equal(t, "cost/sell 1.15 > 1.00", byID(t, scores, 1).Reason, "the surcharge crosses the loss threshold")
	assert.Empty(t, byID(t, scores, 2).Reason)
}

func TestQuoteRejectsInvalidAndUnrepresentableTotals(t *testing.T) {
	audio := func(mode string, base, extra float64) CostConfig {
		return CostConfig{Mode: mode, Prices: map[string]float64{"720p": base}, References: flatReferences(mode, map[string]float64{"audio": extra})}
	}
	withRule := func(base float64, kind string, r ReferenceCost) CostConfig {
		return CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": base}, References: map[string]map[string]ReferenceCost{kind: {"*": r}}}
	}
	twoImages := spec("720p", 1, "image", "image")
	cases := []struct {
		name   string
		cost   CostConfig
		spec   Spec
		reason string
	}{
		{"negative price", audio(ModePerVideo, -1, 0.1), spec("720p", 5), "invalid price"},
		{"nan price", audio(ModePerSecond, math.NaN(), 0.1), spec("720p", 5), "invalid price"},
		{"inf price", audio(ModePerSecond, math.Inf(1), 0.1), spec("720p", 5), "invalid price"},
		{"unknown mode", audio("per_minute", 1, 0.1), spec("720p", 5), "invalid billing mode"},
		{"per-unit billing was removed", audio("per_unit", 1, 0.1), spec("720p", 5), "invalid billing mode"},
		{"empty mode", audio("", 1, 0.1), spec("720p", 5), "invalid billing mode"},
		{"no prices", CostConfig{Mode: ModePerSecond}, spec("720p", 5), "no price configured"},
		{"negative surcharge", audio(ModePerSecond, 1, -1), spec("720p", 5, "audio"), "invalid reference audio price"},
		{"nan surcharge", audio(ModePerSecond, 1, math.NaN()), spec("720p", 5, "audio"), "invalid reference audio price"},
		{"inf surcharge", audio(ModePerSecond, 1, math.Inf(1)), spec("720p", 5, "audio"), "invalid reference audio price"},
		{"zero multiplier", withRule(1, "audio", rule(RefMultiplier, 0)), spec("720p", 5, "audio"), "invalid reference audio price"},
		{"unknown rule mode", withRule(1, "audio", rule("percent", 1)), spec("720p", 5, "audio"), "invalid reference audio rule"},
		{"per-video addition overflow", audio(ModePerVideo, math.MaxFloat64, math.MaxFloat64), spec("720p", 1, "audio"), "invalid total price"},
		{"per-second addition overflow", audio(ModePerSecond, math.MaxFloat64, math.MaxFloat64), spec("720p", 1, "audio"), "invalid total price"},
		{"multiplication overflow", audio(ModePerSecond, math.MaxFloat64, 0), spec("720p", 2, "audio"), "invalid total price"},
		{"positive underflow is not free", audio(ModePerSecond, math.SmallestNonzeroFloat64, 0), spec("720p", 0.5, "audio"), "invalid total price"},
		{"multiplier overflow", withRule(math.MaxFloat64, "audio", rule(RefMultiplier, 3)), spec("720p", 1, "audio"), "invalid total price"},
		{"multiplier underflow is not free", withRule(math.SmallestNonzeroFloat64, "audio", rule(RefMultiplier, 1.1)), spec("720p", 1, "audio"), "invalid total price"},
		{"per-input overflow", withRule(1, "image", rule(RefPerInput, math.MaxFloat64)), twoImages, "invalid total price"},
		{"reference sum overflow", CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": 0}, References: map[string]map[string]ReferenceCost{
			"video": {"*": rule(RefPerRequest, math.MaxFloat64)}, "image": {"*": rule(RefPerRequest, math.MaxFloat64)}}}, spec("720p", 1, "video", "image"), "invalid total price"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := Quote(tc.cost, tc.spec)
			require.Equal(t, tc.reason, q.Reason)
			assert.Zero(t, q.TotalUSD)
			c := edgeCandidate(1)
			c.Cost, c.Spec = tc.cost, tc.spec
			best, scores, err := Select([]Candidate{c}, gatePolicy, nil)
			require.ErrorIs(t, err, ErrNoCandidate)
			assert.Nil(t, best)
			assert.Equal(t, tc.reason, scores[0].Reason)
			assert.Zero(t, scores[0].Total)
		})
	}

	for _, tc := range []struct {
		name                string
		base, seconds, want float64
	}{
		{"largest finite quotes; the cost bound rejects it later", math.MaxFloat64, 1, math.MaxFloat64},
		{"smallest finite", math.SmallestNonzeroFloat64, 1, math.SmallestNonzeroFloat64},
		{"real free", 0, math.MaxFloat64, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := Quote(CostConfig{Mode: ModePerSecond, Prices: map[string]float64{"720p": tc.base}}, spec("720p", tc.seconds))
			require.Empty(t, q.Reason)
			assert.Equal(t, tc.want, q.TotalUSD)
		})
	}
}

func TestCostBound(t *testing.T) {
	c := edgeCandidate(1)
	c.Cost = CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": math.MaxFloat64}}
	c.Sell = SellPrice{Kind: SellKnown, USD: math.MaxFloat64}
	assert.Equal(t, "cost exceeds bound", Evaluate([]Candidate{c}, gatePolicy)[0].Reason, "the largest finite price is not a valid purchase cost")

	withTotal := func(basePrice, surcharge float64) Candidate {
		c := edgeCandidate(1)
		c.Cost = CostConfig{Mode: ModePerVideo, Prices: map[string]float64{"720p": basePrice},
			References: map[string]map[string]ReferenceCost{"video": {"*": rule(RefPerRequest, surcharge)}}}
		c.Spec = spec("720p", 5, "video")
		c.Sell = SellPrice{Kind: SellKnown, USD: 2 * bound}
		return c
	}
	for _, tc := range []struct {
		name            string
		base, surcharge float64
		reason          string
	}{
		{"exactly at the bound", bound, 0, ""},
		{"just above the bound", math.Nextafter(bound, math.Inf(1)), 0, "cost exceeds bound"},
		{"parts within the bound, sum above it", 0.6 * bound, 0.6 * bound, "cost exceeds bound"},
		{"parts summing to the bound", 0.5 * bound, 0.5 * bound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.reason, Evaluate([]Candidate{withTotal(tc.base, tc.surcharge)}, gatePolicy)[0].Reason)
		})
	}

	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		p := gatePolicy
		p.MaxCostUSD = bad
		for _, s := range Evaluate([]Candidate{edgeCandidate(1), edgeCandidate(2)}, p) {
			assert.Equal(t, "invalid policy", s.Reason, "bound=%v", bad)
		}
	}
}

// ---- Evaluate / Select ------------------------------------------------------

func TestEvaluateScoresAndSelection(t *testing.T) {
	const sell = 3.0
	best, scores, err := Select(demoCandidates(spec("720p", 5), sell), gatePolicy, nil)
	require.NoError(t, err)
	want := map[int]struct{ cost, q, s float64 }{
		1: {1.20, 0.70, 0.98 * 0.8},
		2: {1.00, 0.80, 0.95 * 0.5},
		4: {0.75, 0.75, 0.90 * 0.2},
		5: {0.70, 0.95, 0.97 * 0.95},
	}
	for id, w := range want {
		got := byID(t, scores, id)
		p := 1 - w.cost/sell
		require.Empty(t, got.Reason, id)
		assert.InDelta(t, w.cost, got.Quote.TotalUSD, eps, id)
		assert.InDelta(t, p, got.PriceScore, eps, id)
		assert.InDelta(t, w.q, got.Quality, eps, id)
		assert.InDelta(t, w.s, got.Service, eps, id)
		assert.InDelta(t, 0.5*p+0.3*w.q+0.2*w.s, got.Total, eps, id)
	}
	assert.Equal(t, "tier 720p not priced", byID(t, scores, 3).Reason)
	assert.Equal(t, "disabled", byID(t, scores, 6).Reason)
	assert.Equal(t, 5, best.ID)
	for i := 1; i < len(scores); i++ {
		assert.False(t, scores[i-1].Reason != "" && scores[i].Reason == "", "rejected candidate sorted before an eligible one")
	}
}

func TestPriceOnlyPrefersPerVideoOnLongVideo(t *testing.T) {
	priceOnly := Policy{MaxCostUSD: bound, Weights: Weights{Price: 1}}
	short, _, err := Select(demoCandidates(spec("720p", 5), 20), priceOnly, nil)
	require.NoError(t, err)
	long, _, err := Select(demoCandidates(spec("720p", 60), 20), priceOnly, nil)
	require.NoError(t, err)
	assert.Equal(t, ModePerSecond, short.Cost.Mode)
	assert.Equal(t, 2, long.ID, "720p per-video: B=1.00 < A=1.20")
}

func TestReferencesRestrictCandidates(t *testing.T) {
	best, scores, err := Select(demoCandidates(spec("1080p", 10, "audio"), 10), gatePolicy, nil)
	require.NoError(t, err)
	assert.Equal(t, 5, best.ID)
	assert.Equal(t, 1, eligibleCount(scores))
	assert.Equal(t, "reference audio not priced", byID(t, scores, 2).Reason)
}

func TestCapacityLayers(t *testing.T) {
	cands := demoCandidates(spec("720p", 5), 3)
	cands[4].InFlight = cands[4].Capacity
	best, scores, err := Select(cands, gatePolicy, nil)
	require.NoError(t, err)
	assert.Equal(t, "at capacity", byID(t, scores, 5).Reason)
	assert.NotEqual(t, 5, best.ID)
	assert.InDelta(t, 1-0.75/3, byID(t, scores, 4).PriceScore, eps, "a known sell price does not depend on other candidates")

	unlimited := edgeCandidate(1)
	unlimited.Capacity, unlimited.InFlight = 0, 1000
	unlimited.Submit.Rate = 0.8
	assert.InDelta(t, 0.8, Evaluate([]Candidate{unlimited}, gatePolicy)[0].Service, eps)

	grouped := edgeCandidate(1)
	grouped.Capacity, grouped.InFlight = 10, 2 // channel headroom 0.8
	grouped.GroupCapacity, grouped.GroupInFlight = 4, 3
	assert.InDelta(t, 0.25, Evaluate([]Candidate{grouped}, gatePolicy)[0].Service, eps, "headroom is the smaller of the two layers")
	grouped.GroupInFlight = 4
	assert.Equal(t, "at capacity", Evaluate([]Candidate{grouped}, gatePolicy)[0].Reason, "a full capacity group blocks a channel with room")
	grouped.GroupInFlight, grouped.InFlight = 0, 10
	assert.Equal(t, "at capacity", Evaluate([]Candidate{grouped}, gatePolicy)[0].Reason)

	for _, tc := range []struct {
		name   string
		mutate func(*Candidate)
		reason string
	}{
		{"negative capacity", func(c *Candidate) { c.Capacity = -1 }, "invalid capacity"},
		{"negative group capacity", func(c *Candidate) { c.GroupCapacity = -1 }, "invalid capacity"},
		{"negative in-flight", func(c *Candidate) { c.InFlight = -1 }, "invalid in-flight count"},
		{"negative unlimited in-flight", func(c *Candidate) { c.Capacity, c.InFlight = 0, -1 }, "invalid in-flight count"},
		{"negative group in-flight", func(c *Candidate) { c.GroupInFlight = -1 }, "invalid in-flight count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := edgeCandidate(1)
			tc.mutate(&c)
			best, scores, err := Select([]Candidate{c}, gatePolicy, nil)
			require.ErrorIs(t, err, ErrNoCandidate)
			assert.Nil(t, best)
			assert.Equal(t, tc.reason, scores[0].Reason)
		})
	}
}

func TestNoEligibleCandidate(t *testing.T) {
	_, scores, err := Select(demoCandidates(spec("8k", 5), 10), gatePolicy, nil)
	require.ErrorIs(t, err, ErrNoCandidate)
	for _, s := range scores {
		assert.NotEmpty(t, s.Reason)
		assert.Zero(t, s.Total)
	}
	for _, pool := range [][]Candidate{nil, {}} {
		best, scores, err := Select(pool, gatePolicy, nil)
		require.ErrorIs(t, err, ErrNoCandidate)
		assert.Nil(t, best)
		assert.Empty(t, scores)
	}
}

func TestSellPriceStates(t *testing.T) {
	freeCost := edgeCandidate(1)
	freeCost.Cost.Prices["720p"], freeCost.Quality = 0, 0.1
	paid := edgeCandidate(2)
	paid.Cost.Prices["720p"] = 0.2 // 1.00 for 5s

	t.Run("known sell scores the margin", func(t *testing.T) {
		freeCost.Sell, paid.Sell = SellPrice{Kind: SellKnown, USD: 1}, SellPrice{Kind: SellKnown, USD: 1}
		scores := Evaluate([]Candidate{freeCost, paid}, gatePolicy)
		assert.InDelta(t, 1, byID(t, scores, 1).PriceScore, eps)
		assert.InDelta(t, 0, byID(t, scores, 2).PriceScore, eps)
		assert.Equal(t, 1, scores[0].Candidate.ID, "free cost with quality 0.1 (0.73) beats paid at break-even (0.5)")
	})
	t.Run("free sell: paid candidates always lose money", func(t *testing.T) {
		freeCost.Sell, paid.Sell = SellPrice{Kind: SellFree}, SellPrice{Kind: SellFree}
		scores := Evaluate([]Candidate{freeCost, paid}, gatePolicy)
		assert.InDelta(t, 1, byID(t, scores, 1).PriceScore, eps)
		assert.InDelta(t, 0, byID(t, scores, 2).PriceScore, eps)
		best, _, err := Select([]Candidate{freeCost, paid}, Policy{MaxCostUSD: bound, Weights: Weights{Quality: 1}}, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, best.ID, "an explicit quality-only preference still wins")
		bothFree := paid
		bothFree.Cost.Prices = map[string]float64{"720p": 0}
		best, scores, err = Select([]Candidate{freeCost, bothFree}, gatePolicy, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, best.ID, "equal price scores compete on quality")
		assert.InDelta(t, 1, byID(t, scores, 1).PriceScore, eps)
		assert.InDelta(t, 1, byID(t, scores, 2).PriceScore, eps)
	})
	t.Run("unknown sell is excluded by default", func(t *testing.T) {
		unknown := paid
		unknown.Sell = SellPrice{Kind: SellUnknown}
		for _, policy := range []string{"", UnknownSellExclude, "bogus"} {
			p := gatePolicy
			p.UnknownSellPolicy = policy
			assert.Equal(t, "sell unknown", Evaluate([]Candidate{unknown}, p)[0].Reason, policy)
		}
	})
	t.Run("relative scoring only without priced candidates", func(t *testing.T) {
		p := gatePolicy
		p.UnknownSellPolicy = UnknownSellRelative
		cheap, dear := paid, paid
		cheap.ID, dear.ID = 1, 2
		cheap.Sell, dear.Sell = SellPrice{Kind: SellUnknown}, SellPrice{Kind: SellUnknown}
		dear.Cost.Prices = map[string]float64{"720p": 0.4}
		scores := Evaluate([]Candidate{cheap, dear}, p)
		assert.InDelta(t, 1, byID(t, scores, 1).PriceScore, eps)
		assert.InDelta(t, 0.5, byID(t, scores, 2).PriceScore, eps)

		known := paid
		known.ID, known.Sell = 3, SellPrice{Kind: SellKnown, USD: 2}
		scores = Evaluate([]Candidate{cheap, dear, known}, p)
		assert.Equal(t, "sell unknown", byID(t, scores, 1).Reason, "margins and cost ratios never share a board")
		assert.Equal(t, "sell unknown", byID(t, scores, 2).Reason)
		assert.Empty(t, byID(t, scores, 3).Reason)
	})
	t.Run("invalid sell", func(t *testing.T) {
		for _, sell := range []SellPrice{{Kind: SellKnown}, {Kind: SellKnown, USD: -1}, {Kind: SellKnown, USD: math.NaN()}, {Kind: SellKnown, USD: math.Inf(1)}} {
			c := paid
			c.Sell = sell
			assert.Equal(t, "invalid sell price", Evaluate([]Candidate{c}, gatePolicy)[0].Reason, sell)
		}
		c := paid
		c.Sell = SellPrice{Kind: "cheap"}
		assert.Equal(t, "invalid sell kind", Evaluate([]Candidate{c}, gatePolicy)[0].Reason)
	})
}

func TestLossThreshold(t *testing.T) {
	c := edgeCandidate(1) // cost 5
	p := gatePolicy
	p.MaxCostToSellRatio = 0.8
	for _, tc := range []struct {
		sell   SellPrice
		reason string
	}{
		{SellPrice{Kind: SellKnown, USD: 10}, ""},
		{SellPrice{Kind: SellKnown, USD: 6.25}, ""},
		{SellPrice{Kind: SellKnown, USD: 6}, "cost/sell 0.83 > 0.80"},
		{SellPrice{Kind: SellKnown, USD: 4}, "cost/sell 1.25 > 0.80"},
		{SellPrice{Kind: SellFree}, "cost/sell +Inf > 0.80"},
	} {
		c.Sell = tc.sell
		assert.Equal(t, tc.reason, Evaluate([]Candidate{c}, p)[0].Reason, tc.sell)
	}
	c.Sell = SellPrice{Kind: SellKnown, USD: 4}
	assert.Empty(t, Evaluate([]Candidate{c}, gatePolicy)[0].Reason, "0 disables the threshold; a losing candidate only scores 0 on price")
}

func TestTieBreakDeterministicAndIndependentOfOrder(t *testing.T) {
	mk := func(id int, price float64) Candidate {
		c := candidate(id, ModePerVideo, map[string]float64{"720p": price}, nil, 0.5, 1, 0, 0, 10)
		c.Spec = spec("720p", 1)
		return c
	}
	for _, pool := range [][]Candidate{
		{mk(3, 1), mk(1, 1), mk(2, 1)}, {mk(3, 1), mk(2, 1), mk(1, 1)}, {mk(1, 1), mk(3, 1), mk(2, 1)},
		{mk(1, 1), mk(2, 1), mk(3, 1)}, {mk(2, 1), mk(3, 1), mk(1, 1)}, {mk(2, 1), mk(1, 1), mk(3, 1)},
	} {
		best, scores, err := Select(pool, gatePolicy, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, best.ID)
		for i, s := range scores {
			assert.Equal(t, i+1, s.Candidate.ID)
		}
	}
	best, _, err := Select([]Candidate{mk(1, 5), mk(2, 1)}, Policy{MaxCostUSD: bound, Weights: Weights{Quality: 1}}, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, best.ID, "equal totals prefer the lower cost")
}

func TestTieEpsilonWeightedDraw(t *testing.T) {
	mk := func(id, weight int, quality float64) Candidate {
		c := edgeCandidate(id)
		c.Weight, c.Quality = weight, quality
		return c
	}
	pool := []Candidate{mk(1, 0, 1), mk(2, 5, 0.99), mk(3, 100, 0.5)}
	p := gatePolicy
	best, _, err := Select(pool, p, rand.New(rand.NewPCG(1, 2)))
	require.NoError(t, err)
	assert.Equal(t, 1, best.ID, "epsilon 0 takes the first")

	p.TieEpsilon = 0.01
	best, _, err = Select(pool, p, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, best.ID, "a nil rnd takes the first")

	best, scores, err := Select(pool, p, rand.New(rand.NewPCG(1, 2)))
	require.NoError(t, err)
	assert.Equal(t, 2, best.ID, "weight 0 is never drawn beside a positive weight; candidates outside epsilon never are")
	assert.Equal(t, 1, scores[0].Candidate.ID, "drawing does not reorder the board")

	equal := []Candidate{mk(1, 0, 1), mk(2, 0, 0.99), mk(3, 0, 0.5)}
	drawn := map[int]bool{}
	for seed := range uint64(8) {
		best, _, err = Select(equal, p, rand.New(rand.NewPCG(seed, 7)))
		require.NoError(t, err)
		drawn[best.ID] = true
	}
	assert.Equal(t, map[int]bool{1: true, 2: true}, drawn, "all-zero weights draw evenly among ties only")

	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		p.TieEpsilon = bad
		best, _, err = Select(pool, p, rand.New(rand.NewPCG(1, 2)))
		require.NoError(t, err)
		assert.Equal(t, 1, best.ID, "invalid epsilon %v takes the first", bad)
	}
}

func TestWeights(t *testing.T) {
	s := spec("720p", 5)
	a := Evaluate(demoCandidates(s, 3), Policy{MaxCostUSD: bound, Weights: Weights{Price: 5, Quality: 3, Service: 2}})
	b := Evaluate(demoCandidates(s, 3), Policy{MaxCostUSD: bound, Weights: DefaultWeights})
	c := Evaluate(demoCandidates(s, 3), Policy{MaxCostUSD: bound})
	for i := range b {
		assert.InDelta(t, b[i].Total, a[i].Total, eps)
		assert.InDelta(t, b[i].Total, c[i].Total, eps)
	}

	// Relative pricing on a single unknown-sell candidate pins PriceScore to 1.
	ch := edgeCandidate(1)
	ch.Quality, ch.Submit.Rate, ch.Sell = 0, 0.5, SellPrice{Kind: SellUnknown}
	for _, tc := range []struct {
		name string
		w    Weights
		want float64
	}{
		{"nan price", Weights{Price: math.NaN(), Quality: 1}, 0},
		{"nan quality", Weights{Quality: math.NaN(), Service: 1}, 0.5},
		{"nan service", Weights{Price: 1, Service: math.NaN()}, 1},
		{"positive infinity", Weights{Price: math.Inf(1), Quality: 1}, 0},
		{"negative infinity", Weights{Price: math.Inf(-1), Quality: 1}, 0},
		{"negative price", Weights{Price: -1, Service: 1}, 0.5},
		{"huge finite weights", Weights{Price: math.MaxFloat64, Quality: math.MaxFloat64}, 0.5},
		{"tiny finite weights", Weights{Price: math.SmallestNonzeroFloat64, Quality: math.SmallestNonzeroFloat64}, 0.5},
		{"zero falls back", Weights{}, 0.6},
		{"all invalid fall back", Weights{Price: math.NaN(), Quality: -1, Service: math.Inf(1)}, 0.6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Evaluate([]Candidate{ch}, Policy{MaxCostUSD: bound, Weights: tc.w, UnknownSellPolicy: UnknownSellRelative})[0]
			require.Empty(t, s.Reason)
			assert.InDelta(t, tc.want, s.Total, eps)
		})
	}
}

func TestWeightSensitivity(t *testing.T) {
	// 1080p 10s at sell 6: B 1.80 (Q.80 S.475), C 3.00 (Q.90 S.99), D 2.80 (Q.75 S.18), E 2.60 (Q.95 S.9215).
	for _, tc := range []struct {
		w    Weights
		want int
	}{
		{Weights{Price: 1}, 2},
		{Weights{Quality: 1}, 5},
		{Weights{Service: 1}, 3},
		// The prototype picked B: cheapest/cost gave it a full price score.
		// Margins compress the gap (0.70 vs 0.57), so E's quality and service win.
		{DefaultWeights, 5},
	} {
		best, _, err := Select(demoCandidates(spec("1080p", 10), 6), Policy{MaxCostUSD: bound, Weights: tc.w}, nil)
		require.NoError(t, err)
		assert.Equal(t, tc.want, best.ID, tc.w)
	}
}

func TestQualityAndRatesClamped(t *testing.T) {
	c := edgeCandidate(1) // cost 5, sell 100
	c.Quality, c.Submit.Rate = 7, -1
	s := Evaluate([]Candidate{c}, Policy{MaxCostUSD: bound, Weights: DefaultWeights})[0]
	require.Empty(t, s.Reason)
	assert.InDelta(t, 1, s.Quality, eps)
	assert.InDelta(t, 0, s.Service, eps)
	assert.InDelta(t, 0.5*0.95+0.3, s.Total, eps)

	for _, tc := range []struct{ value, want float64 }{
		{math.NaN(), 0}, {math.Inf(-1), 0}, {-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {2, 1}, {math.Inf(1), 1},
	} {
		c := edgeCandidate(1)
		c.Quality, c.Capacity, c.InFlight = tc.value, 0, 100000
		s := Evaluate([]Candidate{c}, Policy{MaxCostUSD: bound, Weights: Weights{Quality: 1}})[0]
		require.Empty(t, s.Reason)
		assert.Equal(t, tc.want, s.Total, tc.value)
		assert.Equal(t, 1.0, s.Service)
	}
}

func TestHealthGates(t *testing.T) {
	t.Run("cheap failing channel needs the gate", func(t *testing.T) {
		cheapDead := candidate(1, ModePerVideo, map[string]float64{"720p": 0.1}, nil, 0.5, 0.2, 0, 0, 1.05)
		good := candidate(2, ModePerVideo, map[string]float64{"720p": 1}, nil, 0.9, 0.98, 0, 0, 1.05)
		cheapDead.Spec, good.Spec = spec("720p", 5), spec("720p", 5)
		best, scores, err := Select([]Candidate{cheapDead, good}, gatePolicy, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, best.ID)
		assert.Equal(t, "submit rate 0.20 < 0.50", byID(t, scores, 1).Reason)
		best, _, err = Select([]Candidate{cheapDead, good}, Policy{MaxCostUSD: bound, Weights: DefaultWeights}, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, best.ID, "without the gate additive scoring picks the cheap failing channel")
	})
	t.Run("accepted but never generated trips the generation gate", func(t *testing.T) {
		c := edgeCandidate(1)
		c.Submit, c.Gen = HealthStat{Rate: 1, Samples: 100}, HealthStat{Rate: 0, Samples: 100}
		assert.Equal(t, "generation rate 0.00 < 0.50", Evaluate([]Candidate{c}, gatePolicy)[0].Reason)
	})
	for _, tc := range []struct {
		name string
		rate float64
		p    Policy
		want bool
	}{
		{"just below", math.Nextafter(0.5, 0), gatePolicy, false},
		{"exact boundary", 0.5, gatePolicy, true},
		{"just above", math.Nextafter(0.5, 1), gatePolicy, true},
		{"zero rate with no sample floor", 0, gatePolicy, false},
		{"explicit no gate", 0, Policy{MaxCostUSD: bound, Weights: Weights{Price: 1}}, true},
		{"zero policy adds no gate", 0, Policy{MaxCostUSD: bound}, true},
		{"gate survives weight fallback", 0.2, Policy{MaxCostUSD: bound, MinSubmitRate: 0.5}, false},
		{"perfect boundary", 1, Policy{MaxCostUSD: bound, Weights: Weights{Price: 1}, MinSubmitRate: 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := edgeCandidate(1)
			c.Submit.Rate = tc.rate
			best, _, err := Select([]Candidate{c}, tc.p, nil)
			if tc.want {
				require.NoError(t, err)
				assert.Equal(t, 1, best.ID)
				return
			}
			require.ErrorIs(t, err, ErrNoCandidate)
		})
	}
	t.Run("under-sampled metrics are unproven, not gated", func(t *testing.T) {
		c := edgeCandidate(1)
		c.Submit, c.Gen = HealthStat{Rate: 0, Samples: 5}, HealthStat{Rate: 0.1, Samples: 30}
		p := gatePolicy
		p.MinSamples = 20
		s := Evaluate([]Candidate{c}, p)[0]
		assert.Equal(t, "generation rate 0.10 < 0.50", s.Reason, "a sufficiently sampled metric still gates")
		c.Gen.Rate = 0.8
		s = Evaluate([]Candidate{c}, p)[0]
		require.Empty(t, s.Reason)
		assert.True(t, s.Unproven)
		assert.InDelta(t, 0.8, s.Service, eps, "the under-sampled submit rate counts as 1")
		c.Submit.Samples = 20
		c.Submit.Rate = 0.9
		s = Evaluate([]Candidate{c}, p)[0]
		assert.False(t, s.Unproven)
	})
	for _, rate := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, p := range []Policy{gatePolicy, {Weights: DefaultWeights, MaxCostUSD: bound}} {
			submit, gen := edgeCandidate(1), edgeCandidate(2)
			submit.Submit.Rate, gen.Gen.Rate = rate, rate
			for _, c := range []Candidate{submit, gen} {
				assert.Equal(t, "invalid success rate", Evaluate([]Candidate{c}, p)[0].Reason, "rate=%v gate=%v", rate, p.MinSubmitRate)
			}
		}
	}
	negative := edgeCandidate(1)
	negative.Gen.Samples = -1
	assert.Equal(t, "invalid sample count", Evaluate([]Candidate{negative}, gatePolicy)[0].Reason)
}

func TestInvalidPolicyRejectsEveryCandidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Policy)
		reason string
	}{
		{"negative submit gate", func(p *Policy) { p.MinSubmitRate = -1 }, "invalid minimum success rate"},
		{"nan submit gate", func(p *Policy) { p.MinSubmitRate = math.NaN() }, "invalid minimum success rate"},
		{"inf submit gate", func(p *Policy) { p.MinSubmitRate = math.Inf(1) }, "invalid minimum success rate"},
		{"negative inf gen gate", func(p *Policy) { p.MinGenRate = math.Inf(-1) }, "invalid minimum success rate"},
		{"gen gate above one", func(p *Policy) { p.MinGenRate = 1.01 }, "invalid minimum success rate"},
		{"negative samples", func(p *Policy) { p.MinSamples = -1 }, "invalid minimum samples"},
		{"negative loss threshold", func(p *Policy) { p.MaxCostToSellRatio = -1 }, "invalid loss threshold"},
		{"nan loss threshold", func(p *Policy) { p.MaxCostToSellRatio = math.NaN() }, "invalid loss threshold"},
		{"inf loss threshold", func(p *Policy) { p.MaxCostToSellRatio = math.Inf(1) }, "invalid loss threshold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := gatePolicy
			tc.mutate(&p)
			best, scores, err := Select([]Candidate{edgeCandidate(1), edgeCandidate(2)}, p, nil)
			require.ErrorIs(t, err, ErrNoCandidate)
			assert.Nil(t, best)
			for _, s := range scores {
				assert.Equal(t, tc.reason, s.Reason)
			}
		})
	}
}

func TestPriorityLayers(t *testing.T) {
	high, highGated, low := edgeCandidate(1), edgeCandidate(2), edgeCandidate(3)
	high.Priority, highGated.Priority, low.Priority = 10, 10, 0
	low.Cost.Prices["720p"] = 0.01 // far cheaper, but in a lower layer
	highGated.Submit.Rate = 0.1

	best, scores, err := Select([]Candidate{low, high, highGated}, gatePolicy, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, best.ID)
	assert.Equal(t, "lower priority", byID(t, scores, 3).Reason)
	assert.Equal(t, "submit rate 0.10 < 0.50", byID(t, scores, 2).Reason)

	high.Excluded = "tried"
	best, scores, err = Select([]Candidate{low, high, highGated}, gatePolicy, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, best.ID, "a fully unavailable top layer falls through to the next")
	assert.Equal(t, "tried", byID(t, scores, 1).Reason)

	p := gatePolicy
	p.UnknownSellPolicy = UnknownSellRelative
	lowUnknown := low
	lowUnknown.Sell = SellPrice{Kind: SellUnknown}
	high.Excluded = ""
	scores = Evaluate([]Candidate{high, lowUnknown}, p)
	assert.Equal(t, "lower priority", byID(t, scores, 3).Reason, "a priced top layer does not mix with a lower unknown-sell layer")
}

func TestPerCandidateSpecs(t *testing.T) {
	// Two plugins decode the same client request differently; each quotes its own spec.
	megabyai, seedance := edgeCandidate(1), edgeCandidate(2)
	megabyai.PluginKey, seedance.PluginKey = "megabyai", "seedance-hjmie"
	seedance.Spec = spec("720p", 8, "audio")
	seedance.Spec.SecondsKind = KindFixed
	scores := Evaluate([]Candidate{megabyai, seedance}, gatePolicy)
	assert.InDelta(t, 5, byID(t, scores, 1).Quote.TotalUSD, eps)
	assert.InDelta(t, 8+2, byID(t, scores, 2).Quote.TotalUSD, eps)
	assert.Empty(t, byID(t, scores, 1).Quote.References)
	assert.Len(t, byID(t, scores, 2).Quote.References, 1)

	seedance.Cost.MaxSeconds = 6
	assert.Equal(t, "seconds 8 out of range", Evaluate([]Candidate{seedance}, gatePolicy)[0].Reason, "a fixed length still honors channel limits")
}

// ---- Scenarios -------------------------------------------------------------

func TestReferenceMatrix(t *testing.T) {
	// Independently computed: per-video price, then 0.5s / 5s / 15s per-second prices.
	cases := []struct {
		tier       string
		references []string
		costs      [4]float64
	}{
		{"480p", nil, [4]float64{0.25, 0.125, 1.25, 3.75}},
		{"480p", []string{"audio"}, [4]float64{0.5, 0.25, 2.5, 7.5}},
		{"480p", []string{"video"}, [4]float64{0.75, 0.375, 3.75, 11.25}},
		{"480p", []string{"audio", "video"}, [4]float64{1, 0.5, 5, 15}},
		{"720p", nil, [4]float64{0.5, 0.25, 2.5, 7.5}},
		{"720p", []string{"audio"}, [4]float64{0.75, 0.375, 3.75, 11.25}},
		{"720p", []string{"video"}, [4]float64{1, 0.5, 5, 15}},
		{"720p", []string{"audio", "video"}, [4]float64{1.25, 0.625, 6.25, 18.75}},
		{"1080p", nil, [4]float64{1, 0.5, 5, 15}},
		{"1080p", []string{"audio"}, [4]float64{1.25, 0.625, 6.25, 18.75}},
		{"1080p", []string{"video"}, [4]float64{1.5, 0.75, 7.5, 22.5}},
		{"1080p", []string{"audio", "video"}, [4]float64{1.75, 0.875, 8.75, 26.25}},
		{"", nil, [4]float64{}},
		{"", []string{"audio"}, [4]float64{}},
		{"", []string{"video", "audio"}, [4]float64{}},
	}
	for _, tc := range cases {
		for _, mode := range []string{ModePerVideo, ModePerSecond} {
			for i, seconds := range []float64{0.5, 5, 15} {
				t.Run(fmt.Sprintf("%s/%s/%gs/%v", mode, tc.tier, seconds, tc.references), func(t *testing.T) {
					c := edgeCandidate(1)
					c.Cost = CostConfig{Mode: mode, Prices: map[string]float64{"480p": 0.25, "720p": 0.5, "1080p": 1},
						References: flatReferences(mode, map[string]float64{"audio": 0.25, "video": 0.5})}
					c.Spec = spec(tc.tier, seconds, tc.references...)
					best, scores, err := Select([]Candidate{c}, gatePolicy, nil)
					if tc.tier == "" {
						require.ErrorIs(t, err, ErrNoCandidate)
						assert.Equal(t, "tier unknown", scores[0].Reason)
						return
					}
					want := tc.costs[0]
					if mode == ModePerSecond {
						want = tc.costs[i+1]
					}
					require.NoError(t, err)
					assert.Equal(t, 1, best.ID)
					assert.Equal(t, want, scores[0].Quote.TotalUSD)
					assert.Equal(t, tc.tier, scores[0].Quote.Tier)
				})
			}
		}
	}
}

func TestRejectedCandidatesNeverSetRelativeReference(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(*Candidate)
	}{
		{"excluded", "disabled", func(c *Candidate) { c.Excluded = "disabled" }},
		{"tier", "tier 720p not priced", func(c *Candidate) { c.Cost.Prices = map[string]float64{"480p": 0.01} }},
		{"unknown tier", "tier unknown", func(c *Candidate) { c.Spec.Tier = "" }},
		{"no prices", "no price configured", func(c *Candidate) { c.Cost.Prices = nil }},
		{"reference", "reference audio not priced", func(c *Candidate) { c.Cost.References = nil }},
		{"negative surcharge", "invalid reference audio price", func(c *Candidate) { c.Cost.References["audio"]["*"] = rule(RefPerOutputSecond, -1) }},
		{"nan surcharge", "invalid reference audio price", func(c *Candidate) { c.Cost.References["audio"]["*"] = rule(RefPerOutputSecond, math.NaN()) }},
		{"inf surcharge", "invalid reference audio price", func(c *Candidate) { c.Cost.References["audio"]["*"] = rule(RefPerOutputSecond, math.Inf(1)) }},
		{"unsupported reference", "reference audio unsupported", func(c *Candidate) { c.Cost.References["audio"]["*"] = ReferenceCost{Mode: RefUnsupported} }},
		{"cost bound", "cost exceeds bound", func(c *Candidate) { c.Cost.Prices = map[string]float64{"720p": bound} }},
		{"price", "invalid price", func(c *Candidate) { c.Cost.Prices["720p"] = math.NaN() }},
		{"mode", "invalid billing mode", func(c *Candidate) { c.Cost.Mode = "per_minute" }},
		{"seconds constraint", "seconds 5 out of range", func(c *Candidate) { c.Cost.MaxSeconds = 4 }},
		{"capacity", "at capacity", func(c *Candidate) { c.InFlight = c.Capacity }},
		{"over capacity", "at capacity", func(c *Candidate) { c.InFlight = c.Capacity + 1 }},
		{"group capacity", "at capacity", func(c *Candidate) { c.GroupCapacity, c.GroupInFlight = 3, 3 }},
		{"submit health", "submit rate 0.49 < 0.50", func(c *Candidate) { c.Submit.Rate = 0.49 }},
		{"generation health", "generation rate 0.49 < 0.50", func(c *Candidate) { c.Gen.Rate = 0.49 }},
		{"unknown health", "invalid success rate", func(c *Candidate) { c.Submit.Rate = math.NaN() }},
		{"negative capacity", "invalid capacity", func(c *Candidate) { c.Capacity = -1 }},
		{"negative in-flight", "invalid in-flight count", func(c *Candidate) { c.InFlight = -1 }},
	}
	p := gatePolicy
	p.UnknownSellPolicy = UnknownSellRelative
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad, good := edgeCandidate(1), edgeCandidate(2)
			bad.Cost.Prices = map[string]float64{"720p": 0.01}
			bad.Sell, good.Sell = SellPrice{Kind: SellUnknown}, SellPrice{Kind: SellUnknown}
			bad.Spec, good.Spec = spec("720p", 5, "audio"), spec("720p", 5, "audio")
			tc.mutate(&bad)
			best, scores, err := Select([]Candidate{bad, good}, p, nil)
			require.NoError(t, err)
			assert.Equal(t, 2, best.ID)
			assert.Equal(t, 6.25, scores[0].Quote.TotalUSD)
			assert.Equal(t, 1.0, scores[0].PriceScore)
			assert.Equal(t, tc.reason, scores[1].Reason)
			assert.Zero(t, scores[1].Total)
		})
	}
}

func TestReferencePricePolicy(t *testing.T) {
	a, b, c := edgeCandidate(1), edgeCandidate(2), edgeCandidate(3)
	for _, x := range []*Candidate{&a, &b, &c} {
		x.Cost.Mode = ModePerVideo
		x.Sell = SellPrice{Kind: SellKnown, USD: 4}
	}
	a.Quality, b.Cost.Prices["720p"], c.Cost.Prices["720p"], c.Quality = 0.5, 2, 0.2, 0
	c.Capacity, c.InFlight = 1000, 999

	before, _, err := Select([]Candidate{a, b}, gatePolicy, nil)
	require.NoError(t, err)
	after, scores, err := Select([]Candidate{a, b, c}, gatePolicy, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, before.ID)
	assert.Equal(t, 2, after.ID, "with a known sell price a third candidate cannot reorder A and B")
	assert.InDelta(t, 0.725, byID(t, scores, 1).Total, eps)
	assert.InDelta(t, 0.75, byID(t, scores, 2).Total, eps)

	// Relative pricing keeps the prototype's reference-price reversal, which is
	// why it only runs when no candidate has a sell price.
	p := gatePolicy
	p.UnknownSellPolicy = UnknownSellRelative
	for _, x := range []*Candidate{&a, &b, &c} {
		x.Sell = SellPrice{Kind: SellUnknown}
	}
	before, _, err = Select([]Candidate{a, b}, p, nil)
	require.NoError(t, err)
	after, scores, err = Select([]Candidate{a, b, c}, p, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, before.ID)
	assert.Equal(t, 2, after.ID)
	assert.InDelta(t, 0.45, byID(t, scores, 1).Total, eps)
	assert.InDelta(t, 0.55, byID(t, scores, 2).Total, eps)
}

func TestInvalidRequestsRejectEveryCandidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Spec)
		reason string
	}{
		{"zero seconds", func(s *Spec) { s.OutputSeconds = secs(0) }, "invalid seconds"},
		{"negative seconds", func(s *Spec) { s.OutputSeconds = secs(-1) }, "invalid seconds"},
		{"nan seconds", func(s *Spec) { s.OutputSeconds = secs(math.NaN()) }, "invalid seconds"},
		{"positive inf seconds", func(s *Spec) { s.OutputSeconds = secs(math.Inf(1)) }, "invalid seconds"},
		{"negative inf seconds", func(s *Spec) { s.OutputSeconds = secs(math.Inf(-1)) }, "invalid seconds"},
		{"unsupported tier", func(s *Spec) { s.Tier = "8k" }, "tier 8k not priced"},
		{"unpriced reference", func(s *Spec) { s.References = map[string]int{"audio": 1, "image": 1} }, "reference image not priced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := edgeCandidate(1), edgeCandidate(2)
			a.Cost.Mode = ModePerVideo
			tc.mutate(&a.Spec)
			tc.mutate(&b.Spec)
			best, scores, err := Select([]Candidate{a, b}, gatePolicy, nil)
			require.ErrorIs(t, err, ErrNoCandidate)
			assert.Nil(t, best)
			for _, s := range scores {
				assert.Equal(t, tc.reason, s.Reason)
				assert.Zero(t, s.Total)
			}
		})
	}
}

func TestLifecycleRecomputation(t *testing.T) {
	// The caller updates load, health and exclusion; each Select recomputes.
	a, b := edgeCandidate(1), edgeCandidate(2)
	a.Capacity, b.Capacity, b.Cost.Prices["720p"] = 2, 1, 2
	a.Sell.USD, b.Sell.USD = 10.5, 10.5 // costs 5 and 10: a thin margin keeps A ahead at half load
	pool := []Candidate{a, b}
	choose := func(want int) {
		t.Helper()
		best, _, err := Select(pool, gatePolicy, nil)
		if want == 0 {
			require.ErrorIs(t, err, ErrNoCandidate)
			return
		}
		require.NoError(t, err)
		require.Equal(t, want, best.ID)
		best.InFlight++
	}
	choose(1)
	choose(1)
	choose(2)
	choose(0)
	pool[0].InFlight-- // one task finished
	choose(1)
	pool[0].InFlight, pool[1].InFlight = 0, 0
	pool[0].Submit.Rate = 0.2 // failures observed
	choose(2)
	pool[1].InFlight = 0
	pool[0].Submit.Rate = 1 // a probe succeeded; the algorithm itself never probes
	choose(1)
	pool[0].InFlight = 0
	pool[0].Excluded = "disabled"
	choose(2)
	pool[1].InFlight = 0
	pool[0].Excluded = ""
	choose(1)
}

func TestHeadroomSpreadsLoadWithinCapacity(t *testing.T) {
	// Dispatch 720p 5s without releasing: headroom feedback must spread load,
	// never exceed any capacity, and end with no candidate once all are full.
	pool := demoCandidates(spec("720p", 5), 3)
	for i := range pool {
		pool[i].InFlight = 0
	}
	used := map[int]int{}
	for {
		best, _, err := Select(pool, gatePolicy, nil)
		if err != nil {
			require.ErrorIs(t, err, ErrNoCandidate)
			break
		}
		best.InFlight++
		used[best.ID]++
	}
	assert.Equal(t, map[int]int{1: 10, 2: 10, 4: 10, 5: 20}, used, "every 720p-capable channel fills exactly to capacity")
}

func TestEvaluateLeavesInputsUnchanged(t *testing.T) {
	c := edgeCandidate(1)
	for _, mode := range []string{ModePerVideo, ModePerSecond} {
		c.Cost.Mode = mode
		c.Cost.References = flatReferences(mode, map[string]float64{"audio": 0.25, "video": 0.5})
		c.Spec = spec("720p", 5, "audio", "video")
		pool := []Candidate{c}
		snapshot := fmt.Sprintf("%#v", pool)
		best, scores, err := Select(pool, gatePolicy, nil)
		require.NoError(t, err)
		assert.Same(t, &pool[0], best)
		want := 1.75
		if mode == ModePerSecond {
			want = 8.75
		}
		assert.Equal(t, want, scores[0].Quote.TotalUSD)
		assert.Equal(t, snapshot, fmt.Sprintf("%#v", pool), "Select must not reserve capacity or mutate the snapshot")
	}
}

// marketCandidates is the prototype's synthetic 20-channel market, filtered by
// model the way candidate assembly will. Prices are not real supplier quotes.
func marketCandidates(model string, s Spec, sell float64) []Candidate {
	const seedance, kling, veo = "seedance", "kling", "veo"
	tiers := func(p ...float64) map[string]float64 {
		m := map[string]float64{}
		for i, v := range p {
			if v > 0 {
				m[[]string{"480p", "720p", "1080p", "4k"}[i]] = v
			}
		}
		return m
	}
	type row struct {
		c      Candidate
		models []string
	}
	rows := []row{
		{candidate(101, ModePerSecond, tiers(0.06, 0.14, 0.28, 0.60), map[string]float64{"audio": 0.04, "video": 0.10}, 0.95, 0.985, 30, 200, sell), []string{seedance}},
		{candidate(102, ModePerVideo, map[string]float64{"720p": 0.90, "1080p": 1.60}, nil, 0.80, 0.93, 12, 30, sell), []string{seedance}},
		{candidate(103, ModePerVideo, map[string]float64{"720p": 0.70}, nil, 0.70, 0.88, 25, 30, sell), []string{seedance}},
		{candidate(104, ModePerSecond, map[string]float64{"720p": 0.09}, nil, 0.60, 0.75, 3, 20, sell), []string{seedance}},
		{candidate(105, ModePerSecond, tiers(0.05, 0.11, 0.22), nil, 0.65, 0.80, 18, 20, sell), []string{seedance}},
		{candidate(106, ModePerSecond, tiers(0, 0.16, 0.30, 0.70), map[string]float64{"audio": 0.03}, 0.98, 0.99, 5, 50, sell), []string{seedance}},
		{candidate(107, ModePerVideo, map[string]float64{"720p": 0.30, "1080p": 0.50}, nil, 0.50, 0.20, 0, 10, sell), []string{seedance}},
		{candidate(108, ModePerVideo, map[string]float64{"720p": 0.10}, nil, 1, 1, 0, 0, sell), []string{seedance}},
		{candidate(201, ModePerVideo, map[string]float64{"720p": 2.50, "1080p": 4.50}, nil, 0.95, 0.97, 40, 100, sell), []string{kling}},
		{candidate(202, ModePerVideo, map[string]float64{"720p": 1.80, "1080p": 3.20}, nil, 0.85, 0.92, 8, 20, sell), []string{kling}},
		{candidate(203, ModePerSecond, map[string]float64{"720p": 0.35, "1080p": 0.60}, nil, 0.85, 0.90, 2, 20, sell), []string{kling}},
		{candidate(204, ModePerSecond, tiers(0, 0.20, 0.40), map[string]float64{"audio": 0.05}, 0.80, 0.94, 50, 60, sell), []string{kling, seedance, veo}},
		{candidate(205, ModePerVideo, map[string]float64{"720p": 1.00}, nil, 0.9, 0.95, 10, 10, sell), []string{kling}},
		{candidate(301, ModePerSecond, map[string]float64{"1080p": 2.80}, map[string]float64{"audio": 0.70}, 1.0, 0.99, 20, 100, sell), []string{veo}},
		{candidate(302, ModePerVideo, map[string]float64{"1080p": 18.0}, map[string]float64{"audio": 4.0}, 0.90, 0.90, 3, 15, sell), []string{veo}},
		{candidate(303, ModePerSecond, map[string]float64{"720p": 1.50, "1080p": 2.20}, nil, 0.85, 0.86, 1, 10, sell), []string{veo}},
		{candidate(401, ModePerSecond, nil, nil, 1, 1, 0, 0, sell), []string{seedance}},
		{candidate(402, ModePerVideo, map[string]float64{"720p": 1.10}, nil, 0.75, 0.95, 9999, 0, sell), []string{seedance}},
		{candidate(403, ModePerVideo, map[string]float64{"480p": 0}, nil, 0.40, 0.70, 0, 5, sell), []string{seedance}},
		{candidate(404, ModePerVideo, map[string]float64{"720p": 1.50}, nil, -0.5, 1.5, 0, 10, sell), []string{kling}},
	}
	out := []Candidate{}
	for _, r := range rows {
		if !slices.Contains(r.models, model) {
			continue
		}
		r.c.Spec = s
		if r.c.ID == 108 {
			r.c.Excluded = "disabled"
		}
		out = append(out, r.c)
	}
	return out
}

func TestMarketPoolScenarios(t *testing.T) {
	// Winners and eligible counts were computed independently of this package.
	// Margin scoring changes four prototype winners: 104->101 (quality/service now
	// outweigh a 0.25 cost gap), 102->402, 403->101 and 204->203.
	cases := []struct {
		name     string
		model    string
		spec     Spec
		sell     float64
		want     int
		eligible int
	}{
		{"seedance 720p 5s", "seedance", spec("720p", 5), 2, 101, 8},
		{"seedance 720p 10s", "seedance", spec("720p", 10), 3, 402, 8},
		{"seedance 1080p 8s audio", "seedance", spec("1080p", 8, "audio"), 5, 106, 3},
		{"seedance 480p 5s with free trial", "seedance", spec("480p", 5), 1, 101, 3},
		{"seedance 4k 10s", "seedance", spec("4k", 10), 10, 101, 2},
		{"kling 720p 5s", "kling", spec("720p", 5), 3, 203, 5},
		{"kling 1080p 10s", "kling", spec("1080p", 10), 8, 202, 4},
		{"veo 1080p 8s audio", "veo", spec("1080p", 8, "audio"), 30, 204, 3},
		{"veo 720p 5s", "veo", spec("720p", 5), 10, 204, 2},
		{"unknown model", "sora-2", spec("720p", 5), 10, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := marketCandidates(tc.model, tc.spec, tc.sell)
			best, scores, err := Select(pool, gatePolicy, nil)
			assert.Equal(t, tc.eligible, eligibleCount(scores))
			if tc.want == 0 {
				require.ErrorIs(t, err, ErrNoCandidate)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, best.ID)
			if tc.model == "seedance" {
				assert.Equal(t, "submit rate 0.20 < 0.50", byID(t, scores, 107).Reason, "the failing reseller never gets traffic")
			}

			// Monotonicity for every eligible candidate, not just the winner:
			// a higher cost never improves rank; better quality, health or load never worsens it.
			rank := func(pool []Candidate, id int) int {
				for i, s := range Evaluate(pool, gatePolicy) {
					if s.Candidate.ID == id {
						return i
					}
				}
				return -1
			}
			for base, s := range scores {
				if s.Reason != "" {
					continue
				}
				idx := slices.IndexFunc(pool, func(c Candidate) bool { return c.ID == s.Candidate.ID })
				mutated := func(mutate func(*Candidate)) []Candidate {
					out := slices.Clone(pool)
					mutate(&out[idx])
					return out
				}
				pricier := mutated(func(c *Candidate) {
					prices := map[string]float64{}
					for k, v := range c.Cost.Prices {
						prices[k] = v*10 + 1
					}
					c.Cost.Prices = prices
				})
				assert.GreaterOrEqual(t, rank(pricier, s.Candidate.ID), base, "raising %d's cost improved its rank", s.Candidate.ID)
				for name, improve := range map[string]func(*Candidate){
					"quality": func(c *Candidate) { c.Quality = 1 },
					"health":  func(c *Candidate) { c.Submit.Rate = 1 },
					"load":    func(c *Candidate) { c.InFlight = 0 },
				} {
					assert.LessOrEqual(t, rank(mutated(improve), s.Candidate.ID), base, "improving %d's %s worsened its rank", s.Candidate.ID, name)
				}
			}
		})
	}
}

func TestEvaluateProbeOnlyRelaxesHealthGates(t *testing.T) {
	gated := func(id int) Candidate {
		c := edgeCandidate(id)
		c.Submit.Rate = 0.1 // gated by gatePolicy
		return c
	}
	priced, unpriced := gated(1), gated(2)
	unpriced.Spec = spec("720p", 5, "video") // edgeCandidate prices only audio references

	scores := EvaluateProbe([]Candidate{priced, unpriced}, gatePolicy)
	got := byID(t, scores, 1)
	require.Empty(t, got.Reason, "a gated candidate with a valid quote gets a probe score")
	assert.InDelta(t, 5, got.Quote.TotalUSD, eps)
	assert.InDelta(t, 0.1, got.Service, eps, "the real submit rate still scores; it is not raised to 1")
	assert.Positive(t, got.Total)
	assert.Equal(t, "reference video not priced", byID(t, scores, 2).Reason)
	assert.Zero(t, byID(t, scores, 2).Total)

	for _, s := range Evaluate([]Candidate{priced, unpriced}, gatePolicy) {
		assert.Equal(t, "submit rate 0.10 < 0.50", s.Reason, "ordinary evaluation still excludes gated candidates")
	}
	genGated := edgeCandidate(3)
	genGated.Gen.Rate = 0
	assert.Empty(t, EvaluateProbe([]Candidate{genGated}, gatePolicy)[0].Reason)

	for _, tc := range []struct {
		name   string
		mutate func(*Candidate)
		reason string
	}{
		{"at capacity", func(c *Candidate) { c.InFlight = c.Capacity }, "at capacity"},
		{"group at capacity", func(c *Candidate) { c.GroupCapacity, c.GroupInFlight = 2, 2 }, "at capacity"},
		{"tried", func(c *Candidate) { c.Excluded = "tried" }, "tried"},
		{"disabled", func(c *Candidate) { c.Excluded = "disabled" }, "disabled"},
		{"cost bound", func(c *Candidate) { c.Cost.Prices = map[string]float64{"720p": bound} }, "cost exceeds bound"},
		{"unknown sell", func(c *Candidate) { c.Sell = SellPrice{Kind: SellUnknown} }, "sell unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := gated(1)
			tc.mutate(&c)
			assert.Equal(t, tc.reason, EvaluateProbe([]Candidate{c}, gatePolicy)[0].Reason)
		})
	}
	t.Run("loss threshold", func(t *testing.T) {
		c := gated(1)
		c.Sell.USD = 4
		p := gatePolicy
		p.MaxCostToSellRatio = 1
		assert.Equal(t, "cost/sell 1.25 > 1.00", EvaluateProbe([]Candidate{c}, p)[0].Reason)
	})
	t.Run("invalid policy", func(t *testing.T) {
		p := gatePolicy
		p.MaxCostUSD = 0
		assert.Equal(t, "invalid policy", EvaluateProbe([]Candidate{gated(1)}, p)[0].Reason)
	})
	t.Run("probing stays inside the priority layer", func(t *testing.T) {
		high, low := edgeCandidate(1), gated(2)
		high.Priority, low.Priority = 10, 0
		scores := EvaluateProbe([]Candidate{high, low}, gatePolicy)
		assert.Equal(t, "lower priority", byID(t, scores, 2).Reason, "a healthy higher layer is never bypassed by a probe")
		high.Excluded = "tried"
		scores = EvaluateProbe([]Candidate{high, low}, gatePolicy)
		assert.Empty(t, byID(t, scores, 2).Reason, "an exhausted higher layer falls through to the gated lower one")
	})
}
