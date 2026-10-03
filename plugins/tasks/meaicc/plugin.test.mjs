import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import * as plugin from "./plugin.js";
assert.deepEqual(plugin.parseSubmitResponse({}, { statusCode: 200, body: { success: false, error: { code: "rejected" }, message: "content policy violation" } }), { rejected: { reason: "content policy violation" } });
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

// These same cases can also run in the official New API plugin test command.
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

test("Every declared paid model follows decode, reserve, submit and completion with one request", () => {
  for (const model of plugin.meta.models) {
    const seconds = model === "sd-2.5-c1" || model === "w3-c1" ? 30 : 10;
    const resolution = model === "mx-h3" ? "768p" : model === "w3-c1" ? "1080p" : "720p";
    const request = { model, input: { prompt: "一只猫在海边散步" }, parameters: { duration: seconds, resolution } };
    const intent = plugin.protocols.openai_video.decodeRequest({ model, body: { kind: "json", value: request } });
    const context = { model, upstreamModel: model, baseUrl: "https://api.meaicc.com", apiKey: "fixture-only-key", requestBody: intent.requestBody };
    assert.deepEqual(plugin.extractUsage(context), { requests: 1 }, model);
    assert.deepEqual(plugin.buildSubmitRequest(context).body, request, model);
    assert.deepEqual(plugin.extractUsageOnComplete({}, { status: "SUCCESS" }, { seconds }), { requests: 1 }, model);
  }
});

test("Customer aliases and extra options normalize before billing and forwarding", () => {
  const images = ["https://cdn.example/1.png", "https://cdn.example/2.png", "https://cdn.example/3.png"];
  const request = {
    model: "sd-2-c1", prompt: "横向 16:9，固定 15 秒\n@图1、@图2、@图3 保持人物一致",
    seconds: "15", duration: 15, resolution: "720p", aspect_ratio: "9:16",
    images, image_urls: images, generate_audio: true, client_option: { enabled: true },
  };
  const expected = {
    input: { prompt: request.prompt, media: images.map((url) => ({ type: "reference_image", url })) },
    parameters: { duration: 15, resolution: "720p", ratio: "9:16" },
  };
  const intent = plugin.protocols.openai_video.decodeRequest({ model: request.model, body: { kind: "json", value: request } });
  assert.deepEqual(intent.requestBody, expected);
  const context = { model: request.model, baseUrl: "https://api.meaicc.com", apiKey: "fixture-only-key", requestBody: request };
  assert.deepEqual(plugin.extractUsage(context), { requests: 1 });
  assert.deepEqual(plugin.buildSubmitRequest(context).body, { model: request.model, ...expected });
});

// The spec a submission is scheduled by, derived from the body actually sent upstream.
function submittedSpec(ctx) {
  const body = plugin.buildSubmitRequest({ ...ctx, baseUrl: "https://api.meaicc.com", apiKey: "fixture-only-key" }).body;
  const references = { video: 0, image: 0, audio: 0 };
  for (const item of body.input.media || []) {
    references[item.type === "reference_video" ? "video" : item.type === "reference_voice" ? "audio" : "image"] += 1;
  }
  return { spec_version: 2, reference_video_urls: (body.input.media || []).filter((item) => item.type === "reference_video").map((item) => item.url), output_seconds: body.parameters.duration, seconds_kind: "exact", resolution: body.parameters.resolution, references };
}

test("describeSpec agrees with the submitted body and rejects what usage rejects", () => {
  const decoded = fixture.cases.filter((entry) => (entry.path || []).includes("decodeRequest") && entry.expected?.requestBody);
  assert.ok(decoded.length > 0);
  for (const entry of decoded) {
    const ctx = { model: entry.expected.model, upstreamModel: entry.args[0].upstreamModel || entry.expected.model, requestBody: entry.expected.requestBody };
    let usageError;
    try { plugin.extractUsage(ctx); } catch (error) { usageError = error; }
    if (usageError) {
      assert.throws(() => plugin.describeSpec(ctx), { message: usageError.message }, entry.name);
      continue;
    }
    assert.deepEqual(plugin.describeSpec(ctx), submittedSpec(ctx), entry.name); // no credentials: the hook is read-only
  }
});

