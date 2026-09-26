package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacodexresetattempt"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func (svc *CPAService) codexResetCredential(ctx context.Context, credentialID int) (*ent.CPACredential, *ent.CPAInstance, error) {
	credential, instance, err := svc.repository.credentialWithInstance(ctx, credentialID)
	if err != nil {
		return nil, nil, err
	}
	if !instance.Enabled {
		return nil, nil, fmt.Errorf("CPA instance is disabled")
	}
	if cpaclient.NormalizeProvider(credential.Provider) != "codex" {
		return nil, nil, fmt.Errorf("CPA credential is not Codex")
	}
	if strings.TrimSpace(credential.AuthIndex) == "" || strings.TrimSpace(credential.QuotaContext.CodexAccountID) == "" {
		return nil, nil, fmt.Errorf("Codex credential identity is incomplete")
	}
	return credential, instance, nil
}

// liveCodexResetCredits re-reads the provider cards for one credential. The
// display list comes from the quota snapshot; this read only validates an
// action against the provider immediately before it happens.
func (svc *CPAService) liveCodexResetCredits(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential) ([]objects.CPAQuotaResetCredit, error) {
	release, err := svc.quotaExecutor.acquireProviderCall(ctx, credential.CpaInstanceID)
	if err != nil {
		return nil, err
	}
	defer release()
	credits, err := cpaclient.ListCodexResetCredits(ctx, client, credential.AuthIndex, credential.QuotaContext.CodexAccountID)
	if err != nil {
		return nil, fmt.Errorf("Codex reset credits request failed: %w", err)
	}
	return cpaclient.UsableCodexResetCredits(credits, svc.now()), nil
}

func codexCreditKey(accountID, creditID string) string {
	sum := sha256.Sum256([]byte(accountID + "\x00" + creditID))
	return hex.EncodeToString(sum[:])
}

func (svc *CPAService) claimedCodexCreditKeys(ctx context.Context, keys []string) (map[string]bool, error) {
	claimed := make(map[string]bool, len(keys))
	for start := 0; start < len(keys); start += 400 {
		end := min(start+400, len(keys))
		rows, err := svc.entFromContext(ctx).CPACodexResetAttempt.Query().
			Where(cpacodexresetattempt.CreditKeyIn(keys[start:end]...)).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("check claimed Codex reset credits: %w", err)
		}
		for _, row := range rows {
			claimed[row.CreditKey] = true
		}
	}
	return claimed, nil
}

// unclaimedCodexResetCredits drops cards that already have a claim record. The
// consume path uses it so the earliest-card comparison matches the list the API
// displays; the unique claim insert stays the concurrency gate.
func (svc *CPAService) unclaimedCodexResetCredits(ctx context.Context, accountID string, credits []objects.CPAQuotaResetCredit) ([]objects.CPAQuotaResetCredit, error) {
	if len(credits) == 0 {
		return credits, nil
	}
	keys := make([]string, 0, len(credits))
	for _, credit := range credits {
		keys = append(keys, codexCreditKey(accountID, credit.ID))
	}
	claimed, err := svc.claimedCodexCreditKeys(ctx, keys)
	if err != nil {
		return nil, err
	}
	visible := credits[:0]
	for _, credit := range credits {
		if !claimed[codexCreditKey(accountID, credit.ID)] {
			visible = append(visible, credit)
		}
	}
	return visible, nil
}

