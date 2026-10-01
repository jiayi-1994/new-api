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
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { toast } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import type { OperationsSettings } from '../../types'
import { VideoSchedulingSettingsSection } from '../video-scheduling-settings-section'

const liveSettings = {
  'video_scheduling_setting.mode': 'shadow',
  'video_scheduling_setting.audit_enabled': true,
  'video_scheduling_setting.audit_retention_days': 30,
  'video_scheduling_setting.models': [],
  'video_scheduling_setting.price_weight': 0.5,
  'video_scheduling_setting.quality_weight': 0.3,
  'video_scheduling_setting.service_weight': 0.2,
  'video_scheduling_setting.min_submit_rate': 0.8,
  'video_scheduling_setting.min_gen_rate': 0.5,
  'video_scheduling_setting.min_samples': 20,
  'video_scheduling_setting.window_seconds': 1800,
  'video_scheduling_setting.explore_share': 0.1,
  'video_scheduling_setting.explore_max_in_flight': 2,
  'video_scheduling_setting.probe_ratio': 0.02,
  'video_scheduling_setting.probe_cooldown_sec': 300,
  'video_scheduling_setting.probe_max_in_flight': 1,
  'video_scheduling_setting.unknown_sell_policy': 'exclude',
  'video_scheduling_setting.max_cost_to_sell_ratio': 0,
  'video_scheduling_setting.tie_epsilon': 0,
  'video_scheduling_setting.capacity_groups': '{"account-a":5}',
} as unknown as OperationsSettings

let applyServerValue: (key: string, value: string) => void

