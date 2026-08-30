# Task 4 Implementation Report

## Status

DONE — transactional pricing document store, whole-document CAS writers, model-row mutations, and publish-after-commit recovery are implemented and independently reviewed.

## Implementation

- Added `model/model_pricing_command.go` with the exact twelve protected pricing keys and typed `PricingDocuments` loading.
- Every protected writer uses one process mutex and one GORM transaction. Missing rows are materialized from the current in-memory values with `clause.OnConflict{DoNothing:true}`; rows are then read in the fixed key order through the shared `lockForUpdate` helper.
- Added `ExecuteModelPricingCommand` for save, rename, copy, delete, and whole-document replacement semantics:
  - Resolution save replaces only the target resolution table and retains all eleven legacy entries.
  - Fixed, ratio, and expression saves remove the target resolution table and apply the existing mutually exclusive legacy-map rules.
  - Rename moves only source-owned entries; an absent source entry leaves an existing target entry unchanged.
  - Copy replaces/deletes the target across all twelve documents while retaining the source.
  - Delete removes the target from all twelve documents.
- Added create/update/delete `ModelRowMutation` support inside the same transaction. Update/delete lock the model row before the stable option-row sequence, matching retained model lifecycle lock order during rolling deployments. Command kind/source/target names are validated against the locked current row and requested new model name before document writes.
- Added `UpdateOptionCAS` and `UpdateOptionsBulkCAS`. Comparisons use the exact original database strings, including whitespace; changed rows retain a database-side `key + expected value` predicate.
- Routed protected `UpdateOption` and `UpdateOptionsBulk` calls through the same transaction and publication boundary while preserving mixed protected/unprotected bulk database atomicity.
- Added billing document validation in `setting/billing_setting/tiered_billing.go`: object/duplicate-key/type checks, supported modes, expression smoke tests, and the invariant that every `tiered_expr` mode has a nonblank validated expression.
- Refactored `loadOptionsFromDatabase` so periodic `SyncOptions` reloads the complete protected snapshot through the same validated low-level publisher and converges `PublicationPending` state.

## Publish-After-Commit Semantics

- No protected in-memory setting is changed until the transaction commits.
- Publication uses a transition-safe staged plan: current and final base price/ratio maps are temporarily unioned; old and new expressions are staged before billing-mode activation changes; final cleanup occurs only after the replacement billing path is live. Readers therefore do not observe an unpriced fixed/ratio model or `tiered_expr` without an expression.
- All legacy documents publish before `VideoResolutionPrice`, which is always last.
- An injected first publication failure triggers a database reload and the validated non-failpoint low-level publisher.
- Recovery success returns `Committed=true, PublicationRecovered=true` and no mutation error.
- Reload or replay failure returns `Committed=true, PublicationPending=true` and no unsafe ordinary retry error; pricing/exposed caches remain invalidated until periodic reload convergence.

## Database Compatibility

- Uses GORM transactions, `clause.OnConflict{DoNothing:true}`, struct predicates, and the project `lockForUpdate` helper only.
- No dialect-specific SQL was added. `lockForUpdate` emits `FOR UPDATE` on MySQL/PostgreSQL and skips it on SQLite.
- Model update/delete lock order is model row first, followed by all option rows in one stable order, eliminating the cross-instance inversion with retained lifecycle writers.
- Missing-row creation and all subsequent document/model writes roll back together on validation, CAS, or model-mutation failures.

## RED Evidence

- Initial focused command failed to compile with missing `modelPricingOptionKeys`, command/result types, `ExecuteModelPricingCommand`, CAS APIs, and publication helpers.
- Paired validator regression: `TestUpdateOptionCASRejectsTieredModeWithoutExpression` failed because CAS committed a `tiered_expr` model with no expression.
- Publication-state regression: `TestPricingPublicationPlanKeepsEveryTransitionBillable` initially failed to compile because no transition-safe publication plan existed.
- Lock-order regression: running the query-order test against the previous options-first ordering failed with `modelQuery=12`, `optionQuery=0`.
- Mutation-binding regressions: delete-A/mutate-B and rename-A→B/mutate-A→C tests both committed successfully before coupling validation was added.

## GREEN / Verification Evidence

- `go test ./model -run 'Test(ExecuteModelPricingCommand|UpdateOptionCAS|UpdateOptionsBulkCAS|PricingPublication)' -count=1` — PASS.
- `go test ./model ./setting/billing_setting -count=1` — PASS (`billing_setting` builds; no package-local tests).
- `go test ./controller -count=1` — PASS (compatibility verification only; no controller files changed).
- `go vet ./model ./setting/billing_setting` — PASS.
- `git diff --check` — PASS.
- Independent data-integrity re-review: APPROVED, no remaining P1/P2 findings.

## Files

- Added `model/model_pricing_command.go`
- Added `model/model_pricing_command_test.go`
- Modified `model/option.go`
- Modified `model/option_test.go`
- Modified `setting/billing_setting/tiered_billing.go`
- Replaced this report: `.superpowers/sdd/task-4-report.md`

## Self-Review and Attention Points

- JSON marshal/unmarshal calls in new business code use `common.*`; `encoding/json` is used only for `json.RawMessage` types.
- The staged publication order is a billing-availability invariant, not cosmetic ordering. Future changes must retain staging before activation switches and must keep `VideoResolutionPrice` last.
- Ordinary errors from command/CAS methods indicate pre-commit failure. A committed publication problem is represented only by `PublicationRecovered` or `PublicationPending`.
- Task 5 controllers should consume the new command API directly; this task intentionally did not modify controller or frontend code.
