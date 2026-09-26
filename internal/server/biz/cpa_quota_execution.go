package biz

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

type cpaQuotaExecutionStatus string

const (
	cpaQuotaExecutionSuccess cpaQuotaExecutionStatus = "success"
	cpaQuotaExecutionFailure cpaQuotaExecutionStatus = "failure"
	cpaQuotaExecutionSkipped cpaQuotaExecutionStatus = "skipped"
)

type cpaQuotaExecutionOutcome struct {
	status       cpaQuotaExecutionStatus
	instanceID   int
	credentialID int
	quotaState   objects.CPAQuotaState
	planType     string
	snapshot     objects.CPAQuotaSnapshot
	attemptedAt  time.Time
	err          error
	skipReason   string
	credential   *ent.CPACredential
}

func newCPAQuotaFailureOutcome(instanceID, credentialID int, attemptedAt time.Time, err error) cpaQuotaExecutionOutcome {
	if err == nil {
		err = fmt.Errorf("CPA quota execution failed without an error")
	}
	return cpaQuotaExecutionOutcome{
		status:       cpaQuotaExecutionFailure,
		instanceID:   instanceID,
		credentialID: credentialID,
		quotaState:   objects.CPAQuotaStateError,
		attemptedAt:  attemptedAt,
		err:          err,
	}
}

func newCPAQuotaSkippedOutcome(credential *ent.CPACredential, reason string) cpaQuotaExecutionOutcome {
	if strings.TrimSpace(reason) == "" {
		reason = "unspecified"
	}
	return cpaQuotaExecutionOutcome{
		status:       cpaQuotaExecutionSkipped,
		instanceID:   credential.CpaInstanceID,
		credentialID: credential.ID,
		skipReason:   reason,
		credential:   credential,
	}
}

func (outcome cpaQuotaExecutionOutcome) validate() error {
	switch outcome.status {
	case cpaQuotaExecutionFailure:
		if outcome.err == nil {
			return fmt.Errorf("CPA quota failure outcome is missing an error")
		}
	case cpaQuotaExecutionSkipped:
		if strings.TrimSpace(outcome.skipReason) == "" {
			return fmt.Errorf("CPA quota skipped outcome is missing a reason")
		}
	case cpaQuotaExecutionSuccess:
		switch outcome.quotaState {
		case objects.CPAQuotaStateSuccess, objects.CPAQuotaStateUnsupported, objects.CPAQuotaStateInsufficientData:
		default:
			return fmt.Errorf("CPA quota success outcome has invalid state %q", outcome.quotaState)
		}
	default:
		return fmt.Errorf("unknown CPA quota execution status %q", outcome.status)
	}
	return nil
}

type cpaQuotaExecutor struct {
	registry              *cpaclient.QuotaRegistry
	group                 singleflight.Group
	globalLimiter         *semaphore.Weighted
	instanceMu            sync.Mutex
	instanceLimits        map[int]*semaphore.Weighted
	estimate              func(context.Context, *ent.CPACredential, *objects.CPAQuotaSnapshot)
	now                   func() time.Time
	afterSingleflightJoin func()
}

func newCPAQuotaExecutor(
	registry *cpaclient.QuotaRegistry,
	estimate func(context.Context, *ent.CPACredential, *objects.CPAQuotaSnapshot),
	now func() time.Time,
) *cpaQuotaExecutor {
	return &cpaQuotaExecutor{
		registry:       registry,
		globalLimiter:  semaphore.NewWeighted(maxCPAGlobalConcurrency),
		instanceLimits: make(map[int]*semaphore.Weighted),
		estimate:       estimate,
		now:            now,
	}
}

func (executor *cpaQuotaExecutor) supports(credential *ent.CPACredential) bool {
	return executor.registry.Supports(credential.Provider) &&
		!(credential.Provider == "xai" && credential.QuotaContext.Paid)
}

