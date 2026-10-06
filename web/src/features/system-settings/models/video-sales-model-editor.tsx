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
import {
  VIDEO_INPUT_TOKENS_PER_SECOND,
  videoInputSecondPrice,
  videoInputTokenPrice,
} from '@/features/pricing/lib/price'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SettingsSwitchField } from '../components/settings-form-layout'
import {
  createVideoSalesFormSchema,
  isInputVideoPrice,
  parseVideoSalesSeconds,
  videoSalesFromForm,
  videoSalesInputTokensPerSecond,
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
        <FormField
          control={props.form.control}
          name={`${path}.officialReferenceBilling`}
          render={({ field }) => (
            <SettingsSwitchField
              controlId={`video-sales-official-billing-${props.index}`}
              checked={field.value}
              onCheckedChange={field.onChange}
              disabled={props.pending}
              label={t('Bill orders with reference video the official way')}
              description={t(
                'With reference video, the whole order is billed at the with-reference price for the output plus max(reference, ⌈output × 2/3⌉) seconds.'
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
                          onChange={(event) => {
                            const tierPath =
                              `${path}.tiers.${row.index}` as const
                            const tier = props.form.getValues(tierPath)
                            const before = videoSalesInputTokensPerSecond(
                              tier.resolution
                            )
                            const after = videoSalesInputTokensPerSecond(
                              event.target.value
                            )
                            field.onChange(event)
                            // Token-priced tiers keep the token price, so the
                            // stored per-second price follows the new tier.
                            if (before !== null && after === null) {
                              props.form.setValue(
                                `${tierPath}.inputPrice`,
                                isInputVideoPrice(tier.inputTokenPrice)
                                  ? String(
                                      videoInputSecondPrice(
                                        Number(tier.inputTokenPrice),
                                        before
                                      )
                                    )
                                  : tier.inputTokenPrice
                              )
                            }
                            // Typing passes through untiered values such as
                            // "720"; keep the token price unless the
                            // per-second price was edited meanwhile.
                            if (before === null && after !== null) {
                              const mirrored =
                                isInputVideoPrice(tier.inputTokenPrice) &&
                                [
                                  ...VIDEO_INPUT_TOKENS_PER_SECOND.values(),
                                ].some(
                                  (rate) =>
                                    String(
                                      videoInputSecondPrice(
                                        Number(tier.inputTokenPrice),
                                        rate
                                      )
                                    ) === tier.inputPrice
                                )
                              if (!mirrored) {
                                props.form.setValue(
                                  `${tierPath}.inputTokenPrice`,
                                  isInputVideoPrice(tier.inputPrice)
                                    ? String(
                                        videoInputTokenPrice(
                                          Number(tier.inputPrice),
                                          after
                                        )
                                      )
                                    : tier.inputPrice
                                )
                              }
                            }
                          }}
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
              id: 'input-price',
              header: model.officialReferenceBilling
                ? t('With-reference order price (USD)')
                : t('Reference video price (USD)'),
              className: 'min-w-44',
              cell: (row) => {
                const tokenPriced =
                  videoSalesInputTokensPerSecond(
                    model.tiers[row.index].resolution
                  ) !== null
                let label = tokenPriced
                  ? t('Reference video price per 1M tokens (USD)')
                  : t('Input video price per second (USD)')
                if (model.officialReferenceBilling) {
                  label = tokenPriced
                    ? t('With-reference order price per 1M tokens (USD)')
                    : t('With-reference order price per second (USD)')
                }
                return (
                  <FormField
                    // Remount so the input binds to the field this tier prices by.
                    key={tokenPriced ? 'tokens' : 'seconds'}
                    control={props.form.control}
                    name={`${path}.tiers.${row.index}.${tokenPriced ? 'inputTokenPrice' : 'inputPrice'}`}
                    render={({ field, fieldState }) => (
                      <FormItem>
                        <FormLabel className='sr-only'>{label}</FormLabel>
                        <FormControl>
                          <PricingAmountInput
                            {...field}
                            disabled={props.pending}
                            aria-invalid={fieldState.invalid}
                          />
                        </FormControl>
                        <p className='text-muted-foreground text-xs'>
                          {tokenPriced ? t('per 1M tokens') : t('per second')}
                        </p>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )
              },
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
                const tokensPerSecond = videoSalesInputTokensPerSecond(
                  tier.resolution
                )
                const inputValue =
                  tokensPerSecond === null
                    ? tier.inputPrice
                    : tier.inputTokenPrice
                if (
                  !seconds ||
                  !Number.isFinite(price) ||
                  price <= 0 ||
                  !isInputVideoPrice(inputValue)
                ) {
                  return '—'
                }
                const inputPrice = Number(inputValue)
                const currency = {
                  locale,
                  digitsLarge: 4,
                  digitsSmall: 6,
                  abbreviate: false,
                }
                const inputPriceText = formatBillingCurrencyFromUSD(
                  inputPrice,
                  currency
                )
                let inputNote = t('Input video: no extra charge')
                if (inputPrice > 0 && model.officialReferenceBilling) {
                  inputNote =
                    tokensPerSecond === null
                      ? t(
                          'With reference: (output + max(reference, ⌈output × 2/3⌉)) seconds × {{price}} per second',
                          { price: inputPriceText }
                        )
                      : t(
                          'With reference: (output + max(reference, ⌈output × 2/3⌉)) seconds × {{price}} / 1M tokens',
                          { price: inputPriceText }
                        )
                } else if (inputPrice > 0) {
                  inputNote =
                    tokensPerSecond === null
                      ? t('Plus input video: {{price}} per second', {
                          price: inputPriceText,
                        })
                      : t('Plus reference video: {{price}} / 1M tokens', {
                          price: inputPriceText,
                        })
                }
                // Input duration is unknown here, so no total is shown.
                return (
                  <div className='space-y-1 font-mono text-xs tabular-nums'>
                    {seconds.map((duration) => (
                      <div key={duration}>
                        {t('{{resolution}} × {{seconds}} seconds = {{price}}', {
                          resolution: tier.resolution,
                          seconds: formatNumber(duration, locale),
                          price: formatBillingCurrencyFromUSD(
                            price * duration,
                            currency
                          ),
                        })}
                      </div>
                    ))}
                    <div className='text-muted-foreground font-sans'>
                      {inputNote}
                      {inputPrice > 0 && tokensPerSecond !== null && (
                        <>
                          {' '}
                          <span>
                            {t('({{tokens}} tokens per second)', {
                              tokens: formatNumber(tokensPerSecond, locale),
                            })}
                          </span>
                        </>
                      )}
                    </div>
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
        <p className='text-muted-foreground text-sm'>
          {model.officialReferenceBilling
            ? t(
                'With-reference order price follows the official token rule: tokens = seconds × width × height × 24 / 1024 at the output resolution. It must be above 0; 480p, 720p, 1080p and 4k are priced per 1M tokens, other resolutions per second.'
              )
            : t(
                'Reference video price follows the official token rule: tokens = seconds × width × height × 24 / 1024 at the output resolution. 0 means no extra charge; 480p, 720p, 1080p and 4k are priced per 1M tokens, other resolutions per second.'
              )}
        </p>
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
            tiers.append({
              resolution: '',
              price: '',
              inputPrice: '0',
              inputTokenPrice: '0',
              seconds: '',
            })
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
