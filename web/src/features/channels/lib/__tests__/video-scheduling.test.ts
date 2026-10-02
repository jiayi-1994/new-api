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

import { channelSchema, type VideoSchedulingConfig } from '../../types'
import { getChannelConfigurationSection } from '../channel-configuration'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
} from '../channel-form'
import type { VideoSchedulingDraft } from '../video-scheduling'

const storedConfig: VideoSchedulingConfig = {
  quality: 0.8,
  capacity: 4,
  capacity_group: 'account-a',
  models: {
    'videos-mini': {
      mode: 'per_second',
      prices: { '720p': 0.05, '*': 0 },
      max_seconds: 12,
      allowed_seconds: [5, 10],
      allowed_seconds_by_resolution: { '720p': [5, 10, 15], '1080p': [5, 10] },
      references: {
        video: { '720p': { mode: 'per_input', value: 0 } },
        image: { '*': { mode: 'included' } },
      },
    },
  },
}

function channelWithSettings(settings: Record<string, unknown>) {
  return channelSchema.parse({
    id: 7,
    name: 'Video channel',
    type: 1,
    key: '',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    models: 'videos-mini',
    settings: JSON.stringify(settings),
  })
}

const storedForm = () =>
  transformChannelToFormDefaults(
    channelWithSettings({ video_scheduling: storedConfig, other_key: true })
  )

function draftOf(form: ReturnType<typeof storedForm>): VideoSchedulingDraft {
  if (!form.video_scheduling) throw new Error('the form has no draft')
  return form.video_scheduling
}

const storedDraft = () => draftOf(storedForm())

function savedSettings(draft: VideoSchedulingDraft) {
  const payload = transformFormDataToUpdatePayload(
    { ...storedForm(), video_scheduling: draft },
    7
  )
  return JSON.parse(payload.settings ?? '{}')
}

describe('video scheduling channel settings', () => {
  test('an untouched stored config saves back unchanged, keeping explicit zeros and unknown settings', () => {
    const validated = channelFormSchema.parse(storedForm())
    const settings = savedSettings(draftOf(validated))

    expect(settings.video_scheduling).toEqual(storedConfig)
    expect(settings.other_key).toBe(true)
  })

  test('included and unsupported rules drop a stale value while charging rules keep an explicit 0', () => {
    const draft = storedDraft()
    const references = [
      {
        kind: 'video' as const,
        tier: '720P ',
        mode: 'included' as const,
        value: '0.3',
      },
      {
        kind: 'image' as const,
        tier: '*',
        mode: 'per_request' as const,
        value: '0',
      },
      { kind: 'audio' as const, tier: '*', mode: '' as const, value: '2' },
    ]

    const settings = savedSettings({
      ...draft,
      models: [{ ...draft.models[0], references }],
    })

    expect(settings.video_scheduling.models['videos-mini'].references).toEqual({
      video: { '720p': { mode: 'included' } },
      image: { '*': { mode: 'per_request', value: 0 } },
    })
  })

  test('turning scheduling off removes the stored config', () => {
    const draft = storedDraft()

    expect(savedSettings({ ...draft, enabled: false })).not.toHaveProperty(
      'video_scheduling'
    )
  })

  test('a stored config without quality saves as the backend default 0, while a new draft must enter one', () => {
    const { quality: _omitted, ...withoutQuality } = storedConfig
    const form = transformChannelToFormDefaults(
      channelWithSettings({ video_scheduling: withoutQuality })
    )
    const valid = (draft: VideoSchedulingDraft) =>
      channelFormSchema.safeParse({
        ...form,
        key: 'k',
        video_scheduling: draft,
      }).success

    expect(valid(draftOf(form))).toBe(true)
    expect(transformFormDataToUpdatePayload(form, 7).settings).toContain(
      '"quality":0'
    )
    expect(valid({ ...draftOf(form), quality: '' })).toBe(false)
  })

  test('an empty charging value is a validation error instead of becoming 0', () => {
    const draft = storedDraft()
    const invalid = {
      ...draft,
      models: [
        {
          ...draft.models[0],
          prices: [
            { tier: '720p', price: '0.05' },
            { tier: '720P', price: '0.06' },
            { tier: '1280x720', price: '0.07' },
          ],
          references: [
            {
              kind: 'video' as const,
              tier: '*',
              mode: 'per_input' as const,
              value: '',
            },
            {
              kind: 'image' as const,
              tier: '*',
              mode: 'multiplier' as const,
              value: '0.5',
            },
          ],
        },
      ],
    }

    const result = channelFormSchema.safeParse({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'x',
      models: 'videos-mini',
      key: 'k',
      video_scheduling: invalid,
    })

    expect(result.success).toBe(false)
    const issues = (result.error?.issues ?? []).map((issue) =>
      issue.path.join('.')
    )
    expect(issues).toEqual([
      'video_scheduling.models.0.prices.1.tier',
      'video_scheduling.models.0.prices.2.tier',
      'video_scheduling.models.0.references.0.value',
      'video_scheduling.models.0.references.1.value',
    ])
    expect(getChannelConfigurationSection(issues[0].split('.')[0])).toBe(
      'routing'
    )
  })
})
