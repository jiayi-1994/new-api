package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResolutionPricingAdminLogIncludesPerSecondSelection(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	seedUser(t, 90, 1_000_000)
	seedChannel(t, 90)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)

	info := &relaycommon.RelayInfo{
		UserId:          90,
		OriginModelName: "video-model",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 90},
		PriceData: types.PriceData{
			ModelPrice: 0.18,
			Quota:      900,
			UsePrice:   true,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.25,
			},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{
				Selection: relaycommon.VideoBillingSelection{
					EffectiveResolution:      "1080p",
					EffectiveDurationSeconds: 8,
					IndependentRatios:        map[string]float64{"video_input": 1.2},
				},
				SelectedResolutionPrice: 0.18,
			},
		},
	}

	LogTaskConsumption(c, info)

	log := getLastLog(t)
	require.NotNil(t, log)
	var other map[string]any
	require.NoError(t, common.Unmarshal([]byte(log.Other), &other))
	assert.NotContains(t, other, "task_per_call_billing")
	assert.NotContains(t, other, "model_price")
	assert.Equal(t, map[string]any{"video_input": 1.2}, other["task_ratios"])
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"effective_resolution":       "1080p",
		"selected_price_per_second":  0.18,
		"submitted_duration_seconds": float64(8),
		"effective_duration_seconds": float64(8),
		"independent_ratios":         map[string]any{"video_input": 1.2},
	}, adminInfo["video_resolution_billing"])
}

func TestResolutionPricingUserLogOmitsAdminPricingFields(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	seedUser(t, 91, 1_000_000)
	seedChannel(t, 91)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	info := &relaycommon.RelayInfo{
		UserId:          91,
		OriginModelName: "video-model",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 91},
		PriceData: types.PriceData{
			Quota: 900,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{
				Selection: relaycommon.VideoBillingSelection{
					EffectiveResolution:      "720p",
					EffectiveDurationSeconds: 5,
				},
				SelectedResolutionPrice: 0.1,
			},
		},
	}
	LogTaskConsumption(c, info)

	logs, _, err := model.GetUserLogs(91, 0, 0, 0, "", "", 0, 10, "", "", "")
	require.NoError(t, err)
	require.Len(t, logs, 1)
	var other map[string]any
	require.NoError(t, common.Unmarshal([]byte(logs[0].Other), &other))
	assert.NotContains(t, other, "admin_info")
	assert.NotContains(t, other, "model_price")
	assert.NotContains(t, logs[0].Other, "720p")
	assert.NotContains(t, logs[0].Other, "selected_price_per_second")
}

func TestMain(m *testing.M) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic("failed to open test db: " + err.Error())
	}
	sqlDB, err := db.DB()
	if err != nil {
		panic("failed to get sql.DB: " + err.Error())
	}
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	if err := model.InitLogDB(); err != nil {
		panic("failed to initialize log database metadata: " + err.Error())
	}
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true

	if err := db.AutoMigrate(
		&model.Task{},
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.Channel{},
		&model.TopUp{},
		&model.UserSubscription{},
		&model.SubscriptionPlan{},
		&model.SubscriptionPreConsumeRecord{},
		&model.ResolutionBillingReservation{},
		&model.SystemTask{},
		&model.SystemTaskLock{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}

	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

func truncate(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM tasks")
		model.DB.Exec("DELETE FROM users")
		model.DB.Exec("DELETE FROM tokens")
		model.DB.Exec("DELETE FROM logs")
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM top_ups")
		model.DB.Exec("DELETE FROM user_subscriptions")
		model.DB.Exec("DELETE FROM subscription_plans")
		model.DB.Exec("DELETE FROM subscription_pre_consume_records")
		model.DB.Exec("DELETE FROM resolution_billing_reservations")
		model.DB.Exec("DELETE FROM system_task_locks")
		model.DB.Exec("DELETE FROM system_tasks")
	})
}

func seedUser(t *testing.T, id int, quota int) {
	t.Helper()
	user := &model.User{Id: id, Username: "test_user", Quota: quota, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
}

func seedToken(t *testing.T, id int, userId int, key string, remainQuota int) {
	t.Helper()
	token := &model.Token{
		Id:          id,
		UserId:      userId,
		Key:         key,
		Name:        "test_token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: remainQuota,
		UsedQuota:   0,
	}
	require.NoError(t, model.DB.Create(token).Error)
}

func seedSubscription(t *testing.T, id int, userId int, amountTotal int64, amountUsed int64) {
	t.Helper()
	sub := &model.UserSubscription{
		Id:          id,
		UserId:      userId,
		AmountTotal: amountTotal,
		AmountUsed:  amountUsed,
		Status:      "active",
		StartTime:   time.Now().Unix(),
		EndTime:     time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(sub).Error)
}

func seedChannel(t *testing.T, id int) {
	t.Helper()
	ch := &model.Channel{Id: id, Name: "test_channel", Key: "sk-test", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(ch).Error)
}

func makeTask(userId, channelId, quota, tokenId int, billingSource string, subscriptionId int) *model.Task {
	return &model.Task{
		TaskID:    "task_" + time.Now().Format("150405.000"),
		UserId:    userId,
		ChannelId: channelId,
		Quota:     quota,
		Status:    model.TaskStatus(model.TaskStatusInProgress),
		Group:     "default",
		Data:      json.RawMessage(`{}`),
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		Properties: model.Properties{
			OriginModelName: "test-model",
		},
		PrivateData: model.TaskPrivateData{
			BillingSource:  billingSource,
			SubscriptionId: subscriptionId,
			TokenId:        tokenId,
			BillingContext: &model.TaskBillingContext{
				ModelPrice:      0.02,
				GroupRatio:      1.0,
				OriginModelName: "test-model",
			},
		},
	}
}

func TestPriceDataOtherRatiosFilterAndSnapshot(t *testing.T) {
	priceData := types.PriceData{}

	priceData.AddOtherRatio("zero", 0)
	priceData.AddOtherRatio("negative", -0.5)
	priceData.AddOtherRatio("nan", math.NaN())
	priceData.AddOtherRatio("inf", math.Inf(1))
	priceData.AddOtherRatio("one", 1)
	priceData.AddOtherRatio("positive", 2.5)

	ratios := priceData.OtherRatios()
	require.Len(t, ratios, 2)
	assert.Equal(t, 1.0, ratios["one"])
	assert.Equal(t, 2.5, ratios["positive"])
	assert.True(t, priceData.HasOtherRatio("one"))
	assert.False(t, priceData.HasOtherRatio("zero"))

	ratios["positive"] = 99
	ratios["new"] = 3
	nextSnapshot := priceData.OtherRatios()
	assert.Equal(t, 2.5, nextSnapshot["positive"])
	assert.NotContains(t, nextSnapshot, "new")
}

func TestPriceDataReplaceAndApplyOtherRatios(t *testing.T) {
	priceData := types.PriceData{}

	replaced := priceData.ReplaceOtherRatios(map[string]float64{
		"zero":     0,
		"negative": -3,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
		"one":      1,
		"duration": 2,
		"size":     1.5,
	})

	require.True(t, replaced)
	assert.Equal(t, 3.0, priceData.OtherRatioMultiplier())
	assert.Equal(t, 30.0, priceData.ApplyOtherRatiosToFloat(10))
	assert.Equal(t, 10.0, priceData.RemoveOtherRatiosFromFloat(30))
	assert.True(t, decimal.NewFromInt(30).Equal(priceData.ApplyOtherRatiosToDecimal(decimal.NewFromInt(10))))

	replaced = priceData.ReplaceOtherRatios(map[string]float64{
		"zero": 0,
		"nan":  math.NaN(),
	})

	require.False(t, replaced)
	assert.Nil(t, priceData.OtherRatios())
	assert.Equal(t, 1.0, priceData.OtherRatioMultiplier())
}

func TestTaskBillingOtherFiltersHistoricalOtherRatios(t *testing.T) {
	task := makeTask(1, 1, 100, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.OtherRatios = map[string]float64{
		"seconds":  2,
		"identity": 1,
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	}

	other := taskBillingOther(task)

	assert.Equal(t, 2.0, other["seconds"])
	assert.Equal(t, 1.0, other["identity"])
	assert.NotContains(t, other, "zero")
	assert.NotContains(t, other, "negative")
	assert.NotContains(t, other, "nan")
	assert.NotContains(t, other, "inf")
}

func TestTaskBillingContextPriceDataFiltersMultiplier(t *testing.T) {
	priceData := taskBillingContextPriceData(&model.TaskBillingContext{
		OtherRatios: map[string]float64{
			"seconds":  2,
			"size":     3,
			"identity": 1,
			"zero":     0,
			"negative": -1,
			"nan":      math.NaN(),
			"inf":      math.Inf(1),
		},
	})

	require.NotNil(t, priceData)
	assert.Equal(t, 6.0, priceData.OtherRatioMultiplier())
	assert.Equal(t, map[string]float64{
		"seconds":  2,
		"size":     3,
		"identity": 1,
	}, priceData.OtherRatios())
}

// ---------------------------------------------------------------------------
// Read-back helpers
// ---------------------------------------------------------------------------

func getUserQuota(t *testing.T, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("quota").Where("id = ?", id).First(&user).Error)
	return user.Quota
}

func getTokenRemainQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("remain_quota").Where("id = ?", id).First(&token).Error)
	return token.RemainQuota
}

func getTokenUsedQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&token).Error)
	return token.UsedQuota
}

func getSubscriptionUsed(t *testing.T, id int) int64 {
	t.Helper()
	var sub model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").Where("id = ?", id).First(&sub).Error)
	return sub.AmountUsed
}

func getTaskQuota(t *testing.T, id int64) int {
	t.Helper()
	var task model.Task
	require.NoError(t, model.DB.Select("quota").Where("id = ?", id).First(&task).Error)
	return task.Quota
}

func getLastLog(t *testing.T) *model.Log {
	t.Helper()
	var log model.Log
	err := model.LOG_DB.Order("id desc").First(&log).Error
	if err != nil {
		return nil
	}
	return &log
}

func countLogs(t *testing.T) int64 {
	t.Helper()
	var count int64
	model.LOG_DB.Model(&model.Log{}).Count(&count)
	return count
}

// ===========================================================================
// RefundTaskQuota tests
// ===========================================================================

func TestRefundTaskQuota_Wallet(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 1, 1, 1
	const initQuota, preConsumed = 10000, 3000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-test-key", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, RefundTaskQuota(ctx, task, "task failed: upstream error"))

	// User quota should increase by preConsumed
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Token remain_quota should increase, used_quota should decrease
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, -preConsumed, getTokenUsedQuota(t, tokenID))

	// A refund log should be created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed, log.Quota)
	assert.Equal(t, "test-model", log.ModelName)
	assert.Zero(t, task.Quota)
	assert.Zero(t, getTaskQuota(t, task.ID))
}

func TestRefundTaskQuota_Subscription(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 2, 2, 2, 1
	const preConsumed = 2000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-key", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, RefundTaskQuota(ctx, task, "subscription task failed"))

	// Subscription used should decrease by preConsumed
	assert.Equal(t, subUsed-int64(preConsumed), getSubscriptionUsed(t, subID))

	// Token should also be refunded
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Zero(t, getTaskQuota(t, task.ID))
}

func TestRefundTaskQuota_ZeroQuota(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 3
	seedUser(t, userID, 5000)

	task := makeTask(userID, 0, 0, 0, BillingSourceWallet, 0)

	assert.True(t, RefundTaskQuota(ctx, task, "zero quota task"))

	// No change to user quota
	assert.Equal(t, 5000, getUserQuota(t, userID))

	// No log created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRefundTaskQuota_NoToken(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 4, 4
	const initQuota, preConsumed = 10000, 1500

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0) // TokenId=0
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, RefundTaskQuota(ctx, task, "no token task failed"))

	// User quota refunded
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Log created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Zero(t, getTaskQuota(t, task.ID))
}

func TestRefundTaskQuota_FundingFailureKeepsPendingMarker(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, preConsumed = 5, 1200
	seedUser(t, userID, 5000)
	task := makeTask(userID, 0, preConsumed, 0, BillingSourceSubscription, 9999)
	task.Status = model.TaskStatusFailure
	require.NoError(t, model.DB.Create(task).Error)

	assert.False(t, RefundTaskQuota(ctx, task, "subscription missing"))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, preConsumed, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(0), countLogs(t))
}

// ===========================================================================
// RecalculateTaskQuota tests
// ===========================================================================

func TestRecalculate_PositiveDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 10, 10, 10
	const initQuota, preConsumed = 10000, 2000
	const actualQuota = 3000 // under-charged by 1000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-pos", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")

	// User quota should decrease by the delta (1000 additional charge)
	assert.Equal(t, initQuota-(actualQuota-preConsumed), getUserQuota(t, userID))

	// Token should also be charged the delta
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)

	// Log type should be Consume (additional charge)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, actualQuota-preConsumed, log.Quota)
}

func TestRecalculate_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 11, 11, 11
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged by 2000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-neg", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")

	// User quota should increase by abs(delta) = 2000 (refund overpayment)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))

	// Token should be refunded the difference
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	// task.Quota updated
	assert.Equal(t, actualQuota, task.Quota)

	// Log type should be Refund
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed-actualQuota, log.Quota)
}

func TestRecalculate_ZeroDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 12
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, preConsumed, "exact match")

	// No change to user quota
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No log created (delta is zero)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_ActualQuotaZero(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 13
	const initQuota = 10000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, 5000, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, 0, "zero actual")

	// No change (early return)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_Subscription_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 14, 14, 14, 2
	const preConsumed = 5000
	const actualQuota = 2000 // over-charged by 3000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-recalc", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)

	RecalculateTaskQuota(ctx, task, actualQuota, "subscription over-charge")

	// Subscription used should decrease by delta (refund 3000)
	assert.Equal(t, subUsed-int64(preConsumed-actualQuota), getSubscriptionUsed(t, subID))

	// Token refunded
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	assert.Equal(t, actualQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

// ===========================================================================
// CAS + Billing integration tests
// Simulates the flow in updateVideoSingleTask (service/task_polling.go)
// ===========================================================================

// simulatePollBilling reproduces the CAS + billing logic from updateVideoSingleTask.
// It takes a persisted task (already in DB), applies the new status, and performs
// the conditional update + billing exactly as the polling loop does.
func simulatePollBilling(ctx context.Context, task *model.Task, newStatus model.TaskStatus, actualQuota int) {
	snap := task.Snapshot()

	shouldRefund := false
	shouldSettle := false
	quota := task.Quota

	task.Status = newStatus
	switch string(newStatus) {
	case model.TaskStatusSuccess:
		task.Progress = "100%"
		task.FinishTime = 9999
		shouldSettle = true
	case model.TaskStatusFailure:
		task.Progress = "100%"
		task.FinishTime = 9999
		task.FailReason = "upstream error"
		if quota != 0 {
			shouldRefund = true
		}
	default:
		task.Progress = "50%"
	}

	isDone := task.Status == model.TaskStatus(model.TaskStatusSuccess) || task.Status == model.TaskStatus(model.TaskStatusFailure)
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil {
			shouldRefund = false
			shouldSettle = false
		} else if !won {
			shouldRefund = false
			shouldSettle = false
		}
	} else if !snap.Equal(task.Snapshot()) {
		_, _ = task.UpdateWithStatus(snap.Status)
	}

	if shouldSettle && actualQuota > 0 {
		RecalculateTaskQuota(ctx, task, actualQuota, "test settle")
	}
	if shouldRefund {
		RefundTaskQuota(ctx, task, task.FailReason)
	}
}

