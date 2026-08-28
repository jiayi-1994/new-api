# Task 4 Implementation Report

## Status

DONE

Implementation commit: `ac83da19` (`feat(video): resolve provider pricing tiers`)

## Summary

- Gemini and Vertex share one Veo normalizer for payload and billing, with exact model/resolution capability entries, a 720p default, provider duration validation, and the required eight-second constraint for 1080p/4K.
- Ali uses the mapped upstream model and one strict final payload, merges equivalent size/resolution selectors, rejects conflicts, preserves authoritative aspect ratio, bounds metadata/polling duration, and keeps legacy ratio behavior intact.
- Doubao uses exact model capability entries and documented defaults, rejects unsupported tiers/durations, and applies only the same-tier `video_input` independent ratio while preserving the legacy ratio function.
- Hailuo and Vidu resolve from their final provider payloads, use only explicit documented resolution tiers/defaults, validate duration combinations, and capture only bounded authoritative polling duration.
- Relay tests prove resolver rejection, including Kling/Jimeng with no trustworthy resolution, stops before pre-consume and upstream request construction.

## RED Evidence

- Each provider package initially failed to compile because its `ResolveVideoBilling` implementation did not exist; Doubao also lacked `GetVideoInputIndependentRatio`.
- Ali regression tests observed Wan2.7 using the obsolete `size` protocol, accepting 480p, losing portrait legacy payloads, choosing top-level rather than metadata aspect ratio, and accepting an unsupported explicit ratio.
- Veo tests observed arbitrary bounded durations and 1080p/4K requests at unsupported four- and six-second durations being accepted.
- Vidu tests observed invalid Vidu 2.0 duration/resolution pairs and polling responses failing to capture authoritative duration.
- Doubao accepted undocumented model-name variants through substring capability matching.

## GREEN / Verification Evidence

- Brief focused command across relay and all six provider packages: exit 0; all packages `ok`.
- Full Gemini, Vertex, Ali, Doubao, Hailuo, and Vidu package tests: exit 0; all packages `ok`.
- `go test ./relay/... -count=1`: exit 0; all relay packages passed.
- `go build ./...`: exit 0.
- `git diff --check`: exit 0, no output.
- Final independent adversarial review: `APPROVED`, no findings or testing gaps.

## Unsupported Providers / Decisions

- Kling and Jimeng remain intentionally rejected before pre-consume/upstream because no trustworthy final resolution can be resolved.
- Suno remains on its existing non-video legacy path unchanged.
- Ali models without a reviewed omitted-resolution default remain rejected; only Wan2.7 text-to-video defaults to 1080p.
- Vidu 1.5 remains rejected because its resolution default/capabilities are not trusted by this task's reviewed contract.
- End-to-end settlement consumption of `TaskInfo.EffectiveDurationSeconds` is outside this adapter-focused task and remains the independent review's only residual integration risk.

## Follow-up Capability Correction

Fix commit: `c0d9f811` (`fix(video): enforce provider capability contracts`).

### RED Evidence

- Gemini Veo 3.0 accepted 720p at 4/6 seconds and 1080p portrait; Vertex rejected the same provider-valid 4/6-second tuples because both providers used one duration matrix.
- Hailuo accepted the invalid Cartesian product 1080p x 10 seconds for Hailuo 2.3, 2.3 Fast, and 02. A later independent protocol review also caught that 02 512p is I2V-only and 2.3 Fast is I2V-only; focused tests reproduced both leaks.
- Ali accepted model-wide durations/resolutions, including Wan2.2 Plus 720p x 10 seconds, and polling rejected integer-valued JSON floats instead of retaining a zero snapshot. Wan2.7 I2V also forwarded the T2V-only `ratio` selector.
- Vidu admitted unsupported action/model tuples such as Vidu 2.0 text-to-video. Reference requests used the correct endpoint but the wrong top-level `images` JSON field, and final metadata could remove required images after action selection.
- Vertex did not recognize the current Veo 3.1 GA model IDs.

### GREEN Decisions and Official Sources

