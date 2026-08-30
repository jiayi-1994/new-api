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

import type { PricingModel } from '../../types'
import {
  getMinimumResolutionPrice,
  getResolutionPriceEntries,
  isPerSecondBilledModel,
  isResolutionPricedModel,
} from '../model-helpers'
import { formatFixedPrice, formatRequestPrice } from '../price'

const pricingModel = (overrides: Partial<PricingModel>): PricingModel => ({
  id: 1,
  model_name: 'video-model',
  // quota_type 1 = 固定价格（含分辨率定价）
  quota_type: 1,
  model_ratio: 0,
  completion_ratio: 0,
  enable_groups: ['default'],
  ...overrides,
})

describe('resolution price helpers', () => {
  test('uses minimum configured tier for fixed per-second summaries', () => {
    const model = pricingModel({
      resolution_prices: { '720p': 0.1, '1080p': 0.18 },
    })

    assert.equal(getMinimumResolutionPrice(model), 0.1)
    assert.equal(isResolutionPricedModel(model), true)
    assert.equal(isPerSecondBilledModel(model), true)
  })

  test('lists every resolution ordered by effective height', () => {
    const model = pricingModel({
      resolution_prices: { '4k': 0.5, '1080p': 0.18, '720p': 0.1 },
    })

    assert.deepEqual(getResolutionPriceEntries(model), [
      ['720p', 0.1],
      ['1080p', 0.18],
      ['4k', 0.5],
    ])
  })

  test('invalid resolution price values are omitted instead of rendered as zero', () => {
    const model = pricingModel({
      resolution_prices: {
        '720p': 0,
        '1080p': -1,
        '4k': Number.NaN,
        '2k': 'x' as unknown as number,
        '1440p': 0.3,
      },
    })

    assert.deepEqual(getResolutionPriceEntries(model), [['1440p', 0.3]])
    assert.equal(getMinimumResolutionPrice(model), 0.3)
  })

  test('models without resolution prices are not resolution priced', () => {
    const model = pricingModel({ model_price: 0.5 })

    assert.equal(getMinimumResolutionPrice(model), null)
    assert.equal(isResolutionPricedModel(model), false)
    assert.equal(isPerSecondBilledModel(model), false)
  })

  test('ignores a stale per_call task mode for resolution-priced models', () => {
    const model = pricingModel({
      task_billing_mode: 'per_call',
      resolution_prices: { '720p': 0.1 },
    })

    assert.equal(isPerSecondBilledModel(model), true)
  })

  // 没有分辨率表的旧版视频模型继续走历史价格与 task_billing_mode，
  // 不再被强制标记为不可用。
  test('legacy video models keep their historical billing mode', () => {
    const perCall = pricingModel({
      model_price: 0.5,
      task_billing_mode: 'per_call',
      supported_endpoint_types: ['openai-video'],
    })
    const perSecond = pricingModel({
      model_price: 0.5,
      task_billing_mode: 'per_second',
      supported_endpoint_types: ['openai-video'],
    })

    assert.equal(isResolutionPricedModel(perCall), false)
    assert.equal(isPerSecondBilledModel(perCall), false)
    assert.equal(isResolutionPricedModel(perSecond), false)
    assert.equal(isPerSecondBilledModel(perSecond), true)
  })
})

describe('resolution price formatting', () => {
  test('renders the minimum tier and honors the group multiplier', () => {
    const model = pricingModel({
      model_price: 0.18,
      resolution_prices: { '720p': 0.1, '1080p': 0.18 },
      group_ratio: { default: 2 },
    })

    assert.equal(
      formatRequestPrice(model, false, 1, 1, 'default'),
      formatRequestPrice(
        pricingModel({ model_price: 0.2, group_ratio: { default: 1 } }),
        false,
        1,
        1,
        'default'
      )
    )
  })

  test('recharge conversion applies on top of the minimum tier', () => {
    const model = pricingModel({
      resolution_prices: { '720p': 0.1, '1080p': 0.18 },
      group_ratio: { default: 1 },
    })

    // 充值汇率换算的基准必须是最低档 0.1，而不是 model_price 或最高档
    assert.equal(
      formatFixedPrice(model, 'default', true, 2, 1, { default: 1 }),
      formatFixedPrice(
        pricingModel({ model_price: 0.2, group_ratio: { default: 1 } }),
        'default',
        false,
        1,
        1,
        { default: 1 }
      )
    )
  })

  test('legacy video models format their fixed price like any request-priced model', () => {
    const legacyVideo = pricingModel({
      model_price: 0.5,
      task_billing_mode: 'per_call',
      supported_endpoint_types: ['openai-video'],
      group_ratio: { default: 1 },
    })

    assert.equal(
      formatRequestPrice(legacyVideo, false, 1, 1, 'default'),
      formatRequestPrice(
        pricingModel({ model_price: 0.5, group_ratio: { default: 1 } }),
        false,
        1,
        1,
        'default'
      )
    )
  })

  test('falls back to the legacy model_price when no resolution price is valid', () => {
    const model = pricingModel({
      model_price: 0.25,
      resolution_prices: { '720p': 0 },
      group_ratio: { default: 1 },
    })

    assert.equal(
      formatFixedPrice(model, 'default', false, 1, 1, { default: 1 }),
      formatFixedPrice(
        pricingModel({ model_price: 0.25, group_ratio: { default: 1 } }),
        'default',
        false,
        1,
        1,
        { default: 1 }
      )
    )
  })
})
