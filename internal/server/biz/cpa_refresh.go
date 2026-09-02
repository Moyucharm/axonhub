package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/looplj/axonhub/internal/ent"
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
	result, refreshErr := svc.refreshCredentialBatch(ctx, instance, filtered, func(outcome cpaQuotaExecutionOutcome) {
		svc.recordRefreshOutcome(instanceID, refreshBatch, outcome)
	})
	svc.scheduleNextRefresh(ctx, instance, svc.now().Add(time.Duration(instance.RefreshIntervalMinutes)*time.Minute))
	return result, refreshErr
}

func (svc *CPAService) RefreshCredential(ctx context.Context, credentialID int) (*CPACredentialView, error) {
	credential, instance, err := svc.repository.credentialWithInstance(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("get CPA credential for refresh: %w", err)
	}
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
	outcome := svc.refreshCredentialOutcome(ctx, client, credential)
	if outcome.err != nil {
		return nil, outcome.err
	}
	svc.scheduleNextRefresh(ctx, instance, svc.now().Add(time.Duration(instance.RefreshIntervalMinutes)*time.Minute))
	return buildCPACredentialView(instance, outcome.credential, svc.now()), nil
}

// ToggleCredential enables or disables one CPA credential through the remote
// management API and returns the refreshed view. The remote CPA server stays
// the single source of truth for the disabled flag: the local row converges
// via the credential sync that follows the successful patch.
func (svc *CPAService) ToggleCredential(ctx context.Context, credentialID int, disabled bool) (*CPACredentialView, error) {
	credential, instance, err := svc.repository.credentialWithInstance(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("get CPA credential for toggle: %w", err)
	}
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
	if _, err := svc.syncInstanceCredentialsWithClient(ctx, instance, client); err != nil {
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

func (svc *CPAService) refreshCredentialBatch(ctx context.Context, instance *ent.CPAInstance, credentials []*ent.CPACredential, onResult func(cpaQuotaExecutionOutcome)) (*CPARefreshResult, error) {
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	result, _, err := svc.refreshCredentialBatchOutcomesWithClient(ctx, instance, client, credentials, onResult)
	return result, err
}

func (svc *CPAService) refreshCredentialBatchOutcomesWithClient(
	ctx context.Context,
	instance *ent.CPAInstance,
	client cpaclient.ManagementClient,
	credentials []*ent.CPACredential,
	onResult func(cpaQuotaExecutionOutcome),
) (*CPARefreshResult, []cpaQuotaExecutionOutcome, error) {

	result := &CPARefreshResult{}
	outcomes := make([]cpaQuotaExecutionOutcome, 0, len(credentials))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxCPAInstanceConcurrency)
	resultCh := make(chan cpaQuotaExecutionOutcome, len(credentials))
	for _, credential := range credentials {
		credential := credential
		if !svc.supportsStoredQuota(credential) {
			result.Skipped++
			outcomes = append(outcomes, newCPAQuotaSkippedOutcome(credential, "quota unsupported"))
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
				func() cpaQuotaExecutionOutcome { return svc.refreshCredentialOutcome(groupCtx, client, credential) },
			)
			return nil
		})
	}
	_ = group.Wait()
	close(resultCh)
	for outcome := range resultCh {
		outcomes = append(outcomes, outcome)
		accumulateCPARefreshResult(result, outcome)
	}
	return result, outcomes, nil
}

func accumulateCPARefreshResult(result *CPARefreshResult, outcome cpaQuotaExecutionOutcome) {
	switch outcome.status {
	case cpaQuotaExecutionSuccess:
		result.Succeeded++
	case cpaQuotaExecutionFailure:
		result.Failed++
	case cpaQuotaExecutionSkipped:
		result.Skipped++
	default:
		result.Failed++
	}
}

func runCPARefreshWorker(
	ctx context.Context,
	instanceID int,
	credentialID int,
	resultCh chan<- cpaQuotaExecutionOutcome,
	onResult func(cpaQuotaExecutionOutcome),
	refresh func() cpaQuotaExecutionOutcome,
) {
	outcome := cpaQuotaExecutionOutcome{instanceID: instanceID, credentialID: credentialID}
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = newCPAQuotaFailureOutcome(
				instanceID,
				credentialID,
				time.Now().UTC(),
				fmt.Errorf("CPA credential refresh panicked: %v", recovered),
			)
			log.Error(ctx, "CPA credential refresh worker panicked",
				log.Int("cpa_instance_id", instanceID),
				log.Int("credential_id", credentialID),
				log.Any("panic", recovered),
			)
		}
		resultCh <- outcome
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
				onResult(outcome)
			}()
		}
	}()
	outcome = refresh()
}

func (svc *CPAService) refreshCredentialOutcome(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential) (outcome cpaQuotaExecutionOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = newCPAQuotaFailureOutcome(
				credential.CpaInstanceID,
				credential.ID,
				svc.now(),
				fmt.Errorf("CPA credential refresh panicked: %v", recovered),
			)
			log.Error(ctx, "CPA credential refresh panicked",
				log.Int("cpa_instance_id", credential.CpaInstanceID),
				log.Int("credential_id", credential.ID),
				log.Any("panic", recovered),
			)
		}
	}()
	if !svc.quotaExecutor.supports(credential) {
		return newCPAQuotaSkippedOutcome(credential, "quota unsupported")
	}
	observedRevision := credential.RefreshRevision
	for {
		claim, err := svc.repository.claimRefreshLease(ctx, credential.ID, observedRevision, svc.now())
		if err != nil {
			return newCPAQuotaFailureOutcome(credential.CpaInstanceID, credential.ID, svc.now(), err)
		}
		if claim.completed {
			return refreshOutcomeFromCredential(claim.credential, svc.now())
		}
		if claim.claimed {
			leaseReleased := false
			cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), cpaRefreshLeaseCleanupTimeout)
			defer cancelCleanup()
			defer func() {
				if leaseReleased {
					return
				}
				if releaseErr := svc.repository.releaseRefreshLease(cleanupCtx, credential.ID, claim.token); releaseErr != nil {
					log.Warn(cleanupCtx, "release CPA credential refresh lease after refresh failure",
						log.Int("credential_id", credential.ID),
						log.Cause(releaseErr),
					)
				}
			}()
			outcome := svc.quotaExecutor.execute(ctx, client, claim.credential)
			if outcome.status == cpaQuotaExecutionSkipped {
				return outcome
			}
			persisted, persistErr := svc.repository.applyQuotaOutcomeWithLease(ctx, outcome, claim.token, claim.revision)
			if persistErr == nil {
				leaseReleased = true
				return persisted
			}
			// A late owner must never overwrite a newer owner's result. Prefer the
			// newer terminal revision when one is already visible.
			current, reloadErr := svc.entFromContext(ctx).CPACredential.Get(ctx, credential.ID)
			if reloadErr == nil && current.RefreshRevision > claim.revision {
				return refreshOutcomeFromCredential(current, svc.now())
			}
			_ = svc.repository.releaseRefreshLease(ctx, credential.ID, claim.token)
			return newCPAQuotaFailureOutcome(credential.CpaInstanceID, credential.ID, svc.now(), persistErr)
		}
		if !claim.waiting {
			continue
		}
		current, waitErr := svc.repository.waitForRefreshRevision(ctx, credential.ID, observedRevision)
		if waitErr != nil {
			return newCPAQuotaFailureOutcome(credential.CpaInstanceID, credential.ID, svc.now(), waitErr)
		}
		if current.RefreshRevision > observedRevision {
			return refreshOutcomeFromCredential(current, svc.now())
		}
	}
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
