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
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import type { PricingModel } from '../../types'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
  'customElements',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { ModelCard } = await import('../model-card')
const { formatRequestPrice } = await import('../../lib/price')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const legacyVideoModel = (overrides: Partial<PricingModel>): PricingModel => ({
  id: 1,
  model_name: 'zz-video-legacy',
  // quota_type 1 = 固定价格：旧版视频模型没有 resolution_prices
  quota_type: 1,
  model_ratio: 0,
  completion_ratio: 0,
  model_price: 0.3,
  enable_groups: ['default'],
  group_ratio: { default: 1 },
  supported_endpoint_types: ['openai-video'],
  ...overrides,
})

const cleanups: Array<() => void> = []
after(() => {
  for (const cleanup of cleanups.splice(0)) cleanup()
})

function renderModelCard(model: PricingModel): HTMLElement {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <ModelCard model={model} onClick={() => {}} />
      </I18nextProvider>
    )
  })
  cleanups.push(() => {
    act(() => root.unmount())
    container.remove()
  })
  return container
}

// 旧版视频模型（有 model_price/task_billing_mode、无分辨率表）必须继续展示
// 历史价格与计费单位，而不是被强制标记为“Unsupported”。
describe('legacy video pricing card', () => {
  test('renders the legacy per-call price instead of an unsupported state', () => {
    const model = legacyVideoModel({ task_billing_mode: 'per_call' })
    const container = renderModelCard(model)
    const text = container.textContent ?? ''

    assert.equal(text.includes('Unsupported'), false)
    assert.equal(text.includes('No resolution prices configured'), false)
    assert.equal(text.includes(formatRequestPrice(model, false, 1, 1)), true)
    assert.equal(text.includes('/ request'), true)
  })

  test('renders the legacy per-second unit for per_second billing mode', () => {
    const model = legacyVideoModel({
      model_name: 'zz-video-legacy-seconds',
      task_billing_mode: 'per_second',
    })
    const container = renderModelCard(model)
    const text = container.textContent ?? ''

    assert.equal(text.includes('Unsupported'), false)
    assert.equal(text.includes(formatRequestPrice(model, false, 1, 1)), true)
    assert.equal(text.includes('/ second'), true)
  })
})
