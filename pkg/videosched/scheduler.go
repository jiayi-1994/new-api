// Package videosched scores video-generation candidate channels by margin,
// operator quality and service health. It is pure: no database, gin, Redis or
// plugin dependencies. Callers assemble one Candidate per upstream channel,
// each carrying the Spec its own execution plugin decoded, and pass a snapshot.
package videosched

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
)

// Spec value kinds.
const (
	KindExact    = "exact"
	KindEstimate = "estimate"
	KindFixed    = "fixed" // the request cannot change it; channel constraints still apply

	TierResolution = "resolution"
	TierProduct    = "product"
	TierUnknown    = "unknown"
)

// Cost modes, surcharge kinds and input media keys.
const (
	ModePerVideo  = "per_video"
	ModePerSecond = "per_second"
	ModePerUnit   = "per_unit"

	SurchargeAddPerUnit = "add_per_unit" // added to the base unit price before quantity
	SurchargeAddFlat    = "add_flat"     // added once per video
	SurchargeMultiply   = "multiply"     // scales base + additions; may be < 1

	InputVideoSeconds = "input_video_seconds"
	InputImages       = "input_images"
)

// Sell price kinds and unknown-sell policies.
const (
	SellKnown   = "known"
	SellFree    = "free"
	SellUnknown = "unknown"

	UnknownSellExclude  = "exclude"
	UnknownSellRelative = "relative"
)

// Spec is the normalized request shape one candidate's plugin will send.
// A nil pointer means unknown, which is distinct from zero.
type Spec struct {
	OutputSeconds     *float64
	SecondsKind       string // exact|estimate|fixed
	Tier              string // lowercase resolution or product key
	TierKind          string // resolution|product|unknown
	InputVideoSeconds *float64
	InputMediaKind    string // exact|estimate
	InputImages       *int
	Units             map[string]float64 // tokens/credits estimates
	Conditions        map[string]bool    // video_input, audio ...
	Missing           []string
}

// Surcharge applies when Conditions[When] is true and Tier is empty or matches.
// Every condition the request sets must match at least one surcharge, so
// "included for free" is written as an add_flat surcharge of 0.
type Surcharge struct {
	When  string
	Tier  string
	Kind  string
	Value float64
}

// CostConfig is one channel's purchase price table for one model, in USD.
type CostConfig struct {
	Mode           string
	UnitName       string
	Prices         map[string]float64 // tier -> USD; "*" matches any tier
	MinSeconds     int
	MaxSeconds     int
	AllowedSeconds []int
	Surcharges     []Surcharge
	InputMedia     map[string]float64 // input_video_seconds | input_images -> USD per unit
}

type HealthStat struct {
	Rate    float64
	Samples int
}

type SellPrice struct {
	Kind      string // known|free|unknown
	USD       float64
	Estimated bool
}

// Candidate is a snapshot of one channel for one request.
type Candidate struct {
	ID       int
	Name     string
	Priority int64
	Weight   int

	PluginKey, MappedModel string
	Spec                   Spec // decoded by this candidate's own plugin and mapped model
	Cost                   CostConfig
	Quality                float64

	Capacity, InFlight           int // channel; Capacity 0 = unlimited
	GroupCapacity, GroupInFlight int // capacity group total; 0 = no group

	Submit, Gen HealthStat
	Sell        SellPrice
	Excluded    string // non-empty = excluded while assembling (disabled, tried, ...)
}

// Weights are normalized internally; they need not sum to 1.
type Weights struct {
	Price, Quality, Service float64
}

var DefaultWeights = Weights{Price: 0.5, Quality: 0.3, Service: 0.2}

type Policy struct {
	Weights       Weights
	MinSubmitRate float64 // [0,1]; 0 disables the gate
	MinGenRate    float64 // [0,1]; 0 disables the gate
	MinSamples    int     // metrics with fewer samples count as 1 and mark Unproven

	UnknownSellPolicy  string  // exclude (default) | relative
	MaxCostToSellRatio float64 // 0 = no loss threshold
	TieEpsilon         float64 // 0 = take the first; >0 = weighted draw among near-ties
}

type Score struct {
	Candidate  *Candidate
	Tier       string
	Cost       float64
	Estimated  bool // cost depends on estimated seconds or input media
	PriceScore float64
	Quality    float64
	Service    float64
	Total      float64
	Unproven   bool
	Reason     string // non-empty = not eligible
}

var ErrNoCandidate = errors.New("videosched: no eligible candidate")

