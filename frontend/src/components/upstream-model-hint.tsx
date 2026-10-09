import { useTranslation } from 'react-i18next';
import { isModelMismatch } from '@/lib/model-mismatch';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

export function UpstreamModelHint({ routedModel, responseModel }: { routedModel?: string | null; responseModel?: string | null }) {
  const { t } = useTranslation();
  if (!isModelMismatch(routedModel, responseModel)) return null;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge className='cursor-help border-orange-200 bg-orange-100 text-orange-800 dark:border-orange-800 dark:bg-orange-900/20 dark:text-orange-300'>
          {t('modelMismatch.badge')}
        </Badge>
      </TooltipTrigger>
      <TooltipContent side='right'>
        <span className='text-xs whitespace-nowrap'>
          {t('modelMismatch.upstreamResponse')}: <span className='font-mono'>{responseModel}</span>
        </span>
      </TooltipContent>
    </Tooltip>
  );
}
