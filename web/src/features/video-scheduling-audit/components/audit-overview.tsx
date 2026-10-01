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

import { videoReliabilityLabel } from '@/features/channels/lib/video-reliability'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber, formatPercent } from '@/lib/format'

import type { AuditStats } from '../types'

export function AuditOverview({ stats }: { stats: AuditStats }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <div className='space-y-3'>
      <section className='space-y-2 border-b pb-4'>
        <h3 className='font-medium'>{t('Closed-loop reliability')}</h3>
        {stats.reliability?.supported ? (
          <>
            <dl className='grid grid-cols-2 gap-4 lg:grid-cols-4'>
              {[
                {
                  label: t('Accepted generation rate'),
                  rate: stats.reliability.generation_success,
                },
                {
                  label: t('Channel completion rate'),
                  rate: stats.reliability.channel_completion,
                },
                {
                  label: t('Request completion rate'),
                  rate: stats.reliability.request_completion,
                },
                {
                  label: t('Limited validation share'),
                  rate: stats.reliability.limited_share,
                },
              ].map(({ label, rate }) => (
                <div key={label}>
                  <dt className='text-muted-foreground text-xs'>{label}</dt>
                  <dd className='text-xl font-semibold tabular-nums'>
                    {rate.value == null
                      ? t('Unknown')
                      : formatPercent(rate.value * 100)}
                  </dd>
                  <dd className='text-muted-foreground text-xs'>
                    {formatNumber(rate.numerator, locale)} /{' '}
                    {formatNumber(rate.denominator, locale)}
                  </dd>
                </div>
              ))}
            </dl>
            <p className='text-muted-foreground text-xs'>
              {t('Pending')}:{' '}
              {formatNumber(stats.reliability.request_pending, locale)} ·{' '}
              {t('Unknown')}:{' '}
              {formatNumber(stats.reliability.request_unknown, locale)} ·{' '}
              {t('Data incomplete')}:{' '}
              {formatNumber(stats.reliability.request_missing, locale)} ·{' '}
              {t('User errors / cancellations')}:{' '}
              {formatNumber(stats.reliability.request_user, locale)} /{' '}
              {formatNumber(stats.reliability.request_cancelled, locale)}
            </p>
            <p className='text-muted-foreground text-xs'>
              {t(
                'User errors and cancellations are excluded from reliability denominators. Pending, unknown and missing outcomes cannot establish qualification.'
              )}
            </p>
          </>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {stats.reliability
              ? videoReliabilityLabel(stats.reliability.reason, t)
              : t('Legacy snapshot has no reliability evidence')}
          </p>
        )}
        {Boolean(stats.reliability?.collection_write_failures) && (
          <p role='status' className='text-warning text-sm'>
            {t('Health collection failures')}:{' '}
            {formatNumber(
              stats.reliability?.collection_write_failures ?? 0,
              locale
            )}
          </p>
        )}
      </section>
      <dl className='grid grid-cols-2 gap-x-6 gap-y-4 border-y py-4 md:grid-cols-4 xl:grid-cols-7'>
        {[
          [t('Request success rate'), stats.request_success],
          [t('Submit acceptance rate'), stats.submit_acceptance],
          [t('Generation success rate'), stats.generation_success],
          [t('Retry rate'), stats.retry],
          [t('No candidate rate'), stats.no_candidate],
        ].map(
          ([label, rate]) =>
            typeof rate === 'object' && (
              <div key={String(label)}>
                <dt className='text-muted-foreground text-xs'>
                  {String(label)}
                </dt>
                <dd className='text-xl font-semibold tabular-nums'>
                  {rate.value === null ? '—' : formatPercent(rate.value * 100)}
                </dd>
                <dd className='text-muted-foreground text-xs'>
                  {formatNumber(rate.numerator, locale)} /{' '}
                  {formatNumber(rate.denominator, locale)}
                </dd>
              </div>
            )
        )}
        <div>
          <dt className='text-muted-foreground text-xs'>
            {t('Observed completion P95')}
          </dt>
          <dd className='text-xl font-semibold tabular-nums'>
            {stats.p95_ms === null
              ? '—'
              : `${formatNumber(stats.p95_ms / 1000, locale)} s`}
          </dd>
          <dd className='text-muted-foreground text-xs'>
            {t('Samples')}: {formatNumber(stats.duration_samples, locale)} · P50{' '}
            {stats.p50_ms === null
              ? '—'
              : `${formatNumber(stats.p50_ms / 1000, locale)} s`}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground text-xs'>
            {t('Mean estimated cost')}
          </dt>
          <dd className='text-xl font-semibold tabular-nums'>
            {formatBillingCurrencyFromUSD(stats.mean_cost_usd, {
              locale,
              digitsSmall: 6,
              digitsLarge: 6,
              abbreviate: false,
            })}
          </dd>
          <dd className='text-muted-foreground text-xs'>
            {t('Samples')}: {formatNumber(stats.cost_samples, locale)} ·{' '}
            {t('Missing quotes')}: {formatNumber(stats.cost_missing, locale)}
          </dd>
        </div>
      </dl>
      <div className='flex flex-wrap gap-x-5 gap-y-1 text-xs'>
        {[
          [t('Total requests'), stats.total],
          [t('Pending'), stats.pending],
          [t('Unknown'), stats.unknown],
          [t('Cancelled'), stats.cancelled],
          [t('Data incomplete'), stats.missing],
          [t('Submit outcome unknown'), stats.submit_unknown],
          [t('Not submitted'), stats.submit_local],
          [t('Health ignored'), stats.health_ignored],
        ].map(([label, value]) => (
          <span key={label}>
            {label}: <strong>{formatNumber(Number(value), locale)}</strong>
          </span>
        ))}
        <span>
          {t('Oldest pending')}:{' '}
          {stats.oldest_pending_ms === null
            ? '—'
            : `${formatNumber(stats.oldest_pending_ms / 1000, locale)} s`}
        </span>
        <span>
          {t('Health generation samples')}:{' '}
          {formatNumber(stats.health_success.numerator, locale)} /{' '}
          {formatNumber(stats.health_success.denominator, locale)}
        </span>
        <span>
          {t('Unknown health attribution')}:{' '}
          {formatNumber(stats.health_unknown, locale)}
        </span>
        <span>
          {t('Timed out')}: {formatNumber(stats.timeouts, locale)}
        </span>
        <span>
          {t('Shadow recommendation differences')}:{' '}
          {formatNumber(stats.shadow_difference.numerator, locale)} /{' '}
          {formatNumber(stats.shadow_difference.denominator, locale)}
        </span>
        <span>
          {t('Shadow estimated cost difference')}:{' '}
          {formatBillingCurrencyFromUSD(stats.mean_shadow_cost_delta, {
            locale,
            digitsSmall: 6,
            digitsLarge: 6,
            abbreviate: false,
          })}{' '}
          · {t('Samples')}: {formatNumber(stats.shadow_cost_samples, locale)}
        </span>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Completion includes retries and polling delay. Cost is the final channel quote, not total invoiced cost.'
        )}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Compare matching models, specifications and groups after the task timeout window: {{seconds}} seconds.',
          { seconds: formatNumber(stats.maturity_wait_ms / 1000, locale) }
        )}
      </p>
      {Object.keys(stats.exclusions).length > 0 && (
        <p className='text-muted-foreground text-xs'>
          {t('Candidate exclusions')}:{' '}
          {Object.entries(stats.exclusions)
            .map(
              ([reason, count]) => `${reason}: ${formatNumber(count, locale)}`
            )
            .join(' · ')}{' '}
          ({t('Candidates')}: {formatNumber(stats.candidates, locale)} ·{' '}
          {t('Selections')}: {formatNumber(stats.selections, locale)})
        </p>
      )}
      <p className='text-muted-foreground text-xs'>
        {t('As of')}: {new Date(stats.as_of).toLocaleString(locale)}
      </p>
    </div>
  )
}
