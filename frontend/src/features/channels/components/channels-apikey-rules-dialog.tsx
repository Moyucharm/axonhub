import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { APIKeyAutoDisableRule } from '../data/schema';
import { useUpdateChannel } from '../data/channels';
import { APIKeyAutoDisableRulesDialog } from './api-key-auto-disable-rules-dialog';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentRow: { id: string; name: string; policies?: { apiKeyAutoDisableRules?: APIKeyAutoDisableRule[]; stream?: { enabled: boolean } } | null } | null;
}

export function ChannelsAPIKeyRulesDialog({ open, onOpenChange, currentRow }: Props) {
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
        await updateChannel.mutateAsync({
          id: channelId,
          input: {
            policies: {
              stream: currentPolicies?.stream,
              apiKeyAutoDisableRules: rules.length > 0 ? rules : null,
            },
          },
        });
        toast.success(t('channels.messages.updateSuccess'));
        onOpenChange(false);
      } catch {
        // useUpdateChannel reports the request error.
      }
    },
    [currentPolicies?.stream, currentRow?.id, onOpenChange, t, updateChannel]
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