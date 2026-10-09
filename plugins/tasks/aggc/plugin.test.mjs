import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as plugin from './plugin.js';

// Independent expectations from the public /api/v1/models parameter schemas.
const CASES = [
  ['sd2.0-933-op', 4, 15, ['480p', '720p', '1080p', '4k'], 9, 3, 3],
  ['sd2.0-933-mm', 4, 15, [], 9, 0, 3],
  ['sd2.0-933-oc', 4, 15, [], 9, 3, 3],
  ['sd2.0-933-oa', 4, 15, [], 9, 3, 3],
  ['sd2.0-933-qd', 4, 15, ['480p', '720p', '1080p'], 9, 3, 3],
  ['sd2.0-fast-933-op', 4, 15, ['480p', '720p'], 9, 3, 3],
  ['sd2.0-mini-933-op', 4, 15, ['480p', '720p'], 9, 3, 3],
  ['sd2.0-fast-933-qd', 4, 15, ['480p', '720p'], 9, 3, 3],
  ['sd2.0-mini-933-qd', 4, 15, ['480p', '720p'], 9, 3, 3],
  ['sd2.5-op', 4, 30, ['480p', '720p', '1080p'], 30, 10, 10],
  ['sd2.5-qd', 4, 30, ['480p', '720p', '1080p'], 30, 10, 10],
  ['sd2.5-mm', 4, 30, [], 30, 0, 10],
  ['sd2.5-m2', 4, 30, [], 10, 0, 10],
  ['wan3.0', 5, 30, ['720p', '1080p'], 9, 3, 3],
  ['aggc-minimax-h3', 4, 15, ['768p'], 9, 3, 3],
];
const REQUEST = { model: 'sd2.0-933-op', prompt: '海边奔跑的小猫', seconds: 5, resolution: '720p' };
const CREDENTIALS = { baseUrl: 'https://aggc.site/', apiKey: 'fixture-only-key' };
const VIDEO = 'https://cdn.example/video.mp4?sig=temporary';

function decode(value, extra = {}) {
  return plugin.protocols.openai_video.decodeRequest({ model: value.model, body: { kind: 'json', value }, ...extra });
}

function driver(value, extra = {}) {
  const intent = decode(value, extra);
  return { model: value.model, requestBody: intent.requestBody, ...extra };
}

test('only the 15 public video models are advertised', () => {
  assert.deepEqual([...plugin.meta.models].sort(), CASES.map(row => row[0]).sort());
});

for (const [model, min, max, tiers, images, videos, audios] of CASES) {
  test(`${model}: request, billing and scheduling agree on every tier and duration boundary`, () => {
    for (const seconds of [min, max]) {
      for (const resolution of tiers.length ? tiers : [undefined]) {
        const value = { model, prompt: REQUEST.prompt, seconds };
        if (resolution) value.resolution = resolution;
        const params = { duration: seconds, aspectRatio: '16:9' };
        const refs = { image: images, video: videos, audio: audios };
        for (const [field, count, extension] of [['imageUrls', images, 'png'], ['videoUrls', videos, 'mp4'], ['audioUrls', audios, 'wav']]) {
          if (count) value[field] = params[field] = Array(count).fill(`https://media.example/ref.${extension}`);
        }
        if (resolution) params.resolution = resolution === '768p' ? '768P' : resolution;
        const ctx = driver(value);
        const before = JSON.stringify(ctx);
        const spec = plugin.describeSpec(ctx);
        assert.deepEqual(spec, {
          spec_version: 2, output_seconds: seconds, seconds_kind: 'exact',
          resolution: resolution || '*', references: refs,
          reference_video_urls: value.videoUrls || [],
        });
        const facts = { requests: 1, seconds };
        if (resolution) facts.resolution = resolution;
        assert.deepEqual(plugin.extractUsage(ctx), facts);
        assert.deepEqual(plugin.buildSubmitRequest({ ...ctx, ...CREDENTIALS }), {
          url: 'https://aggc.site/api/v1/prot/generate', method: 'POST',
          headers: { 'x-api-key': 'fixture-only-key', 'Content-Type': 'application/json' },
          body: { model_id: model === 'aggc-minimax-h3' ? 'minimax-h3' : model, type: 'video', prompt: REQUEST.prompt, params },
        });
        assert.equal(JSON.stringify(ctx), before, 'hooks do not mutate request snapshots');
        const profile = plugin.meta.usageProfiles.find(row => row.models.includes(model));
        assert.deepEqual(Object.keys(profile.schema).sort(), Object.keys(facts).sort());
        if (resolution) assert.ok(profile.schema.resolution.enum.includes(resolution));
      }
    }
  });

  test(`${model}: rejected specs cannot enter quotes, reservation or submission`, () => {
    const valid = { model, prompt: REQUEST.prompt, duration: min };
    if (tiers.length) valid.resolution = tiers[0];
    const invalid = [
      { ...valid, duration: min - 1 }, { ...valid, duration: max + 1 },
      { ...valid, resolution: '1440p' },
      ...[['imageUrls', images], ['videoUrls', videos], ['audioUrls', audios]].map(([field, limit]) => ({
        ...valid, [field]: Array(limit + 1).fill('https://media.example/ref'),
      })),
    ];
    if (!tiers.length) invalid.push({ ...valid, resolution: '720p' });
    else {
      invalid.push({ ...valid, resolution: undefined });
      if (!tiers.includes('4k')) invalid.push({ ...valid, resolution: '4k' });
    }
    for (const value of invalid) {
      assert.throws(() => decode(value), undefined, JSON.stringify(value));
      const ctx = { model, requestBody: value, ...CREDENTIALS };
      for (const hook of [plugin.describeSpec, plugin.extractUsage, plugin.buildSubmitRequest]) assert.throws(() => hook(ctx));
    }
  });
}

