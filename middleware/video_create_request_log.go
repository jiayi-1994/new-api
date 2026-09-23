package middleware

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

// VideoCreateRequestDebugLog records the incoming video parameter names and
// resolution-related values before distribution or channel conversion. Prompt,
// credentials, and reference URLs are never included in the log.
func VideoCreateRequestDebugLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.DebugEnabled || c.Request.Method != http.MethodPost || c.Request.URL.Path != "/v1/videos" {
			c.Next()
			return
		}

		fields := make(map[string]any)
		references := make(map[string]int)
		if strings.Contains(c.ContentType(), "multipart/form-data") {
			form, err := common.ParseMultipartFormReusable(c)
			if err != nil {
				logger.LogDebug(c, "video_create_request content_type=%q parse_error=true", c.ContentType())
				c.Next()
				return
			}
			defer form.RemoveAll()
			for name, values := range form.Value {
				if len(values) == 1 {
					fields[name] = values[0]
				} else {
					fields[name] = values
				}
			}
			for name, files := range form.File {
				references[name] = len(files)
				if _, exists := fields[name]; !exists {
					fields[name] = nil
				}
			}
		} else if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
			logger.LogDebug(c, "video_create_request content_type=%q parse_error=true", c.ContentType())
			c.Next()
			return
		}

		fieldNames := make([]string, 0, len(fields))
		for name := range fields {
			fieldNames = append(fieldNames, name)
		}
		sort.Strings(fieldNames)

		result := map[string]any{
			"content_type": c.ContentType(),
			"fields":       fieldNames,
		}
		parameters, _ := fields["parameters"].(map[string]any)
		metadata, _ := fields["metadata"].(map[string]any)
		for _, scope := range []struct {
			name   string
			fields map[string]any
		}{
			{name: "params", fields: fields},
			{name: "parameters", fields: parameters},
			{name: "metadata", fields: metadata},
		} {
			params := make(map[string]any)
			for _, name := range []string{
				"model", "duration", "durationSeconds", "duration_seconds", "seconds",
				"size", "video_size", "ratio", "aspect_ratio", "resolution", "video_resolution",
				"width", "height",
			} {
				value, exists := scope.fields[name]
				if !exists {
					continue
				}
				switch typed := value.(type) {
				case string:
					if strings.Contains(typed, "://") || strings.Contains(strings.ToLower(typed), "token=") {
						params[name] = "[redacted]"
					} else if len(typed) > 128 {
						params[name] = typed[:128] + "..."
					} else {
						params[name] = typed
					}
				case float64, bool, nil:
					params[name] = typed
				default:
					params[name] = fmt.Sprintf("[%T]", value)
				}
			}
			if len(params) > 0 {
				result[scope.name] = params
			}
		}

		for _, name := range []string{
			"referenceImages", "referenceVideos", "referenceAudios",
			"reference_images", "reference_videos", "reference_audios",
			"images", "videos", "audios", "input_reference",
		} {
			if _, exists := references[name]; exists {
				continue
			}
			value, exists := fields[name]
			if !exists {
				continue
			}
			switch typed := value.(type) {
			case []any:
				references[name] = len(typed)
			case []string:
				references[name] = len(typed)
			case string:
				if strings.TrimSpace(typed) != "" {
					references[name] = 1
				} else {
					references[name] = 0
				}
			case nil:
				references[name] = 0
			default:
				references[name] = 1
			}
		}
		if len(references) > 0 {
			result["references"] = references
		}

		encoded, err := common.Marshal(result)
		if err == nil {
			logger.LogDebug(c, "video_create_request %s", encoded)
		}
		c.Next()
	}
}
