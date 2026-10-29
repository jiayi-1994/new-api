package service

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	videodto "github.com/QuantumNous/new-api/relaykit/dto"
)

var (
	upstreamQuotaMessagePattern = regexp.MustCompile(`(?i)额度|余额|余量|欠费|欠款|充值|扣费|quota|balance|insufficient|credit|billing|overdue|arrear|[$￥¥€£]\s*\d|\d(?:\.\d+)?\s*(?:USD|CNY|RMB|EUR|GBP)\b`)
	upstreamCredentialPattern   = regexp.MustCompile(`(?i)(\b(?:authorization|proxy-authorization|x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret|credential)\b["']?\s*[:=]\s*["']?)(?:(?:Bearer|Basic)\s+)?[^\s"',;}\]]+`)
	upstreamBearerPattern       = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-z0-9._~+/=-]+`)
	upstreamAPIKeyPattern       = regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]+`)
)

// SanitizeUnifiedVideoUpstreamError applies the same public error policy to
// submission errors and persisted task failures. Callers first check opt-in.
func SanitizeUnifiedVideoUpstreamError(message string, secrets ...string) string {
	var body videodto.GeneralErrorResponse
	if common.UnmarshalJsonStr(message, &body) == nil {
		if parsed := body.ToMessage(); parsed != "" {
			message = parsed
		}
	}
	if upstreamQuotaMessagePattern.MatchString(message) {
		return "上游账户状态异常，暂时无法处理该请求，请联系管理员"
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "***")
		}
	}
	message = upstreamCredentialPattern.ReplaceAllString(message, "${1}***")
	message = upstreamBearerPattern.ReplaceAllString(message, "${1} ***")
	message = upstreamAPIKeyPattern.ReplaceAllString(message, "***")
	message = strings.TrimSpace(common.MaskSensitiveInfo(message))
	if message == "" {
		return "Video generation failed"
	}
	return message
}

func unifiedVideoHostFailure(task *model.Task) bool {
	return task.VideoHealthAttribution == "host" || task.VideoHealthAttribution == taskAttributionHostLag ||
		strings.HasPrefix(task.FailReason, "poll failed:") || strings.HasPrefix(task.FailReason, "upstream task not found (HTTP ")
}

func unifiedVideoTaskFailureMessage(task *model.Task) string {
	const generic = "Video generation failed"
	if task.ChannelId <= 0 || strings.TrimSpace(task.FailReason) == "" || unifiedVideoHostFailure(task) {
		return generic
	}
	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil || channel == nil || channel.Setting == nil {
		return generic
	}
	// GetSetting repairs malformed JSON by writing the channel. Public reads
	// must instead fail closed without mutating the channel or cached object.
	var setting videodto.ChannelSettings
	if common.UnmarshalJsonStr(*channel.Setting, &setting) != nil || !setting.ErrorPassthroughEnabled {
		return generic
	}
	secrets := []string{task.PrivateData.Key, task.PrivateData.UpstreamTaskID}
	secrets = append(secrets, channel.GetKeys()...)
	return SanitizeUnifiedVideoUpstreamError(task.FailReason, secrets...)
}

// RedactUnifiedVideoTaskDTO keeps execution details out of the public sale
// without changing the persisted task used for polling and administration.
func RedactUnifiedVideoTaskDTO(task *model.Task, item *dto.TaskDto) {
	if !task.IsUnifiedVideoSale() {
		return
	}
	item.Platform = "video"
	item.ChannelId = 0
	item.Data = nil
	item.ResultURL = ""
	item.AdminInfo = nil
	item.RootInfo = nil
	properties := task.Properties
	properties.UpstreamModelName = ""
	item.Properties = properties
	item.FailReason = ""
	if task.Status == model.TaskStatusFailure {
		item.FailReason = unifiedVideoTaskFailureMessage(task)
	}
}

// TaskVideoFacts returns the output seconds and resolution tier frozen on a
// video task at submission (billing facts first, then the scheduling spec).
// Zero values mean the task carries no such fact.
func TaskVideoFacts(task *model.Task) (seconds float64, resolution string) {
	if bc := task.PrivateData.BillingContext; bc != nil {
		if snap := bc.TieredSnapshot; snap != nil {
			seconds, _ = snap.UsageFacts["seconds"].(float64)
			resolution, _ = snap.UsageFacts["resolution"].(string)
		}
		if seconds <= 0 {
			seconds = bc.OtherRatios["seconds"]
		}
	}
	if summary := task.PrivateData.SchedulingSummary; summary != nil && summary.Spec != nil {
		if seconds <= 0 && summary.Spec.OutputSeconds != nil {
			seconds = *summary.Spec.OutputSeconds
		}
		if resolution == "" {
			resolution = summary.Spec.Tier
		}
	}
	return max(seconds, 0), videoAuditResolution(resolution)
}

