# Video Resolution Pricing Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make resolution-based video pricing opt-in while preserving the existing legacy pre-consume, retry, settlement, refund, model-pricing ownership, and public-pricing behavior for every model without an active resolution table.

**Architecture:** Freeze one immutable request billing plan before initial channel selection, derive resolver-capable channel filtering from the real task-adaptor interface, and let the existing billing session and task snapshot carry that frozen decision through retry and settlement. Put all twelve model-pricing option documents behind one transactional model command with deterministic row locking and CAS for whole-document writers, then make the React editors call that backend seam instead of issuing independent model and option mutations.

**Tech Stack:** Go 1.22+, Gin, GORM v2, SQLite/MySQL/PostgreSQL, testify, React 19, TypeScript, React Query, React Hook Form, Bun.

## Global Constraints

- The approved source of truth is `docs/superpowers/specs/2026-08-30-video-resolution-pricing-compatibility-design.md`.
- Suno always remains on legacy task billing. A non-Suno model activates resolution billing only when a non-empty exact or compact-wildcard `VideoResolutionPrice` table matches its original/public model name.
- Freeze billing kind, full matched table, original model name, and request identity once; retries and running tasks must never consult later pricing configuration.
- Preserve the existing legacy `ModelPrice`/`ModelRatio`, adapter `EstimateBilling`/`AdjustBillingOnSubmit`, `TaskBillingMode`, pre-consume, settlement, and refund code paths. Do not redesign them.
- A configured resolution model never falls back to legacy pricing for a missing or invalid tier.
- A finite group ratio of `0` is valid; resolution prices, quota conversion units, and independent ratios remain finite and strictly positive.
- Root `{}` is a valid resolution document; a per-model `{}` is invalid and must not replace persisted or live state.
- The retained legacy snapshot consists of exactly: `ModelPrice`, `ModelRatio`, `CacheRatio`, `CreateCacheRatio`, `CompletionRatio`, `ImageRatio`, `AudioRatio`, `AudioCompletionRatio`, `billing_setting.billing_mode`, `billing_setting.billing_expr`, and `TaskBillingMode`.
- Pricing option rows are locked in this deterministic order: `AudioCompletionRatio`, `AudioRatio`, `CacheRatio`, `CompletionRatio`, `CreateCacheRatio`, `ImageRatio`, `ModelPrice`, `ModelRatio`, `TaskBillingMode`, `VideoResolutionPrice`, `billing_setting.billing_expr`, `billing_setting.billing_mode`.
- All database logic must work on SQLite, MySQL >= 5.7.8, and PostgreSQL >= 9.6; use GORM and `lockForUpdate(tx)`.
- All JSON marshal/unmarshal operations use `common.*` wrappers; `encoding/json` is permitted only for types.
- New or substantially rewritten Go tests use `require` for fatal/setup assertions and `assert` for non-fatal assertions.
- Frontend package/test/build commands use Bun, all user-facing text remains i18n-safe, and affected TypeScript files must pass typecheck and oxlint.
- There is no automatic data migration, no per-call-to-per-second conversion, no new persistent billing-mode option, no running-task schema change, and no provider resolution/duration rule change.
- Do not modify protected project identity or attribution.

## File Structure

- Create `relay/common/task_billing_plan.go` for the immutable request billing kind/table.
- Create `relay/common/task_billing_plan_test.go` for cloning and lookup invariants.
- Modify `relay/relay_adaptor.go` so channel compatibility is derived from `GetTaskAdaptor` and `VideoBillingResolver`.
- Modify `middleware/distributor.go`, `service/channel_select.go`, `model/channel_cache.go`, and `model/ability.go` so the initial, affinity, specific, retry, and database selection paths share the same allowed channel types.
- Modify `relay/relay_task.go`, `relay/common/relay_info.go`, `service/billing_session.go`, and `controller/relay.go` to reuse the frozen plan without changing persisted running-task formats.
- Create `model/model_pricing_command.go` as the transactional owner of the twelve pricing documents.
- Create `model/model_pricing_command_test.go` for rename/copy/delete/rollback/CAS/publication recovery.
- Modify `model/option.go`, `controller/option.go`, `controller/model_meta.go`, and `router/api-router.go` to expose CAS whole-document updates and pricing-aware model commands.
- Create `web/src/features/system-settings/models/model-pricing-persistence.ts` for pure frontend pricing snapshot/payload transformations.
- Create `web/src/features/system-settings/models/__tests__/model-pricing-persistence.test.ts` for legacy retention, copy, delete, and explicit mode switching.
- Modify the model drawer, ratio editor, public pricing components, and focused tests without redesigning their UI.

---

### Task 1: Immutable Request Billing Plan and Resolution Validation

**Files:**
- Create: `relay/common/task_billing_plan.go`
- Create: `relay/common/task_billing_plan_test.go`
- Modify: `relay/common/relay_info.go:822-837`
- Modify: `constant/task.go:35-56`
- Modify: `setting/ratio_setting/video_resolution_price.go:20-68`
- Modify: `setting/ratio_setting/video_resolution_price_test.go`
- Modify: `relay/common/video_billing.go:55-104`
- Modify: `relay/common/video_billing_test.go`

**Interfaces:**
- Produces: `TaskBillingKind`, `TaskBillingPlan`, `NewLegacyTaskBillingPlan(model, requestID string)`, `NewVideoResolutionTaskBillingPlan(model, requestID string, prices map[string]float64)`, `Kind()`, `OriginModelName()`, `RequestID()`, and `ResolutionPrice(string)`.
- Produces: `TaskRelayInfo.BillingPlan *TaskBillingPlan` for Tasks 2 and 3.
- Produces: `constant.IsSunoModel(modelName string) bool`, shared by request freezing and public pricing so Suno classification does not depend on channel-selection timing.

- [ ] **Step 1: Write failing immutable-plan tests**

```go
func TestVideoResolutionTaskBillingPlanClonesPrices(t *testing.T) {
	prices := map[string]float64{"720p": 0.1}
	plan, err := NewVideoResolutionTaskBillingPlan("client-model", "req-frozen", prices)
	require.NoError(t, err)
	prices["720p"] = 9

	price, ok := plan.ResolutionPrice(" 720P ")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
	assert.Equal(t, TaskBillingKindVideoResolution, plan.Kind())
	assert.Equal(t, "client-model", plan.OriginModelName())
	assert.Equal(t, "req-frozen", plan.RequestID())
}

func TestLegacyTaskBillingPlanHasNoResolutionPrice(t *testing.T) {
	plan := NewLegacyTaskBillingPlan("legacy-model", "req-legacy")
	_, ok := plan.ResolutionPrice("720p")
	assert.False(t, ok)
	assert.Equal(t, TaskBillingKindLegacy, plan.Kind())
}
```

