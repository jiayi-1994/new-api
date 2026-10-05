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

import { channelSchema, type VideoSchedulingConfig } from '../../types'
import { ChannelsProvider } from '../channels-provider'
import { ChannelMutateDrawer } from '../drawers/channel-mutate-drawer'

const originalAuth = useAuthStore.getState().auth
let client: QueryClient
let describeSpec: boolean

function editingChannel(config: VideoSchedulingConfig | null, type = 61) {
  return channelSchema.parse({
    id: 42,
    name: 'Video channel',
    type,
    key: '',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'videos-mini',
    group: 'default',
    base_url: 'https://video.example',
    setting: '{"task_plugin_key":"video-a"}',
    settings: JSON.stringify(config ? { video_scheduling: config } : {}),
  })
}

const pricedConfig = (
  reference: VideoSchedulingConfig['models'][string]['references']
): VideoSchedulingConfig => ({
  quality: 0.7,
  capacity: 0,
  capacity_group: 'ghost-account',
  models: {
    'videos-mini': {
      mode: 'per_video',
      prices: { '*': 0.4 },
      references: reference,
    },
  },
})

function Harness(props: { channel: ReturnType<typeof editingChannel> }) {
  const [open, setOpen] = useState(true)
  const [router] = useState(() =>
    createRouter({
      routeTree: createRootRoute(),
      history: createMemoryHistory({ initialEntries: ['/'] }),
    })
  )
  return (
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <ChannelsProvider>
          <ChannelMutateDrawer
            open={open}
            onOpenChange={setOpen}
            currentRow={props.channel}
          />
        </ChannelsProvider>
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

function mockApi(channel: ReturnType<typeof editingChannel>) {
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    const responses: Record<string, unknown> = {
      '/api/channel/42': channel,
      '/api/task_plugin_options': [
        { key: 'video-a', name: 'Video A', models: ['videos-mini'] },
      ],
      '/api/channel/models': [],
      '/api/channel/default_base_urls': {},
      '/api/group/': ['default'],
      '/api/prefill_group': [],
      '/api/option/': [
        {
          key: 'video_scheduling_setting.capacity_groups',
          value: '{"account-a":5}',
        },
      ],
      '/api/channel/video_schedule/schedulable': {
        plugin: config?.params?.plugin,
        describe_spec: describeSpec,
        models: [{ model: 'videos-mini', static_blockers: [] }],
      },
    }
    if (!(url in responses)) throw new Error(`Unexpected GET ${url}`)
    return { data: { success: true, data: responses[url] } }
  })
}

async function openRouting(channel: ReturnType<typeof editingChannel>) {
  mockApi(channel)
  const user = userEvent.setup()
  render(<Harness channel={channel} />)
  await screen.findByDisplayValue('Video channel')
  await user.click(screen.getByRole('tab', { name: /Routing & Mapping/ }))
  return user
}

beforeEach(() => {
  describeSpec = true
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore.setState({
    auth: {
      ...originalAuth,
      user: { id: 1, username: 'root', role: ROLE.SUPER_ADMIN },
    },
  })
})

afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.setState({ auth: originalAuth })
  vi.restoreAllMocks()
})

test('a plugin without describeSpec shows why the channel is not scheduled instead of the editor', async () => {
  describeSpec = false
  await openRouting(editingChannel(null))

  const section = screen.getByRole('group', { name: 'Video scheduling' })
  expect(
    await within(section).findByText(
      /Not schedulable: plugin does not provide describeSpec/
    )
  ).toBeVisible()
  expect(
    within(section).queryByRole('switch', { name: 'Schedule this channel' })
  ).not.toBeInTheDocument()
})

test('a capacity group missing from the global setting is flagged as no group', async () => {
  await openRouting(editingChannel(pricedConfig(undefined)))

  expect(
    await screen.findByText(/Unregistered group: treated as no group/)
  ).toBeVisible()
})

test('switching a reference fee to included hides its value and saves no value', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const user = await openRouting(
    editingChannel(
      pricedConfig({ video: { '*': { mode: 'per_input', value: 0.2 } } })
    )
  )
  const videos = await screen.findByRole('group', { name: 'Reference videos' })
  expect(within(videos).getByLabelText('Fee value')).toHaveValue(0.2)
  expect(within(videos).getByText('USD/input')).toBeVisible()

  await user.click(within(videos).getByRole('combobox', { name: 'Fee mode' }))
  await user.click(
    await screen.findByRole('option', { name: 'Included in base price' })
  )

  expect(within(videos).queryByLabelText('Fee value')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Update Channel' }))
  await waitFor(() => expect(put).toHaveBeenCalled())
  const payload = put.mock.calls[0]?.[1] as { settings: string }
  expect(
    JSON.parse(payload.settings).video_scheduling.models['videos-mini']
      .references
  ).toEqual({ video: { '*': { mode: 'included' } } })
})

