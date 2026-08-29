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
  buildVideoResolutionOptionUpdate,
  validateVideoResolutionPriceRows,
} from '@/features/system-settings/models/video-resolution-pricing'

// 抽屉保存分辨率价格时的契约：只发一条 VideoResolutionPrice 更新，且与后端
// Model.Update 的事务搬迁结果收敛到同一个文档。
describe('model drawer video resolution persistence', () => {
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

  test('clearing prices during a rename removes the entry the backend already moved', () => {
    // 抽屉在模型落库之后才重新拉取选项，此时后端事务已经把 video-old 搬成
    // video-new；清空操作必须把搬过来的这份也删掉，否则旧价格会在新名字上复活。
    const afterBackendMove = { 'video-new': { '720p': 0.1 } }

    const update = buildVideoResolutionOptionUpdate({
      oldName: 'video-old',
      newName: 'video-new',
      videoResolutionPrice: afterBackendMove,
      prices: {},
    })

    assert.deepEqual(JSON.parse(update.value), {})
  })

  test('an unrelated model keeps its prices across a rename', () => {
    const update = buildVideoResolutionOptionUpdate({
      oldName: 'video-old',
      newName: 'video-new',
      videoResolutionPrice: {
        'video-old': { '720p': 0.1 },
        'other-model': { '1080p': 0.18 },
      },
      prices: { '1080p': 0.2 },
    })

    assert.deepEqual(JSON.parse(update.value), {
      'other-model': { '1080p': 0.18 },
      'video-new': { '1080p': 0.2 },
    })
  })

  test('an invalid row yields no prices, so the caller must not treat it as empty', () => {
    // 这是抽屉里那条保存前置校验保护的不变量：校验失败返回 null，而 null 一旦被
    // 当成 {} 就会把整张价格表删掉。
    const invalid = validateVideoResolutionPriceRows([
      { id: 1, resolution: '720p', price: '0.1' },
      { id: 2, resolution: '1920x1080', price: '0.2' },
    ])

    assert.equal(invalid.prices, null)
    assert.notDeepEqual(invalid.prices, {})
  })
})
