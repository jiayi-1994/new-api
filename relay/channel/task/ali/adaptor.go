package ali

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// ============================
// Request / Response structures
// ============================

// AliVideoRequest 阿里通义万相视频生成请求
type AliVideoRequest struct {
	Model      string              `json:"model"`
	Input      AliVideoInput       `json:"input"`
	Parameters *AliVideoParameters `json:"parameters,omitempty"`
}

// AliVideoMedia describes Wan2.7 image-to-video media inputs.
type AliVideoMedia struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// AliVideoInput 视频输入参数
type AliVideoInput struct {
	Prompt         string          `json:"prompt,omitempty"`          // 文本提示词
	ImgURL         string          `json:"img_url,omitempty"`         // 首帧图像URL或Base64（图生视频）
	FirstFrameURL  string          `json:"first_frame_url,omitempty"` // 首帧图片URL（首尾帧生视频）
	LastFrameURL   string          `json:"last_frame_url,omitempty"`  // 尾帧图片URL（首尾帧生视频）
	AudioURL       string          `json:"audio_url,omitempty"`       // 音频URL（wan2.5支持）
	Media          []AliVideoMedia `json:"media,omitempty"`           // 媒体列表（wan2.7-i2v新协议）
	NegativePrompt string          `json:"negative_prompt,omitempty"` // 反向提示词
	Template       string          `json:"template,omitempty"`        // 视频特效模板
}

// AliVideoParameters 视频参数
type AliVideoParameters struct {
	Resolution   string `json:"resolution,omitempty"`    // 分辨率: 480P/720P/1080P（图生视频、首尾帧生视频）
	Size         string `json:"size,omitempty"`          // 尺寸: 如 "832*480"（文生视频）
	Ratio        string `json:"ratio,omitempty"`         // 宽高比（Wan2.7 文生视频）
	Duration     int    `json:"duration,omitempty"`      // 时长: 3-10秒
	PromptExtend bool   `json:"prompt_extend,omitempty"` // 是否开启prompt智能改写
	Watermark    bool   `json:"watermark,omitempty"`     // 是否添加水印
	Audio        *bool  `json:"audio,omitempty"`         // 是否添加音频（wan2.5）
	Seed         int    `json:"seed,omitempty"`          // 随机数种子
}

