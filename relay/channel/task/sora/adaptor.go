package sora

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type"`                // "text" or "image_url"
	Text     string    `json:"text,omitempty"`      // for text type
	ImageURL *ImageURL `json:"image_url,omitempty"` // for image_url type
}

type ImageURL struct {
	URL string `json:"url"`
}

// flexString tolerates upstreams that send a JSON number where the OpenAI
// video API specifies a string (e.g. `"seconds": 15` from some relays).
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*s = ""
		return nil
	}
	if trimmed[0] == '"' {
		var str string
		if err := common.Unmarshal(trimmed, &str); err != nil {
			return err
		}
		*s = flexString(str)
		return nil
	}
	*s = flexString(trimmed)
	return nil
}

// flexInt tolerates upstreams that send a JSON float where the OpenAI video
// API specifies an integer (e.g. `"progress": 0.0` / `"progress": 42.5` from
// Python-backed relays). Fractions are truncated.
type flexInt int

func (i *flexInt) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*i = 0
		return nil
	}
	var f float64
	if err := common.Unmarshal(trimmed, &f); err != nil {
		return err
	}
	*i = flexInt(f)
	return nil
}

type responseTask struct {
	ID                 string     `json:"id"`
	TaskID             string     `json:"task_id,omitempty"` //兼容旧接口
	Object             string     `json:"object"`
	Model              string     `json:"model"`
	Status             string     `json:"status"`
	Progress           flexInt    `json:"progress"`
	CreatedAt          int64      `json:"created_at"`
	CompletedAt        int64      `json:"completed_at,omitempty"`
	ExpiresAt          int64      `json:"expires_at,omitempty"`
	Seconds            flexString `json:"seconds,omitempty"`
	Size               string     `json:"size,omitempty"`
	RemixedFromVideoID string     `json:"remixed_from_video_id,omitempty"`
	Error              *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

type normalizedSoraVideoRequest struct {
	Request   relaycommon.TaskSubmitReq
	Selection relaycommon.VideoBillingSelection
}

const normalizedSoraVideoRequestKey = "sora_normalized_video_request"

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func validateRemixRequest(c *gin.Context) *dto.TaskError {
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("field prompt is required"), "invalid_request", http.StatusBadRequest)
	}
	// 存储原始请求到 context，与 ValidateMultipartDirect 路径保持一致
	c.Set("task_request", req)
	return nil
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if info.Action == constant.TaskActionRemix {
		return validateRemixRequest(c)
	}
	return relaycommon.ValidateMultipartDirect(c, info)
}

func soraVideoBillingNotSupported(info *relaycommon.RelayInfo) *dto.TaskError {
	modelName := ""
	if info != nil {
		modelName = info.OriginModelName
	}
	return service.TaskErrorWrapperLocal(
		fmt.Errorf("video resolution unknown is not configured for model %s", modelName),
		"video_resolution_not_supported",
		http.StatusBadRequest,
	)
}

func resolveSoraResolution(upstreamModelName, size string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "720x1280", "1280x720":
		return "720p", true
	case "1024x1792", "1792x1024":
		if upstreamModelName == "sora-2" {
			return "", false
		}
		return "1024p", true
	default:
		return "", false
	}
}

func normalizeSoraVideoRequest(req relaycommon.TaskSubmitReq, upstreamModelName string) (normalizedSoraVideoRequest, error) {
	size := strings.ToLower(strings.TrimSpace(req.Size))
	if size == "" {
		size = "720x1280"
	}
	resolution, ok := resolveSoraResolution(upstreamModelName, size)
	if !ok {
		return normalizedSoraVideoRequest{}, fmt.Errorf("unsupported Sora size %q", size)
	}

	seconds := 0
	if req.Seconds != "" {
		var err error
		seconds, err = strconv.Atoi(strings.TrimSpace(req.Seconds))
		if err != nil {
			return normalizedSoraVideoRequest{}, fmt.Errorf("invalid Sora seconds: %w", err)
		}
	} else {
		seconds = req.Duration
	}
	if seconds == 0 {
		seconds = 4
	}
	if seconds < 1 || seconds > relaycommon.MaxTaskDurationSeconds {
		return normalizedSoraVideoRequest{}, fmt.Errorf("Sora seconds must be between 1 and %d", relaycommon.MaxTaskDurationSeconds)
	}

	req.Size = size
	req.Seconds = strconv.Itoa(seconds)
	req.Duration = 0
	req.Resolution = resolution
	return normalizedSoraVideoRequest{
		Request: req,
		Selection: relaycommon.VideoBillingSelection{
			EffectiveResolution:      resolution,
			EffectiveDurationSeconds: seconds,
		},
	}, nil
}