func TestCASGuardedRefund_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 20, 20, 20
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-win", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS wins: task in DB should now be FAILURE
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Zero(t, reloaded.Quota)

	// Refund should have happened
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestCASGuardedRefund_Lose(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 21, 21, 21
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-lose", tokenRemain)
	seedChannel(t, channelID)

	// Create task with IN_PROGRESS in DB
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate another process already transitioning to FAILURE
	model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailure)

	// Our process still has the old in-memory state (IN_PROGRESS) and tries to transition
	// task.Status is still IN_PROGRESS in the snapshot
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS lost: user quota should NOT change (no double refund)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))

	// No billing log should be created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestCASGuardedSettle_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 22, 22, 22
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged, should get partial refund
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-settle-win", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusSuccess), actualQuota)

	// CAS wins: task should be SUCCESS
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusSuccess, reloaded.Status)

	// Settlement should refund the over-charge (5000 - 3000 = 2000 back to user)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)
}

func TestNonTerminalUpdate_NoBilling(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 23, 23
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	task.Progress = "20%"
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate a non-terminal poll update (still IN_PROGRESS, progress changed)
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusInProgress), 0)

	// User quota should NOT change
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No billing log
	assert.Equal(t, int64(0), countLogs(t))

	// Task progress should be updated in DB
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.Equal(t, "50%", reloaded.Progress)
}

// ===========================================================================
// Mock adaptor for settleTaskBillingOnComplete tests
// ===========================================================================

type mockAdaptor struct {
	adjustReturn int
	adjustCalls  int
}

func (m *mockAdaptor) Init(_ *relaycommon.RelayInfo) {}
func (m *mockAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return nil, nil
}
func (m *mockAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) { return nil, nil }
func (m *mockAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	m.adjustCalls++
	return m.adjustReturn
}

func resolutionBillingContext(duration int) *model.TaskBillingContext {
	return &model.TaskBillingContext{
		ModelPrice:               99,
		GroupRatio:               1.25,
		ModelRatio:               37.5,
		OtherRatios:              map[string]float64{"stale_duration": 100},
		OriginModelName:          "video-model",
		PerCallBilling:           true,
		PricingKind:              "video_resolution",
		EffectiveResolution:      "1080p",
		SelectedResolutionPrice:  0.1,
		EffectiveDurationSeconds: duration,
		QuotaPerUnit:             common.QuotaPerUnit,
		IndependentRatios:        map[string]float64{"video_input": 1.2},
	}
}

func TestCalculateVideoResolutionSnapshotQuotaUsesOnlyFrozenSelection(t *testing.T) {
	context := resolutionBillingContext(8)

	quota, clamp, err := CalculateVideoResolutionSnapshotQuota(context, 6)
	require.NoError(t, err)
	assert.Nil(t, clamp)
	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 6, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, quota)
}

func TestResolutionSettlementAlwaysUsesDuration(t *testing.T) {
	truncate(t)
	const userID, channelID = 33, 33
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)

	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	adaptor := &mockAdaptor{adjustReturn: 1}
	settleTaskBillingOnComplete(context.Background(), adaptor, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 6,
	})

	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 6, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, task.Quota)
	assert.Zero(t, adaptor.adjustCalls)
}

func TestResolutionSettlementIgnoresLegacyTaskBillingModePerCall(t *testing.T) {
	truncate(t)
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"video-model":"per_call"}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString("{}")) })

	const userID, channelID = 34, 34
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 4,
	})

	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 4, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, task.Quota)
}

func TestResolutionSettlementRunsBeforeAdaptorAndTokenFallback(t *testing.T) {
	truncate(t)
	const userID, channelID = 35, 35
	seedUser(t, userID, 2_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	adaptor := &mockAdaptor{adjustReturn: 123456}

	settleTaskBillingOnComplete(context.Background(), adaptor, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 5,
	})

	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 5, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, task.Quota)
	assert.Zero(t, adaptor.adjustCalls)
}

func TestResolutionSettlementIgnoresTotalTokensAndResidualModelRatio(t *testing.T) {
	truncate(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"video-model":100}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString("{}")) })

	const userID, channelID = 38, 38
	seedUser(t, userID, 2_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	adaptor := &mockAdaptor{}

	settleTaskBillingOnComplete(context.Background(), adaptor, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		TotalTokens:              999999,
		EffectiveDurationSeconds: 5,
	})

	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 5, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, task.Quota)
	assert.Zero(t, adaptor.adjustCalls)
}

func TestResolutionSettlementUsesSnapshotAfterLiveConfigurationChanges(t *testing.T) {
	truncate(t)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"video-model":{"1080p":0.9}}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString("{}")) })

	const userID, channelID = 36, 36
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 4,
	})

	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 4, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, task.Quota)
}

func TestResolutionSettlementUsesFrozenQuotaPerUnit(t *testing.T) {
	truncate(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	const userID, channelID = 39, 39
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	common.QuotaPerUnit = 1_000
	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 4,
	})

	assert.Equal(t, 300, task.Quota)
}

func TestResolutionPreConsumePersistsDurablyWhenBatchingEnabled(t *testing.T) {
	truncate(t)
	originalBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = originalBatchUpdateEnabled })

	const userID, tokenID = 390, 390
	const userQuota, tokenQuota, preConsumed = 10_000, 8_000, 600
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, "sk-resolution-durable-preconsume", tokenQuota)

	g := gin.New()
	recorder := httptest.NewRecorder()
	c := gin.CreateTestContextOnly(recorder, g)
	info := &relaycommon.RelayInfo{
		RequestId:       "resolution-durable-preconsume",
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "sk-resolution-durable-preconsume",
		OriginModelName: "video-model",
		ForcePreConsume: true,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{},
		},
	}
	info.UserSetting.BillingPreference = "wallet_only"

	require.Nil(t, PreConsumeBilling(c, preConsumed, info))
	assert.Equal(t, userQuota-preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota-preConsumed, getTokenRemainQuota(t, tokenID))
	var reservation model.ResolutionBillingReservation
	require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&reservation).Error)
	assert.Equal(t, model.ResolutionReservationStatusReserved, reservation.Status)
	assert.Equal(t, preConsumed, reservation.Quota)
}

