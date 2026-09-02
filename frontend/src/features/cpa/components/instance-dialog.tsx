import { useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { useCreateCPAInstance, useUpdateCPAInstance } from '../data';
import {
  cpaInstanceFormSchema,
  cpaInstanceFormValues,
  cpaInstanceInput,
  DEFAULT_CPA_INSTANCE_FORM_VALUES,
  type CPAInstanceFormValues,
} from '../instance-form';
import type { CPAInstance } from '../types';
import { isExternalHttpURL } from '../url';
import { CPAInstanceConnectionFields } from './instance-connection-fields';
import { CPAInstanceSettings } from './instance-settings';

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
  const [initialBaseURL, setInitialBaseURL] = useState('');
  const [confirmedInsecure, setConfirmedInsecure] = useState(false);
  const form = useForm<CPAInstanceFormValues>({
    resolver: zodResolver(cpaInstanceFormSchema),
    defaultValues: DEFAULT_CPA_INSTANCE_FORM_VALUES,
  });
  const values = form.watch();

  useEffect(() => {
    if (!open) return;
    const baseURL = instance?.baseURL ?? '';
    setInitialBaseURL(baseURL);
    form.reset(cpaInstanceFormValues(instance));
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
    if (
      patrolIntervalsInvalid ||
      (!instance && !submitted.managementSecret.trim()) ||
      (baseURLChanged && !submitted.managementSecret.trim())
    ) {
      return;
    }
    const input = cpaInstanceInput(submitted);
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
      <DialogContent data-testid='cpa-instance-dialog' className='flex max-h-[85vh] flex-col overflow-hidden sm:max-w-xl'>
        <DialogHeader className='shrink-0 text-left'>
          <DialogTitle>{instance ? t('cpa.instance.editTitle') : t('cpa.instance.createTitle')}</DialogTitle>
          <DialogDescription>{t('cpa.instance.description')}</DialogDescription>
        </DialogHeader>
        <form className='flex min-h-0 flex-1 flex-col overflow-hidden' onSubmit={form.handleSubmit(onSubmit)}>
          <div className='min-h-0 flex-1 space-y-5 overflow-y-auto px-1 py-1'>
            <CPAInstanceConnectionFields
              form={form}
              instance={instance}
              baseURLChanged={baseURLChanged}
              externalHttpURL={externalHttpURL}
            />
            <CPAInstanceSettings
              form={form}
              values={values}
              patrolIntervalsInvalid={patrolIntervalsInvalid}
              confirmedInsecure={confirmedInsecure}
              onInsecureConfirmationChange={setConfirmedInsecure}
            />
          </div>

          <DialogFooter className='mt-4 shrink-0'>
            <Button type='button' variant='outline' onClick={() => onOpenChange(false)}>
              {t('common.buttons.cancel')}
            </Button>
            <Button
              data-testid='cpa-instance-submit'
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