func (a *TaskAdaptor) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (relaycommon.VideoBillingSelection, *dto.TaskError) {
	if info.Action != constant.TaskActionRemix {
		req, err := relaycommon.GetTaskRequest(c)
		if err != nil {
			return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info)
		}
		normalized, err := normalizeSoraVideoRequest(req, info.UpstreamModelName)
		if err != nil {
			return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info)
		}
		c.Set(normalizedSoraVideoRequestKey, normalized)
		return normalized.Selection, nil
	}

	originValue, exists := c.Get("origin_task")
	if !exists {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info)
	}
	originTask, ok := originValue.(*model.Task)
	if !ok || originTask == nil {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info)
	}
	if billing := originTask.PrivateData.BillingContext; billing != nil && billing.PricingKind == "video_resolution" {
		selection := relaycommon.VideoBillingSelection{
			EffectiveResolution:      billing.EffectiveResolution,
			EffectiveDurationSeconds: billing.EffectiveDurationSeconds,
			IndependentRatios:        billing.IndependentRatios,
		}
		resolved, err := relaycommon.NewResolvedVideoBilling(selection, 1)
		if err == nil && (resolved.Selection.EffectiveResolution == "720p" || resolved.Selection.EffectiveResolution == "1024p") {
			return resolved.Selection, nil
		}
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info)
	}

	var saved responseTask
	if err := common.Unmarshal(originTask.Data, &saved); err != nil {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info)
	}
	resolution, ok := resolveSoraResolution(info.UpstreamModelName, saved.Size)
	if !ok || strings.TrimSpace(string(saved.Seconds)) == "" {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info)
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(string(saved.Seconds)))
	if err != nil || seconds < 1 || seconds > relaycommon.MaxTaskDurationSeconds {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info)
	}
	return relaycommon.VideoBillingSelection{
		EffectiveResolution:      resolution,
		EffectiveDurationSeconds: seconds,
	}, nil
}

// EstimateBilling 根据用户请求的 seconds 和 size 计算 OtherRatios。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	// remix 路径的 OtherRatios 已在 ResolveOriginTask 中设置
	if info.Action == constant.TaskActionRemix {
		return nil
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}

	seconds, _ := strconv.Atoi(req.Seconds)
	if seconds == 0 {
		seconds = req.Duration
	}
	if seconds <= 0 {
		seconds = 4
	}

	size := req.Size
	if size == "" {
		size = "720x1280"
	}

	ratios := map[string]float64{
		"seconds": float64(seconds),
		"size":    1,
	}
	if size == "1792x1024" || size == "1024x1792" {
		ratios["size"] = 1.666667
	}
	return ratios
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.Action == constant.TaskActionRemix {
		return fmt.Sprintf("%s/v1/videos/%s/remix", a.baseURL, info.OriginTaskID), nil
	}
	return fmt.Sprintf("%s/v1/videos", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	return nil
}