test('public aliases are preserved while the final mapped model constrains capabilities', () => {
  const value = { ...REQUEST, model: 'seedance-2.0', size: '1280x720' };
  const first = decode(value);
  assert.equal(first.model, 'seedance-2.0');
  const ctx = { model: value.model, upstreamModel: 'sd2.0-933-qd', requestBody: first.requestBody };
  assert.equal(plugin.describeSpec(ctx).resolution, '720p');
  assert.equal(plugin.buildSubmitRequest({ ...ctx, ...CREDENTIALS }).body.model_id, 'sd2.0-933-qd');
  for (const model of ['seedance-2.0', 'unknown', 'constructor', '__proto__']) {
    assert.throws(() => plugin.describeSpec({ ...ctx, upstreamModel: model }), /unsupported upstream/);
  }
  assert.throws(() => plugin.describeSpec({ ...ctx, upstreamModel: 'sd2.0-933-mm' }), /no selectable resolution/);
  assert.throws(() => decode({ ...value, seconds: 20 }, { upstreamModel: 'sd2.0-933-qd' }), /duration/);
  assert.throws(() => decode(value, { model: 'different' }), /selected model/);
  const minimax = driver({ ...REQUEST, model: 'public-minimax', resolution: '768p' }, { upstreamModel: 'minimax-h3' });
  assert.equal(plugin.buildSubmitRequest({ ...minimax, ...CREDENTIALS }).body.model_id, 'minimax-h3');
});

test('OpenAI size and reference aliases become the documented AGGC params envelope', () => {
  const value = {
    model: REQUEST.model, prompt: REQUEST.prompt, seconds: '15', size: '3840x2160',
    reference_images: ['https://cdn.example/a.png'], referenceVideos: [VIDEO, VIDEO],
    referenceAudios: ['https://cdn.example/a.wav'],
  };
  const ctx = driver(value);
  assert.deepEqual(ctx.requestBody, {
    prompt: REQUEST.prompt, params: {
      duration: 15, aspectRatio: '16:9', resolution: '4k', imageUrls: ['https://cdn.example/a.png'],
      videoUrls: [VIDEO, VIDEO], audioUrls: ['https://cdn.example/a.wav'],
    },
  });
  assert.deepEqual(plugin.describeSpec(ctx).reference_video_urls, [VIDEO, VIDEO], 'duplicates count as separate references');
  assert.equal(decode({ ...REQUEST, input_reference: { image_url: 'https://cdn.example/a.png' } }).action, 'reference_to_video');
  assert.deepEqual(decode({ ...REQUEST, imageUrls: '["https://cdn.example/a.png"]' }).requestBody.params.imageUrls, ['https://cdn.example/a.png']);
});

