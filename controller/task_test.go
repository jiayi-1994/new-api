package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLegacyFrozenPlanWithStaleResolvedBillingUsesWalletAndPersistsWithoutReservation(t *testing.T) {
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalRedisEnabled := common.RedisEnabled
	originalBatchUpdateEnabled := common.BatchUpdateEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Task{}))
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.RedisEnabled = originalRedisEnabled
		common.BatchUpdateEnabled = originalBatchUpdateEnabled
	})

	newLegacyInfo := func(userID int, requestID string) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			RequestId:       requestID,
			UserId:          userID,
			OriginModelName: "legacy-video",
			UsingGroup:      "default",
			ForcePreConsume: true,
			IsPlayground:    true,
			ChannelMeta:     &relaycommon.ChannelMeta{},
			PriceData: hosttypes.PriceData{
				ModelPrice: 0.2,
				Quota:      100,
				UsePrice:   true,
				GroupRatioInfo: hosttypes.GroupRatioInfo{
					GroupRatio: 1,
				},
			},
			TaskRelayInfo: &relaycommon.TaskRelayInfo{
				BillingPlan: relaycommon.NewLegacyTaskBillingPlan("legacy-video", requestID+"-frozen"),
				ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{
					Selection: relaycommon.VideoBillingSelection{
						EffectiveResolution:      "1080p",
						EffectiveDurationSeconds: 8,
					},
					SelectedResolutionPrice: 0.9,
					QuotaPerUnit:            500,
				},
			},
		}
	}
	newTask := func(info *relaycommon.RelayInfo, billingContext *model.TaskBillingContext) *model.Task {
		task := model.InitTask(constant.TaskPlatform("video-test"), info)
		task.PrivateData.BillingSource = info.BillingSource
		task.PrivateData.BillingContext = billingContext
		if billingContext != nil && billingContext.PricingKind == model.TaskPricingKindVideoResolution {
			task.PrivateData.BillingReservationRequestId = taskBillingReservationRequestID(info)
		}
		task.Quota = 100
		return task
	}

	require.NoError(t, db.Create(&model.User{Id: 701, Username: "legacy-success", AffCode: "legacy-success", Quota: 1_000}).Error)
	successInfo := newLegacyInfo(701, "req-live-success")
	successInfo.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	session, apiErr := service.NewBillingSession(c, successInfo, 100)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Equal(t, service.BillingSourceWallet, successInfo.BillingSource)
	require.NoError(t, session.Settle(100))
	assert.False(t, session.NeedsRefund())
	billingContext := taskBillingContextFromRelayInfo(successInfo)
	require.NotNil(t, billingContext)
	require.Empty(t, billingContext.PricingKind)
	require.Empty(t, taskBillingReservationRequestID(successInfo))
	require.NoError(t, service.PersistSubmittedTask(c, newTask(successInfo, billingContext)))
	var persisted int64
	require.NoError(t, db.Model(&model.Task{}).Where("user_id = ?", 701).Count(&persisted).Error)
	assert.EqualValues(t, 1, persisted)
	var walletQuota int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 701).Select("quota").Scan(&walletQuota).Error)
	assert.Equal(t, 900, walletQuota)
}

func TestTaskBillingContextResolutionPlanRequiresResolvedSelection(t *testing.T) {
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan(
		"video-model", "req-resolution", map[string]float64{"720p": 0.1},
	)
	require.NoError(t, err)

	assert.Nil(t, taskBillingContextFromRelayInfo(&relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{BillingPlan: plan},
	}))
	assert.Nil(t, taskBillingContextFromRelayInfo(&relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			BillingPlan:          plan,
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{},
		},
	}))
	assert.Nil(t, taskBillingContextFromRelayInfo(&relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			BillingPlan: plan,
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{
				Selection: relaycommon.VideoBillingSelection{
					EffectiveResolution:      "720p",
					EffectiveDurationSeconds: 5,
				},
				SelectedResolutionPrice: 0.2,
				QuotaPerUnit:            500,
			},
		},
	}))
}

func TestTaskBillingContextLegacyFixedPerSecondRemainsPerSecond(t *testing.T) {
	original := ratio_setting.TaskBillingMode2JSONString()
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"legacy-video":"per_second"}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(original)) })
	info := &relaycommon.RelayInfo{
		OriginModelName: "legacy-video",
		PriceData: hosttypes.PriceData{
			ModelPrice: 0.3,
			UsePrice:   true,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}

	assert.False(t, taskBillingContextFromRelayInfo(info).PerCallBilling)
}

