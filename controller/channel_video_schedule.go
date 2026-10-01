package controller

import (
	"bytes"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// GetChannelVideoHealth returns a channel's video scheduling health: both
// channel-wide windows, in-flight counts, probe state and each priced model's
// windows. data is null for a channel without a video_scheduling config.
func GetChannelVideoHealth(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	view, err := service.GetVideoHealthView(channel, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

// channelListItems attaches video_health to the channels that carry a
// video_scheduling config. The list query already loaded their settings, so
// this reads only Redis or memory per row.
func channelListItems(channels []*model.Channel) []any {
	items := make([]any, 0, len(channels))
	for _, channel := range channels {
		if channel.OtherSettings == "" {
			// No config; also skips the cache lookup that would query the
			// database when the memory cache is off.
			items = append(items, channel)
			continue
		}
		view, err := service.GetVideoHealthView(channel, false)
		if err != nil {
			common.SysError("video scheduling health read failed: channel=" + strconv.Itoa(channel.Id) + " error=" + err.Error())
		}
		if view == nil {
			items = append(items, channel)
			continue
		}
		items = append(items, struct {
			*model.Channel
			VideoHealth *service.VideoHealthView `json:"video_health"`
		}{channel, view})
	}
	return items
}

type videoSchedulableModel struct {
	Model string `json:"model"`
	// StaticBlockers are the plugins declaring the same model without
	// describeSpec; a request one of them accepts is not scheduled.
	StaticBlockers []string `json:"static_blockers"`
}

// GetVideoSchedulable reports whether a plugin exports describeSpec, and for
// each model it declares, the plugins that could make a request a mixed pool.
func GetVideoSchedulable(c *gin.Context) {
	generation := jsplugin.DefaultRegistry.Generation()
	plugin, ok := generation.Get(c.Query("plugin"))
	if !ok {
		common.ApiErrorMsg(c, "unknown task plugin")
		return
	}
	describeSpec, err := plugin.Engine.HasCallableHook(c.Request.Context(), "describeSpec")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	models := make([]videoSchedulableModel, 0, len(plugin.Meta.Models))
	for _, name := range plugin.Meta.Models {
		blockers := service.VideoSchedStaticBlockers(c.Request.Context(), generation, name)
		if blockers == nil {
			blockers = []string{}
		}
		models = append(models, videoSchedulableModel{Model: name, StaticBlockers: blockers})
	}
	common.ApiSuccess(c, gin.H{"plugin": plugin.Meta.Key, "describe_spec": describeSpec, "models": models})
}

type videoHealthOverride struct {
	Submit *videosched.HealthStat   `json:"submit"`
	Gen    *videosched.HealthStat   `json:"gen"`
	Probe  *service.VideoProbeState `json:"probe"`
}

type videoScheduleSimulateRequest struct {
	Group     string `json:"group"`
	UserGroup string `json:"user_group"`
	Entry     string `json:"entry"`      // protocol | native
	Protocol  string `json:"protocol"`   // protocol entry: host protocol whose create path is used when path is empty
	Path      string `json:"path"`       // protocol entry: host protocol path; native entry: the concrete route path
	PluginKey string `json:"plugin_key"` // native entry
	// RequestBody is the client's original JSON request body.
	RequestBody      common.RawMessage           `json:"request_body"`
	HealthOverride   map[int]videoHealthOverride `json:"health_override"`
	InflightOverride struct {
		Channels map[int]int    `json:"channels"`
		Groups   map[string]int `json:"groups"`
	} `json:"inflight_override"`
	SlotOverride   map[int]int                               `json:"slot_override"`
	ConfigSnapshot *operation_setting.VideoSchedulingSetting `json:"config_snapshot"`
	Seed           *uint64                                   `json:"seed"`
	Now            string                                    `json:"now"` // RFC3339
}

// SimulateVideoSchedule scores a request exactly as live traffic would: the
// same entry middlewares build the request view, the same assembly freezes
// the decision input and the same pure decision runs on it. It acquires no
// probe slot and writes no state. The fingerprint hashes the whole decision
// input; equal fingerprints and seeds give equal results. Overrides pin the
// health, in-flight, probe slot, setting and clock segments; the others use
// live values. config_snapshot also drives the takeover/shadow decision.
func SimulateVideoSchedule(c *gin.Context) {
	var req videoScheduleSimulateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiError(c, err)
		return
	}
	switch {
	case req.Group == "" || req.Group == "auto":
		common.ApiErrorMsg(c, "group must name a concrete group")
		return
	case !common.MemoryCacheEnabled:
		common.ApiErrorMsg(c, "video scheduling reads candidates from the memory cache, which is disabled")
		return
	case len(req.RequestBody) == 0:
		common.ApiErrorMsg(c, "request_body is required")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	if req.Now != "" {
		parsed, err := time.Parse(time.RFC3339, req.Now)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		now = parsed.UTC()
	}
	// A default seed stays below 2^53 so the echoed value survives JavaScript.
	seed := rand.Uint64() >> 11
	if req.Seed != nil {
		seed = *req.Seed
	}
	setting := operation_setting.GetVideoSchedulingSetting()
	if req.ConfigSnapshot != nil {
		// Health samples are stored in buckets sized by the live window, so a
		// different window cannot be read back from them.
		if live := setting.WindowSeconds; req.ConfigSnapshot.WindowSeconds != live {
			common.ApiErrorMsg(c, "config_snapshot.window_seconds must equal the live window_seconds "+strconv.Itoa(live)+": health samples are bucketed by the live window")
			return
		}
		if err := operation_setting.ValidateVideoSchedulingSnapshot(req.ConfigSnapshot); err != nil {
			common.ApiErrorMsg(c, "invalid config_snapshot: "+err.Error())
			return
		}
		setting = req.ConfigSnapshot
	}

	// The entry middlewares only run inside a gin engine; the scoring below
	// happens in the terminal handler because gin recycles the context after
	// ServeHTTP returns.
	engine := gin.New()
	engine.Use(middleware.BodyStorageCleanup())
	var result gin.H
	terminal := func(sc *gin.Context) {
		common.SetContextKey(sc, constant.ContextKeyUserGroup, req.UserGroup)
		modelName := sc.GetString("resolved_task_model")
		if modelName == "" {
			result = gin.H{"error": "no task plugin claimed the request"}
			return
		}
		constraints := service.GetChannelConstraints(sc)
		constraints.AddFilter(taskdto.ChannelFilter{Kind: taskdto.FilterRequestPath, RequestPath: sc.Request.URL.Path})
		channels, err := model.SatisfiedChannelSnapshot(req.Group, modelName, constraints.Filters)
		if err != nil {
			result = gin.H{"error": err.Error()}
			return
		}
		input := service.AssembleVideoDecision(sc, setting, req.Group, modelName, channels, seed)
		input.Now = now
		for i := range input.Candidates {
			candidate := &input.Candidates[i]
			if override, ok := req.HealthOverride[candidate.ID]; ok {
				if override.Submit != nil {
					candidate.Submit = *override.Submit
				}
				if override.Gen != nil {
					candidate.Gen = *override.Gen
				}
				if override.Probe != nil {
					input.Probe[candidate.ID] = *override.Probe
				}
			}
			if n, ok := req.InflightOverride.Channels[candidate.ID]; ok {
				candidate.InFlight = n
			}
			if cfg, ok := service.VideoSchedulingConfigOf(channels[i]); ok && cfg.CapacityGroup != "" {
				if n, ok := req.InflightOverride.Groups[cfg.CapacityGroup]; ok {
					candidate.GroupInFlight = n
				}
			}
			if n, ok := req.SlotOverride[candidate.ID]; ok {
				delete(input.SlotOccupancy, candidate.ID)
				if n > 0 {
					input.SlotOccupancy[candidate.ID] = n
				}
			}
		}
		fingerprint, segments, err := service.VideoDecisionFingerprint(input)
		if err != nil {
			result = gin.H{"error": err.Error()}
			return
		}
		choice := service.DecideVideoSchedule(input)
		recommended := 0
		if choice.Best != nil {
			recommended = choice.Best.ID
		}
		result = gin.H{
			"decision":    service.VideoSchedDecisionFrom(sc),
			"group":       req.Group,
			"model":       modelName,
			"group_ratio": service.VideoEffectiveGroupRatio(sc, req.Group),
			"recommended": recommended,
			"probe":       choice.Probe,
			"explore":     choice.Explore,
			"candidates":  service.VideoScheduleBoard(choice.Board),
			"now":         input.Now.Format(time.RFC3339),
			"seed":        seed,
			"fingerprint": fingerprint,
			"segments":    segments,
		}
	}
	identify := func(sc *gin.Context) {
		sc.Set("id", c.GetInt("id"))
		sc.Set("group", req.UserGroup)
		// The entry decides takeover/shadow under the same setting that scores.
		common.SetContextKey(sc, constant.ContextKeyVideoSchedSetting, setting)
		sc.Next()
	}

	path := req.Path
	switch req.Entry {
	case "protocol":
		if path == "" {
			definition, _ := jsplugin.HostProtocol(req.Protocol)
			for _, operation := range definition.Operations {
				if operation.ModelField != "" {
					path = operation.Path
					break
				}
			}
		}
		if path == "" || !strings.HasPrefix(path, "/") {
			common.ApiErrorMsg(c, "protocol entry needs a host protocol path or protocol")
			return
		}
		engine.POST(path, identify, middleware.PinTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), terminal)
	case "native":
		generation := jsplugin.DefaultRegistry.Generation()
		registered := false
		for _, binding := range generation.Routes() {
			if binding.Plugin.Meta.Key != req.PluginKey || binding.Route.Type != jsplugin.RouteTypeSubmit || binding.Route.Method != http.MethodPost {
				continue
			}
			pinRoute := func(sc *gin.Context) {
				sc.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: binding.Plugin})
				sc.Set(jsplugin.ContextKeyPinnedRoute, jsplugin.PinnedRoute{Generation: generation, Plugin: binding.Plugin, Route: binding.Route})
				sc.Next()
			}
			engine.POST(binding.Route.Path, identify, pinRoute, middleware.PrepareTaskPluginRoute(), terminal)
			registered = true
		}
		if !registered || !strings.HasPrefix(path, "/") {
			common.ApiErrorMsg(c, "native entry needs a plugin_key with POST submit routes and a concrete route path")
			return
		}
	default:
		common.ApiErrorMsg(c, "entry must be protocol or native")
		return
	}

	synthetic, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, path, bytes.NewReader(req.RequestBody))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	synthetic.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, synthetic)
	switch {
	case result == nil:
		common.ApiErrorMsg(c, "the entry rejected the request: status "+strconv.Itoa(recorder.Code)+" "+recorder.Body.String())
	case result["error"] != nil:
		common.ApiErrorMsg(c, result["error"].(string))
	default:
		common.ApiSuccess(c, result)
	}
}
