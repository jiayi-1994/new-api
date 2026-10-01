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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type {
  VideoScheduleSimulation,
  VideoSchedulingSetting,
} from '../../types'
import { VideoScheduleSimulatorDialog } from '../video-schedule-simulator-dialog'

const simulation: VideoScheduleSimulation = {
  candidates: [
    {
      id: 3,
      name: 'priced',
      plugin: 'megabyai',
      mapped_model: 'videos-mini',
      spec: {
        output_seconds: 5,
        seconds_kind: 'exact',
        tier: '720p',
        references: { video: 1, image: 0, audio: 0 },
      },
      tier: '720p',
      cost_usd: 0.35,
      base_cost_usd: 0.25,
      reference_cost_usd: 0.1,
      references: [
        {
          kind: 'video',
          mode: 'per_input',
          tier: '*',
          quantity: 1,
          value: 0.1,
          usd: 0.1,
        },
      ],
      sell_kind: 'free',
      p: 0.5,
      q: 0.7,
      s: 1,
      total: 0.66,
    },
    {
      id: 4,
      name: 'unpriced',
      plugin: 'seedance-hjmie',
      mapped_model: 'videos-mini',
      spec: {
        output_seconds: 5,
        seconds_kind: 'exact',
        tier: '720p',
        references: { video: 1, image: 0, audio: 0 },
      },
      sell_kind: 'unknown',
      p: 0,
      q: 0,
      s: 0,
      total: 0,
      excluded: 'reference video not priced',
    },
  ],
  decision: { takeover: false, shadow: false, reason: 'mode_off' },
  group: 'default',
  model: 'videos-mini',
  group_ratio: 1,
  recommended: 3,
  probe: false,
  explore: false,
  now: '2026-10-01T00:00:00Z',
  seed: 42,
  fingerprint: 'f'.repeat(64),
  segments: { candidates: 'a'.repeat(64), seed: 'b'.repeat(64) },
}

async function runSimulation(response: unknown) {
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: response })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <VideoScheduleSimulatorDialog
        getConfigSnapshot={() => ({}) as VideoSchedulingSetting}
      />
    </QueryClientProvider>
  )
  await user.click(
    screen.getByRole('button', { name: 'Open scheduling simulator' })
  )
  await user.click(screen.getByRole('button', { name: 'Simulate' }))
  return post
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

test('the default protocol entry sends the protocol without a path that would override it', async () => {
  const post = await runSimulation({ success: true, data: simulation })

  const body = post.mock.calls[0][1] as Record<string, unknown>
  expect(body.protocol).toBe('openai_video')
  expect(body.path).toBeUndefined()
})

test('an invalid quote shows no cost instead of 0 and keeps its exclusion reason', async () => {
  await runSimulation({ success: true, data: simulation })

  const row = (await screen.findByText(/#4 unpriced/)).closest(
    'tr'
  ) as HTMLElement
  expect(within(row).getByText('reference video not priced')).toBeVisible()
  expect(within(row).getAllByText('—').length).toBeGreaterThanOrEqual(3)
  expect(within(row).queryByText(/\$0/)).not.toBeInTheDocument()
  const priced = screen.getByText(/#3 priced/).closest('tr') as HTMLElement
  expect(within(priced).getByText('$0.35')).toBeVisible()
  expect(within(priced).getByText('Free')).toBeVisible()
})

test('the result shows the fingerprint and segment hashes with the fixed note and no reproducibility claim', async () => {
  await runSimulation({ success: true, data: simulation })

  expect(await screen.findByText('f'.repeat(64))).toBeVisible()
  expect(screen.getByText('a'.repeat(64))).toBeVisible()
  expect(
    screen.getByText(
      'Same complete input yields the same recommendation; candidates and pricing use current values'
    )
  ).toBeVisible()
  expect(screen.queryByText(/reproducible/i)).not.toBeInTheDocument()
})

test('a rejected simulation shows the server message', async () => {
  await runSimulation({
    success: false,
    message: 'config_snapshot window_seconds must equal the live value 1800',
  })

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'config_snapshot window_seconds must equal the live value 1800'
  )
})
