import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import * as plugin from "./plugin.js";
assert.deepEqual(plugin.parseSubmitResponse({}, { statusCode: 200, body: { success: false, error: null, message: "content policy violation" } }), { rejected: { reason: "content policy violation" } });
// Explicit rejection is distinct from an unreadable/ambiguous accepted response.
for (const reason of ["content policy violation", "quota exhausted"]) {
  const result = plugin.parseSubmitResponse({}, { statusCode: 200, body: { success: false, error: { message: reason } } });
  assert.deepEqual(result, { rejected: { reason } });
  assert.equal(plugin.classifyFailure(result.rejected.reason), reason === "content policy violation" ? "user" : "upstream");
}
for (const body of [
  {}, "not JSON",
  { id: "created", error: { message: "content policy violation" } },
  { id: 123, error: { message: "quota exhausted" } },
  { task_id: " ", id: "created", error: { message: "quota exhausted" } },
  { id: " ", task_id: "created", error: { message: "quota exhausted" } },
  { status: "completed", error: { message: "quota exhausted" } },
  { status: "processing", success: false, message: "quota exhausted" },
]) {
  assert.throws(() => plugin.parseSubmitResponse({}, { statusCode: 200, body }));
}

const fixture = JSON.parse(readFileSync(new URL("./fixture.json", import.meta.url), "utf8"));

test("one output is accepted in JSON and multipart without changing the submitted request", () => {
  const request = { model: "videos-fast", prompt: "a cat", seconds: 8, resolution: "720p" };
  for (const kind of ["json", "multipart"]) {
    const decode = (n) => {
      const value = { ...request };
      if (n !== undefined) value.n = n;
      const body = kind === "json" ? { kind, value } : { kind, fields: Object.fromEntries(Object.entries(value).map(([key, item]) => [key, [String(item)]])) };
      return plugin.protocols.openai_video.decodeRequest({ model: request.model, body });
    };
    assert.deepEqual(decode(1), decode(undefined), kind);
    for (const n of [0, 2, -1, 1.5, "invalid"]) assert.throws(() => decode(n), /n must be 1/, kind);
  }
});

for (const entry of fixture.cases) {
  test(entry.name, () => {
    let hook = plugin[entry.hook];
    for (const name of entry.path || []) hook = hook[name];
    if (entry.expectedError !== undefined) {
      assert.throws(() => hook(...entry.args), (error) => error.message.includes(entry.expectedError));
    } else {
      assert.deepEqual(hook(...entry.args), entry.expected);
    }
  });
}

// The spec a submission is scheduled by, derived from the body actually sent upstream.
function submittedSpec(ctx) {
  const body = plugin.buildSubmitRequest({ ...ctx, baseUrl: "https://api.hjmie.cc.cd", apiKey: "fixture-only-key" }).body;
  return {
    spec_version: 2, reference_video_urls: body.videos || [], output_seconds: body.duration, seconds_kind: "exact", resolution: body.resolution,
    references: { video: (body.videos || []).length, image: (body.images || []).length, audio: (body.audios || []).length },
  };
}

test("describeSpec agrees with the submitted body and reserved usage for every decoded fixture", () => {
  const decoded = fixture.cases.filter((entry) => (entry.path || []).includes("decodeRequest") && entry.expected?.requestBody);
  assert.ok(decoded.length > 0);
  for (const entry of decoded) {
    const ctx = { model: entry.expected.model, upstreamModel: entry.expected.model, requestBody: entry.expected.requestBody };
    const spec = plugin.describeSpec(ctx); // no credentials: the hook is read-only
    assert.deepEqual(spec, submittedSpec(ctx), entry.name);
    const usage = plugin.extractUsage(ctx);
    assert.equal(spec.output_seconds, usage.seconds, entry.name);
    assert.equal(spec.resolution, usage.resolution, entry.name);
  }
});

test("describeSpec counts each reference kind from the final body", () => {
  const value = {
    model: "videos-fast", prompt: "a cat", duration: 8, resolution: "2160p",
    referenceImages: ["https://cdn.example/1.png", "https://cdn.example/2.png"],
    reference_videos: ["https://cdn.example/1.mp4"], audios: ["https://cdn.example/1.mp3"],
  };
  const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, body: { kind: "json", value } });
  const ctx = { model: value.model, upstreamModel: value.model, requestBody: intent.requestBody };
  assert.deepEqual(plugin.describeSpec(ctx), {
    spec_version: 2, reference_video_urls: ["https://cdn.example/1.mp4"], output_seconds: 8, seconds_kind: "exact", resolution: "4k",
    references: { video: 1, image: 2, audio: 1 },
  });
  assert.deepEqual(plugin.describeSpec(ctx), submittedSpec(ctx));

  const plain = { model: value.model, requestBody: { prompt: "a cat", duration: 5, resolution: "480p" } };
  assert.deepEqual(plugin.describeSpec(plain).references, { video: 0, image: 0, audio: 0 });
  assert.throws(() => plugin.describeSpec({ model: value.model, requestBody: { prompt: "a cat", resolution: "480p" } }), /duration/);
});

