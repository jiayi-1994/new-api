import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as plugin from './plugin.js';

// Independent expectations from the open-api page: [model, max seconds, tiers].
const CASES = [
  ['bytefor-2.5', 30, ['480p', '720p']],
  ['bytefor-2.0-real-priority', 15, ['480p', '720p', '1080p', '4k']],
  ['bytefor-2.0-real-standard', 15, ['480p', '720p', '1080p', '4k']],
  ['bytefor-2.0-fast', 15, ['480p', '720p', '1080p', '4k']],
];
const CREDENTIALS = { baseUrl: 'https://open.bytefor.com/', apiKey: 'fixture-only-key' };
const IMAGE = 'https://cdn.example/person.png';
const VIDEO = 'https://cdn.example/motion.mp4';
const AUDIO = 'https://cdn.example/voice.mp3';

function decode(value, extra = {}) {
  return plugin.protocols.openai_video.decodeRequest({ model: value.model, body: { kind: 'json', value }, ...extra });
}

function driver(value, extra = {}) {
  return { model: value.model, requestBody: decode(value, extra).requestBody, ...extra };
}

test('only the documented models are advertised', () => {
  assert.deepEqual([...plugin.meta.models].sort(), CASES.map(row => row[0]).sort());
});

for (const [model, max, tiers] of CASES) {
  test(`${model}: decode, usage, spec and submission agree on every tier and duration boundary`, () => {
    for (const seconds of [4, max]) {
      for (const resolution of tiers) {
        const value = { model, prompt: 'walk', seconds, resolution, images: [IMAGE], videos: [VIDEO], audios: [AUDIO], generate_audio: true };
        const ctx = driver(value);
        const before = JSON.stringify(ctx);
        assert.deepEqual(plugin.extractUsage(ctx), { seconds, resolution, video_input: true });
        assert.deepEqual(plugin.describeSpec(ctx), {
          spec_version: 2, output_seconds: seconds, seconds_kind: 'exact', resolution,
          references: { video: 1, image: 1, audio: 1 }, reference_video_urls: [VIDEO],
        });
        assert.deepEqual(plugin.buildSubmitRequest({ ...ctx, ...CREDENTIALS }), {
          url: 'https://open.bytefor.com/api/v3/contents/generations/tasks', method: 'POST',
          headers: { Authorization: 'Bearer fixture-only-key', 'Content-Type': 'application/json' },
          body: {
            model, resolution, duration: seconds, generate_audio: true,
            content: [
              { type: 'text', text: 'walk' },
              { type: 'image_url', role: 'reference_image', image_url: { url: IMAGE } },
              { type: 'video_url', role: 'reference_video', video_url: { url: VIDEO } },
              { type: 'audio_url', role: 'reference_audio', audio_url: { url: AUDIO } },
            ],
          },
        });
        assert.equal(JSON.stringify(ctx), before, 'hooks do not mutate request snapshots');
        assert.deepEqual(decode({ model, ...ctx.requestBody }).requestBody, ctx.requestBody, 'normalization is idempotent');
        const profile = plugin.meta.usageProfiles.find(row => row.models.includes(model));
        assert.deepEqual((profile ? profile.schema : plugin.meta.usageSchema).resolution.enum, tiers);
      }
    }
  });

  test(`${model}: out-of-range specs are rejected before billing`, () => {
    const valid = { model, prompt: 'walk', duration: 5, resolution: tiers[0] };
    for (const value of [
      { ...valid, duration: 3 }, { ...valid, duration: max + 1 }, { ...valid, duration: -1 }, { ...valid, duration: 5.5 },
      { ...valid, resolution: '2k' }, { ...valid, n: 2 }, { ...valid, priority: 9 },
    ]) {
      assert.throws(() => decode(value), undefined, JSON.stringify(value));
    }
  });
}

