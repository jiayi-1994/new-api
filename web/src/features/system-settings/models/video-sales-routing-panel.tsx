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
import { lazy, Suspense, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { getTaskPluginOptions } from '@/features/channels/api'
import { ChannelsProvider } from '@/features/channels/components/channels-provider'
import type { Channel } from '@/features/channels/types'
import type { VideoSalesModel } from '@/features/pricing/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  asciiFoldVideoModelName,
  canonicalVideoSalesTier,
} from './video-sales-config'
import {
  getVideoSalesChannels,
  inspectVideoSalesRoute,
  type VideoSalesRoute,
} from './video-sales-routing'

const ChannelMutateDrawer = lazy(async () => {
  const module =
    await import('@/features/channels/components/drawers/channel-mutate-drawer')
  return { default: module.ChannelMutateDrawer }
})

export function VideoSalesRoutingPanel(props: {
  modelName: string
  sales: VideoSalesModel
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [editing, setEditing] = useState<Channel | null>(null)
  const channels = useQuery({
    queryKey: ['channels', 'video-sales-routing'],
    queryFn: getVideoSalesChannels,
    meta: { errorToast: false },
  })
  const plugins = useQuery({
    queryKey: ['task-plugin-options'],
    queryFn: getTaskPluginOptions,
    meta: { errorToast: false },
  })
  const loading = channels.isPending || plugins.isPending
  const error = channels.error || plugins.error
  const referenceKinds: Record<string, string> = {
    video: t('Video'),
    image: t('Image'),
    audio: t('Audio'),
  }
  const referenceModes = {
    unsupported: t('Unsupported'),
    included: t('Included in base price'),
    per_request: t('Per request'),
    per_input: t('Per input'),
    per_output_second: t('Per output second'),
    per_input_second: t('Per input video second'),
    multiplier: t('Multiplier'),
  }
  const modelName = asciiFoldVideoModelName(props.modelName)
  const routes = (channels.data ?? [])
    .filter((channel) =>
      channel.models
        .split(',')
        .some((name) => asciiFoldVideoModelName(name.trim()) === modelName)
    )
    .map((channel) =>
      inspectVideoSalesRoute(
        channel,
        props.modelName,
        props.sales,
        plugins.data ?? []
      )
    )
  const uncovered = Object.entries(props.sales.resolutions).flatMap(
    ([resolution, sale]) => {
      const tier = canonicalVideoSalesTier(resolution)
      const seconds = sale.seconds.filter(
        (duration) =>
          !routes.some((route) =>
            route.coverage.some(
              (coverage) =>
                coverage.tier === tier && coverage.seconds.includes(duration)
            )
          )
      )
      return seconds.length
        ? [
            `${tier ?? resolution}: ${seconds.map((value) => t('{{seconds}} s', { seconds: formatNumber(value, locale) })).join(', ')}`,
          ]
        : []
    }
  )
  const columns: StaticDataTableColumn<VideoSalesRoute>[] = [
    {
      id: 'channel',
      header: t('Channel'),
      cellClassName: 'whitespace-normal',
      cell: (route) => (
        <div className='space-y-1'>
          <p className='font-medium break-all'>{route.channel.name}</p>
          <p className='text-muted-foreground text-xs'>#{route.channel.id}</p>
          <StatusBadge
            copyable={false}
            variant={route.channel.status === 1 ? 'success' : 'neutral'}
            label={route.channel.status === 1 ? t('Enabled') : t('Disabled')}
          />
        </div>
      ),
    },
    {
      id: 'target',
      header: t('Bound plugins / upstream target'),
      cellClassName: 'whitespace-normal',
      cell: (route) => (
        <div className='space-y-1 break-all'>
          <p>{route.plugin || '—'}</p>
          <p className='text-muted-foreground'>{route.target || '—'}</p>
          {route.issue && (
            <p className='text-destructive text-xs'>{t(route.issue)}</p>
          )}
        </div>
      ),
    },
    {
      id: 'procurement',
      header: t('Procurement cost'),
      cellClassName: 'whitespace-normal',
      cell: (route) => {
        if (!route.cost) return <span>—</span>
        return (
          <div className='space-y-1'>
            <p className='font-medium'>{t('Base generation cost')}</p>
            <p>
              {route.cost.mode === 'per_second'
                ? t('Per second')
                : t('Per video')}
            </p>
            {Object.entries(route.cost.prices).map(([tier, price]) => (
              <p key={tier} className='text-muted-foreground text-xs'>
                {tier}:{' '}
                {formatBillingCurrencyFromUSD(price, {
                  locale,
                  digitsSmall: 6,
                })}
              </p>
            ))}
            {Object.keys(route.cost.references ?? {}).length > 0 && (
              <p className='pt-1 font-medium'>{t('Reference surcharges')}</p>
            )}
            {Object.entries(route.cost.references ?? {}).flatMap(
              ([kind, tiers]) =>
                Object.entries(tiers).map(([tier, rule]) => {
                  let price = ''
                  if (rule.mode !== 'included' && rule.mode !== 'unsupported') {
                    price =
                      rule.value === undefined
                        ? '—'
                        : formatBillingCurrencyFromUSD(rule.value, {
                            locale,
                            digitsSmall: 6,
                          })
                    if (
                      rule.mode === 'multiplier' &&
                      rule.value !== undefined
                    ) {
                      price = `×${new Intl.NumberFormat(locale, { maximumSignificantDigits: 15 }).format(rule.value)}`
                    }
                  }
                  return (
                    <p
                      key={`${kind}/${tier}`}
                      className='text-muted-foreground text-xs'
                    >
                      {referenceKinds[kind]} {tier}: {referenceModes[rule.mode]}{' '}
                      {price}
                    </p>
                  )
                })
            )}
          </div>
        )
      },
    },
    {
      id: 'coverage',
      header: t('Configured sales coverage'),
      cellClassName: 'whitespace-normal',
      cell: (route) => {
        if (route.uncertain) {
          return (
            <span className='text-muted-foreground'>
              {t('Routing requires runtime confirmation')}
            </span>
          )
        }
        return (
          <div className='space-y-1'>
            {route.coverage.length ? (
              route.coverage.map((coverage) => (
                <p key={coverage.tier}>
                  {coverage.tier}:{' '}
                  {coverage.seconds
                    .map((value) =>
                      t('{{seconds}} s', {
                        seconds: formatNumber(value, locale),
                      })
                    )
                    .join(', ')}
                </p>
              ))
            ) : (
              <span className='text-muted-foreground'>
                {t('No configured coverage')}
              </span>
            )}
          </div>
        )
      },
    },
    {
      id: 'capacity',
      header: t('Capacity group'),
      cellClassName: 'whitespace-normal break-all',
      cell: (route) => <span>{route.capacityGroup || '—'}</span>,
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: (route) => (
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => setEditing(route.channel)}
          aria-label={t('Edit channel {{name}}', { name: route.channel.name })}
        >
          {t('Edit channel')}
        </Button>
      ),
    },
  ]

  let content: ReactNode
  if (error) {
    content = (
      <ErrorState
        className='min-h-32'
        title={t('Failed to load channel routing')}
        description={getServerErrorMessage(error)}
        onRetry={() => {
          void channels.refetch()
          void plugins.refetch()
        }}
      />
    )
  } else if (loading) {
    content = (
      <LoadingState
        className='min-h-32'
        message={t('Loading channel routing...')}
      />
    )
  } else {
    content = (
      <>
        {routes.some(
          (route) => route.uncertain && route.channel.status === 1
        ) ? (
          <Alert>
            <AlertTitle>
              {t('Routing requires runtime confirmation')}
            </AlertTitle>
            <AlertDescription>
              {t(
                'Some model aliases depend on server settings. Configuration coverage cannot be fully determined here.'
              )}
            </AlertDescription>
          </Alert>
        ) : (
          uncovered.length > 0 && (
            <Alert>
              <AlertTitle>
                {t('Sales combinations without configured channel coverage')}
              </AlertTitle>
              <AlertDescription>{uncovered.join(' · ')}</AlertDescription>
            </Alert>
          )
        )}
        {routes.length === 0 ? (
          <EmptyState
            className='min-h-32'
            title={t('No channels expose this sales model')}
            description={t(
              'Add the public sales model to a channel and configure its upstream mapping and procurement cost.'
            )}
          />
        ) : (
          <StaticDataTable
            data={routes}
            columns={columns}
            getRowKey={(route) => route.channel.id}
            tableClassName='min-w-[850px]'
            tableProps={{ 'aria-label': t('Video channel routing') }}
          />
        )}
      </>
    )
  }

  return (
    <div className='min-w-0 space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h4 className='font-medium'>{t('Video channel routing')}</h4>
        <Button
          type='button'
          size='sm'
          variant='ghost'
          disabled={channels.isFetching || plugins.isFetching}
          onClick={() => {
            void channels.refetch()
            void plugins.refetch()
          }}
        >
          {t('Refresh')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Configuration coverage is for requests without references. Plugin support, account capacity, health, and profitability are checked at request time.'
        )}
      </p>
      {content}
      {editing && (
        <Suspense
          fallback={
            <LoadingState inline message={t('Loading channel editor...')} />
          }
        >
          <ChannelsProvider>
            <ChannelMutateDrawer
              open
              onOpenChange={(open) => {
                if (!open) setEditing(null)
              }}
              currentRow={editing}
            />
          </ChannelsProvider>
        </Suspense>
      )}
    </div>
  )
}
