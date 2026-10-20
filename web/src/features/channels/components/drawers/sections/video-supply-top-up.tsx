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

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency, getCurrencyDisplay } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { addChannelVideoSupply } from '../../../api'
import { channelsQueryKeys } from '../../../lib/channel-actions'
import type { VideoSupplyUnit } from '../../../types'

type VideoSupplyTopUpProps = {
  /** null while the channel is not saved yet; top-ups need a channel id. */
  channelId: number | null
  unit: VideoSupplyUnit
  /** Remaining stock from the channel list; null when unknown. */
  remaining: number | null
  disabled: boolean
}

/** Shows the remaining stock and adds a signed amount to it. */
export function VideoSupplyTopUp(props: VideoSupplyTopUpProps) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [amount, setAmount] = useState('')
  const [remaining, setRemaining] = useState<number | null>(props.remaining)
  const mutation = useMutation({
    mutationFn: (delta: number) =>
      addChannelVideoSupply(props.channelId as number, delta),
    retry: false,
    meta: { errorToast: false },
    onSuccess: (value) => {
      setRemaining(value)
      setAmount('')
      toast.success(t('Supply added'))
      void client.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
    },
  })

  const parsed = Number(amount)
  const valid =
    amount.trim() !== '' &&
    Number.isFinite(parsed) &&
    parsed !== 0 &&
    (props.unit === 'usd' || Number.isInteger(parsed))
  const quotaPerUnit = getCurrencyDisplay().config.quotaPerUnit
  const delta =
    props.unit === 'usd' ? Math.round(parsed * quotaPerUnit) : parsed
  let shown = t('Unknown')
  if (remaining !== null) {
    shown =
      props.unit === 'usd'
        ? formatQuotaWithCurrency(remaining)
        : formatNumber(remaining, locale)
  }

  return (
    <div
      role='group'
      aria-label={t('Remaining supply')}
      className='bg-muted/40 flex flex-col gap-3 rounded-md border p-3 sm:flex-row sm:items-end'
    >
      <div className='min-w-0 flex-1 space-y-1'>
        <p className='text-sm font-medium'>
          {t('Remaining supply')}: {shown}
        </p>
        <p className='text-muted-foreground text-xs'>
          {remaining !== null && remaining <= 0
            ? t(
                'Out of stock: scheduling skips this channel until supply is added'
              )
            : t(
                'Only successful tasks consume supply; a negative amount corrects the balance'
              )}
        </p>
        {mutation.isError && (
          <p role='alert' className='text-destructive text-xs'>
            {getServerErrorMessage(mutation.error)}
          </p>
        )}
      </div>
      {props.channelId === null ? (
        <p className='text-muted-foreground text-xs'>
          {t('Save the channel before adding supply')}
        </p>
      ) : (
        <div className='flex items-end gap-2'>
          <div className='space-y-1'>
            <Label htmlFor='video-supply-amount' className='text-xs'>
              {props.unit === 'usd' ? t('Amount (USD)') : t('Videos')}
            </Label>
            <Input
              id='video-supply-amount'
              type='number'
              step={props.unit === 'usd' ? 'any' : 1}
              value={amount}
              onChange={(event) => setAmount(event.target.value)}
              disabled={props.disabled || mutation.isPending}
              className='w-36'
            />
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={props.disabled || mutation.isPending || !valid}
            onClick={() => mutation.mutate(delta)}
          >
            {t('Add supply')}
          </Button>
        </div>
      )}
    </div>
  )
}
