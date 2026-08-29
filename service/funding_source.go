package service

import (
	"time"

	"github.com/QuantumNous/new-api/model"
)

// ---------------------------------------------------------------------------
// FundingSource — 资金来源接口（钱包 or 订阅）
// ---------------------------------------------------------------------------

// FundingSource 抽象了预扣费的资金来源。
type FundingSource interface {
	// Source 返回资金来源标识："wallet" 或 "subscription"
	Source() string
	// PreConsume 从该资金来源预扣 amount 额度
	PreConsume(amount int) error
	// Settle 根据差额调整资金来源（正数补扣，负数退还）
	Settle(delta int) error
	// Refund 退还所有预扣费
	Refund() error
}

// ---------------------------------------------------------------------------
// WalletFunding — 钱包资金来源实现
// ---------------------------------------------------------------------------

type WalletFunding struct {
	userId   int
	consumed int // 实际预扣的用户额度
}

func (w *WalletFunding) Source() string { return BillingSourceWallet }

func (w *WalletFunding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}
	if err := model.DecreaseUserQuota(w.userId, amount, false); err != nil {
		return err
	}
	w.consumed = amount
	return nil
}

func (w *WalletFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		return model.DecreaseUserQuota(w.userId, delta, false)
	}
	return model.IncreaseUserQuota(w.userId, -delta, false)
}

func (w *WalletFunding) Refund() error {
	if w.consumed <= 0 {
		return nil
	}
	// IncreaseUserQuota 是 quota += N 的非幂等操作，不能重试，否则会多退额度。
	// 订阅的 RefundSubscriptionPreConsume 有 requestId 幂等保护所以可以重试。
	return model.IncreaseUserQuota(w.userId, w.consumed, false)
}

// ---------------------------------------------------------------------------
// SubscriptionFunding — 订阅资金来源实现
// ---------------------------------------------------------------------------

type SubscriptionFunding struct {
	requestId      string
	userId         int
	modelName      string
	amount         int64 // 预扣的订阅额度（subConsume）
	subscriptionId int
	preConsumed    int64
	// 以下字段在 PreConsume 成功后填充，供 RelayInfo 同步使用
	AmountTotal     int64
	AmountUsedAfter int64
	PlanId          int
	PlanTitle       string
}

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }

func (s *SubscriptionFunding) PreConsume(_ int) error {
	// amount 参数被忽略，使用内部 s.amount（已在构造时根据 preConsumedQuota 计算）
	res, err := model.PreConsumeUserSubscription(s.requestId, s.userId, s.modelName, 0, s.amount)
	if err != nil {
		return err
	}
	s.subscriptionId = res.UserSubscriptionId
	s.preConsumed = res.PreConsumed
	s.AmountTotal = res.AmountTotal
	s.AmountUsedAfter = res.AmountUsedAfter
	// 获取订阅计划信息
	if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(res.UserSubscriptionId); err == nil && planInfo != nil {
		s.PlanId = planInfo.PlanId
		s.PlanTitle = planInfo.PlanTitle
	}
	return nil
}

func (s *SubscriptionFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	return model.PostConsumeUserSubscriptionDelta(s.subscriptionId, int64(delta))
}

func (s *SubscriptionFunding) Refund() error {
	if s.preConsumed <= 0 {
		return nil
	}
	return refundWithRetry(func() error {
		return model.RefundSubscriptionPreConsume(s.requestId)
	})
}

// ---------------------------------------------------------------------------
// ResolutionReservationFunding — 分辨率任务的预留账本资金来源
// ---------------------------------------------------------------------------

// ResolutionReservationFunding 通过 ResolutionBillingReservation 账本预扣费。
// 与钱包/订阅实现不同，它在同一个事务里扣减资金来源和令牌额度，并留下一条
// 以 requestId 为幂等键的持久记录：Task.Insert 附着该记录，插入失败时可以
// 同步退款，两者都没发生时由轮询的孤儿清扫兜底。
type ResolutionReservationFunding struct {
	requestId    string
	userId       int
	tokenId      int
	modelName    string
	isPlayground bool
	source       string

	reserved       int
	subscriptionId int
	preConsumed    int64
	// 以下字段在预留成功后填充，供 RelayInfo 同步使用
	AmountTotal     int64
	AmountUsedAfter int64
	PlanId          int
	PlanTitle       string
}

func (r *ResolutionReservationFunding) Source() string { return r.source }

func (r *ResolutionReservationFunding) PreConsume(amount int) error {
	result, err := model.ReserveResolutionBilling(model.ResolutionBillingReservationParams{
		RequestId:     r.requestId,
		UserId:        r.userId,
		TokenId:       r.tokenId,
		BillingSource: r.source,
		Quota:         amount,
		ModelName:     r.modelName,
		IsPlayground:  r.isPlayground,
	})
	if err != nil {
		return err
	}
	r.reserved = amount
	r.applyReservationResult(result)
	return nil
}

func (r *ResolutionReservationFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	result, err := model.AdjustResolutionBillingReservation(r.requestId, r.reserved+delta)
	if err != nil {
		return err
	}
	r.reserved += delta
	r.applyReservationResult(result)
	return nil
}

func (r *ResolutionReservationFunding) Refund() error {
	// RefundResolutionBillingReservation 以 requestId 为幂等键且完全事务化，重试安全。
	return refundWithRetry(func() error {
		_, err := model.RefundResolutionBillingReservation(r.requestId, "resolution task submission failed")
		return err
	})
}

func (r *ResolutionReservationFunding) applyReservationResult(result *model.ResolutionBillingReservationResult) {
	if result == nil {
		return
	}
	r.subscriptionId = result.SubscriptionId
	r.preConsumed = result.PreConsumed
	r.AmountTotal = result.AmountTotal
	r.AmountUsedAfter = result.AmountUsedAfter
	if r.source != BillingSourceSubscription || r.subscriptionId <= 0 || r.PlanId > 0 {
		return
	}
	if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(r.subscriptionId); err == nil && planInfo != nil {
		r.PlanId = planInfo.PlanId
		r.PlanTitle = planInfo.PlanTitle
	}
}

// refundWithRetry 尝试多次执行退款操作以提高成功率，只能用于基于事务的退款函数！！！！！！
// try to refund with retries, only for refund functions based on transactions!!!
func refundWithRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	const maxAttempts = 3
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < maxAttempts-1 {
			time.Sleep(time.Duration(200*(i+1)) * time.Millisecond)
		}
	}
	return lastErr
}
