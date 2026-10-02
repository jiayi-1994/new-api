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
import { formatVideoSalesPrice } from '../lib/price'
import type { PricingModel } from '../types'
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
  return (
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
          header: t('Price per second'),
          cellClassName: 'font-mono tabular-nums',
          cell: (row) =>
            formatVideoSalesPrice(row.usd_per_second, {
              ...props.options,
              groupRatio: getConfiguredGroupRatio(
                props.groupRatio ?? {},
                row.group
              ),
            }),
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
  )
}
