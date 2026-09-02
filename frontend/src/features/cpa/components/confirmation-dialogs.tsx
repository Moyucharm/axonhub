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
