package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/cparesetattempt"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

// claudeResetRetryWindow bounds how long an uncertain claim may be retried with
// its original request ID. Afterwards the grant use stays locked until a quota
// refresh proves it was spent.
const claudeResetRetryWindow = 10 * time.Minute

// claudeResetAutoWindow is the lead time within which an expiring grant is
// used automatically.
const claudeResetAutoWindow = 30 * time.Minute

// CPAClaudeResetOutcome is the result of one Claude reset claim.
type CPAClaudeResetOutcome struct {
	// Result is the settled provider answer; nil when the outcome is unknown.
	Result *objects.CPAClaudeResetResult
	// Uncertain means the grant use may have been spent.
	Uncertain bool
	// RetryUntil is the deadline for retrying with the original request ID.
	RetryUntil *time.Time
}

// claudeResetKey identifies one grant use. A spent use lowers resets_left, so
// the next use gets a fresh key while a repeated or uncertain one collides.
func claudeResetKey(organization, grantID string, resetsLeft int) string {
	sum := sha256.Sum256([]byte("claude\x00" + organization + "\x00" + grantID + "\x00" + strconv.Itoa(resetsLeft)))
	return hex.EncodeToString(sum[:])
}

func claudeResetRetryUntil(attempt *ent.CPAResetAttempt, now time.Time) *time.Time {
	if attempt.State != cparesetattempt.StateUncertain {
		return nil
	}
	deadline := attempt.CreatedAt.Add(claudeResetRetryWindow)
	if !deadline.After(now) {
		return nil
	}
	return &deadline
}

func (svc *CPAService) claudeResetCredential(ctx context.Context, credentialID int) (*ent.CPACredential, *ent.CPAInstance, error) {
	credential, instance, err := svc.repository.credentialWithInstance(ctx, credentialID)
	if err != nil {
		return nil, nil, err
	}
	if !instance.Enabled {
		return nil, nil, fmt.Errorf("CPA instance is disabled")
	}
	if cpaclient.NormalizeProvider(credential.Provider) != "claude" {
		return nil, nil, fmt.Errorf("CPA credential is not Claude")
	}
	if strings.TrimSpace(credential.AuthIndex) == "" {
		return nil, nil, fmt.Errorf("Claude credential identity is incomplete")
	}
	return credential, instance, nil
}

func (svc *CPAService) liveClaudeOrganization(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential) (string, error) {
	release, err := svc.quotaExecutor.acquireProviderCall(ctx, credential.CpaInstanceID)
	if err != nil {
		return "", err
	}
	defer release()
	return cpaclient.ReadClaudeOrganization(ctx, client, credential.AuthIndex)
}

// liveClaudeReset re-reads the grant status right before a claim; the stored
// snapshot only drives the display.
func (svc *CPAService) liveClaudeReset(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential) (*objects.CPAClaudeReset, error) {
	release, err := svc.quotaExecutor.acquireProviderCall(ctx, credential.CpaInstanceID)
	if err != nil {
		return nil, err
	}
	defer release()
	return cpaclient.ReadClaudeReset(ctx, client, credential.AuthIndex)
}

// sendClaudeClaim posts one claim under a provider-call permit.
func (svc *CPAService) sendClaudeClaim(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential, organization string, attempt *ent.CPAResetAttempt) (objects.CPAClaudeResetResult, error) {
	release, err := svc.quotaExecutor.acquireProviderCall(ctx, credential.CpaInstanceID)
	if err != nil {
		return "", fmt.Errorf("Claude reset provider call unavailable: %w", err)
	}
	defer release()
	return cpaclient.ClaimClaudeReset(ctx, client, credential.AuthIndex, organization, attempt.GrantID, attempt.RequestID)
}

