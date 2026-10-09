import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import * as plugin from "./plugin.js";

const fixture = JSON.parse(readFileSync(new URL("./fixture.json", import.meta.url), "utf8"));
const catalog = JSON.parse(readFileSync(new URL("./model-catalog.json", import.meta.url), "utf8"));
const model = "sdas-mg-sd2.5-720p";
const base = { model, prompt: "a cat", duration: 8, resolution: "720p" };
const image = "https://cdn.example/image.png";
const video = "https://cdn.example/video.mp4";
const audio = "https://cdn.example/audio.mp3";

function context(value = base, upstreamModel = value.model, body) {
  const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, body: body || { kind: "json", value } });
  return { model: value.model, upstreamModel, requestBody: intent.requestBody, baseUrl: plugin.meta.baseUrl, apiKey: "fixture-only-key" };
}

for (const entry of fixture.cases) {
  test(entry.name, () => {
    let hook = plugin[entry.hook];
    for (const name of entry.path || []) hook = hook[name];
    if (entry.expectedError) assert.throws(() => hook(...entry.args), error => error.message.includes(entry.expectedError));
    else assert.deepEqual(hook(...entry.args), entry.expected);
  });
}

test("all published video models build requests with their own duration, resolution and billing unit", () => {
  const documented = catalog.models.filter(entry => entry.description);
  assert.deepEqual([...plugin.meta.models].sort(), documented.map(entry => entry.model_name).sort());
  for (const entry of documented) {
    const name = entry.model_name;
    const duration = name === "sdas-ll-sd2.5-pro-30s-720p" ? 30 : 15;
    const seconds = name === "sdas-pd-sd2.0-mini-903-720p" || name === "sdas-wf-sd2.0-mini-933-720p" ? 12 : duration;
    const ctx = context({ model: name, seconds });
    const wire = plugin.buildSubmitRequest(ctx).body;
    assert.equal(wire.model, name);
    assert.equal(wire.duration, seconds);
    assert.deepEqual(JSON.parse(wire.metadata.payload), { aspectRatio: "16:9", mode: "references" });
    assert.equal(wire.resolution, undefined);
    const tier = /-(480p|720p|1080p|2k|4k)$/.exec(name)?.[1] || "720p";
    const usage = plugin.extractUsage(ctx);
    assert.deepEqual(usage, entry.quota_type === 1 ? { requests: 1, resolution: tier } : { seconds, resolution: tier });
    const profile = plugin.meta.usageProfiles.find(profile => profile.models.includes(name));
    assert.deepEqual(Object.keys(profile.schema).sort(), Object.keys(usage).sort());
    const spec = plugin.describeSpec(ctx);
    if (tier === "2k") assert.deepEqual(spec, { unsupported: true });
    else {
      assert.equal(spec.resolution, tier);
      assert.equal(spec.output_seconds, seconds);
    }
  }
  for (const entry of catalog.excluded) {
    assert.throws(() => plugin.buildSubmitRequest(context({ model: entry.model, seconds: 8 })), /unsupported Sudashui model/);
  }
});

test("per-model duration boundaries and discrete/fixed products agree across quote, usage and submission", () => {
  const cases = [
    ["sdas-mg-sd2.5-720p", [4, 30], [3, 31]],
    ["sdas-mg-sd2.0-720p", [4, 15], [3, 16, 30]],
    ["sdas-pd-sd2.0-mini-903-720p", [5, 12], [4, 13]],
    ["sdas-wf-sd2.0-mini-933-720p", [4, 12], [3, 13]],
    ["sdas-pd-sd2.0-mini-903-480p", [5, 15], [4, 16]],
    ["sdas-hn-sd2.0-fast-720p", [5, 10, 15], [4, 6, 11]],
    ["sdas-hn-sd2.0-pro-933-720p", [15], [4, 14, 16]],
    ["sdas-xl-sd2.0-903-mini-480p", [15], [4, 14, 16]],
    ["sdas-ll-sd2.5-pro-30s-720p", [30], [4, 15, 29]],
  ];
  for (const [model, valid, invalid] of cases) {
    for (const seconds of valid) {
      const ctx = context({ model, seconds });
      assert.equal(plugin.describeSpec(ctx).output_seconds, seconds);
      assert.equal(plugin.buildSubmitRequest(ctx).body.duration, seconds);
      assert.ok(plugin.extractUsage(ctx));
    }
    for (const duration of invalid) {
      const ctx = { model, requestBody: { duration } };
      for (const hook of [plugin.describeSpec, plugin.extractUsage, plugin.buildSubmitRequest]) assert.throws(() => hook(ctx), /duration/, model);
    }
  }
});

