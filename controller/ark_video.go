package controller

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	videodto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// The Volcengine Ark video task protocol is served by the OpenAI video
// handler chain: requests are rewritten to /v1/videos so scheduling, unified
// sales and billing see exactly the equivalent OpenAI request, and responses
// are rewritten back to the Ark shape.
const (
	ArkVideoTasksPath = "/api/v3/contents/generations/tasks"
	ArkVideoTaskPath  = ArkVideoTasksPath + "/:task_id"
)

// arkVideoResponseWriter holds the downstream response until the Ark
// envelope has rewritten it.
type arkVideoResponseWriter struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
}

func (w *arkVideoResponseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *arkVideoResponseWriter) WriteHeaderNow() {}

func (w *arkVideoResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *arkVideoResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *arkVideoResponseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *arkVideoResponseWriter) Size() int {
	if w.status == 0 && w.body.Len() == 0 {
		return -1
	}
	return w.body.Len()
}

func (w *arkVideoResponseWriter) Written() bool {
	return w.status != 0 || w.body.Len() > 0
}

func (w *arkVideoResponseWriter) Flush() {}

// ArkVideoEnvelope runs before authentication and never reads the request
// body: it points the request at the OpenAI video route and renders whatever
// the chain answers, including authentication failures, in the Ark shape.
func ArkVideoEnvelope() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := "/v1/videos"
		retrieve := c.Request.Method == http.MethodGet
		if retrieve {
			path += "/" + url.PathEscape(c.Param("task_id"))
		}
		c.Request.URL.Path, c.Request.URL.RawPath, c.Request.RequestURI = path, "", path

		original := c.Writer
		buffered := &arkVideoResponseWriter{ResponseWriter: original}
		c.Writer = buffered
		defer func() {
			if recovered := recover(); recovered != nil {
				// Let the outer recovery answer on the real writer.
				c.Writer = original
				panic(recovered)
			}
		}()
		c.Next()
		c.Writer = original

		status := buffered.Status()
		var payload any
		switch {
		case status >= http.StatusBadRequest:
			payload = videodto.ArkVideoErrorFromBody(status, buffered.body.Bytes())
		default:
			var video videodto.OpenAIVideo
			if err := common.Unmarshal(buffered.body.Bytes(), &video); err != nil || video.ID == "" {
				logger.LogError(c, "ark video: downstream returned an unreadable video object")
				status = http.StatusInternalServerError
				payload = videodto.ArkVideoErrorResponse{Error: videodto.ArkVideoError{Code: videodto.ArkVideoErrorInternal, Message: "unexpected video task response"}}
			} else if retrieve {
				payload = videodto.ArkVideoTaskFromOpenAI(video, arkVideoContentURL(c, video))
			} else {
				payload = map[string]string{"id": video.ID}
			}
		}
		encoded, err := common.Marshal(payload)
		if err != nil {
			status, encoded = http.StatusInternalServerError, []byte(`{"error":{"code":"InternalServiceError","message":"failed to encode response"}}`)
		}
		header := original.Header()
		header.Del("Content-Length")
		header.Set("Content-Type", "application/json")
		original.WriteHeader(status)
		_, _ = original.Write(encoded)
	}
}

// ArkVideoRequest runs after authentication. It converts the Ark create body
// into the equivalent OpenAI video body and bounds its only billing
// multiplier, duration, before any plugin or scheduler sees it.
func ArkVideoRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			abortArkVideo(c, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			abortArkVideo(c, http.StatusBadRequest, "failed to read request body")
			return
		}
		raw, err := storage.Bytes()
		if err != nil {
			abortArkVideo(c, http.StatusBadRequest, "failed to read request body")
			return
		}
		converted, err := videodto.ConvertArkVideoRequest(raw, relaycommon.MaxTaskDurationSeconds)
		if err != nil {
			abortArkVideo(c, http.StatusBadRequest, err.Error())
			return
		}
		body, err := common.Marshal(converted)
		if err != nil {
			abortArkVideo(c, http.StatusInternalServerError, "failed to encode video request")
			return
		}
		replacement, err := common.CreateBodyStorage(body)
		if err != nil {
			abortArkVideo(c, http.StatusInternalServerError, "failed to store video request")
			return
		}
		_ = storage.Close()
		c.Set(common.KeyBodyStorage, replacement)
		c.Set(common.KeyRequestBody, nil)
		c.Request.Body = io.NopCloser(replacement)
		c.Request.ContentLength = int64(len(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Next()
	}
}

func abortArkVideo(c *gin.Context, status int, message string) {
	code := videodto.ArkVideoErrorInvalidParameter
	if status >= http.StatusInternalServerError {
		code = videodto.ArkVideoErrorInternal
	}
	c.AbortWithStatusJSON(status, videodto.ArkVideoErrorResponse{Error: videodto.ArkVideoError{Code: code, Message: message}})
}

// arkVideoContentURL is the credential-free capability link of the task's
// video. It never comes from the rendered object, which may carry upstream
// links.
func arkVideoContentURL(c *gin.Context, video videodto.OpenAIVideo) string {
	if video.Status != videodto.VideoStatusCompleted {
		return ""
	}
	task, exists, err := model.GetByTaskId(c.GetInt("id"), video.ID)
	if err != nil || !exists || task == nil {
		return ""
	}
	key := ""
	if artifacts, err := projectTaskArtifacts(task); err == nil {
		for _, artifact := range artifacts {
			if artifact.Type == "video" {
				key = artifact.Key
				break
			}
		}
	}
	if key == "" && legacyVideoAvailable(task) {
		key = "video"
	}
	if key == "" {
		logger.LogWarn(c, "ark video: completed task has no video artifact: "+task.TaskID)
		return ""
	}
	contentURL, err := service.BuildTaskArtifactContentURL(task.TaskID, key)
	if err != nil {
		logger.LogError(c, "ark video: build content URL failed: "+err.Error())
		return ""
	}
	return contentURL
}
