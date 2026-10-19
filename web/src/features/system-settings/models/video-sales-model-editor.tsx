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
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { PricingAmountInput } from '@/features/model-pricing/pricing-amount-input'
import { videoTokens, videoTokenSaleUSD } from '@/features/pricing/lib/price'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SettingsSwitchField } from '../components/settings-form-layout'
import {
  createVideoSalesFormSchema,
  isInputVideoPrice,
  isPositivePrice,
  parseVideoSalesSeconds,
  videoSalesFromForm,
  videoSalesTiersForUnit,
  videoSalesTokenSizes,
  type VideoPricingUnit,
  type VideoSalesFormValues,
} from './video-sales-form'
import { VideoSalesRoutingPanel } from './video-sales-routing-panel'

type TierPriceField =
  | 'price'
  | 'inputPrice'
  | 'tokenPrice'
  | 'withVideoTokenPrice'

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
  const tokenPriced = model.pricingUnit === 'token'
  const validated = createVideoSalesFormSchema(t).safeParse({ models: [model] })
  const sales = validated.success
    ? videoSalesFromForm(validated.data)[model.name]
    : null
  const tierError = props.form.formState.errors.models?.[props.index]?.tiers
  const currency = {
    locale,
    digitsLarge: 4,
    digitsSmall: 6,
    abbreviate: false,
  }

  const priceColumn = (field: TierPriceField, header: string) => ({
    id: field,
    header,
    className: 'min-w-44',
    cell: (row: { index: number }) => (
      <FormField
        control={props.form.control}
        name={`${path}.tiers.${row.index}.${field}`}
        render={({ field: input, fieldState }) => (
          <FormItem>
            <FormLabel className='sr-only'>{header}</FormLabel>
            <FormControl>
              <PricingAmountInput
                {...input}
                disabled={props.pending}
                aria-invalid={fieldState.invalid}
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />
    ),
  })
  const priceColumns = tokenPriced
    ? [
        priceColumn('tokenPrice', t('No-reference price per 1M tokens (USD)')),
        priceColumn(
          'withVideoTokenPrice',
          model.officialReferenceBilling
            ? t('With-reference order price per 1M tokens (USD)')
            : t('Reference video price per 1M tokens (USD)')
        ),
      ]
    : [
        priceColumn('price', t('Price per second (USD)')),
        priceColumn(
          'inputPrice',
          model.officialReferenceBilling
            ? t('With-reference order price per second (USD)')
            : t('Input video price per second (USD)')
        ),
      ]

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
        <div className='flex flex-wrap items-start gap-3'>
          <FormField
            control={props.form.control}
            name={`${path}.pricingUnit`}
            render={({ field }) => (
              <FormItem className='min-w-56'>
                <FormLabel>{t('Pricing unit')}</FormLabel>
                <FormControl>
                  <Select
                    items={[
                      { value: 'second', label: t('Per second') },
                      { value: 'token', label: t('Per 1M tokens') },
                    ]}
                    value={field.value}
                    onValueChange={(value) => {
                      const unit = value as VideoPricingUnit
                      if (unit === field.value) return
                      // Prefill the other unit's prices for the administrator to confirm.
                      tiers.replace(videoSalesTiersForUnit(model, unit))
                      field.onChange(unit)
                    }}
                    disabled={props.pending}
                  >
                    <SelectTrigger aria-label={t('Pricing unit')}>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        <SelectItem value='second'>
                          {t('Per second')}
                        </SelectItem>
                        <SelectItem value='token'>
                          {t('Per 1M tokens')}
                        </SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </FormControl>
                <FormDescription>
                  {t(
                    'Switching prefills the other unit at the 16:9 token rate; check the prices before saving.'
                  )}
                </FormDescription>
              </FormItem>
            )}
          />
          {tokenPriced && (
            <FormField
              control={props.form.control}
              name={`${path}.dimensionProfile`}
              render={({ field }) => (
                <FormItem className='min-w-56'>
                  <FormLabel>{t('Size table')}</FormLabel>
                  <FormControl>
                    <Select
                      items={[
                        { value: 'seedance-2.0', label: 'Seedance 2.0' },
                        { value: 'seedance-2.5', label: 'Seedance 2.5' },
                      ]}
                      value={field.value}
                      onValueChange={field.onChange}
                      disabled={props.pending}
                    >
                      <SelectTrigger aria-label={t('Size table')}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          <SelectItem value='seedance-2.0'>
                            Seedance 2.0
                          </SelectItem>
                          <SelectItem value='seedance-2.5'>
                            Seedance 2.5
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Sets the output width and height used to count tokens, matching the official calculator.'
                    )}
                  </FormDescription>
                </FormItem>
              )}
            />
          )}
        </div>
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
            ...priceColumns,
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
                if (!seconds) return '—'
                if (tokenPriced) {
                  const size = videoSalesTokenSizes(
                    tier.resolution,
                    model.dimensionProfile
                  )?.[0]
                  if (
                    !size ||
                    !isPositivePrice(tier.tokenPrice) ||
                    !isInputVideoPrice(tier.withVideoTokenPrice)
                  ) {
                    return '—'
                  }
                  const sale = {
                    usdPerMillionTokens: Number(tier.tokenPrice),
                    withVideoUSDPerMillionTokens: Number(
                      tier.withVideoTokenPrice
                    ),
                    official: model.officialReferenceBilling,
                  }
                  // A reference as long as the output, the official example.
                  const example = seconds[0]
                  const exampleTokens = videoTokens(size[0], size[1], example)
                  return (
                    <div className='space-y-1 font-mono text-xs tabular-nums'>
                      {seconds.map((duration) => {
                        const tokens = videoTokens(size[0], size[1], duration)
                        return (
                          <div key={duration}>
                            {t(
                              '{{resolution}} 16:9 × {{seconds}} s = {{tokens}} tokens = {{price}}',
                              {
                                resolution: tier.resolution,
                                seconds: formatNumber(duration, locale),
                                tokens: formatNumber(tokens, locale),
                                price: formatBillingCurrencyFromUSD(
                                  videoTokenSaleUSD({
                                    ...sale,
                                    outputTokens: tokens,
                                    inputTokens: 0,
                                  }),
                                  currency
                                ),
                              }
                            )}
                          </div>
                        )
                      })}
                      <div className='text-muted-foreground font-sans'>
                        {sale.withVideoUSDPerMillionTokens > 0
                          ? t(
                              'With a {{seconds}} s reference: {{tokens}} tokens = {{price}}',
                              {
                                seconds: formatNumber(example, locale),
                                tokens: formatNumber(exampleTokens * 2, locale),
                                price: formatBillingCurrencyFromUSD(
                                  videoTokenSaleUSD({
                                    ...sale,
                                    outputTokens: exampleTokens,
                                    inputTokens: exampleTokens,
                                  }),
                                  currency
                                ),
                              }
                            )
                          : t('Input video: no extra charge')}
                      </div>
                    </div>
                  )
                }
                const price = Number(tier.price)
                if (
                  !isPositivePrice(tier.price) ||
                  !isInputVideoPrice(tier.inputPrice)
                ) {
                  return '—'
                }
                const inputPrice = Number(tier.inputPrice)
                const inputPriceText = formatBillingCurrencyFromUSD(
                  inputPrice,
                  currency
                )
                let inputNote = t('Input video: no extra charge')
                if (inputPrice > 0 && model.officialReferenceBilling) {
                  inputNote = t(
                    'With reference: (output + max(reference, ⌈output × 2/3⌉)) seconds × {{price}} per second',
                    { price: inputPriceText }
                  )
                } else if (inputPrice > 0) {
                  inputNote = t('Plus input video: {{price}} per second', {
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
          {tokenPriced &&
            t(
              'Tokens = width × height × 24 × seconds / 1024 at the official output size; reference video is counted at the output size too. 480p, 720p, 1080p and 4k only.'
            )}
          {!tokenPriced &&
            (model.officialReferenceBilling
              ? t(
                  'With-reference order price is per second of output plus billable reference video. It must be above 0.'
                )
              : t(
                  'Input video price is per second of reference video. 0 means no extra charge.'
                ))}
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
              tokenPrice: '',
              withVideoTokenPrice: '0',
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