test('nested params are validated and cannot override billing or references', () => {
  assert.deepEqual(decode({ model: REQUEST.model, prompt: REQUEST.prompt, params: { duration: 5, resolution: '720p' } }).requestBody, decode(REQUEST).requestBody);
  for (const patch of [
    { params: { duration: 9 } }, { params: { resolution: '1080p' } },
    { params: { n: 100 } }, { params: { model_id: 'sd2.5-op' } },
    { params: [] }, { params: null }, { duration: 9 }, { resolution: '720p', size: '1920x1080' },
    { size: '1280x720', ratio: '9:16' }, { metadata: { duration: 100 } },
    { images: [VIDEO], imageUrls: [VIDEO] }, { params: { videoUrls: [VIDEO] }, videos: [VIDEO] },
    { input: { prompt: 'ignored' } }, { parameters: { duration: 100 } }, { n: 0 }, { n: 2 },
  ]) assert.throws(() => decode({ ...REQUEST, ...patch }), undefined, JSON.stringify(patch));
  for (const seconds of [0, -1, 4.5, true, null, {}, [], '1e2', 'Infinity', NaN, Infinity, 3601, 18446744073709551615]) {
    assert.throws(() => decode({ ...REQUEST, seconds }), undefined, String(seconds));
  }
  for (const url of ['file:///tmp/x', 'https://user:pass@cdn.example/a', 'https://cdn.example\\@private/x', 42]) {
    assert.throws(() => decode({ ...REQUEST, videos: [url] }));
  }
  assert.throws(() => decode({ ...REQUEST, videos: ['https://cdn.example/a\nb'] }));
});

test('multipart text and repeated URLs match JSON while file bytes are rejected', () => {
  const body = { kind: 'multipart', files: [], fields: {
    model: [REQUEST.model], prompt: [REQUEST.prompt], seconds: ['5'], size: ['1280x720'],
    'imageUrls[]': ['https://cdn.example/1.png', 'https://cdn.example/2.png'],
    videoUrls: ['["https://cdn.example/ref.mp4"]'],
  } };
  const intent = plugin.protocols.openai_video.decodeRequest({ model: REQUEST.model, body });
  assert.deepEqual(intent.requestBody.params, {
    duration: 5, resolution: '720p', aspectRatio: '16:9',
    imageUrls: ['https://cdn.example/1.png', 'https://cdn.example/2.png'], videoUrls: ['https://cdn.example/ref.mp4'],
  });
  for (const fields of [
    { ...body.fields, seconds: ['5', '10'] },
    { ...body.fields, imageUrls: [VIDEO] },
    { ...body.fields, params: ['{"duration":99}'] },
  ]) assert.throws(() => plugin.protocols.openai_video.decodeRequest({ model: REQUEST.model, body: { ...body, fields } }));
  assert.throws(() => plugin.protocols.openai_video.decodeRequest({ model: REQUEST.model, body: { ...body, files: [{ ref: 'request_file:input_reference' }] } }), /file uploads/);
});

test('face options preserve false and are confined to documented models', () => {
  const value = { model: 'sd2.5-mm', prompt: 'cat', duration: 30, faceModeEnabled: false };
  const body = plugin.buildSubmitRequest({ ...driver(value), ...CREDENTIALS }).body;
  assert.equal(body.params.faceModeEnabled, false);
  assert.equal(decode({ ...value, faceModeEnabled: true, facePreset: 'blur|face|ellipse' }).requestBody.params.facePreset, 'blur|face|ellipse');
  assert.throws(() => decode({ ...value, faceModeEnabled: 'false' }), /boolean/);
  assert.throws(() => decode({ ...value, facePreset: 'blur|face|ellipse' }), /conflicts/);
  assert.throws(() => decode({ ...value, facePreset: 'unknown' }), /facePreset/);
  assert.throws(() => decode({ ...REQUEST, faceModeEnabled: false }), /not supported/);
  const multipart = { kind: 'multipart', fields: { model: [value.model], prompt: ['cat'], duration: ['30'], faceModeEnabled: ['false'] }, files: [] };
  assert.equal(plugin.protocols.openai_video.decodeRequest({ model: value.model, body: multipart }).requestBody.params.faceModeEnabled, false);
});

