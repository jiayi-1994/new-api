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
import { zodResolver } from '@hookform/resolvers/zod'
import { Link } from '@tanstack/react-router'
import { useEffect, useMemo, useRef } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { JsonEditor } from '@/components/json-editor'
import { TagInput } from '@/components/tag-input'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { formatPercent } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import type { OperationsSettings, VideoSchedulingSetting } from '../types'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { VideoScheduleSimulatorDialog } from './video-schedule-simulator-dialog'

const PREFIX = 'video_scheduling_setting.'
const MODE_KEY = `${PREFIX}mode`

function parseCapacityGroups(value: string): Record<string, number> | null {
  if (!value.trim()) return {}
  try {
    const parsed: unknown = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    const entries = Object.entries(parsed as Record<string, unknown>)
    const valid = entries.every(
      ([name, quota]) =>
        name !== '' &&
        name === name.trim() &&
        new TextEncoder().encode(name).length <= 64 &&
        Number.isInteger(quota) &&
        (quota as number) > 0
    )
    return valid ? (parsed as Record<string, number>) : null
  } catch {
    return null
  }
}

const rate = z
  .number()
  .min(0, 'Enter a value between 0 and 1')
  .max(1, 'Enter a value between 0 and 1')
const nonNegative = z.number().min(0, 'Enter a non-negative number')
const count = z
  .number()
  .int('Enter a non-negative whole number')
  .min(0, 'Enter a non-negative whole number')
const positive = z
  .number()
  .int('Enter a positive whole number')
  .min(1, 'Enter a positive whole number')

const videoSchedulingSchema = z.object({
  mode: z.enum(['off', 'shadow', 'on']),
  audit_enabled: z.boolean(),
  audit_retention_days: z
    .number()
    .int()
    .min(7, 'Audit retention must be between 7 and 180 days')
    .max(180, 'Audit retention must be between 7 and 180 days'),
  models: z.array(z.string()),
  price_weight: nonNegative,
  quality_weight: nonNegative,
  service_weight: nonNegative,
  min_submit_rate: rate,
  min_gen_rate: rate,
  min_samples: count,
  window_seconds: positive,
  explore_share: rate,
  explore_max_in_flight: count,
  probe_ratio: rate,
  probe_cooldown_sec: positive,
  probe_max_in_flight: count,
  unknown_sell_policy: z.enum(['exclude', 'relative']),
  max_cost_to_sell_ratio: nonNegative,
  tie_epsilon: nonNegative,
  capacity_groups: z
    .string()
    .refine(
      (value) => parseCapacityGroups(value) !== null,
      'Each capacity group needs a trimmed name of at most 64 bytes and a whole-number quota above 0'
    ),
})

type VideoSchedulingFormValues = z.infer<typeof videoSchedulingSchema>
type SettingField = keyof VideoSchedulingSetting

/** The form values as the backend setting object. */
function toVideoSchedulingSetting(
  values: VideoSchedulingFormValues
): VideoSchedulingSetting {
  return {
    ...values,
    capacity_groups: parseCapacityGroups(values.capacity_groups) ?? {},
  }
}

/** Option values keyed by option name, as PUT /api/option/ stores them. */
function toOptionValues(setting: VideoSchedulingSetting) {
  return Object.fromEntries(
    Object.entries(setting).map(([field, value]) => [
      PREFIX + field,
      typeof value === 'object' ? JSON.stringify(value) : String(value),
    ])
  ) as Record<string, string>
}

function sameOptionValues(
  a: VideoSchedulingFormValues,
  b: VideoSchedulingFormValues
): boolean {
  const left = toOptionValues(toVideoSchedulingSetting(a))
  const right = toOptionValues(toVideoSchedulingSetting(b))
  return Object.keys(left).every((key) => left[key] === right[key])
}

function toFormValues(settings: OperationsSettings): VideoSchedulingFormValues {
  const values = Object.fromEntries(
    Object.keys(videoSchedulingSchema.shape).map((field) => [
      field,
      settings[`${PREFIX}${field as SettingField}`],
    ])
  ) as VideoSchedulingFormValues
  const groups = parseCapacityGroups(values.capacity_groups) ?? {}
  return {
    ...values,
    audit_enabled: values.audit_enabled ?? true,
    audit_retention_days: values.audit_retention_days ?? 30,
    capacity_groups: Object.keys(groups).length
      ? JSON.stringify(groups, null, 2)
      : '',
  }
}

type NumberFieldName = {
  [K in keyof VideoSchedulingFormValues]: VideoSchedulingFormValues[K] extends number
    ? K
    : never
}[keyof VideoSchedulingFormValues]

