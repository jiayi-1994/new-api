package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/plugins"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRelayChannelDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousCache := common.MemoryCacheEnabled
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.Channel{}))
	model.DB = database
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.MemoryCacheEnabled = previousCache
		require.NoError(t, sqlDB.Close())
	})
	return database
}

func TestApplyChannelPinPreservesOriginTasksAndRetryMode(t *testing.T) {
	database := setupRelayChannelDB(t)
	channel := &model.Channel{Name: "origin-channel", Key: "sk-test", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeDoubaoVideo}
	require.NoError(t, database.Create(channel).Error)
	originTask := &model.Task{
		TaskID: "task-lock", ChannelId: channel.Id, Action: "text_to_video", Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{UpstreamTaskID: "upstream-task-lock"},
		Data:        []byte(`{"id":"upstream-task-lock"}`),
	}

	for _, tc := range []struct {
		name     string
		tokenPin bool
		apply    func(*gin.Context, *relaycommon.RelayInfo) *dto.TaskError
	}{
		{name: "origin affinity", apply: ApplyOriginTaskAffinity},
		{name: "same channel retry", apply: ApplyChannelPin},
		{name: "token pin suppresses channel lock", tokenPin: true, apply: ApplyChannelPin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyOriginTasks, []*model.Task{originTask})
			constraints := service.GetChannelConstraints(c)
			constraints.AddPin(dto.ChannelPin{ChannelId: channel.Id, Source: dto.PinSourceOriginTask, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel})
			if tc.tokenPin {
				constraints.AddPin(dto.ChannelPin{ChannelId: channel.Id, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
			}
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			require.Nil(t, tc.apply(c, info))
			if tc.tokenPin {
				assert.Nil(t, info.LockedChannel)
			} else {
				locked, ok := info.LockedChannel.(*model.Channel)
				require.True(t, ok)
				require.NotNil(t, locked)
				assert.Equal(t, channel.Id, locked.Id)
			}
			require.Len(t, info.OriginTasks, 1)
			assert.Equal(t, "task-lock", info.OriginTasks[0].TaskID)
			assert.Equal(t, "upstream-task-lock", info.OriginTasks[0].UpstreamTaskID)
			assert.Equal(t, "text_to_video", info.OriginTasks[0].Action)
			assert.Equal(t, string(model.TaskStatusSuccess), info.OriginTasks[0].Status)
			assert.Equal(t, []byte(originTask.Data), info.OriginTasks[0].Data)
		})
	}
}

func TestTaskModel2DtoNormalizesLegacyAction(t *testing.T) {
	task := &model.Task{Action: "firstTailGenerate"}

	dtoTask := TaskModel2Dto(task)

	assert.Equal(t, constant.TaskActionFirstTailToVideo, dtoTask.Action)
	assert.Equal(t, "firstTailGenerate", task.Action)
}

const mappingOrderSubmitPlugin = `
export const meta = {apiVersion:1,key:"maporder",name:"Map Order",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) {
  return {url: ctx.baseUrl+"/submit", method:"POST", body:{upstreamModel: ctx.upstreamModel, model: ctx.model}, action:"text_to_video"};
}
export function parseSubmitResponse(){return {taskId:"1"};}
export function buildQueryRequest(){return {url:"https://provider.example"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
`

const mappingOrderRewritePlugin = `
export const meta = {apiVersion:1,key:"maporder-rw",name:"Map Order RW",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) {
  return {url: ctx.baseUrl+"/submit", method:"POST", body:{upstreamModel: ctx.upstreamModel}, rewriteModel:"rewritten"};
}
export function parseSubmitResponse(){return {taskId:"1"};}
export function buildQueryRequest(){return {url:"https://provider.example"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
`

func pinMappingOrderPlugin(t *testing.T, c *gin.Context, source string) {
	t.Helper()
	plugin, err := pluginruntime.NewRegistry().Register(source, pluginruntime.Options{})
	require.NoError(t, err)
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
}

func newTaskSubmitContext(t *testing.T, originalModel, mapping string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, originalModel)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://provider.example")
	if mapping != "" {
		c.Set("model_mapping", mapping)
	}
	c.Set("task_request", map[string]any{"prompt": "p"})
	return c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
}