test("describeSpec and submission apply the same mapped-model validation", () => {
  const value = { model: "videos-fast", prompt: "a cat", duration: 8, resolution: "720p" };
  const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, body: { kind: "json", value } });
  for (const identity of [
    { model: value.model, upstreamModel: "unsupported-mapped-model" },
    { model: "unsupported-mapped-model" },
  ]) {
    const ctx = { ...identity, requestBody: intent.requestBody, usagePurpose: "spec" };
    const expected = { message: "unsupported upstream video model: unsupported-mapped-model" };
    assert.throws(() => submittedSpec(ctx), expected);
    assert.throws(() => plugin.describeSpec(ctx), expected);
  }

  for (const identity of [
    { model: value.model, upstreamModel: "videos-standard" },
    { model: "client-alias", upstreamModel: "videos-standard" },
  ]) {
    const ctx = { ...identity, requestBody: intent.requestBody, usagePurpose: "spec" };
    assert.deepEqual(plugin.describeSpec(ctx), submittedSpec(ctx));
  }
});

test("a public alias decodes before channel selection and submits the selected real model", () => {
  const value = { model: "video-unified", prompt: "a cat", seconds: 8, size: "1280x720" };
  for (const upstreamModel of [undefined, "videos-standard"]) {
    const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, upstreamModel, body: { kind: "json", value } });
    assert.equal(intent.model, value.model);
    const ctx = { model: value.model, upstreamModel: "videos-standard", requestBody: intent.requestBody };
    assert.deepEqual(plugin.describeSpec(ctx), submittedSpec(ctx));
    const submitted = plugin.buildSubmitRequest({ ...ctx, baseUrl: "https://api.hjmie.cc.cd", apiKey: "fixture-only-key" });
    assert.equal(submitted.body.model, "videos-standard");
    assert.equal(submitted.body.duration, 8);
    assert.equal(submitted.body.resolution, "720p");
  }
  assert.throws(() => plugin.protocols.openai_video.decodeRequest({ model: value.model, upstreamModel: "unknown", body: { kind: "json", value } }), /unsupported upstream video model/);
});

const REQUEST = { model: "videos-fast", prompt: "a cat", duration: 8, resolution: "720p" };
const BASE_URL = "https://poxiaoapi001.com";

test("the Po Xiao public model submits all three public reference types without a legacy mapping", () => {
  const value = {
    model: "seedance-2.0", prompt: "一只猫在海边奔跑", seconds: 5, resolution: "720p", ratio: "16:9",
    images: ["https://cdn.example/image.png"], videos: ["https://cdn.example/video.mp4"],
    audios: ["https://cdn.example/audio.mp3"],
  };
  const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, upstreamModel: value.model, body: { kind: "json", value } });
  assert.equal(intent.model, value.model);
  assert.equal(intent.action, "reference_to_video");
  const ctx = { model: value.model, upstreamModel: value.model, requestBody: intent.requestBody };
  const request = plugin.buildSubmitRequest({ ...ctx, baseUrl: BASE_URL, apiKey: "fixture-only-key" });
  assert.deepEqual(request, {
    url: BASE_URL + "/v1/videos", method: "POST",
    headers: { Authorization: "Bearer fixture-only-key", "Content-Type": "application/json" },
    body: {
      model: value.model, prompt: value.prompt, duration: 5, resolution: "720p", ratio: "16:9",
      images: value.images, videos: value.videos, audios: value.audios,
    },
  });
  assert.deepEqual(plugin.describeSpec(ctx), {
    spec_version: 2, reference_video_urls: value.videos, output_seconds: 5, seconds_kind: "exact", resolution: "720p",
    references: { video: 1, image: 1, audio: 1 },
  });
  assert.deepEqual(plugin.extractUsage(ctx), { seconds: 5, resolution: "720p" });
});

