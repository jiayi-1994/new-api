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
import { combineBillingExpr } from '@/features/pricing/lib/billing-expr'

import { safeJsonParse } from '../utils/json-parser'
import {
  isCompleteFinitePricingNumber,
  type ModelRatioData,
} from './model-pricing-core'
import { normalizeJsonString } from './utils'
import {
  parseVideoResolutionPriceOption,
  sanitizeVideoResolutionPriceMap,
  serializeVideoResolutionPriceOption,
  type VideoResolutionPriceMap,
} from './video-resolution-pricing'

export const PRICING_DOCUMENT_KEYS = [
  'AudioCompletionRatio',
  'AudioRatio',
  'CacheRatio',
  'CompletionRatio',
  'CreateCacheRatio',
  'ImageRatio',
  'ModelPrice',
  'ModelRatio',
  'TaskBillingMode',
  'VideoResolutionPrice',
  'billing_setting.billing_expr',
  'billing_setting.billing_mode',
] as const

export type PricingDocumentKey = (typeof PRICING_DOCUMENT_KEYS)[number]

export type PricingDocuments = {
  ModelPrice: Record<string, number>
  ModelRatio: Record<string, number>
  CacheRatio: Record<string, number>
  CreateCacheRatio: Record<string, number>
  CompletionRatio: Record<string, number>
  ImageRatio: Record<string, number>
  AudioRatio: Record<string, number>
  AudioCompletionRatio: Record<string, number>
  'billing_setting.billing_mode': Record<string, string>
  'billing_setting.billing_expr': Record<string, string>
  TaskBillingMode: Record<string, string>
  VideoResolutionPrice: Record<string, VideoResolutionPriceMap>
}

export type ModelPricingSelection = {
  mode: 'per_request' | 'per_token' | 'tiered_expr' | 'video_resolution'
  price?: number
  ratio?: number
  cache_ratio?: number
  create_cache_ratio?: number
  completion_ratio?: number
  image_ratio?: number
  audio_ratio?: number
  audio_completion_ratio?: number
  billing_expr?: string
  task_billing_mode?: string
  resolution_prices?: VideoResolutionPriceMap
}

export type ModelPricingMutation =
  | {
      kind: 'save'
      name: string
      selection: ModelPricingSelection
    }
  | {
      kind: 'rename' | 'copy'
      sourceName: string
      targetName: string
      selection?: ModelPricingSelection
    }
  | { kind: 'delete'; name: string }

export type PricingDocumentReplacement = {
  values: Partial<Record<PricingDocumentKey, string>>
  expected_documents: Partial<Record<PricingDocumentKey, string>>
}

export function buildPricingDocumentReplacement(
  currentNormalized: Partial<Record<PricingDocumentKey, string>>,
  nextValues: Partial<Record<PricingDocumentKey, string>>,
  expectedRaw: Partial<Record<PricingDocumentKey, string>>
): PricingDocumentReplacement {
  const values: Partial<Record<PricingDocumentKey, string>> = {}
  const expectedDocuments: Partial<Record<PricingDocumentKey, string>> = {}
  for (const key of PRICING_DOCUMENT_KEYS) {
    const next = nextValues[key]
    if (next === undefined) continue
    const normalized = normalizeJsonString(next)
    if (normalized === currentNormalized[key]) continue
    values[key] = normalized
    expectedDocuments[key] = expectedRaw[key] ?? ''
  }
  return { values, expected_documents: expectedDocuments }
}

const numericDocumentKeys = [
  'ModelPrice',
  'ModelRatio',
  'CacheRatio',
  'CreateCacheRatio',
  'CompletionRatio',
  'ImageRatio',
  'AudioRatio',
  'AudioCompletionRatio',
] as const

const stringDocumentKeys = [
  'billing_setting.billing_mode',
  'billing_setting.billing_expr',
  'TaskBillingMode',
] as const

function numberOrUndefined(value?: string): number | undefined {
  return isCompleteFinitePricingNumber(value) ? Number(value) : undefined
}

