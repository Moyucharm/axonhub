import { RotateCcw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import type { CPAClaudeReset, CPAClaudeResetGrant, CPACredential } from '../types';
import { resetCreditTiming } from './codex-reset-credits';
import type { CPAClaudeResetConfirmation } from './confirmation-dialogs';

// Mirrors the backend ClaudeResetBlocker so the panel can explain why the
// button is disabled; the backend re-reads the provider before every claim.
export function claudeResetBlocker(status: CPAClaudeReset, grant: CPAClaudeResetGrant, now = Date.now()): string | null {
  if (!status.eligible) return 'ineligible';
  if (status.cooldownUntil && Date.parse(status.cooldownUntil) > now) return 'cooldown';
  if (grant.paused) return 'paused';
  if (!grant.usableNow) return 'not_usable';
  if (grant.resetsLeft <= 0) return 'exhausted';
  if (grant.startsAt && Date.parse(grant.startsAt) > now) return 'not_started';
  if (grant.endsAt && Date.parse(grant.endsAt) <= now) return 'expired';
  if (grant.useRequiresLimit && !status.atLimit) return 'not_limited';
  return null;
}

export interface ClaudeResetAction {
  grant: CPAClaudeResetGrant;
  retry: boolean;
  blocker: string | null;
}

// The single action the panel offers: retrying an uncertain claim inside its
// window takes precedence; otherwise the provider's next grant (or the first
// claimable one by ID), falling back to the next grant to explain a blocker.
export function claudeResetAction(status: CPAClaudeReset, now = Date.now()): ClaudeResetAction | null {
  const retryable = status.grants.find((grant) => grant.uncertain && grant.retryUntil && Date.parse(grant.retryUntil) > now);
  if (retryable) return { grant: retryable, retry: true, blocker: null };
  const open = status.grants.filter((grant) => !grant.uncertain);
  const claimable = open.filter((grant) => !claudeResetBlocker(status, grant, now));
  const selected =
    claimable.find((grant) => grant.id === status.nextGrantID) ?? [...claimable].sort((a, b) => a.id.localeCompare(b.id))[0];
  if (selected) return { grant: selected, retry: false, blocker: null };
  const fallback = open.find((grant) => grant.id === status.nextGrantID) ?? open[0];
  if (!fallback) return null;
  return { grant: fallback, retry: false, blocker: claudeResetBlocker(status, fallback, now) ?? 'unavailable' };
}

// An uncertain claim may have spent at most one use, so count the rest.
export function claudeResetRemaining(status: CPAClaudeReset): number {
  return status.grants.reduce((sum, grant) => sum + Math.max(0, grant.uncertain ? grant.resetsLeft - 1 : grant.resetsLeft), 0);
}

export function claudeResetIsCurrent(credential: Pick<CPACredential, 'quotaState' | 'quotaData'>): boolean {
  return credential.quotaState === 'success' && !credential.quotaData.claudeResetFailed && Boolean(credential.quotaData.claudeReset);
}

export function claudeResetConfirmation(credential: CPACredential, action: ClaudeResetAction): CPAClaudeResetConfirmation {
  return {
    credentialID: credential.id,
    grantID: action.grant.id,
    displayName: credential.email || credential.displayName || credential.remoteName,
    label: action.grant.label || action.grant.id,
    endsAt: action.grant.endsAt,
    retry: action.retry,
  };
}

interface ClaudeResetGrantsProps {
  credential: CPACredential;
  canWrite: boolean;
  instanceEnabled: boolean;
  resetPending: boolean;
  locale: string;
  onRequestReset: (confirmation: CPAClaudeResetConfirmation) => void;
}

export function ClaudeResetGrants({ credential, canWrite, instanceEnabled, resetPending, locale, onRequestReset }: ClaudeResetGrantsProps) {
  const { t } = useTranslation();
  const status = credential.quotaData.claudeReset;

  if (!claudeResetIsCurrent(credential) || !status) {
    if (!credential.quotaData.claudeResetFailed) return null;
    return (
      <p data-testid={`cpa-claude-reset-failed-${credential.id}`} className='text-muted-foreground mt-4 text-sm'>
        {t('cpa.claudeReset.readFailed')}
      </p>
    );
  }
  if (!status.eligible) {
    return (
      <p data-testid={`cpa-claude-reset-ineligible-${credential.id}`} className='text-muted-foreground mt-4 text-sm'>
        {t('cpa.claudeReset.ineligible', { reason: t(`cpa.claudeReset.reason.${status.ineligibleReason || 'unknown'}`) })}
      </p>
    );
  }
  if (status.grants.length === 0) {
    return (
      <p data-testid={`cpa-claude-reset-empty-${credential.id}`} className='text-muted-foreground mt-4 text-sm'>
        {t('cpa.claudeReset.empty')}
      </p>
    );
  }

  const action = claudeResetAction(status);
  const lockedGrant = status.grants.find((grant) => grant.uncertain && !(grant.retryUntil && Date.parse(grant.retryUntil) > Date.now()));
  const remaining = claudeResetRemaining(status);
  const canUse = canWrite && instanceEnabled && !resetPending && Boolean(action) && !action?.blocker;
  const cooldown = status.cooldownUntil ? resetCreditTiming(status.cooldownUntil, locale) : null;

  return (
    <section data-testid={`cpa-claude-reset-${credential.id}`} className='mt-4 rounded-lg border p-4 text-sm'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='space-y-1'>
          <p className='flex items-center gap-2 font-medium'>
            <RotateCcw className='text-muted-foreground h-4 w-4' />
            {t('cpa.claudeReset.remaining', { count: remaining })}
          </p>
          {action?.retry && action.grant.retryUntil && (
            <p className='text-amber-600 dark:text-amber-400 text-xs'>
              {t('cpa.claudeReset.retryHint', { time: resetCreditTiming(action.grant.retryUntil, locale)?.absolute ?? '—' })}
            </p>
          )}
          {lockedGrant && <p className='text-destructive text-xs'>{t('cpa.claudeReset.locked')}</p>}
          {action?.blocker && (
            <p className='text-muted-foreground text-xs'>
              {t(`cpa.claudeReset.blocker.${action.blocker}`, { time: cooldown?.absolute ?? '—' })}
            </p>
          )}
        </div>
        {action && (
          <Button
            data-testid={`cpa-claude-reset-credential-${credential.id}`}
            variant='outline'
            size='sm'
            disabled={!canUse}
            onClick={() => onRequestReset(claudeResetConfirmation(credential, action))}
          >
            {action.retry ? t('cpa.claudeReset.retry') : t('cpa.claudeReset.use')}
          </Button>
        )}
      </div>
      <ul className='mt-3 divide-y overflow-hidden rounded-md border'>
        {status.grants.map((grant) => {
          const ends = resetCreditTiming(grant.endsAt, locale);
          const clears = grant.clears.map((window) => t(`cpa.claudeReset.window.${window}`)).join(' / ');
          return (
            <li key={grant.id} className='flex items-start justify-between gap-3 px-3 py-1.5'>
              <span className='flex flex-col gap-0.5'>
                <span>{grant.label || grant.id}</span>
                {clears && <span className='text-muted-foreground text-xs'>{t('cpa.claudeReset.clears', { windows: clears })}</span>}
              </span>
              <span className='flex flex-col items-end gap-0.5 text-right'>
                <span className='tabular-nums'>
                  {grant.uncertain
                    ? t('cpa.claudeReset.uncertainBadge')
                    : t('cpa.claudeReset.count', { left: grant.resetsLeft, total: grant.resetsTotal })}
                </span>
                <span className='text-muted-foreground text-xs'>
                  {t('cpa.claudeReset.ends', { time: ends ? `${ends.absolute} · ${ends.relative}` : '—' })}
                </span>
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
