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
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import type { VideoReliability } from '@/features/system-settings/types'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getChannels } from '../../api'
import { aggregateChannelsByTag } from '../../lib'
import { channelsQueryKeys } from '../../lib/channel-actions'
import { channelSchema, type Channel } from '../../types'
import { VideoHealthCell } from '../channels-columns'
import { VideoReliabilityDetails } from '../video-reliability-details'
import { VideoUnknownReviewSession } from '../video-unknown-review'

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

const originalAuth = useAuthStore.getState().auth

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.setState({ auth: originalAuth })
})

test('review requires evidence, preserves the note on failure, and refreshes after success', async () => {
  const user = userEvent.setup()
  const attempt = {
    id: 31,
    request_id: 'unknown-request',
    model: 'video',
    started_at: 1,
    task_pk: null,
    reviewed_at: 0,
    reviewed_by: 0,
    review_note: '',
  }
  vi.spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: { success: true, data: [attempt] },
    })
    .mockResolvedValue({
      data: {
        success: true,
        data: [
          {
            ...attempt,
            reviewed_at: 123,
            reviewed_by: 1,
            review_note: 'Provider records checked',
          },
        ],
      },
    })
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Task is still active' },
    })
    .mockResolvedValueOnce({
      data: { success: true, data: { ...attempt, reviewed_at: 123 } },
    })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <VideoUnknownReviewSession channelId={9} onClose={() => {}} />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('button', { name: 'Review unknown submission' })
  )
  const save = screen.getByRole('button', { name: 'Record review' })
  expect(save).toBeDisabled()
  const note = screen.getByRole('textbox', { name: 'Review evidence' })
  await user.type(note, 'Provider records checked')
  await user.click(save)
  expect(await screen.findByText('Task is still active')).toBeVisible()
  expect(note).toHaveValue('Provider records checked')
  await user.click(save)
  await waitFor(() =>
    expect(
      screen.queryByRole('textbox', { name: 'Review evidence' })
    ).not.toBeInTheDocument()
  )
  expect(post).toHaveBeenLastCalledWith(
    '/api/channel/video_schedule/health_attempts/31/review',
    { note: 'Provider records checked' }
  )
  expect(await screen.findByText(/Reviewed by 1/)).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Review unknown submission' })
  ).not.toBeInTheDocument()
})

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

const scheduledHealth = scheduled.video_health
if (!scheduledHealth) throw new Error('Missing test health')

const blockedChannel = {
  ...scheduled,
  video_health: {
    ...scheduledHealth,
    selection_policy: 'stability_cost_v2',
    models: {
      video: { reliability: { ...health, state: 'blocked', state_version: 7 } },
    },
    as_of: 1000,
  },
} satisfies Channel

function RecoveryChannelQuery() {
  const query = useQuery({
    queryKey: channelsQueryKeys.list({}),
    queryFn: () => getChannels(),
    initialData: {
      success: true,
      data: { items: [blockedChannel], total: 1, page: 1, page_size: 10 },
    },
    staleTime: Infinity,
  })
  const channel = query.data.data?.items[0]
  return channel ? <VideoHealthCell channel={channel} /> : null
}

test.each([
  {
    name: 'operator',
    role: ROLE.ADMIN,
    operate: true,
    status: 1,
    state: 'blocked',
    integrity: 'complete',
    visible: true,
  },
  {
    name: 'root',
    role: ROLE.SUPER_ADMIN,
    operate: false,
    status: 1,
    state: 'blocked',
    integrity: 'complete',
    visible: true,
  },
  {
    name: 'read-only administrator',
    role: ROLE.ADMIN,
    operate: false,
    status: 1,
    state: 'blocked',
    integrity: 'complete',
    visible: false,
  },
  {
    name: 'ordinary user',
    role: ROLE.USER,
    operate: true,
    status: 1,
    state: 'blocked',
    integrity: 'complete',
    visible: false,
  },
  {
    name: 'disabled channel',
    role: ROLE.ADMIN,
    operate: true,
    status: 2,
    state: 'blocked',
    integrity: 'complete',
    visible: false,
  },
  {
    name: 'incomplete health',
    role: ROLE.ADMIN,
    operate: true,
    status: 1,
    state: 'blocked',
    integrity: 'uncertain',
    visible: false,
  },
  {
    name: 'recovering model',
    role: ROLE.ADMIN,
    operate: true,
    status: 1,
    state: 'recovering',
    integrity: 'complete',
    visible: false,
  },
  {
    name: 'unverified model',
    role: ROLE.ADMIN,
    operate: true,
    status: 1,
    state: 'unverified',
    integrity: 'complete',
    visible: false,
  },
  {
    name: 'recovery already requested',
    role: ROLE.ADMIN,
    operate: true,
    status: 1,
    state: 'blocked',
    integrity: 'complete',
    reason: 'manual_recovery_requested',
    visible: false,
  },
] as const)(
  'scheduling recovery visibility matches $name eligibility',
  async (scenario) => {
    useAuthStore.getState().auth.setUser({
      id: 2,
      username: 'operator',
      role: scenario.role,
      permissions: {
        admin_permissions: { channel: { operate: scenario.operate } },
      },
    })
    const user = userEvent.setup()
    const client = new QueryClient()
    render(
      <QueryClientProvider client={client}>
        <VideoHealthCell
          channel={{
            ...blockedChannel,
            status: scenario.status,
            video_health: {
              ...blockedChannel.video_health,
              models: {
                video: {
                  reliability: {
                    ...health,
                    state: scenario.state,
                    integrity: scenario.integrity,
                    reason: scenario.reason ?? health.reason,
                  },
                },
              },
            },
          }}
        />
      </QueryClientProvider>
    )
    await user.click(screen.getByRole('button', { name: /Verified models/ }))
    const recover = screen.queryByRole('button', {
      name: 'Restore scheduling eligibility',
    })
    if (scenario.visible) expect(recover).toBeVisible()
    else expect(recover).not.toBeInTheDocument()
    client.clear()
  }
)

