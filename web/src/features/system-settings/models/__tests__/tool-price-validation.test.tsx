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
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { useState } from 'react'
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest'

// Preload the real lazy drawer so the interaction assertion does not time module loading.
import '@/features/channels/components/drawers/channel-mutate-drawer'
import { channelSchema } from '@/features/channels/types'
import type { VideoSalesModel } from '@/features/pricing/types'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { ToolPriceSettings } from '../tool-price-settings'
import {
  createVideoSalesFormSchema,
  videoSalesFormValues,
} from '../video-sales-form'
import { VideoSalesSettingsCard } from '../video-sales-settings-card'

describe('tool price validation', () => {
  beforeAll(() => {
    i18next.addResourceBundle('en', 'translation', {
      'Price ($/1K calls)': 'Price ($/1K calls)',
      'Please enter a valid number': 'Please enter a valid number',
      'Tool identifier': 'Tool identifier',
    })
  })

  test('blocks an empty price without converting it to an explicit zero', () => {
    const queryClient = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <ToolPriceSettings defaultValue='{"web_search":10}' />
      </QueryClientProvider>
    )

    const priceInput = screen.getByRole('spinbutton', {
      name: 'Price ($/1K calls): web_search',
    })
    const saveButton = screen.getByRole('button', { name: 'Save tool prices' })

    fireEvent.change(priceInput, { target: { value: '' } })

    expect(priceInput).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText('Please enter a valid number')).toBeInTheDocument()
    expect(saveButton).toBeDisabled()

    fireEvent.change(priceInput, { target: { value: '0' } })

    expect(priceInput).toHaveAttribute('aria-invalid', 'false')
    expect(saveButton).toBeEnabled()

    fireEvent.change(priceInput, { target: { value: '0.04' } })

    expect(priceInput).toHaveValue(0.04)
    expect(priceInput).toBeValid()
    expect(saveButton).toBeEnabled()

    fireEvent.change(priceInput, { target: { value: '0.0001' } })
    expect(priceInput).toBeValid()

    fireEvent.change(priceInput, { target: { value: '0.00001' } })
    expect((priceInput as HTMLInputElement).validity.stepMismatch).toBe(true)

    fireEvent.change(priceInput, { target: { value: '-0.04' } })

    expect(priceInput).toHaveAttribute('aria-invalid', 'true')
    expect(saveButton).toBeDisabled()

    queryClient.clear()
  })
})

const sales: Record<string, VideoSalesModel> = {
  'video-main': {
    disabled: true,
    resolutions: {
      '720p': {
        usd_per_second: 0.02,
        input_video_usd_per_second: 0,
        seconds: [5, 10, 15],
      },
    },
  },
  'video-other': {
    disabled: false,
    resolutions: {
      '1080p': {
        usd_per_second: 0.08,
        input_video_usd_per_second: 0,
        seconds: [5, 10],
      },
    },
  },
}
const videoClients: QueryClient[] = []
const originalAuth = useAuthStore.getState().auth

function VideoSalesFixture(props: { value?: string }) {
  const [client] = useState(() => {
    const next = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    videoClients.push(next)
    return next
  })
  const [router] = useState(() =>
    createRouter({
      routeTree: createRootRoute(),
      history: createMemoryHistory({ initialEntries: ['/'] }),
    })
  )
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <div ref={setActions} />
        <SettingsPageProvider actionsContainer={actions}>
          <VideoSalesSettingsCard
            defaultValue={props.value ?? JSON.stringify(sales)}
          />
        </SettingsPageProvider>
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  videoClients.forEach((client) => client.clear())
  videoClients.length = 0
  useAuthStore.setState({ auth: originalAuth })
  vi.restoreAllMocks()
})

