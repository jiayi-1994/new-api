package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/videosched"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Measures the synchronous journal/observer path and periodic aggregation on
// real database engines. Network generation time is deliberately excluded.
func BenchmarkVideoReliability(b *testing.B) {
	gin.SetMode(gin.TestMode)
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		b.Run(dialect, func(b *testing.B) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(b.TempDir(), "health.db"))
			case "mysql":
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					b.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case "postgres":
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					b.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: "vs_health_bench_"}})
			require.NoError(b, err)
			sqlDB, err := db.DB()
			require.NoError(b, err)
			oldDB, oldRedis, oldKind := model.DB, common.RedisEnabled, common.MainDatabaseType()
			model.DB, common.RedisEnabled = db, false
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			b.Cleanup(func() {
				model.DB, common.RedisEnabled = oldDB, oldRedis
				common.SetMainDatabaseType(oldKind)
				videoReliabilityCache.Clear()
				require.NoError(b, sqlDB.Close())
			})
			models := []any{&model.VideoHealthRegistration{}, &model.VideoHealthState{}, &model.VideoHealthAttempt{}, &model.VideoHealthRequest{}, &model.Channel{}}
			require.NoError(b, db.Migrator().DropTable(models...))
			b.Cleanup(func() { require.NoError(b, db.Migrator().DropTable(models...)) })
			require.NoError(b, db.AutoMigrate(models...))
			now := time.Now().Unix()
			certificate := videosched.ReliabilityEvidence{Version: 1, Source: "window", WindowSeconds: 1800, BatchStart: now - 7200, BatchEnd: now - 5400, AsOf: now - 3600, ValidatedAt: now - 3600, ExpiresAt: now + 86400, Submitted: 100, Accepted: 100, Succeeded: 100}
			data, err := common.Marshal(certificate)
			require.NoError(b, err)
			channel := &model.Channel{Id: 1, Models: "video", Key: "fixture"}
			require.NoError(b, db.Create(channel).Error)
			require.NoError(b, model.EnsureVideoHealthState(b.Context(), 1, "video", channel.VideoHealthIdentity()))
			require.NoError(b, db.Model(&model.VideoHealthState{}).Where("channel_id = ?", 1).Updates(map[string]any{"state": videosched.HealthNormal, "qualification_json": string(data)}).Error)
			setting := &operation_setting.VideoSchedulingSetting{Mode: "on", SelectionPolicy: videosched.PolicyStabilityCostV2, WindowSeconds: 1800, MinSamples: 20, MinGenRate: .8, MinOverallRate: .6, QualificationTTLSeconds: 86400, ValidationPeriodSeconds: 604800}
			b.Run("journal_submit_terminal", func(b *testing.B) {
				b.ReportAllocs()
				latencies := make([]int64, 0)
				admissionLatencies := make([]int64, 0)
				failures := VideoReliabilityCollectionFailures()
				sequence := 0
				for b.Loop() {
					started := time.Now()
					sequence++
					c := healthTestContext()
					c.Set(common.RequestIdKey, fmt.Sprintf("bench-%d", sequence))
					common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, VideoSchedDecision{Takeover: true})
					common.SetContextKey(c, constant.ContextKeyVideoSchedSetting, setting)
					BindVideoHealthChannel(c, channel, "video")
					RequestPolicy(c).BeginAttempt(channel, "default")
					c.Set(videoHealthAdmissionKey, videoHealthAdmission{ChannelID: 1, Version: 1, Flow: "normal", Model: "video", Identity: channel.VideoHealthIdentity()})
					require.NoError(b, BeginVideoHealthTransmission(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}, OriginModelName: "video"}))
					admissionLatencies = append(admissionLatencies, time.Since(started).Nanoseconds())
					ObserveVideoReliabilitySubmit(c, nil, false)
					task := &model.Task{ID: int64(sequence), Status: model.TaskStatusSuccess}
					task.PrivateData.VideoHealth = VideoHealthReference(c)
					observeVideoReliabilityTerminal(task, VideoOutcomeSuccess, "success")
					stopVideoHealthSubmissionOwner(c)
					latencies = append(latencies, time.Since(started).Nanoseconds())
				}
				require.Equal(b, failures, VideoReliabilityCollectionFailures())
				slices.Sort(latencies)
				slices.Sort(admissionLatencies)
				b.ReportMetric(float64(latencies[(len(latencies)-1)*95/100])/1e6, "p95_ms/op")
				b.ReportMetric(float64(admissionLatencies[(len(admissionLatencies)-1)*95/100])/1e6, "admission_p95_ms/op")
			})
			require.NoError(b, model.EnsureVideoHealthState(b.Context(), 2, "video"))
			p := videoReliabilityPolicy(setting)
			_, err = model.RefreshVideoHealthState(b.Context(), 2, "video", p, now-7200)
			require.NoError(b, err)
			facts := make([]model.VideoHealthAttempt, 1000)
			for i := range facts {
				at := now - 5400 + int64(i)
				facts[i] = model.VideoHealthAttempt{RequestID: fmt.Sprintf("aggregate-%d", i), AttemptSeq: 1, ChannelID: 2, ModelName: "video", StartedAt: at, BatchStart: at / 1800 * 1800, WindowSeconds: 1800, StateVersion: 1, ValidationRound: 1, SubmitOutcome: "accepted", FinalOutcome: "success"}
			}
			require.NoError(b, db.CreateInBatches(&facts, 100).Error)
			b.Run("aggregate_1000_facts_and_publish", func(b *testing.B) {
				b.ReportAllocs()
				latencies := make([]int64, 0)
				for b.Loop() {
					started := time.Now()
					view, err := model.RefreshVideoHealthState(b.Context(), 2, "video", p, now)
					require.NoError(b, err)
					require.NoError(b, publishVideoReliability(2, view))
					latencies = append(latencies, time.Since(started).Nanoseconds())
				}
				slices.Sort(latencies)
				b.ReportMetric(float64(latencies[(len(latencies)-1)*95/100])/1e6, "p95_ms/op")
			})
			b.Run("cached_state_read", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					require.NotNil(b, GetVideoReliability(2, "video"))
				}
			})
		})
	}
}

