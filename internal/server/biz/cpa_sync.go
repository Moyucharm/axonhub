package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/cpausageevent"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func (svc *CPAService) syncInstanceCredentials(ctx context.Context, instance *ent.CPAInstance) ([]*ent.CPACredential, error) {
	value, err, _ := svc.syncGroup.Do(fmt.Sprintf("instance:%d", instance.ID), func() (any, error) {
		return svc.syncInstanceCredentialsOnce(ctx, instance)
	})
	return synchronizedCPACredentials(value, err)
}

func (svc *CPAService) syncInstanceCredentialsWithClient(ctx context.Context, instance *ent.CPAInstance, client cpaclient.ManagementClient) ([]*ent.CPACredential, error) {
	value, err, _ := svc.syncGroup.Do(fmt.Sprintf("instance:%d", instance.ID), func() (any, error) {
		return svc.syncInstanceCredentialsOnceWithClient(ctx, instance, client)
	})
	return synchronizedCPACredentials(value, err)
}

func synchronizedCPACredentials(value any, err error) ([]*ent.CPACredential, error) {
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
	return withCPAConnection(client, func(client cpaclient.ManagementClient) ([]*ent.CPACredential, error) {
		return svc.syncInstanceCredentialsOnceWithClient(ctx, instance, client)
	})
}