func TestResolutionSubmissionPersistsBaseStatsBeforeBatchFlush(t *testing.T) {
	truncate(t)
	originalBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = originalBatchUpdateEnabled })

	const userID, channelID, tokenID = 405, 405, 405
	seedUser(t, userID, 1_000_000)
	seedToken(t, tokenID, userID, "sk-resolution-base-stats", 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		RequestId:       "resolution-base-stats",
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "sk-resolution-base-stats",
		OriginModelName: "video-model",
		UsingGroup:      "default",
		ForcePreConsume: true,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
		PriceData: types.PriceData{
			Quota: preConsumed,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1.25,
			},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{
				Selection: relaycommon.VideoBillingSelection{
					EffectiveResolution:      "1080p",
					EffectiveDurationSeconds: 8,
					IndependentRatios:        map[string]float64{"video_input": 1.2},
				},
				SelectedResolutionPrice: 0.1,
				QuotaPerUnit:            common.QuotaPerUnit,
			},
		},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	require.Nil(t, PreConsumeBilling(c, preConsumed, info))
	LogTaskConsumption(c, info)
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	task.PrivateData.BillingReservationRequestId = info.RequestId
	require.NoError(t, task.Insert())

	var submittedUser model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&submittedUser, userID).Error)
	assert.Equal(t, preConsumed, submittedUser.UsedQuota)
	assert.Equal(t, 1, submittedUser.RequestCount)
	var submittedChannel model.Channel
	require.NoError(t, model.DB.Select("used_quota").First(&submittedChannel, channelID).Error)
	assert.EqualValues(t, preConsumed, submittedChannel.UsedQuota)

	assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 4,
	}))
	actualQuota, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 4, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&submittedUser, userID).Error)
	assert.Equal(t, actualQuota, submittedUser.UsedQuota)
	assert.Equal(t, 1, submittedUser.RequestCount)
	require.NoError(t, model.DB.Select("used_quota").First(&submittedChannel, channelID).Error)
	assert.EqualValues(t, actualQuota, submittedChannel.UsedQuota)
}

// resolutionSubmitRelayInfo 构造一次分辨率任务提交的 RelayInfo：0.1/秒 × 8 秒 ×
// 1.25 分组倍率 × 1.2 video_input 倍率。
func resolutionSubmitRelayInfo(requestId string, userID, tokenID, channelID, quota int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RequestId:       requestId,
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "sk-" + requestId,
		OriginModelName: "video-model",
		UsingGroup:      "default",
		ForcePreConsume: true,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
		PriceData: types.PriceData{
			Quota:          quota,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.25},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{
				Selection: relaycommon.VideoBillingSelection{
					EffectiveResolution:      "1080p",
					EffectiveDurationSeconds: 8,
					IndependentRatios:        map[string]float64{"video_input": 1.2},
				},
				SelectedResolutionPrice: 0.1,
				QuotaPerUnit:            common.QuotaPerUnit,
			},
		},
	}
}

func TestResolutionTaskInsertFailureSynchronouslyRefundsReservation(t *testing.T) {
	truncate(t)
	const userID, tokenID, channelID = 410, 410, 410
	const userQuota, tokenQuota = 2_000_000, 1_500_000
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, "sk-resolution-insert-failure", tokenQuota)
	seedChannel(t, channelID)

	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	info := resolutionSubmitRelayInfo("resolution-insert-failure", userID, tokenID, channelID, preConsumed)
	info.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)

	require.Nil(t, PreConsumeBilling(c, preConsumed, info))
	require.NoError(t, SettleBilling(c, info, preConsumed))
	LogTaskConsumption(c, info)
	require.Equal(t, userQuota-preConsumed, getUserQuota(t, userID))

	const callbackName = "test:fail_resolution_task_insert"
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" {
			tx.AddError(errors.New("forced task insert failure"))
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove(callbackName) })

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	task.PrivateData.BillingReservationRequestId = info.RequestId

	require.Error(t, PersistSubmittedTask(c, task))

	var persistedTasks int64
	require.NoError(t, model.DB.Model(&model.Task{}).Count(&persistedTasks).Error)
	assert.Zero(t, persistedTasks)
	assert.Equal(t, userQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))

	var user model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&user, userID).Error)
	assert.Zero(t, user.UsedQuota)
	assert.Zero(t, user.RequestCount)
	var channel model.Channel
	require.NoError(t, model.DB.Select("used_quota").First(&channel, channelID).Error)
	assert.Zero(t, channel.UsedQuota)

	var reservation model.ResolutionBillingReservation
	require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&reservation).Error)
	assert.Equal(t, model.ResolutionReservationStatusRefunded, reservation.Status)
	assert.Zero(t, reservation.TaskId)

	var refundLogs []model.Log
	require.NoError(t, model.DB.Where("type = ?", model.LogTypeRefund).Find(&refundLogs).Error)
	require.Len(t, refundLogs, 1)
	assert.Equal(t, preConsumed, refundLogs[0].Quota)
}

func TestResolutionTaskInsertRefundLogReportsTheReservationAmount(t *testing.T) {
	truncate(t)
	const userID, tokenID, channelID = 413, 413, 413
	const userQuota, tokenQuota = 2_000_000, 2_000_000
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, "sk-resolution-quota-mismatch", tokenQuota)
	seedChannel(t, channelID)

	reserved, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	info := resolutionSubmitRelayInfo("resolution-quota-mismatch", userID, tokenID, channelID, reserved)
	info.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	require.Nil(t, PreConsumeBilling(c, reserved, info))

	// 结算失败后任务额度与预留不一致：落库会被一致性校验拒绝，此时退款日志必须记
	// 预留实际退还的金额，而不是任务上那个从未入账的金额。
	task := makeTask(userID, channelID, reserved*5, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	task.PrivateData.BillingReservationRequestId = info.RequestId

	require.Error(t, PersistSubmittedTask(c, task))

	assert.Equal(t, userQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
	var refundLogs []model.Log
	require.NoError(t, model.DB.Where("type = ?", model.LogTypeRefund).Find(&refundLogs).Error)
	require.Len(t, refundLogs, 1)
	assert.Equal(t, reserved, refundLogs[0].Quota)
}

func TestSweepOrphanedResolutionReservationsRecordsARefundLog(t *testing.T) {
	truncate(t)
	const userID, tokenID = 414, 414
	const userQuota, tokenQuota, quota = 10_000, 8_000, 600
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, "sk-resolution-orphan-log", tokenQuota)

	info := resolutionSubmitRelayInfo("resolution-orphan-log", userID, tokenID, 0, quota)
	info.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	require.Nil(t, PreConsumeBilling(c, quota, info))
	expired := time.Now().Add(-resolutionReservationOrphanGrace() - time.Minute).Unix()
	require.NoError(t, model.DB.Model(&model.ResolutionBillingReservation{}).
		Where("request_id = ?", info.RequestId).
		Updates(map[string]any{"created_at": expired, "updated_at": expired}).Error)

	sweepOrphanedResolutionReservations(context.Background())

	// 提交时已写过一条消费日志，兜底退款必须补一条退款日志，否则用量报表永久多算
	assert.Equal(t, userQuota, getUserQuota(t, userID))
	var refundLogs []model.Log
	require.NoError(t, model.DB.Where("type = ?", model.LogTypeRefund).Find(&refundLogs).Error)
	require.Len(t, refundLogs, 1)
	assert.Equal(t, quota, refundLogs[0].Quota)
}

