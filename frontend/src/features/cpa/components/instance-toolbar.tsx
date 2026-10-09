import { AlertTriangle, Pencil } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { formatTime } from '../quota-windows';
import type { CPAInstance, CPAStats } from '../types';
import { versionBelow } from '../version';

interface CPAInstanceToolbarProps {
  instances: CPAInstance[];
  selectedInstanceID?: number;
  selectedInstance?: CPAInstance;
  stats?: CPAStats;
  canWrite: boolean;
  onSelect: (value: string) => void;
  onEdit: (instance: CPAInstance) => void;
}

const pillClass = 'bg-card text-muted-foreground inline-flex h-7 items-center gap-1 rounded-full border px-2.5 text-xs whitespace-nowrap';

export function CPAInstanceToolbar({ instances, selectedInstanceID, selectedInstance, stats, canWrite, onSelect, onEdit }: CPAInstanceToolbarProps) {
  const { t } = useTranslation();
  if (instances.length === 0) return null;

  const abnormal = stats?.abnormal ?? 0;

  return (
    <div className='flex flex-wrap items-center gap-2'>
      {selectedInstance && (
        <>
          <Tooltip>
            <TooltipTrigger asChild>
              <span data-testid='cpa-stats-available' className={cn(pillClass, 'cursor-default')}>
                {t('cpa.stats.available')}
                <span className='text-foreground font-medium tabular-nums'>
                  {stats?.available ?? '—'}/{stats?.total ?? '—'}
                </span>
              </span>
            </TooltipTrigger>
            <TooltipContent side='bottom' className='max-w-sm text-xs'>
              <InstanceDetails instance={selectedInstance} />
            </TooltipContent>
          </Tooltip>
          <span
            data-testid='cpa-stats-abnormal'
            className={cn(pillClass, abnormal > 0 && 'border-destructive/30 bg-destructive/10 text-destructive')}
          >
            {t('cpa.stats.abnormal')}
            <span className={cn('font-medium tabular-nums', abnormal === 0 && 'text-foreground')}>{stats?.abnormal ?? '—'}</span>
          </span>
        </>
      )}
      <Select value={selectedInstanceID?.toString()} onValueChange={onSelect}>
        <SelectTrigger data-testid='cpa-instance-select' className='bg-card h-9 max-w-[240px] min-w-[140px]'>
          <SelectValue placeholder={t('cpa.instance.select')} />
        </SelectTrigger>
        <SelectContent>
          {instances.map((instance) => (
            <SelectItem key={instance.id} value={instance.id.toString()}>
              <InstanceStatusDot instance={instance} />
              {instance.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selectedInstance && canWrite && (
        <Button
          data-testid='cpa-edit-instance'
          variant='outline'
          size='icon'
          className='bg-card'
          title={t('common.buttons.edit')}
          onClick={() => onEdit(selectedInstance)}
        >
          <Pencil className='h-4 w-4' />
        </Button>
      )}
    </div>
  );
}

type InstanceHealth = 'disabled' | 'error' | 'warning' | 'ok';

function instanceWarnings(instance: CPAInstance) {
  return {
    connectionError: instance.connectionStatus === 'error',
    insecureTLS: instance.insecureSkipTLS,
    oldVersion: Boolean(instance.serverVersion && versionBelow(instance.serverVersion, '7.1.0')),
  };
}

function instanceHealth(instance: CPAInstance): InstanceHealth {
  if (!instance.enabled) return 'disabled';
  const warnings = instanceWarnings(instance);
  if (warnings.connectionError) return 'error';
  if (warnings.insecureTLS || warnings.oldVersion) return 'warning';
  return 'ok';
}

const DOT_CLASS: Record<InstanceHealth, string> = {
  ok: 'bg-emerald-500',
  warning: 'bg-amber-500',
  error: 'bg-destructive',
  disabled: 'bg-muted-foreground/50',
};

function InstanceStatusDot({ instance }: { instance: CPAInstance }) {
  const health = instanceHealth(instance);
  return <span data-testid='cpa-instance-health' data-health={health} className={cn('size-2 shrink-0 rounded-full', DOT_CLASS[health])} />;
}

function InstanceDetails({ instance }: { instance: CPAInstance }) {
  const { t } = useTranslation();
  const warnings = instanceWarnings(instance);

  return (
    <div className='space-y-1.5'>
      <div className='flex items-center gap-2 font-semibold'>
        <span className='break-all'>{instance.name}</span>
        <span className='opacity-80'>{instance.enabled ? t('cpa.status.enabled') : t('cpa.status.disabled')}</span>
      </div>
      {instance.serverVersion && <div className='opacity-90'>CPA {instance.serverVersion}</div>}
      <div className='opacity-90'>
        {t('cpa.stats.lastSync')}: {formatTime(instance.lastSyncSuccessAt)}
      </div>
      {warnings.connectionError && (
        <Warning title={t('cpa.warnings.connection.title')} detail={instance.lastError ?? t('cpa.warnings.connection.description')} />
      )}
      {warnings.insecureTLS && <Warning title={t('cpa.warnings.insecureTLS.title')} detail={t('cpa.warnings.insecureTLS.active')} />}
      {warnings.oldVersion && (
        <Warning
          title={t('cpa.warnings.oldVersion.title')}
          detail={t('cpa.warnings.oldVersion.description', { version: instance.serverVersion })}
        />
      )}
    </div>
  );
}

function Warning({ title, detail }: { title: string; detail: string }) {
  return (
    <div className='border-background/20 border-t pt-1.5'>
      <div className='flex items-center gap-1 font-semibold'>
        <AlertTriangle className='h-3.5 w-3.5 shrink-0' />
        {title}
      </div>
      <div className='break-all opacity-90'>{detail}</div>
    </div>
  );
}