// recordClaudeClaim stores the claim answer. Refusals of a first claim prove
// nothing was spent, so the claim row is removed and the use stays available.
// A refusal on a retry cannot prove the earlier ambiguous claim did not spend.
func (svc *CPAService) recordClaudeClaim(ctx context.Context, attempt *ent.CPAResetAttempt, result objects.CPAClaudeResetResult, claimErr error, retry bool) (*CPAClaudeResetOutcome, error) {
	client := svc.entFromContext(ctx).CPAResetAttempt
	switch {
	case claimErr == nil && result.Spent():
		if _, err := client.UpdateOneID(attempt.ID).SetState(cparesetattempt.StateRedeemed).Save(ctx); err != nil {
			// The pending row keeps blocking the use until a refresh settles it.
			log.Warn(ctx, "CPA Claude reset state update failed", log.Int("credential_id", attempt.CredentialID), log.Cause(err))
		}
		return &CPAClaudeResetOutcome{Result: &result}, nil
	case claimErr == nil && !retry:
		if err := client.DeleteOneID(attempt.ID).Exec(ctx); err != nil {
			log.Warn(ctx, "CPA Claude reset claim release failed", log.Int("credential_id", attempt.CredentialID), log.Cause(err))
		}
		return &CPAClaudeResetOutcome{Result: &result}, nil
	case claimErr == nil:
		return &CPAClaudeResetOutcome{Result: &result, Uncertain: true, RetryUntil: claudeResetRetryUntil(attempt, svc.now())}, nil
	case errors.Is(claimErr, cpaclient.ErrClaudeResetOutcomeUnknown):
		if attempt.State != cparesetattempt.StateUncertain {
			updated, err := client.UpdateOneID(attempt.ID).SetState(cparesetattempt.StateUncertain).Save(ctx)
			if err != nil {
				log.Warn(ctx, "CPA Claude reset state update failed", log.Int("credential_id", attempt.CredentialID), log.Cause(err))
				return &CPAClaudeResetOutcome{Uncertain: true}, nil
			}
			attempt = updated
		}
		log.Warn(ctx, "CPA Claude reset outcome uncertain", log.Int("credential_id", attempt.CredentialID))
		return &CPAClaudeResetOutcome{Uncertain: true, RetryUntil: claudeResetRetryUntil(attempt, svc.now())}, nil
	default:
		// The claim was rejected before it was sent.
		if !retry {
			if err := client.DeleteOneID(attempt.ID).Exec(ctx); err != nil {
				log.Warn(ctx, "CPA Claude reset claim release failed", log.Int("credential_id", attempt.CredentialID), log.Cause(err))
			}
		}
		return nil, claimErr
	}
}

// claimClaudeReset validates one grant use live and claims it exactly once.
func (svc *CPAService) claimClaudeReset(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential, grantID string) (*CPAClaudeResetOutcome, error) {
	organization, err := svc.liveClaudeOrganization(ctx, client, credential)
	if err != nil {
		return nil, err
	}
	status, err := svc.liveClaudeReset(ctx, client, credential)
	if err != nil {
		return nil, err
	}
	now := svc.now()
	// The confirmation freezes one grant ID; a stale request must never claim a
	// different grant than the one the provider would use next.
	selected := cpaclient.SelectClaudeResetGrant(status, now)
	if selected == nil || selected.ID != grantID {
		blocker := cpaclient.ClaudeResetBlocker(status, grantID, now)
		if blocker == "" {
			blocker = "not_next_grant"
		}
		return nil, fmt.Errorf("Claude reset grant is not claimable (%s)", blocker)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attempt, err := svc.entFromContext(ctx).CPAResetAttempt.Create().
		SetProvider(cparesetattempt.ProviderClaude).
		// The retry window is measured on the service clock.
		SetCreatedAt(now).
		SetCreditKey(claudeResetKey(organization, selected.ID, selected.ResetsLeft)).
		SetCredentialID(credential.ID).
		SetRequestID(uuid.NewString()).
		SetGrantID(selected.ID).
		SetResetsLeft(selected.ResetsLeft).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return nil, fmt.Errorf("Claude reset grant use was already attempted")
	}
	if err != nil {
		return nil, fmt.Errorf("claim Claude reset grant: %w", err)
	}
	result, claimErr := svc.sendClaudeClaim(ctx, client, credential, organization, attempt)
	return svc.recordClaudeClaim(ctx, attempt, result, claimErr, false)
}

// retryClaudeReset resends an uncertain claim with its original request ID so
// the provider can deduplicate it.
func (svc *CPAService) retryClaudeReset(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential, attempt *ent.CPAResetAttempt) (*CPAClaudeResetOutcome, error) {
	if claudeResetRetryUntil(attempt, svc.now()) == nil || attempt.ResetsLeft == nil {
		return nil, fmt.Errorf("Claude reset retry window expired; check the account before any further action")
	}
	organization, err := svc.liveClaudeOrganization(ctx, client, credential)
	if err != nil {
		return nil, err
	}
	if claudeResetKey(organization, attempt.GrantID, *attempt.ResetsLeft) != attempt.CreditKey {
		return nil, fmt.Errorf("Claude account changed since the uncertain reset claim")
	}
	result, claimErr := svc.sendClaudeClaim(ctx, client, credential, organization, attempt)
	return svc.recordClaudeClaim(ctx, attempt, result, claimErr, true)
}

