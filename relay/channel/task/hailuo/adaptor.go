package hailuo

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
)

// https://platform.minimaxi.com/docs/api-reference/video-generation-intro
type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

type hailuoVideoCapability struct {
	defaultResolution     string
	durationsByResolution map[string][]int
}

type hailuoVideoCapabilityKey struct {
	model string
	mode  string
}

const (
	hailuoTextToVideo  = "text-to-video"
	hailuoImageToVideo = "image-to-video"
)

var hailuoVideoCapabilities = map[hailuoVideoCapabilityKey]hailuoVideoCapability{
	{model: "MiniMax-Hailuo-2.3", mode: hailuoTextToVideo}: {
		defaultResolution:     Resolution768P,
		durationsByResolution: map[string][]int{Resolution768P: {6, 10}, Resolution1080P: {6}},
	},
	{model: "MiniMax-Hailuo-2.3", mode: hailuoImageToVideo}: {
		defaultResolution:     Resolution768P,
		durationsByResolution: map[string][]int{Resolution768P: {6, 10}, Resolution1080P: {6}},
	},
	{model: "MiniMax-Hailuo-2.3-Fast", mode: hailuoImageToVideo}: {
		defaultResolution:     Resolution768P,
		durationsByResolution: map[string][]int{Resolution768P: {6, 10}, Resolution1080P: {6}},
	},
	{model: "MiniMax-Hailuo-02", mode: hailuoTextToVideo}: {
		defaultResolution:     Resolution768P,
		durationsByResolution: map[string][]int{Resolution768P: {6, 10}, Resolution1080P: {6}},
	},
	{model: "MiniMax-Hailuo-02", mode: hailuoImageToVideo}: {
		defaultResolution: Resolution768P,
		durationsByResolution: map[string][]int{
			Resolution512P: {6, 10}, Resolution768P: {6, 10}, Resolution1080P: {6},
		},
	},
	{model: "T2V-01-Director", mode: hailuoTextToVideo}: {
		defaultResolution:     Resolution720P,
		durationsByResolution: map[string][]int{Resolution720P: {6}},
	},
	{model: "T2V-01", mode: hailuoTextToVideo}: {
		defaultResolution:     Resolution720P,
		durationsByResolution: map[string][]int{Resolution720P: {6}},
	},
	{model: "I2V-01-Director", mode: hailuoImageToVideo}: {
		defaultResolution:     Resolution720P,
		durationsByResolution: map[string][]int{Resolution720P: {6}},
	},
	{model: "I2V-01-live", mode: hailuoImageToVideo}: {
		defaultResolution:     Resolution720P,
		durationsByResolution: map[string][]int{Resolution720P: {6}},
	},
	{model: "I2V-01", mode: hailuoImageToVideo}: {
		defaultResolution:     Resolution720P,
		durationsByResolution: map[string][]int{Resolution720P: {6}},
	},
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *taskdto.TaskError) {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s%s", a.baseURL, TextToVideoEndpoint), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return nil, fmt.Errorf("request not found in context")
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		return nil, fmt.Errorf("invalid request type in context")
	}

	body, err := a.convertToRequestPayload(&req, info)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(data), nil
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
	_ = resp.Body.Close()

	var hResp VideoResponse
	if err := common.Unmarshal(responseBody, &hResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if hResp.BaseResp.StatusCode != StatusSuccess {
		taskErr = service.TaskErrorWrapper(
			fmt.Errorf("hailuo api error: %s", hResp.BaseResp.StatusMsg),
			strconv.Itoa(hResp.BaseResp.StatusCode),
			http.StatusBadRequest,
		)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return hResp.TaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s%s?task_id=%s", baseUrl, QueryTaskEndpoint, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
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

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*VideoRequest, error) {
	if !contains(ModelList, info.UpstreamModelName) {
		return nil, fmt.Errorf("unsupported Hailuo model %q", info.UpstreamModelName)
	}
	duration := DefaultDuration
	if req.Duration > 0 {
		duration = req.Duration
	}
	videoRequest := &VideoRequest{
		Model:    info.UpstreamModelName,
		Prompt:   req.Prompt,
		Duration: &duration,
	}
	if err := req.UnmarshalMetadata(&videoRequest); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata to video request failed")
	}
	mode := hailuoTextToVideo
	if strings.TrimSpace(videoRequest.FirstFrameImage) != "" {
		mode = hailuoImageToVideo
	}
	capability, ok := hailuoVideoCapabilities[hailuoVideoCapabilityKey{model: info.UpstreamModelName, mode: mode}]
	if !ok {
		return nil, fmt.Errorf("unsupported Hailuo %s mode for model %q", mode, info.UpstreamModelName)
	}
	resolution := capability.defaultResolution
	requestedResolution := req.Size
	if strings.TrimSpace(req.Resolution) != "" {
		requestedResolution = req.Resolution
	}
	if strings.TrimSpace(requestedResolution) != "" {
		var err error
		resolution, err = a.parseResolutionFromSize(requestedResolution)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(videoRequest.Resolution) == "" {
		videoRequest.Resolution = resolution
	}
	videoRequest.Model = info.UpstreamModelName
	videoRequest.Resolution = strings.ToUpper(strings.TrimSpace(videoRequest.Resolution))
	supportedDurations, ok := capability.durationsByResolution[videoRequest.Resolution]
	if !ok {
		return nil, fmt.Errorf("resolution %q is not supported by Hailuo model %s", videoRequest.Resolution, info.UpstreamModelName)
	}
	if videoRequest.Duration == nil || *videoRequest.Duration < 1 || *videoRequest.Duration > relaycommon.MaxTaskDurationSeconds || !containsInt(supportedDurations, *videoRequest.Duration) {
		return nil, fmt.Errorf("duration is not supported by Hailuo model %s", info.UpstreamModelName)
	}

	return videoRequest, nil
}

func (a *TaskAdaptor) parseResolutionFromSize(size string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(size))
	normalized = strings.ReplaceAll(normalized, "*", "x")
	switch normalized {
	case "1080p", "1920x1080", "1080x1920":
		return Resolution1080P, nil
	case "768p", "1366x768", "768x1366":
		return Resolution768P, nil
	case "720p", "1280x720", "720x1280":
		return Resolution720P, nil
	case "512p":
		return Resolution512P, nil
	default:
		return "", fmt.Errorf("unsupported Hailuo resolution %q", size)
	}
}

