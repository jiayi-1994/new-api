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
import { Table2 } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { videoReliabilityLabel } from '@/features/channels/lib/video-reliability'
import { getGroups } from '@/features/users/api'
import { toIntlLocale } from '@/i18n/languages'
import { formatPercent } from '@/lib/format'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { simulateVideoSchedule } from '../api'
import { useSystemOptions } from '../hooks/use-system-options'
import { parseVideoSales } from '../models/video-sales-config'
import type {
  VideoScheduleCandidate,
  VideoScheduleSimulation,
  VideoSchedulingSetting,
} from '../types'
import {
  buildVideoScheduleMatrixCells,
  parseReferenceSeconds,
  videoCandidateProfit,
  videoExclusionCategory,
  videoScheduleMatrixRequest,
  videoScheduleMatrixVerdict,
  type VideoScheduleMatrixCell,
  type VideoScheduleMatrixVerdict,
} from './video-schedule-matrix'

type VideoScheduleMatrixDialogProps = {
  /** The settings form's current values, sent when the snapshot is enabled. */
  getConfigSnapshot: () => VideoSchedulingSetting
}

type MatrixRow = {
  cell: VideoScheduleMatrixCell
  result?: VideoScheduleSimulation
  error?: string
}

type RowVerdict = VideoScheduleMatrixVerdict | 'error' | 'pending'

// Each cell runs plugin hooks for every candidate channel; a few at a time
// keeps the server responsive while a full table finishes in seconds.
const CONCURRENCY = 4

function rowVerdict(row: MatrixRow): RowVerdict {
  if (row.error) return 'error'
  if (!row.result) return 'pending'
  return videoScheduleMatrixVerdict(row.result)
}

const VERDICT_VARIANT: Record<RowVerdict, StatusVariant> = {
  ok: 'success',
  config: 'warning',
  loss: 'danger',
  no_channel: 'danger',
  not_served: 'neutral',
  error: 'danger',
  pending: 'neutral',
}

const CATEGORY_CLASS = {
  config: 'text-destructive',
  runtime: 'text-warning',
  ranking: 'text-muted-foreground',
}

