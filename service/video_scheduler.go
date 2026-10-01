package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	videospec "github.com/QuantumNous/new-api/pkg/videosched/spec"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// Reasons a request is neither taken over nor shadowed by video scheduling.
const (
	VideoSchedReasonModeOff        = "mode_off"
	VideoSchedReasonNotSubmit      = "not_submit"
	VideoSchedReasonModelNotListed = "model_not_listed"
	VideoSchedReasonMixedPool      = "mixed_pool"
	// Mode on without its runtime prerequisites (memory cache, Redis when
	// multi-instance, a first in-flight calibration) keeps ordinary selection.
	VideoSchedReasonNotReady = "not_ready"
)

// videoSchedSpecHook is the optional plugin export that makes a plugin
// schedulable.
const videoSchedSpecHook = "describeSpec"

// VideoSchedDecision says whether video scheduling applies to one request. It
// is decided once when the task entry is prepared and frozen in the request
// context, so every later reader (affinity, retry budget, selector, AutoBan)
// sees the same answer even if the global mode changes mid-request.
type VideoSchedDecision struct {
	Takeover bool   `json:"takeover"`         // mode on: the scheduler chooses the channel
	Shadow   bool   `json:"shadow"`           // mode shadow: the scheduler only scores and logs
	Reason   string `json:"reason,omitempty"` // why neither applies; empty when one does
}

// DecideVideoSched freezes the decision for a prepared task submit request.
// Takeover (mode on, with its runtime prerequisites met) and Shadow (mode
// shadow) both require a listed model
// (an empty allow list lists every model), a fresh submission rather than a
// continuation of an origin task, and a describeSpec hook on every accepted
// candidate plugin. A single hook-less candidate keeps the whole request on
// ordinary channel selection, in every group.
func DecideVideoSched(c *gin.Context) (decision VideoSchedDecision) {
	setting := operation_setting.GetVideoSchedulingSetting()
	if snapshot, ok := common.GetContextKeyType[*operation_setting.VideoSchedulingSetting](c, constant.ContextKeyVideoSchedSetting); ok && snapshot != nil {
		setting = snapshot
	}
	auditEnabled := setting.AuditEnabled
	defer func() {
		common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
		freezeVideoScheduleAudit(c, decision, auditEnabled)
	}()
	if setting.Mode != operation_setting.VideoSchedulingModeOn && setting.Mode != operation_setting.VideoSchedulingModeShadow {
		return VideoSchedDecision{Reason: VideoSchedReasonModeOff}
	}
	if tasks, ok := common.GetContextKeyType[[]*model.Task](c, constant.ContextKeyOriginTasks); ok && len(tasks) > 0 {
		return VideoSchedDecision{Reason: VideoSchedReasonNotSubmit}
	}
	modelName := c.GetString("resolved_task_model")
	if len(setting.Models) > 0 && !slices.Contains(setting.Models, modelName) {
		return VideoSchedDecision{Reason: VideoSchedReasonModelNotListed}
	}
	// A saved mode can bypass option validation (startup load), so the
	// prerequisites are checked again per request. Both modes read candidates
	// from the memory cache; only on also needs calibrated in-flight gauges.
	if err := operation_setting.ValidateVideoSchedulingOption("video_scheduling_setting.mode", setting.Mode); err != nil ||
		(setting.Mode == operation_setting.VideoSchedulingModeOn && !VideoHealthReady()) {
		return VideoSchedDecision{Reason: VideoSchedReasonNotReady}
	}

	var plugins []*jsplugin.LoadedPlugin
	if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		pinned, _ := value.(jsplugin.PinnedEndpoint)
		for _, candidate := range pinned.Candidates {
			plugins = append(plugins, candidate.Plugin)
		}
	} else if value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin); exists {
		pinned, _ := value.(jsplugin.PinnedPlugin)
		plugins = append(plugins, pinned.Plugin)
	}
	keys := make([]string, 0, len(plugins))
	var blockers []string
	for _, plugin := range plugins {
		if plugin == nil {
			blockers = append(blockers, "<nil>")
			continue
		}
		keys = append(keys, plugin.Meta.Key)
		if callable, err := plugin.Engine.HasCallableHook(context.WithoutCancel(c.Request.Context()), videoSchedSpecHook); err != nil || !callable {
			blockers = append(blockers, plugin.Meta.Key)
		}
	}
	if len(plugins) == 0 || len(blockers) > 0 {
		logger.LogWarn(c, "video scheduling skipped: mixed pool model=%q accepted=%q without_describe_spec=%q",
			modelName, strings.Join(keys, ","), strings.Join(blockers, ","))
		return VideoSchedDecision{Reason: VideoSchedReasonMixedPool}
	}
	if setting.Mode == operation_setting.VideoSchedulingModeOn {
		return VideoSchedDecision{Takeover: true}
	}
	return VideoSchedDecision{Shadow: true}
}