test("a unified gateway response retains public output facts and uses authenticated canonical content", () => {
  const accepted = {
    id: "task_poxiao", object: "video", model: "seedance-2.0", status: "queued", progress: 0,
    seconds: "5", metadata: { resolution: "720p" },
  };
  assert.deepEqual(plugin.parseSubmitResponse({}, { statusCode: 200, body: accepted }), {
    taskId: "task_poxiao", taskData: accepted,
  });
  const completed = {
    ...accepted, status: "completed", progress: 100,
    metadata: { resolution: "720p", url: "/v1/videos/task_poxiao/content" },
  };
  const result = plugin.parseTaskResult({}, completed);
  assert.deepEqual(result, { status: "SUCCESS", progress: "100%" });
  assert.deepEqual(plugin.extractUsageOnComplete({}, result, completed), { seconds: 5, resolution: "720p" });
  const task = { task_id: "task_downstream", status: "SUCCESS", data: completed };
  assert.deepEqual(plugin.protocols.openai_video.render({}, task), { seconds: "5", resolution: "720p" });
  assert.deepEqual(plugin.listArtifacts(task), [{ key: "video", type: "video", mimeType: "video/mp4" }]);
  for (const method of ["GET", "HEAD"]) {
    for (const url of ["/v1/videos/task_poxiao/content", "https://untrusted.example/content"]) {
      assert.deepEqual(plugin.buildContentRequest({
        baseUrl: BASE_URL, apiKey: "fixture-only-key", upstreamTaskId: accepted.id, artifactKey: "video",
        clientRequest: { method, headers: {} }, data: { ...completed, metadata: { ...completed.metadata, url } },
      }), {
        url: BASE_URL + "/v1/videos/task_poxiao/content", method,
        headers: { Authorization: "Bearer fixture-only-key" },
      });
    }
  }
});

test("terminal create responses retain their task identity and complete immediately", () => {
  for (const [body, immediate] of [
    [{ id: "task_failed", status: "failed", progress: 100, error: { code: "video_generation_failed", message: "Video generation failed" } },
      { status: "FAILURE", progress: "100%", reason: "Video generation failed" }],
    [{ task_id: "task_rejected", status: "failed", error: { code: "video_request_rejected", message: "Video generation failed" } },
      { status: "FAILURE", reason: "video request rejected" }],
    [{ id: "task_cancelled", status: "failed", error: { code: "video_generation_cancelled", message: "Video generation failed" } },
      { status: "FAILURE", reason: "cancelled: video generation cancelled" }],
    [{ id: "task_completed", status: "completed", progress: 100, seconds: "5", metadata: { resolution: "720p" } },
      { status: "SUCCESS", progress: "100%" }],
  ]) {
    assert.deepEqual(plugin.parseSubmitResponse({}, { statusCode: 200, body }), {
      taskId: body.task_id || body.id, taskData: body, immediate,
    });
    assert.deepEqual(plugin.parseTaskResult({}, body), immediate);
  }
});

test("a definite failure without a task id is rejected while ambiguous creates still throw", () => {
  for (const [body, reason] of [
    [{ status: "failed", fail_reason: "content policy violation" }, "content policy violation"],
    [{ status: "failed", error: { code: "video_request_rejected", message: "Video generation failed" } }, "video request rejected"],
    [{ status: "cancelled" }, "cancelled: video generation cancelled"],
  ]) {
    assert.deepEqual(plugin.parseSubmitResponse({}, { statusCode: 200, body }), { rejected: { reason } });
  }
  for (const body of [
    { status: "completed" },
    { id: "task_unknown", status: "unknown", error: { message: "Video generation failed" } },
    { id: "task_completed", status: "completed", error: { code: "video_request_rejected", message: "Video generation failed" } },
    { id: "task_processing", status: "processing", success: false },
    { status: "processing", error: { code: "video_request_rejected", message: "Video generation failed" } },
    { id: 42, status: "failed", error: { message: "Video generation failed" } },
  ]) assert.throws(() => plugin.parseSubmitResponse({}, { statusCode: 200, body }), JSON.stringify(body));
});

