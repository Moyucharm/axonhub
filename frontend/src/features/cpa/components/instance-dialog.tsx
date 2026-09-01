import { useEffect, useState } from 'react';
import { z } from 'zod';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import { isExternalHttpURL } from '../url';
import { useCreateCPAInstance, useUpdateCPAInstance } from '../data';
import type { CPAInstance } from '../types';

interface CPAInstanceDialogProps {
  open: boolean;
  instance?: CPAInstance;
  onOpenChange: (open: boolean) => void;
  onSaved: (instance: CPAInstance) => void;
}

const cpaInstanceFormSchema = z.object({
  name: z.string().trim().min(1),
  baseURL: z.string().trim().min(1),
  managementSecret: z.string(),
  enabled: z.boolean(),
  insecureSkipTLS: z.boolean(),
  autoRefreshEnabled: z.boolean(),
  refreshIntervalMinutes: z.number().int().min(5).max(1440),
  autoManageEnabled: z.boolean(),
  usageStreamEnabled: z.boolean(),
  enabledPatrolIntervalMinutes: z.number().int().min(1).max(1440),
  disabledPatrolIntervalMinutes: z.number().int().min(60).max(10080),
});

type CPAInstanceFormValues = z.infer<typeof cpaInstanceFormSchema>;

const DEFAULT_FORM_VALUES: CPAInstanceFormValues = {
  name: '',
  baseURL: '',
  managementSecret: '',
  enabled: true,
  insecureSkipTLS: false,
  autoRefreshEnabled: true,
  refreshIntervalMinutes: 5,
  autoManageEnabled: false,
  usageStreamEnabled: false,
  enabledPatrolIntervalMinutes: 5,
  disabledPatrolIntervalMinutes: 480,
};

