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
import * as z from 'zod'

import { isCompleteFinitePricingNumber } from '@/features/system-settings/models/model-pricing-core'

export function createExtendedModelFormSchema(t: (key: string) => string) {
  const optionalFiniteNumberString = z
    .string()
    .optional()
    .refine(
      (value) =>
        value === undefined ||
        value === '' ||
        isCompleteFinitePricingNumber(value),
      t('Please enter a valid number')
    )

  return z.object({
    id: z.number().optional(),
    model_name: z.string().min(1, t('Model name is required')),
    description: z.string(),
    icon: z.string(),
    tags: z.array(z.string()),
    vendor_id: z.number().optional(),
    endpoints: z.string(),
    name_rule: z.number(),
    status: z.boolean(),
    sync_official: z.boolean(),
    price: optionalFiniteNumberString,
    ratio: optionalFiniteNumberString,
    cacheRatio: optionalFiniteNumberString,
    createCacheRatio: optionalFiniteNumberString,
    completionRatio: optionalFiniteNumberString,
    imageRatio: optionalFiniteNumberString,
    audioRatio: optionalFiniteNumberString,
    audioCompletionRatio: optionalFiniteNumberString,
  })
}

export type ExtendedModelFormValues = z.infer<
  ReturnType<typeof createExtendedModelFormSchema>
>
