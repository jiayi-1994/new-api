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
import i18next from 'i18next'
import { afterEach, beforeAll, describe, expect, test } from 'vitest'

import type { UsageLog } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { DetailsDialog } from '../dialogs/details-dialog'

const i18nKeys = {
  'Log Details': 'Log Details',
  Consume: 'Consume',
  'Billing Details': 'Billing Details',
  'Billing Mode': 'Billing Mode',
  'Per-token': 'Per-token',
  'Dynamic Pricing': 'Dynamic Pricing',
  'Matched Tier': 'Matched Tier',
  'Group Ratio': 'Group Ratio',
  'Total Cost': 'Total Cost',
  'Usage parameters': 'Usage parameters',
}

function makeLog(other: LogOtherData): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    username: 'user',
    token_name: 'token',
    model_name: 'wan2.5-i2v-preview',
    quota: 5000,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: false,
    channel: 1,
    channel_name: '',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-1',
    upstream_request_id: '',
  }
}

function renderDetails(other: LogOtherData, promptTokens = 0): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(['status'], {}, { updatedAt: freshAt })
  queryClient.setQueryData(
    ['pricing'],
    { data: [], vendors: [] },
    { updatedAt: freshAt }
  )

  render(
    <QueryClientProvider client={queryClient}>
      <DetailsDialog
        log={{ ...makeLog(other), prompt_tokens: promptTokens }}
        isAdmin={false}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
  return queryClient
}

function rowValue(label: string): string | null {
  return screen.getByText(label).nextElementSibling?.textContent ?? null
}

test('shows the recorded request and response models in log details', () => {
  const queryClient = renderDetails({
    response_model: {
      requested_model: 'requested-model',
      upstream_model: 'mapped-model',
      returned_model: 'unexpected-model',
    },
  })
  expect(screen.getByText('Response model: unexpected-model')).toBeVisible()
  expect(rowValue('Request Model')).toBe('requested-model')
  expect(rowValue('Upstream Model')).toBe('mapped-model')
  expect(screen.getByText('unexpected-model')).toBeVisible()
  queryClient.clear()
})

