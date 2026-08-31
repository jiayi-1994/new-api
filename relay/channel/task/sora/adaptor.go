package sora

import (
	"bytes"
	"fmt"
	"io"
	"math"
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
	Request    relaycommon.TaskSubmitReq
	Selection  relaycommon.VideoBillingSelection
	Parameters map[string]any
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

// soraVideoBillingNotSupported builds the strict 400 required by the resolution
// pricing contract. resolution is the canonical effective resolution when the
// adapter could determine one; pass "" when it is genuinely undeterminable.
func soraVideoBillingNotSupported(info *relaycommon.RelayInfo, resolution string) *dto.TaskError {
	modelName := ""
	if info != nil {
		modelName = info.OriginModelName
	}
	if resolution == "" {
		resolution = "unknown"
	}
	return service.TaskErrorWrapperLocal(
		fmt.Errorf("video resolution %s is not configured for model %s", resolution, modelName),
		"video_resolution_not_supported",
		http.StatusBadRequest,
	)
}

// soraUnsupportedResolutionError marks a request whose resolution was
// recognized (canonical name known) but is not supported by the upstream
// model, so the strict 400 can name it instead of reporting "unknown".
type soraUnsupportedResolutionError struct {
	resolution string
}

func (e *soraUnsupportedResolutionError) Error() string {
	return fmt.Sprintf("unsupported Sora resolution %q", e.resolution)
}

// resolveSoraResolution maps a size string to its canonical resolution.
// When the size is recognizable but unsupported for the model, the canonical
// name is still returned alongside ok=false so error messages can cite it.
func resolveSoraResolution(upstreamModelName, size string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "720x1280", "1280x720":
		return "720p", true
	case "1024x1792", "1792x1024":
		return "1024p", upstreamModelName != "sora-2"
	default:
		return "", false
	}
}

// resolveSoraTier validates and canonicalizes an explicit resolution. The
// shared billing plan owns tier availability and rejects unconfigured keys.
func resolveSoraTier(rawResolution string) (string, bool) {
	resolution, err := common.NormalizeVideoResolutionKey(rawResolution)
	if err != nil {
		return "", false
	}
	return resolution, true
}

func mergeSoraResolution(current *string, candidate string) error {
	if *current == "" {
		*current = candidate
		return nil
	}
	if *current != candidate {
		return fmt.Errorf("conflicting Sora resolutions %s and %s", *current, candidate)
	}
	return nil
}

func parseSoraDuration(value any) (int, error) {
	seconds, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	if err != nil || seconds < 1 || seconds > relaycommon.MaxTaskDurationSeconds {
		return 0, fmt.Errorf("Sora seconds must be between 1 and %d", relaycommon.MaxTaskDurationSeconds)
	}
	return seconds, nil
}

func mergeSoraDuration(current *int, candidate int) error {
	if *current == 0 {
		*current = candidate
		return nil
	}
	if *current != candidate {
		return fmt.Errorf("conflicting Sora durations %d and %d", *current, candidate)
	}
	return nil
}

func readSoraDashScopeParameters(c *gin.Context) (map[string]any, error) {
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return nil, nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	value, exists := payload["parameters"]
	if !exists {
		return nil, nil
	}
	parameters, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Sora parameters must be an object")
	}
	clone := make(map[string]any, len(parameters))
	for key, parameter := range parameters {
		clone[key] = parameter
	}
	return clone, nil
}

