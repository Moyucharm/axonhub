import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { formatLocalDateTime } from '@/utils/format-date-time';
import type { CPARoutingCooldownSnapshot } from '../types';

export function activeRoutingCooldowns(snapshot: CPARoutingCooldownSnapshot, now = Date.now()) {
  return snapshot.known ? snapshot.items.filter((item) => new Date(item.retryAt).getTime() > now) : [];
}

function remainingTime(retryAt: string, locale: string, now: number): string {
  const milliseconds = new Date(retryAt).getTime() - now;
  const [unit, size]: [Intl.RelativeTimeFormatUnit, number] = milliseconds >= 86_400_000
    ? ['day', 86_400_000]
    : milliseconds >= 3_600_000
      ? ['hour', 3_600_000]
      : milliseconds >= 60_000
        ? ['minute', 60_000]
        : ['second', 1_000];
  return new Intl.RelativeTimeFormat(locale, { numeric: 'always' }).format(Math.ceil(milliseconds / size), unit);
}

export function CPARoutingCooldownBadge({ snapshot }: { snapshot: CPARoutingCooldownSnapshot }) {
  const { t } = useTranslation();
  const items = activeRoutingCooldowns(snapshot);
  if (items.length === 0) return null;
  return (
    <Badge variant='secondary'>
      {t(items.every((item) => item.scope === 'model') ? 'cpa.routingCooldown.modelTitle' : 'cpa.routingCooldown.title')}
    </Badge>
  );
}

export function CPARoutingCooldownDetails({ snapshot, locale }: { snapshot: CPARoutingCooldownSnapshot; locale: string }) {
  const { t } = useTranslation();
  const now = Date.now();
  const items = activeRoutingCooldowns(snapshot, now);
  return (
    <section className='mt-4 space-y-2 border-t pt-4' aria-label={t('cpa.routingCooldown.title')}>
      <h4 className='text-sm font-medium'>{t('cpa.routingCooldown.title')}</h4>
      {snapshot.observedAt && (
        <p className='text-muted-foreground text-xs'>
          {t('cpa.routingCooldown.observedAt', { time: formatLocalDateTime(snapshot.observedAt, locale) ?? '—' })}
        </p>
      )}
      {!snapshot.known ? (
        <p className='text-muted-foreground text-sm'>{t('cpa.routingCooldown.unknown')}</p>
      ) : items.length === 0 ? (
        <p className='text-muted-foreground text-sm'>{t('cpa.routingCooldown.empty')}</p>
      ) : (
        <ul className='grid gap-2 lg:grid-cols-2'>
          {items.map((item, index) => (
            <li key={`${item.scope}-${item.modelKey}-${item.retryAt}-${index}`} className='min-w-0 space-y-1 rounded-md border p-3 text-sm'>
              <div className='flex flex-wrap items-center gap-2'>
                <Badge variant='outline'>{t(`cpa.routingCooldown.scope.${item.scope}`)}</Badge>
                {item.scope === 'model' && <code className='break-all text-xs'>{item.modelKey}</code>}
                <span>{t(`cpa.routingCooldown.reason.${item.reason}`)}</span>
              </div>
              <p>{t('cpa.routingCooldown.retryAt', { time: formatLocalDateTime(item.retryAt, locale) ?? '—' })}</p>
              <p className='text-muted-foreground'>{t('cpa.routingCooldown.remaining', { time: remainingTime(item.retryAt, locale, now) })}</p>
              {item.httpStatus != null && <p>{t('cpa.routingCooldown.httpStatus', { status: item.httpStatus })}</p>}
              {item.backoffLevel != null && <p>{t('cpa.routingCooldown.backoffLevel', { level: item.backoffLevel })}</p>}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