test('recovery requires a trimmed reason of at most 500 Unicode characters and Escape cancels without a request', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'root', role: ROLE.SUPER_ADMIN })
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post')
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <VideoHealthCell channel={blockedChannel} />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: /Verified models/ }))
  await user.click(
    screen.getByRole('button', { name: 'Restore scheduling eligibility' })
  )
  const dialog = screen.getByRole('alertdialog')
  const confirm = within(dialog).getByRole('button', {
    name: 'Restore scheduling eligibility',
  })
  const note = within(dialog).getByRole('textbox', { name: 'Recovery reason' })
  expect(confirm).toBeDisabled()
  fireEvent.change(note, { target: { value: '   ' } })
  await waitFor(() => expect(note).toHaveAttribute('aria-invalid', 'true'))
  expect(confirm).toBeDisabled()
  fireEvent.change(note, { target: { value: '𠮷'.repeat(500) } })
  await waitFor(() => expect(confirm).toBeEnabled())
  fireEvent.change(note, { target: { value: '𠮷'.repeat(501) } })
  await waitFor(() => expect(confirm).toBeDisabled())
  expect(screen.getByText('Use 500 characters or fewer.')).toBeVisible()
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(
    screen.getByRole('dialog', { name: 'Video reliability' })
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  client.clear()
})

test('recovery keeps server errors and evidence, prevents duplicate submission, and refreshes channel health after success', async () => {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'operator',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { channel: { operate: true } } },
  })
  const user = userEvent.setup()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  let finishPost: (value: { data: { success: boolean } }) => void = () => {}
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Review unknown submissions first' },
    })
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finishPost = resolve
        })
    )
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: { items: [blockedChannel], total: 1, page: 1, page_size: 10 },
      },
    })
    .mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            {
              ...blockedChannel,
              video_health: {
                ...blockedChannel.video_health,
                models: {
                  video: {
                    reliability: {
                      ...health,
                      state: 'blocked',
                      reason: 'manual_recovery_requested',
                      state_version: 8,
                    },
                  },
                },
              },
            },
          ],
          total: 1,
          page: 1,
          page_size: 10,
        },
      },
    })
  render(
    <QueryClientProvider client={client}>
      <RecoveryChannelQuery />
    </QueryClientProvider>
  )
  await user.click(screen.getByRole('button', { name: /Verified models/ }))
  await user.click(
    screen.getByRole('button', { name: 'Restore scheduling eligibility' })
  )
  const dialog = screen.getByRole('alertdialog')
  const note = within(dialog).getByRole('textbox', { name: 'Recovery reason' })
  const confirm = within(dialog).getByRole('button', {
    name: 'Restore scheduling eligibility',
  })
  await user.type(note, '  Provider checked  ')
  await user.click(confirm)
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Review unknown submissions first'
  )
  expect(note).toHaveValue('  Provider checked  ')
  await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
  expect(post).toHaveBeenLastCalledWith('/api/channel/9/video_health/recover', {
    model: 'video',
    state_version: 7,
    note: 'Provider checked',
  })
  await user.click(confirm)
  await waitFor(() => expect(confirm).toBeDisabled())
  expect(note).toBeDisabled()
  await user.keyboard('{Escape}')
  expect(dialog).toBeVisible()
  await user.click(confirm)
  expect(post).toHaveBeenCalledTimes(2)
  await act(async () => {
    finishPost({ data: { success: true } })
  })
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(get).toHaveBeenCalledWith('/api/channel', { params: {} })
  expect(await screen.findByText('Scheduling recovery requested')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Restore scheduling eligibility' })
  ).not.toBeInTheDocument()
  client.clear()
})

