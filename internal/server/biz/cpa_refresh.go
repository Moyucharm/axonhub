package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

// CPARefreshResult summarizes one manual or scheduled refresh.
type CPARefreshResult struct {
	Requested int
	Succeeded int
	Failed    int
	Skipped   int
}

func (svc *CPAService) RefreshInstance(ctx context.Context, instanceID int, provider *string) (*CPARefreshResult, error) {
	instance, err := svc.entFromContext(ctx).CPAInstance.Get(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get CPA instance for refresh: %w", err)
	}
	if !instance.Enabled {
		return nil, fmt.Errorf("CPA instance is disabled")
	}
	credentials, err := svc.syncInstanceCredentials(ctx, instance)
	if err != nil {
		return nil, err
	}
	filtered := make([]*ent.CPACredential, 0, len(credentials))
	skipped := 0
	for _, credential := range credentials {
		// Manual refresh covers every credential, disabled ones included:
		// quota collection works through the api-call proxy regardless.
		if provider != nil && strings.TrimSpace(*provider) != "" && credential.Provider != strings.TrimSpace(*provider) {
			continue
		}
		if !svc.supportsStoredQuota(credential) {
			skipped++
		}
		filtered = append(filtered, credential)
	}
	refreshBatch := svc.beginRefreshProgress(instanceID, len(filtered)-skipped, skipped)
	defer svc.finishRefreshProgress(instanceID, refreshBatch)
	result, refreshErr := svc.refreshCredentialBatch(ctx, instance, filtered, func(failed bool) {
		svc.recordRefreshResult(instanceID, refreshBatch, failed)
	})
	svc.scheduleNextRefresh(ctx, instance, svc.now().Add(time.Duration(instance.RefreshIntervalMinutes)*time.Minute))
	return result, refreshErr
}

func (svc *CPAService) RefreshCredential(ctx context.Context, credentialID int) (*CPACredentialView, error) {
	credential, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.IDEQ(credentialID)).
		WithCpaInstance().
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("get CPA credential for refresh: %w", err)
	}
	instance := credential.Edges.CpaInstance
	if instance == nil || !instance.Enabled {
		return nil, fmt.Errorf("CPA instance is disabled")
	}
	if !svc.supportsStoredQuota(credential) {
		return nil, fmt.Errorf("CPA credential quota is unsupported")
	}

	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	if err := svc.refreshOneCredential(ctx, client, credential); err != nil {
		return nil, err
	}
	svc.scheduleNextRefresh(ctx, instance, svc.now().Add(time.Duration(instance.RefreshIntervalMinutes)*time.Minute))
	updated, err := svc.entFromContext(ctx).CPACredential.Get(ctx, credential.ID)
	if err != nil {
		return nil, fmt.Errorf("reload CPA credential after refresh: %w", err)
	}
	return buildCPACredentialView(instance, updated, svc.now()), nil
}

// ToggleCredential enables or disables one CPA credential through the remote
// management API and returns the refreshed view. The remote CPA server stays
// the single source of truth for the disabled flag: the local row converges
// via the credential sync that follows the successful patch.
func (svc *CPAService) ToggleCredential(ctx context.Context, credentialID int, disabled bool) (*CPACredentialView, error) {
	credential, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.IDEQ(credentialID)).
		WithCpaInstance().
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("get CPA credential for toggle: %w", err)
	}
	instance := credential.Edges.CpaInstance
	if instance == nil || !instance.Enabled {
		return nil, fmt.Errorf("CPA instance is disabled")
	}
	if credential.Disabled == disabled {
		return buildCPACredentialView(instance, credential, svc.now()), nil
	}

	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	if err := client.PatchAuthFileStatus(ctx, credential.RemoteName, credential.AuthIndex, disabled); err != nil {
		return nil, fmt.Errorf("toggle CPA credential on remote: %w", err)
	}
	if _, err := svc.syncInstanceCredentials(ctx, instance); err != nil {
		log.Warn(ctx, "CPA remote credential toggle succeeded but local sync failed",
			log.Int("cpa_instance_id", instance.ID),
			log.Int("credential_id", credential.ID),
			log.Any("disabled", disabled),
			log.Cause(err),
		)
		return nil, fmt.Errorf("remote CPA credential toggle succeeded but local sync failed: %w", err)
	}
	updated, err := svc.entFromContext(ctx).CPACredential.Get(ctx, credential.ID)
	if err != nil {
		return nil, fmt.Errorf("reload CPA credential after toggle: %w", err)
	}
	return buildCPACredentialView(instance, updated, svc.now()), nil
}

