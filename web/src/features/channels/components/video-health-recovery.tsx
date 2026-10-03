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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Textarea } from '@/components/ui/textarea'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { requestVideoHealthRecovery } from '../api'
import { channelsQueryKeys } from '../lib/channel-actions'
import {
  videoHealthRecoverySchema,
  type VideoHealthRecoveryValues,
} from '../lib/video-reliability'

export function VideoHealthRecovery(props: {
  channelId: number
  channelName: string
  model: string
  stateVersion: number
  canRecover: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null)
  const form = useForm<VideoHealthRecoveryValues>({
    resolver: zodResolver(videoHealthRecoverySchema),
    defaultValues: { note: '' },
    mode: 'onChange',
  })
  const mutation = useMutation({
    mutationFn: (
      values: VideoHealthRecoveryValues & { state_version: number }
    ) =>
      requestVideoHealthRecovery(props.channelId, {
        model: props.model,
        ...values,
      }),
    retry: false,
    meta: { errorToast: false },
    onError: () => {
      void client.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
    },
    onSuccess: async () => {
      toast.success(t('Scheduling recovery requested'))
      setSelectedVersion(null)
      await Promise.all([
        client.invalidateQueries({ queryKey: channelsQueryKeys.all }),
        client.invalidateQueries({ queryKey: ['video-scheduling-audit'] }),
        client.invalidateQueries({ queryKey: ['video-health-unknown'] }),
      ])
    },
  })
  return (
    <>
      {props.canRecover && (
        <Button
          variant='outline'
          size='sm'
          className='h-auto max-w-full py-1 text-left whitespace-normal'
          disabled={mutation.isPending}
          onClick={() => {
            form.reset()
            mutation.reset()
            setSelectedVersion(props.stateVersion)
          }}
        >
          {t('Restore scheduling eligibility')}
        </Button>
      )}
      <ConfirmDialog
        open={selectedVersion !== null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setSelectedVersion(null)
        }}
        title={t('Restore scheduling eligibility')}
        desc={t(
          'Skip the current cooldown and allow limited recovery verification. Normal scheduling resumes only after verification passes. Existing failures and unknown submissions are preserved.'
        )}
        confirmText={t('Restore scheduling eligibility')}
        disabled={!props.canRecover || !form.formState.isValid}
        isLoading={mutation.isPending}
        handleConfirm={form.handleSubmit((values) => {
          if (
            selectedVersion !== null &&
            props.canRecover &&
            !mutation.isPending
          ) {
            mutation.mutate({ ...values, state_version: selectedVersion })
          }
        })}
      >
        <p className='text-sm break-words'>
          {t('Channel {{name}} model {{model}}', {
            name: props.channelName,
            model: props.model,
          })}
        </p>
        <Form {...form}>
          <FormField
            control={form.control}
            name='note'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Recovery reason')}</FormLabel>
                <FormControl>
                  <Textarea {...field} disabled={mutation.isPending} />
                </FormControl>
                <FormDescription>
                  {t(
                    'Up to 500 characters. Do not include credentials or customer content.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </Form>
        {mutation.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {getServerErrorMessage(mutation.error)}
          </p>
        )}
      </ConfirmDialog>
    </>
  )
}
