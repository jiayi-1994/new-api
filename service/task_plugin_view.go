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
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

var (
	upstreamCredentialPattern = regexp.MustCompile(`(?i)(\b(?:authorization|proxy-authorization|x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret|credential)\b["']?\s*[:=]\s*["']?)(?:(?:Bearer|Basic)\s+)?[^\s"',;}\]]+`)
	upstreamBearerPattern     = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-z0-9._~+/=-]+`)
	upstreamAPIKeyPattern     = regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]+`)
	upstreamEnvelopePattern   = regexp.MustCompile(`^(?:status_code=\d+, |upstream status \d+: )`)
	// Any URL, address or mailbox: a hidden host is still a supply-side detail.
	upstreamLinkPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>，。；！？、）】》]+` +
		`|\bdata:[a-z]+/[a-z0-9.+-]+[^\s"']*` +
		`|\bwww\.[^\s"'<>，。；！？、）】》]+` +
		`|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}` +
		`|\[[0-9a-f:]{2,}\](?::\d+)?` +
		`|\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?(?:/[^\s"'<>，。；！？、）】》]*)?` +
		`|\b(?:[a-z0-9-]+\.)+(?:com|net|org|cn|io|ai|top|cc|co|xyz|dev|app|cloud|site|tech|me|info|biz|vip|ltd|shop|online|store|hk|tw|jp|us|uk|de|ru|invalid|example)\b(?::\d+)?(?:/[^\s"'<>，。；！？、）】》]*)?`)
)

const upstreamLinkPlaceholder = "[已隐藏]"

// unifiedVideoUpstreamMessage unwraps the status prefix and the JSON layers
// upstreams and gateways put around the message a person is meant to read.
func unifiedVideoUpstreamMessage(message string) string {
	message = upstreamEnvelopePattern.ReplaceAllString(strings.TrimSpace(message), "")
	for range 3 {
		var body videodto.GeneralErrorResponse
		if common.UnmarshalJsonStr(message, &body) != nil {
			break
		}
		parsed := body.ToMessage()
		if parsed == "" {
			break
		}
		message = parsed
	}
	return strings.TrimSpace(message)
}

// SanitizeUnifiedVideoUpstreamError applies the same public error policy to
// submission errors and persisted task failures. Callers first check opt-in.
func SanitizeUnifiedVideoUpstreamError(message string, secrets ...string) string {
	message = unifiedVideoUpstreamMessage(message)
	if operation_setting.UpstreamQuotaMessagePattern.MatchString(message) {
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
	message = upstreamLinkPattern.ReplaceAllString(message, upstreamLinkPlaceholder)
	message = strings.TrimSpace(common.MaskSensitiveInfo(message))
	// Structured bodies carry field names and details no person should read.
	if message == "" || strings.HasPrefix(message, "{") || strings.HasPrefix(message, "[{") || strings.Contains(message, `{"`) {
		return model.UnifiedVideoRequestFailedMessage
	}
	return message
}

// WhitelistedUnifiedVideoError returns the public wording of a failure an
// administrator has approved, independent of the channel passthrough opt-in.
func WhitelistedUnifiedVideoError(message string, secrets ...string) (string, bool) {
	if operation_setting.IsVideoErrorNeverPublic(message) {
		return "", false
	}
	display, matched := operation_setting.MatchVideoErrorWhitelist(message)
	if !matched {
		return "", false
	}
	if display != "" {
		return display, true
	}
	return SanitizeUnifiedVideoUpstreamError(message, secrets...), true
}

func unifiedVideoHostFailure(task *model.Task) bool {
	return task.VideoHealthAttribution == "host" || task.VideoHealthAttribution == taskAttributionHostLag ||
		strings.HasPrefix(task.FailReason, "poll failed:") || strings.HasPrefix(task.FailReason, "upstream task not found (HTTP ")
}

func unifiedVideoTaskError(task *model.Task) *videodto.OpenAIVideoError {
	failure := &videodto.OpenAIVideoError{
		Code: "video_generation_failed", Message: model.UnifiedVideoRequestFailedMessage,
	}
	if unifiedVideoHostFailure(task) {
		return failure
	}
	// Classify once so the public code and fallback wording agree on every
	// retrieval surface. Host diagnostics must never reach the classifier.
	if GetTaskAdaptorFunc != nil {
		if classifier, ok := GetTaskAdaptorFunc(task.Platform).(VideoFailureClassifier); ok {
			if class, valid := classifier.ClassifyFailure(task.FailReason); valid {
				switch class {
				case VideoFailureUser:
					failure.Code = "video_request_rejected"
					failure.Message = model.UnifiedVideoRequestRejectedMessage
				case VideoFailureCancelled:
					failure.Code = "video_generation_cancelled"
				}
			}
		}
	}
	if task.ChannelId <= 0 || strings.TrimSpace(task.FailReason) == "" {
		return failure
	}
	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil || channel == nil {
		return failure
	}
	secrets := []string{task.PrivateData.Key, task.PrivateData.UpstreamTaskID}
	secrets = append(secrets, channel.GetKeys()...)
	if message, ok := WhitelistedUnifiedVideoError(task.FailReason, secrets...); ok {
		failure.Message = message
		return failure
	}
	if channel.Setting == nil {
		return failure
	}
	// GetSetting repairs malformed JSON by writing the channel. Public reads
	// must instead fail closed without mutating the channel or cached object.
	var setting videodto.ChannelSettings
	if common.UnmarshalJsonStr(*channel.Setting, &setting) != nil || !setting.ErrorPassthroughEnabled {
		return failure
	}
	failure.Message = SanitizeUnifiedVideoUpstreamError(task.FailReason, secrets...)
	return failure
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
		item.FailReason = unifiedVideoTaskError(task).Message
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
		for key, value := range unifiedVideoOriginFields(task) {
			video.SetMetadata(key, value)
		}
	}
	if task.Status != model.TaskStatusFailure {
		return video
	}
	video.Error = unifiedVideoTaskError(task)
	return video
}

// unifiedVideoOriginNames are the only vendor origin fields the unified sale
// exposes. Each plugin's openai_video renderer maps its vendor's naming onto
// these names, so a new upstream changes its plugin, never the host.
var unifiedVideoOriginNames = []string{"volcesVideoUrl", "volcesCgt"}

type openAIVideoRenderer interface {
	ConvertToOpenAIVideo(task *model.Task) ([]byte, error)
}

// unifiedVideoOriginFields asks the executing plugin's renderer for the origin
// links of a successful task and keeps only the fixed public names.
func unifiedVideoOriginFields(task *model.Task) map[string]string {
	if GetTaskAdaptorFunc == nil {
		return nil
	}
	renderer, ok := GetTaskAdaptorFunc(task.Platform).(openAIVideoRenderer)
	if !ok {
		return nil
	}
	rendered, err := renderer.ConvertToOpenAIVideo(task)
	if err != nil {
		return nil
	}
	var fields map[string]any
	if common.Unmarshal(rendered, &fields) != nil {
		return nil
	}
	origin := map[string]string{}
	for _, name := range unifiedVideoOriginNames {
		if value, ok := fields[name].(string); ok && value != "" {
			origin[name] = value
		}
	}
	return origin
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
