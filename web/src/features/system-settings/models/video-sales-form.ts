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

import {
  videoInputSecondPrice,
  videoInputTokenPrice,
  videoInputTokensPerSecond,
} from '@/features/pricing/lib/price'
import type { VideoSalesModel } from '@/features/pricing/types'

import {
  asciiFoldVideoModelName,
  canonicalVideoSalesTier,
  MAX_VIDEO_SALES_SECONDS,
} from './video-sales-config'

export function parseVideoSalesSeconds(value: string): number[] | null {
  if (!/^\s*\d+(?:[\s,]+\d+)*\s*$/.test(value)) return null
  const seconds = value
    .trim()
    .split(/[\s,]+/)
    .map(Number)
  return seconds.every(
    (duration) =>
      Number.isInteger(duration) &&
      duration >= 1 &&
      duration <= MAX_VIDEO_SALES_SECONDS
  )
    ? [...new Set(seconds)].sort((a, b) => a - b)
    : null
}

/** Tiers with an official token rate take the reference video price per 1M tokens. */
export function videoSalesInputTokensPerSecond(
  resolution: string
): number | null {
  const tier = canonicalVideoSalesTier(resolution)
  return tier ? videoInputTokensPerSecond(tier) : null
}

export function isInputVideoPrice(value: string): boolean {
  return (
    value.trim() !== '' && Number.isFinite(Number(value)) && Number(value) >= 0
  )
}

type Translate = (key: string, options?: Record<string, unknown>) => string

export function createVideoSalesFormSchema(t: Translate) {
  return z
    .object({
      models: z.array(
        z.object({
          name: z
            .string()
            .min(1, t('Model name is required'))
            .refine(
              (name) => name === name.trim(),
              t('Model name must not contain surrounding spaces')
            ),
          disabled: z.boolean(),
          officialReferenceBilling: z.boolean(),
          tiers: z
            .array(
              z.object({
                resolution: z
                  .string()
                  .refine(
                    (value) => canonicalVideoSalesTier(value) !== null,
                    t('Enter a resolution such as 720p, 1080p, or 4k')
                  ),
                price: z
                  .string()
                  .refine(
                    (value) =>
                      value.trim() !== '' &&
                      Number.isFinite(Number(value)) &&
                      Number(value) > 0,
                    t('Price per second must be a positive number')
                  ),
                // Validated in superRefine against the field the tier shows.
                inputPrice: z.string(),
                inputTokenPrice: z.string(),
                seconds: z
                  .string()
                  .refine(
                    (value) => parseVideoSalesSeconds(value) !== null,
                    t(
                      'Enter whole seconds from 1 to {{max}}, separated by commas',
                      { max: MAX_VIDEO_SALES_SECONDS }
                    )
                  ),
              })
            )
            .min(1, t('Add at least one resolution')),
        })
      ),
    })
    .superRefine((values, context) => {
      const models = new Set<string>()
      values.models.forEach((model, index) => {
        const folded = asciiFoldVideoModelName(model.name)
        if (models.has(folded)) {
          context.addIssue({
            code: 'custom',
            path: ['models', index, 'name'],
            message: t('Model names must be unique regardless of letter case'),
          })
        }
        models.add(folded)
        const tiers = new Set<string>()
        model.tiers.forEach((tier, tierIndex) => {
          // A cleared field is an error, never a silent free price.
          const tokenPriced =
            videoSalesInputTokensPerSecond(tier.resolution) !== null
          const priceField = tokenPriced ? 'inputTokenPrice' : 'inputPrice'
          if (!isInputVideoPrice(tier[priceField])) {
            context.addIssue({
              code: 'custom',
              path: ['models', index, 'tiers', tierIndex, priceField],
              message: t(
                'Input video price must be 0 or more; 0 means no extra charge'
              ),
            })
          } else if (
            model.officialReferenceBilling &&
            Number(tier[priceField]) === 0
          ) {
            // The with-reference price bills the whole order; 0 would make it free.
            context.addIssue({
              code: 'custom',
              path: ['models', index, 'tiers', tierIndex, priceField],
              message: t(
                'Official reference billing needs a positive with-reference price'
              ),
            })
          }
          const canonical = canonicalVideoSalesTier(tier.resolution)
          if (!canonical) return
          if (tiers.has(canonical)) {
            context.addIssue({
              code: 'custom',
              path: ['models', index, 'tiers', tierIndex, 'resolution'],
              message: t(
                'Resolution is duplicated; 2160p and 4k are the same tier'
              ),
            })
          }
          tiers.add(canonical)
        })
      })
    })
}

export type VideoSalesFormValues = z.infer<
  ReturnType<typeof createVideoSalesFormSchema>
>

export function videoSalesFormValues(
  sales: Record<string, VideoSalesModel>
): VideoSalesFormValues {
  return {
    models: Object.entries(sales).map(([name, model]) => ({
      name,
      disabled: model.disabled ?? false,
      officialReferenceBilling: model.official_reference_billing ?? false,
      tiers: Object.entries(model.resolutions).map(([resolution, tier]) => {
        const inputPrice = tier.input_video_usd_per_second ?? 0
        const tokensPerSecond = videoSalesInputTokensPerSecond(resolution)
        return {
          resolution,
          price: String(tier.usd_per_second),
          inputPrice: String(inputPrice),
          inputTokenPrice:
            tokensPerSecond === null
              ? ''
              : String(videoInputTokenPrice(inputPrice, tokensPerSecond)),
          seconds: tier.seconds.join(', '),
        }
      }),
    })),
  }
}

/** Called only after form validation; carries every model into the whole-table option. */
export function videoSalesFromForm(
  values: VideoSalesFormValues
): Record<string, VideoSalesModel> {
  return Object.fromEntries(
    values.models.map((model) => [
      model.name,
      {
        disabled: model.disabled,
        // Omitted when off, like the server's omitempty.
        ...(model.officialReferenceBilling && {
          official_reference_billing: true,
        }),
        resolutions: Object.fromEntries(
          model.tiers.map((tier) => {
            const tokensPerSecond = videoSalesInputTokensPerSecond(
              tier.resolution
            )
            return [
              canonicalVideoSalesTier(tier.resolution) ?? tier.resolution,
              {
                usd_per_second: Number(tier.price),
                input_video_usd_per_second:
                  tokensPerSecond === null
                    ? Number(tier.inputPrice)
                    : videoInputSecondPrice(
                        Number(tier.inputTokenPrice),
                        tokensPerSecond
                      ),
                seconds: parseVideoSalesSeconds(tier.seconds) ?? [],
              },
            ]
          })
        ),
      },
    ])
  )
}
