# Changelog

## 2.1.0

- Add a per-second surcharge using `surcharge_seconds`, always equal to the validated output duration.
- Keep one row per resolution and the same pricing for all supported reference media.
- Official hosts retain their separate per-request additional-charge field. Move surcharge prices to the new per-second column and set the old per-request charge to zero to avoid double charging.
- Existing expressions are not changed automatically; re-save pricing after installing this version.

## 2.0.0

- Restrict resolutions to 480p, 720p, 1080p and 4k. Use the top-level allowlist to disable a tier on official images.
- Remove reference-media pricing dimensions. Each resolution now has one shared per-second price and the host's optional per-request additional charge.
- Keep forwarding all supported reference media independently of pricing.
- Derive usage examples from the allowlist, and reject disabled resolutions through both explicit fields and pixel sizes.
- Breaking pricing change: remove `reference_type` from usage facts. Re-save prices after updating; old expressions are not migrated automatically. An empty or zero price does not disable a tier on the official host.

## 1.1.0

- Simplify reference pricing to two groups: without reference video (`none`) and with reference video (`video`).
- Keep forwarding image and audio references; their presence no longer adds pricing conditions.
- Classify every image/video/audio combination, empty video arrays and supported video aliases consistently.
- Reconfigure saved 1.0.0 prices for the two groups after uploading this version; old expressions are not automatically merged.

## 1.0.0

- Add direct Mega video creation, polling and artifact download through the official New API Task Plugin API v1.
- Normalize OpenAI-style duration, size and image URL references to Mega's documented JSON fields.
- Report seconds, resolution and reference-media type for task usage expressions.
- Require explicit duration and resolution; reject unsupported uploads, conflicting aliases and unknown fields.
- Keep channel credentials off external result downloads.

### Migration

Configure plugin-specific prices and a Mega channel before accepting traffic. This plugin does not import the custom fork's resolution price tables, historical tasks or measured input-video-duration surcharges.
