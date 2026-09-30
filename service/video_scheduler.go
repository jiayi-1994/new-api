package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// Reasons a request is neither taken over nor shadowed by video scheduling.
const (
	VideoSchedReasonModeOff        = "mode_off"
	VideoSchedReasonNotSubmit      = "not_submit"
	VideoSchedReasonModelNotListed = "model_not_listed"
	VideoSchedReasonMixedPool      = "mixed_pool"
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
// Takeover (mode on) and Shadow (mode shadow) both require a listed model
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