function Harness() {
  const [client] = useState(() => new QueryClient())
  const [router] = useState(() =>
    createRouter({
      routeTree: createRootRoute(),
      history: createMemoryHistory({ initialEntries: ['/'] }),
    })
  )
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  const [settings, setSettings] = useState(liveSettings)
  applyServerValue = (key, value) =>
    setSettings((previous) => ({
      ...previous,
      [key]:
        typeof previous[key as keyof OperationsSettings] === 'number'
          ? Number(value)
          : value,
    }))
  return (
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <div ref={setActions} />
        <SettingsPageProvider actionsContainer={actions}>
          <VideoSchedulingSettingsSection settings={settings} />
        </SettingsPageProvider>
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

/** Accepts every key except `failKey`, echoing accepted values back as a refetch would. */
function mockOptionSaves(failKey?: string, failMessage = 'rejected') {
  return vi.spyOn(api, 'put').mockImplementation(async (_url, body) => {
    const { key, value } = body as { key: string; value: string }
    if (key === failKey) {
      return { data: { success: false, message: failMessage } }
    }
    applyServerValue(key, value)
    return { data: { success: true } }
  })
}

async function chooseMode(
  user: ReturnType<typeof userEvent.setup>,
  mode: string
) {
  await user.click(screen.getByRole('combobox', { name: 'Scheduling mode' }))
  await user.click(await screen.findByRole('option', { name: mode }))
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

test('audit collection and retention save independently and reject retention below seven days', async () => {
  const put = mockOptionSaves()
  const user = userEvent.setup()
  render(<Harness />)
  expect(
    screen.getByRole('link', { name: 'Open scheduling audit' })
  ).toHaveAttribute('href', expect.stringContaining('/video-scheduling/audit'))
  await user.click(
    screen.getByRole('switch', { name: 'Collect scheduling audits' })
  )
  fireEvent.change(screen.getByLabelText('Audit retention (days)'), {
    target: { value: '6' },
  })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(
    await screen.findByText('Audit retention must be between 7 and 180 days')
  ).toBeVisible()
  expect(put).not.toHaveBeenCalled()
  fireEvent.change(screen.getByLabelText('Audit retention (days)'), {
    target: { value: '14' },
  })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(put.mock.calls.map((call) => call[1])).toEqual([
    { key: 'video_scheduling_setting.audit_enabled', value: 'false' },
    { key: 'video_scheduling_setting.audit_retention_days', value: '14' },
  ])
})

test('the saved tri-state mode is shown and a new mode saves as its option value', async () => {
  const put = mockOptionSaves()
  const user = userEvent.setup()
  render(<Harness />)
  expect(
    screen.getByRole('combobox', { name: 'Scheduling mode' })
  ).toHaveTextContent('Shadow')

  await chooseMode(user, 'On')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  expect(put.mock.calls[0]?.[1]).toEqual({
    key: 'video_scheduling_setting.mode',
    value: 'on',
  })
})

test('mode is saved after every other changed key', async () => {
  const put = mockOptionSaves()
  const user = userEvent.setup()
  render(<Harness />)

  await chooseMode(user, 'On')
  fireEvent.change(screen.getByLabelText('Price weight'), {
    target: { value: '0.6' },
  })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(
    put.mock.calls.map((call) => (call[1] as { key: string }).key)
  ).toEqual([
    'video_scheduling_setting.price_weight',
    'video_scheduling_setting.mode',
  ])
})

test('a failed key keeps mode unsaved, names the saved keys and keeps the unsaved edits', async () => {
  const put = mockOptionSaves('video_scheduling_setting.quality_weight')
  const error = vi.spyOn(toast, 'error')
  const user = userEvent.setup()
  render(<Harness />)

  await chooseMode(user, 'On')
  fireEvent.change(screen.getByLabelText('Price weight'), {
    target: { value: '0.6' },
  })
  fireEvent.change(screen.getByLabelText('Quality weight'), {
    target: { value: '0.1' },
  })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() =>
    expect(error).toHaveBeenCalledWith(
      'Mode was not saved. Saved settings: price_weight'
    )
  )
  expect(
    put.mock.calls.map((call) => (call[1] as { key: string }).key)
  ).toEqual([
    'video_scheduling_setting.price_weight',
    'video_scheduling_setting.quality_weight',
  ])
  expect(
    screen.getByRole('combobox', { name: 'Scheduling mode' })
  ).toHaveTextContent('On')
  expect(screen.getByLabelText('Quality weight')).toHaveValue(0.1)
})

test('a rejected mode switch shows the server reason next to the mode', async () => {
  mockOptionSaves(
    'video_scheduling_setting.mode',
    'video scheduling mode on requires Redis in multi-instance deployments'
  )
  const user = userEvent.setup()
  render(<Harness />)

  await chooseMode(user, 'On')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  expect(
    await screen.findByText(
      'video scheduling mode on requires Redis in multi-instance deployments',
      { selector: '[data-slot="form-message"]' }
    )
  ).toBeVisible()
})

test('the simulator snapshot carries unsaved edits but keeps the live window', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: false, message: 'no channels' } })
  const user = userEvent.setup()
  render(<Harness />)

  fireEvent.change(screen.getByLabelText('Window (seconds)'), {
    target: { value: '60' },
  })
  fireEvent.change(screen.getByLabelText('Price weight'), {
    target: { value: '0.9' },
  })
  await user.click(
    screen.getByRole('button', { name: 'Open scheduling simulator' })
  )
  await user.click(
    screen.getByRole('checkbox', { name: /Use the unsaved settings/ })
  )
  await user.click(screen.getByRole('button', { name: 'Simulate' }))

  await waitFor(() => expect(post).toHaveBeenCalled())
  const body = post.mock.calls[0]?.[1] as {
    config_snapshot: Record<string, unknown>
  }
  expect(body.config_snapshot).toMatchObject({
    window_seconds: 1800,
    price_weight: 0.9,
    capacity_groups: { 'account-a': 5 },
  })
})

test('an edit made while the save is in flight survives its success and stays unsaved', async () => {
  let finishSave = () => {}
  const put = vi.spyOn(api, 'put').mockImplementation(async (_url, body) => {
    const { key, value } = body as { key: string; value: string }
    if (put.mock.calls.length === 1) {
      await new Promise<void>((resolve) => {
        finishSave = resolve
      })
    }
    applyServerValue(key, value)
    return { data: { success: true } }
  })
  const user = userEvent.setup()
  render(<Harness />)

  fireEvent.change(screen.getByLabelText('Price weight'), {
    target: { value: '0.6' },
  })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  fireEvent.change(screen.getByLabelText('Price weight'), {
    target: { value: '0.7' },
  })
  finishSave()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )

  expect(screen.getByLabelText('Price weight')).toHaveValue(0.7)
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(put.mock.calls[1]?.[1]).toEqual({
    key: 'video_scheduling_setting.price_weight',
    value: '0.7',
  })
})
