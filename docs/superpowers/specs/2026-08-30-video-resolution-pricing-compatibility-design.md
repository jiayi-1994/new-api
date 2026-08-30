# Video Resolution Pricing Compatibility Design

## Summary

Resolution pricing is an opt-in video billing method instead of a mandatory
replacement for legacy task billing. Existing installations can upgrade without
reconfiguring every video model: a model continues to use `ModelPrice`,
`ModelRatio`, adapter `EstimateBilling` / `AdjustBillingOnSubmit` ratios, and
`TaskBillingMode` until it has a valid `VideoResolutionPrice` table.

This design supersedes the strict rollout and no-fallback requirements in
`2026-08-28-video-resolution-pricing-design.md`. It otherwise preserves the
existing task billing, pre-consume, retry, settlement, snapshot, and refund
lifecycles. The feature adds a new billing method; it does not redefine how
running tasks or legacy billing behave.

## Authoritative Billing Selection

The system uses one authoritative activation rule across routing, relay billing,
administration, and public pricing:

1. Suno always uses the legacy task billing path and cannot opt into video
   resolution pricing.
2. A non-Suno model with a non-empty matching `VideoResolutionPrice` table opts
   into resolution pricing.
3. A model without a matching resolution table uses the unchanged legacy task
   billing path.

For an opted-in model, channel selection filters out Suno channels and task
adapters that do not implement `VideoBillingResolver`. Incompatible channels are
not selected and cannot cause nondeterministic HTTP 400 responses when another
compatible channel is available. If no compatible channel can serve the model,
the request fails before pre-consume or upstream submission with HTTP 400
`video_resolution_not_supported`.

Capability is evaluated independently for each concrete model after existing
exact or compact-wildcard price matching. Wildcard configuration remains
shape/value validated at save time; it is not rejected based on a set of channel
bindings that can change or gain future matches. Routing re-evaluates resolver
capability whenever channel bindings or availability change. Public pricing uses
the stable table/Suno activation rule and does not switch the displayed billing
method because of temporary channel availability. A Suno-only model ignores a
matching table and remains legacy. A non-Suno model with a matching table but no
compatible channel remains configured for resolution pricing but is unavailable
for submission until a compatible channel is enabled.

Resolution configuration has precedence over every retained legacy
configuration while it is active. Once a model opts in, every reachable
resolution must have an explicit positive price. A configured model never falls
back to legacy pricing when the requested resolution is absent, invalid,
unknown, or unsupported; it returns HTTP 400
`video_resolution_not_supported` before charging or submission.

## Request and Retry Snapshot

Billing selection occurs once, before the first pre-consume attempt. The request
freezes:

- the selected billing kind (`legacy` or `video_resolution`);
- the complete matching resolution table when resolution pricing is active;
- the original model name used for pricing;
- the original request identity used to create billing state.

Every channel retry uses this frozen selection and, for resolution pricing, the
frozen table. A live configuration change cannot switch an in-flight request
between legacy funding and the resolution reservation ledger. Configuration
changes affect only requests whose billing selection has not yet been frozen.
The pre-consume step creates a funding session bound permanently to the frozen
billing kind, and retries reuse that session.

For resolution pricing, the selected channel may change during retry, but every
retry must still use a non-Suno adapter that implements `VideoBillingResolver`.
The adapter resolves the effective provider resolution and duration for that
attempt, while the price is read only from the request's frozen table.

## Billing Semantics

The resolution path remains per-second and uses:

`resolution price × effective duration seconds × group ratio × independent ratios`

`TaskBillingMode` is ignored only while resolution pricing is active. Legacy
models retain their existing `per_call` or `per_second` behavior, including
adapter estimate and authoritative submit-time adjustment ratios.

A finite group ratio of zero is valid on both paths and produces zero quota.
Resolution prices, quota conversion units, and independent ratios remain finite
and strictly positive. Existing duration/count bounds, checked quota conversion,
insufficient-quota handling, and saturation auditing remain mandatory on both
legacy and resolution paths.

