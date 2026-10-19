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
import { z } from 'zod'

import type { VideoSalesModel } from '@/features/pricing/types'

// Keep aligned with relay/common.MaxTaskDurationSeconds.
export const MAX_VIDEO_SALES_SECONDS = 3600

export function asciiFoldVideoModelName(name: string): string {
  return name.replaceAll(/[A-Z]/g, (letter) => letter.toLowerCase())
}

export function canonicalVideoSalesTier(tier: string): string | null {
  const normalized = tier.trim().toLowerCase()
  if (normalized === '4k' || normalized === '2160p') return '4k'
  if (!/^[1-9]\d*p$/.test(normalized)) return null
  const height = Number(normalized.slice(0, -1))
  return Number.isSafeInteger(height) ? normalized : null
}

const videoSalesModelSchema = z
  .object({
    disabled: z.boolean().optional(),
    official_reference_billing: z.boolean().optional(),
    pricing_unit: z.literal('token').optional(),
    dimension_profile: z.literal('seedance-2.5').optional(),
    resolutions: z.record(
      z.string(),
      z.object({
        // A token tier stores an unused 0 per-second price.
        usd_per_second: z.number().finite().min(0),
        input_video_usd_per_second: z.number().finite().min(0).optional(),
        usd_per_million_tokens: z.number().finite().positive().optional(),
        with_video_usd_per_million_tokens: z
          .number()
          .finite()
          .min(0)
          .optional(),
        seconds: z
          .array(z.number().int().min(1).max(MAX_VIDEO_SALES_SECONDS))
          .min(1),
      })
    ),
  })
  .refine((model) =>
    Object.values(model.resolutions).every((tier) =>
      model.pricing_unit === 'token'
        ? tier.usd_per_million_tokens !== undefined
        : tier.usd_per_second > 0
    )
  )

/** Invalid source data must never be silently replaced with an empty sales table. */
export function parseVideoSales(
  value: string
): Record<string, VideoSalesModel> | null {
  try {
    const parsed: unknown = JSON.parse(value || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    const entries: Array<[string, VideoSalesModel]> = []
    for (const [name, entry] of Object.entries(parsed)) {
      const checked = videoSalesModelSchema.safeParse(entry)
      if (!checked.success) return null
      entries.push([name, checked.data])
    }
    return Object.fromEntries(entries)
  } catch {
    return null
  }
}