func TestResolutionReservationFallsBackToWalletWhenSubscriptionIsExhausted(t *testing.T) {
	truncate(t)
	const userID, channelID, planID, subscriptionID = 411, 411, 411, 411
	const userQuota, tokenQuota = 2_000_000, 2_000_000
	seedUser(t, userID, userQuota)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Create(&model.SubscriptionPlan{
		Id: planID, Title: "resolution fallback plan", DurationUnit: "month", DurationValue: 1, Enabled: true, TotalAmount: 10_000,
	}).Error)

	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	// 订阅余额只够一次提交，另一次必须回退到钱包
	require.NoError(t, model.DB.Create(&model.UserSubscription{
		Id: subscriptionID, UserId: userID, PlanId: planID,
		AmountTotal: int64(preConsumed), AmountUsed: 0, AllowWalletOverflow: true,
		Status: "active", StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: time.Now().Add(time.Hour).Unix(),
	}).Error)

	requestIds := []string{"resolution-fallback-a", "resolution-fallback-b"}
	for i, requestId := range requestIds {
		seedToken(t, 4110+i, userID, "sk-"+requestId, tokenQuota)
	}

	var submissions sync.WaitGroup
	errs := make(chan *relaytypes.NewAPIError, len(requestIds))
	sources := make(chan string, len(requestIds))
	for i, requestId := range requestIds {
		submissions.Add(1)
		go func() {
			defer submissions.Done()
			info := resolutionSubmitRelayInfo(requestId, userID, 4110+i, channelID, preConsumed)
			info.UserSetting.BillingPreference = "subscription_first"
			c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
			if apiErr := PreConsumeBilling(c, preConsumed, info); apiErr != nil {
				errs <- apiErr
				return
			}
			sources <- info.BillingSource
		}()
	}
	submissions.Wait()
	close(errs)
	close(sources)

	require.Empty(t, errs)
	usedSources := make([]string, 0, len(requestIds))
	for source := range sources {
		usedSources = append(usedSources, source)
	}
	assert.ElementsMatch(t, []string{BillingSourceSubscription, BillingSourceWallet}, usedSources)

	assert.EqualValues(t, preConsumed, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, userQuota-preConsumed, getUserQuota(t, userID))
	var reservations []model.ResolutionBillingReservation
	require.NoError(t, model.DB.Order("request_id").Find(&reservations).Error)
	require.Len(t, reservations, 2)
	for _, reservation := range reservations {
		assert.Equal(t, model.ResolutionReservationStatusReserved, reservation.Status)
		assert.Equal(t, preConsumed, reservation.Quota)
	}
	assert.ElementsMatch(t,
		[]string{model.ResolutionReservationSourceSubscription, model.ResolutionReservationSourceWallet},
		[]string{reservations[0].BillingSource, reservations[1].BillingSource},
	)
}

func TestSweepOrphanedResolutionReservationsOnlyRefundsAfterGracePeriod(t *testing.T) {
	truncate(t)
	const userID, tokenID = 412, 412
	const userQuota, tokenQuota, quota = 10_000, 8_000, 600
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, "sk-resolution-orphan-sweep", tokenQuota)

	fresh := resolutionSubmitRelayInfo("resolution-orphan-fresh", userID, tokenID, 0, quota)
	fresh.UserSetting.BillingPreference = "wallet_only"
	stale := resolutionSubmitRelayInfo("resolution-orphan-stale", userID, tokenID, 0, quota)
	stale.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	require.Nil(t, PreConsumeBilling(c, quota, fresh))
	require.Nil(t, PreConsumeBilling(c, quota, stale))

	expired := time.Now().Add(-resolutionReservationOrphanGrace() - time.Minute).Unix()
	require.NoError(t, model.DB.Model(&model.ResolutionBillingReservation{}).
		Where("request_id = ?", stale.RequestId).
		Updates(map[string]any{"created_at": expired, "updated_at": expired}).Error)

	sweepOrphanedResolutionReservations(context.Background())

	var staleReservation model.ResolutionBillingReservation
	require.NoError(t, model.DB.Where("request_id = ?", stale.RequestId).First(&staleReservation).Error)
	assert.Equal(t, model.ResolutionReservationStatusRefunded, staleReservation.Status)
	var freshReservation model.ResolutionBillingReservation
	require.NoError(t, model.DB.Where("request_id = ?", fresh.RequestId).First(&freshReservation).Error)
	assert.Equal(t, model.ResolutionReservationStatusReserved, freshReservation.Status)

	assert.Equal(t, userQuota-quota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota-quota, getTokenRemainQuota(t, tokenID))
}

func TestResolutionTaskInsertRollsBackWhenBaseStatsCannotReachChannel(t *testing.T) {
	truncate(t)
	const userID, missingChannelID = 409, 409
	seedUser(t, userID, 1_000_000)
	task := makeTask(userID, missingChannelID, 600, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.Error(t, task.Insert())

	var taskCount int64
	require.NoError(t, model.DB.Model(&model.Task{}).Where("task_id = ?", task.TaskID).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
	var user model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&user, userID).Error)
	assert.Zero(t, user.UsedQuota)
	assert.Zero(t, user.RequestCount)
}

func TestResolutionRoundedZeroSubscriptionDoesNotReserveSyntheticTokenQuota(t *testing.T) {
	truncate(t)
	server, _, _, _ := useIndependentAuthSessionRedis(t)
	const userID, tokenID, planID, subscriptionID = 406, 406, 406, 406
	const tokenQuota = 1_000
	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-resolution-zero-subscription", tokenQuota)
	plan := &model.SubscriptionPlan{
		Id:            planID,
		Title:         "resolution zero plan",
		DurationUnit:  "month",
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   10_000,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	subscription := &model.UserSubscription{
		Id:          subscriptionID,
		UserId:      userID,
		PlanId:      planID,
		AmountTotal: 10_000,
		AmountUsed:  100,
		Status:      "active",
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(subscription).Error)

	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "sk-resolution-zero-subscription",
		RequestId:       "request-resolution-zero-subscription",
		OriginModelName: "video-model",
		ForcePreConsume: true,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{},
		},
	}
	info.UserSetting.BillingPreference = "subscription_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	session, apiErr := NewBillingSession(c, info, 0)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	server.Close()
	require.NoError(t, session.Settle(0))

	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(100), getSubscriptionUsed(t, subscriptionID))
	var record model.SubscriptionPreConsumeRecord
	require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&record).Error)
	assert.Zero(t, record.PreConsumed)
}

func TestResolutionWalletOverdraftReturnsInsufficientQuotaAndLeavesNoReservation(t *testing.T) {
	truncate(t)
	const userID = 407
	seedUser(t, userID, 20)
	info := &relaycommon.RelayInfo{
		RequestId:       "resolution-wallet-overdraft",
		UserId:          userID,
		OriginModelName: "video-model",
		ForcePreConsume: true,
		IsPlayground:    true,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{},
		},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())

	apiErr := PreConsumeBilling(c, 80, info)

	require.NotNil(t, apiErr)
	assert.Equal(t, relaytypes.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	assert.Equal(t, 20, getUserQuota(t, userID))
	var reservations int64
	require.NoError(t, model.DB.Model(&model.ResolutionBillingReservation{}).Count(&reservations).Error)
	assert.Zero(t, reservations)
}

