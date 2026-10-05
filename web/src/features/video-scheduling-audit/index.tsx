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
import { getRouteApi } from '@tanstack/react-router'
import type { ColumnDef } from '@tanstack/react-table'
import { lazy, Suspense, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getAuditList, getAuditStats } from './api'
import { AuditExportButton } from './components/audit-export-button'
import { AuditFilterBar } from './components/audit-filter-bar'
import { AuditOverview } from './components/audit-overview'
import { ChannelOverview } from './components/channel-overview'
import { auditOutcomeLabel } from './lib/outcome'
import type { AuditFilters, AuditRun } from './types'

const AuditDetailDialog = lazy(() =>
  import('./components/audit-detail-dialog').then((module) => ({
    default: module.AuditDetailDialog,
  }))
)
const EMPTY: AuditRun[] = []
const route = getRouteApi('/_authenticated/video-scheduling/audit')

export function VideoSchedulingAudit() {
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const [end] = useState(Date.now)
  const filters = {
    ...search,
    start: search.start ?? end - 86400000,
    end: search.end ?? end,
  }
  return (
    <AuditViewer
      filters={filters}
      onChange={(next) => void navigate({ search: next })}
    />
  )
}

export function AuditViewer({
  filters,
  onChange,
}: {
  filters: AuditFilters
  onChange: (filters: AuditFilters) => void
}) {
  const { t, i18n } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const root = user?.role === ROLE.SUPER_ADMIN
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [detail, setDetail] = useState('')
  const valid =
    !!filters.start &&
    !!filters.end &&
    filters.end > filters.start &&
    filters.end - filters.start <= 31 * 86400000
  const enabled = root && valid
  const list = useQuery({
    queryKey: ['video-scheduling-audit', user?.id, 'list', filters],
    queryFn: () => getAuditList(filters),
    enabled,
    retry: false,
  })
  const statsFilters = { ...filters, page: 1, page_size: 25 }
  const stats = useQuery({
    queryKey: ['video-scheduling-audit', user?.id, 'stats', statsFilters],
    queryFn: () => getAuditStats(statsFilters),
    enabled,
    retry: false,
  })
  const columns = useMemo<ColumnDef<AuditRun>[]>(
    () => [
      {
        accessorKey: 'started_at',
        header: t('Start time'),
        cell: ({ row }) => (
          <span className='text-xs whitespace-nowrap'>
            {new Date(row.original.started_at).toLocaleString(locale)}
          </span>
        ),
      },
      {
        accessorKey: 'request_id',
        header: t('Request ID'),
        cell: ({ row }) => (
          <div className='flex min-w-0 items-center gap-1'>
            <Button
              variant='link'
              className='h-auto max-w-60 truncate p-0'
              onClick={() => setDetail(row.original.request_id)}
            >
              {row.original.request_id}
            </Button>
            <CopyButton value={row.original.request_id} />
          </div>
        ),
      },
      {
        accessorKey: 'model_name',
        header: t('Model'),
        cell: ({ row }) => (
          <span className='break-words'>
            {row.original.model_name}
            <span className='text-muted-foreground block text-xs'>
              {row.original.actual_group} ·{' '}
              {row.original.resolution || t('Unknown')} ·{' '}
              {formatNumber(row.original.output_seconds, locale)} s
            </span>
          </span>
        ),
      },
      {
        accessorKey: 'selected_channel',
        header: t('Actual channel'),
        cell: ({ row }) => row.original.selected_channel || '—',
      },
      {
        accessorKey: 'submit_attempts',
        header: t('Submit attempts'),
        cell: ({ row }) => formatNumber(row.original.submit_attempts, locale),
      },
      {
        id: 'result',
        header: t('Status'),
        cell: ({ row }) => (
          <div className='space-y-1'>
            <StatusBadge
              copyable={false}
              variant={
                row.original.task_status === 'SUCCESS' ? 'success' : 'neutral'
              }
            >
              {auditOutcomeLabel(
                row.original.terminal_class === 'cancelled'
                  ? 'cancelled'
                  : row.original.task_status || row.original.request_outcome,
                t
              )}
            </StatusBadge>
            {!row.original.snapshot_complete && (
              <span className='text-warning block text-xs'>
                {t('Data incomplete')}
              </span>
            )}
            <span className='text-muted-foreground block max-w-48 truncate text-xs'>
              {row.original.task_id || t('No linked task')}
            </span>
          </div>
        ),
      },
      {
        accessorKey: 'duration_ms',
        header: t('Observed duration'),
        cell: ({ row }) =>
          row.original.duration_ms === null
            ? '—'
            : `${formatNumber(row.original.duration_ms / 1000, locale)} s`,
      },
      {
        accessorKey: 'cost_usd',
        header: t('Estimated cost'),
        cell: ({ row }) =>
          formatBillingCurrencyFromUSD(row.original.cost_usd, {
            locale,
            digitsSmall: 6,
            digitsLarge: 6,
            abbreviate: false,
          }),
      },
    ],
    [t, locale]
  )
  const { table } = useDataTable({
    columns,
    data: list.isError || !enabled ? EMPTY : (list.data?.items ?? EMPTY),
    totalCount: list.isError ? 0 : (list.data?.total ?? 0),
    getRowId: (row) => row.request_id,
    enableSorting: false,
    enableRowSelection: false,
    manualPagination: true,
    manualFiltering: true,
    pagination: { pageIndex: filters.page - 1, pageSize: filters.page_size },
    onPaginationChange: (updater) => {
      if (list.isFetching || list.isError) return
      const next =
        typeof updater === 'function'
          ? updater({
              pageIndex: filters.page - 1,
              pageSize: filters.page_size,
            })
          : updater
      onChange({
        ...filters,
        page: next.pageSize === filters.page_size ? next.pageIndex + 1 : 1,
        page_size: next.pageSize,
      })
    },
  })
  if (!root) return <ErrorState title={t('Access denied')} />
  const collection = list.data?.collection
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Video scheduling audit')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {valid && (
          <AuditExportButton
            key={JSON.stringify(statsFilters)}
            filters={statsFilters}
          />
        )}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex min-h-0 flex-col gap-4'>
          <Alert>
            <AlertDescription className='space-y-1'>
              <p>
                {t(
                  'Only enrolled on/shadow requests are recorded. Historical collection completeness is unknown.'
                )}
              </p>
              {collection && (
                <p className='text-xs'>
                  {t('Collection node')}: {collection.node} · {t('Started')}:{' '}
                  {new Date(collection.started_at).toLocaleString(locale)} ·{' '}
                  {t('Queued')}: {formatNumber(collection.pending, locale)} ·{' '}
                  {t('Dropped')}: {formatNumber(collection.dropped, locale)} ·{' '}
                  {t('Write failures')}:{' '}
                  {formatNumber(collection.write_failures, locale)} ·{' '}
                  {t('Incomplete snapshots')}:{' '}
                  {formatNumber(collection.truncated, locale)}
                </p>
              )}
              {collection?.last_issue_at ? (
                <p className='text-warning text-xs'>
                  {t('Collection issues first / last')}:{' '}
                  {new Date(
                    collection.first_issue_at || collection.last_issue_at
                  ).toLocaleString(locale)}{' '}
                  → {new Date(collection.last_issue_at).toLocaleString(locale)}{' '}
                  · <code>{collection.last_issue}</code>
                </p>
              ) : null}
              {list.data && !list.data.audit_enabled && (
                <p>
                  {t(
                    'New audit collection is disabled. Enrolled tasks still receive terminal updates.'
                  )}
                </p>
              )}
              {collection && !collection.running && (
                <p>{t('Audit writer is not running')}</p>
              )}
            </AlertDescription>
          </Alert>
          {stats.isError && (
            <ErrorState
              title={t('Statistics unavailable')}
              description={getServerErrorMessage(stats.error)}
              onRetry={() => void stats.refetch()}
            />
          )}
          {stats.isPending && enabled && (
            <LoadingState inline message={t('Loading...')} />
          )}
          {!stats.isError && valid && stats.data && (
            <AuditOverview stats={stats.data} />
          )}
          {valid && <ChannelOverview filters={statsFilters} />}
          <Accordion defaultValue={valid ? [] : ['filters']}>
            <AccordionItem value='filters'>
              <AccordionTrigger>
                {t('Filters')} · {filters.mode === 'on' ? t('On') : t('Shadow')}
              </AccordionTrigger>
              <AccordionContent>
                <AuditFilterBar
                  key={JSON.stringify(filters)}
                  filters={filters}
                  onApply={onChange}
                  busy={list.isFetching}
                />
              </AccordionContent>
            </AccordionItem>
          </Accordion>
          {list.isError && (
            <ErrorState
              title={t('Failed to load audit records')}
              description={getServerErrorMessage(list.error)}
              onRetry={() => void list.refetch()}
            />
          )}
          <DataTablePage
            table={table}
            columns={columns}
            isLoading={list.isPending && enabled}
            isFetching={list.isFetching}
            emptyTitle={t('No records')}
            toolbarProps={null}
            paginationInFooter
            className='min-h-80'
          />
          {detail && (
            <Suspense fallback={<LoadingState />}>
              <AuditDetailDialog
                requestId={detail}
                onClose={() => setDetail('')}
              />
            </Suspense>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