// VideoSchedDecisionFrom returns the request's frozen decision, or the zero
// decision (neither Takeover nor Shadow) when none was made: entries other
// than the protocol and native task entries never schedule.
func VideoSchedDecisionFrom(c *gin.Context) VideoSchedDecision {
	if c == nil {
		return VideoSchedDecision{}
	}
	decision, _ := common.GetContextKeyType[VideoSchedDecision](c, constant.ContextKeyVideoSchedDecision)
	return decision
}

// VideoSchedStaticBlockers lists the plugins that declare modelName without a
// describeSpec hook. Any of them may join a request's accepted candidates and
// make it a mixed pool; whether one does depends on the request (protocol and
// whether its decoder accepts the body), so this is the conservative view.
func VideoSchedStaticBlockers(ctx context.Context, generation *jsplugin.RoutingGeneration, modelName string) []string {
	var blockers []string
	for _, plugin := range generation.PluginsByModel(modelName) {
		if callable, err := plugin.Engine.HasCallableHook(ctx, videoSchedSpecHook); err != nil || !callable {
			blockers = append(blockers, plugin.Meta.Key)
		}
	}
	return blockers
}

// videoSchedBlockerWarning describes the static blockers of every scheduled
// model, or "" when there are none. An empty allow list schedules every model
// of a plugin that exports describeSpec.
func videoSchedBlockerWarning(ctx context.Context, generation *jsplugin.RoutingGeneration, allowList []string) string {
	models := allowList
	if len(models) == 0 {
		for _, plugin := range generation.Plugins() {
			if callable, err := plugin.Engine.HasCallableHook(ctx, videoSchedSpecHook); err != nil || !callable {
				continue
			}
			for _, name := range plugin.Meta.Models {
				if !slices.Contains(models, name) {
					models = append(models, name)
				}
			}
		}
	}
	var warning strings.Builder
	for _, name := range models {
		if blockers := VideoSchedStaticBlockers(ctx, generation, name); len(blockers) > 0 {
			fmt.Fprintf(&warning, " %s=[%s]", name, strings.Join(blockers, ","))
		}
	}
	if warning.Len() == 0 {
		return ""
	}
	return "video scheduling: plugins without describeSpec also declare scheduled models, requests they accept keep ordinary selection:" + warning.String()
}

const (
	contextKeyVideoSpecCache     = "video_sched_spec_cache"
	contextKeyVideoSummaryLogged = "video_sched_summary_logged"
	videoSchedProbeRounds        = 3
	// VideoSchedAdmissionProbeTaken marks a probe choice whose slot another
	// request took first; a new selection follows it.
	VideoSchedAdmissionProbeTaken = "probe_slot_taken"
	// VideoSchedAdmissionProbeError marks a probe choice whose slot could not
	// be claimed because the store failed; the selection is decided again
	// without probing.
	VideoSchedAdmissionProbeError = "probe_slot_error"
	// VideoSchedAdmissionChannelUnavailable marks a choice whose channel was
	// disabled or left the request's group/model while it was scored; a new
	// selection follows it.
	VideoSchedAdmissionChannelUnavailable = "channel_unavailable"
)

func init() {
	model.TierSelector = selectVideoChannel
}

// acquireVideoProbeSlot is AcquireVideoProbeSlot and recheckVideoChannel is
// model.CacheGetSatisfiedChannel; tests replace them to lose the races between
// a decision and its admission.
var (
	acquireVideoProbeSlot = AcquireVideoProbeSlot
	recheckVideoChannel   = model.CacheGetSatisfiedChannel
)

// VideoExploreSettings are the probe and explore parameters in effect for one
// decision. They are host concepts, so they stay out of videosched.Policy.
// ProbeMaxInFlight 0 disables probing; ExploreMaxInFlight 0 leaves unproven
// channels uncapped (a literal zero cap would never let a new channel in).
type VideoExploreSettings struct {
	ProbeRatio         float64
	ProbeCooldownSec   int
	ProbeMaxInFlight   int
	ExploreShare       float64
	ExploreMaxInFlight int
}

// VideoDecisionInput is everything one scheduling decision reads, frozen
// before the decision runs: the assembled candidates with their hard
// exclusions (gating and capacity are left to the algorithm), the policy
// actually passed to videosched, the probe and explore settings, per-channel
// probe state, probe slot occupancy, the clock and the seed. The decision
// itself reads nothing else (request policy, settings, Redis).
type VideoDecisionInput struct {
	Candidates []videosched.Candidate
	Policy     videosched.Policy
	Explore    VideoExploreSettings
	Probe      map[int]VideoProbeState
	// SlotOccupancy is the probe slots held per channel, apart from in-flight
	// counts: a slot is held from acquisition while the task is only counted
	// once inserted. Channels holding none are absent.
	SlotOccupancy map[int]int
	Now           time.Time // UTC, whole seconds
	Seed          uint64
}