// Explicit top-level and DashScope nested values are coequal constraints:
// one source supplies the value, equivalent normalized values merge, and any
// conflict is rejected. Provider defaults apply only when every source omits it.
func normalizeSoraVideoRequest(
	req relaycommon.TaskSubmitReq,
	upstreamModelName string,
	parameters map[string]any,
) (normalizedSoraVideoRequest, error) {
	size := strings.ToLower(strings.TrimSpace(req.Size))
	resolution := ""
	hasExplicitResolution := strings.TrimSpace(req.Resolution) != ""
	if size != "" {
		sizeResolution, ok := resolveSoraResolution(upstreamModelName, size)
		if !ok {
			if sizeResolution != "" {
				return normalizedSoraVideoRequest{}, &soraUnsupportedResolutionError{resolution: sizeResolution}
			}
			return normalizedSoraVideoRequest{}, fmt.Errorf("unsupported Sora size %q", size)
		}
		resolution = sizeResolution
	}
	if strings.TrimSpace(req.Resolution) != "" {
		topResolution, ok := resolveSoraTier(req.Resolution)
		if !ok {
			if topResolution != "" {
				return normalizedSoraVideoRequest{}, &soraUnsupportedResolutionError{resolution: topResolution}
			}
			return normalizedSoraVideoRequest{}, fmt.Errorf("unsupported Sora resolution %q", req.Resolution)
		}
		if err := mergeSoraResolution(&resolution, topResolution); err != nil {
			return normalizedSoraVideoRequest{}, err
		}
	}
	if nestedResolution, exists := parameters["resolution"]; exists {
		hasExplicitResolution = true
		nestedResolutionString, ok := nestedResolution.(string)
		if !ok {
			return normalizedSoraVideoRequest{}, fmt.Errorf("Sora resolution must be a string")
		}
		canonical, ok := resolveSoraTier(nestedResolutionString)
		if !ok {
			if canonical != "" {
				return normalizedSoraVideoRequest{}, &soraUnsupportedResolutionError{resolution: canonical}
			}
			return normalizedSoraVideoRequest{}, fmt.Errorf("unsupported Sora resolution %q", nestedResolutionString)
		}
		if err := mergeSoraResolution(&resolution, canonical); err != nil {
			return normalizedSoraVideoRequest{}, err
		}
	}
	if resolution == "" {
		resolution = "720p"
	}
	if size == "" && !hasExplicitResolution {
		if resolution == "1024p" {
			size = "1024x1792"
		} else {
			size = "720x1280"
		}
	}

	seconds := 0
	if strings.TrimSpace(req.Seconds) != "" {
		value, err := parseSoraDuration(req.Seconds)
		if err != nil {
			return normalizedSoraVideoRequest{}, err
		}
		seconds = value
	}
	if req.Duration != 0 {
		value, err := parseSoraDuration(req.Duration)
		if err != nil {
			return normalizedSoraVideoRequest{}, err
		}
		if err := mergeSoraDuration(&seconds, value); err != nil {
			return normalizedSoraVideoRequest{}, err
		}
	}
	if nestedDuration, exists := parameters["duration"]; exists {
		value, err := parseSoraDuration(nestedDuration)
		if err != nil {
			return normalizedSoraVideoRequest{}, err
		}
		if err := mergeSoraDuration(&seconds, value); err != nil {
			return normalizedSoraVideoRequest{}, err
		}
	}
	if seconds == 0 {
		seconds = 4
	}

	req.Size = size
	req.Seconds = strconv.Itoa(seconds)
	req.Duration = 0
	req.Resolution = resolution
	if parameters != nil {
		parameters["resolution"] = strings.ToUpper(resolution)
		parameters["duration"] = seconds
	}
	return normalizedSoraVideoRequest{
		Request: req,
		Selection: relaycommon.VideoBillingSelection{
			EffectiveResolution:      resolution,
			EffectiveDurationSeconds: seconds,
		},
		Parameters: parameters,
	}, nil
}

