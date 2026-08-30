# Video Resolution Pricing Compatibility Design

## Summary

Resolution pricing becomes an opt-in video billing method instead of a mandatory replacement for legacy task billing. Existing installations can upgrade without reconfiguring every video model: a model continues to use `ModelPrice`, `ModelRatio`, adapter `EstimateBilling` ratios, and `TaskBillingMode` until it has a valid `VideoResolutionPrice` table.

This design supersedes the strict rollout and no-fallback requirements in `2026-08-28-video-resolution-pricing-design.md`.

## Runtime Selection

Billing mode is selected from the original model name before pre-consume or upstream submission:

1. Suno always uses the legacy task billing path.
2. A non-Suno model with a non-empty matching `VideoResolutionPrice` table uses resolution pricing.
3. A model without a matching resolution table uses the unchanged legacy task billing path.

Resolution configuration has precedence over legacy configuration while both are stored. Once a model opts in, every reachable resolution must have an explicit positive price. A configured model does not fall back to `ModelPrice` when the requested resolution is absent, invalid, unknown, or unsupported; it returns HTTP 400 `video_resolution_not_supported` before charging or submission.

An adapter that does not implement `VideoBillingResolver` remains usable through legacy billing. If an administrator nevertheless assigns it a resolution table, the request fails before submission because the selected mode cannot be resolved safely.

## Billing Semantics

The new path remains per-second and uses:

`resolution price × effective duration seconds × group ratio × independent ratios`

`TaskBillingMode` is ignored only while resolution pricing is active. Legacy models retain their existing `per_call` or `per_second` behavior. A finite group ratio of zero is valid on both paths and produces a free charge; resolution prices and independent ratios remain finite and strictly positive.

The existing reservation ledger, bounded submission, settlement, task snapshot, remix, polling, and saturation-audit behavior remains exclusive to resolution-priced tasks. Legacy tasks continue to use their existing billing lifecycle.

## Administration Behavior

The model pricing UI may continue to present one active editing mode at a time, but saving `video_resolution` must preserve any stored legacy price, ratio, provider multiplier, and `TaskBillingMode` entries for that model. These retained values are inactive fallback configuration while the resolution table exists.

Removing the model's resolution table reactivates the preserved legacy configuration. Switching explicitly from resolution pricing to a legacy editor mode removes the resolution table and updates the selected legacy configuration using the existing mutual-exclusion rules between fixed-price, ratio, and expression modes.

Rename and copy operations preserve both the active resolution table and retained legacy configuration. Delete operations continue to remove all configuration owned by the deleted model.

Invalid, duplicate, or empty resolution rows are rejected before any model mutation or option update.

## Public Pricing

A model with resolution prices exposes `resolution_prices`, reports `per_second`, and uses the lowest valid tier as its summary price.

A model without resolution prices exposes its legacy `ModelPrice` or `ModelRatio` exactly as before the feature. The public pricing UI must not mark a legacy-priced video model unsupported merely because `resolution_prices` is absent.

## Upgrade Behavior

There is no automatic data migration. Existing `ModelPrice`, `ModelRatio`, provider ratios, and `TaskBillingMode` settings remain active after upgrade, so existing video models continue serving requests. Administrators migrate models individually by adding resolution tables. Removing a table rolls the model back to its retained legacy settings.

## Error Handling

- Missing model resolution table: use legacy billing.
- Present table but missing effective resolution: HTTP 400 `video_resolution_not_supported`.
- Present table but adapter lacks a resolver: HTTP 400 before pre-consume and submission.
- Invalid or empty table update: reject the configuration update and retain the previously published table.
- Legacy price missing after fallback selection: return the existing legacy model-price error.

## Test Contract

Backend regression tests must prove:

- an unconfigured video model with `ModelPrice` submits through legacy billing;
- legacy `per_call` and `per_second` behavior remains intact;
- a configured resolution model ignores legacy mode and uses the resolution price;
- a configured model never falls back when its effective tier is missing;
- an adapter without a resolver works in legacy mode and rejects an explicit resolution opt-in;
- a zero group ratio produces zero quota without weakening positive price and independent-ratio validation;
- the pricing endpoint exposes legacy video pricing when no resolution table exists.

Frontend regression tests must prove:

- public pricing renders a legacy video price instead of an unsupported state;
- saving resolution prices retains stored legacy settings;
- removing resolution prices reactivates the retained legacy snapshot;
- invalid or empty resolution rows still block submission.

## Non-goals

- Automatically converting per-call prices into per-second prices.
- Falling back from a configured but incomplete resolution table.
- Adding a second persistent billing-mode option when table presence already selects the mode.
- Changing provider resolution or duration capability rules.
- Migrating historical task snapshots.
