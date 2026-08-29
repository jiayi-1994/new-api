package model

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedResolutionReservationUserAndToken(t *testing.T, userID, tokenID, userQuota, tokenQuota int, unlimited bool) {
	t.Helper()
	require.NoError(t, DB.Create(&User{Id: userID, Username: "reservation-user", Password: "password", Status: common.UserStatusEnabled, Quota: userQuota}).Error)
	require.NoError(t, DB.Create(&Token{
		Id:             tokenID,
		UserId:         userID,
		Key:            "sk-resolution-reservation",
		Name:           "reservation-token",
		Status:         common.TokenStatusEnabled,
		RemainQuota:    tokenQuota,
		UnlimitedQuota: unlimited,
	}).Error)
}

func TestResolutionReservationAtomicallyPreConsumesWalletAndUnlimitedTokenOnce(t *testing.T) {
	truncateTables(t)
	seedResolutionReservationUserAndToken(t, 9601, 9601, 100, 0, true)
	params := ResolutionBillingReservationParams{
		RequestId:     "resolution-wallet-concurrent",
		UserId:        9601,
		TokenId:       9601,
		BillingSource: ResolutionReservationSourceWallet,
		Quota:         80,
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var requests sync.WaitGroup
	for range 2 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-start
			_, err := ReserveResolutionBilling(params)
			results <- err
		}()
	}
	close(start)
	requests.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}

	var user User
	require.NoError(t, DB.First(&user, 9601).Error)
	assert.Equal(t, 20, user.Quota)
	var token Token
	require.NoError(t, DB.First(&token, 9601).Error)
	assert.Equal(t, -80, token.RemainQuota)
	assert.Equal(t, 80, token.UsedQuota)
	var records []ResolutionBillingReservation
	require.NoError(t, DB.Find(&records).Error)
	require.Len(t, records, 1)
	assert.Equal(t, ResolutionReservationStatusReserved, records[0].Status)
}

func TestResolutionReservationAtomicallyPreConsumesSubscriptionAndToken(t *testing.T) {
	truncateTables(t)
	seedResolutionReservationUserAndToken(t, 9602, 9602, 0, 1_000, false)
	plan := &SubscriptionPlan{Id: 9602, Title: "reservation plan", DurationUnit: "month", DurationValue: 1, Enabled: true, TotalAmount: 10_000}
	require.NoError(t, DB.Create(plan).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		Id: 9602, UserId: 9602, PlanId: 9602, AmountTotal: 10_000, AmountUsed: 100,
		Status: "active", StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: time.Now().Add(time.Hour).Unix(),
	}).Error)

	result, err := ReserveResolutionBilling(ResolutionBillingReservationParams{
		RequestId:     "resolution-subscription",
		UserId:        9602,
		TokenId:       9602,
		BillingSource: ResolutionReservationSourceSubscription,
		Quota:         80,
		ModelName:     "video-model",
	})
	require.NoError(t, err)
	assert.Equal(t, 9602, result.SubscriptionId)
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, 9602).Error)
	assert.EqualValues(t, 180, subscription.AmountUsed)
	var token Token
	require.NoError(t, DB.First(&token, 9602).Error)
	assert.Equal(t, 920, token.RemainQuota)
	assert.Equal(t, 80, token.UsedQuota)
}

