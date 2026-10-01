package videosched

import (
	"math"
	"sort"
)

// evaluateStabilityCost keeps the admission gates identical for every flow.
// Validation bypasses only normal qualification and ranking; the host applies
// frozen state/cooldown/slot rules before choosing a validation priority tier.
func evaluateStabilityCost(cands []Candidate, p Policy, validation bool) []Score {
	policyValid := validRate(p.MinGenRate) && p.MinGenRate >= .8 && validRate(p.MinOverallRate) && p.MinOverallRate >= .6 &&
		validRate(p.MinMarginRate) && p.MinMarginRate < 1 && validRate(p.StabilityTolerance) && p.MinSamples > 0 && p.Now > 0 &&
		p.MaxCostUSD > 0 && isFinite(p.MaxCostUSD) && p.QualificationTTLSeconds > 0 && p.QualificationTTLSeconds <= 7*86400 && p.ValidationPeriodSeconds > 0 && p.ValidationPeriodSeconds <= 30*86400
	scores := make([]Score, len(cands))
	for i := range cands {
		c, s := &cands[i], &scores[i]
		s.Candidate = c
		switch {
		case c.Excluded != "":
			s.Reason = c.Excluded
		case !policyValid:
			s.Reason = "invalid policy"
		case c.Capacity < 0 || c.GroupCapacity < 0:
			s.Reason = "invalid capacity"
		case c.InFlight < 0 || c.GroupInFlight < 0:
			s.Reason = "invalid in-flight count"
		case c.Capacity > 0 && c.InFlight >= c.Capacity || c.GroupCapacity > 0 && c.GroupInFlight >= c.GroupCapacity:
			s.Reason = "at capacity"
		case !validRate(c.Quality):
			s.Reason = "invalid quality"
		case c.Sell.Kind != SellKnown:
			s.Reason = "positive sell price required"
		case !(c.Sell.USD > 0) || !isFinite(c.Sell.USD) || c.Sell.USD > p.MaxCostUSD:
			s.Reason = "invalid sell price"
		}
		if s.Reason != "" {
			continue
		}
		s.Quote = Quote(c.Cost, c.Spec)
		if s.Quote.Reason != "" {
			s.Reason = s.Quote.Reason
			continue
		}
		if s.Quote.TotalUSD > p.MaxCostUSD {
			s.Reason = "cost exceeds bound"
			continue
		}
		margin := (c.Sell.USD - s.Quote.TotalUSD) / c.Sell.USD
		s.EstimatedMargin = &margin
		// Compare the allowed cost directly: binary subtraction of 1-.9 must
		// not reject the exact 10% boundary. No tolerance accepts a real loss.
		if s.Quote.TotalUSD > c.Sell.USD*(1-p.MinMarginRate) {
			s.Reason = "margin below minimum"
			continue
		}
		s.Quality = c.Quality
		if c.Reliability == nil || c.Reliability.Validate() != nil || c.Reliability.Integrity == "unavailable" {
			s.Reason = "health_state_unavailable"
			continue
		}
		if q := c.Reliability.Qualification; q != nil && q.Validate() == nil {
			gen, overall := q.GenerationRate(), q.OverallRate()
			s.GenerationRate, s.OverallRate = &gen, &overall
		}
		if !validation {
			s.Reason = c.Reliability.NormalReason(p, p.Now)
		}
		s.Unproven = s.Reason != "" || c.Reliability.State != HealthNormal
	}
	if !validation {
		top, found, best := int64(0), false, 0.0
		for _, s := range scores {
			if s.Reason == "" && (!found || s.Candidate.Priority > top) {
				top, found = s.Candidate.Priority, true
			}
		}
		for i := range scores {
			s := &scores[i]
			if s.Reason != "" {
				continue
			}
			if s.Candidate.Priority != top {
				s.Reason = "lower priority"
				continue
			}
			best = max(best, *s.GenerationRate)
		}
		quality := -1.0
		for i := range scores {
			s := &scores[i]
			if s.Reason != "" {
				continue
			}
			s.BestGenerationRate = &best
			// Adding the tolerance avoids the 99%-98% subtraction rounding
			// above .01; every candidate is compared with the same best rate.
			if *s.GenerationRate+p.StabilityTolerance < best {
				s.Reason = "stability gap too large"
				continue
			}
			quality = max(quality, s.Candidate.Quality)
		}
		cheapest := math.Inf(1)
		for i := range scores {
			s := &scores[i]
			if s.Reason != "" {
				continue
			}
			if s.Candidate.Quality != quality {
				s.Reason = "lower quality tier"
				continue
			}
			cheapest = min(cheapest, s.Quote.TotalUSD)
		}
		for i := range scores {
			s := &scores[i]
			if s.Reason == "" && s.Quote.TotalUSD != cheapest {
				s.Reason = "higher cost at same quality"
			}
		}
	}
	sort.SliceStable(scores, func(i, j int) bool {
		a, b := scores[i], scores[j]
		if (a.Reason == "") != (b.Reason == "") {
			return a.Reason == ""
		}
		if a.Candidate.Priority != b.Candidate.Priority {
			return a.Candidate.Priority > b.Candidate.Priority
		}
		if a.Candidate.Quality != b.Candidate.Quality {
			return a.Candidate.Quality > b.Candidate.Quality
		}
		if a.Quote.TotalUSD != b.Quote.TotalUSD {
			return a.Quote.TotalUSD < b.Quote.TotalUSD
		}
		return a.Candidate.ID < b.Candidate.ID
	})
	return scores
}
