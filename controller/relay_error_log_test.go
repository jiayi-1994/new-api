package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// errorLogTestDB points the main and log databases at an in-memory SQLite
// holding user 7, with error logging on and Redis and the memory cache off.
func errorLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled, previousMemoryCache := common.RedisEnabled, common.MemoryCacheEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousErrorLogEnabled := constant.ErrorLogEnabled

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}, &model.Channel{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedisEnabled, previousMemoryCache
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		constant.ErrorLogEnabled = previousErrorLogEnabled
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, database.Create(&model.User{Id: 7, Username: "log-owner", Group: "default"}).Error)
	return database
}

func TestProcessChannelErrorUsesSnapshotWithoutLeakingChannelMetadata(t *testing.T) {
	database := errorLogTestDB(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 7)
	ctx.Set("username", "log-owner")
	ctx.Set("token_name", "test-token")
	ctx.Set("token_id", 11)
	ctx.Set("original_model", "gpt-test")
	ctx.Set("group", "default")
	ctx.Set("channel_id", 202)
	ctx.Set("channel_name", "mutable-context-channel")
	ctx.Set("channel_type", 9)
	ctx.Set("use_channel", []string{"101"})
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now().Add(-time.Second))
	// The second attempt failed; only its scheduling selection belongs to this row.
	service.RequestPolicy(ctx).BeginAttempt(&model.Channel{Id: 100}, "default")
	service.RequestPolicy(ctx).BeginAttempt(&model.Channel{Id: 101}, "default")
	common.SetContextKey(ctx, constant.ContextKeyVideoSchedBoard, []service.VideoScheduleRecord{
		{SelectionSeq: 1, AttemptSeq: 1, Mode: "on", Group: "default", Recommended: 100, Candidates: []service.VideoScheduleRow{{ID: 100, Name: "first"}}},
		{SelectionSeq: 2, AttemptSeq: 2, Mode: "on", Group: "default", Recommended: 101, Candidates: []service.VideoScheduleRow{{ID: 100, Name: "first", Excluded: "tried"}, {ID: 101, Name: "snapshot-channel"}}},
	})

	channelSnapshot := types.ChannelError{
		ChannelId:   101,
		ChannelType: 1,
		ChannelName: "snapshot-channel",
		AutoBan:     false,
	}
	apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	processChannelError(ctx, channelSnapshot, apiErr, nil)

	var stored model.Log
	require.NoError(t, database.First(&stored).Error)
	assert.Equal(t, channelSnapshot.ChannelId, stored.ChannelId)
	storedOther, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusBadGateway), storedOther["status_code"])
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, storedOther, key)
	}
	adminInfo, ok := storedOther["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"101"}, adminInfo["use_channel"])
	schedule, ok := adminInfo["video_schedule"].(map[string]any)
	require.True(t, ok)
	attempts, ok := schedule["attempts"].([]any)
	require.True(t, ok)
	require.Len(t, attempts, 1)
	assert.Equal(t, float64(2), attempts[0].(map[string]any)["attempt_seq"])
	assert.NotContains(t, storedOther, "video_schedule")

	logs, total, err := model.GetUserLogs(7, model.LogTypeError, 0, 0, "", "", 0, 10, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	assert.Zero(t, logs[0].ChannelId)
	assert.Empty(t, logs[0].ChannelName)
	userOther, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, userOther, "admin_info")
	assert.NotContains(t, userOther, "video_schedule")
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, userOther, key)
	}
}