func (svc *CPAService) refreshCredentialBatch(ctx context.Context, instance *ent.CPAInstance, credentials []*ent.CPACredential, onResult func(failed bool)) (*CPARefreshResult, error) {
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()

	result := &CPARefreshResult{}
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxCPAInstanceConcurrency)
	resultCh := make(chan error, len(credentials))
	for _, credential := range credentials {
		credential := credential
		if !svc.supportsStoredQuota(credential) {
			result.Skipped++
			continue
		}
		result.Requested++
		group.Go(func() error {
			runCPARefreshWorker(
				groupCtx,
				instance.ID,
				credential.ID,
				resultCh,
				onResult,
				func() error { return svc.refreshOneCredential(groupCtx, client, credential) },
			)
			return nil
		})
	}
	_ = group.Wait()
	close(resultCh)
	for refreshErr := range resultCh {
		if refreshErr != nil {
			result.Failed++
		} else {
			result.Succeeded++
		}
	}
	return result, nil
}

func runCPARefreshWorker(
	ctx context.Context,
	instanceID int,
	credentialID int,
	resultCh chan<- error,
	onResult func(failed bool),
	refresh func() error,
) {
	var refreshErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			refreshErr = fmt.Errorf("CPA credential refresh panicked: %v", recovered)
			log.Error(ctx, "CPA credential refresh worker panicked",
				log.Int("cpa_instance_id", instanceID),
				log.Int("credential_id", credentialID),
				log.Any("panic", recovered),
			)
		}
		resultCh <- refreshErr
		if onResult != nil {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						log.Error(ctx, "CPA credential refresh progress callback panicked",
							log.Int("cpa_instance_id", instanceID),
							log.Int("credential_id", credentialID),
							log.Any("panic", recovered),
						)
					}
				}()
				onResult(refreshErr != nil)
			}()
		}
	}()
	refreshErr = refresh()
}

func (svc *CPAService) refreshOneCredential(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential) error {
	key := fmt.Sprintf("credential:%d", credential.ID)
	_, err, _ := svc.refreshGroup.Do(key, func() (any, error) {
		instanceLimiter := svc.instanceQuotaLimiter(credential.CpaInstanceID)
		if err := instanceLimiter.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		defer instanceLimiter.Release(1)
		if err := svc.globalQuota.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		defer svc.globalQuota.Release(1)

		now := svc.now()
		result, fetchErr := svc.quotaRegistry.Fetch(ctx, client, cpaclient.CredentialInput{
			AuthIndex:   credential.AuthIndex,
			Provider:    credential.Provider,
			PlanType:    credential.PlanType,
			ProjectID:   credential.QuotaContext.ProjectID,
			AccountID:   credential.QuotaContext.CodexAccountID,
			AccountType: credential.QuotaContext.AccountType,
			Paid:        credential.QuotaContext.Paid,
		})
		if fetchErr != nil {
			message := sanitizeCPAErrorMessage(fetchErr.Error())
			updateErr := svc.withCPAInstanceWriteRetry(ctx, credential.CpaInstanceID, func() error {
				current, currentErr := svc.entFromContext(ctx).CPACredential.Get(ctx, credential.ID)
				if currentErr != nil {
					return fmt.Errorf("reload CPA credential before quota error: %w", currentErr)
				}
				projection := projectStoredCPACredentialWithQuota(
					current,
					objects.CPAQuotaStateError,
					current.QuotaData,
					now,
				)
				update := svc.entFromContext(ctx).CPACredential.UpdateOneID(current.ID).
					SetQuotaState(string(objects.CPAQuotaStateError)).
					SetQuotaLastAttemptAt(now).
					SetQuotaLastFailureAt(now).
					SetQuotaLastError(message).
					SetDisplayNameSortKey(projection.displayNameSortKey).
					SetDisplayNameSortLength(projection.displayNameSortLength).
					SetHealthState(string(projection.healthState)).
					SetQuotaCooling(projection.quotaCooling).
					SetProjectionVersion(currentCPAProjectionVersion)
				if projection.quotaCooldownUntil == nil {
					update.ClearQuotaCooldownUntil()
				} else {
					update.SetQuotaCooldownUntil(*projection.quotaCooldownUntil)
				}
				return update.Exec(ctx)
			})
			if updateErr != nil {
				return nil, fmt.Errorf("persist CPA quota error: %w", updateErr)
			}
			return nil, fetchErr
		}

		snapshot := result.Snapshot
		if result.State == objects.CPAQuotaStateSuccess && strings.EqualFold(strings.TrimSpace(credential.Provider), "codex") {
			svc.applyQuotaEstimate(ctx, credential, &snapshot)
		}
		persistedSnapshot := snapshot
		if result.State == objects.CPAQuotaStateUnsupported || result.State == objects.CPAQuotaStateInsufficientData {
			persistedSnapshot = objects.CPAQuotaSnapshot{}
		}
		if err := svc.withCPAInstanceWriteRetry(ctx, credential.CpaInstanceID, func() error {
			current, currentErr := svc.entFromContext(ctx).CPACredential.Get(ctx, credential.ID)
			if currentErr != nil {
				return fmt.Errorf("reload CPA credential before quota result: %w", currentErr)
			}
			projection := projectStoredCPACredentialWithQuota(current, result.State, persistedSnapshot, now)
			update := svc.entFromContext(ctx).CPACredential.UpdateOneID(current.ID).
				SetQuotaState(string(result.State)).
				SetQuotaLastAttemptAt(now).
				SetQuotaLastError("").
				SetDisplayNameSortKey(projection.displayNameSortKey).
				SetDisplayNameSortLength(projection.displayNameSortLength).
				SetHealthState(string(projection.healthState)).
				SetQuotaCooling(projection.quotaCooling).
				SetProjectionVersion(currentCPAProjectionVersion)
			if projection.quotaCooldownUntil == nil {
				update.ClearQuotaCooldownUntil()
			} else {
				update.SetQuotaCooldownUntil(*projection.quotaCooldownUntil)
			}
			if result.PlanType != "" {
				update.SetPlanType(result.PlanType)
			}
			switch result.State {
			case objects.CPAQuotaStateSuccess:
				update.SetQuotaData(persistedSnapshot).SetQuotaLastSuccessAt(now)
			case objects.CPAQuotaStateUnsupported, objects.CPAQuotaStateInsufficientData:
				update.SetQuotaData(persistedSnapshot)
			}
			return update.Exec(ctx)
		}); err != nil {
			return nil, fmt.Errorf("persist CPA quota result: %w", err)
		}
		return nil, nil
	})
	return err
}

