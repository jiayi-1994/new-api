package service

import (
	"cmp"
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/gin-gonic/gin"
)

// The normal board is ranked first. Probabilities only buy additional
// validation within that tier; without normal candidates validation is
// deterministic and may descend past tiers that have no admission slots.
func decideReliableVideoSchedule(input VideoDecisionInput) VideoScheduleChoice {
	rnd := rand.New(rand.NewPCG(input.Seed, 0))
	p := input.Policy
	p.Now = input.Now.Unix()
	normal, board, _ := videosched.Select(input.Candidates, p, rnd)
	hard := videosched.EvaluateProbe(input.Candidates, p)
	type validationCandidate struct {
		score   videosched.Score
		flow    string
		samples int64
	}
	var limited []validationCandidate
	reason := "hard_filter_exhausted"
	for i := range hard {
		s := &hard[i]
		if s.Reason != "" {
			if s.Reason == "health_state_unavailable" {
				reason = s.Reason
			}
			continue
		}
		c, flow := s.Candidate, ""
		h := c.Reliability
		if normal != nil && c.Priority != normal.Priority {
			continue
		}
		evidence, limit := h.Current, input.Explore.ExploreMaxInFlight
		switch h.State {
		case videosched.HealthUnverified:
			flow = "explore"
			if h.Reason == "evidence_expired" || h.Qualification != nil {
				flow = "revalidate"
			}
		case videosched.HealthNormal:
			reason := h.NormalReason(p, p.Now)
			if reason == "health qualification expired" || reason == "health samples insufficient" || reason == "health evidence incomplete" {
				flow = "revalidate"
			} else {
				continue
			}
		case videosched.HealthBlocked, videosched.HealthRecovering:
			flow, limit, evidence = "recover", input.Explore.ProbeMaxInFlight, h.Recovery
			if limit == 0 {
				s.Reason, reason = "recovery_disabled", "recovery_disabled"
				continue
			}
			last := max(h.BlockedAt, h.LastValidationAt)
			if p.Now-last < int64(VideoProbeCooldown(input.Explore.ProbeCooldownSec, VideoProbeState{ConsecutiveFails: h.ProbeFailures}).Seconds()) {
				s.Reason, reason = "recovery_cooldown", "recovery_cooldown"
				continue
			}
		default:
			s.Reason, reason = "health_state_unavailable", "health_state_unavailable"
			continue
		}
		if limit < 1 || limit > 16 || input.SlotOccupancy[c.ID] >= limit {
			s.Reason, reason = "validation_slots_full", "validation_slots_full"
			continue
		}
		samples := int64(0)
		if evidence != nil {
			samples = evidence.OverallSamples()
		}
		limited = append(limited, validationCandidate{score: *s, flow: flow, samples: samples})
	}
	slices.SortFunc(limited, func(a, b validationCandidate) int {
		if n := cmp.Compare(b.score.Candidate.Priority, a.score.Candidate.Priority); n != 0 {
			return n
		}
		if (a.flow == "recover") != (b.flow == "recover") {
			if a.flow == "recover" {
				return 1
			}
			return -1
		}
		if n := cmp.Compare(a.score.Candidate.Reliability.LastValidationAt, b.score.Candidate.Reliability.LastValidationAt); n != 0 {
			return n
		}
		if n := cmp.Compare(a.samples, b.samples); n != 0 {
			return n
		}
		if n := cmp.Compare(b.score.Candidate.Quality, a.score.Candidate.Quality); n != 0 {
			return n
		}
		if n := cmp.Compare(a.score.Quote.TotalUSD, b.score.Quote.TotalUSD); n != 0 {
			return n
		}
		return cmp.Compare(a.score.Candidate.ID, b.score.Candidate.ID)
	})
	var selected *validationCandidate
	selectionReason := "no_normal_candidate"
	if normal == nil && len(limited) > 0 {
		selected = &limited[0]
	}
	if normal != nil {
		selectionReason = "validation_budget"
		probeDraw, exploreDraw := rnd.Float64(), rnd.Float64()
		for i := range limited {
			if limited[i].flow == "recover" && probeDraw < input.Explore.ProbeRatio {
				selected = &limited[i]
				break
			}
		}
		if selected == nil {
			for i := range limited {
				if limited[i].flow != "recover" && exploreDraw < input.Explore.ExploreShare {
					selected = &limited[i]
					break
				}
			}
		}
	}
	if selected != nil {
		for i := range hard {
			if hard[i].Candidate.ID == selected.score.Candidate.ID {
				hard[i].Reason = ""
			} else if hard[i].Reason == "" {
				hard[i].Reason = "validation order"
			}
		}
		return VideoScheduleChoice{Best: selected.score.Candidate, Board: hard, Probe: selected.flow == "recover", Explore: selected.flow != "recover", Flow: selected.flow, Reason: selectionReason}
	}
	if normal != nil {
		return VideoScheduleChoice{Best: normal, Board: board, Flow: "normal"}
	}
	return VideoScheduleChoice{Board: hard, Reason: reason}
}