func (executor *cpaQuotaExecutor) execute(
	ctx context.Context,
	client cpaclient.ManagementClient,
	credential *ent.CPACredential,
) cpaQuotaExecutionOutcome {
	if !executor.supports(credential) {
		return newCPAQuotaSkippedOutcome(credential, "quota unsupported")
	}

	resultCh := executor.group.DoChan(fmt.Sprintf("credential:%d", credential.ID), func() (any, error) {
		return executor.executeOnce(ctx, client, credential), nil
	})
	if executor.afterSingleflightJoin != nil {
		executor.afterSingleflightJoin()
	}
	value := (<-resultCh).Val
	outcome, ok := value.(cpaQuotaExecutionOutcome)
	if !ok {
		return newCPAQuotaFailureOutcome(
			credential.CpaInstanceID,
			credential.ID,
			executor.now(),
			fmt.Errorf("unexpected CPA quota execution result"),
		)
	}
	if err := outcome.validate(); err != nil {
		return newCPAQuotaFailureOutcome(credential.CpaInstanceID, credential.ID, executor.now(), err)
	}
	return outcome
}

func (executor *cpaQuotaExecutor) executeOnce(
	ctx context.Context,
	client cpaclient.ManagementClient,
	credential *ent.CPACredential,
) cpaQuotaExecutionOutcome {
	outcome := cpaQuotaExecutionOutcome{
		instanceID:   credential.CpaInstanceID,
		credentialID: credential.ID,
		attemptedAt:  executor.now(),
	}
	release, err := executor.acquireProviderCall(ctx, credential.CpaInstanceID)
	if err != nil {
		return newCPAQuotaFailureOutcome(outcome.instanceID, outcome.credentialID, outcome.attemptedAt, err)
	}
	defer release()

	result, err := executor.registry.Fetch(ctx, client, cpaclient.CredentialInput{
		AuthIndex:   credential.AuthIndex,
		Provider:    credential.Provider,
		PlanType:    credential.PlanType,
		ProjectID:   credential.QuotaContext.ProjectID,
		AccountID:   credential.QuotaContext.CodexAccountID,
		AccountType: credential.QuotaContext.AccountType,
		Paid:        credential.QuotaContext.Paid,
	})
	if err != nil {
		return newCPAQuotaFailureOutcome(outcome.instanceID, outcome.credentialID, outcome.attemptedAt, err)
	}

	if result.State != objects.CPAQuotaStateSuccess &&
		result.State != objects.CPAQuotaStateUnsupported &&
		result.State != objects.CPAQuotaStateInsufficientData {
		return newCPAQuotaFailureOutcome(
			outcome.instanceID,
			outcome.credentialID,
			outcome.attemptedAt,
			fmt.Errorf("provider returned invalid CPA quota state %q", result.State),
		)
	}

	snapshot := result.Snapshot
	if result.State == objects.CPAQuotaStateSuccess && strings.EqualFold(strings.TrimSpace(credential.Provider), "codex") && executor.estimate != nil {
		executor.estimate(ctx, credential, &snapshot)
	}
	if result.State == objects.CPAQuotaStateUnsupported || result.State == objects.CPAQuotaStateInsufficientData {
		snapshot = objects.CPAQuotaSnapshot{}
	}
	outcome.status = cpaQuotaExecutionSuccess
	outcome.quotaState = result.State
	outcome.planType = result.PlanType
	outcome.snapshot = snapshot
	return outcome
}

func (executor *cpaQuotaExecutor) acquireProviderCall(ctx context.Context, instanceID int) (func(), error) {
	instanceLimiter := executor.instanceLimiter(instanceID)
	if err := instanceLimiter.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	if err := executor.globalLimiter.Acquire(ctx, 1); err != nil {
		instanceLimiter.Release(1)
		return nil, err
	}
	return func() {
		executor.globalLimiter.Release(1)
		instanceLimiter.Release(1)
	}, nil
}

func (executor *cpaQuotaExecutor) instanceLimiter(instanceID int) *semaphore.Weighted {
	executor.instanceMu.Lock()
	defer executor.instanceMu.Unlock()
	limiter := executor.instanceLimits[instanceID]
	if limiter == nil {
		limiter = semaphore.NewWeighted(maxCPAInstanceConcurrency)
		executor.instanceLimits[instanceID] = limiter
	}
	return limiter
}

func (executor *cpaQuotaExecutor) forgetInstance(instanceID int) {
	executor.instanceMu.Lock()
	delete(executor.instanceLimits, instanceID)
	executor.instanceMu.Unlock()
}
