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
		if task.Status == model.TaskStatusFailure && !isSensitiveTaskFailure(task.FailReason) {
			result[i].FailReason = task.FailReason
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

var taskFailureHostPattern = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]_-])(?:[[:alnum:]-]+\.)+[a-z]{2,}(?::[0-9]{1,5})?(?:[/?#]|$)|\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
var taskFailureCredentialPattern = regexp.MustCompile(`(?i)\b(?:signature|ossaccesskeyid|x-(?:amz|oss)-[a-z0-9-]+)\s*=`)
var taskFailureSchemeRelativeURLPattern = regexp.MustCompile(`(?i)(?:^|\s)//(?:\[[0-9a-f:.]+\]|[a-z0-9][a-z0-9._-]*)(?::[0-9]{1,5})?(?:[/?#]|$)`)

func isSensitiveTaskFailure(reason string) bool {
	if strings.TrimSpace(reason) == "" {
		return false
	}
	if common.MaskSensitiveInfo(reason) != reason {
		return true
	}
	if taskFailureCredentialPattern.MatchString(reason) {
		return true
	}
	if taskFailureSchemeRelativeURLPattern.MatchString(reason) {
		return true
	}

	lowerReason := strings.ToLower(reason)
	for _, marker := range []string{
		"http://", "https://", "data:", "oss://", "amazonaws.com", "r2.",
	} {
		if strings.Contains(lowerReason, marker) {
			return true
		}
	}
	return taskFailureHostPattern.MatchString(reason)
}
