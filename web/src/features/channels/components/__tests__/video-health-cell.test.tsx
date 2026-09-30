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
import { afterEach, expect, test } from 'vitest'

import { aggregateChannelsByTag } from '../../lib'
import { channelSchema, type Channel } from '../../types'
import { VideoHealthCell } from '../channels-columns'

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

test('a channel row shows its own video health', () => {
  render(<VideoHealthCell channel={scheduled} />)

  expect(screen.getByText(/in flight 1\/3/)).toBeVisible()
})

test('a tag aggregate row shows no video health even though it copies a child channel', () => {
  const [tagRow] = aggregateChannelsByTag([scheduled])

  const { container } = render(<VideoHealthCell channel={tagRow as Channel} />)

  expect(container).toBeEmptyDOMElement()
})
