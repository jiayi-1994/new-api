package controller

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/publicsuffix"
)

func GetAllTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	taskID, taskRecordID := taskIDFilter(c.Query("task_id"))
	// 解析其他查询参数
	queryParams := model.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         taskID,
		TaskRecordID:   taskRecordID,
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ChannelID:      c.Query("channel_id"),
	}

	items := model.TaskGetAllTasks(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	taskDtos, err := tasksToDto(items, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total := model.TaskCountAllTasks(queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(taskDtos)
	common.ApiSuccess(c, pageInfo)
}

func GetUserTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	userId := c.GetInt("id")

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	taskID, taskRecordID := taskIDFilter(c.Query("task_id"))

	queryParams := model.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         taskID,
		TaskRecordID:   taskRecordID,
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
	}

	items := model.TaskGetAllUserTask(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	taskDtos, err := tasksToDto(items, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total := model.TaskCountAllUserTask(userId, queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(taskDtos)
	common.ApiSuccess(c, pageInfo)
}

func taskIDFilter(taskID string) (string, int64) {
	if taskRecordID, ok := service.ParseVideoContentPublicTaskID(taskID); ok {
		return "", taskRecordID
	}
	return taskID, 0
}

func tasksToDto(tasks []*model.Task, fillUser bool) ([]*dto.TaskDto, error) {
	var userIdMap map[int]*model.UserBase
	if fillUser {
		userIdMap = make(map[int]*model.UserBase)
		userIds := types.NewSet[int]()
		for _, task := range tasks {
			userIds.Add(task.UserId)
		}
		for _, userId := range userIds.Items() {
			cacheUser, err := model.GetUserCache(userId)
			if err == nil {
				userIdMap[userId] = cacheUser
			}
		}
	}
	result := make([]*dto.TaskDto, len(tasks))
	for i, task := range tasks {
		if fillUser {
			if user, ok := userIdMap[task.UserId]; ok {
				task.Username = user.Username
			}
		}
		result[i] = relay.TaskModel2Dto(task)
		if task.Platform == constant.TaskPlatformSuno {
			continue
		}

		publicTaskID, err := service.VideoContentPublicTaskID(task.ID, task.TaskID, strings.TrimSpace(task.PrivateData.UpstreamTaskID) != "")
		if err != nil {
			return nil, err
		}

		result[i].TaskID = publicTaskID
		result[i].ResultURL = ""
		result[i].FailReason = ""
		projection := taskVideoPublicProjection{
			Object:   "video",
			Status:   task.Status.ToVideoStatus(),
			Progress: taskVideoProgress(task.Progress),
			TaskID:   publicTaskID,
			Model:    task.Properties.OriginModelName,
		}
		if task.Status == model.TaskStatusSuccess {
			token, _, err := service.IssueVideoContentToken(publicTaskID, task.UserId, task.ID)
			if err != nil {
				return nil, err
			}
			query := url.Values{}
			query.Set("video_token", token)
			projection.URL = "/v1/videos/" + url.PathEscape(publicTaskID) + "/content?" + query.Encode()
			result[i].ResultURL = projection.URL
		}
		upstreamTaskID := task.GetUpstreamTaskID()
		if strings.TrimSpace(upstreamTaskID) == "" {
			upstreamTaskID = task.TaskID
		}
		if task.Status == model.TaskStatusFailure && !isSensitiveTaskFailure(task.FailReason, upstreamTaskID) {
			result[i].FailReason = task.FailReason
		}
		result[i].Properties = model.Properties{
			Input:           task.Properties.Input,
			OriginModelName: task.Properties.OriginModelName,
		}
		data, err := common.Marshal(projection)
		if err != nil {
			return nil, err
		}
		result[i].Data = data
	}
	return result, nil
}

type taskVideoPublicProjection struct {
	Object   string `json:"object"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	TaskID   string `json:"task_id"`
	Model    string `json:"model"`
	URL      string `json:"url,omitempty"`
}

func taskVideoProgress(progress string) int {
	value, _ := strconv.Atoi(strings.TrimSuffix(progress, "%"))
	return value
}

var taskFailureSchemePattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://|\bdata:[^[:space:]]+`)
var taskFailureHostWithLocationPattern = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]_.-])(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+|(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3})(?::[0-9]{1,5}|[/?#])`)
var taskFailureStandaloneHostPattern = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+|(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3})(?::[0-9]{1,5})?$`)
var taskFailureIPv6HostPattern = regexp.MustCompile(`(?i)\[[0-9a-f:.]+(?:%[a-z0-9._-]+)?\](?::[0-9]{1,5})?`)
var taskFailureDomainCandidatePattern = regexp.MustCompile(`(?i)(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z](?:[a-z0-9-]*[a-z0-9])?`)
var taskFailureContextHostPattern = regexp.MustCompile(`(?i)\b(?:at|from|to|via|with|host|endpoint|upstream|provider|lookup|resolve|resolving|not|dial(?:\s+tcp)?|hostname(?:\s+mismatch)?\s*:?|certificate(?:\s+(?:for|is\s+valid\s+for))?)\s+(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:$|[^[:alnum:]_.-])`)
var taskFailureCredentialPattern = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]_-])(?:x-(?:goog-)?api-key(?:\s+(?:provided|received|used|supplied|is|was))?|x-goog-signature|x-amz-[a-z0-9-]+|x-oss-[a-z0-9-]+|x-ms-[a-z0-9-]+|ossaccesskeyid|googleaccessid|awsaccesskeyid|(?:api[ _-]?key|access[ _-]?token|client[ _-]?secret|secret[ _-]?key)(?:\s+(?:provided|received|used|supplied|is|was))?|credential|signature|sig)\s*[:=]`)
var taskFailureAuthorizationPattern = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]_-])authorization\s*[:=]\s*(?:bearer|basic)\s+[^[:space:]]+`)
var taskFailureBearerPattern = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]_-])bearer\s+[^[:space:]]+`)
var taskFailureSchemeRelativeURLPattern = regexp.MustCompile(`(?i)(?:^|\s)//(?:\[[0-9a-f:.]+\]|[a-z0-9][a-z0-9._-]*)(?::[0-9]{1,5})?(?:[/?#]|$)`)

