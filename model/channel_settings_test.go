package model

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	filterdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestChannelValidateSettingsRejectsInvalidHTTPTransport(t *testing.T) {
	tests := []struct {
		name    string
		setting dto.ChannelSettings
		wantErr string
	}{
		{
			name:    "auto with shards is valid",
			setting: dto.ChannelSettings{HTTPProtocol: "auto", HTTP2ConnectionShards: 4},
		},
		{
			name:    "http1 with shards greater than one rejected",
			setting: dto.ChannelSettings{HTTPProtocol: "http1", HTTP2ConnectionShards: 2},
			wantErr: "http2_connection_shards",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{}
			channel.SetSetting(tt.setting)
			err := channel.ValidateSettings()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestChannelValidateSettingsVideoSchedulingCostBound(t *testing.T) {
	orig := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = orig })
	common.QuotaPerUnit = 500 * 1000.0
	bound := float64(common.MaxWalletQuota) / common.QuotaPerUnit

	channelWithPrice := func(price float64) *Channel {
		return &Channel{OtherSettings: fmt.Sprintf(`{"video_scheduling":{"models":{"m":{"mode":"per_video","prices":{"*":%v}}}}}`, price)}
	}
	require.NoError(t, channelWithPrice(bound).ValidateSettings())
	require.ErrorContains(t, channelWithPrice(bound*1.000001).ValidateSettings(), "price for tier")

	common.QuotaPerUnit = 1e-300
	require.ErrorContains(t, channelWithPrice(1).ValidateSettings(), "cost bound")
	require.NoError(t, (&Channel{OtherSettings: `{"tool_loss_policy":"allow"}`}).ValidateSettings(), "channels without video_scheduling skip the bound")
}

func TestChannelVideoSchedulingNormalizesSavedResolutionTiers(t *testing.T) {
	channel := &Channel{OtherSettings: `{"future_setting":{"id":9007199254740993},"tool_loss_policy":"allow","video_scheduling":{"models":{"video-unified":{"mode":"per_second","prices":{"2160p":0.03},"allowed_seconds_by_resolution":{"2160p":[5,10]}}}}}`}
	require.NoError(t, channel.ValidateSettings())
	assert.JSONEq(t, `{"future_setting":{"id":9007199254740993},"tool_loss_policy":"allow","video_scheduling":{"quality":0,"capacity":0,"models":{"video-unified":{"mode":"per_second","prices":{"4k":0.03},"allowed_seconds_by_resolution":{"4k":[5,10]}}}}}`, channel.OtherSettings)
	saved := channel.OtherSettings
	require.NoError(t, channel.ValidateSettings())
	assert.Equal(t, saved, channel.OtherSettings, "validation is idempotent")
	channel.OtherSettings = `{"video_scheduling":{"models":{"m":{"mode":"per_video","prices":{"2160p":0.1,"4k":0.2}}}}}`
	previous := channel.OtherSettings
	require.Error(t, channel.ValidateSettings())
	assert.Equal(t, previous, channel.OtherSettings, "invalid aliases must not mutate saved settings")
}

func TestAdvancedCustomChannelRequiresModelListRouteOnlyWhenUpdateChecksEnabled(t *testing.T) {
	inferenceRoute := dto.AdvancedCustomRoute{
		IncomingPath: "/v1/chat/completions",
		UpstreamPath: "/v1/chat/completions",
		Converter:    "none",
	}

	tests := []struct {
		name          string
		checksEnabled bool
		routes        []dto.AdvancedCustomRoute
		wantErr       string
	}{
		{
			name:   "legacy channel without discovery route remains valid",
			routes: []dto.AdvancedCustomRoute{inferenceRoute},
		},
		{
			name:          "enabled checks require discovery route",
			checksEnabled: true,
			routes:        []dto.AdvancedCustomRoute{inferenceRoute},
			wantErr:       dto.AdvancedCustomModelListPath,
		},
		{
			name:          "enabled checks accept discovery route",
			checksEnabled: true,
			routes: []dto.AdvancedCustomRoute{
				inferenceRoute,
				{
					IncomingPath: dto.AdvancedCustomModelListPath,
					UpstreamPath: dto.AdvancedCustomModelListPath,
					Converter:    "none",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{Type: constant.ChannelTypeAdvancedCustom}
			channel.SetOtherSettings(dto.ChannelOtherSettings{
				UpstreamModelUpdateCheckEnabled: tt.checksEnabled,
				AdvancedCustom: &dto.AdvancedCustomConfig{
					Routes: tt.routes,
				},
			})

			err := channel.ValidateSettings()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Video scheduling calibration reads scheduled channels and active task counts;
// both queries must behave the same on every supported database.
func TestVideoSchedulingCalibrationQueries(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "video.db"))
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
			// The prefix keeps the fixture off the real channels/tasks tables.
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "video_sched_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.Migrator().DropTable(&Channel{}, &Task{}))
			require.NoError(t, db.AutoMigrate(&Channel{}, &Task{}))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&Channel{}, &Task{})) })
			var version string
			if dialect == "sqlite" {
				require.NoError(t, db.Raw("select sqlite_version()").Scan(&version).Error)
			} else {
				require.NoError(t, db.Raw("select version()").Scan(&version).Error)
			}
			t.Logf("%s version: %s", dialect, version)
			previousDB := DB
			DB = db
			t.Cleanup(func() { DB = previousDB })

			scheduled := &Channel{Name: "scheduled", Key: "k", OtherSettings: `{"video_scheduling":{"capacity_group":"acct","models":{"m":{"mode":"per_video","prices":{"*":1}}}}}`}
			plain := &Channel{Name: "plain", Key: "k", OtherSettings: `{"tool_loss_policy":"allow"}`}
			corrupt := &Channel{Name: "corrupt", Key: "k", OtherSettings: `{"video_scheduling":`}
			for _, channel := range []*Channel{scheduled, plain, corrupt} {
				require.NoError(t, db.Create(channel).Error)
			}
			configs, err := GetVideoScheduledChannels()
			require.NoError(t, err)
			require.Len(t, configs, 1, "only decodable scheduling configs are returned")
			assert.Equal(t, "acct", configs[scheduled.Id].CapacityGroup)

			channels, groups, err := CountActiveScheduledTasks()
			require.NoError(t, err)
			assert.Empty(t, channels)
			assert.Empty(t, groups)

			for i, row := range []struct {
				channel  int
				status   TaskStatus
				progress string
				group    string
				tracked  bool
			}{
				{scheduled.Id, TaskStatusSubmitted, "", "acct", true}, {scheduled.Id, TaskStatusInProgress, "40%", "old", true},
				{scheduled.Id, TaskStatusUnknown, "", "", true}, {scheduled.Id, TaskStatusQueued, "", "", false},
				{scheduled.Id, TaskStatusSuccess, "100%", "acct", true}, {scheduled.Id, TaskStatusFailure, "100%", "acct", true},
				{scheduled.Id, TaskStatusInProgress, "100%", "acct", true},
				{plain.Id, TaskStatusQueued, "", "acct", true},
			} {
				task := &Task{TaskID: fmt.Sprintf("video-%d", i), ChannelId: row.channel, Status: row.status, Progress: row.progress}
				if row.tracked {
					task.PrivateData.SchedulingSummary = &TaskSchedulingSummary{Model: "m", CapacityGroup: row.group}
				}
				require.NoError(t, db.Create(task).Error)
			}
			channels, groups, err = CountActiveScheduledTasks()
			require.NoError(t, err)
			assert.Equal(t, map[int]int64{scheduled.Id: 3, plain.Id: 1}, channels, "terminal, summary-less and stuck 100% tasks are not counted")
			assert.Equal(t, map[string]int64{"acct": 2, "old": 1}, groups, "groups follow the capacity group saved at submit, whatever the channel's current config")
		})
	}
}

