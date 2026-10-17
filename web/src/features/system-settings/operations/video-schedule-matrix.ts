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
import type { VideoSalesModel } from '@/features/pricing/types'

import { MAX_VIDEO_SALES_SECONDS } from '../models/video-sales-config'
import type {
  VideoScheduleCandidate,
  VideoScheduleSimulateRequest,
  VideoScheduleSimulation,
  VideoSchedulingSetting,
} from '../types'

/** One unified sale the matrix simulates: model × resolution × output seconds × reference video seconds. */
export type VideoScheduleMatrixCell = {
  model: string
  tier: string
  seconds: number
  /** Total reference video length; 0 sends no reference video. */
  referenceSeconds: number
}

/** One seed for every cell, so probe and exploration draws never differ between adjacent rows. */
export const VIDEO_SCHEDULE_MATRIX_SEED = 1

function tierHeight(tier: string) {
  return tier === '4k' ? 2160 : Number.parseInt(tier, 10)
}

/** Parses "0, 5, 10" into sorted unique reference video lengths; null when any entry is invalid. */
export function parseReferenceSeconds(value: string): number[] | null {
  const parts = value
    .split(/[,，\s]+/)
    .map((part) => part.trim())
    .filter(Boolean)
  if (parts.length === 0) return [0]
  const values = new Set<number>()
  for (const part of parts) {
    const seconds = Number(part)
    if (
      !Number.isFinite(seconds) ||
      seconds < 0 ||
      seconds > MAX_VIDEO_SALES_SECONDS
    ) {
      return null
    }
    values.add(seconds)
  }
  return [...values].sort((a, b) => a - b)
}

/** Every sale the unified price table offers; disabled models are refused at the entry and skipped. */
export function buildVideoScheduleMatrixCells(
  sales: Record<string, VideoSalesModel>,
  referenceSeconds: number[]
): VideoScheduleMatrixCell[] {
  const cells: VideoScheduleMatrixCell[] = []
  const models = Object.keys(sales).sort((a, b) => a.localeCompare(b))
  for (const model of models) {
    if (sales[model].disabled) continue
    const tiers = Object.keys(sales[model].resolutions).sort(
      (a, b) => tierHeight(a) - tierHeight(b)
    )
    for (const tier of tiers) {
      const seconds = [...sales[model].resolutions[tier].seconds].sort(
        (a, b) => a - b
      )
      for (const second of seconds) {
        for (const reference of referenceSeconds) {
          cells.push({
            model,
            tier,
            seconds: second,
            referenceSeconds: reference,
          })
        }
      }
    }
  }
  return cells
}

/**
 * A reference video is declared to the simulator by URL and length instead of
 * being fetched. Every schedulable plugin accepts it under `videos`.
 */
export function videoScheduleMatrixRequest(
  cell: VideoScheduleMatrixCell,
  group: string,
  userGroup: string,
  configSnapshot?: VideoSchedulingSetting
): VideoScheduleSimulateRequest {
  const body: Record<string, unknown> = {
    model: cell.model,
    prompt: 'scheduling matrix simulation',
    resolution: cell.tier,
    seconds: cell.seconds,
  }
  let durations: Record<string, number> | undefined
  if (cell.referenceSeconds > 0) {
    const url = 'https://simulate.invalid/reference-video.mp4'
    body.videos = [url]
    durations = { [url]: cell.referenceSeconds }
  }
  return {
    group,
    user_group: userGroup || undefined,
    entry: 'protocol',
    protocol: 'openai_video',
    request_body: body,
    reference_video_durations: durations,
    config_snapshot: configSnapshot,
    seed: VIDEO_SCHEDULE_MATRIX_SEED,
  }
}

export type VideoExclusionCategory = 'config' | 'ranking' | 'runtime'

const RANKING_REASONS = new Set([
  'lower priority',
  'lower quality tier',
  'higher cost at same quality',
  'stability gap too large',
  'validation order',
])

const CONFIG_REASON_PREFIXES = [
  'disabled',
  'not schedulable',
  'spec invalid',
  'spec does not match unified sale',
  'input video URLs unavailable',
  'unified input videos conflict',
  'tier ',
  'no price configured',
  'invalid ',
  'seconds unknown',
  'reference ',
  'input seconds requires video references',
  'input video seconds unknown',
  'cost exceeds bound',
  'cost/sell',
  'margin below minimum',
  'sell unknown',
  'positive sell price required',
]

/**
 * Splits exclusions into what an administrator fixes in configuration, what
 * only loses the ranking to a better channel, and live health or capacity
 * state that changes on its own. Unknown reasons count as runtime state.
 */
export function videoExclusionCategory(reason: string): VideoExclusionCategory {
  if (RANKING_REASONS.has(reason)) return 'ranking'
  if (CONFIG_REASON_PREFIXES.some((prefix) => reason.startsWith(prefix))) {
    return 'config'
  }
  return 'runtime'
}

/** Sell minus cost in USD; undefined when either side is unpriced. */
export function videoCandidateProfit(
  candidate: VideoScheduleCandidate
): number | undefined {
  if (candidate.cost_usd === undefined) return undefined
  if (candidate.sell_kind === 'free') return -candidate.cost_usd
  if (candidate.sell_kind !== 'known') return undefined
  return (candidate.sell_usd ?? 0) - candidate.cost_usd
}

export type VideoScheduleMatrixVerdict =
  | 'ok'
  | 'config'
  | 'loss'
  | 'no_channel'
  | 'not_served'

/** The most severe problem of one simulated sale. */
export function videoScheduleMatrixVerdict(
  result: VideoScheduleSimulation
): VideoScheduleMatrixVerdict {
  const recommended = result.candidates.find(
    (candidate) => candidate.id === result.recommended
  )
  // A group may deliberately not sell a model; that is not a broken route.
  if (result.candidates.length === 0) return 'not_served'
  if (!recommended) return 'no_channel'
  const profit = videoCandidateProfit(recommended)
  if (profit !== undefined && profit < 0) return 'loss'
  const misconfigured = result.candidates.some(
    (candidate) =>
      candidate.excluded &&
      videoExclusionCategory(candidate.excluded) === 'config'
  )
  return misconfigured ? 'config' : 'ok'
}
