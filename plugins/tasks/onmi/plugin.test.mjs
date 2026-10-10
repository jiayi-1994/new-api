import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as plugin from './plugin.js';

const request = {
  model: 'e组-sd2.0mini',
  prompt: '保持 @Image1 中主体的外观，参考 @Video1 的运镜和 @Audio1 的节奏，拍摄海边日落。',
  seconds: 10,
  resolution: '480p',
  aspect_ratio: '16:9',
  generate_audio: true,
  references: [
    { type: 'image', url: 'https://media.example.com/subject.png' },
    { type: 'video', url: 'https://media.example.com/motion.mp4' },
    { type: 'audio', url: 'https://media.example.com/music.mp3' },
  ],
};
const credentials = { baseUrl: plugin.meta.baseUrl, apiKey: 'fixture-only-key', publicTaskId: 'task_fixture_1' };
function context(value = request, upstreamModel = value.model) {
  const intent = plugin.protocols.openai_video.decodeRequest({ model: value.model, body: { kind: 'json', value } });
  return { ...credentials, model: intent.model, upstreamModel, requestBody: intent.requestBody };
}

test('User example preserves prompt, typed reference order and audio setting', () => {
  const ctx = context();
  const submitted = plugin.buildSubmitRequest(ctx);
  assert.deepEqual(submitted.body, request);
  assert.equal(submitted.url, 'https://api.onmi.eu.cc/v1/videos');
  assert.equal(submitted.method, 'POST');
  assert.equal(submitted.headers.Authorization, 'Bearer fixture-only-key');
  assert.equal(submitted.headers['Idempotency-Key'], 'task_fixture_1');
  assert.deepEqual(plugin.buildSubmitRequest(ctx), submitted);
  assert.notEqual(plugin.buildSubmitRequest({ ...ctx, publicTaskId: 'task_fixture_2' }).headers['Idempotency-Key'], submitted.headers['Idempotency-Key']);
  assert.throws(() => plugin.buildSubmitRequest({ ...ctx, publicTaskId: '' }), /idempotency/);
  assert.equal(plugin.buildSubmitRequest(context({ ...request, generate_audio: false })).body.generate_audio, false);
  const { generate_audio, ...withoutAudio } = request;
  assert.equal(plugin.buildSubmitRequest(context(withoutAudio)).body.generate_audio, true);
});

test('Mapped aliases retain their public identity and obey the final model limits', () => {
  const value = { ...request, model: 'public-video' };
  const ctx = context(value, 'e组-sd2.0mini');
  assert.equal(ctx.model, 'public-video');
  const submitted = plugin.buildSubmitRequest(ctx);
  assert.equal(submitted.body.model, 'e组-sd2.0mini');
  assert.equal(submitted.model, undefined);
  for (const hook of ['buildSubmitRequest', 'extractUsage', 'describeSpec']) {
    assert.throws(() => plugin[hook]({ ...ctx, upstreamModel: 'd组-sd2.0mini' }), /unsupported upstream/);
    assert.throws(() => plugin[hook]({ ...ctx, requestBody: { ...ctx.requestBody, seconds: 16 } }), /limit of 15/);
    assert.throws(() => plugin[hook]({ ...ctx, requestBody: { ...ctx.requestBody, resolution: '1080p' } }), /only 480p and 720p/);
    assert.throws(() => plugin[hook]({ ...ctx, requestBody: { ...ctx.requestBody, seconds: Infinity } }), /seconds/);
  }
});

test('All E models enforce documented duration and resolution boundaries', () => {
  for (const model of plugin.meta.models) {
    const large = model === 'e组-sd2.5';
    for (const seconds of [4, large ? 30 : 15]) {
      for (const resolution of large ? ['480p', '720p', '1080p'] : ['480p', '720p']) {
        const ctx = context({ ...request, model, seconds, resolution });
        assert.equal(plugin.buildSubmitRequest(ctx).body.seconds, seconds);
        assert.equal(plugin.extractUsage(ctx).output_seconds, seconds);
        assert.equal(plugin.describeSpec(ctx).resolution, resolution);
      }
    }
  }
  assert.equal(plugin.buildSubmitRequest(context({ ...request, model: 'e组-sd2.5', seconds: 17 })).body.seconds, 17);
  for (const seconds of [0, -1, 3, 31, 4.5, NaN, Infinity, true, null, '', '18446744073686646784']) {
    assert.throws(() => context({ ...request, seconds }), /seconds/);
  }
});

