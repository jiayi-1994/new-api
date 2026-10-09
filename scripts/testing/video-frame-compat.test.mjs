import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as bytefor from '../../plugins/tasks/bytefor/plugin.js';
import * as cangyuan from '../../plugins/tasks/cangyuan/plugin.js';
import * as meaicc from '../../plugins/tasks/meaicc/plugin.js';
import * as sudashui from '../../plugins/tasks/sudashui/plugin.js';
import * as gateway from '../../plugins/tasks/seedance-hjmie/plugin.js';
import * as pidoi from '../../plugins/tasks/pidoi/plugin.js';
import * as mega from '../../plugins/tasks/megabyai/plugin.js';

const first = 'https://cdn.example/first.png';
const last = 'https://cdn.example/last.png';
const pairs = [{ first_image_url: first, last_image_url: last }, { first_image: first, last_image: last }];
const cases = [
  [bytefor, 'bytefor-2.0-real-priority', body => body.content.filter(item => item.type === 'image_url').map(item => [item.role, item.image_url.url])],
  [cangyuan, 'sd11-seedance-2.0', body => [['first_frame', body.first_image_url], ['last_frame', body.last_image_url]]],
  [meaicc, 'sd-2-c1', body => body.input.media.map(item => [item.type, item.url])],
  [sudashui, 'sdas-qd-seedance-2.0-720p', body => { const p = JSON.parse(body.metadata.payload); assert.equal(p.mode, 'frames'); return [['first_frame', p.firstFrameUrl], ['last_frame', p.lastFrameUrl]]; }],
  [gateway, 'seedance-2.0', body => [['first_frame', body.first_image_url], ['last_frame', body.last_image_url]]],
];

function decode(plugin, model, extra, kind = 'json', upstreamModel) {
  const value = { model, prompt: 'A smooth transition', seconds: 5, resolution: '720p', ...extra };
  const body = kind === 'json' ? { kind, value } : { kind, fields: Object.fromEntries(Object.entries(value).map(([key, value]) => [key, [typeof value === 'string' ? value : JSON.stringify(value)]])) };
  return plugin.protocols.openai_video.decodeRequest({ model, upstreamModel, body });
}

for (const [plugin, model, frames] of cases) {
  test(`${plugin.meta.key}: supported request formats preserve frame roles, spec and existing usage`, () => {
    for (const kind of plugin === bytefor ? ['json'] : ['json', 'multipart']) {
      for (const pair of [...pairs, { ...pairs[0], ...pairs[1] }]) {
        const intent = decode(plugin, model, pair, kind);
        const ctx = { model, upstreamModel: model, requestBody: intent.requestBody, baseUrl: 'https://upstream.example', apiKey: 'fixture-only-key' };
        const snapshot = JSON.stringify(ctx);
        const wire = plugin.buildSubmitRequest(ctx).body;
        assert.deepEqual(frames(wire), [['first_frame', first], ['last_frame', last]]);
        assert.deepEqual(plugin.describeSpec(ctx).references, { image: 0, video: 0, audio: 0, frame: 2 });
        assert.deepEqual(plugin.extractUsage(ctx), plugin.extractUsage({ ...ctx, requestBody: decode(plugin, model, {}).requestBody }));
        assert.equal(intent.action, 'reference_to_video');
        assert.equal(JSON.stringify(ctx), snapshot);
        assert.deepEqual(plugin.protocols.openai_video.decodeRequest({ model, body: { kind: 'json', value: { model, ...intent.requestBody } } }).requestBody, intent.requestBody);
      }
    }
  });
  test(`${plugin.meta.key}: conflicting and invalid frame aliases fail before submission`, () => {
    for (const extra of [
      { ...pairs[0], first_image: last },
      ...[null, '', [], [first], {}, 5, 'file:///tmp/image.png', 'https://user:pass@cdn.example/image.png'].map(first_image => ({ ...pairs[0], first_image })),
    ]) assert.throws(() => decode(plugin, model, extra));
  });
}

test('public gateway output remains compatible with all supported provider decoders', () => {
  const body = gateway.buildSubmitRequest({ model: 'seedance-2.0', requestBody: decode(gateway, 'seedance-2.0', pairs[1]).requestBody, baseUrl: 'https://poxiaoapi001.com', apiKey: 'fixture-only-key' }).body;
  for (const [plugin, upstreamModel, frames] of cases.slice(0, -1)) {
    const intent = plugin.protocols.openai_video.decodeRequest({ model: body.model, upstreamModel, body: { kind: 'json', value: body } });
    const ctx = { model: body.model, upstreamModel, requestBody: intent.requestBody, baseUrl: 'https://upstream.example', apiKey: 'fixture-only-key', salesSource: 'video_request' };
    assert.deepEqual(frames(plugin.buildSubmitRequest(ctx).body), [['first_frame', first], ['last_frame', last]]);
    assert.equal(plugin.describeSpec(ctx).references.frame, 2);
  }
});

test('unsupported providers and mapped Cangyuan models cannot silently discard frames', () => {
  for (const pair of pairs) {
    for (const kind of ['json', 'multipart']) {
      assert.throws(() => decode(mega, 'seedance-2.0', pair, kind), /unsupported/);
      assert.throws(() => decode(pidoi, 'tejiasd-mini-720p', pair, kind), /not supported/);
    }
    const intent = decode(cangyuan, 'seedance-2.0', pair);
    assert.throws(() => cangyuan.describeSpec({ model: 'seedance-2.0', upstreamModel: 'sd13-seedance-2.0', requestBody: intent.requestBody }), /does not support/);
  }
});

test('matching native roles are deduplicated and contradictory native roles are rejected', () => {
  const ark = [{ type: 'image_url', role: 'first_frame', image_url: { url: first } }];
  const media = [{ type: 'first_frame', url: first }];
  assert.equal(decode(bytefor, cases[0][1], { ...pairs[0], content: ark }).requestBody.content.length, 2);
  assert.equal(decode(meaicc, cases[2][1], { ...pairs[0], media }).requestBody.input.media.length, 2);
  assert.throws(() => decode(bytefor, cases[0][1], { ...pairs[0], content: [...ark, ...ark] }), /conflicts/);
  assert.throws(() => decode(bytefor, cases[0][1], { ...pairs[0], content: [{ ...ark[0], image_url: { url: last } }] }), /conflicts/);
  assert.throws(() => decode(meaicc, cases[2][1], { ...pairs[0], media: [{ type: 'first_frame', url: last }] }), /conflicts/);
  assert.throws(() => decode(sudashui, cases[3][1], { ...pairs[0], firstFrameUrl: last }), /conflicts/);
  assert.throws(() => decode(sudashui, cases[3][1], { ...pairs[0], mode: 'references' }), /cannot include frame/);
});

test('frame-only restrictions and media limits remain enforced', () => {
  assert.throws(() => decode(gateway, 'seedance-2.0', { last_image: last }), /requires/);
  assert.throws(() => decode(cangyuan, 'sd11-seedance-2.0', { last_image: last }), /requires/);
  assert.throws(() => decode(cangyuan, 'sd14-seedance-2.0', { ...pairs[1], images: [first] }), /cannot be combined/);
  assert.throws(() => decode(sudashui, cases[3][1], { first_image: first }), /requires/);
  assert.throws(() => decode(sudashui, cases[3][1], { ...pairs[0], images: [first] }), /cannot include reference/);
  assert.throws(() => decode(bytefor, cases[0][1], { ...pairs[0], images: Array(29).fill(first) }), /at most 30/);
});