export function buildModelPricingSelection(
  data: ModelRatioData
): ModelPricingSelection {
  const allNumbers = {
    price: numberOrUndefined(data.price),
    ratio: numberOrUndefined(data.ratio),
    cache_ratio: numberOrUndefined(data.cacheRatio),
    create_cache_ratio: numberOrUndefined(data.createCacheRatio),
    completion_ratio: numberOrUndefined(data.completionRatio),
    image_ratio: numberOrUndefined(data.imageRatio),
    audio_ratio: numberOrUndefined(data.audioRatio),
    audio_completion_ratio: numberOrUndefined(data.audioCompletionRatio),
  }
  const billingExpr = combineBillingExpr(
    data.billingExpr || '',
    data.requestRuleExpr || ''
  )

  if (data.billingMode === 'video_resolution') {
    return {
      mode: 'video_resolution',
      ...allNumbers,
      ...(billingExpr ? { billing_expr: billingExpr } : {}),
      ...(data.taskBillingMode
        ? { task_billing_mode: data.taskBillingMode }
        : {}),
      resolution_prices: sanitizeVideoResolutionPriceMap(data.resolutionPrices),
    }
  }
  if (data.billingMode === 'tiered_expr') {
    return {
      mode: 'tiered_expr',
      ...allNumbers,
      ...(billingExpr ? { billing_expr: billingExpr } : {}),
    }
  }
  if (data.billingMode === 'per-request') {
    return {
      mode: 'per_request',
      ...(allNumbers.price === undefined ? {} : { price: allNumbers.price }),
      ...(data.taskBillingMode
        ? { task_billing_mode: data.taskBillingMode }
        : {}),
    }
  }
  return {
    mode: 'per_token',
    ...(allNumbers.ratio === undefined ? {} : { ratio: allNumbers.ratio }),
    ...(allNumbers.cache_ratio === undefined
      ? {}
      : { cache_ratio: allNumbers.cache_ratio }),
    ...(allNumbers.create_cache_ratio === undefined
      ? {}
      : { create_cache_ratio: allNumbers.create_cache_ratio }),
    ...(allNumbers.completion_ratio === undefined
      ? {}
      : { completion_ratio: allNumbers.completion_ratio }),
    ...(allNumbers.image_ratio === undefined
      ? {}
      : { image_ratio: allNumbers.image_ratio }),
    ...(allNumbers.audio_ratio === undefined
      ? {}
      : { audio_ratio: allNumbers.audio_ratio }),
    ...(allNumbers.audio_completion_ratio === undefined
      ? {}
      : { audio_completion_ratio: allNumbers.audio_completion_ratio }),
  }
}

export function parsePricingDocuments(
  raw: Record<PricingDocumentKey, string>
): PricingDocuments {
  return {
    ModelPrice: safeJsonParse(raw.ModelPrice, { fallback: {}, silent: true }),
    ModelRatio: safeJsonParse(raw.ModelRatio, { fallback: {}, silent: true }),
    CacheRatio: safeJsonParse(raw.CacheRatio, { fallback: {}, silent: true }),
    CreateCacheRatio: safeJsonParse(raw.CreateCacheRatio, {
      fallback: {},
      silent: true,
    }),
    CompletionRatio: safeJsonParse(raw.CompletionRatio, {
      fallback: {},
      silent: true,
    }),
    ImageRatio: safeJsonParse(raw.ImageRatio, { fallback: {}, silent: true }),
    AudioRatio: safeJsonParse(raw.AudioRatio, { fallback: {}, silent: true }),
    AudioCompletionRatio: safeJsonParse(raw.AudioCompletionRatio, {
      fallback: {},
      silent: true,
    }),
    'billing_setting.billing_mode': safeJsonParse(
      raw['billing_setting.billing_mode'],
      { fallback: {}, silent: true }
    ),
    'billing_setting.billing_expr': safeJsonParse(
      raw['billing_setting.billing_expr'],
      { fallback: {}, silent: true }
    ),
    TaskBillingMode: safeJsonParse(raw.TaskBillingMode, {
      fallback: {},
      silent: true,
    }),
    VideoResolutionPrice: parseVideoResolutionPriceOption(
      raw.VideoResolutionPrice
    ),
  }
}

export function serializePricingDocuments(
  documents: PricingDocuments
): Record<PricingDocumentKey, string> {
  return {
    ModelPrice: JSON.stringify(documents.ModelPrice, null, 2),
    ModelRatio: JSON.stringify(documents.ModelRatio, null, 2),
    CacheRatio: JSON.stringify(documents.CacheRatio, null, 2),
    CreateCacheRatio: JSON.stringify(documents.CreateCacheRatio, null, 2),
    CompletionRatio: JSON.stringify(documents.CompletionRatio, null, 2),
    ImageRatio: JSON.stringify(documents.ImageRatio, null, 2),
    AudioRatio: JSON.stringify(documents.AudioRatio, null, 2),
    AudioCompletionRatio: JSON.stringify(
      documents.AudioCompletionRatio,
      null,
      2
    ),
    'billing_setting.billing_mode': JSON.stringify(
      documents['billing_setting.billing_mode'],
      null,
      2
    ),
    'billing_setting.billing_expr': JSON.stringify(
      documents['billing_setting.billing_expr'],
      null,
      2
    ),
    TaskBillingMode: JSON.stringify(documents.TaskBillingMode, null, 2),
    VideoResolutionPrice: serializeVideoResolutionPriceOption(
      documents.VideoResolutionPrice
    ),
  }
}

function clonePricingDocuments(documents: PricingDocuments): PricingDocuments {
  const clone = {} as PricingDocuments
  for (const key of numericDocumentKeys) clone[key] = { ...documents[key] }
  for (const key of stringDocumentKeys) clone[key] = { ...documents[key] }
  clone.VideoResolutionPrice = Object.fromEntries(
    Object.entries(documents.VideoResolutionPrice).map(([name, prices]) => [
      name,
      { ...prices },
    ])
  )
  return clone
}