func TestInferencePresetSettingsAndDatabaseRoundTrip(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "presets.db"))
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
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			table := db.Table("inference_preset_channels").Session(&gorm.Session{})
			require.NoError(t, table.AutoMigrate(&Channel{}))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable("inference_preset_channels")) })
			var version string
			if dialect == "sqlite" {
				require.NoError(t, db.Raw("select sqlite_version()").Scan(&version).Error)
			} else {
				require.NoError(t, db.Raw("select version()").Scan(&version).Error)
			}
			t.Logf("%s version: %s", dialect, version)
			for _, channelType := range []int{constant.ChannelTypeVLLM, constant.ChannelTypeSGLang} {
				t.Run(fmt.Sprint(channelType), func(t *testing.T) {
					channel := &Channel{Type: channelType, Key: "EMPTY", Name: "inference", Status: common.ChannelStatusEnabled}
					require.NoError(t, channel.ValidateSettings())
					require.NotNil(t, channel.GetOtherSettings().AdvancedCustom)
					require.NoError(t, table.Create(channel).Error)
					for range 2 {
						var loaded Channel
						require.NoError(t, table.First(&loaded, channel.Id).Error)
						assert.Equal(t, channelType, loaded.Type)
						assert.Empty(t, loaded.OtherSettings)
						defaults := loaded.GetOtherSettings().AdvancedCustom
						require.NotNil(t, defaults)
						assert.True(t, defaults.SupportsPath("/v1/messages"))
						assert.Empty(t, loaded.OtherSettings, "reading defaults must not rewrite saved settings")
						defaults.Routes[0].UpstreamPath = "/changed-locally"
						assert.Equal(t, "/v1/chat/completions", loaded.GetOtherSettings().AdvancedCustom.Routes[0].UpstreamPath)
					}
					settings := dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/chat/completions", UpstreamPath: "/custom/chat", Models: []string{"allowed"}, Auth: &dto.AdvancedCustomRouteAuth{Type: "none"}}}}}
					channel.SetOtherSettings(settings)
					require.NoError(t, channel.ValidateSettings())
					require.NoError(t, table.Save(channel).Error)
					for range 2 {
						var loaded Channel
						require.NoError(t, table.First(&loaded, channel.Id).Error)
						actual := loaded.GetOtherSettings().AdvancedCustom
						require.Equal(t, common.GetAdvancedCustomPreset(channelType), actual, "named channels must ignore editable advanced_custom overrides")
						assert.Equal(t, channel.OtherSettings, loaded.OtherSettings)
						for _, tc := range []struct {
							path, model string
							allowed     bool
						}{
							{"/v1/chat/completions", "allowed", true},
							{"/v1/chat/completions", "other", true},
							{"/v1/messages", "allowed", true},
							{"/v1/images/generations", "allowed", false},
						} {
							ok, _ := ChannelSatisfiesFilters(&loaded, tc.model, []filterdto.ChannelFilter{{Kind: filterdto.FilterRequestPath, RequestPath: tc.path}})
							assert.Equal(t, tc.allowed, ok)
						}
						endpoints := getPricingEndpointTypesForAbility(AbilityWithChannel{ChannelType: channelType, Ability: Ability{Model: "allowed", ChannelId: loaded.Id}}, map[int]*dto.AdvancedCustomConfig{loaded.Id: actual})
						expectedEndpoints := []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse, constant.EndpointTypeAnthropic, constant.EndpointTypeEmbeddings}
						if channelType == constant.ChannelTypeSGLang {
							expectedEndpoints = append(expectedEndpoints, constant.EndpointTypeJinaRerank)
						}
						assert.ElementsMatch(t, expectedEndpoints, endpoints)
					}
				})
			}
		})
	}
}
