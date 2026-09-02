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

type cpaRepository struct {
	entFromContext                 func(context.Context) *ent.Client
	withInstanceWriteRetry         func(context.Context, int, func() error) error
	supportsNormalizedQuota        func(cpaclient.NormalizedCredential) bool
	now                            func() time.Time
	invalidateUsageCredentialCache func(int)
}

func newCPARepository(svc *CPAService) *cpaRepository {
	return &cpaRepository{
		entFromContext:          svc.entFromContext,
		withInstanceWriteRetry:  svc.withCPAInstanceWriteRetry,
		supportsNormalizedQuota: svc.supportsNormalizedQuota,
		now:                     svc.now,
		invalidateUsageCredentialCache: func(instanceID int) {
			if svc.usageRepository != nil {
				svc.usageRepository.invalidateCredentialCache(instanceID)
			}
		},
	}
}

func (repository *cpaRepository) credentialWithInstance(ctx context.Context, credentialID int) (*ent.CPACredential, *ent.CPAInstance, error) {
	credential, err := repository.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.IDEQ(credentialID)).
		WithCpaInstance().
		Only(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get CPA credential: %w", err)
	}
	if credential.Edges.CpaInstance == nil {
		return nil, nil, fmt.Errorf("CPA credential instance is missing")
	}
	return credential, credential.Edges.CpaInstance, nil
}

func (repository *cpaRepository) patrolCandidates(ctx context.Context, instanceID int, disabled bool) ([]*ent.CPACredential, error) {
	credentials, err := repository.entFromContext(ctx).CPACredential.Query().
		Where(
			cpacredential.CpaInstanceIDEQ(instanceID),
			cpacredential.DisabledEQ(disabled),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query CPA patrol credentials: %w", err)
	}
	return credentials, nil
}

func (repository *cpaRepository) applyQuotaOutcome(ctx context.Context, outcome cpaQuotaExecutionOutcome) (cpaQuotaExecutionOutcome, error) {
	return repository.applyQuotaOutcomeWithLease(ctx, outcome, "", -1)
}

func (repository *cpaRepository) applyQuotaOutcomeWithLease(
	ctx context.Context,
	outcome cpaQuotaExecutionOutcome,
	leaseToken string,
	leaseRevision int,
) (cpaQuotaExecutionOutcome, error) {
	if err := outcome.validate(); err != nil {
		return outcome, err
	}
	if outcome.status == cpaQuotaExecutionSkipped {
		return outcome, nil
	}
	var persisted *ent.CPACredential
	err := repository.withInstanceWriteRetry(ctx, outcome.instanceID, func() error {
		current, err := repository.entFromContext(ctx).CPACredential.Get(ctx, outcome.credentialID)
		if err != nil {
			return fmt.Errorf("reload CPA credential before quota persistence: %w", err)
		}

		quotaState := outcome.quotaState
		quotaData := outcome.snapshot
		if outcome.status == cpaQuotaExecutionFailure {
			quotaState = objects.CPAQuotaStateError
			quotaData = current.QuotaData
		}
		projection := projectStoredCPACredentialWithQuota(current, quotaState, quotaData, outcome.attemptedAt)
		update := repository.entFromContext(ctx).CPACredential.UpdateOneID(current.ID).
			SetQuotaState(string(quotaState)).
			SetQuotaLastAttemptAt(outcome.attemptedAt).
			SetDisplayNameSortKey(projection.displayNameSortKey).
			SetDisplayNameSortLength(projection.displayNameSortLength).
			SetHealthState(string(projection.healthState)).
			SetQuotaCooling(projection.quotaCooling).
			SetProjectionVersion(currentCPAProjectionVersion)
		if strings.TrimSpace(leaseToken) != "" {
			update = update.Where(
				cpacredential.RefreshLeaseTokenEQ(leaseToken),
				cpacredential.RefreshRevisionEQ(leaseRevision),
			)
		}
		if projection.quotaCooldownUntil == nil {
			update.ClearQuotaCooldownUntil()
		} else {
			update.SetQuotaCooldownUntil(*projection.quotaCooldownUntil)
		}

		if outcome.status == cpaQuotaExecutionFailure {
			update.
				SetQuotaLastFailureAt(outcome.attemptedAt).
				SetQuotaLastError(sanitizeCPAErrorMessage(outcome.err.Error()))
		} else {
			update.SetQuotaLastError("")
			if outcome.planType != "" {
				update.SetPlanType(outcome.planType)
			}
			switch quotaState {
			case objects.CPAQuotaStateSuccess:
				update.SetQuotaData(quotaData).SetQuotaLastSuccessAt(outcome.attemptedAt)
			case objects.CPAQuotaStateUnsupported, objects.CPAQuotaStateInsufficientData:
				update.SetQuotaData(quotaData)
			}
		}
		if strings.TrimSpace(leaseToken) != "" {
			update.
				SetRefreshRevision(leaseRevision + 1).
				SetRefreshLeaseToken("").
				ClearRefreshLeaseUntil()
		}
		persisted, err = update.Save(ctx)
		if err != nil {
			return fmt.Errorf("persist CPA quota outcome: %w", err)
		}
		return nil
	})
	if err != nil {
		return outcome, err
	}
	outcome.credential = persisted
	return outcome, nil
}
