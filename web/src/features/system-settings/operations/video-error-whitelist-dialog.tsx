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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getSystemOptions, updateSystemOption } from '../api'
import {
  VIDEO_ERROR_WHITELIST_OPTION_KEY,
  parseVideoErrorWhitelist,
} from './video-error-whitelist'

const MAX_KEYWORD_LENGTH = 500

export function VideoErrorWhitelistDialog(props: {
  keyword: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [keyword, setKeyword] = useState(
    props.keyword.trim().slice(0, MAX_KEYWORD_LENGTH)
  )
  const [displayText, setDisplayText] = useState('')
  const add = useMutation({
    // Merges into the latest saved rules, not a possibly stale settings view.
    mutationFn: async () => {
      const options = await getSystemOptions()
      const stored = options.data?.find(
        (option) => option.key === VIDEO_ERROR_WHITELIST_OPTION_KEY
      )
      const rules = {
        ...parseVideoErrorWhitelist(stored?.value),
        [keyword.trim()]: displayText.trim(),
      }
      return requireServerSuccess(
        await updateSystemOption({
          key: VIDEO_ERROR_WHITELIST_OPTION_KEY,
          value: JSON.stringify(rules),
        })
      )
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      queryClient.invalidateQueries({
        queryKey: ['video-error-candidates'],
      })
      toast.success(t('Added to the video error whitelist'))
      props.onOpenChange(false)
    },
    onError: (error: Error) =>
      handleServerError(error, t('Failed to update setting')),
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Add to Video Error Whitelist')}
      description={t(
        'Keep only the stable part of the error: remove task IDs, URLs and numbers so the same error matches next time.'
      )}
      contentClassName='sm:max-w-lg'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <div className='space-y-2 py-2'>
        <Label htmlFor='video-error-keyword'>{t('Error keyword')}</Label>
        <Textarea
          id='video-error-keyword'
          rows={4}
          maxLength={MAX_KEYWORD_LENGTH}
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
        />
      </div>
      <div className='space-y-2'>
        <Label htmlFor='video-error-display-text'>
          {t('Display text (optional)')}
        </Label>
        <Input
          id='video-error-display-text'
          maxLength={MAX_KEYWORD_LENGTH}
          placeholder={t('Empty shows the sanitized upstream message')}
          value={displayText}
          onChange={(event) => setDisplayText(event.target.value)}
        />
      </div>
      <div className='flex justify-end pb-2'>
        <Button
          type='button'
          disabled={!keyword.trim() || add.isPending}
          onClick={() => add.mutate()}
        >
          {t('Add to whitelist')}
        </Button>
      </div>
    </Dialog>
  )
}