func TestTaskBillingReservationRequestIDUsesFrozenPlanIdentity(t *testing.T) {
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan(
		"video-model", "req-frozen", map[string]float64{"720p": 0.1},
	)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		RequestId:     "req-live-mutated",
		TaskRelayInfo: &relaycommon.TaskRelayInfo{BillingPlan: plan},
	}

	assert.Equal(t, "req-frozen", taskBillingReservationRequestID(info))
}

func resolutionPricesFromPublicPricing(t *testing.T, modelName string) map[string]float64 {
	t.Helper()
	model.InvalidatePricingCache()
	for _, pricing := range model.GetPricing() {
		if pricing.ModelName == modelName {
			return pricing.ResolutionPrices
		}
	}
	require.FailNow(t, "model missing from public pricing response")
	return nil
}

// 切换渠道的实际能力（渠道类型）会改变分辨率计划的可路由性，
// 但不得改动存储的分辨率表、冻结的计划快照或公开定价响应。
func TestChannelCapabilityToggleChangesRoutingWithoutMutatingPricingState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := model.DB
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.MemoryCacheEnabled = true
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
		model.InvalidatePricingCache()
	})

	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"toggle-resolution-model":{"720p":0.1}}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices))
	})

	priority := int64(1)
	require.NoError(t, db.Create(&model.Channel{
		Id: 993021, Type: constant.ChannelTypeKling, Key: "toggle-key", Name: "toggle-channel",
		Status: common.ChannelStatusEnabled, Group: "default", Models: "toggle-resolution-model", Priority: &priority,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "toggle-resolution-model", ChannelId: 993021, Enabled: true, Priority: &priority,
	}).Error)
	model.InitChannelCache()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	plan, err := relay.PrepareTaskBillingPlan(c, "toggle-resolution-model", "req-capability-toggle")
	require.NoError(t, err)
	require.Equal(t, relaycommon.TaskBillingKindVideoResolution, plan.Kind())
	storedBefore := ratio_setting.VideoResolutionPrice2JSONString()
	pricingBefore := resolutionPricesFromPublicPricing(t, "toggle-resolution-model")
	require.Equal(t, map[string]float64{"720p": 0.1}, pricingBefore)

	info := &relaycommon.RelayInfo{
		OriginModelName: "toggle-resolution-model",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	newRetryParam := func() *service.RetryParam {
		return &service.RetryParam{
			Ctx:                 c,
			TokenGroup:          "default",
			ModelName:           info.OriginModelName,
			RequestPath:         c.Request.URL.Path,
			AllowedChannelTypes: relay.CompatibleTaskChannelTypes(plan.Kind()),
			Retry:               common.GetPointer(0),
		}
	}

	// 唯一渠道类型不具备分辨率计费能力：路由不可用
	unavailable, channelErr := getChannel(c, info, newRetryParam())
	assert.Nil(t, unavailable)
	require.NotNil(t, channelErr)

	// 切换该渠道到具备能力的类型：路由恢复可用
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 993021).
		Update("type", constant.ChannelTypeSora).Error)
	model.InitChannelCache()

	selected, channelErr := getChannel(c, info, newRetryParam())
	require.Nil(t, channelErr)
	require.NotNil(t, selected)
	assert.Equal(t, constant.ChannelTypeSora, selected.Type)

	// 能力切换不改动定价状态
	assert.Equal(t, storedBefore, ratio_setting.VideoResolutionPrice2JSONString())
	frozenPrice, ok := plan.ResolutionPrice("720p")
	require.True(t, ok)
	assert.Equal(t, 0.1, frozenPrice)
	assert.Equal(t, pricingBefore, resolutionPricesFromPublicPricing(t, "toggle-resolution-model"))
}

