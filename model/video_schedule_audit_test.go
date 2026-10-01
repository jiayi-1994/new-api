package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Captures SQL only during the reconciliation contract check: task provenance
// and private payloads must never be scanned by the scheduled audit job.
type videoAuditSQLRecorder struct {
	logger.Interface
	statements []string
}

func (r *videoAuditSQLRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	r.statements = append(r.statements, sql)
}

func TestVideoScheduleAuditDatabase(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "audit.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: "vs_audit_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			previous := DB
			DB = db
			t.Cleanup(func() { DB = previous })
			var version string
			if dialect == "sqlite" {
				require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
			} else {
				require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
			}
			t.Logf("%s: %s", dialect, version)
			models := []any{&VideoScheduleDecision{}, &VideoScheduleRun{}, &Task{}, &Option{}}
			require.NoError(t, db.Migrator().DropTable(models...))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })

			for _, scenario := range []string{"fresh", "p0_p5_upgrade"} {
				t.Run(scenario, func(t *testing.T) {
					require.NoError(t, db.Migrator().DropTable(models...))
					var existing Task
					if scenario == "p0_p5_upgrade" {
						require.NoError(t, db.AutoMigrate(&Task{}, &Option{}))
						existing = Task{TaskID: "existing-p0-p5", Status: TaskStatusSubmitted, ChannelId: 7}
						existing.PrivateData.SchedulingSummary = &TaskSchedulingSummary{Model: "video", CapacityGroup: "account"}
						require.NoError(t, db.Create(&existing).Error)
						require.NoError(t, db.Create(&Option{Key: "video_scheduling_setting.mode", Value: "shadow"}).Error)
					}
					for range 2 {
						require.NoError(t, db.AutoMigrate(&Task{}, &Option{}, &VideoScheduleRun{}, &VideoScheduleDecision{}))
					}
					if existing.ID != 0 {
						var after Task
						require.NoError(t, db.First(&after, existing.ID).Error)
						assert.Equal(t, existing.PrivateData, after.PrivateData)
						assert.Equal(t, existing.Status, after.Status)
						var option Option
						require.NoError(t, db.Where(&Option{Key: "video_scheduling_setting.mode"}).First(&option).Error)
						assert.Equal(t, "shadow", option.Value)
					}
					assert.True(t, db.Migrator().HasIndex(&VideoScheduleDecision{}, "idx_vs_decision_sequence"))
					assert.True(t, db.Migrator().HasIndex(&VideoScheduleRun{}, "idx_vs_run_pending"))
					now := time.Now().Truncate(time.Millisecond)
					ctx := context.Background()
					job := Task{TaskID: "finished-job", Status: TaskStatusSuccess}
					require.NoError(t, db.Create(&job).Error)
					audit := VideoScheduleAudit{
						Run:       VideoScheduleRun{RequestID: "audit-one", StartedAt: now.Add(-10 * time.Second).UnixMilli(), EndedAt: now.Add(-9 * time.Second).UnixMilli(), TaskPK: &job.ID, ModelName: "video", Mode: "on", SubmitAttempts: 2, RequestOutcome: "submitted", SnapshotComplete: true},
						Decisions: []VideoScheduleDecision{{SelectionSeq: 1, AttemptSeq: 1, InputJSON: `{"Seed":7}`, BoardJSON: `[]`, SnapshotComplete: true}, {SelectionSeq: 2, AttemptSeq: 1, Admission: "probe_slot_taken"}, {SelectionSeq: 3, AttemptSeq: 2, Selected: 7}},
					}
					// Terminal can arrive before the request batch; it must not create a phantom run.
					require.NoError(t, CompleteVideoScheduleAudit(ctx, "audit-one", VideoAuditTerminal{Status: TaskStatusSuccess, Class: "success", Health: "success", ObservedAt: now.UnixMilli()}))
					require.NoError(t, InsertVideoScheduleAudit(ctx, &audit))
					require.NoError(t, InsertVideoScheduleAudit(ctx, &audit))
					got, err := GetVideoScheduleAudit(ctx, "audit-one")
					require.NoError(t, err)
					require.Len(t, got.Decisions, 3)
					assert.Nil(t, got.Run.TerminalObservedAt)
					assert.Equal(t, `{"Seed":7}`, string(got.Decisions[0].InputJSON))
					recorder := &videoAuditSQLRecorder{Interface: logger.Default}
					DB = db.Session(&gorm.Session{Logger: recorder})
					repaired, err := ReconcileVideoScheduleAudits(ctx, now)
					DB = db
					require.NoError(t, err)
					assert.Equal(t, 1, repaired)
					for _, sql := range recorder.statements {
						assert.NotContains(t, strings.ToLower(sql), "private_data")
					}
					// Neither a duplicated batch nor a later conflicting terminal may rewrite the first result.
					require.NoError(t, InsertVideoScheduleAudit(ctx, &audit))
					require.NoError(t, CompleteVideoScheduleAudit(ctx, "audit-one", VideoAuditTerminal{Status: TaskStatusFailure, Class: "upstream", Health: "fail", ObservedAt: now.Add(time.Minute).UnixMilli()}))
					got, err = GetVideoScheduleAudit(ctx, "audit-one")
					require.NoError(t, err)
					assert.Equal(t, string(TaskStatusSuccess), got.Run.TaskStatus)
					require.NotNil(t, got.Run.DurationMS)
					assert.EqualValues(t, 10000, *got.Run.DurationMS)
					filter := VideoScheduleAuditFilter{Start: now.Add(-time.Hour).UnixMilli(), End: now.Add(time.Hour).UnixMilli(), Mode: "on", Model: "video", Page: 1, PageSize: 25}
					rows, total, err := ListVideoScheduleAudits(ctx, filter)
					require.NoError(t, err)
					assert.EqualValues(t, 1, total)
					require.Len(t, rows, 1)
					// A bad decision batch rolls back its run; a later valid retry can still commit.
					bad := VideoScheduleAudit{Run: VideoScheduleRun{RequestID: "bad-batch"}, Decisions: []VideoScheduleDecision{{SelectionSeq: 1}, {SelectionSeq: 1}}}
					require.Error(t, InsertVideoScheduleAudit(ctx, &bad))
					_, err = GetVideoScheduleAudit(ctx, "bad-batch")
					require.ErrorIs(t, err, gorm.ErrRecordNotFound)
					// Pending tasks survive retention; empty-task failures expire with their details.
					old := now.Add(-40 * 24 * time.Hour).UnixMilli()
					for _, name := range []string{"pending-old", "failed-old"} {
						run := VideoScheduleRun{RequestID: name, StartedAt: old, EndedAt: old}
						if name == "pending-old" {
							run.TaskPK = &job.ID
						}
						require.NoError(t, InsertVideoScheduleAudit(ctx, &VideoScheduleAudit{Run: run, Decisions: []VideoScheduleDecision{{SelectionSeq: 1}}}))
					}
					deleted, err := DeleteExpiredVideoScheduleAudits(ctx, now.Add(-30*24*time.Hour).UnixMilli())
					require.NoError(t, err)
					assert.EqualValues(t, 1, deleted)
					_, err = GetVideoScheduleAudit(ctx, "pending-old")
					require.NoError(t, err)
					require.NoError(t, CompleteVideoScheduleAudit(ctx, "failed-old", VideoAuditTerminal{Status: TaskStatusSuccess, Class: "success", Health: "success", ObservedAt: now.UnixMilli()}))
					var count int64
					require.NoError(t, db.Model(&VideoScheduleDecision{}).Where("request_id = ?", "failed-old").Count(&count).Error)
					assert.Zero(t, count)
					_, err = GetVideoScheduleAudit(ctx, "failed-old")
					require.ErrorIs(t, err, gorm.ErrRecordNotFound)
					// A valid snapshot may exceed MySQL's 64 KiB TEXT ceiling.
					largeInput := `{"fixture":"` + strings.Repeat("x", 200*1024) + `"}`
					largeAudit := VideoScheduleAudit{Run: VideoScheduleRun{RequestID: "large-snapshot", AttemptSummary: VideoAuditSnapshot(largeInput)}, Decisions: []VideoScheduleDecision{{SelectionSeq: 1, InputJSON: VideoAuditSnapshot(largeInput)}}}
					require.NoError(t, InsertVideoScheduleAudit(ctx, &largeAudit))
					largeStored, err := GetVideoScheduleAudit(ctx, "large-snapshot")
					require.NoError(t, err)
					require.Len(t, largeStored.Decisions, 1)
					assert.Equal(t, largeInput, string(largeStored.Decisions[0].InputJSON))
					assert.Equal(t, largeInput, string(largeStored.Run.AttemptSummary))
					verifyVideoAuditStatisticsAndExport(t, ctx, now)
				})
			}
			t.Run("indexed_capacity_and_log_routing", func(t *testing.T) {
				if os.Getenv("P51_CAPACITY") != "1" {
					t.Skip("set P51_CAPACITY=1 for the planned 100k-row capacity measurement")
				}
				ctx := context.Background()
				now := time.Now()
				started := time.Now()
				// A representative large task table contains private JSON; the
				// reconciliation query must only fetch the indexed audit task IDs.
				for batch := range 100 {
					tasks := make([]Task, 1000)
					for i := range tasks {
						tasks[i] = Task{TaskID: fmt.Sprintf("capacity-%d-%d", batch, i), Status: TaskStatusSuccess}
						tasks[i].PrivateData.Execution = &TaskExecutionSnapshot{RequestID: "sensitive-history-not-scanned"}
					}
					require.NoError(t, db.Select("task_id", "status", "private_data").CreateInBatches(tasks, 500).Error)
				}
				t.Logf("insert 100,000 tasks: %s", time.Since(started))
				var task Task
				require.NoError(t, db.Select("id").Where("task_id = ?", "capacity-99-999").First(&task).Error)
				require.NoError(t, InsertVideoScheduleAudit(ctx, &VideoScheduleAudit{Run: VideoScheduleRun{RequestID: "capacity-pending", TaskPK: &task.ID, StartedAt: now.UnixMilli(), EndedAt: now.UnixMilli()}}))
				logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "separate-log.db")), &gorm.Config{})
				require.NoError(t, err)
				previousLog, previousType := LOG_DB, common.LogDatabaseType()
				LOG_DB = logDB
				common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
				t.Setenv("LOG_SQL_DSN", "clickhouse://log-only.invalid:9000/logs")
				t.Cleanup(func() {
					LOG_DB = previousLog
					common.SetLogDatabaseType(previousType)
					sqlLog, _ := logDB.DB()
					_ = sqlLog.Close()
				})
				// Fail any accidental cross-database read/write instead of silently
				// allowing the shared SQLite driver to hide a routing error.
				rejectLog := func(tx *gorm.DB) { tx.AddError(errors.New("audit attempted to use separate ClickHouse log route")) }
				require.NoError(t, logDB.Callback().Query().Before("gorm:query").Register("reject_audit", rejectLog))
				require.NoError(t, logDB.Callback().Create().Before("gorm:create").Register("reject_audit", rejectLog))
				require.NoError(t, logDB.Callback().Update().Before("gorm:update").Register("reject_audit", rejectLog))
				recorder := &videoAuditSQLRecorder{Interface: logger.Default}
				DB = db.Session(&gorm.Session{Logger: recorder})
				started = time.Now()
				_, err = ReconcileVideoScheduleAudits(ctx, now)
				require.NoError(t, err)
				t.Logf("indexed reconciliation with 100k tasks: %s", time.Since(started))
				for _, sql := range recorder.statements {
					assert.NotContains(t, strings.ToLower(sql), "private_data")
				}
				DB = db
				got, err := GetVideoScheduleAudit(ctx, "capacity-pending")
				require.NoError(t, err)
				assert.Equal(t, string(TaskStatusSuccess), got.Run.TaskStatus)
				assert.False(t, logDB.Migrator().HasTable(&VideoScheduleRun{}))
				assert.False(t, logDB.Migrator().HasTable(&VideoScheduleDecision{}))
				// Exact stats reject a >100k cohort before scanning snapshot TEXT.
				started = time.Now()
				for batch := range 100 {
					runs := make([]VideoScheduleRun, 1000)
					for i := range runs {
						runs[i] = VideoScheduleRun{RequestID: fmt.Sprintf("cohort-%d-%d", batch, i), StartedAt: now.UnixMilli(), EndedAt: now.UnixMilli(), ModelName: "large-cohort", Mode: "on", RequestOutcome: "no_candidate", SnapshotComplete: true}
					}
					require.NoError(t, db.CreateInBatches(runs, 100).Error)
				}
				t.Logf("insert 100,000 runs: %s", time.Since(started))
				filter := VideoScheduleAuditFilter{Model: "large-cohort", Start: now.Add(-time.Hour).UnixMilli(), End: now.Add(time.Hour).UnixMilli()}
				started = time.Now()
				stats, err := GetVideoScheduleAuditStats(ctx, filter)
				require.NoError(t, err)
				assert.EqualValues(t, 100000, stats.Total)
				t.Logf("100k exact scalar stats: %s", time.Since(started))
				started = time.Now()
				rows, total, err := ListVideoScheduleAudits(ctx, filter)
				require.NoError(t, err)
				assert.Len(t, rows, 25)
				assert.EqualValues(t, 100000, total)
				t.Logf("indexed first-page query: %s", time.Since(started))
				require.NoError(t, db.Create(&VideoScheduleRun{RequestID: "cohort-overflow", ModelName: "large-cohort", StartedAt: now.UnixMilli()}).Error)
				_, err = GetVideoScheduleAuditStats(ctx, filter)
				require.ErrorContains(t, err, "narrow the time range")
			})
		})
	}
}

