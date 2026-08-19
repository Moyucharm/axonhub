import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { APIKeyAutoDisableRule, Channel } from '../data/schema';
import { useUpdateChannel } from '../data/channels';
import { APIKeyAutoDisableRulesDialog } from './api-key-auto-disable-rules-dialog';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentRow: Channel | null;
  onChannelChange?: (channel: Channel) => void;
}

export function ChannelsAPIKeyRulesDialog({ open, onOpenChange, currentRow, onChannelChange }: Props) {
  const { t } = useTranslation();
  const updateChannel = useUpdateChannel();
  const currentPolicies = currentRow?.policies ?? null;

  const handleSave = useCallback(
    async (rules: APIKeyAutoDisableRule[]) => {
      const channelId = currentRow?.id;
      if (!channelId) {
        onOpenChange(false);
        return;
      }

      try {
        const updatedChannel = await updateChannel.mutateAsync({
          id: channelId,
          input: {
            policies: {
              ...(currentPolicies ?? {}),
              apiKeyAutoDisableRules: rules.length > 0 ? rules : null,
            },
          },
        });
        onChannelChange?.(updatedChannel);
        toast.success(t('channels.messages.updateSuccess'));
        onOpenChange(false);
      } catch {
        // useUpdateChannel reports the request error.
      }
    },
    [currentPolicies, currentRow?.id, onChannelChange, onOpenChange, t, updateChannel]
  );

  const rules = currentPolicies?.apiKeyAutoDisableRules ?? [];

  return (
    <APIKeyAutoDisableRulesDialog
      open={open}
      onOpenChange={onOpenChange}
      rules={rules}
      onSave={handleSave}
      title={t('channels.dialogs.apiKeyRules.title')}
      description={t('channels.dialogs.apiKeyRules.description', { name: currentRow?.name ?? '' })}
    />
  );
}