func TestLegacyRoundedZeroSubscriptionRetainsSyntheticReservation(t *testing.T) {
	truncate(t)
	const userID, tokenID, planID, subscriptionID = 408, 408, 408, 408
	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-legacy-zero-subscription", 1_000)
	plan := &model.SubscriptionPlan{
		Id:            planID,
		Title:         "legacy zero plan",
		DurationUnit:  "month",
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   10_000,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	require.NoError(t, model.DB.Create(&model.UserSubscription{
		Id:          subscriptionID,
		UserId:      userID,
		PlanId:      planID,
		AmountTotal: 10_000,
		AmountUsed:  100,
		Status:      "active",
		StartTime:   time.Now().Add(-time.Hour).Unix(),
		EndTime:     time.Now().Add(time.Hour).Unix(),
	}).Error)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "sk-legacy-zero-subscription",
		RequestId:       "request-legacy-zero-subscription",
		OriginModelName: "legacy-task-model",
		ForcePreConsume: true,
	}
	info.UserSetting.BillingPreference = "subscription_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	session, apiErr := NewBillingSession(c, info, 0)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.NoError(t, session.Settle(1))
	assert.Equal(t, 999, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(101), getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, 1, info.FinalPreConsumedQuota)
	var record model.SubscriptionPreConsumeRecord
	require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&record).Error)
	assert.EqualValues(t, 1, record.PreConsumed)
}

func TestLegacyForcePreConsumeRetainsBatchAccounting(t *testing.T) {
	truncate(t)
	originalBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = originalBatchUpdateEnabled })

	const userID, tokenID = 395, 395
	const userQuota, tokenQuota, preConsumed = 10_000, 8_000, 600
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, "sk-legacy-batched-preconsume", tokenQuota)
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "sk-legacy-batched-preconsume",
		OriginModelName: "legacy-task-model",
		ForcePreConsume: true,
	}
	info.UserSetting.BillingPreference = "wallet_only"

	require.Nil(t, PreConsumeBilling(c, preConsumed, info))
	assert.Equal(t, userQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
}

func TestResolutionSettlementChargesPositiveDeltaToUnlimitedToken(t *testing.T) {
	truncate(t)
	const userID, tokenID, channelID = 391, 391, 391
	const userQuota = 10_000
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, "sk-resolution-unlimited", 0)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", tokenID).Update("unlimited_quota", true).Error)
	seedChannel(t, channelID)

	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuotaAtUnit(0.1, 4, 1.25, map[string]float64{"video_input": 1.2}, 500)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"used_quota":    preConsumed,
		"request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(4)
	task.PrivateData.BillingContext.QuotaPerUnit = 500
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 8,
	}))

	actualQuota, _, err := relaycommon.CalculateVideoResolutionQuotaAtUnit(0.1, 8, 1.25, map[string]float64{"video_input": 1.2}, 500)
	require.NoError(t, err)
	delta := actualQuota - preConsumed
	assert.Equal(t, actualQuota, getTaskQuota(t, task.ID))
	assert.Equal(t, userQuota-delta, getUserQuota(t, userID))
	assert.Equal(t, -delta, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, delta, getTokenUsedQuota(t, tokenID))
	var chargedUser model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&chargedUser, userID).Error)
	assert.Equal(t, actualQuota, chargedUser.UsedQuota)
	assert.Equal(t, 1, chargedUser.RequestCount)
	var chargedChannel model.Channel
	require.NoError(t, model.DB.Select("used_quota").First(&chargedChannel, channelID).Error)
	assert.EqualValues(t, actualQuota, chargedChannel.UsedQuota)
}

func TestResolutionSettlementStatsTrackFinalQuotaWithoutDoubleCountingRequest(t *testing.T) {
	for index, actualDuration := range []int{16, 4} {
		name := "positive delta"
		if actualDuration < 8 {
			name = "negative delta"
		}
		t.Run(name, func(t *testing.T) {
			truncate(t)
			userID := 403 + index
			channelID := 403 + index
			seedUser(t, userID, 1_000_000)
			seedChannel(t, channelID)
			preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
				"used_quota":    preConsumed,
				"request_count": 1,
			}).Error)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)
			task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
			task.PrivateData.BillingContext = resolutionBillingContext(8)
			require.NoError(t, model.DB.Create(task).Error)

			assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
				Status:                   model.TaskStatusSuccess,
				EffectiveDurationSeconds: actualDuration,
			}))
			actualQuota, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, actualDuration, 1.25, map[string]float64{"video_input": 1.2})
			require.NoError(t, err)
			var user model.User
			require.NoError(t, model.DB.Select("used_quota", "request_count").First(&user, userID).Error)
			assert.Equal(t, actualQuota, user.UsedQuota)
			assert.Equal(t, 1, user.RequestCount)
			var channel model.Channel
			require.NoError(t, model.DB.Select("used_quota").First(&channel, channelID).Error)
			assert.EqualValues(t, actualQuota, channel.UsedQuota)
		})
	}
}

func TestResolutionSettlementCompletesWhenTheChannelWasDeleted(t *testing.T) {
	truncate(t)
	const userID, channelID = 410, 410
	const initialWallet = 1_000_000
	seedUser(t, userID, initialWallet)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"used_quota":    preConsumed,
		"request_count": 1,
	}).Error)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	require.NoError(t, model.DB.Delete(&model.Channel{}, channelID).Error)

	// 渠道统计行缺失只是统计损失；若因此让结算失败，任务永远到不了终态，
	// 最终会被超时清扫全额退款——用户成功拿到了视频却一分钱不付。
	assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 4,
	}))
	actualQuota, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 4, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, initialWallet+(preConsumed-actualQuota), getUserQuota(t, userID))
	assert.Equal(t, actualQuota, getTaskQuota(t, task.ID))
	var user model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&user, userID).Error)
	assert.Equal(t, actualQuota, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
}

func TestResolutionSettlementAllowsRoundedZeroQuotaToComplete(t *testing.T) {
	truncate(t)
	const userID, channelID = 396, 396
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	task := makeTask(userID, channelID, 0, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		PricingKind:              model.TaskPricingKindVideoResolution,
		OriginModelName:          "tiny-price-video-model",
		EffectiveResolution:      "720p",
		SelectedResolutionPrice:  1e-300,
		EffectiveDurationSeconds: 1,
		GroupRatio:               1,
		QuotaPerUnit:             500,
	}
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 1,
	}))
	var completed model.Task
	require.NoError(t, model.DB.First(&completed, task.ID).Error)
	assert.Zero(t, completed.Quota)
	assert.True(t, completed.PrivateData.BillingContext.SettlementCompleted)
	assert.Zero(t, countLogs(t))
}

func TestResolutionExactSettlementInvalidatesPreConsumeCaches(t *testing.T) {
	truncate(t)
	server, client, _, _ := useIndependentAuthSessionRedis(t)
	const userID, tokenID, channelID = 400, 400, 400
	const tokenKey = "sk-resolution-exact-cache"
	seedUser(t, userID, 1_000_000)
	seedToken(t, tokenID, userID, tokenKey, 500_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"used_quota":    preConsumed,
		"request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	userCacheKey := "user:" + strconv.Itoa(userID)
	tokenCacheKey := "token:" + common.GenerateHMAC(tokenKey)
	require.NoError(t, client.HSet(context.Background(), userCacheKey, "Quota", "stale").Err())
	require.NoError(t, client.HSet(context.Background(), tokenCacheKey, "RemainQuota", "stale").Err())
	require.True(t, server.Exists(userCacheKey))
	require.True(t, server.Exists(tokenCacheKey))

	assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 8,
	}))
	assert.False(t, server.Exists(userCacheKey))
	assert.False(t, server.Exists(tokenCacheKey))
}

