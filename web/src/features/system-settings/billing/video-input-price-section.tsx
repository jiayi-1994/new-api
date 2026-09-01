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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

type Values = {
  VideoInputSecondPrice: string
}

export function VideoInputPriceSection({
  defaultValue,
}: {
  defaultValue: string
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<Values>({
    defaultValues: {
      VideoInputSecondPrice: defaultValue || '{}',
    },
  })

  const { isDirty, isSubmitting } = form.formState

  async function onSubmit(values: Values) {
    const raw = values.VideoInputSecondPrice.trim() || '{}'
    try {
      JSON.parse(raw)
    } catch {
      toast.error(t('Invalid JSON'))
      return
    }
    await updateOption.mutateAsync({
      key: 'VideoInputSecondPrice',
      value: raw,
    })
    form.reset({ VideoInputSecondPrice: raw })
  }

  return (
    <SettingsSection title={t('Video Input Surcharge')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel='Save video input surcharge'
          />
          <FormField
            control={form.control}
            name='VideoInputSecondPrice'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Input reference video price per second')}
                </FormLabel>
                <FormControl>
                  <Textarea
                    rows={10}
                    spellCheck={false}
                    className='font-mono'
                    placeholder='{"seedance-2-5": {"720p": 0.3, "1080p": 1.05}}'
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Per-second surcharge for input reference videos, keyed by model then output resolution. The gateway probes each reference video duration at submit time; requests whose reference video cannot be resolved are rejected. Same shape as video resolution prices.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
