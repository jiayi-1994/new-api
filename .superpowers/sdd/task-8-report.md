# Task 8 report — Cross-Lifecycle Regression Suite

Branch: `codex/video-pricing-task7`. Commits: `65adbbea` (suite) + `9d0ef883` (review fixes).
Tests only — no production code was touched; no test exposed a production defect.

## Coverage against the plan

- **Step 1 (config change between retries):** `TestRelayTaskSubmitRetryKeepsLegacyPlanWhenResolutionTableAppears`
  mirrors the existing resolution-retry freeze test in the opposite direction: a resolution
  table appearing between attempts cannot convert a frozen legacy plan; the same billing
  session/funding is reused, no second pre-consume, `ResolvedVideoBilling` stays nil.
- **Step 2 (running-task settlement/refund):** `mutateEveryLivePricingOption` mutates
  ModelPrice, ModelRatio, TaskBillingMode, VideoResolutionPrice, GroupRatio, and
  `common.QuotaPerUnit` after task persistence. Three tests prove persisted legacy and
  resolution tasks settle (`TestLegacySettlementUsesStoredContextAfterEveryLivePricingChange`,
  `TestResolutionSettlementUsesSnapshotAfterEveryLivePricingChange`) and refund
  (`TestPersistedTasksRefundStoredQuotaAfterEveryLivePricingChange`) purely from their stored
  `TaskBillingContext`; live `per_call` cannot override stored `PerCallBilling=false`; the
  stored reservation identifier and context survive unchanged. No task schema added.
- **Step 3 (wildcard + channel capability):**
  `TestPrepareTaskBillingPlanCompactWildcardActivatesConcreteModelsIndependently` (one
  `*-openai-compact` table activates two concrete non-Suno models with independent frozen
  plans; plain names stay legacy; a Suno-platform request matching the wildcard stays legacy;
  the stored document is not mutated).
  `TestChannelCapabilityToggleChangesRoutingWithoutMutatingPricingState` (controller) flips a
  live channel from Kling to Sora and observes routing availability change through the real
  `getChannel` + `CompatibleTaskChannelTypes` path while the stored table, the frozen plan
  tier, and the public pricing response all stay identical.
  `TestTaskChannelCapabilityGatesRoutingWithoutMutatingFrozenPricingState` (relay) pins the
  legacy-kind nil compatible set and capability-query immutability.
- **Step 4 (concurrent writers):** `blockFirstPricingLockHolder` blocks the first
  serializable-transaction writer while it holds the options row locks.
  `TestStaleCASWriterConflictsAfterConcurrentLifecycleCommit`: a stale CAS raw writer racing a
  committed lifecycle save receives `OptionConflictError` (with the lifecycle's current
  document) and cannot overwrite it or drop sibling entries.
  `TestLifecycleCommandRebasesOnDocumentsCommittedByCASWriter`: a rename running second moves
  the CAS-committed price/mode/resolution entries, proving the lifecycle rebases on locked
  current documents rather than a pre-CAS snapshot.
- **Extra cross-boundary pin:** `TestLegacyBillingContextIgnoresLiveResolutionTableAddedAfterFreeze`
  (controller) — a legacy-frozen request persists a legacy context (empty `PricingKind`, no
  reservation id) even when a live resolution table matches at persistence time.
- **Frontend:** no missing cross-boundary contract found; Tasks 6/7 frontend tests unchanged.

## Verification

- Step 5 backend suite: all nine packages pass except two **pre-existing** Windows flakes,
  both reproduced at the base commit with these changes stashed:
  `service/channel_affinity_usage_cache_test.go` (order-dependent `UnixNano` cache-key
  collision, already documented) and — newly observed, also baseline —
  `TestResolutionPollingSerializesSeparateLogDatabasePublicationAcrossSQLiteSessions`
  (intermittent in full-package runs, passes alone; failed at baseline run 3 of 3).
- Step 5 frontend suite: 48/48 tests pass across the six listed files. gofmt clean.

## Independent review

Verdict: with fixes → fixed in `9d0ef883`:
- Important: the Step 3 "toggle channel availability" clause was only queried statically —
  added the controller routing-level toggle test above.
- Minors: dead `"stale"` assertion replaced with sibling-preservation assertions; duplicated
  fail-first `DoRequest` bodies extracted to `failFirstTaskSubmitRequest`; structurally
  guaranteed `NotSame` dropped; duplicated Kling capability assertions trimmed.
Reviewer confirmed the concurrency tests are non-vacuous (removing the CAS raw comparison or
the rebase would fail them) and deterministic (`-count=5` stable, mutex-ordered).
