package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ResolutionReservationSourceWallet       = "wallet"
	ResolutionReservationSourceSubscription = "subscription"

	ResolutionReservationStatusReserved = "reserved"
	ResolutionReservationStatusAttached = "attached"
	ResolutionReservationStatusRefunded = "refunded"
)

// ResolutionBillingReservation is the durable idempotency boundary between
// accepting a resolution-priced task upstream and persisting its local Task.
// Funding and token quota are reserved in the same main-database transaction
// that creates this row. A later Task.Insert attaches the reservation, while a
// failed insert or timeout sweep can refund an unattached row by RequestId.
type ResolutionBillingReservation struct {
	Id             int64  `json:"id"`
	RequestId      string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId         int    `json:"user_id" gorm:"index"`
	TokenId        int    `json:"token_id" gorm:"index"`
	BillingSource  string `json:"billing_source" gorm:"type:varchar(32)"`
	SubscriptionId int    `json:"subscription_id" gorm:"index"`
	Quota          int    `json:"quota"`
	ModelName      string `json:"model_name" gorm:"type:varchar(191)"`
	IsPlayground   bool   `json:"is_playground"`
	Status         string `json:"status" gorm:"type:varchar(32);index"`
	TaskId         int64  `json:"task_id" gorm:"index"`
	RefundAttempts int    `json:"refund_attempts"`
	RefundReason   string `json:"refund_reason" gorm:"type:text"`
	LastError      string `json:"last_error" gorm:"type:text"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt      int64  `json:"updated_at" gorm:"bigint;index"`
}

func (r *ResolutionBillingReservation) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

func (r *ResolutionBillingReservation) BeforeUpdate(_ *gorm.DB) error {
	r.UpdatedAt = common.GetTimestamp()
	return nil
}

type ResolutionBillingReservationParams struct {
	RequestId     string
	UserId        int
	TokenId       int
	BillingSource string
	Quota         int
	ModelName     string
	IsPlayground  bool
}

type ResolutionBillingReservationResult struct {
	SubscriptionId   int
	PreConsumed      int64
	AmountTotal      int64
	AmountUsedBefore int64
	AmountUsedAfter  int64
}

func ReserveResolutionBilling(params ResolutionBillingReservationParams) (*ResolutionBillingReservationResult, error) {
	params.RequestId = strings.TrimSpace(params.RequestId)
	if err := validateResolutionBillingReservationParams(params); err != nil {
		return nil, err
	}
	now := GetDBTimestamp()
	var result *ResolutionBillingReservationResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		reservation := ResolutionBillingReservation{
			RequestId: params.RequestId, UserId: params.UserId, TokenId: params.TokenId,
			BillingSource: params.BillingSource, Quota: params.Quota,
			ModelName: params.ModelName, IsPlayground: params.IsPlayground,
			Status: ResolutionReservationStatusReserved,
		}
		created := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "request_id"}},
			DoNothing: true,
		}).Create(&reservation)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			stored, err := lockResolutionBillingReservation(tx, params.RequestId)
			if err != nil {
				return err
			}
			if err := validateMatchingResolutionReservation(stored, params); err != nil {
				return err
			}
			if stored.Status == ResolutionReservationStatusRefunded {
				return fmt.Errorf("resolution billing reservation %q was already refunded", params.RequestId)
			}
			result, err = resolutionReservationResultTx(tx, stored)
			return err
		}

		var subscriptionResult *SubscriptionPreConsumeResult
		if params.BillingSource == ResolutionReservationSourceWallet {
			if err := debitResolutionWalletTx(tx, params.UserId, params.Quota); err != nil {
				return err
			}
		} else {
			var err error
			subscriptionResult, err = preConsumeUserSubscriptionTx(tx, params.RequestId, params.UserId, params.ModelName, 0, int64(params.Quota), now)
			if err != nil {
				return err
			}
			reservation.SubscriptionId = subscriptionResult.UserSubscriptionId
		}
		if !params.IsPlayground {
			if err := adjustResolutionTokenTx(tx, params.TokenId, params.UserId, params.Quota); err != nil {
				return err
			}
		}
		if reservation.SubscriptionId > 0 {
			if err := tx.Model(&ResolutionBillingReservation{}).
				Where("id = ?", reservation.Id).
				Update("subscription_id", reservation.SubscriptionId).Error; err != nil {
				return err
			}
		}
		if err := invalidateResolutionBillingCaches(tx, params.UserId, params.TokenId, params.IsPlayground); err != nil {
			return err
		}
		if subscriptionResult == nil {
			result = &ResolutionBillingReservationResult{}
		} else {
			result = &ResolutionBillingReservationResult{
				SubscriptionId:   subscriptionResult.UserSubscriptionId,
				PreConsumed:      subscriptionResult.PreConsumed,
				AmountTotal:      subscriptionResult.AmountTotal,
				AmountUsedBefore: subscriptionResult.AmountUsedBefore,
				AmountUsedAfter:  subscriptionResult.AmountUsedAfter,
			}
		}
		return nil
	})
	return result, err
}

func validateResolutionBillingReservationParams(params ResolutionBillingReservationParams) error {
	if params.RequestId == "" || len(params.RequestId) > 64 {
		return errors.New("resolution billing reservation requestId is invalid")
	}
	if params.UserId <= 0 {
		return errors.New("resolution billing reservation userId is invalid")
	}
	if params.Quota < 0 {
		return errors.New("resolution billing reservation quota must not be negative")
	}
	if params.BillingSource != ResolutionReservationSourceWallet && params.BillingSource != ResolutionReservationSourceSubscription {
		return fmt.Errorf("unsupported resolution billing source %q", params.BillingSource)
	}
	if !params.IsPlayground && params.TokenId <= 0 {
		return errors.New("resolution billing reservation tokenId is invalid")
	}
	return nil
}

func validateMatchingResolutionReservation(stored *ResolutionBillingReservation, params ResolutionBillingReservationParams) error {
	if stored.UserId != params.UserId || stored.TokenId != params.TokenId || stored.BillingSource != params.BillingSource ||
		stored.Quota != params.Quota || stored.ModelName != params.ModelName || stored.IsPlayground != params.IsPlayground {
		return fmt.Errorf("resolution billing reservation %q does not match the original request", params.RequestId)
	}
	return nil
}

func lockResolutionBillingReservation(tx *gorm.DB, requestId string) (*ResolutionBillingReservation, error) {
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		result := tx.Model(&ResolutionBillingReservation{}).
			Where("request_id = ?", requestId).
			UpdateColumn("id", gorm.Expr("id"))
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, gorm.ErrRecordNotFound
		}
	}
	var reservation ResolutionBillingReservation
	if err := lockForUpdate(tx).Where("request_id = ?", requestId).First(&reservation).Error; err != nil {
		return nil, err
	}
	return &reservation, nil
}

func resolutionReservationResultTx(tx *gorm.DB, reservation *ResolutionBillingReservation) (*ResolutionBillingReservationResult, error) {
	result := &ResolutionBillingReservationResult{SubscriptionId: reservation.SubscriptionId, PreConsumed: int64(reservation.Quota)}
	if reservation.SubscriptionId <= 0 {
		return result, nil
	}
	var subscription UserSubscription
	if err := tx.Where("id = ?", reservation.SubscriptionId).First(&subscription).Error; err != nil {
		return nil, err
	}
	result.AmountTotal = subscription.AmountTotal
	result.AmountUsedBefore = subscription.AmountUsed - int64(reservation.Quota)
	result.AmountUsedAfter = subscription.AmountUsed
	return result, nil
}

func debitResolutionWalletTx(tx *gorm.DB, userId, quota int) error {
	if quota == 0 {
		var count int64
		if err := tx.Model(&User{}).Where("id = ?", userId).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
	result := tx.Model(&User{}).Where("id = ? AND quota >= ?", userId, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: need=%d", ErrInsufficientUserQuota, quota)
	}
	return nil
}

func adjustResolutionTokenTx(tx *gorm.DB, tokenId, userId, quota int) error {
	var token Token
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", tokenId, userId).First(&token).Error; err != nil {
		return err
	}
	if quota > 0 && !token.UnlimitedQuota && token.RemainQuota < quota {
		return fmt.Errorf("%w: need=%d remain=%d", ErrInsufficientTokenQuota, quota, token.RemainQuota)
	}
	if quota == 0 {
		return nil
	}
	return tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
		"remain_quota":  gorm.Expr("remain_quota - ?", quota),
		"used_quota":    gorm.Expr("used_quota + ?", quota),
		"accessed_time": common.GetTimestamp(),
	}).Error
}

func invalidateResolutionBillingCaches(tx *gorm.DB, userId, tokenId int, isPlayground bool) error {
	if !common.RedisEnabled {
		return nil
	}
	if err := invalidateUserCache(userId); err != nil {
		return err
	}
	if isPlayground {
		return nil
	}
	var token Token
	if err := tx.Select("key").Where("id = ?", tokenId).First(&token).Error; err != nil {
		return err
	}
	return cacheDeleteToken(token.Key)
}

// AdjustResolutionBillingReservation atomically changes an unattached
// reservation to targetQuota. It is used when an accepted upstream response
// reports a quota different from the estimate made before submission.
func AdjustResolutionBillingReservation(requestId string, targetQuota int) (*ResolutionBillingReservationResult, error) {
	if strings.TrimSpace(requestId) == "" || targetQuota < 0 {
		return nil, errors.New("invalid resolution billing reservation adjustment")
	}
	var result *ResolutionBillingReservationResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		reservation, err := lockResolutionBillingReservation(tx, requestId)
		if err != nil {
			return err
		}
		if reservation.Status != ResolutionReservationStatusReserved {
			if reservation.Status == ResolutionReservationStatusAttached && reservation.Quota == targetQuota {
				result, err = resolutionReservationResultTx(tx, reservation)
				return err
			}
			return fmt.Errorf("resolution billing reservation %q cannot be adjusted from status %s", requestId, reservation.Status)
		}
		delta := targetQuota - reservation.Quota
		if delta == 0 {
			result, err = resolutionReservationResultTx(tx, reservation)
			return err
		}
		if reservation.BillingSource == ResolutionReservationSourceWallet {
			if delta > 0 {
				if err := debitResolutionWalletTx(tx, reservation.UserId, delta); err != nil {
					return err
				}
			} else if err := tx.Model(&User{}).Where("id = ?", reservation.UserId).
				Update("quota", gorm.Expr("quota + ?", -delta)).Error; err != nil {
				return err
			}
		} else if err := adjustResolutionSubscriptionTx(tx, reservation, delta); err != nil {
			return err
		}
		if !reservation.IsPlayground {
			if err := adjustResolutionTokenDeltaTx(tx, reservation.TokenId, reservation.UserId, delta); err != nil {
				return err
			}
		}
		if err := tx.Model(&ResolutionBillingReservation{}).Where("id = ?", reservation.Id).
			Updates(map[string]interface{}{"quota": targetQuota, "last_error": ""}).Error; err != nil {
			return err
		}
		reservation.Quota = targetQuota
		if err := invalidateResolutionBillingCaches(tx, reservation.UserId, reservation.TokenId, reservation.IsPlayground); err != nil {
			return err
		}
		result, err = resolutionReservationResultTx(tx, reservation)
		return err
	})
	return result, err
}

func adjustResolutionSubscriptionTx(tx *gorm.DB, reservation *ResolutionBillingReservation, delta int) error {
	if reservation.SubscriptionId <= 0 {
		return errors.New("resolution subscription reservation is missing subscriptionId")
	}
	var subscription UserSubscription
	if err := lockForUpdate(tx).Where("id = ?", reservation.SubscriptionId).First(&subscription).Error; err != nil {
		return err
	}
	newUsed := subscription.AmountUsed + int64(delta)
	if newUsed < 0 {
		return errors.New("resolution subscription reservation would make used quota negative")
	}
	if subscription.AmountTotal > 0 && newUsed > subscription.AmountTotal {
		return fmt.Errorf("subscription quota insufficient, need=%d", delta)
	}
	if err := tx.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Update("amount_used", newUsed).Error; err != nil {
		return err
	}
	newPreConsumed := int64(reservation.Quota + delta)
	result := tx.Model(&SubscriptionPreConsumeRecord{}).
		Where("request_id = ? AND status = ?", reservation.RequestId, "consumed").
		Update("pre_consumed", newPreConsumed)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("resolution subscription pre-consume record was not updated")
	}
	return nil
}

func adjustResolutionTokenDeltaTx(tx *gorm.DB, tokenId, userId, delta int) error {
	if delta == 0 {
		return nil
	}
	var token Token
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", tokenId, userId).First(&token).Error; err != nil {
		return err
	}
	if delta > 0 && !token.UnlimitedQuota && token.RemainQuota < delta {
		return fmt.Errorf("%w: need=%d remain=%d", ErrInsufficientTokenQuota, delta, token.RemainQuota)
	}
	return tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
		"remain_quota":  gorm.Expr("remain_quota - ?", delta),
		"used_quota":    gorm.Expr("used_quota + ?", delta),
		"accessed_time": common.GetTimestamp(),
	}).Error
}

func RefundResolutionBillingReservation(requestId, reason string) error {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return errors.New("resolution billing reservation requestId is empty")
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		reservation, err := lockResolutionBillingReservation(tx, requestId)
		if err != nil {
			return err
		}
		if reservation.Status == ResolutionReservationStatusRefunded {
			return nil
		}
		if reservation.Status != ResolutionReservationStatusReserved {
			return fmt.Errorf("resolution billing reservation %q cannot be refunded from status %s", requestId, reservation.Status)
		}
		if reservation.BillingSource == ResolutionReservationSourceWallet {
			if reservation.Quota > 0 {
				result := tx.Model(&User{}).Where("id = ?", reservation.UserId).
					Update("quota", gorm.Expr("quota + ?", reservation.Quota))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return errors.New("resolution reservation wallet refund did not update the user")
				}
			}
		} else if err := refundResolutionSubscriptionTx(tx, reservation); err != nil {
			return err
		}
		if !reservation.IsPlayground && reservation.Quota > 0 {
			result := tx.Model(&Token{}).Where("id = ? AND user_id = ?", reservation.TokenId, reservation.UserId).
				Updates(map[string]interface{}{
					"remain_quota":  gorm.Expr("remain_quota + ?", reservation.Quota),
					"used_quota":    gorm.Expr("used_quota - ?", reservation.Quota),
					"accessed_time": common.GetTimestamp(),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("resolution reservation token refund did not update the token")
			}
		}
		if err := invalidateResolutionBillingCaches(tx, reservation.UserId, reservation.TokenId, reservation.IsPlayground); err != nil {
			return err
		}
		updates := map[string]interface{}{
			"status": ResolutionReservationStatusRefunded, "refund_attempts": gorm.Expr("refund_attempts + ?", 1),
			"refund_reason": reason, "last_error": "", "task_id": 0,
		}
		return tx.Model(&ResolutionBillingReservation{}).Where("id = ?", reservation.Id).Updates(updates).Error
	})
	if err == nil {
		return nil
	}
	audit := DB.Model(&ResolutionBillingReservation{}).
		Where("request_id = ? AND status = ?", requestId, ResolutionReservationStatusReserved).
		Updates(map[string]interface{}{
			"refund_attempts": gorm.Expr("refund_attempts + ?", 1),
			"refund_reason":   reason,
			"last_error":      err.Error(),
		})
	if audit.Error != nil {
		common.SysError(fmt.Sprintf("failed to audit resolution reservation refund %s: %v (refund error: %v)", requestId, audit.Error, err))
	} else {
		common.SysError(fmt.Sprintf("resolution reservation refund failed request_id=%s: %v", requestId, err))
	}
	return err
}

func refundResolutionSubscriptionTx(tx *gorm.DB, reservation *ResolutionBillingReservation) error {
	if reservation.SubscriptionId <= 0 {
		return errors.New("resolution subscription reservation is missing subscriptionId")
	}
	var subscription UserSubscription
	if err := lockForUpdate(tx).Where("id = ?", reservation.SubscriptionId).First(&subscription).Error; err != nil {
		return err
	}
	if subscription.AmountUsed < int64(reservation.Quota) {
		return errors.New("resolution subscription refund exceeds used quota")
	}
	if reservation.Quota > 0 {
		if err := tx.Model(&UserSubscription{}).Where("id = ?", subscription.Id).
			Update("amount_used", subscription.AmountUsed-int64(reservation.Quota)).Error; err != nil {
			return err
		}
	}
	result := tx.Model(&SubscriptionPreConsumeRecord{}).
		Where("request_id = ? AND status = ?", reservation.RequestId, "consumed").
		Update("status", "refunded")
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("resolution subscription refund did not update the pre-consume record")
	}
	return nil
}

func RefundOrphanedResolutionBillingReservations(cutoff int64, limit int) (int, error) {
	if cutoff <= 0 {
		return 0, errors.New("resolution reservation orphan cutoff must be positive")
	}
	if limit <= 0 {
		limit = 100
	}
	var reservations []ResolutionBillingReservation
	if err := DB.Where("status = ? AND updated_at < ?", ResolutionReservationStatusReserved, cutoff).
		Order("id").Limit(limit).Find(&reservations).Error; err != nil {
		return 0, err
	}
	refunded := 0
	for _, reservation := range reservations {
		if err := RefundResolutionBillingReservation(reservation.RequestId, "orphaned resolution reservation timed out"); err != nil {
			return refunded, err
		}
		refunded++
	}
	return refunded, nil
}
