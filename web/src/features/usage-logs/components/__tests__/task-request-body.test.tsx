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

function renderDialog() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <TaskDetailsDialog
        log={task}
        isAdmin={false}
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

test('a recorded request body is shown exactly as the client sent it', async () => {
  const body = '{"model":"sora-2","seed":12345678901234567890}'
  vi.mocked(getTaskRequestBody).mockResolvedValue({
    success: true,
    data: { content_type: 'application/json', size: body.length, body },
  })

  renderDialog()

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  expect(await within(dialog).findByText(body)).toBeVisible()
  expect(within(dialog).getByText('application/json')).toBeVisible()
  expect(getTaskRequestBody).toHaveBeenCalledWith('public-task-id')
})

test('a task submitted before bodies were recorded reports the body as not recorded', async () => {
  vi.mocked(getTaskRequestBody).mockResolvedValue({ success: true, data: null })

  renderDialog()

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  expect(await within(dialog).findByText('Not recorded')).toBeVisible()
})
