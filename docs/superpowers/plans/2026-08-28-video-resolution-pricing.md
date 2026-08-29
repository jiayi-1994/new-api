# Video Resolution Pricing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Price every video request per second from its model, effective output resolution, and bounded duration, and reject unconfigured resolutions before pre-consume or upstream submission.

**Architecture:** Add a validated standalone VideoResolutionPrice option and a video-only resolver contract implemented by each task adapter. The relay asks the adapter for the exact resolution and duration used by its final upstream payload, builds PriceData from the matching per-second price, and always applies bounded duration; administration and public pricing surfaces consume the same nested map. Existing TaskBillingMode and legacy task-pricing paths remain unchanged and are never consulted by the resolution-pricing path.

**Tech Stack:** Go 1.22+, Gin, GORM v2, testify, React 19, TypeScript, Base UI/shadcn composition, React Hook Form, Zod, node:test, Bun, i18next.

## Global Constraints

- Missing model or resolution entries fail with HTTP 400 code video_resolution_not_supported; video pricing never falls back to ModelPrice, model ratios, nearest resolution, or multiplier 1.
- VideoResolutionPrice is independent of TaskBillingMode; existing TaskBillingMode storage, APIs, UI, environment compatibility, and legacy consumers remain unchanged.
- Effective resolution must match the final upstream payload after metadata, size, resolution, multipart, model mapping, and provider defaults are applied.
- Canonical keys are trimmed lowercase values matching ^[1-9][0-9]{2,4}p$ or ^[1-9][0-9]*k$; dimension aliases are mapped only by provider adapters.
- Resolution pricing always applies resolution price, bounded effective duration, group ratio, and allowed independent ratios exactly once.
- The only initially allowed independent ratio is Doubao video_input.
- Duration remains bounded by relaycommon.MaxTaskDurationSeconds; quota conversion uses checked helpers and preserves saturation auditing.
- Task snapshots mark pricing_kind=video_resolution and freeze selected per-second price, group ratio, independent ratios, and submitted duration; polling settlement never re-reads live pricing, TaskBillingMode, or token billing.
- Provider capabilities/defaults/independent ratios use UpstreamModelName; only the configured price lookup uses OriginModelName.
- VideoResolutionPrice is validated and published independently; this feature does not add a public bulk-option API or couple writes to TaskBillingMode.
- Public Pricing.ModelPrice is the minimum valid resolution price for legacy summary consumers, while relay billing ignores that compatibility field.
- All Go JSON operations use common wrappers.
- No relational schema migration is introduced; snapshot additions remain optional JSON and work on SQLite, MySQL, and PostgreSQL.
- New or substantially rewritten Go tests use testify/require and testify/assert.
- Frontend forms use FieldGroup/Field, accessible labels, functional state updates, and no new dependency.
- Locale JSON is written only by web/scripts/add-missing-keys.mjs followed by bun run i18n:sync.
- Preserve all protected project and organization identity references.

## File Structure

