package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Invoked by the isolated upgrade harness around actual executable startups.
func TestVideoHealthDatabaseUpgrade(t *testing.T) {
	phase := os.Getenv("P52_UPGRADE_PHASE")
	if phase == "" {
		t.Skip("isolated upgrade harness is not active")
	}
	oldPath, oldDB, oldKind := common.SQLitePath, DB, common.MainDatabaseType()
	common.SQLitePath = os.Getenv("SQLITE_PATH")
	db, kind, err := chooseDB("SQL_DSN", false)
	require.NoError(t, err)
	DB = db
	common.SetMainDatabaseType(kind)
	logDB, _, err := chooseDB("LOG_SQL_DSN", true)
	require.NoError(t, err)
	t.Cleanup(func() {
		DB, common.SQLitePath = oldDB, oldPath
		common.SetMainDatabaseType(oldKind)
		for _, handle := range []*gorm.DB{db, logDB} {
			sqlDB, err := handle.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
		}
	})
	const privateData = `{"execution":{"request_id":"p52-preserved"},"channel_key":"fixture-only-private-marker"}`
	if phase == "seed" || phase == "seed-audit" {
		if phase == "seed" {
			require.NoError(t, db.Table("tasks").Create(map[string]any{"id": int64(9201), "task_id": "p52-preserved-task", "status": TaskStatusSuccess, "private_data": privateData}).Error)
			require.NoError(t, db.Create(&Option{Key: "video_scheduling_setting.audit_retention_days", Value: "45"}).Error)
			require.NoError(t, logDB.Table("logs").Create(map[string]any{"id": int64(9201), "content": "p52-preserved-log"}).Error)
		}
		if os.Getenv("P52_UPGRADE_BASELINE") == "p51" {
			// Write only columns in P5.1, before P5.2 adds selection_reason.
			require.NoError(t, db.Table("video_schedule_runs").Create(map[string]any{"request_id": "p52-preserved-audit", "started_at": time.Now().UnixMilli(), "snapshot_complete": true}).Error)
			require.NoError(t, db.Table("video_schedule_decisions").Create(map[string]any{"request_id": "p52-preserved-audit", "selection_seq": 1, "input_json": `{"Seed":7}`, "schema_version": "1"}).Error)
		}
		assert.False(t, db.Migrator().HasTable(&VideoHealthState{}))
		return
	}
	if os.Getenv("P52_UPGRADE_BASELINE") != "fresh" {
		var task struct{ TaskID, Status, PrivateData string }
		require.NoError(t, db.Table("tasks").Where("id = ?", 9201).First(&task).Error)
		assert.Equal(t, "p52-preserved-task", task.TaskID)
		assert.Equal(t, string(TaskStatusSuccess), task.Status)
		assert.JSONEq(t, privateData, task.PrivateData)
		var option Option
		require.NoError(t, db.Where(&Option{Key: "video_scheduling_setting.audit_retention_days"}).First(&option).Error)
		assert.Equal(t, "45", option.Value)
		var log struct{ Content string }
		require.NoError(t, logDB.Table("logs").Where("id = ?", 9201).First(&log).Error)
		assert.Equal(t, "p52-preserved-log", log.Content)
	}
	if os.Getenv("P52_UPGRADE_BASELINE") == "p51" {
		audit, err := GetVideoScheduleAudit(t.Context(), "p52-preserved-audit")
		require.NoError(t, err)
		require.Len(t, audit.Decisions, 1)
		assert.JSONEq(t, `{"Seed":7}`, string(audit.Decisions[0].InputJSON))
		assert.Equal(t, "1", audit.Decisions[0].SchemaVersion)
	}
	for _, table := range []any{&VideoHealthRegistration{}, &VideoHealthState{}, &VideoHealthAttempt{}, &VideoHealthRequest{}, &VideoScheduleRun{}, &VideoScheduleDecision{}} {
		assert.True(t, db.Migrator().HasTable(table))
		assert.False(t, logDB.Migrator().HasTable(table), "online scheduling state belongs to the primary database")
	}
	for _, index := range []string{"idx_vh_attempt", "idx_vh_cohort", "idx_vh_round", "idx_vh_pending"} {
		assert.True(t, db.Migrator().HasIndex(&VideoHealthAttempt{}, index))
	}
	assert.True(t, db.Migrator().HasIndex(&VideoHealthState{}, "idx_vh_state"))
	assert.True(t, db.Migrator().HasIndex(&VideoHealthRegistration{}, "idx_vh_registration"))
	for _, column := range []string{"config_identity", "config_version"} {
		assert.True(t, db.Migrator().HasColumn(&VideoHealthState{}, column))
	}
	for _, column := range []string{"config_identity", "reviewed_at", "reviewed_by", "review_note"} {
		assert.True(t, db.Migrator().HasColumn(&VideoHealthAttempt{}, column))
	}
	assert.True(t, db.Migrator().HasIndex(&Task{}, "idx_tasks_video_health_attempt_id"))
	assert.True(t, db.Migrator().HasIndex(&VideoScheduleDecision{}, "idx_vs_decision_sequence"))
	if phase == "verify-1" {
		require.NoError(t, EnsureVideoHealthState(t.Context(), 9201, "upgrade-video"))
		a := VideoHealthAttempt{RequestID: "p52-upgrade-failure", AttemptSeq: 1, ChannelID: 9201, ModelName: "upgrade-video", WindowSeconds: 1800, StartedAt: time.Now().Unix(), Flow: "shadow", SubmitOwner: "upgrade-owner", SubmitLeaseExpires: 1900000000}
		require.NoError(t, BeginVideoHealthAttempt(t.Context(), &a, false))
		require.NoError(t, ObserveVideoHealthAttempt(t.Context(), a.RequestID, 1, nil, "rejected", "upstream", "upstream", a.StartedAt+1))
	} else {
		require.Equal(t, "verify-2", phase)
	}
	var state VideoHealthState
	require.NoError(t, db.Where("channel_id = ? AND model_name = ?", 9201, "upgrade-video").First(&state).Error)
	assert.Equal(t, videosched.HealthBlocked, state.State)
	assert.EqualValues(t, 2, state.Version)
	assert.Error(t, db.Create(&VideoHealthState{ChannelID: 9201, ModelName: "upgrade-video"}).Error, "pair uniqueness survives repeated migration")
	attempts, err := ListVideoHealthAttempts(t.Context(), "p52-upgrade-failure")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, "upstream", attempts[0].FinalOutcome)
	assert.Equal(t, "upgrade-owner", attempts[0].SubmitOwner)
	assert.EqualValues(t, 1900000000, attempts[0].SubmitLeaseExpires)
	duplicate := attempts[0]
	duplicate.ID = 0
	assert.Error(t, db.Create(&duplicate).Error, "request-attempt uniqueness survives repeated migration")
	var dbVersion string
	query := "SELECT version()"
	if kind == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&dbVersion).Error)
	t.Logf("baseline=%s phase=%s engine=%s version=%s", os.Getenv("P52_UPGRADE_BASELINE"), phase, kind, dbVersion)
}