// VideoScheduleChoice is the outcome of DecideVideoSchedule.
type VideoScheduleChoice struct {
	Best    *videosched.Candidate // nil when no candidate is eligible
	Board   []videosched.Score    // the board the choice was made on
	Probe   bool                  // Best is a gated channel drawn for a recovery probe
	Explore bool                  // Best is an unproven channel drawn for exploration
}

// DecideVideoSchedule makes the scheduling choice from input alone, so live
// traffic, shadow scoring and the simulator agree on the same input. Draws
// come from one generator seeded by input.Seed in a fixed order (probe,
// explore, then the tie-break of videosched.Select). It never acquires a probe
// slot.
//
// Probe: a channel failing a health gate on enough samples, whose cooldown has
// elapsed and that has a free probe slot, is scored with the gates relaxed
// (EvaluateProbe, which keeps its top priority layer); with ProbeRatio the best
// of them is chosen. Explore: an unproven channel at ExploreMaxInFlight is out.
// When the top layer also holds proven channels, with ExploreShare the unproven
// channel with the fewest submit+generation samples is chosen (ties by score),
// and otherwise unproven channels are left out of the argmax.
func DecideVideoSchedule(input VideoDecisionInput) VideoScheduleChoice {
	rnd := rand.New(rand.NewPCG(input.Seed, 0))
	policy, explore := input.Policy, input.Explore

	probeBoard := videosched.EvaluateProbe(input.Candidates, policy)
	var probe *videosched.Candidate
	for _, score := range probeBoard {
		if score.Reason != "" {
			break // eligible scores come first
		}
		candidate := score.Candidate
		state := input.Probe[candidate.ID]
		if videoGated(candidate.Submit, candidate.Gen, policy.MinSubmitRate, policy.MinGenRate, policy.MinSamples) && input.SlotOccupancy[candidate.ID] < explore.ProbeMaxInFlight &&
			input.Now.Sub(time.Unix(state.LastProbeAt, 0)) >= VideoProbeCooldown(explore.ProbeCooldownSec, state) {
			probe = candidate
			break
		}
	}
	if probe != nil && rnd.Float64() < explore.ProbeRatio {
		return VideoScheduleChoice{Best: probe, Board: probeBoard, Probe: true}
	}

	candidates := slices.Clone(input.Candidates)
	if explore.ExploreMaxInFlight > 0 {
		for i := range candidates {
			candidate := &candidates[i]
			unproven := candidate.Submit.Samples < policy.MinSamples || candidate.Gen.Samples < policy.MinSamples
			if candidate.Excluded == "" && unproven && candidate.InFlight >= explore.ExploreMaxInFlight {
				candidate.Excluded = "explore limit"
			}
		}
	}
	board := videosched.Evaluate(candidates, policy)
	proven := false
	var unproven []videosched.Score
	for _, score := range board {
		if score.Reason != "" {
			break
		}
		if score.Unproven {
			unproven = append(unproven, score)
		} else {
			proven = true
		}
	}
	if proven && len(unproven) > 0 {
		if rnd.Float64() < explore.ExploreShare {
			pick := slices.MinFunc(unproven, func(a, b videosched.Score) int {
				return (a.Candidate.Submit.Samples + a.Candidate.Gen.Samples) - (b.Candidate.Submit.Samples + b.Candidate.Gen.Samples)
			})
			return VideoScheduleChoice{Best: pick.Candidate, Board: board, Explore: true}
		}
		for _, score := range unproven {
			score.Candidate.Excluded = "unproven" // points into candidates
		}
	}
	best, board, _ := videosched.Select(candidates, policy, rnd)
	return VideoScheduleChoice{Best: best, Board: board}
}

// VideoDecisionFingerprint returns the sha256 of the canonical JSON of the
// whole input and of each segment (candidates, settings, probe, slots, now,
// seed). common.Marshal sorts map keys and prints numbers exactly, which is
// what makes the JSON canonical.
func VideoDecisionFingerprint(input VideoDecisionInput) (string, map[string]string, error) {
	segments := map[string]any{
		"candidates": input.Candidates,
		"settings":   map[string]any{"policy": input.Policy, "explore": input.Explore},
		"probe":      input.Probe,
		"slots":      input.SlotOccupancy,
		"now":        input.Now,
		"seed":       input.Seed,
		"":           input,
	}
	hashes := make(map[string]string, len(segments))
	for name, value := range segments {
		data, err := common.Marshal(value)
		if err != nil {
			return "", nil, err
		}
		hashes[name] = hex.EncodeToString(common.Sha256Raw(data))
	}
	whole := hashes[""]
	delete(hashes, "")
	return whole, hashes, nil
}

