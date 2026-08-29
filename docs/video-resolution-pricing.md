# Video resolution pricing — operator guide

Video tasks are billed **per second**, from a price chosen by the model and the
**effective output resolution** of the request. There is no fallback: if the
resolution a request resolves to has no configured price, the request fails with
HTTP 400 `video_resolution_not_supported` **before** any quota is pre-consumed
and before anything is sent upstream.

That strictness is deliberate — it makes a misconfiguration fail loudly instead
of silently billing at the wrong rate — but it means **an existing video model
stops working the moment you upgrade, until you configure its prices**.

## Configuring prices

Set the `VideoResolutionPrice` option (System Settings → Model Pricing → the
`Video resolution` tab, or the raw JSON field). It is a nested map of
`model → resolution → USD price per second`:

```json
{
  "wan2.7-t2v": { "480p": 0.02, "720p": 0.05, "1080p": 0.10 },
  "sora-2":     { "720p": 0.10 }
}
```

`VideoResolutionPrice` is completely independent of the legacy
`TaskBillingMode` option. Resolution pricing is always per-second; it never
reads `TaskBillingMode`, and saving it never writes one.

### Key format vs. provider validity — read this before configuring

A key is accepted by validation if it matches `^[1-9]\d{2,4}p$` or
`^[1-9]\d*k$`. So `1440p`, `2k` and `2160p` all *save* successfully.

**Saving successfully does not mean the key will ever be used.** Each provider
adaptor derives the effective resolution from the request payload it actually
sends upstream, and it can only ever produce the tiers in the table below. A
price configured for a tier a provider never produces is dead configuration; a
tier the provider *does* produce but you did **not** price makes every such
request 400.

> The 400 message names the resolution the **request** resolved to, not the one
> you configured. If you see `video resolution 1080p is not configured for
> model X` while your config says `1440p`, the config key is the problem.

## Which resolutions each provider can actually produce

| Platform | Producible tiers | Per-model defaults |
| --- | --- | --- |
| **sora** | `720p`, `1024p` | `720p` / 4 s. `sora-2` is capped at `720p` — a `1024p` price for `sora-2` is dead config. The high tier is `1024p`, **not** `1080p`. |
| **gemini** | `720p`, `1080p`, `4k` | `720p` / 8 s. Only `veo-3.1-generate-preview` and `veo-3.1-fast-generate-preview` are supported. `4k` is reachable **only** on these two, and only at 8 s. |
| **vertex** | `720p`, `1080p` | `720p` / 8 s. Only `veo-3.1-generate-001` and `veo-3.1-fast-generate-001`. **Vertex never produces `4k`.** |
| **ali** | `480p`, `720p`, `1080p` | `1080p`: `wan2.7-t2v`, `wan2.7-i2v`, `wan2.5-i2v-preview`, `wan2.2-i2v-plus` · `720p`: `wan2.2-i2v-flash`, `wanx2.1-i2v-plus`, `wanx2.1-i2v-turbo` |
| **doubao** | `480p`, `720p`, `1080p` | `1080p`: `doubao-seedance-1-0-pro-250528`, `doubao-seedance-1-0-pro-fast-250528` · `720p`: all others. `480p` is never a default — the client must ask for it. |
| **hailuo** | `512p`, `720p`, `768p`, `1080p` | `768p`: `MiniMax-Hailuo-2.3`, `MiniMax-Hailuo-2.3-Fast`, `MiniMax-Hailuo-02` · `720p`: `T2V-01`, `T2V-01-Director`, `I2V-01`, `I2V-01-Director`, `I2V-01-live`. `512p` only on `MiniMax-Hailuo-02` image-to-video. |
| **vidu** | `360p`, `540p`, `720p`, `1080p` | `viduq1` = `1080p` · `viduq2` = `720p` · `vidu2.0` = `360p` at 4 s, `720p` at 8 s |
| **kling**, **jimeng** | — | **Not supported.** Every video request returns 400 and no price configuration changes that. |
| **suno** | — | Not a video platform; unchanged legacy per-call billing. |

The complete set of tiers any adaptor can produce is:
`360p`, `480p`, `512p`, `540p`, `720p`, `768p`, `1024p`, `1080p`, `4k`.

**`1440p`, `2k` and `2160p` are never produced by any adaptor.** 4K is keyed
`4k`, not `2160p`. Ali maps the square `1440*1440` size to **`1080p`**, which
matches Alibaba's own tier table — 1440×1440 and 1920×1080 are the same pixel
budget.

## Known model gaps after the upgrade

These are separate from pricing — no configuration re-enables them:

- **Kling and Jimeng** have no resolution resolver, so all their video requests
  return 400.
- **Hailuo `S2V-01`** is advertised but has no documented resolution or
  duration upstream, so it cannot be billed per second and returns 400.
- **`MiniMax-Hailuo-2.3-Fast` text-to-video** returns 400. This one is correct —
  MiniMax only documents that model for image-to-video.
- **Gemini/Vertex `veo-3.0-*` and `veo-2.0-*`** are still offered when adding a
  channel and still ship default model ratios, but the video path only supports
  the Veo 3.1 models listed above, so they return 400.

## Timeouts and stuck charges

A resolution task reserves quota before it submits upstream. If the process
dies between the reservation and the task row, a sweep in the polling loop
refunds the reservation after `TaskReservationOrphanGrace` (15 minutes).

That sweep infers "the request is dead" from the row's age, so the submit
itself must have a bound — otherwise a request still waiting on a slow upstream
would be refunded while it is alive, and the task it later creates would be
untracked. `RELAY_TIMEOUT` defaults to `0` (no timeout), so resolution-priced
submissions are bounded separately by **`TASK_SUBMIT_TIMEOUT`** (seconds,
default `300`). The orphan grace is derived from it — `TASK_SUBMIT_TIMEOUT + 10
minutes`, never below 15 minutes — so the two cannot drift apart.

### The unavoidable trade-off

A submit timeout is **ambiguous**: the upstream may have accepted the task and
will bill us for it, but we never got the task id, so we refund the user and
have no local record. The provider charges us; the user pays nothing.

No timeout value removes this — it only moves it:

- **Shorter** bound: we give up sooner on a slow-but-healthy upstream, so this
  happens more often.
- **Longer** bound: fewer false give-ups, but a genuinely stuck submit holds the
  user's pre-consumed quota longer before the sweep releases it.

Raise `TASK_SUBMIT_TIMEOUT` if your providers are slow to acknowledge
submissions. Task submit endpoints normally return a task id in seconds, so the
300 s default is already ~10× headroom; treat a timeout as a real upstream
fault, not as normal slowness.

Every ambiguous timeout is logged for reconciliation:

```
resolution task submit timed out after 5m0s; upstream may have accepted and
billed us (request_id=... model=... upstream_model=... channel=...) —
reconcile against the provider console
```

Also watch for these; both mean a charge needed manual attention:

- `orphaned resolution reservation ... could not be refunded`
- `resolution reservation ... refunded funding but its token ... no longer exists`

## Upgrade checklist

1. List the video models your channels actually serve.
2. For each, find its row above and note which tiers it can produce.
3. Configure a price for **every** tier your users can reach — including the
   model's default tier, which is what a request with no explicit resolution
   will use.
4. Deploy, then send one request per model at its default resolution and
   confirm it is not rejected.
5. Check the consumption log: a resolution-priced task records
   `admin_info.video_resolution_billing` with the selected tier, the per-second
   price and the duration used.