func TestVideoHealthBrowserFixture(t *testing.T) {
	file := os.Getenv("P52_BROWSER_DB")
	if file == "" {
		t.Skip("isolated browser fixture is not requested")
	}
	require.Contains(t, filepath.ToSlash(file), "/.scratch/video-smart-scheduling/p52-browser/")
	db, err := gorm.Open(sqlite.Open(file), &gorm.Config{})
	require.NoError(t, err)
	oldDB := DB
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		handle, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, handle.Close())
	})
	now := time.Now().Unix()
	for key, value := range map[string]string{
		"video_scheduling_setting.selection_policy": "stability_cost_v2", "video_scheduling_setting.mode": "shadow", "video_scheduling_setting.models": `["videos-mini"]`,
		"video_scheduling_setting.min_gen_rate": "0.8", "video_scheduling_setting.min_overall_rate": "0.6", "video_scheduling_setting.min_margin_rate": "0.1", "video_scheduling_setting.stability_tolerance": "0.01",
		"video_scheduling_setting.qualification_ttl_seconds": "86400", "video_scheduling_setting.validation_period_seconds": "604800", "video_scheduling_setting.explore_max_in_flight": "2", "ModelPrice": `{"videos-mini":1}`,
	} {
		var option Option
		result := db.Where("key = ?", key).First(&option)
		if result.Error == nil {
			require.NoError(t, db.Model(&Option{}).Where("key = ?", key).Update("value", value).Error)
		} else {
			require.ErrorIs(t, result.Error, gorm.ErrRecordNotFound)
			require.NoError(t, db.Create(&Option{Key: key, Value: value}).Error)
		}
	}
	for i, stateName := range []string{videosched.HealthUnverified, videosched.HealthBlocked, videosched.HealthRecovering, videosched.HealthNormal} {
		id := 71 + i
		setting, baseURL := `{"task_plugin_key":"megabyai"}`, "http://127.0.0.1:1"
		channel := Channel{Id: id, Type: 61, Key: "fixture-only-key", Status: common.ChannelStatusEnabled, Name: "P5.2 " + stateName, Models: "videos-mini", Group: "default", Setting: &setting, BaseURL: &baseURL, OtherSettings: `{"video_scheduling":{"quality":0.9,"capacity":10,"models":{"videos-mini":{"mode":"per_video","prices":{"*":0.5}}}}}`}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(db))
		require.NoError(t, EnsureVideoHealthState(t.Context(), id, "videos-mini"))
		updates := map[string]any{"state": stateName, "reason": "new_channel", "window_seconds": 1800, "validation_round": 1, "validation_started": now - 3600, "validation_expires": now + 604800, "validation_source": "cold_start"}
		if stateName == videosched.HealthBlocked || stateName == videosched.HealthRecovering {
			updates["blocked_at"], updates["reason"] = now-600, "new upstream failure"
		}
		if stateName == videosched.HealthRecovering {
			updates["recovery_started"], updates["recovery_expires"] = now-300, now+604800
		}
		var evidence *videosched.ReliabilityEvidence
		if stateName == videosched.HealthNormal {
			evidence = &videosched.ReliabilityEvidence{Version: 1, Source: "cold_start", BatchStart: now - 7200, BatchEnd: now - 5400, WindowSeconds: 1800, AsOf: now - 1800, ValidatedAt: now - 1800, ExpiresAt: now + 84600, Submitted: 20, Accepted: 20, Succeeded: 20}
			data, err := common.Marshal(evidence)
			require.NoError(t, err)
			updates["qualification_json"], updates["reason"] = string(data), "qualified"
		}
		require.NoError(t, db.Model(&VideoHealthState{}).Where("channel_id = ?", id).Updates(updates).Error)
		var state VideoHealthState
		require.NoError(t, db.Where("channel_id = ?", id).First(&state).Error)
		view, err := state.Snapshot()
		require.NoError(t, err)
		flow := "explore"
		if stateName == videosched.HealthNormal {
			flow = "normal"
		}
		if stateName == videosched.HealthRecovering || stateName == videosched.HealthBlocked {
			flow = "recover"
		}
		board := []map[string]any{{"id": id, "name": channel.Name, "q": .9, "cost_usd": .5, "sell_usd": 1, "sell_kind": "known", "estimated_margin": .5, "reliability": view}}
		boardJSON, err := common.Marshal(board)
		require.NoError(t, err)
		inputJSON, err := common.Marshal(map[string]any{"Candidates": []map[string]any{{"ID": id, "Capacity": 10, "InFlight": 0, "Reliability": view}}, "SlotOccupancy": map[int]int{id: 0}, "Explore": map[string]int{"ExploreMaxInFlight": 2, "ProbeMaxInFlight": 1}, "Policy": map[string]any{"SelectionPolicy": videosched.PolicyStabilityCostV2}})
		require.NoError(t, err)
		requestID := fmt.Sprintf("p52-browser-%d", id)
		audit := VideoScheduleAudit{Run: VideoScheduleRun{RequestID: requestID, StartedAt: now*1000 - int64(i)*60000, EndedAt: now * 1000, Mode: "on", ModelName: "videos-mini", ActualGroup: "default", SelectedChannel: id, SubmitAttempts: 1, SubmitAccepted: 1, RequestOutcome: "submitted", TaskStatus: TaskStatusSuccess, TerminalHealth: "success", TerminalClass: "success", SnapshotComplete: true}, Decisions: []VideoScheduleDecision{{SelectionSeq: 1, AttemptSeq: 1, SelectedAt: now * 1000, ActualGroup: "default", Recommended: id, Selected: id, ChoiceKind: flow, SelectionReason: "no_normal_candidate", SubmitOutcome: "accepted", HealthOutcome: "success", CandidateCount: 1, SchemaVersion: "2", SchedulerVersion: videosched.PolicyStabilityCostV2, BuildVersion: "p52-browser-fixture", SnapshotComplete: true, BoardJSON: VideoAuditSnapshot(boardJSON), InputJSON: VideoAuditSnapshot(inputJSON)}}}
		require.NoError(t, InsertVideoScheduleAudit(t.Context(), &audit))
		require.NoError(t, db.Create(&VideoHealthAttempt{RequestID: requestID, AttemptSeq: 1, ChannelID: id, ModelName: "videos-mini", StartedAt: now, WindowSeconds: 1800, Mode: "on", SelectionPolicy: videosched.PolicyStabilityCostV2, Flow: flow, SubmitOutcome: "accepted", FinalOutcome: "success"}).Error)
		require.NoError(t, db.Create(&VideoHealthRequest{RequestID: requestID, StartedAt: now, ModelName: "videos-mini", Mode: "on", SelectionPolicy: videosched.PolicyStabilityCostV2, ChannelID: id, Outcome: "success", FinishedAt: now}).Error)
	}
}