// selectVideoChannel is model.TierSelector: it chooses among the satisfied
// channels of a request that video scheduling took over, ignoring the legacy
// priority retry index. Every other request keeps ordinary selection.
func selectVideoChannel(c *gin.Context, group, modelName string, filters []taskdto.ChannelFilter) (*model.Channel, error) {
	if !VideoSchedDecisionFrom(c).Takeover {
		return nil, model.ErrTierSelectorNotApplicable
	}
	// Slot TTL only backstops a lost release, so it covers the longest task.
	probeTTL := time.Duration(constant.TaskTimeoutMinutes) * time.Minute
	if probeTTL <= 0 {
		probeTTL = videoSchedProbeCooldownMax
	}
	// A probe that loses its slot to a concurrent request, or a choice whose
	// channel was disabled or regrouped while it was scored, is decided again
	// on a fresh snapshot under a new selection_seq, never silently under the
	// old fingerprint. A slot store that fails to write turns probing off for
	// the rest of the selection instead of costing the request its channel.
	// The bound only guards against a store that keeps failing.
	probeOff := false
	for range videoSchedProbeRounds {
		channels, err := model.SatisfiedChannelSnapshot(group, modelName, filters)
		if err != nil {
			recordVideoAuditAssemblyError(c)
			return nil, err
		}
		input := AssembleVideoDecision(c, operation_setting.GetVideoSchedulingSetting(), group, modelName, channels, rand.Uint64())
		if probeOff {
			input.Explore.ProbeMaxInFlight = 0
		}
		choice := DecideVideoSchedule(input)
		record := VideoScheduleRecord{Mode: operation_setting.VideoSchedulingModeOn, Group: group, Probe: choice.Probe, Explore: choice.Explore, Input: &input}
		record.Fingerprint, _, _ = VideoDecisionFingerprint(input)
		if choice.Best == nil {
			appendVideoScheduleRecord(c, record, choice.Board)
			return nil, model.ErrTierSelectorNoCandidate
		}
		record.Recommended = choice.Best.ID
		// Scoring ran without the cache lock (plugins, Redis), so the choice is
		// checked against the live cache before it is admitted. The probe slot
		// is claimed only afterwards: claiming starts the probe cooldown.
		selected, available := recheckVideoChannel(group, modelName, filters, choice.Best.ID)
		if !available {
			record.Admission = VideoSchedAdmissionChannelUnavailable
			appendVideoScheduleRecord(c, record, choice.Board)
			continue
		}
		if choice.Probe {
			acquired := false
			for n := range input.Explore.ProbeMaxInFlight {
				acquired, err = acquireVideoProbeSlot(c, choice.Best.ID, n, probeTTL)
				if err != nil || acquired {
					break
				}
			}
			if err != nil {
				logger.LogWarn(c, "video scheduling probe slot acquisition failed: channel=%d error=%v", choice.Best.ID, err)
				record.Admission = VideoSchedAdmissionProbeError
				appendVideoScheduleRecord(c, record, choice.Board)
				probeOff = true
				continue
			}
			if !acquired {
				record.Admission = VideoSchedAdmissionProbeTaken
				appendVideoScheduleRecord(c, record, choice.Board)
				continue
			}
		}
		appendVideoScheduleRecord(c, record, choice.Board)
		return selected, nil
	}
	return nil, model.ErrTierSelectorNoCandidate
}

// ShadowObserveVideoSched scores a shadow request's candidates after ordinary
// selection chose selected, and only records and logs the recommendation. It
// acquires no probe slot and changes no selection or auto-group state.
func ShadowObserveVideoSched(c *gin.Context, group, modelName string, selected *model.Channel, affinityHit bool) {
	if selected == nil || !VideoSchedDecisionFrom(c).Shadow {
		return
	}
	channels, err := model.SatisfiedChannelSnapshot(group, modelName, GetChannelConstraints(c).Filters)
	if err != nil {
		logger.LogWarn(c, "video scheduling shadow skipped: %v", err)
		recordVideoAuditAssemblyError(c)
		return
	}
	// Seeded per request so probe and explore draws sample their ratios across
	// requests, without drawing from the global random source.
	seed := uint64(RequestPolicy(c).StartedAt.UnixNano())
	input := AssembleVideoDecision(c, operation_setting.GetVideoSchedulingSetting(), group, modelName, channels, seed)
	choice := DecideVideoSchedule(input)
	record := VideoScheduleRecord{Mode: operation_setting.VideoSchedulingModeShadow, Group: group, Selected: selected.Id, AffinityHit: affinityHit, Probe: choice.Probe, Explore: choice.Explore, Input: &input}
	record.Fingerprint, _, _ = VideoDecisionFingerprint(input)
	if choice.Best != nil {
		record.Recommended = choice.Best.ID
	}
	appendVideoScheduleRecord(c, record, choice.Board)
	logger.LogInfo(c, fmt.Sprintf("video scheduling shadow: group=%q model=%q selected=%d recommended=%d affinity_hit=%t",
		group, modelName, selected.Id, record.Recommended, affinityHit))
}