func TestRelayTaskSubmitMapsBeforeValidateWhenOriginSet(t *testing.T) {
	const mapping = `{"alias-model":"mid-model","mid-model":"declared-model"}`

	c, info := newTaskSubmitContext(t, "alias-model", mapping)
	pinMappingOrderPlugin(t, c, mappingOrderSubmitPlugin)
	info.OriginModelName = "alias-model"

	_, taskErr := RelayTaskSubmit(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_price_error", taskErr.Code)
	assert.Equal(t, "alias-model", info.OriginModelName)
	assert.Equal(t, "declared-model", info.UpstreamModelName)
	assert.True(t, info.IsModelMapped)
}

func TestRelayTaskSubmitDeclaredNameWithoutMappingIsUnchanged(t *testing.T) {
	c, info := newTaskSubmitContext(t, "declared-model", "")
	pinMappingOrderPlugin(t, c, mappingOrderSubmitPlugin)
	info.OriginModelName = "declared-model"

	_, taskErr := RelayTaskSubmit(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_price_error", taskErr.Code)
	assert.Equal(t, "declared-model", info.OriginModelName)
	assert.Equal(t, "declared-model", info.UpstreamModelName)
	assert.False(t, info.IsModelMapped)
}

func TestRelayTaskSubmitDoesNotApplyMappingTwice(t *testing.T) {
	c, info := newTaskSubmitContext(t, "alias-model", `{"alias-model":"declared-model"}`)
	pinMappingOrderPlugin(t, c, mappingOrderRewritePlugin)
	info.OriginModelName = "alias-model"

	_, taskErr := RelayTaskSubmit(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_price_error", taskErr.Code)
	assert.Equal(t, "rewritten", info.UpstreamModelName, "late mapping would overwrite rewriteModel with the chain tail")
	assert.Equal(t, "alias-model", info.OriginModelName)
}

func TestRelayTaskSubmitEmptyOriginKeepsLateMapping(t *testing.T) {
	plugin, err := pluginruntime.NewRegistry().Register(mappingOrderSubmitPlugin, pluginruntime.Options{})
	require.NoError(t, err)
	synthesized := service.CoverTaskActionToModelName(constant.TaskPlatform(plugin.Meta.Key), "text_to_video")
	c, info := newTaskSubmitContext(t, "pre-validate-upstream",
		`{"pre-validate-upstream":"should-not-apply-early","`+synthesized+`":"legacy-tail"}`)
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
	info.OriginModelName = ""

	_, taskErr := RelayTaskSubmit(c, info)
	require.NotNil(t, taskErr)
	assert.Equal(t, "model_price_error", taskErr.Code)
	assert.Equal(t, synthesized, info.OriginModelName)
	assert.Equal(t, "legacy-tail", info.UpstreamModelName)
	assert.True(t, info.IsModelMapped)
}

const billingFallbackPlugin = `
export const meta = {apiVersion:1,key:"bill-fallback",name:"Bill Fallback",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) {
  return {url: ctx.baseUrl+"/submit", method:"POST", body:{upstreamModel: ctx.upstreamModel, model: ctx.model}, action:"text_to_video"};
}
export function parseSubmitResponse(){return {taskId:"1"};}
export function buildQueryRequest(){return {url:"https://provider.example"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
`

func saveBillingConfig(t *testing.T) {
	t.Helper()
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
}

func TestRelayTaskSubmitAliasBillingIdentityAndExprFallback(t *testing.T) {
	const mapping = `{"alias-model":"declared-model"}`
	const aliasExpr = `tier("alias", 2)`
	const tailExpr = `tier("tail", 3)`
	const legacyExpr = `tier("legacy", u("old_units") * 2)`

	tests := []struct {
		name       string
		modes      map[string]string
		exprs      map[string]string
		wantTiered bool
		wantExpr   string
		source     string
	}{
		{
			name:       "alias own tiered wins",
			modes:      map[string]string{"alias-model": "tiered_expr", "declared-model": "tiered_expr"},
			exprs:      map[string]string{"alias-model": aliasExpr, "declared-model": tailExpr},
			wantTiered: true,
			wantExpr:   aliasExpr,
		},
		{
			name:       "fallback uses tail expr",
			modes:      map[string]string{"declared-model": "tiered_expr"},
			exprs:      map[string]string{"declared-model": tailExpr},
			wantTiered: true,
			wantExpr:   tailExpr,
		},
		{
			name:       "stored expression keeps running after schema narrows",
			modes:      map[string]string{"declared-model": "tiered_expr"},
			exprs:      map[string]string{"declared-model": legacyExpr},
			wantTiered: true,
			wantExpr:   legacyExpr,
			source: strings.Replace(billingFallbackPlugin, `fetchMode:"per_task"`, `fetchMode:"per_task", usageSchema:{old_units:{type:"number",unit:"count"}}, usageProfiles:[{models:["declared-model"],schema:{seconds:{type:"number",unit:"second"}}}]`, 1) + `
export function extractUsage(){return {old_units:2};}
`,
		},
		{
			name:       "neither tiered uses ordinary pricing",
			wantTiered: false,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			saveBillingConfig(t)
			if len(testCase.modes) > 0 {
				modeJSON, marshalErr := common.Marshal(testCase.modes)
				require.NoError(t, marshalErr)
				exprJSON, marshalErr := common.Marshal(testCase.exprs)
				require.NoError(t, marshalErr)
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
					"billing_setting.billing_mode": string(modeJSON),
					"billing_setting.billing_expr": string(exprJSON),
				}))
				if testCase.wantExpr == aliasExpr {
					require.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("alias-model"))
				} else {
					require.Equal(t, billing_setting.BillingModeRatio, billing_setting.GetBillingMode("alias-model"))
					require.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("declared-model"))
				}
			}

			c, info := newTaskSubmitContext(t, "alias-model", mapping)
			c.Set("group", "default")
			info.UserGroup = "default"
			info.UsingGroup = "default"
			source := testCase.source
			if source == "" {
				source = billingFallbackPlugin
			}
			pinMappingOrderPlugin(t, c, source)
			info.OriginModelName = "alias-model"

			_, taskErr := RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, "alias-model", info.OriginModelName)
			assert.Equal(t, "declared-model", info.UpstreamModelName)
			assert.True(t, info.IsModelMapped)

			task := model.InitTask(constant.TaskPlatform("bill-fallback"), info)
			assert.Equal(t, "alias-model", task.Properties.OriginModelName)
			assert.Equal(t, "declared-model", task.Properties.UpstreamModelName)

			if testCase.wantTiered {
				require.NotNil(t, info.TieredBillingSnapshot, "submission error: %+v", taskErr)
				assert.Equal(t, "alias-model", info.TieredBillingSnapshot.ModelName)
				assert.Equal(t, testCase.wantExpr, info.TieredBillingSnapshot.ExprString)
				assert.Equal(t, billingexpr.ExprHashString(testCase.wantExpr), info.TieredBillingSnapshot.ExprHash)
				assert.NotEqual(t, "model_price_error", taskErr.Code)
				if testCase.wantExpr == legacyExpr {
					assert.Equal(t, 4*common.QuotaPerUnit, info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup)
				}
			} else {
				assert.Nil(t, info.TieredBillingSnapshot)
				assert.Equal(t, "model_price_error", taskErr.Code)
			}
		})
	}
}

