# Video Resolution Pricing Design

## Summary

Video generation pricing will be configured by model and effective output resolution. Every configured value is a per-second price, and the final charge is based on the request's effective output resolution and bounded duration. A video request is supported only when its effective resolution has an explicit price entry.

This replaces the current model-level base price plus provider-specific hard-coded resolution multipliers for video generation. Non-video pricing remains unchanged.

The existing `TaskBillingMode` option and legacy task-pricing paths remain unchanged for backward compatibility. The new resolution-pricing path does not read, update, or depend on `TaskBillingMode` and never performs per-call billing.

## Goals

- Configure a distinct price for every supported resolution of a video model.
- Charge every resolution price per second.
- Resolve omitted request resolutions to the actual provider or protocol default before pricing.
- Reject video requests whose model or effective resolution has no configured price.
- Charge from the same effective resolution that is sent upstream.
- Preserve quota overflow protections, pre-consume behavior, settlement, and auditability.
- Expose resolution prices consistently in both administration entry points and the public pricing view.

## Non-goals

- Replacing token pricing, ordinary fixed `ModelPrice`, or tiered billing expressions.
- Guessing a price from pixel count or interpolating between configured resolutions.
- Automatically migrating an existing video `ModelPrice` into a resolution price.
- Changing or removing the existing `TaskBillingMode` option or its legacy consumers.
- Supporting per-call billing on the new resolution-pricing path.
- Introducing database-specific schema changes.

## Configuration Model

Add a `VideoResolutionPrice` option with this JSON shape:

```json
{
  "sora-2": {
    "720p": 0.1,
    "1080p": 0.18,
    "4k": 0.35
  }
}
```

The outer key is the original model name used for billing configuration. Existing model-name normalization and compact wildcard matching apply. The inner key is a canonical resolution, and the value is a USD price per second.

Resolution prices must be finite and greater than zero. Resolution keys are trimmed and lowercased before storage and lookup. If two input keys normalize to the same canonical resolution, the update is rejected instead of silently overwriting one value.

A canonical key must match either `^[1-9][0-9]{2,4}p$` for a vertical-resolution label or `^[1-9][0-9]*k$` for a provider-defined K tier. Thus `480p`, `720p`, `1080p`, `1440p`, `2160p`, `2k`, and `4k` are valid keys; `uhd` and raw dimensions are not canonical configuration keys. `2160p` and `4k` remain distinct unless an adapter explicitly declares one as the provider's effective tier. Each adapter maps its accepted protocol values, including orientation variants such as `1280x720` and `720x1280`, to the canonical tier that the upstream provider actually produces. No generic pixel-count heuristic may override provider semantics.

## Pricing Semantics

The selected resolution price becomes the request's per-second base model price:

`resolution price × effective duration seconds × group ratio × product of allowed independent ratios`

The initial allowlist of independent ratios contains only Doubao's `video_input` ratio. Duration is represented by the billing formula, and output resolution is represented by the selected direct price, so neither is also included as an independent ratio. Any future independent ratio must define its name, business meaning, validation bound, ownership, and non-duplication test before it is added to the resolver result.

The resolution-pricing path always applies duration and ignores `TaskBillingMode`, even when that legacy option contains a matching model entry. Administrators configure only resolution prices; there is no billing-unit selector for this mode.

The following conditions produce an HTTP 400 `video_resolution_not_supported` response:

- the video model has no `VideoResolutionPrice` entry;
- the effective resolution has no price entry;
- the adapter cannot determine a trustworthy effective resolution;
- the request contains a resolution the upstream adapter does not support.

There is no fallback to `ModelPrice`, a multiplier of `1`, the nearest resolution, or another channel's default.

The error message is `video resolution <resolution> is not configured for model <model>`, using the canonical effective resolution and original model name. When the adapter cannot determine a resolution, `<resolution>` is the literal `unknown`.

## Effective Resolution Contract

Introduce an optional video-specific billing resolver interface for task adapters rather than expanding the common task adapter contract used by non-video task types. The resolver returns a structured selection containing:

- canonical effective resolution;
- effective bounded duration;
- independent non-resolution ratios, if any;
- a validation error when the request cannot be priced safely.

Each video adapter must derive this selection from the same normalized request values used to build its upstream payload. Shared provider-specific conversion functions should be used by both paths so `metadata.resolution`, top-level `size`, multipart fields, defaults, and model-specific behavior cannot drift between billing and delivery.

When the client omits resolution, the adapter returns the actual default selected by that provider or protocol. Providers with a documented fixed output resolution may declare that fixed value. If the effective output resolution cannot be established, the request remains unsupported until the adapter can resolve it reliably.

Initial adapter work covers the existing resolution-bearing video integrations, including Sora, Gemini/Vertex, Ali, Doubao, Hailuo, and Vidu. Other video adapters must either declare their documented fixed default or reject pricing; they must not silently use a made-up tier. Official provider documentation must be checked when a default is not already unambiguous in the adapter code.

## Backend Data Flow

1. Parse and validate the video task request as today.
2. Apply channel model mapping, retaining the original model name for price configuration and the upstream model name for provider semantics.
3. Ask the video billing resolver for the effective resolution, duration, and independent ratios.
4. Resolve the price from the original model name and effective resolution using existing model-name normalization and compact wildcard matching.
5. Build `PriceData` from the selected direct price and group ratio.
6. Apply the bounded duration and only non-duplicative independent ratios.
7. Convert quota with the checked helpers in `common/quota_math.go`, attach any saturation marker, and perform pre-consume.
8. Submit the upstream request and settle using the same selected billing data.
9. Save the effective resolution, selected per-second price, effective duration, and ratios in the task billing snapshot.