// AssembleVideoDecision freezes the decision input for channels, the
// satisfied channels of modelName in group (auto already expanded), under
// setting. Channels this request already attempted are excluded as tried.
// It reads health, in-flight and probe slot state but writes none.
func AssembleVideoDecision(c *gin.Context, setting *operation_setting.VideoSchedulingSetting, group, modelName string, channels []*model.Channel, seed uint64) VideoDecisionInput {
	maxCost, err := operation_setting.VideoSchedMaxCostUSD()
	if err != nil {
		// A zero bound invalidates the policy, so every candidate is excluded.
		logger.LogWarn(c, "video scheduling: %v", err)
	}
	input := VideoDecisionInput{
		Policy: videosched.Policy{
			Weights:            videosched.Weights{Price: setting.PriceWeight, Quality: setting.QualityWeight, Service: setting.ServiceWeight},
			MinSubmitRate:      setting.MinSubmitRate,
			MinGenRate:         setting.MinGenRate,
			MinSamples:         setting.MinSamples,
			UnknownSellPolicy:  setting.UnknownSellPolicy,
			MaxCostToSellRatio: setting.MaxCostToSellRatio,
			MaxCostUSD:         maxCost,
			TieEpsilon:         setting.TieEpsilon,
		},
		Explore: VideoExploreSettings{
			ProbeRatio:         setting.ProbeRatio,
			ProbeCooldownSec:   setting.ProbeCooldownSec,
			ProbeMaxInFlight:   setting.ProbeMaxInFlight,
			ExploreShare:       setting.ExploreShare,
			ExploreMaxInFlight: setting.ExploreMaxInFlight,
		},
		Probe:         make(map[int]VideoProbeState, len(channels)),
		SlotOccupancy: map[int]int{},
		Now:           time.Now().UTC().Truncate(time.Second),
		Seed:          seed,
	}
	tried := make(map[int]bool)
	for _, event := range RequestPolicy(c).Events() {
		if event.Decision.Action == "attempt" {
			tried[event.ChannelID] = true
		}
	}
	for _, channel := range channels {
		candidate, probe := assembleVideoCandidate(c, group, modelName, channel, tried[channel.Id], setting)
		input.Candidates = append(input.Candidates, candidate)
		input.Probe[channel.Id] = probe
		if candidate.Excluded != "" || setting.ProbeMaxInFlight <= 0 {
			continue
		}
		held, err := VideoProbeSlotsHeld(channel.Id, setting.ProbeMaxInFlight)
		if err != nil {
			// An unreadable slot is treated as held: never probe blind.
			logger.LogWarn(c, "video scheduling probe slot read failed: channel=%d error=%v", channel.Id, err)
			held = setting.ProbeMaxInFlight
		}
		if held > 0 {
			input.SlotOccupancy[channel.Id] = held
		}
	}
	return input
}

// assembleVideoCandidate snapshots one channel: its execution plugin and
// mapped model, the spec and sell price that plugin derives, the channel's cost
// table, health and in-flight counts. Only hard exclusions are written into
// Excluded; health gates and capacity are judged by the algorithm.
func assembleVideoCandidate(c *gin.Context, group, clientModel string, channel *model.Channel, tried bool, setting *operation_setting.VideoSchedulingSetting) (candidate videosched.Candidate, probe VideoProbeState) {
	candidate = videosched.Candidate{
		ID: channel.Id, Name: channel.Name, Priority: channel.GetPriority(), Weight: channel.GetWeight(),
		Submit: videosched.HealthStat{Rate: 1}, Gen: videosched.HealthStat{Rate: 1},
	}
	switch {
	case channel.Status != common.ChannelStatusEnabled:
		candidate.Excluded = "disabled"
		return
	case tried:
		candidate.Excluded = "tried"
		return
	}
	cfg, ok := VideoSchedulingConfigOf(channel)
	if !ok {
		candidate.Excluded = "not schedulable: no cost table"
		return
	}
	candidate.Quality, candidate.Capacity = cfg.Quality, cfg.Capacity
	cost, ok := videoModelCost(cfg.Models, clientModel)
	if !ok {
		candidate.Excluded = "not schedulable: model not priced"
		return
	}
	candidate.Cost = videoCostConfig(cost)

	health, err := GetVideoChannelHealth(channel.Id, clientModel, setting.MinSamples)
	if err != nil {
		// No last-known value is kept: an unreadable window counts as unproven
		// rather than excluding every channel while the store is down, but an
		// unknown in-flight count is taken as full, like unreadable probe slots.
		logger.LogWarn(c, "video scheduling health read failed: channel=%d error=%v", channel.Id, err)
		candidate.InFlight = candidate.Capacity
	} else {
		candidate.Submit, candidate.Gen, candidate.InFlight, probe = health.Submit, health.Gen, health.InFlight, health.Probe
	}
	// An unregistered group name is no group.
	if quota := setting.CapacityGroups[cfg.CapacityGroup]; cfg.CapacityGroup != "" && quota > 0 {
		candidate.GroupCapacity = quota
		if candidate.GroupInFlight, err = GetVideoGroupInFlight(cfg.CapacityGroup); err != nil {
			logger.LogWarn(c, "video scheduling group in-flight read failed: group=%q error=%v", cfg.CapacityGroup, err)
			candidate.GroupInFlight = quota
		}
	}

	plugin, body, action, ok := videoCandidateRequest(c, channel)
	if !ok {
		candidate.Excluded = "not schedulable: no execution plugin"
		return
	}
	mappedModel, _, err := relaycommon.MapModelName(channel.GetModelMapping(), clientModel)
	if err != nil {
		candidate.Excluded = "not schedulable: " + err.Error()
		return
	}
	candidate.PluginKey, candidate.MappedModel = plugin.Meta.Key, mappedModel
	spec, err := describeVideoSpec(c, plugin, clientModel, mappedModel, body, action)
	switch {
	case errors.Is(err, videospec.ErrOptOut):
		candidate.Excluded = "not schedulable: model opt-out"
		return
	case errors.Is(err, videospec.ErrVersion):
		candidate.Excluded = err.Error()
		return
	case err != nil:
		candidate.Excluded = "spec invalid: " + err.Error()
		return
	}
	candidate.Spec = spec
	candidate.Sell = EstimateVideoSell(c, group, plugin, clientModel, mappedModel, body, action)
	return
}