func verifyVideoAuditStatisticsAndExport(t *testing.T, ctx context.Context, now time.Time) {
	t.Helper()
	zero, cost := 0.0, 2.0
	duration1, duration2 := int64(1000), int64(9000)
	pk, terminal := int64(900), now.UnixMilli()
	runs := []VideoScheduleRun{
		{RequestID: "stats-success-retry", TaskPK: &pk, TaskStatus: TaskStatusSuccess, TerminalClass: "success", TerminalObservedAt: &terminal, DurationMS: &duration1, RequestOutcome: "submitted", SubmitAttempts: 2, SubmitAccepted: 1, SubmitRejected: 1, CostUSD: &zero},
		{RequestID: "stats-success", TaskPK: &pk, TaskStatus: TaskStatusSuccess, TerminalClass: "success", TerminalObservedAt: &terminal, DurationMS: &duration2, RequestOutcome: "submitted", SubmitAttempts: 1, SubmitAccepted: 1, CostUSD: &cost},
		{RequestID: "stats-user-failure", TaskPK: &pk, TaskStatus: TaskStatusFailure, TerminalClass: "user", TerminalObservedAt: &terminal, RequestOutcome: "submitted", SubmitAttempts: 1, SubmitAccepted: 1},
		{RequestID: "stats-pending", TaskPK: &pk, RequestOutcome: "submitted", SubmitAttempts: 1, SubmitAccepted: 1},
		{RequestID: "stats-unknown", RequestOutcome: "outcome_unknown", SubmitAttempts: 1, SubmitUnknown: 1},
		{RequestID: "stats-cancelled", RequestOutcome: "cancelled", SubmitAttempts: 1, SubmitCancelled: 1},
		{RequestID: "stats-no-candidate", RequestOutcome: "no_candidate", SubmitLocal: 1},
		{RequestID: "stats-persist-failed", RequestOutcome: "persistence_failure", SubmitAttempts: 1, SubmitAccepted: 1},
	}
	for i := range runs {
		if runs[i].TaskStatus == TaskStatusSuccess {
			runs[i].TerminalHealth = "success"
		} else if runs[i].TerminalClass == "user" {
			runs[i].TerminalHealth = "ignored"
		}
		runs[i].ModelName, runs[i].StartedAt, runs[i].EndedAt, runs[i].SnapshotComplete = "stats", now.Add(-time.Minute).UnixMilli(), now.UnixMilli(), true
		audit := VideoScheduleAudit{Run: runs[i]}
		if i == 0 {
			audit.Decisions = []VideoScheduleDecision{{SelectionSeq: 1, CandidateCount: 3, ExclusionsJSON: `{"capacity":1}`, ShadowComparable: true, ShadowDifferent: true, ShadowCostDelta: &zero}, {SelectionSeq: 2, CandidateCount: 2, ExclusionsJSON: `{"capacity":1}`}, {SelectionSeq: 3, CandidateCount: 2}}
		}
		require.NoError(t, InsertVideoScheduleAudit(ctx, &audit))
	}
	filter := VideoScheduleAuditFilter{Model: "stats", Start: now.Add(-time.Hour).UnixMilli(), End: now.Add(time.Hour).UnixMilli()}
	stats, err := GetVideoScheduleAuditStats(ctx, filter)
	require.NoError(t, err)
	assert.EqualValues(t, 8, stats.Total)
	assert.Equal(t, videoAuditRate(2, 4), stats.RequestSuccess)
	assert.Equal(t, videoAuditRate(5, 6), stats.SubmitAcceptance)
	assert.Equal(t, videoAuditRate(2, 3), stats.GenerationSuccess)
	assert.Equal(t, videoAuditRate(2, 2), stats.HealthSuccess)
	assert.Equal(t, videoAuditRate(1, 8), stats.Retry)
	assert.Equal(t, videoAuditRate(1, 8), stats.NoCandidate)
	assert.EqualValues(t, 1, stats.Pending)
	assert.EqualValues(t, 2, stats.Unknown)
	assert.EqualValues(t, 1, stats.Cancelled)
	assert.EqualValues(t, 1000, *stats.P50MS)
	assert.EqualValues(t, 9000, *stats.P95MS)
	assert.EqualValues(t, 2, stats.CostSamples)
	assert.EqualValues(t, 6, stats.CostMissing)
	assert.Equal(t, 1.0, *stats.MeanCostUSD)
	assert.EqualValues(t, 3, stats.Selections)
	assert.EqualValues(t, 7, stats.Candidates)
	assert.EqualValues(t, 2, stats.Exclusions["capacity"])
	filter.Model = "empty"
	empty, err := GetVideoScheduleAuditStats(ctx, filter)
	require.NoError(t, err)
	assert.Nil(t, empty.RequestSuccess.Value)
	assert.Nil(t, empty.P95MS)
	assert.Nil(t, empty.MeanCostUSD)
	filter.Model = "export"
	filter.Outcome = "pending"
	audit := VideoScheduleAudit{Run: VideoScheduleRun{RequestID: "export-large", ModelName: "export", TaskPK: common.GetPointer(int64(123)), StartedAt: now.UnixMilli(), EndedAt: now.UnixMilli()}, Decisions: []VideoScheduleDecision{{SelectionSeq: 1, InputJSON: VideoAuditSnapshot(strings.Repeat("x", 3500))}, {SelectionSeq: 2, InputJSON: VideoAuditSnapshot(strings.Repeat("y", 3500))}, {SelectionSeq: 3, InputJSON: VideoAuditSnapshot(strings.Repeat("z", 3500))}}}
	require.NoError(t, InsertVideoScheduleAudit(ctx, &audit))
	require.NoError(t, InsertVideoScheduleAudit(ctx, &VideoScheduleAudit{Run: VideoScheduleRun{RequestID: "export-tail", ModelName: "export", TaskPK: common.GetPointer(int64(124)), StartedAt: now.Add(time.Millisecond).UnixMilli(), EndedAt: now.Add(time.Millisecond).UnixMilli()}}))
	var sequences []int
	continuation := ""
	firstContinuation := ""
	summaries := 0
	for range 5 {
		data, err := exportVideoScheduleAudits(ctx, filter, continuation, 8192)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(data), 8192)
		partial := false
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			var row struct {
				Type         string                `json:"type"`
				Decision     VideoScheduleDecision `json:"decision"`
				Partial      bool                  `json:"partial"`
				Continuation string                `json:"continuation"`
			}
			require.NoError(t, common.UnmarshalJsonStr(line, &row))
			switch row.Type {
			case "request":
				summaries++
			case "decision":
				sequences = append(sequences, row.Decision.SelectionSeq)
			case "footer":
				partial, continuation = row.Partial, row.Continuation
			}
		}
		if !partial {
			break
		}
		require.NotEmpty(t, continuation)
		if firstContinuation == "" {
			firstContinuation = continuation
		}
		wrong := filter
		wrong.Mode = "on"
		_, err = exportVideoScheduleAudits(ctx, wrong, continuation, 8192)
		require.Error(t, err)
	}
	assert.Equal(t, 2, summaries)
	assert.Equal(t, []int{1, 2, 3}, sequences)
	assert.Empty(t, continuation)
	// A completed task no longer matches pending, but that must not end the
	// export while later requests still match the live filter.
	require.NoError(t, CompleteVideoScheduleAudit(ctx, "export-large", VideoAuditTerminal{Status: TaskStatusSuccess, ObservedAt: now.UnixMilli()}))
	resumed, err := exportVideoScheduleAudits(ctx, filter, firstContinuation, 8192)
	require.NoError(t, err)
	assert.NotContains(t, string(resumed), "export-large")
	assert.Contains(t, string(resumed), "export-tail")
	assert.Contains(t, string(resumed), `"partial":false`)
	// The partially exported request can expire between downloads. Its absence
	// must not hide the remaining requests under the captured high-water mark.
	require.NoError(t, DB.Where("request_id = ?", "export-large").Delete(&VideoScheduleDecision{}).Error)
	require.NoError(t, DB.Where("request_id = ?", "export-large").Delete(&VideoScheduleRun{}).Error)
	resumed, err = exportVideoScheduleAudits(ctx, filter, firstContinuation, 8192)
	require.NoError(t, err)
	assert.Contains(t, string(resumed), "export-tail")
	assert.Contains(t, string(resumed), `"partial":false`)
}

