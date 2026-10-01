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
import { useMutation } from '@tanstack/react-query'
import { FlaskConical } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { JsonCodeEditor } from '@/components/json-code-editor'
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
import { toIntlLocale } from '@/i18n/languages'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { simulateVideoSchedule } from '../api'
import type {
  VideoScheduleCandidate,
  VideoScheduleSimulateRequest,
  VideoSchedulingSetting,
} from '../types'

type VideoScheduleSimulatorDialogProps = {
  /** The settings form's current values, sent when the snapshot is enabled. */
  getConfigSnapshot: () => VideoSchedulingSetting
}

type SimulatorInput = {
  group: string
  userGroup: string
  entry: 'protocol' | 'native'
  protocol: string
  path: string
  pluginKey: string
  requestBody: string
  healthOverride: string
  inflightOverride: string
  slotOverride: string
  useSnapshot: boolean
  seed: string
  now: string
}

const INITIAL_INPUT: SimulatorInput = {
  group: 'default',
  userGroup: '',
  entry: 'protocol',
  protocol: 'openai_video',
  path: '',
  pluginKey: '',
  requestBody: '{\n  "model": "",\n  "prompt": "test"\n}',
  healthOverride: '',
  inflightOverride: '',
  slotOverride: '',
  useSnapshot: false,
  seed: '',
  now: '',
}

function parseOptionalJson(value: string, label: string) {
  if (!value.trim()) return undefined
  try {
    return JSON.parse(value) as Record<string, never>
  } catch {
    throw new Error(label)
  }
}

