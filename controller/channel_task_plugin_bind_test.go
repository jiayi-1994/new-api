package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTaskPluginBindChannelTest(t *testing.T) {
	t.Helper()
	wasMaster := common.IsMasterNode
	common.IsMasterNode = true
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	originalDB, originalLogDB := model.DB, model.LOG_DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.Log{}, &model.AuditLog{}, &model.User{}))
	model.DB = database
	model.LOG_DB = database
	require.NoError(t, authz.Init(database))
	t.Cleanup(func() {
		common.IsMasterNode = wasMaster
		common.RedisEnabled = previousRedisEnabled
		model.DB = originalDB
		model.LOG_DB = originalLogDB
	})
}

func postAddChannel(t *testing.T, userID, role int, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", userID)
	context.Set("role", role)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/channel", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	AddChannel(context)
	return recorder
}

func TestAddChannelTaskPluginRequiresBindPermission(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	const key = "channel-bind"
	source := `
export const meta = {apiVersion: 1, key: "channel-bind", name: "Bind", version: "1.0.0", author: {name: "Test"}, models: ["doc"], fetchMode: "per_task"};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`
	_, err := jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(key) })

	taskPluginBody := `{"mode":"single","channel":{"type":61,"name":"plugin-channel","key":"sk","models":"doc","group":"default","base_url":"https://example.com","setting":"{\"task_plugin_key\":\"channel-bind\"}"}}`
	openaiBody := `{"mode":"single","channel":{"type":1,"name":"openai-channel","key":"sk","models":"gpt","group":"default"}}`

	adminDenied := postAddChannel(t, 2, common.RoleAdminUser, taskPluginBody)
	assert.Contains(t, adminDenied.Body.String(), "task plugin channels require the task_plugin.bind permission")
	assert.Contains(t, adminDenied.Body.String(), `"success":false`)

	rootAllowed := postAddChannel(t, 1, common.RoleRootUser, taskPluginBody)
	assert.Contains(t, rootAllowed.Body.String(), `"success":true`)
	assert.NotContains(t, rootAllowed.Body.String(), "task_plugin.bind")

	adminOtherType := postAddChannel(t, 2, common.RoleAdminUser, openaiBody)
	assert.Contains(t, adminOtherType.Body.String(), `"success":true`)
	assert.NotContains(t, adminOtherType.Body.String(), "task_plugin.bind")
}

func TestUpdateChannelTaskPluginRequiresBindPermission(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	const key = "channel-bind-update"
	source := `
export const meta = {apiVersion: 1, key: "channel-bind-update", name: "Bind", version: "1.0.0", author: {name: "Test"}, models: ["doc"], fetchMode: "per_task"};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`
	_, err := jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(key) })

	baseURL := "https://example.com"
	setting := `{"task_plugin_key":"channel-bind-update"}`
	channel := model.Channel{
		Type:    constant.ChannelTypeTaskPlugin,
		Status:  common.ChannelStatusEnabled,
		Name:    "existing-plugin",
		Models:  "doc",
		Group:   "default",
		Key:     "sk",
		BaseURL: &baseURL,
		Setting: &setting,
	}
	require.NoError(t, channel.Insert())

	payload := fmt.Sprintf(
		`{"id":%d,"type":61,"name":"existing-plugin","key":"sk","models":"doc","group":"default","base_url":"https://example.com","setting":"{\"task_plugin_key\":\"channel-bind-update\"}"}`,
		channel.Id,
	)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", 2)
	context.Set("role", common.RoleAdminUser)
	context.Request = httptest.NewRequest(http.MethodPut, "/api/channel", strings.NewReader(payload))
	context.Request.Header.Set("Content-Type", "application/json")
	UpdateChannel(context)
	assert.Contains(t, recorder.Body.String(), "task plugin channels require the task_plugin.bind permission")
	assert.Contains(t, recorder.Body.String(), `"success":false`)
}