func (a *TaskAdaptor) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (relaycommon.VideoBillingSelection, *dto.TaskError) {
	if info.Action != constant.TaskActionRemix {
		req, err := relaycommon.GetTaskRequest(c)
		if err != nil {
			return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, "")
		}
		var parameters map[string]any
		if info.ChannelSetting.VideoPayloadFormat == "dashscope" {
			parameters, err = readSoraDashScopeParameters(c)
			if err != nil {
				return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, "")
			}
		}
		normalized, err := normalizeSoraVideoRequest(req, info.UpstreamModelName, parameters)
		if err != nil {
			var unsupported *soraUnsupportedResolutionError
			if errors.As(err, &unsupported) {
				return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, unsupported.resolution)
			}
			return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, "")
		}
		c.Set(normalizedSoraVideoRequestKey, normalized)
		return normalized.Selection, nil
	}

	originValue, exists := c.Get("origin_task")
	if !exists {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, "")
	}
	originTask, ok := originValue.(*model.Task)
	if !ok || originTask == nil {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, "")
	}
	if billing := originTask.PrivateData.BillingContext; billing != nil && billing.PricingKind == "video_resolution" {
		selection := relaycommon.VideoBillingSelection{
			EffectiveResolution:      billing.EffectiveResolution,
			EffectiveDurationSeconds: billing.EffectiveDurationSeconds,
			IndependentRatios:        billing.IndependentRatios,
		}
		resolved, err := relaycommon.NewResolvedVideoBilling(selection, 1)
		if err == nil {
			return resolved.Selection, nil
		}
		snapshotResolution := ""
		if canonical, normalizeErr := common.NormalizeVideoResolutionKey(billing.EffectiveResolution); normalizeErr == nil {
			snapshotResolution = canonical
		}
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, snapshotResolution)
	}

	var saved responseTask
	if err := common.Unmarshal(originTask.Data, &saved); err != nil {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, "")
	}
	resolution, ok := resolveSoraResolution(info.UpstreamModelName, saved.Size)
	if !ok || strings.TrimSpace(string(saved.Seconds)) == "" {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, resolution)
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(string(saved.Seconds)))
	if err != nil || seconds < 1 || seconds > relaycommon.MaxTaskDurationSeconds {
		return relaycommon.VideoBillingSelection{}, soraVideoBillingNotSupported(info, resolution)
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
// dialect input and unrelated parameters are preserved, while duration/resolution
// are overwritten from the shared normalized values used for billing.
func wrapDashScopeVideoPayload(bodyMap map[string]any) {
	input := map[string]any{}
	if existing, ok := bodyMap["input"].(map[string]any); ok {
		for key, value := range existing {
			input[key] = value
		}
	}
	if _, exists := input["prompt"]; !exists {
		if prompt, _ := bodyMap["prompt"].(string); prompt != "" {
			input["prompt"] = prompt
		}
	}
	if _, exists := input["media"]; !exists {
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
	}
	bodyMap["input"] = input

	parameters := map[string]any{}
	if existing, ok := bodyMap["parameters"].(map[string]any); ok {
		for key, value := range existing {
			parameters[key] = value
		}
	}
	if _, exists := parameters["prompt_extend"]; !exists {
		parameters["prompt_extend"] = false
	}
	if _, exists := parameters["watermark"]; !exists {
		parameters["watermark"] = false
	}
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

// megabyaiHost marks upstreams that only accept their own camelCase video dialect.
const megabyaiHost = "megabyai.cc"

// megabyaiReferenceURLs collects reference media URLs for one modality. Keys are
// tried in order against the body and then against `metadata`, so a client may
// speak the gateway's snake_case/plural aliases or megabyai's own camelCase key.
// Both a JSON array and a bare string are accepted for each key.
func megabyaiReferenceURLs(bodyMap map[string]any, keys ...string) []string {
	scopes := []map[string]any{bodyMap}
	if metadata, ok := bodyMap["metadata"].(map[string]any); ok {
		scopes = append(scopes, metadata)
	}
	urls := make([]string, 0, 2)
	for _, scope := range scopes {
		for _, key := range keys {
			switch value := scope[key].(type) {
			case string:
				if trimmed := strings.TrimSpace(value); trimmed != "" {
					urls = append(urls, trimmed)
				}
			case []any:
				for _, item := range value {
					if u, ok := item.(string); ok {
						if trimmed := strings.TrimSpace(u); trimmed != "" {
							urls = append(urls, trimmed)
						}
					}
				}
			}
			if len(urls) > 0 {
				return urls
			}
		}
	}
	return nil
}

// buildMegabyaiVideoPayload rebuilds an OpenAI-style /v1/videos body into the
// exact payload megabyai documents. The upstream validates strictly, so the
// result carries only its own keys instead of being layered onto the original
// body the way the DashScope dialect is. OpenAI's `size` ("720x1280") is split
// into megabyai's `ratio` + `resolution`; a client that already sends
// ratio/resolution/referenceX wins over anything derived.
func buildMegabyaiVideoPayload(bodyMap map[string]any) map[string]any {
	payload := map[string]any{"model": bodyMap["model"]}
	if prompt, _ := bodyMap["prompt"].(string); prompt != "" {
		payload["prompt"] = prompt
	}

	duration := 0
	if seconds, _ := bodyMap["seconds"].(string); seconds != "" {
		duration, _ = strconv.Atoi(seconds)
	}
	if duration <= 0 {
		switch seconds := bodyMap["seconds"].(type) {
		case float64:
			duration = int(seconds)
		}
	}
	if duration <= 0 {
		if raw, ok := bodyMap["duration"].(float64); ok {
			duration = int(raw)
		} else if raw, ok := bodyMap["duration"].(string); ok {
			duration, _ = strconv.Atoi(raw)
		}
	}
	if duration > 0 {
		payload["duration"] = duration
	}

	width, height := 0, 0
	if size, _ := bodyMap["size"].(string); size != "" {
		if parts := strings.SplitN(strings.ToLower(size), "x", 2); len(parts) == 2 {
			width, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
			height, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
		}
	}

	ratio, _ := bodyMap["ratio"].(string)
	if ratio == "" {
		ratio, _ = bodyMap["aspect_ratio"].(string)
	}
	if ratio == "" && width > 0 && height > 0 {
		// snap to the closest ratio megabyai accepts; exotic sizes such as
		// 1792x1024 reduce to 7:4, which the upstream rejects
		best, bestDelta := "", 0.0
		actual := float64(width) / float64(height)
		for _, candidate := range []struct {
			label string
			value float64
		}{
			{"16:9", 16.0 / 9.0}, {"9:16", 9.0 / 16.0}, {"1:1", 1},
			{"4:3", 4.0 / 3.0}, {"3:4", 3.0 / 4.0}, {"21:9", 21.0 / 9.0}, {"9:21", 9.0 / 21.0},
		} {
			delta := math.Abs(actual - candidate.value)
			if best == "" || delta < bestDelta {
				best, bestDelta = candidate.label, delta
			}
		}
		ratio = best
	}
	if ratio != "" {
		payload["ratio"] = ratio
	}

	resolution, _ := bodyMap["resolution"].(string)
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	if resolution == "" && width > 0 && height > 0 {
		shortSide := min(width, height)
		best, bestDelta := "", 0
		for _, candidate := range []struct {
			label string
			value int
		}{{"480p", 480}, {"720p", 720}, {"1080p", 1080}, {"1440p", 1440}, {"4k", 2160}} {
			delta := shortSide - candidate.value
			if delta < 0 {
				delta = -delta
			}
			if best == "" || delta < bestDelta {
				best, bestDelta = candidate.label, delta
			}
		}
		resolution = best
	}
	if resolution != "" {
		payload["resolution"] = resolution
	}

	if images := megabyaiReferenceURLs(bodyMap, "referenceImages", "reference_images", "images", "input_reference", "image"); len(images) > 0 {
		payload["referenceImages"] = images
	}
	if videos := megabyaiReferenceURLs(bodyMap, "referenceVideos", "reference_videos", "videos", "video"); len(videos) > 0 {
		payload["referenceVideos"] = videos
	}
	if audios := megabyaiReferenceURLs(bodyMap, "referenceAudios", "reference_audios", "audios", "audio"); len(audios) > 0 {
		payload["referenceAudios"] = audios
	}
	return payload
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
	isMegabyai := strings.Contains(a.baseURL, megabyaiHost)
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
				if info.ChannelSetting.VideoPayloadFormat == "dashscope" {
					parameters := map[string]any{}
					if existing, ok := bodyMap["parameters"].(map[string]any); ok {
						for key, value := range existing {
							parameters[key] = value
						}
					}
					delete(parameters, "resolution")
					delete(parameters, "duration")
					bodyMap["parameters"] = parameters
				}
			} else if normalized.Selection.EffectiveResolution != "" {
				if normalized.Request.Size != "" {
					bodyMap["size"] = normalized.Request.Size
				} else {
					delete(bodyMap, "size")
				}
				bodyMap["seconds"] = normalized.Request.Seconds
				delete(bodyMap, "duration")
				bodyMap["resolution"] = normalized.Selection.EffectiveResolution
				if info.ChannelSetting.VideoPayloadFormat == "dashscope" {
					parameters := make(map[string]any, len(normalized.Parameters))
					for key, value := range normalized.Parameters {
						parameters[key] = value
					}
					bodyMap["parameters"] = parameters
				}
			}
			// 先归一化再改方言：megabyai 会把 size/seconds 折成 ratio+resolution+duration，
			// 读到的必须是 ResolveVideoBilling 算账用的那份值，否则计费与载荷会分叉。
			if isMegabyai {
				bodyMap = buildMegabyaiVideoPayload(bodyMap)
			} else if info.ChannelSetting.VideoPayloadFormat == "dashscope" {
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
		hasNormalizedVideoSelection := normalized.Selection.EffectiveResolution != ""
		if info.Action != constant.TaskActionRemix && hasNormalizedVideoSelection {
			if normalized.Request.Size != "" {
				writer.WriteField("size", normalized.Request.Size)
			}
			writer.WriteField("seconds", normalized.Request.Seconds)
			writer.WriteField("resolution", normalized.Selection.EffectiveResolution)
		}
		for key, values := range formData.Value {
			isVideoParameter := key == "size" || key == "seconds" || key == "duration" || key == "resolution"
			if key == "model" || (isVideoParameter && (info.Action == constant.TaskActionRemix || hasNormalizedVideoSelection)) {
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
	inlineFailureReason := ""
	if prefix, reason, ok := strings.Cut(strings.TrimSpace(resTask.Status), ":"); ok {
		switch strings.ToLower(strings.TrimSpace(prefix)) {
		case "failed", "cancelled", "canceled":
			status = strings.ToLower(strings.TrimSpace(prefix))
			inlineFailureReason = strings.TrimSpace(reason)
		}
	}
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
		} else if inlineFailureReason != "" {
			taskResult.Reason = inlineFailureReason
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