// A request that scored candidates but persisted no task leaves exactly one
// summary row holding every selection, readable only by administrators. The
// Distribute exit writes it after a rejection and, on a panic, releases the
// probe lease and re-raises the original value.
func TestDistributeLogsVideoScheduleSummaryOnce(t *testing.T) {
	database := errorLogTestDB(t)
	require.NoError(t, i18n.Init())
	records := []service.VideoScheduleRecord{
		{SelectionSeq: 1, AttemptSeq: 1, Mode: "on", Group: "vip", Candidates: []service.VideoScheduleRow{{ID: 300, Name: "secret-upstream", Excluded: "at capacity"}}},
		{SelectionSeq: 2, AttemptSeq: 1, Mode: "on", Group: "default", Candidates: []service.VideoScheduleRow{{ID: 301, Name: "other-upstream", Excluded: "cost/sell 1.75 > 1.00"}}},
	}
	serve := func(path string, prepare func(*gin.Context), handler gin.HandlerFunc) *httptest.ResponseRecorder {
		engine := gin.New()
		engine.Any(path, func(c *gin.Context) {
			c.Set("id", 7)
			c.Set("username", "log-owner")
			c.Set("token_name", "video-token")
			c.Set("token_id", 11)
			c.Set("group", "default")
			common.SetContextKey(c, constant.ContextKeyVideoSchedBoard, records)
			prepare(c)
			c.Next()
		}, middleware.Distribute(), handler)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		return recorder
	}
	pinMissingChannel := func(c *gin.Context) {
		c.Set("resolved_task_model", "videos-fast")
		service.GetChannelConstraints(c).AddPin(taskdto.ChannelPin{ChannelId: 99999, Source: taskdto.PinSourceToken})
	}

	// Selection rejects the request before any channel is attempted.
	recorder := serve("/v1/videos", pinMissingChannel, func(c *gin.Context) { t.Fatal("a rejected request never reaches the relay") })
	require.GreaterOrEqual(t, recorder.Code, http.StatusBadRequest)
	// A persisted task owns its own log; its request writes no summary.
	serve("/v1/videos", func(c *gin.Context) {
		pinMissingChannel(c)
		common.SetContextKey(c, constant.ContextKeyTaskPersisted, "task_done")
	}, func(c *gin.Context) {})

	var rows []model.Log
	require.NoError(t, database.Order("id").Find(&rows).Error)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, model.LogTypeError, row.Type)
	assert.Equal(t, "videos-fast", row.ModelName)
	assert.Zero(t, row.ChannelId, "no channel was selected")
	assert.Equal(t, 11, row.TokenId)
	other, err := common.StrToMap(row.Other)
	require.NoError(t, err)
	assert.NotContains(t, other, "video_schedule")
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	schedule, ok := adminInfo["video_schedule"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "rejected", schedule["outcome"])
	attempts, ok := schedule["attempts"].([]any)
	require.True(t, ok)
	require.Len(t, attempts, 2, "every selection, across auto groups")
	assert.Equal(t, "vip", attempts[0].(map[string]any)["group"])
	assert.Equal(t, float64(2), attempts[1].(map[string]any)["selection_seq"])
	assert.Equal(t, float64(1), attempts[1].(map[string]any)["attempt_seq"])

	userLogs, total, err := model.GetUserLogs(7, model.LogTypeError, 0, 0, "", "", 0, 10, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	tokenLogs, err := model.GetLogByTokenId(11)
	require.NoError(t, err)
	require.Len(t, tokenLogs, 1)
	for _, view := range []*model.Log{userLogs[0], tokenLogs[0]} {
		assert.NotContains(t, view.Other, "admin_info")
		assert.NotContains(t, view.Other, "video_schedule")
		assert.NotContains(t, view.Other, "secret-upstream")
	}

	// A panic after selection: the lease is released, one sanitized summary is
	// written although the status is still 200, and the original value reaches
	// the outer recovery.
	const probedChannel = 88001
	sentinel := errors.New("relay exploded with a private prompt")
	require.PanicsWithValue(t, sentinel, func() {
		serve("/mj/notify", func(c *gin.Context) {
			acquired, err := service.AcquireVideoProbeSlot(c, probedChannel, 0, time.Hour)
			require.NoError(t, err)
			require.True(t, acquired)
		}, func(c *gin.Context) { panic(sentinel) })
	})
	rival, _ := gin.CreateTestContext(httptest.NewRecorder())
	acquired, err := service.AcquireVideoProbeSlot(rival, probedChannel, 0, time.Hour)
	require.NoError(t, err)
	assert.True(t, acquired, "the panicking request released its probe slot")
	service.ReleaseUnpersistedVideoProbeLease(rival)

	rows = nil
	require.NoError(t, database.Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.NotContains(t, rows[1].Content, "private prompt")
	other, err = common.StrToMap(rows[1].Other)
	require.NoError(t, err)
	assert.NotContains(t, other, "status_code")
	assert.Equal(t, "internal_failure", other["admin_info"].(map[string]any)["video_schedule"].(map[string]any)["outcome"])
}