func (a *TaskAdaptor) ResolveVideoBilling(c *gin.Context, info *relaycommon.RelayInfo) (relaycommon.VideoBillingSelection, *taskdto.TaskError) {
	req, err := relaycommon.GetTaskRequest(c)
	if err == nil {
		body, resolveErr := a.convertToRequestPayload(&req, info)
		if resolveErr == nil {
			return relaycommon.VideoBillingSelection{
				EffectiveResolution:      strings.ToLower(body.Resolution),
				EffectiveDurationSeconds: *body.Duration,
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

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := QueryTaskResponse{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{}

	if resTask.BaseResp.StatusCode == StatusSuccess {
		taskResult.Code = 0
	} else {
		taskResult.Code = resTask.BaseResp.StatusCode
		taskResult.Reason = resTask.BaseResp.StatusMsg
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
	}

	switch resTask.Status {
	case TaskStatusPreparing, TaskStatusQueueing, TaskStatusProcessing:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
		if resTask.Status == TaskStatusProcessing {
			taskResult.Progress = "50%"
		}
	case TaskStatusSuccess:
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = a.buildVideoURL(resTask.TaskID, resTask.FileID)
	case TaskStatusFailed:
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		if taskResult.Reason == "" {
			taskResult.Reason = "task failed"
		}
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var hailuoResp QueryTaskResponse
	if err := common.Unmarshal(originTask.Data, &hailuoResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal hailuo task data failed")
	}

	openAIVideo := originTask.ToOpenAIVideo()
	if hailuoResp.BaseResp.StatusCode != StatusSuccess {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: hailuoResp.BaseResp.StatusMsg,
			Code:    strconv.Itoa(hailuoResp.BaseResp.StatusCode),
		}
	}

	jsonData, err := common.Marshal(openAIVideo)
	if err != nil {
		return nil, errors.Wrap(err, "marshal openai video failed")
	}

	return jsonData, nil
}

func (a *TaskAdaptor) buildVideoURL(_, fileID string) string {
	if a.apiKey == "" || a.baseURL == "" {
		return ""
	}

	url := fmt.Sprintf("%s/v1/files/retrieve?file_id=%s", a.baseURL, fileID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return ""
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := service.GetHttpClient().Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var retrieveResp RetrieveFileResponse
	if err := common.Unmarshal(responseBody, &retrieveResp); err != nil {
		return ""
	}

	if retrieveResp.BaseResp.StatusCode != StatusSuccess {
		return ""
	}

	return retrieveResp.File.DownloadURL
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsInt(slice []int, item int) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
