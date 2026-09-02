import { Pencil, Server, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { formatTime } from '../quota-windows';
import type { CPAInstance, CPAStats } from '../types';

interface CPAInstanceToolbarProps {
  instances: CPAInstance[];
  selectedInstanceID?: number;
  selectedInstance?: CPAInstance;
  stats?: CPAStats;
  canWrite: boolean;
  onSelect: (value: string) => void;
  onEdit: (instance: CPAInstance) => void;
  onDelete: (instance: CPAInstance) => void;
}

export function CPAInstanceToolbar({
  instances,
  selectedInstanceID,
  selectedInstance,
  stats,
  canWrite,
  onSelect,
  onEdit,
  onDelete,
}: CPAInstanceToolbarProps) {
  const { t } = useTranslation();

  return (
    <div className='flex flex-wrap items-center gap-2'>
      <Select value={selectedInstanceID?.toString()} onValueChange={onSelect}>
        <SelectTrigger data-testid='cpa-instance-select' className='w-[260px]'>
          <Server className='mr-2 h-4 w-4' />
          <SelectValue placeholder={t('cpa.instance.select')} />
        </SelectTrigger>
        <SelectContent>
          {instances.map((instance) => (
            <SelectItem key={instance.id} value={instance.id.toString()}>
              {instance.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selectedInstance && canWrite && (
        <>
          <Button
            data-testid='cpa-edit-instance'
            variant='outline'
            size='icon'
            title={t('common.buttons.edit')}
            onClick={() => onEdit(selectedInstance)}
          >
            <Pencil className='h-4 w-4' />
          </Button>
          <Button
            data-testid='cpa-delete-instance'
            variant='outline'
            size='icon'
            title={t('common.buttons.delete')}
            onClick={() => onDelete(selectedInstance)}
          >
            <Trash2 className='text-destructive h-4 w-4' />
          </Button>
        </>
      )}
      {selectedInstance && (
        <Badge variant={selectedInstance.enabled ? 'default' : 'secondary'}>
          {selectedInstance.enabled ? t('cpa.status.enabled') : t('cpa.status.disabled')}
        </Badge>
      )}
      {selectedInstance?.serverVersion && <Badge variant='outline'>CPA {selectedInstance.serverVersion}</Badge>}
      {selectedInstance && (
        <div className='text-muted-foreground ml-auto flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-sm'>
          <span className='shrink-0'>
            {t('cpa.stats.available')}{' '}
            <span className='text-foreground font-medium tabular-nums'>
              {stats?.available ?? '—'} / {stats?.total ?? '—'}
            </span>
          </span>
          <span className='shrink-0'>
            {t('cpa.stats.abnormal')}{' '}
            <span className={`font-medium tabular-nums ${(stats?.abnormal ?? 0) > 0 ? 'text-destructive' : 'text-foreground'}`}>
              {stats?.abnormal ?? '—'}
            </span>
          </span>
          <span className='min-w-0 truncate' title={formatTime(selectedInstance.lastSyncSuccessAt)}>
            {t('cpa.stats.lastSync')} <span className='text-foreground font-medium'>{formatTime(selectedInstance.lastSyncSuccessAt)}</span>
          </span>
        </div>
      )}
    </div>
  );
}