func TestVideoHealthDatabase(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "health.db"))
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
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: "vs_health_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			previous, previousKind := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			t.Cleanup(func() { DB = previous; common.SetMainDatabaseType(previousKind) })
			models := []any{&VideoHealthRegistration{}, &VideoHealthState{}, &VideoHealthAttempt{}, &VideoHealthRequest{}, &Task{}, &Channel{}}
			require.NoError(t, db.Migrator().DropTable(models...))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(models...)) })
			for range 2 {
				require.NoError(t, db.AutoMigrate(models...))
			}
			var dbVersion string
			if dialect == "sqlite" {
				require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&dbVersion).Error)
			} else {
				require.NoError(t, db.Raw("SELECT version()").Scan(&dbVersion).Error)
			}
			t.Logf("%s: %s", dialect, dbVersion)
			ctx := context.Background()
			p := videosched.Policy{MinSamples: 20, MinGenRate: .8, MinOverallRate: .6, QualificationTTLSeconds: 86400, ValidationPeriodSeconds: 604800}
			const start int64 = 1800000000 // aligned to the ordinary 1800-second bucket
			t.Run("upstream_configuration_fences_evidence_and_transport", func(t *testing.T) {
				channel := Channel{Id: 901, Key: "fixture", Models: "video", BaseURL: common.GetPointer("https://old.example")}
				require.NoError(t, db.Create(&channel).Error)
				oldIdentity := channel.VideoHealthIdentity()
				require.NoError(t, EnsureVideoHealthState(ctx, channel.Id, "video", oldIdentity))
				certificate := videosched.ReliabilityEvidence{Version: 1, Source: "window", BatchStart: start - 3600, BatchEnd: start - 1800, WindowSeconds: 1800, AsOf: start - 60, ValidatedAt: start - 60, ExpiresAt: start + 86400, Submitted: 20, Accepted: 20, Succeeded: 20}
				encoded, err := marshalVideoHealthEvidence(certificate)
				require.NoError(t, err)
				require.NoError(t, db.Model(&VideoHealthState{}).Where("channel_id = ?", channel.Id).Updates(map[string]any{"state": videosched.HealthNormal, "qualification_json": encoded}).Error)
				old := VideoHealthAttempt{RequestID: "old-upstream", AttemptSeq: 1, ChannelID: channel.Id, ModelName: "video", ConfigIdentity: oldIdentity, WindowSeconds: 1800, StartedAt: start + 1, Flow: "shadow"}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &old, false))
				channel.BaseURL = common.GetPointer("https://new.example")
				require.NoError(t, db.Model(&channel).Update("base_url", channel.BaseURL).Error)
				stale := old
				stale.ID, stale.RequestID = 0, "stale-admission"
				require.ErrorIs(t, BeginVideoHealthAttempt(ctx, &stale, false), ErrVideoHealthStateChanged, "even a stale cache cannot submit against changed configuration")
				require.NoError(t, EnsureVideoHealthState(ctx, channel.Id, "video", channel.VideoHealthIdentity()))
				require.ErrorIs(t, EnsureVideoHealthState(ctx, channel.Id, "video", oldIdentity), ErrVideoHealthStateChanged, "a delayed refresh cannot restore the old identity")
				require.NoError(t, ObserveVideoHealthAttempt(ctx, old.RequestID, 1, nil, "unknown", "unknown", "transport", start+2))
				view, err := RefreshVideoHealthState(ctx, channel.Id, "video", p, start+3)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthUnverified, view.State)
				assert.Equal(t, "complete", view.Integrity)
				assert.Nil(t, view.Qualification)
				assert.Greater(t, view.StateVersion, old.StateVersion)
				// Returning to an earlier configuration still starts a new epoch.
				channel.BaseURL = common.GetPointer("https://old.example")
				require.NoError(t, db.Model(&channel).Update("base_url", channel.BaseURL).Error)
				require.NoError(t, EnsureVideoHealthState(ctx, channel.Id, "video", oldIdentity))
				view, err = RefreshVideoHealthState(ctx, channel.Id, "video", p, start+4)
				require.NoError(t, err)
				assert.Equal(t, "complete", view.Integrity, "older unknown facts cannot poison a new configuration epoch")
				facts, err := ListVideoHealthAttempts(ctx, old.RequestID)
				require.NoError(t, err)
				require.Len(t, facts, 1)
				assert.Equal(t, "unknown", facts[0].FinalOutcome, "historical facts are retained")
			})
			t.Run("reviewed_unknown_requires_fresh_recovery_and_preserves_audit", func(t *testing.T) {
				require.NoError(t, EnsureVideoHealthState(ctx, 902, "video"))
				unknown := VideoHealthAttempt{RequestID: "review-unknown", AttemptSeq: 1, ChannelID: 902, ModelName: "video", StartedAt: start + 1, WindowSeconds: 1800, Flow: "shadow", SubmitLeaseExpires: start + 300}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &unknown, false))
				require.NoError(t, ObserveVideoHealthAttempt(ctx, unknown.RequestID, 1, nil, "unknown", "unknown", "transport", start+2))
				_, err := ReviewVideoHealthUnknown(ctx, unknown.ID, 1, "Checked provider records", start+200)
				require.Error(t, err, "a live submission cannot be reviewed")
				_, err = ReviewVideoHealthUnknown(ctx, unknown.ID, 1, " ", start+301)
				require.Error(t, err, "evidence is required")
				task := Task{TaskID: "review-linked-task", Status: TaskStatusSubmitted, VideoHealthAttemptID: &unknown.ID}
				require.NoError(t, db.Create(&task).Error)
				_, err = ReviewVideoHealthUnknown(ctx, unknown.ID, 1, "Checked provider records", start+301)
				require.Error(t, err, "a task's indexed link protects it before the callback arrives")
				require.NoError(t, db.Delete(&task).Error)
				result, err := ReviewVideoHealthUnknown(ctx, unknown.ID, 1, "Checked provider records", start+301)
				require.NoError(t, err)
				assert.Equal(t, "unknown", result.FinalOutcome)
				assert.Equal(t, 1, result.ReviewedBy)
				again, err := ReviewVideoHealthUnknown(ctx, unknown.ID, 2, "different note", start+302)
				require.NoError(t, err)
				assert.Equal(t, result.ReviewedAt, again.ReviewedAt)
				assert.Equal(t, result.ReviewNote, again.ReviewNote)
				view, err := RefreshVideoHealthState(ctx, 902, "video", p, start+302)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthBlocked, view.State)
				assert.Nil(t, view.Qualification)
				version, err := StartVideoHealthRecovery(ctx, 902, "video", view.StateVersion, start+603, p.ValidationPeriodSeconds)
				require.NoError(t, err)
				for i := range 20 {
					a := VideoHealthAttempt{RequestID: fmt.Sprintf("review-recovery-%d", i), AttemptSeq: 1, ChannelID: 902, ModelName: "video", StartedAt: start + 604 + int64(i), WindowSeconds: 1800, StateVersion: version, Flow: "recover"}
					require.NoError(t, BeginVideoHealthAttempt(ctx, &a, false))
					require.NoError(t, ObserveVideoHealthAttempt(ctx, a.RequestID, 1, nil, "accepted", "success", "success", a.StartedAt+1))
				}
				view, err = RefreshVideoHealthState(ctx, 902, "video", p, start+1801)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthNormal, view.State)
				require.NoError(t, CleanupVideoHealthFacts(ctx, start+40*86400))
				facts, err := ListVideoHealthAttempts(ctx, unknown.RequestID)
				require.NoError(t, err)
				require.Len(t, facts, 1)
				assert.Equal(t, "unknown", facts[0].FinalOutcome)
				assert.Equal(t, "Checked provider records", facts[0].ReviewNote)
			})
			t.Run("qualification_rechecked_after_policy_change", func(t *testing.T) {
				for i, scenario := range []string{"sample_admission", "sample_refresh", "generation_gate", "overall_gate"} {
					t.Run(scenario, func(t *testing.T) {
						channelID := 60 + i
						require.NoError(t, EnsureVideoHealthState(ctx, channelID, "video"))
						certificate := videosched.ReliabilityEvidence{Version: 1, Source: "window", BatchStart: start, BatchEnd: start + 1800, WindowSeconds: 1800, AsOf: start + 1801, ValidatedAt: start + 1801, ExpiresAt: start + 88201, Submitted: 20, Accepted: 20, Succeeded: 20}
						changedPolicy := p
						expectedState := videosched.HealthUnverified
						switch scenario {
						case "sample_admission", "sample_refresh":
							changedPolicy.MinSamples = 40
						case "generation_gate":
							certificate.Succeeded, certificate.GenerationFailed = 18, 2
							changedPolicy.MinGenRate, expectedState = .95, videosched.HealthBlocked
						case "overall_gate":
							certificate.Submitted, certificate.Rejected = 25, 5
							changedPolicy.MinOverallRate, expectedState = .9, videosched.HealthBlocked
						}
						encoded, err := marshalVideoHealthEvidence(certificate)
						require.NoError(t, err)
						require.NoError(t, db.Model(&VideoHealthState{}).Where("channel_id = ?", channelID).Updates(map[string]any{"state": videosched.HealthNormal, "qualification_json": encoded, "validation_started": start + 1800, "validation_expires": start + 604800}).Error)
						if scenario == "sample_admission" {
							version, err := StartVideoHealthValidation(ctx, channelID, "video", 1, start+1900, changedPolicy, "revalidate")
							require.NoError(t, err)
							assert.EqualValues(t, 2, version)
							attempt := VideoHealthAttempt{RequestID: "policy-revalidation", AttemptSeq: 1, ChannelID: channelID, ModelName: "video", StartedAt: start + 1901, WindowSeconds: 1800, StateVersion: version, Flow: "revalidate", ValidationLimit: 1, SlotKey: "policy-slot", SlotToken: "owner", SlotExpires: start + 3600}
							require.NoError(t, BeginVideoHealthAttempt(ctx, &attempt, true), "more required samples must not prevent the next limited request")
						} else {
							_, err = RefreshVideoHealthState(ctx, channelID, "video", changedPolicy, start+1900)
							require.NoError(t, err)
						}
						var state VideoHealthState
						require.NoError(t, db.Where("channel_id = ?", channelID).First(&state).Error)
						assert.Equal(t, expectedState, state.State)
						assert.Equal(t, encoded, state.QualificationJSON, "policy changes do not renew old evidence")
						if expectedState == videosched.HealthBlocked {
							assert.Equal(t, start+1900, state.BlockedAt)
						} else {
							assert.Equal(t, "revalidation", state.ValidationSource)
							assert.EqualValues(t, 2, state.ValidationRound)
						}
					})
				}
			})
			t.Run("submission_owner_expiry_and_late_handoff", func(t *testing.T) {
				for i, scenario := range []string{"live", "expired", "terminal_won", "task_insert_won", "late_terminal", "rejected_abandoned", "rejected_finished"} {
					t.Run(scenario, func(t *testing.T) {
						channelID := 50 + i
						require.NoError(t, EnsureVideoHealthState(ctx, channelID, "video"))
						attempt := VideoHealthAttempt{RequestID: "owner-" + scenario, AttemptSeq: 1, ChannelID: channelID, ModelName: "video", StartedAt: start, WindowSeconds: 1800, Flow: "shadow", SubmitOwner: "current-owner", SubmitLeaseExpires: start + 120}
						require.NoError(t, BeginVideoHealthAttempt(ctx, &attempt, false))
						if scenario == "live" {
							require.NoError(t, RenewVideoHealthSubmission(ctx, attempt.ID, attempt.SubmitOwner, start+420))
						} else {
							require.NoError(t, RenewVideoHealthSubmission(ctx, attempt.ID, "stale-owner", start+420))
						}
						if scenario == "terminal_won" {
							require.NoError(t, ObserveVideoHealthAttempt(ctx, attempt.RequestID, 1, nil, "accepted", "success", "success", start+200))
							require.NoError(t, FinishVideoHealthRequest(ctx, &VideoHealthRequest{RequestID: attempt.RequestID, StartedAt: start, ChannelID: channelID, Outcome: "unknown", FinishedAt: start + 201, Missing: true}), "a panic before local task linkage cannot mark an already successful request missing")
						}
						if scenario == "task_insert_won" {
							require.NoError(t, db.Create(&Task{TaskID: attempt.RequestID, Status: TaskStatusSubmitted, VideoHealthAttemptID: &attempt.ID}).Error)
						}
						if scenario == "rejected_abandoned" || scenario == "rejected_finished" {
							require.NoError(t, ObserveVideoHealthAttempt(ctx, attempt.RequestID, 1, nil, "rejected", "upstream", "upstream", start+1))
							if scenario == "rejected_finished" {
								require.NoError(t, FinishVideoHealthRequest(ctx, &VideoHealthRequest{RequestID: attempt.RequestID, StartedAt: start, Outcome: "failure", FinishedAt: start + 2}))
							}
						}
						// Reconciliation may already have read an older no-task snapshot.
						require.NoError(t, ExpireVideoHealthSubmission(ctx, attempt.RequestID, 1, start+300))
						require.NoError(t, ReconcileAbandonedVideoHealthRequests(ctx, start+300))
						if scenario == "late_terminal" {
							pk := int64(999)
							require.NoError(t, ObserveVideoHealthAttempt(ctx, attempt.RequestID, 1, &pk, "accepted", "success", "success", start+301))
							require.NoError(t, ExpireVideoHealthSubmission(ctx, attempt.RequestID, 1, start+302))
						}
						facts, err := ListVideoHealthAttempts(ctx, attempt.RequestID)
						require.NoError(t, err)
						require.Len(t, facts, 1)
						switch scenario {
						case "expired":
							assert.Equal(t, "unknown", facts[0].FinalOutcome)
							assert.True(t, facts[0].Missing)
							var state VideoHealthState
							require.NoError(t, db.Where("channel_id = ?", channelID).First(&state).Error)
							assert.Equal(t, videosched.HealthBlocked, state.State)
						case "terminal_won", "late_terminal":
							assert.Equal(t, "success", facts[0].FinalOutcome)
							assert.False(t, facts[0].Missing)
						case "rejected_abandoned", "rejected_finished":
							assert.Equal(t, "upstream", facts[0].FinalOutcome)
							assert.False(t, facts[0].Missing)
						default:
							assert.Empty(t, facts[0].FinalOutcome)
							assert.False(t, facts[0].Missing)
						}
						var request VideoHealthRequest
						require.NoError(t, db.Where("request_id = ?", attempt.RequestID).First(&request).Error)
						switch scenario {
						case "expired", "rejected_abandoned":
							assert.Equal(t, "unknown", request.Outcome)
							assert.True(t, request.Missing)
						case "terminal_won", "late_terminal":
							assert.Equal(t, "success", request.Outcome)
							assert.False(t, request.Missing)
						case "rejected_finished":
							assert.Equal(t, "failure", request.Outcome)
						default:
							assert.Equal(t, "pending", request.Outcome)
							assert.False(t, request.Missing)
						}
					})
				}
			})
			t.Run("concurrent_durable_admission_across_models", func(t *testing.T) {
				for _, name := range []string{"race-a", "race-b"} {
					require.NoError(t, EnsureVideoHealthState(ctx, 41, name))
				}
				errors := make([]error, 2)
				barrier := make(chan struct{})
				var wg sync.WaitGroup
				for i, name := range []string{"race-a", "race-b"} {
					wg.Go(func() {
						<-barrier
						attempt := VideoHealthAttempt{RequestID: name, AttemptSeq: 1, ChannelID: 41, ModelName: name, StartedAt: start, WindowSeconds: 1800, StateVersion: 1, Flow: "explore", ValidationLimit: 1, SlotKey: name, SlotToken: name, SlotExpires: start + 3600}
						errors[i] = BeginVideoHealthAttempt(ctx, &attempt, true)
					})
				}
				close(barrier)
				wg.Wait()
				winners := 0
				for _, err := range errors {
					if err == nil {
						winners++
					}
				}
				assert.Equal(t, 1, winners, "independent models cannot each consume the same channel-wide last slot")
				var count int64
				require.NoError(t, db.Model(&VideoHealthAttempt{}).Where("channel_id = ?", 41).Count(&count).Error)
				assert.EqualValues(t, 1, count)
			})
			t.Run("durable_slots_survive_cache_loss_across_models", func(t *testing.T) {
				for _, name := range []string{"video-a", "video-b"} {
					require.NoError(t, EnsureVideoHealthState(ctx, 40, name))
				}
				a := VideoHealthAttempt{RequestID: "slot-a", AttemptSeq: 1, ChannelID: 40, ModelName: "video-a", StartedAt: start, WindowSeconds: 1800, StateVersion: 1, Flow: "explore", ValidationLimit: 1, SlotKey: "slot-40-0", SlotToken: "owner-a", SlotExpires: start + 3600}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &a, true))
				// A flushed cache could hand out the same physical slot to a new
				// owner. Its durable outstanding task must still consume the cap.
				b := VideoHealthAttempt{RequestID: "slot-b", AttemptSeq: 1, ChannelID: 40, ModelName: "video-b", StartedAt: start + 1, WindowSeconds: 1800, StateVersion: 1, Flow: "explore", ValidationLimit: 1, SlotKey: "slot-40-0", SlotToken: "owner-b", SlotExpires: start + 3600}
				require.Error(t, BeginVideoHealthAttempt(ctx, &b, true))
				var count int64
				require.NoError(t, db.Model(&VideoHealthAttempt{}).Where("channel_id = ?", 40).Count(&count).Error)
				assert.EqualValues(t, 1, count, "rejected before transport is not an actual attempt")
				require.NoError(t, ObserveVideoHealthAttempt(ctx, "slot-a", 1, nil, "accepted", "success", "success", start+2))
				require.NoError(t, BeginVideoHealthAttempt(ctx, &b, true))
			})
			t.Run("durable_task_outlives_validation_cache_ttl", func(t *testing.T) {
				require.NoError(t, EnsureVideoHealthState(ctx, 42, "video"))
				a := VideoHealthAttempt{RequestID: "long-validation", AttemptSeq: 1, ChannelID: 42, ModelName: "video", StartedAt: start, WindowSeconds: 1800, StateVersion: 1, Flow: "explore", ValidationLimit: 1, SlotKey: "long-slot", SlotToken: "old-owner", SlotExpires: start + 120, SubmitLeaseExpires: start + 300}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &a, true))
				b := VideoHealthAttempt{RequestID: "next-validation", AttemptSeq: 1, ChannelID: 42, ModelName: "video", StartedAt: start + 200, WindowSeconds: 1800, StateVersion: 1, Flow: "explore", ValidationLimit: 1, SlotKey: "long-slot", SlotToken: "new-owner", SlotExpires: start + 3600}
				require.ErrorIs(t, BeginVideoHealthAttempt(ctx, &b, true), ErrVideoHealthStateChanged, "a live submit retains its slot after the cache TTL")
				task := Task{TaskID: "long-running-task", Status: TaskStatusSubmitted, VideoHealthAttemptID: &a.ID}
				require.NoError(t, db.Create(&task).Error)
				b.StartedAt = start + 600
				require.ErrorIs(t, BeginVideoHealthAttempt(ctx, &b, true), ErrVideoHealthStateChanged, "task ownership survives both request and cache lease expiry")
				owners, err := ListVideoValidationOwners(ctx, 42, b.StartedAt)
				require.NoError(t, err)
				require.Len(t, owners, 1)
				assert.Equal(t, a.SlotToken, owners[0].SlotToken)
				require.NoError(t, db.Model(&task).Update("status", TaskStatusSuccess).Error)
				require.NoError(t, BeginVideoHealthAttempt(ctx, &b, true), "a terminal task no longer owns its expired slot")
			})
			for _, tc := range []struct {
				channel, accepted, succeeded int
				state                        string
			}{
				{1, 75, 60, videosched.HealthNormal}, {2, 70, 56, videosched.HealthBlocked},
			} {
				t.Run(fmt.Sprintf("independent_rates_%d", tc.channel), func(t *testing.T) {
					require.NoError(t, EnsureVideoHealthState(ctx, tc.channel, "video"))
					_, err := RefreshVideoHealthState(ctx, tc.channel, "video", p, start)
					require.NoError(t, err)
					var facts []VideoHealthAttempt
					for i := range 100 {
						fact := VideoHealthAttempt{RequestID: fmt.Sprintf("cohort-%d-%d", tc.channel, i), AttemptSeq: 1, ChannelID: tc.channel, ModelName: "video", StartedAt: start + 1, BatchStart: start, WindowSeconds: 1800, StateVersion: 1, ValidationRound: 1, Flow: "shadow", SubmitOutcome: "rejected", FinalOutcome: "upstream"}
						if i < tc.accepted {
							fact.SubmitOutcome = "accepted"
						}
						if i < tc.succeeded {
							fact.FinalOutcome = "success"
						}
						facts = append(facts, fact)
					}
					require.NoError(t, db.Create(&facts).Error)
					view, err := RefreshVideoHealthState(ctx, tc.channel, "video", p, start+1801)
					require.NoError(t, err)
					assert.Equal(t, tc.state, view.State)
					require.NotNil(t, view.Current)
					assert.Equal(t, .8, view.Current.GenerationRate())
					assert.Equal(t, float64(tc.succeeded)/100, view.Current.OverallRate())
					if tc.state == videosched.HealthNormal {
						require.NotNil(t, view.Qualification)
						expires := view.Qualification.ExpiresAt
						view, err = RefreshVideoHealthState(ctx, tc.channel, "video", p, start+2000)
						require.NoError(t, err)
						assert.Equal(t, expires, view.Qualification.ExpiresAt)
						pLonger := p
						pLonger.QualificationTTLSeconds *= 2
						view, err = RefreshVideoHealthState(ctx, tc.channel, "video", pLonger, expires+1)
						require.NoError(t, err)
						assert.Equal(t, videosched.HealthUnverified, view.State)
						assert.Equal(t, "evidence_expired", view.Reason)
						assert.Equal(t, expires, view.Qualification.ExpiresAt)
					} else {
						view, err = RefreshVideoHealthState(ctx, tc.channel, "video", p, start+40*86400)
						require.NoError(t, err)
						assert.Equal(t, videosched.HealthBlocked, view.State)
					}
				})
			}
			t.Run("closed_cohorts_and_low_traffic", func(t *testing.T) {
				require.NoError(t, EnsureVideoHealthState(ctx, 3, "video"))
				_, err := RefreshVideoHealthState(ctx, 3, "video", p, start)
				require.NoError(t, err)
				var last VideoHealthAttempt
				for i := range 20 {
					at := start + int64(i/2)*3600 + 1
					last = VideoHealthAttempt{RequestID: fmt.Sprintf("low-%d", i), AttemptSeq: 1, ChannelID: 3, ModelName: "video", StartedAt: at, BatchStart: at / 1800 * 1800, WindowSeconds: 1800, StateVersion: 1, ValidationRound: 1, Flow: "explore", SubmitOutcome: "accepted", FinalOutcome: "success"}
					if i == 19 {
						last.FinalOutcome = ""
					}
					require.NoError(t, db.Create(&last).Error)
					view, err := RefreshVideoHealthState(ctx, 3, "video", p, at+1800)
					require.NoError(t, err)
					assert.Equal(t, videosched.HealthUnverified, view.State)
				}
				// The slow task is still part of the submitted cohort, so 19 fast
				// successes cannot qualify it. A later open batch cannot poison it.
				future := VideoHealthAttempt{RequestID: "future-pending", AttemptSeq: 1, ChannelID: 3, ModelName: "video", StartedAt: start + 36000, BatchStart: start + 36000, WindowSeconds: 1800, StateVersion: 1, ValidationRound: 1, SubmitOutcome: "accepted"}
				require.NoError(t, db.Create(&future).Error)
				require.NoError(t, ObserveVideoHealthAttempt(ctx, last.RequestID, 1, nil, "accepted", "success", "success", start+36001))
				view, err := RefreshVideoHealthState(ctx, 3, "video", p, start+36002)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthNormal, view.State)
				require.NotNil(t, view.Qualification)
				assert.EqualValues(t, 20, view.Qualification.Submitted)
				assert.Equal(t, "cold_start", view.Qualification.Source)
				// A late failure revokes the certificate, while a duplicate success
				// from its old epoch can never clear the new block.
				require.NoError(t, ObserveVideoHealthAttempt(ctx, "future-pending", 1, nil, "accepted", "upstream", "host", start+37000))
				require.NoError(t, ObserveVideoHealthAttempt(ctx, last.RequestID, 1, nil, "accepted", "success", "success", start+37001))
				view, err = RefreshVideoHealthState(ctx, 3, "video", p, start+37002)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthBlocked, view.State)
				blockedVersion := view.StateVersion
				round, err := StartVideoHealthRecovery(ctx, 3, "video", blockedVersion, start+40000, p.ValidationPeriodSeconds)
				require.NoError(t, err)
				_, err = StartVideoHealthRecovery(ctx, 3, "video", blockedVersion, start+40001, p.ValidationPeriodSeconds)
				require.ErrorIs(t, err, ErrVideoHealthStateChanged)
				for i := range 20 {
					at := start + int64(i+12)*3600 + 1
					fact := VideoHealthAttempt{RequestID: fmt.Sprintf("recover-%d", i), AttemptSeq: 1, ChannelID: 3, ModelName: "video", StartedAt: at, WindowSeconds: 1800, StateVersion: round, Flow: "recover", ValidationLimit: 1, SlotKey: "recover-slot", SlotToken: fmt.Sprintf("owner-%d", i), SlotExpires: at + 3600}
					require.NoError(t, BeginVideoHealthAttempt(ctx, &fact, true))
					require.NoError(t, ObserveVideoHealthAttempt(ctx, fact.RequestID, 1, nil, "accepted", "success", "success", at+1))
				}
				view, err = RefreshVideoHealthState(ctx, 3, "video", p, start+33*3600)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthNormal, view.State)
				require.NotNil(t, view.Qualification)
				assert.Equal(t, "recovery", view.Qualification.Source)
				assert.EqualValues(t, 20, view.Qualification.Submitted)
				require.NoError(t, db.AutoMigrate(models...))
				viewAfterRestart, err := RefreshVideoHealthState(ctx, 3, "video", p, start+33*3600+1)
				require.NoError(t, err)
				assert.Equal(t, view.Qualification, viewAfterRestart.Qualification)
			})
			t.Run("sparse_round_expiry_keeps_limited_admission", func(t *testing.T) {
				require.NoError(t, EnsureVideoHealthState(ctx, 7, "video"))
				before, err := RefreshVideoHealthState(ctx, 7, "video", p, start)
				require.NoError(t, err)
				first := VideoHealthAttempt{RequestID: "sparse-before-expiry", AttemptSeq: 1, ChannelID: 7, ModelName: "video", StartedAt: start + 1, WindowSeconds: 1800, StateVersion: before.StateVersion, Flow: "explore", ValidationLimit: 1, SlotKey: "sparse-slot", SlotToken: "first", SlotExpires: start + 3600}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &first, true))
				require.NoError(t, ObserveVideoHealthAttempt(ctx, first.RequestID, 1, nil, "accepted", "success", "success", start+2))
				view, err := RefreshVideoHealthState(ctx, 7, "video", p, start+1801)
				require.NoError(t, err)
				require.NotNil(t, view.Current)
				assert.EqualValues(t, 1, view.Current.Succeeded)
				assert.Equal(t, videosched.HealthUnverified, view.State)
				after := start + int64(p.ValidationPeriodSeconds) + 1
				view, err = RefreshVideoHealthState(ctx, 7, "video", p, after)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthUnverified, view.State)
				assert.Equal(t, "insufficient_samples", view.Reason)
				assert.Greater(t, view.ValidationRound, before.ValidationRound)
				assert.Nil(t, view.Qualification)
				version, err := StartVideoHealthValidation(ctx, 7, "video", view.StateVersion, after, p, "explore")
				require.NoError(t, err)
				next := VideoHealthAttempt{RequestID: "sparse-after-expiry", AttemptSeq: 1, ChannelID: 7, ModelName: "video", StartedAt: after + 1, WindowSeconds: 1800, StateVersion: version, Flow: "explore", ValidationLimit: 1, SlotKey: "sparse-slot", SlotToken: "second", SlotExpires: after + 3600}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &next, true))
				require.NoError(t, ObserveVideoHealthAttempt(ctx, next.RequestID, 1, nil, "accepted", "success", "success", after+2))
				view, err = RefreshVideoHealthState(ctx, 7, "video", p, after+1801)
				require.NoError(t, err)
				require.NotNil(t, view.Current)
				assert.EqualValues(t, 1, view.Current.Succeeded, "the previous round does not pad the new evidence")
				assert.Equal(t, videosched.HealthUnverified, view.State)
				facts, err := ListVideoHealthAttempts(ctx, first.RequestID)
				require.NoError(t, err)
				require.Len(t, facts, 1, "expiry preserves historical facts")
			})
			t.Run("attempt_identity_retry_and_integrity", func(t *testing.T) {
				for _, ch := range []int{4, 5} {
					require.NoError(t, EnsureVideoHealthState(ctx, ch, "video"))
					_, err := RefreshVideoHealthState(ctx, ch, "video", p, start)
					require.NoError(t, err)
				}
				a := VideoHealthAttempt{RequestID: "retried", AttemptSeq: 1, ChannelID: 4, ModelName: "video", StartedAt: start + 1, WindowSeconds: 1800, StateVersion: 1, ValidationRound: 1, Flow: "explore", ValidationLimit: 1, SlotKey: "retry-4", SlotToken: "a", SlotExpires: start + 3600}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &a, true))
				require.Error(t, BeginVideoHealthAttempt(ctx, &a, true))
				require.NoError(t, ObserveVideoHealthAttempt(ctx, "retried", 1, nil, "rejected", "upstream", "upstream", start+2))
				b := VideoHealthAttempt{RequestID: "retried", AttemptSeq: 2, ChannelID: 5, ModelName: "video", StartedAt: start + 3, WindowSeconds: 1800, StateVersion: 1, ValidationRound: 1, Flow: "explore", ValidationLimit: 1, SlotKey: "retry-5", SlotToken: "b", SlotExpires: start + 3600}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &b, true))
				pk := int64(10)
				require.NoError(t, ObserveVideoHealthAttempt(ctx, "retried", 2, nil, "accepted", "success", "success", start+4))
				require.NoError(t, ObserveVideoHealthAttempt(ctx, "retried", 2, &pk, "accepted", "", "", start+5))
				require.NoError(t, FinishVideoHealthRequest(ctx, &VideoHealthRequest{RequestID: "retried", StartedAt: start, Mode: "on", ChannelID: 5, Outcome: "pending"}))
				facts, err := ListVideoHealthAttempts(ctx, "retried")
				require.NoError(t, err)
				require.Len(t, facts, 2)
				assert.Equal(t, "upstream", facts[0].FinalOutcome)
				assert.Equal(t, "success", facts[1].FinalOutcome)
				require.NotNil(t, facts[1].TaskPK)
				assert.Equal(t, pk, *facts[1].TaskPK)
				var request VideoHealthRequest
				require.NoError(t, db.First(&request, "request_id = ?", "retried").Error)
				assert.Equal(t, "success", request.Outcome)
				assert.Equal(t, 5, request.ChannelID, "request attribution follows the successful retry")
				assert.Equal(t, 2, request.LastAttemptSeq)
				stats, err := GetVideoReliabilityStats(ctx, VideoScheduleAuditFilter{Start: start * 1000, End: (start + 1800) * 1000, RequestID: "retried"})
				require.NoError(t, err)
				require.True(t, stats.Supported)
				assert.EqualValues(t, 2, stats.Attempts.Submitted)
				assert.Equal(t, videoAuditRate(1, 1), stats.GenerationSuccess)
				assert.Equal(t, videoAuditRate(1, 2), stats.ChannelCompletion)
				assert.Equal(t, videoAuditRate(1, 1), stats.RequestCompletion)
				assert.Equal(t, videoAuditRate(2, 2), stats.LimitedShare)
				// Missing persistent state cannot be reinterpreted as a new channel.
				require.NoError(t, db.Where("channel_id = ?", 4).Delete(&VideoHealthState{}).Error)
				require.ErrorIs(t, EnsureVideoHealthState(ctx, 4, "video"), gorm.ErrRecordNotFound)
				// An unknown outcome persists beyond both evidence and retention windows.
				unknown := VideoHealthAttempt{RequestID: "unknown", AttemptSeq: 1, ChannelID: 5, ModelName: "video", StartedAt: start + 10, WindowSeconds: 1800, StateVersion: 1, ValidationRound: 1, Flow: "explore", ValidationLimit: 1, SlotKey: "retry-5", SlotToken: "c", SlotExpires: start + 3600}
				require.NoError(t, BeginVideoHealthAttempt(ctx, &unknown, true))
				require.NoError(t, ObserveVideoHealthAttempt(ctx, "unknown", 1, nil, "unknown", "unknown", "unknown", start+11))
				require.NoError(t, CleanupVideoHealthFacts(ctx, start+40*86400))
				view, err := RefreshVideoHealthState(ctx, 5, "video", p, start+40*86400)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthBlocked, view.State)
				assert.Equal(t, "uncertain", view.Integrity)
				facts, err = ListVideoHealthAttempts(ctx, "unknown")
				require.NoError(t, err)
				require.Len(t, facts, 1)
			})
			t.Run("continuous_cohorts_activation_and_window_change", func(t *testing.T) {
				pWindow := p
				pWindow.WindowSeconds = 1800
				require.NoError(t, EnsureVideoHealthState(ctx, 6, "video"))
				_, err := RefreshVideoHealthState(ctx, 6, "video", pWindow, start)
				require.NoError(t, err)
				var facts []VideoHealthAttempt
				for i := range 40 {
					at := start + 1
					if i >= 20 {
						at += 1800
					}
					fact := VideoHealthAttempt{RequestID: fmt.Sprintf("continuous-%d", i), AttemptSeq: 1, ChannelID: 6, ModelName: "video", StartedAt: at, BatchStart: at / 1800 * 1800, WindowSeconds: 1800, StateVersion: 1, ValidationRound: 1, SubmitOutcome: "accepted", FinalOutcome: "success"}
					if i >= 20 {
						fact.FinalOutcome = ""
					}
					facts = append(facts, fact)
				}
				require.NoError(t, db.Create(&facts).Error)
				first, err := RefreshVideoHealthState(ctx, 6, "video", pWindow, start+1801)
				require.NoError(t, err)
				require.Equal(t, videosched.HealthNormal, first.State)
				require.NotNil(t, first.Qualification)
				for i := 20; i < 40; i++ {
					require.NoError(t, ObserveVideoHealthAttempt(ctx, fmt.Sprintf("continuous-%d", i), 1, nil, "accepted", "success", "success", start+3500))
				}
				next, err := RefreshVideoHealthState(ctx, 6, "video", pWindow, start+3601)
				require.NoError(t, err)
				require.Equal(t, videosched.HealthNormal, next.State)
				require.NotNil(t, next.Qualification)
				assert.EqualValues(t, 20, next.Qualification.Submitted)
				assert.Equal(t, start+1800, next.Qualification.BatchStart)
				assert.Greater(t, next.Qualification.ValidatedAt, first.Qualification.ValidatedAt)
				again, err := RefreshVideoHealthState(ctx, 6, "video", pWindow, start+3601)
				require.NoError(t, err)
				assert.Equal(t, next.Qualification, again.Qualification)
				pWindow.WindowSeconds = 3600
				changed, err := RefreshVideoHealthState(ctx, 6, "video", pWindow, start+3602)
				require.NoError(t, err)
				assert.Equal(t, next.Qualification, changed.Qualification)
				assert.Greater(t, changed.ValidationRound, next.ValidationRound)
				require.NoError(t, db.Transaction(BeginVideoHealthActivation))
				fresh, err := RefreshVideoHealthState(ctx, 6, "video", pWindow, start+3603)
				require.NoError(t, err)
				assert.Equal(t, videosched.HealthUnverified, fresh.State)
				assert.Greater(t, fresh.ValidationRound, changed.ValidationRound)
				_, err = StartVideoHealthValidation(ctx, 6, "video", fresh.StateVersion, start+3603, p, "revalidate")
				require.NoError(t, err, "same-epoch admission must work even when no business field changes on MySQL")
				var blocked VideoHealthState
				require.NoError(t, db.Where("channel_id = ?", 5).First(&blocked).Error)
				assert.Equal(t, videosched.HealthBlocked, blocked.State, "activation preserves unresolved faults")
			})
		})
	}
}