export function CPAInstanceDialog({ open, instance, onOpenChange, onSaved }: CPAInstanceDialogProps) {
  const { t } = useTranslation();
  const createMutation = useCreateCPAInstance();
  const updateMutation = useUpdateCPAInstance();
  const [initialBaseURL, setInitialBaseURL] = useState('');
  const [confirmedInsecure, setConfirmedInsecure] = useState(false);
  const form = useForm<CPAInstanceFormValues>({
    resolver: zodResolver(cpaInstanceFormSchema),
    defaultValues: DEFAULT_FORM_VALUES,
  });
  const values = form.watch();

  useEffect(() => {
    if (!open) return;
    const baseURL = instance?.baseURL ?? '';
    setInitialBaseURL(baseURL);
    form.reset({
      name: instance?.name ?? '',
      baseURL,
      managementSecret: '',
      enabled: instance?.enabled ?? true,
      insecureSkipTLS: instance?.insecureSkipTLS ?? false,
      autoRefreshEnabled: instance?.autoRefreshEnabled ?? true,
      refreshIntervalMinutes: instance?.refreshIntervalMinutes ?? 5,
      autoManageEnabled: instance?.autoManageEnabled ?? false,
      usageStreamEnabled: instance?.usageStreamEnabled ?? false,
      enabledPatrolIntervalMinutes: instance?.enabledPatrolIntervalMinutes ?? 5,
      disabledPatrolIntervalMinutes: instance?.disabledPatrolIntervalMinutes ?? 480,
    });
    setConfirmedInsecure(false);
  }, [form, instance, open]);

  const pending = createMutation.isPending || updateMutation.isPending;
  const baseURLChanged = Boolean(instance) && values.baseURL.trim() !== initialBaseURL.trim();
  const externalHttpURL = isExternalHttpURL(values.baseURL);
  const patrolIntervalsInvalid =
    !Number.isInteger(values.enabledPatrolIntervalMinutes) ||
    values.enabledPatrolIntervalMinutes < 1 ||
    values.enabledPatrolIntervalMinutes > 1440 ||
    !Number.isInteger(values.disabledPatrolIntervalMinutes) ||
    values.disabledPatrolIntervalMinutes < 60 ||
    values.disabledPatrolIntervalMinutes > 10080;

  const onSubmit = async (submitted: CPAInstanceFormValues) => {
    if (patrolIntervalsInvalid || (!instance && !submitted.managementSecret.trim()) || (baseURLChanged && !submitted.managementSecret.trim())) {
      return;
    }
    const input = {
      ...submitted,
      name: submitted.name.trim(),
      baseURL: submitted.baseURL.trim(),
      managementSecret: submitted.managementSecret.trim() || undefined,
    };
    try {
      const saved = instance
        ? await updateMutation.mutateAsync({ id: instance.id, input })
        : await createMutation.mutateAsync({ ...input, managementSecret: submitted.managementSecret.trim() });
      onSaved(saved);
      onOpenChange(false);
    } catch {
      // The mutation hook displays the error and the dialog remains open for correction.
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[85vh] flex-col overflow-hidden sm:max-w-xl'>
        <DialogHeader className='shrink-0 text-left'>
          <DialogTitle>{instance ? t('cpa.instance.editTitle') : t('cpa.instance.createTitle')}</DialogTitle>
          <DialogDescription>{t('cpa.instance.description')}</DialogDescription>
        </DialogHeader>
        <form className='flex min-h-0 flex-1 flex-col overflow-hidden' onSubmit={form.handleSubmit(onSubmit)}>
          <div className='min-h-0 flex-1 space-y-5 overflow-y-auto px-1 py-1'>
            <div className='grid gap-2'>
              <Label htmlFor='cpa-name'>{t('cpa.instance.name')}</Label>
              <Input id='cpa-name' {...form.register('name')} required />
            </div>
            <div className='grid gap-2'>
              <Label htmlFor='cpa-url'>{t('cpa.instance.url')}</Label>
              <Input id='cpa-url' {...form.register('baseURL')} placeholder='http://127.0.0.1:8317' required />
              <p className='text-muted-foreground text-xs'>{t('cpa.instance.urlHint')}</p>
              {externalHttpURL && <p className='text-amber-600 text-xs'>{t('cpa.warnings.externalHTTP')}</p>}
            </div>
            <div className='grid gap-2'>
              <Label htmlFor='cpa-secret'>{t('cpa.instance.secret')}</Label>
              <Input
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

            <div className='space-y-4 rounded-lg border p-4'>
              <div className='flex items-center justify-between gap-4'>
                <div>
                  <Label htmlFor='cpa-enabled'>{t('cpa.instance.enabled')}</Label>
                  <p className='text-muted-foreground text-xs'>{t('cpa.instance.enabledHint')}</p>
                </div>
                <Switch id='cpa-enabled' checked={values.enabled} onCheckedChange={(value) => form.setValue('enabled', value)} />
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
                <Input id='cpa-refresh-interval' type='number' min={5} max={1440} {...form.register('refreshIntervalMinutes', { valueAsNumber: true })} required />
                <p className='text-muted-foreground text-xs'>{t('cpa.instance.refreshIntervalHint')}</p>
              </div>
              <div className='flex items-center justify-between gap-4'>
                <div>
                  <Label htmlFor='cpa-auto-manage'>{t('cpa.instance.autoManage')}</Label>
                  <p className='text-muted-foreground text-xs'>{t('cpa.instance.autoManageHint')}</p>
                </div>
                <Switch
                  id='cpa-auto-manage'
                  checked={values.autoManageEnabled}
                  onCheckedChange={(value) => form.setValue('autoManageEnabled', value)}
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
                  id='cpa-insecure-tls'
                  checked={values.insecureSkipTLS}
                  onCheckedChange={(value) => {
                    form.setValue('insecureSkipTLS', value);
                    setConfirmedInsecure(false);
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
                    <Checkbox checked={confirmedInsecure} onCheckedChange={(value) => setConfirmedInsecure(value === true)} />
                    <span>{t('cpa.warnings.insecureTLS.confirm')}</span>
                  </label>
                </AlertDescription>
              </Alert>
            )}
          </div>

          <DialogFooter className='mt-4 shrink-0'>
            <Button type='button' variant='outline' onClick={() => onOpenChange(false)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button
              type='submit'
              disabled={
                pending ||
                !values.name.trim() ||
                !values.baseURL.trim() ||
                (!instance && !values.managementSecret.trim()) ||
                (baseURLChanged && !values.managementSecret.trim()) ||
                (values.insecureSkipTLS && !confirmedInsecure) ||
                patrolIntervalsInvalid
              }
            >
              {pending ? t('common.buttons.saving') : t('common.buttons.save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
