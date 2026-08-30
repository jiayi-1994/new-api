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
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, test } from 'node:test'

// @ts-expect-error Bun exposes module mocks at runtime without installed types.
const { mock, spyOn } = await import('bun:test')
const { api } = await import('@/lib/api')

const putCalls: Array<{
  url: string
  body: unknown
  config: unknown
}> = []
spyOn(api, 'put').mockImplementation((async (
  url: string,
  body: unknown,
  config: unknown
) => {
  putCalls.push({ url, body, config })
  return {
    data: {
      success: true,
      committed: true,
      publication_recovered: false,
      publication_pending: false,
      data: { ModelPrice: '{"video":0.3}' },
    },
  }
}) as typeof api.put)

const { updatePricingCommand, updateSystemOption } = await import('../../api')

after(() => {
  mock.restore()
})

describe('pricing command API', () => {
  beforeEach(() => {
    putCalls.length = 0
  })

  afterEach(() => {
    mock.clearAllMocks()
  })

  test('sends one replace_documents request with exact raw expectations', async () => {
    const command = {
      kind: 'replace_documents' as const,
      target_name: '',
      values: { ModelPrice: '{"video":0.3}' },
      expected_documents: { ModelPrice: '{ "video": 0.2 }' },
    }

    const response = await updatePricingCommand(command)

    assert.deepEqual(putCalls, [
      {
        url: '/api/option/pricing',
        body: command,
        config: { skipErrorHandler: true },
      },
    ])
    assert.equal(response.committed, true)
  })

  test('passes exact expected_value through the ordinary option endpoint', async () => {
    const request = {
      key: 'ModelPrice',
      value: '{"video":0.3}',
      expected_value: '{ "video": 0.2 }',
    }

    await updateSystemOption(request)

    assert.deepEqual(putCalls, [
      {
        url: '/api/option/',
        body: request,
        config: { skipErrorHandler: true },
      },
    ])
  })
})
