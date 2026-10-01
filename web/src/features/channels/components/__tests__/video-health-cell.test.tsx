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
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test } from 'vitest'

import type { VideoReliability } from '@/features/system-settings/types'

import { aggregateChannelsByTag } from '../../lib'
import { channelSchema, type Channel } from '../../types'
import { VideoHealthCell } from '../channels-columns'
import { VideoReliabilityDetails } from '../video-reliability-details'

const scheduled = channelSchema.parse({
  id: 9,
  name: 'Tagged video channel',
  type: 61,
  key: '',
  status: 1,
  created_time: 1,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
  tag: 'video',
  video_health: {
    submit: { rate: 0.9, samples: 10 },
    gen: { rate: 0.8, samples: 8 },
    in_flight: 1,
    probe: { last_probe_at: 0, consecutive_fails: 0 },
    gated: false,
    capacity: 3,
    probe_slots_held: 0,
  },
})

afterEach(cleanup)

const health: VideoReliability = {
  version: 1,
  model: 'video',
  state: 'unverified',
  state_version: 1,
  state_revision: 1,
  validation_round: 1,
  probe_failures: 0,
  reason: 'new_channel',
  integrity: 'complete',
  blocked_at: 0,
  recovery_started: 0,
  recovery_expires: 0,
  validation_started: 0,
  validation_expires: 0,
  last_validation_at: 0,
  qualification: null,
  current: null,
  recovery: null,
}

test('zero evidence stays unknown and channel details explain limited validation', async () => {
  const user = userEvent.setup()
  const legacy = scheduled.video_health
  if (!legacy) throw new Error('Missing test health')
  render(
    <VideoHealthCell
      channel={{
        ...scheduled,
        video_health: {
          ...legacy,
          selection_policy: 'stability_cost_v2',
          models: { video: { reliability: health } },
          as_of: 1000,
        },
      }}
    />
  )
  await user.click(screen.getByRole('button', { name: /Verified models/ }))
  expect(screen.getByText('Unverified')).toBeVisible()
  expect(
    screen.getByText(/No verified candidates does not mean no service/)
  ).toBeVisible()
  expect(screen.queryByText(/100%/)).not.toBeInTheDocument()
})

test('an expired recovery certificate displays unverified and translates its evidence source', async () => {
  const user = userEvent.setup()
  const qualification = {
    version: 1,
    source: 'recovery' as const,
    batch_start: 1,
    batch_end: 10,
    window_seconds: 10,
    as_of: 20,
    validated_at: 20,
    expires_at: 100,
    submitted: 20,
    accepted: 20,
    succeeded: 20,
    rejected: 0,
    generation_failed: 0,
    user: 0,
    cancelled: 0,
    pending: 0,
    unknown: 0,
    missing: 0,
  }
  render(
    <VideoReliabilityDetails
      health={{
        ...health,
        state: 'normal',
        reason: 'qualified',
        qualification,
      }}
      asOf={101}
    />
  )
  expect(screen.getByText('Unverified')).toBeVisible()
  expect(screen.getByText('Qualification expired')).toBeVisible()
  const trigger = screen.getByRole('button', { name: 'Evidence details' })
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  await user.click(trigger)
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByText('Qualification expires')).toBeVisible()
  expect(screen.getByText('Recovery verification')).toBeVisible()
})

test('a channel row shows its own video health', () => {
  render(<VideoHealthCell channel={scheduled} />)

  expect(screen.getByText(/in flight 1\/3/)).toBeVisible()
})

test('a tag aggregate row shows no video health even though it copies a child channel', () => {
  const [tagRow] = aggregateChannelsByTag([scheduled])

  const { container } = render(<VideoHealthCell channel={tagRow as Channel} />)

  expect(container).toBeEmptyDOMElement()
})
