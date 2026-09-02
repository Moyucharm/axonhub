import { AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import type { CPAInstance } from '../types';
import { versionBelow } from '../version';

export function CPAInstanceAlerts({ instance }: { instance: CPAInstance }) {
  const { t } = useTranslation();

  return (
    <>
      {instance.insecureSkipTLS && (
        <Alert variant='destructive'>
          <AlertTriangle className='h-4 w-4' />
          <AlertTitle>{t('cpa.warnings.insecureTLS.title')}</AlertTitle>
          <AlertDescription>{t('cpa.warnings.insecureTLS.active')}</AlertDescription>
        </Alert>
      )}
      {instance.serverVersion && versionBelow(instance.serverVersion, '7.1.0') && (
        <Alert>
          <AlertTriangle className='h-4 w-4' />
          <AlertTitle>{t('cpa.warnings.oldVersion.title')}</AlertTitle>
          <AlertDescription>{t('cpa.warnings.oldVersion.description', { version: instance.serverVersion })}</AlertDescription>
        </Alert>
      )}
      {instance.connectionStatus === 'error' && (
        <Alert variant='destructive'>
          <AlertTriangle className='h-4 w-4' />
          <AlertTitle>{t('cpa.warnings.connection.title')}</AlertTitle>
          <AlertDescription>{instance.lastError ?? t('cpa.warnings.connection.description')}</AlertDescription>
        </Alert>
      )}
    </>
  );
}
