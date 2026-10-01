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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber, formatPercent } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getAuditDetail } from '../api'
import { auditOutcomeLabel } from '../lib/outcome'
import type { AuditCandidate, AuditDecision } from '../types'

type CandidateInput = {
  ID: number
  Capacity: number
  InFlight: number
  GroupCapacity: number
  GroupInFlight: number
  Submit: { rate: number; samples: number }
  Gen: { rate: number; samples: number }
}

function SelectionDetails({ decision }: { decision: AuditDecision }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const money = (value: number | null | undefined) =>
    formatBillingCurrencyFromUSD(value, {
      locale,
      digitsSmall: 6,
      digitsLarge: 6,
      abbreviate: false,
    })
  let candidates: AuditCandidate[] = []
  let inputs: CandidateInput[] = []
  let invalid = false
  try {
    const board: unknown = JSON.parse(decision.board_json || '[]')
    if (
      !Array.isArray(board) ||
      board.some((row) => !row || typeof row.id !== 'number')
    ) {
      throw new Error('Invalid board')
    }
    candidates = board as AuditCandidate[]
    const input = JSON.parse(decision.input_json || '{}') as {
      Candidates?: CandidateInput[]
    }
    inputs = Array.isArray(input.Candidates) ? input.Candidates : []
  } catch {
    invalid = true
  }
  const byID = new Map(inputs.map((input) => [input.ID, input]))
  const candidateColumns: StaticDataTableColumn<AuditCandidate>[] = [
    {
      id: 'channel',
      header: t('Channel'),
      cell: (row) => (
        <span className='break-words'>
          {row.name} · {formatNumber(row.id, locale)}{' '}
          {row.id === decision.selected && (
            <StatusBadge copyable={false} variant='success'>
              {t('Actual channel')}
            </StatusBadge>
          )}
        </span>
      ),
    },
    {
      id: 'result',
      header: t('Selection'),
      cell: (row) =>
        row.excluded ||
        (row.id === decision.recommended ? t('Recommended channel') : '—'),
    },
    {
      id: 'cost',
      header: t('Estimated cost'),
      cell: (row) => (
        <span>
          {money(row.cost_usd)}
          <span className='text-muted-foreground block text-xs'>
            {t('Base cost')}: {money(row.base_cost_usd)} · {t('Reference cost')}
            : {money(row.reference_cost_usd)}
          </span>
        </span>
      ),
    },
    {
      id: 'sell',
      header: t('Selling price'),
      cell: (row) => {
        let sell = t('Unknown')
        if (row.sell_kind === 'free') {
          sell = money(0)
        } else if (row.sell_kind === 'known') {
          sell = money(row.sell_usd)
        }
        return (
          <span>
            {sell}
            {row.sell_estimated && (
              <span className='block text-xs'>{t('Estimated')}</span>
            )}
          </span>
        )
      },
    },
    {
      id: 'score',
      header: t('Score'),
      cell: (row) => (
        <span>
          {formatNumber(row.total, locale)}
          <span className='text-muted-foreground block text-xs'>
            P {formatNumber(row.p, locale)} · Q {formatNumber(row.q, locale)} ·
            S {formatNumber(row.s, locale)}
          </span>
        </span>
      ),
    },
    {
      id: 'health',
      header: t('Health samples'),
      cell: (row) => {
        const input = byID.get(row.id)
        return input ? (
          <span>
            {formatPercent(input.Submit.rate * 100)} (
            {formatNumber(input.Submit.samples, locale)})
            <span className='block'>
              {formatPercent(input.Gen.rate * 100)} (
              {formatNumber(input.Gen.samples, locale)})
            </span>
          </span>
        ) : (
          '—'
        )
      },
    },
    {
      id: 'capacity',
      header: t('In flight / capacity'),
      cell: (row) => {
        const input = byID.get(row.id)
        return input
          ? `${formatNumber(input.InFlight, locale)} / ${input.Capacity || '∞'} · ${formatNumber(input.GroupInFlight, locale)} / ${input.GroupCapacity || '∞'}`
          : '—'
      },
    },
  ]
  return (
    <div className='space-y-3'>
      {(!decision.snapshot_complete || invalid) && (
        <Alert>
          <AlertDescription>
            {t(
              'Snapshot incomplete. Historical inputs cannot be fully reconstructed.'
            )}
          </AlertDescription>
        </Alert>
      )}
      <dl className='grid grid-cols-2 gap-3 text-sm sm:grid-cols-4'>
        {[
          [t('Group'), decision.actual_group],
          [t('Recommended channel'), decision.recommended || '—'],
          [t('Actual channel'), decision.selected || t('Not executed')],
          [t('Submit result'), auditOutcomeLabel(decision.submit_outcome, t)],
          [t('Admission'), decision.admission || '—'],
          [t('Health attribution'), decision.health_outcome || '—'],
          [t('Error source'), decision.error_source || '—'],
          [t('Status code'), decision.status_code || '—'],
        ].map(([label, value]) => (
          <div key={label}>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd className='break-words'>{value}</dd>
          </div>
        ))}
      </dl>
      <StaticDataTable
        data={candidates}
        getRowKey={(row) => row.id}
        className='hidden md:block'
        columns={candidateColumns}
      />
      <Accordion multiple className='md:hidden'>
        {candidates.map((candidate) => (
          <AccordionItem key={candidate.id} value={candidate.id}>
            <AccordionTrigger>
              {candidate.name} · {formatNumber(candidate.id, locale)}
            </AccordionTrigger>
            <AccordionContent>
              <dl className='grid grid-cols-2 gap-3 text-sm'>
                {candidateColumns.slice(1).map((column) => (
                  <div key={column.id}>
                    <dt className='text-muted-foreground'>{column.header}</dt>
                    <dd>{column.cell?.(candidate, 0)}</dd>
                  </div>
                ))}
              </dl>
            </AccordionContent>
          </AccordionItem>
        ))}
      </Accordion>
      <Accordion>
        <AccordionItem value='snapshot'>
          <AccordionTrigger>
            {t('Decision snapshot and versions')}
          </AccordionTrigger>
          <AccordionContent>
            <dl className='grid grid-cols-1 gap-2 text-xs sm:grid-cols-2'>
              {[
                [t('Scheduler version'), decision.scheduler_version],
                [t('Build version'), decision.build_version],
                [t('Configuration version'), decision.config_version],
                [t('Fingerprint'), decision.fingerprint],
              ].map(([label, value]) => (
                <div key={label}>
                  <dt className='text-muted-foreground'>{label}</dt>
                  <dd className='flex min-w-0 items-center gap-1'>
                    <code className='break-all'>{value}</code>
                    <CopyButton value={value} />
                  </dd>
                </div>
              ))}
            </dl>
            <CopyButton value={decision.input_json} size='sm'>
              {t('Copy snapshot')}
            </CopyButton>
            <pre className='bg-muted max-h-80 overflow-auto rounded-md p-3 text-xs break-all whitespace-pre-wrap'>
              {decision.input_json || t('No data')}
            </pre>
            <pre className='text-muted-foreground mt-2 text-xs break-all whitespace-pre-wrap'>
              {decision.plugins_json}
            </pre>
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </div>
  )
}

