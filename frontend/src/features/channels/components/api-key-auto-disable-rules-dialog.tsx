import { useCallback, useEffect, useState } from 'react';
import { z } from 'zod';
import { useFieldArray, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { IconPlus, IconTrash } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { apiKeyAutoDisableRuleFormSchema, type APIKeyAutoDisableRule } from '../data/schema';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Initial rules to edit. */
  rules: APIKeyAutoDisableRule[];
  /** Called when the user saves. Receives the edited rules. */
  onSave: (rules: APIKeyAutoDisableRule[]) => Promise<void>;
  title: string;
  description: string;
}

const formSchema = z.object({
  rules: z.array(apiKeyAutoDisableRuleFormSchema),
});

type FormValues = z.infer<typeof formSchema>;

const PRESET_DISABLE_DURATIONS = [5, 15, 30, 60, 120, 360, 720, 1440];

export function APIKeyAutoDisableRulesDialog({ open, onOpenChange, rules: initialRules, onSave, title, description }: Props) {
  const { t } = useTranslation();
  const [customDurationModes, setCustomDurationModes] = useState<Record<string, true>>({});
  const [anyErrorBackups, setAnyErrorBackups] = useState<Record<string, { statusCodes: number[]; keywordPatterns: string[] }>>({});

  const toFormRules = useCallback(
    () =>
      initialRules.map((rule) => ({
        statusCodes: rule.statusCodes ?? [],
        keywordPatterns: rule.keywordPatterns ?? [],
        times: rule.times,
        action: rule.action,
        disableDurationMinutes: rule.action === 'temporary_disable' ? (rule.disableDurationMinutes ?? 30) : null,
      })),
    [initialRules]
  );

  const form = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: { rules: toFormRules() },
  });

  const { fields, append, remove } = useFieldArray({
    control: form.control,
    name: 'rules',
  });

  useEffect(() => {
    if (open) {
      setCustomDurationModes({});
      setAnyErrorBackups({});
      form.reset({ rules: toFormRules() });
    }
  }, [form, open, toFormRules]);

  const setCustomDurationMode = useCallback((fieldID: string, enabled: boolean) => {
    setCustomDurationModes((previous) => {
      if (enabled) return previous[fieldID] ? previous : { ...previous, [fieldID]: true };
      if (!previous[fieldID]) return previous;
      const next = { ...previous };
      delete next[fieldID];
      return next;
    });
  }, []);

  /** Toggles the any-error mode for a rule, keeping a backup of user input so it can be restored. */
  const setAnyErrorMode = useCallback(
    (fieldID: string, index: number, enabled: boolean) => {
      if (enabled) {
        // 进入任意错误模式：先备份用户输入，再清空状态码/关键词
        const statusCodes = form.getValues(`rules.${index}.statusCodes`) ?? [];
        const keywordPatterns = form.getValues(`rules.${index}.keywordPatterns`) ?? [];
        setAnyErrorBackups((previous) => ({ ...previous, [fieldID]: { statusCodes, keywordPatterns } }));
        form.setValue(`rules.${index}.statusCodes`, []);
        form.setValue(`rules.${index}.keywordPatterns`, []);
      } else {
        // 退出任意错误模式：恢复备份，无有效备份则回填默认状态码 500
        const backup = anyErrorBackups[fieldID];
        if (backup && (backup.statusCodes.length > 0 || backup.keywordPatterns.length > 0)) {
          form.setValue(`rules.${index}.statusCodes`, backup.statusCodes);
          form.setValue(`rules.${index}.keywordPatterns`, backup.keywordPatterns);
        } else {
          form.setValue(`rules.${index}.statusCodes`, [500]);
          form.setValue(`rules.${index}.keywordPatterns`, []);
        }
        setAnyErrorBackups((previous) => {
          if (!previous[fieldID]) return previous;
          const next = { ...previous };
          delete next[fieldID];
          return next;
        });
      }
    },
    [anyErrorBackups, form]
  );

  const onSubmit = useCallback(
    async (values: FormValues) => {
      const rules = values.rules.map((rule) => ({
        statusCodes: rule.statusCodes?.filter((code): code is number => code != null) ?? [],
        keywordPatterns: rule.keywordPatterns?.map((pattern) => pattern.trim()).filter(Boolean) ?? [],
        times: rule.times,
        action: rule.action,
        disableDurationMinutes: rule.action === 'temporary_disable' ? (rule.disableDurationMinutes ?? null) : null,
      }));

      await onSave(rules);
    },
    [onSave]
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-3xl'>
        <DialogHeader className='text-left'>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form className='space-y-4' onSubmit={form.handleSubmit(onSubmit)}>
            {fields.length === 0 && (
              <div className='text-muted-foreground rounded-md border border-dashed p-6 text-center text-sm'>
                {t('channels.dialogs.apiKeyRules.empty')}
              </div>
            )}

            {fields.map((field, index) => {
              const duration = form.watch(`rules.${index}.disableDurationMinutes`);
              const customDuration = !!customDurationModes[field.id] || (duration != null && !PRESET_DISABLE_DURATIONS.includes(duration));
              const durationValue = customDuration ? 'custom' : String(duration ?? 30);
              const isAnyError =
                (form.watch(`rules.${index}.statusCodes`)?.length ?? 0) === 0 &&
                !(form.watch(`rules.${index}.keywordPatterns`)?.some((pattern) => pattern.trim() !== '') ?? false);

              return (
                <div key={field.id} className='space-y-3 rounded-md border p-4'>
                  <div className='flex items-center justify-between'>
                    <div className='flex items-center gap-3'>
                      <Badge variant='outline'>{t('channels.dialogs.apiKeyRules.ruleLabel', { index: index + 1 })}</Badge>
                      <label className='flex cursor-pointer items-center gap-1.5 text-sm'>
                        <Checkbox
                          checked={isAnyError}
                          onCheckedChange={(checked) => {
                            if (checked === true) {
                              setAnyErrorMode(field.id, index, true);
                            } else if (checked === false) {
                              setAnyErrorMode(field.id, index, false);
                            }
                          }}
                        />
                        {t('channels.dialogs.apiKeyRules.anyError')}
                      </label>
                    </div>
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      aria-label={t('common.buttons.delete')}
                      onClick={() => {
                        remove(index);
                        setCustomDurationMode(field.id, false);
                        setAnyErrorBackups((previous) => {
                          if (!previous[field.id]) return previous;
                          const next = { ...previous };
                          delete next[field.id];
                          return next;
                        });
                      }}
                    >
                      <IconTrash className='h-4 w-4 text-red-500' />
                    </Button>
                  </div>

                  <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                    <FormField
                      control={form.control}
                      name={`rules.${index}.statusCodes`}
                      render={({ field: input }) => (
                        <FormItem>
                          <FormLabel>{t('channels.dialogs.apiKeyRules.fields.statusCodes')}</FormLabel>
                          <FormControl>
                            <Input
                              key={`status-${field.id}-${isAnyError}`}
                              defaultValue={input.value?.join(', ') ?? ''}
                              disabled={isAnyError}
                              placeholder={t('channels.dialogs.apiKeyRules.fields.statusCodesPlaceholder')}
                              onBlur={(event) => {
                                const codes = event.target.value
                                  .split(/[,\s]+/)
                                  .map((value) => Number.parseInt(value, 10))
                                  .filter((value) => Number.isInteger(value) && value >= 100 && value <= 599);
                                input.onChange(codes);
                              }}
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name={`rules.${index}.times`}
                      render={({ field: input }) => (
                        <FormItem>
                          <FormLabel>{t('channels.dialogs.apiKeyRules.fields.times')}</FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={1}
                              value={input.value ?? ''}
                              onChange={(event) => {
                                const raw = event.target.value;
                                input.onChange(raw === '' ? undefined : Number.parseInt(raw, 10));
                              }}
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>

                  <FormField
                    control={form.control}
                    name={`rules.${index}.keywordPatterns`}
                    render={({ field: input }) => (
                      <FormItem>
                        <FormLabel>{t('channels.dialogs.apiKeyRules.fields.keywordPatterns')}</FormLabel>
                        <FormControl>
                          <Textarea
                            key={`patterns-${field.id}-${isAnyError}`}
                            className='min-h-24 font-mono text-sm'
                            disabled={isAnyError}
                            defaultValue={input.value?.join('\n') ?? ''}
                            placeholder={t('channels.dialogs.apiKeyRules.fields.keywordPatternsPlaceholder')}
                            onBlur={(event) =>
                              input.onChange(
                                event.target.value
                                  .split(/\r?\n/)
                                  .map((value) => value.trim())
                                  .filter(Boolean)
                              )
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                    <FormField
                      control={form.control}
                      name={`rules.${index}.action`}
                      render={({ field: input }) => (
                        <FormItem>
                          <FormLabel>{t('channels.dialogs.apiKeyRules.fields.action')}</FormLabel>
                          <Select
                            value={input.value}
                            onValueChange={(value) => {
                              input.onChange(value);
                              if (value === 'permanent_disable_delete') {
                                setCustomDurationMode(field.id, false);
                                form.setValue(`rules.${index}.disableDurationMinutes`, null);
                              } else if (!form.getValues(`rules.${index}.disableDurationMinutes`)) {
                                form.setValue(`rules.${index}.disableDurationMinutes`, 30);
                              }
                            }}
                          >
                            <FormControl>
                              <SelectTrigger>
                                <SelectValue />
                              </SelectTrigger>
                            </FormControl>
                            <SelectContent>
                              <SelectItem value='temporary_disable'>
                                {t('channels.dialogs.apiKeyRules.actions.temporaryDisable')}
                              </SelectItem>
                              <SelectItem value='permanent_disable_delete'>
                                {t('channels.dialogs.apiKeyRules.actions.permanentDelete')}
                              </SelectItem>
                            </SelectContent>
                          </Select>
                        </FormItem>
                      )}
                    />

                    {form.watch(`rules.${index}.action`) === 'temporary_disable' && (
                      <FormField
                        control={form.control}
                        name={`rules.${index}.disableDurationMinutes`}
                        render={({ field: input }) => (
                          <FormItem>
                            <FormLabel>{t('channels.dialogs.apiKeyRules.fields.disableDuration')}</FormLabel>
                            <div className='flex gap-2'>
                              <Select
                                value={durationValue}
                                onValueChange={(value) => {
                                  if (value === 'custom') {
                                    setCustomDurationMode(field.id, true);
                                    if (!input.value) input.onChange(30);
                                  } else {
                                    setCustomDurationMode(field.id, false);
                                    input.onChange(Number.parseInt(value, 10));
                                  }
                                }}
                              >
                                <SelectTrigger className={customDuration ? 'w-1/2' : undefined}>
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {PRESET_DISABLE_DURATIONS.map((minutes) => (
                                    <SelectItem key={minutes} value={String(minutes)}>
                                      {t(`channels.dialogs.apiKeyRules.durations.${minutes}`)}
                                    </SelectItem>
                                  ))}
                                  <SelectItem value='custom'>{t('channels.dialogs.apiKeyRules.fields.disableDurationCustom')}</SelectItem>
                                </SelectContent>
                              </Select>
                              {customDuration && (
                                <Input
                                  type='number'
                                  min={1}
                                  className='w-1/2'
                                  value={input.value ?? ''}
                                  placeholder={t('channels.dialogs.apiKeyRules.fields.disableDurationCustomPlaceholder')}
                                  onChange={(event) => {
                                    const value = Number.parseInt(event.target.value, 10);
                                    input.onChange(Number.isInteger(value) && value > 0 ? value : null);
                                  }}
                                />
                              )}
                            </div>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    )}
                  </div>
                </div>
              );
            })}

            <Button
              type='button'
              variant='outline'
              className='w-full'
              onClick={() =>
                append({
                  statusCodes: [],
                  keywordPatterns: [],
                  times: 3,
                  action: 'temporary_disable',
                  disableDurationMinutes: 30,
                })
              }
            >
              <IconPlus className='mr-2 h-4 w-4' />
              {t('channels.dialogs.apiKeyRules.addRule')}
            </Button>

            <DialogFooter>
              <Button type='button' variant='outline' onClick={() => onOpenChange(false)}>
                {t('common.buttons.cancel')}
              </Button>
              <Button type='submit' disabled={form.formState.isSubmitting}>
                {form.formState.isSubmitting ? t('common.buttons.saving') : t('common.buttons.save')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}