// TaskVideoInputFacts projects only recorded reference facts for the dashboard.
// Missing history stays unknown; reference seconds are measured, not billable seconds.
func TaskVideoInputFacts(task *model.Task) (mode string, referenceSeconds *float64) {
	if summary := task.PrivateData.SchedulingSummary; summary != nil && summary.Spec != nil {
		spec := summary.Spec
		referenceSeconds = spec.InputVideoSeconds
		if spec.References != nil {
			switch {
			case spec.References["video"] > 0:
				// A video reference takes precedence over accompanying images or frames.
				mode = "reference_to_video"
			case spec.References["image"] > 0 || spec.References["frame"] > 0:
				mode = "image_to_video"
			default:
				mode = "text_to_video"
			}
			if spec.References["video"] == 0 {
				zero := 0.0
				referenceSeconds = &zero
			}
		}
	}
	if bc := task.PrivateData.BillingContext; bc != nil && bc.TieredSnapshot != nil {
		if seconds, ok := bc.TieredSnapshot.UsageFacts["input_video_seconds"].(float64); ok {
			referenceSeconds = &seconds
		}
	}
	if referenceSeconds != nil {
		seconds := *referenceSeconds
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
			referenceSeconds = nil
		} else if seconds > 0 {
			mode = "reference_to_video"
		}
	}
	return
}

// TaskVideoTokens returns the total billed video quantity for a successful
// unified token sale. Use the frozen output and billable reference tokens,
// including any reference minimum, without rounding or reading provider usage.
// Missing facts and unfinished/failed tasks have no completed usage to report.
func TaskVideoTokens(task *model.Task) *float64 {
	if !task.IsUnifiedVideoSale() || task.Status != model.TaskStatusSuccess {
		return nil
	}
	facts := task.PrivateData.BillingContext.TieredSnapshot.UsageFacts
	tokens, ok := facts["video_tokens"].(float64)
	if !ok || tokens < 0 || math.IsNaN(tokens) || math.IsInf(tokens, 0) {
		return nil
	}
	if value, exists := facts["input_video_tokens"]; exists {
		input, ok := value.(float64)
		if !ok || input < 0 || math.IsNaN(input) || math.IsInf(input, 0) {
			return nil
		}
		tokens += input
	}
	if math.IsInf(tokens, 0) {
		return nil
	}
	return &tokens
}

// BuildUnifiedVideoResponse uses only host lifecycle and frozen sale fields.
// Provider renderers may include arbitrary metadata and upstream URLs.
func BuildUnifiedVideoResponse(task *model.Task) *videodto.OpenAIVideo {
	video := task.ToOpenAIVideo()
	video.VideoTokens = TaskVideoTokens(task)
	if video.CreatedAt == 0 {
		video.CreatedAt = task.SubmitTime
	}
	if task.IsUnifiedVideoSale() {
		facts := task.PrivateData.BillingContext.TieredSnapshot.UsageFacts
		if seconds, exists := facts["seconds"]; exists {
			video.Seconds = fmt.Sprint(seconds)
		}
		if resolution, ok := facts["resolution"].(string); ok {
			video.SetMetadata("resolution", resolution)
		}
	}
	if task.Status == model.TaskStatusSuccess {
		video.SetMetadata("url", "/v1/videos/"+url.PathEscape(task.TaskID)+"/content")
	}
	if task.Status != model.TaskStatusFailure {
		return video
	}
	video.Error = &videodto.OpenAIVideoError{Code: "video_generation_failed", Message: unifiedVideoTaskFailureMessage(task)}
	// Keep stable failure categories independent of the message opt-in.
	// Older untracked tasks lack the host marker, but retain these poll reasons.
	if unifiedVideoHostFailure(task) || GetTaskAdaptorFunc == nil {
		return video
	}
	classifier, ok := GetTaskAdaptorFunc(task.Platform).(VideoFailureClassifier)
	if !ok {
		return video
	}
	class, valid := classifier.ClassifyFailure(task.FailReason)
	if !valid {
		return video
	}
	switch class {
	case VideoFailureUser:
		video.Error.Code = "video_request_rejected"
	case VideoFailureCancelled:
		video.Error.Code = "video_generation_cancelled"
	}
	return video
}

// BuildTaskPluginView converts a persisted task into the deliberately narrow
// public shape permitted at JavaScript plugin boundaries.
func BuildTaskPluginView(task *model.Task) (dto.TaskView, error) {
	createdAt := task.CreatedAt
	if createdAt == 0 {
		createdAt = task.SubmitTime
	}
	view := dto.TaskView{
		TaskID:     task.TaskID,
		Platform:   string(task.Platform),
		Status:     string(task.Status),
		Progress:   task.Progress,
		FailReason: task.FailReason,
		CreatedAt:  createdAt,
		UpdatedAt:  task.UpdatedAt,
		FinishedAt: task.FinishTime,
	}
	if len(task.Data) > 0 {
		if err := common.Unmarshal(task.Data, &view.Data); err != nil {
			return dto.TaskView{}, err
		}
		view.Data = replacePrivateTaskID(view.Data, task.PrivateData.UpstreamTaskID, task.TaskID)
	}
	return view, nil
}

// replacePrivateTaskID rewrites exact private IDs only in known task-ID fields.
// Map keys and opaque strings, including URLs containing the ID, are preserved.
func replacePrivateTaskID(value any, privateTaskID, publicTaskID string) any {
	if privateTaskID == "" || privateTaskID == publicTaskID {
		return value
	}
	switch typed := value.(type) {
	case []any:
		replaced := make([]any, len(typed))
		for index, item := range typed {
			replaced[index] = replacePrivateTaskID(item, privateTaskID, publicTaskID)
		}
		return replaced
	case map[string]any:
		replaced := make(map[string]any, len(typed))
		for key, item := range typed {
			if (key == "id" || key == "task_id" || key == "taskId") && item == privateTaskID {
				replaced[key] = publicTaskID
				continue
			}
			replaced[key] = replacePrivateTaskID(item, privateTaskID, publicTaskID)
		}
		return replaced
	default:
		return value
	}
}