test("public failure codes preserve safe attribution in both creation and polling", () => {
  for (const [code, message, reason, classification] of [
    ["video_request_rejected", "upstream quota exhausted: private detail", "video request rejected", "user"],
    ["video_generation_cancelled", "upstream internal error: private detail", "cancelled: video generation cancelled", "cancelled"],
    ["video_generation_failed", "Video generation failed", "Video generation failed", "upstream"],
    ["unknown_code", "Video generation failed", "Video generation failed", "upstream"],
    ["VIDEO_REQUEST_REJECTED", "Video generation failed", "Video generation failed", "upstream"],
    [["video_request_rejected"], "Video generation failed", "Video generation failed", "upstream"],
  ]) {
    const body = { id: "task_failed", status: "failed", error: { code, message } };
    const expected = { status: "FAILURE", reason };
    assert.deepEqual(plugin.parseTaskResult({}, body), expected);
    assert.deepEqual(plugin.parseSubmitResponse({}, { statusCode: 200, body }).immediate, expected);
    assert.equal(plugin.classifyFailure(reason), classification);
  }
  for (const [status, expected] of [["processing", "IN_PROGRESS"], ["completed", "SUCCESS"]]) {
    for (const code of ["video_request_rejected", "video_generation_cancelled"]) {
      assert.deepEqual(plugin.parseTaskResult({}, { status, error: { code, message: "Video generation failed" } }), { status: expected });
    }
  }
});

test("completion resolution metadata is bounded and does not replace explicit legacy facts", () => {
  for (const [data, expected] of [
    [{ seconds: "5", metadata: { resolution: "2160p" } }, { seconds: 5, resolution: "4k" }],
    [{ seconds: "5", metadata: { resolution: "1440p" } }, { seconds: 5 }],
    [{ seconds: "999999999999999999999", metadata: { resolution: "720p" } }, { resolution: "720p" }],
    [{ resolution: "1080p", metadata: { resolution: "720p" } }, { resolution: "1080p" }],
    [{ size: "1920x1080", metadata: { resolution: "720p" } }, { resolution: "1080p" }],
    [{ resolution: "invalid", metadata: { resolution: "720p" } }, {}],
    [{ metadata: ["720p"] }, {}],
  ]) {
    assert.deepEqual(plugin.extractUsageOnComplete({}, { status: "SUCCESS" }, data), expected);
  }
  assert.deepEqual(plugin.protocols.openai_video.render({}, {
    status: "SUCCESS", data: { seconds: "5", metadata: { resolution: "1440p", url: "/private-upstream-path" } },
  }), { seconds: "5" });
});

test("A raw channel key and a resolved Bearer header both reach upstream as one Bearer header", () => {
  const intent = plugin.protocols.openai_video.decodeRequest({ model: REQUEST.model, body: { kind: "json", value: REQUEST } });
  const ctx = { model: REQUEST.model, upstreamModel: REQUEST.model, baseUrl: BASE_URL, requestBody: intent.requestBody, taskId: "task-1" };
  // Vendor channels receive the raw key as authHeader; New API channels receive "Bearer <key>".
  for (const authHeader of ["fixture-only-key", "Bearer fixture-only-key"]) {
    const credentials = { authHeader, apiKey: "fixture-only-key" };
    assert.equal(plugin.buildSubmitRequest({ ...ctx, ...credentials }).headers.Authorization, "Bearer fixture-only-key", authHeader);
    assert.equal(plugin.buildQueryRequest({ ...ctx, ...credentials }).headers.Authorization, "Bearer fixture-only-key", authHeader);
  }
});

test("A cancelled upstream task is attributed as cancelled, not as a channel failure", () => {
  for (const body of [{ status: "cancelled" }, { status: "CANCELED" }, { status: "cancelled", error: "quota exhausted" }]) {
    const result = plugin.parseTaskResult({}, body);
    assert.equal(result.status, "FAILURE", JSON.stringify(body));
    assert.match(result.reason, /^cancelled: /, JSON.stringify(body));
    assert.equal(plugin.classifyFailure(result.reason), "cancelled", JSON.stringify(body));
  }
  assert.equal(plugin.classifyFailure(plugin.parseTaskResult({}, { status: "failed" }).reason), "upstream");
  const failed = plugin.parseTaskResult({}, { status: "failed", error: "job cancelled by provider due to internal error" });
  assert.equal(plugin.classifyFailure(failed.reason), "upstream");
});

test("Provider-side cancellations and constraint violations count against the upstream", () => {
  for (const [reason, kind] of [
    ["job cancelled by provider due to internal error", "upstream"],
    ["constraint violation", "upstream"],
    ["content policy violation", "user"],
    ["moderation service timeout", "upstream"],
    ["content policy validation internal error", "upstream"],
    ["sensitive data service unavailable", "upstream"],
    ["unsupported video backend", "upstream"],
    ["invalid input", "upstream"],
    ["审核服务超时", "upstream"],
    ["moderation rejected", "user"],
    ["提示词包含敏感违规内容或参数格式错误", "user"],
  ]) assert.equal(plugin.classifyFailure(reason), kind, reason);
});
