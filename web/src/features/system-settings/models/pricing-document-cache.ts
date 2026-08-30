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

import type { SystemOptionsResponse } from '../types'
import {
  PRICING_DOCUMENT_KEYS,
  type PricingDocumentKey,
} from './model-pricing-persistence'

export async function adoptCommittedPricingDocuments(
  queryClient: QueryClient,
  documents: Record<PricingDocumentKey, string>
): Promise<void> {
  await queryClient.cancelQueries({ queryKey: ['system-options'] })
  queryClient.setQueryData<SystemOptionsResponse>(
    ['system-options'],
    (current) => ({
      success: true,
      message: current?.message ?? '',
      data: [
        ...(current?.data.filter(
          (option) =>
            !PRICING_DOCUMENT_KEYS.includes(option.key as PricingDocumentKey)
        ) ?? []),
        ...PRICING_DOCUMENT_KEYS.map((key) => ({
          key,
          value: documents[key],
        })),
      ],
    })
  )
}
