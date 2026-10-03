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
import type { TFunction } from 'i18next'
import { z } from 'zod'

export const videoHealthRecoverySchema = z.object({
  note: z
    .string()
    .trim()
    .min(1, 'Enter a reason for restoring scheduling eligibility.')
    .refine((value) => [...value].length <= 500, {
      message: 'Use 500 characters or fewer.',
    }),
})

export type VideoHealthRecoveryValues = z.infer<
  typeof videoHealthRecoverySchema
>

export function videoReliabilityLabel(
  value: string | undefined,
  t: TFunction
): string {
  switch (value) {
    case 'normal':
      return t('Verified')
    case 'qualified':
      return t('Qualified evidence')
    case 'activation_validation':
      return t('Activation requires validation')
    case 'activation requires recovery verification':
      return t('Activation requires recovery verification')
    case 'recovery verification expired':
      return t('Recovery verification expired')
    case 'recovery verification':
      return t('Recovery verification')
    case 'unverified':
      return t('Unverified')
    case 'blocked':
      return t('Blocked')
    case 'recovering':
    case 'recover':
    case 'recovery':
      return t('Recovery verification')
    case 'explore':
    case 'cold_start':
      return t('Cold-start validation')
    case 'revalidate':
    case 'revalidation':
      return t('Revalidation')
    case 'window':
      return t('Observation window')
    case 'new_channel':
      return t('New channel')
    case 'upstream_configuration_changed':
      return t('Upstream configuration changed; validation required')
    case 'unknown_submission_reviewed':
      return t('Unknown submission reviewed; recovery required')
    case 'manual_recovery_requested':
      return t('Scheduling recovery requested')
    case 'insufficient_samples':
    case 'health samples insufficient':
      return t('Insufficient samples')
    case 'evidence_expired':
    case 'health qualification expired':
      return t('Qualification expired')
    case 'health_state_unavailable':
      return t('Health state unavailable')
    case 'health evidence incomplete':
      return t('Health evidence incomplete')
    case 'no_normal_candidate':
      return t('No verified candidates; limited validation')
    case 'validation_budget':
      return t('Additional validation traffic')
    case 'hard_filter_exhausted':
      return t('No candidates pass the admission rules')
    case 'validation_slots_full':
      return t('Validation slots full')
    case 'recovery_cooldown':
      return t('Recovery cooldown')
    case 'recovery_disabled':
      return t('Recovery disabled')
    case 'admission_conflict':
      return t('Admission conflict')
    case 'margin below minimum':
      return t('Margin below minimum')
    case 'positive sell price required':
      return t('Positive sell price required')
    case 'generation rate below minimum':
      return t('Generation rate below minimum')
    case 'overall completion below minimum':
      return t('Channel completion below minimum')
    case 'waiting for recovery verification':
      return t('Waiting for recovery verification')
    case 'stability gap too large':
      return t('Stability gap too large')
    case 'lower priority':
      return t('Lower priority')
    case 'lower quality tier':
      return t('Lower quality tier')
    case 'higher cost at same quality':
      return t('Higher cost at the same quality')
    case 'validation order':
      return t('Waiting for validation turn')
    case 'new upstream failure':
      return t('New upstream failure')
    case 'submit outcome unknown':
      return t('Submit outcome unknown')
    case 'success':
      return t('Success')
    case 'accepted':
      return t('Accepted')
    case 'rejected':
      return t('Rejected')
    case 'dispatching':
      return t('Submitting')
    case 'upstream':
      return t('Upstream failure')
    case 'user':
      return t('User error')
    case 'cancelled':
      return t('Cancelled')
    case 'unknown':
      return t('Unknown')
    case 'supported':
      return t('Available')
    case 'unavailable':
      return t('Health state unavailable')
    case 'unsupported_health_filter':
      return t('Health metrics do not support these filters')
    case 'no_health_facts':
      return t('No health facts for this period')
    case undefined:
    case '':
      return '—'
    default:
      return value
  }
}