test("every published media limit is enforced before quoting or submitting, including zero-video models", () => {
  for (const entry of catalog.models.filter(entry => entry.description)) {
    const name = entry.model_name;
    const seconds = name === "sdas-ll-sd2.5-pro-30s-720p" ? 30 : name.includes("mini-903-720p") || name.includes("mini-933-720p") ? 12 : 15;
    for (const [field, pattern, url] of [["images", /(\d+)图/, image], ["videos", /(\d+)视频/, video], ["audios", /(\d+)音频/, audio]]) {
      const max = Number(pattern.exec(entry.description)[1]);
      const value = { model: name, seconds, [field]: Array(max).fill(url) };
      const ctx = context(value);
      const wire = JSON.parse(plugin.buildSubmitRequest(ctx).body.metadata.payload);
      const upstreamField = { images: "imageUrls", videos: "videoUrls", audios: "audioUrls" }[field];
      assert.equal(wire[upstreamField].length, max, name);
      const invalid = context({ ...value, [field]: Array(max + 1).fill(url) });
      for (const hook of [plugin.describeSpec, plugin.extractUsage, plugin.buildSubmitRequest]) assert.throws(() => hook(invalid), /reference limits/, name);
    }
  }
});

test("metadata payload, OpenAI aliases and multipart normalize to the same wire and scheduling facts", () => {
  const payload = { aspectRatio: "9:16", mode: "references", imageUrls: [image], videoUrls: [video, video], audioUrls: [audio] };
  const native = context({ ...base, metadata: { payload: JSON.stringify(payload) } });
  const aliases = context({ ...base, aspect_ratio: "9:16", images: [image], reference_videos: [video, video], audios: [audio] });
  const multipart = context(base, model, { kind: "multipart", fields: {
    model: [model], duration: ["8"], size: ["720x1280"], "images[]": [image], "videos[]": [video, video], audios: [JSON.stringify([audio])],
  } });
  for (const ctx of [native, aliases, multipart]) {
    const wire = plugin.buildSubmitRequest(ctx).body;
    assert.deepEqual(JSON.parse(wire.metadata.payload), payload);
    assert.deepEqual(plugin.describeSpec(ctx), {
      spec_version: 2, output_seconds: 8, seconds_kind: "exact", resolution: "720p",
      references: { image: 1, video: 2, audio: 1 }, reference_video_urls: [video, video],
    });
    assert.deepEqual(plugin.extractUsage(ctx), { seconds: 8, resolution: "720p" });
  }
});

test("frames count both images and never merge with reference mode", () => {
  const ctx = context({ ...base, mode: "frames", firstFrameUrl: image, lastFrameUrl: image });
  assert.equal(plugin.describeSpec(ctx).references.image, 2);
  assert.deepEqual(JSON.parse(plugin.buildSubmitRequest(ctx).body.metadata.payload), { aspectRatio: "16:9", mode: "frames", firstFrameUrl: image, lastFrameUrl: image });
  for (const patch of [{ lastFrameUrl: undefined }, { images: [] }, { videos: [video] }, { mode: "references" }]) {
    assert.throws(() => context({ ...base, mode: "frames", firstFrameUrl: image, lastFrameUrl: image, ...patch }), /frames mode|references mode/);
  }
});

