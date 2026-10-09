import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as plugin from './plugin.js';

const sd10 = 'sd10-seedance-2.0';
const official = 'doubao-seedance-2-0-260128';
const alias = 'cangyuan-' + official;
const image = 'https://cdn.example/image.png';
const video = 'https://cdn.example/video.mp4?signature=original%2Bvalue';
const audio = 'https://cdn.example/audio.wav';
const credentials = { baseUrl: 'https://ai.cangyuansuanli.cn/', apiKey: 'fixture-only-key' };

// Independently transcribed from each model page and the authenticated model
// list, not generated from the implementation's capability table.
const modelCases = [
  ['sd10-seedance-2.0', [5, 10, 15], ['720p']],
  ['sd10-seedance-2.0-fast', [5, 10, 15], ['720p']],
  ['sd10-seedance-2.0-mini', [5, 10], ['720p']],
  ['sd10-seedance-2.5', [30], ['720p']],
  ['sd11-seedance-2.0', [4, 7, 15], ['480p', '720p', '1080p']],
  ['sd11-seedance-2.0-fast', [4, 7, 15], ['480p', '720p']],
  ['sd11-seedance-2.0-mini', [4, 7, 15], ['480p', '720p']],
  ['sd11-seedance-2.5', [4, 5, 30], ['480p', '720p', '1080p']],
  ['sd13-seedance-2.0', [4, 8, 15], ['480p', '720p', '1080p', '4k']],
  ['sd13-seedance-2.0-fast', [4, 8, 15], ['480p', '720p']],
  ['sd13-seedance-2.0-mini', [4, 8, 15], ['480p', '720p']],
  ['sd13-seedance-2.5', [4, 5, 30], ['480p', '720p']],
  ['sd14-seedance-2.0', [4, 10, 15], ['720p']],
  ['sd15-seedance-2.0', [4, 5, 15], ['480p', '720p']],
  ['sd15-seedance-2.5', [4, 5, 30], ['480p', '720p']],
  ['sd7-seedance-2.0-1080p', [4, 10, 15], ['1080p']],
  ['sd7-seedance-2.0-720p', [4, 10, 15], ['720p']],
  ['sd8-seedance-2.5', [30], []],
  ['doubao-seedance-2-0-260128', [4, 10, 15], ['720p']],
  ['doubao-seedance-2-0-fast-260128', [4, 10, 15], ['720p']],
  ['doubao-seedance-2-5-260628', [4, 10, 30], ['720p']],
];

function decode(model, fields, upstreamModel) {
  return plugin.protocols.openai_video.decodeRequest({ model, upstreamModel, body: { kind: 'json', value: { model, prompt: '海边日出', ...fields } } });
}

function context(model, fields, upstreamModel) {
  const intent = decode(model, fields, upstreamModel);
  return { model, upstreamModel, requestBody: intent.requestBody, ...credentials };
}

test('Plugin covers the 21 account-visible models while isolating the official provider', () => {
  const expected = modelCases.map(([model]) => model.startsWith('doubao-') ? 'cangyuan-' + model : model);
  assert.deepEqual([...plugin.meta.models].sort(), expected.sort());
});

for (const [wireModel, durations, tiers] of modelCases) {
  test('Independent model contract: ' + wireModel, () => {
    const isOfficial = wireModel.startsWith('doubao-');
    const model = isOfficial ? 'cangyuan-' + wireModel : wireModel;
    for (const duration of durations) {
      for (const tier of tiers.length ? tiers : [undefined]) {
        const input = { duration, ...(tier ? { resolution: tier } : {}) };
        const ctx = { ...context('unified-seedance', input, model), salesSource: 'video_request' };
        const request = plugin.buildSubmitRequest(ctx);
        const spec = plugin.describeSpec(ctx);
        assert.equal(request.body.model, wireModel);
        assert.equal(request.body.duration, duration);
        assert.equal(request.body.resolution, tier);
        assert.equal(spec.output_seconds, duration);
        assert.equal(spec.resolution, tier || '*');
        assert.equal(spec.seconds_kind, durations.length === 1 ? 'fixed' : 'exact');
        assert.deepEqual(plugin.extractUsage(ctx), isOfficial ? {} : { requests: 1, ...(tier ? { resolution: tier } : {}) });
        const schema = plugin.meta.usageProfiles.find(profile => profile.models.includes(model)).schema;
        if (!isOfficial) {
          assert.equal(schema.requests.unit, 'count');
          assert.deepEqual(schema.resolution?.enum || [], tiers);
        }
      }
    }
    for (const duration of [durations[0] - 1, durations.at(-1) + 1]) assert.throws(() => context(model, { duration }));
    for (const resolution of ['480p', '720p', '1080p', '4k'].filter(tier => !tiers.includes(tier))) {
      assert.throws(() => context(model, { duration: durations[0], resolution }));
    }
  });
}

