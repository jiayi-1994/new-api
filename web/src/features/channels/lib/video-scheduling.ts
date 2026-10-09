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

import type {
  VideoCostMode,
  VideoModelCost,
  VideoReferenceKind,
  VideoReferenceMode,
  VideoSchedulingConfig,
} from '../types'

// Mirrors relaykit dto.VideoSchedulingConfig.Validate. The USD upper bound
// depends on QuotaPerUnit and stays server-side.
export const VIDEO_REFERENCE_KINDS: VideoReferenceKind[] = [
  'video',
  'image',
  'audio',
  'frame',
]
export const VIDEO_REFERENCE_CHARGING_MODES: VideoReferenceMode[] = [
  'per_request',
  'per_input',
  'per_output_second',
  'per_input_second',
  'multiplier',
]
export const MAX_TASK_DURATION_SECONDS = 3600
const MAX_CAPACITY_GROUP_BYTES = 64

/** A reference fee row; mode '' is "not configured" and is never saved. */
export type VideoReferenceDraft = {
  kind: VideoReferenceKind
  tier: string
  mode: VideoReferenceMode | ''
  value: string
}

export type VideoModelDraft = {
  model: string
  mode: VideoCostMode
  prices: { tier: string; price: string }[]
  min_seconds: string
  max_seconds: string
  allowed_seconds: string
  allowed_seconds_by_resolution?: Record<string, number[]>
  references: VideoReferenceDraft[]
}

/** Form state for settings.video_scheduling; numbers stay strings so empty is never 0. */
export type VideoSchedulingDraft = {
  enabled: boolean
  quality: string
  capacity: string
  capacity_group: string
  models: VideoModelDraft[]
}

export const EMPTY_VIDEO_SCHEDULING_DRAFT: VideoSchedulingDraft = {
  enabled: false,
  quality: '',
  capacity: '',
  capacity_group: '',
  models: [],
}

export function newVideoModelDraft(model: string): VideoModelDraft {
  return {
    model,
    mode: 'per_video',
    prices: [{ tier: '*', price: '' }],
    min_seconds: '',
    max_seconds: '',
    allowed_seconds: '',
    references: [],
  }
}

function draftNumber(value: unknown): string {
  return typeof value === 'number' && Number.isFinite(value)
    ? String(value)
    : ''
}

export function parseVideoSchedulingDraft(
  config: unknown
): VideoSchedulingDraft {
  if (!config || typeof config !== 'object') return EMPTY_VIDEO_SCHEDULING_DRAFT
  const stored = config as Partial<VideoSchedulingConfig>
  const models = Object.entries(stored.models ?? {}).map(
    ([model, cost]: [string, VideoModelCost]): VideoModelDraft => ({
      model,
      mode: cost.mode === 'per_second' ? 'per_second' : 'per_video',
      prices: Object.entries(cost.prices ?? {}).map(([tier, price]) => ({
        tier,
        price: draftNumber(price),
      })),
      min_seconds: cost.min_seconds ? String(cost.min_seconds) : '',
      max_seconds: cost.max_seconds ? String(cost.max_seconds) : '',
      allowed_seconds: (cost.allowed_seconds ?? []).join(', '),
      allowed_seconds_by_resolution: cost.allowed_seconds_by_resolution,
      references: VIDEO_REFERENCE_KINDS.flatMap((kind) =>
        Object.entries(cost.references?.[kind] ?? {}).map(([tier, rule]) => ({
          kind,
          tier,
          mode: rule.mode,
          value: draftNumber(rule.value),
        }))
      ),
    })
  )
  return {
    enabled: true,
    // The backend decodes a missing quality as 0 and accepts it.
    quality: draftNumber(stored.quality ?? 0),
    capacity: stored.capacity ? String(stored.capacity) : '',
    capacity_group: stored.capacity_group ?? '',
    models,
  }
}

function normalizeTier(tier: string): string {
  return tier.trim().toLowerCase()
}

function parseSeconds(value: string): number[] {
  return value
    .split(/[,\s]+/)
    .filter(Boolean)
    .map((item) => Number(item))
}

/** Returns the stored config, or undefined when the channel has none. */
export function buildVideoSchedulingConfig(
  draft: VideoSchedulingDraft | undefined
): VideoSchedulingConfig | undefined {
  if (!draft?.enabled) return undefined
  const models: Record<string, VideoModelCost> = {}
  for (const item of draft.models) {
    const cost: VideoModelCost = {
      mode: item.mode,
      prices: Object.fromEntries(
        item.prices.map((row) => [normalizeTier(row.tier), Number(row.price)])
      ),
    }
    if (Number(item.min_seconds) > 0) {
      cost.min_seconds = Number(item.min_seconds)
    }
    if (Number(item.max_seconds) > 0) {
      cost.max_seconds = Number(item.max_seconds)
    }
    const allowed = parseSeconds(item.allowed_seconds)
    if (allowed.length) cost.allowed_seconds = allowed
    if (item.allowed_seconds_by_resolution !== undefined) {
      cost.allowed_seconds_by_resolution = item.allowed_seconds_by_resolution
    }
    for (const row of item.references) {
      if (!row.mode) continue
      const rules = (cost.references ??= {})
      const kindRules = (rules[row.kind] ??= {})
      kindRules[normalizeTier(row.tier)] =
        VIDEO_REFERENCE_CHARGING_MODES.includes(row.mode)
          ? { mode: row.mode, value: Number(row.value) }
          : { mode: row.mode }
    }
    models[item.model.trim()] = cost
  }
  const config: VideoSchedulingConfig = {
    quality: Number(draft.quality),
    capacity: Number(draft.capacity) || 0,
    models,
  }
  const group = draft.capacity_group.trim()
  if (group) config.capacity_group = group
  return config
}