// Quote prices spec against a channel's cost table.
func Quote(cost CostConfig, spec Spec) (tier string, usd float64, reason string) {
	if cost.Mode != ModePerVideo && cost.Mode != ModePerSecond && cost.Mode != ModePerUnit {
		return "", 0, "invalid billing mode"
	}
	if len(cost.Prices) == 0 {
		return "", 0, "no price configured"
	}

	tier = strings.ToLower(strings.TrimSpace(spec.Tier))
	base, ok := cost.Prices[tier]
	switch {
	case spec.TierKind == TierUnknown || tier == "":
		// Never guess the cheapest tier: only a lone wildcard price quotes blind.
		base, ok = cost.Prices["*"]
		if !ok || len(cost.Prices) != 1 {
			return "", 0, "tier unknown"
		}
		tier = "*"
	case !ok:
		if base, ok = cost.Prices["*"]; !ok {
			return "", 0, "tier " + tier + " not supported"
		}
	}
	if !finiteNonNeg(base) {
		return "", 0, "invalid price"
	}

	constrained := cost.MinSeconds > 0 || cost.MaxSeconds > 0 || len(cost.AllowedSeconds) > 0
	seconds := 0.0
	if spec.OutputSeconds == nil {
		if cost.Mode == ModePerSecond || constrained {
			return "", 0, "seconds unknown"
		}
	} else {
		seconds = *spec.OutputSeconds
		if !(seconds > 0) || math.IsInf(seconds, 0) { // also rejects NaN
			return "", 0, "invalid seconds"
		}
		if (cost.MinSeconds > 0 && seconds < float64(cost.MinSeconds)) || (cost.MaxSeconds > 0 && seconds > float64(cost.MaxSeconds)) {
			return "", 0, fmt.Sprintf("seconds %g out of range", seconds)
		}
		if len(cost.AllowedSeconds) > 0 && !slices.ContainsFunc(cost.AllowedSeconds, func(allowed int) bool { return float64(allowed) == seconds }) {
			return "", 0, fmt.Sprintf("seconds %g not allowed", seconds)
		}
	}

	quantity := 1.0
	switch cost.Mode {
	case ModePerSecond:
		quantity = seconds
	case ModePerUnit:
		units, ok := spec.Units[cost.UnitName]
		if cost.UnitName == "" || !ok {
			return "", 0, "units unknown"
		}
		if !finiteNonNeg(units) {
			return "", 0, "invalid units"
		}
		quantity = units
	}

	unit, flat, multiplier := base, 0.0, 1.0
	for _, condition := range slices.Sorted(maps.Keys(spec.Conditions)) {
		if !spec.Conditions[condition] {
			continue
		}
		priced := false
		for _, s := range cost.Surcharges {
			if s.When != condition || (s.Tier != "" && !strings.EqualFold(s.Tier, tier)) {
				continue
			}
			priced = true
			switch {
			case s.Kind == SurchargeAddPerUnit && finiteNonNeg(s.Value):
				unit += s.Value
			case s.Kind == SurchargeAddFlat && finiteNonNeg(s.Value):
				flat += s.Value
			case s.Kind == SurchargeMultiply && s.Value > 0 && !math.IsInf(s.Value, 1):
				multiplier *= s.Value
			default:
				return "", 0, "invalid surcharge"
			}
		}
		if !priced {
			return "", 0, "condition " + condition + " not priced"
		}
	}

	usd = unit * quantity
	if !finiteNonNeg(usd) || (unit > 0 && quantity > 0 && usd == 0) {
		return "", 0, "invalid total price"
	}
	usd += flat
	scaled := usd * multiplier
	if !finiteNonNeg(scaled) || (usd > 0 && scaled == 0) {
		return "", 0, "invalid total price"
	}
	usd = scaled

	for _, media := range slices.Sorted(maps.Keys(cost.InputMedia)) {
		price := cost.InputMedia[media]
		if !finiteNonNeg(price) {
			return "", 0, "invalid input media price"
		}
		var amount float64
		switch media {
		case InputVideoSeconds:
			if spec.InputVideoSeconds == nil {
				return "", 0, "input media unknown"
			}
			amount = *spec.InputVideoSeconds
		case InputImages:
			if spec.InputImages == nil {
				return "", 0, "input media unknown"
			}
			amount = float64(*spec.InputImages)
		default:
			return "", 0, "unsupported input media " + media
		}
		if !finiteNonNeg(amount) {
			return "", 0, "invalid input media"
		}
		charge := price * amount
		if !finiteNonNeg(charge) || (price > 0 && amount > 0 && charge == 0) {
			return "", 0, "invalid total price"
		}
		usd += charge
	}
	if !finiteNonNeg(usd) {
		return "", 0, "invalid total price"
	}
	return tier, usd, ""
}