test('a video fee can charge input seconds independently of output seconds', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const user = await openRouting(
    editingChannel(
      pricedConfig({
        video: { '*': { mode: 'per_output_second', value: 0.2 } },
      })
    )
  )
  const videos = await screen.findByRole('group', { name: 'Reference videos' })
  await user.click(within(videos).getByRole('combobox', { name: 'Fee mode' }))
  await user.click(
    await screen.findByRole('option', { name: 'Per input video second' })
  )
  expect(within(videos).getByText('USD/input video second')).toBeVisible()
  expect(
    within(videos).queryByText('USD/output second')
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Update Channel' }))
  await waitFor(() => expect(put).toHaveBeenCalled())
  const payload = put.mock.calls[0]?.[1] as { settings: string }
  expect(
    JSON.parse(payload.settings).video_scheduling.models['videos-mini']
      .references
  ).toEqual({ video: { '*': { mode: 'per_input_second', value: 0.2 } } })
})

test('a missing fee value blocks saving and brings the routing tab into focus', async () => {
  const put = vi.spyOn(api, 'put')
  const channel = editingChannel(
    pricedConfig({ image: { '*': { mode: 'per_request' } } })
  )
  mockApi(channel)
  const user = userEvent.setup()
  render(<Harness channel={channel} />)
  await screen.findByDisplayValue('Video channel')

  await user.click(screen.getByRole('button', { name: 'Update Channel' }))

  const routing = screen.getByRole('tab', { name: /Routing & Mapping/ })
  await waitFor(() => expect(routing).toHaveAttribute('aria-selected', 'true'))
  const images = await screen.findByRole('group', { name: 'Reference images' })
  const value = within(images).getByLabelText('Fee value')
  await waitFor(() => expect(value).toHaveFocus())
  expect(value).toHaveAttribute('aria-invalid', 'true')
  expect(put).not.toHaveBeenCalled()
})

test('an enabled draft stays editable after its plugin binding is gone, so its errors can be fixed', async () => {
  const put = vi.spyOn(api, 'put')
  const channel = editingChannel(
    pricedConfig({ image: { '*': { mode: 'per_request' } } }),
    1
  )
  mockApi(channel)
  const user = userEvent.setup()
  render(<Harness channel={channel} />)
  await screen.findByDisplayValue('Video channel')

  await user.click(screen.getByRole('button', { name: 'Update Channel' }))

  const images = await screen.findByRole('group', { name: 'Reference images' })
  const value = within(images).getByLabelText('Fee value')
  await waitFor(() => expect(value).toHaveFocus())
  expect(
    screen.getByRole('switch', { name: 'Schedule this channel' })
  ).toBeChecked()
  expect(put).not.toHaveBeenCalled()
})

test('an out-of-range quality on a hidden tab still reveals routing and focuses the field', async () => {
  const put = vi.spyOn(api, 'put')
  const user = await openRouting(
    editingChannel({ ...pricedConfig(undefined), quality: 2 })
  )
  await screen.findByLabelText('Quality')
  await user.click(screen.getByRole('tab', { name: /Connection & Models/ }))
  // Browsers refuse to submit a natively invalid form before the schema's
  // invalid handler can reveal the hidden tab; jsdom does not, so check it.
  const form = screen
    .getByRole('dialog', { name: 'Edit Channel' })
    .querySelector('form')
  expect(form).toHaveAttribute('novalidate')

  await user.click(screen.getByRole('button', { name: 'Update Channel' }))

  const routing = screen.getByRole('tab', { name: /Routing & Mapping/ })
  await waitFor(() => expect(routing).toHaveAttribute('aria-selected', 'true'))
  const quality = await screen.findByLabelText('Quality')
  await waitFor(() => expect(quality).toHaveFocus())
  expect(quality).toHaveAttribute('aria-invalid', 'true')
  expect(put).not.toHaveBeenCalled()
})

test('quality is validated on blur and correcting it clears the error before saving', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const user = await openRouting(editingChannel(pricedConfig(undefined)))
  const quality = await screen.findByLabelText('Quality')

  await user.clear(quality)
  await user.type(quality, '2')
  await user.tab()

  expect(
    await screen.findByText('Enter a quality between 0 and 1')
  ).toBeVisible()
  expect(quality).toHaveAttribute('aria-invalid', 'true')
  expect(put).not.toHaveBeenCalled()

  await user.clear(quality)
  await user.type(quality, '1')
  await waitFor(() => expect(quality).toHaveAttribute('aria-invalid', 'false'))
  await user.click(screen.getByRole('button', { name: 'Update Channel' }))
  await waitFor(() => expect(put).toHaveBeenCalled())
  const payload = put.mock.calls[0]?.[1] as { settings: string }
  expect(JSON.parse(payload.settings).video_scheduling.quality).toBe(1)
})

test('clearing routing weight blocks saving until an explicit zero or positive integer is entered', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const user = await openRouting(editingChannel(pricedConfig(undefined)))
  const weight = await screen.findByLabelText('Weight')

  await user.clear(weight)
  await user.click(screen.getByRole('button', { name: 'Update Channel' }))

  await waitFor(() => expect(weight).toHaveAttribute('aria-invalid', 'true'))
  expect(weight).toHaveValue(null)
  expect(put).not.toHaveBeenCalled()

  await user.type(weight, '0')
  await user.click(screen.getByRole('button', { name: 'Update Channel' }))
  await waitFor(() => expect(put).toHaveBeenCalled())
  expect(put.mock.calls[0]?.[1]).toMatchObject({ weight: 0 })
})

test('the only base price tier cannot be removed', async () => {
  await openRouting(editingChannel(pricedConfig(undefined)))

  expect(
    await screen.findByRole('button', { name: 'Remove price tier' })
  ).toBeDisabled()
})
