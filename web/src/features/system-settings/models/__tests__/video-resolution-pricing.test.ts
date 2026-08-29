/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  buildModelSnapshots,
  getPriceDetail,
  getPriceSummary,
  isTaskPerCallBilling,
} from '../model-pricing-snapshots'
import {
  buildVideoResolutionOptionUpdate,
  parseVideoResolutionPriceOption,
  serializeVideoResolutionPriceOption,
  validateVideoResolutionPriceRows,
} from '../video-resolution-pricing'

const snapshotInput = (
  overrides: Partial<Parameters<typeof buildModelSnapshots>[0]>
) =>
  buildModelSnapshots({
    modelPrice: '{}',
    modelRatio: '{}',
    cacheRatio: '{}',
    createCacheRatio: '{}',
    completionRatio: '{}',
    imageRatio: '{}',
    audioRatio: '{}',
    audioCompletionRatio: '{}',
    billingMode: '{}',
    billingExpr: '{}',
    taskBillingMode: '{}',
    videoResolutionPrice: '{}',
    ...overrides,
  })

describe('video resolution price rows', () => {
  test('normalizes canonical resolutions and keeps positive prices', () => {
    const result = validateVideoResolutionPriceRows([
      { id: 1, resolution: ' 1080P ', price: '0.18' },
      { id: 2, resolution: '4K', price: '0.5' },
    ])

    assert.deepEqual(result.errorsByRowId, {})
    assert.deepEqual(result.prices, { '1080p': 0.18, '4k': 0.5 })
  })

  test('rejects duplicate normalized rows', () => {
    const result = validateVideoResolutionPriceRows([
      { id: 1, resolution: ' 720P ', price: '0.10' },
      { id: 2, resolution: '720p', price: '0.20' },
    ])

    assert.equal(result.prices, null)
    assert.equal(result.errorsByRowId[2]?.resolution, 'duplicate')
  })

  test('rejects non-canonical resolutions and non-positive prices', () => {
    const result = validateVideoResolutionPriceRows([
      { id: 1, resolution: '1920x1080', price: '0.1' },
      { id: 2, resolution: 'uhd', price: '0.1' },
      { id: 3, resolution: '', price: '0.1' },
      { id: 4, resolution: '720p', price: '0' },
      { id: 5, resolution: '1080p', price: '-1' },
      { id: 6, resolution: '4k', price: 'abc' },
      { id: 7, resolution: '2k', price: '' },
    ])

    assert.equal(result.prices, null)
    assert.equal(result.errorsByRowId[1]?.resolution, 'invalid')
    assert.equal(result.errorsByRowId[2]?.resolution, 'invalid')
    assert.equal(result.errorsByRowId[3]?.resolution, 'required')
    assert.equal(result.errorsByRowId[4]?.price, 'invalid')
    assert.equal(result.errorsByRowId[5]?.price, 'invalid')
    assert.equal(result.errorsByRowId[6]?.price, 'invalid')
    assert.equal(result.errorsByRowId[7]?.price, 'required')
  })

  test('drops invalid values while parsing the stored option', () => {
    const option = parseVideoResolutionPriceOption(
      '{"sora-2":{"720p":0.1,"1080p":0,"4k":-1,"uhd":2},"empty":{"bad":0}}'
    )

    assert.deepEqual(option, { 'sora-2': { '720p': 0.1 } })
  })

  test('serializes models and resolutions in a stable numeric order', () => {
    const value = serializeVideoResolutionPriceOption({
      'video-b': { '4k': 0.5, '720p': 0.1, '1080p': 0.18 },
      'video-a': { '720p': 0.1 },
    })

    assert.deepEqual(Object.keys(JSON.parse(value)), ['video-a', 'video-b'])
    assert.deepEqual(Object.keys(JSON.parse(value)['video-b']), [
      '720p',
      '1080p',
      '4k',
    ])
  })
})

describe('video resolution option updates', () => {
  test('rename moves resolution prices without coupling TaskBillingMode', () => {
    const update = buildVideoResolutionOptionUpdate({
      oldName: 'video-old',
      newName: 'video-new',
      videoResolutionPrice: { 'video-old': { '720p': 0.1 } },
    })

    assert.equal(update.key, 'VideoResolutionPrice')
    assert.deepEqual(JSON.parse(update.value), {
      'video-new': { '720p': 0.1 },
    })
    assert.equal('TaskBillingMode' in update, false)
  })

  test('empty prices remove the model entry instead of writing an empty map', () => {
    const update = buildVideoResolutionOptionUpdate({
      oldName: 'video-a',
      newName: 'video-a',
      videoResolutionPrice: {
        'video-a': { '720p': 0.1 },
        'video-b': { '1080p': 0.2 },
      },
      prices: {},
    })

    assert.deepEqual(JSON.parse(update.value), {
      'video-b': { '1080p': 0.2 },
    })
  })
})

describe('model pricing snapshots', () => {
  test('builds video resolution snapshot without ModelPrice', () => {
    const snapshots = snapshotInput({
      videoResolutionPrice: '{"sora-2":{"720p":0.1,"1024p":0.2}}',
    })

    assert.equal(snapshots.length, 1)
    assert.equal(snapshots[0].billingMode, 'video_resolution')
    assert.deepEqual(snapshots[0].resolutionPrices, {
      '720p': 0.1,
      '1024p': 0.2,
    })
    assert.equal(snapshots[0].price, '')
  })

  test('video resolution snapshot ignores legacy per-call task mode', () => {
    const snapshots = snapshotInput({
      videoResolutionPrice: '{"sora-2":{"720p":0.1}}',
      taskBillingMode: '{"sora-2":"per_call"}',
    })

    assert.equal(snapshots[0].billingMode, 'video_resolution')
    // 管理端摘要必须按秒展示，不能被过期的 per_call 影响
    assert.equal(isTaskPerCallBilling(snapshots[0]), false)
    assert.match(
      getPriceSummary(snapshots[0], (key) => key),
      /\/ second$/
    )
    assert.match(
      getPriceDetail(snapshots[0], (key) => key),
      /Prices shown per second/
    )
  })

  test('expression pricing still wins over resolution pricing', () => {
    const snapshots = snapshotInput({
      billingMode: '{"sora-2":"tiered_expr"}',
      billingExpr: '{"sora-2":"tier(\\"base\\", p * 0 + c * 0)"}',
      videoResolutionPrice: '{"sora-2":{"720p":0.1}}',
    })

    assert.equal(snapshots[0].billingMode, 'tiered_expr')
    assert.deepEqual(snapshots[0].resolutionPrices, { '720p': 0.1 })
  })

  test('ordinary fixed-price models keep per-request mode', () => {
    const snapshots = snapshotInput({
      modelPrice: '{"video-legacy":0.5}',
      taskBillingMode: '{"video-legacy":"per_call"}',
    })

    assert.equal(snapshots[0].billingMode, 'per-request')
    assert.equal(snapshots[0].taskBillingMode, 'per_call')
    assert.equal(snapshots[0].resolutionPrices, undefined)
  })
})
