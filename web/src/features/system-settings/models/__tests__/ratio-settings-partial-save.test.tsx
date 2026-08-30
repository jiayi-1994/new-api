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
  'localStorage',
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
const { toast } = await import('sonner')
const { api } = await import('@/lib/api')
const { RatioSettingsCard } = await import('../ratio-settings-card')

const warningSpy = spyOn(toast, 'warning')
const errorSpy = spyOn(toast, 'error')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const rawDocuments = {
  ModelPrice: '{ "video": 0.3 }',
  ModelRatio: '{}',
  CacheRatio: '{}',
  CreateCacheRatio: '{}',
  CompletionRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  'billing_setting.billing_mode': '{}',
  'billing_setting.billing_expr': '{}',
  TaskBillingMode: '{}',
  VideoResolutionPrice: '{}',
}

const committedDocuments = {
  ...rawDocuments,
  ModelPrice: '{"video":0.4}',
}

const concurrentlyUpdatedDocuments = {
  ...committedDocuments,
  ModelRatio: '{ "concurrent": 2 }',
  TaskBillingMode: '{ "video": "per_call" }',
}

function modelDefaultsFromDocuments(
  documents: typeof rawDocuments,
  exposeRatioEnabled: boolean
) {
  return {
    ModelPrice: documents.ModelPrice,
    ModelRatio: documents.ModelRatio,
    CacheRatio: documents.CacheRatio,
    CreateCacheRatio: documents.CreateCacheRatio,
    CompletionRatio: documents.CompletionRatio,
    ImageRatio: documents.ImageRatio,
    AudioRatio: documents.AudioRatio,
    AudioCompletionRatio: documents.AudioCompletionRatio,
    ExposeRatioEnabled: exposeRatioEnabled,
    BillingMode: documents['billing_setting.billing_mode'],
    BillingExpr: documents['billing_setting.billing_expr'],
    TaskBillingMode: documents.TaskBillingMode,
    VideoResolutionPrice: documents.VideoResolutionPrice,
  }
}

type PutResponder = (
  url: string,
  body: unknown
) => Promise<{ data: Record<string, unknown> }>

let putResponder: PutResponder = async () => ({ data: { success: true } })
const putCalls: Array<{ url: string; body: unknown }> = []
spyOn(api, 'put').mockImplementation((async (url: string, body: unknown) => {
  putCalls.push({ url, body })
  return putResponder(url, body)
}) as typeof api.put)

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

const findButton = (label: string) =>
  [...document.querySelectorAll<HTMLButtonElement>('button')].find((button) =>
    button.textContent?.includes(label)
  )