// filterClaimedResetCredits hides cards already claimed locally, so the API
// never offers a card whose consume was already attempted, whether it succeeded
// or left an uncertain remote outcome.
func (svc *CPAService) filterClaimedResetCredits(ctx context.Context, views ...*CPACredentialView) error {
	keys := make([]string, 0, len(views))
	for _, view := range views {
		if view == nil || view.credential == nil {
			continue
		}
		for _, credit := range view.QuotaData.ResetCredits {
			keys = append(keys, codexCreditKey(view.credential.QuotaContext.CodexAccountID, credit.ID))
		}
	}
	if len(keys) == 0 {
		return nil
	}
	claimed, err := svc.claimedCodexCreditKeys(ctx, keys)
	if err != nil {
		return err
	}
	for _, view := range views {
		if view == nil || view.credential == nil || len(view.QuotaData.ResetCredits) == 0 {
			continue
		}
		accountID := view.credential.QuotaContext.CodexAccountID
		visible := view.QuotaData.ResetCredits[:0]
		for _, credit := range view.QuotaData.ResetCredits {
			if !claimed[codexCreditKey(accountID, credit.ID)] {
				visible = append(visible, credit)
			}
		}
		view.QuotaData.ResetCredits = visible
	}
	return nil
}

func (svc *CPAService) consumeCodexResetCredit(ctx context.Context, client cpaclient.ManagementClient, credential *ent.CPACredential, creditID string) error {
	key := codexCreditKey(credential.QuotaContext.CodexAccountID, creditID)
	attempt, err := svc.entFromContext(ctx).CPACodexResetAttempt.Create().
		SetCreditKey(key).SetCredentialID(credential.ID).Save(ctx)
	if ent.IsConstraintError(err) {
		return fmt.Errorf("Codex reset credit was already attempted")
	}
	if err != nil {
		return fmt.Errorf("claim Codex reset credit: %w", err)
	}
	release, err := svc.quotaExecutor.acquireProviderCall(ctx, credential.CpaInstanceID)
	if err != nil {
		return fmt.Errorf("Codex reset claim retained; provider call unavailable: %w", err)
	}
	defer release()
	consumeErr := cpaclient.ConsumeCodexResetCredit(ctx, client, credential.AuthIndex, credential.QuotaContext.CodexAccountID, creditID, uuid.NewString())
	state := cpacodexresetattempt.StateRedeemed
	if consumeErr != nil {
		state = cpacodexresetattempt.StateUncertain
	}
	// The pending unique row still blocks retries if this update fails.
	_, updateErr := svc.entFromContext(ctx).CPACodexResetAttempt.UpdateOneID(attempt.ID).SetState(state).Save(ctx)
	if consumeErr != nil || updateErr != nil {
		log.Warn(ctx, "CPA Codex reset outcome uncertain", log.Int("credential_id", credential.ID), log.Cause(consumeErr), log.Any("state_update_error", updateErr))
		return fmt.Errorf("Codex reset outcome uncertain; check provider before any further action")
	}
	return nil
}

func (svc *CPAService) ResetCodexCredential(ctx context.Context, credentialID int, creditID string) (bool, error) {
	credential, instance, err := svc.codexResetCredential(ctx, credentialID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(creditID) == "" {
		return false, fmt.Errorf("Codex reset credit ID is empty")
	}
	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return false, err
	}
	defer client.CloseIdleConnections()
	credits, err := svc.liveCodexResetCredits(ctx, client, credential)
	if err != nil {
		return false, err
	}
	// The display hides claimed cards, so the earliest comparison must use the
	// same view; otherwise a claimed card upstream would keep its successor
	// permanently rejected.
	credits, err = svc.unclaimedCodexResetCredits(ctx, credential.QuotaContext.CodexAccountID, credits)
	if err != nil {
		return false, err
	}
	// The confirmation freezes one credit ID; a repeated or stale request must
	// never silently consume a different card.
	if len(credits) == 0 || credits[0].ID != creditID {
		return false, fmt.Errorf("selected Codex reset credit is not the earliest available")
	}
	if err := svc.consumeCodexResetCredit(ctx, client, credential, creditID); err != nil {
		return false, err
	}
	if outcome := svc.refreshCredentialOutcome(ctx, client, credential); outcome.err != nil {
		log.Warn(ctx, "CPA quota refresh after Codex reset failed", log.Int("credential_id", credentialID), log.Cause(outcome.err))
	}
	return true, nil
}
