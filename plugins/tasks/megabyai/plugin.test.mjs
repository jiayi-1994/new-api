import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import * as plugin from "./plugin.js";
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
  const body = plugin.buildSubmitRequest({ ...ctx, baseUrl: "https://newapi.megabyai.cc", apiKey: "fixture-only-key" }).body;
  return {
    spec_version: 2, reference_video_urls: body.referenceVideos || [], output_seconds: body.duration, seconds_kind: "exact", resolution: body.resolution,
    references: { video: (body.referenceVideos || []).length, image: (body.referenceImages || []).length, audio: (body.referenceAudios || []).length },
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

test("describeSpec counts each reference kind from the final body, independent of surcharge_seconds", () => {
  const value = {
    model: "videos-fast", prompt: "a cat", seconds: "8", size: "1280x720",
    reference_images: ["https://cdn.example/1.png", "https://cdn.example/2.png"],
    videos: ["https://cdn.example/1.mp4"], referenceAudios: ["https://cdn.example/1.mp3"],
  };
  const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, body: { kind: "json", value } });
  const ctx = { model: value.model, requestBody: intent.requestBody };
  assert.deepEqual(plugin.describeSpec(ctx), {
    spec_version: 2, reference_video_urls: ["https://cdn.example/1.mp4"], output_seconds: 8, seconds_kind: "exact", resolution: "720p",
    references: { video: 1, image: 2, audio: 1 },
  });
  assert.deepEqual(plugin.describeSpec(ctx), submittedSpec(ctx));

  const plain = { model: value.model, requestBody: { prompt: "a cat", duration: 5, resolution: "4k" } };
  assert.deepEqual(plugin.describeSpec(plain).references, { video: 0, image: 0, audio: 0 });
  assert.throws(() => plugin.describeSpec({ model: value.model, requestBody: { prompt: "a cat", duration: 5 } }), /resolution/);
});

const REQUEST = { model: "videos-fast", prompt: "a cat", seconds: "8", size: "1280x720" };
const BASE_URL = "https://newapi.megabyai.cc";

test("one output is accepted in JSON and multipart without changing the submitted request", () => {
  for (const kind of ["json", "multipart"]) {
    const decode = (n) => {
      const value = { ...REQUEST };
      if (n !== undefined) value.n = n;
      const body = kind === "json" ? { kind, value } : { kind, fields: Object.fromEntries(Object.entries(value).map(([key, item]) => [key, [String(item)]])) };
      return plugin.protocols.openai_video.decodeRequest({ model: REQUEST.model, body });
    };
    assert.deepEqual(decode(1), decode(undefined), kind);
    for (const n of [0, 2, -1, 1.5, "invalid"]) assert.throws(() => decode(n), /n must be 1/, kind);
  }
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
    // Anonymized terminal messages from this provider's task logs, 2026-10-01.
    ["出于肖像保护考虑，未认证人脸暂不支持用 Dreamina Seedance 2.5 生成视频。你可以尝试换其它参考图或文生视频。", "user"],
    ["上游服务繁忙，请稍后重试", "upstream"],
    ["service is temporarily unavailable", "upstream"],
    ["video generation timed out", "upstream"],
  ]) assert.equal(plugin.classifyFailure(reason), kind, reason);
});
