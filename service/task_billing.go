package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	resolutionBilling := info.TaskRelayInfo != nil &&
		info.TaskRelayInfo.BillingPlan != nil &&
		info.TaskRelayInfo.BillingPlan.Kind() == relaycommon.TaskBillingKindVideoResolution
	var resolvedVideoBilling *relaycommon.ResolvedVideoBilling
	if resolutionBilling {
		resolvedVideoBilling = info.TaskRelayInfo.ResolvedVideoBilling
	}
	// 支持任务仅按次计费：额度不随参数变化，但参数（时长等）仍需记录以便追溯
	perCallBilling := !resolutionBilling && ratio_setting.IsTaskPerCallBilling(info.OriginModelName)
	if perCallBilling {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	}
	otherRatios := info.PriceData.OtherRatios()
	if resolvedVideoBilling != nil {
		otherRatios = resolvedVideoBilling.Selection.IndependentRatios
		logContent = fmt.Sprintf("%s，按秒计费（%s）", logContent, resolvedVideoBilling.Selection.EffectiveResolution)
	}
	if len(otherRatios) > 0 {
		var contents []string
		for key, ra := range otherRatios {
			if 1.0 != ra {
				contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
			}
		}
		if len(contents) > 0 {
			label := "计算参数"
			if perCallBilling {
				// 按次计费下这些参数不参与额度计算，避免读日志的人误解为乘数
				label = "任务参数"
			}
			logContent = fmt.Sprintf("%s, %s：%s", logContent, label, strings.Join(contents, ", "))
		}
	}
	other := make(map[string]interface{})
	other["is_task"] = true
	other["request_path"] = c.Request.URL.Path
	if !resolutionBilling {
		other["model_price"] = info.PriceData.ModelPrice
	} else if resolvedVideoBilling != nil {
		adminInfo := map[string]interface{}{
			"video_resolution_billing": map[string]interface{}{
				"effective_resolution":       resolvedVideoBilling.Selection.EffectiveResolution,
				"selected_price_per_second":  resolvedVideoBilling.SelectedResolutionPrice,
				"submitted_duration_seconds": resolvedVideoBilling.Selection.EffectiveDurationSeconds,
				"effective_duration_seconds": resolvedVideoBilling.Selection.EffectiveDurationSeconds,
				"independent_ratios":         resolvedVideoBilling.Selection.IndependentRatios,
			},
		}
		other["admin_info"] = adminInfo
	}
	if perCallBilling {
		other["task_per_call_billing"] = true
	}
	if len(otherRatios) > 0 {
		other["task_ratios"] = otherRatios
	}
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	other["group_ratio"] = info.PriceData.GroupRatioInfo.GroupRatio
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	if info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = info.UpstreamModelName
	}
	attachQuotaSaturation(c, info, other)
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.OriginModelName,
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		Content:   logContent,
		TokenId:   info.TokenId,
		Group:     info.UsingGroup,
		Other:     other,
	})
	if !resolutionBilling {
		model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
		model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
	}
}

// PersistSubmittedTask 在上游已接受提交后把任务落库。
// 落库失败意味着用户已被扣费却没有可轮询、可结算的任务，因此必须同步退还分辨率
// 预留并记账；同步退款也失败时记录告警，由轮询的孤儿清扫兜底。
func PersistSubmittedTask(c *gin.Context, task *model.Task) error {
	insertErr := task.Insert()
	if insertErr == nil {
		return nil
	}
	requestId := task.PrivateData.BillingReservationRequestId
	if requestId == "" {
		return insertErr
	}
	refundedQuota, refundErr := model.RefundResolutionBillingReservation(requestId, "task insert failed: "+insertErr.Error())
	if refundErr != nil {
		logger.LogError(c, fmt.Sprintf("退还分辨率预留失败 (request_id=%s): %s", requestId, refundErr.Error()))
		return insertErr
	}
	logger.LogWarn(c, fmt.Sprintf("任务落库失败，已退还分辨率预留 (request_id=%s)", requestId))
	// 记账金额取自预留实际退还的额度，而不是任务上的额度：结算失败时两者会不同，
	// 而并发路径已经退过时 refundedQuota 为 0，不能再记一条重复的退款日志。
	if refundedQuota > 0 {
		other := taskBillingOther(task)
		other["reason"] = "task insert failed"
		model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
			UserId:    task.UserId,
			LogType:   model.LogTypeRefund,
			Content:   "任务提交后落库失败，已退还预扣费",
			ChannelId: task.ChannelId,
			ModelName: taskModelName(task),
			Quota:     refundedQuota,
			TokenId:   task.PrivateData.TokenId,
			Group:     task.Group,
			Other:     other,
		})
	}
	return insertErr
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// resolveTokenKey 通过 TokenId 运行时获取令牌 Key（用于 Redis 缓存操作）。
// 如果令牌已被删除或查询失败，返回空字符串。
func resolveTokenKey(ctx context.Context, tokenId int, taskID string) string {
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("获取令牌 key 失败 (tokenId=%d, task=%s): %s", tokenId, taskID, err.Error()))
		return ""
	}
	return token.Key
}

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding 调整任务的资金来源（钱包或订阅），delta > 0 表示扣费，delta < 0 表示退还。
func taskAdjustFunding(task *model.Task, delta int) error {
	if taskIsSubscription(task) {
		return model.PostConsumeUserSubscriptionDelta(task.PrivateData.SubscriptionId, int64(delta))
	}
	if delta > 0 {
		return model.DecreaseUserQuota(task.UserId, delta, false)
	}
	return model.IncreaseUserQuota(task.UserId, -delta, false)
}

