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
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'

import { useUnifiedVideoSales } from '../hooks/use-unified-video-sales'

export function UnifiedVideoPricingNotice(props: { modelName: string }) {
  const { t } = useTranslation()
  const sales = useUnifiedVideoSales(props.modelName)
  if (!sales) return null
  return (
    <Alert>
      <AlertDescription>
        {t(
          'Unified video sales controls this model. Prices edited here are stored but do not affect video charges.'
        )}
        {sales.official_reference_billing && (
          <p>
            {t(
              'Without reference video: output seconds × output price. With reference video: (output + max(reference, ⌈output × 2/3⌉)) seconds × with-reference price.'
            )}
          </p>
        )}
        {!sales.official_reference_billing &&
          Object.values(sales.resolutions).some(
            (tier) => (tier.input_video_usd_per_second ?? 0) > 0
          ) && (
            <p>
              {t(
                'Output seconds × output price, plus reference video seconds × reference video price.'
              )}
            </p>
          )}
        {sales.disabled && <span>{t('Video sales paused')}</span>}
      </AlertDescription>
    </Alert>
  )
}