test("mapping preserves the public identity and rejects changed tiers or incompatible ordinary billing units", () => {
  const ctx = context({ ...base, model: "seedance-2.5" }, model);
  assert.equal(ctx.model, "seedance-2.5");
  assert.equal(plugin.buildSubmitRequest(ctx).body.model, model);
  const mixedUnits = context({ ...base, model: "sdas-qd-seedance-2.5-720p" }, model);
  for (const hook of [plugin.describeSpec, plugin.extractUsage, plugin.buildSubmitRequest]) {
    assert.throws(() => hook(mixedUnits), /billing unit/);
    assert.ok(hook({ ...mixedUnits, salesSource: "video_request" }));
    assert.throws(() => hook({ ...ctx, upstreamModel: "sdas-mg-sd2.5-1080p" }), /resolution conflicts/);
    assert.throws(() => hook({ ...ctx, upstreamModel: "unknown-model" }), /unsupported Sudashui model/);
  }
  for (const size of ["3840x2160", "2160p", "4k"]) {
    const large = context({ model: "sdas-gf-seedance-2.0-4k", seconds: 4, size });
    assert.equal(plugin.describeSpec(large).resolution, "4k");
    assert.equal(plugin.buildSubmitRequest(large).body.resolution, undefined);
  }
});

test("request conflicts, hidden multipliers and invalid values cannot bypass validation", () => {
  const invalid = [
    ...[0, -1, 3, 31, 8.5, null, false, {}, "NaN", "Infinity", 18446744073686646784].map(duration => ({ duration })),
    { seconds: 9 }, { n: 2 }, { n: null }, { size: "1920x1080" }, { size: "1280x720", aspectRatio: "9:16" },
    { images: [image], imageUrls: [image] }, { videos: [{ url: video }] }, { images: ["data:image/png;base64,a"] },
    { images: ["https://name:secret@cdn.example/image.png"] }, { videos: ["https://cdn.example/\nvideo.mp4"] },
    { metadata: { payload: {} } }, { metadata: { payload: "null" } }, { metadata: { payload: "not-json" } },
    { metadata: { payload: JSON.stringify({ duration: 99999 }) } }, { metadata: { payload: JSON.stringify({ n: 2 }) } },
    { metadata: { payload: "{}", duration: 99999 } }, { parameters: { duration: 99 } },
    { imageUrls: [image], metadata: { payload: JSON.stringify({ imageUrls: [image] }) } },
    { bypassCopyrightReferenceLevel: 0 }, { bypassCopyrightReferenceLevel: "0.7" },
  ];
  for (const patch of invalid) assert.throws(() => context({ ...base, ...patch }), JSON.stringify(patch));
  for (const duration of [NaN, Infinity, -Infinity]) assert.throws(() => context({ ...base, duration }));
  for (const body of [
    { kind: "multipart", files: [{ ref: "request_file:input_reference" }], fields: { model: [model] } },
    { kind: "multipart", fields: { model: [model], duration: ["4", "8"] } },
    { kind: "multipart", fields: { model: [model], duration: ["8"], images: [image], "images[]": [image] } },
  ]) assert.throws(() => context(base, model, body));
});

test("model-specific preprocessing, ratios and prompt limits stay consistent in driver hooks", () => {
  const valid = context({ ...base, model: "sdas-qd-seedance-2.0-720p", bypassCopyrightReferenceLevel: 0.1 });
  assert.equal(JSON.parse(plugin.buildSubmitRequest(valid).body.metadata.payload).bypassCopyrightReferenceLevel, 0.1);
  for (const upstreamModel of [model, "sdas-qd-seedance-2.0-no-face-720p"]) assert.throws(() => plugin.buildSubmitRequest({ ...valid, model: "public-model", upstreamModel }), /bypassCopyrightReferenceLevel/);
  assert.throws(() => plugin.describeSpec(context({ model: "sdas-hn-sd2.0-fast-720p", seconds: 5, aspectRatio: "1:1" })), /aspectRatio/);
  const prompt = "猫".repeat(15000);
  assert.ok(plugin.describeSpec(context({ model: "ld-sdas-2-cvk", seconds: 5, prompt })));
  assert.throws(() => plugin.describeSpec(context({ model: "ld-sdas-2-cvk", seconds: 5, prompt: prompt + "猫" })), /prompt exceeds/);
});