Only the reservation ledger, frozen resolution task snapshot, resolution-aware
polling settlement, and their resolution-specific audit fields are exclusive to
resolution-priced tasks. Legacy tasks continue to use their existing billing and
refund lifecycle.

## Running Tasks, Settlement, and Refunds

Once an upstream task has been accepted, later pricing configuration changes do
not affect it. A running task continues to use the billing context captured when
it was submitted, including its billing kind, selected price or legacy price
data, group ratio, independent ratios, effective duration, quota conversion
basis, funding source, and reservation identifier.

Resolution-priced tasks keep the existing reservation-ledger settlement,
bounded submission, task snapshot, remix, polling, saturation audit, orphan
recovery, and refund paths. Legacy tasks keep the existing pre-consume,
settlement, adjustment, and refund paths. Failures never convert one billing
kind into the other's refund mechanism.

## Configuration Validation and Removal

`VideoResolutionPrice` remains a nested `model → resolution → positive USD price
per second` option.

- Root `{}` is valid and removes all resolution tables.
- A per-model empty table such as `{"model": {}}` is invalid.
- Invalid, duplicate, non-canonical, non-positive, or non-finite rows are
  rejected before any database, model, or live-option mutation.
- Clearing every row while the editor remains in `video_resolution` mode is a
  validation error.
- Explicitly switching to a legacy editor mode removes that model's resolution
  table and publishes the chosen retained legacy configuration.
- Compact wildcard matching follows the existing model-matching rules. A
  wildcard table is active for each non-Suno concrete model independently. Its
  resolver capability is checked against that model's current eligible channels
  during routing, not during raw option save or public-pricing construction.

An invalid update retains the previously persisted and published table.

## Retained Legacy Configuration

While resolution pricing is active, the system retains the model's complete
persistent legacy pricing snapshot:

- `ModelPrice`;
- `ModelRatio`;
- `CacheRatio`;
- `CreateCacheRatio`;
- `CompletionRatio`;
- `ImageRatio`;
- `AudioRatio`;
- `AudioCompletionRatio`;
- `billing_setting.billing_mode`;
- `billing_setting.billing_expr`;
- `TaskBillingMode`.

Adapter `EstimateBilling` and `AdjustBillingOnSubmit` values are request-derived,
not persistent option entries. They are neither copied nor stored as fallback
configuration; the legacy path recomputes them for each request as before.

Removing the resolution table reactivates the retained legacy snapshot.
Switching explicitly from resolution pricing to a legacy editor mode removes
the table and updates the selected fixed-price, ratio, or expression
configuration using the existing mutual-exclusion behavior.

## Administration Transactions

The existing ownership and collision rules remain authoritative:

- model-name uniqueness checks continue to reject duplicate renames;
- configuration that the current operation does not own or has not loaded is
  not silently overwritten;
- an explicit pricing copy continues to replace the target configuration using
  the existing copy semantics;
- deleting a model removes all configuration owned by that model.

Pricing-aware save, rename, explicit pricing copy, and delete operations use a
single backend command that carries the model mutation and every owned pricing
change. They must not be implemented as a model request followed by independent
frontend option requests. Metadata-only and status-only model saves preserve the
existing behavior and do not rewrite pricing documents.

The backend validates every proposed document first, locks affected `Option`
rows in a deterministic order using the repository's cross-database locking
conventions, and commits the owned model and pricing changes together. All other
writers of the persistent options listed above—including raw option editing and
ratio settings—participate in the same shared row-locking or compare-and-swap
protocol so they cannot overwrite a concurrent lifecycle mutation with a stale
whole-document value.

Any failure before the database commit rolls back the complete operation.
In-memory settings publish only after commit. A post-commit publication failure
does not claim to roll back committed data: it invalidates and reloads affected
caches from the database, records an operational error, and lets other instances
converge from the committed database state.

The transaction must work with SQLite, MySQL, and PostgreSQL. It must preserve
the existing collision outcome rather than introduce new overwrite behavior.
Rename moves the active resolution table and retained legacy snapshot from the
old key to the new key. Copy leaves the source intact and replaces only the
target according to the established copy ownership/collision behavior. Delete
removes both active and retained state owned by the deleted model.

