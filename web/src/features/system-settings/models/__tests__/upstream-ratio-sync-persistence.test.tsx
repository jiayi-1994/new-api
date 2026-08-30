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

import type { SystemOptionsResponse } from '../../types'
import {
  PRICING_DOCUMENT_KEYS,
  type PricingDocumentKey,
} from '../model-pricing-persistence'

// @ts-expect-error Bun exposes module mocks at runtime without installed types.
const { mock, spyOn } = await import('bun:test')

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
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

mock.module('../channel-selector-dialog', () => ({
  ChannelSelectorDialog: (props: {
    open: boolean
    channels: Array<{ id: number }>
    onSelectedChannelIdsChange: (ids: number[]) => void
    onConfirm: (ids: number[]) => void
  }) =>
    props.open && props.channels.length > 0 ? (
      <button
        type='button'
        onClick={() => {
          props.onSelectedChannelIdsChange([1])
          props.onConfirm([1])
        }}
      >
        Confirm test channel
      </button>
    ) : null,
}))

mock.module('../upstream-ratio-sync-table', () => ({
  UpstreamRatioSyncTable: (props: {
    differences: Record<string, unknown>
    onSelectValue: (
      model: string,
      ratioType: 'model_price',
      value: number,
      sourceName: string
    ) => void
  }) =>
    Object.keys(props.differences).length > 0 ? (
      <button
        type='button'
        onClick={() =>
          props.onSelectValue('video', 'model_price', 0.4, 'upstream')
        }
      >
        Select upstream value
      </button>
    ) : null,
}))

mock.module('../conflict-confirm-dialog', () => ({
  ConflictConfirmDialog: () => null,
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { api } = await import('@/lib/api')
const { UpstreamRatioSync } = await import('../upstream-ratio-sync')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const rawDocuments: Record<PricingDocumentKey, string> = Object.fromEntries(
  PRICING_DOCUMENT_KEYS.map((key) => [key, '{}'])
) as Record<PricingDocumentKey, string>
rawDocuments.ModelPrice = '{ "video": 0.3 }'

const committedDocuments: Record<PricingDocumentKey, string> = {
  ...rawDocuments,
  ModelPrice: '{ "video": 0.4 }',
  ModelRatio: '{ "concurrent": 2 }',
}

const modelRatios = {
  ModelPrice: rawDocuments.ModelPrice,
  ModelRatio: rawDocuments.ModelRatio,
  CompletionRatio: rawDocuments.CompletionRatio,
  CacheRatio: rawDocuments.CacheRatio,
  CreateCacheRatio: rawDocuments.CreateCacheRatio,
  ImageRatio: rawDocuments.ImageRatio,
  AudioRatio: rawDocuments.AudioRatio,
  AudioCompletionRatio: rawDocuments.AudioCompletionRatio,
  'billing_setting.billing_mode': rawDocuments['billing_setting.billing_mode'],
  'billing_setting.billing_expr': rawDocuments['billing_setting.billing_expr'],
}

type PutResponse = { data: Record<string, unknown> }
let putResponder: () => Promise<PutResponse>
let pricingPutCount = 0

spyOn(api, 'get').mockImplementation((async () => ({
  data: {
    success: true,
    message: '',
    data: [
      { id: 1, name: 'upstream', base_url: 'https://example.com', status: 1 },
    ],
  },
})) as typeof api.get)

spyOn(api, 'post').mockImplementation((async () => ({
  data: {
    success: true,
    message: '',
    data: {
      differences: {
        video: {
          model_price: {
            current: 0.3,
            upstreams: { upstream: 0.4 },
            confidence: { upstream: true },
          },
        },
      },
      test_results: [],
    },
  },
})) as typeof api.post)

spyOn(api, 'put').mockImplementation((async () => {
  pricingPutCount += 1
  return putResponder()
}) as typeof api.put)

const findButton = (label: string) =>
  [...document.querySelectorAll<HTMLButtonElement>('button')].find((button) =>
    button.textContent?.includes(label)
  )

async function waitForButton(label: string) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    const button = findButton(label)
    if (button) return button
    await act(
      async () =>
        new Promise<void>((resolve) => domWindow.setTimeout(resolve, 0))
    )
  }
  return undefined
}

async function waitForEnabledButton(label: string) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    const button = findButton(label)
    if (button && !button.disabled) return button
    await act(
      async () =>
        new Promise<void>((resolve) => domWindow.setTimeout(resolve, 0))
    )
  }
  return undefined
}