export function VideoScheduleMatrixDialog(
  props: VideoScheduleMatrixDialogProps
) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [open, setOpen] = useState(false)
  const [group, setGroup] = useState('default')
  const [userGroup, setUserGroup] = useState('')
  const [referenceInput, setReferenceInput] = useState('0')
  const [useSnapshot, setUseSnapshot] = useState(false)
  const [problemsOnly, setProblemsOnly] = useState(false)
  const [inputError, setInputError] = useState('')
  const [rows, setRows] = useState<MatrixRow[]>([])
  const [running, setRunning] = useState(false)
  const runId = useRef(0)

  const groups = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
    enabled: open,
  })
  const options = useSystemOptions(open)
  const groupItems = (groups.data?.data ?? [])
    .filter((name) => name !== 'auto')
    .map((name) => ({ value: name, label: name }))

  const usd = new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: 6,
  })
  const money = (value: number | undefined) =>
    value === undefined ? '—' : usd.format(value)

  const verdictLabel: Record<RowVerdict, string> = {
    ok: t('OK'),
    config: t('Configuration issue'),
    loss: t('Loss'),
    no_channel: t('No schedulable channel'),
    not_served: t('Not served in this group'),
    error: t('Simulation failed'),
    pending: t('Pending'),
  }
  const categoryLabel = {
    config: t('Configuration'),
    runtime: t('Runtime state'),
    ranking: t('Ranked out'),
  }

  const stop = () => {
    runId.current += 1
    setRunning(false)
  }

  const run = async () => {
    setInputError('')
    const referenceSeconds = parseReferenceSeconds(referenceInput)
    if (!referenceSeconds) {
      setInputError(
        t('Reference video seconds must be numbers between 0 and 3600')
      )
      return
    }
    const raw = options.data?.data?.find(
      (option) => option.key === 'billing_setting.video_sales'
    )?.value
    const sales = parseVideoSales(raw ?? '{}')
    if (!sales) {
      setInputError(t('The unified video price table is invalid'))
      return
    }
    const cells = buildVideoScheduleMatrixCells(sales, referenceSeconds)
    if (cells.length === 0) {
      setInputError(t('No enabled unified video models are configured'))
      return
    }
    let snapshot: VideoSchedulingSetting | undefined
    try {
      snapshot = useSnapshot ? props.getConfigSnapshot() : undefined
    } catch (error) {
      setInputError((error as Error).message)
      return
    }

    const id = ++runId.current
    setRows(cells.map((cell) => ({ cell })))
    setRunning(true)
    let next = 0
    const worker = async () => {
      while (next < cells.length && runId.current === id) {
        const index = next
        next += 1
        const request = videoScheduleMatrixRequest(
          cells[index],
          group,
          userGroup.trim(),
          snapshot
        )
        let row: MatrixRow
        try {
          const response = await simulateVideoSchedule(request)
          row = {
            cell: cells[index],
            result: requireServerSuccess(response).data,
          }
        } catch (error) {
          row = {
            cell: cells[index],
            error: getServerErrorMessage(error, t('Simulation failed')),
          }
        }
        if (runId.current !== id) return
        setRows((previous) =>
          previous.map((current, i) => (i === index ? row : current))
        )
      }
    }
    await Promise.all(Array.from({ length: CONCURRENCY }, worker))
    if (runId.current === id) setRunning(false)
  }

  const counts = rows.reduce<Partial<Record<RowVerdict, number>>>(
    (total, row) => {
      const verdict = rowVerdict(row)
      total[verdict] = (total[verdict] ?? 0) + 1
      return total
    },
    {}
  )
  const done = rows.length - (counts.pending ?? 0)
  const visibleRows = problemsOnly
    ? rows.filter(
        (row) => !['ok', 'pending', 'not_served'].includes(rowVerdict(row))
      )
    : rows
  // Off, shadow and unlisted models keep ordinary routing in live traffic,
  // so their simulated order describes what the scheduler would do.
  const notTakenOver = rows.filter(
    (row) => row.result && !row.result.decision.takeover
  ).length

  const profitText = (candidate: VideoScheduleCandidate) => {
    const profit = videoCandidateProfit(candidate)
    if (profit === undefined) return t('Profit unknown')
    const sell = candidate.sell_usd ?? 0
    const margin = sell > 0 ? ` (${formatPercent((profit / sell) * 100)})` : ''
    return `${money(profit)}${margin}`
  }
  const profitClass = (candidate: VideoScheduleCandidate) => {
    const profit = videoCandidateProfit(candidate)
    if (profit === undefined) return 'text-muted-foreground'
    return profit < 0 ? 'text-destructive' : 'text-success'
  }

  const columns = [
    {
      id: 'sale',
      header: t('Sale'),
      cell: (row: MatrixRow) => (
        <div className='min-w-0'>
          <div className='font-mono font-medium'>{row.cell.model}</div>
          <div className='text-muted-foreground text-xs'>
            {t('{{tier}} · {{seconds}} s · reference video {{reference}} s', {
              tier: row.cell.tier,
              seconds: row.cell.seconds,
              reference: row.cell.referenceSeconds,
            })}
          </div>
        </div>
      ),
    },
    {
      id: 'sell',
      header: t('Sell price'),
      cell: (row: MatrixRow) => {
        const priced = row.result?.candidates.find(
          (candidate) => candidate.sell_kind === 'known'
        )
        return priced ? money(priced.sell_usd ?? 0) : '—'
      },
    },
    {
      id: 'recommended',
      header: t('Recommended channel'),
      cell: (row: MatrixRow) => {
        if (!row.result) return '—'
        const recommended = row.result.candidates.find(
          (candidate) => candidate.id === row.result?.recommended
        )
        if (!recommended) {
          return (
            <span className='text-destructive text-sm'>
              {t('No schedulable channel')}
            </span>
          )
        }
        return (
          <div className='text-sm'>
            <div className='font-medium'>
              #{recommended.id} {recommended.name}
            </div>
            <div className='text-xs'>
              {t('Cost')}: {money(recommended.cost_usd)}
            </div>
            <div className={`text-xs ${profitClass(recommended)}`}>
              {t('Profit')}: {profitText(recommended)}
            </div>
            {(row.result.probe || row.result.explore) && (
              <div className='text-warning text-xs'>
                {row.result.probe ? t('Probe') : t('Exploration')}
              </div>
            )}
          </div>
        )
      },
    },
    {
      id: 'order',
      header: t('Scheduling order'),
      cell: (row: MatrixRow) => {
        if (row.error) {
          return <span className='text-destructive text-xs'>{row.error}</span>
        }
        if (!row.result) return '—'
        if (row.result.candidates.length === 0) {
          return (
            <span className='text-muted-foreground text-xs'>
              {t('No channel in this group serves the model')}
            </span>
          )
        }
        return (
          <ol className='flex flex-col gap-1 text-xs'>
            {row.result.candidates.map((candidate, index) => {
              const category = candidate.excluded
                ? videoExclusionCategory(candidate.excluded)
                : undefined
              return (
                <li key={candidate.id}>
                  <span className='font-medium'>
                    {index + 1}. #{candidate.id} {candidate.name}
                  </span>{' '}
                  <span className='text-muted-foreground'>
                    {money(candidate.cost_usd)}
                  </span>{' '}
                  <span className={profitClass(candidate)}>
                    {profitText(candidate)}
                  </span>{' '}
                  {category ? (
                    <span className={CATEGORY_CLASS[category]}>
                      [{categoryLabel[category]}]{' '}
                      {videoReliabilityLabel(candidate.excluded, t)}
                    </span>
                  ) : (
                    <span className='text-success'>[{t('Eligible')}]</span>
                  )}
                </li>
              )
            })}
          </ol>
        )
      },
    },
    {
      id: 'verdict',
      header: t('Result'),
      cell: (row: MatrixRow) => {
        const verdict = rowVerdict(row)
        return (
          <div className='flex flex-col gap-1'>
            <StatusBadge
              label={verdictLabel[verdict]}
              variant={VERDICT_VARIANT[verdict]}
              copyable={false}
            />
            {row.result?.decision.reason && (
              <span className='text-muted-foreground font-mono text-xs'>
                {row.result.decision.reason}
              </span>
            )}
          </div>
        )
      },
    },
  ]

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) stop()
        setOpen(next)
      }}
      title={t('Group scheduling matrix')}
      description={t(
        'Simulates every unified video sale of a group with live health and pricing, to find loss-making or misconfigured combinations'
      )}
      contentClassName='sm:max-w-7xl'
      trigger={
        <Button type='button' variant='outline' className='self-start'>
          <Table2 aria-hidden='true' />
          {t('Group scheduling matrix')}
        </Button>
      }
      footer={
        running ? (
          <Button type='button' variant='outline' onClick={stop}>
            {t('Stop ({{done}}/{{total}})', { done, total: rows.length })}
          </Button>
        ) : (
          <Button
            type='button'
            onClick={() => void run()}
            disabled={!group || options.isPending}
          >
            {t('Run matrix')}
          </Button>
        )
      }
    >
      <div className='flex flex-col gap-4'>
        <div className='grid gap-3 sm:grid-cols-3'>
          <div className='flex flex-col gap-1.5'>
            <Label>{t('Group')}</Label>
            <Select
              items={groupItems}
              value={group}
              onValueChange={(value) => setGroup(value ?? '')}
            >
              <SelectTrigger aria-label={t('Group')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                {groupItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='video-matrix-user-group'>
              {t('User group (optional)')}
            </Label>
            <Input
              id='video-matrix-user-group'
              value={userGroup}
              onChange={(event) => setUserGroup(event.target.value)}
            />
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='video-matrix-reference'>
              {t('Reference video seconds')}
            </Label>
            <Input
              id='video-matrix-reference'
              value={referenceInput}
              placeholder='0, 5, 10'
              onChange={(event) => setReferenceInput(event.target.value)}
            />
          </div>
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Reference video seconds is a comma-separated list; 0 sends no reference video. Each other value adds one reference video of that length, priced without being fetched.'
          )}
        </p>
        <div className='flex flex-wrap gap-4'>
          <Label className='flex items-center gap-2 font-normal'>
            <Checkbox
              checked={useSnapshot}
              onCheckedChange={(checked) => setUseSnapshot(checked === true)}
            />
            {t(
              'Use the unsaved settings on this page as the config snapshot (the window stays at the live value)'
            )}
          </Label>
          <Label className='flex items-center gap-2 font-normal'>
            <Checkbox
              checked={problemsOnly}
              onCheckedChange={(checked) => setProblemsOnly(checked === true)}
            />
            {t('Only show problems')}
          </Label>
        </div>

        {inputError && (
          <p role='alert' className='text-destructive text-sm'>
            {inputError}
          </p>
        )}

        {rows.length > 0 && (
          <p className='text-sm' aria-live='polite'>
            {t(
              '{{done}}/{{total}} simulated · {{ok}} OK · {{config}} configuration issues · {{loss}} losses · {{none}} without a channel · {{unserved}} not served · {{error}} failed',
              {
                done,
                total: rows.length,
                ok: counts.ok ?? 0,
                config: counts.config ?? 0,
                loss: counts.loss ?? 0,
                none: counts.no_channel ?? 0,
                unserved: counts.not_served ?? 0,
                error: counts.error ?? 0,
              }
            )}
          </p>
        )}

        {notTakenOver > 0 && (
          <p className='text-warning text-sm'>
            {t(
              '{{count}} sales are not taken over by the live scheduler (off, shadow or not listed); live traffic routes them by ordinary channel selection',
              { count: notTakenOver }
            )}
          </p>
        )}

        {rows.length > 0 && (
          <StaticDataTable
            columns={columns}
            data={visibleRows}
            getRowKey={(row) =>
              `${row.cell.model}|${row.cell.tier}|${row.cell.seconds}|${row.cell.referenceSeconds}`
            }
            getRowClassName={(row) => {
              const verdict = rowVerdict(row)
              if (verdict === 'config') return 'bg-warning/5'
              if (['loss', 'no_channel', 'error'].includes(verdict)) {
                return 'bg-destructive/5'
              }
              return undefined
            }}
            emptyContent={t('No problems found')}
          />
        )}
      </div>
    </Dialog>
  )
}
