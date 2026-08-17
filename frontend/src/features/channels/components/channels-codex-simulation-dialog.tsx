'use client';

import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Fingerprint, RefreshCw } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Separator } from '@/components/ui/separator';
import { Switch } from '@/components/ui/switch';
import { useRandomizeChannelCodexSimulation, useUpdateChannel } from '../data/channels';
import {
  Channel,
  CodexSimulationOptions,
  CodexSimulationPlatform,
  CodexSimulationPreset,
  CodexSimulationSettings,
  CodexSimulationStrategy,
  CODEX_SIMULATION_PRESET_DEFAULTS,
  DEFAULT_CODEX_SIMULATION_PLATFORM,
  DEFAULT_CODEX_SIMULATION_VERSION,
  buildCodexSimulationUserAgents,
  inferCodexSimulationPlatform,
  isCodexSimulationCustomized,
} from '../data/schema';
import { mergeChannelSettingsForUpdate } from '../utils/merge';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentRow: Channel;
}

const PRESET_ORDER: CodexSimulationPreset[] = ['ua', 'normal', 'enhanced'];

const OPTION_KEYS: Array<keyof CodexSimulationOptions> = [
  'prompt',
  'userAgent',
  'codexHeaders',
  'clientMetadata',
  'responsesShape',
  'additionalTool',
];

function defaultsFromSettings(sim: CodexSimulationSettings | null | undefined): {
  enabled: boolean;
  preset: CodexSimulationPreset;
  options: CodexSimulationOptions;
  version: string;
  platform: CodexSimulationPlatform;
  standardUserAgent: string;
  liteUserAgent: string;
  strategy: CodexSimulationStrategy | null;
} {
  const preset: CodexSimulationPreset = sim?.preset ?? 'normal';
  const version = sim?.version?.trim() || DEFAULT_CODEX_SIMULATION_VERSION;
  const platform =
    sim?.platform ?? inferCodexSimulationPlatform(sim?.standardUserAgent, sim?.liteUserAgent);
  const defaultUserAgents = buildCodexSimulationUserAgents(version, platform);
  return {
    enabled: sim?.enabled ?? false,
    preset,
    options: sim?.options ?? CODEX_SIMULATION_PRESET_DEFAULTS[preset],
    version,
    platform,
    standardUserAgent: sim?.standardUserAgent?.trim() || defaultUserAgents.standard,
    liteUserAgent: sim?.liteUserAgent?.trim() || defaultUserAgents.lite,
    strategy: sim?.strategy ?? null,
  };
}

