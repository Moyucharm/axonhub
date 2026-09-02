package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/objects"
)

const (
	// The lease covers the CPA client's request timeout with room for parsing and
	// one local persistence retry. It is never held open by a database transaction.
	cpaRefreshLeaseDuration       = 90 * time.Second
	cpaRefreshLeasePoll           = 25 * time.Millisecond
	cpaRefreshLeaseCleanupTimeout = 5 * time.Second
)

type cpaRefreshLeaseClaim struct {
	credential *ent.CPACredential
	token      string
	revision   int
	claimed    bool
	completed  bool
	waiting    bool
}

// claimRefreshLease uses a revision plus an expiring token as a database CAS.
// The read and conditional update are short writes; network I/O happens only
// after this method returns and never while a transaction is open.
func (repository *cpaRepository) claimRefreshLease(
	ctx context.Context,
	credentialID int,
	observedRevision int,
	now time.Time,
) (cpaRefreshLeaseClaim, error) {
	current, err := repository.entFromContext(ctx).CPACredential.Get(ctx, credentialID)
	if err != nil {
		return cpaRefreshLeaseClaim{}, fmt.Errorf("load CPA credential refresh lease: %w", err)
	}
	if current.RefreshRevision > observedRevision {
		return cpaRefreshLeaseClaim{credential: current, completed: true}, nil
	}
	if refreshLeaseActive(current, now) {
		return cpaRefreshLeaseClaim{credential: current, waiting: true}, nil
	}

	token := uuid.NewString()
	claim := cpaRefreshLeaseClaim{}
	claimOperation := func() error {
		latest, getErr := repository.entFromContext(ctx).CPACredential.Get(ctx, credentialID)
		if getErr != nil {
			return fmt.Errorf("reload CPA credential for refresh lease: %w", getErr)
		}
		if latest.RefreshRevision > observedRevision {
			claim = cpaRefreshLeaseClaim{credential: latest, completed: true}
			return nil
		}
		if refreshLeaseActive(latest, now) {
			claim = cpaRefreshLeaseClaim{credential: latest, waiting: true}
			return nil
		}
		leaseUntil := now.Add(cpaRefreshLeaseDuration)
		update := repository.entFromContext(ctx).CPACredential.UpdateOneID(credentialID).
			Where(
				cpacredential.RefreshRevisionEQ(latest.RefreshRevision),
				cpacredential.Or(
					cpacredential.RefreshLeaseUntilIsNil(),
					cpacredential.RefreshLeaseUntilLTE(now),
				),
			).
			SetRefreshLeaseToken(token).
			SetRefreshLeaseUntil(leaseUntil)
		if err := update.Exec(ctx); err != nil {
			if ent.IsNotFound(err) {
				claim = cpaRefreshLeaseClaim{waiting: true}
				return nil
			}
			return fmt.Errorf("claim CPA credential refresh lease: %w", err)
		}
		latest.RefreshLeaseToken = token
		latest.RefreshLeaseUntil = &leaseUntil
		claim = cpaRefreshLeaseClaim{
			credential: latest,
			token:      token,
			revision:   latest.RefreshRevision,
			claimed:    true,
		}
		return nil
	}
	if repository.withInstanceWriteRetry != nil {
		err = repository.withInstanceWriteRetry(ctx, current.CpaInstanceID, claimOperation)
	} else {
		err = claimOperation()
	}
	if err != nil {
		return cpaRefreshLeaseClaim{}, err
	}
	return claim, nil
}

func refreshLeaseActive(credential *ent.CPACredential, now time.Time) bool {
	return credential != nil && strings.TrimSpace(credential.RefreshLeaseToken) != "" &&
		credential.RefreshLeaseUntil != nil && credential.RefreshLeaseUntil.After(now)
}

func (repository *cpaRepository) waitForRefreshRevision(
	ctx context.Context,
	credentialID int,
	observedRevision int,
) (*ent.CPACredential, error) {
	for {
		current, err := repository.entFromContext(ctx).CPACredential.Get(ctx, credentialID)
		if err != nil {
			return nil, fmt.Errorf("poll CPA credential refresh lease: %w", err)
		}
		now := repository.now()
		if current.RefreshRevision > observedRevision || !refreshLeaseActive(current, now) {
			return current, nil
		}
		wait := cpaRefreshLeasePoll
		if until := time.Until(*current.RefreshLeaseUntil); until > 0 && until < wait {
			wait = until
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
}

func (repository *cpaRepository) releaseRefreshLease(ctx context.Context, credentialID int, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	current, err := repository.entFromContext(ctx).CPACredential.Get(ctx, credentialID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("load CPA credential for lease release: %w", err)
	}
	release := func() error {
		update := repository.entFromContext(ctx).CPACredential.UpdateOneID(credentialID).
			Where(cpacredential.RefreshLeaseTokenEQ(token)).
			SetRefreshLeaseToken("").
			ClearRefreshLeaseUntil()
		if err := update.Exec(ctx); err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("release CPA credential refresh lease: %w", err)
		}
		return nil
	}
	if repository.withInstanceWriteRetry != nil {
		return repository.withInstanceWriteRetry(ctx, current.CpaInstanceID, release)
	}
	return release()
}

func refreshOutcomeFromCredential(credential *ent.CPACredential, now time.Time) cpaQuotaExecutionOutcome {
	outcome := cpaQuotaExecutionOutcome{
		instanceID:   credential.CpaInstanceID,
		credentialID: credential.ID,
		credential:   credential,
		planType:     credential.PlanType,
		quotaState:   objects.CPAQuotaState(credential.QuotaState),
		snapshot:     credential.QuotaData,
		attemptedAt:  now,
	}
	if credential.QuotaLastAttemptAt != nil {
		outcome.attemptedAt = *credential.QuotaLastAttemptAt
	}
	switch outcome.quotaState {
	case objects.CPAQuotaStateSuccess, objects.CPAQuotaStateUnsupported, objects.CPAQuotaStateInsufficientData:
		outcome.status = cpaQuotaExecutionSuccess
	case objects.CPAQuotaStateError:
		outcome.status = cpaQuotaExecutionFailure
		message := sanitizeCPAErrorMessage(credential.QuotaLastError)
		if message == "" {
			message = "CPA quota refresh failed"
		}
		outcome.err = fmt.Errorf("%s", message)
	default:
		outcome.status = cpaQuotaExecutionFailure
		outcome.err = fmt.Errorf("CPA quota refresh did not publish a terminal result")
	}
	return outcome
}
