package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func (svc *CPAService) syncInstanceCredentials(ctx context.Context, instance *ent.CPAInstance) ([]*ent.CPACredential, error) {
	value, err, _ := svc.syncGroup.Do(fmt.Sprintf("instance:%d", instance.ID), func() (any, error) {
		return svc.syncInstanceCredentialsOnce(ctx, instance)
	})
	if err != nil {
		return nil, err
	}
	credentials, ok := value.([]*ent.CPACredential)
	if !ok {
		return nil, fmt.Errorf("unexpected synchronized CPA credential result")
	}
	return credentials, nil
}

func (svc *CPAService) syncInstanceCredentialsOnce(ctx context.Context, instance *ent.CPAInstance) ([]*ent.CPACredential, error) {
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()

	now := svc.now()
	authFiles, buildInfo, err := client.ListCredentials(ctx)
	if err != nil {
		svc.recordInstanceSyncError(ctx, instance.ID, now, err)
		return nil, err
	}

	err = svc.RunInTransaction(ctx, func(txCtx context.Context) error {
		if err := svc.syncCredentialSnapshot(txCtx, instance, authFiles.Files, now); err != nil {
			return err
		}
		return svc.entFromContext(txCtx).CPAInstance.UpdateOneID(instance.ID).
			SetServerVersion(buildInfo.Version).
			SetServerCommit(buildInfo.Commit).
			SetServerBuildDate(buildInfo.BuildDate).
			SetLastSyncAttemptAt(now).
			SetLastSyncSuccessAt(now).
			ClearLastError().
			ClearLastErrorAt().
			Exec(txCtx)
	})
	if err != nil {
		svc.recordInstanceSyncError(ctx, instance.ID, now, err)
		return nil, err
	}
	credentials, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.CpaInstanceIDEQ(instance.ID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load synchronized CPA credentials: %w", err)
	}
	return credentials, nil
}

func (svc *CPAService) syncCredentialSnapshot(ctx context.Context, instance *ent.CPAInstance, files []cpaclient.AuthFile, now time.Time) error {
	client := svc.entFromContext(ctx)
	existing, err := client.CPACredential.Query().
		Where(cpacredential.CpaInstanceIDEQ(instance.ID)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("load existing CPA credentials: %w", err)
	}
	byExternalKey := make(map[string]*ent.CPACredential, len(existing))
	byProviderName := make(map[string]*ent.CPACredential, len(existing))
	for _, credential := range existing {
		byExternalKey[credential.ExternalKey] = credential
		byProviderName[credential.Provider+":"+credential.RemoteName] = credential
	}

	normalizedByKey := make(map[string]cpaclient.NormalizedCredential, len(files))
	for _, file := range files {
		if file.RuntimeOnly && strings.TrimSpace(file.AuthIndex) == "" {
			continue
		}
		normalized, normalizeErr := cpaclient.NormalizeAuthFile(file)
		if normalizeErr != nil {
			return normalizeErr
		}
		normalizedByKey[normalized.ExternalKey] = normalized
	}

	seenIDs := make(map[int]struct{}, len(normalizedByKey))
	for _, normalized := range normalizedByKey {
		current := byExternalKey[normalized.ExternalKey]
		if current == nil {
			current = byProviderName[normalized.Provider+":"+normalized.RemoteName]
		}
		if current == nil {
			quotaState := objects.CPAQuotaStatePending
			if !svc.supportsNormalizedQuota(normalized) {
				quotaState = objects.CPAQuotaStateUnsupported
			}
			created, createErr := client.CPACredential.Create().
				SetCpaInstanceID(instance.ID).
				SetExternalKey(normalized.ExternalKey).
				SetAuthIndex(normalized.AuthIndex).
				SetRemoteName(normalized.RemoteName).
				SetLabel(normalized.Label).
				SetDisplayName(normalized.DisplayName).
				SetProvider(normalized.Provider).
				SetEmail(normalized.Email).
				SetStatus(normalized.Status).
				SetStatusMessage(normalized.StatusMessage).
				SetDisabled(normalized.Disabled).
				SetUnavailable(normalized.Unavailable).
				SetRuntimeOnly(normalized.RuntimeOnly).
				SetPriority(normalized.Priority).
				SetPlanType(normalized.PlanType).
				SetQuotaContext(normalized.QuotaContext).
				SetQuotaState(string(quotaState)).
				Save(ctx)
			if createErr != nil {
				return fmt.Errorf("create CPA credential %q: %w", normalized.RemoteName, createErr)
			}
			seenIDs[created.ID] = struct{}{}
			continue
		}

		providerChanged := current.Provider != normalized.Provider
		quotaCapabilityChanged := providerChanged || current.QuotaContext.Paid != normalized.QuotaContext.Paid
		update := client.CPACredential.UpdateOneID(current.ID).
			SetExternalKey(normalized.ExternalKey).
			SetAuthIndex(normalized.AuthIndex).
			SetRemoteName(normalized.RemoteName).
			SetLabel(normalized.Label).
			SetDisplayName(normalized.DisplayName).
			SetProvider(normalized.Provider).
			SetEmail(normalized.Email).
			SetStatus(normalized.Status).
			SetStatusMessage(normalized.StatusMessage).
			SetDisabled(normalized.Disabled).
			SetUnavailable(normalized.Unavailable).
			SetRuntimeOnly(normalized.RuntimeOnly).
			SetPriority(normalized.Priority).
			SetQuotaContext(normalized.QuotaContext)
		if normalized.PlanType != "" || current.PlanType == "" || strings.EqualFold(current.PlanType, "oauth") {
			update.SetPlanType(normalized.PlanType)
		}
		if quotaCapabilityChanged {
			quotaState := objects.CPAQuotaStatePending
			if !svc.supportsNormalizedQuota(normalized) {
				quotaState = objects.CPAQuotaStateUnsupported
			}
			update.
				SetQuotaState(string(quotaState)).
				SetQuotaData(objects.CPAQuotaSnapshot{}).
				SetQuotaLastError("").
				ClearQuotaLastAttemptAt().
				ClearQuotaLastSuccessAt().
				ClearQuotaLastFailureAt()
		}
		if _, updateErr := update.Save(ctx); updateErr != nil {
			return fmt.Errorf("update CPA credential %q: %w", normalized.RemoteName, updateErr)
		}
		seenIDs[current.ID] = struct{}{}
	}

	missingIDs := make([]int, 0)
	for _, credential := range existing {
		if _, ok := seenIDs[credential.ID]; !ok {
			missingIDs = append(missingIDs, credential.ID)
		}
	}
	if len(missingIDs) > 0 {
		if _, err := client.CPACredential.Delete().Where(cpacredential.IDIn(missingIDs...)).Exec(ctx); err != nil {
			return fmt.Errorf("delete missing CPA credentials: %w", err)
		}
	}
	return nil
}

func (svc *CPAService) recordInstanceSyncError(ctx context.Context, instanceID int, now time.Time, syncErr error) {
	message := strings.TrimSpace(syncErr.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	if err := svc.entFromContext(ctx).CPAInstance.UpdateOneID(instanceID).
		SetLastSyncAttemptAt(now).
		SetLastErrorAt(now).
		SetLastError(message).
		Exec(ctx); err != nil {
		// The caller already receives the primary sync error. Avoid hiding it with persistence failure.
		return
	}
}