func TestAddChannelTaskPluginPersistsPluginDefaultBaseURLAndAuditsSource(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	for key, baseURLField := range map[string]string{"bind-default-url": `baseUrl: "http://10.0.0.5:8000/",`, "bind-no-default": ""} {
		source := fmt.Sprintf(`
export const meta = {apiVersion: 1, key: %q, name: "Bind", version: "1.0.0", author: {name: "Test"}, %s models: ["doc"], fetchMode: "per_task"};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`, key, baseURLField)
		_, err := jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
		require.NoError(t, err)
		t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(key) })
	}
	body := func(pluginKey string) string {
		return fmt.Sprintf(`{"mode":"single","channel":{"type":61,"name":"%s","key":"sk","models":"doc","group":"default","setting":"{\"task_plugin_key\":\"%s\"}"}}`, pluginKey, pluginKey)
	}

	noDefault := postAddChannel(t, 1, common.RoleRootUser, body("bind-no-default"))
	assert.Contains(t, noDefault.Body.String(), "base URL is required for task plugin channels")

	filled := postAddChannel(t, 1, common.RoleRootUser, body("bind-default-url"))
	require.Contains(t, filled.Body.String(), `"success":true`)
	var created model.Channel
	require.NoError(t, model.DB.Where("name = ?", "bind-default-url").First(&created).Error)
	require.NotNil(t, created.BaseURL)
	assert.Equal(t, "http://10.0.0.5:8000", *created.BaseURL, "the normalized plugin default is stored on the channel row")

	var audits []model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "channel.create").Find(&audits).Error)
	encoded, err := common.Marshal(audits)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"base_url_source":"plugin_default"`)
}

func putUpdateChannel(t *testing.T, userID, role int, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", userID)
	context.Set("role", role)
	context.Request = httptest.NewRequest(http.MethodPut, "/api/channel", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	UpdateChannel(context)
	return recorder
}

func TestNewAPIChannelPluginBindingsRequireBindPermission(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	const key = "gateway-bind"
	source := `