async function renderSync() {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  queryClient.setQueryData<SystemOptionsResponse>(['system-options'], {
    success: true,
    message: '',
    data: PRICING_DOCUMENT_KEYS.map((key) => ({
      key,
      value: rawDocuments[key],
    })),
  })
  let invalidateCount = 0
  const originalInvalidate = queryClient.invalidateQueries.bind(queryClient)
  queryClient.invalidateQueries = (async (...args) => {
    invalidateCount += 1
    return originalInvalidate(...args)
  }) as typeof queryClient.invalidateQueries

  const render = () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <UpstreamRatioSync modelRatios={{ ...modelRatios }} />
        </I18nextProvider>
      </QueryClientProvider>
    )
  }
  await act(async () => render())

  const selectChannels = findButton('Select Sync Channels')
  assert.ok(selectChannels)
  await act(async () => selectChannels.click())
  await act(async () => await Promise.resolve())
  const confirmChannel = await waitForButton('Confirm test channel')
  assert.ok(confirmChannel)
  await act(async () => confirmChannel.click())
  const selectValue = await waitForButton('Select upstream value')
  assert.ok(selectValue)
  await act(async () => selectValue.click())

  return {
    queryClient,
    invalidateCount: () => invalidateCount,
    rerenderStale: async () => act(async () => render()),
    apply: () => {
      const button = findButton('Apply Sync')
      assert.ok(button)
      button.click()
    },
    cleanup: async () => {
      await act(async () => root.unmount())
      queryClient.clear()
      container.remove()
    },
  }
}

beforeEach(() => {
  pricingPutCount = 0
})

after(() => {
  mock.restore()
  domWindow.close()
})

describe('upstream ratio sync pricing persistence', () => {
  test('publication pending adopts committed documents without stale refetch rollback', async () => {
    putResponder = async () => ({
      data: {
        success: true,
        committed: true,
        publication_recovered: false,
        publication_pending: true,
        data: committedDocuments,
      },
    })
    const view = await renderSync()
    try {
      await act(async () => view.apply())
      await view.rerenderStale()

      const cached = view.queryClient.getQueryData<SystemOptionsResponse>([
        'system-options',
      ])
      assert.equal(view.invalidateCount(), 0)
      assert.deepEqual(
        Object.fromEntries(
          PRICING_DOCUMENT_KEYS.map((key) => [
            key,
            cached?.data.find((option) => option.key === key)?.value,
          ])
        ),
        committedDocuments
      )
      assert.equal(findButton('Apply Sync')?.disabled, true)
      assert.equal(pricingPutCount, 1)
    } finally {
      await view.cleanup()
    }
  })

  test('synchronous double apply sends one pricing command', async () => {
    let settle: ((value: PutResponse) => void) | undefined
    const pendingResponse = new Promise<PutResponse>((resolve) => {
      settle = resolve
    })
    putResponder = async () => pendingResponse
    const view = await renderSync()
    try {
      await act(async () => {
        view.apply()
        view.apply()
        await Promise.resolve()
      })
      assert.equal(pricingPutCount, 1)

      assert.ok(settle)
      await act(async () => {
        settle?.({
          data: {
            success: true,
            committed: true,
            publication_recovered: true,
            publication_pending: false,
            data: committedDocuments,
          },
        })
        await pendingResponse
      })
    } finally {
      await view.cleanup()
    }
  })

  test('a failed sync releases the guard for an explicit retry', async () => {
    let rejectFirst: ((reason: Error) => void) | undefined
    const firstResponse = new Promise<PutResponse>((_resolve, reject) => {
      rejectFirst = reject
    })
    let attempt = 0
    putResponder = async () => {
      attempt += 1
      if (attempt === 1) return firstResponse
      return {
        data: {
          success: true,
          committed: true,
          publication_recovered: true,
          publication_pending: false,
          data: committedDocuments,
        },
      }
    }
    const view = await renderSync()
    try {
      await act(async () => {
        view.apply()
        view.apply()
        await Promise.resolve()
      })
      assert.equal(pricingPutCount, 1)

      assert.ok(rejectFirst)
      await act(async () => {
        rejectFirst?.(new Error('network failed'))
        await firstResponse.catch(() => undefined)
      })
      assert.ok(await waitForEnabledButton('Apply Sync'))
      await act(async () => {
        view.apply()
        await Promise.resolve()
      })
      assert.equal(pricingPutCount, 2)
    } finally {
      await view.cleanup()
    }
  })
})