// videoCandidateRequest resolves the plugin that executes the request on
// channel and the body and action that plugin decoded. The protocol entry
// decoded the body once per accepted plugin; the native and legacy entries pin
// one plugin whose body is task_request.
func videoCandidateRequest(c *gin.Context, channel *model.Channel) (*jsplugin.LoadedPlugin, any, string, bool) {
	taskRequest, _ := c.Get("task_request")
	if _, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		binding, ok := PinnedEndpointCandidateForChannel(c, channel, c.GetString("expected_task_plugin_key"))
		body := binding.DecodedBody
		if body == nil && !binding.DecodedBodyPresent {
			// Only an omitted field leaves task_request in place at submit;
			// explicit null must reach this candidate's own spec and usage hooks.
			body = taskRequest
		}
		return binding.Plugin, body, binding.DecodedAction, ok
	}
	pinned, _ := c.Value(jsplugin.ContextKeyPinnedPlugin).(jsplugin.PinnedPlugin)
	return pinned.Plugin, taskRequest, c.GetString("task_action"), pinned.Plugin != nil
}

// videoModelCost finds a model's cost entry. Keys are spelled as in the
// channel's models, so after the exact name it accepts the routing-normalized
// and ASCII-folded spellings that channel matching accepts.
func videoModelCost(models map[string]dto.VideoModelCost, modelName string) (dto.VideoModelCost, bool) {
	if cost, ok := models[modelName]; ok {
		return cost, true
	}
	normalized, folded := ratio_setting.RoutingMatchModelName(modelName), jsplugin.ASCIIFold(modelName)
	for _, key := range slices.Sorted(maps.Keys(models)) {
		if ratio_setting.RoutingMatchModelName(key) == normalized || jsplugin.ASCIIFold(key) == folded {
			return models[key], true
		}
	}
	return dto.VideoModelCost{}, false
}

// videoCostConfig converts a saved cost entry into the algorithm's cost table.
func videoCostConfig(cost dto.VideoModelCost) videosched.CostConfig {
	config := videosched.CostConfig{
		Mode: cost.Mode, Prices: cost.Prices,
		MinSeconds: cost.MinSeconds, MaxSeconds: cost.MaxSeconds, AllowedSeconds: cost.AllowedSeconds,
	}
	if len(cost.References) > 0 {
		config.References = make(map[string]map[string]videosched.ReferenceCost, len(cost.References))
		for kind, tiers := range cost.References {
			rules := make(map[string]videosched.ReferenceCost, len(tiers))
			for tier, rule := range tiers {
				rules[tier] = videosched.ReferenceCost{Mode: rule.Mode, Value: rule.Value}
			}
			config.References[kind] = rules
		}
	}
	return config
}

type videoSpecResult struct {
	spec videosched.Spec
	err  error
}

// describeVideoSpec calls plugin's describeSpec on its own decoded body and
// validates the answer. The plugin's answers, including thrown errors, are
// cached per request by (plugin, mapped model), so ignored descriptive keys
// are logged once per entry.
func describeVideoSpec(c *gin.Context, plugin *jsplugin.LoadedPlugin, clientModel, mappedModel string, body any, action string) (videosched.Spec, error) {
	cache, _ := c.Value(contextKeyVideoSpecCache).(map[string]videoSpecResult)
	if cache == nil {
		cache = make(map[string]videoSpecResult)
		c.Set(contextKeyVideoSpecCache, cache)
	}
	key := plugin.Meta.Key + "::" + mappedModel
	if result, ok := cache[key]; ok {
		return result.spec, result.err
	}
	var result videoSpecResult
	value, err := plugin.Engine.Call(c.Request.Context(), videoSchedSpecHook, videoUsageContext(c, clientModel, mappedModel, body, action, "spec"))
	raw, isObject := value.(map[string]any)
	var hookErr *jsplugin.HookError
	switch {
	case err != nil && !errors.As(err, &hookErr):
		// A timeout or admission failure says nothing about the request: a
		// later attempt in this request asks again.
		return videosched.Spec{}, err
	case err != nil:
		result.err = err
	case !isObject:
		result.err = errors.New("describeSpec must return an object")
	default:
		var ignored []string
		result.spec, ignored, result.err = videospec.Parse(raw)
		if len(ignored) > 0 {
			logger.LogDebug(c, "video scheduling: describeSpec of %s for %s returned ignored keys %q", plugin.Meta.Key, mappedModel, ignored)
		}
	}
	cache[key] = result
	return result.spec, result.err
}

