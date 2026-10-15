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
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { TaskLog } from '../../types'
import { UsageLogsProvider } from '../usage-logs-provider'
import { UsageLogsTable } from '../usage-logs-table'

const task: TaskLog = {
  id: 1,
  user_id: 1,
  platform: 'video',
  task_id: 'reference-video-task',
  action: 'GENERATE',
  channel_id: 0,
  group: 'default',
  quota: 0,
  submit_time: 1788840000,
  status: 'SUCCESS',
  video_mode: 'reference_to_video',
  reference_seconds: 2.25,
}

async function renderTasks(records: TaskLog[] = [task]) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'tester', role: ROLE.USER })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: records, total: records.length } },
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: () => <UsageLogsTable logCategory='task' />,
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: ['/usage-logs/task'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rendered = render(
    <QueryClientProvider client={client}>
      <UsageLogsProvider>
        <RouterProvider router={router} />
      </UsageLogsProvider>
    </QueryClientProvider>
  )
  await screen.findByText(records[0].task_id)
  await waitFor(() => expect(client.isFetching()).toBe(0))
  return { ...rendered, client }
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  localStorage.clear()
})

test('generation type distinguishes video references, images and text without a separate reference column', async () => {
  await renderTasks([
    task,
    {
      ...task,
      id: 2,
      task_id: 'text-video-task',
      video_mode: 'text_to_video',
      reference_seconds: 0,
    },
    {
      ...task,
      id: 3,
      task_id: 'legacy-task',
      video_mode: undefined,
      reference_seconds: undefined,
    },
    {
      ...task,
      id: 4,
      task_id: 'image-video-task',
      video_mode: 'image_to_video',
      reference_seconds: 0,
    },
  ])
  const referenceRow = screen.getByRole('row', { name: /reference-video-task/ })
  expect(
    within(referenceRow).getByText('Video reference generation')
  ).toBeVisible()
  const imageRow = screen.getByRole('row', { name: /image-video-task/ })
  expect(within(imageRow).getByText('Image to Video')).toBeVisible()
  const textRow = screen.getByRole('row', { name: /text-video-task/ })
  expect(within(textRow).getByText('Text to Video')).toBeVisible()
  const legacyRow = screen.getByRole('row', { name: /legacy-task/ })
  expect(
    within(legacyRow).queryByText('Video reference generation')
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('columnheader', { name: 'Has reference' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('columnheader', { name: 'Reference seconds' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('2.25s')).not.toBeInTheDocument()
})

test('ordinary plugin tasks display canonical and legacy generation actions without a scheduling spec', async () => {
  const cases = [
    ['text_to_video', 'Text to Video'],
    ['image_to_video', 'Image to Video'],
    ['reference_to_video', 'Reference Video'],
    ['first_tail_to_video', 'First/Last Frame to Video'],
    ['remix', 'Video Remix'],
    ['textGenerate', 'Text to Video'],
  ] as const
  await renderTasks(
    cases.map(([action], index) => ({
      ...task,
      id: index + 10,
      task_id: `ordinary-${action}`,
      action,
      video_mode: undefined,
      reference_seconds: undefined,
      seconds: 15,
      resolution: '720p',
    }))
  )
  for (const [action, label] of cases) {
    const row = screen.getByRole('row', {
      name: new RegExp(`ordinary-${action}`),
    })
    expect(within(row).getByText(label)).toBeVisible()
    expect(within(row).getByText('720p')).toBeVisible()
    expect(within(row).getByText('15s')).toBeVisible()
  }
})

test('reference seconds can be enabled in View and remain visible after remounting', async () => {
  const user = userEvent.setup()
  const view = await renderTasks()
  await user.click(screen.getByRole('button', { name: 'View' }))
  expect(
    screen.queryByRole('menuitemcheckbox', { name: 'Has reference' })
  ).not.toBeInTheDocument()
  const option = screen.getByRole('menuitemcheckbox', {
    name: 'Reference seconds',
  })
  expect(option).toHaveAttribute('aria-checked', 'false')
  await user.click(option)
  await user.keyboard('{Escape}')
  expect(
    screen.getByRole('columnheader', { name: 'Reference seconds' })
  ).toBeVisible()
  expect(screen.getByText('2.25s')).toBeVisible()
  view.unmount()
  view.client.clear()
  await renderTasks()
  expect(screen.getByText('2.25s')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'View' }))
  await user.click(
    screen.getByRole('menuitemcheckbox', { name: 'Reference seconds' })
  )
  await user.keyboard('{Escape}')
  expect(screen.queryByText('2.25s')).not.toBeInTheDocument()
})

test('visible reference seconds preserve zero while missing duration stays unknown', async () => {
  localStorage.setItem(
    'usage-logs:task:self:column-visibility',
    JSON.stringify({ reference_seconds: true })
  )
  await renderTasks([
    { ...task, task_id: 'zero-duration-task', reference_seconds: 0 },
    {
      ...task,
      id: 2,
      task_id: 'unknown-duration-task',
      reference_seconds: undefined,
    },
  ])
  expect(screen.getByText('0s')).toBeVisible()
  const unknownRow = screen.getByRole('row', { name: /unknown-duration-task/ })
  expect(within(unknownRow).queryByText('0s')).not.toBeInTheDocument()
})

test('mobile task cards follow the reference seconds visibility toggle', async () => {
  const matchMedia = window.matchMedia.bind(window)
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...matchMedia(query),
    matches: query.includes('max-width: 640px') || matchMedia(query).matches,
  }))
  const user = userEvent.setup()
  await renderTasks()
  expect(screen.getByText('Generation type')).toBeVisible()
  expect(screen.getByText('Video reference generation')).toBeVisible()
  expect(screen.queryByText('Has reference')).not.toBeInTheDocument()
  expect(screen.queryByText('Reference seconds')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'View' }))
  await user.click(
    screen.getByRole('menuitemcheckbox', { name: 'Reference seconds' })
  )
  await user.keyboard('{Escape}')
  expect(screen.getByText('Reference seconds')).toBeVisible()
  expect(screen.getByText('2.25s')).toBeVisible()
})