test('recovery targets the selected model and rejects a changed version without replacing its confirmation snapshot', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'root', role: ROLE.SUPER_ADMIN })
  const user = userEvent.setup()
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  const channel = {
    ...blockedChannel,
    video_health: {
      ...blockedChannel.video_health,
      models: {
        ...blockedChannel.video_health.models,
        'video-pro': {
          reliability: {
            ...health,
            model: 'video-pro',
            state: 'blocked' as const,
            state_version: 11,
          },
        },
      },
    },
  }
  const view = render(
    <QueryClientProvider client={client}>
      <VideoHealthCell channel={channel} />
    </QueryClientProvider>
  )
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: false,
      message: 'Health state changed; refresh before trying again',
    },
  })
  await user.click(screen.getByRole('button', { name: /Verified models/ }))
  await user.click(
    screen.getAllByRole('button', { name: 'Restore scheduling eligibility' })[1]
  )
  expect(
    within(screen.getByRole('alertdialog')).getByText(
      'Channel Tagged video channel model video-pro'
    )
  ).toBeVisible()
  view.rerender(
    <QueryClientProvider client={client}>
      <VideoHealthCell
        channel={{
          ...channel,
          video_health: {
            ...channel.video_health,
            models: {
              ...channel.video_health.models,
              'video-pro': {
                reliability: {
                  ...channel.video_health.models['video-pro'].reliability,
                  state_version: 12,
                },
              },
            },
          },
        }}
      />
    </QueryClientProvider>
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Recovery reason' }),
    'Provider recovered'
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Restore scheduling eligibility',
    })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Health state changed; refresh before trying again'
  )
  expect(post).toHaveBeenCalledWith('/api/channel/9/video_health/recover', {
    model: 'video-pro',
    state_version: 11,
    note: 'Provider recovered',
  })
  expect(screen.getByRole('textbox', { name: 'Recovery reason' })).toHaveValue(
    'Provider recovered'
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Cancel',
    })
  )
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  await user.click(
    screen.getAllByRole('button', { name: 'Restore scheduling eligibility' })[1]
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Recovery reason' }),
    'Provider recovered'
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Restore scheduling eligibility',
    })
  )
  await waitFor(() =>
    expect(post).toHaveBeenLastCalledWith(
      '/api/channel/9/video_health/recover',
      {
        model: 'video-pro',
        state_version: 12,
        note: 'Provider recovered',
      }
    )
  )
  client.clear()
})

test.each([
  {
    name: 'unknown evidence',
    integrity: 'uncertain',
    reason: 'submit outcome unknown',
  },
  {
    name: 'another recovery request',
    integrity: 'complete',
    reason: 'manual_recovery_requested',
  },
] as const)(
  'eligibility changes during error refresh preserve the open recovery reason: $name',
  async (changed) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 2, username: 'root', role: ROLE.SUPER_ADMIN })
    const user = userEvent.setup()
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          items: [
            {
              ...blockedChannel,
              video_health: {
                ...blockedChannel.video_health,
                models: {
                  video: {
                    reliability: {
                      ...blockedChannel.video_health.models.video.reliability,
                      integrity: changed.integrity,
                      reason: changed.reason,
                      state_version: 8,
                    },
                  },
                },
              },
            },
          ],
          total: 1,
          page: 1,
          page_size: 10,
        },
      },
    })
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: false,
        message: 'Health state changed before admission',
      },
    })
    render(
      <QueryClientProvider client={client}>
        <RecoveryChannelQuery />
      </QueryClientProvider>
    )
    await user.click(screen.getByRole('button', { name: /Verified models/ }))
    await user.click(
      screen.getByRole('button', { name: 'Restore scheduling eligibility' })
    )
    await user.type(
      screen.getByRole('textbox', { name: 'Recovery reason' }),
      'Checked upstream recovery'
    )
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Restore scheduling eligibility',
      })
    )
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Health state changed before admission'
    )
    expect(
      screen.getByRole('textbox', { name: 'Recovery reason' })
    ).toHaveValue('Checked upstream recovery')
    await waitFor(() =>
      expect(
        within(screen.getByRole('alertdialog')).getByRole('button', {
          name: 'Restore scheduling eligibility',
        })
      ).toBeDisabled()
    )
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Cancel',
      })
    )
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(
      screen.queryByRole('button', { name: 'Restore scheduling eligibility' })
    ).not.toBeInTheDocument()
    client.clear()
  }
)

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

test('Escape cancels the review confirmation while keeping the submission list open', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          id: 31,
          request_id: 'unknown-request',
          model: 'video',
          started_at: 1,
          task_pk: null,
          reviewed_at: 0,
        },
      ],
    },
  })
  const close = vi.fn()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <VideoUnknownReviewSession channelId={9} onClose={close} />
    </QueryClientProvider>
  )
  await user.click(
    await screen.findByRole('button', { name: 'Review unknown submission' })
  )
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(close).not.toHaveBeenCalled()
  expect(
    screen.getByRole('dialog', { name: 'Review unknown submissions' })
  ).toBeVisible()
})

test('a tag aggregate row shows no video health even though it copies a child channel', () => {
  const [tagRow] = aggregateChannelsByTag([scheduled])

  const { container } = render(<VideoHealthCell channel={tagRow as Channel} />)

  expect(container).toBeEmptyDOMElement()
})