export function ChannelsCodexSimulationDialog({ open, onOpenChange, currentRow }: Props) {
  const { t } = useTranslation();
  const updateChannel = useUpdateChannel();
  const randomizeSimulation = useRandomizeChannelCodexSimulation();

  const [enabled, setEnabled] = useState(false);
  const [preset, setPreset] = useState<CodexSimulationPreset>('normal');
  const [options, setOptions] = useState<CodexSimulationOptions>(CODEX_SIMULATION_PRESET_DEFAULTS.normal);
  const [version, setVersion] = useState(DEFAULT_CODEX_SIMULATION_VERSION);
  const [platform, setPlatform] = useState<CodexSimulationPlatform>(DEFAULT_CODEX_SIMULATION_PLATFORM);
  const [standardUserAgent, setStandardUserAgent] = useState(
    buildCodexSimulationUserAgents(DEFAULT_CODEX_SIMULATION_VERSION).standard
  );
  const [liteUserAgent, setLiteUserAgent] = useState(
    buildCodexSimulationUserAgents(DEFAULT_CODEX_SIMULATION_VERSION).lite
  );
  const [strategy, setStrategy] = useState<CodexSimulationStrategy | null>(null);

  useEffect(() => {
    if (open) {
      const initial = defaultsFromSettings(currentRow.settings?.codexSimulation);
      setEnabled(initial.enabled);
      setPreset(initial.preset);
      setOptions(initial.options);
      setVersion(initial.version);
      setPlatform(initial.platform);
      setStandardUserAgent(initial.standardUserAgent);
      setLiteUserAgent(initial.liteUserAgent);
      setStrategy(initial.strategy);
    }
  }, [open, currentRow]);

  const defaultUserAgents = buildCodexSimulationUserAgents(version, platform);
  const persisted = defaultsFromSettings(currentRow.settings?.codexSimulation);
  const draftDirty =
    enabled !== persisted.enabled ||
    preset !== persisted.preset ||
    JSON.stringify(options) !== JSON.stringify(persisted.options) ||
    version !== persisted.version ||
    platform !== persisted.platform ||
    standardUserAgent !== persisted.standardUserAgent ||
    liteUserAgent !== persisted.liteUserAgent;
  const customized =
    isCodexSimulationCustomized(preset, options) ||
    version !== DEFAULT_CODEX_SIMULATION_VERSION ||
    standardUserAgent !== defaultUserAgents.standard ||
    liteUserAgent !== defaultUserAgents.lite;

  const updateVersion = (nextVersion: string) => {
    const previousDefaults = buildCodexSimulationUserAgents(version, platform);
    const nextDefaults = buildCodexSimulationUserAgents(nextVersion, platform);
    setVersion(nextVersion);
    setStandardUserAgent((current) => (current === previousDefaults.standard ? nextDefaults.standard : current));
    setLiteUserAgent((current) => (current === previousDefaults.lite ? nextDefaults.lite : current));
  };

  const applyPreset = (nextPreset: CodexSimulationPreset) => {
    setPreset(nextPreset);
    setOptions(CODEX_SIMULATION_PRESET_DEFAULTS[nextPreset]);
  };

  const toggleOption = (key: keyof CodexSimulationOptions, checked: boolean) => {
    setOptions((prev) => ({ ...prev, [key]: checked }));
  };

  const onSubmit = async () => {
    const codexSimulation = {
      enabled,
      preset,
      options,
      version,
      platform,
      standardUserAgent,
      liteUserAgent,
    };
    const nextSettings = mergeChannelSettingsForUpdate(currentRow.settings, { codexSimulation });

    await updateChannel.mutateAsync({
      id: currentRow.id,
      input: { settings: nextSettings },
    });
    onOpenChange(false);
  };

  const onRandomize = async () => {
    const sim = await randomizeSimulation.mutateAsync(currentRow.id);
    if (sim) {
      const next = defaultsFromSettings(sim);
      setEnabled(next.enabled);
      setPreset(next.preset);
      setOptions(next.options);
      setVersion(next.version);
      setPlatform(next.platform);
      setStandardUserAgent(next.standardUserAgent);
      setLiteUserAgent(next.liteUserAgent);
      setStrategy(next.strategy);
    }
  };

  const busy = updateChannel.isPending || randomizeSimulation.isPending;

  return (
    <Dialog
      open={open}
      onOpenChange={(state) => {
        if (!state) {
          onOpenChange(state);
        }
      }}
    >
      <DialogContent className='flex h-[85vh] max-h-[700px] flex-col sm:max-w-2xl'>
        <DialogHeader className='shrink-0 text-left'>
          <DialogTitle>{t('channels.codexSimulation.title')}</DialogTitle>
          <DialogDescription>{t('channels.codexSimulation.description', { name: currentRow.name })}</DialogDescription>
        </DialogHeader>

        <div className='min-h-0 flex-1 space-y-6 overflow-y-auto py-1 pr-1'>
          {/* 总开关 */}
          <div className='flex items-center justify-between rounded-lg border p-4'>
            <div className='space-y-0.5'>
              <p className='text-sm font-medium'>{t('channels.codexSimulation.enable.label')}</p>
              <p className='text-muted-foreground text-xs'>{t('channels.codexSimulation.enable.description')}</p>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} />
          </div>

          {/* 预设等级 */}
          <Card>
            <CardHeader>
              <CardTitle className='text-lg'>{t('channels.codexSimulation.preset.title')}</CardTitle>
              <CardDescription>{t('channels.codexSimulation.preset.description')}</CardDescription>
            </CardHeader>
            <CardContent className='space-y-3'>
              {PRESET_ORDER.map((value) => (
                <label
                  key={value}
                  className={`flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors ${
                    preset === value ? 'border-primary bg-primary/5' : 'hover:bg-muted/50'
                  }`}
                >
                  <Checkbox
                    checked={preset === value}
                    onCheckedChange={() => applyPreset(value)}
                    aria-label={t(`channels.codexSimulation.preset.${value}.label`)}
                  />
                  <div className='space-y-0.5'>
                    <p className='text-sm font-medium'>{t(`channels.codexSimulation.preset.${value}.label`)}</p>
                    <p className='text-muted-foreground text-xs'>{t(`channels.codexSimulation.preset.${value}.description`)}</p>
                  </div>
                </label>
              ))}
            </CardContent>
          </Card>

          {/* 子选项 */}
          <Card>
            <CardHeader className='flex flex-row items-center justify-between space-y-0'>
              <div>
                <CardTitle className='text-lg'>{t('channels.codexSimulation.options.title')}</CardTitle>
                <CardDescription>{t('channels.codexSimulation.options.description')}</CardDescription>
              </div>
              {customized && <Badge variant='secondary'>{t('channels.codexSimulation.customized')}</Badge>}
            </CardHeader>
            <CardContent className='space-y-4'>
              {OPTION_KEYS.map((key) => (
                <div key={key} className='flex items-center gap-2'>
                  <Checkbox
                    id={`codex-sim-option-${key}`}
                    checked={options[key]}
                    onCheckedChange={(checked) => toggleOption(key, checked === true)}
                  />
                  <div className='space-y-0.5'>
                    <label
                      htmlFor={`codex-sim-option-${key}`}
                      className='cursor-pointer text-sm font-normal'
                    >
                      {t(`channels.codexSimulation.options.${key}.label`)}
                    </label>
                    <p className='text-muted-foreground text-xs'>
                      {t(`channels.codexSimulation.options.${key}.description`)}
                    </p>
                  </div>
                </div>
              ))}
            </CardContent>
          </Card>

          {/* 渠道独立策略 */}
          <Card>
            <CardHeader className='flex flex-row items-center justify-between space-y-0'>
              <div>
                <CardTitle className='text-lg'>{t('channels.codexSimulation.strategy.title')}</CardTitle>
                <CardDescription>{t('channels.codexSimulation.strategy.description')}</CardDescription>
              </div>
              <Fingerprint size={16} className='text-muted-foreground' />
            </CardHeader>
            <CardContent className='space-y-3'>
              <div className='space-y-1.5'>
                <label htmlFor='codex-sim-platform' className='text-sm font-medium'>
                  {t('channels.codexSimulation.fingerprint.platform.label')}
                </label>
                <Input
                  id='codex-sim-platform'
                  value={t(`channels.codexSimulation.fingerprint.platform.values.${platform}`)}
                  readOnly
                  className='font-mono text-xs'
                />
                <p className='text-muted-foreground text-xs'>
                  {t('channels.codexSimulation.fingerprint.platform.description')}
                </p>
              </div>
              <div className='space-y-1.5'>
                <label htmlFor='codex-sim-version' className='text-sm font-medium'>
                  {t('channels.codexSimulation.fingerprint.version.label')}
                </label>
                <Input
                  id='codex-sim-version'
                  value={version}
                  maxLength={32}
                  disabled={busy}
                  onChange={(event) => updateVersion(event.target.value)}
                  placeholder={DEFAULT_CODEX_SIMULATION_VERSION}
                  className='font-mono text-xs'
                />
                <p className='text-muted-foreground text-xs'>
                  {t('channels.codexSimulation.fingerprint.version.description')}
                </p>
              </div>
              <div className='space-y-1.5'>
                <label htmlFor='codex-sim-standard-ua' className='text-sm font-medium'>
                  {t('channels.codexSimulation.fingerprint.standardUserAgent')}
                </label>
                <Input
                  id='codex-sim-standard-ua'
                  value={standardUserAgent}
                  maxLength={512}
                  disabled={busy}
                  onChange={(event) => {
                    setPlatform('custom');
                    setStandardUserAgent(event.target.value);
                  }}
                  className='font-mono text-xs'
                />
              </div>
              <div className='space-y-1.5'>
                <label htmlFor='codex-sim-lite-ua' className='text-sm font-medium'>
                  {t('channels.codexSimulation.fingerprint.liteUserAgent')}
                </label>
                <Input
                  id='codex-sim-lite-ua'
                  value={liteUserAgent}
                  maxLength={512}
                  disabled={busy}
                  onChange={(event) => {
                    setPlatform('custom');
                    setLiteUserAgent(event.target.value);
                  }}
                  className='font-mono text-xs'
                />
              </div>
              <Separator />
              {strategy ? (
                <>
                  <div className='flex items-center justify-between gap-4 text-sm'>
                    <span className='text-muted-foreground shrink-0'>
                      {t('channels.codexSimulation.strategy.installationId')}
                    </span>
                    <code className='truncate font-mono text-xs'>{strategy.installationId}</code>
                  </div>
                  <div className='flex items-center justify-between gap-4 text-sm'>
                    <span className='text-muted-foreground shrink-0'>{t('channels.codexSimulation.strategy.threadId')}</span>
                    <code className='truncate font-mono text-xs'>{strategy.threadId}</code>
                  </div>
                  <div className='flex items-center justify-between gap-4 text-sm'>
                    <span className='text-muted-foreground shrink-0'>
                      {t('channels.codexSimulation.strategy.windowGeneration')}
                    </span>
                    <code className='truncate font-mono text-xs'>{strategy.windowGeneration}</code>
                  </div>
                </>
              ) : (
                <p className='text-muted-foreground text-sm'>{t('channels.codexSimulation.strategy.empty')}</p>
              )}
              <Separator className='my-2' />
              <div className='flex items-center justify-end gap-2'>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={!enabled || busy || draftDirty}
                  title={draftDirty ? t('channels.codexSimulation.messages.saveBeforeRandomize') : undefined}
                  onClick={onRandomize}
                >
                  <RefreshCw size={14} className='mr-1' />
                  {t('channels.codexSimulation.buttons.randomize')}
                </Button>
              </div>
            </CardContent>
          </Card>
        </div>

        <DialogFooter className='shrink-0'>
          <Button type='button' variant='outline' onClick={() => onOpenChange(false)}>
            {t('common.buttons.cancel')}
          </Button>
          <Button type='button' onClick={onSubmit} disabled={busy}>
            {busy ? t('common.buttons.saving') : t('common.buttons.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