test('Fixed durations and model-specific defaults do not leak across mappings', () => {
  for (const [model, duration, resolution] of [
    ['sd10-seedance-2.5', 30, '720p'], ['sd11-seedance-2.0', 7, '480p'],
    ['sd11-seedance-2.5', 5, '480p'], ['sd13-seedance-2.0', 8, '480p'],
    ['sd15-seedance-2.0', 5, '720p'], ['sd8-seedance-2.5', 30, undefined],
  ]) {
    const decoded = decode('public-alias', {});
    const ctx = { model: decoded.model, upstreamModel: model, requestBody: decoded.requestBody, ...credentials };
    const body = plugin.buildSubmitRequest(ctx).body;
    assert.equal(body.duration, duration);
    assert.equal(body.resolution, resolution);
  }
  for (const model of ['sd10-seedance-2.0', 'sd10-seedance-2.0-fast', 'sd10-seedance-2.0-mini']) assert.throws(() => context(model, { duration: 6 }));
  assert.throws(() => context('sd10-seedance-2.0-mini', { duration: 15 }));
});

test('Each family enforces its own reference limits and unsupported media', () => {
  for (const [model, images, videos, audios] of [
    ['sd11-seedance-2.0', 9, 3, 3], ['sd11-seedance-2.0-fast', 9, 3, 3], ['sd11-seedance-2.0-mini', 9, 3, 3],
    ['sd11-seedance-2.5', 30, 10, 10], ['sd13-seedance-2.0', 9, 3, 0],
    ['sd13-seedance-2.0-fast', 9, 3, 0], ['sd13-seedance-2.0-mini', 9, 3, 0], ['sd13-seedance-2.5', 30, 10, 0],
    ['sd14-seedance-2.0', 9, 3, 3], ['sd15-seedance-2.0', 9, 3, 3], ['sd15-seedance-2.5', 30, 0, 10],
    ['sd7-seedance-2.0-1080p', 5, 3, 3], ['sd7-seedance-2.0-720p', 5, 3, 3], ['sd8-seedance-2.5', 9, 0, 0],
  ]) {
    const duration = model === 'sd8-seedance-2.5' ? 30 : 10;
    const fields = { duration, images: Array(images).fill(image), videos: Array(videos).fill(video), audios: Array(audios).fill(audio) };
    assert.deepEqual(plugin.describeSpec(context(model, fields)).references, { image: images, video: videos, audio: audios });
    for (const [field, max, url] of [['images', images, image], ['videos', videos, video], ['audios', audios, audio]]) {
      assert.throws(() => context(model, { duration, [field]: Array(max + 1).fill(url) }), undefined, model + ' ' + field);
    }
  }
});

test('SD11 and SD14 first-frame rules differ from paired official frames', () => {
  for (const model of ['sd11-seedance-2.0', 'sd11-seedance-2.0-fast', 'sd11-seedance-2.0-mini', 'sd11-seedance-2.5']) {
    const ctx = context(model, { first_image_url: image, images: [image], seed: 0, generate_audio: false });
    assert.equal(plugin.describeSpec(ctx).references.image, 2);
    assert.equal(plugin.buildSubmitRequest(ctx).body.last_image_url, undefined);
    assert.equal(plugin.describeSpec(context(model, { first_image_url: image, last_image_url: image, images: [image] })).references.image, 3);
    assert.throws(() => context(model, { seed: -1 }), /non-negative/);
    assert.throws(() => context(model, { last_image_url: image }), /requires first_image_url/);
  }
  assert.equal(plugin.describeSpec(context('sd14-seedance-2.0', { duration: 10, first_image_url: image })).references.image, 1);
  assert.throws(() => context('sd14-seedance-2.0', { duration: 10, first_image_url: image, images: [image] }), /cannot be combined/);
  for (const model of ['sd13-seedance-2.0', 'sd15-seedance-2.0', 'sd7-seedance-2.0-720p']) {
    assert.throws(() => context(model, { duration: 10, first_image_url: image }), /does not support/);
  }
});