async function renderSettings(
  refetchedModelDefaults?: () => ReturnType<typeof modelDefaultsFromDocuments>,
  editMode: 'json' | 'visual' = 'json'
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  let refetchCount = 0
  const originalRefetch = queryClient.refetchQueries.bind(queryClient)
  queryClient.refetchQueries = (async (...args) => {
    refetchCount += 1
    const result = await originalRefetch(...args)
    if (refetchedModelDefaults) renderCard(refetchedModelDefaults())
    return result
  }) as typeof queryClient.refetchQueries

  const staleModelDefaults = modelDefaultsFromDocuments(rawDocuments, false)
  const groupDefaults = {
    GroupRatio: '{}',
    TopupGroupRatio: '{}',
    UserUsableGroups: '{}',
    GroupGroupRatio: '{}',
    AutoGroups: '[]',
    DefaultUseAutoGroup: false,
    GroupSpecialUsableGroup: '{}',
  }
  const renderCard = (modelDefaults = staleModelDefaults) => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <RatioSettingsCard
            modelDefaults={modelDefaults}
            groupDefaults={groupDefaults}
            toolPricesDefault='{}'
            visibleTabs={['models']}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
  }

  await act(async () => {
    renderCard()
  })

  if (editMode === 'json') {
    const switchToJson = findButton('Switch to JSON')
    assert.ok(switchToJson)
    await act(async () => {
      switchToJson.click()
    })
    const priceTextarea = document.querySelector<HTMLTextAreaElement>(
      'textarea[name="ModelPrice"]'
    )
    assert.ok(priceTextarea)
    await act(async () => {
      changeTextareaValue(priceTextarea, '{"video":0.4}')
    })
  } else {
    const videoRow = [
      ...document.querySelectorAll<HTMLTableRowElement>('tr'),
    ].find((row) => row.textContent?.includes('video'))
    assert.ok(videoRow)
    await act(async () => {
      videoRow.click()
    })
    const priceInput = document.querySelector<HTMLInputElement>(
      'input[name="price"]'
    )
    assert.ok(priceInput)
    await act(async () => {
      changeInputValue(priceInput, '0.4')
    })
  }

  const exposeSwitch = document.querySelector<HTMLElement>('[role="switch"]')
  assert.ok(exposeSwitch)
  await act(async () => {
    exposeSwitch.click()
  })

  return {
    refetchCount: () => refetchCount,
    rerenderStaleDefaults: async () => {
      await act(async () => {
        renderCard({ ...staleModelDefaults })
      })
    },
    save: async () => {
      const saveButton = findButton('Save model prices')
      assert.ok(saveButton)
      await act(async () => {
        saveButton.click()
      })
    },
    reopenVideoEditor: async () => {
      const videoRow = [
        ...document.querySelectorAll<HTMLTableRowElement>('tr'),
      ].find((row) => row.textContent?.includes('video'))
      assert.ok(videoRow)
      await act(async () => {
        videoRow.click()
      })
    },
    saveTwiceSynchronously: async () => {
      const saveButton = findButton('Save model prices')
      assert.ok(saveButton)
      await act(async () => {
        saveButton.click()
        saveButton.click()
        await Promise.resolve()
      })
    },
    cleanup: async () => {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
    },
  }
}

beforeEach(() => {
  putCalls.length = 0
  warningSpy.mockClear()
  errorSpy.mockClear()
})

after(() => {
  mock.restore()
  domWindow.close()
})

