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
import { Plus, Trash2 } from 'lucide-react'
import { useFieldArray, useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { FieldGroup, FieldLegend, FieldSet } from '@/components/ui/field'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { PricingAmountInput } from '@/features/model-pricing/pricing-amount-input'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SettingsSwitchField } from '../components/settings-form-layout'
import {
  createVideoSalesFormSchema,
  parseVideoSalesSeconds,
  videoSalesFromForm,
  type VideoSalesFormValues,
} from './video-sales-form'
import { VideoSalesRoutingPanel } from './video-sales-routing-panel'

export function VideoSalesModelEditor(props: {
  form: UseFormReturn<VideoSalesFormValues>
  index: number
  pending: boolean
  onDelete: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  useSystemConfigStore((state) => state.config.currency)
  const path = `models.${props.index}` as const
  useWatch({ control: props.form.control, name: path })
  const tiers = useFieldArray({
    control: props.form.control,
    name: `${path}.tiers`,
  })
  // Field-array edits update form values before the watch subscription catches up.
  const model = props.form.getValues(path)
  const modelLabel = model.name || t('New video model')
  const validated = createVideoSalesFormSchema(t).safeParse({ models: [model] })
  const sales = validated.success
    ? videoSalesFromForm(validated.data)[model.name]
    : null
  const tierError = props.form.formState.errors.models?.[props.index]?.tiers

  return (
    <FieldSet
      className='min-w-0 rounded-xl border p-4'
      disabled={props.pending}
    >
      <FieldLegend>{modelLabel}</FieldLegend>
      <FieldGroup>
        <div className='flex flex-wrap items-start gap-3'>
          <FormField
            control={props.form.control}
            name={`${path}.name`}
            render={({ field }) => (
              <FormItem className='min-w-0 flex-1'>
                <FormLabel>{t('Public model name')}</FormLabel>
                <FormControl>
                  <Input {...field} disabled={props.pending} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={props.pending}
            onClick={props.onDelete}
            aria-label={t('Delete {{model}}', { model: modelLabel })}
          >
            <Trash2 aria-hidden='true' data-icon='inline-start' />
            {t('Delete')}
          </Button>
        </div>
        <FormField
          control={props.form.control}
          name={`${path}.disabled`}
          render={({ field }) => (
            <SettingsSwitchField
              controlId={`video-sales-enabled-${props.index}`}
              checked={!field.value}
              onCheckedChange={(checked) => field.onChange(!checked)}
              disabled={props.pending}
              label={t('Enable video sales')}
              description={t(
                'New models start paused. Configure their channels before enabling sales.'
              )}
            />
          )}
        />
        <StaticDataTable
          data={tiers.fields.map((tier, index) => ({ ...tier, index }))}
          getRowKey={(row) => row.id}
          tableProps={{
            'aria-label': t('Sale tiers for {{model}}', { model: modelLabel }),
          }}
          emptyContent={t('Add at least one resolution')}
          columns={[
            {
              id: 'resolution',
              header: t('Resolution'),
              className: 'min-w-32',
              cell: (row) => (
                <FormField
                  control={props.form.control}
                  name={`${path}.tiers.${row.index}.resolution`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel className='sr-only'>
                        {t('Resolution')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          disabled={props.pending}
                          placeholder='720p'
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ),
            },
            {
              id: 'price',
              header: t('Price per second (USD)'),
              className: 'min-w-40',
              cell: (row) => (
                <FormField
                  control={props.form.control}
                  name={`${path}.tiers.${row.index}.price`}
                  render={({ field, fieldState }) => (
                    <FormItem>
                      <FormLabel className='sr-only'>
                        {t('Price per second (USD)')}
                      </FormLabel>
                      <FormControl>
                        <PricingAmountInput
                          {...field}
                          disabled={props.pending}
                          aria-invalid={fieldState.invalid}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ),
            },
            {
              id: 'seconds',
              header: t('Allowed durations (seconds)'),
              className: 'min-w-44',
              cell: (row) => (
                <FormField
                  control={props.form.control}
                  name={`${path}.tiers.${row.index}.seconds`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel className='sr-only'>
                        {t('Allowed durations (seconds)')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          disabled={props.pending}
                          placeholder='5, 10, 15'
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ),
            },
            {
              id: 'preview',
              header: t('Price preview'),
              className: 'min-w-52',
              cell: (row) => {
                const tier = model.tiers[row.index]
                const seconds = parseVideoSalesSeconds(tier.seconds)
                const price = Number(tier.price)
                if (!seconds || !Number.isFinite(price) || price <= 0) {
                  return '—'
                }
                return (
                  <div className='space-y-1 font-mono text-xs tabular-nums'>
                    {seconds.map((duration) => (
                      <div key={duration}>
                        {t('{{resolution}} × {{seconds}} seconds = {{price}}', {
                          resolution: tier.resolution,
                          seconds: formatNumber(duration, locale),
                          price: formatBillingCurrencyFromUSD(
                            price * duration,
                            {
                              locale,
                              digitsLarge: 4,
                              digitsSmall: 6,
                              abbreviate: false,
                            }
                          ),
                        })}
                      </div>
                    ))}
                  </div>
                )
              },
            },
            {
              id: 'actions',
              header: t('Actions'),
              cell: (row) => (
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  disabled={props.pending}
                  onClick={() => tiers.remove(row.index)}
                  aria-label={t('Remove resolution {{resolution}}', {
                    resolution:
                      model.tiers[row.index].resolution ||
                      formatNumber(row.index + 1, locale),
                  })}
                >
                  <Trash2 aria-hidden='true' className='size-4' />
                </Button>
              ),
            },
          ]}
        />
        {(tierError?.root?.message || tierError?.message) && (
          <p role='alert' className='text-destructive text-sm'>
            {tierError.root?.message || tierError.message}
          </p>
        )}
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='w-fit'
          disabled={props.pending}
          onClick={() =>
            tiers.append({ resolution: '', price: '', seconds: '' })
          }
        >
          <Plus aria-hidden='true' data-icon='inline-start' />
          {t('Add resolution')}
        </Button>
        {sales && (
          <VideoSalesRoutingPanel modelName={model.name} sales={sales} />
        )}
      </FieldGroup>
    </FieldSet>
  )
}