test("unknown, conflicting, wrong-task and error polling responses never masquerade as completion", () => {
  const cases = [
    {}, null, [], { code: "fail_to_fetch_task", data: null },
    { code: "unauthorized", data: { status: "SUCCESS", result_url: video } },
    { status: "new-status" }, { status: "toString" }, { status: "SUCCESS" },
    { status: "SUCCESS", result_url: "not a URL" },
    { task_id: "different", status: "SUCCESS", result_url: video },
    { status: "IN_PROGRESS", data: { state: "success" }, result_url: video },
  ];
  for (const body of cases) assert.equal(plugin.parseTaskResult({ taskId: "job" }, body).status, "UNKNOWN");
  for (const [status, state, expected] of [["SUBMITTED", "queueing", "QUEUED"], ["IN_PROGRESS", "processing", "IN_PROGRESS"], ["SUCCESS", "success", "SUCCESS"], ["FAILURE", "failed", "FAILURE"]]) {
    const body = { code: "success", data: { task_id: "job", status, result_url: video, data: { state } } };
    assert.equal(plugin.parseTaskResult({ taskId: "job" }, body).status, expected);
  }
});

test("submission ambiguity stays non-retryable, while accepted terminal failure has a task id", () => {
  for (const body of [{}, null, { id: 42 }, { id: "a", task_id: "b" }, { id: "a", error: { message: "quota" } }, { id: "a", code: "fail_to_fetch_task" }, { code: "fail_to_fetch_task", message: "unknown", data: null }]) {
    assert.throws(() => plugin.parseSubmitResponse({}, { body }));
  }
  const body = { id: "task", task_id: "task", status: "failed", fail_reason: "invalid image" };
  assert.deepEqual(plugin.parseSubmitResponse({}, { body }), { taskId: "task", taskData: body, immediate: { status: "FAILURE", reason: "invalid image" } });
  for (const evidence of [{ id: 0 }, { task_id: "accepted" }, { creations: [{ url: video }] }]) {
    const failure = { state: "failed", message: "invalid request", creations: [], ...evidence };
    assert.throws(() => plugin.parseSubmitResponse({}, { body: { code: "fail_to_fetch_task", message: JSON.stringify(failure), data: null } }), /ambiguous/);
  }
});

test("artifact fallback uses the creations or official URL, never forwards credentials or private task data", () => {
  for (const details of [{ creations: [{ url: video }] }, { volcesVideoUrl: video }]) {
    const data = { code: "success", data: { id: 12, task_id: "private-task", status: "SUCCESS", data: { state: "success", ...details }, credits: 999, username: "private-user" } };
    assert.deepEqual(plugin.buildContentRequest({ artifactKey: "video", data, apiKey: "secret", clientRequest: { method: "GET", headers: { Authorization: "secret" } } }), { url: video, method: "GET", credentialless: true });
    assert.deepEqual(plugin.protocols.openai_video.render({}, { status: "SUCCESS", data }), { url: video });
    assert.deepEqual(plugin.listArtifacts({ status: "FAILURE", data }), []);
  }
});

test("authentication is normalized and health attribution keeps infrastructure failures upstream", () => {
  for (const authHeader of ["fixture-only-key", "Bearer fixture-only-key"]) {
    const ctx = { ...context(), authHeader };
    assert.equal(plugin.buildSubmitRequest(ctx).headers.Authorization, "Bearer fixture-only-key");
    assert.equal(plugin.buildQueryRequest({ ...ctx, taskId: "id" }).headers.Authorization, "Bearer fixture-only-key");
  }
  for (const [reason, expected] of [
    ["invalid_request: invalid image", "user"], ["Real human faces are not supported.", "user"],
    ["content moderation rejected the prompt", "user"], ["内容违规", "user"], ["task cancelled by user", "cancelled"],
    ["invalid_request: moderation service unavailable", "upstream"], ["quota exhausted", "upstream"], ["unauthorized", "upstream"], ["unknown error", "upstream"],
  ]) assert.equal(plugin.classifyFailure(reason), expected, reason);
  for (const [message, expected] of [["duration must be from 4 to 15 seconds", "user"], ["moderation service unavailable", "upstream"]]) {
    const failed = plugin.parseTaskResult({}, { code: "success", data: { status: "FAILURE", data: { state: "failed", err_code: "invalid_request", message } } });
    assert.equal(plugin.classifyFailure(failed.reason), expected);
  }
});