func TestResolutionReservationRefundFailureIsAuditedAndRetryIsIdempotent(t *testing.T) {
	truncateTables(t)
	seedResolutionReservationUserAndToken(t, 9603, 9603, 100, 100, false)
	params := ResolutionBillingReservationParams{
		RequestId:     "resolution-refund-retry",
		UserId:        9603,
		TokenId:       9603,
		BillingSource: ResolutionReservationSourceWallet,
		Quota:         80,
	}
	_, err := ReserveResolutionBilling(params)
	require.NoError(t, err)

	const callbackName = "test:fail_resolution_reservation_token_refund"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Token" {
			tx.AddError(errors.New("forced resolution reservation token refund failure"))
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, DB.Callback().Update().Remove(callbackName))
		}
	})
	require.Error(t, RefundResolutionBillingReservation(params.RequestId, "task insert failed"))
	var failed ResolutionBillingReservation
	require.NoError(t, DB.Where("request_id = ?", params.RequestId).First(&failed).Error)
	assert.Equal(t, ResolutionReservationStatusReserved, failed.Status)
	assert.Equal(t, 1, failed.RefundAttempts)
	assert.Contains(t, failed.LastError, "forced resolution reservation token refund failure")

	require.NoError(t, DB.Callback().Update().Remove(callbackName))
	callbackRegistered = false
	require.NoError(t, RefundResolutionBillingReservation(params.RequestId, "task insert failed"))
	require.NoError(t, RefundResolutionBillingReservation(params.RequestId, "duplicate retry"))
	var refunded ResolutionBillingReservation
	require.NoError(t, DB.Where("request_id = ?", params.RequestId).First(&refunded).Error)
	assert.Equal(t, ResolutionReservationStatusRefunded, refunded.Status)
	assert.Equal(t, 2, refunded.RefundAttempts)
	var user User
	require.NoError(t, DB.First(&user, 9603).Error)
	assert.Equal(t, 100, user.Quota)
	var token Token
	require.NoError(t, DB.First(&token, 9603).Error)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
}

func TestResolutionReservationAttachPreventsOrphanRefund(t *testing.T) {
	truncateTables(t)
	seedResolutionReservationUserAndToken(t, 9604, 9604, 1_000, 1_000, false)
	require.NoError(t, DB.Create(&Channel{Id: 9604, Name: "reservation-channel", Key: "sk-channel", Status: common.ChannelStatusEnabled}).Error)
	params := ResolutionBillingReservationParams{
		RequestId:     "resolution-attached",
		UserId:        9604,
		TokenId:       9604,
		BillingSource: ResolutionReservationSourceWallet,
		Quota:         80,
	}
	_, err := ReserveResolutionBilling(params)
	require.NoError(t, err)
	task := &Task{
		TaskID: "resolution-attached-task", UserId: 9604, ChannelId: 9604, Quota: 80, Status: TaskStatusInProgress,
		PrivateData: TaskPrivateData{
			TokenId: 9604, BillingSource: "wallet", BillingReservationRequestId: params.RequestId,
			BillingContext: &TaskBillingContext{PricingKind: TaskPricingKindVideoResolution},
		},
	}
	require.NoError(t, task.Insert())
	refunded, err := RefundOrphanedResolutionBillingReservations(time.Now().Add(time.Hour).Unix(), 10)
	require.NoError(t, err)
	assert.Zero(t, refunded)
	var reservation ResolutionBillingReservation
	require.NoError(t, DB.Where("request_id = ?", params.RequestId).First(&reservation).Error)
	assert.Equal(t, ResolutionReservationStatusAttached, reservation.Status)
	assert.Equal(t, task.ID, reservation.TaskId)
	var user User
	require.NoError(t, DB.First(&user, 9604).Error)
	assert.Equal(t, 920, user.Quota)
}

func TestResolutionReservationOrphanSweepRefundsOnlyExpiredUnattachedRows(t *testing.T) {
	truncateTables(t)
	seedResolutionReservationUserAndToken(t, 9605, 9605, 1_000, 1_000, false)
	params := ResolutionBillingReservationParams{
		RequestId:     "resolution-orphan",
		UserId:        9605,
		TokenId:       9605,
		BillingSource: ResolutionReservationSourceWallet,
		Quota:         80,
	}
	_, err := ReserveResolutionBilling(params)
	require.NoError(t, err)
	old := time.Now().Add(-2 * time.Hour).Unix()
	require.NoError(t, DB.Model(&ResolutionBillingReservation{}).Where("request_id = ?", params.RequestId).Updates(map[string]interface{}{
		"created_at": old,
		"updated_at": old,
	}).Error)

	refunded, err := RefundOrphanedResolutionBillingReservations(time.Now().Add(-time.Hour).Unix(), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, refunded)
	var user User
	require.NoError(t, DB.First(&user, 9605).Error)
	assert.Equal(t, 1_000, user.Quota)
}