describe('usage facts billing details', () => {
  test('shows the settled image count and a per-image price', () => {
    const queryClient = renderDetails({
      billing_mode: 'tiered_expr',
      expr_b64: btoa('tier("image", fixed(0.04)) * image_count'),
      billing_unit: 'request',
      fixed_price: 0.04,
      image_count: 2,
      matched_tier: 'image',
    })
    expect(
      screen.getByText('Billable image count').parentElement
    ).toHaveTextContent('2')
    expect(screen.getAllByText(/\/image/).length).toBeGreaterThan(0)
    queryClient.clear()
  })
  const queryClients: QueryClient[] = []

  test('shows actual billable image and cache tokens while retaining the aggregate cache count', () => {
    queryClients.push(
      renderDetails(
        {
          billing_mode: 'tiered_expr',
          expr_b64: btoa(
            'tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)'
          ),
          matched_tier: 'standard',
          cache_tokens: 300,
          image_cache_tokens: 200,
          billing_tokens: { p: 300, cr: 100, img: 400, img_cr: 200, c: 100 },
        },
        1000
      )
    )
    const billable = within(
      screen.getByRole('group', { name: 'Billable token breakdown' })
    )
    expect(
      billable.getByText('Image Cache').nextElementSibling
    ).toHaveTextContent('200')
    expect(
      billable.getByText('Cache Read').nextElementSibling
    ).toHaveTextContent('100')
    expect(billable.getByText('Image In').nextElementSibling).toHaveTextContent(
      '400'
    )
    expect(
      screen.getByText('Input Tokens').nextElementSibling
    ).toHaveTextContent('1,000')
    expect(
      screen
        .getAllByText('Cache Read')
        .some((label) => label.nextElementSibling?.textContent === '300')
    ).toBe(true)
  })

  beforeAll(() => {
    i18next.addResourceBundle('en', 'translation', i18nKeys)
  })

  afterEach(() => {
    for (const queryClient of queryClients) {
      queryClient.clear()
    }
    queryClients.length = 0
  })

  test('renders one raw-key row per usage fact before total cost', () => {
    const expression = 'tier("720P", u("seconds") * 5)'
    queryClients.push(
      renderDetails({
        group_ratio: 1,
        billing_mode: 'tiered_expr',
        expr_b64: Buffer.from(expression, 'utf8').toString('base64'),
        matched_tier: '720P',
        usage_facts: {
          resolution: '720P',
          seconds: 5,
        },
      })
    )

    expect(screen.getByText('Usage parameters')).toBeInTheDocument()
    expect(rowValue('resolution')).toBe('720P')
    expect(rowValue('seconds')).toBe('5')
    expect(rowValue('Billing Mode')).toBe('Dynamic Pricing')
    expect(rowValue('Matched Tier')).toBe('720P')

    const usageHeader = screen.getByText('Usage parameters')
    const totalCost = screen.getByText('Total Cost')
    expect(
      usageHeader.compareDocumentPosition(totalCost) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
  })

  test('shows reference video tokens, token price and cost for a unified video log', () => {
    queryClients.push(
      renderDetails({
        group_ratio: 2,
        billing_mode: 'tiered_expr',
        expr_b64: btoa(
          'tier("720p", u("seconds") * 0.56 + u("input_video_seconds") * 0.0864)'
        ),
        matched_tier: '720p',
        usage_facts: {
          seconds: 10,
          resolution: '720p',
          input_video_seconds: 8.25,
        },
      })
    )

    expect(rowValue('Reference video tokens')).toBe('178,200')
    expect(rowValue('Reference video price')).toBe('$4 / 1M tokens')
    expect(rowValue('Reference video cost')).toBe('$0.7128')
  })

  test('prices the billable reference seconds of an added-fee expression', () => {
    queryClients.push(
      renderDetails({
        billing_mode: 'tiered_expr',
        expr_b64: btoa(
          'tier("720p", u("seconds") * 0.56 + u("input_video_billable_seconds") * 0.0864)'
        ),
        matched_tier: '720p',
        usage_facts: {
          seconds: 10,
          resolution: '720p',
          input_video_seconds: 8.25,
          input_video_billable_seconds: 8.25,
        },
      })
    )

    expect(rowValue('Reference video tokens')).toBe('178,200')
    expect(rowValue('Reference video price')).toBe('$4 / 1M tokens')
    expect(rowValue('Reference video cost')).toBe('$0.7128')
    expect(screen.queryByText('With-reference order')).toBeNull()
  })

  test.each([
    {
      measured: 2,
      billed: 10,
      details:
        'Output 15 s + reference billed 10 s (measured 2 s) = 540,000 tokens × $28 / 1M tokens',
      minimum:
        'Reference video is shorter than 2/3 of the output; billed as 10 seconds',
    },
    {
      measured: 15,
      billed: 15,
      details:
        'Output 15 s + reference billed 15 s (measured 15 s) = 648,000 tokens × $28 / 1M tokens',
      minimum: null,
    },
  ])(
    'bills the whole order at the with-reference price for $measured s of reference',
    (row) => {
      queryClients.push(
        renderDetails({
          billing_mode: 'tiered_expr',
          expr_b64: btoa(
            'tier("720p", (u("seconds") + u("input_video_billable_seconds")) * 0.6048)'
          ),
          matched_tier: '720p',
          usage_facts: {
            seconds: 15,
            resolution: '720p',
            input_video_seconds: row.measured,
            input_video_billable_seconds: row.billed,
          },
        })
      )

      // The generic tier parser does not read this shape; only the tier shows.
      expect(rowValue('Matched Tier')).toBe('720p')
      expect(rowValue('With-reference order')).toBe(row.details)
      if (row.minimum) {
        expect(rowValue('Reference video minimum')).toBe(row.minimum)
      } else {
        expect(screen.queryByText('Reference video minimum')).toBeNull()
      }
      expect(screen.queryByText('Reference video cost')).toBeNull()
    }
  )

  test('shows only the reference video tokens when the expression has no input price', () => {
    queryClients.push(
      renderDetails({
        billing_mode: 'tiered_expr',
        expr_b64: btoa('tier("720p", u("seconds") * 0.56)'),
        matched_tier: '720p',
        usage_facts: {
          seconds: 10,
          resolution: '720p',
          input_video_seconds: 8.25,
        },
      })
    )

    expect(rowValue('Reference video tokens')).toBe('178,200')
    expect(screen.queryByText('Reference video price')).toBeNull()
    expect(screen.queryByText('Reference video cost')).toBeNull()
  })

  test.each([
    [
      'a tier without a token rate',
      { resolution: '540p', input_video_seconds: 8 },
    ],
    ['no reference video', { resolution: '720p' }],
  ])('hides reference video rows for %s', (_, facts) => {
    queryClients.push(
      renderDetails({
        billing_mode: 'tiered_expr',
        expr_b64: btoa(
          'tier("720p", u("seconds") * 0.56 + u("input_video_seconds") * 0.0864)'
        ),
        matched_tier: facts.resolution,
        usage_facts: { seconds: 10, ...facts },
      })
    )

    expect(screen.queryByText('Reference video tokens')).toBeNull()
    expect(screen.queryByText('Reference video price')).toBeNull()
  })

  test('does not render usage parameter rows when usage_facts is absent', () => {
    queryClients.push(
      renderDetails({
        group_ratio: 1,
      })
    )

    expect(screen.queryByText('Usage parameters')).toBeNull()
    expect(screen.queryByText('resolution')).toBeNull()
    expect(screen.queryByText('seconds')).toBeNull()
    expect(screen.getByText('Total Cost')).toBeInTheDocument()
  })

  test('does not render usage parameter rows when usage_facts is empty', () => {
    queryClients.push(
      renderDetails({
        group_ratio: 1,
        usage_facts: {},
      })
    )

    expect(screen.queryByText('Usage parameters')).toBeNull()
    expect(screen.queryByText('resolution')).toBeNull()
    expect(screen.queryByText('seconds')).toBeNull()
    expect(screen.getByText('Total Cost')).toBeInTheDocument()
  })
})