## Public Pricing

An actively resolution-priced model exposes `resolution_prices`, reports
`per_second`, and uses the lowest valid tier as its summary price.

A model that does not activate resolution pricing exposes its legacy
`ModelPrice` or `ModelRatio` exactly as before the feature, including its legacy
`TaskBillingMode`. The public pricing UI must not mark a legacy-priced video
model unsupported merely because `resolution_prices` is absent.

Suno is always represented by its legacy pricing even if an exact or wildcard
table matches its name. A non-Suno model with a matching table exposes the
resolution contract; routing independently reports
`video_resolution_not_supported` when it currently has no compatible channel.

## Upgrade Behavior

There is no automatic data migration. Existing persistent pricing options remain
active after upgrade, so existing video models continue serving requests.
Administrators migrate models individually by adding valid resolution tables.
Removing a table rolls the model back to its retained legacy settings.

Requests already in pre-consume, retry, submission, polling, settlement, or
refund keep their frozen request/task billing state. An upgrade or administrator
edit never reprices a running task.

## Error Handling

- Missing model resolution table: use legacy billing.
- Present table but missing effective resolution: HTTP 400
  `video_resolution_not_supported`.
- Present table but no compatible resolver channel: HTTP 400 before pre-consume
  and submission.
- Incompatible channels alongside compatible channels: filter the incompatible
  channels and continue normal channel selection.
- Invalid or per-model empty table update: reject the configuration update and
  retain the previously published table.
- Legacy price missing after fallback selection: return the existing legacy
  model-price error.
- Failure after pre-consume: use the refund path belonging to the request's
  frozen billing kind.

## Test Contract

Backend regression tests must prove:

- an unconfigured video model with `ModelPrice` submits through legacy billing;
- legacy `per_call` and `per_second` estimate, adjustment, snapshot, settlement,
  and refund behavior remains intact;
- a configured resolution model ignores retained legacy mode and pricing and
  uses the frozen resolution price;
- a configured model never falls back when its effective tier is missing;
- mixed compatible/incompatible channels route only to compatible resolvers;
- an opted-in model with no compatible resolver fails before pre-consume;
- Suno remains legacy even when an exact or wildcard table matches it;
- wildcard tables activate independently for each matched non-Suno model and
  follow live channel capability changes during routing without mutating stored
  or publicly displayed prices;
- changing or removing configuration between retries does not change the
  request's frozen billing kind or price table;
- running legacy and resolution tasks keep their original settlement and refund
  mechanisms after configuration changes;
- a zero group ratio produces zero quota without weakening positive price,
  quota-unit, and independent-ratio validation;
- root `{}` removes all tables, while a per-model empty table is rejected without
  replacing persisted or live configuration;
- pricing-aware save, rename, copy, and delete atomically preserve or remove the
  active resolution table and every retained legacy option, including
  pre-commit rollback, post-commit publication recovery, and concurrent generic
  or raw-option writers on supported databases;
- the pricing endpoint exposes legacy fixed-price and ratio-priced video models
  when no active resolution table exists, and exposes only the active resolution
  contract when the model opts in.

Frontend regression tests must prove:

- public pricing renders a legacy video price and unit instead of an unsupported
  state;
- saving resolution prices retains every persistent legacy setting;
- removing resolution prices reactivates the retained fixed, ratio, or
  expression snapshot;
- rename and copy preserve both active resolution and retained legacy state using
  the existing ownership/collision rules;
- invalid or empty resolution rows block submission;
- switching explicitly to a legacy mode removes the resolution table and keeps
  existing mutual-exclusion behavior.

## Non-goals

- Automatically converting per-call prices into per-second prices.
- Falling back from a configured but incomplete resolution table.
- Adding another persistent billing-mode option when table presence already
  selects the mode.
- Repricing historical or running task snapshots.
- Changing provider resolution or duration capability rules.
- Storing adapter estimate/adjustment ratios as administration configuration.
- Changing established model-name collision, copy-ownership, settlement, or
  refund semantics beyond making their multi-option persistence atomic.