func TestResolutionCompletedSettlementRejectsConcurrentStaleInProgressUpdate(t *testing.T) {
	truncate(t)
	const userID, channelID = 397, 397
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	var stale model.Task
	require.NoError(t, model.DB.First(&stale, task.ID).Error)

	assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 4,
	}))
	stale.Progress = "80%"
	won, err := stale.UpdateWithStatus(model.TaskStatusInProgress)
	require.NoError(t, err)
	assert.False(t, won)

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 4, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, persisted.Quota)
	assert.True(t, persisted.PrivateData.BillingContext.SettlementCompleted)
}

func TestResolutionPendingSettlementRejectsConcurrentFailureUpdate(t *testing.T) {
	truncate(t)
	const userID, channelID = 398, 398
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)
	var staleFailure model.Task
	require.NoError(t, model.DB.First(&staleFailure, task.ID).Error)

	const callbackName = "test:pause_resolution_publication_with_log_failure"
	require.NoError(t, model.LOG_DB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Log" {
			tx.AddError(errors.New("forced pending settlement publication failure"))
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, model.LOG_DB.Callback().Create().Remove(callbackName))
		}
	})
	assert.False(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 4,
	}))
	require.NoError(t, model.LOG_DB.Callback().Create().Remove(callbackName))
	callbackRegistered = false

	staleFailure.Status = model.TaskStatusFailure
	staleFailure.Progress = "100%"
	won, err := staleFailure.UpdateWithStatus(model.TaskStatusInProgress)
	require.NoError(t, err)
	assert.False(t, won)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
	assert.True(t, persisted.PrivateData.BillingContext.SettlementPending)
}

func TestResolutionSettlementLosesToConcurrentFailureTerminalCAS(t *testing.T) {
	truncate(t)
	const userID, channelID = 399, 399
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	staleSuccess := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	staleSuccess.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(staleSuccess).Error)
	var failure model.Task
	require.NoError(t, model.DB.First(&failure, staleSuccess.ID).Error)
	failure.Status = model.TaskStatusFailure
	failure.Progress = "100%"
	won, err := failure.UpdateWithStatus(model.TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)

	assert.False(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, staleSuccess, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 4,
	}))
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, staleSuccess.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), persisted.Status)
	assert.Equal(t, preConsumed, persisted.Quota)
	assert.False(t, persisted.PrivateData.BillingContext.SettlementPending)
	assert.False(t, persisted.PrivateData.BillingContext.SettlementCompleted)
}

func TestResolutionSettlementRetriesFailedLogPublicationFromDurableMarker(t *testing.T) {
	truncate(t)
	const userID, channelID = 394, 394
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	const callbackName = "test:fail_resolution_settlement_log_create"
	require.NoError(t, model.LOG_DB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Log" {
			tx.AddError(errors.New("forced resolution settlement log failure"))
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, model.LOG_DB.Callback().Create().Remove(callbackName))
		}
	})

	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, EffectiveDurationSeconds: 4}
	assert.False(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, result))
	var pending model.Task
	require.NoError(t, model.DB.First(&pending, task.ID).Error)
	require.NotNil(t, pending.PrivateData.BillingContext)
	assert.True(t, pending.PrivateData.BillingContext.SettlementPending)
	assert.False(t, pending.PrivateData.BillingContext.SettlementCompleted)
	assert.Zero(t, countLogs(t))

	require.NoError(t, model.LOG_DB.Callback().Create().Remove(callbackName))
	callbackRegistered = false
	retryResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, EffectiveDurationSeconds: 8}
	assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, &pending, retryResult))
	var published model.Task
	require.NoError(t, model.DB.First(&published, task.ID).Error)
	assert.False(t, published.PrivateData.BillingContext.SettlementPending)
	assert.True(t, published.PrivateData.BillingContext.SettlementCompleted)
	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 4, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, want, published.Quota)
	assert.Equal(t, int64(1), countLogs(t))
}

func TestResolutionSettlementExportsUsageOnlyAfterSameDatabaseCommit(t *testing.T) {
	truncate(t)
	originalDataExportEnabled := common.DataExportEnabled
	common.DataExportEnabled = true
	model.CacheQuotaDataLock.Lock()
	model.CacheQuotaData = make(map[string]*model.QuotaData)
	model.CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		common.DataExportEnabled = originalDataExportEnabled
		model.CacheQuotaDataLock.Lock()
		model.CacheQuotaData = make(map[string]*model.QuotaData)
		model.CacheQuotaDataLock.Unlock()
	})

	const userID, channelID = 401, 401
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	const callbackName = "test:fail_same_database_pending_clear_after_log"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Task" {
			return
		}
		values, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok {
			return
		}
		if _, hasPrivateData := values["private_data"]; hasPrivateData {
			if _, hasQuota := values["quota"]; !hasQuota {
				tx.AddError(errors.New("forced same-database pending clear failure"))
			}
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
		}
	})

	result := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, EffectiveDurationSeconds: 16}
	assert.False(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, result))
	assert.Zero(t, countLogs(t))
	model.CacheQuotaDataLock.Lock()
	assert.Empty(t, model.CacheQuotaData)
	model.CacheQuotaDataLock.Unlock()

	require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	callbackRegistered = false
	var pending model.Task
	require.NoError(t, model.DB.First(&pending, task.ID).Error)
	assert.True(t, settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, &pending, result))
	assert.Equal(t, int64(1), countLogs(t))
	model.CacheQuotaDataLock.Lock()
	assert.Len(t, model.CacheQuotaData, 1)
	model.CacheQuotaDataLock.Unlock()
}

