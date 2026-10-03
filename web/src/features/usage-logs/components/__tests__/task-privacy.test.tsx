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
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'

import { DataTableView } from '@/components/data-table'

import type { TaskLog } from '../../types'
import { useTaskLogsColumns } from '../columns/task-logs-columns'
import { UsageLogsProvider } from '../usage-logs-provider'

const task: TaskLog = {
  id: 1,
  user_id: 7,
  platform: 'private-video-provider',
  task_id: 'public-task-id',
  action: 'textGenerate',
  channel_id: 927,
  group: 'default',
  quota: 100,
  submit_time: 1,
  status: 'SUCCESS',
  properties: {
    origin_model_name: 'public-video-model',
    upstream_model_name: 'private-upstream-model',
  },
  admin_info: {
    task_plugin: {
      key: 'private-video-provider',
      name: 'Private Video Provider',
      version: '1.2.3',
    },
  },
  root_info: {
    upstream_task_id: 'private-upstream-task',
    node_name: 'private-worker-node',
  },
}

function TaskTable(props: { log: TaskLog; isAdmin: boolean; isRoot: boolean }) {
  const columns = useTaskLogsColumns(props.isAdmin, props.isRoot)
  const table = useReactTable({
    data: [props.log],
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  return <DataTableView table={table} />
}

function renderTask(log = task, isAdmin = false, isRoot = false) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <UsageLogsProvider>
        <TaskTable log={log} isAdmin={isAdmin} isRoot={isRoot} />
      </UsageLogsProvider>
    </QueryClientProvider>
  )
}

test('opening task details in the self view hides backend identifiers while retaining public task information', async () => {
  const user = userEvent.setup()
  renderTask()

  await user.click(screen.getByRole('button', { name: 'View details' }))

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  expect(within(dialog).getByText('public-video-model')).toBeVisible()
  expect(within(dialog).getByText('Text to Video')).toBeVisible()
  expect(within(dialog).getByText('Success')).toBeVisible()
  expect(within(dialog).getByText('public-task-id')).toBeVisible()
  expect(within(dialog).queryByText('Platform')).not.toBeInTheDocument()
  expect(within(dialog).queryByText('Actual Model')).not.toBeInTheDocument()
  expect(within(dialog).queryByText('Channel')).not.toBeInTheDocument()
  expect(dialog).not.toHaveTextContent('private-video-provider')
  expect(dialog).not.toHaveTextContent('private-upstream-model')
  expect(dialog).not.toHaveTextContent('Private Video Provider')
  expect(dialog).not.toHaveTextContent('#927')
  expect(dialog).not.toHaveTextContent('private-upstream-task')
  expect(dialog).not.toHaveTextContent('private-worker-node')
})

test('the self task list labels a task with its public model and action instead of its platform', () => {
  renderTask()

  expect(screen.getByText('public-video-model · Text to Video')).toBeVisible()
  expect(screen.getByRole('table')).not.toHaveTextContent(
    'private-video-provider'
  )
  expect(
    screen.queryByRole('columnheader', { name: 'Channel' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('columnheader', { name: 'Plugin' })
  ).not.toBeInTheDocument()
})

test('the admin view retains platform, plugin, upstream model and channel diagnostics', async () => {
  const user = userEvent.setup()
  renderTask(task, true)

  expect(
    screen.getByText('private-video-provider · Text to Video')
  ).toBeVisible()
  expect(screen.getByRole('columnheader', { name: 'Channel' })).toBeVisible()
  expect(screen.getByRole('columnheader', { name: 'Plugin' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'View details' }))

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  expect(within(dialog).getByText('Platform')).toBeVisible()
  expect(dialog).toHaveTextContent('private-video-provider')
  expect(within(dialog).getByText('private-upstream-model')).toBeVisible()
  expect(within(dialog).getByText('Private Video Provider')).toBeVisible()
  expect(within(dialog).getByText('#927')).toBeVisible()
  expect(within(dialog).getByText('public-video-model')).toBeVisible()
  expect(within(dialog).queryByText('Root Diagnostics')).not.toBeInTheDocument()
  expect(dialog).not.toHaveTextContent('private-upstream-task')
})

test('a legacy music task without a public model keeps its action caption and audio preview', async () => {
  const user = userEvent.setup()
  renderTask({
    ...task,
    platform: 'suno',
    action: 'MUSIC',
    properties: undefined,
    admin_info: undefined,
    root_info: undefined,
  })

  expect(screen.getByText('Generate Music')).toBeVisible()
  expect(screen.getByRole('table')).not.toHaveTextContent('suno')
  expect(
    screen.getByRole('button', { name: 'Click to preview audio' })
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'View details' }))

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  expect(within(dialog).getByText('Generate Music')).toBeVisible()
  expect(within(dialog).getByText('Success')).toBeVisible()
})