test('Ratios, generated audio, seeds and size use the selected model capability', () => {
  assert.equal(plugin.buildSubmitRequest(context('sd13-seedance-2.0', { size: '3840x2160' })).body.resolution, '4k');
  assert.equal(plugin.buildSubmitRequest(context('sd11-seedance-2.5', { aspect_ratio: 'adaptive' })).body.aspect_ratio, 'adaptive');
  assert.equal(plugin.buildSubmitRequest(context('sd13-seedance-2.0', {})).body.aspect_ratio, 'auto');
  for (const [model, fields] of [
    ['sd14-seedance-2.0', { aspect_ratio: '21:9' }], ['sd15-seedance-2.5', { size: '960x720' }],
    ['sd13-seedance-2.0', { aspect_ratio: 'adaptive' }], ['sd11-seedance-2.0', { aspect_ratio: 'auto' }],
    ['sd13-seedance-2.0', { seed: 0 }], ['sd15-seedance-2.5', { generate_audio: false }],
    ['sd11-seedance-2.0', { camera_movement: 'fixed' }], ['sd11-seedance-2.0', { face_mode: true }],
    ['sd13-seedance-2.0', { images: ['asset://image-1'] }],
  ]) assert.throws(() => context(model, { duration: 10, ...fields }));
  assert.equal(plugin.parseTaskResult({}, { status: 'completed', url: video }).status, 'SUCCESS');
});

test('SD10 charges once for each supported duration and reports exact scheduling facts', () => {
  for (const duration of [5, 10, 15]) {
    const ctx = context(sd10, { seconds: String(duration), size: '1280x720', camera_movement: 'fixed' });
    assert.deepEqual(plugin.extractUsage(ctx), { requests: 1, resolution: '720p' });
    assert.deepEqual(plugin.extractUsageOnComplete(ctx, { status: 'SUCCESS' }, { duration: 999999, usage: { total_tokens: 999999 } }), {});
    assert.deepEqual(plugin.describeSpec(ctx), {
      spec_version: 2, output_seconds: duration, seconds_kind: 'exact', resolution: '720p',
      references: { image: 0, video: 0, audio: 0 }, reference_video_urls: [],
    });
    assert.deepEqual(plugin.buildSubmitRequest(ctx), {
      url: 'https://ai.cangyuansuanli.cn/v1/videos', method: 'POST',
      headers: { Authorization: 'Bearer fixture-only-key', 'Content-Type': 'application/json' },
      body: { model: sd10, prompt: '海边日出', duration, resolution: '720p', aspect_ratio: '16:9', camera_movement: 'fixed' },
    });
  }
});

test('Official wire model is unchanged and only a host-frozen sale permits submission', () => {
  const ctx = context(alias, { duration: 6, generate_audio: false, seed: 0, face_mode: false });
  assert.equal(plugin.describeSpec(ctx).output_seconds, 6);
  for (const salesSource of [undefined, false, 'plugin_usage']) {
    assert.throws(() => plugin.extractUsage({ ...ctx, salesSource }), /unified video sales/);
    assert.throws(() => plugin.buildSubmitRequest({ ...ctx, salesSource }), /unified video sales/);
  }
  const unified = { ...ctx, salesSource: 'video_request' };
  assert.deepEqual(plugin.extractUsage(unified), {});
  const request = plugin.buildSubmitRequest(unified);
  assert.equal(request.body.model, official);
  assert.equal(request.body.generate_audio, false);
  assert.equal(request.body.face_mode, false);
  assert.equal(request.body.seed, 0);
  assert.equal(request.model, undefined);
  assert.deepEqual(plugin.meta.usageProfiles.find(profile => profile.models.includes(alias)).schema, {});
  assert.ok(!plugin.meta.models.includes(official));
  assert.throws(() => decode(alias, { salesSource: 'video_request' }), /unsupported video parameter/);
});

