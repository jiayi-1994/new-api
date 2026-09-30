package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// useVideoHealthBackend points channel health at a fresh memory store or a
// fresh miniredis, and turns scheduling on in shadow mode.
func useVideoHealthBackend(t *testing.T, backend string) {
	t.Helper()
	previousRedis, previousRDB := common.RedisEnabled, common.RDB
	previousSetting := *operation_setting.GetVideoSchedulingSetting()
	previousMemory := memoryVideoHealth
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousRedis, previousRDB
		*operation_setting.GetVideoSchedulingSetting() = previousSetting
		memoryVideoHealth = previousMemory
		videoCalibrationDrift.Clear()
	})
	memoryVideoHealth = &memoryVideoHealthStore{hashes: map[string]map[string]int64{}, gauges: map[string]int64{}, slots: map[string]memoryVideoSlot{}}
	common.RedisEnabled, common.RDB = false, nil
	if backend == "redis" {
		server := miniredis.RunT(t)
		common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
		common.RedisEnabled = true
	}
	operation_setting.GetVideoSchedulingSetting().Mode = operation_setting.VideoSchedulingModeShadow
	operation_setting.GetVideoSchedulingSetting().WindowSeconds = 1800
	videoCalibrationDrift.Clear()
}

func scheduledTestChannel(id int, group string) *model.Channel {
	return &model.Channel{Id: id, OtherSettings: fmt.Sprintf(`{"video_scheduling":{"capacity_group":%q,"models":{"m":{"mode":"per_video","prices":{"*":1}}}}}`, group)}
}

func healthTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	return c
}

func TestVideoChannelHealthSeparatesSubmitAndGeneration(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			useVideoHealthBackend(t, backend)
			channel := scheduledTestChannel(7, "acct")
			for i := range 4 {
				c := healthTestContext()
				ObserveVideoSubmit(c, channel, "m", nil)
				task := &model.Task{ChannelId: 7, Status: model.TaskStatusInProgress}
				task.PrivateData.SchedulingSummary = NewVideoSchedulingSummary(c, channel, "m")
				require.NotNil(t, task.PrivateData.SchedulingSummary)
				VideoTaskPersisted(c, task)
				task.Status, task.FailReason = model.TaskStatusFailure, fmt.Sprintf("upstream error %d", i)
				ObserveVideoTerminal(task, i == 0)
			}

			health, err := GetVideoChannelHealth(7, "m", 1)
			require.NoError(t, err)
			assert.Equal(t, 1.0, health.Submit.Rate, "every submission was accepted")
			assert.Equal(t, 4, health.Submit.Samples)
			assert.Equal(t, 0.0, health.Gen.Rate, "every generation failed")
			assert.Equal(t, 4, health.Gen.Samples)
			assert.Zero(t, health.InFlight, "each terminal released its count")
			group, err := GetVideoGroupInFlight("acct")
			require.NoError(t, err)
			assert.Zero(t, group)

			// Another model on the same channel falls back to the channel-wide
			// window once its own window is under-sampled.
			other, err := GetVideoChannelHealth(7, "other", 2)
			require.NoError(t, err)
			assert.Equal(t, 4, other.Gen.Samples)
		})
	}
}

func TestVideoTaskPersistedCountsOnlyPolledTasks(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			useVideoHealthBackend(t, backend)
			channel := scheduledTestChannel(3, "acct")
			c := healthTestContext()

			immediate := &model.Task{ChannelId: 3, Status: model.TaskStatusSuccess}
			immediate.PrivateData.SchedulingSummary = NewVideoSchedulingSummary(c, channel, "m")
			VideoTaskPersisted(c, immediate)
			health, err := GetVideoChannelHealth(3, "m", 0)
			require.NoError(t, err)
			assert.Zero(t, health.InFlight, "an immediate result is never in flight")
			assert.Equal(t, 1, health.Gen.Samples)

			polled := &model.Task{ChannelId: 3, Status: model.TaskStatusSubmitted}
			polled.PrivateData.SchedulingSummary = NewVideoSchedulingSummary(c, channel, "m")
			VideoTaskPersisted(c, polled)
			health, err = GetVideoChannelHealth(3, "m", 0)
			require.NoError(t, err)
			assert.Equal(t, 1, health.InFlight)

			// A late duplicate release floors at zero instead of going negative.
			ObserveVideoTerminal(polled, true)
			ObserveVideoTerminal(polled, true)
			health, err = GetVideoChannelHealth(3, "m", 0)
			require.NoError(t, err)
			assert.Zero(t, health.InFlight)

			untracked := &model.Channel{Id: 4}
			assert.Nil(t, NewVideoSchedulingSummary(c, untracked, "m"), "channels without a cost table are not tracked")
			operation_setting.GetVideoSchedulingSetting().Mode = operation_setting.VideoSchedulingModeOff
			assert.Nil(t, NewVideoSchedulingSummary(c, channel, "m"), "nothing is tracked while scheduling is off")
		})
	}
}

