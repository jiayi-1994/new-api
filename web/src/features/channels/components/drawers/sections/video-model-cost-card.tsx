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
import { Plus, Trash2, TriangleAlert } from 'lucide-react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from '@/components/ui/input-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import type { ChannelFormValues } from '../../../lib'
import {
  VIDEO_REFERENCE_CHARGING_MODES,
  VIDEO_REFERENCE_KINDS,
  type VideoModelDraft,
} from '../../../lib/video-scheduling'
import type { VideoReferenceKind } from '../../../types'

type VideoModelCostCardProps = {
  index: number
  staticBlockers: string[]
  onRemove: () => void
}

const NOT_CONFIGURED = 'none'

export function VideoModelCostCard(props: VideoModelCostCardProps) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  const base = `video_scheduling.models.${props.index}` as const
  const item = useWatch({ control: form.control, name: base })
  if (!item) return null

  const update = (next: Partial<VideoModelDraft>) =>
    form.setValue(base, { ...item, ...next }, { shouldDirty: true })
  const baseUnit =
    item.mode === 'per_second' ? t('USD/output second') : t('USD/video')
  const modeItems = [
    { value: 'per_video', label: t('Per video') },
    { value: 'per_second', label: t('Per output second') },
  ]
  const referenceModeItems = [
    { value: NOT_CONFIGURED, label: t('Not configured') },
    { value: 'unsupported', label: t('Unsupported') },
    { value: 'included', label: t('Included in base price') },
    { value: 'per_request', label: t('Per request') },
    { value: 'per_input', label: t('Per input') },
    { value: 'per_output_second', label: t('Per output second') },
    { value: 'multiplier', label: t('Base cost multiplier') },
  ]
  const referenceUnits: Record<string, string> = {
    per_request: t('USD/request'),
    per_input: t('USD/input'),
    per_output_second: t('USD/output second'),
    multiplier: t('× base cost'),
  }
  const kindLabels: Record<VideoReferenceKind, string> = {
    video: t('Reference videos'),
    image: t('Reference images'),
    audio: t('Reference audio'),
  }

  return (
    <div
      role='group'
      aria-label={item.model}
      className='border-border/60 flex flex-col gap-4 rounded-lg border p-3'
    >
      <div className='flex items-start justify-between gap-2'>
        <div className='min-w-0'>
          <div className='truncate font-mono text-sm font-medium'>
            {item.model}
          </div>
          {props.staticBlockers.length > 0 && (
            <p className='text-warning mt-1 flex items-center gap-1 text-xs'>
              <TriangleAlert className='size-3.5 shrink-0' aria-hidden='true' />
              {t(
                'Requests may stay on ordinary routing: {{plugins}} declare this model without describeSpec',
                { plugins: props.staticBlockers.join(', ') }
              )}
            </p>
          )}
        </div>
        <Button
          type='button'
          variant='ghost'
          size='icon-sm'
          aria-label={t('Remove {{model}}', { model: item.model })}
          onClick={props.onRemove}
        >
          <Trash2 aria-hidden='true' />
        </Button>
      </div>

      <FormField
        control={form.control}
        name={`${base}.mode`}
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Base price mode')}</FormLabel>
            <Select
              items={modeItems}
              value={field.value}
              onValueChange={field.onChange}
            >
              <FormControl>
                <SelectTrigger className='sm:w-60'>
                  <SelectValue />
                </SelectTrigger>
              </FormControl>
              <SelectContent alignItemWithTrigger={false}>
                {modeItems.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </FormItem>
        )}
      />

      <div className='flex flex-col gap-2'>
        <div className='text-sm font-medium'>{t('Base prices')}</div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Purchase price per resolution tier, excluding reference media fees. Use * for untiered.'
          )}
        </p>
        {/* Rows bind form paths by position, so the position is their identity. */}
        {Array.from(item.prices.keys(), (rowIndex) => (
          <div key={rowIndex} className='grid grid-cols-[1fr_1.5fr_auto] gap-2'>
            <FormField
              control={form.control}
              name={`${base}.prices.${rowIndex}.tier`}
              render={({ field }) => (
                <FormItem>
                  <FormControl>
                    <Input
                      {...field}
                      aria-label={t('Resolution tier')}
                      placeholder='720p / *'
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name={`${base}.prices.${rowIndex}.price`}
              render={({ field }) => (
                <FormItem>
                  <InputGroup>
                    <FormControl>
                      <InputGroupInput
                        {...field}
                        type='number'
                        min={0}
                        step='any'
                        inputMode='decimal'
                        aria-label={t('Base price')}
                      />
                    </FormControl>
                    <InputGroupAddon align='inline-end'>
                      <InputGroupText>{baseUnit}</InputGroupText>
                    </InputGroupAddon>
                  </InputGroup>
                  <FormMessage />
                </FormItem>
              )}
            />
            <Button
              type='button'
              variant='ghost'
              size='icon'
              aria-label={t('Remove price tier')}
              onClick={() =>
                update({
                  prices: item.prices.filter((__, i) => i !== rowIndex),
                })
              }
            >
              <Trash2 aria-hidden='true' />
            </Button>
          </div>
        ))}
        <FormField
          control={form.control}
          name={`${base}.prices`}
          render={() => (
            <FormItem>
              <FormMessage />
            </FormItem>
          )}
        />
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='self-start'
          onClick={() =>
            update({ prices: [...item.prices, { tier: '', price: '' }] })
          }
        >
          <Plus aria-hidden='true' />
          {t('Add price tier')}
        </Button>
      </div>

      <div className='grid gap-3 sm:grid-cols-3'>
        {(
          [
            ['min_seconds', t('Minimum seconds')],
            ['max_seconds', t('Maximum seconds')],
          ] as const
        ).map(([name, label]) => (
          <FormField
            key={name}
            control={form.control}
            name={`${base}.${name}`}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{label}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='number'
                    min={0}
                    step={1}
                    placeholder={t('No limit')}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        ))}
        <FormField
          control={form.control}
          name={`${base}.allowed_seconds`}
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Allowed seconds')}</FormLabel>
              <FormControl>
                <Input {...field} placeholder='5, 10' />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>

      <div className='flex flex-col gap-3'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <div>
            <div className='text-sm font-medium'>
              {t('Reference media fees')}
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Only applies when the request carries that kind of media. Not configured is never treated as included.'
              )}
            </p>
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() =>
              update({
                references: VIDEO_REFERENCE_KINDS.map((kind) => ({
                  kind,
                  tier: '*',
                  mode: 'included',
                  value: '',
                })),
              })
            }
          >
            {t('All three included')}
          </Button>
        </div>
        {VIDEO_REFERENCE_KINDS.map((kind) => {
          const rows = item.references
            .map((row, rowIndex) => ({ row, rowIndex }))
            .filter((entry) => entry.row.kind === kind)
          return (
            <div
              key={kind}
              role='group'
              aria-label={kindLabels[kind]}
              className='flex flex-col gap-2'
            >
              <div className='text-muted-foreground text-xs font-medium'>
                {kindLabels[kind]}
              </div>
              {rows.length === 0 && (
                <p className='text-muted-foreground text-xs'>
                  {t('Not configured')}
                </p>
              )}
              {rows.map(({ row, rowIndex }) => {
                const path = `${base}.references.${rowIndex}` as const
                const charging =
                  row.mode !== '' &&
                  VIDEO_REFERENCE_CHARGING_MODES.includes(row.mode)
                return (
                  <div
                    key={rowIndex}
                    className='grid grid-cols-[1fr_1.5fr_1.5fr_auto] gap-2'
                  >
                    <FormField
                      control={form.control}
                      name={`${path}.tier`}
                      render={({ field }) => (
                        <FormItem>
                          <FormControl>
                            <Input
                              {...field}
                              aria-label={t('Resolution tier')}
                              placeholder='720p / *'
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name={`${path}.mode`}
                      render={({ field }) => (
                        <FormItem>
                          <Select
                            items={referenceModeItems}
                            value={field.value || NOT_CONFIGURED}
                            onValueChange={(value) =>
                              field.onChange(
                                value === NOT_CONFIGURED ? '' : value
                              )
                            }
                          >
                            <FormControl>
                              <SelectTrigger aria-label={t('Fee mode')}>
                                <SelectValue />
                              </SelectTrigger>
                            </FormControl>
                            <SelectContent alignItemWithTrigger={false}>
                              {referenceModeItems.map((option) => (
                                <SelectItem
                                  key={option.value}
                                  value={option.value}
                                >
                                  {option.label}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </FormItem>
                      )}
                    />
                    {charging ? (
                      <FormField
                        control={form.control}
                        name={`${path}.value`}
                        render={({ field }) => (
                          <FormItem>
                            <InputGroup>
                              <FormControl>
                                <InputGroupInput
                                  {...field}
                                  type='number'
                                  min={row.mode === 'multiplier' ? 1 : 0}
                                  step='any'
                                  inputMode='decimal'
                                  aria-label={t('Fee value')}
                                />
                              </FormControl>
                              <InputGroupAddon align='inline-end'>
                                <InputGroupText>
                                  {referenceUnits[row.mode]}
                                </InputGroupText>
                              </InputGroupAddon>
                            </InputGroup>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    ) : (
                      <div />
                    )}
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      aria-label={t('Remove fee rule')}
                      onClick={() =>
                        update({
                          references: item.references.filter(
                            (__, i) => i !== rowIndex
                          ),
                        })
                      }
                    >
                      <Trash2 aria-hidden='true' />
                    </Button>
                  </div>
                )
              })}
              <Button
                type='button'
                variant='ghost'
                size='sm'
                className='self-start'
                onClick={() =>
                  update({
                    references: [
                      ...item.references,
                      { kind, tier: '*', mode: '', value: '' },
                    ],
                  })
                }
              >
                <Plus aria-hidden='true' />
                {t('Add fee rule')}
              </Button>
            </div>
          )
        })}
      </div>
    </div>
  )
}
