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
import type { ApiResponse } from '@/features/profile/types'
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

import type { AuditDetail, AuditFilters, AuditList, AuditStats } from './types'

const BASE = '/api/channel/video_schedule'

async function queryAudit<T>(path: string, params?: AuditFilters) {
  const { data } = await api.get<ApiResponse<T>>(BASE + path, { params })
  if (!data.success || !data.data) throw createServerError(data)
  return data.data
}
export const getAuditList = (filters: AuditFilters) =>
  queryAudit<AuditList>('/audits', filters)
export const getAuditStats = (filters: AuditFilters) =>
  queryAudit<AuditStats>('/audit_stats', filters)
export const getAuditDetail = (requestId: string) =>
  queryAudit<AuditDetail>(`/audits/${encodeURIComponent(requestId)}`)

export async function exportAudit(
  filters: AuditFilters,
  continuation?: string
) {
  const { data } = await api.get<string>(`${BASE}/audit_export`, {
    params: { ...filters, continuation },
    responseType: 'text',
  })
  const lines = data.trim().split('\n')
  const footer = JSON.parse(lines.at(-1) ?? '{}') as {
    type?: string
    partial?: boolean
    continuation?: string
    success?: boolean
    message?: string
  }
  if (footer.type !== 'footer') throw createServerError(footer)
  if (footer.partial && !footer.continuation) {
    throw new Error('Missing export continuation')
  }
  return {
    text: data,
    partial: footer.partial === true,
    continuation: footer.continuation || '',
  }
}
