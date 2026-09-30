import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import * as plugin from "./plugin.js";

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
  return { spec_version: 1, output_seconds: body.parameters.duration, seconds_kind: "exact", resolution: body.parameters.resolution, references };
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
    spec_version: 1, output_seconds: 12, seconds_kind: "exact", resolution: "1080p",
    references: { video: 1, image: 3, audio: 2 },
  });
  assert.deepEqual(plugin.describeSpec(ctx), submittedSpec(ctx));
  assert.throws(() => plugin.describeSpec({ model: "sd-2-fast", requestBody: intent.requestBody }), /sd-2-fast/);
});
