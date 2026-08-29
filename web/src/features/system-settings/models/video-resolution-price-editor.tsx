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
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'

import type {
  VideoResolutionPriceRow,
  VideoResolutionPriceRowErrors,
} from './video-resolution-pricing'

export type VideoResolutionPriceEditorProps = {
  rows: VideoResolutionPriceRow[]
  errorsByRowId: Record<number, VideoResolutionPriceRowErrors>
  disabled?: boolean
  onChange: (rows: VideoResolutionPriceRow[]) => void
}

const resolutionErrorKey = {
  required: 'Resolution is required',
  invalid: 'Use a canonical resolution such as 720p or 4k',
  duplicate: 'Each resolution can only be configured once',
} as const

const priceErrorKey = {
  required: 'Price is required',
  invalid: 'Price must be a finite number greater than zero',
} as const

export function VideoResolutionPriceEditor(
  props: VideoResolutionPriceEditorProps
) {
  const { t } = useTranslation()

  const updateRow = (
    id: number,
    field: 'resolution' | 'price',
    value: string
  ) => {
    props.onChange(
      props.rows.map((row) =>
        row.id === id ? { ...row, [field]: value } : row
      )
    )
  }

  const addRow = () => {
    const nextId =
      props.rows.reduce((maxId, row) => Math.max(maxId, row.id), 0) + 1
    props.onChange([...props.rows, { id: nextId, resolution: '', price: '' }])
  }

  const removeRow = (id: number) => {
    props.onChange(props.rows.filter((row) => row.id !== id))
  }

  return (
    <FieldGroup className='gap-4'>
      {props.rows.length === 0 ? (
        <FieldDescription>
          {t('No resolution prices configured')}
        </FieldDescription>
      ) : null}

      {props.rows.map((row, index) => {
        const errors = props.errorsByRowId[row.id]
        // row.id 是稳定身份而非位置，删行后会跳号，所以朗读用的序号取索引
        const resolutionLabel = `${t('Resolution')} ${index + 1}`
        const priceLabel = `${t('USD price per second')}: ${row.resolution || resolutionLabel}`
        const resolutionErrorId = `video-resolution-${row.id}-error`
        const priceErrorId = `video-resolution-price-${row.id}-error`
        return (
          <div
            key={row.id}
            className='grid items-start gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]'
          >
            <Field data-invalid={Boolean(errors?.resolution)}>
              <FieldLabel htmlFor={`video-resolution-${row.id}`}>
                {t('Resolution')}
              </FieldLabel>
              <Input
                id={`video-resolution-${row.id}`}
                value={row.resolution}
                placeholder='720p'
                disabled={props.disabled}
                aria-invalid={Boolean(errors?.resolution)}
                aria-label={resolutionLabel}
                aria-describedby={
                  errors?.resolution ? resolutionErrorId : undefined
                }
                onChange={(event) =>
                  updateRow(row.id, 'resolution', event.target.value)
                }
              />
              {errors?.resolution ? (
                <FieldError id={resolutionErrorId}>
                  {t(resolutionErrorKey[errors.resolution])}
                </FieldError>
              ) : null}
            </Field>

            <Field data-invalid={Boolean(errors?.price)}>
              <FieldLabel htmlFor={`video-resolution-price-${row.id}`}>
                {t('USD price per second')}
              </FieldLabel>
              <InputGroup>
                <InputGroupAddon>$</InputGroupAddon>
                <InputGroupInput
                  id={`video-resolution-price-${row.id}`}
                  inputMode='decimal'
                  value={row.price}
                  placeholder='0.1'
                  disabled={props.disabled}
                  aria-invalid={Boolean(errors?.price)}
                  aria-label={priceLabel}
                  aria-describedby={errors?.price ? priceErrorId : undefined}
                  onChange={(event) =>
                    updateRow(row.id, 'price', event.target.value)
                  }
                />
                <InputGroupAddon align='inline-end'>
                  {t('second')}
                </InputGroupAddon>
              </InputGroup>
              {errors?.price ? (
                <FieldError id={priceErrorId}>
                  {t(priceErrorKey[errors.price])}
                </FieldError>
              ) : null}
            </Field>

            <Button
              type='button'
              variant='ghost'
              size='icon'
              className='sm:mt-6'
              disabled={props.disabled}
              aria-label={`${t('Remove resolution')}: ${row.resolution || resolutionLabel}`}
              onClick={() => removeRow(row.id)}
            >
              <Trash2 className='text-destructive h-4 w-4' />
            </Button>
          </div>
        )
      })}

      <div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled}
          onClick={addRow}
        >
          <Plus className='mr-2 h-4 w-4' />
          {t('Add resolution')}
        </Button>
      </div>

      <FieldDescription>
        {t('Resolution prices are always charged per second.')}
      </FieldDescription>
    </FieldGroup>
  )
}