| Path | Responsibility |
| --- | --- |
| common/video_resolution.go | Canonical resolution validation. |
| setting/ratio_setting/video_resolution_price.go | Validated nested option storage and model matching. |
| relay/common/video_billing.go | Effective resolution, duration, and independent-ratio value object. |
| relay/channel/adapter.go | Optional VideoBillingResolver interface. |
| relay/helper/video_price.go | Safe PriceData construction from a selected resolution price. |
| relay/relay_task.go | Strict selection before pre-consume and retry-safe billing. |
| relay/channel/task/*/adaptor.go | Provider-owned effective resolution/default mapping. |
| model/task.go and service/task_billing.go | Frozen billing snapshot, video-only settlement, and audit fields. |
| model/pricing.go | Public resolution_prices contract. |
| web/src/features/system-settings/models/video-resolution-pricing.ts | Shared row/map normalization and validation. |
| web/src/features/system-settings/models/video-resolution-price-editor.tsx | Repeatable accessible row editor. |
| web/src/features/system-settings/models/* | Visual/JSON settings and CRUD persistence. |
| web/src/features/models/components/drawers/model-mutate-drawer.tsx | Model create/edit path. |
| web/src/features/pricing/* | Public minimum summaries and full resolution table. |

Official adapter references:

- https://platform.openai.com/docs/api-reference/videos
- https://cloud.google.com/vertex-ai/generative-ai/docs/video/generate-videos-from-text
- https://cloud.google.com/vertex-ai/generative-ai/docs/models/veo/3-1-generate
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/video/generate-videos-from-text
- https://help.aliyun.com/en/model-studio/text-to-video-api-reference
- https://api.volcengine.com/api-docs/view?action=CreateContentsGenerationsTasks&serviceCode=ark&version=2024-01-01
- https://platform.minimaxi.com/docs/api-reference/video-generation-i2v
- https://platform.vidu.com/docs/text-to-video

---

### Task 1: Independent Resolution Price Configuration

**Files:**
- Create: common/video_resolution.go
- Create: common/video_resolution_test.go
- Modify: common/json.go
- Test: common/json_test.go
- Create: setting/ratio_setting/video_resolution_price.go
- Create: setting/ratio_setting/video_resolution_price_test.go
- Modify: setting/ratio_setting/model_ratio.go
- Modify: model/option.go
- Modify: controller/option.go
- Modify: router/api-router.go
- Modify: setting/ratio_setting/exposed_cache.go
- Test: model/option_test.go
- Test: controller/option_test.go

**Interfaces:**
- Produces: NormalizeVideoResolutionKey(value string) (string, error).
- Produces: GetVideoResolutionPrice(model, resolution string) (float64, bool).
- Produces: GetVideoResolutionPrices, GetVideoResolutionPriceMap, HasVideoResolutionPrice, VideoResolutionPrice2JSONString, ValidateVideoResolutionPriceByJSONString, and UpdateVideoResolutionPriceByJSONString.
- Produces: persisted option key VideoResolutionPrice.
- Preserves: all pre-existing TaskBillingMode functions and behavior without coupling them to VideoResolutionPrice.

- [x] **Step 1: Write failing normalization, isolation, and update tests**

~~~go
func TestNormalizeVideoResolutionKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: " 1080P ", want: "1080p", ok: true},
		{input: "4K", want: "4k", ok: true},
		{input: "1920x1080", ok: false},
		{input: "uhd", ok: false},
	}
	for _, tc := range tests {
		got, err := NormalizeVideoResolutionKey(tc.input)
		if tc.ok {
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		} else {
			assert.Error(t, err)
		}
	}
}

func TestInvalidResolutionPriceDoesNotReplaceLiveConfig(t *testing.T) {
	require.NoError(t, UpdateVideoResolutionPriceByJSONString("{\"sora-2\":{\"720p\":0.1}}"))
	require.Error(t, UpdateVideoResolutionPriceByJSONString("{\"sora-2\":{\"720P\":0.1,\"720p\":0.2}}"))
	price, ok := GetVideoResolutionPrice("sora-2", "720p")
	assert.True(t, ok)
	assert.Equal(t, 0.1, price)
}
~~~

Add named tests `TestNormalizeVideoResolutionKeyRejectsEmptyAndNonCanonicalValues`, `TestValidateJSONNoDuplicateKeys`, `TestUpdateVideoResolutionPriceRejectsNonPositiveAndNonFinitePrices`, `TestVideoResolutionPriceRejectsIdenticalRawJSONKeys`, `TestGetVideoResolutionPriceUsesCompactWildcardModel`, `TestGetVideoResolutionPriceMapReturnsDeepCopy`, `TestUpdateOptionRejectsInvalidVideoResolutionPriceWithoutPersisting`, `TestVideoResolutionPriceUpdateLeavesTaskBillingModeUntouched`, `TestTaskBillingModeUpdateLeavesVideoResolutionPriceUntouched`, `TestLoadOptionsFromDatabasePublishesVideoResolutionPriceIndependently`, `TestConcurrentVideoResolutionPriceUpdatesPublishLatestDatabaseValue`, and `TestExposedDataIncludesVideoResolutionPriceCopy`. The price-validation table covers JSON `0`, negative values, string values, parser-rejected `NaN`/`Infinity`, exact duplicate JSON keys, and normalization collisions; every invalid row asserts an error and an unchanged live map. The isolation tests seed different TaskBillingMode and VideoResolutionPrice values, update one option through the existing single-option path, and assert the other persisted/live option remains byte-for-byte unchanged. The concurrency test pauses the first publisher while holding the standalone price-option write lock, starts a second price update, proves it cannot publish out of order, releases the first, and then asserts the database option, `common.OptionMap`, live nested map, and exposed/pricing cache view all contain the second value. The deep-copy test mutates both a returned inner map and the outer map, then reads again and asserts the store is unchanged.

- [x] **Step 2: Run tests and confirm RED**

~~~powershell
go test ./common ./setting/ratio_setting ./model ./controller -count=1
~~~

Expected on the current v1.4 worktree: the new isolation tests fail because the first Task 1 implementation paired VideoResolutionPrice with TaskBillingMode and exposed a bulk route; duplicate-key tests remain red until the common validator is retained in the standalone parser.

- [x] **Step 3: Implement normalization, standalone storage, and option lifecycle**

~~~go
const VideoResolutionPriceOptionKey = "VideoResolutionPrice"

func NormalizeVideoResolutionKey(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if !videoResolutionKeyPattern.MatchString(normalized) {
		return "", fmt.Errorf("invalid canonical video resolution: %s", value)
	}
	return normalized, nil
}

func GetVideoResolutionPrice(model, resolution string) (float64, bool) {
	prices, ok := GetVideoResolutionPrices(model)
	if !ok {
		return 0, false
	}
	resolution, err := common.NormalizeVideoResolutionKey(resolution)
	if err != nil {
		return 0, false
	}
	price, ok := prices[resolution]
	return price, ok
}
~~~

Parse VideoResolutionPrice into a temporary nested map, use the common duplicate-key validator before map decoding, validate and normalize it completely, then replace the live map in one lock operation. Do not use RWMap.LoadFromJsonString because it clears before parsing. Add a model-package `videoResolutionPriceOptionMu` that protects only this one option's database read/write plus post-commit publication; `UpdateOption`, an internal `UpdateOptionsBulk` containing this key, `loadOptionsFromDatabase`, and `SyncOptions` all acquire it before their price transaction/read and hold it through `updateOptionMap`, `common.OptionMap`, pricing-cache, and exposed-cache publication. Provide a package-level publisher seam for deterministic ordering tests. This lock never protects or reads TaskBillingMode. Register independent initialization, validation-before database save, completion-ratio metadata model discovery, exposed output, pricing-cache invalidation, and exposed-cache invalidation. Remove the feature-added `PUT /api/option/bulk` route/handler and every VideoResolutionPrice/TaskBillingMode pair lock, combined getter, combined publisher, and special pair branch. Restore the pre-feature TaskBillingMode RWMap implementation in `model_ratio.go`; do not modify its public API, compact wildcard behavior, or TASK_PRICE_PATCH compatibility. Keep the existing internal `model.UpdateOptionsBulk` implementation generic for its pre-existing consumers, adding only the same standalone price lock when its values contain VideoResolutionPrice.

- [x] **Step 4: Run tests and confirm GREEN**

Run the Step 2 command. Expected: all tests pass and invalid input leaves persisted/live configuration unchanged.

- [x] **Step 5: Commit**

~~~powershell
git add common/json.go common/json_test.go common/video_resolution.go common/video_resolution_test.go setting/ratio_setting/video_resolution_price.go setting/ratio_setting/video_resolution_price_test.go setting/ratio_setting/model_ratio.go model/option.go model/option_test.go controller/option.go controller/option_test.go router/api-router.go setting/ratio_setting/exposed_cache.go
git commit -m "refactor(video): isolate resolution price configuration" -m "Constraint: Existing TaskBillingMode behavior must remain unchanged" -m "Rejected: Pair resolution prices with TaskBillingMode | resolution pricing is per-second only" -m "Confidence: high" -m "Scope-risk: moderate"
~~~

### Task 2: Video Billing Contract and Safe Price Math

**Files:**
- Create: relay/common/video_billing.go
- Create: relay/helper/video_price.go
- Create: relay/helper/video_price_test.go
- Modify: relay/channel/adapter.go
- Modify: relay/common/relay_info.go
- Modify: relay/common/relay_utils.go
- Modify: model/task.go
- Create: model/task_billing_context_test.go

**Interfaces:**
- Consumes: Task 1 price lookup.
- Produces: relaycommon.VideoBillingSelection.
- Produces: relaycommon.ResolvedVideoBilling containing the cloned selection and selected per-second direct price.
- Produces: relaycommon.CalculateVideoResolutionQuota, the shared pure checked formula used at pre-consume and settlement.
- Produces: optional channel.VideoBillingResolver.
- Produces: helper.BuildVideoResolutionPriceData returning PriceData, optional QuotaClamp, and error.
- Produces: optional TaskBillingContext resolution snapshot fields and TaskInfo.EffectiveDurationSeconds used by Tasks 3-5.

- [x] **Step 1: Write failing formula tests**

~~~go
func TestBuildVideoResolutionPriceDataAlwaysMultipliesDuration(t *testing.T) {
	selection := relaycommon.VideoBillingSelection{
		EffectiveResolution:      "1080p",
		EffectiveDurationSeconds: 8,
		IndependentRatios:        map[string]float64{"video_input": 1.5},
	}
	priceData, clamp, err := BuildVideoResolutionPriceData(
		testContext(), testRelayInfo(), 0.1, selection,
	)
	require.NoError(t, err)
	assert.Nil(t, clamp)
	assert.Equal(t, 600, priceData.Quota)
}
~~~

The fixture uses QuotaPerUnit=500 and group ratio=1. Add an explicit table alongside it:

~~~go
func TestBuildVideoResolutionPriceDataRejectsUnsafeInputs(t *testing.T) {
	tests := []struct {
		name      string
		price     float64
		seconds   int
		ratios    map[string]float64
		wantClamp bool
	}{
		{name: "zero duration", price: 0.1, seconds: 0},
		{name: "duration above maximum", price: 0.1, seconds: relaycommon.MaxTaskDurationSeconds + 1},
		{name: "unknown independent ratio", price: 0.1, seconds: 1, ratios: map[string]float64{"size": 2}},
		{name: "non-positive price", price: 0, seconds: 1},
		{name: "saturated product", price: math.MaxFloat64, seconds: 1, wantClamp: true},
	}
	for _, tc := range tests {
		selection := relaycommon.VideoBillingSelection{
			EffectiveResolution:      "1080p",
			EffectiveDurationSeconds: tc.seconds,
			IndependentRatios:        tc.ratios,
		}
		priceData, clamp, err := BuildVideoResolutionPriceData(
			testContext(), testRelayInfo(), tc.price, selection,
		)
		if tc.wantClamp {
			require.NoError(t, err)
			require.NotNil(t, clamp)
			assert.Equal(t, int(math.MaxInt32), priceData.Quota)
			continue
		}
		assert.Error(t, err)
		assert.Nil(t, clamp)
	}
}

func TestTaskBillingContextVideoResolutionFieldsRoundTrip(t *testing.T) {
	want := TaskBillingContext{
		PricingKind:              "video_resolution",
		EffectiveResolution:      "1080p",
		SelectedResolutionPrice:  0.18,
		EffectiveDurationSeconds: 8,
		IndependentRatios:        map[string]float64{"video_input": 1.2},
	}
	raw, err := common.Marshal(want)
	require.NoError(t, err)
	var got TaskBillingContext
	require.NoError(t, common.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}
~~~

- [x] **Step 2: Run the test and confirm RED**

~~~powershell
go test ./relay/common ./relay/helper ./model -run 'Test(CalculateVideoResolutionQuota|BuildVideoResolutionPriceData|TaskBillingContextVideoResolutionFieldsRoundTrip)' -count=1
~~~

Expected: compilation fails because the value object and builder are undefined.

- [x] **Step 3: Implement the optional contract and checked formula**

~~~go
type VideoBillingSelection struct {
	EffectiveResolution      string
	EffectiveDurationSeconds int
	IndependentRatios        map[string]float64
}

type ResolvedVideoBilling struct {
	Selection               VideoBillingSelection
	SelectedResolutionPrice float64
}

type VideoBillingResolver interface {
	ResolveVideoBilling(
		c *gin.Context,
		info *relaycommon.RelayInfo,
	) (relaycommon.VideoBillingSelection, *taskdto.TaskError)
}
~~~

Do not add this method to TaskAdaptor or BaseBilling. Add `Resolution string` with JSON tag `resolution,omitempty` to `TaskSubmitReq` so multipart/provider-normalized values reach the resolver. Add optional `PricingKind`, `EffectiveResolution`, `SelectedResolutionPrice`, `EffectiveDurationSeconds`, and `IndependentRatios` fields to TaskBillingContext and `EffectiveDurationSeconds` to TaskInfo now, before provider/remix tasks consume them; do not add BillingUnit and do not change legacy PerCallBilling behavior. Implement `CalculateVideoResolutionQuota(resolutionPrice float64, durationSeconds int, groupRatio float64, independentRatios map[string]float64) (int, *common.QuotaClamp, error)` in `relay/common/video_billing.go`: validate positive bounded duration, add allowlisted independent ratios, apply seconds exactly once, calculate resolutionPrice * QuotaPerUnit * group ratio, and use QuotaFromFloatChecked. `helper.BuildVideoResolutionPriceData` has no billing-mode parameter; it adds request/group context and delegates to that pure function. After strict lookup, store an immutable ResolvedVideoBilling with only selection and selected price on TaskRelayInfo; clone the independent-ratio map so adapter-owned maps cannot mutate after pre-consume. Add a regression that seeds legacy TaskBillingMode=per_call for the same model and proves this builder still multiplies duration.

- [x] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: exact quotas, validation failures, and clamp propagation pass.

- [x] **Step 5: Commit**

~~~powershell
git add relay/common/video_billing.go relay/channel/adapter.go relay/common/relay_info.go relay/common/relay_utils.go relay/helper/video_price.go relay/helper/video_price_test.go model/task.go model/task_billing_context_test.go
git commit -m "feat(video): add per-second resolution billing contract" -m "Constraint: Resolution pricing always multiplies bounded duration" -m "Rejected: Read TaskBillingMode | legacy configuration must not affect this path" -m "Confidence: high" -m "Scope-risk: moderate"
~~~

### Task 3: Strict Relay Selection and Sora Mapping

**Files:**
- Modify: relay/relay_task.go
- Modify: relay/relay_task_test.go
- Modify: relay/channel/task/sora/adaptor.go
- Create: relay/channel/task/sora/video_billing_test.go
- Modify: relay/common/relay_utils.go
- Modify: relay/common/relay_utils_test.go

**Interfaces:**
- Consumes: Tasks 1 and 2.
- Produces: strict selection before pre-consume.
- Produces: Sora mapping 720x1280/1280x720 to 720p and 1024x1792/1792x1024 to 1024p.

- [x] **Step 1: Write failing relay and Sora tests**

~~~go
func TestRelayTaskSubmitRejectsUnconfiguredResolutionBeforeRequest(t *testing.T) {
	selection := relaycommon.VideoBillingSelection{
		EffectiveResolution:      "1080p",
		EffectiveDurationSeconds: 5,
	}
	fake := newVideoTaskAdaptor(selection)
	originalFactory := getTaskAdaptor
	getTaskAdaptor = func(constant.TaskPlatform) channel.TaskAdaptor { return fake }
	t.Cleanup(func() { getTaskAdaptor = originalFactory })
	require.NoError(t, ratio_setting.UpdateVideoResolutionPriceByJSONString(
		`{"client-model":{"720p":0.1}}`,
	))
	result, taskErr := RelayTaskSubmit(c, info)
	assert.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "video_resolution_not_supported", taskErr.Code)
	assert.Contains(t, taskErr.Message, "1080p")
	assert.False(t, fake.didRequest)
}
~~~

Add concrete tests named `TestRelayTaskSubmitUsesOriginalModelForResolutionPrice`, `TestRelayTaskSubmitResolutionPriceAlwaysMultipliesDuration`, `TestRelayTaskSubmitResolutionPricingIgnoresLegacyPerCallMode`, `TestRelayTaskSubmitRejectsVideoAdaptorWithoutResolver`, `TestSoraResolveVideoBillingDefaultsTo720pAndFourSeconds`, `TestSoraResolveVideoBillingMapsHighDimensionsTo1024p`, `TestSoraResolveVideoBillingRejectsUnsupportedDimensions`, `TestSoraRemixVideoBillingRestoresSavedSelection`, `TestSoraRemixVideoBillingRecovers720pFromLegacyTaskData`, `TestSoraRemixVideoBillingRecovers1024pFromLegacyTaskData`, and `TestSoraRemixVideoBillingRejectsWhenSnapshotAndTaskDataHaveNoResolution`. Each test asserts the returned tier/duration, exact quota or 400 code, and whether pre-consume/upstream request spies were called. The legacy-mode regression configures TaskBillingMode=per_call for the same model but still expects `selected price × duration`. Define `getTaskAdaptor = GetTaskAdaptor` as a package seam in `relay_task.go`; the test restores it with `t.Cleanup`, and the fake embeds the existing task test base while recording pre-consume and `DoRequest` calls.

- [x] **Step 2: Run tests and confirm RED**

~~~powershell
go test ./relay ./relay/common ./relay/channel/task/sora -run 'Test(RelayTaskSubmit.*VideoResolution|SoraResolveVideoBilling|SoraRemixVideoBilling|ValidateMultipartDirect)' -count=1
~~~

Expected: relay still falls back to ModelPrice and Sora still emits a size multiplier.

- [x] **Step 3: Implement strict relay order and Sora resolver**

~~~go
func videoResolutionNotSupported(modelName, resolution string) *dto.TaskError {
	if resolution == "" {
		resolution = "unknown"
	}
	return service.TaskErrorWrapperLocal(
		fmt.Errorf("video resolution %s is not configured for model %s", resolution, modelName),
		"video_resolution_not_supported",
		http.StatusBadRequest,
	)
}
~~~

After model mapping, require VideoBillingResolver for video adapters; Suno keeps the old task price path. Normalize selection, look up only OriginModelName plus tier through GetVideoResolutionPrice, create ResolvedVideoBilling without a unit, build per-second PriceData, attach clamp, then pre-consume. Do not call GetTaskBillingMode, IsTaskPerCallBilling, or legacy EstimateBilling in this new branch. Preserve the old Sora EstimateBilling implementation for legacy callers; bypassing it in the resolution branch prevents its size ratio from double charging without changing old behavior. OpenAI remix first uses the frozen billing snapshot, then attempts the existing `task.Data.size` recovery for legacy tasks; only a task with neither a saved selection nor recoverable provider size returns unknown. The two high Sora dimensions map to `1024p`, never `1080p`.

- [x] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: no fallback, no upstream call on missing tier, and Sora high tier is 1024p.

- [x] **Step 5: Commit**

~~~powershell
git add relay/relay_task.go relay/relay_task_test.go relay/common/relay_utils.go relay/common/relay_utils_test.go relay/channel/task/sora/adaptor.go relay/channel/task/sora/video_billing_test.go
git commit -m "feat(video): price Sora by effective resolution" -m "Constraint: Sora dimensions map to provider tiers 720p and 1024p" -m "Rejected: Treat high Sora size as 1080p | contradicts upstream dimensions" -m "Confidence: high" -m "Scope-risk: broad" -m "Directive: Unconfigured tiers fail before upstream submission"
~~~

### Task 4: Remaining Provider Resolvers

**Files:**
- Modify: relay/channel/task/gemini/billing.go
- Modify: relay/channel/task/gemini/billing_test.go
- Modify: relay/channel/task/gemini/adaptor.go
- Modify: relay/channel/task/vertex/adaptor.go
- Create: relay/channel/task/vertex/adaptor_test.go
- Modify: relay/channel/task/ali/adaptor.go
- Modify: relay/channel/task/ali/adaptor_test.go
- Modify: relay/channel/task/doubao/adaptor.go
- Modify: relay/channel/task/doubao/constants.go
- Create: relay/channel/task/doubao/adaptor_test.go
- Modify: relay/channel/task/hailuo/adaptor.go
- Create: relay/channel/task/hailuo/adaptor_test.go
- Modify: relay/channel/task/vidu/adaptor.go
- Create: relay/channel/task/vidu/adaptor_test.go

**Interfaces:**
- Consumes: VideoBillingResolver.
- Produces: provider-owned canonical tier/defaults and allowed independent ratios for the new branch while preserving legacy multiplier functions.

- [x] **Step 1: Write failing payload-parity tests**

~~~go
func TestVeoVideoBillingMatchesPayload(t *testing.T) {
	selection, taskErr := adaptor.ResolveVideoBilling(c, info)
	require.Nil(t, taskErr)
	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	var payload VeoRequestPayload
	require.NoError(t, common.DecodeJson(body, &payload))
	assert.Equal(t, payload.Parameters.Resolution, selection.EffectiveResolution)
	assert.Equal(t, payload.Parameters.DurationSeconds, selection.EffectiveDurationSeconds)
}

func TestDoubaoVideoInputIndependentRatioUsesSelectedTier(t *testing.T) {
	withoutVideo, ok := GetVideoInputIndependentRatio(
		"doubao-seedance-2-0-260128", "1080p", false,
	)
	require.True(t, ok)
	withVideo, ok := GetVideoInputIndependentRatio(
		"doubao-seedance-2-0-260128", "1080p", true,
	)
	require.True(t, ok)
	assert.Equal(t, 1.0, withoutVideo)
	assert.InDelta(t, 31.0/51.0, withVideo, 1e-9)
}
~~~

Add exact provider contract tests named `TestAliVideoBillingUsesMappedUpstreamModelDefaultAndOriginPriceKey`, `TestAliVideoBillingRejectsConflictingSizeAndResolution`, `TestAliVideoBillingCollapsesEquivalentSizeAndResolutionToOneField`, `TestAliVideoBillingUsesWan27TextToVideo1080pDefault`, `TestAliVideoBillingRejectsUnknownDefault`, `TestDoubaoVideoBillingUsesMappedModelCapabilitiesAndOriginPriceKey`, `TestDoubaoVideoBillingUsesPerModelDocumentedDefaults`, `TestDoubaoVideoBillingRejects1080pForLiteI2VAndSeedance20Fast`, `TestDoubaoVideoBillingRejectsUnsupportedTierAndUnknownDuration`, `TestVeo30VideoBillingAllows720pAnd1080pButRejects4k`, `TestVeo31PreviewVideoBillingAllows4k`, `TestVeoVideoBillingDefaultsTo720p`, `TestHailuoVideoBillingMatches512p720p768pAnd1080pPayloads`, `TestViduVideoBillingDefaultsViduQ1To1080p`, `TestViduVideoBillingDefaultsViduQ2To720p`, `TestProviderVideoBillingMetadataOverrideMatchesPayload`, and `TestUnsupportedVideoAdaptorRejectsKlingAndJimengBeforeRequest`. Assert both the decoded final upstream body and billing selection for every successful row; assert the mapped upstream model drives capabilities/defaults/independent ratios while the original model drives the configured price key. Unknown/conflicting rows assert HTTP 400 and a zero upstream-call count.

- [x] **Step 2: Run tests and confirm RED**

~~~powershell
go test ./relay ./relay/channel/task/gemini ./relay/channel/task/vertex ./relay/channel/task/ali ./relay/channel/task/doubao ./relay/channel/task/hailuo ./relay/channel/task/vidu -run 'Test(VeoVideoBilling|VertexVideoBilling|AliVideoBilling|DoubaoVideo|GetVideoInputIndependentRatio|HailuoVideoBilling|ViduVideoBilling|UnsupportedVideoAdaptor)' -count=1
~~~

Expected: resolvers/defaults and the same-tier independent ratio are absent.

- [x] **Step 3: Implement final-payload resolvers**

Gemini/Vertex reuse one Veo parameter resolver but preserve the existing VeoResolutionRatio and old EstimateBilling behavior for legacy callers. Encode an upstream-model capability table instead of the existing dimension heuristic in the new resolver: Veo 3.0 accepts `720p`/`1080p`; Veo 3.1 Preview additionally accepts `4k`; every current Veo model defaults to `720p`. Ali changes only shared final-payload normalization needed by the resolver so all protocol branches and defaults use `info.UpstreamModelName`; preserve ProcessAliOtherRatios and old EstimateBilling. The normalizer emits exactly one of Size or Resolution, collapses proven equivalent aliases, and rejects conflicting selectors before billing. Set only documented defaults such as `wan2.7-t2v=1080p`, otherwise return unknown. Doubao uses `UpstreamModelName` for its capability/default and a new `GetVideoInputIndependentRatio` same-tier lookup, while relay price lookup remains on `OriginModelName`; do not change the existing GetVideoInputRatio or its legacy callers. Its explicit table uses `1080p` for Seedance 1.0 Pro/Pro Fast, `720p` for 1.0 Lite/1.5 Pro/2.0/2.0 Fast, rejects 1080p for 1.0 Lite reference-image requests and 2.0 Fast, and admits only tiers documented for that upstream model/scenario. An omitted duration without a trustworthy default is rejected instead of guessed. Hailuo reuses convertToRequestPayload and explicit ModelConfig supported resolutions; invalid input cannot silently choose another tier. Vidu reuses final payload and model defaults (viduq1=1080p, viduq2=720p). Every resolver omits `size`/`resolution` ratios; only Doubao may return `video_input`. When a polling response explicitly reports actual duration, its parser writes the bounded value to `TaskInfo.EffectiveDurationSeconds`; absent duration remains zero so settlement retains the submitted snapshot value. Kling/Jimeng remain unsupported until a trustworthy fixed tier exists. Suno remains non-video.

- [x] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: new-path billing equals payload, resolver selections contain no resolution ratio, legacy multiplier tests remain unchanged, Doubao is not double-counted, and unknown adapters fail before pre-consume.

- [x] **Step 5: Commit**

~~~powershell
git add relay/channel/task/gemini relay/channel/task/vertex relay/channel/task/ali relay/channel/task/doubao relay/channel/task/hailuo relay/channel/task/vidu relay/relay_task_test.go
git commit -m "feat(video): resolve provider pricing tiers" -m "Constraint: Only final-payload or documented defaults are billable" -m "Rejected: Generic dimension inference | provider tiers are protocol-specific" -m "Confidence: medium" -m "Scope-risk: broad" -m "Not-tested: Providers without a trustworthy resolution remain unsupported"
~~~

### Task 5: Snapshots, Logs, Settlement, and Pricing API

**Files:**
- Modify: model/task.go
- Modify: model/model_meta.go
- Create: model/model_meta_test.go
- Modify: controller/relay.go
- Modify: relay/common/relay_info.go
- Modify: service/task_billing.go
- Modify: service/task_billing_test.go
- Modify: service/task_polling.go
- Modify: service/task_polling_test.go
- Modify: model/pricing.go
- Modify: model/pricing_endpoint_test.go
- Modify: relay/helper/price.go
- Modify: controller/model_meta.go
- Create: controller/model_meta_test.go
- Modify: dto/task.go
- Modify: controller/task.go
- Modify: controller/task_test.go
- Create: controller/ratio_sync_test.go

**Interfaces:**
- Consumes: selected resolution, per-second price, duration, and ratios from the relay.
- Produces: frozen video-resolution snapshot and dedicated settlement branch.
- Produces: optional public resolution_prices plus legacy non-zero minimum model_price.
- Produces: transactional model rename/delete cleanup for VideoResolutionPrice only.
- Produces: enhanced `func (mi *Model) Update() error` and `func DeleteModelMetaByID(id int) error` lifecycle entry points.

- [x] **Step 1: Write failing persistence and API tests**

~~~go
func TestPricingEndpointExposesResolutionPrices(t *testing.T) {
	pricing := pricingForModelWithResolutionPrices(t, "video-model", map[string]float64{
		"720p":  0.10,
		"1080p": 0.18,
	})
	assert.Equal(t, 1, pricing.QuotaType)
	assert.Equal(t, ratio_setting.TaskBillingModePerSecond, pricing.TaskBillingMode)
	assert.Equal(t, map[string]float64{"720p": 0.10, "1080p": 0.18}, pricing.ResolutionPrices)
}

func TestResolutionSnapshotOmitsBillingUnitAndLegacyPerCallFlag(t *testing.T) {
	context := newResolutionTaskBillingContext()
	assert.Equal(t, "video_resolution", context.PricingKind)
	assert.False(t, context.PerCallBilling)
	assert.Equal(t, 8, context.EffectiveDurationSeconds)
}
~~~

Add named tests that assert the exact boundaries: `TestTaskBillingContextRoundTripsFrozenResolutionSelection`, `TestResolutionPricingAdminLogIncludesPerSecondSelection`, `TestResolutionPricingUserLogOmitsAdminPricingFields`, `TestResolutionPricedTaskPollingSettlesPerSecondDifference`, `TestResolutionSettlementAlwaysUsesDuration`, `TestResolutionSettlementIgnoresLegacyTaskBillingModePerCall`, `TestResolutionSettlementRunsBeforeAdaptorAndTokenFallback`, `TestResolutionSettlementIgnoresTotalTokensAndResidualModelRatio`, `TestResolutionSettlementUsesSnapshotAfterLiveConfigurationChanges`, `TestResolutionSettlementOnlyAcceptsBoundedActualDuration`, `TestResolutionSettlementAuditsQuotaSaturation`, `TestHasModelBillingConfigIncludesResolutionOnlyModel`, `TestPricingLegacySummaryUsesMinimumResolutionPrice`, `TestPricingResolutionModelReportsDerivedPerSecondUnit`, `TestPricingResolutionModelIgnoresLegacyPerCallMode`, `TestModelMetaRenameMovesOnlyVideoResolutionPriceAtomically`, `TestModelMetaDeleteRemovesOnlyVideoResolutionPriceAtomically`, `TestModelMetaResolutionPriceMutationRollsBackWithModelWrite`, `TestModelMetaLifecycleDoesNotOverwriteConcurrentVideoResolutionPriceUpdate`, and `TestRatioSyncLeavesNestedResolutionPricesUnchanged`.

The token-fallback test seeds a stale positive ModelRatio and `TaskInfo.TotalTokens`, then proves settlement uses `SelectedResolutionPrice × frozen GroupRatio × frozen independent ratios × authoritative duration`. The legacy-mode test seeds TaskBillingMode=per_call for the same model and proves both pre-consume and settlement still multiply duration while the old non-resolution task test remains unchanged. The configuration-change test mutates live VideoResolutionPrice after submission and asserts the original snapshot still settles exactly. The bounded-duration table covers zero/absent (retain submitted duration), a valid different duration (settle difference), negative/over-limit (warn and retain pre-consume), and a saturating product (MaxInt32 plus admin audit marker). Lifecycle tests seed VideoResolutionPrice and unrelated legacy TaskBillingMode entries, rename/delete a model, and prove the model row plus only the nested VideoResolutionPrice document commit or roll back together while TaskBillingMode stays byte-for-byte unchanged. The concurrent lifecycle test uses the Task 1 publisher seam to pause rename/delete between commit and publication, starts a normal VideoResolutionPrice update, proves that update waits on the same standalone mutex, then releases both and asserts database, OptionMap, live price map, and caches reflect commit order. The ratio-sync test seeds a local nested value, imports a type-2 pricing response containing a conflicting `resolution_prices` object, and asserts the local nested option is byte-for-byte unchanged.

- [x] **Step 2: Run tests and confirm RED**

~~~powershell
go test ./model ./service ./controller ./relay/helper -run 'Test(TaskBillingContext|ResolutionPriced|ResolutionSettlement|Pricing.*Resolution|HasModelBillingConfig.*Resolution|ModelMeta.*VideoResolutionPrice|RatioSync.*ResolutionPrice)' -count=1
~~~

Expected: missing fields/classification and the existing PriceData.UsePrice shortcut incorrectly marks per-second fixed prices as per-call.

- [x] **Step 3: Implement JSON snapshot and public contract**

Populate the optional TaskBillingContext and TaskInfo fields introduced in Task 2. For new video-resolution tasks set `PricingKind="video_resolution"`, freeze every input at submission, leave PerCallBilling false, and do not add BillingUnit; retain all existing fields and PerCallBilling behavior only for historical/legacy paths.

Implement `CalculateVideoResolutionSnapshotQuota` in `service/task_billing.go`. It accepts only the frozen snapshot plus an effective duration, validates the snapshot and duration, then delegates to `relaycommon.CalculateVideoResolutionQuota`; pre-consume and settlement therefore cannot drift. In `settleTaskBillingOnComplete`, check `PricingKind` before legacy PerCallBilling, `AdjustBillingOnComplete`, and `TotalTokens`: use a valid positive TaskInfo duration when supplied, otherwise the submitted duration, and then call RecalculateTaskQuota with the returned clamp. Invalid upstream duration cannot change quota. Resolution, selected price, group ratio, or ratios returned after submission are ignored, and neither TaskBillingMode nor live pricing is read.

Add an optional `TaskBillingDetails` DTO containing resolution, selected per-second price, submitted/effective duration, and independent ratios; populate it only in `tasksToDto(..., fillUser=true)` so administrator task details can audit the selection and ordinary user task responses cannot see the private snapshot. `LogTaskConsumption` reads resolution/selected price/duration from `ResolvedVideoBilling`, never calls the live `IsTaskPerCallBilling` lookup for this branch, and records independent ratios separately from duration so the log matches the frozen snapshot even if configuration changes concurrently.

Add ResolutionPrices map[string]float64 to Pricing, classify resolution-priced models as fixed price, derive their compatibility TaskBillingMode as the constant per_second without reading the legacy map, set ModelPrice to the minimum valid tier for old summary consumers, include them in HasModelBillingConfig, and update pricing version. Relay billing continues to use only the strict nested lookup, never this minimum compatibility field.

Keep model-meta request JSON unchanged, but make rename and delete transactional with the standalone VideoResolutionPrice option. Both lifecycle entry points acquire Task 1's `videoResolutionPriceOptionMu` before starting the transaction and hold it through the post-commit price/OptionMap/cache publication, using the same publisher seam and lock order as normal option updates. `Model.Update` reads and locks the existing model row and VideoResolutionPrice option in one transaction, moves the old nested key only when the name changes, updates the model row, commits, and then publishes the prevalidated price document. `DeleteModelMetaByID` loads the model name, removes only that nested price key, deletes the model row, commits, and publishes after commit; the controller calls this function instead of `DB.Delete` directly. If the option row is absent, create a `{}` default inside the same transaction rather than reading live memory. Use `lockForUpdate(tx)` and deterministic row order for MySQL/PostgreSQL while remaining valid on SQLite. TaskBillingMode and every other option are untouched. A model/option failure rolls back both rows and does not publish live state. Administration Tasks 6 and 7 still transform and persist the complete document when the user edits or copies prices; the backend lifecycle path protects API/old-client rename and deletion. Keep ratio synchronization flat-only: an upstream `resolution_prices` field is ignored and must not create, overwrite, or clear local VideoResolutionPrice configuration.

- [x] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: per-second polling uses only frozen resolution inputs, legacy per-call/token/adaptor quota fallbacks are bypassed for resolution tasks, public legacy and new pricing fields are non-zero, and user/admin visibility is separated.

- [x] **Step 5: Commit**

~~~powershell
git add model/task.go model/model_meta.go model/model_meta_test.go model/pricing.go model/pricing_endpoint_test.go controller/relay.go controller/model_meta.go controller/model_meta_test.go controller/task.go controller/task_test.go controller/ratio_sync_test.go dto/task.go relay/common/relay_info.go service/task_billing.go service/task_billing_test.go service/task_polling.go service/task_polling_test.go relay/helper/price.go
git commit -m "feat(video): persist and expose resolution pricing" -m "Constraint: Per-second fixed prices remain adjustable during polling" -m "Rejected: Treat all direct prices as per-call | breaks duration billing" -m "Confidence: high" -m "Scope-risk: broad"
~~~

### Task 6: Frontend Pricing State and Settings Persistence

**Files:**
- Create: web/src/features/system-settings/models/video-resolution-pricing.ts
- Create: web/src/features/system-settings/models/__tests__/video-resolution-pricing.test.ts
- Modify: web/src/features/system-settings/types.ts
- Modify: web/src/features/system-settings/billing/index.tsx
- Modify: web/src/features/system-settings/billing/section-registry.tsx
- Modify: web/src/features/system-settings/models/index.tsx
- Modify: web/src/features/system-settings/models/ratio-settings-card.tsx
- Modify: web/src/features/system-settings/models/model-ratio-form.tsx
- Modify: web/src/features/system-settings/models/model-pricing-core.ts
- Modify: web/src/features/system-settings/models/model-pricing-snapshots.ts

**Interfaces:**
- Consumes: backend VideoResolutionPrice option.
- Produces: VideoResolutionPriceMap, row validation, and PricingMode video_resolution.
- Produces: buildVideoResolutionOptionUpdate(...) returning one existing-option mutation without TaskBillingMode.

- [x] **Step 1: Write failing pure-state tests**

~~~ts
test('rejects duplicate normalized rows', () => {
  const result = validateVideoResolutionPriceRows([
    { id: 1, resolution: ' 720P ', price: '0.10' },
    { id: 2, resolution: '720p', price: '0.20' },
  ])
  assert.equal(result.prices, null)
  assert.equal(result.errorsByRowId[2]?.resolution, 'duplicate')
})

test('builds video resolution snapshot without ModelPrice', () => {
  const snapshots = buildModelSnapshots(
    snapshotInput({
      videoResolutionPrice: '{"sora-2":{"720p":0.1,"1024p":0.2}}',
    })
  )
  assert.equal(snapshots[0].billingMode, 'video_resolution')
  assert.deepEqual(snapshots[0].resolutionPrices, {
    '720p': 0.1,
    '1024p': 0.2,
  })
})

test('video resolution snapshot ignores legacy per-call task mode', () => {
  const snapshots = buildModelSnapshots(
    snapshotInput({
      videoResolutionPrice: '{"sora-2":{"720p":0.1}}',
      taskBillingMode: '{"sora-2":"per_call"}',
    })
  )
  assert.equal(snapshots[0].billingMode, 'video_resolution')
  assert.equal(snapshots[0].displayUnit, 'per_second')
})
~~~

- [x] **Step 2: Run tests and confirm RED**

~~~powershell
Push-Location web
bun test src/features/system-settings/models/__tests__/video-resolution-pricing.test.ts
Pop-Location
~~~

Expected: missing validation module and snapshot field.

- [x] **Step 3: Implement types, validation, and option plumbing**

~~~ts
export type VideoResolutionPriceMap = Record<string, number>
export type VideoResolutionPriceOption = Record<
  string,
  VideoResolutionPriceMap
>
export type VideoResolutionPriceRow = {
  id: number
  resolution: string
  price: string
}
export type PricingMode =
  | 'per-token'
  | 'per-request'
  | 'video_resolution'
  | 'tiered_expr'

export function buildVideoResolutionOptionUpdate(input: {
  oldName?: string
  newName?: string
  videoResolutionPrice: VideoResolutionPriceOption
}): { key: 'VideoResolutionPrice'; value: string }
~~~

Validate canonical regex, duplicates, and finite positive values; serialize sorted maps. Add VideoResolutionPrice to every settings/default/form/reset/diff path. Snapshot precedence is tiered_expr, video_resolution, per-request, per-token. Include sorted maps in signatures and configured-base checks. Save VideoResolutionPrice independently through the existing single-option mutation hook. A video_resolution snapshot always renders `/second` and never reads or emits TaskBillingMode. Preserve the existing TaskBillingMode fields and save behavior for ordinary fixed-price/per-request mode, and add a regression proving that saving VideoResolutionPrice does not generate a TaskBillingMode update.

- [x] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: normalization, invalid values, duplicates, stable serialization, and mode inference pass.

- [x] **Step 5: Commit**

~~~powershell
git add web/src/features/system-settings/models/video-resolution-pricing.ts web/src/features/system-settings/models/__tests__/video-resolution-pricing.test.ts web/src/features/system-settings/types.ts web/src/features/system-settings/billing/index.tsx web/src/features/system-settings/billing/section-registry.tsx web/src/features/system-settings/models/index.tsx web/src/features/system-settings/models/ratio-settings-card.tsx web/src/features/system-settings/models/model-ratio-form.tsx web/src/features/system-settings/models/model-pricing-core.ts web/src/features/system-settings/models/model-pricing-snapshots.ts
git commit -m "feat(web): model video resolution pricing state" -m "Constraint: Resolution pricing is always per-second and independent of TaskBillingMode" -m "Rejected: Add a bulk pricing-unit mutation | no related unit exists" -m "Confidence: high" -m "Scope-risk: moderate"
~~~

### Task 7: Administration Editors

**Files:**
- Create: web/src/features/system-settings/models/video-resolution-price-editor.tsx
- Create: web/src/features/system-settings/models/__tests__/video-resolution-price-editor.test.tsx
- Modify: web/src/features/system-settings/models/model-pricing-sheet.tsx
- Modify: web/src/features/system-settings/models/model-ratio-visual-editor.tsx
- Modify: web/src/features/system-settings/models/model-ratio-table-columns.tsx
- Modify: web/src/features/models/components/drawers/model-mutate-drawer.tsx
- Create: web/src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx

**Interfaces:**
- Consumes: Task 6 row state.
- Produces: accessible row editing plus rename/copy persistence; Task 5 guarantees delete and rename cleanup for API clients.

- [x] **Step 1: Write failing user-visible tests**

~~~tsx
test('blocks save for duplicate canonical resolutions', async () => {
	const view = renderResolutionEditor()
	await view.addRow({ resolution: '720P', price: '0.10' })
	await view.addRow({ resolution: '720p', price: '0.20' })
	assert.equal(view.commit(), null)
	assert.match(view.getErrorForRow(2), /only be configured once/i)
	assert.equal(view.getResolutionInput(2).getAttribute('aria-invalid'), 'true')
})

test('rename moves resolution prices without coupling TaskBillingMode', () => {
	const update = buildVideoResolutionOptionUpdate({
		oldName: 'video-old',
		newName: 'video-new',
		videoResolutionPrice: { 'video-old': { '720p': 0.1 } },
	})
	assert.equal(update.key, 'VideoResolutionPrice')
	assert.deepEqual(JSON.parse(update.value), {
		'video-new': { '720p': 0.1 },
	})
	assert.equal('TaskBillingMode' in update, false)
})
~~~

Use node:test, node:assert, happy-dom, and React act like the existing tool-price validation test.

- [x] **Step 2: Run tests and confirm RED**

~~~powershell
Push-Location web
bun test src/features/system-settings/models/__tests__/video-resolution-price-editor.test.tsx src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx
Pop-Location
~~~

Expected: editor and new mode controls are absent.

- [x] **Step 3: Implement Base UI composition and both admin paths**

~~~ts
export type VideoResolutionPriceEditorProps = {
  rows: VideoResolutionPriceRow[]
  errorsByRowId: Record<number, VideoResolutionPriceRowErrors>
  disabled?: boolean
  onChange: (rows: VideoResolutionPriceRow[]) => void
}
~~~

Use FieldGroup/Field, labels, Input, FieldError, and translated icon-button aria-labels. Use functional updates for current-row changes. Add a fourth video_resolution tab containing only repeatable resolution/price rows plus a fixed per-second explanation and preview; do not render a billing-unit selector in this mode. Include VideoResolutionPrice in memo equality and batch-copy paths, but copy/save only this standalone option and never clear, copy, or rewrite a target model's TaskBillingMode. Add `buildVideoResolutionOptionUpdate` to the Task 6 state module; it returns one `{ key: 'VideoResolutionPrice', value: string }` update after applying add or rename to the complete nested document. In the model drawer, append that update to the existing `useUpdateOption().mutateAsync` sequence only when the complete VideoResolutionPrice document changed; do not add a backend `pricing_options` field or attach a TaskBillingMode update for video mode. Task 5 performs transactional cleanup for model rename/delete, so existing single and batch delete actions need no pricing-specific request and cannot leave stale entries. Preserve the existing per-request TaskBillingMode selector and submit behavior with a regression. Clear mutually exclusive flat price/ratio/expression values through the existing model pricing flow when saving this mode.

- [x] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: add/edit/remove, invalid state, fixed per-second preview, uncoupled rename/copy, and legacy fixed-price TaskBillingMode regression pass.

- [x] **Step 5: Commit**

~~~powershell
git add web/src/features/system-settings/models/video-resolution-price-editor.tsx web/src/features/system-settings/models/__tests__/video-resolution-price-editor.test.tsx web/src/features/system-settings/models/model-pricing-sheet.tsx web/src/features/system-settings/models/model-ratio-visual-editor.tsx web/src/features/system-settings/models/model-ratio-table-columns.tsx web/src/features/models/components/drawers/model-mutate-drawer.tsx web/src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx
git commit -m "feat(web): edit per-second video prices by resolution" -m "Constraint: Resolution mode must not modify legacy TaskBillingMode" -m "Rejected: Show a per-call selector | resolution pricing supports only per-second" -m "Confidence: high" -m "Scope-risk: broad"
~~~

### Task 8: Public Pricing, Seven Locales, and Final Verification

**Files:**
- Modify: web/src/features/pricing/types.ts
- Modify: web/src/features/pricing/lib/model-helpers.ts
- Modify: web/src/features/pricing/lib/price.ts
- Modify: web/src/features/pricing/lib/filters.ts
- Create: web/src/features/pricing/lib/__tests__/resolution-price.test.ts
- Modify: web/src/features/pricing/components/model-card.tsx
- Modify: web/src/features/pricing/components/pricing-columns.tsx
- Modify: web/src/features/pricing/components/model-details.tsx
- Modify: web/src/features/pricing/components/model-billing-mode-badge.tsx
- Create: web/src/features/pricing/components/__tests__/video-resolution-pricing.test.tsx
- Generate by script: web/src/i18n/locales/en.json, zh.json, zh-TW.json, fr.json, ja.json, ru.json, vi.json

**Interfaces:**
- Consumes: resolution_prices; legacy task_billing_mode remains only for ordinary fixed-price models.
- Produces: minimum per-second summaries, full per-second tables, and translated copy.

- [x] **Step 1: Write failing pricing tests**

~~~ts
test('uses minimum configured tier for fixed per-second summaries', () => {
  const model = pricingModel({
    resolution_prices: { '720p': 0.1, '1080p': 0.18 },
  })
  assert.equal(getMinimumResolutionPrice(model), 0.1)
  assert.equal(isResolutionPricedModel(model), true)
})
~~~

Add tests named `renders From with minimum per-second resolution price`, `ignores stale per_call task mode for resolution-priced models`, `details list every resolution sorted numerically with per-second units`, `ordinary fixed-price models retain existing task billing mode`, `resolution prices honor group multiplier and recharge conversion`, and `invalid resolution price values are omitted instead of rendered as zero`. Use a `720p: 0.1, 1080p: 0.18` fixture, including a stale `task_billing_mode: per_call` variant, assert the exact localized per-second text and tier ordering, then inject `0`, negative, `NaN`, and string values through a casted API fixture and assert none is shown as a valid price.

- [x] **Step 2: Run tests and confirm RED**

~~~powershell
Push-Location web
bun test src/features/pricing/lib/__tests__/resolution-price.test.ts src/features/pricing/components/__tests__/video-resolution-pricing.test.tsx
Pop-Location
~~~

Expected: helper exports and table rendering are absent; existing formatting reads missing model_price as zero.

- [x] **Step 3: Implement pricing helpers, UI, and scripted translations**

~~~ts
export function getResolutionPriceEntries(
  model: PricingModel
): Array<[string, number]> {
  return Object.entries(model.resolution_prices ?? {})
    .filter(([, price]) => Number.isFinite(price) && price > 0)
    .sort(([left], [right]) =>
      left.localeCompare(right, undefined, { numeric: true })
    )
}

export function getMinimumResolutionPrice(
  model: PricingModel
): number | null {
  const prices = getResolutionPriceEntries(model).map(([, price]) => price)
  return prices.length === 0 ? null : Math.min(...prices)
}

export function isResolutionPricedModel(model: PricingModel): boolean {
  return getResolutionPriceEntries(model).length > 0
}
~~~

Use minimum price in cards/tables/filters/group summaries and full entries in details. Resolution-priced models always render per_second before consulting any legacy task_billing_mode; ordinary fixed-price models preserve existing behavior. Add translations for Video resolution, Resolution prices, Resolution, USD price per second, Add resolution, Remove resolution, No resolution prices configured, Resolution is required, Use a canonical resolution such as 720p or 4k, Each resolution can only be configured once, Price is required, Price must be a finite number greater than zero, From, Prices shown per second, and Resolution prices are always charged per second. Populate all seven locale maps in add-missing-keys.mjs, execute it once, delete it, then run i18n sync.

The temporary script uses this exact translation matrix; it updates only absent/different keys through parsed JSON and never performs string replacement:

~~~js
const translations = {
  'Video resolution': {
    en: 'Video resolution', zh: '视频分辨率', 'zh-TW': '影片解析度', fr: 'Résolution vidéo', ja: '動画解像度', ru: 'Разрешение видео', vi: 'Độ phân giải video',
  },
  'Resolution prices': {
    en: 'Resolution prices', zh: '分辨率价格', 'zh-TW': '解析度價格', fr: 'Prix par résolution', ja: '解像度別の価格', ru: 'Цены по разрешению', vi: 'Giá theo độ phân giải',
  },
  Resolution: {
    en: 'Resolution', zh: '分辨率', 'zh-TW': '解析度', fr: 'Résolution', ja: '解像度', ru: 'Разрешение', vi: 'Độ phân giải',
  },
  'USD price per second': {
    en: 'USD price per second', zh: '每秒美元价格', 'zh-TW': '每秒美元價格', fr: 'Prix par seconde en USD', ja: '1秒あたりの価格（USD）', ru: 'Цена за секунду в USD', vi: 'Giá USD mỗi giây',
  },
  'Add resolution': {
    en: 'Add resolution', zh: '添加分辨率', 'zh-TW': '新增解析度', fr: 'Ajouter une résolution', ja: '解像度を追加', ru: 'Добавить разрешение', vi: 'Thêm độ phân giải',
  },
  'Remove resolution': {
    en: 'Remove resolution', zh: '移除分辨率', 'zh-TW': '移除解析度', fr: 'Supprimer la résolution', ja: '解像度を削除', ru: 'Удалить разрешение', vi: 'Xóa độ phân giải',
  },
  'No resolution prices configured': {
    en: 'No resolution prices configured', zh: '未配置分辨率价格', 'zh-TW': '尚未設定解析度價格', fr: 'Aucun prix par résolution configuré', ja: '解像度別の価格が設定されていません', ru: 'Цены по разрешению не настроены', vi: 'Chưa cấu hình giá theo độ phân giải',
  },
  'Resolution is required': {
    en: 'Resolution is required', zh: '请输入分辨率', 'zh-TW': '請輸入解析度', fr: 'La résolution est obligatoire', ja: '解像度は必須です', ru: 'Укажите разрешение', vi: 'Cần nhập độ phân giải',
  },
  'Use a canonical resolution such as 720p or 4k': {
    en: 'Use a canonical resolution such as 720p or 4k', zh: '请使用规范分辨率，例如 720p 或 4k', 'zh-TW': '請使用標準解析度，例如 720p 或 4k', fr: 'Utilisez une résolution canonique telle que 720p ou 4k', ja: '720p や 4k などの標準的な解像度を使用してください', ru: 'Используйте стандартное разрешение, например 720p или 4k', vi: 'Dùng độ phân giải chuẩn như 720p hoặc 4k',
  },
  'Each resolution can only be configured once': {
    en: 'Each resolution can only be configured once', zh: '每个分辨率只能配置一次', 'zh-TW': '每個解析度只能設定一次', fr: 'Chaque résolution ne peut être configurée qu’une seule fois', ja: '各解像度は1回だけ設定できます', ru: 'Каждое разрешение можно настроить только один раз', vi: 'Mỗi độ phân giải chỉ được cấu hình một lần',
  },
  'Price is required': {
    en: 'Price is required', zh: '请输入价格', 'zh-TW': '請輸入價格', fr: 'Le prix est obligatoire', ja: '価格は必須です', ru: 'Укажите цену', vi: 'Cần nhập giá',
  },
  'Price must be a finite number greater than zero': {
    en: 'Price must be a finite number greater than zero', zh: '价格必须是大于零的有限数值', 'zh-TW': '價格必須是大於零的有限數值', fr: 'Le prix doit être un nombre fini supérieur à zéro', ja: '価格は0より大きい有限の数値にしてください', ru: 'Цена должна быть конечным числом больше нуля', vi: 'Giá phải là số hữu hạn lớn hơn không',
  },
  From: {
    en: 'From', zh: '起', 'zh-TW': '起', fr: 'À partir de', ja: '最低', ru: 'От', vi: 'Từ',
  },
  'Prices shown per second': {
    en: 'Prices shown per second', zh: '显示的是每秒价格', 'zh-TW': '顯示每秒價格', fr: 'Prix affichés par seconde', ja: '1秒ごとの価格を表示', ru: 'Цены указаны за секунду', vi: 'Giá hiển thị theo mỗi giây',
  },
  'Resolution prices are always charged per second.': {
    en: 'Resolution prices are always charged per second.', zh: '分辨率价格始终按秒计费。', 'zh-TW': '解析度價格一律按秒計費。', fr: 'Les prix par résolution sont toujours facturés à la seconde.', ja: '解像度別の価格は常に1秒単位で課金されます。', ru: 'Цены по разрешению всегда тарифицируются посекундно.', vi: 'Giá theo độ phân giải luôn được tính theo giây.',
  },
}
~~~

- [x] **Step 4: Run complete verification**

~~~powershell
go test ./common ./setting/ratio_setting ./relay/common ./relay/helper ./relay ./relay/channel/task/sora ./relay/channel/task/gemini ./relay/channel/task/vertex ./relay/channel/task/ali ./relay/channel/task/doubao ./relay/channel/task/hailuo ./relay/channel/task/vidu ./model ./service ./controller -count=1
go build ./...

Push-Location web
bun test src/features/system-settings/models/__tests__/video-resolution-pricing.test.ts src/features/system-settings/models/__tests__/video-resolution-price-editor.test.tsx src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx src/features/pricing/lib/__tests__/resolution-price.test.ts src/features/pricing/components/__tests__/video-resolution-pricing.test.tsx
bun run typecheck
bun run lint
bun run i18n:sync
bun run format:check
bun run build
Pop-Location
~~~

Expected: all commands exit zero. Confirm by tests that different request resolutions produce different charges, resolution pricing always multiplies bounded duration and renders per second even when legacy TaskBillingMode is per_call, existing ordinary fixed-price TaskBillingMode behavior is unchanged, defaults match payloads, missing tiers fail before pre-consume/upstream calls, mapped models use original names for price, resolution is not an OtherRatio, and snapshots/admin/public UI agree.

- [x] **Step 5: Dispatch independent reviews and commit verified fixes**

Review fixed range f8fa8445..HEAD against the design and this plan with separate correctness, billing-safety, frontend, and maintainability reviewers. Resolve all critical/high findings and rerun affected commands.

~~~powershell
git status --short
git diff --check
git add --all
git commit -m "feat(video): complete resolution-based pricing" -m "Constraint: Final behavior preserves strict no-fallback billing" -m "Confidence: high" -m "Scope-risk: broad"
~~~

Skip this commit only when the worktree is clean after review.
