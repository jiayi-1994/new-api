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
import { afterEach, beforeEach, describe, expect, test } from 'vitest'

import { useSystemConfigStore } from '@/stores/system-config-store'

import { LOG_TYPE_ENUM } from '../../constants'
import { getLogProfitUSD } from '../format'

describe('log profit', () => {
  beforeEach(() => {
    useSystemConfigStore.getState().setConfig({
      currency: {
        displayInCurrency: true,
        quotaDisplayType: 'USD',
        quotaPerUnit: 500000,
        usdExchangeRate: 1,
        customCurrencySymbol: '¤',
        customCurrencyExchangeRate: 1,
      },
    })
  })

  afterEach(() => {
    useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  })

  test('consume row with a booked cost yields revenue minus cost', () => {
    const profit = getLogProfitUSD(
      { type: LOG_TYPE_ENUM.CONSUME, quota: 3_175_000 },
      { admin_info: { cost_usd: 4.5 } }
    )
    expect(profit).toBeCloseTo(1.85)
  })

  test('failed task nets zero across its consume and refund rows', () => {
    const other = (cost: number) => ({ admin_info: { cost_usd: cost } })
    const consume = getLogProfitUSD(
      { type: LOG_TYPE_ENUM.CONSUME, quota: 3_175_000 },
      other(4.5)
    )
    const refund = getLogProfitUSD(
      { type: LOG_TYPE_ENUM.REFUND, quota: 3_175_000 },
      other(-4.5)
    )
    expect((consume ?? Number.NaN) + (refund ?? Number.NaN)).toBeCloseTo(0)
  })

  test('partial-refund row with zero cost returns the refunded revenue', () => {
    const profit = getLogProfitUSD(
      { type: LOG_TYPE_ENUM.REFUND, quota: 250_000 },
      { admin_info: { cost_usd: 0 } }
    )
    expect(profit).toBeCloseTo(-0.5)
  })

  test.each([
    { name: 'no other data', other: null },
    { name: 'no admin info', other: {} },
    { name: 'no booked cost', other: { admin_info: {} } },
  ])('row with $name has no profit', ({ other }) => {
    expect(
      getLogProfitUSD({ type: LOG_TYPE_ENUM.CONSUME, quota: 500000 }, other)
    ).toBeNull()
  })
})