func TestVideoReliabilityTransportAndDurableTaskLink(t *testing.T) {
	useVideoHealthBackend(t, "memory")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous; videoReliabilityCache.Clear(); videoReliabilityReconcileCursor.Store(0) })
	require.NoError(t, db.AutoMigrate(&model.VideoHealthRegistration{}, &model.VideoHealthState{}, &model.VideoHealthAttempt{}, &model.VideoHealthRequest{}, &model.Task{}, &model.Channel{}))
	s := operation_setting.GetVideoSchedulingSetting()
	s.Mode = "on"
	s.SelectionPolicy = videosched.PolicyStabilityCostV2
	s.MinGenRate = .8
	s.MinOverallRate = .6
	s.MinSamples = 20
	s.QualificationTTLSeconds = 86400
	s.ValidationPeriodSeconds = 604800
	s.AuditEnabled = false
	ctx := context.Background()
	now := time.Now().Unix()
	ch := scheduledTestChannel(7, "")
	require.NoError(t, db.Create(ch).Error)
	require.NoError(t, model.EnsureVideoHealthState(ctx, 7, "video", ch.VideoHealthIdentity()))
	_, err = model.RefreshVideoHealthState(ctx, 7, "video", videoReliabilityPolicy(s), now-1)
	require.NoError(t, err)
	c := healthTestContext()
	c.Set(common.RequestIdKey, "health-without-audit")
	c.Set("resolved_task_model", "video")
	common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, VideoSchedDecision{Takeover: true})
	common.SetContextKey(c, constant.ContextKeyVideoSchedSetting, s)
	BindVideoHealthChannel(c, ch, "video")
	RequestPolicy(c).BeginAttempt(ch, "default")
	c.Set(videoHealthAdmissionKey, videoHealthAdmission{ChannelID: 7, Version: 1, Flow: "explore", Model: "video", Identity: ch.VideoHealthIdentity()})
	claimed, err := acquireVideoValidationSlot(c, 7, 2, time.Hour)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.Nil(t, VideoHealthReference(c), "selection and slot acquisition are not actual submit samples")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7}, OriginModelName: "video"}
	require.NoError(t, BeginVideoHealthTransmission(c, info))
	t.Cleanup(func() { stopVideoHealthSubmissionOwner(c) })
	// A long response has not reached the durable task barrier yet, but its
	// live submission owner prevents the reconciler from inventing a crash.
	require.NoError(t, db.Model(&model.VideoHealthAttempt{}).Where("request_id = ?", "health-without-audit").Update("started_at", now-300).Error)
	require.NoError(t, reconcileVideoReliability(ctx))
	live, err := model.ListVideoHealthAttempts(ctx, "health-without-audit")
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Empty(t, live[0].FinalOutcome)
	assert.False(t, live[0].Missing)
	videoReliabilityReconcileCursor.Store(0)
	ObserveVideoReliabilitySubmit(c, nil, false)
	ref := VideoHealthReference(c)
	require.NotNil(t, ref)
	task := model.Task{TaskID: "durable-health", Status: model.TaskStatusSubmitted, ChannelId: 7, VideoHealthAttemptID: &ref.AttemptID}
	task.PrivateData.VideoHealth = ref
	require.NoError(t, db.Create(&task).Error)
	// Cache expiry is not a task deadline. Restore the durable owner even if
	// the original slot TTL elapsed while the accepted task was still pending.
	require.NoError(t, db.Model(&model.VideoHealthAttempt{}).Where("id = ?", ref.AttemptID).Updates(map[string]any{"slot_expires": now - 1, "submit_lease_expires": now - 1}).Error)
	memoryVideoHealth.mu.Lock()
	memoryVideoHealth.slots = map[string]memoryVideoSlot{}
	memoryVideoHealth.mu.Unlock()
	// Simulate a lost post-insert linkage callback. The independent scalar
	// link repairs it without a SchedulingSummary or enabled audit writer.
	require.NoError(t, reconcileVideoReliability(ctx))
	held, err := videoHealthStore().held(videoValidationKeys(7))
	require.NoError(t, err)
	assert.Equal(t, 1, held, "unfinished task restores its expired cache lease")
	attempts, err := model.ListVideoHealthAttempts(ctx, ref.RequestID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.NotNil(t, attempts[0].TaskPK)
	assert.Equal(t, task.ID, *attempts[0].TaskPK)
	s.Mode = "off"
	task.Status = model.TaskStatusSuccess
	require.NoError(t, db.Model(&task).Update("status", task.Status).Error)
	ObserveVideoTerminal(&task, false)
	ObserveVideoTerminal(&task, false)
	attempts, err = model.ListVideoHealthAttempts(ctx, ref.RequestID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, "success", attempts[0].FinalOutcome)
	count, err := videoHealthStore().held(videoValidationKeys(7))
	require.NoError(t, err)
	assert.Zero(t, count)
	// A persisted state deletion is not a new channel or a reason to send.
	require.NoError(t, db.Where("channel_id = ?", 7).Delete(&model.VideoHealthState{}).Error)
	RequestPolicy(c).BeginAttempt(ch, "default")
	require.ErrorIs(t, BeginVideoHealthTransmission(c, info), ErrVideoHealthAdmission)
	attempts, err = model.ListVideoHealthAttempts(ctx, ref.RequestID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
}

func useVideoReliabilityDatabase(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "sqlite":
		driver = sqlite.Open(filepath.Join(t.TempDir(), "health.db"))
	case "mysql":
		if os.Getenv("TEST_MYSQL_DSN") == "" {
			t.Skip("TEST_MYSQL_DSN is not configured")
		}
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case "postgres":
		if os.Getenv("TEST_POSTGRES_DSN") == "" {
			t.Skip("TEST_POSTGRES_DSN is not configured")
		}
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: "vs_reliability_regression_"}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB, previousKind := model.DB, common.MainDatabaseType()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseType(dialect))
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousKind)
		videoReliabilityCache.Clear()
		videoReliabilityReconcileCursor.Store(0)
		require.NoError(t, sqlDB.Close())
	})
	tables := []any{&model.VideoHealthRegistration{}, &model.VideoHealthState{}, &model.VideoHealthAttempt{}, &model.VideoHealthRequest{}, &model.Task{}, &model.Channel{}}
	require.NoError(t, db.Migrator().DropTable(tables...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })
	require.NoError(t, db.AutoMigrate(tables...))
	videoReliabilityCache.Clear()
	videoReliabilityReconcileCursor.Store(0)
	return db
}

