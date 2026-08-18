import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { IconPlus, IconTrash } from '@tabler/icons-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { useUpdateChannel } from '../data/channels';
import type { Channel, ChannelAutoDisablePolicy } from '../data/schema';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentRow: Channel | null;
}

const MAX_COOLDOWN_DURATION_MINUTES = 10080;

const DEFAULT_POLICY: ChannelAutoDisablePolicy = {
  mode: 'codes',
  times: 3,
  statuses: [{ status: 500, times: 3 }],
  action: 'disable',
  cooldownDurationMinutes: 30,
};

function clonePolicy(policy: ChannelAutoDisablePolicy | null | undefined): ChannelAutoDisablePolicy {
  return {
    mode: policy?.mode ?? DEFAULT_POLICY.mode,
    times: policy?.times ?? DEFAULT_POLICY.times,
    statuses: (policy?.statuses ?? DEFAULT_POLICY.statuses ?? []).map((status) => ({ ...status })),
    action: policy?.action ?? DEFAULT_POLICY.action,
    cooldownDurationMinutes: policy?.cooldownDurationMinutes ?? DEFAULT_POLICY.cooldownDurationMinutes,
  };
}

export function ChannelAutoDisableRulesDialog({ open, onOpenChange, currentRow }: Props) {
  const { t } = useTranslation();
  const updateChannel = useUpdateChannel();
  const [enabled, setEnabled] = useState(false);
  const [policy, setPolicy] = useState<ChannelAutoDisablePolicy>(DEFAULT_POLICY);

  useEffect(() => {
    if (!open) return;
    const currentPolicy = currentRow?.policies?.channelAutoDisable;
    setEnabled(!!currentPolicy);
    setPolicy(clonePolicy(currentPolicy));
  }, [currentRow, open]);

  const updatePolicy = useCallback(<K extends keyof ChannelAutoDisablePolicy>(field: K, value: ChannelAutoDisablePolicy[K]) => {
    setPolicy((previous) => ({ ...previous, [field]: value }));
  }, []);

  const addStatus = useCallback(() => {
    setPolicy((previous) => ({
      ...previous,
      statuses: [...(previous.statuses ?? []), { status: 500, times: 3 }],
    }));
  }, []);

  const removeStatus = useCallback((index: number) => {
    setPolicy((previous) => ({
      ...previous,
      statuses: (previous.statuses ?? []).filter((_, statusIndex) => statusIndex !== index),
    }));
  }, []);

  const updateStatus = useCallback((index: number, field: 'status' | 'times', value: number) => {
    setPolicy((previous) => ({
      ...previous,
      statuses: (previous.statuses ?? []).map((status, statusIndex) =>
        statusIndex === index ? { ...status, [field]: value } : status
      ),
    }));
  }, []);

  const handleSubmit = useCallback(async () => {
    if (!currentRow) return;

    const statuses = policy.statuses ?? [];
    if (enabled && policy.times < 1) {
      toast.error(t('channels.dialogs.channelAutoDisableRules.times'));
      return;
    }
    if (enabled && policy.mode === 'codes' && statuses.length === 0) {
      toast.error(t('channels.dialogs.channelAutoDisableRules.emptyStatuses'));
      return;
    }
    if (enabled && statuses.some((status) => status.status < 100 || status.status > 599 || status.times < 1)) {
      toast.error(t('channels.dialogs.channelAutoDisableRules.statuses'));
      return;
    }
    if (
      enabled &&
      policy.action === 'cooldown' &&
      (policy.cooldownDurationMinutes < 1 || policy.cooldownDurationMinutes > MAX_COOLDOWN_DURATION_MINUTES)
    ) {
      toast.error(t('channels.dialogs.channelAutoDisableRules.cooldownMinutes'));
      return;
    }

    try {
      await updateChannel.mutateAsync({
        id: currentRow.id,
        input: {
          policies: {
            stream: currentRow.policies?.stream,
            channelAutoDisable: enabled
              ? {
                  ...policy,
                  statuses: policy.mode === 'codes' ? statuses : [],
                }
              : null,
            apiKeyAutoDisableRules: currentRow.policies?.apiKeyAutoDisableRules ?? null,
          },
        },
      });
      onOpenChange(false);
    } catch {
      // useUpdateChannel reports the request error.
    }
  }, [currentRow, enabled, onOpenChange, policy, t, updateChannel]);

  const statuses = policy.statuses ?? [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader className='text-left'>
          <DialogTitle>{t('channels.dialogs.channelAutoDisableRules.title')}</DialogTitle>
          <DialogDescription>
            {t('channels.dialogs.channelAutoDisableRules.description', { name: currentRow?.name ?? '' })}
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-4'>
          <div className='flex items-center justify-between rounded-md border p-3'>
            <div className='space-y-1'>
              <Label>{t('channels.dialogs.channelAutoDisableRules.enabled')}</Label>
              <p className='text-muted-foreground text-sm'>{t('channels.dialogs.channelAutoDisableRules.enabledDescription')}</p>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} />
          </div>

          {enabled && (
            <div className='space-y-4 rounded-md border p-4'>
              <div className='grid gap-4 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <Label>{t('channels.dialogs.channelAutoDisableRules.mode')}</Label>
                  <Select value={policy.mode} onValueChange={(value: 'any' | 'codes') => updatePolicy('mode', value)}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value='any'>{t('channels.dialogs.channelAutoDisableRules.modeAny')}</SelectItem>
                      <SelectItem value='codes'>{t('channels.dialogs.channelAutoDisableRules.modeCodes')}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className='space-y-2'>
                  <Label>{t('channels.dialogs.channelAutoDisableRules.times')}</Label>
                  <Input
                    type='number'
                    min={1}
                    value={policy.times}
                    onChange={(event) => updatePolicy('times', Number.parseInt(event.target.value, 10) || 0)}
                  />
                </div>
              </div>

              {policy.mode === 'codes' && (
                <div className='space-y-3'>
                  <div className='flex items-center justify-between'>
                    <Label>{t('channels.dialogs.channelAutoDisableRules.statuses')}</Label>
                    <Button type='button' variant='outline' size='sm' onClick={addStatus}>
                      <IconPlus className='mr-1 h-4 w-4' />
                      {t('channels.dialogs.channelAutoDisableRules.addStatus')}
                    </Button>
                  </div>
                  {statuses.length === 0 ? (
                    <div className='text-muted-foreground rounded-md border border-dashed p-4 text-sm'>
                      {t('channels.dialogs.channelAutoDisableRules.emptyStatuses')}
                    </div>
                  ) : (
                    statuses.map((status, index) => (
                      <div key={`${index}-${status.status}`} className='flex items-center gap-2'>
                        <Badge variant='outline'>{index + 1}</Badge>
                        <Input
                          type='number'
                          min={100}
                          max={599}
                          value={status.status}
                          placeholder={t('channels.dialogs.channelAutoDisableRules.statusPlaceholder')}
                          onChange={(event) => updateStatus(index, 'status', Number.parseInt(event.target.value, 10) || 0)}
                        />
                        <Input
                          type='number'
                          min={1}
                          value={status.times}
                          placeholder={t('channels.dialogs.channelAutoDisableRules.timesPlaceholder')}
                          onChange={(event) => updateStatus(index, 'times', Number.parseInt(event.target.value, 10) || 0)}
                        />
                        <Button type='button' variant='ghost' size='icon' onClick={() => removeStatus(index)}>
                          <IconTrash className='h-4 w-4 text-red-500' />
                        </Button>
                      </div>
                    ))
                  )}
                </div>
              )}

              <div className='grid gap-4 sm:grid-cols-2'>
                <div className='space-y-2'>
                  <Label>{t('channels.dialogs.channelAutoDisableRules.action')}</Label>
                  <Select value={policy.action} onValueChange={(value: 'disable' | 'cooldown') => updatePolicy('action', value)}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value='disable'>{t('channels.dialogs.channelAutoDisableRules.actionDisable')}</SelectItem>
                      <SelectItem value='cooldown'>{t('channels.dialogs.channelAutoDisableRules.actionCooldown')}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                {policy.action === 'cooldown' && (
                  <div className='space-y-2'>
                    <Label>{t('channels.dialogs.channelAutoDisableRules.cooldownMinutes')}</Label>
                    <div className='flex items-center gap-2'>
                      <Input
                        type='number'
                        min={1}
                        max={MAX_COOLDOWN_DURATION_MINUTES}
                        value={policy.cooldownDurationMinutes}
                        onChange={(event) => updatePolicy('cooldownDurationMinutes', Number.parseInt(event.target.value, 10) || 0)}
                      />
                      <span className='text-muted-foreground text-sm'>{t('channels.dialogs.channelAutoDisableRules.cooldownUnit')}</span>
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button type='button' variant='outline' onClick={() => onOpenChange(false)}>
            {t('common.buttons.cancel')}
          </Button>
          <Button type='button' onClick={handleSubmit} disabled={updateChannel.isPending}>
            {t('common.buttons.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
