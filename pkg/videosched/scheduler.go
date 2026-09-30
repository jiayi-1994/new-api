// Package videosched scores video-generation candidate channels by margin,
// operator quality and service health. It is pure: no database, gin, Redis or
// plugin dependencies. Callers assemble one Candidate per upstream channel,
// each carrying the Spec its own execution plugin decoded, and pass a snapshot.
package videosched

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
)

// Spec seconds kinds.
const (
	KindExact = "exact"
	KindFixed = "fixed" // the request cannot change it; channel constraints still apply
)

// Base cost modes.
const (
	ModePerVideo  = "per_video"
	ModePerSecond = "per_second"
)

// Reference media rule modes. Each kind and tier selects exactly one rule.
const (
	RefUnsupported     = "unsupported"
	RefIncluded        = "included"
	RefPerRequest      = "per_request"       // Value USD once per request
	RefPerInput        = "per_input"         // Value USD per submitted item of the kind
	RefPerOutputSecond = "per_output_second" // Value USD per output second
	RefMultiplier      = "multiplier"        // adds base × (Value − 1); Value ≥ 1
)

// ReferenceKinds is the fixed order in which reference lines are priced and
// reported, so the first failure never depends on map iteration.
var ReferenceKinds = []string{"video", "image", "audio"}

// Sell price kinds and unknown-sell policies.
const (
	SellKnown   = "known"
	SellFree    = "free"
	SellUnknown = "unknown"

	UnknownSellExclude  = "exclude"
	UnknownSellRelative = "relative"
)

// Spec is the validated request shape one candidate's plugin will submit.
type Spec struct {
	OutputSeconds *float64 // nil = unknown, distinct from zero
	SecondsKind   string   // exact|fixed
	Tier          string   // lowercase resolution or product key; "*" = the model has no tiers; "" = unknown
	References    map[string]int
	Missing       []string
}

// ReferenceCost is one reference media rule. Value distinguishes a missing
// price (nil) from an explicit zero.
type ReferenceCost struct {
	Mode  string
	Value *float64
}

// CostConfig is one channel's purchase price table for one model, in USD. The
// base price excludes reference surcharges. An absent References kind or tier
// is unpriced, never included.
type CostConfig struct {
	Mode           string
	Prices         map[string]float64 // tier -> USD; "*" matches any tier
	MinSeconds     int
	MaxSeconds     int
	AllowedSeconds []int
	References     map[string]map[string]ReferenceCost // kind -> tier|"*" -> rule
}

// CostQuote is the purchase cost of one spec. A non-empty Reason invalidates
// the whole quote: its amounts are zero and must not be scored. All-zero
// amounts with an empty Reason are a valid free quote.
type CostQuote struct {
	Tier                            string // Prices key that priced the base
	Reason                          string
	BaseUSD, ReferenceUSD, TotalUSD float64
	References                      []ReferenceLine // one per kind with a positive count, in ReferenceKinds order
}

type ReferenceLine struct {
	Kind, Mode, Tier string   // Tier is the rule key matched, which may differ from the base tier
	Quantity         *float64 // 1 per request, item count or output seconds; nil for multipliers
	Value            *float64 // unit price or multiplier; nil when included
	USD              float64
	Reason           string
}

type HealthStat struct {
	Rate    float64 `json:"rate"`
	Samples int     `json:"samples"`
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

	Submit, Gen HealthStat // raw; health gates are applied here, not during assembly
	Sell        SellPrice
	Excluded    string // hard exclusion from assembly (disabled, tried, not schedulable, ...); never gated or at capacity
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
	MaxCostUSD         float64 // purchase cost bound frozen by the host; must be finite and > 0
	TieEpsilon         float64 // 0 = take the first; >0 = weighted draw among near-ties
}

type Score struct {
	Candidate  *Candidate
	Quote      CostQuote // the quote every price comparison of this score used
	PriceScore float64
	Quality    float64
	Service    float64
	Total      float64
	Unproven   bool
	Reason     string // non-empty = not eligible
}

var ErrNoCandidate = errors.New("videosched: no eligible candidate")

