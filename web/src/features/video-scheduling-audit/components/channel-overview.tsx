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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { VideoReliabilityDetails } from '@/features/channels/components/video-reliability-details'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber, formatPercent } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getChannelOverview } from '../api'
import type { AuditFilters, ChannelOverviewRow } from '../types'

/**
 * Live scheduling inputs per channel and model beside the requests each
 * channel served in the filtered window. Weighted totals are omitted: the
 * price score depends on each request, which only the simulator can score.
 */
export function ChannelOverview(props: { filters: AuditFilters }) {
  const { t, i18n } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const overview = useQuery({
    queryKey: [
      'video-scheduling-audit',
      userId,
      'channel-overview',
      props.filters,
    ],
    queryFn: () => getChannelOverview(props.filters),
    retry: false,
  })
  if (overview.isError) {
    return (
      <ErrorState
        title={t('Channel overview unavailable')}
        description={getServerErrorMessage(overview.error)}
        onRetry={() => void overview.refetch()}
      />
    )
  }
  if (!overview.data) return <LoadingState inline message={t('Loading...')} />
  const data = overview.data
  const v2 = data.selection_policy === 'stability_cost_v2'
  const servedByModel = new Map<string, number>()
  for (const row of data.rows) {
    servedByModel.set(
      row.model,
      (servedByModel.get(row.model) ?? 0) + row.usage.requests
    )
  }
  const money = (usd: number | null) =>
    usd == null
      ? '—'
      : formatBillingCurrencyFromUSD(usd, {
          locale,
          digitsSmall: 6,
          digitsLarge: 6,
          abbreviate: false,
        })
  const columns: StaticDataTableColumn<ChannelOverviewRow>[] = [
    {
      id: 'channel',
      header: t('Channel'),
      cell: (row) => {
        let state = ''
        if (row.status === 0) state = t('Channel deleted')
        else if (row.status !== 1) state = t('Disabled')
        else if (!row.scheduled) state = t('Not scheduled')
        return (
          <span className='break-words'>
            {row.name || '—'} · {formatNumber(row.channel_id, locale)}
            <span className='text-muted-foreground block text-xs'>
              {t('Priority')} {formatNumber(row.priority, locale)} ·{' '}
              {t('Weight')} {formatNumber(row.weight, locale)} · {row.group}
            </span>
            {state && (
              <StatusBadge copyable={false} variant='neutral'>
                {state}
              </StatusBadge>
            )}
          </span>
        )
      },
    },
    {
      id: 'model',
      header: t('Model'),
      cell: (row) => (
        <span className='break-all'>
          {row.model}
          {row.prices && (
            <span className='text-muted-foreground block text-xs'>
              {row.cost_mode === 'per_second'
                ? t('Per second')
                : t('Per video')}
              :{' '}
              {Object.entries(row.prices)
                .map(([tier, usd]) => `${tier} ${money(usd)}`)
                .join(' · ')}
            </span>
          )}
        </span>
      ),
    },
    {
      id: 'quality',
      header: t('Quality'),
      cell: (row) => (row.scheduled ? formatNumber(row.quality, locale) : '—'),
    },
    {
      id: 'health',
      header: v2 ? t('Video reliability') : t('Service score'),
      cell: (row) => {
        if (!row.scheduled) return '—'
        if (!row.health) return t('Health state unavailable')
        if (v2) {
          return (
            <VideoReliabilityDetails
              health={row.health.reliability}
              asOf={data.as_of / 1000}
            />
          )
        }
        const health = row.health
        return (
          <span className='space-y-1'>
            <span className='block font-semibold tabular-nums'>
              {row.service == null ? '—' : formatNumber(row.service, locale)}
            </span>
            <span className='text-muted-foreground block text-xs'>
              {t(
                'Submit {{submit}} / gen {{gen}} ({{samples}} samples) · in flight {{inFlight}}/{{capacity}}',
                {
                  submit:
                    health.submit.samples > 0
                      ? formatPercent(health.submit.rate * 100)
                      : t('Unknown'),
                  gen:
                    health.gen.samples > 0
                      ? formatPercent(health.gen.rate * 100)
                      : t('Unknown'),
                  samples: health.submit.samples,
                  inFlight: health.in_flight,
                  capacity: row.capacity > 0 ? row.capacity : '∞',
                }
              )}
            </span>
            {row.gated && (
              <StatusBadge copyable={false} variant='danger'>
                {t('Below health gate')}
              </StatusBadge>
            )}
            {row.unproven && (
              <StatusBadge copyable={false} variant='warning'>
                {t('Unproven')}
              </StatusBadge>
            )}
          </span>
        )
      },
    },
    {
      id: 'requests',
      header: t('Requests served'),
      cell: (row) => {
        const total = servedByModel.get(row.model) ?? 0
        return (
          <span className='tabular-nums'>
            {formatNumber(row.usage.requests, locale)}
            {total > 0 && (
              <span className='text-muted-foreground'>
                {' '}
                · {formatPercent((row.usage.requests / total) * 100)}
              </span>
            )}
            <span className='text-muted-foreground block text-xs'>
              {t('Success')} {formatNumber(row.usage.success, locale)} ·{' '}
              {t('Failed')} {formatNumber(row.usage.failure, locale)}
            </span>
          </span>
        )
      },
    },
    {
      id: 'cost',
      header: t('Mean estimated cost'),
      cell: (row) => (
        <span className='tabular-nums'>
          {money(row.usage.mean_cost_usd)}
          <span className='text-muted-foreground block text-xs'>
            {t('Mean success duration')}:{' '}
            {row.usage.mean_duration_ms == null
              ? '—'
              : `${formatNumber(row.usage.mean_duration_ms / 1000, locale)} s`}
          </span>
        </span>
      ),
    },
  ]
  return (
    <section className='space-y-2'>
      <h3 className='font-medium'>{t('Channel overview')}</h3>
      <p className='text-muted-foreground text-xs'>
        {v2
          ? t(
              'Stability-cost policy: at the highest priority, channels that pass reliability qualification compete; the most stable win, then the highest quality, then the lowest cost.'
            )
          : t(
              'Composite score = price × {{price}} + quality × {{quality}} + service × {{service}}. The price score depends on each request (resolution, duration, references and selling price), so use the scheduling simulator to rank channels for a concrete request. Only the highest priority with an eligible channel competes.',
              {
                price: formatNumber(data.price_weight, locale),
                quality: formatNumber(data.quality_weight, locale),
                service: formatNumber(data.service_weight, locale),
              }
            )}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Requests served counts the channel that finally handled each request in this time range; the share is within the same model.'
        )}
      </p>
      <StaticDataTable
        columns={columns}
        data={data.rows}
        getRowKey={(row) => `${row.channel_id}:${row.model}`}
        emptyContent={t('No records')}
      />
    </section>
  )
}
