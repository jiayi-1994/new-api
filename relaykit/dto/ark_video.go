package dto

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

// Volcengine Ark content generation task protocol
// (POST /api/v3/contents/generations/tasks, GET .../tasks/{id}), served by
// translating to and from the OpenAI video protocol.

const (
	ArkVideoStatusQueued    = "queued"
	ArkVideoStatusRunning   = "running"
	ArkVideoStatusSucceeded = "succeeded"
	ArkVideoStatusFailed    = "failed"

	ArkVideoErrorInvalidParameter = "InvalidParameter"
	ArkVideoErrorAuthentication   = "AuthenticationError"
	ArkVideoErrorInternal         = "InternalServiceError"
)

type ArkVideoTask struct {
	ID         string           `json:"id"`
	Model      string           `json:"model,omitempty"`
	Status     string           `json:"status,omitempty"`
	Content    *ArkVideoContent `json:"content,omitempty"`
	Usage      *ArkVideoUsage   `json:"usage,omitempty"`
	Duration   *int             `json:"duration,omitempty"`
	Resolution string           `json:"resolution,omitempty"`
	CreatedAt  int64            `json:"created_at,omitempty"`
	UpdatedAt  int64            `json:"updated_at,omitempty"`
	Error      *ArkVideoError   `json:"error,omitempty"`
}

type ArkVideoContent struct {
	VideoURL string `json:"video_url"`
}

type ArkVideoUsage struct {
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type ArkVideoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ArkVideoErrorResponse struct {
	Error ArkVideoError `json:"error"`
}

// arkPromptCommand matches Ark's legacy in-prompt parameters. They would let the
// upstream generate something other than the JSON fields the gateway prices.
var arkPromptCommand = regexp.MustCompile(`(?i)(^|\s)--(rs|resolution|dur|duration|rt|ratio|seed|cf|camerafixed|wm|watermark|fps|frames)\b`)

// ConvertArkVideoRequest turns an Ark create-task body into the equivalent
// OpenAI video create body. Only documented Ark fields are accepted, so no
// alias of a billing quantity can bypass the duration bound. Fields equal to
// Ark defaults are dropped because they change nothing upstream yet many video
// providers reject them. maxSeconds is the host's task duration bound.
func ConvertArkVideoRequest(body []byte, maxSeconds int) (map[string]any, error) {
	var input map[string]any
	if err := kitutil.Unmarshal(body, &input); err != nil || input == nil {
		return nil, errors.New("request body must be a JSON object")
	}
	for name, value := range input {
		if value == nil {
			delete(input, name)
		}
	}
	output := map[string]any{}
	for name, value := range input {
		switch name {
		case "model", "resolution", "ratio":
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("%s must be a non-empty string", name)
			}
			output[name] = text
		case "content":
			if err := convertArkVideoContent(value, output); err != nil {
				return nil, err
			}
		case "duration":
			seconds, ok := value.(float64)
			if !ok || seconds != math.Trunc(seconds) || seconds < 1 || seconds > float64(maxSeconds) {
				return nil, fmt.Errorf("duration must be an integer from 1 to %d seconds", maxSeconds)
			}
			output["seconds"] = int(seconds)
		case "generate_audio":
			flag, ok := value.(bool)
			if !ok {
				return nil, errors.New("generate_audio must be a boolean")
			}
			output[name] = flag
		case "watermark", "camera_fixed":
			flag, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("%s must be a boolean", name)
			}
			if flag {
				output[name] = true
			}
		case "seed":
			seed, ok := value.(float64)
			if !ok || seed != math.Trunc(seed) || seed < -1 || seed > math.MaxUint32 {
				return nil, errors.New("seed must be an integer from -1 to 4294967295")
			}
			if seed != -1 {
				output[name] = int64(seed)
			}
		case "return_last_frame", "draft":
			flag, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("%s must be a boolean", name)
			}
			if flag {
				return nil, fmt.Errorf("%s is not supported", name)
			}
		case "service_tier":
			if value != "default" {
				return nil, errors.New("service_tier is not supported; omit it or use default")
			}
		case "callback_url":
			if value != "" {
				return nil, errors.New("callback_url is not supported; poll the task instead")
			}
		default:
			// frames, execution_expires_after and non-Ark fields such as seconds.
			return nil, fmt.Errorf("unsupported parameter: %s", name)
		}
	}
	if _, ok := output["model"]; !ok {
		return nil, errors.New("model is required")
	}
	return output, nil
}