test('Arbitrary public aliases defer model-specific defaults until mapping', () => {
  const intent = decode('unified-seedance', { duration: 10 });
  assert.equal(intent.requestBody.aspect_ratio, undefined);
  for (const [upstreamModel, aspect_ratio] of [[sd10, '9:16'], [alias, '16:9'], [official, '16:9']]) {
    const ctx = { model: intent.model, requestBody: intent.requestBody, upstreamModel, salesSource: 'video_request', ...credentials };
    assert.equal(plugin.buildSubmitRequest(ctx).body.aspect_ratio, aspect_ratio);
    assert.equal(plugin.describeSpec(ctx).output_seconds, 10);
  }
  assert.equal(plugin.describeSpec(context(alias, {})).output_seconds, 4);
  assert.throws(() => context(sd10, {}), /requires duration or seconds/);
  assert.throws(() => plugin.describeSpec({ model: 'unknown', requestBody: intent.requestBody }), /unsupported Cangyuan model/);
});

test('SD10 forwards all user-confirmed references and preserves duplicate videos', () => {
  const ctx = context(sd10, { duration: 10, images: [image], videos: [video, video], audios: [audio], face_mode: true });
  const before = structuredClone(ctx.requestBody);
  Object.freeze(ctx.requestBody.reference_image_urls);
  Object.freeze(ctx.requestBody.reference_videos);
  Object.freeze(ctx.requestBody.reference_audios);
  Object.freeze(ctx.requestBody);
  const body = plugin.buildSubmitRequest(ctx).body;
  assert.deepEqual(body.reference_image_urls, [image]);
  assert.deepEqual(body.reference_videos, [video, video]);
  assert.deepEqual(body.reference_audios, [audio]);
  assert.equal(body.face_mode, true);
  assert.deepEqual(plugin.describeSpec(ctx).references, { image: 1, video: 2, audio: 1 });
  assert.deepEqual(plugin.describeSpec(ctx).reference_video_urls, [video, video]);
  assert.deepEqual(ctx.requestBody, before);
});

test('Equivalent reference aliases are not double-counted; different aliases fail', () => {
  const ctx = context(sd10, { duration: 10, images: [image], reference_image_urls: [image], reference_videos: [video], videos: [video] });
  assert.deepEqual(plugin.describeSpec(ctx).references, { image: 1, video: 1, audio: 0 });
  assert.throws(() => context(sd10, { duration: 10, images: [image], reference_image_urls: ['https://cdn.example/other.png'] }), /conflicting reference/);
});

test('Asset references remain on the wire and never become partial duration sources', () => {
  for (const model of [sd10, alias]) {
    const ctx = { ...context(model, {
      duration: 10, reference_image_urls: ['asset://asset-image'],
      reference_videos: [video, 'asset://asset-video'], reference_audios: ['asset://asset-audio'],
    }), salesSource: 'video_request' };
    assert.deepEqual(plugin.buildSubmitRequest(ctx).body.reference_videos, [video, 'asset://asset-video']);
    const spec = plugin.describeSpec(ctx);
    assert.deepEqual(spec.references, { image: 1, video: 2, audio: 1 });
    assert.equal(Object.hasOwn(spec, 'reference_video_urls'), false);
    for (const face_mode of [false, true]) {
      assert.throws(() => plugin.describeSpec({ ...ctx, requestBody: { ...ctx.requestBody, face_mode } }), /omit face_mode/);
    }
  }
});

test('Official first and last frames are paired, counted and exclusive with references', () => {
  const frames = { first_image_url: image, last_image_url: 'asset://asset-last' };
  const ctx = context(alias, { duration: 8, ...frames });
  assert.deepEqual(plugin.describeSpec(ctx).references, { image: 2, video: 0, audio: 0 });
  assert.equal(plugin.buildSubmitRequest({ ...ctx, salesSource: 'video_request' }).body.last_image_url, frames.last_image_url);
  for (const field of ['first_image_url', 'last_image_url']) assert.throws(() => context(alias, { [field]: image }), /supplied together|requires first_image_url/);
  for (const field of ['images', 'reference_videos', 'reference_audios']) assert.throws(() => context(alias, { ...frames, [field]: [image] }), /cannot be combined/);
  assert.throws(() => context(sd10, { duration: 10, ...frames }), /does not support/);
});

