# Task 7 report — Public Pricing Legacy Fallback

Branch: `codex/video-pricing-task7` (worktree based on `ef7b42d8`, created because the main
checkout held uncommitted Task 6 remediation). Rebase/cherry-pick onto the final Task 6
remediation commit before merging.

Commits: `1f6c4664` (implementation, TDD) + `083679c2` (review fix).

## What changed

- `model/pricing.go`: resolution-table exposure in `updatePricing` is gated on
  `!constant.IsSunoModel(model)` — the same shared classifier request freezing uses in
  `relay/relay_adaptor.go` `PrepareTaskBillingPlan`. The `TaskBillingMode` resolution block
  (explicit config, else per-second inference for `openai-video` endpoints) now applies to both
  legacy branches (fixed price and ratio), so ratio-priced video models expose their historical
  mode too.
- `model/pricing_endpoint_test.go`: `legacyPricingForModel` helper + four regressions
  (legacy fixed, legacy ratio, resolution-wins-over-retained-legacy-price, Suno-stays-legacy).
- Frontend: deleted `isVideoModelMissingResolutionPrices` and every strict "Unsupported"
  branch (`model-helpers.ts`, `model-card.tsx`, `pricing-columns.tsx`, `model-details.tsx` ×2).
  Legacy video models render through existing `formatRequestPrice` / minimum-tier /
  `ModelBillingModeBadge` logic. No provider-name heuristics added.
- New `web/src/features/pricing/components/__tests__/legacy-video-pricing.test.tsx` renders the
  real `ModelCard` (happy-dom + react-dom) and pins price/unit visible, no "Unsupported".
  `resolution-price.test.ts` legacy coverage replaces the superseded strict-rollout tests.

## Verification

- RED confirmed before the production change: legacy-ratio missing `TaskBillingMode`, Suno
  exposing its resolution table, `ModelCard` forcing "Unsupported".
- `go test ./model -count=1` full package PASS; focused frontend tests 12/12 PASS;
  `bun run typecheck` PASS; oxlint clean on every changed file.

## Independent review

Verdict: ready to merge, no Critical findings. The one Important finding (the
resolution-wins test never installed a retained legacy `ModelPrice`; a plan gap reproduced
faithfully) was fixed in `083679c2` — the test now retains `{"zz-video-resolution":0.4}` and
asserts the table minimum `0.1` wins. Reviewer confirmed the ratio-branch `TaskBillingMode`
addition is behavior-safe for non-video models by tracing every frontend/backend consumer.

## Residuals

- Orphaned i18n keys after overlay removal: `"Unsupported"` and `"Video requests to this model
  are rejected until an administrator configures resolution prices."` (the
  `"No resolution prices configured"` key is still used by system-settings). Run
  `bun run i18n:sync` in a follow-up before release; locale files were outside this task's
  commit scope and are dirty in the main checkout.
- Plan step-5 lint expectation is stale: `src/features/pricing` carries ~45 pre-existing oxlint
  errors in untouched files (`mock-stats.ts`, `billing-expr.ts`, `model-details-api.tsx`).
  Changed files are clean; verified pre-existing at `ef7b42d8` via stash comparison.
