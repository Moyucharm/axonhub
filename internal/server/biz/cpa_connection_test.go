package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

func TestCPAConnectionProviderForwardsConfig(t *testing.T) {
	var captured cpaclient.Config
	client := &cpaConnectionTestClient{}
	provider := &cpaConnectionProvider{
		newClient: func(config cpaclient.Config) (cpaclient.ManagementClient, error) {
			captured = config
			return client, nil
		},
	}

	config := cpaclient.Config{
		BaseURL:          "https://cpa.example.test",
		ManagementSecret: "secret",
		InsecureSkipTLS:  true,
	}
	opened, err := provider.openConfig(config)
	require.NoError(t, err)
	require.Same(t, client, opened)
	require.Equal(t, config, captured)
}

func TestWithCPAConnectionClosesExactlyOnce(t *testing.T) {
	tests := []struct {
		name string
		fn   func(cpaclient.ManagementClient) (int, error)
		err  bool
	}{
		{name: "success", fn: func(cpaclient.ManagementClient) (int, error) { return 7, nil }},
		{name: "error", fn: func(cpaclient.ManagementClient) (int, error) { return 0, errors.New("boom") }, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &cpaConnectionTestClient{}
			value, err := withCPAConnection[int](client, tt.fn)
			if tt.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, 7, value)
			}
			require.Equal(t, 1, client.closeCount)
		})
	}

	client := &cpaConnectionTestClient{}
	require.Panics(t, func() {
		_, _ = withCPAConnection[int](client, func(cpaclient.ManagementClient) (int, error) {
			panic("boom")
		})
	})
	require.Equal(t, 1, client.closeCount)
}

type cpaConnectionTestClient struct {
	closeCount int
}

func (client *cpaConnectionTestClient) CloseIdleConnections() { client.closeCount++ }

func (client *cpaConnectionTestClient) ListCredentials(context.Context) (*cpaclient.AuthFilesResponse, cpaclient.BuildInfo, error) {
	return nil, cpaclient.BuildInfo{}, errors.New("unexpected ListCredentials call")
}

func (client *cpaConnectionTestClient) CallProvider(context.Context, cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
	return nil, errors.New("unexpected CallProvider call")
}

func (client *cpaConnectionTestClient) ListUsageQueue(context.Context, int) ([]*cpaclient.UsageEvent, error) {
	return nil, errors.New("unexpected ListUsageQueue call")
}

func (client *cpaConnectionTestClient) PatchAuthFileStatus(context.Context, string, string, bool) error {
	return errors.New("unexpected PatchAuthFileStatus call")
}
