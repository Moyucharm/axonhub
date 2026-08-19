'use client';

import { useEffect, useMemo, useState } from 'react';
import { Ban, Download, Eye, EyeOff, Play, Plus, RefreshCw, Settings2, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import {
  ChannelAPIKeyStatusFilter,
  useChannelDisabledAPIKeys,
  useCheckChannelAPIKeys,
  useDisableChannelAPIKey,
  useEnableAllChannelAPIKeys,
  useEnableChannelAPIKey,
  useEnableSelectedChannelAPIKeys,
  useExportChannelAPIKeys,
  useImportChannelAPIKeys,
  useRemoveChannelAPIKeys,
  useTestChannelAPIKey,
  useUpdateChannel,
} from '../data/channels';
import { APIKeyAutoDisableRule, Channel } from '../data/schema';
import { DEFAULT_API_KEY_POOL_REQUEST_COUNT } from '../utils/key-pool';
import { mergeChannelSettingsForUpdate } from '../utils/merge';
import { ChannelsAPIKeyRulesDialog } from './channels-apikey-rules-dialog';

interface Props {
  channel: Channel;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Optional callback fired after mutations refresh the channel so parents can update their snapshot. */
  onChannelChange?: (channel: Channel) => void;
}

function parseKeyList(text: string): string[] {
  return Array.from(
    new Set(
      text
        .split(/[\s,;]+/)
        .map((key) => key.trim())
        .filter((key) => key.length > 0)
    )
  );
}

// Default rule shape managed by the Key pool auto-disable toggle. Advanced rules
// (custom status codes / keyword patterns) are managed via the rules dialog and
// left untouched here.
const DEFAULT_DISABLE_STATUS_CODES = [429, 500, 502, 503, 504];
const DEFAULT_DISABLE_DURATION_MINUTES = 30;

function isDefaultDisableRule(rule: APIKeyAutoDisableRule): boolean {
  const codes = [...(rule.statusCodes ?? [])].sort((a, b) => a - b);
  const sameCodes =
    codes.length === DEFAULT_DISABLE_STATUS_CODES.length &&
    codes.every((code, index) => code === DEFAULT_DISABLE_STATUS_CODES[index]);
  const hasPatterns = rule.keywordPatterns?.some((pattern) => pattern.trim() !== '') ?? false;
  return sameCodes && !hasPatterns && rule.action === 'temporary_disable';
}

export function ChannelAPIKeyPoolPanel({ channel, open, onOpenChange, onChannelChange }: Props) {
  const { t } = useTranslation();
  const [showKeys, setShowKeys] = useState(false);
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState<ChannelAPIKeyStatusFilter>('all');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [rulesOpen, setRulesOpen] = useState(false);
  const [confirmDisableOpen, setConfirmDisableOpen] = useState(false);
  const [confirmRemoveOpen, setConfirmRemoveOpen] = useState(false);
  const [testResult, setTestResult] = useState<Map<string, { success: boolean; error?: string | null }>>(new Map());
  // Local key list so mutations (import/remove) reflect immediately without
  // waiting for the parent channel snapshot to refresh.
  const [localKeys, setLocalKeys] = useState<string[] | null>(null);

  useEffect(() => {
    if (open) {
      setLocalKeys(null);
      setSelected(new Set());
      setSearch('');
      setStatus('all');
      setTestResult(new Map());
    }
  }, [open]);

  const { data: disabledKeys = [] } = useChannelDisabledAPIKeys(channel.id, { enabled: open });
  const disabledSet = useMemo(() => new Set(disabledKeys.map((entry) => entry.key)), [disabledKeys]);
  const disabledMeta = useMemo(
    () => new Map(disabledKeys.map((entry) => [entry.key, entry])),
    [disabledKeys]
  );
  const states = useMemo(
    () => new Map((channel.credentials?.apiKeyStates ?? []).map((entry) => [entry.key, entry])),
    [channel.credentials?.apiKeyStates]
  );

  const allKeys = localKeys ?? channel.credentials?.apiKeys ?? [];
  const keys = useMemo(
    () =>
      allKeys.filter((key) => {
        const disabled = disabledSet.has(key);
        if (status === 'enabled' && disabled) return false;
        if (status === 'disabled' && !disabled) return false;
        if (!search.trim()) return true;
        return key.toLowerCase().includes(search.trim().toLowerCase()) || key.slice(-4).includes(search.trim());
      }),
    [allKeys, disabledSet, search, status]
  );

  const importKeys = useImportChannelAPIKeys();
  const exportKeys = useExportChannelAPIKeys();
  const removeKeys = useRemoveChannelAPIKeys();
  const checkKeys = useCheckChannelAPIKeys();
  const enableKey = useEnableChannelAPIKey();
  const enableAllKeys = useEnableAllChannelAPIKeys();
  const enableSelectedKeys = useEnableSelectedChannelAPIKeys();
  const disableKey = useDisableChannelAPIKey();
  const testKey = useTestChannelAPIKey();
  const updateChannel = useUpdateChannel();

  const poolSettings = channel.settings?.apiKeyPool;
  const [retryCount, setRetryCount] = useState(poolSettings?.retryCount ?? DEFAULT_API_KEY_POOL_REQUEST_COUNT);
  const [autoCheckEnabled, setAutoCheckEnabled] = useState(poolSettings?.autoCheckEnabled ?? false);
  const [autoCheckIntervalHours, setAutoCheckIntervalHours] = useState(poolSettings?.autoCheckIntervalHours ?? 24);
  const [autoCheckConcurrency, setAutoCheckConcurrency] = useState(poolSettings?.autoCheckConcurrency ?? 4);
  const [autoCheckTimeoutSeconds, setAutoCheckTimeoutSeconds] = useState(poolSettings?.autoCheckTimeoutSeconds ?? 20);
  const [autoDisableEnabled, setAutoDisableEnabled] = useState(false);
  const [autoDisableThreshold, setAutoDisableThreshold] = useState(3);
  const [hasAdvancedRules, setHasAdvancedRules] = useState(false);

  useEffect(() => {
    if (settingsOpen) {
      setRetryCount(poolSettings?.retryCount ?? DEFAULT_API_KEY_POOL_REQUEST_COUNT);
      setAutoCheckEnabled(poolSettings?.autoCheckEnabled ?? false);
      setAutoCheckIntervalHours(poolSettings?.autoCheckIntervalHours ?? 24);
      setAutoCheckConcurrency(poolSettings?.autoCheckConcurrency ?? 4);
      setAutoCheckTimeoutSeconds(poolSettings?.autoCheckTimeoutSeconds ?? 20);
      const rules = channel.policies?.apiKeyAutoDisableRules ?? [];
      const defaultRule = rules.find(isDefaultDisableRule);
      const advancedRules = rules.filter((rule) => !isDefaultDisableRule(rule));
      setAutoDisableEnabled(rules.length > 0);
      setAutoDisableThreshold(defaultRule?.times ?? advancedRules[0]?.times ?? 3);
      setHasAdvancedRules(advancedRules.length > 0);
    }
  }, [settingsOpen, poolSettings, channel]);

  // Push an updated channel snapshot to the parent (e.g. edit dialog summary).
  const syncNext = (nextKeys: string[]) => {
    setLocalKeys(nextKeys);
    onChannelChange?.({
      ...channel,
      credentials: {
        ...(channel.credentials ?? {}),
        apiKeys: nextKeys,
      },
    });
  };

  const handleExport = async () => {
    if (selected.size > 0) {
      // Export the selected keys locally; the backend export only supports status filters.
      const text = Array.from(selected).join('\n');
      const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = `${channel.name}-keys-selected.txt`;
      anchor.click();
      URL.revokeObjectURL(url);
      toast.success(t('channels.keyPool.exportSelectedResult', { count: selected.size }));
      return;
    }
    const text = await exportKeys.mutateAsync({ channelID: channel.id, status });
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `${channel.name}-keys-${status}.txt`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  const handleImport = async () => {
    const parsed = parseKeyList(importText);
    if (parsed.length === 0) return;
    const result = await importKeys.mutateAsync({ channelID: channel.id, text: importText });
    toast.success(t('channels.keyPool.importResult', result));
    syncNext(Array.from(new Set([...allKeys, ...parsed])));
    setImportText('');
    setImportOpen(false);
  };

  const handleRemove = async () => {
    if (selected.size === 0) return;
    await removeKeys.mutateAsync({ channelID: channel.id, keys: Array.from(selected) });
    syncNext(allKeys.filter((key) => !selected.has(key)));
    setSelected(new Set());
    setConfirmRemoveOpen(false);
  };

  const handleEnableSelected = async () => {
    if (selected.size === 0) return;
    await enableSelectedKeys.mutateAsync({ channelID: channel.id, keys: Array.from(selected) });
    setSelected(new Set());
  };

  const handleDisableSelected = async () => {
    if (selected.size === 0) return;
    await Promise.all(Array.from(selected).map((key) => disableKey.mutateAsync({ channelID: channel.id, key })));
    setSelected(new Set());
    setConfirmDisableOpen(false);
  };

  const handleEnableAll = async () => {
    await enableAllKeys.mutateAsync({ channelID: channel.id });
    setSelected(new Set());
  };

  const handleCheck = async () => {
    if (selected.size > 0) {
      // Check only the selected keys via the single-key mutation (no backend change).
      const results = await Promise.all(
        Array.from(selected).map(async (key) => {
          const result = await testKey.mutateAsync({ channelID: channel.id, key });
          return { key, success: result.success, error: result.error };
        })
      );
      const success = results.filter((result) => result.success).length;
      toast.success(t('channels.keyPool.checkResult', { success, total: results.length }));
      setTestResult(new Map(results.map((result) => [result.key, result])));
      return;
    }
    const results = await checkKeys.mutateAsync({ channelID: channel.id, status });
    const success = results.filter((result) => result.success).length;
    toast.success(t('channels.keyPool.checkResult', { success, total: results.length }));
    setTestResult(new Map(results.map((result) => [result.key, result])));
  };

  const handleTestKey = async (key: string) => {
    const result = await testKey.mutateAsync({ channelID: channel.id, key });
    setTestResult((previous) => new Map(previous).set(key, { success: result.success, error: result.error }));
  };

  const handleSaveSettings = async () => {
    if (retryCount < 1) {
      toast.error(t('channels.keyPool.requestCountTooSmall'));
      return;
    }
    if (autoCheckEnabled && autoCheckIntervalHours < 1) {
      toast.error(t('channels.keyPool.intervalTooSmall'));
      return;
    }
    if (autoDisableEnabled && autoDisableThreshold < 1) {
      toast.error(t('channels.keyPool.thresholdTooSmall'));
      return;
    }
    const existingPolicies = channel.policies ?? {};
    let nextRules: APIKeyAutoDisableRule[];
    if (autoDisableEnabled) {
      // Make sure the default rule exists (advanced rules are kept untouched).
      const rules = [...(channel.policies?.apiKeyAutoDisableRules ?? [])];
      const defaultRule: APIKeyAutoDisableRule = {
        statusCodes: DEFAULT_DISABLE_STATUS_CODES,
        keywordPatterns: [],
        times: autoDisableThreshold,
        action: 'temporary_disable',
        disableDurationMinutes: DEFAULT_DISABLE_DURATION_MINUTES,
      };
      const index = rules.findIndex(isDefaultDisableRule);
      if (index >= 0) {
        rules[index] = defaultRule;
      } else {
        rules.push(defaultRule);
      }
      nextRules = rules;
    } else {
      // Remove only the default rule (nothing else remains when there are no advanced rules).
      nextRules = (channel.policies?.apiKeyAutoDisableRules ?? []).filter((rule) => !isDefaultDisableRule(rule));
    }
    await updateChannel.mutateAsync({
      id: channel.id,
      input: {
        policies: { ...existingPolicies, apiKeyAutoDisableRules: nextRules },
        settings: mergeChannelSettingsForUpdate(channel.settings, {
          apiKeyPool: {
            retryCount,
            autoCheckEnabled,
            autoCheckIntervalHours: autoCheckEnabled ? autoCheckIntervalHours : poolSettings?.autoCheckIntervalHours,
            autoCheckConcurrency: autoCheckEnabled ? autoCheckConcurrency : poolSettings?.autoCheckConcurrency,
            autoCheckTimeoutSeconds: autoCheckEnabled ? autoCheckTimeoutSeconds : poolSettings?.autoCheckTimeoutSeconds,
          },
        }),
      },
    });
    setSettingsOpen(false);
  };

  const selectedDisabledCount = Array.from(selected).filter((key) => disabledSet.has(key)).length;
  const totalKeys = allKeys.length;
  const enabledKeys = Math.max(0, totalKeys - disabledSet.size);

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className='flex max-h-[90vh] flex-col sm:max-w-4xl'>
          <DialogHeader>
            <DialogTitle>{t('channels.keyPool.title', { name: channel.name })}</DialogTitle>
            <DialogDescription>{t('channels.keyPool.description')}</DialogDescription>
          </DialogHeader>
          <div className='flex flex-wrap items-center gap-2'>
            <Badge variant='secondary' className='px-1.5 py-0 tabular-nums'>
              {t('channels.keyPool.summary', { enabled: enabledKeys, total: totalKeys })}
            </Badge>
            {disabledSet.size > 0 && (
              <Badge variant='outline' className='px-1.5 py-0 text-amber-500'>
                {t('channels.keyPool.disabledCount', { count: disabledSet.size })}
              </Badge>
            )}
          </div>
          <div className='flex flex-wrap gap-2'>
            <Input className='min-w-52 flex-1' value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t('channels.keyPool.search')} />
            <Select value={status} onValueChange={(value) => setStatus(value as ChannelAPIKeyStatusFilter)}>
              <SelectTrigger className='w-36'><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value='all'>{t('channels.keyPool.status.all')}</SelectItem>
                <SelectItem value='enabled'>{t('channels.keyPool.status.enabled')}</SelectItem>
                <SelectItem value='disabled'>{t('channels.keyPool.status.disabled')}</SelectItem>
              </SelectContent>
            </Select>
            <Button variant='outline' onClick={() => setShowKeys((value) => !value)}>{showKeys ? <EyeOff className='h-4 w-4' /> : <Eye className='h-4 w-4' />}</Button>
            <Button variant='outline' onClick={() => setImportOpen(true)}><Plus className='mr-1 h-4 w-4' />{t('channels.keyPool.import')}</Button>
            <Button variant='outline' onClick={handleExport} disabled={exportKeys.isPending}>
              <Download className='mr-1 h-4 w-4' />
              {selected.size > 0 ? t('channels.keyPool.exportSelected', { count: selected.size }) : t('channels.keyPool.exportAll')}
            </Button>
            <Button variant='outline' onClick={handleCheck} disabled={checkKeys.isPending || testKey.isPending}>
              <Play className='mr-1 h-4 w-4' />
              {selected.size > 0 ? t('channels.keyPool.checkSelected', { count: selected.size }) : t('channels.keyPool.checkAll')}
            </Button>
            <Button variant='outline' onClick={() => setSettingsOpen(true)}><Settings2 className='mr-1 h-4 w-4' />{t('channels.keyPool.settings')}</Button>
          </div>
          <div className='min-h-0 flex-1 space-y-2 overflow-auto rounded-md border p-2'>
            <div className='text-muted-foreground flex items-center gap-3 border-b pb-2 text-xs'>
              <Checkbox
                checked={
                  keys.length > 0 && keys.every((key) => selected.has(key))
                    ? true
                    : keys.some((key) => selected.has(key))
                      ? 'indeterminate'
                      : false
                }
                onCheckedChange={(checked) => setSelected(checked ? new Set(keys) : new Set())}
              />
              <span>{t('channels.keyPool.selectAll')}</span>
              <span className='ml-auto tabular-nums'>{t('channels.keyPool.selectedCount', { count: selected.size })}</span>
            </div>
            {keys.length === 0 && <p className='text-muted-foreground p-4 text-center text-sm'>{t('channels.keyPool.empty')}</p>}
            {keys.map((key) => {
              const disabled = disabledSet.has(key);
              const state = states.get(key);
              const meta = disabledMeta.get(key);
              const test = testResult.get(key);
              return (
                <div key={key} className='flex items-center gap-3 rounded-md border p-3'>
                  <Checkbox checked={selected.has(key)} onCheckedChange={(checked) => setSelected((previous) => {
                    const next = new Set(previous);
                    if (checked) next.add(key); else next.delete(key);
                    return next;
                  })} />
                  <div className='min-w-0 flex-1'>
                    <code className='block truncate text-xs'>{showKeys ? key : `${key.slice(0, 4)}****${key.slice(-4)}`}</code>
                    <div className='text-muted-foreground mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs'>
                      <Badge variant={disabled ? 'destructive' : 'secondary'} className='px-1.5 py-0'>{disabled ? t('channels.keyPool.status.disabled') : t('channels.keyPool.status.enabled')}</Badge>
                      {state && (state.failureCount > 0 || state.lastErrorCode !== 0) ? (
                        <>
                          {state.failureCount > 0 && <span>{t('channels.keyPool.failures', { count: state.failureCount })}</span>}
                          {state.lastFailedAt && <span>{t('channels.keyPool.lastFailed', { time: state.lastFailedAt })}</span>}
                          {state.lastErrorCode !== 0 && <span>{t('channels.keyPool.errorCode', { code: state.lastErrorCode })}</span>}
                        </>
                      ) : null}
                      {meta?.reason && <span className='text-muted-foreground truncate'>{meta.reason}</span>}
                      {meta?.expiresAt && <span>{t('channels.keyPool.expiresAt', { time: meta.expiresAt })}</span>}
                      {test && (
                        <span className={test.success ? 'text-green-600' : 'text-destructive'}>
                          {test.success ? t('channels.keyPool.testPassed') : t('channels.keyPool.testFailed', { error: test.error ?? '' })}
                        </span>
                      )}
                    </div>
                  </div>
                  <div className='flex shrink-0 items-center gap-1'>
                    <Button size='sm' variant='ghost' onClick={() => handleTestKey(key)} disabled={testKey.isPending} title={t('channels.keyPool.check')}>
                      <Play className='h-4 w-4' />
                    </Button>
                    {disabled ? (
                      <Button size='sm' variant='ghost' onClick={() => enableKey.mutate({ channelID: channel.id, key })}><RefreshCw className='h-4 w-4' /></Button>
                    ) : (
                      <Button size='sm' variant='ghost' onClick={() => disableKey.mutate({ channelID: channel.id, key })} title={t('channels.keyPool.disable')}>
                        <Ban className='h-4 w-4' />
                      </Button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
          <DialogFooter className='flex flex-wrap items-center gap-2'>
            <div className='mr-auto flex flex-wrap items-center gap-2'>
              <Button variant='outline' size='sm' onClick={() => setConfirmDisableOpen(true)} disabled={selected.size === 0 || selectedDisabledCount === 0}>
                {t('channels.keyPool.disableSelected', { count: selected.size })}
              </Button>
              <Button variant='outline' size='sm' onClick={handleEnableSelected} disabled={selected.size === 0 || selectedDisabledCount === 0}>
                {t('channels.keyPool.enableSelected', { count: selected.size })}
              </Button>
              <Button variant='outline' size='sm' onClick={handleEnableAll} disabled={disabledSet.size === 0}>
                {t('channels.keyPool.enableAll')}
              </Button>
            </div>
            <Button variant='destructive' size='sm' onClick={() => setConfirmRemoveOpen(true)} disabled={selected.size === 0 || removeKeys.isPending}>
              <Trash2 className='mr-1 h-4 w-4' />{t('channels.keyPool.removeSelected', { count: selected.size })}
            </Button>
            <Button variant='outline' onClick={() => onOpenChange(false)}>{t('common.buttons.close')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={importOpen} onOpenChange={setImportOpen}>
        <DialogContent className='grid-rows-[auto_minmax(0,1fr)_auto] overflow-x-hidden overflow-y-hidden sm:max-w-lg'>
          <DialogHeader><DialogTitle>{t('channels.keyPool.importTitle')}</DialogTitle><DialogDescription>{t('channels.keyPool.importDescription')}</DialogDescription></DialogHeader>
          <Textarea value={importText} onChange={(event) => setImportText(event.target.value)} rows={12} className='min-h-0 resize-y overflow-y-auto font-mono' />
          <DialogFooter><Button variant='outline' onClick={() => setImportOpen(false)}>{t('common.buttons.cancel')}</Button><Button onClick={handleImport} disabled={!importText.trim() || importKeys.isPending}>{t('channels.keyPool.import')}</Button></DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={settingsOpen} onOpenChange={setSettingsOpen}>
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-lg'>
          <DialogHeader>
            <DialogTitle>{t('channels.keyPool.settingsTitle')}</DialogTitle>
            <DialogDescription>{t('channels.keyPool.settingsDescription')}</DialogDescription>
          </DialogHeader>
          <div className='space-y-3'>
            <Card className='gap-4 py-4'>
              <CardHeader className='flex flex-row items-start justify-between gap-4 px-4'>
                <div className='space-y-1'>
                  <CardTitle className='text-sm'>{t('channels.keyPool.requestStrategyTitle')}</CardTitle>
                  <CardDescription className='text-xs leading-relaxed'>{t('channels.keyPool.retryCountDescription')}</CardDescription>
                </div>
                <div className='flex shrink-0 items-center gap-2'>
                  <Input
                    type='number'
                    min={1}
                    className='w-20'
                    value={retryCount}
                    onChange={(event) => setRetryCount(Number(event.target.value))}
                    aria-label={t('channels.keyPool.retryCount')}
                  />
                  <span className='text-muted-foreground text-xs'>{t('channels.keyPool.times')}</span>
                </div>
              </CardHeader>
            </Card>

            <Card className='gap-4 py-4'>
              <CardHeader className='flex flex-row items-start justify-between gap-4 px-4'>
                <div className='space-y-1'>
                  <CardTitle className='text-sm'>{t('channels.keyPool.autoCheck')}</CardTitle>
                  <CardDescription className='text-xs leading-relaxed'>{t('channels.keyPool.autoCheckDescription')}</CardDescription>
                </div>
                <Checkbox checked={autoCheckEnabled} onCheckedChange={(checked) => setAutoCheckEnabled(!!checked)} />
              </CardHeader>
              {autoCheckEnabled && (
                <CardContent className='space-y-3 px-4 pt-0'>
                  <div className='grid gap-3 sm:grid-cols-3'>
                    <div className='space-y-1'>
                      <label className='text-muted-foreground text-xs font-medium'>{t('channels.keyPool.autoCheckInterval')}</label>
                      <div className='flex items-center gap-2'>
                        <Input type='number' min={1} className='w-full' value={autoCheckIntervalHours} onChange={(event) => setAutoCheckIntervalHours(Number(event.target.value))} />
                        <span className='text-muted-foreground text-xs'>{t('channels.keyPool.hours')}</span>
                      </div>
                    </div>
                    <div className='space-y-1'>
                      <label className='text-muted-foreground text-xs font-medium'>{t('channels.keyPool.autoCheckConcurrency')}</label>
                      <Input type='number' min={1} max={32} className='w-full' value={autoCheckConcurrency} onChange={(event) => setAutoCheckConcurrency(Number(event.target.value))} />
                    </div>
                    <div className='space-y-1'>
                      <label className='text-muted-foreground text-xs font-medium'>{t('channels.keyPool.autoCheckTimeout')}</label>
                      <div className='flex items-center gap-2'>
                        <Input type='number' min={1} max={600} className='w-full' value={autoCheckTimeoutSeconds} onChange={(event) => setAutoCheckTimeoutSeconds(Number(event.target.value))} />
                        <span className='text-muted-foreground text-xs'>{t('channels.keyPool.seconds')}</span>
                      </div>
                    </div>
                  </div>
                  {poolSettings?.lastAutoCheckAt && (
                    <p className='text-muted-foreground text-xs'>{t('channels.keyPool.lastAutoCheck', { time: poolSettings.lastAutoCheckAt })}</p>
                  )}
                </CardContent>
              )}
            </Card>

            <Card className='gap-4 py-4'>
              <CardHeader className='flex flex-row items-start justify-between gap-4 px-4'>
                <div className='space-y-1'>
                  <CardTitle className='text-sm'>{t('channels.keyPool.autoDisable')}</CardTitle>
                  <CardDescription className='text-xs leading-relaxed'>{t('channels.keyPool.autoDisableDescription')}</CardDescription>
                </div>
                <Checkbox checked={autoDisableEnabled} disabled={hasAdvancedRules} onCheckedChange={(checked) => setAutoDisableEnabled(!!checked)} />
              </CardHeader>
              <CardContent className='space-y-3 px-4 pt-0'>
                {hasAdvancedRules && <p className='text-muted-foreground text-xs'>{t('channels.keyPool.advancedRulesHint')}</p>}
                {autoDisableEnabled && (
                  <div className='flex items-center justify-between gap-3'>
                    <label className='text-sm font-medium'>{t('channels.keyPool.autoDisableThreshold')}</label>
                    <div className='flex items-center gap-2'>
                      <Input type='number' min={1} className='w-20' value={autoDisableThreshold} onChange={(event) => setAutoDisableThreshold(Number(event.target.value))} />
                      <span className='text-muted-foreground text-xs'>{t('channels.keyPool.times')}</span>
                    </div>
                  </div>
                )}
                <div className='flex items-center justify-between gap-3 border-t pt-3'>
                  <div>
                    <p className='text-sm font-medium'>{t('channels.keyPool.rules')}</p>
                    <p className='text-muted-foreground text-xs'>
                      {t('channels.keyPool.rulesCount', {
                        count: channel.policies?.apiKeyAutoDisableRules?.length ?? 0,
                      })}
                    </p>
                  </div>
                  <Button variant='outline' size='sm' onClick={() => setRulesOpen(true)}>
                    <Settings2 className='mr-1 h-4 w-4' />
                    {t('channels.keyPool.manageRules')}
                  </Button>
                </div>
              </CardContent>
            </Card>
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => setSettingsOpen(false)}>{t('common.buttons.cancel')}</Button>
            <Button onClick={handleSaveSettings} disabled={updateChannel.isPending}>{t('common.buttons.save')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={confirmDisableOpen} onOpenChange={setConfirmDisableOpen}>
        <DialogContent className='sm:max-w-md'>
          <DialogHeader>
            <DialogTitle>{t('channels.keyPool.confirmDisableSelected', { count: selected.size })}</DialogTitle>
          </DialogHeader>
          <DialogFooter>
            <Button variant='outline' onClick={() => setConfirmDisableOpen(false)}>{t('common.buttons.cancel')}</Button>
            <Button variant='destructive' onClick={handleDisableSelected} disabled={disableKey.isPending}>{t('common.buttons.confirm')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={confirmRemoveOpen} onOpenChange={setConfirmRemoveOpen}>
        <DialogContent className='sm:max-w-md'>
          <DialogHeader>
            <DialogTitle>{t('channels.keyPool.confirmRemoveSelected', { count: selected.size })}</DialogTitle>
          </DialogHeader>
          <DialogFooter>
            <Button variant='outline' onClick={() => setConfirmRemoveOpen(false)}>{t('common.buttons.cancel')}</Button>
            <Button variant='destructive' onClick={handleRemove} disabled={removeKeys.isPending}>{t('common.buttons.confirm')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ChannelsAPIKeyRulesDialog open={rulesOpen} onOpenChange={setRulesOpen} currentRow={channel} />
    </>
  );
}