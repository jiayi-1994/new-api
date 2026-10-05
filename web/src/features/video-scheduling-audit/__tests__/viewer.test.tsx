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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterContextProvider,
} from '@tanstack/react-router'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { AuditViewer } from '..'
import { AuditExportButton } from '../components/audit-export-button'
import { AuditOverview } from '../components/audit-overview'
import {
  auditSearchSchema,
  type AuditDetail,
  type AuditFilters,
  type AuditList,
  type AuditRun,
  type AuditStats,
  type ChannelOverview,
} from '../types'

const filters = {
  mode: 'on',
  page: 1,
  page_size: 25,
  start: 1780000000000,
  end: 1780086400000,
} satisfies AuditFilters
const run: AuditRun = {
  id: 1,
  request_id: 'request-1',
  started_at: filters.start,
  ended_at: filters.start + 1000,
  mode: 'on',
  model_name: 'video',
  request_group: 'auto',
  actual_group: 'default',
  selected_channel: 7,
  task_pk: null,
  task_id: '',
  platform: '',
  submit_attempts: 0,
  submit_accepted: 0,
  submit_rejected: 0,
  submit_unknown: 0,
  submit_cancelled: 0,
  submit_local: 0,
  request_outcome: 'no_candidate',
  task_status: '',
  terminal_class: '',
  terminal_observed_at: null,
  duration_ms: null,
  cost_usd: null,
  output_seconds: null,
  resolution: '',
  reference_video: null,
  reference_image: null,
  reference_audio: null,
  scheduler_version: '1',
  config_version: 'hash',
  snapshot_complete: false,
  data_issue: 'incomplete_snapshot',
  assembly_error: '',
}
const list: AuditList = {
  items: [run],
  total: 40,
  filter: filters,
  as_of: filters.end,
  audit_enabled: true,
  retention_days: 30,
  data_range: { first: filters.start, last: filters.end },
  collection: {
    node: 'test',
    running: true,
    started_at: filters.start,
    pending: 0,
    pending_bytes: 0,
    oldest_at: 0,
    written: 1,
    dropped: 0,
    write_failures: 0,
    truncated: 0,
    last_issue: '',
    first_issue_at: 0,
    last_issue_at: 0,
    coverage: 'unknown',
  },
}
const rate = { numerator: 0, denominator: 0, value: null }
const stats: AuditStats = {
  as_of: filters.end,
  total: 1,
  pending: 0,
  unknown: 0,
  cancelled: 0,
  missing: 1,
  submit_unknown: 0,
  submit_cancelled: 0,
  submit_local: 0,
  health_ignored: 0,
  health_unknown: 0,
  timeouts: 0,
  maturity_wait_ms: 3600000,
  request_success: rate,
  submit_acceptance: rate,
  generation_success: rate,
  health_success: rate,
  retry: rate,
  no_candidate: rate,
  p50_ms: null,
  p95_ms: null,
  duration_samples: 0,
  oldest_pending_ms: null,
  cost_samples: 0,
  cost_missing: 1,
  mean_cost_usd: null,
  selections: 1,
  candidates: 1,
  shadow_difference: rate,
  shadow_cost_samples: 0,
  mean_shadow_cost_delta: null,
  exclusions: {},
}
const detail: AuditDetail = {
  run,
  attempts: [],
  decisions: [
    {
      selection_seq: 1,
      attempt_seq: 1,
      selected_at: filters.start,
      actual_group: 'default',
      recommended: 7,
      selected: 0,
      choice_kind: 'probe',
      affinity_hit: false,
      admission: 'probe_slot_taken',
      submit_outcome: '',
      error_source: '',
      status_code: 0,
      health_outcome: '',
      candidate_count: 1,
      schema_version: '1',
      scheduler_version: '1',
      build_version: 'test',
      config_version: 'hash',
      fingerprint: 'abc',
      snapshot_complete: false,
      input_json: '',
      board_json: JSON.stringify([
        {
          id: 7,
          name: 'Historical channel',
          p: 0,
          q: 1,
          s: 1,
          total: 0.5,
          cost_usd: 0,
          sell_kind: 'free',
        },
      ]),
      plugins_json: '{}',
    },
  ],
}

