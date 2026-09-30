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
import { useQueries, useQuery } from '@tanstack/react-query'
import { Clapperboard } from 'lucide-react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ComboboxInput } from '@/components/ui/combobox-input'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { getSystemOptions } from '@/features/system-settings/api'
import { ROLE } from '@/lib/roles'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getVideoSchedulable } from '../../../api'
import type { ChannelFormValues } from '../../../lib'
import type { ChannelConfigurationStatus } from '../../../lib/channel-configuration'
import {
  EMPTY_VIDEO_SCHEDULING_DRAFT,
  newVideoModelDraft,
} from '../../../lib/video-scheduling'
import { ChannelConfigurationStatusIndicator } from '../channel-configuration'
import { VideoModelCostCard } from './video-model-cost-card'

type ChannelVideoSchedulingSectionProps = {
  /** Task plugins bound to the channel; without one only an existing draft renders. */
  pluginKeys: string[]
  channelModels: string[]
  disabled: boolean
  status: ChannelConfigurationStatus
  className: string
}

export function ChannelVideoSchedulingSection(
  props: ChannelVideoSchedulingSectionProps
) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  const draft =
    useWatch({ control: form.control, name: 'video_scheduling' }) ??
    EMPTY_VIDEO_SCHEDULING_DRAFT
  const schedulable = useQueries({
    queries: props.pluginKeys.map((plugin) => ({
      queryKey: ['channel-video-schedulable', plugin],
      queryFn: () => getVideoSchedulable(plugin),
      meta: { errorToast: false },
      staleTime: 60_000,
    })),
  })
  // Root-only endpoint: other channel editors just type a group name.
  const isRoot = useAuthStore((s) => s.auth.user?.role === ROLE.SUPER_ADMIN)
  const capacityGroups = useQuery({
    enabled: isRoot,
    queryKey: ['video-scheduling-capacity-groups'],
    queryFn: async () => {
      const options = requireServerSuccess(await getSystemOptions()).data
      const raw = options.find(
        (option) => option.key === 'video_scheduling_setting.capacity_groups'
      )?.value
      return Object.keys(JSON.parse(raw || '{}') as Record<string, number>)
    },
    meta: { errorToast: false },
    retry: false,
    staleTime: 60_000,
  })

  // An enabled draft stays editable after its plugin binding goes away, since
  // the form still validates and saves it.
  if (props.pluginKeys.length === 0 && !draft.enabled) return null
  const supported = schedulable.some((query) => query.data?.describe_spec)
  const blockers = new Map<string, string[]>()
  for (const query of schedulable) {
    if (!query.data?.describe_spec) continue
    for (const entry of query.data.models) {
      blockers.set(entry.model, [
        ...(blockers.get(entry.model) ?? []),
        ...entry.static_blockers,
      ])
    }
  }
  const configured = new Set(draft.models.map((item) => item.model))
  const addableModels = props.channelModels.filter(
    (model) => !configured.has(model)
  )
  const group = draft.capacity_group.trim()
  const unregisteredGroup =
    group !== '' &&
    capacityGroups.data !== undefined &&
    !capacityGroups.data.includes(group)

  return (
    <div
      role='group'
      aria-label={t('Video scheduling')}
      className={props.className}
    >
      <div className='flex items-center gap-3'>
        <IconBadge tone='info' size='md'>
          <Clapperboard />
        </IconBadge>
        <h3 className='min-w-0 flex-1 text-sm font-semibold tracking-tight'>
          {t('Video scheduling')}
        </h3>
        <ChannelConfigurationStatusIndicator status={props.status} />
      </div>

      {props.pluginKeys.length === 0 && (
        <p className='text-muted-foreground mt-3 text-xs'>
          {t(
            'No task plugin is bound, so scheduling does not apply; turn it off to remove the saved prices'
          )}
        </p>
      )}
      <ul className='text-muted-foreground mt-3 space-y-1 text-xs'>
        {schedulable.map((query, index) => {
          const plugin = props.pluginKeys[index]
          let message = t('Checking scheduling support...')
          if (query.isError) message = getServerErrorMessage(query.error)
          if (query.data?.describe_spec) {
            message = t('Schedulable: the plugin provides describeSpec')
          } else if (query.data) {
            message = t('Not schedulable: plugin does not provide describeSpec')
          }
          return (
            <li key={plugin}>
              <span className='font-mono'>{plugin}</span>
              {' · '}
              {message}
            </li>
          )
        })}
      </ul>

      {(supported || draft.enabled) && (
        <fieldset
          disabled={props.disabled}
          className='mt-4 flex flex-col gap-4 disabled:opacity-60'
        >
          <FormField
            control={form.control}
            name='video_scheduling.enabled'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between gap-4'>
                <div className='space-y-0.5'>
                  <FormLabel>{t('Schedule this channel')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Price the channel so video scheduling can compare it with other upstreams'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          {draft.enabled && (
            <>
              <div className='grid gap-4 sm:grid-cols-3'>
                <FormField
                  control={form.control}
                  name='video_scheduling.quality'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Quality')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          step='any'
                          placeholder='0 - 1'
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='video_scheduling.capacity'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Capacity')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          step='any'
                          placeholder={t('Unlimited')}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Concurrent tasks; empty or 0 means unlimited')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='video_scheduling.capacity_group'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Capacity group')}</FormLabel>
                      <FormControl>
                        <ComboboxInput
                          options={(capacityGroups.data ?? []).map((name) => ({
                            value: name,
                            label: name,
                          }))}
                          value={field.value}
                          onValueChange={field.onChange}
                          allowCustomValue
                          placeholder={t('No group')}
                          disabled={props.disabled}
                        />
                      </FormControl>
                      {unregisteredGroup && (
                        <p className='text-warning text-xs'>
                          {t(
                            'Unregistered group: treated as no group until it is added to the capacity groups setting'
                          )}
                        </p>
                      )}
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              {draft.models.map((item, index) => (
                <VideoModelCostCard
                  key={item.model}
                  index={index}
                  staticBlockers={blockers.get(item.model) ?? []}
                  onRemove={() =>
                    form.setValue(
                      'video_scheduling.models',
                      draft.models.filter((_, i) => i !== index),
                      { shouldDirty: true }
                    )
                  }
                />
              ))}

              {addableModels.length > 0 ? (
                <Select
                  items={addableModels.map((model) => ({
                    value: model,
                    label: model,
                  }))}
                  value={null}
                  onValueChange={(model) => {
                    if (!model) return
                    form.setValue(
                      'video_scheduling.models',
                      [...draft.models, newVideoModelDraft(model)],
                      { shouldDirty: true }
                    )
                  }}
                >
                  <SelectTrigger
                    className='sm:w-72'
                    aria-label={t('Add model price')}
                  >
                    <SelectValue placeholder={t('Add model price')} />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    {addableModels.map((model) => (
                      <SelectItem key={model} value={model}>
                        {model}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                draft.models.length === 0 && (
                  <p className='text-muted-foreground text-xs'>
                    {t('Add models to the channel before pricing them')}
                  </p>
                )
              )}
            </>
          )}
        </fieldset>
      )}
    </div>
  )
}
