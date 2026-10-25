package biz

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/looplj/axonhub/internal/scopes"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

// CredentialModels lists the models CPA currently routes to one credential.
// The registry is live CPA state, so it is read on demand and never stored.
func (svc *CPAService) CredentialModels(ctx context.Context, credentialID int) ([]cpaclient.AuthFileModel, error) {
	if !scopes.UserHasScope(ctx, scopes.ScopeReadSettings) {
		return nil, fmt.Errorf("permission denied: requires read:settings scope")
	}

	credential, instance, err := svc.repository.credentialWithInstance(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("get CPA credential for models: %w", err)
	}
	if !instance.Enabled {
		return nil, fmt.Errorf("CPA instance is disabled")
	}

	client, err := svc.clientForInstance(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("open CPA connection for models: %s", sanitizeCPAErrorMessage(err.Error()))
	}
	defer client.CloseIdleConnections()

	models, err := client.ListAuthFileModels(ctx, credential.RemoteName)
	if err != nil {
		return nil, cpaCredentialModelsError(err)
	}
	return models, nil
}

// cpaCredentialModelsError hides raw transport errors, which may contain URLs
// or credentials, behind stable messages.
func cpaCredentialModelsError(err error) error {
	var managementErr *cpaclient.ManagementHTTPError
	if !errors.As(err, &managementErr) {
		return errors.New("read CPA credential models failed; check the CPA connection")
	}
	switch managementErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("CPA management authentication failed (HTTP %d)", managementErr.StatusCode)
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return fmt.Errorf("CPA credential models are unsupported by this management API (HTTP %d)", managementErr.StatusCode)
	case http.StatusServiceUnavailable:
		return errors.New("CPA management service is unavailable (HTTP 503)")
	default:
		return fmt.Errorf("CPA management rejected the credential models request (HTTP %d)", managementErr.StatusCode)
	}
}