// applyQuotaEstimate computes and attaches an independent interval estimate to
// every weekly/monthly window. Monthly baselines advance only at quota refresh;
// weekly baselines are maintained from precise usage response headers.
func (svc *CPAService) applyQuotaEstimate(ctx context.Context, credential *ent.CPACredential, snapshot *objects.CPAQuotaSnapshot) {
	collectorSessionID, latestEventID := svc.usageCollectorCheckpoint(credential.CpaInstanceID, credential.AuthIndex)
	for _, item := range estimateWindowItems(*snapshot) {
		if item.PeriodSeconds != nil && isMonthlyQuotaPeriod(*item.PeriodSeconds) {
			prepareCodexMonthlyInterval(
				item,
				previousQuotaItem(credential.QuotaData, item),
				collectorSessionID,
				latestEventID,
			)
		}
		estimate := svc.estimateCredentialQuotaForItem(ctx, credential.CpaInstanceID, credential.AuthIndex, item, credential.QuotaObserved)
		if estimate == nil {
			continue
		}
		limit := estimate.LimitUSD
		cost := estimate.CostUSD
		item.EstimatedLimitUSD = &limit
		item.EstimatedCostUSD = &cost
		item.EstimateSource = estimate.Source
	}
}

func (svc *CPAService) instanceQuotaLimiter(instanceID int) *semaphore.Weighted {
	svc.instanceQuotaMu.Lock()
	defer svc.instanceQuotaMu.Unlock()
	if svc.instanceQuota == nil {
		svc.instanceQuota = make(map[int]*semaphore.Weighted)
	}
	limiter := svc.instanceQuota[instanceID]
	if limiter == nil {
		limiter = semaphore.NewWeighted(maxCPAInstanceConcurrency)
		svc.instanceQuota[instanceID] = limiter
	}
	return limiter
}

func (svc *CPAService) scheduleNextRefresh(ctx context.Context, instance *ent.CPAInstance, next time.Time) {
	if !instance.Enabled || !instance.AutoRefreshEnabled {
		return
	}
	if err := svc.withCPAInstanceWriteRetry(ctx, instance.ID, func() error {
		return svc.entFromContext(ctx).CPAInstance.UpdateOneID(instance.ID).SetNextRefreshAt(next).Exec(ctx)
	}); err != nil {
		// A failed schedule write leaves the instance due, so the next dispatch
		// tick retries it. Log instead of swallowing to make the retry visible.
		log.Warn(ctx, "failed to schedule CPA next refresh",
			log.Int("cpa_instance_id", instance.ID),
			log.Cause(err),
		)
	}
}