test('unsupported reliability filters report the limitation instead of zero success', () => {
  render(
    <AuditOverview
      stats={{
        ...stats,
        reliability: {
          supported: false,
          reason: 'unsupported_health_filter',
          as_of: 1,
          attempts: {
            version: 1,
            source: 'window',
            batch_start: 0,
            batch_end: 1,
            window_seconds: 1,
            as_of: 1,
            validated_at: 0,
            expires_at: 0,
            submitted: 0,
            accepted: 0,
            succeeded: 0,
            rejected: 0,
            generation_failed: 0,
            user: 0,
            cancelled: 0,
            pending: 0,
            unknown: 0,
            missing: 0,
          },
          generation_success: rate,
          channel_completion: rate,
          request_completion: rate,
          limited_share: rate,
          requests: 0,
          request_pending: 0,
          request_unknown: 0,
          request_missing: 0,
          request_user: 0,
          request_cancelled: 0,
          collection_write_failures: 1,
        },
      }}
    />
  )
  expect(
    screen.getByText('Health metrics do not support these filters')
  ).toBeVisible()
  expect(screen.getByRole('status')).toHaveTextContent(
    'Health collection failures: 1'
  )
  expect(screen.queryByText('Channel completion rate')).not.toBeInTheDocument()
})

function Harness() {
  const [current, setCurrent] = useState<AuditFilters>(filters)
  const [client] = useState(
    () => new QueryClient({ defaultOptions: { queries: { retry: false } } })
  )
  const [router] = useState(() =>
    createRouter({
      routeTree: createRootRoute(),
      history: createMemoryHistory({ initialEntries: ['/'] }),
    })
  )
  return (
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <AuditViewer filters={current} onChange={setCurrent} />
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

beforeEach(() =>
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
)
afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

const overview: ChannelOverview = {
  selection_policy: 'weighted_v1',
  price_weight: 0.5,
  quality_weight: 0.3,
  service_weight: 0.2,
  min_samples: 20,
  as_of: filters.end,
  rows: [
    {
      channel_id: 7,
      name: 'Busy channel',
      status: 1,
      group: 'default',
      priority: 5,
      weight: 1,
      model: 'video',
      scheduled: true,
      quality: 0.9,
      cost_mode: 'per_video',
      prices: { '720p': 1 },
      capacity: 4,
      health: {
        submit: { rate: 0.75, samples: 40 },
        gen: { rate: 1, samples: 30 },
        in_flight: 1,
      },
      gated: true,
      unproven: false,
      service: 0.5625,
      usage: {
        requests: 3,
        success: 2,
        failure: 1,
        mean_cost_usd: 1,
        mean_duration_ms: 5000,
      },
    },
    {
      channel_id: 9,
      name: 'Retired channel',
      status: 1,
      group: 'default',
      priority: 0,
      weight: 0,
      model: 'video',
      scheduled: false,
      quality: 0,
      capacity: 0,
      gated: false,
      unproven: false,
      usage: {
        requests: 1,
        success: 1,
        failure: 0,
        mean_cost_usd: null,
        mean_duration_ms: null,
      },
    },
  ],
}

function mockAuditAPI(channels: ChannelOverview = overview) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: AuditStats | AuditDetail | AuditList | ChannelOverview = list
    if (url.endsWith('/audit_stats')) {
      data = stats
    } else if (url.endsWith('/audits/request-1')) {
      data = detail
    } else if (url.endsWith('/channel_overview')) {
      data = channels
    }
    return { data: { success: true, data } }
  })
}