// taskAdjustTokenQuota 调整任务的令牌额度，delta > 0 表示扣费，delta < 0 表示退还。
// 需要通过 resolveTokenKey 运行时获取 key（不从 PrivateData 中读取）。
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return
	}
	tokenKey := resolveTokenKey(ctx, task.PrivateData.TokenId, task.TaskID)
	if tokenKey == "" {
		return
	}
	var err error
	if delta > 0 {
		err = model.DecreaseTokenQuota(task.PrivateData.TokenId, tokenKey, delta)
	} else {
		err = model.IncreaseTokenQuota(task.PrivateData.TokenId, tokenKey, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("调整令牌额度失败 (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
	}
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) map[string]interface{} {
	other := make(map[string]interface{})
	if bc := task.PrivateData.BillingContext; bc != nil {
		if bc.PricingKind == model.TaskPricingKindVideoResolution {
			effectiveDuration := bc.SettledDurationSeconds
			if effectiveDuration <= 0 {
				effectiveDuration = bc.EffectiveDurationSeconds
			}
			billingInfo := map[string]interface{}{
				"effective_resolution":       bc.EffectiveResolution,
				"selected_price_per_second":  bc.SelectedResolutionPrice,
				"submitted_duration_seconds": bc.EffectiveDurationSeconds,
				"effective_duration_seconds": effectiveDuration,
			}
			if len(bc.IndependentRatios) > 0 {
				billingInfo["independent_ratios"] = bc.IndependentRatios
				other["task_ratios"] = bc.IndependentRatios
			}
			other["admin_info"] = map[string]interface{}{
				"video_resolution_billing": billingInfo,
			}
			other["group_ratio"] = bc.GroupRatio
		} else {
			other["model_price"] = bc.ModelPrice
			if bc.ModelRatio > 0 {
				other["model_ratio"] = bc.ModelRatio
			}
			other["group_ratio"] = bc.GroupRatio
			if priceData := taskBillingContextPriceData(bc); priceData != nil {
				for k, v := range priceData.OtherRatios() {
					other[k] = v
				}
			}
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = props.UpstreamModelName
	}
	return other
}

func taskBillingContextPriceData(bc *model.TaskBillingContext) *types.PriceData {
	if bc == nil || len(bc.OtherRatios) == 0 {
		return nil
	}
	priceData := &types.PriceData{}
	if !priceData.ReplaceOtherRatios(bc.OtherRatios) {
		return nil
	}
	return priceData
}

// CalculateVideoResolutionSnapshotQuota recalculates a video task exclusively
// from its frozen per-second resolution billing snapshot.
func CalculateVideoResolutionSnapshotQuota(bc *model.TaskBillingContext, effectiveDurationSeconds int) (int, *common.QuotaClamp, error) {
	if bc == nil || bc.PricingKind != model.TaskPricingKindVideoResolution {
		return 0, nil, fmt.Errorf("video resolution billing snapshot is missing")
	}
	resolution, err := common.NormalizeVideoResolutionKey(bc.EffectiveResolution)
	if err != nil || resolution != bc.EffectiveResolution {
		return 0, nil, fmt.Errorf("video resolution billing snapshot has invalid resolution")
	}
	return relaycommon.CalculateVideoResolutionQuotaAtUnit(
		bc.SelectedResolutionPrice,
		effectiveDurationSeconds,
		bc.GroupRatio,
		bc.IndependentRatios,
		bc.QuotaPerUnit,
	)
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，将预扣的 quota 退还给用户（支持钱包和订阅），并退还令牌额度。
// 返回资金来源是否已成功退还；失败时保留 quota，供显式重试或人工对账。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) bool {
	quota := task.Quota
	if quota == 0 {
		return true
	}

	// 1. 退还资金来源（钱包或订阅）
	if err := taskAdjustFunding(task, -quota); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("退还资金来源失败 task %s: %s", task.TaskID, err.Error()))
		return false
	}

	// 2. 退还令牌额度
	taskAdjustTokenQuota(ctx, task, -quota)

	// 3. 记录日志
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["reason"] = reason
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     quota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
	})

	// 4. 资金退款完成后再清除持久化标记。
	// 回写失败必须显式告警，避免漏掉潜在的重复退款风险。
	task.Quota = 0
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("退款成功但清除 task quota 失败 task %s: %s", task.TaskID, err.Error()))
	}
	return true
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
// clamps 可选：若计算 actualQuota 时发生额度饱和，将其记入日志 admin_info（仅管理员可见）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, clamps ...*common.QuotaClamp) {
	if actualQuota <= 0 {
		return
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	if quotaDelta == 0 {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	// 调整资金来源
	if err := taskAdjustFunding(task, quotaDelta); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
		return
	}

	// 调整令牌额度
	taskAdjustTokenQuota(ctx, task, quotaDelta)

	task.Quota = actualQuota
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算回写 quota 失败 task %s: %s", task.TaskID, err.Error()))
	}

	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
		model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
	} else {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = preConsumedQuota
	other["actual_quota"] = actualQuota
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   logType,
		Content:   reason,
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     logQuota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
		NodeName:  task.PrivateData.NodeName,
	})
}