- Gemini and Vertex still share normalization, but capability lookup is provider-aware. Gemini Veo 3.0 is fixed at 8 seconds and 1080p portrait is rejected; Vertex supports 720p/1080p, 4/6/8 seconds, and both documented aspects. Vertex also accepts current `veo-3.1-generate-001` and `veo-3.1-fast-generate-001`. Sources: [Gemini Veo](https://ai.google.dev/gemini-api/docs/veo), [Vertex Veo 3.1](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/models/veo/3-1-generate), and [Vertex release notes](https://docs.cloud.google.com/vertex-ai/docs/release-notes).
- Hailuo capabilities are keyed by final payload mode plus exact model/resolution/duration tuple: Hailuo 02 exposes 512p only with `first_frame_image`, 2.3 Fast is I2V-only, and 1080p is six seconds only. Sources: [MiniMax I2V](https://platform.minimax.io/docs/api-reference/video-generation-i2v), [MiniMax T2V](https://platform.minimax.io/docs/api-reference/video-generation-t2v), and [MiniMax S2V](https://platform.minimax.io/docs/api-reference/video-generation-s2v).
- Ali uses an exact seven-model table. Wan2.2 Flash uses the conservative 480p/720p cross-region intersection; unknown legacy-only models remain strict. Wan2.7 I2V rejects `ratio`. Polling accepts bounded positive integers from `usage.duration` (Wan2.2+) or `usage.video_duration` (Wan2.1), including integer-valued floats and numeric strings; invalid values leave zero without failing polling. Sources: [Wan T2V](https://help.aliyun.com/en/model-studio/text-to-video-api-reference), [Wan I2V](https://help.aliyun.com/en/model-studio/image-to-video-general-api-reference), and [legacy Wan I2V](https://help.aliyun.com/en/model-studio/legacy-image-to-video-api-reference/).
- Vidu capabilities are keyed by action/model/resolution/duration. Reference payloads now use `subjects` with named image groups, enforce the documented one-to-seven total image limit, and validate final payload inputs before billing. Sources: [Vidu T2V](https://platform.vidu.cn/docs/text-to-video.md), [Vidu I2V](https://platform.vidu.cn/docs/image-to-video.md), [Vidu start/end](https://platform.vidu.cn/docs/start-end-to-video.md), and [Vidu reference](https://platform.vidu.com/docs/reference-to-video).
- Doubao was re-audited: the resolver capability lookup and scenario rules use exact reviewed model keys, with no substring widening. Legacy `GetVideoInputRatio` remains unchanged.

### Follow-up Verification

- Every correction was preceded by a failing focused test and then passed its focused GREEN run.
- Brief-focused command across relay and all six provider packages: exit 0.
- Full Gemini, Vertex, Ali, Doubao, Hailuo, and Vidu packages: exit 0.
- Full `go test ./relay/... -count=1`: exit 0.
- `go build . ./relay` plus all six affected provider packages: exit 0. The monolithic `go build ./...` produced no diagnostics but was safely interrupted after 60 seconds twice on Windows as directed; it is not claimed as passed.
- Final `git diff --check`: exit 0.
- Final independent official-protocol review: no remaining P1/P2.

### Lifecycle Concern

- Official lifecycle pages mark Gemini/Vertex Veo 3.0 IDs and Vertex 3.1 preview IDs as retired by the current date. Current Vertex 3.1 GA IDs were added and only those live IDs are advertised by Vertex. Historical Vertex entries remain resolver-compatible for explicit tuple regression tests but are not advertised. The second review correction below makes Gemini stricter by removing its retired 3.0 production capabilities entirely.

## Second Review Correction

Fix commit: `915ccf57` (`fix(video): reject retired Gemini and normalize Vidu references`).

### RED Evidence

- Vidu q2 flat image arrays with one or two images were inferred as image-to-video/start-end and rejected despite q2 supporting those inputs through reference-to-video. Four and seven images were placed into one subject, exceeding the provider's three-image per-subject maximum. Focused provider and relay tests reproduced both failures.
- Metadata could inject a single Vidu subject containing four images while remaining under the seven-image total, so the resolver allowed pre-consume before the upstream rejected the subject.
- Gemini still advertised both retired Veo 3.0 IDs and accepted a valid historical 720p x 8-second tuple through production billing/build. Relay integration showed pre-consume, payload build, and upstream invocation all occurred.

### GREEN Decisions

- For mapped `viduq2`, an implicit flat image array selects the supported reference action. Explicit supported action semantics remain authoritative. Flat images are split in stable input order into named subjects of at most three images (`3+3+1` for seven), with no dropped images; the URL consumes the normalized action.
- Vidu final payload validation enforces one-to-three images per subject and one-to-seven images total. Provider tests cover flat counts 1/2/3/4/7, explicit reference, total eight, and per-subject overflow. Relay tests assert decoded subjects plus pre-consume/build/upstream counts for every successful count and zero counts for both invalid limits.
- Gemini no longer advertises or resolves `veo-3.0-generate-001` or `veo-3.0-fast-generate-001`. Every advertised Gemini model is exercised through production capability resolution, and both retired mappings return HTTP 400 before pre-consume/build/upstream. Legacy `EstimateBilling` remains unchanged.
- Sources: [Vidu Reference-to-Video](https://platform.vidu.com/docs/reference-to-video) and [Gemini deprecations](https://ai.google.dev/gemini-api/docs/deprecations).

### Second Review Verification

- Vidu and Gemini RED tests failed on the reported behavior before implementation and passed after the minimal fixes.
- Full Gemini and Vidu packages: exit 0.
- Task 4 focused command across relay and all six providers: exit 0.
- `go test ./relay/... -count=1`: exit 0.
- `go build . ./relay ./relay/channel/task/gemini ./relay/channel/task/vidu`: exit 0.
- `git diff --check`: exit 0.
- Independent official-protocol review: no remaining P1/P2.
