# Video Resolution Pricing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Price every video request from its model and effective output resolution, using either per-task or per-second billing, and reject unconfigured resolutions before pre-consume or upstream submission.

**Architecture:** Add a validated VideoResolutionPrice option and a video-only resolver contract implemented by each task adapter. The relay asks the adapter for the exact resolution and duration used by its final upstream payload, builds PriceData from the matching direct price, and applies duration only for per_second; administration and public pricing surfaces consume the same nested map.

**Tech Stack:** Go 1.22+, Gin, GORM v2, testify, React 19, TypeScript, Base UI/shadcn composition, React Hook Form, Zod, node:test, Bun, i18next.

## Global Constraints

- Missing model or resolution entries fail with HTTP 400 code video_resolution_not_supported; video pricing never falls back to ModelPrice, model ratios, nearest resolution, or multiplier 1.
- All configured resolutions of one model share TaskBillingMode; absent mode means per_second.
- Effective resolution must match the final upstream payload after metadata, size, resolution, multipart, model mapping, and provider defaults are applied.
- Canonical keys are trimmed lowercase values matching ^[1-9][0-9]{2,4}p$ or ^[1-9][0-9]*k$; dimension aliases are mapped only by provider adapters.
- per_call applies resolution price, group ratio, and allowed independent ratios; per_second additionally applies bounded effective duration.
- The only initially allowed independent ratio is Doubao video_input.
- Duration remains bounded by relaycommon.MaxTaskDurationSeconds; quota conversion uses checked helpers and preserves saturation auditing.
- Task snapshots mark pricing_kind=video_resolution and freeze selected price, group ratio, independent ratios, unit, and submitted duration; polling settlement never re-reads live pricing or falls back to token billing.
- Provider capabilities/defaults/independent ratios use UpstreamModelName; only the configured price lookup uses OriginModelName.
- VideoResolutionPrice and TaskBillingMode are validated and committed together; model create/rename/delete plus their pricing changes use one database transaction before one in-memory publish.
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
| setting/ratio_setting/video_resolution_price.go | Atomic nested option storage and model matching. |
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

### Task 1: Validated Resolution Price Configuration

**Files:**
- Create: common/video_resolution.go
- Create: common/video_resolution_test.go
- Create: setting/ratio_setting/video_resolution_price.go
- Create: setting/ratio_setting/video_resolution_price_test.go
- Modify: model/option.go
- Modify: controller/option.go
- Modify: router/api-router.go
- Modify: setting/ratio_setting/exposed_cache.go
- Test: model/option_test.go
- Test: controller/option_test.go

**Interfaces:**
- Produces: NormalizeVideoResolutionKey(value string) (string, error).
- Produces: GetVideoResolutionPrice(model, resolution string) (float64, bool).
- Produces: GetVideoResolutionBillingConfig(model, resolution string) (price float64, mode string, ok bool) as the atomic reader used by relay billing.
- Produces: GetVideoResolutionPrices, GetVideoResolutionPriceMap, HasVideoResolutionPrice, VideoResolutionPrice2JSONString, ValidateVideoResolutionPriceByJSONString, and UpdateVideoResolutionPriceByJSONString.
- Produces: persisted option key VideoResolutionPrice.
- Produces: PUT /api/option/bulk for atomic related-option updates.

- [ ] **Step 1: Write failing normalization and atomic-update tests**

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

Add named tests `TestNormalizeVideoResolutionKeyRejectsEmptyAndNonCanonicalValues`, `TestUpdateVideoResolutionPriceRejectsNonPositiveAndNonFinitePrices`, `TestGetVideoResolutionPriceUsesCompactWildcardModel`, `TestGetVideoResolutionPriceMapReturnsDeepCopy`, `TestUpdateOptionRejectsInvalidVideoResolutionPriceWithoutPersisting`, `TestUpdateTaskBillingModeRejectsInvalidExplicitUnit`, `TestUpdateOptionsBulkPublishesVideoPriceAndUnitTogether`, `TestUpdateOptionsBulkRollsBackVideoPriceAndUnitOnSecondWriteFailure`, `TestLoadOptionsFromDatabasePublishesVideoPriceAndUnitTogether`, and `TestExposedDataIncludesVideoResolutionPriceCopy`. The price-validation table covers JSON `0`, negative values, string values, and parser-rejected `NaN`/`Infinity`; every row asserts an error and an unchanged live map. The task-mode test accepts only `per_call` and `per_second`; an absent model key defaults at lookup time, while an explicit `per_minute` is rejected without changing DB or memory. The deep-copy test mutates both a returned inner map and the outer map, then reads again and asserts the store is unchanged. The bulk rollback test registers a temporary GORM callback that fails the second option write and proves neither persistent nor live configuration changed. The database-load test pauses the combined publisher, runs concurrent combined getters before and after release, and proves no reader can observe one new document paired with one old document.