// Quote prices spec against a channel's cost table: the base cost plus one
// surcharge per reference kind the request carries.
func Quote(cost CostConfig, spec Spec) CostQuote {
	q := quoteBase(cost, spec)
	if q.Reason != "" {
		return q
	}
	for _, kind := range ReferenceKinds {
		n := spec.References[kind]
		if n == 0 {
			continue
		}
		line := quoteReference(kind, n, cost.References[kind], spec, q.BaseUSD)
		q.References = append(q.References, line)
		if q.Reason != "" {
			continue
		}
		q.Reason = line.Reason
		q.ReferenceUSD += line.USD
		if q.Reason == "" && !finiteNonNeg(q.ReferenceUSD) {
			q.Reason = "invalid total price"
		}
	}
	q.TotalUSD = q.BaseUSD + q.ReferenceUSD
	if q.Reason == "" && !finiteNonNeg(q.TotalUSD) {
		q.Reason = "invalid total price"
	}
	if q.Reason != "" {
		q.BaseUSD, q.ReferenceUSD, q.TotalUSD = 0, 0, 0
	}
	return q
}

// quoteBase prices the base tier and checks the seconds constraints.
func quoteBase(cost CostConfig, spec Spec) CostQuote {
	if cost.Mode != ModePerVideo && cost.Mode != ModePerSecond {
		return CostQuote{Reason: "invalid billing mode"}
	}
	if len(cost.Prices) == 0 {
		return CostQuote{Reason: "no price configured"}
	}

	tier := strings.ToLower(strings.TrimSpace(spec.Tier))
	key := tier
	base, ok := cost.Prices[key]
	switch {
	case tier == "":
		// Never guess the cheapest tier: only a lone wildcard price quotes blind.
		key = "*"
		base, ok = cost.Prices[key]
		if !ok || len(cost.Prices) != 1 {
			return CostQuote{Reason: "tier unknown"}
		}
	case !ok:
		key = "*"
		if base, ok = cost.Prices[key]; !ok {
			return CostQuote{Reason: "tier " + tier + " not priced"}
		}
	}
	if !finiteNonNeg(base) {
		return CostQuote{Reason: "invalid price"}
	}

	constrained := cost.MinSeconds > 0 || cost.MaxSeconds > 0 || len(cost.AllowedSeconds) > 0
	quantity := 1.0
	if spec.OutputSeconds == nil {
		if cost.Mode == ModePerSecond || constrained {
			return CostQuote{Reason: "seconds unknown"}
		}
	} else {
		seconds := *spec.OutputSeconds
		if !(seconds > 0) || math.IsInf(seconds, 0) { // also rejects NaN
			return CostQuote{Reason: "invalid seconds"}
		}
		if (cost.MinSeconds > 0 && seconds < float64(cost.MinSeconds)) || (cost.MaxSeconds > 0 && seconds > float64(cost.MaxSeconds)) {
			return CostQuote{Reason: fmt.Sprintf("seconds %g out of range", seconds)}
		}
		if len(cost.AllowedSeconds) > 0 && !slices.ContainsFunc(cost.AllowedSeconds, func(allowed int) bool { return float64(allowed) == seconds }) {
			return CostQuote{Reason: fmt.Sprintf("seconds %g not allowed", seconds)}
		}
		if cost.Mode == ModePerSecond {
			quantity = seconds
		}
	}

	usd, ok := product(base, quantity)
	if !ok {
		return CostQuote{Reason: "invalid total price"}
	}
	return CostQuote{Tier: key, BaseUSD: usd}
}

// quoteReference prices one reference kind carrying n items. The rule is
// matched on the request tier and falls back to an explicit "*"; the tier the
// base price hit never hides a tier-specific surcharge.
func quoteReference(kind string, n int, rules map[string]ReferenceCost, spec Spec, base float64) ReferenceLine {
	line := ReferenceLine{Kind: kind}
	if n < 0 {
		line.Reason = "invalid reference " + kind + " count"
		return line
	}
	tier := strings.ToLower(strings.TrimSpace(spec.Tier))
	rule, ok := rules[tier]
	if tier == "" || !ok {
		tier = "*"
		rule, ok = rules[tier]
	}
	if !ok {
		line.Reason = "reference " + kind + " not priced"
		return line
	}
	line.Mode, line.Tier = rule.Mode, tier

	quantity := 0.0
	switch rule.Mode {
	case RefUnsupported:
		line.Reason = "reference " + kind + " unsupported"
		return line
	case RefIncluded:
		return line
	case RefPerRequest:
		quantity = 1
	case RefPerInput:
		quantity = float64(n)
	case RefPerOutputSecond:
		if spec.OutputSeconds == nil {
			line.Reason = "seconds unknown"
			return line
		}
		quantity = *spec.OutputSeconds
	case RefMultiplier:
	default:
		line.Reason = "invalid reference " + kind + " rule"
		return line
	}
	if rule.Value == nil {
		line.Reason = "invalid reference " + kind + " price"
		return line
	}
	value := *rule.Value
	line.Value = &value

	multiplier := rule.Mode == RefMultiplier
	if (multiplier && !(value >= 1)) || !finiteNonNeg(value) {
		line.Reason = "invalid reference " + kind + " price"
		return line
	}
	a, b := value, quantity
	if multiplier {
		a, b = base, value-1
	} else {
		line.Quantity = &quantity
	}
	usd, ok := product(a, b)
	if !ok {
		line.Reason = "invalid total price"
		return line
	}
	line.USD = usd
	return line
}

