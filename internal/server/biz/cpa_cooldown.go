package biz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/scopes"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

// CPACredentialCooldownResetOutcome distinguishes an accepted remote write from
// the subsequent refresh, which may fail independently of that write.
type CPACredentialCooldownResetOutcome struct {
	Applied      bool
	Credential   *CPACredentialView
	RefreshError *string
}

// ResetCredentialCooldown clears only this credential's CPA routing cooldown.
// It never resets provider quota or changes the credential's enabled state.
func (svc *CPAService) ResetCredentialCooldown(ctx context.Context, credentialID int) (*CPACredentialCooldownResetOutcome, error) {
	if !scopes.UserHasScope(ctx, scopes.ScopeWriteSettings) {
		return nil, fmt.Errorf("permission denied: requires write:settings scope")
	}

	credential, instance, err := svc.repository.credentialWithInstance(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("get CPA credential for cooldown reset: %w", err)
	}
	if !instance.Enabled {
		return nil, fmt.Errorf("CPA instance is disabled")
	}
	if strings.TrimSpace(credential.AuthIndex) == "" {
		return nil, fmt.Errorf("CPA credential auth index is missing")
	}

	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("open CPA connection for cooldown reset: %s", sanitizeCPAErrorMessage(err.Error()))
	}
	defer client.CloseIdleConnections()

	// Read directly: an older singleflight snapshot cannot authorize a write or
	// select the management API for the credential's current remote identity.
	freshResponse, _, err := client.ListCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("read CPA credential before cooldown reset: %w", cpaCooldownResetRemoteError(err))
	}
	var matched cpaclient.AuthFile
	matches := 0
	for _, file := range freshResponse.Files {
		if strings.TrimSpace(file.AuthIndex) == credential.AuthIndex {
			matched = file
			matches++
		}
	}
	if matches > 1 {
		return nil, fmt.Errorf("CPA credential auth index is ambiguous; refresh credentials first")
	}
	if matches == 0 {
		return nil, fmt.Errorf("CPA credential changed or disappeared; refresh credentials first")
	}
	fresh, err := cpaclient.NormalizeAuthFile(matched)
	if err != nil || fresh.Provider != credential.Provider || fresh.RemoteName != credential.RemoteName {
		return nil, fmt.Errorf("CPA credential changed or disappeared; refresh credentials first")
	}
	cooldown := activeCPARoutingCooldown(fresh.RoutingCooldown, svc.now())
	if !cooldown.Known {
		return nil, fmt.Errorf("CPA does not provide a reliable cooldown snapshot; update CPA or use its management console")
	}

	applied := len(cooldown.Items) > 0
	if applied {
		// Keep this single-shot write outside all instance locks and DB retry
		// closures. An uncertain response must never cause an automatic resend.
		if err := client.ResetCooldown(ctx, credential.AuthIndex, freshResponse.API); err != nil {
			return nil, cpaCooldownResetRemoteError(err)
		}
	}

	view, err := svc.refreshCredentialAfterCooldownReset(ctx, instance, client, credentialID)
	if err != nil {
		message := sanitizeCPAErrorMessage(err.Error())
		if !applied {
			return nil, fmt.Errorf("refresh CPA credential after cooldown reset check: %s", message)
		}
		message = "CPA accepted the cooldown reset, but state refresh failed: " + message
		return &CPACredentialCooldownResetOutcome{
			Applied:      true,
			RefreshError: &message,
		}, nil
	}
	return &CPACredentialCooldownResetOutcome{Applied: applied, Credential: view}, nil
}

func (svc *CPAService) refreshCredentialAfterCooldownReset(ctx context.Context, instance *ent.CPAInstance, client cpaclient.ManagementClient, credentialID int) (*CPACredentialView, error) {
	// Force a new GET even if a pre-write sync is still in flight. Only the
	// normal sync persists the observed state; never optimistically clear it.
	if _, err := svc.syncInstanceCredentialsOnceWithClient(ctx, instance, client); err != nil {
		return nil, fmt.Errorf("sync CPA credentials after cooldown reset: %w", err)
	}
	credential, refreshedInstance, err := svc.repository.credentialWithInstance(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("reload CPA credential after cooldown reset: %w", err)
	}
	view, err := svc.credentialView(ctx, refreshedInstance, credential)
	if err != nil {
		return nil, fmt.Errorf("build CPA credential view after cooldown reset: %w", err)
	}
	return view, nil
}

// cpaCooldownResetRemoteError exposes only classified management failures, not
// upstream bodies, transport URLs, or credentials embedded in arbitrary errors.
func cpaCooldownResetRemoteError(err error) error {
	var managementErr *cpaclient.ManagementHTTPError
	if !errors.As(err, &managementErr) {
		if err.Error() == "CPA cooldown reset response is invalid; refresh state before retrying" {
			return fmt.Errorf("CPA cooldown reset response is invalid; refresh state before retrying (result is unconfirmed)")
		}
		return fmt.Errorf("CPA cooldown reset result is unconfirmed; refresh state before retrying")
	}
	switch managementErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("CPA management authentication failed (HTTP %d)", managementErr.StatusCode)
	case http.StatusNotFound:
		if managementErr.Reason == "auth not found" {
			return fmt.Errorf("CPA credential not found; refresh credentials first")
		}
		return fmt.Errorf("CPA cooldown reset is unsupported by this management API (HTTP 404)")
	case http.StatusMethodNotAllowed:
		return fmt.Errorf("CPA cooldown reset is unsupported by this management API (HTTP 405)")
	case http.StatusServiceUnavailable:
		return fmt.Errorf("CPA management service is unavailable (HTTP 503)")
	default:
		if managementErr.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("CPA service error during cooldown reset (HTTP %d)", managementErr.StatusCode)
		}
		return fmt.Errorf("CPA management rejected the cooldown reset (HTTP %d); refresh state before retrying", managementErr.StatusCode)
	}
}