func (svc *CPAService) syncInstanceCredentialsOnceWithClient(ctx context.Context, instance *ent.CPAInstance, client cpaclient.ManagementClient) ([]*ent.CPACredential, error) {
	now := svc.now()
	authFiles, buildInfo, err := client.ListCredentials(ctx)
	if err != nil {
		svc.recordInstanceSyncError(ctx, instance.ID, now, err)
		return nil, err
	}

	err = svc.withCPAInstanceWriteRetry(ctx, instance.ID, func() error {
		return svc.RunInTransaction(ctx, func(txCtx context.Context) error {
			if err := svc.repository.syncCredentialSnapshot(txCtx, instance, authFiles.Files, now); err != nil {
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
	return svc.repository.syncCredentialSnapshot(ctx, instance, files, now)
}

func (repository *cpaRepository) syncCredentialSnapshot(ctx context.Context, instance *ent.CPAInstance, files []cpaclient.AuthFile, now time.Time) error {
	client := repository.entFromContext(ctx)
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
	targetAuthIndexes := make(map[string]string, len(files))
	for _, file := range files {
		if file.RuntimeOnly && strings.TrimSpace(file.AuthIndex) == "" {
			continue
		}
		normalized, normalizeErr := cpaclient.NormalizeAuthFile(file)
		if normalizeErr != nil {
			return normalizeErr
		}
		newAuthIndex := strings.TrimSpace(normalized.AuthIndex)
		if newAuthIndex != "" {
			if owner, exists := targetAuthIndexes[newAuthIndex]; exists {
				return fmt.Errorf("duplicate CPA auth index %q for %q and %q", newAuthIndex, owner, normalized.RemoteName)
			}
			targetAuthIndexes[newAuthIndex] = normalized.RemoteName
		}
		normalizedByKey[normalized.ExternalKey] = normalized
	}

	currentByKey := make(map[string]*ent.CPACredential, len(normalizedByKey))
	remaps := make([]authIndexRemap, 0)
	for key, normalized := range normalizedByKey {
		current := byProviderName[normalized.Provider+":"+normalized.RemoteName]
		if current == nil {
			current = byExternalKey[normalized.ExternalKey]
		}
		currentByKey[key] = current
		newAuthIndex := strings.TrimSpace(normalized.AuthIndex)
		if current == nil {
			continue
		}
		oldAuthIndex := strings.TrimSpace(current.AuthIndex)
		if oldAuthIndex != "" && newAuthIndex != "" && oldAuthIndex != newAuthIndex {
			remaps = append(remaps, authIndexRemap{
				credentialID: current.ID,
				remoteName:   normalized.RemoteName,
				oldIndex:     oldAuthIndex,
				newIndex:     newAuthIndex,
			})
		}
	}
	if err := applyAuthIndexRemaps(ctx, client, instance.ID, remaps, targetAuthIndexes); err != nil {
		return err
	}

	seenIDs := make(map[int]struct{}, len(normalizedByKey))
	for key, normalized := range normalizedByKey {
		current := currentByKey[key]
		if current == nil {
			quotaState := objects.CPAQuotaStatePending
			if !repository.supportsNormalizedQuota(normalized) {
				quotaState = objects.CPAQuotaStateUnsupported
			}
			projection := projectCPACredential(
				normalized.DisplayName,
				normalized.Status,
				normalized.Disabled,
				normalized.Unavailable,
				quotaState,
				objects.CPAQuotaSnapshot{},
				now,
			)
			create := client.CPACredential.Create().
				SetCpaInstanceID(instance.ID).
				SetExternalKey(normalized.ExternalKey).
				SetAuthIndex(normalized.AuthIndex).
				SetRemoteName(normalized.RemoteName).
				SetLabel(normalized.Label).
				SetDisplayName(normalized.DisplayName).
				SetDisplayNameSortKey(projection.displayNameSortKey).
				SetDisplayNameSortLength(projection.displayNameSortLength).
				SetProvider(normalized.Provider).
				SetEmail(normalized.Email).
				SetStatus(normalized.Status).
				SetStatusMessage(sanitizeCPAErrorMessage(normalized.StatusMessage)).
				SetDisabled(normalized.Disabled).
				SetUnavailable(normalized.Unavailable).
				SetRuntimeOnly(normalized.RuntimeOnly).
				SetPriority(normalized.Priority).
				SetPlanType(normalized.PlanType).
				SetQuotaContext(normalized.QuotaContext).
				SetQuotaState(string(quotaState)).
				SetHealthState(string(projection.healthState)).
				SetQuotaCooling(projection.quotaCooling).
				SetProjectionVersion(currentCPAProjectionVersion)
			if projection.quotaCooldownUntil != nil {
				create.SetQuotaCooldownUntil(*projection.quotaCooldownUntil)
			}
			created, createErr := create.Save(ctx)
			if createErr != nil {
				return fmt.Errorf("create CPA credential %q: %w", normalized.RemoteName, createErr)
			}
			seenIDs[created.ID] = struct{}{}
			continue
		}

		providerChanged := current.Provider != normalized.Provider
		quotaCapabilityChanged := providerChanged || current.QuotaContext.Paid != normalized.QuotaContext.Paid
		quotaState := objects.CPAQuotaState(current.QuotaState)
		quotaData := current.QuotaData
		if quotaCapabilityChanged {
			quotaState = objects.CPAQuotaStatePending
			if !repository.supportsNormalizedQuota(normalized) {
				quotaState = objects.CPAQuotaStateUnsupported
			}
			quotaData = objects.CPAQuotaSnapshot{}
		}
		projection := projectCPACredential(
			normalized.DisplayName,
			normalized.Status,
			normalized.Disabled,
			normalized.Unavailable,
			quotaState,
			quotaData,
			now,
		)
		update := client.CPACredential.UpdateOneID(current.ID).
			SetExternalKey(normalized.ExternalKey).
			SetAuthIndex(normalized.AuthIndex).
			SetRemoteName(normalized.RemoteName).
			SetLabel(normalized.Label).
			SetDisplayName(normalized.DisplayName).
			SetDisplayNameSortKey(projection.displayNameSortKey).
			SetDisplayNameSortLength(projection.displayNameSortLength).
			SetProvider(normalized.Provider).
			SetEmail(normalized.Email).
			SetStatus(normalized.Status).
			SetStatusMessage(sanitizeCPAErrorMessage(normalized.StatusMessage)).
			SetDisabled(normalized.Disabled).
			SetUnavailable(normalized.Unavailable).
			SetRuntimeOnly(normalized.RuntimeOnly).
			SetPriority(normalized.Priority).
			SetQuotaContext(normalized.QuotaContext).
			SetHealthState(string(projection.healthState)).
			SetQuotaCooling(projection.quotaCooling).
			SetProjectionVersion(currentCPAProjectionVersion)
		if projection.quotaCooldownUntil == nil {
			update.ClearQuotaCooldownUntil()
		} else {
			update.SetQuotaCooldownUntil(*projection.quotaCooldownUntil)
		}
		if normalized.PlanType != "" || current.PlanType == "" || strings.EqualFold(current.PlanType, "oauth") {
			update.SetPlanType(normalized.PlanType)
		}
		if quotaCapabilityChanged {
			update.
				SetQuotaState(string(quotaState)).
				SetQuotaData(quotaData).
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
	if len(remaps) > 0 {
		repository.invalidateUsageCredentialCache(instance.ID)
	}
	return nil
}

type authIndexRemap struct {
	credentialID int
	remoteName   string
	oldIndex     string
	newIndex     string
}

func applyAuthIndexRemaps(ctx context.Context, client *ent.Client, instanceID int, remaps []authIndexRemap, targets map[string]string) error {
	if len(remaps) == 0 {
		return nil
	}
	temporary := make(map[int]string, len(remaps))
	reserved := make(map[string]struct{}, len(remaps)*3)
	for target := range targets {
		reserved[target] = struct{}{}
	}
	for _, remap := range remaps {
		reserved[remap.oldIndex] = struct{}{}
	}
	for _, remap := range remaps {
		candidate := fmt.Sprintf("__axonhub_auth_remap_%d_%d__", instanceID, remap.credentialID)
		if _, exists := reserved[candidate]; exists {
			return fmt.Errorf("temporary CPA auth index conflicts for %q", remap.remoteName)
		}
		temporary[remap.credentialID] = candidate
		reserved[candidate] = struct{}{}
	}
	for _, remap := range remaps {
		stagedIndex := temporary[remap.credentialID]
		if err := client.CPACredential.UpdateOneID(remap.credentialID).
			SetExternalKey(stagedIndex).
			SetAuthIndex(stagedIndex).
			Exec(ctx); err != nil {
			return fmt.Errorf("stage CPA credential index for %q: %w", remap.remoteName, err)
		}
		if _, err := client.CpaUsageEvent.Update().
			Where(
				cpausageevent.CpaInstanceIDEQ(instanceID),
				cpausageevent.AuthIndexEQ(remap.oldIndex),
			).
			SetAuthIndex(stagedIndex).
			Save(ctx); err != nil {
			return fmt.Errorf("stage CPA usage events for %q: %w", remap.remoteName, err)
		}
	}
	for _, remap := range remaps {
		if _, err := client.CpaUsageEvent.Update().
			Where(
				cpausageevent.CpaInstanceIDEQ(instanceID),
				cpausageevent.AuthIndexEQ(temporary[remap.credentialID]),
			).
			SetAuthIndex(remap.newIndex).
			Save(ctx); err != nil {
			return fmt.Errorf("finalize CPA usage events for %q: %w", remap.remoteName, err)
		}
	}
	return nil
}

func (svc *CPAService) recordInstanceSyncError(ctx context.Context, instanceID int, now time.Time, syncErr error) {
	message := sanitizeCPAErrorMessage(syncErr.Error())
	if err := svc.withCPAInstanceWriteRetry(ctx, instanceID, func() error {
		return svc.entFromContext(ctx).CPAInstance.UpdateOneID(instanceID).
			SetLastSyncAttemptAt(now).
			SetLastErrorAt(now).
			SetLastError(message).
			Exec(ctx)
	}); err != nil {
		// The caller already receives the primary sync error. Avoid hiding it with persistence failure.
		return
	}
}