test('Standard video parameters become typed references consistently across submission and scheduling', () => {
  const value = {
    model: 'seedance-2.0', prompt: request.prompt, seconds: 15, resolution: '720p', ratio: '16:9', n: 1,
    images: ['https://media.example.com/first.png', 'https://media.example.com/second.png'],
    videos: ['https://media.example.com/motion.mp4'], audios: ['https://media.example.com/music.mp3'],
  };
  const original = structuredClone(value);
  const expected = {
    model: 'e组-sd2.0', prompt: value.prompt, seconds: 15, resolution: '720p', aspect_ratio: '16:9', generate_audio: true,
    references: [
      { type: 'image', url: value.images[0] }, { type: 'image', url: value.images[1] },
      { type: 'video', url: value.videos[0] }, { type: 'audio', url: value.audios[0] },
    ],
  };
  const ctx = context(value, expected.model);
  assert.equal(ctx.model, value.model);
  assert.deepEqual(plugin.buildSubmitRequest(ctx).body, expected);
  assert.deepEqual(plugin.buildSubmitRequest({ ...ctx, requestBody: value }).body, expected);
  assert.deepEqual(plugin.describeSpec(ctx), {
    spec_version: 2, output_seconds: 15, seconds_kind: 'exact', resolution: '720p',
    references: { image: 2, video: 1, audio: 1 }, reference_video_urls: value.videos,
  });
  assert.deepEqual(plugin.extractUsage(ctx), { output_seconds: 15, resolution: '720p', video_input: true });
  assert.deepEqual(value, original);
});

test('Standard reference arrays preserve repeated inputs and share native reference limits', () => {
  const value = { ...request, images: [request.references[0].url], videos: [request.references[1].url], audios: [] };
  const ctx = context(value);
  assert.deepEqual(plugin.buildSubmitRequest(ctx).body.references, [
    ...request.references, { type: 'image', url: request.references[0].url }, { type: 'video', url: request.references[1].url },
  ]);
  assert.deepEqual(plugin.describeSpec(ctx).reference_video_urls, [request.references[1].url, request.references[1].url]);
  assert.equal(value.references.length, 3);
  assert.throws(() => plugin.describeSpec(context({ ...request, videos: Array(3).fill(request.references[1].url) })), /too many video/);
  assert.throws(() => plugin.buildSubmitRequest(context({ ...request, references: [], audios: [request.references[2].url] })), /require an image or video/);
});

test('Standard reference arrays and single-output count reject malformed or unsupported values', () => {
  for (const field of ['images', 'videos', 'audios']) {
    for (const invalid of [null, false, 'https://media.example.com/a', {}, [null], [1], [{ url: request.references[0].url }]]) {
      assert.throws(() => context({ ...request, [field]: invalid }), /array of.*URLs/);
    }
    for (const url of ['file:///tmp/a', 'http://127.0.0.1/a', 'https://user:secret@host.example/a']) {
      assert.throws(() => context({ ...request, [field]: [url] }), /URL/);
    }
  }
  for (const n of [0, -1, 2, 1.5, '1', true, null]) {
    const value = { ...request, n };
    assert.throws(() => context(value), /n must be 1/);
    for (const hook of ['buildSubmitRequest', 'extractUsage', 'describeSpec']) {
      assert.throws(() => plugin[hook]({ ...credentials, model: request.model, requestBody: value }), /n must be 1/);
    }
  }
});

