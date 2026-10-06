package service

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	videodto "github.com/QuantumNous/new-api/relaykit/dto"
)

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
		item.FailReason = "Video generation failed"
	}
}

// BuildUnifiedVideoResponse uses only host lifecycle and frozen sale fields.
// Provider renderers may include arbitrary metadata and upstream URLs.
func BuildUnifiedVideoResponse(task *model.Task) *videodto.OpenAIVideo {
	video := task.ToOpenAIVideo()
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
	video.Error = &videodto.OpenAIVideoError{Code: "video_generation_failed", Message: "Video generation failed"}
	// Publish only a stable category, never the provider's failure details.
	// Older untracked tasks lack the host marker, but retain these poll reasons.
	if task.VideoHealthAttribution == "host" || task.VideoHealthAttribution == taskAttributionHostLag ||
		strings.HasPrefix(task.FailReason, "poll failed:") || strings.HasPrefix(task.FailReason, "upstream task not found (HTTP ") ||
		GetTaskAdaptorFunc == nil {
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