func TestSharedTaskBillingExpressionSelectionAndFrozenSettlement(t *testing.T) {
	const baseExpr = `tier("base", u("seconds") * 2)`
	const alphaExpr = `tier("alpha", u("seconds") * 3)`
	const betaExpr = `tier("beta", u("credits") * 5)`
	const aliasExpr = `tier("alias", u("credits") * 7)`
	for _, tc := range []struct {
		name, plugin, model, mapping, modelExpr, mode, wantExpr string
		variants                                                map[string]string
		wantPriceError, profiled                                bool
	}{
		{name: "executing plugin override", plugin: "billing-beta", model: "declared-model", modelExpr: baseExpr, mode: "tiered_expr", variants: map[string]string{"billing-alpha::declared-model": alphaExpr, "billing-beta::declared-model": betaExpr}, wantExpr: betaExpr},
		{name: "override ignores model mode", plugin: "billing-beta", model: "declared-model", modelExpr: baseExpr, mode: "ratio", variants: map[string]string{"billing-beta::declared-model": betaExpr}, wantExpr: betaExpr},
		{name: "model expression fallback", plugin: "billing-alpha", model: "declared-model", modelExpr: baseExpr, mode: "tiered_expr", wantExpr: baseExpr},
		{name: "alias override precedes mapped override", plugin: "billing-beta", model: "alias-model", mapping: `{"alias-model":"declared-model"}`, modelExpr: baseExpr, mode: "tiered_expr", variants: map[string]string{"billing-beta::declared-model": betaExpr, "billing-beta::alias-model": aliasExpr}, wantExpr: aliasExpr},
		{name: "mapped override precedes model fallback", plugin: "billing-beta", model: "alias-model", mapping: `{"alias-model":"declared-model"}`, modelExpr: baseExpr, mode: "tiered_expr", variants: map[string]string{"billing-beta::declared-model": betaExpr}, wantExpr: betaExpr},
		{name: "unconfigured plugin cannot use another schema", plugin: "billing-beta", model: "declared-model", modelExpr: baseExpr, mode: "tiered_expr", wantPriceError: true},
		{name: "missing usage in skipped branch remains incompatible", plugin: "billing-beta", model: "declared-model", modelExpr: `true ? tier("free", 0) : tier("missing", u("seconds"))`, mode: "tiered_expr", wantPriceError: true},
		{name: "fixed pricing is still rejected", plugin: "billing-beta", model: "declared-model", variants: map[string]string{"billing-beta::declared-model": `tier("fixed", fixed(1))`}, wantPriceError: true},
		{name: "endpoint mapping keeps the declared profile", plugin: "billing-beta", model: "declared-model", mapping: `{"declared-model":"ep-endpoint"}`, modelExpr: baseExpr, mode: "tiered_expr", variants: map[string]string{"billing-beta::declared-model": betaExpr}, wantExpr: betaExpr, profiled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saveBillingConfig(t)
			registry := pluginruntime.NewRegistry()
			for _, spec := range []struct{ key, field, unit string }{{"billing-alpha", "seconds", "second"}, {"billing-beta", "credits", "credit"}} {
				source := strings.ReplaceAll(billingFallbackPlugin, "bill-fallback", spec.key)
				schema := `usageSchema:{` + spec.field + `:{type:"number",unit:"` + spec.unit + `"}}`
				if tc.profiled && spec.key == "billing-beta" {
					// The endpoint ID is undeclared, so only the declared model's profile carries this schema.
					schema = `usageSchema:{seconds:{type:"number",unit:"second"}},usageProfiles:[{models:["declared-model"],schema:{` + spec.field + `:{type:"number",unit:"` + spec.unit + `"}}}]`
				}
				source = strings.Replace(source, `fetchMode:"per_task"`, `fetchMode:"per_task",`+schema, 1)
				source += `export function extractUsage(){return {` + spec.field + `:2};}`
				_, err := registry.Register(source, pluginruntime.Options{})
				require.NoError(t, err)
			}
			variants := tc.variants
			if variants == nil {
				variants = map[string]string{}
			}
			rawVariants, err := common.Marshal(variants)
			require.NoError(t, err)
			modes, err := common.Marshal(map[string]string{"declared-model": tc.mode})
			require.NoError(t, err)
			expressions, err := common.Marshal(map[string]string{"declared-model": tc.modelExpr})
			require.NoError(t, err)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
				billing_setting.PluginBillingExprOption: string(rawVariants), "billing_setting.billing_mode": string(modes), "billing_setting.billing_expr": string(expressions),
			}))
			c, info := newTaskSubmitContext(t, tc.model, tc.mapping)
			c.Set("group", "default")
			c.Set("task_plugin_key", tc.plugin)
			info.UserGroup = "default"
			info.UsingGroup = "default"
			info.OriginModelName = tc.model
			plugin, ok := registry.Generation().Get(tc.plugin)
			require.True(t, ok)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: registry.Generation(), Plugin: plugin})
			_, taskErr := RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr) // This fixture stops at reservation, before upstream submission.
			if tc.wantPriceError {
				assert.Equal(t, "model_price_error", taskErr.Code)
				assert.Nil(t, info.TieredBillingSnapshot)
				return
			}
			require.NotNil(t, info.TieredBillingSnapshot, "submission error: %+v", taskErr)
			assert.Equal(t, tc.wantExpr, info.TieredBillingSnapshot.ExprString)
			assert.NotEqual(t, "model_price_error", taskErr.Code)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: `{}`, "billing_setting.billing_expr": `{}`}))
			field := "seconds"
			if tc.plugin == "billing-beta" {
				field = "credits"
			}
			result, usage, err := service.EvaluateTaskCompletionUsage(info.TieredBillingSnapshot, map[string]any{field: float64(4)})
			require.NoError(t, err)
			assert.Equal(t, float64(4), usage[field])
			assert.Equal(t, float64(2), info.TieredBillingSnapshot.UsageFacts[field])
			assert.Equal(t, 2*info.TieredBillingSnapshot.EstimatedQuotaAfterGroup, result.ActualQuotaAfterGroup)
			assert.Equal(t, tc.wantExpr, info.TieredBillingSnapshot.ExprString)
		})
	}
}