func TestVideoReliabilityAdmissionConflictRefreshesCache(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, backend := range []string{"memory", "redis"} {
			t.Run(dialect+"/"+backend, func(t *testing.T) {
				useVideoHealthBackend(t, backend)
				db := useVideoReliabilityDatabase(t, dialect)
				channel := scheduledTestChannel(7, "")
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, model.EnsureVideoHealthState(t.Context(), 7, "video", channel.VideoHealthIdentity()))
				now := time.Now().Unix()
				certificate, err := common.Marshal(videosched.ReliabilityEvidence{Version: 1, Source: "window", WindowSeconds: 1800, BatchStart: now - 7200, BatchEnd: now - 5400, AsOf: now - 3600, ValidatedAt: now - 3600, ExpiresAt: now + 86400, Submitted: 100, Accepted: 100, Succeeded: 100})
				require.NoError(t, err)
				require.NoError(t, db.Model(&model.VideoHealthState{}).Where("channel_id = ?", 7).Updates(map[string]any{"state": videosched.HealthNormal, "qualification_json": string(certificate)}).Error)
				publishPersistedVideoReliability(t.Context(), 7, "video")
				s := operation_setting.GetVideoSchedulingSetting()
				s.Mode, s.SelectionPolicy = "on", videosched.PolicyStabilityCostV2
				c := healthTestContext()
				c.Set(common.RequestIdKey, "admission-conflict")
				common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, VideoSchedDecision{Takeover: true})
				common.SetContextKey(c, constant.ContextKeyVideoSchedSetting, s)
				BindVideoHealthChannel(c, channel, "video")
				RequestPolicy(c).BeginAttempt(channel, "default")
				c.Set(videoHealthAdmissionKey, videoHealthAdmission{ChannelID: 7, Version: 1, Flow: "normal", Model: "video", Identity: channel.VideoHealthIdentity()})
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7}, OriginModelName: "video"}
				// A cohort refresh advances the version after selection, without
				// making the channel unhealthy or losing its certificate.
				require.NoError(t, db.Model(&model.VideoHealthState{}).Where("channel_id = ?", 7).Update("version", 2).Error)
				failures := VideoReliabilityCollectionFailures()
				require.ErrorIs(t, BeginVideoHealthTransmission(c, info), model.ErrVideoHealthStateChanged)
				assert.Equal(t, failures, VideoReliabilityCollectionFailures(), "an expected conflict is not a persistence outage")
				view := GetVideoReliability(7, "video")
				require.NotNil(t, view, "bounded reselection needs the current snapshot")
				assert.EqualValues(t, 2, view.StateVersion)
				c.Set(videoHealthAdmissionKey, videoHealthAdmission{ChannelID: 7, Version: view.StateVersion, Flow: "normal", Model: "video", Identity: channel.VideoHealthIdentity()})
				require.NoError(t, BeginVideoHealthTransmission(c, info))
				stopVideoHealthSubmissionOwner(c)
				attempts, err := model.ListVideoHealthAttempts(t.Context(), "admission-conflict")
				require.NoError(t, err)
				require.Len(t, attempts, 1, "only the accepted admission is journaled")
				// Refresh must also preserve a concurrent block, never resurrect
				// the old normal snapshot or admit transport against that block.
				require.NoError(t, db.Model(&model.VideoHealthState{}).Where("channel_id = ?", 7).Updates(map[string]any{"version": 3, "state": videosched.HealthBlocked}).Error)
				RequestPolicy(c).BeginAttempt(channel, "default")
				require.ErrorIs(t, BeginVideoHealthTransmission(c, info), model.ErrVideoHealthStateChanged)
				view = GetVideoReliability(7, "video")
				require.NotNil(t, view)
				assert.Equal(t, videosched.HealthBlocked, view.State)
				assert.EqualValues(t, 3, view.StateVersion)
				// A real database failure still makes the cached state unavailable.
				require.NoError(t, db.Migrator().DropTable(&model.VideoHealthState{}))
				require.ErrorIs(t, BeginVideoHealthTransmission(c, info), ErrVideoHealthAdmission)
				assert.Nil(t, GetVideoReliability(7, "video"))
				assert.Greater(t, VideoReliabilityCollectionFailures(), failures)
			})
		}
	}
}