test('applying a model and shadow filter resets server pagination and preserves the time cohort', async () => {
  const get = mockAuditAPI()
  const user = userEvent.setup()
  render(<Harness />)
  await user.click(
    await screen.findByRole('button', { name: 'Go to next page' })
  )
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/channel/video_schedule/audits', {
      params: expect.objectContaining({ page: 2 }),
    })
  )
  const filterToggle = screen.getByRole('button', { name: 'Filters · On' })
  expect(filterToggle).toHaveAttribute('aria-expanded', 'false')
  await user.click(filterToggle)
  expect(filterToggle).toHaveAttribute('aria-expanded', 'true')
  fireEvent.change(screen.getByLabelText('Model'), {
    target: { value: 'video-2' },
  })
  await user.click(screen.getByRole('combobox', { name: 'Scheduling mode' }))
  await user.click(await screen.findByRole('option', { name: 'Shadow' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/channel/video_schedule/audit_stats',
      {
        params: expect.objectContaining({
          mode: 'shadow',
          model: 'video-2',
          page: 1,
          start: filters.start,
          end: filters.end,
        }),
      }
    )
  )
})

test('channel overview shows live service inputs beside per-model call share without a request-independent total', async () => {
  const get = mockAuditAPI()
  render(<Harness />)
  const busy = await screen.findByRole('row', { name: /Busy channel/ })
  expect(within(busy).getByText('0.56')).toBeVisible()
  expect(within(busy).getByText('Below health gate')).toBeVisible()
  expect(within(busy).getByText(/· 75%/)).toBeVisible()
  const retired = screen.getByRole('row', { name: /Retired channel/ })
  expect(within(retired).getByText('Not scheduled')).toBeVisible()
  expect(within(retired).getByText(/· 25%/)).toBeVisible()
  expect(screen.getByText(/Composite score = price × 0.5/)).toBeVisible()
  expect(get).toHaveBeenCalledWith(
    '/api/channel/video_schedule/channel_overview',
    {
      params: expect.objectContaining({
        page: 1,
        start: filters.start,
        end: filters.end,
      }),
    }
  )
})

test('stability-cost overview shows reliability instead of a service score', async () => {
  mockAuditAPI({
    ...overview,
    selection_policy: 'stability_cost_v2',
    rows: [{ ...overview.rows[0], service: undefined }],
  })
  render(<Harness />)
  const busy = await screen.findByRole('row', { name: /Busy channel/ })
  expect(within(busy).getByText('Health state unavailable')).toBeVisible()
  expect(within(busy).queryByText('Below health gate')).not.toBeInTheDocument()
  expect(screen.getByText(/Stability-cost policy/)).toBeVisible()
})

test('keyboard opens historical candidates and incomplete snapshots without treating a missing task as rejection', async () => {
  mockAuditAPI()
  render(<Harness />)
  const trigger = await screen.findByRole('button', { name: 'request-1' })
  trigger.focus()
  await userEvent.keyboard('{Enter}')
  const dialog = await screen.findByRole('dialog', {
    name: 'Scheduling audit details',
  })
  expect(await within(dialog).findByText('No linked task')).toBeVisible()
  expect(
    within(dialog).getByText(
      'Snapshot incomplete. Historical inputs cannot be fully reconstructed.'
    )
  ).toBeVisible()
  expect(within(dialog).getByText('probe_slot_taken')).toBeVisible()
  expect(
    within(dialog).getByRole('cell', { name: /Historical channel/ })
  ).toBeVisible()
  const candidate = within(dialog).getByRole('button', {
    name: /Historical channel/,
  })
  expect(candidate).toHaveAttribute('aria-expanded', 'false')
  await userEvent.click(candidate)
  expect(candidate).toHaveAttribute('aria-expanded', 'true')
  expect(within(dialog).getAllByText('Selling price').length).toBeGreaterThan(1)
  const selection = within(dialog).getByRole('button', { name: /Selection 1/ })
  expect(selection).toHaveAttribute('aria-expanded', 'true')
  await userEvent.click(selection)
  expect(selection).toHaveAttribute('aria-expanded', 'false')
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
})

