package vidu

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/pkg/errors"
)

// ============================
// Request / Response structures
// ============================

type requestPayload struct {
	Model             string    `json:"model"`
	Images            []string  `json:"images,omitempty"`
	Subjects          []subject `json:"subjects,omitempty"`
	Prompt            string    `json:"prompt,omitempty"`
	Duration          int       `json:"duration,omitempty"`
	Seed              int       `json:"seed,omitempty"`
	Resolution        string    `json:"resolution,omitempty"`
	MovementAmplitude string    `json:"movement_amplitude,omitempty"`
	Bgm               bool      `json:"bgm,omitempty"`
	Payload           string    `json:"payload,omitempty"`
	CallbackUrl       string    `json:"callback_url,omitempty"`
}

type subject struct {
	Name   string   `json:"name"`
	Images []string `json:"images"`
}

type responsePayload struct {
	TaskId            string   `json:"task_id"`
	State             string   `json:"state"`
	Model             string   `json:"model"`
	Images            []string `json:"images"`
	Prompt            string   `json:"prompt"`
	Duration          int      `json:"duration"`
	Seed              int      `json:"seed"`
	Resolution        string   `json:"resolution"`
	Bgm               bool     `json:"bgm"`
	MovementAmplitude string   `json:"movement_amplitude"`
	Payload           string   `json:"payload"`
	CreatedAt         string   `json:"created_at"`
}

type taskResultResponse struct {
	State     string     `json:"state"`
	ErrCode   string     `json:"err_code"`
	Credits   int        `json:"credits"`
	Payload   string     `json:"payload"`
	Creations []creation `json:"creations"`
}

