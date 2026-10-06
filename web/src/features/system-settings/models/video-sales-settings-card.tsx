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
import { Plus } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'
import type { VideoSalesModel } from '@/features/pricing/types'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { parseVideoSales } from './video-sales-config'
import {
  createVideoSalesFormSchema,
  videoSalesFormValues,
  videoSalesFromForm,
  type VideoSalesFormValues,
} from './video-sales-form'
import { VideoSalesModelEditor } from './video-sales-model-editor'

export function VideoSalesSettingsCard(props: { defaultValue: string }) {
  const { t } = useTranslation()
  const sales = useMemo(
    () => parseVideoSales(props.defaultValue),
    [props.defaultValue]
  )
  if (!sales) {
    return (
      <ErrorState
        title={t('Invalid video sales configuration')}
        description={t(
          'The saved video sales configuration could not be read. No changes have been made.'
        )}
      />
    )
  }
  return (
    <VideoSalesSettingsForm defaultValue={props.defaultValue} sales={sales} />
  )
}

function VideoSalesSettingsForm(props: {
  defaultValue: string
  sales: Record<string, VideoSalesModel>
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const source = useRef(props.defaultValue)
  const latestSource = useRef(props.defaultValue)
  const savedValues = useRef(videoSalesFormValues(props.sales))
  const formElement = useRef<HTMLFormElement>(null)
  const [deleteIndex, setDeleteIndex] = useState<number | null>(null)
  const form = useForm<VideoSalesFormValues>({
    defaultValues: videoSalesFormValues(props.sales),
    resolver: zodResolver(createVideoSalesFormSchema(t)),
    mode: 'onChange',
  })
  const models = useFieldArray({ control: form.control, name: 'models' })
  const isDirty = form.formState.isDirty
  const pending = updateOption.isPending

  useEffect(() => {
    latestSource.current = props.defaultValue
    if (
      props.defaultValue === source.current ||
      pending ||
      JSON.stringify(form.getValues()) !== JSON.stringify(savedValues.current)
    ) {
      return
    }
    source.current = props.defaultValue
    savedValues.current = videoSalesFormValues(props.sales)
    setDeleteIndex(null)
    form.reset(savedValues.current)
  }, [props.defaultValue, props.sales, form, isDirty, pending])

  async function save(values: VideoSalesFormValues) {
    form.clearErrors('root.server')
    try {
      await updateOption.mutateAsync({
        key: 'billing_setting.video_sales',
        value: JSON.stringify(videoSalesFromForm(values)),
      })
      // A refetch received while editing must not restore an older value after this save.
      source.current = latestSource.current
      savedValues.current = values
      form.reset(values)
      setDeleteIndex(null)
    } catch (error) {
      form.setError('root.server', {
        message: getServerErrorMessage(error, t('Failed to update setting')),
      })
    }
  }

  function confirmDelete() {
    if (deleteIndex === null) return
    if (!formElement.current?.reportValidity()) return
    const values = form.getValues()
    const remaining = {
      models: values.models.filter((_, index) => index !== deleteIndex),
    }
    const validated = createVideoSalesFormSchema(t).safeParse(remaining)
    if (!validated.success) {
      setDeleteIndex(null)
      void form.trigger()
      return
    }
    // The server checks channel references. A failed deletion leaves every draft intact.
    void save(validated.data)
  }

  return (
    <SettingsSection title={t('Unified video sales')}>
      <FormDirtyIndicator isDirty={isDirty} />
      <FormNavigationGuard when={isDirty && !pending} />
      <SettingsPageFormActions
        onSave={() => formElement.current?.requestSubmit()}
        isSaving={pending}
        isSaveDisabled={!isDirty}
        saveLabel='Save video sales'
      />
      <p className='text-muted-foreground text-sm'>
        {t(
          'Set one customer price for each resolution and duration, regardless of the selected channel.'
        )}
      </p>
      <Form {...form}>
        <form
          ref={formElement}
          onSubmit={(event) => {
            // Channel drawers render their own forms in portals under this React tree.
            if (event.target !== event.currentTarget) return
            void form.handleSubmit(save)(event)
          }}
          className='space-y-5'
        >
          {form.formState.errors.root?.server?.message && (
            <p role='alert' className='text-destructive text-sm'>
              {form.formState.errors.root.server.message}
            </p>
          )}
          {models.fields.length === 0 && (
            <EmptyState
              title={t('No unified video models configured')}
              className='min-h-32'
            />
          )}
          {models.fields.map((model, index) => (
            <VideoSalesModelEditor
              key={model.id}
              form={form}
              index={index}
              pending={pending}
              onDelete={() => setDeleteIndex(index)}
            />
          ))}
          <Button
            type='button'
            variant='outline'
            disabled={pending}
            onClick={() =>
              models.append({
                name: '',
                disabled: true,
                tiers: [
                  {
                    resolution: '720p',
                    price: '',
                    inputPrice: '0',
                    inputTokenPrice: '0',
                    seconds: '5, 10, 15',
                  },
                ],
              })
            }
          >
            <Plus aria-hidden='true' data-icon='inline-start' />
            {t('Add video model')}
          </Button>
        </form>
      </Form>
      <ConfirmDialog
        open={deleteIndex !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteIndex(null)
        }}
        title={t('Delete unified video model?')}
        desc={t(
          'Deletion saves the full sales table. Models still listed by a channel cannot be deleted; pause sales instead.'
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={pending}
        handleConfirm={confirmDelete}
      >
        {form.formState.errors.root?.server?.message && (
          <p role='alert' className='text-destructive text-sm'>
            {form.formState.errors.root.server.message}
          </p>
        )}
      </ConfirmDialog>
    </SettingsSection>
  )
}
