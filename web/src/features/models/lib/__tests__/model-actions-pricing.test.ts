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
import i18next from 'i18next'
import { toast } from 'sonner'

import type { SystemOptionsResponse } from '@/features/system-settings/types'
import { api } from '@/lib/api'

import {
  PRICING_DOCUMENT_KEYS,
  type PricingDocumentKey,
} from '../../../system-settings/models/model-pricing-persistence'
import { adoptCommittedPricingDocuments } from '../../../system-settings/models/pricing-document-cache'
import type { ModelMutationResponse } from '../../api'
import { handleBatchDeleteModels, handleDeleteModel } from '../model-actions'

// @ts-expect-error Bun exposes module mocks at runtime without installed types.
const { mock, spyOn } = await import('bun:test')
const warningSpy = spyOn(toast, 'warning')
const errorSpy = spyOn(toast, 'error')
const unknownDeleteMessage =
  'Delete result is unknown. Do not retry; refresh and review the model list.'
const unknownBatchDeleteMessage =
  'Delete result is unknown for {{count}} model(s). Do not retry; refresh and review the model list.'
await i18next.init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        [unknownDeleteMessage]: unknownDeleteMessage,
        [unknownBatchDeleteMessage]: unknownBatchDeleteMessage,
      },
    },
  },
})

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

let getResponder = async (_url: string): Promise<{ data: unknown }> => ({
  data: fullOptions(baseDocuments),
})
let deleteResponder = async (): Promise<{ data: ModelMutationResponse }> => ({
  data: { success: true, data: null },
})
const requestOrder: string[] = []

