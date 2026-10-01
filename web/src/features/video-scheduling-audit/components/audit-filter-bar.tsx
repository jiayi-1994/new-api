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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DateTimePicker } from '@/components/datetime-picker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import type { AuditFilters } from '../types'

export function AuditFilterBar({
  filters,
  onApply,
  busy,
}: {
  filters: AuditFilters
  onApply: (filters: AuditFilters) => void
  busy: boolean
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(filters)
  const invalid =
    !draft.start ||
    !draft.end ||
    draft.end <= draft.start ||
    draft.end - draft.start > 31 * 86400000
  const modeItems = [
    { value: 'on', label: t('On') },
    { value: 'shadow', label: t('Shadow') },
  ]
  const outcomes = [
    { value: 'all', label: t('All') },
    { value: 'success', label: t('Success') },
    { value: 'failure', label: t('Failed') },
    { value: 'pending', label: t('Pending') },
    { value: 'cancelled', label: t('Cancelled') },
    { value: 'outcome_unknown', label: t('Submit outcome unknown') },
    { value: 'no_candidate', label: t('No candidate') },
    { value: 'rejected', label: t('Rejected') },
    { value: 'local_failure', label: t('Not submitted') },
    { value: 'persistence_failure', label: t('Accepted without task') },
    { value: 'internal_failure', label: t('Internal error') },
  ]
  const fields = [
    { key: 'model', label: t('Model') },
    { key: 'channel', label: t('Channel ID'), numeric: true },
    { key: 'group', label: t('Group') },
    { key: 'version', label: t('Scheduler version') },
    { key: 'resolution', label: t('Resolution') },
    { key: 'seconds', label: t('Duration (seconds)'), numeric: true },
    { key: 'reference_video', label: t('Reference videos'), numeric: true },
    { key: 'reference_image', label: t('Reference images'), numeric: true },
    { key: 'reference_audio', label: t('Reference audio'), numeric: true },
    { key: 'request_id', label: t('Request ID') },
  ] as const
  return (
    <form
      className='space-y-3'
      onSubmit={(event) => {
        event.preventDefault()
        if (!invalid) onApply({ ...draft, page: 1 })
      }}
    >
      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4'>
        <div className='space-y-1'>
          <Label>{t('Start time')}</Label>
          <DateTimePicker
            value={draft.start ? new Date(draft.start) : undefined}
            onChange={(date) => setDraft({ ...draft, start: date?.getTime() })}
            placeholder={t('Start time')}
          />
        </div>
        <div className='space-y-1'>
          <Label>{t('End time')}</Label>
          <DateTimePicker
            value={draft.end ? new Date(draft.end) : undefined}
            onChange={(date) => setDraft({ ...draft, end: date?.getTime() })}
            placeholder={t('End time')}
          />
        </div>
        <div className='space-y-1'>
          <Label htmlFor='audit-mode'>{t('Scheduling mode')}</Label>
          <Select
            items={modeItems}
            value={draft.mode}
            onValueChange={(mode) => {
              if (mode === 'on' || mode === 'shadow') {
                setDraft({ ...draft, mode })
              }
            }}
          >
            <SelectTrigger id='audit-mode'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {modeItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className='space-y-1'>
          <Label htmlFor='audit-outcome'>{t('Status')}</Label>
          <Select
            items={outcomes}
            value={draft.outcome || 'all'}
            onValueChange={(outcome) =>
              setDraft({
                ...draft,
                outcome: outcome === 'all' ? undefined : (outcome ?? undefined),
              })
            }
          >
            <SelectTrigger id='audit-outcome'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {outcomes.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        {fields.map((field) => (
          <div key={field.key} className='space-y-1'>
            <Label htmlFor={`audit-${field.key}`}>{field.label}</Label>
            <Input
              id={`audit-${field.key}`}
              type={'numeric' in field ? 'number' : 'text'}
              min={0}
              step={field.key === 'seconds' ? 'any' : 1}
              value={draft[field.key] ?? ''}
              onChange={(event) => {
                let value: string | number | undefined = event.target.value
                if (value === '') {
                  value = undefined
                } else if ('numeric' in field) {
                  value = Number(value)
                }
                setDraft({ ...draft, [field.key]: value })
              }}
            />
          </div>
        ))}
      </div>
      {invalid && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Choose a time range of up to 31 days')}
        </p>
      )}
      <div className='flex gap-2'>
        <Button type='submit' disabled={busy || invalid}>
          {t('Search')}
        </Button>
        <Button
          type='button'
          variant='outline'
          disabled={busy}
          onClick={() =>
            onApply({
              mode: filters.mode,
              page: 1,
              page_size: filters.page_size,
              start: Date.now() - 86400000,
              end: Date.now(),
            })
          }
        >
          {t('Last 24 hours')}
        </Button>
      </div>
    </form>
  )
}
