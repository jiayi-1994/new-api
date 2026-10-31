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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { getTaskRequestBody, getTaskResponseBody } from '../../api'
import type { TaskLog } from '../../types'
import { TaskDetailsDialog } from '../dialogs/task-details-dialog'

vi.mock('../../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api')>()),
  getTaskRequestBody: vi.fn(),
  getTaskResponseBody: vi.fn(),
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

// Indented by the server; the seed exceeds 2^53 and must not be rounded.
const platformBody =
  '{\n  "id": "public-task-id",\n  "seed": 18446744073709551615\n}'
const upstreamBody = '{\n  "upstream_account": "acct-private"\n}'

function renderDialog(role: 'user' | 'admin' | 'root') {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <TaskDetailsDialog
        log={task}
        isAdmin={role !== 'user'}
        isRoot={role === 'root'}
        open
        onOpenChange={() => {}}
      />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.mocked(getTaskRequestBody).mockResolvedValue({ success: true, data: null })
})

afterEach(() => {
  vi.mocked(getTaskRequestBody).mockReset()
  vi.mocked(getTaskResponseBody).mockReset()
})

test('a root user sees the platform and upstream responses exactly as the server returned them', async () => {
  vi.mocked(getTaskResponseBody).mockResolvedValue({
    success: true,
    data: {
      platform_format: 'openai_video',
      platform: platformBody,
      upstream: upstreamBody,
    },
  })

  renderDialog('root')

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  expect(
    (await within(dialog).findByText(/18446744073709551615/)).textContent
  ).toBe(platformBody)
  expect(within(dialog).getByText(/acct-private/).textContent).toBe(
    upstreamBody
  )
  expect(within(dialog).getByText('openai_video')).toBeVisible()
  expect(getTaskResponseBody).toHaveBeenCalledWith('public-task-id')
})

test.each(['user', 'admin'] as const)(
  'a %s sees the platform response but never an upstream response section',
  async (role) => {
    vi.mocked(getTaskResponseBody).mockResolvedValue({
      success: true,
      data: { platform_format: 'task', platform: platformBody },
    })

    renderDialog(role)

    const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
    expect(
      (await within(dialog).findByText(/18446744073709551615/)).textContent
    ).toBe(platformBody)
    expect(within(dialog).queryByText('Upstream response')).toBeNull()
  }
)

test('a root user viewing a task without an upstream snapshot sees it reported as not recorded', async () => {
  vi.mocked(getTaskResponseBody).mockResolvedValue({
    success: true,
    data: { platform_format: 'task', platform: platformBody },
  })

  renderDialog('root')

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  await within(dialog).findByText(/18446744073709551615/)
  const upstreamSection = within(dialog)
    .getByText('Upstream response')
    .closest('section')
  expect(upstreamSection).not.toBeNull()
  expect(
    within(upstreamSection as HTMLElement).getByText('Not recorded')
  ).toBeVisible()
})

test('a failed response body request is reported as failed to load', async () => {
  vi.mocked(getTaskResponseBody).mockRejectedValue(new Error('network'))

  renderDialog('user')

  const dialog = await screen.findByRole('dialog', { name: /Task Details/ })
  const platformSection = within(dialog)
    .getByText('Platform response')
    .closest('section')
  expect(platformSection).not.toBeNull()
  expect(
    await within(platformSection as HTMLElement).findByText('Failed to load')
  ).toBeVisible()
})