type creation struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	CoverURL string `json:"cover_url"`
	Video    struct {
		Duration float64 `json:"duration"`
	} `json:"video"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	if err := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); err != nil {
		return err
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapper(err, "get_task_request_failed", http.StatusBadRequest)
	}
	action := constant.TaskActionTextGenerate
	if meatAction, ok := req.Metadata["action"]; ok {
		action, _ = meatAction.(string)
	} else if req.HasImage() {
		action = constant.TaskActionGenerate
		if info.ChannelType == constant.ChannelTypeVidu {
			// vidu 增加 首尾帧生视频和参考图生视频
			if len(req.Images) == 2 {
				action = constant.TaskActionFirstTailGenerate
			} else if len(req.Images) > 2 {
				action = constant.TaskActionReferenceGenerate
			}
		}
	}
	info.Action = action
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return nil, fmt.Errorf("request not found in context")
	}
	req := v.(relaycommon.TaskSubmitReq)

	body, err := a.convertToRequestPayload(&req, info)
	if err != nil {
		return nil, err
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	var path string
	switch info.Action {
	case constant.TaskActionGenerate:
		path = "/img2video"
	case constant.TaskActionFirstTailGenerate:
		path = "/start-end2video"
	case constant.TaskActionReferenceGenerate:
		path = "/reference2video"
	default:
		path = "/text2video"
	}
	return fmt.Sprintf("%s/ent/v2%s", a.baseURL, path), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+info.ApiKey)
	return nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}

	var vResp responsePayload
	err = common.Unmarshal(responseBody, &vResp)
	if err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrap(err, fmt.Sprintf("%s", responseBody)), "unmarshal_response_failed", http.StatusInternalServerError)
		return
	}

	if vResp.State == "failed" {
		taskErr = service.TaskErrorWrapperLocal(fmt.Errorf("task failed"), "task_failed", http.StatusBadRequest)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return vResp.TaskId, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	url := fmt.Sprintf("%s/ent/v2/tasks/%s/creations", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return []string{"viduq2", "viduq1", "vidu2.0", "vidu1.5"}
}

func (a *TaskAdaptor) GetChannelName() string {
	return "vidu"
}

// ============================
// helpers
// ============================

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, error) {
	model := taskcommon.DefaultString(info.UpstreamModelName, "viduq1")
	action := info.Action
	_, explicitAction := req.Metadata["action"]
	if model == "viduq2" && !explicitAction && len(req.Images) > 0 {
		action = constant.TaskActionReferenceGenerate
		info.Action = action
	}
	if action == constant.TaskActionReferenceGenerate && model == "viduq2" {
		// 参考图生视频只能用 viduq2 模型, 不能带有pro或turbo后缀 https://platform.vidu.cn/docs/reference-to-video
		model = "viduq2"
	}
	capability, ok := viduVideoCapabilities[viduVideoCapabilityKey{action: action, model: model}]
	if !ok {
		return nil, fmt.Errorf("unsupported Vidu action %q for model %s", action, model)
	}
	requestedResolution := req.Size
	if strings.TrimSpace(req.Resolution) != "" {
		requestedResolution = req.Resolution
	}
	duration := taskcommon.DefaultInt(req.Duration, capability.defaultDuration)
	defaultResolution := capability.defaultResolution
	if resolution, ok := capability.defaultResolutionByDuration[duration]; ok {
		defaultResolution = resolution
	}
	r := requestPayload{
		Model:             model,
		Images:            req.Images,
		Prompt:            req.Prompt,
		Duration:          duration,
		Resolution:        taskcommon.DefaultString(requestedResolution, defaultResolution),
		MovementAmplitude: "auto",
		Bgm:               false,
	}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}
	r.Model = model
	r.Resolution = strings.ToLower(strings.TrimSpace(r.Resolution))
	_, metadataResolution := req.Metadata["resolution"]
	if strings.TrimSpace(requestedResolution) == "" && !metadataResolution {
		if resolution, ok := capability.defaultResolutionByDuration[r.Duration]; ok {
			r.Resolution = resolution
		}
	}
	if action == constant.TaskActionReferenceGenerate {
		if len(r.Subjects) > 0 && len(r.Images) > 0 {
			return nil, fmt.Errorf("Vidu reference-to-video accepts subjects or flat images, not both")
		}
		if len(r.Subjects) == 0 {
			for start := 0; start < len(r.Images); start += 3 {
				end := min(start+3, len(r.Images))
				r.Subjects = append(r.Subjects, subject{
					Name:   fmt.Sprintf("subject%d", len(r.Subjects)+1),
					Images: append([]string(nil), r.Images[start:end]...),
				})
			}
		}
		r.Images = nil
	}
	if err := validateViduVideoInputs(&r, action); err != nil {
		return nil, err
	}
	if err := validateViduVideoPayload(&r, capability); err != nil {
		return nil, err
	}
	return &r, nil
}

func validateViduVideoInputs(body *requestPayload, action string) error {
	switch action {
	case constant.TaskActionTextGenerate:
		if len(body.Images) != 0 || len(body.Subjects) != 0 {
			return fmt.Errorf("Vidu text-to-video does not accept image inputs")
		}
	case constant.TaskActionGenerate:
		if len(body.Images) != 1 || len(body.Subjects) != 0 {
			return fmt.Errorf("Vidu image-to-video requires exactly one image")
		}
	case constant.TaskActionFirstTailGenerate:
		if len(body.Images) != 2 || len(body.Subjects) != 0 {
			return fmt.Errorf("Vidu start-end-to-video requires exactly two images")
		}
	case constant.TaskActionReferenceGenerate:
		if len(body.Images) != 0 || len(body.Subjects) == 0 {
			return fmt.Errorf("Vidu reference-to-video requires subjects")
		}
		totalImages := 0
		for _, subject := range body.Subjects {
			if strings.TrimSpace(subject.Name) == "" || len(subject.Images) < 1 || len(subject.Images) > 3 {
				return fmt.Errorf("Vidu reference-to-video subjects require a name and images")
			}
			for _, image := range subject.Images {
				if strings.TrimSpace(image) == "" {
					return fmt.Errorf("Vidu reference-to-video images cannot be empty")
				}
				totalImages++
			}
		}
		if totalImages < 1 || totalImages > 7 {
			return fmt.Errorf("Vidu reference-to-video supports between one and seven images")
		}
	default:
		return fmt.Errorf("unsupported Vidu action %q", action)
	}
	return nil
}

func (a *TaskAdaptor) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (relaycommon.VideoBillingSelection, *taskdto.TaskError) {
	req, err := relaycommon.GetTaskRequest(c)
	if err == nil {
		body, resolveErr := a.convertToRequestPayload(&req, info)
		if resolveErr == nil {
			return relaycommon.VideoBillingSelection{
				EffectiveResolution:      body.Resolution,
				EffectiveDurationSeconds: body.Duration,
			}, nil
		}
		err = resolveErr
	}
	return relaycommon.VideoBillingSelection{}, service.TaskErrorWrapperLocal(
		err,
		"video_resolution_not_supported",
		http.StatusBadRequest,
	)
}

type viduVideoCapabilityKey struct {
	action string
	model  string
}

type viduVideoCapability struct {
	defaultResolution           string
	defaultDuration             int
	defaultResolutionByDuration map[int]string
	minDuration                 int
	maxDuration                 int
	resolutions                 map[string]bool
	resolutionsByDuration       map[int]map[string]bool
}

var viduVideoCapabilities = map[viduVideoCapabilityKey]viduVideoCapability{
	{action: constant.TaskActionTextGenerate, model: "viduq2"}: {
		defaultResolution: "720p", defaultDuration: 5, minDuration: 1, maxDuration: 10,
		resolutions: map[string]bool{"540p": true, "720p": true, "1080p": true},
	},
	{action: constant.TaskActionReferenceGenerate, model: "viduq2"}: {
		defaultResolution: "720p", defaultDuration: 5, minDuration: 1, maxDuration: 10,
		resolutions: map[string]bool{"540p": true, "720p": true, "1080p": true},
	},
	{action: constant.TaskActionTextGenerate, model: "viduq1"}: {
		defaultResolution: "1080p", defaultDuration: 5,
		resolutionsByDuration: map[int]map[string]bool{5: {"1080p": true}},
	},
	{action: constant.TaskActionGenerate, model: "viduq1"}: {
		defaultResolution: "1080p", defaultDuration: 5,
		resolutionsByDuration: map[int]map[string]bool{5: {"1080p": true}},
	},
	{action: constant.TaskActionFirstTailGenerate, model: "viduq1"}: {
		defaultResolution: "1080p", defaultDuration: 5,
		resolutionsByDuration: map[int]map[string]bool{5: {"1080p": true}},
	},
	{action: constant.TaskActionReferenceGenerate, model: "viduq1"}: {
		defaultResolution: "1080p", defaultDuration: 5,
		resolutionsByDuration: map[int]map[string]bool{5: {"1080p": true}},
	},
	{action: constant.TaskActionGenerate, model: "vidu2.0"}: {
		defaultResolution: "360p", defaultDuration: 4,
		defaultResolutionByDuration: map[int]string{4: "360p", 8: "720p"},
		resolutionsByDuration: map[int]map[string]bool{
			4: {"360p": true, "720p": true, "1080p": true},
			8: {"720p": true},
		},
	},
	{action: constant.TaskActionFirstTailGenerate, model: "vidu2.0"}: {
		defaultResolution: "360p", defaultDuration: 4,
		defaultResolutionByDuration: map[int]string{4: "360p", 8: "720p"},
		resolutionsByDuration: map[int]map[string]bool{
			4: {"360p": true, "720p": true, "1080p": true},
			8: {"720p": true},
		},
	},
	{action: constant.TaskActionReferenceGenerate, model: "vidu2.0"}: {
		defaultResolution: "360p", defaultDuration: 4,
		resolutionsByDuration: map[int]map[string]bool{4: {"360p": true, "720p": true}},
	},
}

func validateViduVideoPayload(body *requestPayload, capability viduVideoCapability) error {
	if body.Duration < 1 || body.Duration > relaycommon.MaxTaskDurationSeconds {
		return fmt.Errorf("Vidu duration must be between 1 and %d", relaycommon.MaxTaskDurationSeconds)
	}
	if capability.resolutionsByDuration != nil {
		resolutions, ok := capability.resolutionsByDuration[body.Duration]
		if !ok || !resolutions[body.Resolution] {
			return fmt.Errorf("unsupported %s resolution %q at %d seconds", body.Model, body.Resolution, body.Duration)
		}
		return nil
	}
	if body.Duration < capability.minDuration || body.Duration > capability.maxDuration {
		return fmt.Errorf("unsupported %s duration %d", body.Model, body.Duration)
	}
	if !capability.resolutions[body.Resolution] {
		return fmt.Errorf("unsupported %s resolution %q", body.Model, body.Resolution)
	}
	return nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	taskInfo := &relaycommon.TaskInfo{}

	var taskResp taskResultResponse
	err := common.Unmarshal(respBody, &taskResp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal response body")
	}

	state := taskResp.State
	switch state {
	case "created", "queueing":
		taskInfo.Status = model.TaskStatusSubmitted
	case "processing":
		taskInfo.Status = model.TaskStatusInProgress
	case "success":
		taskInfo.Status = model.TaskStatusSuccess
		if len(taskResp.Creations) > 0 {
			taskInfo.Url = taskResp.Creations[0].URL
			duration := taskResp.Creations[0].Video.Duration
			if duration > 0 && duration <= relaycommon.MaxTaskDurationSeconds && math.Trunc(duration) == duration {
				taskInfo.EffectiveDurationSeconds = int(duration)
			}
		}
	case "failed":
		taskInfo.Status = model.TaskStatusFailure
		if taskResp.ErrCode != "" {
			taskInfo.Reason = taskResp.ErrCode
		}
	default:
		return nil, fmt.Errorf("unknown task state: %s", state)
	}

	return taskInfo, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var viduResp taskResultResponse
	if err := common.Unmarshal(originTask.Data, &viduResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal vidu task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt

	if len(viduResp.Creations) > 0 && viduResp.Creations[0].URL != "" {
		openAIVideo.SetMetadata("url", viduResp.Creations[0].URL)
	}

	if viduResp.State == "failed" && viduResp.ErrCode != "" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: viduResp.ErrCode,
			Code:    viduResp.ErrCode,
		}
	}

	return common.Marshal(openAIVideo)
}
