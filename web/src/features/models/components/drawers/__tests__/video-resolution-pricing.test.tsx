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
import { after, beforeEach, describe, test } from 'node:test'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import {
  buildVideoResolutionOptionUpdate,
  validateVideoResolutionPriceRows,
} from '@/features/system-settings/models/video-resolution-pricing'

// @ts-expect-error Bun exposes module mocks at runtime without installed types.
const { mock, spyOn } = await import('bun:test')

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
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
const { api } = await import('@/lib/api')
const { ROLE } = await import('@/lib/roles')
const { useAuthStore } = await import('@/stores/auth-store')
const drawerModule = await import('../model-mutate-drawer')
const { ModelMutateDrawer } = drawerModule
const { createExtendedModelFormSchema } =
  await import('../../../lib/model-mutate-schema')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: { translation: {} },
    zh: {
      translation: {
        'Please enter a valid number': '请输入有效的数值',
      },
    },
  },
})

const modelFixture = {
  id: 1,
  model_name: 'video',
  description: '',
  icon: '',
  tags: '',
  vendor_id: undefined,
  endpoints: '',
  status: 1,
  sync_official: 1,
  created_time: 1,
  updated_time: 1,
  name_rule: 0,
}

const pricingOptions = [
  { key: 'ModelPrice', value: '{"video":0.3}' },
  { key: 'ModelRatio', value: '{"video":1.5}' },
  { key: 'CacheRatio', value: '{}' },
  { key: 'CreateCacheRatio', value: '{"video":1.25}' },
  { key: 'CompletionRatio', value: '{}' },
  { key: 'ImageRatio', value: '{}' },
  { key: 'AudioRatio', value: '{}' },
  { key: 'AudioCompletionRatio', value: '{}' },
  { key: 'billing_setting.billing_mode', value: '{}' },
  { key: 'billing_setting.billing_expr', value: '{}' },
  { key: 'TaskBillingMode', value: '{"video":"per_call"}' },
  { key: 'VideoResolutionPrice', value: '{"video":{"720p":0.1}}' },
]

const putCalls: Array<{ url: string; body: unknown }> = []
const postCalls: Array<{ url: string; body: unknown }> = []
let putResponder = async () => ({
  data: { success: true, data: modelFixture },
})
let postResponder = async () => ({
  data: { success: true, data: modelFixture },
})
spyOn(api, 'get').mockImplementation((async (url: string) => {
  if (url === '/api/option/') {
    return { data: { success: true, message: '', data: pricingOptions } }
  }
  if (url === '/api/models/1') {
    return { data: { success: true, data: modelFixture } }
  }
  if (url === '/api/vendors/') {
    return { data: { success: true, data: { items: [] } } }
  }
  throw new Error(`unexpected GET ${url}`)
}) as typeof api.get)
spyOn(api, 'put').mockImplementation((async (url: string, body: unknown) => {
  putCalls.push({ url, body })
  return putResponder()
}) as typeof api.put)
spyOn(api, 'post').mockImplementation((async (url: string, body: unknown) => {
  postCalls.push({ url, body })
  return postResponder()
}) as typeof api.post)

function changeInputValue(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(
    new domWindow.Event('input', { bubbles: true }) as unknown as Event
  )
}

function changeTextareaValue(input: HTMLTextAreaElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLTextAreaElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(
    new domWindow.Event('input', { bubbles: true }) as unknown as Event
  )
}

function setRole(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role })
}

async function renderDrawer(
  currentRow: typeof modelFixture | null = modelFixture
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  queryClient.setQueryData(['system-options'], {
    success: true,
    message: '',
    data: pricingOptions,
  })
  queryClient.setQueryData(['models', 'detail', 1], {
    success: true,
    data: modelFixture,
  })
  queryClient.setQueryData(['vendors', 'list', undefined], {
    success: true,
    data: { items: [] },
  })
  const render = () =>
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <ModelMutateDrawer
            open
            onOpenChange={() => undefined}
            currentRow={currentRow}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
  await act(async () => {
    render()
  })
  return {
    queryClient,
    remount: async () => {
      await act(async () => root.render(null))
      await act(async () => render())
    },
    cleanup: async () => {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
    },
  }
}