// VideoScheduleRecord is one scheduling selection of a request: every
// candidate with its score or exclusion. A request records one per selection;
// empty auto groups share an attempt_seq, so selection_seq tells them apart.
type VideoScheduleRecord struct {
	Input        *VideoDecisionInput `json:"-"`
	SelectionSeq int                 `json:"selection_seq"`
	AttemptSeq   int                 `json:"attempt_seq"` // the submission attempt this selection feeds, from 1
	Mode         string              `json:"mode"`        // on | shadow
	Group        string              `json:"group"`
	Recommended  int                 `json:"recommended,omitempty"` // best candidate; 0 when none is eligible
	Selected     int                 `json:"selected,omitempty"`    // shadow: the channel ordinary selection used
	AffinityHit  bool                `json:"affinity_hit,omitempty"`
	Probe        bool                `json:"probe,omitempty"`     // Recommended was drawn as a recovery probe
	Explore      bool                `json:"explore,omitempty"`   // Recommended was drawn to explore an unproven channel
	Admission    string              `json:"admission,omitempty"` // probe_slot_taken: the choice was not used
	Fingerprint  string              `json:"fingerprint,omitempty"`
	Candidates   []VideoScheduleRow  `json:"candidates"`
}

// VideoScheduleRow is one candidate on the board. Cost amounts are nil when
// the quote is invalid or was never made, so a missing price never reads as 0.
type VideoScheduleRow struct {
	ID               int                      `json:"id"`
	Name             string                   `json:"name"`
	Plugin           string                   `json:"plugin,omitempty"`
	MappedModel      string                   `json:"mapped_model,omitempty"`
	Spec             *model.VideoSpecView     `json:"spec,omitempty"`
	Tier             string                   `json:"tier,omitempty"`
	CostUSD          *float64                 `json:"cost_usd,omitempty"` // total: base plus reference surcharges
	BaseCostUSD      *float64                 `json:"base_cost_usd,omitempty"`
	ReferenceCostUSD *float64                 `json:"reference_cost_usd,omitempty"`
	References       []VideoScheduleReference `json:"references,omitempty"`
	SellKind         string                   `json:"sell_kind,omitempty"`
	SellUSD          float64                  `json:"sell_usd,omitempty"`
	SellEstimated    bool                     `json:"sell_estimated,omitempty"`
	P                float64                  `json:"p"`
	Q                float64                  `json:"q"`
	S                float64                  `json:"s"`
	Total            float64                  `json:"total"`
	Unproven         bool                     `json:"unproven,omitempty"`
	Excluded         string                   `json:"excluded,omitempty"`
}

type VideoScheduleReference struct {
	Kind     string   `json:"kind"`
	Mode     string   `json:"mode,omitempty"`
	Tier     string   `json:"tier,omitempty"`
	Quantity *float64 `json:"quantity,omitempty"`
	Value    *float64 `json:"value,omitempty"`
	USD      float64  `json:"usd"`
}

// VideoScheduleRecords returns the request's scheduling selections in order.
func VideoScheduleRecords(c *gin.Context) []VideoScheduleRecord {
	records, _ := common.GetContextKeyType[[]VideoScheduleRecord](c, constant.ContextKeyVideoSchedBoard)
	return records
}

// appendVideoScheduleRecord numbers record, fills its board from scores and
// appends it to the request's selections.
func appendVideoScheduleRecord(c *gin.Context, record VideoScheduleRecord, scores []videosched.Score) {
	if videoScheduleAuditState(c) == nil {
		record.Input = nil
	}
	records := VideoScheduleRecords(c)
	record.SelectionSeq = len(records) + 1
	record.AttemptSeq = RequestPolicy(c).Attempts + 1
	record.Candidates = VideoScheduleBoard(scores)
	common.SetContextKey(c, constant.ContextKeyVideoSchedBoard, append(records, record))
}