spyOn(api, 'get').mockImplementation((async (url: string) => {
  requestOrder.push(`GET ${url}`)
  return getResponder(url)
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
  test('a response-lost single delete leaves pricing untouched while reconciling the model list', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    let modelListInvalidations = 0
    let modelListRefetches = 0
    let systemOptionsInvalidations = 0
    const originalInvalidate = queryClient.invalidateQueries.bind(queryClient)
    const originalRefetch = queryClient.refetchQueries.bind(queryClient)
    let markModelListRefetched: (() => void) | undefined
    const modelListRefetched = new Promise<void>((resolve) => {
      markModelListRefetched = resolve
    })
    queryClient.invalidateQueries = (async (...args) => {
      const filters = args[0]
      if (filters && 'queryKey' in filters) {
        if (filters.queryKey?.[0] === 'models') modelListInvalidations += 1
        if (filters.queryKey?.[0] === 'system-options') {
          systemOptionsInvalidations += 1
        }
      }
      return originalInvalidate(...args)
    }) as typeof queryClient.invalidateQueries
    queryClient.refetchQueries = (async (...args) => {
      const filters = args[0]
      if (
        filters &&
        'queryKey' in filters &&
        filters.queryKey?.[0] === 'models'
      ) {
        modelListRefetches += 1
        markModelListRefetched?.()
      }
      return originalRefetch(...args)
    }) as typeof queryClient.refetchQueries
    deleteResponder = async () => {
      throw Object.assign(new Error('connection lost'), { isAxiosError: true })
    }
    let completed = false
    const warningsBefore = warningSpy.mock.calls.length

    const outcome = await handleDeleteModel(1, queryClient, () => {
      completed = true
    })
    await modelListRefetched

    assert.deepEqual(requestOrder, ['DELETE /api/models/1'])
    assert.deepEqual(pricingDocumentsFromCache(queryClient), baseDocuments)
    assert.equal(modelListInvalidations, 1)
    assert.equal(modelListRefetches, 1)
    assert.equal(systemOptionsInvalidations, 0)
    assert.equal(completed, false)
    assert.equal(outcome, 'unknown')
    assert.ok(
      warningSpy.mock.calls
        .slice(warningsBefore)
        .some((call: unknown[]) => call[0] === unknownDeleteMessage)
    )
    queryClient.clear()
  })

  test('a failed response-loss recovery warns not to retry without reporting an ordinary delete failure', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('connection lost'), { isAxiosError: true })
    }
    let markRefetchStarted: (() => void) | undefined
    const refetchStarted = new Promise<void>((resolve) => {
      markRefetchStarted = resolve
    })
    queryClient.refetchQueries = (async () => {
      markRefetchStarted?.()
      throw new Error('model list unavailable')
    }) as typeof queryClient.refetchQueries
    const warningsBefore = warningSpy.mock.calls.length
    const errorsBefore = errorSpy.mock.calls.length

    let completed = false
    const outcome = await handleDeleteModel(1, queryClient, () => {
      completed = true
    })
    await refetchStarted

    assert.deepEqual(requestOrder, ['DELETE /api/models/1'])
    assert.ok(
      warningSpy.mock.calls
        .slice(warningsBefore)
        .some(
          (call: unknown[]) =>
            call[0] ===
            'Delete result is unknown. Do not retry; refresh and review the model list.'
        ),
      JSON.stringify(warningSpy.mock.calls.slice(warningsBefore))
    )
    assert.equal(errorSpy.mock.calls.length, errorsBefore)
    assert.equal(completed, false)
    assert.equal(outcome, 'unknown')
    queryClient.clear()
  })

  test('a pre-send no-response failure remains unknown without changing pricing', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('request was not sent'), {
        isAxiosError: true,
      })
    }
    let completed = false

    const outcome = await handleDeleteModel(1, queryClient, () => {
      completed = true
    })

    assert.equal(outcome, 'unknown')
    assert.equal(completed, false)
    assert.deepEqual(pricingDocumentsFromCache(queryClient), baseDocuments)
    queryClient.clear()
  })

  test('a response loss for a model without pricing remains unknown', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('response lost after commit'), {
        isAxiosError: true,
      })
    }
    let completed = false

    const outcome = await handleDeleteModel(404, queryClient, () => {
      completed = true
    })

    assert.equal(outcome, 'unknown')
    assert.equal(completed, false)
    assert.deepEqual(pricingDocumentsFromCache(queryClient), baseDocuments)
    queryClient.clear()
  })

  test('a delayed model-list reconciliation cannot delay the unknown outcome', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('response lost'), { isAxiosError: true })
    }
    let finishRefetch: (() => void) | undefined
    let markRefetchStarted: (() => void) | undefined
    const refetchStarted = new Promise<void>((resolve) => {
      markRefetchStarted = resolve
    })
    queryClient.refetchQueries = (() => {
      markRefetchStarted?.()
      return new Promise<void>((resolve) => {
        finishRefetch = resolve
      })
    }) as typeof queryClient.refetchQueries
    let completed = false

    const deletion = handleDeleteModel(1, queryClient, () => {
      completed = true
    })
    await refetchStarted

    const outcome = await deletion
    assert.deepEqual(pricingDocumentsFromCache(queryClient), baseDocuments)
    assert.equal(outcome, 'unknown')
    assert.equal(completed, false)
    assert.ok(finishRefetch)
    finishRefetch()
    queryClient.clear()
  })

  test('a failed model-list refetch keeps the response-lost delete unknown and pricing untouched', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('response lost'), { isAxiosError: true })
    }
    let markRefetchStarted: (() => void) | undefined
    const refetchStarted = new Promise<void>((resolve) => {
      markRefetchStarted = resolve
    })
    queryClient.refetchQueries = (async () => {
      markRefetchStarted?.()
      throw new Error('model list unavailable')
    }) as typeof queryClient.refetchQueries
    let completed = false

    const outcome = await handleDeleteModel(1, queryClient, () => {
      completed = true
    })
    await refetchStarted

    assert.equal(outcome, 'unknown')
    assert.equal(completed, false)
    assert.deepEqual(pricingDocumentsFromCache(queryClient), baseDocuments)
    queryClient.clear()
  })

  test('never-settling reconciliation cannot delay the unknown outcome or warning', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('response lost'), { isAxiosError: true })
    }
    let markRefetchStarted: (() => void) | undefined
    const refetchStarted = new Promise<void>((resolve) => {
      markRefetchStarted = resolve
    })
    const never = new Promise<never>(() => undefined)
    queryClient.refetchQueries = (() => {
      markRefetchStarted?.()
      return never
    }) as typeof queryClient.refetchQueries
    const warningsBefore = warningSpy.mock.calls.length
    let observedOutcome: unknown

    void handleDeleteModel(1, queryClient).then((outcome) => {
      observedOutcome = outcome
    })
    await refetchStarted
    await Promise.resolve()
    await Promise.resolve()

    assert.equal(observedOutcome, 'unknown')
    assert.ok(
      warningSpy.mock.calls
        .slice(warningsBefore)
        .some((call: unknown[]) => call[0] === unknownDeleteMessage)
    )
    queryClient.clear()
  })

  test('unknown recovery never fetches pricing or overwrites a newer committed snapshot', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('response lost'), { isAxiosError: true })
    }
    const never = new Promise<never>(() => undefined)
    queryClient.refetchQueries = (() =>
      never) as typeof queryClient.refetchQueries

    const outcome = await handleDeleteModel(1, queryClient)
    await adoptCommittedPricingDocuments(queryClient, afterSecondDelete)

    assert.equal(outcome, 'unknown')
    assert.deepEqual(requestOrder, ['DELETE /api/models/1'])
    assert.deepEqual(pricingDocumentsFromCache(queryClient), afterSecondDelete)
    queryClient.clear()
  })

  for (const transport of [
    { name: 'HTTP 408', status: 408 },
    { name: 'HTTP 502', status: 502 },
    { name: 'HTTP 503', status: 503 },
    { name: 'HTTP 504', status: 504 },
    { name: 'HTTP 500 without API body', status: 500 },
    { name: 'ECONNABORTED', status: 500, code: 'ECONNABORTED' },
    { name: 'ETIMEDOUT', status: 500, code: 'ETIMEDOUT' },
  ]) {
    test(`${transport.name} without a trusted mutation result remains unknown`, async () => {
      const queryClient = new QueryClient()
      queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
      deleteResponder = async () => {
        throw Object.assign(new Error(transport.name), {
          isAxiosError: true,
          code: transport.code,
          response: { status: transport.status },
        })
      }
      let completed = false

      const outcome = await handleDeleteModel(1, queryClient, () => {
        completed = true
      })

      assert.equal(outcome, 'unknown')
      assert.equal(completed, false)
      queryClient.clear()
    })
  }

  test('HTTP 500 with a trusted API error body is a known failure', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('database rejected mutation'), {
        isAxiosError: true,
        response: {
          status: 500,
          data: { success: false, message: 'database rejected mutation' },
        },
      })
    }
    let completed = false

    const outcome = await handleDeleteModel(1, queryClient, () => {
      completed = true
    })

    assert.equal(outcome, 'failed')
    assert.equal(completed, false)
    queryClient.clear()
  })

  test('a gateway-timeout batch item is unknown rather than failed or successful', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      throw Object.assign(new Error('gateway timeout'), {
        isAxiosError: true,
        response: { status: 504 },
      })
    }
    let completed = false

    const outcome = await handleBatchDeleteModels([1], queryClient, () => {
      completed = true
    })

    assert.deepEqual(outcome, {
      successCount: 0,
      failedCount: 0,
      unknownIds: [1],
    })
    assert.equal(completed, false)
    queryClient.clear()
  })

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

  test('a committed single delete remains successful if the pricing cache disappears before adoption', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      queryClient.removeQueries({ queryKey: ['system-options'] })
      return {
        data: {
          success: true,
          data: null,
          committed: true,
          publication_pending: true,
          pricing_documents: afterFirstDelete,
        },
      }
    }
    let completed = false

    const outcome = await handleDeleteModel(1, queryClient, () => {
      completed = true
    })

    assert.equal(outcome, 'success')
    assert.equal(completed, true)
    assert.deepEqual(requestOrder, ['DELETE /api/models/1'])
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

  test('committed batch deletes remain successful when cache adoption and the callback fail', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    deleteResponder = async () => {
      queryClient.removeQueries({ queryKey: ['system-options'] })
      return {
        data: {
          success: true,
          data: null,
          committed: true,
          publication_pending: true,
          pricing_documents: afterFirstDelete,
        },
      }
    }

    const outcome = await handleBatchDeleteModels([1, 2], queryClient, () => {
      throw new Error('consumer unmounted')
    })

    assert.deepEqual(outcome, {
      successCount: 2,
      failedCount: 0,
      unknownIds: [],
    })
    assert.deepEqual(requestOrder, [
      'DELETE /api/models/1',
      'DELETE /api/models/2',
    ])
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

  test('batch delete reconciles a final response loss after an earlier pending commit', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(['system-options'], fullOptions(baseDocuments))
    let modelListInvalidations = 0
    let modelListRefetches = 0
    let systemOptionsInvalidations = 0
    const originalInvalidate = queryClient.invalidateQueries.bind(queryClient)
    const originalRefetch = queryClient.refetchQueries.bind(queryClient)
    let markModelListRefetched: (() => void) | undefined
    const modelListRefetched = new Promise<void>((resolve) => {
      markModelListRefetched = resolve
    })
    queryClient.invalidateQueries = (async (...args) => {
      const filters = args[0]
      if (filters && 'queryKey' in filters) {
        if (filters.queryKey?.[0] === 'models') modelListInvalidations += 1
        if (filters.queryKey?.[0] === 'system-options') {
          systemOptionsInvalidations += 1
        }
      }
      return originalInvalidate(...args)
    }) as typeof queryClient.invalidateQueries
    queryClient.refetchQueries = (async (...args) => {
      const filters = args[0]
      if (
        filters &&
        'queryKey' in filters &&
        filters.queryKey?.[0] === 'models'
      ) {
        modelListRefetches += 1
        markModelListRefetched?.()
      }
      return originalRefetch(...args)
    }) as typeof queryClient.refetchQueries
    deleteResponder = async () => {
      if (requestOrder.at(-1) === 'DELETE /api/models/2') {
        throw Object.assign(new Error('connection lost'), {
          isAxiosError: true,
        })
      }
      return {
        data: {
          success: true,
          data: null,
          committed: true,
          publication_pending: true,
          pricing_documents: afterFirstDelete,
        },
      }
    }
    let completedCount = -1

    const warningsBefore = warningSpy.mock.calls.length
    const outcome = await handleBatchDeleteModels(
      [1, 2],
      queryClient,
      (count) => {
        completedCount = count
      }
    )
    await modelListRefetched

    assert.deepEqual(requestOrder, [
      'DELETE /api/models/1',
      'DELETE /api/models/2',
    ])
    assert.deepEqual(pricingDocumentsFromCache(queryClient), afterFirstDelete)
    assert.equal(modelListInvalidations, 1)
    assert.equal(modelListRefetches, 1)
    assert.equal(systemOptionsInvalidations, 0)
    assert.equal(completedCount, -1)
    assert.deepEqual(outcome, {
      successCount: 1,
      failedCount: 0,
      unknownIds: [2],
    })
    assert.ok(
      warningSpy.mock.calls
        .slice(warningsBefore)
        .some(
          (call: unknown[]) =>
            call[0] ===
            'Delete result is unknown for 1 model(s). Do not retry; refresh and review the model list.'
        )
    )
    queryClient.clear()
  })
})
