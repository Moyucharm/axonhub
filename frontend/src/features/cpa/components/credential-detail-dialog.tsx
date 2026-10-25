import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { QuotaWindowsBlock } from '@/components/quota-capsule';
import { providerLabel } from '../labels';
import { cpaQuotaItemsToWindows } from '../quota-windows';
import type { CPACredential } from '../types';
import { ClaudeResetGrants } from './claude-reset-grants';
import { CodexResetCredits } from './codex-reset-credits';
import type { CPAClaudeResetConfirmation, CPACodexResetConfirmation, CPACredentialCooldownResetConfirmation } from './confirmation-dialogs';
import { CPACredentialModelsPanel } from './credential-models-panel';
import { activeRoutingCooldowns, CPARoutingCooldownDetails } from './routing-cooldown';

interface CPACredentialDetailDialogProps {
  credential?: CPACredential;
  canWrite: boolean;
  instanceEnabled: boolean;
  resetPending: boolean;
  claudeResetPending: boolean;
  cooldownResetPending: boolean;
  locale: string;
  onOpenChange: (open: boolean) => void;
  onRequestReset: (confirmation: CPACodexResetConfirmation) => void;
  onRequestClaudeReset: (confirmation: CPAClaudeResetConfirmation) => void;
  onRequestCooldownReset: (confirmation: CPACredentialCooldownResetConfirmation) => void;
}

function quotaStateText(credential: CPACredential, t: ReturnType<typeof useTranslation>['t']): string {
  if (credential.quotaState === 'unsupported') return t('cpa.quota.unsupported');
  if (credential.quotaState === 'insufficient_data') return t('cpa.quota.insufficient');
  if (credential.quotaState === 'pending') return t('cpa.quota.pending');
  if (credential.quotaState === 'error') return t('cpa.quota.error');
  return '—';
}

export function CPACredentialDetailDialog({
  credential,
  canWrite,
  instanceEnabled,
  resetPending,
  claudeResetPending,
  cooldownResetPending,
  locale,
  onOpenChange,
  onRequestReset,
  onRequestClaudeReset,
  onRequestCooldownReset,
}: CPACredentialDetailDialogProps) {
  const { t } = useTranslation();
  const displayName = credential ? credential.email || credential.displayName || credential.remoteName : '';

  return (
    <Dialog open={credential != null} onOpenChange={onOpenChange}>
      <DialogContent data-testid='cpa-credential-detail' className='flex h-[80vh] flex-col sm:max-w-4xl'>
        {credential && (
          <>
            <DialogHeader>
              <DialogTitle className='break-all'>{displayName}</DialogTitle>
              <DialogDescription className='break-all'>
                {providerLabel(credential.provider, t)} · {credential.remoteName}
              </DialogDescription>
            </DialogHeader>
            {/* Remount per credential so the models filter and tab reset. */}
            <Tabs key={credential.id} defaultValue='quota' className='flex min-h-0 flex-1 flex-col gap-4'>
              <TabsList className='grid w-full shrink-0 grid-cols-2'>
                <TabsTrigger value='quota'>
                  {t('cpa.detail.quota')}
                </TabsTrigger>
                <TabsTrigger data-testid={`cpa-credential-models-${credential.id}`} value='models'>
                  {t('cpa.models.title')}
                </TabsTrigger>
              </TabsList>
              <TabsContent value='quota' className='min-h-0 overflow-y-auto'>
                {credential.quotaData.items.length > 0 ? (
                  <QuotaWindowsBlock windows={cpaQuotaItemsToWindows(credential.quotaData.items, t, locale)} />
                ) : (
                  <p className='text-muted-foreground text-sm'>{quotaStateText(credential, t)}</p>
                )}
                {credential.provider === 'codex' && (
                  <CodexResetCredits
                    credential={credential}
                    canWrite={canWrite}
                    instanceEnabled={instanceEnabled}
                    resetPending={resetPending}
                    locale={locale}
                    onRequestReset={onRequestReset}
                  />
                )}
                {credential.provider === 'claude' && (
                  <ClaudeResetGrants
                    credential={credential}
                    canWrite={canWrite}
                    instanceEnabled={instanceEnabled}
                    resetPending={claudeResetPending}
                    locale={locale}
                    onRequestReset={onRequestClaudeReset}
                  />
                )}
                <CPARoutingCooldownDetails snapshot={credential.routingCooldown} locale={locale} />
                {canWrite && credential.canResetCooldown && activeRoutingCooldowns(credential.routingCooldown).length > 0 && (
                  <Button
                    data-testid={`cpa-clear-cooldown-${credential.id}`}
                    variant='outline'
                    size='sm'
                    className='mt-3'
                    disabled={cooldownResetPending}
                    onClick={() => onRequestCooldownReset({ credentialID: credential.id, displayName })}
                  >
                    {t('cpa.cooldownReset.action')}
                  </Button>
                )}
              </TabsContent>
              <TabsContent value='models' className='flex min-h-0 flex-col'>
                {instanceEnabled ? (
                  <CPACredentialModelsPanel credentialID={credential.id} />
                ) : (
                  <p className='text-muted-foreground py-6 text-center text-sm'>{t('cpa.models.instanceDisabled')}</p>
                )}
              </TabsContent>
            </Tabs>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