// AliVideoResponse 阿里通义万相响应
type AliVideoResponse struct {
	Output    AliVideoOutput `json:"output"`
	RequestID string         `json:"request_id"`
	Code      string         `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
	Usage     *AliUsage      `json:"usage,omitempty"`
}

// AliVideoOutput 输出信息
type AliVideoOutput struct {
	TaskID        string `json:"task_id"`
	TaskStatus    string `json:"task_status"`
	SubmitTime    string `json:"submit_time,omitempty"`
	ScheduledTime string `json:"scheduled_time,omitempty"`
	EndTime       string `json:"end_time,omitempty"`
	OrigPrompt    string `json:"orig_prompt,omitempty"`
	ActualPrompt  string `json:"actual_prompt,omitempty"`
	VideoURL      string `json:"video_url,omitempty"`
	Code          string `json:"code,omitempty"`
	Message       string `json:"message,omitempty"`
}

// AliUsage 使用统计
type AliUsage struct {
	Duration      json.RawMessage `json:"duration,omitempty"`
	VideoDuration json.RawMessage `json:"video_duration,omitempty"`
	VideoCount    dto.IntValue    `json:"video_count,omitempty"`
	SR            dto.IntValue    `json:"SR,omitempty"`
}

type AliMetadata struct {
	// Input 相关
	AudioURL       string          `json:"audio_url,omitempty"`       // 音频URL
	ImgURL         string          `json:"img_url,omitempty"`         // 图片URL（图生视频）
	FirstFrameURL  string          `json:"first_frame_url,omitempty"` // 首帧图片URL（首尾帧生视频）
	LastFrameURL   string          `json:"last_frame_url,omitempty"`  // 尾帧图片URL（首尾帧生视频）
	Media          []AliVideoMedia `json:"media,omitempty"`           // 媒体列表（wan2.7-i2v新协议）
	NegativePrompt string          `json:"negative_prompt,omitempty"` // 反向提示词
	Template       string          `json:"template,omitempty"`        // 视频特效模板

	// Parameters 相关
	Resolution   *string `json:"resolution,omitempty"`    // 分辨率: 480P/720P/1080P
	Size         *string `json:"size,omitempty"`          // 尺寸: 如 "832*480"
	Duration     *int    `json:"duration,omitempty"`      // 时长
	PromptExtend *bool   `json:"prompt_extend,omitempty"` // 是否开启prompt智能改写
	Watermark    *bool   `json:"watermark,omitempty"`     // 是否添加水印
	Audio        *bool   `json:"audio,omitempty"`         // 是否添加音频
	Seed         *int    `json:"seed,omitempty"`          // 随机数种子
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

const aliResolvedVideoRequestKey = "ali_resolved_video_request"

type aliResolvedVideoRequest struct {
	Request   *AliVideoRequest
	Selection relaycommon.VideoBillingSelection
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *taskdto.TaskError) {
	// ValidateMultipartDirect 负责解析并将原始 TaskSubmitReq 存入 context
	return relaycommon.ValidateMultipartDirect(c, info)
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/api/v1/services/aigc/video-generation/video-synthesis", a.baseURL), nil
}

// BuildRequestHeader sets required headers for Ali API
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DashScope-Async", "enable") // 阿里异步任务必须设置
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	taskReq, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, errors.Wrap(err, "get_task_request_failed")
	}

	var aliReq *AliVideoRequest
	if cached, ok := c.Get(aliResolvedVideoRequestKey); ok {
		if resolved, valid := cached.(aliResolvedVideoRequest); valid {
			aliReq = resolved.Request
		}
	}
	if aliReq == nil {
		aliReq, err = a.convertToAliRequest(info, taskReq)
		if err != nil {
			return nil, errors.Wrap(err, "convert_to_ali_request_failed")
		}
	}
	logger.LogJson(c, "ali video request body", aliReq)

	bodyBytes, err := common.Marshal(aliReq)
	if err != nil {
		return nil, errors.Wrap(err, "marshal_ali_request_failed")
	}
	return bytes.NewReader(bodyBytes), nil
}

var (
	size480p = []string{
		"832*480",
		"480*832",
		"624*624",
	}
	size720p = []string{
		"1280*720",
		"720*1280",
		"960*960",
		"1088*832",
		"832*1088",
	}
	size1080p = []string{
		"1920*1080",
		"1080*1920",
		"1440*1440",
		"1632*1248",
		"1248*1632",
	}
)

func sizeToResolution(size string) (string, error) {
	if lo.Contains(size480p, size) {
		return "480P", nil
	} else if lo.Contains(size720p, size) {
		return "720P", nil
	} else if lo.Contains(size1080p, size) {
		return "1080P", nil
	}
	return "", fmt.Errorf("invalid size: %s", size)
}

func ProcessAliOtherRatios(aliReq *AliVideoRequest) (map[string]float64, error) {
	otherRatios := make(map[string]float64)
	aliRatios := map[string]map[string]float64{
		"wan2.6-i2v": {
			"720P":  1,
			"1080P": 1 / 0.6,
		},
		"wan2.5-t2v-preview": {
			"480P":  1,
			"720P":  2,
			"1080P": 1 / 0.3,
		},
		"wan2.2-t2v-plus": {
			"480P":  1,
			"1080P": 0.7 / 0.14,
		},
		"wan2.5-i2v-preview": {
			"480P":  1,
			"720P":  2,
			"1080P": 1 / 0.3,
		},
		"wan2.2-i2v-plus": {
			"480P":  1,
			"1080P": 0.7 / 0.14,
		},
		"wan2.2-kf2v-flash": {
			"480P":  1,
			"720P":  2,
			"1080P": 4.8,
		},
		"wan2.2-i2v-flash": {
			"480P": 1,
			"720P": 2,
		},
		"wan2.2-s2v": {
			"480P": 1,
			"720P": 0.9 / 0.5,
		},
	}
	var resolution string

	// size match
	if aliReq.Parameters.Size != "" {
		toResolution, err := sizeToResolution(aliReq.Parameters.Size)
		if err != nil {
			return nil, err
		}
		resolution = toResolution
	} else {
		resolution = strings.ToUpper(aliReq.Parameters.Resolution)
		if !strings.HasSuffix(resolution, "P") {
			resolution = resolution + "P"
		}
	}
	if otherRatio, ok := aliRatios[aliReq.Model]; ok {
		if ratio, ok := otherRatio[resolution]; ok {
			otherRatios[fmt.Sprintf("resolution-%s", resolution)] = ratio
		}
	}
	return otherRatios, nil
}

func isWan27I2VModel(model string) bool {
	return strings.HasPrefix(model, "wan2.7-i2v")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstTaskImage(req relaycommon.TaskSubmitReq) string {
	if image := strings.TrimSpace(req.Image); image != "" {
		return image
	}
	for _, image := range req.Images {
		if trimmed := strings.TrimSpace(image); trimmed != "" {
			return trimmed
		}
	}
	if inputReference := strings.TrimSpace(req.InputReference); inputReference != "" {
		return inputReference
	}
	return ""
}

func secondTaskImage(req relaycommon.TaskSubmitReq) string {
	nonEmptyImages := 0
	for _, image := range req.Images {
		trimmed := strings.TrimSpace(image)
		if trimmed == "" {
			continue
		}
		nonEmptyImages++
		if nonEmptyImages == 2 {
			return trimmed
		}
	}
	return ""
}

func normalizeWan27I2VInput(aliReq *AliVideoRequest, req relaycommon.TaskSubmitReq) error {
	if !isWan27I2VModel(aliReq.Model) {
		return nil
	}

	if len(aliReq.Input.Media) == 0 {
		firstFrameURL := firstNonEmpty(aliReq.Input.FirstFrameURL, aliReq.Input.ImgURL, firstTaskImage(req))
		lastFrameURL := firstNonEmpty(aliReq.Input.LastFrameURL, secondTaskImage(req))
		audioURL := aliReq.Input.AudioURL

		if firstFrameURL != "" {
			aliReq.Input.Media = append(aliReq.Input.Media, AliVideoMedia{
				Type: "first_frame",
				URL:  firstFrameURL,
			})
		}
		if lastFrameURL != "" {
			aliReq.Input.Media = append(aliReq.Input.Media, AliVideoMedia{
				Type: "last_frame",
				URL:  lastFrameURL,
			})
		}
		if audioURL != "" {
			aliReq.Input.Media = append(aliReq.Input.Media, AliVideoMedia{
				Type: "driving_audio",
				URL:  audioURL,
			})
		}
	}

	if len(aliReq.Input.Media) == 0 {
		return fmt.Errorf("wan2.7-i2v requires image, images, input_reference, or input.media")
	}

	// Wan2.7 image-to-video uses the new input.media protocol. Avoid sending
	// legacy fields that belong to wan2.6 and earlier image-to-video APIs.
	aliReq.Input.ImgURL = ""
	aliReq.Input.FirstFrameURL = ""
	aliReq.Input.LastFrameURL = ""
	aliReq.Input.AudioURL = ""
	return nil
}

func (a *TaskAdaptor) convertToAliRequest(info *relaycommon.RelayInfo, req relaycommon.TaskSubmitReq) (*AliVideoRequest, error) {
	return a.convertToAliRequestMode(info, req, false)
}

func (a *TaskAdaptor) convertToAliRequestMode(info *relaycommon.RelayInfo, req relaycommon.TaskSubmitReq, resolutionPricing bool) (*AliVideoRequest, error) {
	upstreamModel := req.Model
	if strings.TrimSpace(info.UpstreamModelName) != "" {
		upstreamModel = info.UpstreamModelName
	}
	aliReq := &AliVideoRequest{
		Model: upstreamModel,
		Input: AliVideoInput{
			Prompt: req.Prompt,
			ImgURL: firstTaskImage(req),
		},
		Parameters: &AliVideoParameters{
			PromptExtend: true, // 默认开启智能改写
			Watermark:    false,
		},
	}

	// 处理分辨率映射
	if req.Size != "" {
		// text to video size must be contained *
		if !resolutionPricing && strings.Contains(upstreamModel, "t2v") && !strings.Contains(req.Size, "*") {
			return nil, fmt.Errorf("invalid size: %s, example: %s", req.Size, "1920*1080")
		}
		if strings.Contains(req.Size, "*") {
			aliReq.Parameters.Size = req.Size
		} else {
			resolution := strings.ToUpper(req.Size)
			// 支持 480p, 720p, 1080p 或 480P, 720P, 1080P
			if !strings.HasSuffix(resolution, "P") {
				resolution = resolution + "P"
			}
			aliReq.Parameters.Resolution = resolution
		}
	} else {
		// 根据模型设置默认分辨率
		if strings.Contains(upstreamModel, "t2v") { // text to video
			if strings.HasPrefix(upstreamModel, "wan2.5") {
				aliReq.Parameters.Size = "1920*1080"
			} else if strings.HasPrefix(upstreamModel, "wan2.2") {
				aliReq.Parameters.Size = "1920*1080"
			} else {
				aliReq.Parameters.Size = "1280*720"
			}
		} else {
			if strings.HasPrefix(upstreamModel, "wan2.6") {
				aliReq.Parameters.Resolution = "1080P"
			} else if strings.HasPrefix(upstreamModel, "wan2.5") {
				aliReq.Parameters.Resolution = "1080P"
			} else if strings.HasPrefix(upstreamModel, "wan2.2-i2v-flash") {
				aliReq.Parameters.Resolution = "720P"
			} else if strings.HasPrefix(upstreamModel, "wan2.2-i2v-plus") {
				aliReq.Parameters.Resolution = "1080P"
			} else {
				aliReq.Parameters.Resolution = "720P"
			}
		}
	}

	// 处理时长
	if req.Duration > 0 {
		aliReq.Parameters.Duration = req.Duration
	} else if req.Seconds != "" {
		seconds, err := strconv.Atoi(req.Seconds)
		if err != nil {
			return nil, errors.Wrap(err, "convert seconds to int failed")
		} else {
			aliReq.Parameters.Duration = seconds
		}
	}
	if aliReq.Parameters.Duration <= 0 {
		aliReq.Parameters.Duration = 5 // 默认5秒
	}

	// 从 metadata 中提取额外参数
	if req.Metadata != nil {
		if metadataBytes, err := common.Marshal(req.Metadata); err == nil {
			err = common.Unmarshal(metadataBytes, aliReq)
			if err != nil {
				return nil, errors.Wrap(err, "unmarshal metadata failed")
			}
		} else {
			return nil, errors.Wrap(err, "marshal metadata failed")
		}
	}

	if aliReq.Model != upstreamModel {
		return nil, errors.New("can't change model with metadata")
	}

	if err := normalizeWan27I2VInput(aliReq, req); err != nil {
		return nil, err
	}

	return aliReq, nil
}

func (a *TaskAdaptor) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (relaycommon.VideoBillingSelection, *taskdto.TaskError) {
	taskReq, err := relaycommon.GetTaskRequest(c)
	if err == nil {
		aliReq, convertErr := a.convertToAliRequestMode(info, taskReq, true)
		if convertErr == nil {
			selection, normalizeErr := normalizeAliVideoBillingRequest(aliReq, taskReq)
			if normalizeErr == nil {
				c.Set(aliResolvedVideoRequestKey, aliResolvedVideoRequest{Request: aliReq, Selection: selection})
				return selection, nil
			}
			err = normalizeErr
		} else {
			err = convertErr
		}
	}
	return relaycommon.VideoBillingSelection{}, service.TaskErrorWrapperLocal(
		err,
		"video_resolution_not_supported",
		http.StatusBadRequest,
	)
}

type aliVideoCapability struct {
	defaultResolution  string
	allowedResolutions map[string]bool
	allowedDurations   map[int]bool
	minDuration        int
	maxDuration        int
	usesRatio          bool
}

var aliVideoCapabilities = map[string]aliVideoCapability{
	"wan2.7-t2v": {
		defaultResolution:  "1080p",
		allowedResolutions: map[string]bool{"720p": true, "1080p": true},
		minDuration:        2, maxDuration: 15, usesRatio: true,
	},
	"wan2.7-i2v": {
		defaultResolution:  "1080p",
		allowedResolutions: map[string]bool{"720p": true, "1080p": true},
		minDuration:        2, maxDuration: 15,
	},
	"wan2.5-i2v-preview": {
		defaultResolution:  "1080p",
		allowedResolutions: map[string]bool{"480p": true, "720p": true, "1080p": true},
		allowedDurations:   map[int]bool{5: true, 10: true},
	},
	"wan2.2-i2v-flash": {
		defaultResolution:  "720p",
		allowedResolutions: map[string]bool{"480p": true, "720p": true},
		allowedDurations:   map[int]bool{5: true},
	},
	"wan2.2-i2v-plus": {
		defaultResolution:  "1080p",
		allowedResolutions: map[string]bool{"480p": true, "1080p": true},
		allowedDurations:   map[int]bool{5: true},
	},
	"wanx2.1-i2v-plus": {
		defaultResolution:  "720p",
		allowedResolutions: map[string]bool{"720p": true},
		allowedDurations:   map[int]bool{5: true},
	},
	"wanx2.1-i2v-turbo": {
		defaultResolution:  "720p",
		allowedResolutions: map[string]bool{"480p": true, "720p": true},
		allowedDurations:   map[int]bool{3: true, 4: true, 5: true},
	},
}

func normalizeAliVideoBillingRequest(aliReq *AliVideoRequest, req relaycommon.TaskSubmitReq) (relaycommon.VideoBillingSelection, error) {
	if aliReq == nil || aliReq.Parameters == nil {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("Ali video parameters are required")
	}
	capability, ok := aliVideoCapabilities[aliReq.Model]
	if !ok {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("unknown Ali video capabilities for model %s", aliReq.Model)
	}

	metadataSize := ""
	metadataResolution := ""
	if parameters, ok := req.Metadata["parameters"].(map[string]any); ok {
		if value, ok := parameters["size"].(string); ok {
			metadataSize = strings.TrimSpace(value)
		}
		if value, ok := parameters["resolution"].(string); ok {
			metadataResolution = strings.TrimSpace(value)
		}
	}

	selectors := make([]string, 0, 4)
	if strings.TrimSpace(req.Size) != "" {
		selectors = append(selectors, req.Size)
	}
	if strings.TrimSpace(req.Resolution) != "" {
		selectors = append(selectors, req.Resolution)
	}
	if metadataSize != "" {
		selectors = append(selectors, metadataSize)
	}
	if metadataResolution != "" {
		selectors = append(selectors, metadataResolution)
	}

	resolution := ""
	for _, selector := range selectors {
		canonical, err := canonicalAliResolution(selector)
		if err != nil {
			return relaycommon.VideoBillingSelection{}, err
		}
		if resolution != "" && canonical != resolution {
			return relaycommon.VideoBillingSelection{}, fmt.Errorf("conflicting Ali video size and resolution")
		}
		resolution = canonical
	}
	if resolution == "" {
		resolution = capability.defaultResolution
	}
	if !capability.allowedResolutions[resolution] {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Ali video resolution %q", resolution)
	}

	if aliReq.Parameters.Duration < 1 || aliReq.Parameters.Duration > relaycommon.MaxTaskDurationSeconds {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("Ali video duration must be between 1 and %d", relaycommon.MaxTaskDurationSeconds)
	}
	if capability.minDuration > 0 {
		if aliReq.Parameters.Duration < capability.minDuration || aliReq.Parameters.Duration > capability.maxDuration {
			return relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Ali video duration %d for model %s", aliReq.Parameters.Duration, aliReq.Model)
		}
	} else if !capability.allowedDurations[aliReq.Parameters.Duration] {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Ali video duration %d for model %s", aliReq.Parameters.Duration, aliReq.Model)
	}

	shapeSelector := strings.TrimSpace(req.Size)
	if shapeSelector == "" {
		shapeSelector = strings.TrimSpace(req.Resolution)
	}
	if metadataResolution != "" && shapeSelector == "" {
		shapeSelector = metadataResolution
	}
	if metadataSize != "" {
		shapeSelector = metadataSize
	}

	if capability.usesRatio {
		if aliReq.Parameters.Ratio != "" {
			switch aliReq.Parameters.Ratio {
			case "16:9", "9:16", "1:1", "4:3", "3:4":
			default:
				return relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Wan2.7 ratio %q", aliReq.Parameters.Ratio)
			}
		}
		if aliReq.Parameters.Ratio == "" {
			if ratio, ok := aliRatioForSize(shapeSelector); ok {
				aliReq.Parameters.Ratio = ratio
			}
		}
		aliReq.Parameters.Size = ""
		aliReq.Parameters.Resolution = strings.ToUpper(resolution)
	} else if strings.TrimSpace(aliReq.Parameters.Ratio) != "" {
		return relaycommon.VideoBillingSelection{}, fmt.Errorf("Ali model %s does not support a ratio selector", aliReq.Model)
	} else if strings.Contains(aliReq.Model, "t2v") {
		if _, ok := aliRatioForSize(shapeSelector); ok {
			aliReq.Parameters.Size = strings.ReplaceAll(strings.ToLower(shapeSelector), "x", "*")
		} else {
			aliReq.Parameters.Size = aliSizeForResolution(resolution)
		}
		aliReq.Parameters.Resolution = ""
	} else {
		aliReq.Parameters.Size = ""
		aliReq.Parameters.Resolution = strings.ToUpper(resolution)
	}
	return relaycommon.VideoBillingSelection{
		EffectiveResolution:      resolution,
		EffectiveDurationSeconds: aliReq.Parameters.Duration,
	}, nil
}

func aliRatioForSize(size string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(size))
	normalized = strings.ReplaceAll(normalized, "x", "*")
	switch normalized {
	case "832*480", "1280*720", "1920*1080":
		return "16:9", true
	case "480*832", "720*1280", "1080*1920":
		return "9:16", true
	case "624*624", "960*960", "1440*1440":
		return "1:1", true
	case "1104*832", "1648*1248":
		return "4:3", true
	case "832*1104", "1248*1648":
		return "3:4", true
	default:
		return "", false
	}
}

func canonicalAliResolution(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "x", "*")
	if strings.Contains(normalized, "*") {
		switch normalized {
		case "832*480", "480*832", "624*624":
			return "480p", nil
		case "1280*720", "720*1280", "960*960", "1104*832", "832*1104":
			return "720p", nil
		case "1920*1080", "1080*1920", "1440*1440", "1648*1248", "1248*1648":
			return "1080p", nil
		default:
			return "", fmt.Errorf("unsupported Ali video resolution %q", value)
		}
	}
	if !strings.HasSuffix(normalized, "p") {
		normalized += "p"
	}
	switch normalized {
	case "480p", "720p", "1080p":
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported Ali video resolution %q", value)
	}
}

func aliSizeForResolution(resolution string) string {
	switch resolution {
	case "480p":
		return "832*480"
	case "720p":
		return "1280*720"
	default:
		return "1920*1080"
	}
}

// EstimateBilling 根据用户请求参数计算 OtherRatios（时长、分辨率等）。
// 在 ValidateRequestAndSetAction 之后、价格计算之前调用。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	taskReq, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}

	aliReq, err := a.convertToAliRequest(info, taskReq)
	if err != nil {
		return nil
	}

	// metadata can override Duration past standard request validation;
	// cap it because it is used as a billing multiplier.
	otherRatios := map[string]float64{
		"seconds": float64(min(aliReq.Parameters.Duration, relaycommon.MaxTaskDurationSeconds)),
	}
	ratios, err := ProcessAliOtherRatios(aliReq)
	if err != nil {
		return otherRatios
	}
	for k, v := range ratios {
		otherRatios[k] = v
	}
	return otherRatios
}

// DoRequest delegates to common helper
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// 解析阿里响应
	var aliResp AliVideoResponse
	if err := common.Unmarshal(responseBody, &aliResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	// 检查错误
	if aliResp.Code != "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("%s: %s", aliResp.Code, aliResp.Message), "ali_api_error", resp.StatusCode)
		return
	}

	if aliResp.Output.TaskID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	// 转换为 OpenAI 格式响应
	openAIResp := dto.NewOpenAIVideo()
	openAIResp.ID = info.PublicTaskID
	openAIResp.TaskID = info.PublicTaskID
	openAIResp.Model = c.GetString("model")
	if openAIResp.Model == "" && info != nil {
		openAIResp.Model = info.OriginModelName
	}
	openAIResp.Status = convertAliStatus(aliResp.Output.TaskStatus)
	openAIResp.CreatedAt = common.GetTimestamp()

	// 返回 OpenAI 格式
	c.JSON(http.StatusOK, openAIResp)

	return aliResp.Output.TaskID, responseBody, nil
}

// FetchTask 查询任务状态
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v1/tasks/%s", baseUrl, taskID)

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

// ParseTaskResult 解析任务结果
func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var aliResp AliVideoResponse
	if err := common.Unmarshal(respBody, &aliResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}
	if aliResp.Usage != nil {
		for _, rawDuration := range []json.RawMessage{aliResp.Usage.Duration, aliResp.Usage.VideoDuration} {
			if len(rawDuration) == 0 {
				continue
			}
			var duration float64
			switch common.GetJsonType(rawDuration) {
			case "number":
				if err := common.Unmarshal(rawDuration, &duration); err != nil {
					continue
				}
			case "string":
				var value string
				if err := common.Unmarshal(rawDuration, &value); err != nil {
					continue
				}
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil {
					continue
				}
				duration = parsed
			default:
				continue
			}
			if duration > 0 && duration <= relaycommon.MaxTaskDurationSeconds && math.Trunc(duration) == duration {
				taskResult.EffectiveDurationSeconds = int(duration)
				break
			}
		}
	}

	// 状态映射
	switch aliResp.Output.TaskStatus {
	case "PENDING":
		taskResult.Status = model.TaskStatusQueued
	case "RUNNING":
		taskResult.Status = model.TaskStatusInProgress
	case "SUCCEEDED":
		taskResult.Status = model.TaskStatusSuccess
		// 阿里直接返回视频URL，不需要额外的代理端点
		taskResult.Url = aliResp.Output.VideoURL
	case "FAILED", "CANCELED", "UNKNOWN":
		taskResult.Status = model.TaskStatusFailure
		if aliResp.Message != "" {
			taskResult.Reason = aliResp.Message
		} else if aliResp.Output.Message != "" {
			taskResult.Reason = fmt.Sprintf("task failed, code: %s , message: %s", aliResp.Output.Code, aliResp.Output.Message)
		} else {
			taskResult.Reason = "task failed"
		}
	default:
		taskResult.Status = model.TaskStatusQueued
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	var aliResp AliVideoResponse
	if err := common.Unmarshal(task.Data, &aliResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal ali response failed")
	}

	openAIResp := dto.NewOpenAIVideo()
	openAIResp.ID = task.TaskID
	openAIResp.Status = convertAliStatus(aliResp.Output.TaskStatus)
	openAIResp.Model = task.Properties.OriginModelName
	openAIResp.SetProgressStr(task.Progress)
	openAIResp.CreatedAt = task.CreatedAt
	openAIResp.CompletedAt = task.UpdatedAt

	// 设置视频URL（核心字段）
	openAIResp.SetMetadata("url", aliResp.Output.VideoURL)

	// 错误处理
	if aliResp.Code != "" {
		openAIResp.Error = &dto.OpenAIVideoError{
			Code:    aliResp.Code,
			Message: aliResp.Message,
		}
	} else if aliResp.Output.Code != "" {
		openAIResp.Error = &dto.OpenAIVideoError{
			Code:    aliResp.Output.Code,
			Message: aliResp.Output.Message,
		}
	}

	return common.Marshal(openAIResp)
}

func convertAliStatus(aliStatus string) string {
	switch aliStatus {
	case "PENDING":
		return dto.VideoStatusQueued
	case "RUNNING":
		return dto.VideoStatusInProgress
	case "SUCCEEDED":
		return dto.VideoStatusCompleted
	case "FAILED", "CANCELED", "UNKNOWN":
		return dto.VideoStatusFailed
	default:
		return dto.VideoStatusUnknown
	}
}
