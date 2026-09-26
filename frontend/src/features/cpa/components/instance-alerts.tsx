import { AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { CPAInstance } from '../types';
import { versionBelow } from '../version';

export function CPAInstanceAlerts({ instance }: { instance: CPAInstance }) {
  const { t } = useTranslation();

  const hasConnectionError = instance.connectionStatus === 'error';
  const hasInsecureTLS = instance.insecureSkipTLS;
  const hasOldVersion = Boolean(instance.serverVersion && versionBelow(instance.serverVersion, '7.1.0'));

  if (!hasConnectionError && !hasInsecureTLS && !hasOldVersion) return null;

  return (
    <div className='flex items-center gap-1.5'>
      {hasConnectionError && (
        <Tooltip>
          <TooltipTrigger asChild>
            <div
              data-testid='cpa-alert-connection-error'
              className='flex cursor-pointer items-center gap-1.5 rounded-full border border-destructive/30 bg-destructive/10 px-2.5 py-0.5 text-xs text-destructive transition-colors hover:bg-destructive/15'
            >
              <AlertTriangle className='h-3.5 w-3.5 shrink-0' />
              <span className='font-medium'>{t('cpa.warnings.connection.title')}</span>
            </div>
          </TooltipTrigger>
          <TooltipContent side='bottom' className='max-w-md break-all text-xs'>
            <div className='mb-0.5 font-semibold'>{t('cpa.warnings.connection.title')}</div>
            <div className='opacity-90'>{instance.lastError ?? t('cpa.warnings.connection.description')}</div>
          </TooltipContent>
        </Tooltip>
      )}
      {hasInsecureTLS && (
        <Tooltip>
          <TooltipTrigger asChild>
            <div
              data-testid='cpa-alert-insecure-tls'
              className='flex cursor-pointer items-center gap-1 rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-xs text-amber-600 transition-colors hover:bg-amber-500/15 dark:text-amber-400'
            >
              <AlertTriangle className='h-3.5 w-3.5 shrink-0' />
              <span className='font-medium'>{t('cpa.warnings.insecureTLS.title')}</span>
            </div>
          </TooltipTrigger>
          <TooltipContent side='bottom' className='max-w-xs text-xs'>
            <div>{t('cpa.warnings.insecureTLS.active')}</div>
          </TooltipContent>
        </Tooltip>
      )}
      {hasOldVersion && (
        <Tooltip>
          <TooltipTrigger asChild>
            <div
              data-testid='cpa-alert-old-version'
              className='flex cursor-pointer items-center gap-1 rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-xs text-amber-600 transition-colors hover:bg-amber-500/15 dark:text-amber-400'
            >
              <AlertTriangle className='h-3.5 w-3.5 shrink-0' />
              <span className='font-medium'>{t('cpa.warnings.oldVersion.title')}</span>
            </div>
          </TooltipTrigger>
          <TooltipContent side='bottom' className='max-w-xs text-xs'>
            <div>{t('cpa.warnings.oldVersion.description', { version: instance.serverVersion })}</div>
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  );
}
