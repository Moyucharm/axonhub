import type { UseFormReturn } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { CPAInstanceFormValues } from '../instance-form';
import type { CPAInstance } from '../types';

interface CPAInstanceConnectionFieldsProps {
  form: UseFormReturn<CPAInstanceFormValues>;
  instance?: CPAInstance;
  baseURLChanged: boolean;
  externalHttpURL: boolean;
}

export function CPAInstanceConnectionFields({ form, instance, baseURLChanged, externalHttpURL }: CPAInstanceConnectionFieldsProps) {
  const { t } = useTranslation();
  const values = form.watch();

  return (
    <>
      <div className='grid gap-2'>
        <Label htmlFor='cpa-name'>{t('cpa.instance.name')}</Label>
        <Input data-testid='cpa-instance-name' id='cpa-name' {...form.register('name')} required />
      </div>
      <div className='grid gap-2'>
        <Label htmlFor='cpa-url'>{t('cpa.instance.url')}</Label>
        <Input
          data-testid='cpa-instance-base-url'
          id='cpa-url'
          {...form.register('baseURL')}
          placeholder='http://127.0.0.1:8317'
          required
        />
        <p className='text-muted-foreground text-xs'>{t('cpa.instance.urlHint')}</p>
        {externalHttpURL && <p className='text-xs text-amber-600'>{t('cpa.warnings.externalHTTP')}</p>}
      </div>
      <div className='grid gap-2'>
        <Label htmlFor='cpa-secret'>{t('cpa.instance.secret')}</Label>
        <Input
          data-testid='cpa-instance-secret'
          id='cpa-secret'
          type='password'
          autoComplete='new-password'
          {...form.register('managementSecret')}
          required={!instance}
          placeholder={instance?.hasSecret ? t('cpa.instance.secretPreserve') : ''}
        />
        <p className='text-muted-foreground text-xs'>{t('cpa.instance.secretHint')}</p>
        {baseURLChanged && !values.managementSecret.trim() && (
          <p className='text-destructive text-xs'>{t('cpa.instance.secretRequiredForURLChange')}</p>
        )}
      </div>
    </>
  );
}
