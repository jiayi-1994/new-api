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
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { exportAudit } from '../api'
import type { AuditFilters } from '../types'

export function AuditExportButton({ filters }: { filters: AuditFilters }) {
  const { t } = useTranslation()
  const [continuation, setContinuation] = useState('')
  const download = useMutation({
    mutationFn: () => exportAudit(filters, continuation),
    onSuccess: (result) => {
      const url = URL.createObjectURL(
        new Blob([result.text], { type: 'application/x-ndjson' })
      )
      const link = document.createElement('a')
      link.href = url
      link.download = 'video-scheduling-audit.ndjson'
      link.click()
      URL.revokeObjectURL(url)
      setContinuation(result.continuation)
    },
  })
  return (
    <div className='flex flex-wrap items-center gap-2'>
      <Button
        variant='outline'
        disabled={download.isPending}
        onClick={() => download.mutate()}
      >
        {continuation ? t('Continue export') : t('Export NDJSON')}
      </Button>
      {continuation && (
        <span role='status' className='text-warning text-xs'>
          {t('Partial export. Continue to download the remaining selections.')}
        </span>
      )}
      {download.isError && (
        <span role='alert' className='text-destructive text-xs'>
          {t('Export failed')}
        </span>
      )}
    </div>
  )
}
