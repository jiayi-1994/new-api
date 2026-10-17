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
import { describe, expect, test } from 'vitest'

import type { VideoScheduleSimulation } from '../../types'
import {
  buildVideoScheduleMatrixCells,
  parseReferenceSeconds,
  videoExclusionCategory,
  videoScheduleMatrixRequest,
  videoScheduleMatrixVerdict,
} from '../video-schedule-matrix'

const simulation = (
  candidates: VideoScheduleSimulation['candidates'],
  recommended: number
): VideoScheduleSimulation => ({
  candidates,
  recommended,
  decision: { takeover: true, shadow: false },
  group: 'default',
  model: 'video',
  group_ratio: 1,
  probe: false,
  explore: false,
  now: '2026-10-01T00:00:00Z',
  seed: 1,
  fingerprint: '',
  segments: {},
})

const candidate = (
  id: number,
  fields: Partial<VideoScheduleSimulation['candidates'][number]>
) => ({ id, name: `c${id}`, p: 0, q: 0, s: 0, total: 0, ...fields })

describe('matrix cells', () => {
  test('enabled sales expand into every tier, seconds and reference length in display order', () => {
    const cells = buildVideoScheduleMatrixCells(
      {
        'b-video': {
          resolutions: {
            '4k': { usd_per_second: 1, seconds: [5] },
            '720p': { usd_per_second: 0.1, seconds: [10, 5] },
          },
        },
        'a-off': {
          disabled: true,
          resolutions: { '720p': { usd_per_second: 0.1, seconds: [5] } },
        },
      },
      [0, 6]
    )

    expect(
      cells.map((c) => `${c.tier}/${c.seconds}/${c.referenceSeconds}`)
    ).toEqual([
      '720p/5/0',
      '720p/5/6',
      '720p/10/0',
      '720p/10/6',
      '4k/5/0',
      '4k/5/6',
    ])
    expect(new Set(cells.map((c) => c.model))).toEqual(new Set(['b-video']))
  })

  test('reference seconds input accepts separators and rejects out-of-range values', () => {
    expect(parseReferenceSeconds('10, 0，5 5')).toEqual([0, 5, 10])
    expect(parseReferenceSeconds('  ')).toEqual([0])
    expect(parseReferenceSeconds('3601')).toBeNull()
    expect(parseReferenceSeconds('-1')).toBeNull()
    expect(parseReferenceSeconds('abc')).toBeNull()
  })
})

describe('matrix request', () => {
  test('a reference length declares one video URL with that duration', () => {
    const request = videoScheduleMatrixRequest(
      { model: 'video', tier: '720p', seconds: 8, referenceSeconds: 6 },
      'vip',
      ''
    )
    const body = request.request_body as { videos: string[] }

    expect(request).toMatchObject({
      group: 'vip',
      entry: 'protocol',
      protocol: 'openai_video',
      request_body: { model: 'video', resolution: '720p', seconds: 8 },
    })
    expect(body.videos).toHaveLength(1)
    expect(request.reference_video_durations).toEqual({ [body.videos[0]]: 6 })
  })

  test('zero reference seconds sends no reference video', () => {
    const request = videoScheduleMatrixRequest(
      { model: 'video', tier: '720p', seconds: 8, referenceSeconds: 0 },
      'vip',
      ''
    )

    expect(request.request_body).not.toHaveProperty('videos')
    expect(request.reference_video_durations).toBeUndefined()
  })
})

describe('matrix verdict', () => {
  test('exclusions split into configuration, ranking and runtime state', () => {
    expect(videoExclusionCategory('not schedulable: model not priced')).toBe(
      'config'
    )
    expect(videoExclusionCategory('tier 1080p not priced')).toBe('config')
    expect(videoExclusionCategory('cost/sell 1.20 > 1.00')).toBe('config')
    expect(videoExclusionCategory('lower priority')).toBe('ranking')
    expect(videoExclusionCategory('submit rate 0.50 < 0.80')).toBe('runtime')
    expect(videoExclusionCategory('at capacity')).toBe('runtime')
  })

  test('a sale without a recommended channel has no channel', () => {
    const result = simulation([candidate(1, { excluded: 'at capacity' })], 0)

    expect(videoScheduleMatrixVerdict(result)).toBe('no_channel')
  })

  test('a sale with no channel in the group is not served rather than broken', () => {
    expect(videoScheduleMatrixVerdict(simulation([], 0))).toBe('not_served')
  })

  test('a recommended channel costing more than the sell price is a loss', () => {
    const result = simulation(
      [candidate(1, { cost_usd: 1.2, sell_kind: 'known', sell_usd: 1 })],
      1
    )

    expect(videoScheduleMatrixVerdict(result)).toBe('loss')
  })

  test('a profitable sale with a misconfigured candidate is a configuration issue', () => {
    const result = simulation(
      [
        candidate(1, { cost_usd: 0.5, sell_kind: 'known', sell_usd: 1 }),
        candidate(2, { excluded: 'spec does not match unified sale' }),
        candidate(3, { excluded: 'lower priority' }),
      ],
      1
    )

    expect(videoScheduleMatrixVerdict(result)).toBe('config')
    result.candidates = result.candidates.filter((row) => row.id !== 2)
    expect(videoScheduleMatrixVerdict(result)).toBe('ok')
  })
})