// product multiplies two non-negative amounts, rejecting overflow and a
// positive product that underflows to a free zero.
func product(a, b float64) (float64, bool) {
	p := a * b
	return p, finiteNonNeg(p) && !(a > 0 && b > 0 && p == 0)
}

// Evaluate scores every candidate and returns them sorted: eligible first,
// then Total desc, TotalUSD asc, ID asc. Only the highest Priority layer holding
// an eligible candidate is ranked; eligible candidates below it get the reason
// "lower priority". Evaluate does not modify cands.
func Evaluate(cands []Candidate, p Policy) []Score {
	return evaluate(cands, p, false)
}

// EvaluateProbe scores the same snapshot for a recovery probe: it only skips
// the health gates, so a gated candidate gets its real score. Hard exclusions,
// capacity, the quote, the cost bound, the sell policy and the loss threshold
// all still apply.
func EvaluateProbe(cands []Candidate, p Policy) []Score {
	return evaluate(cands, p, true)
}

func evaluate(cands []Candidate, p Policy, relaxHealth bool) []Score {
	wp, wq, ws := normalizeWeights(p.Weights)
	policyReason := ""
	switch {
	case !validRate(p.MinSubmitRate) || !validRate(p.MinGenRate):
		policyReason = "invalid minimum success rate"
	case p.MinSamples < 0:
		policyReason = "invalid minimum samples"
	case !finiteNonNeg(p.MaxCostToSellRatio):
		policyReason = "invalid loss threshold"
	case !(p.MaxCostUSD > 0) || math.IsInf(p.MaxCostUSD, 1):
		policyReason = "invalid policy"
	}

	scores := make([]Score, len(cands))
	for i := range cands {
		c := &cands[i]
		s := &scores[i]
		s.Candidate = c
		if s.Reason = candidateReason(c, p, policyReason, relaxHealth); s.Reason != "" {
			continue
		}
		if s.Quote = Quote(c.Cost, c.Spec); s.Quote.Reason != "" {
			s.Reason = s.Quote.Reason
			continue
		}
		cost := s.Quote.TotalUSD
		if cost > p.MaxCostUSD {
			s.Reason = "cost exceeds bound"
			continue
		}
		s.Unproven = c.Submit.Samples < p.MinSamples || c.Gen.Samples < p.MinSamples
		if p.MaxCostToSellRatio > 0 && c.Sell.Kind != SellUnknown {
			ratio := 0.0
			switch {
			case c.Sell.Kind == SellKnown:
				ratio = cost / c.Sell.USD
			case cost > 0: // a paid channel against a free sell price always loses
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
			cheapestUnknown = min(cheapestUnknown, s.Quote.TotalUSD)
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
		cost := s.Quote.TotalUSD
		switch c.Sell.Kind {
		case SellKnown:
			s.PriceScore = 1 - clamp01(cost/c.Sell.USD)
		case SellFree:
			if cost == 0 {
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
			case cost == 0:
				s.PriceScore = 1
			case cheapestUnknown > 0:
				s.PriceScore = cheapestUnknown / cost
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
		if a.Quote.TotalUSD != b.Quote.TotalUSD {
			return a.Quote.TotalUSD < b.Quote.TotalUSD
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
// relaxHealth skips only the two health gates.
func candidateReason(c *Candidate, p Policy, policyReason string, relaxHealth bool) string {
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
	case !relaxHealth && p.MinSubmitRate > 0 && c.Submit.Samples >= p.MinSamples && c.Submit.Rate < p.MinSubmitRate:
		return fmt.Sprintf("submit rate %.2f < %.2f", c.Submit.Rate, p.MinSubmitRate)
	case !relaxHealth && p.MinGenRate > 0 && c.Gen.Samples >= p.MinSamples && c.Gen.Rate < p.MinGenRate:
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