test('a measured input video duration is displayed separately from the output duration', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: AuditStats | AuditDetail | AuditList | ChannelOverview = list
    if (url.endsWith('/audit_stats')) data = stats
    if (url.endsWith('/channel_overview')) data = overview
    if (url.endsWith('/audits/request-1')) {
      data = {
        ...detail,
        decisions: detail.decisions.map((decision) => ({
          ...decision,
          board_json: JSON.stringify([
            {
              id: 7,
              name: 'Input duration channel',
              p: 1,
              q: 1,
              s: 1,
              total: 1,
              cost_usd: 1.4,
              base_cost_usd: 1,
              reference_cost_usd: 0.4,
              spec: {
                output_seconds: 5,
                input_video_seconds: 20,
                references: { video: 2, image: 0, audio: 0 },
              },
            },
          ]),
        })),
      }
    }
    return { data: { success: true, data } }
  })
  render(<Harness />)
  await userEvent.click(
    await screen.findByRole('button', { name: 'request-1' })
  )
  const dialog = await screen.findByRole('dialog', {
    name: 'Scheduling audit details',
  })
  expect(
    await within(dialog).findByText('Total input video seconds: 20 s')
  ).toBeVisible()
  expect(
    within(dialog).queryByText('Total input video seconds: 5 s')
  ).not.toBeInTheDocument()
})

test('non-root viewers make no audit requests', () => {
  const get = mockAuditAPI()
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'admin', role: ROLE.ADMIN })
  render(<Harness />)
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
})

test('a failed query shows retry and does not render prior request data', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(new Error('offline'))
  render(<Harness />)
  expect(await screen.findByText('Failed to load audit records')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'request-1' })
  ).not.toBeInTheDocument()
  expect(
    screen.getAllByRole('button', { name: 'Retry' }).length
  ).toBeGreaterThan(0)
})

test('empty statistics retain null rates and do not display zero percent', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: AuditStats | AuditList | ChannelOverview = {
      ...list,
      items: [],
      total: 0,
    }
    if (url.endsWith('/audit_stats')) data = stats
    if (url.endsWith('/channel_overview')) data = { ...overview, rows: [] }
    return { data: { success: true, data } }
  })
  render(<Harness />)
  await waitFor(() => expect(screen.getAllByText('No records')).toHaveLength(2))
  expect(screen.queryByText('0%')).not.toBeInTheDocument()
})

test('partial export keeps the continuation and fetches the remaining decisions', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: '{"type":"footer","partial":true,"continuation":"next-selection"}\n',
    })
    .mockResolvedValueOnce({
      data: '{"type":"footer","partial":false,"continuation":""}\n',
    })
  const originalCreate = URL.createObjectURL,
    originalRevoke = URL.revokeObjectURL
  URL.createObjectURL = vi.fn(() => 'blob:audit')
  URL.revokeObjectURL = vi.fn()
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
    () => undefined
  )
  try {
    const client = new QueryClient()
    render(
      <QueryClientProvider client={client}>
        <AuditExportButton filters={filters} />
      </QueryClientProvider>
    )
    await userEvent.click(screen.getByRole('button', { name: 'Export NDJSON' }))
    expect(await screen.findByRole('status')).toHaveTextContent(
      'Partial export'
    )
    await userEvent.click(
      screen.getByRole('button', { name: 'Continue export' })
    )
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith(
        '/api/channel/video_schedule/audit_export',
        {
          params: { ...filters, continuation: 'next-selection' },
          responseType: 'text',
        }
      )
    )
    await waitFor(() =>
      expect(screen.queryByRole('status')).not.toBeInTheDocument()
    )
  } finally {
    URL.createObjectURL = originalCreate
    URL.revokeObjectURL = originalRevoke
  }
})

test('URL filters preserve explicit zero references and free-duration values', () => {
  expect(
    auditSearchSchema.parse({ ...filters, reference_video: 0, seconds: 0 })
  ).toMatchObject({ reference_video: 0, seconds: 0, mode: 'on' })
  expect(() => auditSearchSchema.parse({ ...filters, seconds: -1 })).toThrow()
})