const resolutionPriceInput = () =>
  document.querySelector<HTMLInputElement>('#video-resolution-price-1')

const submitButton = () =>
  [...document.querySelectorAll<HTMLButtonElement>('button')].find((button) =>
    /(?:Update Model|Save changes)/.test(button.textContent || '')
  )

beforeEach(async () => {
  putCalls.length = 0
  postCalls.length = 0
  putResponder = async () => ({
    data: { success: true, data: modelFixture },
  })
  postResponder = async () => ({
    data: { success: true, data: modelFixture },
  })
  setRole(ROLE.SUPER_ADMIN)
  await i18n.changeLanguage('en')
})

after(() => {
  useAuthStore.getState().auth.reset()
  mock.restore()
  domWindow.close()
})

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

  test('submits metadata and changed resolution pricing in one model request', async () => {
    const view = await renderDrawer()
    assert.equal(
      document.querySelector<HTMLInputElement>('input[name="ratio"]'),
      null
    )
    const input = resolutionPriceInput()
    assert.ok(input)
    await act(async () => {
      changeInputValue(input, '0.2')
    })
    const button = submitButton()
    assert.ok(button)
    await act(async () => {
      button.click()
    })

    assert.equal(putCalls.length, 1)
    assert.equal(putCalls[0].url, '/api/models/')
    assert.deepEqual((putCalls[0].body as { pricing?: unknown }).pricing, {
      mode: 'video_resolution',
      resolution_prices: { '720p': 0.2 },
    })
    assert.equal(
      putCalls.some((call) => call.url === '/api/option/'),
      false
    )
    await view.cleanup()
  })

  test('publication pending adopts committed documents before the drawer is reopened', async () => {
    const committedPricingOptions = Object.fromEntries(
      pricingOptions.map((option) => [option.key, option.value])
    )
    committedPricingOptions.VideoResolutionPrice =
      '{ "video": { "1080p": 0.25 } }'
    putResponder = async () => ({
      data: {
        success: true,
        data: modelFixture,
        committed: true,
        publication_pending: true,
        pricing_documents: committedPricingOptions,
      },
    })
    const view = await renderDrawer()
    try {
      const input = resolutionPriceInput()
      assert.ok(input)
      await act(async () => {
        changeInputValue(input, '0.2')
      })
      const button = submitButton()
      assert.ok(button)
      await act(async () => {
        button.click()
      })
      await view.remount()

      const cached = view.queryClient.getQueryData<{
        data: Array<{ key: string; value: string }>
      }>(['system-options'])
      assert.deepEqual(
        Object.fromEntries(
          cached?.data
            .filter((option) => option.key in committedPricingOptions)
            .map((option) => [option.key, option.value]) ?? []
        ),
        committedPricingOptions
      )
      const reopenedPrice = resolutionPriceInput()
      assert.ok(reopenedPrice)
      assert.equal(reopenedPrice.value, '0.25')
    } finally {
      await view.cleanup()
    }
  })

  test('omits pricing for an untouched same-name metadata save', async () => {
    const view = await renderDrawer()
    const iconInput =
      document.querySelector<HTMLInputElement>('input[name="icon"]')
    assert.ok(iconInput)
    await act(async () => {
      changeInputValue(iconInput, 'video-icon')
    })
    const button = submitButton()
    assert.ok(button)
    await act(async () => {
      button.click()
    })

    assert.equal(putCalls.length, 1)
    assert.equal(Object.hasOwn(putCalls[0].body as object, 'pricing'), false)
    await view.cleanup()
  })

  test('omits pricing during an untouched rename so the backend moves all documents', async () => {
    const view = await renderDrawer()
    const nameInput = document.querySelector<HTMLInputElement>(
      'input[name="model_name"]'
    )
    assert.ok(nameInput)
    await act(async () => {
      changeInputValue(nameInput, 'video-renamed')
    })
    const button = submitButton()
    assert.ok(button)
    await act(async () => {
      button.click()
    })

    assert.equal(putCalls.length, 1)
    assert.equal(Object.hasOwn(putCalls[0].body as object, 'pricing'), false)
    assert.equal(
      (putCalls[0].body as { model_name?: string }).model_name,
      'video-renamed'
    )
    await view.cleanup()
  })

  test('does not submit when a resolution row is invalid', async () => {
    const view = await renderDrawer()
    const input = resolutionPriceInput()
    assert.ok(input)
    await act(async () => {
      changeInputValue(input, '0')
    })
    const button = submitButton()
    assert.ok(button)
    await act(async () => {
      button.click()
    })

    assert.equal(putCalls.length, 0)
    await view.cleanup()
  })

  test('does not submit when the resolution table is empty', async () => {
    const view = await renderDrawer()
    const removeButton = document.querySelector<HTMLButtonElement>(
      'button[aria-label="Remove resolution: 720p"]'
    )
    assert.ok(removeButton)
    await act(async () => {
      removeButton.click()
    })
    const button = submitButton()
    assert.ok(button)
    await act(async () => {
      button.click()
    })

    assert.equal(putCalls.length, 0)
    await view.cleanup()
  })

  for (const invalidValue of [
    '1x',
    'Infinity',
    '   ',
    '',
    '.',
    '1.',
    '-1',
    '1e2',
  ]) {
    test(`does not submit a destructive ratio mutation for ${JSON.stringify(invalidValue)}`, async () => {
      const view = await renderDrawer()
      try {
        const perTokenRadio = document.querySelector<HTMLElement>('#per-token')
        assert.ok(perTokenRadio)
        await act(async () => {
          perTokenRadio.click()
        })
        const ratioInput = document.querySelector<HTMLInputElement>(
          'input[name="ratio"]'
        )
        assert.ok(ratioInput)
        await act(async () => {
          changeInputValue(ratioInput, invalidValue)
        })
        const button = submitButton()
        assert.ok(button)
        await act(async () => {
          button.click()
        })

        assert.equal(putCalls.length, 0)
        assert.match(
          document.body.textContent || '',
          /Please enter a valid number/
        )
      } finally {
        await view.cleanup()
      }
    })
  }

  test('does not create a model when the selected ratio is incomplete', async () => {
    const view = await renderDrawer(null)
    try {
      const nameInput = document.querySelector<HTMLInputElement>(
        'input[name="model_name"]'
      )
      const ratioInput = document.querySelector<HTMLInputElement>(
        'input[name="ratio"]'
      )
      assert.ok(nameInput)
      assert.ok(ratioInput)
      await act(async () => {
        changeInputValue(nameInput, 'new-video')
        changeInputValue(ratioInput, '1.')
      })
      const button = submitButton()
      assert.ok(button)
      await act(async () => {
        button.click()
      })

      assert.equal(postCalls.length, 0)
    } finally {
      await view.cleanup()
    }
  })

  test('does not update when a populated advanced ratio is incomplete', async () => {
    const view = await renderDrawer()
    try {
      const perTokenRadio = document.querySelector<HTMLElement>('#per-token')
      assert.ok(perTokenRadio)
      await act(async () => {
        perTokenRadio.click()
      })
      const advancedButton = [
        ...document.querySelectorAll<HTMLButtonElement>('button'),
      ].find((button) => button.textContent?.includes('Advanced options'))
      assert.ok(advancedButton)
      await act(async () => {
        advancedButton.click()
      })
      const cacheRatioInput = document.querySelector<HTMLInputElement>(
        'input[name="cacheRatio"]'
      )
      assert.ok(cacheRatioInput)
      await act(async () => {
        changeInputValue(cacheRatioInput, '1.')
      })
      const button = submitButton()
      assert.ok(button)
      await act(async () => {
        button.click()
      })

      assert.equal(putCalls.length, 0)
    } finally {
      await view.cleanup()
    }
  })

  test('localizes numeric validation errors in the active language', async () => {
    await i18n.changeLanguage('zh')
    const view = await renderDrawer()
    try {
      const perTokenRadio = document.querySelector<HTMLElement>('#per-token')
      assert.ok(perTokenRadio)
      await act(async () => {
        perTokenRadio.click()
      })
      const ratioInput = document.querySelector<HTMLInputElement>(
        'input[name="ratio"]'
      )
      assert.ok(ratioInput)
      await act(async () => {
        changeInputValue(ratioInput, '1x')
      })
      const button = submitButton()
      assert.ok(button)
      await act(async () => {
        button.click()
      })

      assert.equal(putCalls.length, 0)
      assert.match(document.body.textContent || '', /请输入有效的数值/)
      assert.doesNotMatch(
        document.body.textContent || '',
        /Please enter a valid number/
      )
    } finally {
      await view.cleanup()
    }
  })

  test('builds numeric validation with the active translator', () => {
    const result = createExtendedModelFormSchema((key) =>
      key === 'Please enter a valid number' ? '请输入有效的数值' : key
    ).safeParse({
      model_name: 'video',
      description: '',
      icon: '',
      tags: [],
      endpoints: '',
      name_rule: 0,
      status: true,
      sync_official: true,
      ratio: '1x',
    })
    assert.equal(result.success, false)
    assert.ok(
      result.error?.issues.some((issue) => issue.message === '请输入有效的数值')
    )
  })

  test('keeps schema factories out of the React component module', () => {
    assert.equal('createExtendedModelFormSchema' in drawerModule, false)
  })

  test('ordinary admins can save metadata but cannot rename or mutate pricing', async () => {
    setRole(ROLE.ADMIN)
    const view = await renderDrawer()
    try {
      const nameInput = document.querySelector<HTMLInputElement>(
        'input[name="model_name"]'
      )
      const descriptionInput = document.querySelector<HTMLTextAreaElement>(
        'textarea[name="description"]'
      )
      assert.ok(nameInput)
      assert.ok(descriptionInput)
      assert.equal(nameInput.disabled, true)
      assert.equal(document.body.textContent?.includes('Pricing mode'), false)
      assert.match(
        document.body.textContent || '',
        /Only super administrators can rename models or change model pricing\./
      )
      await act(async () => {
        changeInputValue(nameInput, 'forbidden-rename')
        changeTextareaValue(descriptionInput, 'metadata update')
      })
      const button = submitButton()
      assert.ok(button)
      await act(async () => {
        button.click()
      })

      assert.equal(putCalls.length, 1)
      const body = putCalls[0].body as {
        model_name?: string
        description?: string
        pricing?: unknown
      }
      assert.equal(body.model_name, 'video')
      assert.equal(body.description, 'metadata update')
      assert.equal(Object.hasOwn(body, 'pricing'), false)
    } finally {
      await view.cleanup()
    }
  })

  test('super administrators retain rename and pricing controls', async () => {
    const view = await renderDrawer()
    try {
      const nameInput = document.querySelector<HTMLInputElement>(
        'input[name="model_name"]'
      )
      assert.ok(nameInput)
      assert.equal(nameInput.disabled, false)
      assert.match(document.body.textContent || '', /Pricing mode/)
    } finally {
      await view.cleanup()
    }
  })

  test('synchronous double submit sends only one model mutation', async () => {
    let releaseResponse: (() => void) | undefined
    putResponder = () =>
      new Promise((resolve) => {
        releaseResponse = () =>
          resolve({ data: { success: true, data: modelFixture } })
      })
    const view = await renderDrawer()
    try {
      const iconInput =
        document.querySelector<HTMLInputElement>('input[name="icon"]')
      assert.ok(iconInput)
      await act(async () => {
        changeInputValue(iconInput, 'video-icon')
      })
      const button = submitButton()
      assert.ok(button)
      await act(async () => {
        button.click()
        button.click()
        await Promise.resolve()
        await Promise.resolve()
      })

      assert.equal(putCalls.length, 1)
      assert.ok(releaseResponse)
      await act(async () => {
        releaseResponse?.()
      })
    } finally {
      await view.cleanup()
    }
  })
})