func TestVideoHealthBucketsStayBoundedAndExpire(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			useVideoHealthBackend(t, backend)
			store := videoHealthStore()
			const buckets = 3
			require.NoError(t, store.addSample("k", videoGenOK, 100, buckets))
			require.NoError(t, store.addSample("k", videoGenOK, 101, buckets))
			require.NoError(t, store.addSample("k", videoGenFail, 103, buckets)) // reuses minute 100's bucket
			sums, err := store.window("k", 103, buckets)
			require.NoError(t, err)
			assert.Equal(t, int64(1), sums[videoGenOK], "minute 100 was overwritten, 101 is still in the window")
			assert.Equal(t, int64(1), sums[videoGenFail])
			sums, err = store.window("k", 110, buckets)
			require.NoError(t, err)
			assert.Empty(t, sums[videoGenOK]+sums[videoGenFail], "everything aged out")

			if backend == "redis" {
				fields, err := common.RDB.HLen(t.Context(), "k").Result()
				require.NoError(t, err)
				assert.LessOrEqual(t, fields, int64(5*buckets))
			}
		})
	}
}

func TestVideoProbeLeaseLifecycle(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			useVideoHealthBackend(t, backend)
			operation_setting.GetVideoSchedulingSetting().ProbeCooldownSec = 300
			channel := scheduledTestChannel(9, "")

			// A rejected probe submission releases its slot at once and extends
			// the cooldown.
			c := healthTestContext()
			ok, err := AcquireVideoProbeSlot(c, 9, 0, time.Hour)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = AcquireVideoProbeSlot(healthTestContext(), 9, 0, time.Hour)
			require.NoError(t, err)
			assert.False(t, ok, "the slot is taken")
			ObserveVideoSubmit(c, channel, "m", &taskdto.TaskError{StatusCode: http.StatusBadGateway})
			health, err := GetVideoChannelHealth(9, "m", 0)
			require.NoError(t, err)
			assert.Equal(t, 1, health.Probe.ConsecutiveFails)
			assert.Positive(t, health.Probe.LastProbeAt)
			assert.Equal(t, 10*time.Minute, VideoProbeCooldown(health.Probe))

			// An accepted probe hands the slot to its task, which releases it at
			// the terminal state and clears the failure streak.
			c = healthTestContext()
			ok, err = AcquireVideoProbeSlot(c, 9, 0, time.Hour)
			require.NoError(t, err)
			require.True(t, ok)
			ObserveVideoSubmit(c, channel, "m", nil)
			task := &model.Task{ChannelId: 9, Status: model.TaskStatusSubmitted}
			task.PrivateData.SchedulingSummary = NewVideoSchedulingSummary(c, channel, "m")
			require.NotNil(t, task.PrivateData.SchedulingSummary.ProbeSlot)
			VideoTaskPersisted(c, task)
			ReleaseUnpersistedVideoProbeLease(c) // request end must not free the task's slot
			ok, err = AcquireVideoProbeSlot(healthTestContext(), 9, 0, time.Hour)
			require.NoError(t, err)
			assert.False(t, ok, "the persisted task still owns the slot")
			task.Status = model.TaskStatusSuccess
			ObserveVideoTerminal(task, false)
			health, err = GetVideoChannelHealth(9, "m", 0)
			require.NoError(t, err)
			assert.Zero(t, health.Probe.ConsecutiveFails)

			// A request that never persisted a task frees its slot on exit, and a
			// stale token cannot delete a slot re-acquired by someone else.
			c = healthTestContext()
			ok, err = AcquireVideoProbeSlot(c, 9, 0, time.Hour)
			require.NoError(t, err)
			require.True(t, ok)
			stale, _ := peekVideoProbeLease(c)
			ReleaseUnpersistedVideoProbeLease(c)
			ok, err = AcquireVideoProbeSlot(healthTestContext(), 9, 0, time.Hour)
			require.NoError(t, err)
			require.True(t, ok)
			releaseVideoProbeSlot(stale.Key, stale.Token)
			ok, err = AcquireVideoProbeSlot(healthTestContext(), 9, 0, time.Hour)
			require.NoError(t, err)
			assert.False(t, ok, "compare-and-delete kept the new holder's slot")
		})
	}
}

