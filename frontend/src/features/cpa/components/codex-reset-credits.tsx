import { Ticket } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { formatLocalDateTime } from '@/utils/format-date-time';
import type { CPACredential } from '../types';
import type { CPACodexResetConfirmation } from './confirmation-dialogs';

interface CodexResetCreditsProps {
  credential: CPACredential;
  canWrite: boolean;
  instanceEnabled: boolean;
  resetPending: boolean;
  locale: string;
  onRequestReset: (confirmation: CPACodexResetConfirmation) => void;
}

// Reset cards arrive with the quota refresh, so this panel renders stored data
// only. Labels mirror the provider panel: absolute local time plus a relative
// badge.
function resetCreditTiming(value: string | null | undefined, locale: string) {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  const absolute = formatLocalDateTime(value, locale) ?? value;
  const hours = (date.getTime() - Date.now()) / 3_600_000;
  const unit = hours < 36 ? 'hour' : 'day';
  const amount = Math.max(0, Math.round(unit === 'hour' ? hours : hours / 24));
  const relative = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(amount, unit);
  return { absolute, relative };
}

function resetCreditIsUsable(expiresAt: string | null | undefined) {
  if (!expiresAt) return true;
  const date = new Date(expiresAt);
  return Number.isNaN(date.getTime()) || date.getTime() > Date.now();
}

export function CodexResetCredits({
  credential,
  canWrite,
  instanceEnabled,
  resetPending,
  locale,
  onRequestReset,
}: CodexResetCreditsProps) {
  const { t } = useTranslation();
  const { resetCredits, resetCreditsFailed } = credential.quotaData;
  const refreshed = credential.quotaState === 'success';

  if (!refreshed || resetCreditsFailed) {
    // A failed refresh can only offer stale data, so never render an actionable
    // list or an enabled reset button from it.
    if (resetCredits.length === 0 && !resetCreditsFailed) return null;
    return (
      <p data-testid={`cpa-reset-failed-${credential.id}`} className='text-muted-foreground mt-4 text-sm'>
        {t('cpa.reset.readFailed')}
      </p>
    );
  }

  // Cards expire between quota refreshes; undated cards stay manual-only.
  const credits = resetCredits.filter((credit) => resetCreditIsUsable(credit.expiresAt));
  if (credits.length === 0) {
    return (
      <p data-testid={`cpa-reset-empty-${credential.id}`} className='text-muted-foreground mt-4 text-sm'>
        {t('cpa.reset.empty')}
      </p>
    );
  }

  const firstCredit = credits[0];
  const earliest = resetCreditTiming(firstCredit.expiresAt, locale);

  return (
    <section data-testid={`cpa-reset-credits-${credential.id}`} className='mt-4 rounded-lg border p-4 text-sm'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='space-y-1'>
          <p className='flex items-center gap-2 font-medium'>
            <Ticket className='text-muted-foreground h-4 w-4' />
            {t('cpa.reset.count', { count: credits.length })}
          </p>
          {earliest && (
            <p className='text-muted-foreground text-xs'>
              {t('cpa.reset.earliest', { time: `${earliest.absolute} · ${earliest.relative}` })}
            </p>
          )}
        </div>
        <Button
          data-testid={`cpa-reset-credential-${credential.id}`}
          variant='outline'
          size='sm'
          disabled={!canWrite || !instanceEnabled || resetPending}
          onClick={() =>
            onRequestReset({
              credentialID: credential.id,
              creditID: firstCredit.id,
              displayName: credential.email || credential.displayName || credential.remoteName,
              expiresAt: firstCredit.expiresAt,
            })
          }
        >
          {t('cpa.reset.use')}
        </Button>
      </div>
      <ul className='mt-3 divide-y overflow-hidden rounded-md border'>
        {credits.map((credit, index) => {
          const expiry = resetCreditTiming(credit.expiresAt, locale);
          const granted = resetCreditTiming(credit.grantedAt, locale);
          const details = [
            credit.title,
            credit.resetType ? `${t('cpa.reset.type')}: ${credit.resetType}` : '',
            `${t('cpa.reset.granted')}: ${granted?.absolute ?? '—'}`,
          ].filter((detail) => detail !== '');
          return (
            <li key={credit.id} className='flex items-start justify-between gap-3 px-3 py-1.5'>
              <span className='text-muted-foreground'>{t('cpa.reset.item', { index: index + 1 })}</span>
              <span className='flex flex-col items-end gap-0.5 text-right'>
                <span className='flex items-baseline gap-2'>
                  <span>{expiry?.absolute ?? '—'}</span>
                  {expiry && <span className='text-muted-foreground text-xs'>{expiry.relative}</span>}
                </span>
                <span className='text-muted-foreground text-xs'>{details.join(' · ')}</span>
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