A tested domain helper constructs video `PriceData` from an explicit resolution price. This is a stable billing concept and avoids mutating the global `ModelPrice` map or duplicating quota setup logic at the relay call site.

## Existing Provider Multipliers

Hard-coded resolution ratios in Sora, Gemini/Vertex, and Ali are removed from the direct-resolution pricing path. Their resolution parsers remain useful for determining the canonical effective resolution.

Doubao currently combines output resolution and video-input state in one hard-coded price table. The output-resolution portion must be replaced by the configured direct price. If video input still changes provider cost, its independent ratio must be calculated within the already selected resolution tier so the resolution component is not charged twice.

Any adjusted billing returned after submission must preserve the selected resolution and may adjust only fields the upstream response can authoritatively correct, such as actual duration.

## Remix and Task Snapshots

Extend the existing JSON `TaskBillingContext` with optional fields for effective resolution, selected per-second resolution price, and effective duration. This requires no relational database migration and remains compatible with SQLite, MySQL, and PostgreSQL. New resolution-priced tasks do not set or consult the legacy per-call snapshot flag.

A remix request inherits the original task's saved effective resolution only when the remix protocol guarantees that the upstream payload preserves that resolution. Otherwise the provider adapter resolves the effective output resolution again from the remix payload. For older tasks without this snapshot field, the relay first attempts to recover a resolution from saved task data using the provider adapter. If recovery is impossible, it uses the adapter's real default only when the remix protocol actually sends that default; otherwise it returns `video_resolution_not_supported`.

The consume log and administrator-visible task details record the canonical resolution and selected price. Existing quota saturation audit behavior remains unchanged.

## Administration UI

Add a `video_resolution` pricing mode to the model pricing editor. Its form contains:

- a repeatable list of resolution and USD price rows;
- inline validation for required, unique canonical resolutions and positive finite prices;
- a preview showing each resolution price with `/second`.

Both administration paths must read and write the new option:

- system settings model pricing editor;
- model create/edit drawer.

Model delete, rename, and copy operations must update `VideoResolutionPrice` together with the other model settings. Raw JSON editing remains available with the same backend validation.

All user-facing strings use i18next and are added for `en`, `zh`, `zh-TW`, `fr`, `ja`, `ru`, and `vi`.

## Public Pricing Contract

Extend the pricing response with `resolution_prices`. A model with resolution prices is treated as fixed-price video billing even when it has no ordinary `ModelPrice`.

Pricing cards show the lowest configured price as a `from` value with the per-second unit. Model details show the complete resolution-to-price table. The existing compatibility billing-unit field may report `per_second` for a resolution-priced model, but it is not sourced from `TaskBillingMode`. Existing consumers that ignore the new optional field continue to work.

The exposed pricing cache and billing-configuration checks include `VideoResolutionPrice` and are invalidated when it changes. Ratio synchronization does not invent or import resolution prices unless its upstream contract explicitly supplies the nested map.

## Validation and Safety

- Configuration updates reject malformed nested JSON and invalid prices before replacing the live map.
- Request resolution is selected from the actual adapter payload semantics, not from an untrusted field in isolation.
- Duration continues to use `relaycommon.MaxTaskDurationSeconds` across ordinary fields, metadata, multipart, and passthrough paths.
- All quota conversion uses checked quota helpers and records saturation on the relay/task log.
- Missing configuration fails before pre-consume or upstream submission.
- Model matching uses the original model name; resolution interpretation uses the mapped upstream model and provider request.

## Test Strategy

Implementation follows red-green-refactor. Tests protect these observable contracts:

### Backend

- Nested configuration load, replacement, canonicalization, compact wildcard matching, invalid values, duplicate aliases, and missing entries.
- Exact 720p/1080p prices for the same model.
- Different durations multiply the selected resolution price exactly once, including when the legacy `TaskBillingMode` contains `per_call` for the same model.
- Omitted resolution uses each adapter's actual default.
- Metadata overrides top-level size only when the upstream payload does the same.
- Unsupported and unconfigured resolutions return HTTP 400 before pre-consume.
- Legacy hard-coded resolution ratios do not double-charge.
- Independent provider ratios remain correct.
- Pre-consume, settlement, refund, quota saturation, task snapshots, remix, and administrator log fields retain their invariants.
- Pricing API recognizes a resolution-priced model and exposes its table.

New or substantially rewritten Go tests use `testify/require` for fatal setup assertions and `testify/assert` for non-fatal value assertions.

### Frontend

- Add, edit, remove, rename, copy, and save resolution price rows.
- Reject empty, duplicate, zero, negative, and non-finite prices.
- Render the fixed per-second unit without exposing a per-call selector for resolution pricing.
- Render pricing card minimums and detail tables.
- Cover the user-visible form behavior with React Testing Library and pure transformations with Vitest.
- Run i18n synchronization and verify all supported locale files.

### Verification Commands

- Focused Go package tests for settings, relay helpers, affected task adapters, service settlement, and pricing endpoints.
- Broader Go tests for affected modules and a root Go build.
- Frontend focused tests, `bun run typecheck`, lint on changed files, i18n checks, and `bun run build`.

## Rollout and Compatibility

This is intentionally strict for video generation. Existing video models configured only with `ModelPrice` stop accepting new video requests until an administrator adds resolution prices. The UI marks this state as unsupported rather than presenting the old price as active.

Non-video calls and historical task records remain compatible. New snapshot fields are optional, and no destructive data migration is required.
