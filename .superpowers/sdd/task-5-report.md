# Task 5 Report: Atomic Model/Option HTTP Contracts

## Status

Implemented and verified the HTTP/controller/model lifecycle wiring on top of Task 4's `ExecuteModelPricingCommand`, model-name guard, protected-document locking, CAS, and publication recovery primitives. No Task 4 primitive was bypassed or rewritten.

## API contracts

### Model create/update/delete

- Existing `POST /api/models/` and `PUT /api/models/` payloads remain compatible through `ModelMutationRequest`, which embeds the existing `model.Model` and adds optional `pricing`.
- Legacy create without `pricing` still uses the guarded model insert path.
- Same-name metadata updates without `pricing` use `UpdateMetadata(expectedName)`, which revalidates the authorized name inside the serializable namespace transaction and never touches pricing documents.
- `status_only=true` without `pricing` still updates only status and does not touch pricing. A root request that combines status-only and pricing is rejected as ambiguous; a non-root request is denied before that validation.
- Pricing-bearing create/save uses `PricingCommandSave` with the model row mutation in the same transaction.
- Every rename uses `PricingCommandRename`, including omitted pricing. Nil pricing moves the locked source entries unchanged across all 12 protected documents; non-nil pricing applies the explicit selection after the move.
- Every model delete uses `PricingCommandDelete` with the model row deletion in the same transaction. `Model.Delete` and `DeleteModelMetaByID` both route through the same full-document command lifecycle.

### Option CAS and semantic pricing endpoint

- `OptionUpdateRequest` now accepts optional `expected_value`.
- `PUT /api/option/` requires an exact raw `expected_value` for each protected pricing key. Non-protected keys keep their legacy behavior.
- A stale protected update returns HTTP 409 with `success:false`, the required reload/retry message, and `{key,current_value}` containing the exact current DB raw string.
- `PUT /api/option/pricing` accepts semantic `save`, `copy`, and `delete` commands.
- The endpoint also accepts Task 6's one-shot bulk CAS command, `replace_documents`, with `values` and `expected_documents`. Every changed key must have an exact expected raw string; Task 4 performs the recheck while all pricing rows are locked.
- Bulk-CAS conflicts use the same HTTP 409 contract and never partially write another changed document.
- Successful semantic/bulk commands return all 12 committed raw documents in `data`, plus `committed`, `publication_recovered`, and `publication_pending`.
- A recovered publication returns HTTP success with a recovery message. A pending post-commit publication returns HTTP success and explicitly says `do not retry`, so clients do not repeat an already committed mutation.

## Authorization

- `PUT /api/option/pricing` is registered under the existing `RootAuth` option group.
- Pricing-bearing model create/update requests are dynamically restricted to root after decoding.
- Rename is dynamically restricted to root after loading the current model name; the authorized source name is revalidated under the namespace transaction lock, closing the rename race.
- Model delete is registered in a separate `RootAuth` models group rather than stacking `RootAuth` on the inherited `AdminAuth` group.
- Same-name/no-pricing metadata and status updates remain available to ordinary admins.
- Dynamic root denials remain visible to the inherited AdminAuth failed-write audit trail.
- Real Gin route tests use admin PAT authentication and `ServeHTTP`; they do not inspect source strings. They prove admin 403 for semantic pricing, rename, and delete, and prove legacy admin create/same-name update remains allowed.

## Transactions, rollback, and publication

- Model row and 12-document mutations share Task 4's serializable command transaction and preserve model-before-options lock order.
- Duplicate rename leaves the source model and every document unchanged.
- Injected model-update failure rolls back all document changes.
- Invalid semantic save and stale bulk CAS leave all 12 documents unchanged.
- Missing pricing rows are materialized through Task 4's validated live defaults.
- Controller code does not publish settings before commit. It forwards Task 4's committed/recovered/pending result and avoids rebuilding stale pricing state when publication remains pending.

## TDD evidence

### RED

- Controller build failed on missing `UpdatePricingOption` and `writePricingCommandSuccess`.
- `/api/option/pricing` returned 404 through the real router.
- Admin model rename/delete returned 200 instead of 403.
- Protected `PUT /api/option/` accepted missing/stale CAS inputs instead of the required 400/409 behavior.
- Legacy model rename/delete changed only `VideoResolutionPrice`; new lifecycle tests showed the other protected documents were untouched.
- Bulk `replace_documents` was rejected with HTTP 400; conflict/success contract tests failed until values and exact expected documents were wired.

### GREEN

- Focused controller/router contract tests pass.
- Focused model lifecycle tests pass.
- Full `model`, `controller`, and `router` package tests pass.
- Scoped `go vet` and `git diff --check` pass.

## Files

- `controller/model_meta.go`
- `controller/model_meta_test.go`
- `controller/option.go`
- `controller/option_test.go`
- `model/model_meta.go`
- `model/model_meta_test.go`
- `router/api-router.go`
- `router/api-router_test.go` (explicitly authorized route/auth contract test)

## Verification evidence

- `go test ./model ./controller ./router -count=1` — PASS
- `go test ./controller ./router -run 'Test(ModelMetaPricing|UpdateOptionCAS|PricingCommandRoute)' -count=1` — PASS
- Focused `TestModelMeta...` lifecycle command — PASS
- `go vet ./model ./controller ./router` — PASS
- `git diff --check` — PASS

## Self-review and independent review

- Confirmed no direct protected option writer was introduced; controllers delegate to Task 4 command/CAS APIs.
- Confirmed no direct JSON marshal/unmarshal was added in business code.
- Confirmed no database-specific SQL or locking syntax was added.
- Confirmed same-name admin authorization cannot race into a rename because `UpdateMetadata` binds the expected source name inside the namespace transaction.
- Independent review initially found missing bulk CAS, suppressed denial auditing, weak copy/delete assertions, and missing real legacy-admin route coverage. All were addressed and re-reviewed.
- Independent re-review found no remaining Critical or Important issue.
- Non-blocking limitation: recovered/pending HTTP formatting uses constructed Task 4 results at the controller boundary because the publication failpoint is model-private. Task 4 tests exercise real failure/recovery/pending production; controller tests verify those result flags are emitted as successful, non-retry responses.