// wrapDashScopeVideoPayload rewrites an OpenAI-style video body for upstreams (e.g. meaicc
// sd-2-* Seedance) that only read prompt/media from a DashScope-like `input` object and
// knobs from `parameters`. Top-level fields are kept: such upstreams ignore unknown fields,
// while this gateway's own validation and per-second billing still read them. Existing
// dialect input is preserved, but parameters are always rebuilt from the normalized
// top-level values so client-supplied nested duration/resolution cannot bypass billing.
func wrapDashScopeVideoPayload(bodyMap map[string]any) {
	hasWrappedInput := false
	if input, ok := bodyMap["input"].(map[string]any); ok {
		if _, has := input["prompt"]; has {
			hasWrappedInput = true
		}
	}
	if !hasWrappedInput {
		input := map[string]any{}
		if prompt, _ := bodyMap["prompt"].(string); prompt != "" {
			input["prompt"] = prompt
		}
		if images, ok := bodyMap["images"].([]any); ok {
			media := make([]any, 0, len(images))
			for _, item := range images {
				if u, ok := item.(string); ok && u != "" {
					media = append(media, map[string]any{"type": "reference_image", "url": u})
				}
			}
			if len(media) > 0 {
				input["media"] = media
			}
		}
		bodyMap["input"] = input
	}

	parameters := map[string]any{"prompt_extend": false, "watermark": false}
	if resolution, _ := bodyMap["resolution"].(string); resolution != "" {
		parameters["resolution"] = strings.ToUpper(resolution)
	}
	if seconds, _ := bodyMap["seconds"].(string); seconds != "" {
		if n, err := strconv.Atoi(seconds); err == nil && n > 0 {
			parameters["duration"] = n
		}
	}
	if _, has := parameters["duration"]; !has {
		if duration, ok := bodyMap["duration"].(float64); ok && duration > 0 {
			parameters["duration"] = int(duration)
		}
	}
	if ratio, _ := bodyMap["aspect_ratio"].(string); ratio != "" {
		parameters["ratio"] = ratio
	}
	bodyMap["parameters"] = parameters
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, errors.Wrap(err, "get_request_body_failed")
	}
	cachedBody, err := storage.Bytes()
	if err != nil {
		return nil, errors.Wrap(err, "read_body_bytes_failed")
	}
	contentType := c.GetHeader("Content-Type")
	var normalized normalizedSoraVideoRequest
	if value, ok := c.Get(normalizedSoraVideoRequestKey); ok {
		normalized, _ = value.(normalizedSoraVideoRequest)
	}

	if strings.HasPrefix(contentType, "application/json") {
		var bodyMap map[string]interface{}
		if err := common.Unmarshal(cachedBody, &bodyMap); err == nil {
			bodyMap["model"] = info.UpstreamModelName
			if info.Action == constant.TaskActionRemix {
				delete(bodyMap, "size")
				delete(bodyMap, "seconds")
				delete(bodyMap, "duration")
				delete(bodyMap, "resolution")
			} else if normalized.Request.Size != "" {
				bodyMap["size"] = normalized.Request.Size
				bodyMap["seconds"] = normalized.Request.Seconds
				delete(bodyMap, "duration")
				delete(bodyMap, "resolution")
				if info.ChannelSetting.VideoPayloadFormat == "dashscope" {
					bodyMap["resolution"] = normalized.Selection.EffectiveResolution
				}
			}
			if info.ChannelSetting.VideoPayloadFormat == "dashscope" {
				wrapDashScopeVideoPayload(bodyMap)
			}
			if newBody, err := common.Marshal(bodyMap); err == nil {
				return bytes.NewReader(newBody), nil
			}
		}
		return bytes.NewReader(cachedBody), nil
	}

	if strings.Contains(contentType, "multipart/form-data") {
		formData, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return bytes.NewReader(cachedBody), nil
		}
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		writer.WriteField("model", info.UpstreamModelName)
		if info.Action != constant.TaskActionRemix && normalized.Request.Size != "" {
			writer.WriteField("size", normalized.Request.Size)
			writer.WriteField("seconds", normalized.Request.Seconds)
			if info.ChannelSetting.VideoPayloadFormat == "dashscope" {
				writer.WriteField("resolution", normalized.Selection.EffectiveResolution)
			}
		}
		for key, values := range formData.Value {
			isVideoParameter := key == "size" || key == "seconds" || key == "duration" || key == "resolution"
			if key == "model" || (isVideoParameter && (info.Action == constant.TaskActionRemix || normalized.Request.Size != "")) {
				continue
			}
			for _, v := range values {
				writer.WriteField(key, v)
			}
		}
		for fieldName, fileHeaders := range formData.File {
			for _, fh := range fileHeaders {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				ct := fh.Header.Get("Content-Type")
				if ct == "" || ct == "application/octet-stream" {
					buf512 := make([]byte, 512)
					n, _ := io.ReadFull(f, buf512)
					ct = http.DetectContentType(buf512[:n])
					// Re-open after sniffing so the full content is copied below
					f.Close()
					f, err = fh.Open()
					if err != nil {
						continue
					}
				}
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, fh.Filename))
				h.Set("Content-Type", ct)
				part, err := writer.CreatePart(h)
				if err != nil {
					f.Close()
					continue
				}
				io.Copy(part, f)
				f.Close()
			}
		}
		writer.Close()
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return &buf, nil
	}

	return common.ReaderOnly(storage), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Sora response
	var dResp responseTask
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	upstreamID := dResp.ID
	if upstreamID == "" {
		upstreamID = dResp.TaskID
	}
	if upstreamID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	// 使用公开 task_xxxx ID 返回给客户端
	dResp.ID = info.PublicTaskID
	dResp.TaskID = info.PublicTaskID
	c.JSON(http.StatusOK, dResp)
	return upstreamID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/v1/videos/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	status := strings.ToLower(strings.TrimSpace(resTask.Status))
	switch status {
	case "unknown":
		taskResult.Status = model.TaskStatusQueued
	case "submitted", "created":
		taskResult.Status = model.TaskStatusSubmitted
	case "queued", "pending":
		taskResult.Status = model.TaskStatusQueued
	case "processing", "in_progress", "running":
		// OpenAI-compatible relay upstreams may report the in-progress state
		// as "running" (often uppercase) instead of the official "in_progress".
		taskResult.Status = model.TaskStatusInProgress
	case "completed", "succeeded", "success":
		// Some OpenAI-compatible relay upstreams report the terminal success
		// state as "succeeded"/"success" instead of the official "completed".
		taskResult.Status = model.TaskStatusSuccess
		// Url intentionally left empty — the caller constructs the proxy URL using the public task ID
	case "failed", "cancelled", "canceled":
		taskResult.Status = model.TaskStatusFailure
		if resTask.Error != nil {
			taskResult.Reason = resTask.Error.Message
		} else {
			taskResult.Reason = "task failed"
		}
	default:
	}
	if resTask.Progress > 0 && resTask.Progress < 100 {
		taskResult.Progress = fmt.Sprintf("%d%%", resTask.Progress)
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	data := task.Data
	var err error
	if data, err = sjson.SetBytes(data, "id", task.TaskID); err != nil {
		return nil, errors.Wrap(err, "set id failed")
	}
	// 上游任务 JSON 可能携带上游视频直链与上游任务 ID（url/result_url/metadata.url 等），
	// 对外统一映射为本站签名代理地址，不暴露上游。存储的 task.Data 保持原样，
	// 供 /content 代理端点解析上游直链。附带 video_token 能力签名，
	// 浏览器 <video>/<a> 无法携带 Authorization 头也能访问。
	// meaicc 类中转把签名直链塞在非标准 `object` 字段（官方值应为字面量 "video"），
	// 仅当其值形如 http(s) URL 时一并改写，避免破坏正常的 "video" 字面量。
	rewritePaths := []string{"url", "video_url", "result_url", "output_url", "metadata.url", "metadata.origin_video_url"}
	if obj := strings.TrimSpace(gjson.GetBytes(data, "object").String()); strings.HasPrefix(obj, "http://") || strings.HasPrefix(obj, "https://") {
		rewritePaths = append(rewritePaths, "object")
	}
	taskSucceeded := task.Status == model.TaskStatusSuccess
	proxyURL := ""
	for _, path := range rewritePaths {
		field := gjson.GetBytes(data, path)
		if !field.Exists() {
			continue
		}
		// 部分中转在任务未完成时就带空的 url/result_url 占位键(官方完成前不带这些键)。
		// 未成功且原值为空时保持空值,否则客户端会把 queued 任务误判为已出结果。
		if !taskSucceeded && strings.TrimSpace(field.String()) == "" {
			continue
		}
		if proxyURL == "" {
			proxyURL = taskcommon.BuildProxyURL(task.TaskID)
			if token, _, tokenErr := service.IssueVideoContentToken(task.TaskID, task.UserId, task.ID); tokenErr == nil {
				proxyURL += "?video_token=" + url.QueryEscape(token)
			}
		}
		if data, err = sjson.SetBytes(data, path, proxyURL); err != nil {
			return nil, errors.Wrapf(err, "rewrite %s failed", path)
		}
	}
	if gjson.GetBytes(data, "task_id").Exists() {
		if data, err = sjson.SetBytes(data, "task_id", task.TaskID); err != nil {
			return nil, errors.Wrap(err, "rewrite task_id failed")
		}
	}
	return data, nil
}
