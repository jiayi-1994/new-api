package doubao

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	SafetyIdentifier string         `json:"safety_identifier,omitempty"`
	Priority         *dto.IntValue  `json:"priority,omitempty"`
	Resolution       string         `json:"resolution,omitempty"`
	Ratio            string         `json:"ratio,omitempty"`
	Duration         *dto.IntValue  `json:"duration,omitempty"`
	Frames           *dto.IntValue  `json:"frames,omitempty"`
	Seed             *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed      *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark        *dto.BoolValue `json:"watermark,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Seed            int          `json:"seed"`
	Resolution      string       `json:"resolution"`
	Duration        dto.IntValue `json:"duration"`
	Ratio           string       `json:"ratio"`
	FramesPerSecond int          `json:"framespersecond"`
	ServiceTier     string       `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
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

const doubaoResolvedVideoRequestKey = "doubao_resolved_video_request"

type doubaoResolvedVideoRequest struct {
	Request   *requestPayload
	Selection relaycommon.VideoBillingSelection
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *taskdto.TaskError) {
	// Accept only POST /v1/video/generations as "generate" action.
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/api/v3/contents/generations/tasks", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling 根据请求 metadata 中的输出分辨率与是否包含视频输入，返回相对基准价的计费 OtherRatio。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	hasVideo := hasVideoInMetadata(req.Metadata)
	resolution, _ := req.Metadata["resolution"].(string)
	ratio, ok := GetVideoInputRatio(info.OriginModelName, resolution, hasVideo)
	if !ok || ratio == 1.0 {
		return nil
	}
	return map[string]float64{"video_input": ratio}
}

// hasVideoInMetadata 直接检查 metadata 的 content 数组是否包含 video_url 条目，
// 避免构建完整的上游 requestPayload。
func hasVideoInMetadata(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	contentRaw, ok := metadata["content"]
	if !ok {
		return false
	}
	contentSlice, ok := contentRaw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	var body *requestPayload
	if cached, ok := c.Get(doubaoResolvedVideoRequestKey); ok {
		if resolved, valid := cached.(doubaoResolvedVideoRequest); valid {
			body = resolved.Request
		}
	}
	if body == nil {
		body, _, err = a.resolveVideoRequest(&req, info)
		if err != nil {
			return nil, errors.Wrap(err, "convert request payload failed")
		}
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if dResp.ID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return dResp.ID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
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

func (a *TaskAdaptor) resolveVideoRequest(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, relaycommon.VideoBillingSelection, error) {
	upstreamModel := strings.TrimSpace(info.UpstreamModelName)
	if upstreamModel == "" {
		upstreamModel = req.Model
	}
	r := requestPayload{
		Model:   upstreamModel,
		Content: []ContentItem{},
	}

	// Add images if present
	if req.HasImage() {
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
			})
		}
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, relaycommon.VideoBillingSelection{}, errors.Wrap(err, "unmarshal metadata failed")
	}

	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	} else if r.Duration == nil && req.Duration > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(req.Duration))
	}
	if r.Resolution == "" {
		if strings.TrimSpace(req.Resolution) != "" {
			r.Resolution = req.Resolution
		} else if strings.TrimSpace(req.Size) != "" {
			r.Resolution = req.Size
		}
	}

	r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
	r.Content = append(r.Content, ContentItem{
		Type: "text",
		Text: req.Prompt,
	})

	selection, err := normalizeDoubaoVideoRequest(&r)
	if err != nil {
		return nil, relaycommon.VideoBillingSelection{}, err
	}
	return &r, selection, nil
}

func (a *TaskAdaptor) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (relaycommon.VideoBillingSelection, *taskdto.TaskError) {
	req, err := relaycommon.GetTaskRequest(c)
	if err == nil {
		body, selection, resolveErr := a.resolveVideoRequest(&req, info)
		if resolveErr == nil {
			c.Set(doubaoResolvedVideoRequestKey, doubaoResolvedVideoRequest{Request: body, Selection: selection})
			return selection, nil
		}
		err = resolveErr
	}
	return relaycommon.VideoBillingSelection{}, service.TaskErrorWrapperLocal(
		err,
		"video_resolution_not_supported",
		http.StatusBadRequest,
	)
}

type doubaoVideoCapability struct {
	defaultResolution string
	minDuration       int
	maxDuration       int
	allow1080p        bool
}

var doubaoVideoCapabilities = map[string]doubaoVideoCapability{
	"doubao-seedance-1-0-pro-250528":      {defaultResolution: "1080p", minDuration: 2, maxDuration: 12, allow1080p: true},
	"doubao-seedance-1-0-pro-fast-250528": {defaultResolution: "1080p", minDuration: 2, maxDuration: 12, allow1080p: true},
	"doubao-seedance-1-0-lite-t2v":        {defaultResolution: "720p", minDuration: 2, maxDuration: 12, allow1080p: true},
	"doubao-seedance-1-0-lite-i2v":        {defaultResolution: "720p", minDuration: 2, maxDuration: 12},
	"doubao-seedance-1-5-pro-251215":      {defaultResolution: "720p", minDuration: 4, maxDuration: 12, allow1080p: true},
	"doubao-seedance-2-0-260128":          {defaultResolution: "720p", minDuration: 4, maxDuration: 15, allow1080p: true},
	"doubao-seedance-2-0-fast-260128":     {defaultResolution: "720p", minDuration: 4, maxDuration: 15},
}

func getDoubaoVideoCapability(model string) (doubaoVideoCapability, bool) {
	capability, ok := doubaoVideoCapabilities[model]
	return capability, ok
}

func normalizeDoubaoVideoRequest(body *requestPayload) (relaycommon.VideoBillingSelection, error) {
	capability, ok := getDoubaoVideoCapability(body.Model)
	if !ok {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Doubao video model %q", body.Model)
	}
	resolution := strings.ToLower(strings.TrimSpace(body.Resolution))
	if resolution == "" {
		resolution = capability.defaultResolution
	}
	if resolution != "480p" && resolution != "720p" && resolution != "1080p" {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Doubao video resolution %q", resolution)
	}
	if resolution == "1080p" && !capability.allow1080p {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Doubao video resolution %q", resolution)
	}
	if body.Frames != nil {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("Doubao video frames do not provide an exact billing duration")
	}
	if body.Duration == nil {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("Doubao video duration is required")
	}
	duration := int(*body.Duration)
	if duration < capability.minDuration || duration > capability.maxDuration || duration > relaycommon.MaxTaskDurationSeconds {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Doubao video duration %d", duration)
	}

	body.Resolution = resolution
	selection := relaycommon.VideoBillingSelection{
		EffectiveResolution:      resolution,
		EffectiveDurationSeconds: duration,
	}
	if ratio, exists := GetVideoInputIndependentRatio(body.Model, resolution, hasVideoInPayload(body)); exists && ratio != 1 {
		selection.IndependentRatios = map[string]float64{"video_input": ratio}
	}
	return selection, nil
}

func hasVideoInPayload(body *requestPayload) bool {
	for _, item := range body.Content {
		if item.Type == "video_url" || item.VideoURL != nil {
			return true
		}
	}
	return false
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}
	duration := int(resTask.Duration)
	if duration > 0 && duration <= relaycommon.MaxTaskDurationSeconds {
		taskResult.EffectiveDurationSeconds = duration
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Error.Message
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", dResp.Content.VideoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if dResp.Status == "failed" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: dResp.Error.Message,
			Code:    dResp.Error.Code,
		}
	}

	return common.Marshal(openAIVideo)
}