test("describeSpec classifies every media type into the three reference kinds", () => {
  const media = [
    { type: "first_frame", url: "https://cdn.example/f.png" }, { type: "last_frame", url: "https://cdn.example/l.png" },
    { type: "reference_image", url: "https://cdn.example/r.png" }, { type: "reference_video", url: "https://cdn.example/v.mp4" },
    { type: "reference_voice", url: "https://cdn.example/a.mp3" }, { type: "reference_voice", url: "https://cdn.example/b.mp3" },
  ];
  const request = { model: "w3-c1", input: { prompt: "一只猫", media }, parameters: { duration: 12, resolution: "1080p" } };
  const intent = plugin.protocols.openai_video.decodeRequest({ model: request.model, body: { kind: "json", value: request } });
  const ctx = { model: request.model, requestBody: intent.requestBody };
  assert.deepEqual(plugin.describeSpec(ctx), {
    spec_version: 2, reference_video_urls: ["https://cdn.example/v.mp4"], output_seconds: 12, seconds_kind: "exact", resolution: "1080p",
    references: { video: 1, image: 3, audio: 2 },
  });
  assert.deepEqual(plugin.describeSpec(ctx), submittedSpec(ctx));
  assert.throws(() => plugin.describeSpec({ model: "sd-2-fast", requestBody: intent.requestBody }), /sd-2-fast/);
});

const REQUEST = { model: "sd-2-c1", input: { prompt: "一只猫" }, parameters: { duration: 10, resolution: "720p" } };
const BASE_URL = "https://api.meaicc.com";

test("public media aliases retain every submitted reference in JSON and multipart", () => {
  for (const [fields, type, kind] of [
    [["image", "image_url", "reference_image_urls"], "reference_image", "image"],
    [["video_url", "input_video", "reference_video"], "reference_video", "video"],
    [["audio_url", "input_audio"], "reference_voice", "audio"],
  ]) {
    for (const field of fields) {
      for (const bodyKind of ["json", "multipart"]) {
        const value = { model: "sd-2-c4", prompt: "a cat", seconds: 5, resolution: "720p", [field]: "https://cdn.example/reference" };
        const body = bodyKind === "json" ? { kind: bodyKind, value } : { kind: bodyKind, fields: Object.fromEntries(Object.entries(value).map(([key, item]) => [key, [String(item)]])) };
        const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, body });
        const ctx = { model: value.model, requestBody: intent.requestBody, baseUrl: BASE_URL, apiKey: "fixture-only-key" };
        assert.deepEqual(plugin.buildSubmitRequest(ctx).body.input.media, [{ type, url: value[field] }], field);
        assert.deepEqual(plugin.describeSpec(ctx).references, { video: 0, image: 0, audio: 0, [kind]: 1 }, field);
        assert.deepEqual(plugin.describeSpec(ctx), submittedSpec(ctx), field);
      }
    }
  }
  for (const field of ["image", "video_url", "input_video", "audio_url"]) {
    const aliases = { image: "images", video_url: "videos", input_video: "videos", audio_url: "audios" };
    const value = { model: "sd-2-c4", prompt: "a cat", seconds: 5, resolution: "720p", [field]: "https://cdn.example/first", [aliases[field]]: ["https://cdn.example/second"] };
    assert.throws(() => plugin.protocols.openai_video.decodeRequest({ model: value.model, body: { kind: "json", value } }), /conflicting reference fields/, field);
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
    ["内容包含敏感词", "user"],
    ["The generated video may be related to copyright restrictions and has been blocked. Try adjusting your prompt or reference media.", "user"],
  ]) assert.equal(plugin.classifyFailure(reason), kind, reason);
});