// VideoScheduleBoard renders a scored board for logs and the simulator. It
// carries costs, plugins and exclusion reasons, so it belongs only in
// administrator views.
func VideoScheduleBoard(scores []videosched.Score) []VideoScheduleRow {
	rows := make([]VideoScheduleRow, 0, len(scores))
	for _, score := range scores {
		candidate := score.Candidate
		row := VideoScheduleRow{
			ID: candidate.ID, Name: candidate.Name, Plugin: candidate.PluginKey, MappedModel: candidate.MappedModel,
			P: score.PriceScore, Q: score.Quality, S: score.Service, Total: score.Total, Unproven: score.Unproven, Excluded: score.Reason,
		}
		if candidate.Spec.References != nil {
			spec := candidate.Spec
			row.Spec = &model.VideoSpecView{OutputSeconds: spec.OutputSeconds, SecondsKind: spec.SecondsKind, Tier: spec.Tier, References: spec.References, Missing: spec.Missing}
			row.SellKind, row.SellUSD, row.SellEstimated = candidate.Sell.Kind, candidate.Sell.USD, candidate.Sell.Estimated
		}
		if quote := score.Quote; quote.Reason == "" && quote.Tier != "" {
			row.Tier = quote.Tier
			row.CostUSD, row.BaseCostUSD, row.ReferenceCostUSD = &quote.TotalUSD, &quote.BaseUSD, &quote.ReferenceUSD
			for _, line := range quote.References {
				row.References = append(row.References, VideoScheduleReference{Kind: line.Kind, Mode: line.Mode, Tier: line.Tier, Quantity: line.Quantity, Value: line.Value, USD: line.USD})
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// videoScheduleSelection finds the latest used selection feeding the request's
// current submission attempt, and channelID's row on its board. The returned
// record's Probe tells whether the attempt on channelID is a probe: only a
// takeover that chose channelID as one acquired its slot. A shadow record's
// probe is about its hypothetical recommendation and stays on the stored record.
func videoScheduleSelection(c *gin.Context, channelID int) (VideoScheduleRecord, VideoScheduleRow, bool) {
	records := VideoScheduleRecords(c)
	attempt := RequestPolicy(c).Attempts
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].AttemptSeq != attempt || records[i].Admission != "" {
			continue
		}
		for _, row := range records[i].Candidates {
			if row.ID == channelID {
				record := records[i]
				record.Probe = record.Probe && record.Mode == operation_setting.VideoSchedulingModeOn && record.Recommended == channelID
				return record, row, true
			}
		}
	}
	return VideoScheduleRecord{}, VideoScheduleRow{}, false
}

// AppendVideoScheduleConsumeLog records the request's scheduling under
// admin_info.video_schedule of its task consumption log: the mode, the channel
// that runs the task, whether that was a probe, the spec and sell price that
// channel was scored with, and every selection of every attempt. It is
// diagnostic only and changes no billing field.
func AppendVideoScheduleConsumeLog(c *gin.Context, task *model.Task, other *model.LogOther) {
	records := VideoScheduleRecords(c)
	if len(records) == 0 {
		return
	}
	info := map[string]any{"mode": records[len(records)-1].Mode, "selected": task.ChannelId, "attempts": records}
	if record, row, ok := videoScheduleSelection(c, task.ChannelId); ok {
		info["probe"] = record.Probe
		info["spec"] = row.Spec
		info["sell"] = map[string]any{"kind": row.SellKind, "usd": row.SellUSD, "estimated": row.SellEstimated}
	}
	other.SetAdmin("video_schedule", info)
}

// LogVideoScheduleSummary writes the one scheduling summary row of a request
// that scored candidates but persisted no task, when it ends with a status of
// 400 or more or, with panicked, whatever the status. Selection failures never
// reach a channel error log, so this row is the only record of them. It
// reports no channel failure (no health penalty, no disable) and carries no
// request content; the panic value is never written.
func LogVideoScheduleSummary(c *gin.Context, panicked bool) {
	if panicked {
		// A failing log write must not replace the panic being re-raised.
		defer func() {
			if recovered := recover(); recovered != nil {
				common.SysError("video scheduling summary log failed during a panic")
			}
		}()
	}
	records := VideoScheduleRecords(c)
	status := c.Writer.Status()
	if len(records) == 0 || c.GetBool(contextKeyVideoSummaryLogged) || (!panicked && status < http.StatusBadRequest) {
		return
	}
	if _, persisted := common.GetContextKey(c, constant.ContextKeyTaskPersisted); persisted {
		return
	}
	c.Set(contextKeyVideoSummaryLogged, true)
	if !constant.ErrorLogEnabled {
		return
	}
	other := model.NewLogOther()
	if c.Request != nil && c.Request.URL != nil {
		other.SetPublic("request_path", c.Request.URL.Path)
	}
	content := fmt.Sprintf("video scheduling: request ended with status %d before a task was persisted", status)
	outcome := "rejected"
	if panicked {
		content, outcome = "video scheduling: internal failure before a task was persisted", "internal_failure"
	} else {
		other.SetPublic("status_code", status)
	}
	other.SetAdmin("video_schedule", map[string]any{"mode": records[len(records)-1].Mode, "outcome": outcome, "attempts": records})
	useTimeSeconds := int(time.Since(RequestPolicy(c).StartedAt).Seconds())
	model.RecordErrorLog(c, c.GetInt("id"), c.GetInt("channel_id"), c.GetString("resolved_task_model"), c.GetString("token_name"), content,
		c.GetInt("token_id"), useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), c.GetString("group"), other)
}
