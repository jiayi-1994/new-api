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
  VideoScheduleSimulateRequest,
  VideoSchedulingSetting,
} from '../../types'
import { VideoScheduleMatrixDialog } from '../video-schedule-matrix-dialog'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const sales = {
  'unified-video': {
    resolutions: {
      '720p': {
        usd_per_second: 0.1,
        input_video_usd_per_second: 0.2,
        seconds: [5],
      },
    },
  },
}

function mockServer() {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default', 'auto'] } }
    }
    return {
      data: {
        success: true,
        data: [
          { key: 'billing_setting.video_sales', value: JSON.stringify(sales) },
        ],
      },
    }
  })
  return vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
    const request = body as VideoScheduleSimulateRequest
    const withReference = request.reference_video_durations !== undefined
    // The reference video makes the only channel cost more than the sale.
    return {
      data: {
        success: true,
        data: {
          candidates: [
            {
              id: 7,
              name: 'cheap',
              cost_usd: withReference ? 2 : 0.2,
              sell_kind: 'known',
              sell_usd: withReference ? 1.5 : 0.5,
              p: 0,
              q: 0,
              s: 0,
              total: 0,
            },
            {
              id: 8,
              name: 'unpriced',
              excluded: 'not schedulable: model not priced',
              p: 0,
              q: 0,
              s: 0,
              total: 0,
            },
          ],
          recommended: 7,
          decision: { takeover: true, shadow: false },
          group: request.group,
          model: 'unified-video',
          group_ratio: 1,
          probe: false,
          explore: false,
          now: '2026-10-01T00:00:00Z',
          seed: 1,
          fingerprint: '',
          segments: {},
        },
      },
    }
  })
}

test('running the matrix simulates each reference length and flags the loss-making sale', async () => {
  const post = mockServer()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <VideoScheduleMatrixDialog
        getConfigSnapshot={() => ({}) as VideoSchedulingSetting}
      />
    </QueryClientProvider>
  )

  await user.click(
    screen.getByRole('button', { name: 'Group scheduling matrix' })
  )
  const reference = screen.getByLabelText('Reference video seconds')
  await user.clear(reference)
  await user.type(reference, '0, 6')
  await user.click(await screen.findByRole('button', { name: 'Run matrix' }))

  expect(await screen.findByText('Loss')).toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(2)
  const requests = post.mock.calls.map(
    (call) => call[1] as VideoScheduleSimulateRequest
  )
  expect(requests.map((request) => request.group)).toEqual([
    'default',
    'default',
  ])
  expect(requests[1].reference_video_durations).toEqual({
    'https://simulate.invalid/reference-video.mp4': 6,
  })
  expect(screen.getByText('Configuration issue')).toBeInTheDocument()

  await user.click(screen.getByRole('checkbox', { name: 'Only show problems' }))
  const rows = screen.getAllByRole('row')
  expect(rows).toHaveLength(3) // header + both problem rows
  expect(within(rows[2]).getByText('Loss')).toBeInTheDocument()
})

test('an invalid reference length is refused before any simulation runs', async () => {
  const post = mockServer()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient()}>
      <VideoScheduleMatrixDialog
        getConfigSnapshot={() => ({}) as VideoSchedulingSetting}
      />
    </QueryClientProvider>
  )

  await user.click(
    screen.getByRole('button', { name: 'Group scheduling matrix' })
  )
  await user.type(screen.getByLabelText('Reference video seconds'), ', 9999')
  await user.click(await screen.findByRole('button', { name: 'Run matrix' }))

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Reference video seconds must be numbers between 0 and 3600'
  )
  expect(post).not.toHaveBeenCalled()
})