func convertArkVideoContent(value any, output map[string]any) error {
	items, ok := value.([]any)
	if !ok {
		return errors.New("content must be an array")
	}
	var texts, images, videos, audios []string
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return errors.New("content entries must be objects")
		}
		kind, _ := item["type"].(string)
		role, _ := item["role"].(string)
		if roleValue, present := item["role"]; present && roleValue != nil {
			if _, isString := roleValue.(string); !isString {
				return errors.New("content role must be a string")
			}
		}
		if kind == "text" {
			text, ok := item["text"].(string)
			if !ok {
				return errors.New("content text entries must carry text")
			}
			if strings.TrimSpace(text) != "" {
				texts = append(texts, text)
			}
			continue
		}
		if kind == "draft_task" {
			return errors.New("draft_task is not supported; resubmit the draft as a new request")
		}
		if kind != "image_url" && kind != "video_url" && kind != "audio_url" {
			return errors.New("content supports only text, image_url, video_url and audio_url entries")
		}
		url := arkVideoMediaURL(item[kind])
		if url == "" {
			return fmt.Errorf("content %s entries must carry a url", kind)
		}
		switch {
		case kind == "image_url" && (role == "first_frame" || role == "last_frame"):
			field := "first_image_url"
			if role == "last_frame" {
				field = "last_image_url"
			}
			if _, exists := output[field]; exists {
				return fmt.Errorf("content accepts one %s image", role)
			}
			output[field] = url
		case kind == "image_url" && (role == "" || role == "reference_image"):
			images = append(images, url)
		case kind == "video_url" && (role == "" || role == "reference_video"):
			videos = append(videos, url)
		case kind == "audio_url" && (role == "" || role == "reference_audio"):
			audios = append(audios, url)
		default:
			return fmt.Errorf("content %s does not support role %q", kind, role)
		}
	}
	if len(texts) > 0 {
		prompt := strings.Join(texts, "\n")
		if arkPromptCommand.MatchString(prompt) {
			return errors.New("prompt parameters such as --dur or --rs are not supported; use the JSON fields instead")
		}
		output["prompt"] = prompt
	}
	for field, urls := range map[string][]string{"images": images, "videos": videos, "audios": audios} {
		if len(urls) > 0 {
			output[field] = urls
		}
	}
	return nil
}

// arkVideoMediaURL accepts both {"url": "..."} and a bare string.
func arkVideoMediaURL(value any) string {
	if media, ok := value.(map[string]any); ok {
		value = media["url"]
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

// ArkVideoTaskFromOpenAI projects an OpenAI video object onto an Ark task.
// Any URL inside the OpenAI object is ignored: the host passes the only
// public download link the client may see.
func ArkVideoTaskFromOpenAI(video OpenAIVideo, videoURL string) ArkVideoTask {
	task := ArkVideoTask{
		ID:        video.ID,
		Model:     video.Model,
		Status:    ArkVideoStatusQueued,
		CreatedAt: video.CreatedAt,
		UpdatedAt: video.CreatedAt,
	}
	if video.CompletedAt > 0 {
		task.UpdatedAt = video.CompletedAt
	}
	switch video.Status {
	case VideoStatusInProgress:
		task.Status = ArkVideoStatusRunning
	case VideoStatusCompleted:
		task.Status = ArkVideoStatusSucceeded
		if videoURL != "" {
			task.Content = &ArkVideoContent{VideoURL: videoURL}
		}
	case VideoStatusFailed:
		task.Status = ArkVideoStatusFailed
		task.Error = &ArkVideoError{Code: ArkVideoErrorInternal, Message: "video generation failed"}
		if video.Error != nil {
			if video.Error.Code != "" {
				task.Error.Code = video.Error.Code
			}
			if video.Error.Message != "" {
				task.Error.Message = video.Error.Message
			}
		}
	}
	if seconds, err := strconv.Atoi(video.Seconds); err == nil && seconds > 0 {
		task.Duration = &seconds
	}
	if resolution, ok := video.Metadata["resolution"].(string); ok {
		task.Resolution = resolution
	}
	if video.VideoTokens != nil && *video.VideoTokens > 0 && *video.VideoTokens < math.MaxInt32 {
		tokens := int64(math.Round(*video.VideoTokens))
		task.Usage = &ArkVideoUsage{CompletionTokens: tokens, TotalTokens: tokens}
	}
	return task
}

// ArkVideoErrorFromBody reads either error shape the gateway emits, the OpenAI
// {"error":{...}} envelope or a top-level {"code","message"} task error, and
// returns the Ark error envelope for the same HTTP status.
func ArkVideoErrorFromBody(status int, body []byte) ArkVideoErrorResponse {
	var parsed struct {
		Error   any    `json:"error"`
		Code    any    `json:"code"`
		Message string `json:"message"`
	}
	_ = kitutil.Unmarshal(body, &parsed)
	code, message := fmt.Sprint(valueOrEmpty(parsed.Code)), parsed.Message
	switch value := parsed.Error.(type) {
	case map[string]any:
		code = fmt.Sprint(valueOrEmpty(value["code"]))
		message, _ = value["message"].(string)
	case string:
		message = value
	}
	if code == "" {
		switch {
		case status == 401:
			code = ArkVideoErrorAuthentication
		case status >= 500:
			code = ArkVideoErrorInternal
		default:
			code = ArkVideoErrorInvalidParameter
		}
	}
	if message == "" {
		// A short plain-text body is the message itself; anything else is opaque.
		message = strings.TrimSpace(string(body))
		if message == "" || strings.HasPrefix(message, "{") || len(message) > 512 {
			message = "request failed"
		}
	}
	return ArkVideoErrorResponse{Error: ArkVideoError{Code: code, Message: message}}
}

func valueOrEmpty(value any) any {
	if value == nil {
		return ""
	}
	return value
}