// A unified video sale is priced from the facts frozen at the request entry.
// The executing plugin's expression override and usage never apply, and the
// completion settlement keeps the reserved amount whatever the plugin reports.
func TestRelayTaskSubmitPricesUnifiedVideoSaleFromFrozenFacts(t *testing.T) {
	saveBillingConfig(t)
	registry := pluginruntime.NewRegistry()
	source := strings.Replace(billingFallbackPlugin, `fetchMode:"per_task"`, `fetchMode:"per_task",usageSchema:{requests:{type:"number",unit:"count"}}`, 1)
	_, err := registry.Register(source+`export function extractUsage(){return {requests:1};}`, pluginruntime.Options{})
	require.NoError(t, err)
	plugin, ok := registry.Generation().Get("bill-fallback")
	require.True(t, ok)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		billing_setting.VideoSalesOption:        `{"video-unified":{"resolutions":{"720p":{"usd_per_second":0.02,"seconds":[15]}}}}`,
		billing_setting.PluginBillingExprOption: `{"bill-fallback::video-unified":"tier(\"plugin\", u(\"requests\") * 9)"}`,
	}))

	for _, tc := range []struct {
		name, tokenGroup, wantCode string
		frozen                     bool
		wantStatus                 int
		guard                      string
	}{
		{name: "frozen sale", tokenGroup: "default", frozen: true},
		{name: "auto group is refused", tokenGroup: "auto", frozen: true, wantCode: "video_sales_auto_group", wantStatus: http.StatusBadRequest},
		{name: "table appearing after entry never falls back to plugin pricing", tokenGroup: "default", wantCode: "video_sales_unavailable", wantStatus: http.StatusServiceUnavailable},
		{name: "scheduler off", frozen: true, guard: "off", wantCode: "video_sales_scheduler_unavailable", wantStatus: http.StatusServiceUnavailable},
		{name: "scheduler shadow", frozen: true, guard: "shadow", wantCode: "video_sales_scheduler_unavailable", wantStatus: http.StatusServiceUnavailable},
		{name: "scheduler not ready", frozen: true, guard: "not_ready", wantCode: "video_sales_scheduler_unavailable", wantStatus: http.StatusServiceUnavailable},
		{name: "fixed channel", frozen: true, guard: "pin", wantCode: "video_sales_unsupported_request", wantStatus: http.StatusBadRequest},
		{name: "locked channel", frozen: true, guard: "locked", wantCode: "video_sales_unsupported_request", wantStatus: http.StatusBadRequest},
		{name: "origin task", frozen: true, guard: "origin", wantCode: "video_sales_unsupported_request", wantStatus: http.StatusBadRequest},
		{name: "native entry", frozen: true, guard: "native", wantCode: "video_sales_unsupported_request", wantStatus: http.StatusBadRequest},
		{name: "other protocol", frozen: true, guard: "protocol", wantCode: "video_sales_unsupported_request", wantStatus: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info := newTaskSubmitContext(t, "video-unified", `{"video-unified":"declared-model"}`)
			c.Set("group", "default")
			info.UserGroup, info.UsingGroup, info.TokenGroup = "default", "default", tc.tokenGroup
			info.OriginModelName = "video-unified"
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: registry.Generation(), Plugin: plugin})
			endpoint := pluginruntime.PinnedEndpoint{Generation: registry.Generation(), Plugin: plugin, Protocol: "openai_video", Operation: pluginruntime.HostProtocolOperation{Name: "create"}, Model: "video-unified"}
			decision := service.VideoSchedDecision{Takeover: true}
			switch tc.guard {
			case "off":
				decision = service.VideoSchedDecision{Reason: service.VideoSchedReasonModeOff}
			case "shadow":
				decision = service.VideoSchedDecision{Shadow: true}
			case "not_ready":
				decision.Reason = service.VideoSchedReasonNotReady
			case "pin":
				service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 1, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
			case "locked":
				info.LockedChannel = &model.Channel{Id: 1}
			case "origin":
				info.OriginTaskID = "original"
			case "native":
				endpoint.Protocol = ""
			case "protocol":
				endpoint.Protocol = "openai_responses"
			}
			c.Set(pluginruntime.ContextKeyPinnedEndpoint, endpoint)
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
			if tc.frozen {
				service.SetVideoSalesFacts(c, service.VideoSalesFacts{Model: "video-unified", Seconds: 15, Resolution: "720p", USDPerSecond: 0.02})
			}

			_, taskErr := RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr) // This fixture stops at reservation, before upstream submission.
			if tc.wantCode != "" {
				assert.Equal(t, tc.wantCode, taskErr.Code)
				assert.Equal(t, tc.wantStatus, taskErr.StatusCode)
				assert.True(t, taskErr.NoRetry)
				assert.Nil(t, info.TieredBillingSnapshot)
				assert.Nil(t, info.Billing, "local guards must not reserve any quota")
				return
			}
			snap := info.TieredBillingSnapshot
			require.NotNil(t, snap, "submission error: %+v", taskErr)
			assert.Equal(t, billingexpr.SalesSourceVideoRequest, snap.SalesSource)
			assert.Equal(t, `tier("720p", u("seconds") * 0.02)`, snap.ExprString)
			assert.Equal(t, map[string]any{"seconds": float64(15), "resolution": "720p"}, snap.UsageFacts)
			assert.True(t, snap.TaskUsageBilling)
			assert.Equal(t, float64(1), snap.GroupRatio)
			assert.Equal(t, common.QuotaRound(0.3*common.QuotaPerUnit), snap.EstimatedQuotaAfterGroup)
			assert.Equal(t, snap.EstimatedQuotaAfterGroup, info.PriceData.Quota)

			result, usage, err := service.EvaluateTaskCompletionUsage(snap, map[string]any{"requests": float64(1), "seconds": float64(5)})
			require.NoError(t, err)
			assert.Equal(t, snap.EstimatedQuotaAfterGroup, result.ActualQuotaAfterGroup)
			assert.Equal(t, "720p", result.MatchedTier)
			assert.Equal(t, snap.UsageFacts, usage)
		})
	}
}