func TestVideoReliabilityDoesNotRestoreTerminalSlot(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, backend := range []string{"memory", "redis"} {
			for _, path := range []string{"reconcile", "refresh"} {
				for _, replacement := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/replacement=%t", dialect, backend, path, replacement), func(t *testing.T) {
						useVideoHealthBackend(t, backend)
						db := useVideoReliabilityDatabase(t, dialect)
						channel := scheduledTestChannel(7, "")
						channel.Models, channel.OtherSettings = "video", `{"video_scheduling":{"models":{"video":{"mode":"per_video","prices":{"*":1}}}}}`
						require.NoError(t, db.Create(channel).Error)
						require.NoError(t, model.EnsureVideoHealthState(t.Context(), 7, "video", channel.VideoHealthIdentity()))
						now := time.Now().Unix()
						attempt := model.VideoHealthAttempt{RequestID: "terminal-slot", AttemptSeq: 1, ChannelID: 7, ModelName: "video", ConfigIdentity: channel.VideoHealthIdentity(), StartedAt: now, WindowSeconds: 1800, StateVersion: 1, Flow: "explore", SubmitOutcome: "accepted", SlotKey: videoValidationKeys(7)[0], SlotToken: "old-owner", SlotExpires: now + 300}
						require.NoError(t, db.Create(&attempt).Error)
						task := model.Task{TaskID: "terminal-slot", ChannelId: 7, Status: model.TaskStatusSubmitted, VideoHealthAttemptID: &attempt.ID}
						task.PrivateData.VideoHealth = &model.TaskVideoHealthReference{AttemptID: attempt.ID, RequestID: attempt.RequestID, AttemptSeq: 1, ChannelID: 7, Model: "video", Slot: &model.TaskProbeSlot{Key: attempt.SlotKey, Token: attempt.SlotToken}}
						require.NoError(t, db.Create(&task).Error)
						_, err := videoHealthStore().acquire(attempt.SlotKey, attempt.SlotToken, 5*time.Minute)
						require.NoError(t, err)
						// An unfinished durable task must still restore its slot after
						// cache loss, even when both original lease deadlines passed.
						require.NoError(t, db.Model(&attempt).Updates(map[string]any{"slot_expires": now - 1, "submit_lease_expires": now - 1}).Error)
						require.NoError(t, videoHealthStore().release(attempt.SlotKey, attempt.SlotToken))
						require.NoError(t, reconcileVideoReliability(t.Context()))
						held, err := videoHealthStore().held(videoValidationKeys(7))
						require.NoError(t, err)
						require.Equal(t, 1, held)
						videoReliabilityReconcileCursor.Store(0)
						completed := false
						// Finish after the reconciler has read its snapshot, before it
						// restores leases. No sleeps or probabilistic race are needed.
						require.NoError(t, db.Callback().Query().After("gorm:after_query").Register("test:terminal_between_snapshot_and_restore", func(tx *gorm.DB) {
							matches := path == "reconcile" && tx.Statement.Table == "vs_reliability_regression_tasks"
							if path == "refresh" {
								matches = slices.Contains(tx.Statement.Selects, "slot_key")
							}
							if completed || !matches {
								return
							}
							completed = true
							task.Status = model.TaskStatusSuccess
							require.NoError(t, db.Model(&task).Update("status", task.Status).Error)
							observeVideoReliabilityTerminal(&task, VideoOutcomeSuccess, "success")
							if replacement {
								claimed, err := videoHealthStore().acquire(attempt.SlotKey, "new-owner", time.Minute)
								require.NoError(t, err)
								require.True(t, claimed)
							}
						}))
						if path == "reconcile" {
							require.NoError(t, reconcileVideoReliability(t.Context()))
						} else {
							RefreshVideoReliability(t.Context())
						}
						require.True(t, completed, "the terminal callback must win the intended race")
						attempts, err := model.ListVideoHealthAttempts(t.Context(), attempt.RequestID)
						require.NoError(t, err)
						require.Len(t, attempts, 1)
						assert.Equal(t, "success", attempts[0].FinalOutcome)
						held, err = videoHealthStore().held(videoValidationKeys(7))
						require.NoError(t, err)
						if replacement {
							assert.Equal(t, 1, held, "cleanup cannot remove a newer owner's reservation")
							require.NoError(t, videoHealthStore().release(attempt.SlotKey, "new-owner"))
						} else {
							assert.Zero(t, held, "terminal completion must leave no restored lease")
						}
						held, err = videoHealthStore().held(videoValidationKeys(7))
						require.NoError(t, err)
						assert.Zero(t, held)
					})
				}
			}
		}
	}
}

