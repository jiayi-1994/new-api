package service

import (
	"math"
	"mime"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

// ApplyTaskVideoRequestFacts fills missing dashboard fields from the saved client
// request. It never changes billing facts or infers measured reference duration.
func ApplyTaskVideoRequestFacts(item *dto.TaskDto, record *model.TaskRequestBody) {
	var body map[string]any
	mediaType, _, _ := mime.ParseMediaType(record.ContentType)
	if mediaType == "multipart/form-data" {
		// The saved multipart snapshot contains name=value lines, not file bytes.
		body = make(map[string]any)
		for line := range strings.SplitSeq(record.Body, "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			if _, exists := body[key]; exists {
				body[key] = nil // Repeated scalar fields are ambiguous.
			} else {
				body[key] = value
			}
		}
	} else if err := common.UnmarshalJsonStr(record.Body, &body); err != nil {
		return
	}
	if len(body) == 0 {
		return
	}
	sources := []map[string]any{body}
	for _, key := range []string{"metadata", "parameters", "params", "input"} {
		nested, ok := body[key].(map[string]any)
		if value, isString := body[key].(string); isString {
			ok = common.UnmarshalJsonStr(value, &nested) == nil
		}
		if ok && nested != nil {
			sources = append(sources, nested)
		}
	}
	var seconds float64
	var resolution string
	var invalidSeconds, invalidResolution, hasImage, hasVideo bool
	for _, source := range sources {
		for _, key := range []string{"seconds", "duration"} {
			value, exists := source[key]
			if !exists {
				continue
			}
			candidate, ok := value.(float64)
			if text, isString := value.(string); isString {
				var err error
				candidate, err = strconv.ParseFloat(strings.TrimSpace(text), 64)
				ok = err == nil
			}
			if !ok || math.IsNaN(candidate) || candidate <= 0 || candidate > relaycommon.MaxTaskDurationSeconds || seconds > 0 && seconds != candidate {
				invalidSeconds = true
				continue
			}
			seconds = candidate
		}
		for _, key := range []string{"resolution", "video_resolution", "size", "video_size"} {
			value, exists := source[key]
			if !exists {
				continue
			}
			text, _ := value.(string)
			candidate, ok := billing_setting.CanonicalVideoTier(text)
			if !ok {
				widthText, heightText, found := strings.Cut(strings.ToLower(strings.TrimSpace(text)), "x")
				width, widthErr := strconv.Atoi(widthText)
				height, heightErr := strconv.Atoi(heightText)
				if found && widthErr == nil && heightErr == nil && width > 0 && height > 0 {
					candidate = billing_setting.VideoTierForHeight(min(width, height))
					ok = true
				}
			}
			if !ok || resolution != "" && resolution != candidate {
				invalidResolution = true
				continue
			}
			resolution = candidate
		}
		for _, key := range []string{"videos", "video_urls", "referenceVideos", "reference_videos", "video"} {
			hasVideo = hasVideo || taskVideoReferencePresent(source[key])
		}
		for _, key := range []string{"images", "image_urls", "referenceImages", "reference_images", "image", "input_reference", "first_image_url", "last_image_url", "first_image", "last_image"} {
			hasImage = hasImage || taskVideoReferencePresent(source[key])
		}
		content, _ := source["content"].([]any)
		for _, value := range content {
			part, _ := value.(map[string]any)
			switch part["type"] {
			case "video_url", "input_video":
				hasVideo = hasVideo || taskVideoReferencePresent(part["video_url"])
			case "image_url", "input_image":
				hasImage = hasImage || taskVideoReferencePresent(part["image_url"])
			}
		}
	}
	if item.Seconds <= 0 && !invalidSeconds {
		item.Seconds = seconds
	}
	if item.Resolution == "" && !invalidResolution {
		item.Resolution = resolution
	}
	if item.VideoMode == "" {
		switch {
		case hasVideo:
			item.VideoMode = constant.TaskActionReferenceToVideo
		case hasImage:
			item.VideoMode = constant.TaskActionImageToVideo
		case item.Action == constant.TaskActionTextToVideo || item.Action == constant.TaskActionImageToVideo:
			item.VideoMode = item.Action
		}
	}
}

func taskVideoReferencePresent(value any) bool {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		for _, item := range value {
			if taskVideoReferencePresent(item) {
				return true
			}
		}
	case map[string]any:
		if text, ok := value["url"].(string); ok {
			return strings.TrimSpace(text) != ""
		}
	}
	return false
}