function isNumber(value: string): boolean {
  return value.trim() !== '' && Number.isFinite(Number(value))
}

function tierError(tier: string): string | null {
  const normalized = normalizeTier(tier)
  if (!normalized) return 'Enter a resolution tier or *'
  if (/^\d+[x*]\d+$/.test(normalized)) {
    return 'Use the short side such as 720p, not a pixel size'
  }
  return null
}

const referenceDraftSchema = z.object({
  kind: z.enum(['video', 'image', 'audio', 'frame']),
  tier: z.string(),
  mode: z.enum([
    '',
    'unsupported',
    'included',
    'per_request',
    'per_input',
    'per_output_second',
    'per_input_second',
    'multiplier',
  ]),
  value: z.string(),
})

export const videoSchedulingDraftSchema = z
  .object({
    enabled: z.boolean(),
    quality: z.string(),
    capacity: z.string(),
    capacity_group: z.string(),
    models: z.array(
      z.object({
        model: z.string(),
        mode: z.enum(['per_video', 'per_second']),
        prices: z.array(z.object({ tier: z.string(), price: z.string() })),
        min_seconds: z.string(),
        max_seconds: z.string(),
        allowed_seconds: z.string(),
        allowed_seconds_by_resolution: z
          .record(
            z.string(),
            z.array(z.number().int().positive().max(MAX_TASK_DURATION_SECONDS))
          )
          .optional(),
        references: z.array(referenceDraftSchema),
      })
    ),
  })
  .superRefine((draft, ctx) => {
    if (!draft.enabled) return
    const issue = (path: (string | number)[], message: string) =>
      ctx.addIssue({ code: 'custom', path, message })
    const quality = Number(draft.quality)
    if (!isNumber(draft.quality) || quality < 0 || quality > 1) {
      issue(['quality'], 'Enter a quality between 0 and 1')
    }
    const capacity = Number(draft.capacity)
    if (
      draft.capacity.trim() &&
      (!Number.isSafeInteger(capacity) || capacity < 0)
    ) {
      issue(['capacity'], 'Enter a non-negative whole number')
    }
    const groupBytes = new TextEncoder().encode(draft.capacity_group.trim())
    if (groupBytes.length > MAX_CAPACITY_GROUP_BYTES) {
      issue(['capacity_group'], 'Capacity group name must be at most 64 bytes')
    }
    const seenModels = new Set<string>()
    draft.models.forEach((item, index) => {
      const model = item.model.trim()
      if (!model || seenModels.has(model)) {
        issue(['models', index, 'model'], 'Each model can be configured once')
      }
      seenModels.add(model)
      if (item.prices.length === 0) {
        issue(['models', index, 'prices'], 'Add at least one base price')
      }
      const seenTiers = new Set<string>()
      item.prices.forEach((row, rowIndex) => {
        const error = tierError(row.tier)
        if (error || seenTiers.has(normalizeTier(row.tier))) {
          issue(
            ['models', index, 'prices', rowIndex, 'tier'],
            error ?? 'Each tier can be priced once'
          )
        }
        seenTiers.add(normalizeTier(row.tier))
        if (!isNumber(row.price) || Number(row.price) < 0) {
          issue(
            ['models', index, 'prices', rowIndex, 'price'],
            'Enter a price of 0 or more'
          )
        }
      })
      for (const field of ['min_seconds', 'max_seconds'] as const) {
        const seconds = Number(item[field])
        if (
          item[field].trim() &&
          (!Number.isInteger(seconds) ||
            seconds < 0 ||
            seconds > MAX_TASK_DURATION_SECONDS)
        ) {
          issue(['models', index, field], 'Enter whole seconds from 0 to 3600')
        }
      }
      if (
        Number(item.max_seconds) > 0 &&
        Number(item.min_seconds) > Number(item.max_seconds)
      ) {
        issue(
          ['models', index, 'min_seconds'],
          'Minimum seconds exceed maximum seconds'
        )
      }
      const allowedSeconds = parseSeconds(item.allowed_seconds)
      if (
        (item.allowed_seconds.trim() && allowedSeconds.length === 0) ||
        allowedSeconds.some(
          (seconds) =>
            !Number.isInteger(seconds) ||
            seconds < 1 ||
            seconds > MAX_TASK_DURATION_SECONDS
        )
      ) {
        issue(
          ['models', index, 'allowed_seconds'],
          'Enter whole seconds from 1 to 3600, separated by commas'
        )
      }
      const seenRules = new Set<string>()
      item.references.forEach((row, rowIndex) => {
        if (!row.mode) return
        const path = ['models', index, 'references', rowIndex]
        if (row.mode === 'per_input_second' && row.kind !== 'video') {
          issue(
            [...path, 'mode'],
            'Input seconds pricing requires video references'
          )
        }
        const key = `${row.kind}/${normalizeTier(row.tier)}`
        const error = tierError(row.tier)
        if (error || seenRules.has(key)) {
          issue([...path, 'tier'], error ?? 'Each tier can be priced once')
        }
        seenRules.add(key)
        if (!VIDEO_REFERENCE_CHARGING_MODES.includes(row.mode)) return
        const value = Number(row.value)
        if (row.mode === 'multiplier') {
          if (!isNumber(row.value) || value < 1) {
            issue([...path, 'value'], 'Enter a multiplier of 1 or more')
          }
        } else if (!isNumber(row.value) || value < 0) {
          issue([...path, 'value'], 'Enter a price of 0 or more')
        }
      })
    })
  })