test('Unsupported E modes and conflicting aliases fail instead of being dropped', () => {
  for (const name of ['with_sound_effects', 'no_music', 'first_image_url', 'last_image_url', 'first_image', 'last_image', 'extend', 'parameters', 'metadata']) {
    assert.throws(() => context({ ...request, [name]: false }), /not supported/);
  }
  for (const fields of [{ duration: 5 }, { ratio: '1:1' }, { size: '720p' }]) assert.throws(() => context({ ...request, ...fields }), /conflict/);
  assert.throws(() => context({ ...request, generate_audio: 'false' }), /boolean/);
  assert.throws(() => context({ ...request, resolution: undefined }), /required/);
  assert.throws(() => context({ ...request, references: null }), /references/);
  assert.throws(() => plugin.protocols.openai_video.decodeRequest({ body: { kind: 'multipart', files: [] } }), /JSON/);
  assert.throws(() => plugin.protocols.openai_video.decodeRequest({ model: 'different', body: { kind: 'json', value: request } }), /does not match/);
});

test('E supports video-only references and 2.5 supports audio-only references', () => {
  assert.equal(plugin.buildSubmitRequest(context({ ...request, references: [request.references[1]] })).body.references.length, 1);
  assert.throws(() => plugin.buildSubmitRequest(context({ ...request, references: [request.references[2]] })), /require an image or video/);
  assert.equal(plugin.buildSubmitRequest(context({ ...request, model: 'e组-sd2.5', references: [request.references[2]] })).body.references.length, 1);
  const textOnly = { ...request, references: undefined };
  assert.deepEqual(plugin.buildSubmitRequest(context(textOnly)).body.references, []);
});

test('Reference counts, duration metadata and names obey each model capability', () => {
  for (const model of plugin.meta.models) {
    for (const [type, limit] of Object.entries(model === 'e组-sd2.5' ? { image: 30, video: 10, audio: 10 } : { image: 9, video: 3, audio: 3 })) {
      const items = Array.from({ length: limit }, () => request.references.find(item => item.type === type));
      if (type === 'audio') items.unshift(request.references[0]);
      const ctx = context({ ...request, model, references: items });
      assert.equal(plugin.describeSpec(ctx).references[type], limit);
      assert.throws(() => plugin.buildSubmitRequest(context({ ...request, model, references: [...items, items.at(-1)] })), /too many/);
    }
  }
  const reference = { ...request.references[1], name: '运镜', duration: 2.5 };
  assert.deepEqual(plugin.buildSubmitRequest(context({ ...request, references: [reference] })).body.references, [reference]);
  for (const duration of [0, 1, 16, NaN, Infinity, '5']) {
    assert.throws(() => plugin.buildSubmitRequest(context({ ...request, references: [{ ...reference, duration }] })), /duration/);
  }
  assert.throws(() => plugin.buildSubmitRequest(context({ ...request, references: [{ ...reference, duration: 8 }, { ...reference, duration: 8 }] })), /total video/);
});

test('Malformed, credentialed and unsupported reference URLs are rejected', () => {
  for (const url of ['data:video/mp4;base64,AA', 'file:///tmp/a', 'https://user:secret@host.example/a', 'https://host.example:8443/a', 'http://localhost/a', 'http://127.0.0.1/a', 'https://host.example/a b', 'https://host.example\\evil/a', '[https://host.example/a](https://host.example/a)']) {
    assert.throws(() => context({ ...request, references: [{ type: 'video', url }] }), /URL/);
  }
});

test('Scheduling preserves repeated URLs; untrusted metadata never becomes billing usage', () => {
  const references = [{ ...request.references[1], duration: 2 }, { ...request.references[1], duration: 3 }];
  const ctx = context({ ...request, references });
  assert.deepEqual(plugin.describeSpec(ctx), {
    spec_version: 2, output_seconds: 10, seconds_kind: 'exact', resolution: '480p',
    references: { image: 0, video: 2, audio: 0 }, reference_video_urls: references.map(item => item.url),
  });
  assert.deepEqual(plugin.extractUsage(ctx), { output_seconds: 10, resolution: '480p', video_input: true });
  assert.deepEqual(plugin.extractUsageOnComplete({}, { status: 'SUCCESS' }, { seconds: 99999, duration: 0, usage: { seconds: 5 } }), {});
});