test('JSON and multipart converge, retaining false, zero and reference arrays', () => {
  const decoded = plugin.protocols.openai_video.decodeRequest({ model: alias, body: {
    kind: 'multipart', fields: {
      model: [alias], prompt: ['cat'], seconds: ['10'], size: ['720x1280'],
      generate_audio: ['false'], face_mode: ['false'], seed: ['0'],
      'reference_image_urls[]': [image, image], reference_videos: [JSON.stringify([video])], reference_audios: [audio],
    },
  } });
  const expected = decode(alias, {
    prompt: 'cat', duration: 10, resolution: '720p', aspect_ratio: '9:16', generate_audio: false, face_mode: false, seed: 0,
    reference_image_urls: [image, image], reference_videos: [video], reference_audios: [audio],
  });
  assert.deepEqual(decoded, expected);
  assert.throws(() => plugin.protocols.openai_video.decodeRequest({ body: { kind: 'multipart', files: [{ ref: 'request_file:input_reference' }] } }), /local file uploads/);
  assert.throws(() => plugin.protocols.openai_video.decodeRequest({ body: { kind: 'multipart', fields: { duration: ['5', '10'] } } }), /provided once/);
  assert.throws(() => plugin.protocols.openai_video.decodeRequest({ body: { kind: 'multipart', fields: { seed: [''] } } }), /integer/);
});

const invalid = [
  ['zero duration', { duration: 0 }], ['negative duration', { duration: -1 }], ['fractional duration', { duration: 5.5 }],
  ['oversized duration', { duration: 3601 }], ['overflow duration', { duration: '18446744073686646784' }],
  ['boolean duration', { duration: true }], ['null duration', { duration: null }], ['null seconds', { seconds: null }],
  ['conflicting seconds', { seconds: 15 }], ['multiple outputs', { n: 2 }], ['zero outputs', { n: 0 }],
  ['unsupported tier', { resolution: '1080p' }], ['wrong size', { size: '1920x1080' }],
  ['conflicting ratio', { aspect_ratio: '16:9', ratio: '9:16' }], ['conflicting size', { aspect_ratio: '16:9', size: '720x1280' }],
  ['hidden multiplier', { metadata: { duration: 9999 } }], ['hidden parameters', { parameters: { n: 50 } }],
  ['client billing source', { salesSource: 'video_request' }], ['client usage', { usage: { requests: 0 } }],
  ['string boolean', { face_mode: 'false' }], ['empty prompt', { prompt: '' }],
  ['http reference', { reference_videos: ['http://cdn.example/video.mp4'] }],
  ['credential reference', { reference_audios: ['https://user:secret@cdn.example/audio.wav'] }],
  ['backslash URL', { images: ['https://trusted.example\\@evil.example/x'] }],
  ['local reference', { images: ['file:///tmp/input.png'] }], ['data URL', { images: ['data:image/png;base64,abc'] }],
  ['asset URL traversal', { images: ['asset://../secret'] }], ['bad media shape', { images: [{}] }],
  ['too many images', { images: Array(10).fill(image) }], ['mismatched override model', { model: 'another-model' }],
];

for (const [name, fields] of invalid) {
  test('Overrides cannot bypass decode, usage, quote or submission validation: ' + name, () => {
    const ctx = { model: sd10, ...credentials, requestBody: { prompt: 'cat', duration: 10, resolution: '720p', ...fields } };
    for (const hook of ['extractUsage', 'describeSpec', 'buildSubmitRequest']) assert.throws(() => plugin[hook](ctx), undefined, hook);
    if (name !== 'mismatched override model') assert.throws(() => decode(sd10, { duration: 10, ...fields }));
  });
}

test('Mapped model capabilities are checked before quoting or submission', () => {
  for (const [model, fields] of [
    [sd10, { duration: 6 }], [sd10, { duration: 10, generate_audio: false }], [sd10, { duration: 10, seed: 0 }],
    [sd10, { duration: 10, prompt: 'a'.repeat(6001) }], [alias, { duration: 6, camera_movement: 'auto' }],
    [alias, { duration: 6, reference_videos: Array(4).fill(video) }], [alias, { duration: 6, seed: 2 ** 53 }],
  ]) {
    const ctx = { model: 'public-alias', upstreamModel: model, salesSource: 'video_request', ...credentials, requestBody: { prompt: 'cat', ...fields } };
    for (const hook of ['extractUsage', 'describeSpec', 'buildSubmitRequest']) assert.throws(() => plugin[hook](ctx));
  }
});

