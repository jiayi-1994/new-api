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

import { QueryClient } from '@tanstack/react-query'

import type { SystemOptionsResponse } from '@/features/system-settings/types'
import { api } from '@/lib/api'

import {
  PRICING_DOCUMENT_KEYS,
  type PricingDocumentKey,
} from '../../../system-settings/models/model-pricing-persistence'
import type { ModelMutationResponse } from '../../api'
import { handleBatchDeleteModels, handleDeleteModel } from '../model-actions'

// @ts-expect-error Bun exposes module mocks at runtime without installed types.
const { mock, spyOn } = await import('bun:test')

const baseDocuments = Object.fromEntries(
  PRICING_DOCUMENT_KEYS.map((key) => [key, '{"first":1,"second":1}'])
) as Record<PricingDocumentKey, string>
const afterFirstDelete = Object.fromEntries(
  PRICING_DOCUMENT_KEYS.map((key) => [key, '{"second":1}'])
) as Record<PricingDocumentKey, string>
const afterSecondDelete = Object.fromEntries(
  PRICING_DOCUMENT_KEYS.map((key) => [key, '{}'])
) as Record<PricingDocumentKey, string>

function fullOptions(
  documents: Record<PricingDocumentKey, string>
): SystemOptionsResponse {
  return {
    success: true,
    message: '',
    data: [
      { key: 'UnrelatedOption', value: 'preserved' },
      ...PRICING_DOCUMENT_KEYS.map((key) => ({ key, value: documents[key] })),
    ],
  }
}

let getResponder = async () => ({ data: fullOptions(baseDocuments) })
let deleteResponder = async (): Promise<{ data: ModelMutationResponse }> => ({
  data: { success: true, data: null },
})
const requestOrder: string[] = []

spyOn(api, 'get').mockImplementation((async (url: string) => {
  requestOrder.push(`GET ${url}`)
  return getResponder()
}) as typeof api.get)
spyOn(api, 'delete').mockImplementation((async (url: string) => {
  requestOrder.push(`DELETE ${url}`)
  return deleteResponder()
}) as typeof api.delete)

function pricingDocumentsFromCache(queryClient: QueryClient) {
  const cached = queryClient.getQueryData<SystemOptionsResponse>([
    'system-options',
  ])
  return Object.fromEntries(
    PRICING_DOCUMENT_KEYS.map((key) => [
      key,
      cached?.data.find((option) => option.key === key)?.value,
    ])
  )
}

beforeEach(() => {
  requestOrder.length = 0
  getResponder = async () => ({ data: fullOptions(baseDocuments) })
  deleteResponder = async () => ({
    data: { success: true, data: null },
  })
})

after(() => mock.restore())

describe('model delete pricing snapshots', () => {
  test('a pending single delete fetches a cold baseline before adopting committed documents', async () => {
    const queryClient = new QueryClient()
    let systemOptionsInvalidations = 0
    const originalInvalidate = queryClient.invalidateQueries.bind(queryClient)
    queryClient.invalidateQueries = (async (...args) => {
      const filters = args[0]
      if (
        filters &&
        'queryKey' in filters &&
        filters.queryKey?.[0] === 'system-options'
      ) {
        systemOptionsInvalidations += 1
      }
      return originalInvalidate(...args)
    }) as typeof queryClient.invalidateQueries
    deleteResponder = async () => ({
      data: {
        success: true,
        data: null,
        committed: true,
        publication_pending: true,
        pricing_documents: afterFirstDelete,
      },
    })

    await handleDeleteModel(1, queryClient)

    assert.deepEqual(requestOrder, ['GET /api/option/', 'DELETE /api/models/1'])
    assert.deepEqual(pricingDocumentsFromCache(queryClient), afterFirstDelete)
    const cached = queryClient.getQueryData<SystemOptionsResponse>([
      'system-options',
    ])
    assert.equal(
      cached?.data.find((option) => option.key === 'UnrelatedOption')?.value,
      'preserved'
    )
    assert.equal(systemOptionsInvalidations, 0)
    queryClient.clear()
  })

  test('batch delete runs sequentially and follows the final authoritative publication state', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    let systemOptionsInvalidations = 0
    const originalInvalidate = queryClient.invalidateQueries.bind(queryClient)
    queryClient.invalidateQueries = (async (...args) => {
      const filters = args[0]
      if (
        filters &&
        'queryKey' in filters &&
        filters.queryKey?.[0] === 'system-options'
      ) {
        systemOptionsInvalidations += 1
      }
      return originalInvalidate(...args)
    }) as typeof queryClient.invalidateQueries
    let resolveFirst:
      | ((value: { data: ModelMutationResponse }) => void)
      | undefined
    const firstResponse = new Promise<{ data: ModelMutationResponse }>(
      (resolve) => {
        resolveFirst = resolve
      }
    )
    deleteResponder = async () => {
      if (requestOrder.at(-1) === 'DELETE /api/models/1') {
        return firstResponse
      }
      return {
        data: {
          success: true,
          data: null,
          committed: true,
          publication_pending: false,
          pricing_documents: afterSecondDelete,
        },
      }
    }

    const deletion = handleBatchDeleteModels([1, 2], queryClient)
    await Promise.resolve()
    await Promise.resolve()
    const callsBeforeFirstCompletes = requestOrder.filter((request) =>
      request.startsWith('DELETE')
    ).length
    assert.ok(resolveFirst)
    resolveFirst({
      data: {
        success: true,
        data: null,
        committed: true,
        publication_pending: true,
        pricing_documents: afterFirstDelete,
      },
    })
    await deletion

    assert.equal(callsBeforeFirstCompletes, 1)
    assert.deepEqual(requestOrder, [
      'DELETE /api/models/1',
      'DELETE /api/models/2',
    ])
    assert.deepEqual(pricingDocumentsFromCache(queryClient), afterSecondDelete)
    assert.equal(systemOptionsInvalidations, 1)
    queryClient.clear()
  })

  test('batch delete continues after an intermediate transport failure and finalizes successes', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    let systemOptionsInvalidations = 0
    let modelListInvalidations = 0
    const originalInvalidate = queryClient.invalidateQueries.bind(queryClient)
    queryClient.invalidateQueries = (async (...args) => {
      const filters = args[0]
      if (filters && 'queryKey' in filters) {
        if (filters.queryKey?.[0] === 'system-options') {
          systemOptionsInvalidations += 1
        }
        if (filters.queryKey?.[0] === 'models') {
          modelListInvalidations += 1
        }
      }
      return originalInvalidate(...args)
    }) as typeof queryClient.invalidateQueries
    deleteResponder = async () => {
      const request = requestOrder.at(-1)
      if (request === 'DELETE /api/models/2') {
        throw new Error('network failure')
      }
      return {
        data: {
          success: true,
          data: null,
          committed: true,
          publication_pending: false,
          pricing_documents:
            request === 'DELETE /api/models/1'
              ? afterFirstDelete
              : afterSecondDelete,
        },
      }
    }
    let finalizedCount = 0

    await handleBatchDeleteModels([1, 2, 3], queryClient, (count) => {
      finalizedCount = count
    })

    assert.deepEqual(requestOrder, [
      'DELETE /api/models/1',
      'DELETE /api/models/2',
      'DELETE /api/models/3',
    ])
    assert.equal(finalizedCount, 2)
    assert.equal(modelListInvalidations, 1)
    assert.equal(systemOptionsInvalidations, 1)
    assert.deepEqual(pricingDocumentsFromCache(queryClient), afterSecondDelete)
    queryClient.clear()
  })
})