test('Polling maps documented states and unknown states remain poll failures', () => {
  for (const [status, expected] of Object.entries({ queued: 'QUEUED', in_progress: 'IN_PROGRESS', completed: 'SUCCESS', failed: 'FAILURE' })) assert.equal(plugin.parseTaskResult({}, { status }).status, expected);
  for (const body of [null, [], {}, { status: 'new_state' }, { status: 'toString' }]) assert.equal(plugin.parseTaskResult({}, body).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult({}, { status: 'in_progress', progress: 32 }).progress, '32%');
  assert.equal(plugin.parseTaskResult({}, { status: 'in_progress', progress: 101 }).progress, undefined);
});

test('Explicit rejection, ambiguous submission and accepted terminal failure stay distinct', () => {
  assert.deepEqual(plugin.parseSubmitResponse({}, { body: { error: { message: 'quota exhausted' } } }), { rejected: { reason: 'quota exhausted' } });
  for (const body of [{}, [], { id: 1 }, { id: 'a', error: 'bad' }, { status: 'completed', error: 'bad' }, { status: 'in_progress', success: false }]) assert.throws(() => plugin.parseSubmitResponse({}, { body }));
  for (const status of ['failed', 'completed', 'queued']) {
    const body = { id: 'upstream-1', status, ...(status === 'failed' ? { error: { message: 'rejected' } } : {}) };
    const parsed = plugin.parseSubmitResponse({}, { body });
    assert.equal(parsed.taskId, 'upstream-1');
    assert.equal(parsed.immediate?.status, status === 'failed' ? 'FAILURE' : status === 'completed' ? 'SUCCESS' : undefined);
  }
});

test('Content uses canonical authenticated endpoint and never trusts response URLs', () => {
  for (const baseUrl of ['https://api.onmi.eu.cc', 'https://api.onmi.eu.cc/v1/']) {
    for (const authHeader of ['fixture-only-key', 'Bearer fixture-only-key']) {
      const ctx = { ...credentials, baseUrl, authHeader, taskId: 'id/1', upstreamTaskId: 'id/1', artifactKey: 'video', data: { url: 'https://untrusted.example/file' }, clientRequest: { method: 'HEAD' } };
      const query = plugin.buildQueryRequest(ctx);
      assert.equal(query.url, 'https://api.onmi.eu.cc/v1/videos/id%2F1');
      assert.equal(query.headers.Authorization, 'Bearer fixture-only-key');
      assert.deepEqual(plugin.buildContentRequest(ctx), { url: query.url + '/content', method: 'HEAD', headers: query.headers });
      assert.equal(plugin.buildSubmitRequest({ ...context(), baseUrl, authHeader }).url, 'https://api.onmi.eu.cc/v1/videos');
    }
  }
  assert.deepEqual(plugin.listArtifacts({ status: 'SUCCESS' }), [{ key: 'video', type: 'video', mimeType: 'video/mp4' }]);
  assert.deepEqual(plugin.listArtifacts({ status: 'FAILURE' }), []);
  assert.throws(() => plugin.buildContentRequest({ artifactKey: 'other' }), /artifact_not_found/);
  assert.deepEqual(plugin.protocols.openai_video.render({}, { status: 'SUCCESS', data: { id: 'private', url: '/signed-secret', usage: { price: 42 } } }), {});
});

test('Failure attribution honors gateway codes and preserves upstream service failures', () => {
  for (const [code, kind] of [['video_request_rejected', 'user'], ['video_generation_cancelled', 'cancelled'], ['video_generation_failed', 'upstream']]) {
    const result = plugin.parseTaskResult({}, { status: 'failed', error: { code, message: 'Video generation failed' } });
    assert.equal(plugin.classifyFailure(result.reason), kind);
  }
  for (const [reason, expected] of [['content moderation rejected the prompt', 'user'], ['内容违规', 'user'], ['task cancelled by user', 'cancelled'], ['moderation service timeout', 'upstream'], ['quota exhausted', 'upstream']]) assert.equal(plugin.classifyFailure(reason), expected);
});
