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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

// Compile the real lazy drawer before measuring its interactive behavior.
import '@/features/channels/components/drawers/channel-mutate-drawer'
import { CHANNEL_TYPE_TASK_PLUGIN } from '@/features/channels/constants'
import {
  channelSchema,
  type Channel,
  type VideoModelCost,
} from '@/features/channels/types'
import type { VideoSalesModel } from '@/features/pricing/types'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { inspectVideoSalesRoute } from '../../models/video-sales-routing'
import { VideoSalesRoutingPanel } from '../../models/video-sales-routing-panel'
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

test('a cold-start recommendation explains limited admission and hides legacy scores', async () => {
  await runSimulation({
    success: true,
    data: {
      ...simulation,
      selection_policy: 'stability_cost_v2',
      flow: 'explore',
      selection_reason: 'no_normal_candidate',
      slot_occupancy: { 3: 1 },
      validation_limits: { explore: 2, recover: 1 },
    },
  })
  expect(
    await screen.findByText('No verified candidates; limited validation')
  ).toBeVisible()
  expect(screen.getByText(/Cold-start validation/)).toBeVisible()
  expect(screen.queryByText('P / Q / S / Total')).not.toBeInTheDocument()
  expect(screen.getAllByText('Health state unavailable').length).toBe(2)
  expect(screen.getByText('Validation slots: 1 / 2')).toBeVisible()
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

const routeSales: VideoSalesModel = {
  resolutions: {
    '720p': { usd_per_second: 0.02, seconds: [10, 15] },
    '4k': { usd_per_second: 0.05, seconds: [15] },
  },
}
const routePlugins = [
  { key: 'video-a', name: 'Video A', models: ['upstream-mini'] },
]
const routeCost: VideoModelCost = {
  mode: 'per_second',
  prices: { '720p': 0, '2160p': 0.03 },
  max_seconds: 5,
  allowed_seconds_by_resolution: { '720p': [10], '2160p': [15] },
  references: {
    video: { '*': { mode: 'per_input_second', value: 0.2 } },
    image: { '*': { mode: 'included' } },
  },
}

function routingChannel(overrides: Partial<Channel> = {}): Channel {
  return channelSchema.parse({
    id: 42,
    name: 'Route A',
    type: CHANNEL_TYPE_TASK_PLUGIN,
    key: '',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'VIDEO-PUBLIC',
    group: 'default',
    setting: JSON.stringify({ task_plugin_key: 'video-a' }),
    model_mapping: JSON.stringify({
      'Video-Public': 'alias',
      alias: 'upstream-mini',
    }),
    settings: JSON.stringify({
      video_scheduling: {
        capacity_group: 'shared-account',
        models: { 'video-public': routeCost },
      },
    }),
    ...overrides,
  })
}

test('routing coverage uses resolution-specific seconds and accepts a real zero procurement cost', () => {
  const route = inspectVideoSalesRoute(
    routingChannel(),
    'video-public',
    routeSales,
    routePlugins
  )
  expect(route.issue).toBeNull()
  expect(route.target).toBe('upstream-mini')
  expect(route.coverage).toEqual([
    { tier: '720p', seconds: [10], price: 0 },
    { tier: '4k', seconds: [15], price: 0.03 },
  ])
})

test.each([
  [
    { model_mapping: '{"video-public":"a","a":"video-public"}' },
    'Upstream mapping contains a cycle',
  ],
  [
    { model_mapping: '{"video-public":"a","VIDEO-PUBLIC":"b"}' },
    'Conflicting upstream mappings',
  ],
  [{ model_mapping: '{}' }, 'No upstream mapping'],
  [{ status: 2 }, 'Channel disabled'],
  [
    {
      settings:
        '{"video_scheduling":{"models":{"video-public":{"mode":"invalid","prices":{"720p":0}}}}}',
    },
    'Invalid procurement configuration',
  ],
  [{ settings: '{}' }, 'Procurement cost is not configured'],
])('invalid routing %j does not claim sales coverage', (overrides, issue) => {
  const route = inspectVideoSalesRoute(
    routingChannel(overrides),
    'video-public',
    routeSales,
    routePlugins
  )
  expect(route.issue).toBe(issue)
  expect(route.coverage).toEqual([])
})

test.each(['__proto__', 'constructor', 'toString'])(
  'routing preserves the legal public identity %s without inheriting mappings',
  (model) => {
    const channel = routingChannel({
      models: model,
      model_mapping: JSON.stringify(
        Object.fromEntries([[model, 'upstream-mini']])
      ),
      settings: JSON.stringify({
        video_scheduling: { models: Object.fromEntries([[model, routeCost]]) },
      }),
    })
    expect(
      inspectVideoSalesRoute(channel, model, routeSales, routePlugins).coverage
    ).toHaveLength(2)
    const unmapped = inspectVideoSalesRoute(
      { ...channel, model_mapping: '{}' },
      model,
      routeSales,
      routePlugins
    )
    expect(unmapped.target).toBeNull()
    expect(unmapped.issue).toBe('No upstream mapping')
  }
)

test('ambiguous case costs and server-dependent aliases defer coverage to runtime', () => {
  const channel = routingChannel({
    settings: JSON.stringify({
      video_scheduling: {
        models: {
          'video-public': routeCost,
          'VIDEO-PUBLIC': { ...routeCost, prices: { '720p': 1 } },
        },
      },
    }),
  })
  const route = inspectVideoSalesRoute(
    channel,
    'video-public',
    routeSales,
    routePlugins
  )
  expect(route.uncertain).toBe(true)
  expect(route.coverage).toEqual([])
  const alias = inspectVideoSalesRoute(
    routingChannel({ model_mapping: '{"video-public":"upstream@high"}' }),
    'video-public',
    routeSales,
    routePlugins
  )
  expect(alias.issue).toBe('Routing requires runtime confirmation')
  expect(alias.coverage).toEqual([])
})

function renderRouting(): QueryClient {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <VideoSalesRoutingPanel modelName='video-public' sales={routeSales} />
      </RouterContextProvider>
    </QueryClientProvider>
  )
  return client
}

test('the routing panel loads all pages, filters exact public names, and opens the existing channel editor', async () => {
  const originalAuth = useAuthStore.getState().auth
  useAuthStore.setState({
    auth: {
      ...originalAuth,
      user: { id: 1, username: 'root', role: ROLE.SUPER_ADMIN },
    },
  })
  const channel = routingChannel()
  const disabled = routingChannel({
    id: 43,
    name: 'Route B disabled',
    status: 2,
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (url === '/api/channel/search') {
      return {
        data: {
          success: true,
          data: {
            items:
              config?.params?.p === 1
                ? [channel]
                : [
                    disabled,
                    routingChannel({
                      id: 44,
                      name: 'Different model',
                      models: 'video-public-extra',
                    }),
                  ],
            total: 3,
          },
        },
      }
    }
    const responses: Record<string, unknown> = {
      '/api/task_plugin_options': routePlugins,
      '/api/channel/42': channel,
      '/api/channel/models': [],
      '/api/channel/default_base_urls': {},
      '/api/group/': ['default'],
      '/api/prefill_group': [],
      '/api/option/': [],
      '/api/channel/video_schedule/schedulable': {
        plugin: 'video-a',
        describe_spec: true,
        models: [],
      },
    }
    if (!Object.hasOwn(responses, url)) throw new Error(`Unexpected GET ${url}`)
    return { data: { success: true, data: responses[url] } }
  })
  const client = renderRouting()
  try {
    expect(screen.getByText('Loading channel routing...')).toBeVisible()
    const table = await screen.findByRole('table', {
      name: 'Video channel routing',
    })
    expect(within(table).getByText('Route B disabled')).toBeVisible()
    expect(within(table).queryByText('Different model')).not.toBeInTheDocument()
    expect(within(table).getByText('720p: 10 s')).toBeVisible()
    expect(
      within(table).getAllByText('Video *: Per input video second $0.2')
    ).toHaveLength(2)
    expect(
      within(table).getAllByText('Image *: Included in base price')
    ).toHaveLength(2)
    expect(screen.getByRole('alert')).toHaveTextContent('720p: 15 s')
    expect(get).toHaveBeenCalledWith(
      '/api/channel/search',
      expect.objectContaining({ params: expect.objectContaining({ p: 2 }) })
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Edit channel Route A' }))
    expect(
      await screen.findByDisplayValue('Route A', {}, { timeout: 10000 })
    ).toBeVisible()
    expect(screen.getByRole('tab', { name: /Routing & Mapping/ })).toBeVisible()
  } finally {
    cleanup()
    client.clear()
    useAuthStore.setState({ auth: originalAuth })
  }
}, 20000)

test('routing request failure exposes retry and a successful empty response shows the empty state', async () => {
  let failed = true
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/task_plugin_options') {
      return { data: { success: true, data: routePlugins } }
    }
    if (failed) {
      return {
        data: { success: false, message: 'Channel routing unavailable' },
      }
    }
    return { data: { success: true, data: { items: [], total: 0 } } }
  })
  const client = renderRouting()
  try {
    expect(await screen.findByText('Channel routing unavailable')).toBeVisible()
    failed = false
    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByText('No channels expose this sales model')
    ).toBeVisible()
  } finally {
    cleanup()
    client.clear()
  }
})
