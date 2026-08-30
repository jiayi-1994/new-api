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
import type { QueryClient } from '@tanstack/react-query'

import { getSystemOptions } from '../api'
import type { SystemOptionsResponse } from '../types'
import {
  PRICING_DOCUMENT_KEYS,
  type PricingDocumentKey,
} from './model-pricing-persistence'

const SYSTEM_OPTIONS_QUERY_KEY = ['system-options'] as const

function hasCompletePricingDocuments(
  response: SystemOptionsResponse | undefined
): response is SystemOptionsResponse {
  return Boolean(
    response?.success &&
    PRICING_DOCUMENT_KEYS.every((key) =>
      response.data.some((option) => option.key === key)
    )
  )
}

export async function ensureSystemOptionsCacheBase(
  queryClient: QueryClient
): Promise<SystemOptionsResponse> {
  const cached = queryClient.getQueryData<SystemOptionsResponse>(
    SYSTEM_OPTIONS_QUERY_KEY
  )
  if (hasCompletePricingDocuments(cached)) return cached

  const response = await queryClient.fetchQuery({
    queryKey: SYSTEM_OPTIONS_QUERY_KEY,
    queryFn: getSystemOptions,
  })
  if (!hasCompletePricingDocuments(response)) {
    throw new Error('System options response is missing pricing documents')
  }
  return response
}

export async function adoptCommittedPricingDocuments(
  queryClient: QueryClient,
  documents: Record<PricingDocumentKey, string>
): Promise<void> {
  await queryClient.cancelQueries({ queryKey: SYSTEM_OPTIONS_QUERY_KEY })
  const current = queryClient.getQueryData<SystemOptionsResponse>(
    SYSTEM_OPTIONS_QUERY_KEY
  )
  if (!hasCompletePricingDocuments(current)) {
    throw new Error(
      'System options cache must be initialized before adopting pricing documents'
    )
  }
  queryClient.setQueryData<SystemOptionsResponse>(
    SYSTEM_OPTIONS_QUERY_KEY,
    () => ({
      success: true,
      message: current.message,
      data: [
        ...current.data.filter(
          (option) =>
            !PRICING_DOCUMENT_KEYS.includes(option.key as PricingDocumentKey)
        ),
        ...PRICING_DOCUMENT_KEYS.map((key) => ({
          key,
          value: documents[key],
        })),
      ],
    })
  )
}

export async function tryAdoptCommittedPricingDocuments(
  queryClient: QueryClient,
  documents: Record<PricingDocumentKey, string>
): Promise<boolean> {
  try {
    await adoptCommittedPricingDocuments(queryClient, documents)
    return true
  } catch {
    void queryClient
      .invalidateQueries({ queryKey: SYSTEM_OPTIONS_QUERY_KEY })
      .catch(() => undefined)
    return false
  }
}
