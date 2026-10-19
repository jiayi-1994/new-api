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
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  getConfiguredGroupRatio,
  getVideoSalesTiers,
} from '../lib/model-helpers'
import {
  formatVideoSalesPrice,
  VIDEO_TOKEN_DIMENSIONS,
  videoTokens,
  videoTokenSaleUSD,
} from '../lib/price'
import type { PricingModel, VideoSalesTier } from '../types'
import type { ModelPriceCellOptions } from './model-price-cell'

export function VideoSalesPriceTable(props: {
  model: PricingModel
  options: ModelPriceCellOptions
  groups?: string[]
  groupRatio?: Record<string, number>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  // The shared currency formatter reads this store outside React.
  useSystemConfigStore((state) => state.config.currency)
  if (props.model.video_sales?.disabled) {
    return (
      <p className='text-muted-foreground text-sm'>{t('Video sales paused')}</p>
    )
  }
  const tiers = getVideoSalesTiers(props.model)
  const rows = (props.groups ?? ['']).flatMap((group) =>
    tiers.map((tier) => ({ ...tier, group }))
  )
  const official = !!props.model.video_sales?.official_reference_billing
  const tokenPriced = props.model.video_sales?.pricing_unit === 'token'
  const rowOptions = (group: string) => ({
    ...props.options,
    groupRatio: getConfiguredGroupRatio(props.groupRatio ?? {}, group),
  })
  const referencePrice = (row: VideoSalesTier) =>
    tokenPriced
      ? row.with_video_usd_per_million_tokens
      : row.input_video_usd_per_second
  const chargesReference = tiers.some((tier) => !!referencePrice(tier))
  const unitPrice = (usd: number, group: string) =>
    tokenPriced
      ? t('{{price}} / 1M tokens', {
          price: formatVideoSalesPrice(usd, rowOptions(group)),
        })
      : t('{{price}} / second', {
          price: formatVideoSalesPrice(usd, rowOptions(group)),
        })

  // One 16:9 example at the first tier's shortest duration, group ratio applied.
  let example = ''
  const first = rows[0]
  const profile = props.model.video_sales?.dimension_profile ?? 'seedance-2.0'
  const exampleSize =
    first && VIDEO_TOKEN_DIMENSIONS[profile][first.resolution]?.[0]
  if (tokenPriced && first && exampleSize && first.seconds.length > 0) {
    const tokens = videoTokens(exampleSize[0], exampleSize[1], first.seconds[0])
    example = t(
      '{{resolution}} 16:9 × {{seconds}} s ≈ {{tokens}} tokens ≈ {{price}}',
      {
        resolution: first.resolution,
        seconds: formatNumber(first.seconds[0], locale),
        tokens: formatNumber(tokens, locale),
        price: formatVideoSalesPrice(
          videoTokenSaleUSD({
            outputTokens: tokens,
            inputTokens: 0,
            usdPerMillionTokens: first.usd_per_million_tokens ?? 0,
            withVideoUSDPerMillionTokens: 0,
            official,
          }),
          rowOptions(first.group)
        ),
      }
    )
  }
  return (
    <>
      <StaticDataTable
        data={rows}
        getRowKey={(row) => `${row.group}:${row.resolution}`}
        emptyContent={t('Not configured')}
        tableProps={{
          'aria-label': props.groups ? t('Pricing by Group') : t('Base Price'),
        }}
        columns={[
          ...(props.groups
            ? [
                {
                  id: 'group',
                  header: t('Group'),
                  cell: (row: { group: string }) => (
                    <GroupBadge group={row.group} size='sm' />
                  ),
                },
              ]
            : []),
          {
            id: 'resolution',
            header: t('Resolution'),
            cell: (row) => row.resolution,
          },
          {
            id: 'price',
            header: tokenPriced
              ? t('No-reference price')
              : t('Price per second'),
            cellClassName: 'font-mono tabular-nums',
            cell: (row) =>
              tokenPriced
                ? unitPrice(row.usd_per_million_tokens ?? 0, row.group)
                : formatVideoSalesPrice(
                    row.usd_per_second,
                    rowOptions(row.group)
                  ),
          },
          {
            id: 'input-price',
            header: official
              ? t('With-reference order price')
              : t('Reference video price'),
            cellClassName: 'font-mono tabular-nums',
            cell: (row) => {
              const price = referencePrice(row)
              return price ? unitPrice(price, row.group) : t('No extra charge')
            },
          },
          {
            id: 'seconds',
            header: t('Allowed durations (seconds)'),
            cell: (row) =>
              row.seconds
                .map((seconds) => formatNumber(seconds, locale))
                .join(', '),
          },
        ]}
      />
      {tokenPriced && (
        <p className='text-muted-foreground mt-2 text-xs'>
          {t(
            'Tokens = width × height × 24 × seconds / 1024 (official output size; reference video counted at the output size)'
          )}
          {example && (
            <>
              <br />
              {example}
            </>
          )}
        </p>
      )}
      {official && chargesReference && (
        <p className='text-muted-foreground mt-2 text-xs'>
          {t(
            'With reference video, the whole order (output plus reference seconds) is billed at the with-reference price. Reference video shorter than 2/3 of the output duration is billed as 2/3 of it, rounded up to whole seconds.'
          )}
        </p>
      )}
    </>
  )
}
