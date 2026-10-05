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
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { channelSchema, type Channel } from '../../types'
import { useChannelsColumns } from '../channels-columns'
import { ChannelsProvider } from '../channels-provider'
import { NumericSpinnerInput } from '../numeric-spinner-input'

test.each([
  { name: 'a decimal', value: '1.5' },
  { name: 'a negative weight', value: '-1' },
  { name: 'an unsafe integer', value: '9007199254740992' },
  { name: 'a non-finite integer', value: '9'.repeat(400) },
  { name: 'an empty value', value: '' },
])(
  'editing $name preserves the draft and prevents updates',
  async ({ value }) => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<NumericSpinnerInput value={2} onChange={onChange} />)

    await user.click(screen.getByRole('button', { name: '2' }))
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value } })
    await user.keyboard('{Enter}')

    expect(screen.getByRole('textbox')).toHaveValue(value)
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('alert')).toBeVisible()
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Increment' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Decrement' })).toBeDisabled()
  }
)

test('typing a decimal does not silently turn it into a different integer', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<NumericSpinnerInput value={2} onChange={onChange} />)

  await user.click(screen.getByRole('button', { name: '2' }))
  const input = screen.getByRole('textbox')
  await user.clear(input)
  await user.type(input, '1.5')
  await user.keyboard('{Enter}')

  expect(input).toHaveValue('1.5')
  expect(onChange).not.toHaveBeenCalled()
})

test('correcting an invalid weight to zero commits once and clears the error', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const onCommit = vi.fn()
  render(
    <NumericSpinnerInput value={2} onChange={onChange} onCommit={onCommit} />
  )

  await user.click(screen.getByRole('button', { name: '2' }))
  const input = screen.getByRole('textbox')
  fireEvent.change(input, { target: { value: '-1' } })
  await user.keyboard('{Enter}')
  await user.click(input)
  fireEvent.change(input, { target: { value: '0' } })
  onCommit.mockClear()
  await user.keyboard('{Enter}')

  expect(onChange).toHaveBeenCalledExactlyOnceWith(0)
  expect(onCommit).toHaveBeenCalledOnce()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

test('priority allows negative integers below the former arbitrary minimum', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(
    <NumericSpinnerInput
      value={0}
      min={Number.MIN_SAFE_INTEGER}
      onChange={onChange}
    />
  )

  await user.click(screen.getByRole('button', { name: '0' }))
  fireEvent.change(screen.getByRole('textbox'), { target: { value: '-1000' } })
  await user.keyboard('{Enter}')

  expect(onChange).toHaveBeenCalledExactlyOnceWith(-1000)
})

test.each([
  { value: Number.MAX_SAFE_INTEGER, button: 'Increment' },
  { value: Number.MIN_SAFE_INTEGER, button: 'Decrement' },
])(
  'stepping beyond the safe integer boundary $value is disabled',
  ({ value, button }) => {
    const onChange = vi.fn()
    render(
      <NumericSpinnerInput
        value={value}
        min={Number.MIN_SAFE_INTEGER}
        onChange={onChange}
      />
    )

    expect(screen.getByRole('button', { name: button })).toBeDisabled()
    expect(onChange).not.toHaveBeenCalled()
  }
)

test('Escape discards an invalid draft without changing the channel', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<NumericSpinnerInput value={2} onChange={onChange} />)

  await user.click(screen.getByRole('button', { name: '2' }))
  const input = screen.getByRole('textbox')
  fireEvent.change(input, { target: { value: '-1' } })
  await user.keyboard('{Enter}')
  await user.click(input)
  await user.keyboard('{Escape}')

  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: '2' })).toBeVisible()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(onChange).not.toHaveBeenCalled()
})

function ChannelRoutingCell(props: { channel: Channel; field: string }) {
  const table = useReactTable({
    data: [props.channel],
    columns: useChannelsColumns(),
    getCoreRowModel: getCoreRowModel(),
  })
  const cell = table
    .getRowModel()
    .rows[0].getAllCells()
    .find((candidate) => candidate.column.id === props.field)
  return cell ? flexRender(cell.column.columnDef.cell, cell.getContext()) : null
}

test.each([
  {
    tag: false,
    field: 'priority',
    invalid: '9007199254740992',
    valid: '-1000',
  },
  { tag: true, field: 'priority', invalid: '1.5', valid: '-1000' },
  { tag: false, field: 'weight', invalid: '-1', valid: '0' },
  { tag: true, field: 'weight', invalid: '-1', valid: '0' },
])(
  'channel table validates $field before submitting (tag: $tag)',
  async (scenario) => {
    const user = userEvent.setup()
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    const client = new QueryClient()
    const channel = channelSchema.parse({
      id: 9,
      name: 'Routing channel',
      type: 1,
      key: '',
      status: 1,
      created_time: 1,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      priority: 2,
      weight: 2,
      tag: 'routing',
    })
    const row = scenario.tag ? { ...channel, children: [channel] } : channel
    const view = render(
      <QueryClientProvider client={client}>
        <ChannelsProvider>
          <ChannelRoutingCell channel={row} field={scenario.field} />
        </ChannelsProvider>
      </QueryClientProvider>
    )

    await user.click(screen.getByRole('button', { name: '2' }))
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: scenario.invalid } })
    await user.keyboard('{Enter}')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()

    await user.click(input)
    fireEvent.change(input, { target: { value: scenario.valid } })
    await user.keyboard('{Enter}')
    if (scenario.tag) {
      const dialog = await screen.findByRole('alertdialog')
      expect(put).not.toHaveBeenCalled()
      await user.click(within(dialog).getByRole('button', { name: 'Update' }))
    }
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    expect(put.mock.calls[0][1]).toMatchObject({
      [scenario.field]: Number(scenario.valid),
    })
    view.unmount()
    client.clear()
  }
)
