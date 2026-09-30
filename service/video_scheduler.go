package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
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
	Takeover bool   // mode on: the scheduler chooses the channel
	Shadow   bool   // mode shadow: the scheduler only scores and logs
	Reason   string // why neither applies; empty when one does
}

// DecideVideoSched freezes the decision for a prepared task submit request.
// Takeover (mode on, with its runtime prerequisites met) and Shadow (mode
// shadow) both require a listed model
// (an empty allow list lists every model), a fresh submission rather than a
// continuation of an origin task, and a describeSpec hook on every accepted
// candidate plugin. A single hook-less candidate keeps the whole request on
// ordinary channel selection, in every group.
func DecideVideoSched(c *gin.Context) (decision VideoSchedDecision) {
	defer func() { common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision) }()

	setting := operation_setting.GetVideoSchedulingSetting()
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
	if setting.Mode == operation_setting.VideoSchedulingModeOn {
		// A saved mode can bypass option validation (startup load), so the
		// prerequisites are checked again per request.
		if err := operation_setting.ValidateVideoSchedulingOption("video_scheduling_setting.mode", setting.Mode); err != nil || !VideoHealthReady() {
			return VideoSchedDecision{Reason: VideoSchedReasonNotReady}
		}
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

// videoSchedShadowSeed is the fixed seed of shadow scoring, which must never
// draw from the global random source.
const videoSchedShadowSeed = 1

const contextKeyVideoSpecCache = "video_sched_spec_cache"

func init() {
	model.TierSelector = selectVideoChannel
}

// VideoExploreSettings are the probe and explore parameters in effect for one
// decision. They are host concepts, so they stay out of videosched.Policy.
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
	// SlotOccupancy is the probe slots held per channel. Probing is not wired
	// yet, so no reader fills it.
	SlotOccupancy map[int]int
	Now           time.Time
	Seed          uint64
}

// selectVideoChannel is model.TierSelector: it chooses among the satisfied
// channels of a request that video scheduling took over, ignoring the legacy
// priority retry index. Every other request keeps ordinary selection.
func selectVideoChannel(c *gin.Context, group, modelName string, filters []taskdto.ChannelFilter) (*model.Channel, error) {
	if !VideoSchedDecisionFrom(c).Takeover {
		return nil, model.ErrTierSelectorNotApplicable
	}
	channels, err := model.SatisfiedChannelSnapshot(group, modelName, filters)
	if err != nil {
		return nil, err
	}
	input := AssembleVideoDecision(c, group, modelName, channels, rand.Uint64())
	// Probe and explore (PLAN §2.7 step 4) choose from input here, before the
	// argmax; until they land, unproven candidates are only scored.
	best, scores, err := videosched.Select(input.Candidates, input.Policy, rand.New(rand.NewPCG(input.Seed, 0)))
	record := VideoScheduleRecord{Mode: operation_setting.VideoSchedulingModeOn, Group: group}
	if best != nil {
		record.Recommended = best.ID
	}
	appendVideoScheduleRecord(c, record, scores)
	if err != nil {
		return nil, model.ErrTierSelectorNoCandidate
	}
	for _, channel := range channels {
		if channel.Id == best.ID {
			return channel, nil
		}
	}
	return nil, model.ErrTierSelectorNoCandidate
}

// ShadowObserveVideoSched scores a shadow request's candidates after ordinary
// selection chose selected, and only records and logs the recommendation. It
// uses a fixed seed and changes no selection, probe or auto-group state.
func ShadowObserveVideoSched(c *gin.Context, group, modelName string, selected *model.Channel, affinityHit bool) {
	if selected == nil || !VideoSchedDecisionFrom(c).Shadow {
		return
	}
	channels, err := model.SatisfiedChannelSnapshot(group, modelName, GetChannelConstraints(c).Filters)
	if err != nil {
		logger.LogWarn(c, "video scheduling shadow skipped: %v", err)
		return
	}
	input := AssembleVideoDecision(c, group, modelName, channels, videoSchedShadowSeed)
	best, scores, _ := videosched.Select(input.Candidates, input.Policy, rand.New(rand.NewPCG(input.Seed, 0)))
	record := VideoScheduleRecord{Mode: operation_setting.VideoSchedulingModeShadow, Group: group, Selected: selected.Id, AffinityHit: affinityHit}
	if best != nil {
		record.Recommended = best.ID
	}
	appendVideoScheduleRecord(c, record, scores)
	logger.LogInfo(c, fmt.Sprintf("video scheduling shadow: group=%q model=%q selected=%d recommended=%d affinity_hit=%t",
		group, modelName, selected.Id, record.Recommended, affinityHit))
}

