import { useTranslation } from 'react-i18next';
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import type { CPAInstance } from '../types';

export interface CPAToggleConfirmation {
  id: number;
  displayName: string;
  disable: boolean;
}

export interface CPACodexResetConfirmation {
  credentialID: number;
  creditID: string;
  displayName: string;
  expiresAt?: string | null;
}

interface CPACodexResetDialogProps {
  confirmation?: CPACodexResetConfirmation;
  pending: boolean;
  failed: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}

export function CPACodexResetDialog({ confirmation, pending, failed, onOpenChange, onConfirm }: CPACodexResetDialogProps) {
  const { t } = useTranslation();
  const expiresAt = confirmation?.expiresAt ? new Date(confirmation.expiresAt).toLocaleString() : '—';
  return (
    <AlertDialog open={Boolean(confirmation)} onOpenChange={(open) => !pending && onOpenChange(open)}>
      <AlertDialogContent data-testid='cpa-reset-confirmation'>
        <AlertDialogHeader>
          <AlertDialogTitle>{t('cpa.reset.confirmTitle')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('cpa.reset.confirmDescription', { name: confirmation?.displayName, expiresAt })}
          </AlertDialogDescription>
          {failed && <p className='text-destructive mt-2'>{t('cpa.reset.failure')}</p>}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>{t('common.buttons.cancel')}</AlertDialogCancel>
          <Button data-testid='cpa-reset-confirm' onClick={onConfirm} disabled={pending || failed}>
            {t('cpa.reset.use')}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

interface CPACredentialToggleDialogProps {
  confirmation?: CPAToggleConfirmation;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}

export function CPACredentialToggleDialog({ confirmation, pending, onOpenChange, onConfirm }: CPACredentialToggleDialogProps) {
  const { t } = useTranslation();
  return (
    <AlertDialog open={Boolean(confirmation)} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{confirmation?.disable ? t('cpa.credential.disableTitle') : t('cpa.credential.enableTitle')}</AlertDialogTitle>
          <AlertDialogDescription>
            {confirmation?.disable
              ? t('cpa.credential.disableDescription', { name: confirmation?.displayName })
              : t('cpa.credential.enableDescription', { name: confirmation?.displayName })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t('common.buttons.cancel')}</AlertDialogCancel>
          <Button
            data-testid='cpa-toggle-confirm'
            variant={confirmation?.disable ? 'destructive' : 'default'}
            onClick={onConfirm}
            disabled={pending}
          >
            {confirmation?.disable ? t('cpa.credential.disable') : t('cpa.credential.enable')}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

interface CPAInstanceDeleteDialogProps {
  instance?: CPAInstance;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}

export function CPAInstanceDeleteDialog({ instance, pending, onOpenChange, onConfirm }: CPAInstanceDeleteDialogProps) {
  const { t } = useTranslation();
  return (
    <AlertDialog open={Boolean(instance)} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t('cpa.instance.deleteTitle')}</AlertDialogTitle>
          <AlertDialogDescription>{t('cpa.instance.deleteDescription', { name: instance?.name })}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t('common.buttons.cancel')}</AlertDialogCancel>
          <Button data-testid='cpa-delete-confirm' variant='destructive' onClick={onConfirm} disabled={pending}>
            {t('common.buttons.delete')}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