- [ ] **Step 2: Run tests and confirm RED**

~~~powershell
go test ./common ./setting/ratio_setting ./model ./controller -run 'Test(NormalizeVideoResolution|VideoResolutionPrice|UpdateOptionRejectsInvalidVideoResolutionPrice|UpdateTaskBillingModeRejects|UpdateOptionsBulk.*VideoPrice|LoadOptionsFromDatabase.*VideoPrice|ExposedDataIncludesVideoResolutionPrice)' -count=1
~~~

Expected: compilation fails because the normalizer, store, and option wiring do not exist.

- [ ] **Step 3: Implement normalization, atomic storage, and option lifecycle**

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

Parse both option values into temporary maps, validate and normalize them completely, then publish them under a shared `videoResolutionPricingMu`; the combined getter takes the same lock and therefore cannot observe a new price table with an old unit table. Do not use RWMap.LoadFromJsonString because it clears before parsing. Extend `UpdateOptionsBulk` so it validates all values, persists them in one GORM transaction, and invokes the combined in-memory publisher only after commit. Modify `loadOptionsFromDatabase`/periodic `SyncOptions` to collect both documents from the same `AllOption` result, skip their ordinary per-key `updateOptionMap` calls, validate both temporary maps, and invoke the same combined publisher exactly once. Expose the bulk write path through `PUT /api/option/bulk` and keep the single-option endpoint backward compatible. Register initialization, validation-before-save, completion-ratio metadata model discovery, exposed output, pricing-cache invalidation, and exposed-cache invalidation.

- [ ] **Step 4: Run tests and confirm GREEN**

Run the Step 2 command. Expected: all tests pass and invalid input leaves persisted/live configuration unchanged.

- [ ] **Step 5: Commit**

~~~powershell
git add common/video_resolution.go common/video_resolution_test.go setting/ratio_setting/video_resolution_price.go setting/ratio_setting/video_resolution_price_test.go model/option.go model/option_test.go controller/option.go controller/option_test.go router/api-router.go setting/ratio_setting/exposed_cache.go
git commit -m "feat(video): add resolution price configuration" -m "Constraint: Invalid nested pricing must never replace live configuration" -m "Rejected: Reuse flat ModelPrice keys | breaks existing pricing consumers" -m "Confidence: high" -m "Scope-risk: moderate"
~~~

### Task 2: Video Billing Contract and Safe Price Math

**Files:**
- Create: relay/common/video_billing.go
- Create: relay/helper/video_price.go
- Create: relay/helper/video_price_test.go
- Modify: relay/channel/adapter.go
- Modify: relay/common/relay_info.go
- Modify: relay/common/relay_utils.go

**Interfaces:**
- Consumes: Task 1 price lookup.
- Produces: relaycommon.VideoBillingSelection.
- Produces: relaycommon.ResolvedVideoBilling containing the cloned selection, selected direct price, and resolved unit.
- Produces: relaycommon.CalculateVideoResolutionQuota, the shared pure checked formula used at pre-consume and settlement.
- Produces: optional channel.VideoBillingResolver.
- Produces: helper.BuildVideoResolutionPriceData returning PriceData, optional QuotaClamp, and error.

- [ ] **Step 1: Write failing formula tests**

~~~go
func TestBuildVideoResolutionPriceData(t *testing.T) {
	tests := []struct {
		mode      string
		seconds   int
		wantQuota int
	}{
		{mode: ratio_setting.TaskBillingModePerCall, seconds: 8, wantQuota: 75},
		{mode: ratio_setting.TaskBillingModePerSecond, seconds: 8, wantQuota: 600},
	}
	for _, tc := range tests {
		selection := relaycommon.VideoBillingSelection{
			EffectiveResolution:      "1080p",
			EffectiveDurationSeconds: tc.seconds,
			IndependentRatios:        map[string]float64{"video_input": 1.5},
		}
		priceData, clamp, err := BuildVideoResolutionPriceData(
			testContext(), testRelayInfo(), 0.1, tc.mode, selection,
		)
		require.NoError(t, err)
		assert.Nil(t, clamp)
		assert.Equal(t, tc.wantQuota, priceData.Quota)
	}
}
~~~

The fixture uses QuotaPerUnit=500 and group ratio=1. Add an explicit table alongside it:

~~~go
func TestBuildVideoResolutionPriceDataRejectsUnsafeInputs(t *testing.T) {
	tests := []struct {
		name      string
		price     float64
		mode      string
		seconds   int
		ratios    map[string]float64
		wantClamp bool
	}{
		{name: "zero duration", price: 0.1, mode: ratio_setting.TaskBillingModePerSecond, seconds: 0},
		{name: "duration above maximum", price: 0.1, mode: ratio_setting.TaskBillingModePerSecond, seconds: relaycommon.MaxTaskDurationSeconds + 1},
		{name: "unknown independent ratio", price: 0.1, mode: ratio_setting.TaskBillingModePerCall, seconds: 1, ratios: map[string]float64{"size": 2}},
		{name: "non-positive price", price: 0, mode: ratio_setting.TaskBillingModePerCall, seconds: 1},
		{name: "invalid explicit unit", price: 0.1, mode: "per_minute", seconds: 1},
		{name: "saturated product", price: math.MaxFloat64, mode: ratio_setting.TaskBillingModePerCall, seconds: 1, wantClamp: true},
	}
	for _, tc := range tests {
		selection := relaycommon.VideoBillingSelection{
			EffectiveResolution:      "1080p",
			EffectiveDurationSeconds: tc.seconds,
			IndependentRatios:        tc.ratios,
		}
		priceData, clamp, err := BuildVideoResolutionPriceData(
			testContext(), testRelayInfo(), tc.price, tc.mode, selection,
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
~~~

- [ ] **Step 2: Run the test and confirm RED**

~~~powershell
go test ./relay/helper -run 'TestBuildVideoResolutionPriceData' -count=1
~~~

Expected: compilation fails because the value object and builder are undefined.

- [ ] **Step 3: Implement the optional contract and checked formula**

~~~go
type VideoBillingSelection struct {
	EffectiveResolution      string
	EffectiveDurationSeconds int
	IndependentRatios        map[string]float64
}

type VideoBillingResolver interface {
	ResolveVideoBilling(
		c *gin.Context,
		info *relaycommon.RelayInfo,
	) (relaycommon.VideoBillingSelection, *taskdto.TaskError)
}
~~~

Do not add this method to TaskAdaptor or BaseBilling. Add `Resolution string` with JSON tag `resolution,omitempty` to `TaskSubmitReq` so multipart/provider-normalized values reach the resolver. Implement the pure formula in `relay/common/video_billing.go`: add allowlisted independent ratios first, add seconds only for per_second, calculate resolutionPrice * QuotaPerUnit * group ratio, apply PriceData ratios, and use QuotaFromFloatChecked. `helper.BuildVideoResolutionPriceData` adds request/group context and delegates to that pure function. After strict lookup, store an immutable ResolvedVideoBilling on TaskRelayInfo for later snapshot/log work; clone the independent-ratio map so adapter-owned maps cannot mutate after pre-consume.

- [ ] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: exact quotas, validation failures, and clamp propagation pass.

- [ ] **Step 5: Commit**

~~~powershell
git add relay/common/video_billing.go relay/channel/adapter.go relay/common/relay_info.go relay/common/relay_utils.go relay/helper/video_price.go relay/helper/video_price_test.go
git commit -m "feat(video): add resolution billing contract" -m "Constraint: Per-call skips duration but still applies independent ratios" -m "Rejected: Encode resolution as OtherRatio | risks double charging" -m "Confidence: high" -m "Scope-risk: moderate"
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

- [ ] **Step 1: Write failing relay and Sora tests**

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

Add concrete tests named `TestRelayTaskSubmitUsesOriginalModelForResolutionPrice`, `TestRelayTaskSubmitResolutionPricePerCallAndPerSecond`, `TestRelayTaskSubmitRejectsVideoAdaptorWithoutResolver`, `TestSoraResolveVideoBillingDefaultsTo720pAndFourSeconds`, `TestSoraResolveVideoBillingMapsHighDimensionsTo1024p`, `TestSoraResolveVideoBillingRejectsUnsupportedDimensions`, `TestSoraRemixVideoBillingRestoresSavedSelection`, `TestSoraRemixVideoBillingRecovers720pFromLegacyTaskData`, `TestSoraRemixVideoBillingRecovers1024pFromLegacyTaskData`, and `TestSoraRemixVideoBillingRejectsWhenSnapshotAndTaskDataHaveNoResolution`. Each test asserts the returned tier/duration, exact quota or 400 code, and whether pre-consume/upstream request spies were called. Define `getTaskAdaptor = GetTaskAdaptor` as a package seam in `relay_task.go`; the test restores it with `t.Cleanup`, and the fake embeds the existing task test base while recording pre-consume and `DoRequest` calls.

- [ ] **Step 2: Run tests and confirm RED**

~~~powershell
go test ./relay ./relay/common ./relay/channel/task/sora -run 'Test(RelayTaskSubmit.*VideoResolution|SoraResolveVideoBilling|SoraRemixVideoBilling|ValidateMultipartDirect)' -count=1
~~~

Expected: relay still falls back to ModelPrice and Sora still emits a size multiplier.

- [ ] **Step 3: Implement strict relay order and Sora resolver**

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

After model mapping, require VideoBillingResolver for video adapters; Suno keeps the old task price path. Normalize selection, atomically look up OriginModelName plus tier and unit, create ResolvedVideoBilling, build PriceData, attach clamp, then pre-consume. Remove Sora size pricing ratios. OpenAI remix first uses the frozen billing snapshot, then attempts the existing `task.Data.size` recovery for legacy tasks; only a task with neither a saved selection nor recoverable provider size returns unknown. The two high Sora dimensions map to `1024p`, never `1080p`.

- [ ] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: no fallback, no upstream call on missing tier, and Sora high tier is 1024p.

- [ ] **Step 5: Commit**

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
- Produces: provider-owned canonical tier/defaults with no resolution multiplier.

- [ ] **Step 1: Write failing payload-parity tests**

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

func TestDoubaoVideoInputRatioUsesSelectedTier(t *testing.T) {
	withoutVideo, ok := GetVideoInputRatio(
		"doubao-seedance-2-0-260128", "1080p", false,
	)
	require.True(t, ok)
	withVideo, ok := GetVideoInputRatio(
		"doubao-seedance-2-0-260128", "1080p", true,
	)
	require.True(t, ok)
	assert.Equal(t, 1.0, withoutVideo)
	assert.InDelta(t, 31.0/51.0, withVideo, 1e-9)
}
~~~

Add exact provider contract tests named `TestAliVideoBillingUsesMappedUpstreamModelDefaultAndOriginPriceKey`, `TestAliVideoBillingRejectsConflictingSizeAndResolution`, `TestAliVideoBillingCollapsesEquivalentSizeAndResolutionToOneField`, `TestAliVideoBillingUsesWan27TextToVideo1080pDefault`, `TestAliVideoBillingRejectsUnknownDefault`, `TestDoubaoVideoBillingUsesMappedModelCapabilitiesAndOriginPriceKey`, `TestDoubaoVideoBillingUsesPerModelDocumentedDefaults`, `TestDoubaoVideoBillingRejects1080pForLiteI2VAndSeedance20Fast`, `TestDoubaoVideoBillingRejectsUnsupportedTierAndUnknownDuration`, `TestVeo30VideoBillingAllows720pAnd1080pButRejects4k`, `TestVeo31PreviewVideoBillingAllows4k`, `TestVeoVideoBillingDefaultsTo720p`, `TestHailuoVideoBillingMatches512p720p768pAnd1080pPayloads`, `TestViduVideoBillingDefaultsViduQ1To1080p`, `TestViduVideoBillingDefaultsViduQ2To720p`, `TestProviderVideoBillingMetadataOverrideMatchesPayload`, and `TestUnsupportedVideoAdaptorRejectsKlingAndJimengBeforeRequest`. Assert both the decoded final upstream body and billing selection for every successful row; assert the mapped upstream model drives capabilities/defaults/independent ratios while the original model drives the configured price key. Unknown/conflicting rows assert HTTP 400 and a zero upstream-call count.

- [ ] **Step 2: Run tests and confirm RED**

~~~powershell
go test ./relay ./relay/channel/task/gemini ./relay/channel/task/vertex ./relay/channel/task/ali ./relay/channel/task/doubao ./relay/channel/task/hailuo ./relay/channel/task/vidu -run 'Test(VeoVideoBilling|VertexVideoBilling|AliVideoBilling|DoubaoVideo|GetVideoInputRatio|HailuoVideoBilling|ViduVideoBilling|UnsupportedVideoAdaptor)' -count=1
~~~

Expected: resolvers/defaults are absent and legacy resolution multipliers remain.

- [ ] **Step 3: Implement final-payload resolvers**

Gemini/Vertex reuse one Veo parameter resolver and remove VeoResolutionRatio. Encode an upstream-model capability table instead of the existing dimension heuristic: Veo 3.0 accepts `720p`/`1080p`; Veo 3.1 Preview additionally accepts `4k`; every current Veo model defaults to `720p`. Ali changes `convertToAliRequest` so all protocol branches and defaults use `info.UpstreamModelName`; a shared normalizer emits exactly one of Size or Resolution, collapses proven equivalent aliases, and rejects conflicting selectors before billing. Set only documented defaults such as `wan2.7-t2v=1080p`, otherwise return unknown. Doubao also uses `UpstreamModelName` for its capability/default and same-tier `video_input` lookup, while relay price lookup remains on `OriginModelName`. Its explicit table uses `1080p` for Seedance 1.0 Pro/Pro Fast, `720p` for 1.0 Lite/1.5 Pro/2.0/2.0 Fast, rejects 1080p for 1.0 Lite reference-image requests and 2.0 Fast, and admits only tiers documented for that upstream model/scenario. An omitted duration without a trustworthy default is rejected instead of guessed. Hailuo reuses convertToRequestPayload and explicit ModelConfig supported resolutions; invalid input cannot silently choose another tier. Vidu reuses final payload and model defaults (viduq1=1080p, viduq2=720p). When a polling response explicitly reports actual duration, its parser writes the bounded value to `TaskInfo.EffectiveDurationSeconds`; absent duration remains zero so settlement retains the submitted snapshot value. Kling/Jimeng remain unsupported until a trustworthy fixed tier exists. Suno remains non-video.

- [ ] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: billing equals payload, no resolution ratio remains, Doubao is not double-counted, and unknown adapters fail before pre-consume.

- [ ] **Step 5: Commit**

~~~powershell
git add relay/channel/task/gemini relay/channel/task/vertex relay/channel/task/ali relay/channel/task/doubao relay/channel/task/hailuo relay/channel/task/vidu relay/relay_task_test.go
git commit -m "feat(video): resolve provider pricing tiers" -m "Constraint: Only final-payload or documented defaults are billable" -m "Rejected: Generic dimension inference | provider tiers are protocol-specific" -m "Confidence: medium" -m "Scope-risk: broad" -m "Not-tested: Providers without a trustworthy resolution remain unsupported"
~~~

### Task 5: Snapshots, Logs, Pricing API, and Model Lifecycle

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
- Modify: controller/model_meta_test.go
- Modify: dto/task.go
- Modify: controller/task.go
- Modify: controller/task_test.go
- Create: controller/ratio_sync_test.go

**Interfaces:**
- Consumes: selected resolution/price/unit from the relay.
- Produces: frozen video-resolution snapshot and dedicated settlement branch.
- Produces: optional public resolution_prices plus legacy non-zero minimum model_price.
- Produces: atomic model metadata/pricing mutation.

- [ ] **Step 1: Write failing persistence and API tests**

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

func TestPerSecondDirectPriceIsNotSnapshottedAsPerCall(t *testing.T) {
	context := newResolutionTaskBillingContext(ratio_setting.TaskBillingModePerSecond)
	assert.Equal(t, "video_resolution", context.PricingKind)
	assert.Equal(t, ratio_setting.TaskBillingModePerSecond, context.BillingUnit)
	assert.False(t, context.PerCallBilling)
}
~~~

Add named tests that assert the exact boundaries: `TestTaskBillingContextRoundTripsFrozenResolutionSelection`, `TestResolutionPricingAdminLogIncludesSelectionAndUnit`, `TestResolutionPricingUserLogOmitsAdminPricingFields`, `TestResolutionPricedTaskPollingSettlesPerSecondDifference`, `TestResolutionSettlementIgnoresTotalTokensAndResidualModelRatio`, `TestResolutionSettlementUsesSnapshotAfterLiveConfigurationChanges`, `TestResolutionSettlementOnlyAcceptsBoundedActualDuration`, `TestResolutionSettlementAuditsQuotaSaturation`, `TestHasModelBillingConfigIncludesResolutionOnlyModel`, `TestPricingLegacySummaryUsesMinimumResolutionPrice`, `TestModelMetaCreateCommitsModelPriceAndUnitAtomically`, `TestModelMetaRenameMovesResolutionPricesAndTaskUnitAtomically`, `TestModelMetaDeleteRemovesResolutionPricesAndTaskUnitAtomically`, `TestModelMetaMutationRollsBackDatabaseAndMemoryWhenOptionWriteFails`, and `TestRatioSyncLeavesNestedResolutionPricesUnchanged`.

The token-fallback test seeds a stale positive ModelRatio and `TaskInfo.TotalTokens`, then proves settlement uses `SelectedResolutionPrice × frozen GroupRatio × frozen independent ratios × authoritative duration`. The configuration-change test mutates live price/unit after submission and asserts the original snapshot still settles exactly. The bounded-duration table covers zero/absent (retain submitted duration), a valid different duration (settle difference), negative/over-limit (warn and retain pre-consume), and a saturating product (MaxInt32 plus admin audit marker). The lifecycle rollback test injects a failure on the second option write inside the GORM transaction and proves the model row, both option rows, and both live maps are unchanged. The ratio-sync test seeds a local nested value, imports a type-2 pricing response containing a conflicting `resolution_prices` object, and asserts the local nested option is byte-for-byte unchanged.

- [ ] **Step 2: Run tests and confirm RED**

~~~powershell
go test ./model ./service ./controller ./relay/helper -run 'Test(TaskBillingContext|ResolutionPriced|ResolutionSettlement|Pricing.*Resolution|HasModelBillingConfig.*Resolution|ModelMeta.*(ResolutionPrice|ModelPrice)|RatioSync.*ResolutionPrice)' -count=1
~~~

Expected: missing fields/classification and the existing PriceData.UsePrice shortcut incorrectly marks per-second fixed prices as per-call.

- [ ] **Step 3: Implement JSON snapshot and public contract**

Add `PricingKind`, EffectiveResolution, SelectedResolutionPrice, BillingUnit, EffectiveDurationSeconds, and IndependentRatios optional fields to TaskBillingContext. For new video-resolution tasks set `PricingKind="video_resolution"`, freeze every input at submission, and set PerCallBilling only when BillingUnit is per_call; retain PerCallBilling only as a legacy read fallback. Add `EffectiveDurationSeconds` to TaskInfo so a polling adaptor can report a provider-authoritative actual duration.

Implement `CalculateVideoResolutionSnapshotQuota` in `service/task_billing.go`. It accepts only the frozen snapshot plus an effective duration, validates the snapshot and duration, then delegates to `relaycommon.CalculateVideoResolutionQuota`; pre-consume and settlement therefore cannot drift. In `settleTaskBillingOnComplete`, check `PricingKind` before both `AdjustBillingOnComplete` and `TotalTokens`: per_call returns unchanged; per_second uses a valid positive TaskInfo duration when supplied, otherwise the submitted duration, and then calls RecalculateTaskQuota with the returned clamp. Invalid upstream duration cannot change quota. Resolution, selected price, group ratio, or ratios returned after submission are ignored.

Add an optional `TaskBillingDetails` DTO containing the four public audit fields; populate it only in `tasksToDto(..., fillUser=true)` so administrator task details can audit the selection and ordinary user task responses cannot see the private snapshot. `LogTaskConsumption` reads unit/resolution/selected price from `ResolvedVideoBilling` rather than calling the live `IsTaskPerCallBilling` lookup, and records independent ratios separately from duration so the log matches the frozen snapshot even if configuration changes concurrently.

Add ResolutionPrices map[string]float64 to Pricing, classify resolution-priced models as fixed price, default their public unit to per_second, set ModelPrice to the minimum valid tier for old summary consumers, include them in HasModelBillingConfig, and update pricing version. Relay billing continues to use only the strict nested lookup, never this minimum compatibility field.

For model create/update, bind a backward-compatible flattened request with optional `pricing_options: Record<string,string>`. Allow only the existing model-pricing option keys, validate every supplied document, then save the model row and every supplied option row in one GORM transaction. The drawer sends its already-computed complete ModelPrice/ModelRatio/ratio/TaskBillingMode/VideoResolutionPrice documents through this field instead of issuing follow-up requests. For rename and delete, load the old name and at minimum the VideoResolutionPrice and TaskBillingMode maps inside the same transaction, apply the move/removal even for an old client that omitted `pricing_options`, save affected option rows, and mutate the model row; publish all prevalidated maps and refresh caches only after commit. A nil patch preserves pricing on an ordinary update, but a rename still moves the new pair. Keep ratio synchronization flat-only: an upstream `resolution_prices` field is ignored and must not create, overwrite, or clear local VideoResolutionPrice configuration.

- [ ] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: per-second polling uses only frozen resolution inputs, token/adaptor quota fallbacks are bypassed, public legacy and new pricing fields are non-zero, user/admin visibility is separated, and failed lifecycle mutations leave no partial state.

- [ ] **Step 5: Commit**

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
- Create: web/src/features/system-settings/hooks/use-update-options-bulk.ts

**Interfaces:**
- Consumes: backend VideoResolutionPrice option.
- Produces: VideoResolutionPriceMap, row validation, and PricingMode video_resolution.

- [ ] **Step 1: Write failing pure-state tests**

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
~~~

- [ ] **Step 2: Run tests and confirm RED**

~~~powershell
Push-Location web
bun test src/features/system-settings/models/__tests__/video-resolution-pricing.test.ts
Pop-Location
~~~

Expected: missing validation module and snapshot field.

- [ ] **Step 3: Implement types, validation, and option plumbing**

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
~~~

Validate canonical regex, duplicates, and finite positive values; serialize sorted maps. Add VideoResolutionPrice to every settings/default/form/reset/diff path. Snapshot precedence is tiered_expr, video_resolution, per-request, per-token. Include sorted maps in signatures and configured-base checks. Add `useUpdateOptionsBulk()` for the atomic backend endpoint and make both visual and raw JSON settings saves send VideoResolutionPrice and TaskBillingMode together whenever either changes; do not issue two sequential single-option mutations for this pair.

- [ ] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: normalization, invalid values, duplicates, stable serialization, and mode inference pass.

- [ ] **Step 5: Commit**

~~~powershell
git add web/src/features/system-settings/models/video-resolution-pricing.ts web/src/features/system-settings/models/__tests__/video-resolution-pricing.test.ts web/src/features/system-settings/types.ts web/src/features/system-settings/billing/index.tsx web/src/features/system-settings/billing/section-registry.tsx web/src/features/system-settings/models/index.tsx web/src/features/system-settings/models/ratio-settings-card.tsx web/src/features/system-settings/models/model-ratio-form.tsx web/src/features/system-settings/models/model-pricing-core.ts web/src/features/system-settings/models/model-pricing-snapshots.ts web/src/features/system-settings/hooks/use-update-options-bulk.ts
git commit -m "feat(web): model video resolution pricing state" -m "Constraint: Nested state survives visual and JSON editor round trips" -m "Rejected: Add a second billing_mode option | mode derives from the nested map" -m "Confidence: high" -m "Scope-risk: moderate"
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
- Produces: accessible add/edit/remove and delete/rename/copy persistence.

- [ ] **Step 1: Write failing user-visible tests**

~~~tsx
test('blocks save for duplicate canonical resolutions', async () => {
	const view = renderResolutionEditor()
	await view.addRow({ resolution: '720P', price: '0.10' })
	await view.addRow({ resolution: '720p', price: '0.20' })
	assert.equal(view.commit(), null)
	assert.match(view.getErrorForRow(2), /only be configured once/i)
	assert.equal(view.getResolutionInput(2).getAttribute('aria-invalid'), 'true')
})

test('rename removes the old nested key and preserves billing unit', async () => {
	const request = buildModelMutationRequest({
		oldName: 'video-old',
		newName: 'video-new',
		videoResolutionPrice: { 'video-old': { '720p': 0.1 } },
		taskBillingMode: { 'video-old': 'per_call' },
	})
	assert.deepEqual(JSON.parse(request.pricing_options.VideoResolutionPrice), {
		'video-new': { '720p': 0.1 },
	})
	assert.deepEqual(JSON.parse(request.pricing_options.TaskBillingMode), {
		'video-new': 'per_call',
	})
	assert.equal(capturedRequests.length, 1)
})
~~~

Use node:test, node:assert, happy-dom, and React act like the existing tool-price validation test.

- [ ] **Step 2: Run tests and confirm RED**

~~~powershell
Push-Location web
bun test src/features/system-settings/models/__tests__/video-resolution-price-editor.test.tsx src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx
Pop-Location
~~~

Expected: editor and new mode controls are absent.

- [ ] **Step 3: Implement Base UI composition and both admin paths**

~~~ts
export type VideoResolutionPriceEditorProps = {
  rows: VideoResolutionPriceRow[]
  errorsByRowId: Record<number, VideoResolutionPriceRowErrors>
  disabled?: boolean
  onChange: (rows: VideoResolutionPriceRow[]) => void
}
~~~

Use FieldGroup/Field, labels, Input, FieldError, and translated icon-button aria-labels. Use functional updates for current-row changes. Add a fourth video_resolution tab and explicit per_second/per_call selector. Include VideoResolutionPrice in memo equality and batch-copy paths; system-settings copy uses the bulk option endpoint. In the model drawer, include the computed `pricing_options` documents in the single create/update request and let the backend transaction perform create/rename plus every option update. Model delete sends no extra option requests because the backend transaction removes the new pair. Clear mutually exclusive flat price/ratio/expression values through those documents in the same atomic mutation when saving this mode.

- [ ] **Step 4: Run tests and confirm GREEN**

Run Step 2. Expected: add/edit/remove, invalid state, unit preview, rename, and copy pass.

- [ ] **Step 5: Commit**

~~~powershell
git add web/src/features/system-settings/models/video-resolution-price-editor.tsx web/src/features/system-settings/models/__tests__/video-resolution-price-editor.test.tsx web/src/features/system-settings/models/model-pricing-sheet.tsx web/src/features/system-settings/models/model-ratio-visual-editor.tsx web/src/features/system-settings/models/model-ratio-table-columns.tsx web/src/features/models/components/drawers/model-mutate-drawer.tsx web/src/features/models/components/drawers/__tests__/video-resolution-pricing.test.tsx
git commit -m "feat(web): edit video prices by resolution" -m "Constraint: Every model uses one shared per-task or per-second unit" -m "Rejected: Store one unit per row | conflicts with TaskBillingMode" -m "Confidence: high" -m "Scope-risk: broad"
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
- Consumes: resolution_prices and task_billing_mode.
- Produces: minimum summaries, full tables, and translated copy.

- [ ] **Step 1: Write failing pricing tests**

~~~ts
test('uses minimum configured tier for summaries', () => {
  const model = pricingModel({
    resolution_prices: { '720p': 0.1, '1080p': 0.18 },
  })
  assert.equal(getMinimumResolutionPrice(model), 0.1)
  assert.equal(getEffectiveTaskBillingMode(model), 'per_second')
})
~~~

Add tests named `renders From with minimum per-task resolution price`, `renders From with minimum per-second resolution price`, `details list every resolution sorted numerically`, `resolution prices honor group multiplier and recharge conversion`, and `invalid resolution price values are omitted instead of rendered as zero`. Use a `720p: 0.1, 1080p: 0.18` fixture, assert the exact localized unit text and tier ordering, then inject `0`, negative, `NaN`, and string values through a casted API fixture and assert none is shown as a valid price.

- [ ] **Step 2: Run tests and confirm RED**

~~~powershell
Push-Location web
bun test src/features/pricing/lib/__tests__/resolution-price.test.ts src/features/pricing/components/__tests__/video-resolution-pricing.test.tsx
Pop-Location
~~~

Expected: helper exports and table rendering are absent; existing formatting reads missing model_price as zero.

- [ ] **Step 3: Implement pricing helpers, UI, and scripted translations**

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
~~~

Use minimum price in cards/tables/filters/group summaries and full entries in details. Default resolution models to per_second. Add translations for Video resolution, Resolution prices, Resolution, USD price, Add resolution, Remove resolution, No resolution prices configured, Resolution is required, Use a canonical resolution such as 720p or 4k, Each resolution can only be configured once, Price is required, Price must be a finite number greater than zero, From, Prices shown per task, and Prices shown per second. Populate all seven locale maps in add-missing-keys.mjs, execute it once, delete it, then run i18n sync.

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
  'USD price': {
    en: 'USD price', zh: '美元价格', 'zh-TW': '美元價格', fr: 'Prix en USD', ja: 'USD価格', ru: 'Цена в USD', vi: 'Giá USD',
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
  'Prices shown per task': {
    en: 'Prices shown per task', zh: '显示的是每次价格', 'zh-TW': '顯示每次任務的價格', fr: 'Prix affichés par tâche', ja: 'タスクごとの価格を表示', ru: 'Цены указаны за задачу', vi: 'Giá hiển thị theo mỗi tác vụ',
  },
  'Prices shown per second': {
    en: 'Prices shown per second', zh: '显示的是每秒价格', 'zh-TW': '顯示每秒價格', fr: 'Prix affichés par seconde', ja: '1秒ごとの価格を表示', ru: 'Цены указаны за секунду', vi: 'Giá hiển thị theo mỗi giây',
  },
}
~~~

- [ ] **Step 4: Run complete verification**

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

Expected: all commands exit zero. Confirm by tests that different request resolutions produce different charges, both billing units are exact, defaults match payloads, missing tiers fail before pre-consume/upstream calls, mapped models use original names for price, resolution is not an OtherRatio, and snapshots/admin/public UI agree.

- [ ] **Step 5: Dispatch independent reviews and commit verified fixes**

Review fixed range f8fa8445..HEAD against the design and this plan with separate correctness, billing-safety, frontend, and maintainability reviewers. Resolve all critical/high findings and rerun affected commands.

~~~powershell
git status --short
git diff --check
git add --all
git commit -m "feat(video): complete resolution-based pricing" -m "Constraint: Final behavior preserves strict no-fallback billing" -m "Confidence: high" -m "Scope-risk: broad"
~~~

Skip this commit only when the worktree is clean after review.