func (svc *CPAService) openClaudeResetAttempt(ctx context.Context, credentialID int, grantID string) (*ent.CPAResetAttempt, error) {
	attempt, err := svc.entFromContext(ctx).CPAResetAttempt.Query().
		Where(
			cparesetattempt.ProviderEQ(cparesetattempt.ProviderClaude),
			cparesetattempt.CredentialIDEQ(credentialID),
			cparesetattempt.GrantIDEQ(grantID),
			cparesetattempt.StateEQ(cparesetattempt.StateUncertain),
		).
		Order(cparesetattempt.ByCreatedAt(sql.OrderDesc())).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check uncertain Claude reset claims: %w", err)
	}
	return attempt, nil
}

// ResetClaudeCredential claims one use of a Claude reset grant, or retries the
// grant's uncertain claim with its original request inside the retry window.
func (svc *CPAService) ResetClaudeCredential(ctx context.Context, credentialID int, grantID string) (*CPAClaudeResetOutcome, error) {
	credential, instance, err := svc.claudeResetCredential(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(grantID) == "" {
		return nil, fmt.Errorf("Claude reset grant ID is empty")
	}
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	pending, err := svc.openClaudeResetAttempt(ctx, credential.ID, grantID)
	if err != nil {
		return nil, err
	}
	var outcome *CPAClaudeResetOutcome
	if pending != nil {
		outcome, err = svc.retryClaudeReset(ctx, client, credential, pending)
	} else {
		outcome, err = svc.claimClaudeReset(ctx, client, credential, grantID)
	}
	if err != nil {
		return nil, err
	}
	if refreshed := svc.refreshCredentialOutcome(ctx, client, credential); refreshed.err != nil {
		log.Warn(ctx, "CPA quota refresh after Claude reset failed", log.Int("credential_id", credentialID), log.Cause(refreshed.err))
	}
	return outcome, nil
}

// settleClaudeResetAttempts resolves open claims a fresh snapshot proves were
// spent: the grant's remaining uses dropped below the count the claim saw.
func (svc *CPAService) settleClaudeResetAttempts(ctx context.Context, credential *ent.CPACredential) {
	if credential == nil || credential.QuotaState != string(objects.CPAQuotaStateSuccess) || credential.QuotaData.ClaudeReset == nil {
		return
	}
	attempts, err := svc.entFromContext(ctx).CPAResetAttempt.Query().
		Where(
			cparesetattempt.ProviderEQ(cparesetattempt.ProviderClaude),
			cparesetattempt.CredentialIDEQ(credential.ID),
			cparesetattempt.StateIn(cparesetattempt.StatePending, cparesetattempt.StateUncertain),
		).
		All(ctx)
	if err != nil {
		log.Warn(ctx, "CPA Claude reset settlement query failed", log.Int("credential_id", credential.ID), log.Cause(err))
		return
	}
	for _, attempt := range attempts {
		if attempt.ResetsLeft == nil {
			continue
		}
		for _, grant := range credential.QuotaData.ClaudeReset.Grants {
			if grant.ID != attempt.GrantID || grant.ResetsLeft >= *attempt.ResetsLeft {
				continue
			}
			if _, err := svc.entFromContext(ctx).CPAResetAttempt.UpdateOneID(attempt.ID).SetState(cparesetattempt.StateRedeemed).Save(ctx); err != nil {
				log.Warn(ctx, "CPA Claude reset settlement failed", log.Int("credential_id", credential.ID), log.Cause(err))
			}
			break
		}
	}
}

// decorateClaudeResetViews overlays local claims on the stored grant status:
// open claims mark their grant uncertain, and a redeemed claim not yet visible
// in the snapshot lowers the remaining uses it already spent.
func (svc *CPAService) decorateClaudeResetViews(ctx context.Context, views ...*CPACredentialView) error {
	ids := make([]int, 0, len(views))
	for _, view := range views {
		if view != nil && view.QuotaData.ClaudeReset != nil && len(view.QuotaData.ClaudeReset.Grants) > 0 {
			ids = append(ids, view.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	attempts, err := svc.entFromContext(ctx).CPAResetAttempt.Query().
		Where(cparesetattempt.ProviderEQ(cparesetattempt.ProviderClaude), cparesetattempt.CredentialIDIn(ids...)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("check Claude reset claims: %w", err)
	}
	if len(attempts) == 0 {
		return nil
	}
	byCredential := make(map[int][]*ent.CPAResetAttempt, len(attempts))
	for _, attempt := range attempts {
		if attempt.ResetsLeft != nil {
			byCredential[attempt.CredentialID] = append(byCredential[attempt.CredentialID], attempt)
		}
	}
	now := svc.now()
	for _, view := range views {
		if view == nil || view.QuotaData.ClaudeReset == nil {
			continue
		}
		claims := byCredential[view.ID]
		if len(claims) == 0 {
			continue
		}
		// The snapshot is shared with the stored entity; decorate a copy.
		status := *view.QuotaData.ClaudeReset
		status.Grants = slices.Clone(status.Grants)
		for index := range status.Grants {
			grant := &status.Grants[index]
			for _, claim := range claims {
				if claim.GrantID != grant.ID || *claim.ResetsLeft != grant.ResetsLeft {
					continue
				}
				if claim.State == cparesetattempt.StateRedeemed {
					grant.ResetsLeft = max(0, grant.ResetsLeft-1)
					continue
				}
				grant.Uncertain = true
				grant.RetryUntil = claudeResetRetryUntil(claim, now)
			}
		}
		view.QuotaData.ClaudeReset = &status
	}
	return nil
}

// autoResetClaudeInstance claims Claude grants that expire within the automatic
// window, under the same switches and cycle as Codex reset cards. Every
// candidate is still validated live by claimClaudeReset, and uncertain claims
// are never retried automatically.
func (svc *CPAService) autoResetClaudeInstance(ctx context.Context, instance *ent.CPAInstance) {
	if !instance.Enabled || !instance.AutoManageEnabled || !instance.AutoResetEnabled {
		return
	}
	credentials, err := svc.entFromContext(ctx).CPACredential.Query().Where(
		cpacredential.CpaInstanceIDEQ(instance.ID), cpacredential.ProviderEQ("claude"),
		cpacredential.UnavailableEQ(false),
	).All(ctx)
	if err != nil {
		log.Warn(ctx, "CPA Claude auto reset credential query failed", log.Int("cpa_instance_id", instance.ID), log.Cause(err))
		return
	}
	now := svc.now()
	deadline := now.Add(claudeResetAutoWindow)
	var client cpaclient.ManagementClient
	defer func() {
		if client != nil {
			client.CloseIdleConnections()
		}
	}()
	for _, credential := range credentials {
		if strings.TrimSpace(credential.AuthIndex) == "" || credential.QuotaState != string(objects.CPAQuotaStateSuccess) {
			continue
		}
		grant := cpaclient.SelectClaudeResetGrant(credential.QuotaData.ClaudeReset, now)
		if grant == nil || grant.EndsAt == nil || grant.EndsAt.After(deadline) {
			continue
		}
		// An open or settled claim for this use would only burn live reads
		// before the unique claim rejects it.
		claimed, err := svc.entFromContext(ctx).CPAResetAttempt.Query().Where(
			cparesetattempt.ProviderEQ(cparesetattempt.ProviderClaude),
			cparesetattempt.CredentialIDEQ(credential.ID),
			cparesetattempt.GrantIDEQ(grant.ID),
			cparesetattempt.ResetsLeftEQ(grant.ResetsLeft),
		).Exist(ctx)
		if err != nil {
			log.Warn(ctx, "CPA Claude auto reset claim lookup failed", log.Int("credential_id", credential.ID), log.Cause(err))
			continue
		}
		if claimed {
			continue
		}
		if client == nil {
			client, err = svc.clientForInstance(ctx, instance)
			if err != nil {
				log.Warn(ctx, "CPA Claude auto reset connection failed", log.Int("cpa_instance_id", instance.ID), log.Cause(err))
				return
			}
		}
		outcome, err := svc.claimClaudeReset(ctx, client, credential, grant.ID)
		if err != nil {
			log.Warn(ctx, "CPA Claude auto reset attempt failed", log.Int("credential_id", credential.ID), log.Cause(err))
			continue
		}
		if outcome.Result == nil || !outcome.Result.Spent() {
			log.Warn(ctx, "CPA Claude auto reset not applied", log.Int("credential_id", credential.ID), log.Any("result", outcome.Result), log.Bool("uncertain", outcome.Uncertain))
		}
		if refreshed := svc.refreshCredentialOutcome(ctx, client, credential); refreshed.err != nil {
			log.Warn(ctx, "CPA Claude auto reset quota refresh failed", log.Int("credential_id", credential.ID), log.Cause(refreshed.err))
		}
	}
}