function deletePricingName(documents: PricingDocuments, name: string) {
  for (const key of numericDocumentKeys) delete documents[key][name]
  for (const key of stringDocumentKeys) delete documents[key][name]
  delete documents.VideoResolutionPrice[name]
}

function assertCompletePricingSelection(selection: ModelPricingSelection) {
  const numericValues = [
    selection.price,
    selection.ratio,
    selection.cache_ratio,
    selection.create_cache_ratio,
    selection.completion_ratio,
    selection.image_ratio,
    selection.audio_ratio,
    selection.audio_completion_ratio,
  ]
  if (
    numericValues.some(
      (value) => value !== undefined && !isCompleteFinitePricingNumber(value)
    )
  ) {
    throw new Error('pricing selection contains an invalid number')
  }
  if (selection.mode === 'per_request' && selection.price === undefined) {
    throw new Error('per-request pricing requires a complete fixed price')
  }
  if (selection.mode === 'per_token' && selection.ratio === undefined) {
    throw new Error('per-token pricing requires a complete model ratio')
  }
}

function applySelection(
  documents: PricingDocuments,
  name: string,
  selection: ModelPricingSelection
) {
  if (selection.mode === 'video_resolution') {
    const prices = sanitizeVideoResolutionPriceMap(selection.resolution_prices)
    if (Object.keys(prices).length === 0) {
      throw new Error(
        'video resolution pricing requires at least one resolution'
      )
    }
    documents.VideoResolutionPrice[name] = prices
    return
  }

  deletePricingName(documents, name)
  if (selection.mode === 'per_request') {
    if (selection.price !== undefined) {
      documents.ModelPrice[name] = selection.price
    }
    if (selection.task_billing_mode) {
      documents.TaskBillingMode[name] = selection.task_billing_mode
    }
    return
  }

  const setNumbers = () => {
    if (selection.ratio !== undefined) {
      documents.ModelRatio[name] = selection.ratio
    }
    if (selection.cache_ratio !== undefined) {
      documents.CacheRatio[name] = selection.cache_ratio
    }
    if (selection.create_cache_ratio !== undefined) {
      documents.CreateCacheRatio[name] = selection.create_cache_ratio
    }
    if (selection.completion_ratio !== undefined) {
      documents.CompletionRatio[name] = selection.completion_ratio
    }
    if (selection.image_ratio !== undefined) {
      documents.ImageRatio[name] = selection.image_ratio
    }
    if (selection.audio_ratio !== undefined) {
      documents.AudioRatio[name] = selection.audio_ratio
    }
    if (selection.audio_completion_ratio !== undefined) {
      documents.AudioCompletionRatio[name] = selection.audio_completion_ratio
    }
  }

  if (selection.mode === 'tiered_expr') {
    documents['billing_setting.billing_mode'][name] = 'tiered_expr'
    if (selection.billing_expr) {
      documents['billing_setting.billing_expr'][name] = selection.billing_expr
    }
    if (selection.price !== undefined) {
      documents.ModelPrice[name] = selection.price
    }
    setNumbers()
    return
  }
  setNumbers()
}

export function applyModelPricingMutation(
  documents: PricingDocuments,
  mutation: ModelPricingMutation
): PricingDocuments {
  if ('selection' in mutation && mutation.selection) {
    assertCompletePricingSelection(mutation.selection)
  }
  const next = clonePricingDocuments(documents)

  if (mutation.kind === 'delete') {
    deletePricingName(next, mutation.name)
    return next
  }
  if (mutation.kind === 'save') {
    applySelection(next, mutation.name, mutation.selection)
    return next
  }

  for (const key of numericDocumentKeys) {
    const value = next[key][mutation.sourceName]
    if (mutation.kind === 'rename') {
      if (value !== undefined) {
        delete next[key][mutation.sourceName]
        next[key][mutation.targetName] = value
      }
    } else if (value === undefined) {
      delete next[key][mutation.targetName]
    } else {
      next[key][mutation.targetName] = value
    }
  }
  for (const key of stringDocumentKeys) {
    const value = next[key][mutation.sourceName]
    if (mutation.kind === 'rename') {
      if (value !== undefined) {
        delete next[key][mutation.sourceName]
        next[key][mutation.targetName] = value
      }
    } else if (value === undefined) {
      delete next[key][mutation.targetName]
    } else {
      next[key][mutation.targetName] = value
    }
  }

  const prices = next.VideoResolutionPrice[mutation.sourceName]
  if (mutation.kind === 'rename') {
    if (prices !== undefined) {
      delete next.VideoResolutionPrice[mutation.sourceName]
      next.VideoResolutionPrice[mutation.targetName] = { ...prices }
    }
  } else if (prices === undefined) {
    delete next.VideoResolutionPrice[mutation.targetName]
  } else {
    next.VideoResolutionPrice[mutation.targetName] = { ...prices }
  }

  if (mutation.selection) {
    applySelection(next, mutation.targetName, mutation.selection)
  }
  return next
}
