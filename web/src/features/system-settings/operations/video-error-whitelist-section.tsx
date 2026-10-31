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
import { Plus, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { formatTimestampToDate } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getVideoErrorCandidates } from '../api'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  VIDEO_ERROR_WHITELIST_OPTION_KEY,
  parseVideoErrorWhitelist,
} from './video-error-whitelist'
import { VideoErrorWhitelistDialog } from './video-error-whitelist-dialog'

type WhitelistRow = { id: number; match: string; message: string }

// System and upstream account failures never reach this list: the server drops
// them, so everything shown here is safe for an administrator to choose from.
function VideoErrorCandidates() {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState<string | null>(null)
  const query = useQuery({
    queryKey: ['video-error-candidates'],
    queryFn: async () => requireServerSuccess(await getVideoErrorCandidates()),
  })
  const candidates = query.data?.data ?? []

  return (
    <div className='flex flex-col gap-3 border-t pt-4'>
      <div>
        <h4 className='text-sm font-semibold'>
          {t('Failures awaiting review')}
        </h4>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Failures from the last 30 days that are not whitelisted. System and upstream account failures are excluded and can never be added.'
          )}
        </p>
      </div>
      {query.isError ? (
        <p className='text-destructive text-sm'>{t('Failed to load')}</p>
      ) : null}
      {query.isSuccess && candidates.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No failures awaiting review')}
        </p>
      ) : null}
      {candidates.map((candidate) => (
        <div
          key={candidate.keyword + candidate.sample}
          className='flex items-start justify-between gap-3 rounded-md border p-3'
        >
          <div className='min-w-0 space-y-1'>
            <p className='text-sm break-all'>{candidate.sample}</p>
            <p className='text-muted-foreground text-xs'>
              {t('{{count}} occurrences', { count: candidate.count })}
              {' · '}
              {formatTimestampToDate(candidate.last_seen)}
            </p>
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => setKeyword(candidate.keyword || candidate.sample)}
          >
            {t('Add to whitelist')}
          </Button>
        </div>
      ))}
      {keyword === null ? null : (
        <VideoErrorWhitelistDialog
          key={keyword}
          keyword={keyword}
          open
          onOpenChange={(open) => {
            if (!open) setKeyword(null)
          }}
        />
      )}
    </div>
  )
}

export function VideoErrorWhitelistSection(props: { rules: string }) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [rows, setRows] = useState<WhitelistRow[]>(() =>
    Object.entries(parseVideoErrorWhitelist(props.rules))
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([match, message], id) => ({ id, match, message }))
  )
  const [nextId, setNextId] = useState(rows.length)

  function updateRow(id: number, change: Partial<WhitelistRow>) {
    setRows((current) =>
      current.map((row) => (row.id === id ? { ...row, ...change } : row))
    )
  }

  function save() {
    const rules = Object.fromEntries(
      rows
        .filter((row) => row.match.trim())
        .map((row) => [row.match.trim(), row.message.trim()])
    )
    updateOption.mutate({
      key: VIDEO_ERROR_WHITELIST_OPTION_KEY,
      value: JSON.stringify(rules),
    })
  }

  return (
    <SettingsSection title={t('Video Error Whitelist')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Video failures show a generic message by default. A failure containing a keyword below shows the display text, or the sanitized upstream message when the text is empty. System and upstream account failures, such as unpaid balance, no available channel or timeouts, are never shown even if a rule matches.'
        )}
      </p>
      {rows.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No whitelist rules yet')}
        </p>
      ) : null}
      {rows.map((row) => (
        <div key={row.id} className='flex flex-col gap-2 sm:flex-row'>
          <Input
            aria-label={t('Error keyword')}
            placeholder={t('Error keyword')}
            value={row.match}
            onChange={(event) =>
              updateRow(row.id, { match: event.target.value })
            }
          />
          <Input
            aria-label={t('Display text (optional)')}
            placeholder={t('Display text (optional)')}
            value={row.message}
            onChange={(event) =>
              updateRow(row.id, { message: event.target.value })
            }
          />
          <Button
            type='button'
            variant='ghost'
            size='icon'
            aria-label={t('Delete')}
            onClick={() =>
              setRows((current) => current.filter(({ id }) => id !== row.id))
            }
          >
            <Trash2 className='size-4' />
          </Button>
        </div>
      ))}
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          onClick={() => {
            setRows((current) => [
              ...current,
              { id: nextId, match: '', message: '' },
            ])
            setNextId(nextId + 1)
          }}
        >
          <Plus className='size-4' />
          {t('Add rule')}
        </Button>
        <Button type='button' onClick={save} disabled={updateOption.isPending}>
          <Save className='size-4' />
          {t('Save')}
        </Button>
      </div>
      <VideoErrorCandidates />
    </SettingsSection>
  )
}