func videoValidationKeys(channelID int) []string {
	keys := make([]string, 16)
	for i := range keys {
		keys[i] = fmt.Sprintf("%svalidation:%d:%d", videoSchedKeyPrefix, channelID, i)
	}
	return keys
}

// All models and both validation types share the channel's slot namespace.
// Counting all 16 slots atomically also respects a lowered cap/type change.
func acquireVideoValidationSlot(c *gin.Context, channelID, limit int, ttl time.Duration) (bool, error) {
	if common.RedisEnabled && common.RDB == nil {
		return false, ErrVideoHealthAdmission
	}
	if limit < 1 || limit > 16 {
		return false, ErrVideoHealthAdmission
	}
	releaseVideoValidationLease(c)
	keys, token, index := videoValidationKeys(channelID), common.GetRandomString(24), -1
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		n, err := common.RDB.Eval(ctx, `local count=0; local free=0; for i,key in ipairs(KEYS) do if redis.call('EXISTS',key)==1 then count=count+1 elseif free==0 then free=i end end; if count>=tonumber(ARGV[1]) or free==0 then return 0 end; redis.call('SET',KEYS[free],ARGV[2],'PX',ARGV[3]); return free`, keys, limit, token, ttl.Milliseconds()).Int()
		if err != nil {
			return false, err
		}
		index = n - 1
	} else {
		m := memoryVideoHealth
		m.mu.Lock()
		count, now := 0, time.Now()
		for i, key := range keys {
			if slot, ok := m.slots[key]; ok && now.Before(slot.expires) {
				count++
			} else if index < 0 {
				index = i
			}
		}
		if count >= limit {
			index = -1
		}
		if index >= 0 {
			m.slots[keys[index]] = memoryVideoSlot{token: token, expires: now.Add(ttl)}
		}
		m.mu.Unlock()
	}
	if index < 0 {
		return false, nil
	}
	c.Set(videoValidationLeaseKey, &videoValidationLease{ChannelID: channelID, Key: keys[index], Token: token, Expires: time.Now().Add(ttl).Unix()})
	return true, nil
}

func admitVideoReliability(c *gin.Context, choice VideoScheduleChoice, input VideoDecisionInput, ttl time.Duration) (bool, error) {
	if choice.Best == nil || choice.Best.Reliability == nil {
		return false, ErrVideoHealthAdmission
	}
	channelID, version := choice.Best.ID, choice.Best.Reliability.StateVersion
	if choice.Flow != "normal" {
		limit := input.Explore.ExploreMaxInFlight
		if choice.Flow == "recover" {
			limit = input.Explore.ProbeMaxInFlight
		}
		ok, err := acquireVideoValidationSlot(c, channelID, limit, ttl)
		if err != nil || !ok {
			return ok, err
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
		defer cancel()
		version, err = model.StartVideoHealthValidation(ctx, channelID, choice.Best.Reliability.Model, version, input.Now.Unix(), input.Policy, choice.Flow)
		if err != nil {
			releaseVideoValidationLease(c)
			publishPersistedVideoReliability(ctx, channelID, choice.Best.Reliability.Model)
			return false, err
		}
	}
	c.Set(videoHealthAdmissionKey, videoHealthAdmission{ChannelID: channelID, Version: version, Flow: choice.Flow, Model: choice.Best.Reliability.Model, Identity: choice.Best.Reliability.ConfigIdentity})
	return true, nil
}