test('documented create success with numeric job_id waits for a query URL', () => {
  const body = { code: 0, message: 'OK', data: { job_id: 123, status: 'success', credits_frozen: 10, credits_remaining: 500, error_message: null } };
  assert.deepEqual(plugin.parseSubmitResponse({}, { statusCode: 200, body }), { taskId: '123', taskData: body });
  assert.deepEqual(plugin.buildQueryRequest({ ...CREDENTIALS, taskId: '123' }), {
    url: 'https://aggc.site/api/v1/prot/query/123', method: 'GET', headers: { 'x-api-key': 'fixture-only-key' },
  });
  assert.deepEqual(plugin.parseTaskResult({ taskId: '123' }, { code: 0, data: { job_id: 123, status: 'success', video_url: VIDEO } }), { status: 'SUCCESS' });
});

test('terminal submit outcomes are accepted tasks, including the documented code 3001', () => {
  for (const [body, immediate] of [
    [{ code: 0, data: { job_id: '123', status: 'success', video_url: VIDEO } }, { status: 'SUCCESS' }],
    [{ code: 3001, message: '生成失败，请检查文案或素材', data: { job_id: 123, status: 'failed', error_message: '素材问题' } }, { status: 'FAILURE', reason: 'aggc:3001: 素材问题' }],
    [{ code: 0, data: { job_id: 123, status: 'failed', error_message: 'upstream timeout' } }, { status: 'FAILURE', reason: 'upstream timeout' }],
  ]) assert.deepEqual(plugin.parseSubmitResponse({}, { statusCode: 200, body }), { taskId: '123', taskData: body, immediate });
});

test('only unambiguous business rejections without a job id allow safe rejection handling', () => {
  for (const [code, message, classification] of [[3001, '素材问题', 'user'], [1005, '参数校验失败', 'user'], [1004, '积分不足', 'upstream'], [1002, 'API Key 无效', 'upstream']]) {
    const result = plugin.parseSubmitResponse({}, { body: { code, message, data: null } });
    assert.deepEqual(result, { rejected: { reason: `aggc:${code}: ${message}` } });
    assert.equal(plugin.classifyFailure(result.rejected.reason), classification);
  }
  for (const body of [
    {}, { code: '0', data: { job_id: 123 } }, { code: 0, data: {} },
    { code: 5000, message: 'internal error' }, { code: 777, message: 'unknown error' },
    { code: 1005, data: { job_id: 123, status: 'failed' } },
    { code: 3001, data: { job_id: 123, status: 'processing' } },
    { code: 3001, data: { status: 'success' } },
    ...[0, -1, 1.5, 9007199254740992, '', ' ', '123/456', [], {}].map(job_id => ({ code: 0, data: { job_id } })),
  ]) assert.throws(() => plugin.parseSubmitResponse({}, { statusCode: 200, body }), undefined, JSON.stringify(body));
  const id = '9007199254740993';
  assert.equal(plugin.parseSubmitResponse({}, { body: { code: 0, data: { job_id: id, status: 'processing' } } }).taskId, id);
});

test('polling rejects unknown statuses, API errors and mismatched jobs', () => {
  for (const [status, expected] of [['processing', 'IN_PROGRESS'], ['queued', 'QUEUED'], ['pending', 'QUEUED'], ['success', 'SUCCESS'], ['failed', 'FAILURE']]) {
    const result = plugin.parseTaskResult({ taskId: '123' }, { code: 0, data: { job_id: 123, status, video_url: VIDEO } });
    assert.equal(result.status, expected);
  }
  for (const body of [
    { code: 0, data: { job_id: 124, status: 'success', video_url: VIDEO } },
    { code: 0, data: { job_id: 123, status: 'success' } },
    { code: 0, data: { job_id: 123, status: 'constructor' } },
    { code: 0, data: { job_id: 123, status: '' } },
    { code: 0, data: { status: 'processing' } },
    ...[1002, 1004, 1006, 5000, 3001].map(code => ({ code, data: { job_id: 123, status: 'processing' } })),
    null, 'not JSON', [], { code: 0, data: [] },
  ]) assert.equal(plugin.parseTaskResult({ taskId: '123' }, body).status, 'UNKNOWN', JSON.stringify(body));
});

