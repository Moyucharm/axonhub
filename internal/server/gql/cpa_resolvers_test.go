package gql

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestCPAOverviewResolverRequiresReadSettings(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_overview_resolver?mode=memory&_fk=0")
	defer client.Close()
	seedCtx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instance := client.CPAInstance.Create().
		SetName("overview").
		SetBaseURL("http://127.0.0.1:8321").
		SetEncryptedSecret("encrypted").
		SaveX(seedCtx)
	client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("codex:overview").
		SetRemoteName("codex.json").
		SetDisplayName("Overview credential").
		SetProvider("codex").
		SetPlanType("plus").
		SetStatus("active").
		SetQuotaState(string(objects.CPAQuotaStateSuccess)).
		SetHealthState(string(objects.CPACredentialHealthHealthy)).
		SetProjectionVersion(1).
		SaveX(seedCtx)

	resolver := &queryResolver{&Resolver{cpaService: biz.NewCPAService(biz.CPAServiceParams{Ent: client})}}
	user := &ent.User{ID: 42}
	ctx := authz.NewUserContext(ent.NewContext(t.Context(), client), user.ID)
	ctx = contexts.WithUser(ctx, user)

	_, err := resolver.CpaOverview(ctx, instance.ID)
	require.ErrorContains(t, err, "permission denied: requires read:settings scope")

	user.Scopes = []string{string(scopes.ScopeReadSettings)}
	overview, err := resolver.CpaOverview(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, &biz.CPACredentialStats{Available: 1, Total: 1, Abnormal: 0}, overview.Stats)
	require.Equal(t, []*biz.CPAProviderOverview{{Provider: "codex", Count: 1, PlanTypes: []string{"plus"}}}, overview.Providers)
}