describe('unified video sales validation and persistence', () => {
  test('does not publish a previous price when the amount input contains an overflow draft', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(
      <VideoSalesFixture
        value={JSON.stringify({ 'video-main': sales['video-main'] })}
      />
    )
    const user = userEvent.setup()
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Allowed durations (seconds)' }),
      { target: { value: '5, 10' } }
    )
    const price = screen.getByRole('textbox', {
      name: 'Price per second (USD)',
    })
    fireEvent.change(price, { target: { value: '9'.repeat(309) } })
    expect(price).toBeInvalid()
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    expect(put).not.toHaveBeenCalled()
    fireEvent.change(price, { target: { value: '0.06' } })
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { value: string }
    expect(JSON.parse(request.value)['video-main'].resolutions['720p']).toEqual(
      { usd_per_second: 0.06, input_video_usd_per_second: 0, seconds: [5, 10] }
    )
  })

  test('requires a fresh delete confirmation after a clean table is refreshed', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    const { rerender } = render(<VideoSalesFixture />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Delete video-other' }))
    expect(screen.getByRole('alertdialog')).toBeVisible()
    rerender(
      <VideoSalesFixture
        value={JSON.stringify({ 'video-added': sales['video-main'], ...sales })}
      />
    )
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Delete video-other' }))
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      })
    )
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { value: string }
    expect(JSON.parse(request.value)).toEqual({
      'video-added': sales['video-main'],
      'video-main': sales['video-main'],
    })
  })

  test('saves channel drawer changes without publishing the unsaved sales form', async () => {
    const channel = channelSchema.parse({
      id: 42,
      name: 'Existing channel',
      type: 1,
      key: '',
      status: 1,
      created_time: 1,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      models: 'video-main',
      group: 'default',
      base_url: 'https://saved.example',
    })
    useAuthStore.setState({
      auth: {
        ...originalAuth,
        user: { id: 1, username: 'root', role: ROLE.SUPER_ADMIN },
      },
    })
    vi.spyOn(api, 'get').mockImplementation(async (url) => {
      if (url === '/api/channel/search') {
        return { data: { success: true, data: { items: [channel], total: 1 } } }
      }
      if (url === '/api/channel/42') {
        return { data: { success: true, data: channel } }
      }
      if (url === '/api/task_plugin_options' || url === '/api/prefill_group') {
        return { data: { success: true, data: [] } }
      }
      if (url === '/api/channel/models') {
        return { data: { success: true, data: [{ id: 'video-main' }] } }
      }
      if (url === '/api/channel/default_base_urls') {
        return { data: { success: true, data: {} } }
      }
      if (url === '/api/group/') {
        return { data: { success: true, data: ['default'] } }
      }
      throw new Error(`Unexpected GET ${url}`)
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(
      <VideoSalesFixture
        value={JSON.stringify({ 'video-main': sales['video-main'] })}
      />
    )
    const user = userEvent.setup()
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Price per second (USD)' }),
      { target: { value: '0.06' } }
    )
    await user.click(
      await screen.findByRole('button', {
        name: 'Edit channel Existing channel',
      })
    )
    await user.click(
      await screen.findByRole('button', { name: 'Update Channel' })
    )
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    expect(put.mock.calls[0][0]).toBe('/api/channel/')
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: 'Update Channel' })
      ).not.toBeInTheDocument()
    )
    expect(
      screen.getByRole('textbox', { name: 'Price per second (USD)' })
    ).toHaveValue('0.06')
    expect(
      screen.getByRole('button', { name: 'Save video sales' })
    ).toBeEnabled()
    expect(put).toHaveBeenCalledOnce()
  })

  test('preserves unedited public model names that are JavaScript object keys', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    const special = Object.fromEntries([
      ['__proto__', sales['video-other']],
      ['constructor', sales['video-other']],
      ['video-main', sales['video-main']],
    ])
    render(<VideoSalesFixture value={JSON.stringify(special)} />)
    const main = screen.getByRole('table', {
      name: 'Sale tiers for video-main',
    })
    fireEvent.change(
      within(main).getByRole('textbox', { name: 'Price per second (USD)' }),
      { target: { value: '0.06' } }
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { value: string }
    const saved = JSON.parse(request.value)
    expect(Object.keys(saved)).toEqual([
      '__proto__',
      'constructor',
      'video-main',
    ])
    expect(saved['__proto__']).toEqual(sales['video-other'])
    expect(saved.constructor).toEqual(sales['video-other'])
  })

  test('saves the entire table and leaves unedited models intact', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(<VideoSalesFixture />)
    videoClients[0].setQueryData(['pricing'], { stale: true })
    const user = userEvent.setup()
    const main = screen.getByRole('table', {
      name: 'Sale tiers for video-main',
    })
    const price = within(main).getByRole('textbox', {
      name: 'Price per second (USD)',
    })
    fireEvent.change(price, { target: { value: '0.06' } })
    expect(within(main).getByText('720p × 5 seconds = $0.3')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { key: string; value: string }
    expect(request.key).toBe('billing_setting.video_sales')
    expect(JSON.parse(request.value)).toEqual({
      ...sales,
      'video-main': {
        ...sales['video-main'],
        resolutions: {
          '720p': {
            usd_per_second: 0.06,
            input_video_usd_per_second: 0,
            seconds: [5, 10, 15],
          },
        },
      },
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save video sales' })
      ).toBeDisabled()
    )
    expect(videoClients[0].getQueryState(['pricing'])?.isInvalidated).toBe(true)
  })

  test('creates paused models and previews each duration before saving', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(<VideoSalesFixture value='{}' />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Add video model' }))
    expect(
      screen.getByRole('switch', { name: 'Enable video sales' })
    ).not.toBeChecked()
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Public model name' }),
      { target: { value: 'new-video' } }
    )
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Price per second (USD)' }),
      { target: { value: '0.02' } }
    )
    expect(screen.getByText('720p × 15 seconds = $0.3')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { value: string }
    expect(JSON.parse(request.value)).toEqual({
      'new-video': {
        disabled: true,
        resolutions: {
          '720p': {
            usd_per_second: 0.02,
            input_video_usd_per_second: 0,
            seconds: [5, 10, 15],
          },
        },
      },
    })
  })

  test('keeps tier inputs, previews and saved prices aligned after adding and removing resolutions', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(<VideoSalesFixture value='{}' />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Add video model' }))
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Public model name' }),
      { target: { value: 'seedance-2.0' } }
    )

    const table = screen.getByRole('table', {
      name: 'Sale tiers for seedance-2.0',
    })
    const drafts = [
      { resolution: '480p', price: '1', seconds: '5', preview: '$5' },
      { resolution: '720p', price: '2', seconds: '10', preview: '$20' },
      { resolution: '1080p', price: '3', seconds: '15', preview: '$45' },
    ]
    for (const [index, draft] of drafts.entries()) {
      if (index > 0) {
        await user.click(screen.getByRole('button', { name: 'Add resolution' }))
      }
      const row = within(table).getAllByRole('row')[index + 1]
      fireEvent.change(
        within(row).getByRole('textbox', { name: 'Resolution' }),
        { target: { value: draft.resolution } }
      )
      fireEvent.change(
        within(row).getByRole('textbox', { name: 'Price per second (USD)' }),
        { target: { value: draft.price } }
      )
      fireEvent.change(
        within(row).getByRole('textbox', {
          name: 'Allowed durations (seconds)',
        }),
        { target: { value: draft.seconds } }
      )
      expect(
        within(row).getByText(
          `${draft.resolution} × ${draft.seconds} seconds = ${draft.preview}`
        )
      ).toBeVisible()
    }

    await user.click(
      screen.getByRole('button', { name: 'Remove resolution 720p' })
    )
    expect(
      within(table).getAllByRole('textbox', { name: 'Resolution' })
    ).toHaveLength(2)
    const remaining = within(table).getAllByRole('row')[2]
    expect(
      within(remaining).getByRole('textbox', { name: 'Resolution' })
    ).toHaveValue('1080p')
    expect(
      within(remaining).getByRole('textbox', {
        name: 'Price per second (USD)',
      })
    ).toHaveValue('3')
    expect(
      within(remaining).getByText('1080p × 15 seconds = $45')
    ).toBeVisible()
    expect(
      within(remaining).getByRole('button', {
        name: 'Remove resolution 1080p',
      })
    ).toBeEnabled()

    await user.click(
      screen.getByRole('button', { name: 'Remove resolution 480p' })
    )
    expect(
      within(table).getAllByRole('textbox', { name: 'Resolution' })
    ).toHaveLength(1)
    fireEvent.change(
      within(table).getByRole('textbox', { name: 'Resolution' }),
      { target: { value: '1440p' } }
    )
    fireEvent.change(
      within(table).getByRole('textbox', { name: 'Price per second (USD)' }),
      { target: { value: '4' } }
    )
    fireEvent.change(
      within(table).getByRole('textbox', {
        name: 'Allowed durations (seconds)',
      }),
      { target: { value: '8' } }
    )
    expect(within(table).getByText('1440p × 8 seconds = $32')).toBeVisible()
    expect(
      within(table).getByRole('button', {
        name: 'Remove resolution 1440p',
      })
    ).toBeEnabled()

    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { value: string }
    expect(JSON.parse(request.value)).toEqual({
      'seedance-2.0': {
        disabled: true,
        resolutions: {
          '1440p': {
            usd_per_second: 4,
            input_video_usd_per_second: 0,
            seconds: [8],
          },
        },
      },
    })
  })

  test('blocks zero prices and invalid seconds, then saves canonical 4k with valid values', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(
      <VideoSalesFixture
        value={JSON.stringify({ 'video-main': sales['video-main'] })}
      />
    )
    const user = userEvent.setup()
    const price = screen.getByRole('textbox', {
      name: 'Price per second (USD)',
    })
    const durations = screen.getByRole('textbox', {
      name: 'Allowed durations (seconds)',
    })
    fireEvent.change(price, { target: { value: '0' } })
    fireEvent.change(durations, { target: { value: '1.5' } })
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    expect(
      await screen.findByText('Price per second must be a positive number')
    ).toBeVisible()
    expect(durations).toHaveAttribute('aria-invalid', 'true')
    expect(put).not.toHaveBeenCalled()
    fireEvent.change(price, { target: { value: '0.1' } })
    fireEvent.change(durations, { target: { value: '5, 10' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'Resolution' }), {
      target: { value: '2160p' },
    })
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { value: string }
    expect(JSON.parse(request.value)['video-main'].resolutions).toEqual({
      '4k': {
        usd_per_second: 0.1,
        input_video_usd_per_second: 0,
        seconds: [5, 10],
      },
    })
  })

  test('keeps the model and unsaved edits when the server refuses deletion', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi.spyOn(api, 'put').mockResolvedValue({
      data: {
        success: false,
        message: 'This model is still listed by channel 12',
      },
    })
    render(<VideoSalesFixture />)
    const user = userEvent.setup()
    const other = screen.getByRole('table', {
      name: 'Sale tiers for video-other',
    })
    fireEvent.change(
      within(other).getByRole('textbox', { name: 'Price per second (USD)' }),
      { target: { value: '0.09' } }
    )
    await user.click(screen.getByRole('button', { name: 'Delete video-main' }))
    let dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'This model is still listed by channel 12'
    )
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(
      screen.getByRole('table', { name: 'Sale tiers for video-main' })
    ).toBeVisible()
    expect(
      within(other).getByRole('textbox', { name: 'Price per second (USD)' })
    ).toHaveValue('0.09')
    put.mockResolvedValue({ data: { success: true } })
    await user.click(screen.getByRole('button', { name: 'Delete video-main' }))
    dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(
      screen.queryByRole('table', { name: 'Sale tiers for video-main' })
    ).not.toBeInTheDocument()
    const request = put.mock.calls[1][1] as { value: string }
    expect(JSON.parse(request.value)).toEqual({
      'video-other': {
        disabled: false,
        resolutions: {
          '1080p': {
            usd_per_second: 0.09,
            input_video_usd_per_second: 0,
            seconds: [5, 10],
          },
        },
      },
    })
  })

  test('refuses malformed source data instead of replacing the table with an empty object', () => {
    const put = vi.spyOn(api, 'put')
    render(<VideoSalesFixture value='{"video-main":null}' />)
    expect(screen.getByText('Invalid video sales configuration')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Save video sales' })
    ).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  test('does not restore an older refetch after a newer draft is saved', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
    const { rerender } = render(<VideoSalesFixture />)
    const user = userEvent.setup()
    const main = screen.getByRole('table', {
      name: 'Sale tiers for video-main',
    })
    fireEvent.change(
      within(main).getByRole('textbox', { name: 'Price per second (USD)' }),
      { target: { value: '0.06' } }
    )
    const refreshed = {
      ...sales,
      'video-main': {
        ...sales['video-main'],
        resolutions: {
          '720p': {
            usd_per_second: 0.04,
            input_video_usd_per_second: 0,
            seconds: [5, 10, 15],
          },
        },
      },
    }
    rerender(<VideoSalesFixture value={JSON.stringify(refreshed)} />)
    expect(
      within(
        screen.getByRole('table', { name: 'Sale tiers for video-main' })
      ).getByRole('textbox', { name: 'Price per second (USD)' })
    ).toHaveValue('0.06')
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save video sales' })
      ).toBeDisabled()
    )
    expect(
      within(
        screen.getByRole('table', { name: 'Sale tiers for video-main' })
      ).getByRole('textbox', { name: 'Price per second (USD)' })
    ).toHaveValue('0.06')
  })

  test('loads a legacy tier as a free token price, blocks a cleared token price and saves its per-second equivalent', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(
      <VideoSalesFixture
        value={JSON.stringify({
          'video-main': {
            resolutions: { '720p': { usd_per_second: 0.56, seconds: [10] } },
          },
        })}
      />
    )
    const user = userEvent.setup()
    const tokenPrice = screen.getByRole('textbox', {
      name: 'Reference video price per 1M tokens (USD)',
    })
    expect(tokenPrice).toHaveValue('0')
    expect(screen.getByText('Input video: no extra charge')).toBeVisible()

    fireEvent.change(tokenPrice, { target: { value: '' } })
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    expect(
      await screen.findByText(
        'Input video price must be 0 or more; 0 means no extra charge'
      )
    ).toBeVisible()
    expect(tokenPrice).toHaveAttribute('aria-invalid', 'true')
    expect(put).not.toHaveBeenCalled()

    fireEvent.change(tokenPrice, { target: { value: '4' } })
    expect(screen.getByText('720p × 10 seconds = $5.6')).toBeVisible()
    expect(
      screen.getByText('Plus reference video: $4 / 1M tokens')
    ).toBeVisible()
    expect(screen.getByText('(21,600 tokens per second)')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { value: string }
    expect(JSON.parse(request.value)['video-main'].resolutions['720p']).toEqual(
      {
        usd_per_second: 0.56,
        input_video_usd_per_second: 0.0864,
        seconds: [10],
      }
    )
  })

  test('keeps the token price across token tiers and the per-second price across other tiers', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    render(
      <VideoSalesFixture
        value={JSON.stringify({
          'video-main': {
            resolutions: {
              '720p': {
                usd_per_second: 0.56,
                input_video_usd_per_second: 0.0864,
                seconds: [10],
              },
              '540p': {
                usd_per_second: 0.3,
                input_video_usd_per_second: 0.1,
                seconds: [5],
              },
            },
          },
        })}
      />
    )
    const user = userEvent.setup()
    const resolutions = screen.getAllByRole('textbox', { name: 'Resolution' })
    expect(
      screen.getByRole('textbox', {
        name: 'Reference video price per 1M tokens (USD)',
      })
    ).toHaveValue('4')
    expect(
      screen.getByRole('textbox', {
        name: 'Input video price per second (USD)',
      })
    ).toHaveValue('0.1')

    // Typing passes through "1080", which has no token rate.
    fireEvent.change(resolutions[0], { target: { value: '1080' } })
    fireEvent.change(resolutions[0], { target: { value: '1080p' } })
    // 540p → 720p converts the per-second price instead of dropping it.
    fireEvent.change(resolutions[1], { target: { value: '720p' } })
    const tokenPrices = screen.getAllByRole('textbox', {
      name: 'Reference video price per 1M tokens (USD)',
    })
    expect(tokenPrices[0]).toHaveValue('4')
    expect(tokenPrices[1]).toHaveValue('4.62962962963')

    await user.click(screen.getByRole('button', { name: 'Save video sales' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    const request = put.mock.calls[0][1] as { value: string }
    const saved = JSON.parse(request.value)['video-main'].resolutions
    expect(saved['1080p'].input_video_usd_per_second).toBe(0.1944)
    expect(saved['720p'].input_video_usd_per_second).toBe(0.1)
    expect(saved['720p']).not.toHaveProperty('inputTokenPrice')

    fireEvent.change(
      screen.getAllByRole('textbox', { name: 'Resolution' })[0],
      { target: { value: '540p' } }
    )
    expect(
      screen.getByRole('textbox', {
        name: 'Input video price per second (USD)',
      })
    ).toHaveValue('0.1944')
  })

  test('rejects case-folded names, duplicate 4k aliases, unsupported tiers, and duration bounds', () => {
    const schema = createVideoSalesFormSchema((key) => key)
    const values = videoSalesFormValues(sales)
    values.models[1].name = 'VIDEO-MAIN'
    expect(schema.safeParse(values).success).toBe(false)
    values.models[1].name = 'video-other'
    values.models[0].tiers = [
      {
        resolution: '2160p',
        price: '0.1',
        inputPrice: '0',
        inputTokenPrice: '0',
        seconds: '5',
      },
      {
        resolution: '4k',
        price: '0.2',
        inputPrice: '0',
        inputTokenPrice: '0',
        seconds: '5',
      },
    ]
    expect(schema.safeParse(values).success).toBe(false)
    values.models[0].tiers = [
      {
        resolution: '720p',
        price: '0.1',
        inputPrice: '0',
        inputTokenPrice: '0',
        seconds: '5',
      },
    ]
    for (const duration of ['', '0', '-1', '1.5', '3601', '5, nope']) {
      values.models[0].tiers[0].seconds = duration
      expect(schema.safeParse(values).success, duration).toBe(false)
    }
    values.models[0].tiers[0].seconds = '3600'
    for (const resolution of ['', '*', '0p', '0720p', '1920x1080']) {
      values.models[0].tiers[0].resolution = resolution
      expect(schema.safeParse(values).success, resolution).toBe(false)
    }
    values.models[0].tiers[0].resolution = '720p'
    values.models[1].name = 'K-video'
    values.models[0].name = 'K-video'
    expect(schema.safeParse(values).success).toBe(true)
  })
})
