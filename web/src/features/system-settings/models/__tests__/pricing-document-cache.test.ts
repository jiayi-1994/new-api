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

import { QueryClient } from '@tanstack/react-query'

import type { SystemOptionsResponse } from '../../types'
import {
  PRICING_DOCUMENT_KEYS,
  type PricingDocumentKey,
} from '../model-pricing-persistence'
import {
  adoptCommittedPricingDocuments,
  ensureSystemOptionsCacheBase,
} from '../pricing-document-cache'

const documents = Object.fromEntries(
  PRICING_DOCUMENT_KEYS.map((key) => [key, `committed-${key}`])
) as Record<PricingDocumentKey, string>

function completeOptionsResponse(): SystemOptionsResponse {
  return {
    success: true,
    message: 'full response',
    data: [
      { key: 'UnrelatedOption', value: 'preserved' },
      ...PRICING_DOCUMENT_KEYS.map((key) => ({ key, value: `old-${key}` })),
    ],
  }
}

describe('committed pricing document cache adoption', () => {
  test('cancels stale reads before replacing all pricing documents and preserving other options', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData<SystemOptionsResponse>(['system-options'], {
      success: true,
      message: 'existing response',
      data: [
        { key: 'UnrelatedOption', value: 'preserved' },
        ...PRICING_DOCUMENT_KEYS.map((key) => ({ key, value: `old-${key}` })),
      ],
    })
    let cacheWasOldWhenCancelled = false
    const originalCancel = queryClient.cancelQueries.bind(queryClient)
    queryClient.cancelQueries = (async (...args) => {
      const current = queryClient.getQueryData<SystemOptionsResponse>([
        'system-options',
      ])
      cacheWasOldWhenCancelled =
        current?.data.find((option) => option.key === 'ModelPrice')?.value ===
        'old-ModelPrice'
      return originalCancel(...args)
    }) as typeof queryClient.cancelQueries

    await adoptCommittedPricingDocuments(queryClient, documents)

    assert.equal(cacheWasOldWhenCancelled, true)
    const cached = queryClient.getQueryData<SystemOptionsResponse>([
      'system-options',
    ])
    assert.equal(
      cached?.data.find((option) => option.key === 'UnrelatedOption')?.value,
      'preserved'
    )
    assert.deepEqual(
      Object.fromEntries(
        PRICING_DOCUMENT_KEYS.map((key) => [
          key,
          cached?.data.find((option) => option.key === key)?.value,
        ])
      ),
      documents
    )
    queryClient.clear()
  })

  test('refuses to manufacture a pricing-only response from a cold cache', async () => {
    const queryClient = new QueryClient()

    await assert.rejects(() =>
      adoptCommittedPricingDocuments(queryClient, documents)
    )
    assert.equal(
      queryClient.getQueryData<SystemOptionsResponse>(['system-options']),
      undefined
    )
    queryClient.clear()
  })

  test('awaits an in-flight full options query before committed adoption', async () => {
    const queryClient = new QueryClient()
    let fetchCount = 0
    let resolveBase: ((value: SystemOptionsResponse) => void) | undefined
    const baseResponse = new Promise<SystemOptionsResponse>((resolve) => {
      resolveBase = resolve
    })
    const inFlight = queryClient.fetchQuery({
      queryKey: ['system-options'],
      queryFn: () => {
        fetchCount += 1
        return baseResponse
      },
    })

    const ensured = ensureSystemOptionsCacheBase(queryClient)
    await Promise.resolve()
    assert.equal(fetchCount, 1)
    assert.ok(resolveBase)
    resolveBase(completeOptionsResponse())
    await Promise.all([inFlight, ensured])
    await adoptCommittedPricingDocuments(queryClient, documents)

    const cached = queryClient.getQueryData<SystemOptionsResponse>([
      'system-options',
    ])
    assert.equal(
      cached?.data.find((option) => option.key === 'UnrelatedOption')?.value,
      'preserved'
    )
    assert.equal(fetchCount, 1)
    queryClient.clear()
  })
})
