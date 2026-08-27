import { FormEvent, useEffect, useState } from 'react';
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
import { CPAInstance, useCreateCPAInstance, useUpdateCPAInstance } from '../data';

interface CPAInstanceDialogProps {
  open: boolean;
  instance?: CPAInstance;
  onOpenChange: (open: boolean) => void;
  onSaved: (instance: CPAInstance) => void;
}

export function CPAInstanceDialog({ open, instance, onOpenChange, onSaved }: CPAInstanceDialogProps) {
  const { t } = useTranslation();
  const createMutation = useCreateCPAInstance();
  const updateMutation = useUpdateCPAInstance();
  const [name, setName] = useState('');
  const [baseURL, setBaseURL] = useState('');
  const [initialBaseURL, setInitialBaseURL] = useState('');
  const [managementSecret, setManagementSecret] = useState('');
  const [enabled, setEnabled] = useState(true);
  const [insecureSkipTLS, setInsecureSkipTLS] = useState(false);
  const [autoRefreshEnabled, setAutoRefreshEnabled] = useState(true);
  const [refreshIntervalMinutes, setRefreshIntervalMinutes] = useState(5);
  const [autoManageEnabled, setAutoManageEnabled] = useState(false);
  const [usageStreamEnabled, setUsageStreamEnabled] = useState(false);
  const [enabledPatrolIntervalMinutes, setEnabledPatrolIntervalMinutes] = useState(5);
  const [disabledPatrolIntervalMinutes, setDisabledPatrolIntervalMinutes] = useState(480);
  const [confirmedInsecure, setConfirmedInsecure] = useState(false);

  useEffect(() => {
    if (!open) return;
    setName(instance?.name ?? '');
    setBaseURL(instance?.baseURL ?? '');
    setInitialBaseURL(instance?.baseURL ?? '');
    setManagementSecret('');
    setEnabled(instance?.enabled ?? true);
    setInsecureSkipTLS(instance?.insecureSkipTLS ?? false);
    setAutoRefreshEnabled(instance?.autoRefreshEnabled ?? true);
    setRefreshIntervalMinutes(instance?.refreshIntervalMinutes ?? 5);
    setAutoManageEnabled(instance?.autoManageEnabled ?? false);
    setUsageStreamEnabled(instance?.usageStreamEnabled ?? false);
    setEnabledPatrolIntervalMinutes(instance?.enabledPatrolIntervalMinutes ?? 5);
    setDisabledPatrolIntervalMinutes(instance?.disabledPatrolIntervalMinutes ?? 480);
    setConfirmedInsecure(false);
  }, [instance, open]);

  const pending = createMutation.isPending || updateMutation.isPending;
  const baseURLChanged = Boolean(instance) && baseURL.trim() !== initialBaseURL.trim();
  const externalHttpURL = isExternalHttpURL(baseURL);

  // Whole-minute bounds mirror the backend normalization, so an out-of-range
  // or cleared input (Number('') === 0) is caught before submit instead of by
  // a server-side validation error.
  const patrolIntervalsInvalid =
    !Number.isInteger(enabledPatrolIntervalMinutes) ||
    enabledPatrolIntervalMinutes < 1 ||
    enabledPatrolIntervalMinutes > 1440 ||
    !Number.isInteger(disabledPatrolIntervalMinutes) ||
    disabledPatrolIntervalMinutes < 60 ||
    disabledPatrolIntervalMinutes > 10080;

  const onSubmit = async (event: FormEvent) => {
    event.preventDefault();
    if (patrolIntervalsInvalid) return;
    const input = {
      name: name.trim(),
      baseURL: baseURL.trim(),
      managementSecret: managementSecret.trim() || undefined,
      enabled,
      insecureSkipTLS,
      autoRefreshEnabled,
      refreshIntervalMinutes,
      autoManageEnabled,
      usageStreamEnabled,
      enabledPatrolIntervalMinutes,
      disabledPatrolIntervalMinutes,
    };
    try {
      const saved = instance
        ? await updateMutation.mutateAsync({ id: instance.id, input })
        : await createMutation.mutateAsync({ ...input, managementSecret: managementSecret.trim() });
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
        <form className='flex min-h-0 flex-1 flex-col overflow-hidden' onSubmit={onSubmit}>
          <div className='min-h-0 flex-1 space-y-5 overflow-y-auto px-1 py-1'>
          <div className='grid gap-2'>
            <Label htmlFor='cpa-name'>{t('cpa.instance.name')}</Label>
            <Input id='cpa-name' value={name} onChange={(event) => setName(event.target.value)} required />
          </div>
          <div className='grid gap-2'>
            <Label htmlFor='cpa-url'>{t('cpa.instance.url')}</Label>
            <Input
              id='cpa-url'
              value={baseURL}
              onChange={(event) => setBaseURL(event.target.value)}
              placeholder='http://127.0.0.1:8317'
              required
            />
            <p className='text-muted-foreground text-xs'>{t('cpa.instance.urlHint')}</p>
            {externalHttpURL && <p className='text-amber-600 text-xs'>{t('cpa.warnings.externalHTTP')}</p>}
          </div>
          <div className='grid gap-2'>
            <Label htmlFor='cpa-secret'>{t('cpa.instance.secret')}</Label>
            <Input
              id='cpa-secret'
              type='password'
              autoComplete='new-password'
              value={managementSecret}
              onChange={(event) => setManagementSecret(event.target.value)}
              required={!instance}
              placeholder={instance?.hasSecret ? t('cpa.instance.secretPreserve') : ''}
            />
            <p className='text-muted-foreground text-xs'>{t('cpa.instance.secretHint')}</p>
            {baseURLChanged && !managementSecret.trim() && (
              <p className='text-destructive text-xs'>{t('cpa.instance.secretRequiredForURLChange')}</p>
            )}
          </div>

          <div className='space-y-4 rounded-lg border p-4'>
            <div className='flex items-center justify-between gap-4'>
              <div>
                <Label htmlFor='cpa-enabled'>{t('cpa.instance.enabled')}</Label>
                <p className='text-muted-foreground text-xs'>{t('cpa.instance.enabledHint')}</p>
              </div>
              <Switch id='cpa-enabled' checked={enabled} onCheckedChange={setEnabled} />
            </div>
            <div className='flex items-center justify-between gap-4'>
              <div>
                <Label htmlFor='cpa-auto-refresh'>{t('cpa.instance.autoRefresh')}</Label>
                <p className='text-muted-foreground text-xs'>{t('cpa.instance.autoRefreshHint')}</p>
              </div>
              <Switch id='cpa-auto-refresh' checked={autoRefreshEnabled} onCheckedChange={setAutoRefreshEnabled} />
            </div>
            <div className='grid gap-2'>
              <Label htmlFor='cpa-refresh-interval'>{t('cpa.instance.refreshInterval')}</Label>
              <Input
                id='cpa-refresh-interval'
                type='number'
                min={5}
                max={1440}
                value={refreshIntervalMinutes}
                onChange={(event) => setRefreshIntervalMinutes(Number(event.target.value))}
                required
              />
              <p className='text-muted-foreground text-xs'>{t('cpa.instance.refreshIntervalHint')}</p>
            </div>
            <div className='flex items-center justify-between gap-4'>
              <div>
                <Label htmlFor='cpa-auto-manage'>{t('cpa.instance.autoManage')}</Label>
                <p className='text-muted-foreground text-xs'>{t('cpa.instance.autoManageHint')}</p>
              </div>
              <Switch id='cpa-auto-manage' checked={autoManageEnabled} onCheckedChange={setAutoManageEnabled} />
            </div>
            <div className='flex items-center justify-between gap-4'>
              <div>
                <Label htmlFor='cpa-usage-stream'>{t('cpa.instance.usageStream')}</Label>
                <p className='text-muted-foreground text-xs'>{t('cpa.instance.usageStreamHint')}</p>
              </div>
              <Switch id='cpa-usage-stream' checked={usageStreamEnabled} onCheckedChange={setUsageStreamEnabled} />
            </div>
            {autoManageEnabled && (
              <>
                <div className='grid gap-2'>
                  <Label htmlFor='cpa-enabled-patrol-interval'>{t('cpa.instance.enabledPatrolInterval')}</Label>
                  <Input
                    id='cpa-enabled-patrol-interval'
                    type='number'
                    min={1}
                    max={1440}
                    value={enabledPatrolIntervalMinutes}
                    onChange={(event) => setEnabledPatrolIntervalMinutes(Math.trunc(Number(event.target.value)))}
                    required
                    aria-invalid={
                      !Number.isInteger(enabledPatrolIntervalMinutes) ||
                      enabledPatrolIntervalMinutes < 1 ||
                      enabledPatrolIntervalMinutes > 1440
                    }
                    className={cn(
                      (enabledPatrolIntervalMinutes < 1 || enabledPatrolIntervalMinutes > 1440) && 'border-destructive'
                    )}
                  />
                  <p className='text-muted-foreground text-xs'>{t('cpa.instance.enabledPatrolIntervalHint')}</p>
                  {autoManageEnabled && (enabledPatrolIntervalMinutes < 1 || enabledPatrolIntervalMinutes > 1440) && (
                    <p className='text-destructive text-xs'>{t('cpa.instance.patrolIntervalInvalid')}</p>
                  )}
                </div>
                <div className='grid gap-2'>
                  <Label htmlFor='cpa-disabled-patrol-interval'>{t('cpa.instance.disabledPatrolInterval')}</Label>
                  <Input
                    id='cpa-disabled-patrol-interval'
                    type='number'
                    min={60}
                    max={10080}
                    value={disabledPatrolIntervalMinutes}
                    onChange={(event) => setDisabledPatrolIntervalMinutes(Math.trunc(Number(event.target.value)))}
                    required
                    aria-invalid={
                      !Number.isInteger(disabledPatrolIntervalMinutes) ||
                      disabledPatrolIntervalMinutes < 60 ||
                      disabledPatrolIntervalMinutes > 10080
                    }
                    className={cn(
                      (disabledPatrolIntervalMinutes < 60 || disabledPatrolIntervalMinutes > 10080) && 'border-destructive'
                    )}
                  />
                  <p className='text-muted-foreground text-xs'>{t('cpa.instance.disabledPatrolIntervalHint')}</p>
                  {autoManageEnabled && (disabledPatrolIntervalMinutes < 60 || disabledPatrolIntervalMinutes > 10080) && (
                    <p className='text-destructive text-xs'>{t('cpa.instance.patrolIntervalInvalid')}</p>
                  )}
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
                checked={insecureSkipTLS}
                onCheckedChange={(value) => {
                  setInsecureSkipTLS(value);
                  setConfirmedInsecure(false);
                }}
              />
            </div>
          </div>

          {insecureSkipTLS && (
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
                !name.trim() ||
                !baseURL.trim() ||
                (!instance && !managementSecret.trim()) ||
                (baseURLChanged && !managementSecret.trim()) ||
                (insecureSkipTLS && !confirmedInsecure) ||
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