test('bytefor-2.5 rejects tiers it does not offer, including after channel mapping', () => {
  assert.throws(() => decode({ model: 'bytefor-2.5', prompt: 'walk', resolution: '1080p' }), /480p, 720p/);
  const ctx = driver({ model: 'seedance-alias', prompt: 'walk', resolution: '1080p', duration: 20 });
  assert.throws(() => plugin.extractUsage({ ...ctx, upstreamModel: 'bytefor-2.5' }), /480p, 720p/);
  assert.throws(() => plugin.extractUsage({ ...ctx, upstreamModel: 'bytefor-2.0-fast' }), /between 4 and 15/);
});

test('defaults are explicit upstream so the bill matches the submission', () => {
  const ctx = driver({ model: 'bytefor-2.0-fast', prompt: 'walk' });
  assert.deepEqual(plugin.extractUsage(ctx), { seconds: 5, resolution: '720p', video_input: false });
  assert.deepEqual(plugin.buildSubmitRequest({ ...ctx, ...CREDENTIALS }).body,
    { model: 'bytefor-2.0-fast', resolution: '720p', duration: 5, content: [{ type: 'text', text: 'walk' }] });
  const mapped = plugin.buildSubmitRequest({ ...ctx, ...CREDENTIALS, upstreamModel: 'bytefor-2.0-real-priority' });
  assert.equal(mapped.body.model, 'bytefor-2.0-real-priority');
});

test('Ark content is accepted at the top level or in metadata, with roles preserved', () => {
  const content = [
    { type: 'text', text: 'transition' },
    { type: 'image_url', role: 'first_frame', image_url: { url: 'https://cdn.example/start.png' } },
    { type: 'image_url', role: 'last_frame', image_url: { url: 'https://cdn.example/end.png' } },
  ];
  const top = driver({ model: 'bytefor-2.0-real-priority', content, ratio: '16:9', duration: 6, return_last_frame: true });
  const nested = driver({ model: 'bytefor-2.0-real-priority', metadata: { content, ratio: '16:9', duration: 6, return_last_frame: true } });
  assert.deepEqual(nested.requestBody, top.requestBody);
  assert.deepEqual(plugin.buildSubmitRequest({ ...top, ...CREDENTIALS }).body, {
    model: 'bytefor-2.0-real-priority', resolution: '720p', ratio: '16:9', duration: 6, return_last_frame: true, content,
  });
  assert.deepEqual(plugin.describeSpec(top).references, { video: 0, image: 2, audio: 0 });
  // A mirrored URL list must agree with content; a differing one fails instead of dropping a reference.
  const mirrored = driver({ model: 'bytefor-2.0-real-priority', metadata: { content }, images: ['https://cdn.example/start.png', 'https://cdn.example/end.png'] });
  assert.deepEqual(plugin.buildSubmitRequest({ ...mirrored, ...CREDENTIALS }).body.content, content);
  for (const value of [
    { model: 'bytefor-2.5', metadata: { content }, images: [IMAGE] },
    { model: 'bytefor-2.5', content, metadata: { content } },
    { model: 'bytefor-2.5', prompt: 'twice', content },
    { model: 'bytefor-2.5', content: [{ type: 'draft_task', draft_task: { id: 'cgt-1' } }] },
    { model: 'bytefor-2.5', content: [{ type: 'image_url', role: 'reference_video', image_url: { url: IMAGE } }] },
    { model: 'bytefor-2.5', videos: ['face:abc'] },
    { model: 'bytefor-2.5', reference_images: [IMAGE] },
    { model: 'bytefor-2.5', prompt: 'walk', duration: 8, metadata: { duration: 10 } },
    { model: 'bytefor-2.5' },
  ]) {
    assert.throws(() => decode(value), undefined, JSON.stringify(value));
  }
  assert.throws(() => plugin.protocols.openai_video.decodeRequest({ model: 'bytefor-2.5', body: { kind: 'multipart', fields: {}, files: [] } }), /JSON/);
});

