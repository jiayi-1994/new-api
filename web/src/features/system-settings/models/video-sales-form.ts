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
  VIDEO_TOKEN_DIMENSIONS,
  videoSecondPrice,
  videoTokenPrice,
  videoTokensPerSecond,
  type VideoDimensionProfile,
} from '@/features/pricing/lib/price'
import type { VideoSalesModel, VideoSalesTier } from '@/features/pricing/types'

import {
  asciiFoldVideoModelName,
  canonicalVideoSalesTier,
  MAX_VIDEO_SALES_SECONDS,
} from './video-sales-config'

export type VideoPricingUnit = 'second' | 'token'

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

export function isInputVideoPrice(value: string): boolean {
  return (
    value.trim() !== '' && Number.isFinite(Number(value)) && Number(value) >= 0
  )
}

export function isPositivePrice(value: string): boolean {
  return isInputVideoPrice(value) && Number(value) > 0
}

/** The official output sizes a tier is token-priced with, or null outside the profile's table. */
export function videoSalesTokenSizes(
  resolution: string,
  profile: VideoDimensionProfile
): Array<[number, number]> | null {
  const tier = canonicalVideoSalesTier(resolution)
  return (tier && VIDEO_TOKEN_DIMENSIONS[profile][tier]) || null
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
          pricingUnit: z.enum(['second', 'token']),
          dimensionProfile: z.enum(['seedance-2.0', 'seedance-2.5']),
          tiers: z
            .array(
              z.object({
                resolution: z
                  .string()
                  .refine(
                    (value) => canonicalVideoSalesTier(value) !== null,
                    t('Enter a resolution such as 720p, 1080p, or 4k')
                  ),
                // Prices are validated in superRefine against the model's unit.
                price: z.string(),
                inputPrice: z.string(),
                tokenPrice: z.string(),
                withVideoTokenPrice: z.string(),
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
        const tokenPriced = model.pricingUnit === 'token'
        const tiers = new Set<string>()
        model.tiers.forEach((tier, tierIndex) => {
          const path = ['models', index, 'tiers', tierIndex]
          // A cleared field is an error, never a silent free price.
          const basePrice = tokenPriced ? 'tokenPrice' : 'price'
          if (!isPositivePrice(tier[basePrice])) {
            context.addIssue({
              code: 'custom',
              path: [...path, basePrice],
              message: tokenPriced
                ? t('Price per 1M tokens must be a positive number')
                : t('Price per second must be a positive number'),
            })
          }
          const referencePrice = tokenPriced
            ? 'withVideoTokenPrice'
            : 'inputPrice'
          if (!isInputVideoPrice(tier[referencePrice])) {
            context.addIssue({
              code: 'custom',
              path: [...path, referencePrice],
              message: t(
                'Input video price must be 0 or more; 0 means no extra charge'
              ),
            })
          } else if (
            model.officialReferenceBilling &&
            Number(tier[referencePrice]) === 0
          ) {
            // The with-reference price bills the whole order; 0 would make it free.
            context.addIssue({
              code: 'custom',
              path: [...path, referencePrice],
              message: t(
                'Official reference billing needs a positive with-reference price'
              ),
            })
          }
          const canonical = canonicalVideoSalesTier(tier.resolution)
          if (!canonical) return
          if (canonical === '4k' && model.dimensionProfile === 'seedance-2.5') {
            context.addIssue({
              code: 'custom',
              path: [...path, 'resolution'],
              message: t('Seedance 2.5 has no 4k output'),
            })
          } else if (
            tokenPriced &&
            !videoSalesTokenSizes(canonical, model.dimensionProfile)
          ) {
            // Token pricing needs an official output size to count tokens.
            context.addIssue({
              code: 'custom',
              path: [...path, 'resolution'],
              message: t(
                'Token pricing supports only 480p, 720p, 1080p and 4k'
              ),
            })
          }
          if (tiers.has(canonical)) {
            context.addIssue({
              code: 'custom',
              path: [...path, 'resolution'],
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
type VideoSalesFormModel = VideoSalesFormValues['models'][number]

export function videoSalesFormValues(
  sales: Record<string, VideoSalesModel>
): VideoSalesFormValues {
  return {
    models: Object.entries(sales).map(([name, model]) => ({
      name,
      disabled: model.disabled ?? false,
      officialReferenceBilling: model.official_reference_billing ?? false,
      pricingUnit: model.pricing_unit ?? 'second',
      dimensionProfile: model.dimension_profile ?? 'seedance-2.0',
      tiers: Object.entries(model.resolutions).map(([resolution, tier]) => ({
        resolution,
        price: String(tier.usd_per_second),
        inputPrice: String(tier.input_video_usd_per_second ?? 0),
        tokenPrice: String(tier.usd_per_million_tokens ?? ''),
        withVideoTokenPrice: String(
          tier.with_video_usd_per_million_tokens ?? 0
        ),
        seconds: tier.seconds.join(', '),
      })),
    })),
  }
}

/**
 * Tiers prefilled for a pricing unit switch: each price is converted at the
 * tier's 16:9 token rate for the administrator to confirm. A tier without an
 * official size, or with an invalid source price, is left empty to fill in.
 */
export function videoSalesTiersForUnit(
  model: VideoSalesFormModel,
  unit: VideoPricingUnit
): VideoSalesFormModel['tiers'] {
  return model.tiers.map((tier) => {
    const canonical = canonicalVideoSalesTier(tier.resolution)
    const rate = canonical
      ? videoTokensPerSecond(canonical, model.dimensionProfile)
      : null
    const convert = (value: string, toToken: boolean) => {
      if (rate === null || !isInputVideoPrice(value)) return ''
      return String(
        toToken
          ? videoTokenPrice(Number(value), rate)
          : videoSecondPrice(Number(value), rate)
      )
    }
    if (unit === 'token') {
      return {
        ...tier,
        tokenPrice: convert(tier.price, true),
        withVideoTokenPrice: convert(tier.inputPrice, true),
      }
    }
    return {
      ...tier,
      price: convert(tier.tokenPrice, false),
      inputPrice: convert(tier.withVideoTokenPrice, false),
    }
  })
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
        // Omitted when off or default, like the server's omitempty.
        ...(model.officialReferenceBilling && {
          official_reference_billing: true,
        }),
        ...(model.pricingUnit === 'token' && { pricing_unit: 'token' }),
        ...(model.dimensionProfile === 'seedance-2.5' && {
          dimension_profile: 'seedance-2.5',
        }),
        resolutions: Object.fromEntries(
          model.tiers.map((tier) => {
            const seconds = parseVideoSalesSeconds(tier.seconds) ?? []
            // A token tier sells no seconds; 0 is what the server stores.
            const price: VideoSalesTier =
              model.pricingUnit === 'token'
                ? {
                    usd_per_second: 0,
                    usd_per_million_tokens: Number(tier.tokenPrice),
                    with_video_usd_per_million_tokens: Number(
                      tier.withVideoTokenPrice
                    ),
                    seconds,
                  }
                : {
                    usd_per_second: Number(tier.price),
                    input_video_usd_per_second: Number(tier.inputPrice),
                    seconds,
                  }
            return [
              canonicalVideoSalesTier(tier.resolution) ?? tier.resolution,
              price,
            ]
          })
        ),
      },
    ])
  )
}