// RecalculateResolutionTaskQuota atomically commits the resolution task's
// funding/token delta together with its quota and private billing snapshot.
// A false result means no settlement was committed, so callers must restore
// any in-memory snapshot mutation; retrying the same frozen calculation is
// safe because the model layer compares the persisted pre-consume state.
func RecalculateResolutionTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, clamp *common.QuotaClamp, fromStatuses ...model.TaskStatus) bool {
	if actualQuota < 0 {
		return false
	}
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.PricingKind != model.TaskPricingKindVideoResolution {
		return false
	}
	if bc.SettlementCompleted {
		return true
	}
	fromStatus := task.Status
	if len(fromStatuses) > 0 {
		fromStatus = fromStatuses[0]
	}

	preConsumedQuota := task.Quota
	if bc.SettlementPending {
		if bc.SettlementActualQuota != actualQuota || bc.SettlementPreConsumed < 0 {
			logger.LogError(ctx, fmt.Sprintf("resolution task settlement outbox conflict for task %s", task.TaskID))
			return false
		}
		preConsumedQuota = bc.SettlementPreConsumed
	} else {
		bc.SettlementPending = true
		bc.SettlementPreConsumed = preConsumedQuota
		bc.SettlementActualQuota = actualQuota
		bc.SettlementQuotaClamp = clamp
		if _, err := task.SettleResolutionQuota(actualQuota, fromStatus); err != nil {
			bc.SettlementPending = false
			bc.SettlementPreConsumed = 0
			bc.SettlementActualQuota = 0
			bc.SettlementQuotaClamp = nil
			logger.LogError(ctx, fmt.Sprintf("resolution task settlement failed for task %s: %s", task.TaskID, err.Error()))
			return false
		}
	}

	quotaDelta := actualQuota - preConsumedQuota
	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
	} else if quotaDelta < 0 {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = preConsumedQuota
	other["actual_quota"] = actualQuota
	attachQuotaSaturationToOther(other, bc.SettlementQuotaClamp)
	if err := task.PublishResolutionSettlement(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   logType,
		Content:   reason,
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     logQuota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
		NodeName:  task.PrivateData.NodeName,
	}); err != nil {
		logger.LogError(ctx, fmt.Sprintf("resolution task settlement publication failed for task %s: %s", task.TaskID, err.Error()))
		return false
	}

	if quotaDelta == 0 {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return true
	}
	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))
	return true
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) {
	if totalTokens <= 0 {
		return
	}

	modelName := taskModelName(task)

	// 获取模型价格和倍率
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	// 只有配置了倍率(非固定价格)时才按 token 重新计费
	if !hasRatioSetting || modelRatio <= 0 {
		return
	}

	// 获取用户和组的倍率信息
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return
	}

	groupRatio := ratio_setting.GetGroupRatio(group)
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

	var finalGroupRatio float64
	if hasUserGroupRatio {
		finalGroupRatio = userGroupRatio
	} else {
		finalGroupRatio = groupRatio
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if priceData := taskBillingContextPriceData(task.PrivateData.BillingContext); priceData != nil {
		otherMultiplier = priceData.OtherRatioMultiplier()
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier（饱和转换，防止溢出成负数）
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
}
