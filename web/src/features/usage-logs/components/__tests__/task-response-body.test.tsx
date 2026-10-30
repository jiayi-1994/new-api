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
import { render, screen, within } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { getTaskRequestBody } from '../../api'
import type { TaskLog } from '../../types'
import { TaskDetailsDialog } from '../dialogs/task-details-dialog'

vi.mock('../../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api')>()),
  getTaskRequestBody: vi.fn(),
}))

const task: TaskLog = {
  id: 1,
  user_id: 7,
  platform: 'video-provider',
  task_id: 'public-task-id',
  action: 'textGenerate',
  channel_id: 3,
  group: 'default',
  quota: 100,
  submit_time: 1,
  status: 'SUCCESS',
}

function renderDialog(log: TaskLog, isAdmin = true) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <TaskDetailsDialog
        log={log}
        isAdmin={isAdmin}
        isRoot={false}
        open
        onOpenChange={() => {}}
      />
    </QueryClientProvider>
  )
}

afterEach(() => {
  vi.mocked(getTaskRequestBody).mockReset()
})

test('an administrator sees a persisted upstream response pretty-printed with its nested fields', async () => {
  vi.mocked(getTaskRequestBody).mockResolvedValue({
    success: true,
    data: { content_type: 'application/json', size: 2, body: '{}' },
  })

  renderDialog({
    ...task,
    data: { status: 'completed', metadata: { volcesVideoUrl: 'https://ark.example/v.mp4' } },
  })

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  const body = within(dialog).getByText(/"volcesVideoUrl": "https:\/\/ark\.example\/v\.mp4"/)
  expect(body).toBeVisible()
  expect(body.textContent).toBe(
    JSON.stringify({ status: 'completed', metadata: { volcesVideoUrl: 'https://ark.example/v.mp4' } }, null, 2)
  )
})

test('an administrator viewing a task without a persisted response sees it reported as not recorded', async () => {
  vi.mocked(getTaskRequestBody).mockResolvedValue({
    success: true,
    data: { content_type: 'application/json', size: 2, body: '{}' },
  })

  renderDialog({ ...task, data: null })

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  expect(await within(dialog).findByText('Not recorded')).toBeVisible()
})

test('a non-administrator does not get a response body section at all', async () => {
  vi.mocked(getTaskRequestBody).mockResolvedValue({
    success: true,
    data: { content_type: 'application/json', size: 2, body: '{}' },
  })

  renderDialog(
    { ...task, data: { metadata: { volcesVideoUrl: 'https://ark.example/v.mp4' } } },
    false
  )

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  expect(within(dialog).queryByText('Response body')).toBeNull()
  expect(within(dialog).queryByText(/volcesVideoUrl/)).toBeNull()
})