export function VideoScheduleSimulatorDialog(
  props: VideoScheduleSimulatorDialogProps
) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [open, setOpen] = useState(false)
  const [input, setInput] = useState(INITIAL_INPUT)
  const [inputError, setInputError] = useState('')
  const simulate = useMutation({
    mutationFn: async (request: VideoScheduleSimulateRequest) =>
      requireServerSuccess(await simulateVideoSchedule(request)).data,
    meta: { errorToast: false },
  })
  const set = <K extends keyof SimulatorInput>(
    key: K,
    value: SimulatorInput[K]
  ) => setInput((previous) => ({ ...previous, [key]: value }))

  const usd = new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: 6,
  })
  const score = new Intl.NumberFormat(locale, { maximumFractionDigits: 4 })
  const money = (value: number | undefined) =>
    value === undefined ? '—' : usd.format(value)

  const run = () => {
    setInputError('')
    let request: VideoScheduleSimulateRequest
    try {
      request = {
        group: input.group.trim(),
        user_group: input.userGroup.trim() || undefined,
        entry: input.entry,
        protocol:
          input.entry === 'protocol' ? input.protocol.trim() : undefined,
        path: input.path.trim() || undefined,
        plugin_key:
          input.entry === 'native' ? input.pluginKey.trim() : undefined,
        request_body: parseOptionalJson(
          input.requestBody,
          t('Request body must be valid JSON')
        ),
        health_override: parseOptionalJson(
          input.healthOverride,
          t('Health override must be valid JSON')
        ),
        inflight_override: parseOptionalJson(
          input.inflightOverride,
          t('In-flight override must be valid JSON')
        ),
        slot_override: parseOptionalJson(
          input.slotOverride,
          t('Probe slot override must be valid JSON')
        ),
        config_snapshot: input.useSnapshot
          ? props.getConfigSnapshot()
          : undefined,
        seed: input.seed.trim() ? Number(input.seed) : undefined,
        now: input.now.trim() || undefined,
      }
    } catch (error) {
      setInputError((error as Error).message)
      return
    }
    if (request.seed !== undefined && !Number.isSafeInteger(request.seed)) {
      setInputError(t('Seed must be a whole number'))
      return
    }
    simulate.mutate(request)
  }

  const result = simulate.data
  const specSource =
    result?.candidates.find(
      (candidate) => candidate.id === result.recommended && candidate.spec
    ) ?? result?.candidates.find((candidate) => candidate.spec)
  const spec = specSource?.spec

  const sellCell = (row: VideoScheduleCandidate) => {
    let text = '—'
    if (row.sell_kind === 'known') text = money(row.sell_usd ?? 0)
    if (row.sell_kind === 'free') text = t('Free')
    if (row.sell_kind === 'unknown') text = t('Unknown')
    return (
      <span>
        {text}
        {row.sell_estimated && (
          <span className='text-muted-foreground ml-1 text-xs'>
            ({t('Estimated')})
          </span>
        )}
      </span>
    )
  }

  const columns = [
    {
      id: 'channel',
      header: t('Channel'),
      cell: (row: VideoScheduleCandidate) => (
        <div className='min-w-0'>
          <div className='font-medium'>
            #{row.id} {row.name}
            {result?.recommended === row.id && (
              <span className='text-success ml-1 text-xs'>
                ({t('Recommended')})
              </span>
            )}
          </div>
          <div className='text-muted-foreground font-mono text-xs'>
            {row.plugin ?? '—'} · {row.mapped_model ?? '—'}
          </div>
        </div>
      ),
    },
    {
      id: 'tier',
      header: t('Tier'),
      cell: (row: VideoScheduleCandidate) => row.tier ?? '—',
    },
    {
      id: 'base',
      header: t('Base cost'),
      cell: (row: VideoScheduleCandidate) => money(row.base_cost_usd),
    },
    {
      id: 'reference',
      header: t('Reference fee'),
      cell: (row: VideoScheduleCandidate) => (
        <div>
          <div>{money(row.reference_cost_usd)}</div>
          {row.references?.map((line) => (
            <div
              key={line.kind}
              className='text-muted-foreground font-mono text-xs'
            >
              {line.kind} · {line.mode ?? '—'} · {line.tier ?? '—'} ·{' '}
              {line.quantity ?? '—'} × {line.value ?? '—'} = {money(line.usd)}
            </div>
          ))}
        </div>
      ),
    },
    {
      id: 'total-cost',
      header: t('Total cost'),
      cell: (row: VideoScheduleCandidate) => money(row.cost_usd),
    },
    { id: 'sell', header: t('Sell price'), cell: sellCell },
    {
      id: 'scores',
      header: t('P / Q / S / Total'),
      cell: (row: VideoScheduleCandidate) =>
        [row.p, row.q, row.s, row.total]
          .map((value) => score.format(value))
          .join(' / '),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (row: VideoScheduleCandidate) => (
        <div className='text-xs'>
          {row.excluded ? (
            <span className='text-destructive'>{row.excluded}</span>
          ) : (
            t('Eligible')
          )}
          {row.unproven && (
            <div className='text-muted-foreground'>{t('Unproven')}</div>
          )}
        </div>
      ),
    },
  ]

  const textField = (
    key:
      | 'group'
      | 'userGroup'
      | 'protocol'
      | 'path'
      | 'pluginKey'
      | 'seed'
      | 'now',
    label: string,
    placeholder?: string
  ) => (
    <div className='flex flex-col gap-1.5'>
      <Label htmlFor={`video-sim-${key}`}>{label}</Label>
      <Input
        id={`video-sim-${key}`}
        value={input[key]}
        placeholder={placeholder}
        onChange={(event) => set(key, event.target.value)}
      />
    </div>
  )
  const entryItems = [
    { value: 'protocol', label: t('Protocol entry') },
    { value: 'native', label: t('Native plugin route') },
  ]

  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Scheduling simulator')}
      description={t(
        'Same complete input yields the same recommendation; candidates and pricing use current values'
      )}
      contentClassName='sm:max-w-6xl'
      trigger={
        <Button type='button' variant='outline' className='self-start'>
          <FlaskConical aria-hidden='true' />
          {t('Open scheduling simulator')}
        </Button>
      }
      footer={
        <Button type='button' onClick={run} disabled={simulate.isPending}>
          {simulate.isPending ? t('Simulating...') : t('Simulate')}
        </Button>
      }
    >
      <div className='flex flex-col gap-4'>
        <div className='grid gap-3 sm:grid-cols-3'>
          {textField('group', t('Group'))}
          {textField('userGroup', t('User group (optional)'))}
          <div className='flex flex-col gap-1.5'>
            <Label>{t('Entry')}</Label>
            <Select
              items={entryItems}
              value={input.entry}
              onValueChange={(value) =>
                set('entry', value === 'native' ? 'native' : 'protocol')
              }
            >
              <SelectTrigger aria-label={t('Entry')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                {entryItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {input.entry === 'protocol'
            ? textField('protocol', t('Protocol (optional)'), 'openai_video')
            : textField('pluginKey', t('Plugin key'))}
          {textField(
            'path',
            input.entry === 'protocol'
              ? t('Path (optional, overrides protocol)')
              : t('Path'),
            '/v1/videos'
          )}
          {textField('seed', t('Seed (optional)'))}
          {textField(
            'now',
            t('Now (optional, RFC3339)'),
            '2026-10-01T00:00:00Z'
          )}
        </div>

        <div className='flex flex-col gap-1.5'>
          <Label>{t('Request body')}</Label>
          <JsonCodeEditor
            value={input.requestBody}
            onChange={(value) => set('requestBody', value)}
            ariaLabel={t('Request body')}
            heightClassName='h-40 min-h-40 max-h-40'
          />
        </div>

        <div className='grid gap-3 lg:grid-cols-3'>
          {(
            [
              [
                'healthOverride',
                t('Health override (optional)'),
                '{"12": {"submit": {"rate": 1, "samples": 30}}}',
              ],
              [
                'inflightOverride',
                t('In-flight override (optional)'),
                '{"channels": {"12": 1}, "groups": {}}',
              ],
              [
                'slotOverride',
                t('Probe slot override (optional)'),
                '{"12": 1}',
              ],
            ] as const
          ).map(([key, label, placeholder]) => (
            <div key={key} className='flex flex-col gap-1.5'>
              <Label>{label}</Label>
              <JsonCodeEditor
                value={input[key]}
                onChange={(value) => set(key, value)}
                ariaLabel={label}
                placeholder={placeholder}
                heightClassName='h-28 min-h-28 max-h-28'
              />
            </div>
          ))}
        </div>

        <Label className='flex items-center gap-2 font-normal'>
          <Checkbox
            checked={input.useSnapshot}
            onCheckedChange={(checked) => set('useSnapshot', checked === true)}
          />
          {t(
            'Use the unsaved settings on this page as the config snapshot (the window stays at the live value)'
          )}
        </Label>

        {(inputError || simulate.isError) && (
          <p role='alert' className='text-destructive text-sm'>
            {inputError ||
              getServerErrorMessage(simulate.error, t('Simulation failed'))}
          </p>
        )}

        {result && (
          <div className='flex flex-col gap-3'>
            <dl className='grid gap-x-4 gap-y-1 text-sm sm:grid-cols-2'>
              <div>
                <dt className='text-muted-foreground inline'>{t('Model')}: </dt>
                <dd className='inline font-mono'>{result.model}</dd>
              </div>
              <div>
                <dt className='text-muted-foreground inline'>
                  {t('Group ratio')}:{' '}
                </dt>
                <dd className='inline'>{score.format(result.group_ratio)}</dd>
              </div>
              <div>
                <dt className='text-muted-foreground inline'>
                  {t('Decision')}:{' '}
                </dt>
                <dd className='inline font-mono'>
                  {result.decision.takeover && 'takeover'}
                  {result.decision.shadow && 'shadow'}
                  {result.decision.reason}
                  {result.probe && ` · ${t('Probe')}`}
                  {result.explore && ` · ${t('Exploration')}`}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground inline'>{t('Spec')}: </dt>
                <dd className='inline'>
                  {spec ? (
                    <>
                      {t('{{seconds}} s output ({{kind}})', {
                        seconds: spec.output_seconds ?? '—',
                        kind: spec.seconds_kind ?? '—',
                      })}
                      {' · '}
                      {spec.tier ?? '—'}
                      {' · '}
                      {t(
                        'References: {{video}} video, {{image}} image, {{audio}} audio',
                        {
                          video: spec.references.video ?? 0,
                          image: spec.references.image ?? 0,
                          audio: spec.references.audio ?? 0,
                        }
                      )}
                      {spec.missing?.length
                        ? ` · ${t('Missing')}: ${spec.missing.join(', ')}`
                        : ''}
                      <span className='text-muted-foreground'>
                        {' '}
                        ({t('from #{{id}}', { id: specSource?.id })})
                      </span>
                    </>
                  ) : (
                    '—'
                  )}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground inline'>
                  {t('Now / seed')}:{' '}
                </dt>
                <dd className='inline font-mono'>
                  {result.now} · {result.seed}
                </dd>
              </div>
              <div className='sm:col-span-2'>
                <dt className='text-muted-foreground inline'>
                  {t('Fingerprint')}:{' '}
                </dt>
                <dd className='inline font-mono text-xs break-all'>
                  {result.fingerprint}
                </dd>
              </div>
              {Object.entries(result.segments).map(([segment, hash]) => (
                <div key={segment} className='sm:col-span-2'>
                  <dt className='text-muted-foreground inline font-mono text-xs'>
                    {segment}:{' '}
                  </dt>
                  <dd className='inline font-mono text-xs break-all'>{hash}</dd>
                </div>
              ))}
            </dl>
            <StaticDataTable
              columns={columns}
              data={result.candidates}
              getRowKey={(row) => row.id}
              emptyContent={t('No candidates')}
            />
          </div>
        )}
      </div>
    </Dialog>
  )
}