// AssembleVideoDecision freezes the decision input for channels, the
// satisfied channels of modelName in group (auto already expanded). Channels
// this request already attempted are excluded as tried.
func AssembleVideoDecision(c *gin.Context, group, modelName string, channels []*model.Channel, seed uint64) VideoDecisionInput {
	setting := operation_setting.GetVideoSchedulingSetting()
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
		Probe: make(map[int]VideoProbeState, len(channels)),
		Now:   time.Now(),
		Seed:  seed,
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
	cfg, ok := VideoSchedulingTracks(channel)
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
		// rather than excluding every channel while the store is down.
		logger.LogWarn(c, "video scheduling health read failed: channel=%d error=%v", channel.Id, err)
	} else {
		candidate.Submit, candidate.Gen, candidate.InFlight, probe = health.Submit, health.Gen, health.InFlight, health.Probe
	}
	// An unregistered group name is no group.
	if quota := setting.CapacityGroups[cfg.CapacityGroup]; cfg.CapacityGroup != "" && quota > 0 {
		candidate.GroupCapacity = quota
		if candidate.GroupInFlight, err = GetVideoGroupInFlight(cfg.CapacityGroup); err != nil {
			logger.LogWarn(c, "video scheduling group in-flight read failed: group=%q error=%v", cfg.CapacityGroup, err)
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
		if body == nil {
			// A decoder that returns no body leaves task_request in place at submit.
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
// validates the answer. Results are cached per request by (plugin, mapped
// model), so ignored descriptive keys are logged once per entry.
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
	switch {
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
	SelectionSeq int                `json:"selection_seq"`
	AttemptSeq   int                `json:"attempt_seq"` // the submission attempt this selection feeds, from 1
	Mode         string             `json:"mode"`        // on | shadow
	Group        string             `json:"group"`
	Recommended  int                `json:"recommended,omitempty"` // best candidate; 0 when none is eligible
	Selected     int                `json:"selected,omitempty"`    // shadow: the channel ordinary selection used
	AffinityHit  bool               `json:"affinity_hit,omitempty"`
	Candidates   []VideoScheduleRow `json:"candidates"`
}

// VideoScheduleRow is one candidate on the board. Cost amounts are nil when
// the quote is invalid or was never made, so a missing price never reads as 0.
type VideoScheduleRow struct {
	ID               int                      `json:"id"`
	Name             string                   `json:"name"`
	Plugin           string                   `json:"plugin,omitempty"`
	MappedModel      string                   `json:"mapped_model,omitempty"`
	Spec             *VideoScheduleSpec       `json:"spec,omitempty"`
	Tier             string                   `json:"tier,omitempty"`
	CostUSD          *float64                 `json:"cost_usd,omitempty"`
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

type VideoScheduleSpec struct {
	OutputSeconds *float64       `json:"output_seconds,omitempty"`
	SecondsKind   string         `json:"seconds_kind,omitempty"`
	Tier          string         `json:"tier,omitempty"`
	References    map[string]int `json:"references"`
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
	records := VideoScheduleRecords(c)
	record.SelectionSeq = len(records) + 1
	record.AttemptSeq = RequestPolicy(c).Attempts + 1
	for _, score := range scores {
		candidate := score.Candidate
		row := VideoScheduleRow{
			ID: candidate.ID, Name: candidate.Name, Plugin: candidate.PluginKey, MappedModel: candidate.MappedModel,
			P: score.PriceScore, Q: score.Quality, S: score.Service, Total: score.Total, Unproven: score.Unproven, Excluded: score.Reason,
		}
		if candidate.Spec.References != nil {
			spec := candidate.Spec
			row.Spec = &VideoScheduleSpec{OutputSeconds: spec.OutputSeconds, SecondsKind: spec.SecondsKind, Tier: spec.Tier, References: spec.References}
			row.SellKind, row.SellUSD, row.SellEstimated = candidate.Sell.Kind, candidate.Sell.USD, candidate.Sell.Estimated
		}
		if quote := score.Quote; quote.Reason == "" && quote.Tier != "" {
			row.Tier = quote.Tier
			row.CostUSD, row.BaseCostUSD, row.ReferenceCostUSD = &quote.TotalUSD, &quote.BaseUSD, &quote.ReferenceUSD
			for _, line := range quote.References {
				row.References = append(row.References, VideoScheduleReference{Kind: line.Kind, Mode: line.Mode, Tier: line.Tier, Quantity: line.Quantity, Value: line.Value, USD: line.USD})
			}
		}
		record.Candidates = append(record.Candidates, row)
	}
	common.SetContextKey(c, constant.ContextKeyVideoSchedBoard, append(records, record))
}
