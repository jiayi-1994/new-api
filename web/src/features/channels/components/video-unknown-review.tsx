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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  getUnknownVideoAttempts,
  reviewUnknownVideoAttempt,
  type UnknownVideoAttempt,
} from '../api'
import { channelsQueryKeys } from '../lib/channel-actions'

const reviewSchema = z.object({
  note: z
    .string()
    .trim()
    .min(1)
    .refine((value) => new TextEncoder().encode(value).length <= 1000),
})

export function VideoUnknownReview(props: { channelId: number }) {
  const { t } = useTranslation()
  const root = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const [open, setOpen] = useState(false)
  if (!root) return null
  return (
    <>
      <Button
        variant='outline'
        size='sm'
        className='mt-4'
        onClick={() => setOpen(true)}
      >
        {t('Review unknown submissions')}
      </Button>
      {open && (
        <VideoUnknownReviewSession
          channelId={props.channelId}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

export function VideoUnknownReviewSession(props: {
  channelId: number
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const userId = useAuthStore((state) => state.auth.user?.id)
  const client = useQueryClient()
  const noteId = useId()
  const [selected, setSelected] = useState<UnknownVideoAttempt | null>(null)
  const form = useForm({
    resolver: zodResolver(reviewSchema),
    defaultValues: { note: '' },
    mode: 'onChange',
  })
  const queryKey = ['video-health-unknown', userId, props.channelId]
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getUnknownVideoAttempts(props.channelId, signal),
  })
  const mutation = useMutation({
    mutationFn: (values: { id: number; note: string }) =>
      reviewUnknownVideoAttempt(values.id, values.note),
    meta: { errorToast: false },
    onSuccess: async () => {
      setSelected(null)
      await Promise.all([
        client.invalidateQueries({ queryKey }),
        client.invalidateQueries({ queryKey: channelsQueryKeys.lists() }),
        client.invalidateQueries({ queryKey: ['video-scheduling-audit'] }),
      ])
    },
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) props.onClose()
      }}
      title={t('Review unknown submissions')}
      description={t(
        'Unreviewed submissions appear first. Up to 100 records are shown.'
      )}
    >
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          description={getServerErrorMessage(query.error)}
          onRetry={() => query.refetch()}
        />
      )}
      {query.data?.length === 0 && (
        <EmptyState title={t('No unknown submissions')} />
      )}
      <div className='space-y-4'>
        {query.data?.map((attempt) => (
          <section key={attempt.id} className='space-y-2 border-b pb-3 text-sm'>
            <p className='font-medium break-all'>{attempt.model}</p>
            <div className='flex items-center gap-1 break-all'>
              <code>{attempt.request_id}</code>
              <CopyButton value={attempt.request_id} />
            </div>
            <p className='text-muted-foreground'>
              {new Date(attempt.started_at * 1000).toLocaleString(locale)}
            </p>
            {attempt.reviewed_at > 0 ? (
              <p className='break-words'>
                {t('Reviewed by {{operator}}', {
                  operator: formatNumber(attempt.reviewed_by, locale),
                })}{' '}
                · {new Date(attempt.reviewed_at * 1000).toLocaleString(locale)}
                <br />
                {attempt.review_note}
              </p>
            ) : (
              <Button
                variant='outline'
                size='sm'
                disabled={attempt.task_pk != null}
                onClick={() => {
                  form.reset()
                  mutation.reset()
                  setSelected(attempt)
                }}
              >
                {t('Review unknown submission')}
              </Button>
            )}
          </section>
        ))}
      </div>
      <ConfirmDialog
        open={selected != null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setSelected(null)
        }}
        title={t('Review unknown submission')}
        desc={t(
          'Check the provider records first. This preserves the unknown result and requires fresh recovery samples. It does not retry, cancel, or refund the task.'
        )}
        confirmText={t('Record review')}
        disabled={!form.formState.isValid}
        isLoading={mutation.isPending}
        handleConfirm={form.handleSubmit((values) => {
          if (selected) mutation.mutate({ id: selected.id, note: values.note })
        })}
      >
        <div className='space-y-2'>
          <Label htmlFor={noteId}>{t('Review evidence')}</Label>
          <Textarea
            id={noteId}
            {...form.register('note')}
            disabled={mutation.isPending}
            maxLength={1000}
          />
          <p className='text-muted-foreground text-xs'>
            {t(
              'Up to 1000 bytes. Do not include credentials or customer content.'
            )}
          </p>
          {mutation.isError && (
            <p role='alert' className='text-destructive text-sm'>
              {getServerErrorMessage(mutation.error)}
            </p>
          )}
        </div>
      </ConfirmDialog>
    </Dialog>
  )
}
