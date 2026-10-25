package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPACredentialModelsUsesRemoteName(t *testing.T) {
	f := newCPACooldownTestFixture(t)
	var requested string
	f.management.listAuthFileModels = func(_ context.Context, name string) ([]cpaclient.AuthFileModel, error) {
		requested = name
		return []cpaclient.AuthFileModel{{ID: "claude-sonnet-4-5", OwnedBy: "anthropic"}}, nil
	}

	models, err := f.svc.CredentialModels(f.ctx, f.credential.ID)
	require.NoError(t, err)
	require.Equal(t, f.credential.RemoteName, requested)
	require.Equal(t, []cpaclient.AuthFileModel{{ID: "claude-sonnet-4-5", OwnedBy: "anthropic"}}, models)
}

func TestCPACredentialModelsRequiresReadScope(t *testing.T) {
	f := newCPACooldownTestFixture(t)
	ctx := cpaCooldownUserContext(t.Context(), f.client, nil)

	_, err := f.svc.CredentialModels(ctx, f.credential.ID)
	require.ErrorContains(t, err, "permission denied")
	require.Zero(t, f.opens.Load())
}

func TestCPACredentialModelsHidesRawErrors(t *testing.T) {
	f := newCPACooldownTestFixture(t)
	f.management.listAuthFileModels = func(context.Context, string) ([]cpaclient.AuthFileModel, error) {
		return nil, errors.New("dial http://user:" + cpaCooldownTestSecret + "@cpa failed")
	}
	_, err := f.svc.CredentialModels(f.ctx, f.credential.ID)
	require.Error(t, err)
	require.NotContains(t, err.Error(), cpaCooldownTestSecret)

	f.management.listAuthFileModels = func(context.Context, string) ([]cpaclient.AuthFileModel, error) {
		return nil, &cpaclient.ManagementHTTPError{StatusCode: 404}
	}
	_, err = f.svc.CredentialModels(f.ctx, f.credential.ID)
	require.ErrorContains(t, err, "unsupported")
}