// Evaluate scores every candidate and returns them sorted: eligible first,
// then Total desc, Cost asc, ID asc. Only the highest Priority layer holding an
// eligible candidate is ranked; eligible candidates below it get the reason
// "lower priority". Evaluate does not modify cands.
func Evaluate(cands []Candidate, p Policy) []Score {
	wp, wq, ws := normalizeWeights(p.Weights)
	policyReason := ""
	switch {
	case !validRate(p.MinSubmitRate) || !validRate(p.MinGenRate):
		policyReason = "invalid minimum success rate"
	case p.MinSamples < 0:
		policyReason = "invalid minimum samples"
	case !finiteNonNeg(p.MaxCostToSellRatio):
		policyReason = "invalid loss threshold"
	}

	scores := make([]Score, len(cands))
	for i := range cands {
		c := &cands[i]
		s := &scores[i]
		s.Candidate = c
		if s.Reason = candidateReason(c, p, policyReason); s.Reason != "" {
			continue
		}
		if s.Tier, s.Cost, s.Reason = Quote(c.Cost, c.Spec); s.Reason != "" {
			continue
		}
		s.Estimated = c.Spec.SecondsKind == KindEstimate || c.Spec.InputMediaKind == KindEstimate
		s.Unproven = c.Submit.Samples < p.MinSamples || c.Gen.Samples < p.MinSamples
		if p.MaxCostToSellRatio > 0 && c.Sell.Kind != SellUnknown {
			ratio := 0.0
			switch {
			case c.Sell.Kind == SellKnown:
				ratio = s.Cost / c.Sell.USD
			case s.Cost > 0: // a paid channel against a free sell price always loses
				ratio = math.Inf(1)
			}
			if ratio > p.MaxCostToSellRatio {
				s.Reason = fmt.Sprintf("cost/sell %.2f > %.2f", ratio, p.MaxCostToSellRatio)
			}
		}
	}

	topSet, top := false, int64(0)
	for i := range scores {
		if scores[i].Reason == "" && (!topSet || scores[i].Candidate.Priority > top) {
			topSet, top = true, scores[i].Candidate.Priority
		}
	}
	hasPricedSell := false
	cheapestUnknown := math.Inf(1)
	for i := range scores {
		s := &scores[i]
		if s.Reason != "" {
			continue
		}
		if s.Candidate.Priority != top {
			s.Reason = "lower priority"
			continue
		}
		if s.Candidate.Sell.Kind == SellUnknown {
			cheapestUnknown = min(cheapestUnknown, s.Cost)
		} else {
			hasPricedSell = true
		}
	}

	for i := range scores {
		s := &scores[i]
		if s.Reason != "" {
			continue
		}
		c := s.Candidate
		switch c.Sell.Kind {
		case SellKnown:
			s.PriceScore = 1 - clamp01(s.Cost/c.Sell.USD)
		case SellFree:
			if s.Cost == 0 {
				s.PriceScore = 1
			}
		default:
			// Margins and cost ratios are not comparable, so relative scoring
			// only runs when no candidate in the layer has a sell price.
			if hasPricedSell {
				s.Reason = "sell unknown"
				continue
			}
			switch {
			case s.Cost == 0:
				s.PriceScore = 1
			case cheapestUnknown > 0:
				s.PriceScore = cheapestUnknown / s.Cost
			}
		}
		s.Quality = clamp01(c.Quality)
		s.Service = effectiveRate(c.Submit, p.MinSamples) * effectiveRate(c.Gen, p.MinSamples) * clamp01(headroom(c))
		s.Total = wp*s.PriceScore + wq*s.Quality + ws*s.Service
	}

	sort.SliceStable(scores, func(i, j int) bool {
		a, b := scores[i], scores[j]
		if (a.Reason == "") != (b.Reason == "") {
			return a.Reason == ""
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		if a.Cost != b.Cost {
			return a.Cost < b.Cost
		}
		return a.Candidate.ID < b.Candidate.ID
	})
	return scores
}

// Select returns the best candidate and the full board. With TieEpsilon > 0
// and a non-nil rnd it draws among candidates within TieEpsilon of the best
// Total, weighted by Weight (all zero = equal). It never reserves capacity.
func Select(cands []Candidate, p Policy, rnd *rand.Rand) (*Candidate, []Score, error) {
	scores := Evaluate(cands, p)
	if len(scores) == 0 || scores[0].Reason != "" {
		return nil, scores, ErrNoCandidate
	}
	if rnd == nil || !(p.TieEpsilon > 0) || math.IsInf(p.TieEpsilon, 1) {
		return scores[0].Candidate, scores, nil
	}
	floor := scores[0].Total - p.TieEpsilon
	ties, totalWeight := 0, 0
	for ties < len(scores) && scores[ties].Reason == "" && scores[ties].Total >= floor {
		totalWeight += max(0, scores[ties].Candidate.Weight)
		ties++
	}
	if totalWeight == 0 {
		return scores[rnd.IntN(ties)].Candidate, scores, nil
	}
	pick := rnd.IntN(totalWeight)
	for _, s := range scores[:ties] {
		if pick -= max(0, s.Candidate.Weight); pick < 0 {
			return s.Candidate, scores, nil
		}
	}
	return scores[0].Candidate, scores, nil
}

// candidateReason applies the state and policy gates that do not need a quote.
func candidateReason(c *Candidate, p Policy, policyReason string) string {
	switch {
	case c.Excluded != "":
		return c.Excluded
	case policyReason != "":
		return policyReason
	case !isFinite(c.Submit.Rate) || !isFinite(c.Gen.Rate):
		return "invalid success rate"
	case c.Submit.Samples < 0 || c.Gen.Samples < 0:
		return "invalid sample count"
	case c.Capacity < 0 || c.GroupCapacity < 0:
		return "invalid capacity"
	case c.InFlight < 0 || c.GroupInFlight < 0:
		return "invalid in-flight count"
	case (c.Capacity > 0 && c.InFlight >= c.Capacity) || (c.GroupCapacity > 0 && c.GroupInFlight >= c.GroupCapacity):
		return "at capacity"
	case p.MinSubmitRate > 0 && c.Submit.Samples >= p.MinSamples && c.Submit.Rate < p.MinSubmitRate:
		return fmt.Sprintf("submit rate %.2f < %.2f", c.Submit.Rate, p.MinSubmitRate)
	case p.MinGenRate > 0 && c.Gen.Samples >= p.MinSamples && c.Gen.Rate < p.MinGenRate:
		return fmt.Sprintf("generation rate %.2f < %.2f", c.Gen.Rate, p.MinGenRate)
	case c.Sell.Kind == SellKnown && !(c.Sell.USD > 0 && !math.IsInf(c.Sell.USD, 1)):
		return "invalid sell price"
	case c.Sell.Kind == SellUnknown && p.UnknownSellPolicy != UnknownSellRelative:
		return "sell unknown"
	case c.Sell.Kind != SellKnown && c.Sell.Kind != SellFree && c.Sell.Kind != SellUnknown:
		return "invalid sell kind"
	}
	return ""
}

// normalizeWeights zeroes each invalid weight independently, falls back to
// DefaultWeights when all are zero, and scales by the max before summing so
// large finite weights cannot overflow.
func normalizeWeights(w Weights) (float64, float64, float64) {
	for _, weight := range []*float64{&w.Price, &w.Quality, &w.Service} {
		if !finiteNonNeg(*weight) {
			*weight = 0
		}
	}
	scale := max(w.Price, w.Quality, w.Service)
	if scale == 0 {
		w = DefaultWeights
		scale = max(w.Price, w.Quality, w.Service)
	}
	p, q, s := w.Price/scale, w.Quality/scale, w.Service/scale
	sum := p + q + s
	return p / sum, q / sum, s / sum
}

// effectiveRate counts an under-sampled metric as 1; Score.Unproven records it.
func effectiveRate(h HealthStat, minSamples int) float64 {
	if h.Samples < minSamples {
		return 1
	}
	return clamp01(h.Rate)
}

func headroom(c *Candidate) float64 {
	room := 1.0
	if c.Capacity > 0 {
		room = 1 - float64(c.InFlight)/float64(c.Capacity)
	}
	if c.GroupCapacity > 0 {
		room = min(room, 1-float64(c.GroupInFlight)/float64(c.GroupCapacity))
	}
	return room
}

func validRate(v float64) bool { return v >= 0 && v <= 1 }

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func finiteNonNeg(v float64) bool { return v >= 0 && !math.IsInf(v, 1) }

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return min(1, max(0, v))
}
