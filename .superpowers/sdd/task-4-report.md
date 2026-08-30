# Task 4 Implementation Report

## Status

DONE — transactional pricing document store, whole-document CAS writers, model-row mutations, and publish-after-commit recovery are implemented. The external follow-up findings on active model-name uniqueness, billing publication synchronization, and create lock order are resolved and independently re-reviewed.

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

## External Review Remediation (2026-08-30)

### Active Model-Name Namespace

- Added `ModelNameConflictError` and one indexed active-name locking read that excludes the current row for rename/update. The guard is inside the same transaction as the model mutation and all twelve pricing documents.
- Model-name transactions now request `database/sql` `LevelSerializable`. Existing targets are locked through the project `lockForUpdate` helper; absent targets rely on the supported database's serializable predicate/range/write-serialization behavior so two instances cannot both commit the same active name.
- Routed all production model-name writers through the same primitive: `Model.Insert`, legacy `Model.Update`, and `ExecuteModelPricingCommand` create/update mutations. The existing controller duplicate check remains only a friendly preflight; correctness is enforced in the transaction.
- A two-connection SQLite WAL regression test pauses both transactions after they read the same absent target, releases both inserts together, and proves exactly one transaction commits and one active row remains. The composite `(model_name, deleted_at)` index is not treated as the correctness boundary because active rows contain `NULL`.
- Cross-database reasoning follows the engines' documented transaction guarantees: PostgreSQL Serializable detects predicate-read/write serialization cycles and aborts one transaction; MySQL InnoDB locking reads protect the scanned index range/gap; SQLite serializes writes. A loser returns a pre-commit database error and is safe to retry, at which point the committed active target is observed as a typed conflict.

### Model-First Lock Order

- Create mutations now validate coupling, lock the active-name namespace, and create the model row before materializing or locking any pricing option. The create remains inside the pricing transaction, so later document validation or CAS failure rolls it back.
- Update/delete commands retain model-row-first ordering. Legacy `Model.Update` now uses the same serializable target-name guard before its resolution-option lock. This removes the new-command options-to-model edge while retaining model-to-options ordering during rolling deployment.
- The order regression initially observed `option-lock` at index `0` and `model-create` at index `12`; GREEN observes the create before the first option query. A separate validation-failure test proves the early model create and all documents roll back together.

### Atomic Billing Mode/Expression Publication

- Added one `RWMutex` for the billing mode and expression maps, an atomic `UpdatePricingDocuments` pair setter, `PricingDocumentsJSON`, and `GetBillingModeAndExpr` for one-snapshot request reads.
- Publication stages the expression union, activation mode, and final cleanup as atomic mode/expression pairs. Both related `common.OptionMap` entries are updated under one option-map lock. `VideoResolutionPrice` remains the final publication step.
- `config.GlobalConfig` now honors synchronized config-specific import/export interfaces. Database config loads, individual setters, periodic `SyncOptions`, pricing sync output, and the low-level recovery publisher therefore share the same billing snapshot boundary.
- Request hot paths in `relay/helper/price.go` read mode and expression once and pass that expression through tiered pre-consume; pricing metadata construction in `model/pricing.go` uses the same combined getter.

### Follow-up RED Evidence

- `go test ./model -run 'TestExecuteModelPricingCommand(RejectsActiveTargetNameConflict|CreatesModelBeforeLockingPricingDocuments)$' -count=1` — RED: rename, create-save, and create-copy returned nil errors against an existing active target; lock order reported `12 is not less than 0`.
- `go test ./model -run 'TestInsertModelWithActiveNameGuardSerializesAbsentTargetAcrossConnections$' -count=1` — RED compile failure: `undefined: insertModelWithActiveNameGuard`.
- `go test ./setting/billing_setting -run 'Test(PricingDocumentsConcurrentPublish|UpdatePricingDocuments)' -count=1` — RED compile failure for the missing paired snapshot getter/setter and JSON snapshot API.

### Follow-up GREEN / Verification Evidence

- `go test ./model -run 'Test(ExecuteModelPricingCommand|UpdateOptionCAS|UpdateOptionsBulkCAS|PricingPublication)' -count=1` — PASS.
- `go test ./model ./setting/billing_setting ./relay/helper -count=1` — PASS.
- `go vet ./model ./setting/billing_setting ./setting/config ./relay/helper` — PASS.
- `git diff --check` — PASS.
- Independent adversarial re-review — APPROVED, with no Critical/Important findings or residual risks reported.

### Follow-up Files

- Modified `model/model_meta.go` and `model/model_meta_test.go` for the shared serializable active-name namespace and legacy lifecycle regression.
- Modified `model/model_pricing_command.go` and `model/model_pricing_command_test.go` for conflict rollback, cross-connection creation, model-first create ordering, and grouped billing publication.
- Modified `model/option.go`, `model/pricing.go`, and `relay/helper/price.go` to route publication/config/hot-path reads through the synchronized pair.
- Modified `setting/billing_setting/tiered_billing.go`; added `setting/billing_setting/tiered_billing_test.go`; modified `setting/config/config.go` for synchronized config import/export dispatch.

### Follow-up Attention Points

- No live MySQL/PostgreSQL services were available for integration tests; compatibility is based on standard serializable transactions, indexed GORM locking reads, and the existing cross-dialect `lockForUpdate` helper. No migration or dialect-specific SQL was added.
- `go test -race` cannot start test binaries in this Windows Go 1.25.1 environment and exits with loader status `0xc0000139`; deterministic channel-coordinated concurrency tests pass under normal execution. A full `go vet ./...` produced no output for 90 seconds and was stopped; the four changed package scopes pass vet.
- Future request code that needs both billing mode and expression must use `GetBillingModeAndExpr`; separate compatibility getters are synchronized individually but intentionally do not form a cross-call snapshot.