func TestUnifiedVideoSaleMapsAcrossPluginBillingUnits(t *testing.T) {
	saveBillingConfig(t)
	service.InitHttpClient()
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	source, err := plugins.Source("pidoi")
	require.NoError(t, err)
	registry := pluginruntime.NewRegistry()
	plugin, err := registry.Register(source, pluginruntime.Options{})
	require.NoError(t, err)
	const publicModel = "sd-2.5-720p-pro"
	const mapping = `{"sd-2.5-720p-pro":"jiuyue111"}`
	for _, unified := range []bool{false, true} {
		t.Run(strconv.FormatBool(unified), func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := common.DecodeJson(r.Body, &body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"pidoi-mock-task","status":"queued"}`))
			}))
			defer upstream.Close()
			c, info := newTaskSubmitContext(t, publicModel, mapping)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "fixture-only-key")
			info.OriginModelName = publicModel
			info.UserGroup, info.UsingGroup, info.TokenGroup = "default", "default", "default"
			c.Set("group", "default")
			// A client-supplied field must never grant the host's exemption.
			request := map[string]any{"prompt": "cat", "seconds": float64(5), "resolution": "720p", "salesSource": "video_request"}
			c.Set("task_request", request)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: registry.Generation(), Plugin: plugin})
			if unified {
				service.SetVideoSalesFacts(c, service.VideoSalesFacts{Model: publicModel, Seconds: 5, Resolution: "720p", USDPerSecond: 0.02})
			}
			channel := &model.Channel{Id: 8751, Models: publicModel, Status: common.ChannelStatusEnabled, ModelMapping: common.GetPointer(mapping),
				OtherSettings: `{"video_scheduling":{"models":{"sd-2.5-720p-pro":{"mode":"per_video","prices":{"720p":0.01}}}}}`}
			setting := *operation_setting.GetVideoSchedulingSetting()
			setting.SelectionPolicy = ""
			common.SetContextKey(c, constant.ContextKeyVideoSchedSetting, &setting)
			decision := service.AssembleVideoDecision(c, &setting, "default", publicModel, []*model.Channel{channel}, 1)
			require.Len(t, decision.Candidates, 1)
			candidate := decision.Candidates[0]
			if unified {
				require.Empty(t, candidate.Excluded)
				assert.Equal(t, "jiuyue111", candidate.MappedModel)
				assert.Equal(t, float64(5), *candidate.Spec.OutputSeconds)
				assert.Equal(t, "720p", candidate.Spec.Tier)
			} else {
				assert.Contains(t, candidate.Excluded, "model mapping cannot change billing unit")
			}
			c.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{Generation: registry.Generation(), Plugin: plugin, Protocol: "openai_video", Operation: pluginruntime.HostProtocolOperation{Name: "create"}, Model: publicModel})
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, service.VideoSchedDecision{Takeover: true})
			info.Billing = &imageReservation{limit: 1 << 30}
			submission, taskErr := RelayTaskSubmit(c, info)
			if !unified {
				require.NotNil(t, taskErr)
				assert.Equal(t, "plugin_request_invalid", taskErr.Code)
				assert.Contains(t, taskErr.Message, "model mapping cannot change billing unit")
				assert.Nil(t, info.TieredBillingSnapshot)
				assert.Empty(t, requests)
				return
			}
			require.Nil(t, taskErr, "submission error: %+v", taskErr)
			require.NotNil(t, submission)
			assert.Equal(t, "pidoi-mock-task", submission.UpstreamTaskID)
			require.Len(t, requests, 1)
			assert.Equal(t, map[string]any{"model": "jiuyue111", "prompt": "cat", "seconds": "5", "resolution": "720p"}, <-requests)
			require.NotNil(t, info.TieredBillingSnapshot, "submission error: %+v", taskErr)
			assert.Equal(t, "jiuyue111", info.UpstreamModelName)
			assert.Equal(t, common.QuotaRound(0.1*common.QuotaPerUnit), info.PriceData.Quota)
			result, usage, err := service.EvaluateTaskCompletionUsage(info.TieredBillingSnapshot, map[string]any{"requests": float64(1)})
			require.NoError(t, err)
			assert.Equal(t, info.PriceData.Quota, result.ActualQuotaAfterGroup)
			assert.Equal(t, float64(5), usage["seconds"])
		})
	}
}

func TestUnifiedVideoCandidatesPreserveReferenceAliases(t *testing.T) {
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	source, err := plugins.Source("meaicc")
	require.NoError(t, err)
	registry := pluginruntime.NewRegistry()
	plugin, err := registry.Register(source, pluginruntime.Options{})
	require.NoError(t, err)
	const mapping = `{"unified-media":"sd-2-c4"}`
	for _, tc := range []struct{ field, kind, mediaType string }{
		{"image", "image", "reference_image"},
		{"video_url", "video", "reference_video"},
		{"input_video", "video", "reference_video"},
		{"audio_url", "audio", "reference_voice"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			c, _ := newTaskSubmitContext(t, "unified-media", mapping)
			value := map[string]any{"model": "unified-media", "prompt": "cat", "seconds": 5, "resolution": "720p", tc.field: "https://cdn.example/reference"}
			decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
				"model": "unified-media", "body": map[string]any{"kind": "json", "value": value},
			})
			require.NoError(t, err)
			request := decoded.(map[string]any)["requestBody"]
			c.Set("task_request", request)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: registry.Generation(), Plugin: plugin})
			service.SetVideoSalesFacts(c, service.VideoSalesFacts{Model: "unified-media", Seconds: 5, Resolution: "720p", USDPerSecond: 0.02})
			channel := &model.Channel{Id: 8752, Models: "unified-media", Status: common.ChannelStatusEnabled, ModelMapping: common.GetPointer(mapping),
				OtherSettings: fmt.Sprintf(`{"video_scheduling":{"models":{"unified-media":{"mode":"per_video","prices":{"720p":0.01},"references":{%q:{"*":{"mode":"per_input","value":0.02}}}}}}}`, tc.kind)}
			setting := *operation_setting.GetVideoSchedulingSetting()
			setting.SelectionPolicy = ""
			decision := service.AssembleVideoDecision(c, &setting, "default", "unified-media", []*model.Channel{channel}, 1)
			require.Len(t, decision.Candidates, 1)
			candidate := decision.Candidates[0]
			require.Empty(t, candidate.Excluded)
			assert.Equal(t, 1, candidate.Spec.References[tc.kind])
			quote := videosched.Quote(candidate.Cost, candidate.Spec)
			require.Empty(t, quote.Reason)
			assert.InDelta(t, 0.03, quote.TotalUSD, 1e-12, "the reference must contribute to the purchase quote")
			built, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
				"model": "unified-media", "upstreamModel": "sd-2-c4", "requestBody": request, "baseUrl": "https://upstream.example", "apiKey": "fixture-only-key",
			})
			require.NoError(t, err)
			body := built.(map[string]any)["body"].(map[string]any)
			media := body["input"].(map[string]any)["media"]
			assert.Equal(t, []any{map[string]any{"type": tc.mediaType, "url": "https://cdn.example/reference"}}, media)
		})
	}
}

// Issue #7478: task APIs answer 201 Created or 202 Accepted on submission.
// Every 2xx must reach parseSubmitResponse; only other statuses are upstream
// failures that keep the upstream status and body.
func TestRelayTaskSubmitAcceptsAnySuccessfulUpstreamStatus(t *testing.T) {
	service.InitHttpClient()
	const source = `
export const meta = {apiVersion:1,key:"status-echo",name:"Status Echo",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) { return {url: ctx.baseUrl+"/submit", method:"POST", body:{model: ctx.model}, action:"text_to_video"}; }
export function parseSubmitResponse(ctx, response) { return {taskId: response.body.id, taskData: {status: response.statusCode}}; }
export function buildQueryRequest(ctx) { return {url: ctx.baseUrl+"/query"}; }
export function parseTaskResult() { return {status:"SUCCESS"}; }
`
	for _, tc := range []struct {
		status   int
		wantCode string
	}{
		{status: http.StatusOK},
		{status: http.StatusCreated},
		{status: http.StatusAccepted},
		{status: http.StatusBadRequest, wantCode: "fail_to_fetch_task"},
		{status: http.StatusBadGateway, wantCode: "fail_to_fetch_task"},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			saveBillingConfig(t)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
				"billing_setting.billing_mode": `{"declared-model":"tiered_expr"}`,
				"billing_setting.billing_expr": `{"declared-model":"tier(\"flat\", 3)"}`,
			}))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"id":"job-42","message":"upstream body"}`))
			}))
			defer server.Close()

			c, info := newTaskSubmitContext(t, "declared-model", "")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			c.Set("group", "default")
			info.UserGroup, info.UsingGroup = "default", "default"
			info.OriginModelName = "declared-model"
			// An existing reservation skips pre-consume so the fixture reaches the upstream call.
			info.Billing = &imageReservation{limit: 1 << 30}
			pinMappingOrderPlugin(t, c, source)

			result, taskErr := RelayTaskSubmit(c, info)
			if tc.wantCode != "" {
				require.NotNil(t, taskErr)
				assert.Equal(t, tc.wantCode, taskErr.Code)
				assert.Equal(t, tc.status, taskErr.StatusCode)
				assert.Contains(t, taskErr.Message, "upstream body")
				assert.Nil(t, result)
				return
			}
			require.Nil(t, taskErr, "submission error: %+v", taskErr)
			require.NotNil(t, result)
			assert.Equal(t, "job-42", result.UpstreamTaskID)
			assert.JSONEq(t, `{"status":`+strconv.Itoa(tc.status)+`}`, string(result.TaskData))
		})
	}
}