- [ ] **Step 2: Run the new tests and verify the missing types fail compilation**

Run: `go test ./relay/common -run 'Test(VideoResolutionTaskBillingPlan|LegacyTaskBillingPlan)' -count=1`

Expected: FAIL because `TaskBillingPlan` constructors do not exist.

- [ ] **Step 3: Implement the immutable plan and attach it to `TaskRelayInfo`**

```go
type TaskBillingKind uint8

const (
	TaskBillingKindLegacy TaskBillingKind = iota
	TaskBillingKindVideoResolution
)

type TaskBillingPlan struct {
	kind             TaskBillingKind
	originModelName  string
	requestID        string
	resolutionPrices map[string]float64
}

func NewLegacyTaskBillingPlan(model, requestID string) *TaskBillingPlan {
	return &TaskBillingPlan{kind: TaskBillingKindLegacy, originModelName: model, requestID: requestID}
}

func NewVideoResolutionTaskBillingPlan(model, requestID string, prices map[string]float64) (*TaskBillingPlan, error) {
	if model == "" || requestID == "" || len(prices) == 0 {
		return nil, fmt.Errorf("video resolution billing requires model, request identity, and prices")
	}
	clone := make(map[string]float64, len(prices))
	for key, price := range prices {
		normalized, err := rootcommon.NormalizeVideoResolutionKey(key)
		if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return nil, fmt.Errorf("invalid video resolution price %q", key)
		}
		clone[normalized] = price
	}
	return &TaskBillingPlan{kind: TaskBillingKindVideoResolution, originModelName: model, requestID: requestID, resolutionPrices: clone}, nil
}
```

Add `BillingPlan *TaskBillingPlan` next to `ResolvedVideoBilling` in `TaskRelayInfo`. Keep fields private so retries cannot mutate the table.

Add the authoritative model classifier beside `SunoModel2Action`:

```go
func IsSunoModel(modelName string) bool {
	_, ok := SunoModel2Action[modelName]
	return ok
}
```

- [ ] **Step 4: Add validation and zero-group-ratio regression tests**

```go
func TestVideoResolutionPriceRejectsEmptyPerModelTable(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString(`{"kept":{"720p":0.1}}`))
	err := UpdateVideoResolutionPriceByJSONString(`{"invalid":{}}`)
	require.Error(t, err)
	assert.Equal(t, map[string]map[string]float64{"kept": {"720p": 0.1}}, GetVideoResolutionPriceMap())
}

func TestCalculateVideoResolutionQuotaAllowsZeroGroupRatio(t *testing.T) {
	quota, clamp, err := CalculateVideoResolutionQuotaAtUnit(0.1, 5, 0, nil, 500)
	require.NoError(t, err)
	assert.Zero(t, quota)
	assert.Nil(t, clamp)
}
```

- [ ] **Step 5: Implement validation changes**

In `parseVideoResolutionPriceJSON`, reject `len(rawPrices) == 0` inside the model loop, while leaving an empty root map valid. Split group validation from strictly-positive validation in `video_billing.go`:

```go
func validateNonNegativeFinite(name string, value float64) error {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be non-negative and finite", name)
	}
	return nil
}
```

Use it only for `groupRatio`; keep the existing positive validator for price, quota unit, and independent ratios.

- [ ] **Step 6: Run focused tests and commit**

Run: `go test ./setting/ratio_setting ./relay/common -count=1`

Expected: PASS.

```powershell
git add relay/common/task_billing_plan.go relay/common/task_billing_plan_test.go relay/common/relay_info.go relay/common/video_billing.go relay/common/video_billing_test.go setting/ratio_setting/video_resolution_price.go setting/ratio_setting/video_resolution_price_test.go
git commit -m "feat(video): freeze request billing plans"
```

### Task 2: Resolver-Capable Channel Selection Across Every Entry Path

**Files:**
- Modify: `relay/relay_adaptor.go:136-180`
- Modify: `middleware/distributor.go:27-173`
- Create: `middleware/distributor_video_billing_test.go`
- Modify: `service/channel_select.go:14-161`
- Create: `service/channel_select_test.go`
- Modify: `model/channel_cache.go:114-220`
- Create: `model/channel_cache_test.go`
- Modify: `model/ability.go:67-139`
- Create: `model/ability_test.go`
- Modify: `controller/relay.go:297-327,490-570`
- Create: `controller/relay_task_routing_test.go`

**Interfaces:**
- Consumes: `TaskBillingPlan.Kind()` from Task 1.
- Produces: `CompatibleTaskChannelTypes(kind relaycommon.TaskBillingKind) []int`, `TaskChannelTypeSupportsBilling(kind, channelType) bool`, and `RetryParam.AllowedChannelTypes []int`.

- [ ] **Step 1: Write adaptor-derived capability tests**

```go
func TestCompatibleTaskChannelTypesAreDerivedFromResolverInterface(t *testing.T) {
	allowed := CompatibleTaskChannelTypes(relaycommon.TaskBillingKindVideoResolution)
	for _, channelType := range allowed {
		adaptor := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channelType)))
		require.NotNil(t, adaptor)
		_, ok := adaptor.(channel.VideoBillingResolver)
		assert.True(t, ok, "channel type %d", channelType)
	}
	assert.False(t, TaskChannelTypeSupportsBilling(relaycommon.TaskBillingKindVideoResolution, constant.ChannelTypeKling))
	assert.True(t, TaskChannelTypeSupportsBilling(relaycommon.TaskBillingKindLegacy, constant.ChannelTypeKling))
}
```

- [ ] **Step 2: Write memory-cache and DB selector regressions**

Create explicit fixtures with an incompatible high-priority Kling channel and compatible lower-priority Sora channel. Run the same assertions once with `common.MemoryCacheEnabled = true` and once with `false`: `AllowedChannelTypes: []int{constant.ChannelTypeSora}` selects Sora, while `AllowedChannelTypes: nil` preserves the old Kling outcome.

```go
selected, err := GetRandomSatisfiedChannelForTypes("default", "video-model", 0, "/v1/videos", []int{constant.ChannelTypeSora})
require.NoError(t, err)
require.NotNil(t, selected)
assert.Equal(t, constant.ChannelTypeSora, selected.Type)
```

- [ ] **Step 3: Run selector tests and confirm they fail on the current unfiltered priority calculation**

Run: `go test ./relay ./model ./service -run 'Test(CompatibleTaskChannelTypes|GetRandomSatisfiedChannelForTypes|GetChannelForTypes|CacheGetRandomSatisfiedChannelAllowedTypes)' -count=1`

Expected: FAIL because filtered variants and `AllowedChannelTypes` do not exist.