export function AuditDetailDialog({
  requestId,
  onClose,
}: {
  requestId: string
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['video-scheduling-audit', userId, 'detail', requestId],
    queryFn: () => getAuditDetail(requestId),
    retry: false,
  })
  const run = query.data?.run
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
      title={t('Scheduling audit details')}
      description={requestId}
      contentClassName='sm:max-w-6xl'
      contentHeight='75vh'
    >
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          description={getServerErrorMessage(query.error)}
          onRetry={() => void query.refetch()}
        />
      )}
      {!query.isPending && !query.isError && query.data && run && (
        <div className='space-y-4'>
          <div className='flex flex-wrap items-center gap-2'>
            <StatusBadge copyable={false}>
              {auditOutcomeLabel(
                run.terminal_class === 'cancelled'
                  ? 'cancelled'
                  : run.task_status || run.request_outcome,
                t
              )}
            </StatusBadge>
            <CopyButton value={run.request_id} size='sm'>
              {t('Request ID')}
            </CopyButton>
            {run.task_id ? (
              <Link
                className='text-primary underline'
                to='/usage-logs/$section'
                params={{ section: 'task' }}
                search={{ filter: run.task_id }}
              >
                {t('View task')}
              </Link>
            ) : (
              <span className='text-muted-foreground text-sm'>
                {t('No linked task')}
              </span>
            )}
          </div>
          <p className='text-muted-foreground text-sm'>
            {new Date(run.started_at).toLocaleString(locale)} →{' '}
            {run.terminal_observed_at
              ? new Date(run.terminal_observed_at).toLocaleString(locale)
              : t('Terminal result pending or unknown')}
          </p>
          {(run.data_issue || run.assembly_error) && (
            <Alert>
              <AlertDescription>
                {t('Data incomplete')}:{' '}
                <code>{run.data_issue || run.assembly_error}</code>
              </AlertDescription>
            </Alert>
          )}
          <StaticDataTable
            data={query.data.attempts}
            getRowKey={(row) => row.attempt}
            columns={[
              {
                id: 'attempt',
                header: t('Attempt'),
                cell: (row) => formatNumber(row.attempt, locale),
              },
              {
                id: 'channel',
                header: t('Actual channel'),
                cell: (row) => formatNumber(row.channel, locale),
              },
              {
                id: 'result',
                header: t('Submit result'),
                cell: (row) => auditOutcomeLabel(row.outcome, t),
              },
              {
                id: 'source',
                header: t('Error source'),
                cell: (row) => row.error_source || '—',
              },
              {
                id: 'health',
                header: t('Health attribution'),
                cell: (row) => row.health || '—',
              },
            ]}
          />
          {query.data.decisions.length === 0 ? (
            <EmptyState title={t('No recorded selections')} />
          ) : (
            <Accordion
              multiple
              defaultValue={[query.data.decisions[0].selection_seq]}
            >
              {query.data.decisions.map((decision) => (
                <AccordionItem
                  key={decision.selection_seq}
                  value={decision.selection_seq}
                >
                  <AccordionTrigger>
                    <span>
                      {t('Selection {{selection}} · attempt {{attempt}}', {
                        selection: decision.selection_seq,
                        attempt: decision.attempt_seq,
                      })}{' '}
                      ·{' '}
                      {{
                        normal: t('Selection'),
                        probe: t('Probe'),
                        explore: t('Exploration'),
                      }[decision.choice_kind] || t('Unknown')}{' '}
                      {decision.affinity_hit && `· ${t('Affinity hit')}`}
                    </span>
                  </AccordionTrigger>
                  <AccordionContent>
                    <SelectionDetails decision={decision} />
                  </AccordionContent>
                </AccordionItem>
              ))}
            </Accordion>
          )}
        </div>
      )}
    </Dialog>
  )
}