func TestVideoValidationAtomicSlotsAndOwnerRelease(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			useVideoHealthBackend(t, backend)
			contexts := []*gin.Context{healthTestContext(), healthTestContext(), healthTestContext()}
			results := make([]bool, 3)
			errs := make([]error, 3)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range contexts {
				wg.Go(func() { <-start; results[i], errs[i] = acquireVideoValidationSlot(contexts[i], 8, 2, time.Hour) })
			}
			close(start)
			wg.Wait()
			winners := 0
			for i, ok := range results {
				require.NoError(t, errs[i])
				if ok {
					winners++
				}
			}
			assert.Equal(t, 2, winners)
			// Switching from exploration cap 2 to recovery cap 1 cannot use a new
			// pool to escape the two still-running exploration tasks.
			ok, err := acquireVideoValidationSlot(healthTestContext(), 8, 1, time.Hour)
			require.NoError(t, err)
			assert.False(t, ok)
			var first *gin.Context
			for i, ok := range results {
				if ok {
					first = contexts[i]
					break
				}
			}
			old := first.MustGet(videoValidationLeaseKey).(*videoValidationLease)
			releaseVideoValidationLease(first)
			replacement := healthTestContext()
			ok, err = acquireVideoValidationSlot(replacement, 8, 2, time.Hour)
			require.NoError(t, err)
			require.True(t, ok)
			releaseVideoProbeSlot(old.Key, old.Token)
			count, err := videoHealthStore().held(videoValidationKeys(8))
			require.NoError(t, err)
			assert.Equal(t, 2, count)
			retainVideoValidationLease(replacement)
			releaseVideoValidationLease(replacement)
			count, err = videoHealthStore().held(videoValidationKeys(8))
			require.NoError(t, err)
			assert.Equal(t, 2, count, "unknown/accepted submit keeps its slot until reconciliation/terminal/TTL")
		})
	}
}