- [ ] **Step 4: Implement the shared filter before priority selection**

Keep existing APIs as compatibility wrappers:

```go
func GetRandomSatisfiedChannel(group, model string, retry int, path string) (*Channel, error) {
	return GetRandomSatisfiedChannelForTypes(group, model, retry, path, nil)
}

type RetryParam struct {
	Ctx                 *gin.Context
	TokenGroup          string
	ModelName           string
	RequestPath         string
	AllowedChannelTypes []int
	Retry               *int
	resetNextTry        bool
}
```

For memory cache, remove disallowed channel IDs before collecting unique priorities. For DB selection, join `channels` and apply `channels.type IN ?` to both the max-priority subquery and the candidate query; select `abilities.*` to avoid ambiguous columns. A `nil` list must preserve current behavior exactly.

- [ ] **Step 5: Write initial/specific/affinity/retry/locked-channel controller tests**

Tests must prove:

```text
resolution + compatible/incompatible mix -> initial middleware selection uses compatible channel
resolution + incompatible specific channel -> HTTP 400 video_resolution_not_supported
resolution + incompatible affinity -> affinity skipped, compatible channel selected
resolution + no compatible channel -> HTTP 400 before pre-consume
resolution retry -> every retry remains in allowed types
resolution remix + incompatible LockedChannel -> HTTP 400 before pre-consume and no retry
legacy -> no capability filtering and old selector outcome remains
```

- [ ] **Step 6: Freeze the plan before middleware selection and wire every selection path**

Add an idempotent relay helper that stores the plan in Gin context and later attaches the same pointer to `RelayInfo`:

```go
func PrepareTaskBillingPlan(c *gin.Context, modelName, requestID string) *relaycommon.TaskBillingPlan {
	if value, ok := c.Get(taskBillingPlanContextKey); ok {
		return value.(*relaycommon.TaskBillingPlan)
	}
	if requestID == "" {
		requestID = c.GetString(common.RequestIdKey)
	}
	if requestID == "" {
		requestID = common.NewRequestId()
		c.Set(common.RequestIdKey, requestID)
	}
	plan := relaycommon.NewLegacyTaskBillingPlan(modelName, requestID)
	isSuno := constant.TaskPlatform(c.GetString("platform")) == constant.TaskPlatformSuno ||
		constant.IsSunoModel(modelName)
	if !isSuno {
		if prices, ok := ratio_setting.GetVideoResolutionPrices(modelName); ok && len(prices) > 0 {
			plan, _ = relaycommon.NewVideoResolutionTaskBillingPlan(modelName, requestID, prices)
		}
	}
	c.Set(taskBillingPlanContextKey, plan)
	return plan
}
```

Call it as `PrepareTaskBillingPlan(c, modelRequest.Model, "")` in `Distribute` only for `RelayModeVideoSubmit` before specific-channel, affinity, or random selection. `/suno` supplies the route-owned platform marker, while direct/custom entry points are protected by the shared `constant.IsSunoModel` classifier; billing kind must never be inferred from whichever channel happens to be selected. Pass the compatible list to `RetryParam`, validate specific and affinity candidates with `TaskChannelTypeSupportsBilling`, and return HTTP 400/code `video_resolution_not_supported` when the frozen resolution plan has no candidate. Remix skips initial selection; after `ResolveOriginTask`, `controller.RelayTask` calls `PrepareTaskBillingPlan(c, relayInfo.OriginModelName, relayInfo.RequestId)`, attaches it to `TaskRelayInfo`, and validates `LockedChannel` before the retry loop. Direct `RelayTaskSubmit` callers use the same helper with `RelayInfo.RequestId`, so test/internal paths cannot bypass freezing.

- [ ] **Step 7: Run focused tests and commit**

Run: `go test ./relay ./model ./service ./middleware ./controller -run 'Test(Compatible|AllowedTypes|Resolution.*Channel|LockedChannel)' -count=1`

Expected: PASS.

```powershell
git add relay/relay_adaptor.go middleware/distributor.go service/channel_select.go model/channel_cache.go model/ability.go controller/relay.go relay/*_test.go middleware/*_test.go service/*_test.go model/*_test.go controller/*_test.go
git commit -m "feat(video): filter resolution billing channels"
```

### Task 3: Restore the Legacy Relay Path and Bind Funding to the Frozen Kind

**Files:**
- Modify: `relay/relay_task.go:178-330`
- Modify: `relay/relay_task_test.go`
- Modify: `service/billing_session.go:399-430`
- Create: `service/billing_session_test.go`
- Modify: `controller/relay.go:616-649`
- Modify: `controller/task_test.go`
- Verify unchanged: `service/task_billing.go`, `service/task_billing_test.go`, `model/task.go`

**Interfaces:**
- Consumes: `TaskRelayInfo.BillingPlan` and `ResolutionPrice` from Task 1.
- Produces: unchanged `ResolvedVideoBilling`, `BillingSession`, and `TaskBillingContext` formats for all running-task code.

- [ ] **Step 1: Replace strict-rollout tests with legacy compatibility regressions**

Add these deterministic tests using the existing `taskSubmitVideoTestContext` fixture. Extend `taskSubmitTestState` with `preConsumedQuota int` and assign the callback's quota argument to it so each test can assert the old arithmetic:

```go
func TestRelayTaskSubmitUnconfiguredVideoModelUsesLegacyPrice(t *testing.T) {
	base := &taskSubmitTestAdaptor{}
	c, info, deps, state := taskSubmitVideoTestContext(t, base)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.2}`))
	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)
	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.True(t, base.didEstimate)
	assert.True(t, base.didAdjust)
	assert.Equal(t, 980100, state.preConsumedQuota)
	assert.Nil(t, info.ResolvedVideoBilling)
}

func TestRelayTaskSubmitLegacyPerCallIgnoresDurationMultiplier(t *testing.T) {
	base := &taskSubmitTestAdaptor{}
	c, info, deps, state := taskSubmitVideoTestContext(t, base)
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"client-model":0.2}`))
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"client-model":"per_call"}`))
	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)
	require.Nil(t, taskErr)
	assert.Equal(t, 100, state.preConsumedQuota)
	assert.Equal(t, 100, result.Quota)
}

func TestRelayTaskSubmitResolutionPlanUsesFrozenTableAfterLiveRemoval(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "720p", EffectiveDurationSeconds: 5}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan("client-model", "req-frozen", map[string]float64{"720p": 0.1})
	require.NoError(t, err)
	info.TaskRelayInfo.BillingPlan = plan
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(`{}`))
	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)
	require.Nil(t, taskErr)
	assert.Equal(t, 250, state.preConsumedQuota)
	assert.Equal(t, 250, result.Quota)
}