func TestVideoSubmitAndTerminalAttribution(t *testing.T) {
	unknown := &taskdto.TaskError{StatusCode: http.StatusInternalServerError, Error: fmt.Errorf("x: %w: %w", relaycommon.ErrTaskSubmitOutcomeUnknown, io.ErrUnexpectedEOF)}
	for name, tc := range map[string]struct {
		err  *taskdto.TaskError
		want VideoOutcome
	}{
		"accepted":          {nil, VideoOutcomeSuccess},
		"upstream 502":      {&taskdto.TaskError{StatusCode: http.StatusBadGateway}, VideoOutcomeFail},
		"network as 500":    {&taskdto.TaskError{StatusCode: http.StatusInternalServerError, Error: errors.New("dial tcp")}, VideoOutcomeFail},
		"rate limited":      {&taskdto.TaskError{StatusCode: http.StatusTooManyRequests}, VideoOutcomeFail},
		"bad credentials":   {&taskdto.TaskError{StatusCode: http.StatusUnauthorized}, VideoOutcomeFail},
		"client 400":        {&taskdto.TaskError{StatusCode: http.StatusBadRequest}, VideoOutcomeIgnored},
		"client 408":        {&taskdto.TaskError{StatusCode: http.StatusRequestTimeout}, VideoOutcomeIgnored},
		"local rejection":   {&taskdto.TaskError{StatusCode: http.StatusForbidden, LocalError: true}, VideoOutcomeIgnored},
		"outcome unknown":   {unknown, VideoOutcomeIgnored},
		"local 5xx ignored": {&taskdto.TaskError{StatusCode: http.StatusInternalServerError, LocalError: true}, VideoOutcomeIgnored},
	} {
		assert.Equal(t, tc.want, videoSubmitOutcome(tc.err), name)
	}

	previous := GetTaskAdaptorFunc
	t.Cleanup(func() { GetTaskAdaptorFunc = previous })
	GetTaskAdaptorFunc = nil
	assert.Equal(t, VideoOutcomeSuccess, videoTerminalOutcome(&model.Task{Status: model.TaskStatusSuccess}, false))
	assert.Equal(t, VideoOutcomeFail, videoTerminalOutcome(&model.Task{Status: model.TaskStatusFailure, FailReason: "content moderation"}, false), "without a classifier every failure is the upstream's")
	assert.Equal(t, VideoOutcomeFail, videoTerminalOutcome(&model.Task{Status: model.TaskStatusFailure}, true))
}

func TestCalibrateVideoInFlight(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			useVideoHealthBackend(t, backend)
			previousDB := model.DB
			t.Cleanup(func() { model.DB = previousDB })
			database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := database.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Task{}))
			model.DB = database
			for _, channel := range []*model.Channel{scheduledTestChannel(1, "acct"), scheduledTestChannel(2, "acct")} {
				channel.Name, channel.Key = fmt.Sprintf("c%d", channel.Id), "k"
				require.NoError(t, database.Create(channel).Error)
			}
			for i, status := range []model.TaskStatus{model.TaskStatusSubmitted, model.TaskStatusInProgress, model.TaskStatusSuccess} {
				require.NoError(t, database.Create(&model.Task{TaskID: fmt.Sprintf("t%d", i), ChannelId: 2, Status: status}).Error)
			}
			store := videoHealthStore()

			// Channel 1: a quiet zombie count (capacity 1: redis 1, db 0) is
			// within the drift threshold but resets after two identical rounds.
			// Channel 2: far off from the database resets at once.
			require.NoError(t, store.set(videoInFlightKey(1), 1))
			require.NoError(t, store.set(videoInFlightKey(2), 9))
			calibrateVideoInFlight()
			values, err := store.get([]string{videoInFlightKey(1), videoInFlightKey(2), videoGroupInFlightKey("acct")})
			require.NoError(t, err)
			assert.Equal(t, []int64{1, 2, 0}, values, "drifts within max(2, 10%) wait for a second round")
			assert.True(t, VideoHealthReady())

			require.NoError(t, store.release(videoSchedCalibrationKey, "")) // no-op: the lock is held by its own token
			if backend == "redis" {
				require.NoError(t, common.RDB.Del(t.Context(), videoSchedCalibrationKey).Err())
			} else {
				memoryVideoHealth.slots = map[string]memoryVideoSlot{}
			}
			calibrateVideoInFlight()
			values, err = store.get([]string{videoInFlightKey(1), videoGroupInFlightKey("acct")})
			require.NoError(t, err)
			assert.Equal(t, []int64{0, 2}, values, "the same drift twice resets the zombie count and the group")

			// A failed count keeps the gauges instead of zeroing them.
			require.NoError(t, store.set(videoInFlightKey(2), 5))
			require.NoError(t, database.Migrator().DropTable(&model.Task{}))
			if backend == "redis" {
				require.NoError(t, common.RDB.Del(t.Context(), videoSchedCalibrationKey).Err())
			} else {
				memoryVideoHealth.slots = map[string]memoryVideoSlot{}
			}
			calibrateVideoInFlight()
			values, err = store.get([]string{videoInFlightKey(2)})
			require.NoError(t, err)
			assert.Equal(t, []int64{5}, values)
		})
	}
}