describe('ratio settings partial saves', () => {
  for (const publication of [
    { name: 'pending', publicationPending: true, publicationRecovered: false },
    {
      name: 'recovered',
      publicationPending: false,
      publicationRecovered: true,
    },
  ]) {
    test(`committed ${publication.name} pricing is not retried when ratio exposure fails`, async () => {
      let exposureAttempts = 0
      putResponder = async (url) => {
        if (url === '/api/option/pricing') {
          return {
            data: {
              success: true,
              committed: true,
              publication_recovered: publication.publicationRecovered,
              publication_pending: publication.publicationPending,
              data: concurrentlyUpdatedDocuments,
            },
          }
        }
        exposureAttempts += 1
        return {
          data:
            exposureAttempts === 1
              ? { success: false, message: 'exposure rejected' }
              : { success: true, message: '' },
        }
      }
      const view = await renderSettings(() =>
        modelDefaultsFromDocuments(
          concurrentlyUpdatedDocuments,
          exposureAttempts >= 2
        )
      )
      try {
        await view.save()
        assert.equal(view.refetchCount(), 0)
        if (publication.publicationPending) {
          await view.rerenderStaleDefaults()
        }
        await view.save()

        assert.equal(
          putCalls.filter((call) => call.url === '/api/option/pricing').length,
          1
        )
        assert.equal(exposureAttempts, 2)
        if (publication.publicationPending) {
          assert.equal(view.refetchCount(), 0)
        } else {
          assert.ok(view.refetchCount() > 0)
        }
        assert.ok(
          warningSpy.mock.calls.some(
            (call: unknown[]) =>
              call[0] ===
              'Pricing was saved, but ratio exposure could not be updated. Retry only ratio exposure.'
          )
        )
        if (publication.publicationPending) {
          assert.ok(
            warningSpy.mock.calls.some(
              (call: unknown[]) =>
                call[0] ===
                'Pricing was saved, but live settings are still converging. Do not retry.'
            )
          )
        }
      } finally {
        await view.cleanup()
      }
    })
  }

  test('successful ratio exposure is not retried after a pricing conflict', async () => {
    let exposureAttempts = 0
    putResponder = async (url) => {
      if (url === '/api/option/pricing') {
        const conflict = new Error('conflict') as Error & {
          isAxiosError: boolean
          response: { status: number }
        }
        conflict.isAxiosError = true
        conflict.response = { status: 409 }
        throw conflict
      }
      exposureAttempts += 1
      return { data: { success: true, message: '' } }
    }
    const view = await renderSettings()
    try {
      await view.save()
      const firstRefetchCount = view.refetchCount()
      assert.ok(firstRefetchCount > 0)
      assert.ok(findButton('Switch to Visual'))
      await view.save()

      assert.equal(
        putCalls.filter((call) => call.url === '/api/option/pricing').length,
        2
      )
      assert.equal(exposureAttempts, 1)
      assert.ok(view.refetchCount() > firstRefetchCount)
      assert.ok(findButton('Switch to Visual'))
      assert.ok(
        errorSpy.mock.calls.some(
          (call: unknown[]) =>
            call[0] ===
            'Ratio exposure was saved, but pricing changed on the server. Review the refreshed pricing before saving again.'
        )
      )
    } finally {
      await view.cleanup()
    }
  })

  test('successful ratio exposure does not discard a failed pricing draft', async () => {
    let pricingAttempts = 0
    let exposureAttempts = 0
    putResponder = async (url) => {
      if (url === '/api/option/pricing') {
        pricingAttempts += 1
        return {
          data: {
            success: false,
            committed: false,
            message: 'pricing rejected',
          },
        }
      }
      exposureAttempts += 1
      return { data: { success: true, message: '' } }
    }
    const view = await renderSettings(() =>
      modelDefaultsFromDocuments(rawDocuments, true)
    )
    try {
      await view.save()
      assert.equal(view.refetchCount(), 0)
      await view.save()

      assert.equal(pricingAttempts, 2)
      assert.equal(exposureAttempts, 1)
      assert.equal(view.refetchCount(), 0)
    } finally {
      await view.cleanup()
    }
  })

  test('synchronous double save sends only one pricing command', async () => {
    let resolvePricing:
      | ((value: { data: Record<string, unknown> }) => void)
      | undefined
    const pricingResponse = new Promise<{ data: Record<string, unknown> }>(
      (resolve) => {
        resolvePricing = resolve
      }
    )
    putResponder = async (url) => {
      if (url === '/api/option/pricing') return pricingResponse
      return { data: { success: true, message: '' } }
    }
    const view = await renderSettings()
    try {
      await view.saveTwiceSynchronously()
      assert.equal(
        putCalls.filter((call) => call.url === '/api/option/pricing').length,
        1
      )

      const settlePricing = resolvePricing
      assert.ok(settlePricing)
      await act(async () => {
        settlePricing({
          data: {
            success: true,
            committed: true,
            publication_recovered: false,
            publication_pending: false,
            data: committedDocuments,
          },
        })
        await pricingResponse
      })
    } finally {
      await view.cleanup()
    }
  })

  test('visual editor retries only exposure after committed pricing', async () => {
    let exposureAttempts = 0
    putResponder = async (url) => {
      if (url === '/api/option/pricing') {
        return {
          data: {
            success: true,
            committed: true,
            publication_recovered: true,
            publication_pending: false,
            data: concurrentlyUpdatedDocuments,
          },
        }
      }
      exposureAttempts += 1
      return {
        data:
          exposureAttempts === 1
            ? { success: false, message: 'exposure rejected' }
            : { success: true, message: '' },
      }
    }
    const view = await renderSettings(
      () =>
        modelDefaultsFromDocuments(
          concurrentlyUpdatedDocuments,
          exposureAttempts >= 2
        ),
      'visual'
    )
    try {
      await view.save()
      await view.reopenVideoEditor()
      await view.save()

      assert.equal(
        putCalls.filter((call) => call.url === '/api/option/pricing').length,
        1
      )
      assert.equal(exposureAttempts, 2)
    } finally {
      await view.cleanup()
    }
  })
})
