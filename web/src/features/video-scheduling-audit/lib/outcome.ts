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

export function auditOutcomeLabel(outcome: string, t: TFunction): string {
  switch (outcome) {
    case 'SUCCESS':
      return t('Success')
    case 'FAILURE':
      return t('Failed')
    case 'cancelled':
      return t('Cancelled')
    case 'outcome_unknown':
      return t('Submit outcome unknown')
    case 'no_candidate':
      return t('No candidate')
    case 'rejected':
      return t('Rejected')
    case 'local':
    case 'local_failure':
      return t('Not submitted')
    case 'persistence_failure':
      return t('Accepted without task')
    case 'internal_failure':
      return t('Internal error')
    case 'accepted':
      return t('Accepted')
    case 'submitted':
      return t('Pending')
    case '':
      return t('Not executed')
    default:
      return t('Pending')
  }
}