func TestRelayTaskSubmitResolutionPlanDoesNotFallbackForMissingTier(t *testing.T) {
	base := &taskSubmitTestAdaptor{selection: relaycommon.VideoBillingSelection{EffectiveResolution: "1080p", EffectiveDurationSeconds: 5}}
	c, info, deps, state := taskSubmitVideoTestContext(t, &videoTaskSubmitTestAdaptor{base})
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan("client-model", "req-frozen", map[string]float64{"720p": 0.1})
	require.NoError(t, err)
	info.TaskRelayInfo.BillingPlan = plan
	result, taskErr := relayTaskSubmitWithDeps(c, info, deps)
	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Zero(t, state.preConsumeCalls)
}
```

Add the per-second test with different estimate/adjust ratios and exact quota assertions by making those maps fields on `taskSubmitTestAdaptor`. Add the Suno case by setting `c.Set("platform", string(constant.TaskPlatformSuno))`, installing both a matching resolution table and a legacy model price, and asserting `ResolvedVideoBilling == nil` plus the legacy quota.

- [ ] **Step 2: Run relay tests and verify legacy non-Suno cases fail under `platform != Suno`**

Run: `go test ./relay -run 'TestRelayTaskSubmit(Unconfigured|Legacy|ResolutionPlan|Suno)' -count=1`

Expected: FAIL because every non-Suno attempt currently requires resolution pricing.

- [ ] **Step 3: Branch only on the frozen plan and use only its frozen table**

At the start of `relayTaskSubmitWithDeps`, defensively prepare/attach a plan only when the controller was bypassed. Replace `platform != Suno` with:

```go
plan := info.TaskRelayInfo.BillingPlan