// Video scheduling only reads sell prices: the reserved and settled quota of a
// submission are the same whether scheduling is off, shadowing or has taken
// over, and a reference video (a purchase surcharge) never changes the sale.
func TestRelayTaskSubmitBillingIgnoresVideoScheduling(t *testing.T) {
	service.InitHttpClient()
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		billing_setting.PluginBillingExprOption: `{"seedance-hjmie::videos-fast":"u(\"seconds\") * 0.1"}`,
	}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"upstream-1"}`))
	}))
	defer server.Close()
	generation := pluginruntime.DefaultRegistry.Generation()
	seedance, ok := generation.Get("seedance-hjmie")
	require.True(t, ok)
	wantQuota := common.QuotaRound(0.5 * common.QuotaPerUnit) // 5 seconds x $0.1, group ratio 1

	for _, videos := range [][]any{nil, {"https://example.com/ref.mp4"}} {
		for _, decision := range []service.VideoSchedDecision{
			{Reason: service.VideoSchedReasonModeOff}, {Shadow: true}, {Takeover: true},
		} {
			t.Run(fmt.Sprintf("videos=%d/%+v", len(videos), decision), func(t *testing.T) {
				body := map[string]any{"prompt": "cat", "duration": 5, "resolution": "720p"}
				if videos != nil {
					body["videos"] = videos
				}
				c, info := newTaskSubmitContext(t, "videos-fast", "")
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
				common.SetContextKey(c, constant.ContextKeyChannelKey, "k")
				common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
				c.Set("group", "default")
				c.Set("task_request", body)
				c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: generation, Plugin: seedance})
				info.UserGroup, info.UsingGroup, info.OriginModelName = "default", "default", "videos-fast"
				// An existing reservation skips pre-consume so the fixture reaches the upstream call.
				info.Billing = &imageReservation{limit: 1 << 30}

				result, taskErr := RelayTaskSubmit(c, info)
				require.Nil(t, taskErr, "submission error: %+v", taskErr)
				assert.Equal(t, wantQuota, info.PriceData.Quota, "reserved quota")
				assert.Equal(t, wantQuota, result.Quota, "settled quota")
				require.NotNil(t, info.TieredBillingSnapshot)
				assert.Equal(t, map[string]any{"seconds": float64(5), "resolution": "720p"}, info.TieredBillingSnapshot.UsageFacts)
			})
		}
	}
}