// useVideoHealthBackend points channel health at a fresh memory store or a
// fresh miniredis, and turns scheduling on in shadow mode.
func useVideoHealthBackend(t *testing.T, backend string) {
	t.Helper()
	previousRedis, previousRDB := common.RedisEnabled, common.RDB
	previousSetting := *operation_setting.GetVideoSchedulingSetting()
	previousMemory := memoryVideoHealth
	previousReady := videoHealthReady.Load()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousRedis, previousRDB
		*operation_setting.GetVideoSchedulingSetting() = previousSetting
		memoryVideoHealth = previousMemory
		videoCalibrationDrift.Clear()
		videoHealthReady.Store(previousReady)
	})
	videoHealthReady.Store(false)
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

func TestVideoTargetChannelsShareCapacityButKeepIndependentHealth(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			useVideoHealthBackend(t, backend)
			setting := operation_setting.GetVideoSchedulingSetting()
			setting.SelectionPolicy = videosched.PolicyWeightedV1
			setting.CapacityGroups = map[string]int{"shared-account": 2}
			setting.MinSamples = 1
			channels := make([]*model.Channel, 2)
			tasks := make([]*model.Task, 2)
			targets := []string{"videos-mini", "videos-standard"}
			for i, target := range targets {
				mapping := fmt.Sprintf(`{"videos-fast":%q}`, target)
				plugin := `{"task_plugin_key":"seedance-hjmie"}`
				mode, price := "per_video", 0.12
				if i == 1 {
					mode, price = "per_second", 0.01
				}
				channels[i] = &model.Channel{Id: 101 + i, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled,
					Key: "same-account-key", BaseURL: common.GetPointer("https://same-account.example"), Models: "videos-fast", Group: "default", ModelMapping: &mapping, Setting: &plugin,
					OtherSettings: fmt.Sprintf(`{"video_scheduling":{"capacity_group":"shared-account","models":{"videos-fast":{"mode":%q,"prices":{"720p":%g}}}}}`, mode, price)}
			}
			policy := videosched.Policy{MaxCostUSD: 100, Weights: videosched.DefaultWeights}
			for stage, wantGroup := range []int{0, 1, 2, 1, 0} {
				switch stage {
				case 1, 2:
					i := stage - 1
					c := healthTestContext()
					ObserveVideoSubmit(c, channels[i], "videos-fast", nil)
					tasks[i] = &model.Task{ChannelId: channels[i].Id, Status: model.TaskStatusSubmitted}
					tasks[i].PrivateData.SchedulingSummary = NewVideoSchedulingSummary(c, channels[i], "videos-fast")
					require.NotNil(t, tasks[i].PrivateData.SchedulingSummary)
					VideoTaskPersisted(c, tasks[i])
				case 3:
					tasks[0].Status = model.TaskStatusSuccess
					ObserveVideoTerminal(tasks[0], false)
				case 4:
					tasks[1].Status = model.TaskStatusFailure
					ObserveVideoTerminal(tasks[1], true)
				}
				group, err := GetVideoGroupInFlight("shared-account")
				require.NoError(t, err)
				assert.Equal(t, wantGroup, group, "stage %d", stage)
				c := videoSchedProtocolRequest(t, map[string]any{"prompt": "capacity", "duration": 15, "resolution": "720p"}, VideoSchedDecision{Takeover: true})
				SetVideoSalesFacts(c, VideoSalesFacts{Model: "videos-fast", Seconds: 15, Resolution: "720p", USDPerSecond: 0.02})
				candidates := make([]videosched.Candidate, len(channels))
				for i, channel := range channels {
					candidates[i], _ = assembleVideoCandidate(c, "default", "videos-fast", channel, false, setting)
					assert.Equal(t, targets[i], candidates[i].MappedModel)
					assert.Equal(t, wantGroup, candidates[i].GroupInFlight)
					assert.Equal(t, 2, candidates[i].GroupCapacity)
					quote := videosched.Quote(candidates[i].Cost, candidates[i].Spec)
					assert.Empty(t, quote.Reason)
					assert.InDelta(t, []float64{0.12, 0.15}[i], quote.TotalUSD, 1e-9)
				}
				board := videosched.Evaluate(candidates, policy)
				require.Len(t, board, 2)
				for _, score := range board {
					if wantGroup == 2 {
						assert.Equal(t, "at capacity", score.Reason, "both targets must reject the third sequential submission")
					} else {
						assert.Empty(t, score.Reason, "stage %d, channel %d", stage, score.Candidate.ID)
					}
				}
			}
			for i, channel := range channels {
				health, err := GetVideoChannelHealth(channel.Id, "videos-fast", 1)
				require.NoError(t, err)
				assert.Zero(t, health.InFlight)
				assert.Equal(t, videosched.HealthStat{Rate: 1, Samples: 1}, health.Submit)
				assert.Equal(t, videosched.HealthStat{Rate: float64(1 - i), Samples: 1}, health.Gen,
					"shared upstream credentials and capacity do not merge each target channel's health")
			}
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
			held, err := VideoProbeSlotsHeld(9, 2)
			require.NoError(t, err)
			assert.Equal(t, 1, held)
			// A request holds one lease at most: taking slot 1 frees slot 0.
			ok, err = AcquireVideoProbeSlot(c, 9, 1, time.Hour)
			require.NoError(t, err)
			require.True(t, ok)
			held, err = VideoProbeSlotsHeld(9, 1)
			require.NoError(t, err)
			assert.Zero(t, held)
			ObserveVideoSubmit(c, channel, "m", &taskdto.TaskError{StatusCode: http.StatusBadGateway})
			held, err = VideoProbeSlotsHeld(9, 2)
			require.NoError(t, err)
			assert.Zero(t, held, "a rejected probe submission frees its slot")
			health, err := GetVideoChannelHealth(9, "m", 0)
			require.NoError(t, err)
			assert.Equal(t, 1, health.Probe.ConsecutiveFails)
			assert.Positive(t, health.Probe.LastProbeAt)
			assert.Equal(t, 10*time.Minute, VideoProbeCooldown(300, health.Probe))

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
		"outcome unknown":   {unknown, VideoOutcomeFail},
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
			// Channel 2 has two active tracked tasks: one counted in "acct" and
			// one submitted before a regroup, still owned by group "old". A
			// finished task and an untracked one (no summary) count nowhere.
			for i, row := range []struct {
				status model.TaskStatus
				group  string
			}{{model.TaskStatusSubmitted, "acct"}, {model.TaskStatusInProgress, "old"}, {model.TaskStatusSuccess, "acct"}, {model.TaskStatusQueued, ""}} {
				task := &model.Task{TaskID: fmt.Sprintf("t%d", i), ChannelId: 2, Status: row.status}
				if row.group != "" {
					task.PrivateData.SchedulingSummary = &model.TaskSchedulingSummary{Model: "m", CapacityGroup: row.group}
				}
				require.NoError(t, database.Create(task).Error)
			}
			store := videoHealthStore()

			// Before any successful calibration, a lock held elsewhere is not
			// enough for readiness: the holder may still be counting or fail.
			require.NoError(t, store.set(videoGroupInFlightKey("old"), 4))
			ok, err := store.acquire(videoSchedCalibrationKey, "other-instance", time.Minute)
			require.NoError(t, err)
			require.True(t, ok)
			calibrateVideoInFlight()
			assert.False(t, VideoHealthReady())
			if backend == "redis" {
				require.NoError(t, common.RDB.Set(t.Context(), videoSchedCalibratedKey, 1, time.Minute).Err())
				calibrateVideoInFlight()
				assert.True(t, VideoHealthReady(), "another instance published a successful calibration")
				videoHealthReady.Store(false)
				require.NoError(t, common.RDB.Del(t.Context(), videoSchedCalibrationKey, videoSchedCalibratedKey).Err())
			} else {
				memoryVideoHealth.slots = map[string]memoryVideoSlot{}
			}

			// Channel 1: a quiet zombie count (capacity 1: redis 1, db 0) is
			// within the drift threshold but resets after two identical rounds.
			// Channel 2: far off from the database resets at once.
			require.NoError(t, store.set(videoInFlightKey(1), 1))
			require.NoError(t, store.set(videoInFlightKey(2), 9))
			calibrateVideoInFlight()
			values, err := store.get([]string{videoInFlightKey(1), videoInFlightKey(2), videoGroupInFlightKey("acct"), videoGroupInFlightKey("old")})
			require.NoError(t, err)
			assert.Equal(t, []int64{1, 2, 0, 1}, values, "drifts within max(2, 10%) wait for a second round; groups follow saved ownership")
			assert.True(t, VideoHealthReady())
			if backend == "redis" {
				marked, err := common.RDB.Exists(t.Context(), videoSchedCalibratedKey).Result()
				require.NoError(t, err)
				assert.Equal(t, int64(1), marked, "a successful calibration is published to other instances")
			}

			require.NoError(t, store.release(videoSchedCalibrationKey, "")) // no-op: the lock is held by its own token
			if backend == "redis" {
				require.NoError(t, common.RDB.Del(t.Context(), videoSchedCalibrationKey).Err())
			} else {
				memoryVideoHealth.slots = map[string]memoryVideoSlot{}
			}
			calibrateVideoInFlight()
			values, err = store.get([]string{videoInFlightKey(1), videoGroupInFlightKey("acct")})
			require.NoError(t, err)
			assert.Equal(t, []int64{0, 1}, values, "the same drift twice resets the zombie count and the group")

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