if plan.Kind() == relaycommon.TaskBillingKindVideoResolution {
	resolver, ok := adaptor.(channel.VideoBillingResolver)
	if !ok {
		return nil, videoResolutionNotSupported(plan.OriginModelName(), "unknown")
	}
	selection, taskErr := resolver.ResolveVideoBilling(c, info)
	if taskErr != nil { return nil, taskErr }
	selectedPrice, ok := plan.ResolutionPrice(selection.EffectiveResolution)
	if !ok { return nil, videoResolutionNotSupported(plan.OriginModelName(), selection.EffectiveResolution) }
	// Existing BuildVideoResolutionPriceData path remains unchanged.
} else {
	// Restore the exact pre-e79fe1b4 legacy ModelPriceHelperPerCall,
	// EstimateBilling, per-call/per-second, and AdjustBillingOnSubmit blocks.
}
```

Do not read `ratio_setting.GetVideoResolutionPrice` inside an attempt.

- [ ] **Step 4: Add funding-kind and legacy-snapshot regressions**

```go
func TestUsesResolutionReservationLedgerReadsFrozenKind(t *testing.T) {
	resolution, err := relaycommon.NewVideoResolutionTaskBillingPlan("video", "req-resolution", map[string]float64{"720p": 0.1})
	require.NoError(t, err)
	legacy := relaycommon.NewLegacyTaskBillingPlan("video", "req-legacy")
	assert.True(t, usesResolutionReservationLedger(&relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{BillingPlan: resolution}}))
	assert.False(t, usesResolutionReservationLedger(&relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{BillingPlan: legacy, ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{}}}))
}
func TestTaskBillingContextLegacyFixedPerSecondRemainsPerSecond(t *testing.T) {
	original := ratio_setting.TaskBillingMode2JSONString()
	require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(`{"legacy-video":"per_second"}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateTaskBillingModeByJSONString(original)) })
	info := &relaycommon.RelayInfo{
		OriginModelName: "legacy-video",
		PriceData: hosttypes.PriceData{
			ModelPrice: 0.3,
			UsePrice: true,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	assert.False(t, taskBillingContextFromRelayInfo(info).PerCallBilling)
}
```

- [ ] **Step 5: Bind `NewBillingSession` to plan kind and fix the snapshot regression**

```go
usesReservationLedger := info.TaskRelayInfo != nil &&
	info.TaskRelayInfo.BillingPlan != nil &&
	info.TaskRelayInfo.BillingPlan.Kind() == relaycommon.TaskBillingKindVideoResolution
```

Set `ResolutionReservationFunding.requestId` from `BillingPlan.RequestID()` and assert it equals the frozen request identity; continue using the same `BillingSession` and `Reserve` object for retries.

In the legacy snapshot use only `ratio_setting.IsTaskPerCallBilling(originModelName)`; remove `|| relayInfo.PriceData.UsePrice`. Do not alter resolution task snapshot fields, polling settlement, or refund implementations.

- [ ] **Step 6: Run billing/settlement/refund regressions and commit**

Run: `go test ./relay ./service ./controller -run 'Test(RelayTaskSubmit|NewBillingSession|TaskBillingContext|Settle|Refund|Resolution)' -count=1`

Expected: PASS, including the existing reservation recovery and legacy adjustment tests.

```powershell
git add relay/relay_task.go relay/relay_task_test.go service/billing_session.go service/billing_session_test.go controller/relay.go controller/task_test.go
git commit -m "fix(video): preserve legacy task billing lifecycle"
```

### Task 4: Transactional Pricing Document Store and CAS Writers

**Files:**
- Create: `model/model_pricing_command.go`
- Create: `model/model_pricing_command_test.go`
- Modify: `model/option.go:193-339`
- Modify: `model/option_test.go`
- Modify: `setting/billing_setting/tiered_billing.go` only if a reusable validation entry point is missing

**Interfaces:**
- Produces: `PricingDocuments`, `ModelPricingSelection`, `ModelPricingCommand`, `ModelPricingCommandResult`, `ExecuteModelPricingCommand`, `UpdateOptionCAS`, `UpdateOptionsBulkCAS`, and `OptionConflictError`.
- Consumes later: controllers in Task 5 and frontend payloads in Task 6.

- [ ] **Step 1: Write typed document and transaction tests first**

The new suite must cover resolution save retaining all eleven legacy entries, exact reactivation after table removal, rename semantics, explicit copy replacement, delete, missing row materialization, rollback, and post-commit recovery. Use small fixtures with one `owned`, one `target`, and one `unrelated` model; compare parsed maps, not JSON formatting.

```go
func TestExecuteModelPricingCommandResolutionSavePreservesLegacy(t *testing.T) {
	seedAllPricingDocuments(t, legacyFixture("video", 0.3, "per_call"))
	_, err := ExecuteModelPricingCommand(ModelPricingCommand{
		Kind: PricingCommandSave,
		TargetName: "video",
		Selection: &ModelPricingSelection{Mode: PricingModeVideoResolution, ResolutionPrices: map[string]float64{"720p": 0.1}},
	})
	require.NoError(t, err)
	assertPricingEntry(t, "ModelPrice", "video", 0.3)
	assertPricingEntry(t, "TaskBillingMode", "video", "per_call")
	assertResolutionEntry(t, "video", "720p", 0.1)
}
```

- [ ] **Step 2: Run the new model tests and verify the command types are missing**

Run: `go test ./model -run 'Test(ExecuteModelPricingCommand|UpdateOptionCAS|UpdateOptionsBulkCAS)' -count=1`

Expected: FAIL to compile.

- [ ] **Step 3: Implement typed loading, deterministic locks, and validators**

Define the exact protected keys once:

```go
var modelPricingOptionKeys = []string{
	"AudioCompletionRatio", "AudioRatio", "CacheRatio", "CompletionRatio",
	"CreateCacheRatio", "ImageRatio", "ModelPrice", "ModelRatio",
	"TaskBillingMode", ratio_setting.VideoResolutionPriceOptionKey,
	"billing_setting.billing_expr", "billing_setting.billing_mode",
}

type PricingDocuments struct {
	Numeric         map[string]map[string]float64
	Strings         map[string]map[string]string
	ResolutionPrice map[string]map[string]float64
	Raw             map[string]string
}
```

`lockPricingDocuments(tx)` must create missing rows with `clause.OnConflict{DoNothing:true}`, then read every row in the listed order with `lockForUpdate(tx)`. Defaults come from the current in-memory option value so introducing the transaction does not erase built-ins. Validate every final document before any write; validate expressions through the existing billing-expression setting validator documented in `pkg/billingexpr/expr.md`.

- [ ] **Step 4: Implement semantic save/rename/copy/delete transforms**

```go
type PricingCommandKind string
const (
	PricingCommandSave PricingCommandKind = "save"
	PricingCommandRename PricingCommandKind = "rename"
	PricingCommandCopy PricingCommandKind = "copy"
	PricingCommandDelete PricingCommandKind = "delete"
	PricingCommandReplaceDocuments PricingCommandKind = "replace_documents"
)

type PricingMode string
const (
	PricingModeFixed PricingMode = "per_request"
	PricingModeRatio PricingMode = "per_token"
	PricingModeExpression PricingMode = "tiered_expr"
	PricingModeVideoResolution PricingMode = "video_resolution"
)

type ModelPricingSelection struct {
	Mode                 PricingMode        `json:"mode"`
	ModelPrice           *float64           `json:"price,omitempty"`
	ModelRatio           *float64           `json:"ratio,omitempty"`
	CacheRatio           *float64           `json:"cache_ratio,omitempty"`
	CreateCacheRatio     *float64           `json:"create_cache_ratio,omitempty"`
	CompletionRatio      *float64           `json:"completion_ratio,omitempty"`
	ImageRatio           *float64           `json:"image_ratio,omitempty"`
	AudioRatio           *float64           `json:"audio_ratio,omitempty"`
	AudioCompletionRatio *float64           `json:"audio_completion_ratio,omitempty"`
	BillingExpr          *string            `json:"billing_expr,omitempty"`
	TaskBillingMode      *string            `json:"task_billing_mode,omitempty"`
	ResolutionPrices     map[string]float64 `json:"resolution_prices,omitempty"`
}

type ModelRowMutation struct {
	Kind  string
	Model *Model
	ID    int
}

type ModelPricingCommand struct {
	Kind              PricingCommandKind
	SourceName        string
	TargetName        string
	Selection         *ModelPricingSelection
	ModelMutation     *ModelRowMutation
	Values            map[string]string
	ExpectedDocuments map[string]string
}

type ModelPricingCommandResult struct {
	Committed            bool
	PublicationRecovered bool
	PublicationPending   bool
	Values               map[string]string
}
```

Rules implemented against locked current documents:

- `video_resolution` sets/replaces only `VideoResolutionPrice[target]` and preserves every legacy entry.
- Explicit fixed/ratio/expression saves remove the resolution table and apply the existing mutual exclusion exactly.
- Rename removes every source entry; where a source document had an entry it replaces the target entry, while an absent source entry leaves an unseen target entry intact. An explicit selection may replace the target because it establishes ownership.
- Copy leaves source untouched and deletes/replaces target across all twelve documents.
- Delete removes the name across all twelve documents. When `ModelMutation` is present, create/update/delete the locked model row inside this same transaction; do not call `Model.Insert`, `Model.Update`, or `DeleteModelMetaByID` outside the command.

- [ ] **Step 5: Implement CAS for whole-document replacements**

```go
type OptionConflictError struct { Key, CurrentValue string }

func UpdateOptionsBulkCAS(values, expected map[string]string) error {
	return executePricingTransaction(func(tx *gorm.DB, docs *PricingDocuments) error {
		for key, value := range values {
			if docs.Raw[key] != expected[key] {
				return &OptionConflictError{Key: key, CurrentValue: docs.Raw[key]}
			}
			result := tx.Model(&Option{}).
				Where(commonKeyCol+" = ? AND value = ?", key, expected[key]).
				Update("value", value)
			if result.Error != nil { return result.Error }
			if result.RowsAffected != 1 { return &OptionConflictError{Key: key, CurrentValue: docs.Raw[key]} }
		}
		return nil
	})
}
```

Replace the resolution-only mutex with one process mutex used by every protected-key writer. Keep trusted internal `UpdateOption`/`UpdateOptionsBulk` wrappers, but route them through the same lock/transaction/publish mechanism.

- [ ] **Step 6: Implement publish-after-commit and recovery tests**

No in-memory setting may publish before commit. Publish legacy documents first and `VideoResolutionPrice` last. If an injected publisher fails after commit, the command rereads committed rows and republishes through the same validated, non-failpoint low-level publisher, invalidates pricing/exposed caches, logs the operational error, and returns `ModelPricingCommandResult{Committed:true, PublicationRecovered:true}` with a nil mutation error **only when that republish succeeds**. If reload or republish also fails, return `ModelPricingCommandResult{Committed:true, PublicationPending:true}` with a nil mutation error, log both failures, and leave caches invalidated; never return an ordinary pre-commit failure that invites an unsafe blind retry.

Retain the existing periodic `go model.SyncOptions(common.SyncFrequency)` startup loop in `main.go`. Refactor `loadOptionsFromDatabase`/`SyncOptions` to feed protected pricing documents through the same validated low-level publisher, so this process and other instances converge after a pending publication. Add a test that forces both immediate publish attempts to fail, asserts `PublicationPending`, then invokes the next `loadOptionsFromDatabase` cycle and verifies the committed database values become live.

- [ ] **Step 7: Run model tests and commit**

Run: `go test ./model -run 'Test(ExecuteModelPricingCommand|UpdateOptionCAS|UpdateOptionsBulkCAS|PricingPublication)' -count=1`

Expected: PASS.

```powershell
git add model/model_pricing_command.go model/model_pricing_command_test.go model/option.go model/option_test.go setting/billing_setting/tiered_billing.go
git commit -m "feat(pricing): add atomic model pricing commands"
```

### Task 5: Atomic Model/Option HTTP Contracts

**Files:**
- Modify: `controller/model_meta.go:89-169`
- Modify: `controller/model_meta_test.go`
- Modify: `controller/option.go:118-260`
- Modify: `controller/option_test.go`
- Modify: `model/model_meta.go:34-209`
- Modify: `model/model_meta_test.go`
- Modify: `router/api-router.go:191-204,345-356`

**Interfaces:**
- Consumes: `ExecuteModelPricingCommand` and CAS APIs from Task 4.
- Produces: optional pricing on existing model create/update payloads; root-only `PUT /api/option/pricing`; CAS-enabled `PUT /api/option/`.

- [ ] **Step 1: Write controller contract tests**

Cover:

```text
legacy model create/update payload without pricing stays backward-compatible
metadata-only and status-only updates do not touch pricing
pricing-bearing create/update requires root authorization
rename with omitted pricing still moves all twelve documents and requires root authorization
rename plus all twelve source entries commits atomically
duplicate rename rolls back model and all documents
delete requires root authorization and removes model and all twelve owned entries atomically
PUT /api/option/ requires expected_value for protected documents
stale protected update returns HTTP 409 with key/current_value
PUT /api/option/pricing copy/delete/save is root-only and atomic
```

- [ ] **Step 2: Run controller tests and verify missing request fields/routes fail**

Run: `go test ./controller ./router -run 'Test(ModelMetaPricing|UpdateOptionCAS|PricingCommandRoute)' -count=1`

Expected: FAIL.

- [ ] **Step 3: Extend model requests without breaking old clients**

```go
type ModelMutationRequest struct {
	model.Model
	Pricing *model.ModelPricingSelection `json:"pricing,omitempty"`
}

type OptionUpdateRequest struct {
	Key           string  `json:"key"`
	Value         any     `json:"value"`
	ExpectedValue *string `json:"expected_value,omitempty"`
}
```

When `Pricing == nil` and the model name is unchanged, preserve the current metadata/status behavior. Every name change executes `PricingCommandRename` in the same model transaction even when `Pricing == nil`; nil means move the locked source pricing documents unchanged, while non-nil applies the explicit selection to the renamed target. Keep duplicate-name checks and the unique index. Move the existing resolution-only lifecycle code out of `model_meta.go`; every delete calls the full pricing delete command in the same transaction.

- [ ] **Step 4: Add the root-only semantic pricing endpoint and CAS response**

Register `PUT /api/option/pricing` under the existing `RootAuth` option group. Require root authorization for every pricing-bearing create/save, every model rename, every model delete, copy, and pricing-only delete, because each can implicitly mutate protected documents. Same-name metadata/status updates without pricing retain the existing admin authorization. Add explicit non-root 403 tests for rename and delete. For protected keys, reject missing `expected_value`; on `OptionConflictError`, return:

```go
c.JSON(http.StatusConflict, gin.H{
	"success": false,
	"message": "pricing option changed; reload and retry",
	"data": gin.H{"key": conflict.Key, "current_value": conflict.CurrentValue},
})
```

Other option keys remain backward-compatible.

- [ ] **Step 5: Run focused and package tests, then commit**

Run: `go test ./model ./controller ./router -count=1`

Expected: PASS.

```powershell
git add controller/model_meta.go controller/model_meta_test.go controller/option.go controller/option_test.go model/model_meta.go model/model_meta_test.go router/api-router.go
git commit -m "feat(models): persist pricing atomically"
```

### Task 6: Frontend Pricing Persistence and Atomic API Wiring

**Files:**
- Create: `web/src/features/system-settings/models/model-pricing-persistence.ts`
- Create: `web/src/features/system-settings/models/__tests__/model-pricing-persistence.test.ts`
- Modify: `web/src/features/system-settings/models/model-pricing-core.ts`
- Modify: `web/src/features/system-settings/models/model-pricing-snapshots.ts`
- Modify: `web/src/features/system-settings/models/model-pricing-sheet.tsx`
- Modify: `web/src/features/system-settings/models/model-ratio-visual-editor.tsx`
- Modify: `web/src/features/system-settings/models/ratio-settings-card.tsx`
- Modify: `web/src/features/system-settings/models/upstream-ratio-sync.tsx`
- Modify: `web/src/features/system-settings/api.ts`
- Modify: `web/src/features/system-settings/hooks/use-update-option.ts`
- Modify: `web/src/features/system-settings/types.ts`
- Modify: `web/src/features/models/api.ts`
- Modify: `web/src/features/models/components/drawers/model-mutate-drawer.tsx`
- Modify: `web/src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx`

**Interfaces:**
- Consumes: Task 5 HTTP contracts.
- Produces: pure `applyModelPricingMutation`, `buildModelPricingSelection`, and atomic frontend mutations.

- [ ] **Step 1: Write pure persistence tests before extraction**

```ts
test('resolution save retains the complete legacy snapshot', () => {
  const result = applyModelPricingMutation(documentsFixture(), {
    kind: 'save',
    name: 'video',
    selection: resolutionSelection({ '720p': 0.1 }),
  })
  expect(result.ModelPrice.video).toBe(0.3)
  expect(result.CreateCacheRatio.video).toBe(1.25)
  expect(result['billing_setting.billing_expr'].video).toBe('v1:tier("base", p * 1)')
  expect(result.TaskBillingMode.video).toBe('per_call')
  expect(result.VideoResolutionPrice.video).toEqual({ '720p': 0.1 })
})
```

Also test table removal/reactivation, fixed/ratio/expression mutual exclusion, copy replacing the target while preserving source, delete removing all twelve entries, rename preserving target entries absent at source, and `CreateCacheRatio`.

- [ ] **Step 2: Run the focused tests and verify the helper is missing**

Run from `web/`: `bun test src/features/system-settings/models/__tests__/model-pricing-persistence.test.ts src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx`

Expected: FAIL.

- [ ] **Step 3: Extract the pure transform and retain legacy values in resolution mode**

Define exact document types and a discriminated mutation union; do not put React state in the helper.

```ts
export type PricingDocuments = {
  ModelPrice: Record<string, number>
  ModelRatio: Record<string, number>
  CacheRatio: Record<string, number>
  CreateCacheRatio: Record<string, number>
  CompletionRatio: Record<string, number>
  ImageRatio: Record<string, number>
  AudioRatio: Record<string, number>
  AudioCompletionRatio: Record<string, number>
  'billing_setting.billing_mode': Record<string, string>
  'billing_setting.billing_expr': Record<string, string>
  TaskBillingMode: Record<string, string>
  VideoResolutionPrice: Record<string, Record<string, number>>
}
```

In `model-pricing-sheet.tsx`, resolution submit data carries the loaded legacy values and expression fields unchanged plus the validated table. Suppress the legacy-conflict warning only while resolution mode is active. Make resolution table presence outrank retained expression mode in snapshot/editor mode selection. Treat resolution-only models as priced, not unset.

- [ ] **Step 4: Replace sequential settings writes with one CAS bulk mutation**

Add API methods:

```ts
export async function updatePricingCommand(command: PricingCommandRequest) {
  const response = await api.put('/api/option/pricing', command)
  return response.data
}
```

Keep two distinct snapshots in `ratio-settings-card.tsx`: normalized values for dirty detection and the exact raw strings returned by `GET /api/option/` for CAS. Send changed documents once with `expected_documents` from the raw snapshot; after success replace both snapshots from the command response. Never use pretty-printed or normalized JSON as `expected_value`, because the database CAS compares exact stored bytes. Rewire `upstream-ratio-sync.tsx` to use the same atomic CAS command instead of sequential protected-option calls. Extend `UpdateOptionRequest`/`useUpdateOption` so any remaining protected whole-document update includes its exact raw loaded `expected_value`. On HTTP 409, invalidate/refetch `['system-options']`, keep the editor open, and show an i18n error telling the administrator to review the refreshed values.

- [ ] **Step 5: Send model metadata and pricing in one request**

In the drawer, remove the post-success loop of `updateOption.mutateAsync` and the resolution-document re-fetch. Build one optional pricing selection:

```ts
const response = isEditing && currentModelId
  ? await updateModel({ ...modelData, id: currentModelId, pricing })
  : await createModel({ ...modelData, pricing })
```

Resolution mode sends `mode` plus `resolution_prices`; the backend applies that table to the locked current documents and preserves/moves the retained legacy entries itself, avoiding a stale client rewrite. Explicit legacy switching sends the selected fixed/ratio/expression values and removes the table according to the selected mode. An untouched pricing section sends no `pricing` field: same-name metadata-only edits do not mutate pricing, while a rename still causes the server to move all twelve locked documents atomically. Legacy payloads include `CreateCacheRatio`, billing expression, and `TaskBillingMode` where their existing modes permit them.

- [ ] **Step 6: Add invalid/empty submission behavior tests**

Use the real imperative handle to assert `ModelPricingEditorPanel.commitDraft()` returns `null` for an invalid row and for an empty resolution table. Add a drawer test that submits invalid/empty rows and asserts neither `createModel` nor `updateModel` is called.

- [ ] **Step 7: Run frontend persistence tests, typecheck, and lint; commit**

Run from `web/`:

```powershell
bun test src/features/system-settings/models/__tests__/model-pricing-persistence.test.ts src/features/system-settings/models/__tests__/video-resolution-pricing.test.ts src/features/system-settings/models/__tests__/video-resolution-price-editor.test.tsx src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx
bun run typecheck
bunx oxlint -c .oxlintrc.json src/features/system-settings/models src/features/system-settings/api.ts src/features/system-settings/hooks/use-update-option.ts src/features/models/api.ts src/features/models/components/drawers/model-mutate-drawer.tsx
```

Expected: PASS with zero lint errors in changed files.

```powershell
git add web/src/features/system-settings web/src/features/models/api.ts web/src/features/models/components/drawers/model-mutate-drawer.tsx web/src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx
git commit -m "feat(web): save model pricing atomically"
```

### Task 7: Public Pricing Legacy Fallback

**Files:**
- Modify: `model/pricing.go:350-420`
- Modify: `model/pricing_endpoint_test.go`
- Modify: `web/src/features/pricing/lib/model-helpers.ts`
- Modify: `web/src/features/pricing/lib/__tests__/resolution-price.test.ts`
- Modify: `web/src/features/pricing/components/model-card.tsx`
- Modify: `web/src/features/pricing/components/pricing-columns.tsx`
- Modify: `web/src/features/pricing/components/model-details.tsx`
- Create: `web/src/features/pricing/components/__tests__/legacy-video-pricing.test.tsx`

**Interfaces:**
- Consumes: authoritative activation rule and stored pricing state.
- Produces: unchanged pricing response type with either resolution contract or legacy price/ratio contract.

- [ ] **Step 1: Add backend endpoint regressions**

```go
func TestPricingExposesLegacyFixedVideoWithoutResolutionTable(t *testing.T) {
	pricing := legacyPricingForModel(t, "zz-video-fixed", 0.3, 0, ratio_setting.TaskBillingModePerCall)
	assert.Equal(t, 1, pricing.QuotaType)
	assert.Equal(t, 0.3, pricing.ModelPrice)
	assert.Equal(t, ratio_setting.TaskBillingModePerCall, pricing.TaskBillingMode)
	assert.Empty(t, pricing.ResolutionPrices)
}

func TestPricingExposesLegacyRatioVideoWithoutResolutionTable(t *testing.T) {
	pricing := legacyPricingForModel(t, "zz-video-ratio", 0, 1.5, ratio_setting.TaskBillingModePerSecond)
	assert.Equal(t, 0, pricing.QuotaType)
	assert.Equal(t, 1.5, pricing.ModelRatio)
	assert.Equal(t, ratio_setting.TaskBillingModePerSecond, pricing.TaskBillingMode)
	assert.Empty(t, pricing.ResolutionPrices)
}

func TestPricingResolutionTableWinsOverRetainedLegacy(t *testing.T) {
	pricing := resolutionPricingForModel(t, "zz-video-resolution", map[string]float64{"720p": 0.1}, ratio_setting.TaskBillingModePerCall)
	assert.Equal(t, 1, pricing.QuotaType)
	assert.Equal(t, 0.1, pricing.ModelPrice)
	assert.Equal(t, ratio_setting.TaskBillingModePerSecond, pricing.TaskBillingMode)
	assert.Equal(t, map[string]float64{"720p": 0.1}, pricing.ResolutionPrices)
}
```

Implement `legacyPricingForModel` beside the existing `resolutionPricingForModel`: reset the pricing test tables, install `{}` as the resolution document, install either the exact model-price or model-ratio entry and task mode, insert one enabled video channel/ability, invalidate pricing, and return the matching `Pricing`. Before exposing resolution pricing, the backend applies the shared `constant.IsSunoModel(modelName)` classifier. Add a `suno_music` fixture with both a matching resolution table and legacy price and assert the response contains only the legacy contract, independent of channel selection.

- [ ] **Step 2: Add a user-visible card regression**

Render `ModelCard` with an `openai-video` fixture containing `model_price` and `task_billing_mode` but no `resolution_prices`. Assert the price/unit is visible and no “Unsupported” state appears. Update helper tests to cover legacy per-call and per-second formatting instead of the superseded strict rollout.

- [ ] **Step 3: Run tests and verify the current unsupported overlay fails**

Run:

```powershell
go test ./model -run 'TestPricing.*Video' -count=1
Set-Location web
bun test src/features/pricing/lib/__tests__/resolution-price.test.ts src/features/pricing/components/__tests__/legacy-video-pricing.test.tsx
```

Expected: frontend FAIL because missing resolution tables are currently forced to unsupported.

- [ ] **Step 4: Remove only the strict unsupported branches**

Delete `isVideoModelMissingResolutionPrices` and its branches/imports from card, table, and both details sections. Let existing `formatRequestPrice`, minimum resolution tier, and `ModelBillingModeBadge` logic render the contract. Backend keeps exact/compact-wildcard activation and uses the same `constant.IsSunoModel` classifier as request freezing before exposing a resolution table; frontend must not add provider-name heuristics.

- [ ] **Step 5: Run focused tests, typecheck, lint, and commit**

Run from `web/`:

```powershell
bun test src/features/pricing/lib/__tests__/resolution-price.test.ts src/features/pricing/components/__tests__/legacy-video-pricing.test.tsx
bun run typecheck
bunx oxlint -c .oxlintrc.json src/features/pricing
```

Expected: PASS.

```powershell
git add model/pricing.go model/pricing_endpoint_test.go web/src/features/pricing
git commit -m "fix(pricing): show legacy video prices"
```

### Task 8: Cross-Lifecycle Regression Suite

**Files:**
- Modify: `relay/relay_task_test.go`
- Modify: `service/task_billing_test.go`
- Modify: `controller/task_test.go`
- Modify: `model/model_pricing_command_test.go`
- Modify: frontend tests from Tasks 6 and 7 only where a missing cross-boundary contract is found

**Interfaces:**
- Consumes: all production interfaces from Tasks 1-7.
- Produces: no new production API.

- [ ] **Step 1: Add configuration-change-between-retries tests**

Use two deterministic adaptors/channels. Freeze a resolution table, make attempt one retryable, remove/change the live option, and assert attempt two uses the frozen table and reservation ledger. Mirror it for a legacy request: add a resolution table between attempts and assert attempt two remains legacy and uses the original billing session/refund path.

- [ ] **Step 2: Add running-task settlement/refund tests**

Create one persisted legacy task and one persisted resolution task, mutate every live pricing option, then settle/fail them. Assert each consumes/refunds from its stored `TaskBillingContext` and reservation identifier exactly as before. Do not add a new task schema.

- [ ] **Step 3: Add wildcard and live-channel-capability tests**

One compact wildcard table must activate two concrete non-Suno model names independently. Toggle compatible channel availability and assert routing availability changes without mutating the stored table or public pricing response. A Suno request matching the same wildcard remains legacy.

- [ ] **Step 4: Add concurrent writer regression**

Block a lifecycle transaction after row locks, start a CAS raw writer from the old snapshot, release the lifecycle transaction, and assert the CAS writer receives `OptionConflictError` rather than overwriting the lifecycle mutation. Repeat with the operations reversed and assert the lifecycle command rebases on locked current documents.

- [ ] **Step 5: Run all affected backend and frontend tests; commit**

Run:

```powershell
go test ./setting/ratio_setting ./relay/common ./relay/helper ./relay ./middleware ./model ./service ./controller ./router -count=1
Set-Location web
bun test src/features/pricing/lib/__tests__/resolution-price.test.ts src/features/pricing/components/__tests__/legacy-video-pricing.test.tsx src/features/system-settings/models/__tests__/video-resolution-pricing.test.ts src/features/system-settings/models/__tests__/video-resolution-price-editor.test.tsx src/features/system-settings/models/__tests__/model-pricing-persistence.test.ts src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx
```

Expected: PASS.

```powershell
git add relay service controller model web/src/features/pricing web/src/features/system-settings/models web/src/features/models/components/drawers/__tests__
git commit -m "test(video): cover pricing compatibility lifecycle"
```

### Task 9: Full Verification and Independent Review

**Files:**
- Verify: all files changed by Tasks 1-8
- Modify: only files required to resolve concrete review findings

**Interfaces:**
- Consumes: completed implementation.
- Produces: verified branch ready for user review.

- [ ] **Step 1: Run formatting**

Run:

```powershell
$goFiles = git diff --name-only --diff-filter=ACM 31965da4 -- '*.go'
gofmt -w $goFiles
Set-Location web
bun run format
```

Expected: commands exit 0 and only intended files change.

- [ ] **Step 2: Run complete backend verification**

Run from repository root:

```powershell
go test ./setting/ratio_setting ./relay/common ./relay/helper ./relay ./middleware ./model ./service ./controller ./router -count=1
go test ./... -count=1
go build ./...
```

Expected: PASS; build exits 0.

- [ ] **Step 3: Run complete frontend verification**

Run from `web/`:

```powershell
bun test
bun run typecheck
bunx oxlint -c .oxlintrc.json src/features/pricing src/features/system-settings/models src/features/system-settings/api.ts src/features/system-settings/hooks/use-update-option.ts src/features/models/api.ts src/features/models/components/drawers/model-mutate-drawer.tsx
bun run format:check
bun run i18n:sync
bun run build
```

Expected: PASS, no changed-file lint errors, synchronized locale report, production build succeeds.

- [ ] **Step 4: Run independent code review gates**

Request separate correctness, data-integrity, concurrency/reliability, API-contract, testing, and frontend TypeScript reviews against the approved spec. Resolve every blocker/high finding, rerun its focused test, and then rerun Steps 2-3. The authoring agent must not self-approve.

- [ ] **Step 5: Inspect final diff and commit review fixes**

Run:

```powershell
git status --short
git diff --check
git diff --stat 31965da4..HEAD
git log --oneline 31965da4..HEAD
```

Expected: no whitespace errors, no unrelated files, and every task commit present.

If review fixes exist, stage only the implementation directories in this plan and create the review-fix commit:

```powershell
git add relay middleware service model controller router web/src/features/pricing web/src/features/system-settings web/src/features/models/api.ts web/src/features/models/components/drawers/model-mutate-drawer.tsx
git commit -m "fix(video): address compatibility review"
```
