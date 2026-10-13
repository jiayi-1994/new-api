import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { toast } from 'sonner'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { PerformanceSection } from '../performance-section'

function Fixture() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  const [status, setStatus] = useState<HTMLSpanElement | null>(null)
  return (
    <>
      <div ref={setActions} />
      <span ref={setStatus} />
      <SettingsPageProvider
        actionsContainer={actions}
        titleStatusContainer={status}
      >
        <PerformanceSection
          defaultValues={{
            'task_polling_setting.interval_seconds': 15,
            'performance_setting.disk_cache_enabled': false,
            'performance_setting.disk_cache_threshold_mb': 10,
            'performance_setting.disk_cache_max_size_mb': 1024,
            'performance_setting.disk_cache_path': '',
            'performance_setting.monitor_enabled': false,
            'performance_setting.monitor_cpu_threshold': 90,
            'performance_setting.monitor_memory_threshold': 90,
            'performance_setting.monitor_disk_threshold': 95,
          }}
        />
      </SettingsPageProvider>
    </>
  )
}

async function renderSettings() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({ component: Fixture }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return screen.findByRole('spinbutton', {
    name: 'Async task polling interval (seconds)',
  })
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: {} } })
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  vi.spyOn(toast, 'success')
})

test.each(['3', '5', '300'])(
  'saving interval %s maps the nested field to its option key',
  async (value) => {
    const input = await renderSettings()
    expect(input).toHaveValue(15)
    fireEvent.change(input, { target: { value } })
    expect(await screen.findByText('Unsaved changes')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/option/', {
        key: 'task_polling_setting.interval_seconds',
        value: Number(value),
      })
    )
    await waitFor(() =>
      expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
    )
    expect(api.put).toHaveBeenCalledTimes(1)
    expect(input).toHaveValue(Number(value))
  }
)

test.each(['', '0', '2', '-1', '301', '5.5'])(
  'invalid interval "%s" shows a field error and prevents saving',
  async (value) => {
    const input = await renderSettings()
    fireEvent.change(input, { target: { value } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
    expect(
      screen.getByText('Enter a whole number from 3 to 300')
    ).toBeInTheDocument()
    expect(api.put).not.toHaveBeenCalled()
  }
)

test('a failed save preserves the draft and never reports success', async () => {
  vi.mocked(api.put).mockResolvedValue({
    data: { success: false, message: 'test write failure' },
  })
  const input = await renderSettings()
  fireEvent.change(input, { target: { value: '5' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(input).toHaveValue(5)
  expect(screen.getByText('Unsaved changes')).toBeInTheDocument()
  expect(toast.success).not.toHaveBeenCalled()
})

test('saving disables resubmission and reenables the input after persistence', async () => {
  let complete: (value: { data: { success: boolean } }) => void = () => {}
  vi.mocked(api.put).mockImplementation(
    () =>
      new Promise((resolve) => {
        complete = resolve
      })
  )
  const input = await renderSettings()
  fireEvent.change(input, { target: { value: '5' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  expect(
    await screen.findByRole('button', { name: 'Saving...' })
  ).toBeDisabled()
  expect(input).toBeDisabled()
  await act(async () => {
    complete({ data: { success: true } })
  })
  await waitFor(() => expect(input).toBeEnabled())
  expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
})