test('bytefor-2.5 accepts audio-only input and face-library images within documented limits', () => {
  const audioOnly = driver({ model: 'bytefor-2.5', audios: [AUDIO], duration: 30 });
  assert.equal(plugin.buildSubmitRequest({ ...audioOnly, ...CREDENTIALS }).body.content[0].type, 'audio_url');
  driver({ model: 'bytefor-2.5', prompt: 'p', images: ['face:abc123', 'data:image/png;base64,AAAA'] });
  assert.throws(() => decode({ model: 'bytefor-2.5', prompt: 'p', images: Array(31).fill(IMAGE) }), /30 image/);
  assert.throws(() => decode({ model: 'bytefor-2.5', prompt: 'p', videos: Array(11).fill(VIDEO) }), /10 video/);
});

test('create and query responses map to host task states', () => {
  assert.deepEqual(plugin.parseSubmitResponse({}, { body: { id: 'TASK-1', status: 'queued', cost: 2.5 } }),
    { taskId: 'TASK-1', taskData: { id: 'TASK-1', status: 'queued', cost: 2.5 } });
  assert.deepEqual(plugin.parseSubmitResponse({}, { body: { code: 400, msg: 'invalid duration' } }), { rejected: { reason: 'invalid duration' } });
  assert.throws(() => plugin.parseSubmitResponse({}, { body: { status: 'queued' } }), /no task id/);
  assert.equal(plugin.parseSubmitResponse({}, { body: { id: 'T', status: 'failed', error: { message: 'bad' } } }).immediate.status, 'FAILURE');

  assert.deepEqual(plugin.buildQueryRequest({ ...CREDENTIALS, taskId: 'TASK 1' }),
    { url: 'https://open.bytefor.com/api/v3/contents/generations/tasks/TASK%201', method: 'GET', headers: { Authorization: 'Bearer fixture-only-key' } });
  assert.equal(plugin.parseTaskResult({}, { status: 'queued' }).status, 'QUEUED');
  assert.equal(plugin.parseTaskResult({}, { status: 'running' }).status, 'IN_PROGRESS');
  assert.equal(plugin.parseTaskResult({}, { status: 'mystery' }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult({}, {}).status, 'UNKNOWN');
  assert.deepEqual(plugin.parseTaskResult({}, { status: 'failed', error: { message: '内容违规' } }), { status: 'FAILURE', reason: '内容违规' });
  assert.equal(plugin.classifyFailure('内容违规'), 'user');
  assert.equal(plugin.classifyFailure('upstream timeout'), 'upstream');
  assert.equal(plugin.classifyFailure(plugin.parseTaskResult({}, { status: 'cancelled' }).reason), 'cancelled');

  const objectForm = { id: 'T', status: 'succeeded', video_url: 'https://cdn.example/a.mp4', content: { video_url: { url: 'https://cdn.example/a.mp4' } } };
  const stringForm = { id: 'T', status: 'succeeded', content: { video_url: 'https://cdn.example/b.mp4' } };
  assert.deepEqual(plugin.parseTaskResult({}, objectForm), { status: 'SUCCESS', progress: '100%' });
  assert.deepEqual(plugin.extractUsageOnComplete({}, {}, objectForm), {});
  for (const [data, url] of [[objectForm, 'https://cdn.example/a.mp4'], [stringForm, 'https://cdn.example/b.mp4']]) {
    assert.deepEqual(plugin.buildContentRequest({ artifactKey: 'video', data, clientRequest: { method: 'GET' } }), { url, method: 'GET', credentialless: true });
    assert.deepEqual(plugin.protocols.openai_video.render({}, { status: 'SUCCESS', data }), { video_url: url, url });
  }
  assert.throws(() => plugin.buildContentRequest({ artifactKey: 'video', data: { status: 'succeeded' } }), /no downloadable/);
  assert.deepEqual(plugin.protocols.openai_video.render({}, { status: 'FAILURE', data: { status: 'failed', error: { message: 'bad' } } }), { error: { message: 'bad' } });
});
