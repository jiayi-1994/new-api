package model

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	commonRelay "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
)

type TaskStatus string

func (t TaskStatus) ToVideoStatus() string {
	var status string
	switch t {
	case TaskStatusQueued, TaskStatusSubmitted:
		status = dto.VideoStatusQueued
	case TaskStatusInProgress:
		status = dto.VideoStatusInProgress
	case TaskStatusSuccess:
		status = dto.VideoStatusCompleted
	case TaskStatusFailure:
		status = dto.VideoStatusFailed
	default:
		status = dto.VideoStatusUnknown // Default fallback
	}
	return status
}

const (
	TaskStatusNotStart   TaskStatus = "NOT_START"
	TaskStatusSubmitted             = "SUBMITTED"
	TaskStatusQueued                = "QUEUED"
	TaskStatusInProgress            = "IN_PROGRESS"
	TaskStatusFailure               = "FAILURE"
	TaskStatusSuccess               = "SUCCESS"
	TaskStatusUnknown               = "UNKNOWN"
)

// TaskRefundLegacyCutoff separates tasks created before timeout refunds were
// introduced. Those legacy tasks are failed without an automatic refund.
const TaskRefundLegacyCutoff int64 = 1771718400 // 2026-02-22 00:00:00 UTC

type Task struct {
	ID         int64                 `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt  int64                 `json:"created_at" gorm:"index"`
	UpdatedAt  int64                 `json:"updated_at"`
	TaskID     string                `json:"task_id" gorm:"type:varchar(191);index"` // 第三方id，不一定有/ song id\ Task id
	Platform   constant.TaskPlatform `json:"platform" gorm:"type:varchar(30);index"` // 平台
	UserId     int                   `json:"user_id" gorm:"index"`
	Group      string                `json:"group" gorm:"type:varchar(50)"` // 修正计费用
	ChannelId  int                   `json:"channel_id" gorm:"index"`
	Quota      int                   `json:"quota"`
	Action     string                `json:"action" gorm:"type:varchar(40);index"` // 任务类型, song, lyrics, description-mode
	Status     TaskStatus            `json:"status" gorm:"type:varchar(20);index"` // 任务状态
	FailReason string                `json:"fail_reason"`
	SubmitTime int64                 `json:"submit_time" gorm:"index"`
	StartTime  int64                 `json:"start_time" gorm:"index"`
	FinishTime int64                 `json:"finish_time" gorm:"index"`
	Progress   string                `json:"progress" gorm:"type:varchar(20);index"`
	Properties Properties            `json:"properties" gorm:"type:json"`
	Username   string                `json:"username,omitempty" gorm:"-"`
	// 禁止返回给用户，内部可能包含key等隐私信息
	PrivateData TaskPrivateData `json:"-" gorm:"column:private_data;type:json"`
	Data        json.RawMessage `json:"data" gorm:"type:json"`
}

func (t *Task) SetData(data any) {
	b, _ := common.Marshal(data)
	t.Data = json.RawMessage(b)
}

func (t *Task) GetData(v any) error {
	return common.Unmarshal(t.Data, &v)
}

type Properties struct {
	Input             string `json:"input"`
	UpstreamModelName string `json:"upstream_model_name,omitempty"`
	OriginModelName   string `json:"origin_model_name,omitempty"`
}

func (m *Properties) Scan(val interface{}) error {
	bytesValue, _ := val.([]byte)
	if len(bytesValue) == 0 {
		*m = Properties{}
		return nil
	}
	return common.Unmarshal(bytesValue, m)
}

func (m Properties) Value() (driver.Value, error) {
	if m == (Properties{}) {
		return nil, nil
	}
	return common.Marshal(m)
}

type TaskPrivateData struct {
	Key            string `json:"key,omitempty"`
	UpstreamTaskID string `json:"upstream_task_id,omitempty"` // 上游真实 task ID
	ResultURL      string `json:"result_url,omitempty"`       // 任务成功后的结果 URL（视频地址等）
	// 计费上下文：用于异步退款/差额结算（轮询阶段读取）
	BillingSource               string              `json:"billing_source,omitempty"` // "wallet" 或 "subscription"
	BillingReservationRequestId string              `json:"billing_reservation_request_id,omitempty"`
	SubscriptionId              int                 `json:"subscription_id,omitempty"` // 订阅 ID，用于订阅退款
	TokenId                     int                 `json:"token_id,omitempty"`        // 令牌 ID，用于令牌额度退款
	NodeName                    string              `json:"node_name,omitempty"`       // 发起任务的节点名，轮询结算阶段据此归属日志而非最后查询节点
	BillingContext              *TaskBillingContext `json:"billing_context,omitempty"` // 计费参数快照（用于轮询阶段重新计算）
}

const TaskPricingKindVideoResolution = "video_resolution"

// TaskBillingContext 记录任务提交时的计费参数，以便轮询阶段可以重新计算额度。
type TaskBillingContext struct {
	ModelPrice               float64            `json:"model_price,omitempty"`                // 模型单价
	GroupRatio               float64            `json:"group_ratio,omitempty"`                // 分组倍率
	ModelRatio               float64            `json:"model_ratio,omitempty"`                // 模型倍率
	OtherRatios              map[string]float64 `json:"other_ratios,omitempty"`               // 附加倍率（时长、分辨率等）
	OriginModelName          string             `json:"origin_model_name,omitempty"`          // 模型名称，必须为OriginModelName
	PerCallBilling           bool               `json:"per_call_billing,omitempty"`           // 按次计费：跳过轮询阶段的差额结算
	PricingKind              string             `json:"pricing_kind,omitempty"`               // video_resolution 表示按分辨率快照计费
	EffectiveResolution      string             `json:"effective_resolution,omitempty"`       // 上游实际使用的 canonical 分辨率
	SelectedResolutionPrice  float64            `json:"selected_resolution_price,omitempty"`  // 选中的每秒分辨率价格
	EffectiveDurationSeconds int                `json:"effective_duration_seconds,omitempty"` // 提交时用于计费的有效时长
	SettledDurationSeconds   int                `json:"settled_duration_seconds,omitempty"`   // 上游完成时确认的有效时长
	QuotaPerUnit             float64            `json:"quota_per_unit,omitempty"`             // 提交时的额度换算基准
	IndependentRatios        map[string]float64 `json:"independent_ratios,omitempty"`         // 不重复表达分辨率或时长的独立倍率
	SettlementPending        bool               `json:"settlement_pending,omitempty"`         // 差额已入账，等待可靠发布日志/缓存并完成终态
	SettlementCompleted      bool               `json:"settlement_completed,omitempty"`       // 可靠发布已完成；终态 CAS 重试不得再次计费
	SettlementPreConsumed    int                `json:"settlement_pre_consumed,omitempty"`    // 可靠发布所需的原始预扣额度
	SettlementActualQuota    int                `json:"settlement_actual_quota,omitempty"`    // 可靠发布所需的最终额度
	SettlementQuotaClamp     *common.QuotaClamp `json:"settlement_quota_clamp,omitempty"`     // 可靠发布所需的饱和审计标记
}

// GetUpstreamTaskID 获取上游真实 task ID（用于与 provider 通信）
// 旧数据没有 UpstreamTaskID 时，TaskID 本身就是上游 ID
func (t *Task) GetUpstreamTaskID() string {
	if t.PrivateData.UpstreamTaskID != "" {
		return t.PrivateData.UpstreamTaskID
	}
	return t.TaskID
}

// GetResultURL 获取任务结果 URL（视频地址等）
// 新数据存在 PrivateData.ResultURL 中；旧数据回退到 FailReason（历史兼容）
func (t *Task) GetResultURL() string {
	if t.PrivateData.ResultURL != "" {
		return t.PrivateData.ResultURL
	}
	return t.FailReason
}

// GenerateTaskID 生成对外暴露的 task_xxxx 格式 ID
func GenerateTaskID() string {
	key, _ := common.GenerateRandomCharsKey(32)
	return "task_" + key
}

func (p *TaskPrivateData) Scan(val interface{}) error {
	bytesValue, _ := val.([]byte)
	if len(bytesValue) == 0 {
		return nil
	}
	return common.Unmarshal(bytesValue, p)
}

func (p TaskPrivateData) Value() (driver.Value, error) {
	if (p == TaskPrivateData{}) {
		return nil, nil
	}
	return common.Marshal(p)
}

// SyncTaskQueryParams 用于包含所有搜索条件的结构体，可以根据需求添加更多字段
type SyncTaskQueryParams struct {
	Platform       constant.TaskPlatform
	ChannelID      string
	TaskID         string
	TaskRecordID   int64
	UserID         string
	Action         string
	Status         string
	StartTimestamp int64
	EndTimestamp   int64
	UserIDs        []int
}

func InitTask(platform constant.TaskPlatform, relayInfo *commonRelay.RelayInfo) *Task {
	properties := Properties{}
	privateData := TaskPrivateData{}
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		if relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeGemini ||
			relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeVertexAi {
			privateData.Key = relayInfo.ChannelMeta.ApiKey
		}
		if relayInfo.UpstreamModelName != "" {
			properties.UpstreamModelName = relayInfo.UpstreamModelName
		}
		if relayInfo.OriginModelName != "" {
			properties.OriginModelName = relayInfo.OriginModelName
		}
	}

	// 使用预生成的公开 ID（如果有），否则新生成
	taskID := ""
	if relayInfo.TaskRelayInfo != nil && relayInfo.TaskRelayInfo.PublicTaskID != "" {
		taskID = relayInfo.TaskRelayInfo.PublicTaskID
	} else {
		taskID = GenerateTaskID()
	}

	t := &Task{
		TaskID:      taskID,
		UserId:      relayInfo.UserId,
		Group:       relayInfo.UsingGroup,
		SubmitTime:  time.Now().Unix(),
		Status:      TaskStatusNotStart,
		Progress:    "0%",
		ChannelId:   relayInfo.ChannelId,
		Platform:    platform,
		Properties:  properties,
		PrivateData: privateData,
	}
	return t
}

func TaskGetAllUserTask(userId int, startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB.Where("user_id = ?", userId)

	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.TaskRecordID > 0 {
		query = query.Where("id = ?", queryParams.TaskRecordID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.StartTimestamp != 0 {
		// 假设您已将前端传来的时间戳转换为数据库所需的时间格式，并处理了时间戳的验证和解析
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Omit("channel_id").Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func TaskGetAllTasks(startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB

	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.TaskRecordID > 0 {
		query = query.Where("id = ?", queryParams.TaskRecordID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func GetTimedOutUnfinishedTasks(cutoffUnix int64, limit int) []*Task {
	var tasks []*Task
	err := DB.Where("progress != ?", "100%").
		Where("status NOT IN ?", []string{TaskStatusFailure, TaskStatusSuccess}).
		Where("submit_time < ?", cutoffUnix).
		Order("submit_time").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetAllUnFinishSyncTasks(limit int) []*Task {
	var tasks []*Task
	var err error
	// get all tasks progress is not 100%
	err = DB.Where("progress != ?", "100%").Where("status != ?", TaskStatusFailure).Where("status != ?", TaskStatusSuccess).Limit(limit).Order("id").Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

// HasUnfinishedSyncTasks reports whether at least one async (Suno/video) task is
// still in progress. It is a cheap existence check (LIMIT 1) used to decide
// whether the async_task_poll system task needs to run; when no task is pending
// the scheduler skips creating a row entirely.
func HasUnfinishedSyncTasks() bool {
	var id int64
	err := DB.Model(&Task{}).
		Where("progress != ?", "100%").
		Where("status != ?", TaskStatusFailure).
		Where("status != ?", TaskStatusSuccess).
		Limit(1).
		Pluck("id", &id).Error
	return err == nil && id != 0
}

func GetByTaskId(userId int, taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("user_id = ? and task_id = ?", userId, taskId).
		First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

func GetByTaskRecordID(userId int, taskRecordID int64) (*Task, bool, error) {
	if taskRecordID <= 0 {
		return nil, false, nil
	}
	var task *Task
	err := DB.Where("user_id = ? and id = ?", userId, taskRecordID).
		First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, nil
}

func GetByTaskIds(userId int, taskIds []any) ([]*Task, error) {
	if len(taskIds) == 0 {
		return nil, nil
	}
	var task []*Task
	var err error
	err = DB.Where("user_id = ? and task_id in (?)", userId, taskIds).
		Find(&task).Error
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (task *Task) Insert() error {
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.PricingKind != TaskPricingKindVideoResolution {
		return DB.Create(task).Error
	}
	requestId := strings.TrimSpace(task.PrivateData.BillingReservationRequestId)
	if requestId == "" {
		return fmt.Errorf("resolution task requires a billing reservation requestId")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		reservation, err := lockResolutionBillingReservation(tx, requestId)
		if err != nil {
			return err
		}
		if reservation.Status != ResolutionReservationStatusReserved {
			return fmt.Errorf("resolution billing reservation %q cannot attach from status %s", requestId, reservation.Status)
		}
		if reservation.UserId != task.UserId || reservation.TokenId != task.PrivateData.TokenId ||
			reservation.BillingSource != task.PrivateData.BillingSource || reservation.SubscriptionId != task.PrivateData.SubscriptionId ||
			reservation.Quota != task.Quota {
			return fmt.Errorf("resolution billing reservation %q does not match task billing state", requestId)
		}
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		userResult := tx.Model(&User{}).Where("id = ?", task.UserId).Updates(map[string]interface{}{
			"used_quota":    gorm.Expr("used_quota + ?", task.Quota),
			"request_count": gorm.Expr("request_count + ?", 1),
		})
		if userResult.Error != nil {
			return userResult.Error
		}
		if userResult.RowsAffected != 1 {
			return fmt.Errorf("resolution task usage user %d was not updated", task.UserId)
		}
		if task.ChannelId > 0 {
			if task.Quota == 0 {
				var count int64
				if err := tx.Model(&Channel{}).Where("id = ?", task.ChannelId).Count(&count).Error; err != nil {
					return err
				}
				if count != 1 {
					return fmt.Errorf("resolution task usage channel %d was not found", task.ChannelId)
				}
			} else {
				channelResult := tx.Model(&Channel{}).Where("id = ?", task.ChannelId).
					Update("used_quota", gorm.Expr("used_quota + ?", task.Quota))
				if channelResult.Error != nil {
					return channelResult.Error
				}
				if channelResult.RowsAffected != 1 {
					return fmt.Errorf("resolution task usage channel %d was not updated", task.ChannelId)
				}
			}
		}
		attached := tx.Model(&ResolutionBillingReservation{}).
			Where("id = ? AND status = ?", reservation.Id, ResolutionReservationStatusReserved).
			Updates(map[string]interface{}{
				"status":     ResolutionReservationStatusAttached,
				"task_id":    task.ID,
				"last_error": "",
			})
		if attached.Error != nil {
			return attached.Error
		}
		if attached.RowsAffected != 1 {
			return fmt.Errorf("resolution billing reservation %q was not attached", requestId)
		}
		return nil
	})
}

type taskSnapshot struct {
	Status     TaskStatus
	Progress   string
	StartTime  int64
	FinishTime int64
	FailReason string
	ResultURL  string
	Data       json.RawMessage
}

func (s taskSnapshot) Equal(other taskSnapshot) bool {
	return s.Status == other.Status &&
		s.Progress == other.Progress &&
		s.StartTime == other.StartTime &&
		s.FinishTime == other.FinishTime &&
		s.FailReason == other.FailReason &&
		s.ResultURL == other.ResultURL &&
		bytes.Equal(s.Data, other.Data)
}

func (t *Task) Snapshot() taskSnapshot {
	return taskSnapshot{
		Status:     t.Status,
		Progress:   t.Progress,
		StartTime:  t.StartTime,
		FinishTime: t.FinishTime,
		FailReason: t.FailReason,
		ResultURL:  t.PrivateData.ResultURL,
		Data:       t.Data,
	}
}

func (Task *Task) Update() error {
	var err error
	err = DB.Save(Task).Error
	return err
}

func (t *Task) UpdateQuota() error {
	return DB.Model(t).Update("quota", t.Quota).Error
}

// SettleResolutionQuota commits a video-resolution task's funding delta,
// token delta, quota, and private billing snapshot in one main-database
// transaction. A failed transaction leaves the pre-consume state intact and
// is safe to retry with the same task snapshot. The bool reports whether this
// call applied a new settlement; false with a nil error means the exact state
// was already committed by an earlier attempt.
func (t *Task) SettleResolutionQuota(actualQuota int, fromStatus TaskStatus) (bool, error) {
	if t == nil || t.ID <= 0 {
		return false, fmt.Errorf("resolution task settlement requires a persisted task")
	}
	if actualQuota < 0 {
		return false, fmt.Errorf("resolution task settlement quota must not be negative")
	}
	bc := t.PrivateData.BillingContext
	if bc == nil || bc.PricingKind != TaskPricingKindVideoResolution {
		return false, fmt.Errorf("resolution task settlement requires a video-resolution snapshot")
	}
	if !bc.SettlementPending || bc.SettlementActualQuota != actualQuota || bc.SettlementPreConsumed < 0 {
		return false, fmt.Errorf("resolution task settlement outbox is incomplete")
	}

	expectedQuota := bc.SettlementPreConsumed
	desiredPrivateData := t.PrivateData
	quotaDelta := actualQuota - expectedQuota
	applied := false

	err := DB.Transaction(func(tx *gorm.DB) error {
		var stored Task
		if err := lockResolutionTaskForUpdate(tx, t.ID, &stored); err != nil {
			return err
		}
		storedBillingContext := stored.PrivateData.BillingContext
		if storedBillingContext == nil || storedBillingContext.PricingKind != TaskPricingKindVideoResolution {
			return fmt.Errorf("stored resolution task billing snapshot is missing")
		}
		if stored.Status != fromStatus {
			return fmt.Errorf("resolution task settlement status conflict: stored=%s expected=%s", stored.Status, fromStatus)
		}
		if storedBillingContext.SettlementPending {
			if stored.Quota == actualQuota &&
				storedBillingContext.SettlementPreConsumed == expectedQuota &&
				storedBillingContext.SettlementActualQuota == actualQuota {
				t.PrivateData = stored.PrivateData
				t.Quota = stored.Quota
				return nil
			}
			return fmt.Errorf("resolution task settlement outbox conflicts with persisted state")
		}
		if stored.Quota != expectedQuota {
			return fmt.Errorf("resolution task settlement quota conflict: stored=%d expected=%d", stored.Quota, expectedQuota)
		}

		if quotaDelta != 0 {
			if stored.PrivateData.BillingSource == "subscription" {
				if stored.PrivateData.SubscriptionId <= 0 {
					return fmt.Errorf("resolution task subscription settlement is missing subscription id")
				}
				var subscription UserSubscription
				if err := lockForUpdate(tx).Where("id = ?", stored.PrivateData.SubscriptionId).First(&subscription).Error; err != nil {
					return err
				}
				newUsed := subscription.AmountUsed + int64(quotaDelta)
				if newUsed < 0 {
					newUsed = 0
				}
				if subscription.AmountTotal > 0 && newUsed > subscription.AmountTotal {
					return fmt.Errorf("subscription used exceeds total, used=%d total=%d", newUsed, subscription.AmountTotal)
				}
				subscription.AmountUsed = newUsed
				if err := tx.Save(&subscription).Error; err != nil {
					return err
				}
			} else {
				userUpdate := tx.Model(&User{}).Where("id = ?", stored.UserId)
				if quotaDelta > 0 {
					userUpdate = userUpdate.Where("quota >= ?", quotaDelta)
				}
				result := userUpdate.UpdateColumn("quota", gorm.Expr("quota - ?", quotaDelta))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf("wallet quota is insufficient for resolution task settlement")
				}
			}

			if stored.PrivateData.TokenId > 0 {
				var token Token
				tokenErr := lockForUpdate(tx).Where("id = ?", stored.PrivateData.TokenId).First(&token).Error
				if tokenErr != nil && !errors.Is(tokenErr, gorm.ErrRecordNotFound) {
					return tokenErr
				}
				if tokenErr == nil {
					if quotaDelta > 0 && !token.UnlimitedQuota && token.RemainQuota < quotaDelta {
						return fmt.Errorf("token quota is insufficient for resolution task settlement")
					}
					if err := tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
						"remain_quota":  gorm.Expr("remain_quota - ?", quotaDelta),
						"used_quota":    gorm.Expr("used_quota + ?", quotaDelta),
						"accessed_time": common.GetTimestamp(),
					}).Error; err != nil {
						return err
					}
				}
			}

			userStatsResult := tx.Model(&User{}).Where("id = ?", stored.UserId).
				Update("used_quota", gorm.Expr("used_quota + ?", quotaDelta))
			if userStatsResult.Error != nil {
				return userStatsResult.Error
			}
			if userStatsResult.RowsAffected != 1 {
				return fmt.Errorf("resolution settlement usage user %d was not updated", stored.UserId)
			}
			channelStatsResult := tx.Model(&Channel{}).Where("id = ?", stored.ChannelId).
				Update("used_quota", gorm.Expr("used_quota + ?", quotaDelta))
			if channelStatsResult.Error != nil {
				return channelStatsResult.Error
			}
			if channelStatsResult.RowsAffected != 1 {
				return fmt.Errorf("resolution settlement usage channel %d was not updated", stored.ChannelId)
			}
		}

		result := tx.Model(&Task{}).
			Where("id = ? AND quota = ?", stored.ID, expectedQuota).
			Updates(map[string]interface{}{
				"quota":        actualQuota,
				"private_data": desiredPrivateData,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("resolution task settlement lost quota compare-and-swap")
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}

	t.Quota = actualQuota
	return applied, nil
}

// PublishResolutionSettlement reliably publishes a committed settlement and
// clears its durable pending marker. The task row lock serializes the
// deterministic log lookup/create across pollers. If publication fails, the
// marker remains and the still-nonterminal task is eligible for another poll.
func (t *Task) PublishResolutionSettlement(params RecordTaskBillingLogParams) error {
	if t == nil || t.ID <= 0 {
		return fmt.Errorf("resolution task publication requires a persisted task")
	}
	var publishedPrivateData TaskPrivateData
	var deferredExport bool
	var deferredExportCreatedAt int64
	var deferredExportUsername string
	var deferredExportParams RecordTaskBillingLogParams
	err := DB.Transaction(func(tx *gorm.DB) error {
		var stored Task
		if err := lockResolutionTaskForUpdate(tx, t.ID, &stored); err != nil {
			return err
		}
		bc := stored.PrivateData.BillingContext
		if bc == nil || bc.PricingKind != TaskPricingKindVideoResolution {
			return fmt.Errorf("stored resolution task billing snapshot is missing")
		}
		if !bc.SettlementPending {
			publishedPrivateData = stored.PrivateData
			return nil
		}

		params.UserId = stored.UserId
		params.ChannelId = stored.ChannelId
		params.TokenId = stored.PrivateData.TokenId
		params.Group = stored.Group
		params.NodeName = stored.PrivateData.NodeName
		params.RequestId = "task-resolution-settlement-" + strconv.FormatInt(stored.ID, 10)
		var tokenName string
		var tokenKey string
		if stored.PrivateData.TokenId > 0 {
			var token Token
			if err := tx.Unscoped().Select("name", commonKeyCol).Where("id = ?", stored.PrivateData.TokenId).First(&token).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			tokenName = token.Name
			tokenKey = token.Key
		}
		if bc.SettlementActualQuota != bc.SettlementPreConsumed {
			var username string
			if err := tx.Model(&User{}).Where("id = ?", stored.UserId).Select("username").Scan(&username).Error; err != nil {
				return err
			}
			logDB := LOG_DB
			if LOG_DB == DB {
				logDB = tx
			}
			created, createdAt, err := recordTaskBillingLogOnce(logDB, params, username, tokenName)
			if err != nil {
				return err
			}
			if created {
				if LOG_DB == DB {
					deferredExport = true
					deferredExportCreatedAt = createdAt
					deferredExportUsername = username
					deferredExportParams = params
				} else {
					exportTaskBillingLog(params, username, createdAt)
				}
			}
		}
		if err := invalidateUserCache(stored.UserId); err != nil {
			return err
		}
		if tokenKey != "" && common.RedisEnabled {
			if err := cacheDeleteToken(tokenKey); err != nil {
				return err
			}
		}

		bc.SettlementPending = false
		bc.SettlementCompleted = true
		bc.SettlementPreConsumed = 0
		bc.SettlementActualQuota = 0
		bc.SettlementQuotaClamp = nil
		if err := tx.Model(&Task{}).Where("id = ?", stored.ID).Update("private_data", stored.PrivateData).Error; err != nil {
			return err
		}
		publishedPrivateData = stored.PrivateData
		return nil
	})
	if err != nil {
		return err
	}
	if deferredExport {
		exportTaskBillingLog(deferredExportParams, deferredExportUsername, deferredExportCreatedAt)
	}
	t.PrivateData = publishedPrivateData
	return nil
}

func lockResolutionTaskForUpdate(tx *gorm.DB, taskID int64, task *Task) error {
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		// SQLite has no SELECT FOR UPDATE. Acquire its database-wide writer
		// lock before reading the billing ledger/outbox so independent polling
		// connections cannot both act on the same pending state.
		if err := tx.Model(&Task{}).Where("id = ?", taskID).
			UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
			return err
		}
	}
	return lockForUpdate(tx).Where("id = ?", taskID).First(task).Error
}

// UpdateWithStatus performs a conditional UPDATE guarded by fromStatus (CAS).
// Returns (true, nil) if this caller won the update, (false, nil) if
// another process already moved the task out of fromStatus. MySQL commonly
// reports changed rows rather than matched rows, so a same-value no-op update
// can also return false even when the status predicate still matched.
//
// Uses Model().Select("*").Updates() instead of Save() because GORM's Save
// falls back to INSERT ON CONFLICT when the WHERE-guarded UPDATE matches
// zero rows, which silently bypasses the CAS guard.
func (t *Task) UpdateWithStatus(fromStatus TaskStatus) (bool, error) {
	won := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var stored Task
		if err := lockForUpdate(tx).Where("id = ?", t.ID).First(&stored).Error; err != nil {
			return err
		}
		if stored.Status != fromStatus {
			return nil
		}
		if storedBC := stored.PrivateData.BillingContext; storedBC != nil && storedBC.PricingKind == TaskPricingKindVideoResolution {
			if storedBC.SettlementPending {
				return nil
			}
			if storedBC.SettlementCompleted {
				incomingBC := t.PrivateData.BillingContext
				if t.Status != TaskStatusSuccess || t.Quota != stored.Quota || incomingBC == nil || !reflect.DeepEqual(storedBC, incomingBC) {
					return nil
				}
			}
		}
		result := tx.Model(&Task{}).Where("id = ? AND status = ?", t.ID, fromStatus).Select("*").Updates(t)
		if result.Error != nil {
			return result.Error
		}
		won = result.RowsAffected > 0
		return nil
	})
	return won, err
}

// TaskBulkUpdateByID performs an unconditional bulk UPDATE by primary key IDs.
// WARNING: This function has NO CAS (Compare-And-Swap) guard — it will overwrite
// any concurrent status changes. DO NOT use in billing/quota lifecycle flows
// (e.g., timeout, success, failure transitions that trigger refunds or settlements).
// For status transitions that involve billing, use Task.UpdateWithStatus() instead.
func TaskBulkUpdateByID(ids []int64, params map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	return DB.Model(&Task{}).
		Where("id in (?)", ids).
		Updates(params).Error
}

type TaskQuotaUsage struct {
	Mode  string  `json:"mode"`
	Count float64 `json:"count"`
}

// TaskCountAllTasks returns total tasks that match the given query params (admin usage)
func TaskCountAllTasks(queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{})
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.TaskRecordID > 0 {
		query = query.Where("id = ?", queryParams.TaskRecordID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}

// TaskCountAllUserTask returns total tasks for given user
func TaskCountAllUserTask(userId int, queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{}).Where("user_id = ?", userId)
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.TaskRecordID > 0 {
		query = query.Where("id = ?", queryParams.TaskRecordID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}
func (t *Task) ToOpenAIVideo() *dto.OpenAIVideo {
	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = t.TaskID
	openAIVideo.Status = t.Status.ToVideoStatus()
	openAIVideo.Model = t.Properties.OriginModelName
	openAIVideo.SetProgressStr(t.Progress)
	openAIVideo.CreatedAt = t.CreatedAt
	openAIVideo.CompletedAt = t.UpdatedAt
	openAIVideo.SetMetadata("url", t.GetResultURL())
	return openAIVideo
}
