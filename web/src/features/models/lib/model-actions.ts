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
import type { QueryClient, QueryKey } from '@tanstack/react-query'
import axios from 'axios'
import i18next from 'i18next'
import { toast } from 'sonner'

import {
  ensureSystemOptionsCacheBase,
  tryAdoptCommittedPricingDocuments,
} from '@/features/system-settings/models/pricing-document-cache'

import { updateModelStatus, deleteModel as deleteModelAPI } from '../api'
import { modelsQueryKeys } from './query-keys'

function isTransportUncertain(error: unknown): boolean {
  if (!axios.isAxiosError(error)) return false
  if (error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT') return true
  if (error.response === undefined) return true
  if ([408, 502, 503, 504].includes(error.response.status)) return true
  if (error.response.status < 500) return false
  const data = error.response.data
  const hasTrustedAPIErrorBody =
    typeof data === 'object' &&
    data !== null &&
    'success' in data &&
    data.success === false &&
    'message' in data &&
    typeof data.message === 'string'
  return !hasTrustedAPIErrorBody
}

async function reconcileUncertainModelDelete(
  queryClient: QueryClient
): Promise<void> {
  await queryClient.invalidateQueries({
    queryKey: modelsQueryKeys.lists(),
    refetchType: 'none',
  })
  await queryClient.refetchQueries({ queryKey: modelsQueryKeys.lists() })
}

function startBestEffortDeleteReconciliation(
  queryClient: QueryClient | undefined
): void {
  if (!queryClient) return
  void reconcileUncertainModelDelete(queryClient).catch(() => undefined)
}

function invalidateBestEffort(
  queryClient: QueryClient | undefined,
  queryKey: QueryKey
): void {
  if (!queryClient) return
  void queryClient.invalidateQueries({ queryKey }).catch(() => undefined)
}

function invokeBestEffort(callback: (() => void) | undefined): void {
  try {
    callback?.()
  } catch {
    // The server outcome is already known; a detached consumer cannot change it.
  }
}

function warnUncertainModelDelete(): void {
  toast.warning(
    i18next.t(
      'Delete result is unknown. Do not retry; refresh and review the model list.'
    )
  )
}

function warnUncertainBatchDelete(count: number): void {
  toast.warning(
    i18next.t(
      'Delete result is unknown for {{count}} model(s). Do not retry; refresh and review the model list.',
      { count }
    )
  )
}

export type ModelDeleteOutcome = 'success' | 'failed' | 'unknown'

export type BatchModelDeleteOutcome = {
  successCount: number
  failedCount: number
  unknownIds: number[]
}

// ============================================================================
// Model Status Actions
// ============================================================================

/**
 * Enable a model
 */
export async function handleEnableModel(
  id: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  try {
    const response = await updateModelStatus(id, 1)
    if (response.success) {
      toast.success(i18next.t('Model enabled successfully'))
      queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
      onSuccess?.()
    } else {
      toast.error(response.message || i18next.t('Failed to enable model'))
    }
  } catch (error: unknown) {
    toast.error(
      (error as Error)?.message || i18next.t('Failed to enable model')
    )
  }
}

/**
 * Disable a model
 */
export async function handleDisableModel(
  id: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  try {
    const response = await updateModelStatus(id, 0)
    if (response.success) {
      toast.success(i18next.t('Model disabled successfully'))
      queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
      onSuccess?.()
    } else {
      toast.error(response.message || i18next.t('Failed to disable model'))
    }
  } catch (error: unknown) {
    toast.error(
      (error as Error)?.message || i18next.t('Failed to disable model')
    )
  }
}

/**
 * Toggle model status
 */
export async function handleToggleModelStatus(
  id: number,
  currentStatus: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  if (currentStatus === 1) {
    await handleDisableModel(id, queryClient, onSuccess)
  } else {
    await handleEnableModel(id, queryClient, onSuccess)
  }
}

// ============================================================================
// Model Delete Actions
// ============================================================================

/**
 * Delete a single model
 */
export async function handleDeleteModel(
  id: number,
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<ModelDeleteOutcome> {
  let mutationAttempted = false
  let response: Awaited<ReturnType<typeof deleteModelAPI>>
  try {
    if (queryClient) {
      await ensureSystemOptionsCacheBase(queryClient)
    }
    mutationAttempted = true
    response = await deleteModelAPI(id)
  } catch (error: unknown) {
    if (mutationAttempted && isTransportUncertain(error)) {
      warnUncertainModelDelete()
      startBestEffortDeleteReconciliation(queryClient)
      return 'unknown'
    }
    toast.error(
      (error as Error)?.message || i18next.t('Failed to delete model')
    )
    return 'failed'
  }

  if (!response.success) {
    toast.error(response.message || i18next.t('Failed to delete model'))
    return 'failed'
  }

  let cacheAdopted = true
  if (queryClient && response.pricing_documents) {
    cacheAdopted = await tryAdoptCommittedPricingDocuments(
      queryClient,
      response.pricing_documents
    )
  }
  toast.success(i18next.t('Model deleted successfully'))
  invalidateBestEffort(queryClient, modelsQueryKeys.lists())
  if (response.publication_pending || !cacheAdopted) {
    toast.warning(
      i18next.t(
        'Pricing was saved, but live settings are still converging. Do not retry.'
      )
    )
  } else {
    invalidateBestEffort(queryClient, ['system-options'])
  }
  invokeBestEffort(onSuccess)
  return 'success'
}

/**
 * Batch delete models
 */
export async function handleBatchDeleteModels(
  ids: number[],
  queryClient?: QueryClient,
  onSuccess?: (deletedCount: number) => void
): Promise<BatchModelDeleteOutcome> {
  if (ids.length === 0) {
    toast.error(i18next.t('Please select at least one model'))
    return { successCount: 0, failedCount: 0, unknownIds: [] }
  }

  let successCount = 0
  let failedCount = 0
  let publicationPending = false
  let cacheAdoptionFailed = false
  const unknownIds: number[] = []

  if (queryClient) {
    try {
      await ensureSystemOptionsCacheBase(queryClient)
    } catch (error: unknown) {
      toast.error((error as Error)?.message || i18next.t('Batch delete failed'))
      return { successCount: 0, failedCount: ids.length, unknownIds: [] }
    }
  }
  for (const id of ids) {
    let response: Awaited<ReturnType<typeof deleteModelAPI>>
    try {
      response = await deleteModelAPI(id)
    } catch (error: unknown) {
      if (isTransportUncertain(error)) {
        unknownIds.push(id)
        continue
      }
      failedCount++
      // eslint-disable-next-line no-console
      console.error(`Failed to delete model ${id}:`, (error as Error)?.message)
      continue
    }
    if (!response.success) {
      failedCount++
      // eslint-disable-next-line no-console
      console.error(`Failed to delete model ${id}:`, response.message)
      continue
    }

    successCount++
    // 顺序删除下每条命令都重新发布完整文档集：后一次成功发布权威地取代
    // 之前的 pending 状态，因此这里刻意取最后一次响应（有测试钉住）。
    publicationPending = Boolean(response.publication_pending)
    if (queryClient && response.pricing_documents) {
      const adopted = await tryAdoptCommittedPricingDocuments(
        queryClient,
        response.pricing_documents
      )
      cacheAdoptionFailed ||= !adopted
    }
  }

  if (unknownIds.length > 0) {
    warnUncertainBatchDelete(unknownIds.length)
    startBestEffortDeleteReconciliation(queryClient)
  }

  if (successCount > 0) {
    toast.success(
      i18next.t('Successfully deleted {{count}} model(s)', {
        count: successCount,
      })
    )
    if (unknownIds.length === 0) {
      invalidateBestEffort(queryClient, modelsQueryKeys.lists())
      if (publicationPending || cacheAdoptionFailed) {
        toast.warning(
          i18next.t(
            'Pricing was saved, but live settings are still converging. Do not retry.'
          )
        )
      } else {
        invalidateBestEffort(queryClient, ['system-options'])
      }
    }
  }

  if (successCount > 0 && unknownIds.length === 0) {
    invokeBestEffort(() => onSuccess?.(successCount))
  }

  if (failedCount > 0) {
    toast.error(
      i18next.t('Failed to delete {{count}} model(s)', { count: failedCount })
    )
  }
  return { successCount, failedCount, unknownIds }
}

// ============================================================================
// Batch Status Actions
// ============================================================================

/**
 * Batch enable models
 */
export async function handleBatchEnableModels(
  ids: number[],
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  if (ids.length === 0) {
    toast.error(i18next.t('Please select at least one model'))
    return
  }

  try {
    const enablePromises = ids.map((id) => updateModelStatus(id, 1))
    const results = await Promise.all(enablePromises)

    let successCount = 0
    let failedCount = 0

    results.forEach((res) => {
      if (res.success) {
        successCount++
      } else {
        failedCount++
      }
    })

    if (successCount > 0) {
      toast.success(
        i18next.t('Successfully enabled {{count}} model(s)', {
          count: successCount,
        })
      )
      queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
      onSuccess?.()
    }

    if (failedCount > 0) {
      toast.error(
        i18next.t('Failed to enable {{count}} model(s)', { count: failedCount })
      )
    }
  } catch (error: unknown) {
    toast.error((error as Error)?.message || i18next.t('Batch enable failed'))
  }
}

/**
 * Batch disable models
 */
export async function handleBatchDisableModels(
  ids: number[],
  queryClient?: QueryClient,
  onSuccess?: () => void
): Promise<void> {
  if (ids.length === 0) {
    toast.error(i18next.t('Please select at least one model'))
    return
  }

  try {
    const disablePromises = ids.map((id) => updateModelStatus(id, 0))
    const results = await Promise.all(disablePromises)

    let successCount = 0
    let failedCount = 0

    results.forEach((res) => {
      if (res.success) {
        successCount++
      } else {
        failedCount++
      }
    })

    if (successCount > 0) {
      toast.success(
        i18next.t('Successfully disabled {{count}} model(s)', {
          count: successCount,
        })
      )
      queryClient?.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
      onSuccess?.()
    }

    if (failedCount > 0) {
      toast.error(
        i18next.t('Failed to disable {{count}} model(s)', {
          count: failedCount,
        })
      )
    }
  } catch (error: unknown) {
    toast.error((error as Error)?.message || i18next.t('Batch disable failed'))
  }
}
