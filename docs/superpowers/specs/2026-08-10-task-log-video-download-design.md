# Task Log Video Download Links

## Context

Task log responses already include successful video result URLs, but the frontend decides whether to show the video link by checking whether `fail_reason` starts with `http`. Successful tasks now keep failures and results separate, so `fail_reason` is empty while `result_url` and the provider response in `data` contain the video location. The existing condition therefore renders `-` for completed videos.

The task-log response also no longer matches the frontend type: `TaskLog` does not declare `result_url`, and `data` is typed as a JSON string even though the API may return a decoded object or array.

## Goals

- Show the existing “Click to preview video” action on a best-effort basis when a completed video task response contains a syntactically valid temporary direct URL.
- Open the URL in a new browser tab so the upstream response can play or download normally.
- Support both users viewing their own task logs and administrators viewing other users' task logs.
- Preserve existing audio previews, failure details, desktop tables, and mobile cards.

## Non-goals

- Do not make temporary provider URLs permanent or refresh expired signatures.
- Do not change video proxy authentication or grant administrators access through `/v1/videos/:task_id/content`.
- Do not add an in-app video player, download manager, signed-link service, or backend API.
- Do not change task persistence or provider adapters.

## Design

### Frontend data contract

Update `TaskLog` so `result_url` is optional and `data` is `unknown`. The renderer must narrow `data` before reading it because providers return different JSON shapes and legacy rows may still contain a JSON string.

### URL resolution

For a successful video task, normalize a JSON-string `data` value when possible and select the first valid HTTP(S) string from this order:

1. `result_url`, but only when it is not the authenticated video-proxy form for this task
2. `data.url`
3. `data.video_url`
4. `data.metadata.url`
5. `data.metadata.origin_video_url`, retained for the provider response shape supplied with this request
6. A legacy HTTP(S) URL in `fail_reason`

`result_url` remains the canonical source when it is already a direct external result. The nested provider fields are intentionally accepted as temporary download locations when `result_url` points back to the authenticated proxy or is absent. Their signatures may expire after a short period; that is acceptable for this feature.

Detect the authenticated proxy by parsing each candidate against the current origin and comparing its normalized pathname to `/v1/videos/${encodeURIComponent(task_id)}/content`. Ignore the candidate host, query string, and one optional trailing slash so relative URLs, absolute URLs generated with `ServerAddress`, and signed/query variants are all recognized. The proxy form is excluded because a normal new-tab navigation does not carry the dashboard Bearer token, and administrators cannot use the owner-scoped proxy for another user's task.

Relative resolution against the current origin is used only to recognize and exclude authenticated proxy paths. A renderable direct link must be an absolute URL in the original candidate string whose own protocol is `http:` or `https:`. Other relative strings, invalid strings, unsupported schemes, arrays, malformed JSON, and unknown object shapes are ignored without throwing. This validation does not make a network request and cannot guarantee that a provider signature is still live; it only decides whether to render the temporary link returned by the API.

### Rendering and interaction

Keep the current task-log visual language and translated “Click to preview video” label. Render the link only when all of these conditions hold:

- task status is `SUCCESS`;
- task action is one of the existing video-generation actions;
- URL resolution returns a syntactically valid direct URL.

Open the resolved URL with `target="_blank"` and `rel="noopener noreferrer"`. The browser and upstream response decide whether the new tab plays the video or downloads it.

Failure tasks continue to display their failure reason. Successful video tasks without a valid direct URL display `-`, even if a legacy `fail_reason` contains non-URL text. Mobile task cards already reuse the same details cell, so they inherit the behavior without separate rendering logic.

### Internationalization and visual design

No new user-facing copy is required. Reuse the existing translation key and styling so this change belongs to the established usage-log table instead of introducing a new component or visual pattern.

## Security and privacy

- The feature only exposes a URL already present in the authenticated task-list response; it does not expand backend data access.
- Administrators receive other users' task data from the existing admin task endpoint, so they can use the same direct-link renderer.
- URL validation prevents `javascript:`, `data:`, and other unsupported schemes from becoming clickable links.
- `noopener noreferrer` prevents the opened page from controlling the dashboard tab or receiving its referrer.
- Provider URLs can contain temporary credentials. They remain visible only to users already authorized to read that task-log record.

## Tests

Add deterministic frontend regression tests in the usage-log component module before changing production code. Cover these observable behaviors:

- `SUCCESS` video task with empty `fail_reason` and `data.url` displays the preview link.
- An administrator table created with `useTaskLogsColumns(true)` and an admin-endpoint-shaped row for another user displays the same link.
- A direct external `result_url` wins when conflicting provider-response URLs are also present.
- `data.url`, `data.video_url`, and both metadata URL shapes resolve in order when earlier fields are absent or point to the authenticated proxy.
- Object-form and JSON-string-form `data` resolve identically, while malformed JSON does not throw.
- Relative and absolute authenticated proxy URLs, including query strings and trailing slashes, are not rendered as broken new-tab links when no direct provider URL exists.
- Failure tasks continue to show their failure reason and never show the preview link.
- Successful tasks with a non-URL `fail_reason` or without a valid HTTP(S) URL display `-`.
- The rendered link uses `target="_blank"` and `rel="noopener noreferrer"`.

After implementation, run the affected frontend test, TypeScript type checking, lint for changed files, and the production frontend build. No locale files or translation keys change.

## Expected files

- `web/src/features/usage-logs/types.ts`
- `web/src/features/usage-logs/components/columns/task-logs-columns.tsx`
- `web/src/features/usage-logs/components/__tests__/task-video-download.test.tsx`

No backend or locale-file changes are expected.
