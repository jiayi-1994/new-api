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
import { Minus, Plus } from 'lucide-react'
import { useState, useEffect, useRef, useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

interface NumericSpinnerInputProps {
  value: number | null | undefined
  onChange: (value: number) => void
  onCommit?: () => void
  min?: number
  max?: number
  step?: number
  disabled?: boolean
  className?: string
  label?: string
}

export function NumericSpinnerInput({
  value,
  onChange,
  onCommit,
  min = 0,
  max = Number.MAX_SAFE_INTEGER,
  step = 1,
  disabled = false,
  className,
  label,
}: NumericSpinnerInputProps) {
  const { t } = useTranslation()
  const inputId = useId()
  const [localValue, setLocalValue] = useState(String(value ?? 0))
  const [editing, setEditing] = useState(false)
  const [showError, setShowError] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const numericValue = Number(localValue)
  const valid =
    /^-?\d+$/.test(localValue) &&
    Number.isSafeInteger(numericValue) &&
    numericValue >= min &&
    numericValue <= max
  const invalid = showError && !valid
  const atMin = numericValue <= min
  const atMax = numericValue >= max

  useEffect(() => {
    if (!editing) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setLocalValue(String(value ?? 0))
    }
  }, [value, editing])

  const clamp = (v: number) => {
    let result = v
    if (min !== undefined) result = Math.max(min, result)
    if (max !== undefined) result = Math.min(max, result)
    return result
  }

  const handleIncrement = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (disabled || !valid || atMax) return
    const next = clamp(numericValue + step)
    if (!Number.isSafeInteger(next)) return
    setLocalValue(String(next))
    onChange(next)
  }

  const handleDecrement = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (disabled || !valid || atMin) return
    const next = clamp(numericValue - step)
    if (!Number.isSafeInteger(next)) return
    setLocalValue(String(next))
    onChange(next)
  }

  const handleStartEdit = () => {
    if (disabled) return
    setShowError(false)
    setEditing(true)
    requestAnimationFrame(() => inputRef.current?.select())
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setLocalValue(e.target.value)
  }

  const commitValue = () => {
    if (!valid) {
      setShowError(true)
      return
    }
    setShowError(false)
    setEditing(false)
    setLocalValue(String(numericValue))
    if (numericValue !== (value ?? 0)) {
      onChange(numericValue)
    }
  }

  const handleControlBlur = (e: React.FocusEvent<HTMLDivElement>) => {
    if (
      e.relatedTarget instanceof Node &&
      e.currentTarget.contains(e.relatedTarget)
    ) {
      return
    }
    onCommit?.()
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      // Blurring routes Enter through the same focusout path as clicking
      // away (input onBlur -> commitValue, container onBlur -> onCommit),
      // so commit and onCommit each fire exactly once.
      inputRef.current?.blur()
    } else if (e.key === 'Escape') {
      setShowError(false)
      setEditing(false)
      setLocalValue(String(value ?? 0))
    }
  }

  return (
    <div className={cn('inline-flex flex-col items-start gap-1', className)}>
      {label && (
        <Label htmlFor={inputId} className='text-muted-foreground text-xs'>
          {label}
        </Label>
      )}
      <div
        onBlur={handleControlBlur}
        className={cn(
          'group/spinner border-input inline-flex h-7 items-center gap-0 rounded-md border transition-colors',
          !disabled && 'hover:bg-muted/60',
          editing && 'bg-muted/60 ring-primary/30 ring-1'
        )}
      >
        <Button
          type='button'
          variant='ghost'
          size='icon-sm'
          tabIndex={-1}
          aria-label={t('Decrement')}
          onClick={handleDecrement}
          disabled={disabled || !valid || atMin}
          className={cn(
            'text-muted-foreground/0 group-hover/spinner:text-muted-foreground h-7 w-6 rounded-l-md rounded-r-none',
            !disabled &&
              valid &&
              !atMin &&
              'group-hover/spinner:hover:text-foreground group-hover/spinner:hover:bg-muted',
            (disabled || !valid || atMin) && 'group-hover/spinner:opacity-30'
          )}
        >
          <Minus className='size-3' />
        </Button>

        {editing ? (
          <Input
            id={inputId}
            ref={inputRef}
            type='text'
            aria-label={label ?? t('Value')}
            aria-invalid={invalid}
            aria-describedby={invalid ? `${inputId}-error` : undefined}
            value={localValue}
            onChange={handleInputChange}
            onBlur={commitValue}
            onKeyDown={handleKeyDown}
            className='h-7 w-10 rounded-none border-0 bg-transparent px-0 text-center font-mono text-sm'
            autoFocus
          />
        ) : (
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={handleStartEdit}
            disabled={disabled}
            title={localValue}
            className={cn(
              'h-7 min-w-8 max-w-16 cursor-text truncate px-1 text-center font-mono text-sm tabular-nums',
              disabled && 'cursor-default opacity-50'
            )}
          >
            {localValue}
          </Button>
        )}

        <Button
          type='button'
          variant='ghost'
          size='icon-sm'
          tabIndex={-1}
          aria-label={t('Increment')}
          onClick={handleIncrement}
          disabled={disabled || !valid || atMax}
          className={cn(
            'text-muted-foreground/0 group-hover/spinner:text-muted-foreground h-7 w-6 rounded-l-none rounded-r-md',
            !disabled &&
              valid &&
              !atMax &&
              'group-hover/spinner:hover:text-foreground group-hover/spinner:hover:bg-muted',
            (disabled || !valid || atMax) && 'group-hover/spinner:opacity-30'
          )}
        >
          <Plus className='size-3' />
        </Button>
      </div>
      {invalid && (
        <p
          id={`${inputId}-error`}
          role='alert'
          className='text-destructive max-w-48 text-xs whitespace-normal'
        >
          {t('Enter an integer between {{min}} and {{max}}.', { min, max })}
        </p>
      )}
    </div>
  )
}