test('Submission distinguishes rejection, accepted terminal failure and unknown outcome', () => {
  const reason = 'content moderation rejected the prompt';
  assert.deepEqual(plugin.parseSubmitResponse({}, { body: { error: { message: reason } } }), { rejected: { reason } });
  for (const field of ['id', 'task_id']) {
    const queued = { [field]: 'video_42', status: 'queued', progress: 0 };
    assert.deepEqual(plugin.parseSubmitResponse({}, { body: queued }), { taskId: 'video_42', taskData: queued });
  }
  const failed = { id: 'video_42', status: 'failed', error: { message: reason } };
  assert.deepEqual(plugin.parseSubmitResponse({}, { body: failed }).immediate, { status: 'FAILURE', reason });
  const completed = { id: 'video_42', status: 'completed', video_url: video };
  assert.deepEqual(plugin.parseSubmitResponse({}, { body: completed }).immediate, { status: 'SUCCESS' });
  for (const body of [{}, 'bad JSON', { id: 42 }, { id: '' }, { id: 'a', task_id: 'b' },
    { id: 'a', error: { message: reason } }, { status: 'queued', success: false }, { id: 'a', status: 'queued', success: false }]) {
    assert.throws(() => plugin.parseSubmitResponse({}, { body }));
  }
});

test('Queries use persisted IDs; unrecognized or mismatched polls cannot settle', () => {
  const ctx = { ...credentials, taskId: 'video_42' };
  assert.equal(plugin.buildQueryRequest({ ...ctx, taskId: 'a/b?c' }).url, 'https://ai.cangyuansuanli.cn/v1/videos/a%2Fb%3Fc');
  for (const [status, expected] of [['queued', 'QUEUED'], ['in_progress', 'IN_PROGRESS'], ['completed', 'SUCCESS'], ['failed', 'FAILURE']]) {
    assert.equal(plugin.parseTaskResult(ctx, { id: ctx.taskId, status, video_url: video }).status, expected);
  }
  for (const body of [{}, null, { status: 'toString' }, { status: ['completed'], video_url: video }, { status: 'new_unknown_status' }, { status: 'completed' },
    { status: 'completed', video_url: video, id: 'another' }, { status: 'in_progress', error: 'outage' }]) {
    assert.equal(plugin.parseTaskResult(ctx, body).status, 'UNKNOWN');
  }
});

test('Public content URLs remain opaque and receive neither channel nor client credentials', () => {
  for (const data of [{ video_url: video }, { data: [{ url: video }] }]) {
    for (const method of ['GET', 'HEAD']) {
      const ctx = { ...credentials, artifactKey: 'video', data, clientRequest: { method, headers: { Authorization: 'client-secret' } } };
      assert.deepEqual(plugin.buildContentRequest(ctx), { url: video, method, credentialless: true });
    }
    assert.deepEqual(plugin.listArtifacts({ status: 'SUCCESS', data }), [{ key: 'video', type: 'video', mimeType: 'video/mp4' }]);
    assert.deepEqual(plugin.protocols.openai_video.render({}, { status: 'SUCCESS', data: { ...data, id: 'private-task', usage: { cost: 3.9 } } }), { video_url: video });
  }
  assert.deepEqual(plugin.listArtifacts({ status: 'FAILURE', data: { video_url: video } }), []);
  assert.deepEqual(plugin.buildContentRequest({ artifactKey: 'video', data: { video_url: 'http://cdn.example/v.mp4?sig=opaque' } }), {
    url: 'http://cdn.example/v.mp4?sig=opaque', method: 'GET', credentialless: true,
  });
  assert.throws(() => plugin.buildContentRequest({ artifactKey: 'video', data: { video_url: 'https://key:secret@cdn.example/a.mp4' } }), /no valid video URL/);
  assert.throws(() => plugin.buildContentRequest({ artifactKey: 'missing', data: { video_url: video } }), /artifact_not_found/);
});

test('Failure attribution preserves channel health for user refusals and cancellation', () => {
  for (const [reason, expected] of [
    ['content moderation rejected the prompt', 'user'], ['内容违规', 'user'], ['task cancelled by user', 'cancelled'],
    ['upstream timeout', 'upstream'], ['moderation service unavailable', 'upstream'], ['insufficient balance', 'upstream'],
  ]) assert.equal(plugin.classifyFailure(reason), expected);
  for (const [code, expected] of [['video_request_rejected', 'user'], ['video_generation_cancelled', 'cancelled'], ['video_generation_failed', 'upstream']]) {
    const result = plugin.parseTaskResult({}, { status: 'failed', error: { code, message: 'Video generation failed' } });
    assert.equal(plugin.classifyFailure(result.reason), expected);
  }
});