func isSensitiveTaskFailure(reason, upstreamTaskID string) bool {
	if strings.TrimSpace(reason) == "" {
		return false
	}
	upstreamTaskID = strings.TrimSpace(upstreamTaskID)
	if upstreamTaskID != "" && strings.Contains(reason, upstreamTaskID) {
		return true
	}
	if taskFailureCredentialPattern.MatchString(reason) ||
		taskFailureAuthorizationPattern.MatchString(reason) ||
		taskFailureBearerPattern.MatchString(reason) {
		return true
	}
	if taskFailureSchemeRelativeURLPattern.MatchString(reason) {
		return true
	}
	for _, candidate := range taskFailureDomainCandidatePattern.FindAllString(reason, -1) {
		if _, icann := publicsuffix.PublicSuffix(strings.ToLower(candidate)); icann {
			return true
		}
		extension := candidate[strings.LastIndex(candidate, ".")+1:]
		switch strings.ToLower(extension) {
		case "cfg", "conf", "css", "csv", "gif", "go", "htm", "html", "ini", "java", "jpeg", "jpg", "js", "json", "jsx", "log", "md", "mkv", "mov", "mp4", "pdf", "png", "py", "rb", "rs", "sh", "sql", "toml", "ts", "tsx", "txt", "webm", "webp", "xml", "yaml", "yml":
			continue
		default:
			return true
		}
	}
	return taskFailureSchemePattern.MatchString(reason) ||
		taskFailureHostWithLocationPattern.MatchString(reason) ||
		taskFailureIPv6HostPattern.MatchString(reason) ||
		taskFailureContextHostPattern.MatchString(reason) ||
		taskFailureStandaloneHostPattern.MatchString(strings.TrimSpace(reason))
}