// 冻结为旧版的请求在持久化时刻即使已有匹配的在线分辨率表，
// 落库的计费上下文也必须保持旧版合同（无 PricingKind、无预留标识）。
func TestLegacyBillingContextIgnoresLiveResolutionTableAddedAfterFreeze(t *testing.T) {
	originalPrices := ratio_setting.VideoResolutionPrice2JSONString()
	originalModes := ratio_setting.TaskBillingMode2JSONString()
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{"legacy-video":{"720p":0.9}}`))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"legacy-video":"per_call"}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(originalPrices))
		require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(originalModes))
	})

	info := &relaycommon.RelayInfo{
		RequestId:       "req-live",
		OriginModelName: "legacy-video",
		PriceData: hosttypes.PriceData{
			ModelPrice: 0.2,
			UsePrice:   true,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			BillingPlan: relaycommon.NewLegacyTaskBillingPlan("legacy-video", "req-legacy-frozen"),
		},
	}

	billingContext := taskBillingContextFromRelayInfo(info)
	require.NotNil(t, billingContext)
	assert.Empty(t, billingContext.PricingKind)
	assert.Equal(t, 0.2, billingContext.ModelPrice)
	assert.Equal(t, "legacy-video", billingContext.OriginModelName)
	assert.True(t, billingContext.PerCallBilling)
	assert.Empty(t, billingContext.EffectiveResolution)
	assert.Empty(t, taskBillingReservationRequestID(info))
}

func TestResolutionSnapshotOmitsBillingUnitAndLegacyPerCallFlag(t *testing.T) {
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan(
		"video-model", "req-resolution-snapshot", map[string]float64{"1080p": 0.18},
	)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		OriginModelName: "video-model",
		PriceData: hosttypes.PriceData{
			ModelPrice: 0.18,
			UsePrice:   true,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1.25,
			},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			BillingPlan: plan,
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{
				Selection: relaycommon.VideoBillingSelection{
					EffectiveResolution:      "1080p",
					EffectiveDurationSeconds: 8,
					IndependentRatios:        map[string]float64{"video_input": 1.2},
				},
				SelectedResolutionPrice: 0.18,
				QuotaPerUnit:            321_000,
			},
		},
	}

	context := taskBillingContextFromRelayInfo(info)
	require.NotNil(t, context)
	assert.Equal(t, "video_resolution", context.PricingKind)
	assert.False(t, context.PerCallBilling)
	assert.Equal(t, "1080p", context.EffectiveResolution)
	assert.Equal(t, 0.18, context.SelectedResolutionPrice)
	assert.Equal(t, 8, context.EffectiveDurationSeconds)
	assert.Equal(t, 321_000.0, context.QuotaPerUnit)
	assert.Equal(t, map[string]float64{"video_input": 1.2}, context.IndependentRatios)
	assert.Empty(t, context.OtherRatios)

	raw, err := common.Marshal(context)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "billing_unit")
}

func TestResolutionPricingAdminTaskIncludesBillingDetailsAndUserTaskOmitsThem(t *testing.T) {
	originalRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = originalRedisEnabled })
	task := &model.Task{
		ID:       1,
		TaskID:   "suno-task",
		Platform: constant.TaskPlatformSuno,
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{
				PricingKind:              model.TaskPricingKindVideoResolution,
				EffectiveResolution:      "1080p",
				SelectedResolutionPrice:  0.18,
				EffectiveDurationSeconds: 8,
				SettledDurationSeconds:   4,
				IndependentRatios:        map[string]float64{"video_input": 1.2},
			},
		},
	}

	adminTasks, err := tasksToDto([]*model.Task{task}, true)
	require.NoError(t, err)
	require.Len(t, adminTasks, 1)
	require.NotNil(t, adminTasks[0].BillingDetails)
	assert.Equal(t, "1080p", adminTasks[0].BillingDetails.Resolution)
	assert.Equal(t, 0.18, adminTasks[0].BillingDetails.SelectedPricePerSecond)
	assert.Equal(t, 8, adminTasks[0].BillingDetails.SubmittedDurationSeconds)
	assert.Equal(t, 4, adminTasks[0].BillingDetails.EffectiveDurationSeconds)
	assert.Equal(t, map[string]float64{"video_input": 1.2}, adminTasks[0].BillingDetails.IndependentRatios)

	userTasks, err := tasksToDto([]*model.Task{task}, false)
	require.NoError(t, err)
	require.Len(t, userTasks, 1)
	assert.Nil(t, userTasks[0].BillingDetails)
}

func TestTasksToDtoProjectsNonSunoTasksWithoutUpstreamData(t *testing.T) {
	const upstreamTaskID = "upstream/task?X-Amz-Signature=secret"
	const gatewayTaskID = "task_AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	dangerousData, err := common.Marshal(map[string]any{
		"task_id":     upstreamTaskID,
		"url":         "https://storage.example.com/video.mp4?X-Amz-Signature=secret",
		"video_url":   "https://provider.example.com/video.mp4",
		"metadata":    map[string]any{"origin_video_url": "https://r2.example.com/video.mp4?token=secret"},
		"base64_data": "data:video/mp4;base64,secret",
	})
	require.NoError(t, err)

	tasks := []*model.Task{
		{
			ID:          1001,
			TaskID:      upstreamTaskID,
			UserId:      42,
			Platform:    "video-provider",
			Status:      model.TaskStatusSuccess,
			Progress:    "100%",
			FailReason:  "https://legacy.example.com/video.mp4?signature=secret",
			PrivateData: model.TaskPrivateData{ResultURL: "https://storage.example.com/video.mp4?X-Amz-Signature=secret"},
			Properties:  model.Properties{OriginModelName: "public-video-model"},
			Data:        dangerousData,
		},
		{
			ID:          1002,
			TaskID:      gatewayTaskID,
			UserId:      42,
			Platform:    "video-provider",
			Status:      model.TaskStatusInProgress,
			Progress:    "31%",
			PrivateData: model.TaskPrivateData{UpstreamTaskID: "upstream-task-123"},
			Properties:  model.Properties{OriginModelName: "public-video-model"},
			Data:        dangerousData,
		},
		{
			ID:         1003,
			TaskID:     "legacy-upstream-id",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			Progress:   "64%",
			FailReason: "provider rejected the prompt",
			Properties: model.Properties{OriginModelName: "public-video-model"},
			Data:       dangerousData,
		},
		{
			ID:         1004,
			TaskID:     "legacy/with?reserved=chars",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: "data:video/mp4;base64,secret",
			Properties: model.Properties{OriginModelName: "public-video-model"},
			Data:       dangerousData,
		},
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	require.Len(t, results, len(tasks))

	success := results[0]
	assert.Empty(t, success.FailReason)
	require.NotEmpty(t, success.ResultURL)
	assert.Regexp(t, `^task_[0-9a-f]{32}$`, success.TaskID)
	assert.True(t, strings.HasPrefix(success.ResultURL, "/v1/videos/"+success.TaskID+"/content?video_token="))
	parsedURL, err := url.Parse(success.ResultURL)
	require.NoError(t, err)
	grant, err := service.ParseVideoContentToken(parsedURL.Query().Get("video_token"), success.TaskID)
	require.NoError(t, err)
	assert.Equal(t, 42, grant.OwnerUserID)
	assert.Equal(t, int64(1001), grant.TaskRecordID)
	assertProjectedVideoData(t, success.Data, success.TaskID, "completed", 100, "public-video-model", success.ResultURL)
	_, err = service.ParseVideoContentToken(parsedURL.Query().Get("video_token"), upstreamTaskID)
	assert.Error(t, err)

	assert.Empty(t, results[1].ResultURL)
	assert.Empty(t, results[1].FailReason)
	assert.Equal(t, gatewayTaskID, results[1].TaskID)
	assertProjectedVideoData(t, results[1].Data, gatewayTaskID, "in_progress", 31, "public-video-model", "")

	assert.Empty(t, results[2].ResultURL)
	assert.Equal(t, "provider rejected the prompt", results[2].FailReason)
	assertProjectedVideoData(t, results[2].Data, results[2].TaskID, "failed", 64, "public-video-model", "")

	assert.Empty(t, results[3].ResultURL)
	assert.Empty(t, results[3].FailReason)
	assertProjectedVideoData(t, results[3].Data, results[3].TaskID, "failed", 0, "public-video-model", "")

	for _, result := range results {
		serialized, err := common.Marshal(result)
		require.NoError(t, err)
		serializedTask := string(serialized)
		assert.NotContains(t, serializedTask, upstreamTaskID)
		assert.NotContains(t, serializedTask, "storage.example.com")
		assert.NotContains(t, serializedTask, "provider.example.com")
		assert.NotContains(t, serializedTask, "r2.example.com")
		assert.NotContains(t, serializedTask, "X-Amz-Signature")
		assert.NotContains(t, serializedTask, "base64_data")
	}
}

func TestTasksToDtoClearsUnsafeFailureReasons(t *testing.T) {
	unsafeReasons := []string{
		"upstream returned https://provider.example.com/error?signature=secret",
		"download failed from //storage.example.com/video.mp4",
		"data:video/mp4;base64,secret",
		"upload failed at oss-cn-hangzhou.aliyuncs.com/object",
		"request to 192.0.2.4:443 failed",
		"upload failed at oss-cn-hangzhou.aliyuncs.com",
		"request to 192.0.2.4 failed",
		"storage.example.com",
		"192.0.2.4",
		"provider returned X-Amz-Signature=secret",
		"provider returned Signature=secret",
		"provider returned OSSAccessKeyId=secret",
		"provider returned api_key=secret",
		"provider returned access_token=secret",
		"provider returned Authorization: Bearer sk-secret",
		"provider returned X-Api-Key=secret",
		"provider returned X-Goog-Api-Key=secret",
		"Incorrect X-Api-Key provided: sk-secret",
		"upstream rejected Bearer sk-secret",
		"Incorrect API key provided: sk-live-secret",
		"download failed at cdn.download",
		"request to storage.video failed",
		"lookup storage.video: no such host",
		"could not resolve cdn.download",
		"certificate is valid for public.example, not cdn.download",
		"TLS handshake with storage.video failed",
		"hostname mismatch: storage.video",
		"TLS handshake with storage.corp failed",
		"hostname mismatch: storage.lan",
		"dial tcp [2001:db8::1]:443: connect: connection refused",
		"model foo.bar unavailable",
	}
	tasks := make([]*model.Task, 0, len(unsafeReasons))
	for i, reason := range unsafeReasons {
		tasks = append(tasks, &model.Task{
			ID:         int64(i + 2000),
			TaskID:     "legacy-upstream-task",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: reason,
		})
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	for _, result := range results {
		assert.Empty(t, result.FailReason)
		serialized, err := common.Marshal(result)
		require.NoError(t, err)
		assert.NotContains(t, string(serialized), "example.com")
		assert.NotContains(t, string(serialized), "X-Amz-Signature")
	}
}

func TestTasksToDtoPreservesSafeFailureReasons(t *testing.T) {
	safeReasons := []string{
		"request signature is invalid",
		"worker emitted // as a delimiter",
		"X-Amz-Algorithm is unavailable",
		"failed to parse config.json",
		"provider protocol v1.2 is unsupported",
		"API key authentication failed",
		"access token expired",
		"client secret is not configured",
	}
	tasks := make([]*model.Task, 0, len(safeReasons))
	for i, reason := range safeReasons {
		tasks = append(tasks, &model.Task{
			ID:         int64(i + 2200),
			TaskID:     "legacy-upstream-task",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: reason,
		})
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	for i, result := range results {
		assert.Equal(t, safeReasons[i], result.FailReason)
	}
}

func TestTasksToDtoClearsFailuresLeakingUpstreamTaskID(t *testing.T) {
	tasks := []*model.Task{
		{
			ID:         2401,
			TaskID:     "legacy-upstream-task-id",
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: "provider rejected legacy-upstream-task-id",
		},
		{
			ID:          2402,
			TaskID:      "task_AbCdEfGhIjKlMnOpQrStUvWxYz012345",
			Platform:    "video-provider",
			Status:      model.TaskStatusFailure,
			FailReason:  "provider rejected new-upstream-operation-id",
			PrivateData: model.TaskPrivateData{UpstreamTaskID: "new-upstream-operation-id"},
		},
		{
			ID:         2403,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: "provider rejected the prompt",
		},
		{
			ID:         2404,
			TaskID:     "whitespace-legacy-upstream-id",
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: "provider rejected whitespace-legacy-upstream-id",
			PrivateData: model.TaskPrivateData{
				UpstreamTaskID: " \t",
			},
		},
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	require.Len(t, results, len(tasks))
	assert.Empty(t, results[0].FailReason)
	assert.Empty(t, results[1].FailReason)
	assert.Equal(t, "provider rejected the prompt", results[2].FailReason)
	assert.Empty(t, results[3].FailReason)
}

func TestTasksToDtoClearsProviderCredentialFailureReasons(t *testing.T) {
	unsafeReasons := []string{
		"download gs://bucket/object",
		"download s3://bucket/object",
		"download r2://bucket/object",
		"provider returned X-Goog-Signature=secret",
		"provider returned X-Amz-Credential=secret",
		"provider returned X-Oss-Signature=secret",
		"provider returned X-Ms-Signature=secret",
		"provider returned OSSAccessKeyId=secret",
		"provider returned GoogleAccessId=secret",
		"provider returned AWSAccessKeyId=secret",
		"provider returned Credential=secret",
		"provider returned sig=secret",
	}
	tasks := make([]*model.Task, 0, len(unsafeReasons))
	for i, reason := range unsafeReasons {
		tasks = append(tasks, &model.Task{
			ID:         int64(i + 2450),
			TaskID:     "legacy-upstream-task",
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: reason,
		})
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	for _, result := range results {
		assert.Empty(t, result.FailReason)
	}
}

func TestTasksToDtoExposesOnlyPublicNonSunoProperties(t *testing.T) {
	const upstreamModel = "provider-internal-model"
	task := &model.Task{
		ID:       2501,
		TaskID:   "legacy-upstream-task",
		Platform: "video-provider",
		Status:   model.TaskStatusInProgress,
		Properties: model.Properties{
			Input:             "safe user input",
			OriginModelName:   "public-video-model",
			UpstreamModelName: upstreamModel,
		},
	}

	results, err := tasksToDto([]*model.Task{task}, false)
	require.NoError(t, err)
	require.Len(t, results, 1)

	serialized, err := common.Marshal(results[0])
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "upstream_model_name")
	assert.NotContains(t, string(serialized), upstreamModel)
	assert.Contains(t, string(serialized), "safe user input")
	assert.Contains(t, string(serialized), "public-video-model")
}

func TestTasksToDtoRedactsSchemeRelativeFailureURLs(t *testing.T) {
	for i, reason := range []string{
		"download failed from //storage/video?token=secret",
		"download failed from //[2001:db8::1]/video",
	} {
		task := &model.Task{
			ID:         int64(2300 + i),
			TaskID:     "legacy-upstream-task",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: reason,
		}

		results, err := tasksToDto([]*model.Task{task}, false)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Empty(t, results[0].FailReason)
	}
}

func TestTaskIDFilterParsesVideoContentAlias(t *testing.T) {
	publicTaskID, err := service.VideoContentPublicTaskID(4001, "legacy-upstream-task", false)
	require.NoError(t, err)

	taskID, taskRecordID := taskIDFilter(publicTaskID)
	assert.Empty(t, taskID)
	assert.Equal(t, int64(4001), taskRecordID)

	taskID, taskRecordID = taskIDFilter("legacy-upstream-task")
	assert.Equal(t, "legacy-upstream-task", taskID)
	assert.Zero(t, taskRecordID)
}

func TestTasksToDtoAliasesTaskWithoutARealSeparateUpstreamID(t *testing.T) {
	const gatewayTaskID = "task_AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	task := &model.Task{
		ID:          2500,
		TaskID:      gatewayTaskID,
		UserId:      42,
		Platform:    "video-provider",
		Status:      model.TaskStatusInProgress,
		PrivateData: model.TaskPrivateData{UpstreamTaskID: " \t"},
	}

	results, err := tasksToDto([]*model.Task{task}, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.NotEqual(t, gatewayTaskID, results[0].TaskID)
	assert.Regexp(t, `^task_[0-9a-f]{32}$`, results[0].TaskID)
}

func TestTasksToDtoPreservesSunoData(t *testing.T) {
	sunoData, err := common.Marshal(map[string]any{"audio_url": "https://audio.example.com/song.mp3", "metadata": map[string]any{"id": "upstream-song"}})
	require.NoError(t, err)
	task := &model.Task{
		ID:       3001,
		TaskID:   "suno-public-task",
		Platform: constant.TaskPlatformSuno,
		Status:   model.TaskStatusSuccess,
		Data:     sunoData,
	}

	results, err := tasksToDto([]*model.Task{task}, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.JSONEq(t, string(sunoData), string(results[0].Data))
	assert.Equal(t, task.GetResultURL(), results[0].ResultURL)
}

func assertProjectedVideoData(t *testing.T, data []byte, taskID, status string, progress int, modelName, resultURL string) {
	t.Helper()
	var projection map[string]any
	require.NoError(t, common.Unmarshal(data, &projection))
	expected := map[string]any{
		"object":   "video",
		"status":   status,
		"progress": float64(progress),
		"task_id":  taskID,
		"model":    modelName,
	}
	if resultURL != "" {
		expected["url"] = resultURL
	}
	assert.Equal(t, expected, projection)
}
