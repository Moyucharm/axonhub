package biz

import (
	"context"
	"fmt"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

type cpaConnectionProvider struct {
	systemService      *SystemService
	newClient          func(cpaclient.Config) (cpaclient.ManagementClient, error)
	openInstanceClient func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error)
}

func newCPAConnectionProvider(systemService *SystemService) *cpaConnectionProvider {
	return &cpaConnectionProvider{
		systemService: systemService,
		newClient: func(config cpaclient.Config) (cpaclient.ManagementClient, error) {
			return cpaclient.NewClient(config)
		},
	}
}

func (p *cpaConnectionProvider) encryptSecret(ctx context.Context, secret string) (string, error) {
	systemSecret, err := authz.RunWithSystemBypass(ctx, "cpa-encrypt-secret", func(bypassCtx context.Context) (string, error) {
		return p.systemService.SecretKey(bypassCtx)
	})
	if err != nil {
		return "", fmt.Errorf("load system secret for CPA encryption: %w", err)
	}
	return encryptCPASecret(systemSecret, secret)
}

func (p *cpaConnectionProvider) decryptSecret(ctx context.Context, ciphertext string) (string, error) {
	systemSecret, err := authz.RunWithSystemBypass(ctx, "cpa-decrypt-secret", func(bypassCtx context.Context) (string, error) {
		return p.systemService.SecretKey(bypassCtx)
	})
	if err != nil {
		return "", fmt.Errorf("load system secret for CPA decryption: %w", err)
	}
	return decryptCPASecret(systemSecret, ciphertext)
}

func (p *cpaConnectionProvider) openConfig(config cpaclient.Config) (cpaclient.ManagementClient, error) {
	return p.newClient(config)
}

func (p *cpaConnectionProvider) openInstance(ctx context.Context, instance *ent.CPAInstance) (cpaclient.ManagementClient, error) {
	if p.openInstanceClient != nil {
		return p.openInstanceClient(ctx, instance)
	}
	secret, err := p.decryptSecret(ctx, instance.EncryptedSecret)
	if err != nil {
		return nil, err
	}
	return p.openConfig(cpaclient.Config{
		BaseURL:          instance.BaseURL,
		ManagementSecret: secret,
		InsecureSkipTLS:  instance.InsecureSkipTLS,
	})
}

func withCPAConnection[T any](client cpaclient.ManagementClient, fn func(cpaclient.ManagementClient) (T, error)) (T, error) {
	defer client.CloseIdleConnections()
	return fn(client)
}
