package biz

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/scopes"
)

func TestCPAPrivacyRejectsCredentialIDORWithoutSettingsScope(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_idor?mode=memory&_fk=1")
	defer client.Close()

	seedCtx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	instanceA := client.CPAInstance.Create().
		SetName("CPA A").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted-a").
		SaveX(seedCtx)
	instanceB := client.CPAInstance.Create().
		SetName("CPA B").
		SetBaseURL("http://127.0.0.1:8318").
		SetEncryptedSecret("encrypted-b").
		SaveX(seedCtx)
	credentialA := client.CPACredential.Create().
		SetCpaInstanceID(instanceA.ID).
		SetExternalKey("a-key").
		SetRemoteName("a.json").
		SetDisplayName("A credential").
		SaveX(seedCtx)
	credentialB := client.CPACredential.Create().
		SetCpaInstanceID(instanceB.ID).
		SetExternalKey("b-key").
		SetRemoteName("b.json").
		SetDisplayName("B credential").
		SaveX(seedCtx)

	user := &ent.User{ID: 42, Scopes: []string{}}
	userCtx := authz.NewUserContext(ent.NewContext(t.Context(), client), user.ID)
	userCtx = contexts.WithUser(userCtx, user)

	_, err := client.CPAInstance.Get(userCtx, instanceA.ID)
	require.Error(t, err, "a user without read_settings must not retrieve a CPA instance by ID")
	_, err = client.CPACredential.Get(userCtx, credentialB.ID)
	require.Error(t, err, "a user without read_settings must not retrieve another credential by ID")
	_, err = client.CPACredential.UpdateOneID(credentialA.ID).
		SetDisabled(true).
		Save(userCtx)
	require.Error(t, err, "a user without write_settings must not mutate a credential by ID")

	// The CPA resources are system settings, not user-owned resources. A user
	// granted the settings scope is intentionally allowed to access all CPA
	// instances; the instance filter must still prevent cross-instance mixing.
	user.Scopes = []string{string(scopes.ScopeReadSettings)}
	readCtx := authz.NewUserContext(ent.NewContext(t.Context(), client), user.ID)
	readCtx = contexts.WithUser(readCtx, user)
	query := client.CPACredential.Query().Where(cpacredential.CpaInstanceIDEQ(instanceA.ID))
	credentials, err := query.All(readCtx)
	require.NoError(t, err)
	require.Len(t, credentials, 1)
	require.Equal(t, credentialA.ID, credentials[0].ID)
	require.NotEqual(t, credentialB.ID, credentials[0].ID)
}