func TestResolutionSettlementPersistsFundingQuotaAndDurationAtomicallyAndRetriesIdempotently(t *testing.T) {
	truncate(t)
	const userID, tokenID, channelID = 40, 40, 40
	const userQuota, tokenQuota = 1_000_000, 2_000_000
	seedUser(t, userID, userQuota)
	seedToken(t, tokenID, userID, "sk-resolution-atomic", tokenQuota)
	seedChannel(t, channelID)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"used_quota":    preConsumed,
		"request_count": 1,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("used_quota", preConsumed).Error)
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	const callbackName = "test:fail_resolution_task_settlement_persistence"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" {
			tx.AddError(errors.New("forced task quota/context persistence failure"))
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
		}
	})

	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, EffectiveDurationSeconds: 16}
	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, taskResult)

	assert.Equal(t, preConsumed, task.Quota)
	assert.Zero(t, task.PrivateData.BillingContext.SettledDurationSeconds)
	assert.Equal(t, userQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, getTaskQuota(t, task.ID))
	var afterFailure model.Task
	require.NoError(t, model.DB.First(&afterFailure, task.ID).Error)
	require.NotNil(t, afterFailure.PrivateData.BillingContext)
	assert.Zero(t, afterFailure.PrivateData.BillingContext.SettledDurationSeconds)
	assert.Equal(t, int64(0), countLogs(t))
	var statsAfterFailure model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&statsAfterFailure, userID).Error)
	assert.Equal(t, preConsumed, statsAfterFailure.UsedQuota)
	assert.Equal(t, 1, statsAfterFailure.RequestCount)

	require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	callbackRegistered = false
	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, taskResult)

	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 16, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	delta := want - preConsumed
	assert.Equal(t, want, task.Quota)
	assert.Equal(t, userQuota-delta, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota-delta, getTokenRemainQuota(t, tokenID))
	var afterSuccess model.Task
	require.NoError(t, model.DB.First(&afterSuccess, task.ID).Error)
	assert.Equal(t, want, afterSuccess.Quota)
	require.NotNil(t, afterSuccess.PrivateData.BillingContext)
	assert.Equal(t, 16, afterSuccess.PrivateData.BillingContext.SettledDurationSeconds)
	assert.Equal(t, int64(1), countLogs(t))
	var statsAfterSuccess model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&statsAfterSuccess, userID).Error)
	assert.Equal(t, want, statsAfterSuccess.UsedQuota)
	assert.Equal(t, 1, statsAfterSuccess.RequestCount)
	var channelAfterSuccess model.Channel
	require.NoError(t, model.DB.Select("used_quota").First(&channelAfterSuccess, channelID).Error)
	assert.EqualValues(t, want, channelAfterSuccess.UsedQuota)

	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, taskResult)
	assert.Equal(t, userQuota-delta, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota-delta, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
	require.NoError(t, model.DB.Select("used_quota", "request_count").First(&statsAfterSuccess, userID).Error)
	assert.Equal(t, want, statsAfterSuccess.UsedQuota)
	assert.Equal(t, 1, statsAfterSuccess.RequestCount)
}

func TestResolutionSettlementRollsBackSubscriptionWithTaskPersistenceFailure(t *testing.T) {
	truncate(t)
	const userID, channelID, subscriptionID = 41, 41, 41
	const subscriptionTotal, subscriptionUsed int64 = 2_000_000, 1_000_000
	seedUser(t, userID, 0)
	seedChannel(t, channelID)
	seedSubscription(t, subscriptionID, userID, subscriptionTotal, subscriptionUsed)
	preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceSubscription, subscriptionID)
	task.PrivateData.BillingContext = resolutionBillingContext(8)
	require.NoError(t, model.DB.Create(task).Error)

	const callbackName = "test:fail_resolution_subscription_task_persistence"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" {
			tx.AddError(errors.New("forced subscription task persistence failure"))
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
		}
	})

	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, EffectiveDurationSeconds: 4}
	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, taskResult)
	assert.Equal(t, subscriptionUsed, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, preConsumed, getTaskQuota(t, task.ID))
	assert.Zero(t, task.PrivateData.BillingContext.SettledDurationSeconds)
	assert.Equal(t, int64(0), countLogs(t))

	require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	callbackRegistered = false
	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, taskResult)

	want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 4, 1.25, map[string]float64{"video_input": 1.2})
	require.NoError(t, err)
	assert.Equal(t, subscriptionUsed-int64(preConsumed-want), getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, want, getTaskQuota(t, task.ID))
	var settled model.Task
	require.NoError(t, model.DB.First(&settled, task.ID).Error)
	require.NotNil(t, settled.PrivateData.BillingContext)
	assert.Equal(t, 4, settled.PrivateData.BillingContext.SettledDurationSeconds)
	assert.Equal(t, int64(1), countLogs(t))
}

func TestResolutionSettlementAuditsQuotaSaturation(t *testing.T) {
	truncate(t)
	const userID, channelID = 37, 37
	seedUser(t, userID, math.MaxInt32)
	seedChannel(t, channelID)
	task := makeTask(userID, channelID, 1, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		PricingKind:              model.TaskPricingKindVideoResolution,
		OriginModelName:          "video-model",
		EffectiveResolution:      "1080p",
		SelectedResolutionPrice:  1e10,
		EffectiveDurationSeconds: 1,
		GroupRatio:               1,
		QuotaPerUnit:             common.QuotaPerUnit,
	}
	require.NoError(t, model.DB.Create(task).Error)

	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:                   model.TaskStatusSuccess,
		EffectiveDurationSeconds: 1,
	})

	assert.Equal(t, math.MaxInt32, task.Quota)
	log := getLastLog(t)
	require.NotNil(t, log)
	var other map[string]any
	require.NoError(t, common.Unmarshal([]byte(log.Other), &other))
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, adminInfo, "quota_saturation")
	assert.Equal(t, map[string]any{
		"effective_resolution":       "1080p",
		"selected_price_per_second":  1e10,
		"submitted_duration_seconds": float64(1),
		"effective_duration_seconds": float64(1),
	}, adminInfo["video_resolution_billing"])
}

func TestResolutionSettlementOnlyAcceptsBoundedActualDuration(t *testing.T) {
	for _, tc := range []struct {
		name           string
		actualDuration int
		wantDuration   int
		wantUnchanged  bool
	}{
		{name: "absent uses submitted", actualDuration: 0, wantDuration: 8},
		{name: "valid actual", actualDuration: 4, wantDuration: 4},
		{name: "negative rejected", actualDuration: -1, wantUnchanged: true},
		{name: "over limit rejected", actualDuration: relaycommon.MaxTaskDurationSeconds + 1, wantUnchanged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			userID := 100 + tc.actualDuration
			if userID <= 0 {
				userID = 99
			}
			channelID := userID
			seedUser(t, userID, 1_000_000)
			seedChannel(t, channelID)
			preConsumed, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, 8, 1.25, map[string]float64{"video_input": 1.2})
			require.NoError(t, err)
			task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
			task.PrivateData.BillingContext = resolutionBillingContext(8)
			require.NoError(t, model.DB.Create(task).Error)

			settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
				Status:                   model.TaskStatusSuccess,
				EffectiveDurationSeconds: tc.actualDuration,
			})

			if tc.wantUnchanged {
				assert.Equal(t, preConsumed, task.Quota)
				return
			}
			want, _, err := relaycommon.CalculateVideoResolutionQuota(0.1, tc.wantDuration, 1.25, map[string]float64{"video_input": 1.2})
			require.NoError(t, err)
			assert.Equal(t, want, task.Quota)
		})
	}
}

// ===========================================================================
// PerCallBilling tests — settleTaskBillingOnComplete
// ===========================================================================

func TestSettle_PerCallBilling_SkipsAdaptorAdjust(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 30, 30, 30
	const initQuota, preConsumed = 10000, 5000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-adaptor", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 2000}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no adjustment despite adaptor returning 2000
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_PerCallBilling_SkipsTotalTokens(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 31, 31, 31
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 7000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-tokens", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 0}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, TotalTokens: 9999}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no recalculation by tokens
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_NonPerCallBilling_AppliesAdaptorAdjustment(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 32, 32, 32
	const initQuota, preConsumed = 10000, 5000
	const adaptorQuota = 3000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-nonpercall-adj", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	// PerCallBilling defaults to false

	adaptor := &mockAdaptor{adjustReturn: adaptorQuota}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Non-per-call: adaptor adjustment applies (refund 2000)
	assert.Equal(t, initQuota+(preConsumed-adaptorQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-adaptorQuota), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, adaptorQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}
