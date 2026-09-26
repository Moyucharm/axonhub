import type { UseFormReturn } from 'react-hook-form';
import { AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import type { CPAInstanceFormValues } from '../instance-form';

interface CPAInstanceSettingsProps {
  form: UseFormReturn<CPAInstanceFormValues>;
  values: CPAInstanceFormValues;
  patrolIntervalsInvalid: boolean;
  confirmedInsecure: boolean;
  onInsecureConfirmationChange: (confirmed: boolean) => void;
}

export function CPAInstanceSettings({
  form,
  values,
  patrolIntervalsInvalid,
  confirmedInsecure,
  onInsecureConfirmationChange,
}: CPAInstanceSettingsProps) {
  const { t } = useTranslation();

  return (
    <>
      <div className='space-y-4 rounded-lg border p-4'>
        <div className='flex items-center justify-between gap-4'>
          <div>
            <Label htmlFor='cpa-enabled'>{t('cpa.instance.enabled')}</Label>
            <p className='text-muted-foreground text-xs'>{t('cpa.instance.enabledHint')}</p>
          </div>
          <Switch
            data-testid='cpa-instance-enabled'
            id='cpa-enabled'
            checked={values.enabled}
            onCheckedChange={(value) => form.setValue('enabled', value)}
          />
        </div>
        <div className='flex items-center justify-between gap-4'>
          <div>
            <Label htmlFor='cpa-auto-refresh'>{t('cpa.instance.autoRefresh')}</Label>
            <p className='text-muted-foreground text-xs'>{t('cpa.instance.autoRefreshHint')}</p>
          </div>
          <Switch
            id='cpa-auto-refresh'
            checked={values.autoRefreshEnabled}
            onCheckedChange={(value) => form.setValue('autoRefreshEnabled', value)}
          />
        </div>
        <div className='grid gap-2'>
          <Label htmlFor='cpa-refresh-interval'>{t('cpa.instance.refreshInterval')}</Label>
          <Input
            id='cpa-refresh-interval'
            type='number'
            min={5}
            max={1440}
            {...form.register('refreshIntervalMinutes', { valueAsNumber: true })}
            required
          />
          <p className='text-muted-foreground text-xs'>{t('cpa.instance.refreshIntervalHint')}</p>
        </div>
        <div className='flex items-center justify-between gap-4'>
          <div>
            <Label htmlFor='cpa-auto-manage'>{t('cpa.instance.autoManage')}</Label>
            <p className='text-muted-foreground text-xs'>{t('cpa.instance.autoManageHint')}</p>
          </div>
          <Switch
            data-testid='cpa-auto-manage'
            id='cpa-auto-manage'
            checked={values.autoManageEnabled}
            onCheckedChange={(value) => {
              form.setValue('autoManageEnabled', value);
              // Reset-card auto use depends on the patrol that refreshes quota.
              if (!value) form.setValue('autoResetEnabled', false);
            }}
          />
        </div>
        <div className='flex items-center justify-between gap-4'>
          <div>
            <Label htmlFor='cpa-auto-reset'>{t('cpa.instance.autoReset')}</Label>
            <p className='text-muted-foreground text-xs'>
              {values.autoManageEnabled ? t('cpa.instance.autoResetHint') : t('cpa.instance.autoResetDependency')}
            </p>
          </div>
          <Switch
            data-testid='cpa-auto-reset'
            id='cpa-auto-reset'
            checked={values.autoResetEnabled}
            disabled={!values.autoManageEnabled}
            onCheckedChange={(value) => form.setValue('autoResetEnabled', value)}
          />
        </div>
        <div className='flex items-center justify-between gap-4'>
          <div>
            <Label htmlFor='cpa-usage-stream'>{t('cpa.instance.usageStream')}</Label>
            <p className='text-muted-foreground text-xs'>{t('cpa.instance.usageStreamHint')}</p>
          </div>
          <Switch
            id='cpa-usage-stream'
            checked={values.usageStreamEnabled}
            onCheckedChange={(value) => form.setValue('usageStreamEnabled', value)}
          />
        </div>
        {values.autoManageEnabled && (
          <>
            <div className='grid gap-2'>
              <Label htmlFor='cpa-enabled-patrol-interval'>{t('cpa.instance.enabledPatrolInterval')}</Label>
              <Input
                id='cpa-enabled-patrol-interval'
                type='number'
                min={1}
                max={1440}
                {...form.register('enabledPatrolIntervalMinutes', { valueAsNumber: true })}
                required
                aria-invalid={
                  !Number.isInteger(values.enabledPatrolIntervalMinutes) ||
                  values.enabledPatrolIntervalMinutes < 1 ||
                  values.enabledPatrolIntervalMinutes > 1440
                }
                className={cn(
                  (values.enabledPatrolIntervalMinutes < 1 || values.enabledPatrolIntervalMinutes > 1440) && 'border-destructive'
                )}
              />
              <p className='text-muted-foreground text-xs'>{t('cpa.instance.enabledPatrolIntervalHint')}</p>
              {patrolIntervalsInvalid && <p className='text-destructive text-xs'>{t('cpa.instance.patrolIntervalInvalid')}</p>}
            </div>
            <div className='grid gap-2'>
              <Label htmlFor='cpa-disabled-patrol-interval'>{t('cpa.instance.disabledPatrolInterval')}</Label>
              <Input
                id='cpa-disabled-patrol-interval'
                type='number'
                min={60}
                max={10080}
                {...form.register('disabledPatrolIntervalMinutes', { valueAsNumber: true })}
                required
                aria-invalid={
                  !Number.isInteger(values.disabledPatrolIntervalMinutes) ||
                  values.disabledPatrolIntervalMinutes < 60 ||
                  values.disabledPatrolIntervalMinutes > 10080
                }
                className={cn(
                  (values.disabledPatrolIntervalMinutes < 60 || values.disabledPatrolIntervalMinutes > 10080) && 'border-destructive'
                )}
              />
              <p className='text-muted-foreground text-xs'>{t('cpa.instance.disabledPatrolIntervalHint')}</p>
              {patrolIntervalsInvalid && <p className='text-destructive text-xs'>{t('cpa.instance.patrolIntervalInvalid')}</p>}
            </div>
          </>
        )}
        <div className='flex items-center justify-between gap-4'>
          <div>
            <Label htmlFor='cpa-insecure-tls'>{t('cpa.instance.insecureTLS')}</Label>
            <p className='text-muted-foreground text-xs'>{t('cpa.instance.insecureTLSHint')}</p>
          </div>
          <Switch
            data-testid='cpa-insecure-tls'
            id='cpa-insecure-tls'
            checked={values.insecureSkipTLS}
            onCheckedChange={(value) => {
              form.setValue('insecureSkipTLS', value);
              onInsecureConfirmationChange(false);
            }}
          />
        </div>
      </div>

      {values.insecureSkipTLS && (
        <Alert variant='destructive'>
          <AlertTriangle className='h-4 w-4' />
          <AlertTitle>{t('cpa.warnings.insecureTLS.title')}</AlertTitle>
          <AlertDescription className='space-y-3'>
            <p>{t('cpa.warnings.insecureTLS.description')}</p>
            <label className='flex cursor-pointer items-start gap-2 text-sm'>
              <Checkbox
                data-testid='cpa-insecure-confirm'
                checked={confirmedInsecure}
                onCheckedChange={(value) => onInsecureConfirmationChange(value === true)}
              />
              <span>{t('cpa.warnings.insecureTLS.confirm')}</span>
            </label>
          </AlertDescription>
        </Alert>
      )}
    </>
  );
}