export const meta = {apiVersion: 1, key: "gateway-bind", name: "Gateway", version: "1.0.0", author: {name: "Test"}, models: ["gateway-doc"], fetchMode: "per_task", upstreams: ["vendor", "new_api"]};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`
	_, err := jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(key) })

	bound := `{"mode":"single","channel":{"type":60,"name":"gateway","key":"sk","models":"gateway-doc","group":"default","base_url":"https://gateway.example","setting":"{\"task_extend_plugin_keys\":[\"gateway-bind\"]}"}}`
	unbound := `{"mode":"single","channel":{"type":60,"name":"plain-gateway","key":"sk","models":"gpt","group":"default","base_url":"https://gateway.example"}}`
	adminDenied := postAddChannel(t, 2, common.RoleAdminUser, bound)
	assert.Contains(t, adminDenied.Body.String(), "task plugin channels require the task_plugin.bind permission")
	rootAllowed := postAddChannel(t, 1, common.RoleRootUser, bound)
	assert.Contains(t, rootAllowed.Body.String(), `"success":true`)
	adminUnbound := postAddChannel(t, 2, common.RoleAdminUser, unbound)
	assert.Contains(t, adminUnbound.Body.String(), `"success":true`, "a gateway channel without plugin bindings needs no bind permission")

	baseURL := "https://gateway.example"
	setting := `{"task_extend_plugin_keys":["gateway-bind"]}`
	channel := model.Channel{Type: constant.ChannelTypeNewAPI, Status: common.ChannelStatusEnabled, Name: "existing-gateway", Models: "gateway-doc", Group: "default", Key: "sk", BaseURL: &baseURL, Setting: &setting}
	require.NoError(t, channel.Insert())
	update := func(name, setting string) string {
		return fmt.Sprintf(`{"id":%d,"type":60,"name":%q,"key":"sk","models":"gateway-doc","group":"default","base_url":"https://gateway.example","setting":%q}`, channel.Id, name, setting)
	}
	unchanged := putUpdateChannel(t, 2, common.RoleAdminUser, update("renamed-gateway", setting))
	assert.Contains(t, unchanged.Body.String(), `"success":true`, "resubmitting the stored bindings does not need the bind permission")
	assert.NotContains(t, unchanged.Body.String(), "task_plugin.bind")
	rebound := putUpdateChannel(t, 2, common.RoleAdminUser, update("renamed-gateway", `{"task_extend_plugin_keys":[]}`))
	assert.Contains(t, rebound.Body.String(), "task plugin channels require the task_plugin.bind permission")
	rootRebound := putUpdateChannel(t, 1, common.RoleRootUser, update("renamed-gateway", `{"task_extend_plugin_keys":[]}`))
	assert.Contains(t, rootRebound.Body.String(), `"success":true`)

	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("setting", setting).Error)
	copyChannel := func(userID, role int) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Set("id", userID)
		context.Set("role", role)
		context.Params = gin.Params{{Key: "id", Value: fmt.Sprint(channel.Id)}}
		context.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/channel/copy/%d", channel.Id), nil)
		CopyChannel(context)
		return recorder
	}
	assert.Contains(t, copyChannel(2, common.RoleAdminUser).Body.String(), "task plugin channels require the task_plugin.bind permission")
	assert.Contains(t, copyChannel(1, common.RoleRootUser).Body.String(), `"success":true`)
}

func videoScheduleAdminRequest(t *testing.T, handler gin.HandlerFunc, method, target, body string, params ...gin.Param) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", 1)
	context.Set("role", common.RoleRootUser)
	context.Params = params
	context.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	handler(context)
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response), recorder.Body.String())
	return response
}

// The simulator runs a request through the same entry, assembly and decision
// as live traffic. Its fingerprint only repeats for an equal decision input,
// and the segment hashes name the part that changed.
func TestVideoScheduleSimulateAndSchedulable(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	previousMemory := common.MemoryCacheEnabled
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousMemory
		model.InitChannelCache()
	})
	common.MemoryCacheEnabled = true
	for i, plugin := range []string{"megabyai", "seedance-hjmie", "seedance-hjmie"} {
		priority, weight, autoBan := int64(0), uint(100), 0
		binding := fmt.Sprintf(`{"task_plugin_key":%q}`, plugin)
		settings := `{"video_scheduling":{"quality":0.5,"models":{"videos-fast":{"mode":"per_second","prices":{"720p":0.05}}}}}`
		if i == 2 {
			settings = "" // a channel scheduling never considers
		}
		channel := &model.Channel{Id: 7701 + i, Type: constant.ChannelTypeTaskPlugin, Key: "k", Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("sim-%d", i),
			Weight: &weight, Priority: &priority, AutoBan: &autoBan, Models: "videos-fast", Group: "default", Setting: &binding, OtherSettings: settings}
		require.NoError(t, model.DB.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(model.DB))
	}
	model.InitChannelCache()

	snapshot := *operation_setting.GetVideoSchedulingSetting()
	snapshot.UnknownSellPolicy = "relative"
	simulate := func(change func(map[string]any)) map[string]any {
		body := map[string]any{
			"group": "default", "entry": "protocol", "protocol": "openai_video", "seed": 42, "now": "2026-10-01T12:00:00Z",
			"request_body":    map[string]any{"model": "videos-fast", "prompt": "cat", "seconds": 8, "size": "1280x720"},
			"config_snapshot": snapshot,
			"health_override": map[string]any{"7701": map[string]any{"probe": map[string]any{"last_probe_at": 100, "consecutive_fails": 1}}},
		}
		if change != nil {
			change(body)
		}
		encoded, err := common.Marshal(body)
		require.NoError(t, err)
		response := videoScheduleAdminRequest(t, SimulateVideoSchedule, http.MethodPost, "/api/channel/video_schedule/simulate", string(encoded))
		require.Equal(t, true, response["success"], response["message"])
		return response["data"].(map[string]any)
	}

	// Every simulation, accepted or rejected by the entry, releases the body
	// storage its prep middlewares created.
	buffers := common.GetDiskCacheStats().ActiveMemoryBuffers
	first := simulate(nil)
	assert.Equal(t, "videos-fast", first["model"])
	assert.Equal(t, "2026-10-01T12:00:00Z", first["now"])
	assert.Equal(t, float64(42), first["seed"])
	candidates := first["candidates"].([]any)
	require.Len(t, candidates, 3)
	rows := map[float64]map[string]any{}
	for _, candidate := range candidates {
		row := candidate.(map[string]any)
		rows[row["id"].(float64)] = row
	}
	assert.Equal(t, "megabyai", rows[7701]["plugin"])
	assert.Equal(t, "seedance-hjmie", rows[7702]["plugin"])
	assert.InDelta(t, 0.4, rows[7701]["cost_usd"], 1e-9, "8 seconds at $0.05")
	assert.Equal(t, "not schedulable: no cost table", rows[7703]["excluded"])
	assert.NotZero(t, first["recommended"])

	again := simulate(nil)
	assert.Equal(t, first["fingerprint"], again["fingerprint"])
	assert.Equal(t, first["segments"], again["segments"])
	assert.Equal(t, first["recommended"], again["recommended"])

	for _, tc := range []struct {
		name    string
		segment string
		change  func(map[string]any)
	}{
		{"probe state", "probe", func(body map[string]any) {
			body["health_override"] = map[string]any{"7701": map[string]any{"probe": map[string]any{"last_probe_at": 100, "consecutive_fails": 2}}}
		}},
		{"probe cooldown", "settings", func(body map[string]any) {
			changed := snapshot
			changed.ProbeCooldownSec = 600
			body["config_snapshot"] = changed
		}},
		{"probe slots", "slots", func(body map[string]any) { body["slot_override"] = map[string]any{"7701": 1} }},
		{"in-flight", "candidates", func(body map[string]any) {
			body["inflight_override"] = map[string]any{"channels": map[string]any{"7702": 1}}
		}},
		{"seed", "seed", func(body map[string]any) { body["seed"] = 43 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := simulate(tc.change)
			assert.NotEqual(t, first["fingerprint"], changed["fingerprint"])
			segments, changedSegments := first["segments"].(map[string]any), changed["segments"].(map[string]any)
			for name, hash := range segments {
				if name == tc.segment {
					assert.NotEqual(t, hash, changedSegments[name], name)
				} else {
					assert.Equal(t, hash, changedSegments[name], name)
				}
			}
		})
	}

	for _, body := range []string{
		`{"group":"auto","entry":"protocol","protocol":"openai_video","request_body":{"model":"videos-fast"}}`,
		`{"group":"default","entry":"batch","request_body":{"model":"videos-fast"}}`,
		`{"group":"default","entry":"protocol","protocol":"openai_video","request_body":{"model":"videos-fast"}}`, // no plugin accepts a promptless body
	} {
		response := videoScheduleAdminRequest(t, SimulateVideoSchedule, http.MethodPost, "/api/channel/video_schedule/simulate", body)
		assert.Equal(t, false, response["success"], body)
	}
	assert.Equal(t, buffers, common.GetDiskCacheStats().ActiveMemoryBuffers)

	// Health samples sit in buckets sized by the live window, so another window
	// cannot be read back and is refused rather than silently ignored.
	otherWindow := snapshot
	otherWindow.WindowSeconds = snapshot.WindowSeconds * 2
	encoded, err := common.Marshal(map[string]any{
		"group": "default", "entry": "protocol", "protocol": "openai_video", "config_snapshot": otherWindow,
		"request_body": map[string]any{"model": "videos-fast", "prompt": "cat", "seconds": 8, "size": "1280x720"},
	})
	require.NoError(t, err)
	response := videoScheduleAdminRequest(t, SimulateVideoSchedule, http.MethodPost, "/api/channel/video_schedule/simulate", string(encoded))
	assert.Equal(t, false, response["success"])
	assert.Contains(t, response["message"], "window_seconds")

	schedulable := videoScheduleAdminRequest(t, GetVideoSchedulable, http.MethodGet, "/api/channel/video_schedule/schedulable?plugin=seedance-hjmie", "")
	require.Equal(t, true, schedulable["success"])
	data := schedulable["data"].(map[string]any)
	assert.Equal(t, true, data["describe_spec"])
	assert.Contains(t, data["models"], map[string]any{"model": "videos-fast", "static_blockers": []any{}})
	schedulable = videoScheduleAdminRequest(t, GetVideoSchedulable, http.MethodGet, "/api/channel/video_schedule/schedulable?plugin=sora", "")
	assert.Equal(t, false, schedulable["data"].(map[string]any)["describe_spec"])

	health := videoScheduleAdminRequest(t, GetChannelVideoHealth, http.MethodGet, "/api/channel/7701/video_health", "", gin.Param{Key: "id", Value: "7701"})
	require.Equal(t, true, health["success"])
	view := health["data"].(map[string]any)
	assert.Contains(t, view, "submit")
	assert.Contains(t, view["models"], "videos-fast")
	health = videoScheduleAdminRequest(t, GetChannelVideoHealth, http.MethodGet, "/api/channel/7703/video_health", "", gin.Param{Key: "id", Value: "7703"})
	assert.Nil(t, health["data"], "a channel without a cost table has no scheduling health")

	list := videoScheduleAdminRequest(t, GetAllChannels, http.MethodGet, "/api/channel/?p=1&page_size=10", "")
	require.Equal(t, true, list["success"])
	for _, item := range list["data"].(map[string]any)["items"].([]any) {
		channel := item.(map[string]any)
		if channel["id"] == float64(7703) {
			assert.NotContains(t, channel, "video_health")
		} else {
			assert.Contains(t, channel, "video_health")
			assert.Equal(t, fmt.Sprintf("sim-%d", int(channel["id"].(float64))-7701), channel["name"])
		}
	}
}
