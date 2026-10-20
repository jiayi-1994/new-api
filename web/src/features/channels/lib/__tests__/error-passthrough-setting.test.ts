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
import { describe, expect, test } from 'vitest'

import { CHANNEL_TYPE_NEW_API } from '../../constants'
import { channelSchema } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  buildSettingJSON,
  channelFormSchema,
  transformChannelToFormDefaults,
} from '../channel-form'

function gatewayChannel(setting: Record<string, unknown>) {
  return channelSchema.parse({
    id: 7,
    name: 'Gateway',
    type: CHANNEL_TYPE_NEW_API,
    key: '',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'seedance',
    group: 'default',
    base_url: 'https://gateway.example',
    setting: JSON.stringify(setting),
  })
}

describe('error passthrough channel setting', () => {
  test('a stored true flag loads as enabled and a missing flag as disabled', () => {
    expect(
      transformChannelToFormDefaults(
        gatewayChannel({ error_passthrough_enabled: true })
      ).error_passthrough_enabled
    ).toBe(true)
    expect(
      transformChannelToFormDefaults(gatewayChannel({}))
        .error_passthrough_enabled
    ).toBe(false)
  })

  test('an enabled switch is written to the setting JSON and a disabled one stays false', () => {
    const base = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Gateway',
      type: CHANNEL_TYPE_NEW_API,
      base_url: 'https://gateway.example',
      key: 'test-key',
      models: 'seedance',
    }
    expect(
      JSON.parse(
        buildSettingJSON(
          channelFormSchema.parse({ ...base, error_passthrough_enabled: true })
        )
      ).error_passthrough_enabled
    ).toBe(true)
    expect(
      JSON.parse(buildSettingJSON(channelFormSchema.parse(base)))
        .error_passthrough_enabled
    ).toBe(false)
  })
})
