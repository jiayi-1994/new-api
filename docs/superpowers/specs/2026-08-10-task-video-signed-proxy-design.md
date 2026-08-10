# Task Video Signed Proxy Design

## Context

Video task records currently expose two independently populated result paths:

- `result_url` comes from `Task.GetResultURL()`. Depending on the adaptor, it may be a direct upstream URL or the local `/v1/videos/{task_id}/content` proxy.
- `data` contains a redacted copy of the upstream polling response. The current redaction removes large base64 payloads but preserves upstream task IDs and URL fields such as `url`, `video_url`, `metadata.url`, and `metadata.origin_video_url`.

The dashboard cannot safely use the existing local proxy in a normal new-tab navigation because dashboard authentication is carried in an `Authorization` header. A browser navigation does not pass that header. The proxy is also owner-scoped, so an administrator cannot use it for another user's task. The frontend therefore falls back to direct provider URLs, which exposes upstream domains and signed storage credentials.

## Goals

- Never expose upstream video URLs, signed storage credentials, or upstream task IDs through `/api/task` or `/api/task/self`.
- Keep successful video downloads available from a same-origin URL that can open in a new tab.
- Allow administrators to download videos belonging to users they are authorized to inspect.
- Preserve video generation, polling, billing, task persistence, API-token access, Suno audio rendering, failure details, and OpenAI-compatible video APIs.
- Apply response-time redaction to historical and new tasks without a database migration.

## Non-goals

- Do not copy generated videos into local or object storage.
- Do not guarantee that an upstream provider retains video content for seven days.
- Do not refresh or extend expired provider URLs in this change.
- Do not expose a dashboard access token, API key, channel key, or upstream signed URL in a query parameter.

## Compatibility Boundary

The local video capability token is valid for seven days. That token authorizes access to the local proxy; it does not extend the upstream provider's retention period.

Effective content availability is therefore the shorter of:

1. the seven-day local capability lifetime; and
2. the period for which the upstream provider still serves or can reconstruct the video.

This preserves current provider behavior. Providers whose content endpoint can be reconstructed from an upstream task ID continue to use that path. Providers that only return a temporary OSS/R2 URL remain available only while that URL works. If guaranteed seven-day content retention becomes a requirement, it needs a separate object-storage design.

## Public Task Projection

`/api/task` and `/api/task/self` will serialize video tasks through a public projection instead of returning the stored upstream polling body.

The projection contains only locally owned fields:

```json
{
  "object": "video",
  "status": "completed",
  "progress": 100,
  "task_id": "task_public_id",
  "model": "public-model-name",
  "url": "/v1/videos/task_public_id/content?video_token=..."
}
```

Rules:

- Video tasks are non-Suno asynchronous tasks. Suno task data remains unchanged so audio preview continues to work.
- The public `task_id` is the gateway task ID, never the upstream task ID.
- Successful video tasks receive a signed local `result_url` and the same URL in the projected `data.url` for compatibility.
- In-progress video tasks omit the URL.
- Failed video tasks omit `result_url`; their safe error message remains in `fail_reason` and may be projected as an error message.
- Raw `data`, `PrivateData.ResultURL`, legacy URL-shaped `fail_reason`, upstream task IDs, and provider metadata never cross this response boundary.
- Other internal polling and relay paths continue using the stored task data unchanged.

## Seven-day Video Capability

A dedicated video-content token will be issued when a task-log response serializes a successful video task.

Claims include:

- public task ID;
- real task owner user ID;
- issued-at and expiry timestamps;
- a purpose/audience dedicated to video content;
- a version identifier for future format changes.

The token uses the project's existing HS256/session-secret infrastructure with a purpose-separated signing key. Validation must:

- accept only the expected signing algorithm;
- compare the path task ID with the signed task ID;
- reject missing, malformed, tampered, expired, or future-dated tokens;
- reject tokens with the wrong purpose/audience;
- load the task using the signed owner ID and public task ID;
- require the task to be successful.

The token is a seven-day bearer capability. Anyone who obtains it can use it until expiry, so it is returned only from authenticated task-log APIs and must never be written to application logs.

## Video Content Route

`GET /v1/videos/{task_id}/content` keeps the existing authenticated behavior for API clients and adds capability-token authentication for browser navigation.

Authentication paths:

1. If an `Authorization` header is present, preserve the current dashboard/API-token owner-scoped behavior.
2. Otherwise, require and validate `video_token`, then query the task using the signed owner ID.

An administrator viewing another user's task receives a capability signed with that task's actual owner ID. The content endpoint does not need a broad administrator bypass and cannot use the administrator ID to fetch another user's task.

After authorization, the existing provider-specific resolution and SSRF protection remain in place. The server fetches and streams the video; it never redirects the browser to the upstream URL.

## Response Hardening

The proxy will stop copying every upstream response header. It will allow only headers needed for video delivery, such as:

- `Content-Type`
- `Content-Length`
- `Content-Disposition`
- `Accept-Ranges`
- `Content-Range`
- `ETag`
- `Last-Modified`

Headers capable of revealing an upstream location, including `Location`, `Content-Location`, and `Link`, are not forwarded.

The response uses private caching bounded by the capability lifetime rather than `Cache-Control: public, max-age=86400`. Error responses remain generic and do not include upstream URLs.

## Frontend Behavior

Successful video task rows use only the signed local `result_url` and open it with `target="_blank"` plus `rel="noopener noreferrer"`.

The frontend removes direct provider fallbacks from:

- `data.url`
- `data.video_url`
- `data.metadata.url`
- `data.metadata.origin_video_url`
- URL-shaped `fail_reason`

If no valid signed local URL exists, the row renders a dash. Failure dialogs and Suno audio preview remain unchanged.

## Failure Handling

- Invalid or expired capability: `401` with a generic error.
- Valid capability for a missing task: `404`.
- Task not complete: `400`.
- Upstream content expired or unavailable: `502` without the upstream URL.
- SSRF rejection: preserve the existing protected failure, without returning the attempted URL.

Refreshing the task-log page issues a new seven-day local capability, but it cannot restore content that the upstream has already removed.

## Testing

Backend regression coverage will verify:

- token issue/validation, seven-day expiry, tampering, wrong task, wrong purpose, and future timestamps;
- ordinary-user and administrator task projections contain no upstream host, upstream task ID, storage credential, or raw video data;
- successful, in-progress, failed, historical, and Suno tasks preserve their intended contracts;
- authenticated owner access remains compatible;
- signed cross-user administrator access resolves the real owner without granting a general admin bypass;
- proxy responses omit location-bearing headers and use private caching;
- expired upstream content returns a generic error.

Frontend regression coverage will verify:

- signed same-origin links open in a secure new tab;
- provider URLs are never selected;
- missing links render a dash;
- failure details and Suno audio preview remain unchanged.

## Rollout

No schema migration or backfill is required. Upstream URLs remain stored privately for proxy use. Once deployed, both historical and new task-log responses are sanitized dynamically.

The implementation should be deployed with a stable `SESSION_SECRET`; rotating it intentionally invalidates all outstanding video capabilities.