test('account credits and unverified output fields never change reserved usage', () => {
  const ctx = driver(REQUEST);
  assert.deepEqual(plugin.extractUsageOnComplete(ctx, { status: 'SUCCESS' }, {
    code: 0, data: { job_id: 123, status: 'success', video_url: VIDEO, credits_frozen: 9999999999, credits_remaining: -1, seconds: 999999, resolution: '4k' },
  }), {});
  assert.deepEqual(plugin.extractUsage(ctx), { requests: 1, seconds: 5, resolution: '720p' });
});

test('content fetches have no credentials and rendering excludes provider accounting', () => {
  const data = { code: 0, data: { job_id: 123, status: 'success', video_url: VIDEO, video_cover_url: 'https://cdn.example/cover.jpg', credits_frozen: 10, credits_remaining: 500 } };
  const task = { status: 'SUCCESS', data };
  assert.deepEqual(plugin.listArtifacts(task), [{ key: 'video', type: 'video', mimeType: 'video/mp4' }]);
  assert.deepEqual(plugin.protocols.openai_video.render({}, task), { url: VIDEO, video_cover_url: 'https://cdn.example/cover.jpg' });
  for (const method of ['GET', 'HEAD']) {
    assert.deepEqual(plugin.buildContentRequest({ ...CREDENTIALS, data, artifactKey: 'video', clientRequest: { method, headers: { Authorization: 'client-secret' } } }), { url: VIDEO, method, credentialless: true });
  }
  assert.deepEqual(plugin.listArtifacts({ ...task, status: 'FAILURE' }), []);
  assert.deepEqual(plugin.protocols.openai_video.render({}, { ...task, status: 'FAILURE' }), {});
  for (const video_url of ['file:///tmp/x', 'https://user:pass@cdn.example/x', 'https://cdn.example\\@private/x', '']) {
    const invalid = { code: 0, data: { video_url } };
    assert.throws(() => plugin.buildContentRequest({ data: invalid, artifactKey: 'video', clientRequest: { method: 'GET' } }), /artifact_not_found/);
    assert.deepEqual(plugin.listArtifacts({ status: 'SUCCESS', data: invalid }), []);
  }
});

test('API keys use the dedicated header over HTTPS and never enter URL or body', () => {
  const ctx = driver(REQUEST);
  for (const credentials of [{ apiKey: 'fixture-only-key' }, { authHeader: 'fixture-only-key' }, { apiKey: 'fixture-only-key', authHeader: 'Bearer unrelated' }]) {
    const request = plugin.buildSubmitRequest({ ...ctx, baseUrl: CREDENTIALS.baseUrl, ...credentials });
    assert.deepEqual(request.headers, { 'x-api-key': 'fixture-only-key', 'Content-Type': 'application/json' });
    assert.ok(!JSON.stringify([request.url, request.body]).includes('fixture-only-key'));
  }
  for (const apiKey of ['', 'Bearer secret', 'bad\r\nx-header: value', 'bad\u0000key']) {
    assert.throws(() => plugin.buildQueryRequest({ ...CREDENTIALS, apiKey, taskId: '123' }), /API key/);
  }
  for (const baseUrl of ['http://aggc.site', 'https://user:pass@aggc.site', 'https://aggc.site?key=secret', 'https://aggc.site/#x']) {
    assert.throws(() => plugin.buildQueryRequest({ ...CREDENTIALS, baseUrl, taskId: '123' }), /HTTPS/);
  }
  assert.throws(() => plugin.buildQueryRequest({ ...CREDENTIALS, taskId: '../balance' }), /job_id/);
});

test('generation attribution keeps infrastructure failures on the upstream', () => {
  for (const [reason, expected] of [
    ['aggc:3001: 素材问题', 'user'], ['aggc:3001: 上游余额不足', 'upstream'],
    ['aggc:1002: invalid prompt', 'upstream'], ['content moderation rejected the prompt', 'user'],
    ['aggc:1002: task cancelled by user', 'upstream'],
    ['内容违规', 'user'], ['moderation service unavailable', 'upstream'],
    ['task cancelled by user', 'cancelled'], ['job cancelled by provider due to internal error', 'upstream'],
  ]) assert.equal(plugin.classifyFailure(reason), expected, reason);
});
