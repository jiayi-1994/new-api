import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import * as plugin from './plugin.js';
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

const fixture = JSON.parse(readFileSync(new URL('./fixture.json', import.meta.url), 'utf8'));
const catalog = JSON.parse(readFileSync(new URL('./pricing-reference.json', import.meta.url), 'utf8').replace(/^\uFEFF/, ''));
for (const entry of fixture.cases) {
  test(entry.name, () => {
    let hook = plugin[entry.hook];
    for (const member of entry.path || []) hook = hook[member];
    if (entry.expectedError !== undefined) {
      assert.throws(() => hook(...entry.args), error => error.message.includes(entry.expectedError));
    } else assert.deepEqual(hook(...entry.args), entry.expected);
  });
}

test('Every enabled model has exactly its source billing unit, including conflicting prose', () => {
  const expected = catalog.models.filter(row => ['request', 'second'].includes(row.price_unit));
  assert.deepEqual([...plugin.meta.models].sort(), expected.map(row => row.model_name).sort());
  for (const row of expected) {
    const profile = plugin.meta.usageProfiles.find(profile => profile.models.includes(row.model_name));
    const schema = profile?.schema || plugin.meta.usageSchema;
    const key = row.price_unit === 'second' ? 'seconds' : 'requests';
    assert.deepEqual(Object.keys(schema), [key], row.model_name);
    assert.equal(schema[key].unit, row.price_unit === 'second' ? 'second' : 'count', row.model_name);
    for (const unit of Object.values(row.price_unit_by_group)) assert.equal(unit, row.price_unit);
  }
});

test('Decode, reserve, submit and completion agree for every enabled model', () => {
  for (const entry of fixture.cases.filter(entry => entry.name.endsWith(' decode'))) {
    const intent = plugin.protocols.openai_video.decodeRequest(entry.args[0]);
    const ctx = { model: intent.model, upstreamModel: intent.model, baseUrl: 'https://pidoi.com', authHeader: 'fixture-key', requestBody: intent.requestBody };
    const submitted = plugin.buildSubmitRequest(ctx).body;
    const reserved = plugin.extractUsage(ctx);
    const row = catalog.models.find(row => row.model_name === intent.model);
    const price = row.model_price;
    const expectedCharge = row.price_unit === 'second' ? Number(submitted.seconds) * price : price;
    assert.equal((reserved.seconds ?? reserved.requests) * price, expectedCharge, intent.model);
    const completion = plugin.extractUsageOnComplete({ model: intent.model }, { status: 'SUCCESS' }, { seconds: '9999', requests: 9999 });
    assert.deepEqual({ ...reserved, ...completion }, reserved, intent.model);
  }
});

test('describeSpec agrees with the submitted body for every enabled model', () => {
  for (const entry of fixture.cases.filter(entry => entry.name.endsWith(' decode'))) {
    const intent = plugin.protocols.openai_video.decodeRequest(entry.args[0]);
    const ctx = { model: intent.model, upstreamModel: intent.model, requestBody: intent.requestBody };
    const spec = plugin.describeSpec(ctx); // no credentials: the hook is read-only
    const submitted = plugin.buildSubmitRequest({ ...ctx, baseUrl: 'https://pidoi.com', authHeader: 'fixture-key' }).body;
    assert.equal(spec.spec_version, 2);
    assert.deepEqual(spec.reference_video_urls, submitted.reference_videos || []);
    assert.equal(spec.output_seconds, Number(submitted.seconds), intent.model);
    assert.ok(spec.resolution === '*' || spec.resolution === submitted.resolution, intent.model);
    assert.deepEqual(spec.references, {
      video: (submitted.reference_videos || []).length,
      image: (submitted.image_url ? 1 : 0) + (submitted.reference_image_urls || []).length,
      audio: (submitted.audio_urls || []).length,
    }, intent.model);
    const usage = plugin.extractUsage(ctx);
    if (usage.seconds !== undefined) assert.equal(spec.output_seconds, usage.seconds, intent.model);
  }
});

test('describeSpec keeps tiers for per-request models and reports fixed lengths', () => {
  const describe = (value) => {
    const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, body: { kind: 'json', value } });
    return plugin.describeSpec({ model: value.model, upstreamModel: value.model, requestBody: intent.requestBody });
  };
  const none = { video: 0, image: 0, audio: 0 };
  assert.deepEqual(describe({ model: 'tejiasd-mini-720p', prompt: 'cat', seconds: 15, resolution: '480p' }),
    { spec_version: 2, reference_video_urls: [], output_seconds: 15, seconds_kind: 'exact', resolution: '480p', references: none }, 'billed per request, still tiered');
  assert.deepEqual(describe({ model: 'tejiasd-mini-720p', prompt: 'cat', seconds: 10, resolution: '720p' }).resolution, '720p');
  assert.throws(() => describe({ model: 'tejiasd-mini-720p', prompt: 'cat', seconds: 15, resolution: '720p' }), /at most 12 seconds/);
  assert.deepEqual(describe({ model: 'sora-v3-933-pro', prompt: 'cat' }),
    { spec_version: 2, reference_video_urls: [], output_seconds: 15, seconds_kind: 'fixed', resolution: '720p', references: none }, 'fixed length and default tier are filled in');
  assert.equal(describe({ model: 'veo-3.1-fast', prompt: 'cat', seconds: 8, resolution: '1080p' }).resolution, '*', 'no published tiers');
  assert.deepEqual(describe({
    model: 'sd-2.5-480p-plus', prompt: 'cat', seconds: 5,
    images: ['https://cdn.example/1.png', 'https://cdn.example/2.png', 'https://cdn.example/3.png'],
    videos: ['https://cdn.example/1.mp4'], audio_urls: ['https://cdn.example/1.mp3'],
  }).references, { video: 1, image: 3, audio: 1 }, 'image_url plus reference_image_urls');
});

const REQUEST = { model: "veo-3.1-fast", prompt: "cat", seconds: 8, resolution: "1080p" };
const BASE_URL = "https://pidoi.com";

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
    ["内部错误，请稍等重试", "upstream"],
    ["您上传的[图片或视频]包含敏感内容，无法直接使用，请更换后重试。", "user"],
    ["生成的音频内容中可能包含[敏感信息]，请修改输入内容后重试。", "user"],
    ["请求参数或素材格式不符合要求，请检查后重试", "user"],
    ["请求失败，输出视频可能涉及版权限制", "user"],
    ["生成视频未通过内容审核，请调整提示词或参考素材后重试", "user"],
    ["", "upstream"],
    ["生成失败：请重新提交任务。请检查上传的素材和引用的格式符合标准。", "upstream"],
  ]) assert.equal(plugin.classifyFailure(reason), kind, reason);
});