func TestVideoScheduleAuditFilterRejectsUnboundedQueries(t *testing.T) {
	now := time.Unix(1780000000, 0)
	for _, tc := range []VideoScheduleAuditFilter{{Start: 1, End: now.UnixMilli()}, {PageSize: 101}, {Page: -1}, {Mode: "off"}, {Start: 2, End: 1}} {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) { require.Error(t, tc.Validate(now)) })
	}
}

// Used by the release-upgrade harness after the actual released executable
// created the database, then after each current executable startup.
func TestVideoScheduleAuditReleasedDatabase(t *testing.T) {
	phase := os.Getenv("P51_UPGRADE_PHASE")
	if phase == "" {
		t.Skip("release upgrade harness is not active")
	}
	if value := os.Getenv("SQLITE_PATH"); value != "" {
		old := common.SQLitePath
		common.SQLitePath = value
		t.Cleanup(func() { common.SQLitePath = old })
	}
	db, kind, err := chooseDB("SQL_DSN", false)
	require.NoError(t, err)
	oldDB, oldKind := DB, common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(kind)
	t.Cleanup(func() { DB = oldDB; common.SetMainDatabaseType(oldKind); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	const privateData = `{"execution":{"request_id":"p51-legacy-request"},"channel_key":"legacy-private-marker"}`
	if phase == "seed" {
		assert.False(t, db.Migrator().HasTable(&VideoScheduleRun{}))
		require.NoError(t, db.Table("tasks").Create(map[string]any{"id": int64(9001), "task_id": "p51-legacy-task", "status": TaskStatusSuccess, "private_data": privateData}).Error)
		require.NoError(t, db.Create(&Option{Key: "video_scheduling_setting.audit_retention_days", Value: "45"}).Error)
		return
	}
	require.Equal(t, "verify", phase)
	var existing struct {
		TaskID      string
		Status      string
		PrivateData string
	}
	require.NoError(t, db.Table("tasks").Where("id = ?", 9001).First(&existing).Error)
	assert.Equal(t, "p51-legacy-task", existing.TaskID)
	assert.Equal(t, string(TaskStatusSuccess), existing.Status)
	assert.JSONEq(t, privateData, existing.PrivateData)
	var option Option
	require.NoError(t, db.Where(&Option{Key: "video_scheduling_setting.audit_retention_days"}).First(&option).Error)
	assert.Equal(t, "45", option.Value)
	for _, index := range []string{"idx_vs_run_pending", "idx_vs_run_time", "idx_vs_run_model_time", "idx_vs_run_channel_time"} {
		assert.True(t, db.Migrator().HasIndex(&VideoScheduleRun{}, index))
	}
	assert.True(t, db.Migrator().HasIndex(&VideoScheduleDecision{}, "idx_vs_decision_sequence"))
	audit := VideoScheduleAudit{Run: VideoScheduleRun{RequestID: "p51-upgrade-contract", StartedAt: time.Now().UnixMilli(), SnapshotComplete: true}, Decisions: []VideoScheduleDecision{{SelectionSeq: 1, InputJSON: `{"Seed":7}`}}}
	require.NoError(t, InsertVideoScheduleAudit(context.Background(), &audit))
	require.NoError(t, InsertVideoScheduleAudit(context.Background(), &audit))
	got, err := GetVideoScheduleAudit(context.Background(), audit.Run.RequestID)
	require.NoError(t, err)
	require.Len(t, got.Decisions, 1)
	assert.Equal(t, `{"Seed":7}`, string(got.Decisions[0].InputJSON))
	assert.Nil(t, got.Run.CostUSD)
}

func TestVideoScheduleAuditBrowserFixture(t *testing.T) {
	file := os.Getenv("P51_BROWSER_DB")
	if file == "" {
		t.Skip("isolated browser fixture is not requested")
	}
	require.Contains(t, filepath.ToSlash(file), "/.scratch/video-smart-scheduling/p51-browser/")
	db, err := gorm.Open(sqlite.Open(file), &gorm.Config{})
	require.NoError(t, err)
	old := DB
	DB = db
	t.Cleanup(func() { DB = old; sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	password, err := common.Password2Hash("p51-local-browser-only")
	require.NoError(t, err)
	user := User{Username: "p51-root", Password: password, Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "p51-root", AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().UnixMilli()
	cost := 0.25
	duration := int64(82000)
	taskPK := int64(9002)
	require.NoError(t, db.Create(&Task{ID: taskPK, TaskID: "p51-demo-task", UserId: user.Id, Status: TaskStatusSuccess}).Error)
	for i := range 30 {
		run := VideoScheduleRun{RequestID: fmt.Sprintf("p51-demo-request-%02d", i), StartedAt: now - int64(30-i)*60000, EndedAt: now, Mode: "on", ModelName: "videos-mini", ActualGroup: "default", SelectedChannel: 7, SubmitAttempts: 1, SubmitAccepted: 1, RequestOutcome: "submitted", TaskPK: &taskPK, TaskID: "p51-demo-task", TaskStatus: TaskStatusSuccess, TerminalClass: "success", TerminalHealth: "success", TerminalObservedAt: &now, DurationMS: &duration, CostUSD: &cost, Resolution: "720p", SnapshotComplete: true}
		if i%5 == 0 {
			run.TaskStatus = ""
			run.TerminalObservedAt = nil
			run.DurationMS = nil
			run.TerminalHealth = ""
			run.TerminalClass = ""
		}
		decision := VideoScheduleDecision{SelectionSeq: 1, AttemptSeq: 1, SelectedAt: run.StartedAt, ActualGroup: "default", Recommended: 7, Selected: 7, ChoiceKind: "normal", SubmitOutcome: "accepted", HealthOutcome: "success", CandidateCount: 2, SchemaVersion: "1", SchedulerVersion: "1", BuildVersion: "p51-browser", ConfigVersion: "fixture-config", Fingerprint: "fixture-fingerprint", SnapshotComplete: true,
			BoardJSON: `[{"id":7,"name":"Primary video provider","p":1,"q":0.9,"s":0.99,"total":0.968,"cost_usd":0.25,"base_cost_usd":0.2,"reference_cost_usd":0.05,"sell_kind":"known","sell_usd":0.4},{"id":8,"name":"Backup video provider","p":0.5,"q":0.8,"s":0.95,"total":0.68,"excluded":"capacity"}]`,
			InputJSON: `{"Candidates":[{"ID":7,"Capacity":20,"InFlight":4,"GroupCapacity":30,"GroupInFlight":12,"Submit":{"rate":0.99,"samples":42},"Gen":{"rate":0.98,"samples":38}}],"Seed":7}`, PluginsJSON: `{"fixture":{"version":"1","generation":1}}`, ExclusionsJSON: `{"capacity":1}`}
		require.NoError(t, InsertVideoScheduleAudit(context.Background(), &VideoScheduleAudit{Run: run, Decisions: []VideoScheduleDecision{decision}}))
	}
}
