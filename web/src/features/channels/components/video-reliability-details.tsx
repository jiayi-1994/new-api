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

import { StatusBadge } from '@/components/status-badge'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import type {
  VideoReliability,
  VideoReliabilityEvidence,
} from '@/features/system-settings/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatPercent } from '@/lib/format'

import { videoReliabilityLabel } from '../lib/video-reliability'

export function VideoReliabilityDetails(props: {
  health?: VideoReliability | null
  asOf?: number
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const h = props.health
  if (!h) {
    return (
      <span className='text-muted-foreground'>
        {t('Health state unavailable')}
      </span>
    )
  }
  const expired =
    h.qualification != null &&
    h.qualification.expires_at <= (props.asOf ?? h.qualification.as_of)
  let state = h.state
  if (state === 'normal' && expired) state = 'unverified'
  const complete = h.integrity === 'complete'
  let variant: 'success' | 'warning' | 'danger' = 'warning'
  if (state === 'normal' && complete) variant = 'success'
  if (state === 'blocked') variant = 'danger'
  const evidence =
    state === 'normal'
      ? h.qualification
      : (h.current ?? h.recovery ?? h.qualification)
  const rate = (e: VideoReliabilityEvidence, generation: boolean) => {
    const denominator =
      e.succeeded + e.generation_failed + (generation ? 0 : e.rejected)
    const value =
      denominator > 0
        ? formatPercent((e.succeeded / denominator) * 100)
        : t('Unknown')
    return `${value} (${formatNumber(e.succeeded, locale)}/${formatNumber(denominator, locale)})`
  }
  const date = (value: number) =>
    value > 0 ? new Date(value * 1000).toLocaleString(locale) : '—'
  return (
    <div className='min-w-52 space-y-1 text-xs'>
      <StatusBadge variant={variant} copyable={false}>
        {videoReliabilityLabel(state, t)}
      </StatusBadge>
      <p>
        {videoReliabilityLabel(
          expired && h.state === 'normal' ? 'evidence_expired' : h.reason,
          t
        )}
      </p>
      {!complete && (
        <p className='text-warning'>{t('Health evidence incomplete')}</p>
      )}
      {evidence ? (
        <>
          <p>
            {t('Accepted generation rate')}: {rate(evidence, true)}
          </p>
          <p>
            {t('Channel completion rate')}: {rate(evidence, false)}
          </p>
          <p className='text-muted-foreground'>
            {t('Pending')}: {formatNumber(evidence.pending, locale)} ·{' '}
            {t('Unknown')}: {formatNumber(evidence.unknown, locale)} ·{' '}
            {t('Data incomplete')}: {formatNumber(evidence.missing, locale)}
          </p>
        </>
      ) : (
        <p>
          {t('Insufficient samples')} · {t('Unknown')}
        </p>
      )}
      <Accordion>
        <AccordionItem value='evidence'>
          <AccordionTrigger className='py-1 text-xs'>
            {t('Evidence details')}
          </AccordionTrigger>
          <AccordionContent>
            <dl className='space-y-1 text-xs'>
              <div>
                <dt>{t('State version / validation round')}</dt>
                <dd>
                  {formatNumber(h.state_version, locale)} /{' '}
                  {formatNumber(h.validation_round, locale)}
                </dd>
              </div>
              {evidence && (
                <>
                  <div>
                    <dt>{t('Evidence source')}</dt>
                    <dd>{videoReliabilityLabel(evidence.source, t)}</dd>
                  </div>
                  <div>
                    <dt>{t('Observation interval')}</dt>
                    <dd>
                      {date(evidence.batch_start)} – {date(evidence.batch_end)}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('Submitted / accepted')}</dt>
                    <dd>
                      {formatNumber(evidence.submitted, locale)} /{' '}
                      {formatNumber(evidence.accepted, locale)}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('User errors / cancellations')}</dt>
                    <dd>
                      {formatNumber(evidence.user, locale)} /{' '}
                      {formatNumber(evidence.cancelled, locale)}
                    </dd>
                  </div>
                </>
              )}
              {h.qualification && (
                <>
                  <div>
                    <dt>{t('Qualification evidence')}</dt>
                    <dd>
                      {videoReliabilityLabel(h.qualification.source, t)} ·{' '}
                      {date(h.qualification.batch_start)} –{' '}
                      {date(h.qualification.batch_end)}
                    </dd>
                  </div>
                  <div>
                    <dt>{t('Accepted generation rate')}</dt>
                    <dd>{rate(h.qualification, true)}</dd>
                  </div>
                  <div>
                    <dt>{t('Channel completion rate')}</dt>
                    <dd>{rate(h.qualification, false)}</dd>
                  </div>
                  <div>
                    <dt>{t('Qualification expires')}</dt>
                    <dd>{date(h.qualification.expires_at)}</dd>
                  </div>
                </>
              )}
              {state === 'normal' && h.current && (
                <div>
                  <dt>{t('Current observation')}</dt>
                  <dd>
                    {rate(h.current, true)} · {rate(h.current, false)} ·{' '}
                    {t('Pending')}: {formatNumber(h.current.pending, locale)} ·{' '}
                    {t('Unknown')}: {formatNumber(h.current.unknown, locale)}
                  </dd>
                </div>
              )}
              <div>
                <dt>{t('Validation expires')}</dt>
                <dd>
                  {date(
                    h.state === 'recovering'
                      ? h.recovery_expires
                      : h.validation_expires
                  )}
                </dd>
              </div>
              <div>
                <dt>{t('Last validation')}</dt>
                <dd>{date(h.last_validation_at)}</dd>
              </div>
            </dl>
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </div>
  )
}
