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

  // Ark calculator: 15 s output + 5 s reference bills max(5, 10) = 10 s, so
  // 324,000 + 216,000 = 540,000 tokens × $42 / 1M = $22.68.
  test('shows output and reference tokens, prices and cost from a token-priced video log', () => {
    queryClients.push(
      renderDetails({
        group_ratio: 2,
        billing_mode: 'tiered_expr',
        expr_b64: btoa(
          'tier("720p", (u("video_tokens") + u("input_video_tokens")) * 42 / 1000000)'
        ),
        matched_tier: '720p',
        usage_facts: {
          seconds: 15,
          resolution: '720p',
          video_tokens: 324_000,
          output_width: 1280,
          output_height: 720,
          fps: 24,
          input_video_seconds: 5,
          input_video_billable_seconds: 10,
          input_video_tokens: 216_000,
        },
        video_sales: {
          unit: 'token',
          usd_per_million_tokens: 46.3,
          with_video_usd_per_million_tokens: 42,
          width: 1280,
          height: 720,
          fps: 24,
          official_reference_billing: true,
        },
      })
    )

    expect(rowValue('Output size')).toBe('1280×720 · 24 fps')
    expect(rowValue('Output tokens')).toBe('324,000')
    expect(rowValue('No-reference price')).toBe('$46.3 / 1M tokens')
    expect(rowValue('Reference video tokens')).toBe('216,000')
    expect(rowValue('With-reference order price')).toBe('$42 / 1M tokens')
    expect(rowValue('Reference video minimum')).toBe(
      'Reference video is shorter than 2/3 of the output; billed as 10 seconds'
    )
    expect(rowValue('Video cost before group ratio')).toBe('$22.68')
  })

  test('prices output tokens alone when a token-priced log has no reference video', () => {
    queryClients.push(
      renderDetails({
        billing_mode: 'tiered_expr',
        expr_b64: btoa('tier("720p", u("video_tokens") * 46.3 / 1000000)'),
        matched_tier: '720p',
        usage_facts: {
          seconds: 5,
          resolution: '720p',
          video_tokens: 108_000,
          output_width: 1280,
          output_height: 720,
          fps: 24,
        },
        video_sales: {
          unit: 'token',
          usd_per_million_tokens: 46.3,
          with_video_usd_per_million_tokens: 0,
          width: 1280,
          height: 720,
          fps: 24,
        },
      })
    )

    expect(rowValue('Output tokens')).toBe('108,000')
    expect(rowValue('Video cost before group ratio')).toBe('$5.0004')
    expect(screen.queryByText('Reference video tokens')).toBeNull()
    expect(screen.queryByText('Reference video minimum')).toBeNull()
  })

  test('shows only the seconds facts for a per-second or older video log', () => {
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
          input_video_seconds: 2,
          input_video_billable_seconds: 10,
        },
      })
    )

    expect(rowValue('Matched Tier')).toBe('720p')
    expect(rowValue('input_video_billable_seconds')).toBe('10')
    expect(screen.queryByText('Output tokens')).toBeNull()
    expect(screen.queryByText('Reference video tokens')).toBeNull()
    expect(screen.queryByText('With-reference order')).toBeNull()
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
