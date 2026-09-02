import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Progress } from '@/components/ui/progress';
import type { CPARefreshProgress as CPARefreshProgressData } from '../types';

export function CPARefreshProgress({ progress }: { progress: CPARefreshProgressData }) {
  const { t } = useTranslation();
  return (
    <div className='flex items-center gap-3 pb-2'>
      <Progress value={progress.requested > 0 ? (progress.completed / progress.requested) * 100 : 0} className='h-2 flex-1' />
      <span className='text-muted-foreground shrink-0 text-xs'>
        {t('cpa.messages.refreshProgress', {
          completed: progress.completed,
          requested: progress.requested,
        })}
      </span>
      {progress.failed > 0 && (
        <Badge variant='destructive' className='shrink-0'>
          {t('cpa.messages.refreshFailedCount', { count: progress.failed })}
        </Badge>
      )}
    </div>
  );
}