export function VideoSchedulingSettingsSection(props: {
  settings: OperationsSettings
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const formDefaults = useMemo(
    () => toFormValues(props.settings),
    [props.settings]
  )
  const form = useForm<VideoSchedulingFormValues>({
    resolver: zodResolver(videoSchedulingSchema),
    defaultValues: formDefaults,
  })
  // Server values replace the form only while it holds no unsaved edits: each
  // saved key refetches the options, and a reset between keys would discard
  // the rest of the submission, mode included.
  const pristineRef = useRef(formDefaults)
  useEffect(() => {
    if (!sameOptionValues(form.getValues(), pristineRef.current)) return
    pristineRef.current = formDefaults
    form.reset(formDefaults)
  }, [formDefaults, form])
  const liveSetting = useMemo(
    () => toVideoSchedulingSetting(formDefaults),
    [formDefaults]
  )
  // Saved option values; a key moves here only after the server accepts it.
  const baselineRef = useRef(toOptionValues(liveSetting))
  useEffect(() => {
    baselineRef.current = toOptionValues(liveSetting)
  }, [liveSetting])

  const weights = useWatch({
    control: form.control,
    name: ['price_weight', 'quality_weight', 'service_weight'],
  })
  const weightSum = weights.reduce(
    (sum, weight) => sum + (Number(weight) || 0),
    0
  )
  const normalizedWeight = (weight: number) =>
    weightSum > 0
      ? formatPercent(((Number(weight) || 0) / weightSum) * 100)
      : '-'

  const onSubmit = async (values: VideoSchedulingFormValues) => {
    const baseline = baselineRef.current
    const next = toOptionValues(toVideoSchedulingSetting(values))
    const changed = Object.keys(next).filter(
      (key) => next[key] !== baseline[key]
    )
    if (changed.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    // Mode goes last so scheduling never switches on with half-saved settings.
    const ordered = [
      ...changed.filter((key) => key !== MODE_KEY),
      ...changed.filter((key) => key === MODE_KEY),
    ]
    const saved: string[] = []
    for (const key of ordered) {
      try {
        await updateOption.mutateAsync({ key, value: next[key] })
      } catch (error) {
        if (key === MODE_KEY) {
          form.setError('mode', {
            message: getServerErrorMessage(
              error,
              t('Failed to update setting')
            ),
          })
          return
        }
        const keys = saved.map((name) => name.slice(PREFIX.length)).join(', ')
        toast.error(
          changed.includes(MODE_KEY)
            ? t('Mode was not saved. Saved settings: {{keys}}', {
                keys: keys || t('none'),
              })
            : t('Saved settings: {{keys}}', { keys: keys || t('none') })
        )
        return
      }
      saved.push(key)
      baseline[key] = next[key]
    }
    // The form stays editable while saving: saved values become the clean
    // baseline, and fields edited since submit keep their newer value, dirty.
    const current = form.getValues()
    pristineRef.current = values
    form.reset(values)
    for (const field of Object.keys(values) as (keyof typeof values)[]) {
      if (JSON.stringify(current[field]) !== JSON.stringify(values[field])) {
        form.setValue(field, current[field], { shouldDirty: true })
      }
    }
  }

  const numberField = (
    name: NumberFieldName,
    label: string,
    options: { step?: number | 'any'; description?: string } = {}
  ) => (
    <FormField
      key={name}
      control={form.control}
      name={name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <Input
              type='number'
              min={0}
              step={options.step ?? 'any'}
              {...safeNumberFieldProps(field)}
            />
          </FormControl>
          {options.description && (
            <FormDescription>{options.description}</FormDescription>
          )}
          <FormMessage />
        </FormItem>
      )}
    />
  )

  const modeItems = [
    { value: 'off', label: t('Off') },
    { value: 'shadow', label: t('Shadow') },
    { value: 'on', label: t('On') },
  ]
  const sellPolicyItems = [
    { value: 'exclude', label: t('Exclude the candidate') },
    { value: 'relative', label: t('Score price relative to other candidates') },
  ]

  return (
    <SettingsSection title={t('Video Smart Scheduling')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || form.formState.isSubmitting}
          />
          <SettingsFormGrid>
            <FormField
              control={form.control}
              name='mode'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Scheduling mode')}</FormLabel>
                  <Select
                    items={modeItems}
                    value={field.value}
                    onValueChange={field.onChange}
                  >
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      {modeItems.map((item) => (
                        <SelectItem key={item.value} value={item.value}>
                          {item.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t(
                      'Shadow scores candidates and logs the result without changing channel selection. Mode is saved after all other settings.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='models'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Model allow list')}</FormLabel>
                  <FormControl>
                    <TagInput
                      value={field.value}
                      onChange={field.onChange}
                      placeholder={t('Add a model and press Enter')}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Empty schedules every model of plugins that provide describeSpec'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsFormGrid>

          <h4 className='font-medium'>{t('Video scheduling audit')}</h4>
          <SettingsFormGrid>
            <FormField
              control={form.control}
              name='audit_enabled'
              render={({ field }) => (
                <SettingsSwitchField
                  controlId='video-audit-enabled'
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  label={t('Collect scheduling audits')}
                  description={t(
                    'Stopping collection does not stop terminal updates for enrolled tasks.'
                  )}
                />
              )}
            />
            {numberField('audit_retention_days', t('Audit retention (days)'), {
              step: 1,
              description: t(
                'Completed audits are retained for 7–180 days. Pending tasks are kept.'
              ),
            })}
          </SettingsFormGrid>
          <Link
            className='text-primary text-sm underline'
            to='/video-scheduling/audit'
            search={{ mode: 'on', page: 1, page_size: 25 }}
          >
            {t('Open scheduling audit')}
          </Link>

          <h4 className='font-medium'>{t('Score weights')}</h4>
          <div className='grid grid-cols-1 gap-4 md:grid-cols-3'>
            {numberField('price_weight', t('Price weight'), {
              description: t('Normalized: {{value}}', {
                value: normalizedWeight(weights[0]),
              }),
            })}
            {numberField('quality_weight', t('Quality weight'), {
              description: t('Normalized: {{value}}', {
                value: normalizedWeight(weights[1]),
              }),
            })}
            {numberField('service_weight', t('Service weight'), {
              description: t('Normalized: {{value}}', {
                value: normalizedWeight(weights[2]),
              }),
            })}
          </div>

          <h4 className='font-medium'>{t('Health gates')}</h4>
          <div className='grid grid-cols-1 gap-4 md:grid-cols-4'>
            {numberField('min_submit_rate', t('Minimum submit success rate'))}
            {numberField('min_gen_rate', t('Minimum generation success rate'))}
            {numberField('min_samples', t('Minimum samples'), { step: 1 })}
            {numberField('window_seconds', t('Window (seconds)'), { step: 1 })}
          </div>

          <h4 className='font-medium'>
            {t('Exploration and recovery probes')}
          </h4>
          <div className='grid grid-cols-1 gap-4 md:grid-cols-3'>
            {numberField('explore_share', t('Exploration share'), {
              description: t('Target probability for unproven channels'),
            })}
            {numberField(
              'explore_max_in_flight',
              t('Exploration max in flight'),
              { step: 1, description: t('Per unproven channel') }
            )}
            {numberField('probe_ratio', t('Probe probability'), {
              description: t('For channels held back by a health gate'),
            })}
            {numberField('probe_cooldown_sec', t('Probe cooldown (seconds)'), {
              step: 1,
              description: t(
                'Doubles on consecutive failures, capped at one day'
              ),
            })}
            {numberField('probe_max_in_flight', t('Probe max in flight'), {
              step: 1,
            })}
          </div>

          <h4 className='font-medium'>{t('Pricing')}</h4>
          <div className='grid grid-cols-1 gap-4 md:grid-cols-3'>
            <FormField
              control={form.control}
              name='unknown_sell_policy'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Unknown sell price')}</FormLabel>
                  <Select
                    items={sellPolicyItems}
                    value={field.value}
                    onValueChange={field.onChange}
                  >
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      {sellPolicyItems.map((item) => (
                        <SelectItem key={item.value} value={item.value}>
                          {item.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />
            {numberField(
              'max_cost_to_sell_ratio',
              t('Max cost to sell ratio'),
              {
                description: t('0 means no loss threshold'),
              }
            )}
            {numberField('tie_epsilon', t('Tie epsilon'))}
          </div>

          <FormField
            control={form.control}
            name='capacity_groups'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Capacity groups')}</FormLabel>
                <FormDescription>
                  {t(
                    'Shared upstream account groups and their concurrent task quota. Channels reference a group by name.'
                  )}
                </FormDescription>
                <FormControl>
                  <div>
                    <JsonEditor
                      value={field.value}
                      onChange={field.onChange}
                      valueType='any'
                      keyLabel={t('Group name')}
                      valueLabel={t('Quota')}
                      keyPlaceholder='account-a'
                      valuePlaceholder='10'
                      emptyMessage={t('No capacity groups')}
                    />
                  </div>
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
      <VideoScheduleSimulatorDialog
        getConfigSnapshot={() => ({
          ...toVideoSchedulingSetting(form.getValues()),
          // The simulator rejects a window that differs from the live one.
          window_seconds: liveSetting.window_seconds,
        })}
      />
    </SettingsSection>
  )
}
