package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cparesetattempt"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

const claudeTestOrganization = "dd7c7225-448e-4700-8e74-8dc46986bcb1"

// claudeResetProvider simulates the Claude account behind CPA: each spent use
// lowers resets_left, and a repeated request ID is deduplicated.
type claudeResetProvider struct {
	resetsLeft  int
	endsAt      time.Time
	requireLim  bool
	claimReply  func(requestID string) (*cpaclient.ProviderCallResult, error)
	posts       []string
	spentByID   map[string]bool
	usageCalled int
}

func (p *claudeResetProvider) call(_ context.Context, call cpaclient.ProviderCall) (*cpaclient.ProviderCallResult, error) {
	if call.Headers["User-Agent"] == "" {
		return nil, fmt.Errorf("missing Claude CLI user agent")
	}
	switch {
	case strings.Contains(call.URL, "/api/oauth/profile"):
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"organization":{"uuid":"` + claudeTestOrganization + `"}}`)}, nil
	case strings.Contains(call.URL, "/api/oauth/usage"):
		p.usageCalled++
		body := fmt.Sprintf(`{"five_hour":{"utilization":4,"resets_at":"2099-01-01T00:00:00Z"},"cedar_ember":{"eligible":true,"at_limit":false,"next_grant_id":"launch","grants":[{"id":"launch","label":"Launch","resets_total":2,"resets_left":%d,"ends_at":%q,"clears":["five_hour","seven_day"],"paused":false,"usable_now":true,"use_requires_limit":%t}]}}`,
			p.resetsLeft, p.endsAt.Format(time.RFC3339), p.requireLim)
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(body)}, nil
	case strings.HasSuffix(call.URL, "/api/organizations/"+claudeTestOrganization+"/reset_rate_limits"):
		var payload struct {
			Program   string `json:"program"`
			GrantID   string `json:"grant_id"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal([]byte(call.Body), &payload); err != nil || payload.Program != "cedar_ember" || payload.GrantID != "launch" {
			return nil, fmt.Errorf("unexpected claim body %q", call.Body)
		}
		p.posts = append(p.posts, payload.RequestID)
		if p.claimReply != nil {
			return p.claimReply(payload.RequestID)
		}
		return p.spend(payload.RequestID), nil
	}
	return nil, fmt.Errorf("unexpected provider call %s", call.URL)
}

func (p *claudeResetProvider) spend(requestID string) *cpaclient.ProviderCallResult {
	if p.spentByID == nil {
		p.spentByID = map[string]bool{}
	}
	if p.spentByID[requestID] {
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"result":"already_used"}`)}
	}
	p.spentByID[requestID] = true
	p.resetsLeft--
	return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"result":"reset"}`)}
}

func newClaudeResetFixture(t *testing.T, name string, now *time.Time, provider *claudeResetProvider) (context.Context, *ent.Client, *CPAService, *ent.CPAInstance, *ent.CPACredential) {
	t.Helper()
	client := enttest.NewEntClient(t, "sqlite3", "file:"+name+"?mode=memory&_fk=0")
	t.Cleanup(func() { _ = client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	svc := newCPAServiceForTest(client, func() time.Time { return *now })
	instance := client.CPAInstance.Create().SetName(name).SetBaseURL("http://127.0.0.1:8317").SetEncryptedSecret("encrypted").SaveX(ctx)
	credential := client.CPACredential.Create().SetCpaInstanceID(instance.ID).SetExternalKey("claude").SetRemoteName("claude.json").
		SetDisplayName("Claude").SetProvider("claude").SetAuthIndex("auth").SaveX(ctx)
	management := &cpaTestManagementClient{callProvider: provider.call}
	svc.connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) { return management, nil }
	require.NoError(t, svc.refreshCredentialOutcome(ctx, management, credential).err)
	return ctx, client, svc, instance, client.CPACredential.GetX(ctx, credential.ID)
}

func claudeGrantView(t *testing.T, ctx context.Context, svc *CPAService, instanceID int) objects.CPAClaudeResetGrant {
	t.Helper()
	connection, err := svc.QueryCredentials(ctx, QueryCPACredentialsInput{InstanceID: instanceID, First: 10})
	require.NoError(t, err)
	require.Len(t, connection.Edges, 1)
	status := connection.Edges[0].Node.QuotaData.ClaudeReset
	require.NotNil(t, status)
	require.Len(t, status.Grants, 1)
	return status.Grants[0]
}

func TestCPAClaudeResetSpendsEachUseOnce(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	provider := &claudeResetProvider{resetsLeft: 2, endsAt: now.Add(48 * time.Hour)}
	ctx, client, svc, instance, credential := newClaudeResetFixture(t, "claude_reset_once", &now, provider)

	outcome, err := svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.NoError(t, err)
	require.Equal(t, objects.CPAClaudeResetResultReset, *outcome.Result)
	require.False(t, outcome.Uncertain)
	require.Equal(t, 1, claudeGrantView(t, ctx, svc, instance.ID).ResetsLeft)

	// The second use is a distinct key and goes through; the third finds none left.
	_, err = svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.NoError(t, err)
	_, err = svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.ErrorContains(t, err, "exhausted")
	require.Len(t, provider.posts, 2)
	require.Equal(t, 2, client.CPAResetAttempt.Query().Where(cparesetattempt.StateEQ(cparesetattempt.StateRedeemed)).CountX(ctx))
}

func TestCPAClaudeResetRefusalReleasesTheUse(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	provider := &claudeResetProvider{resetsLeft: 1, endsAt: now.Add(48 * time.Hour)}
	provider.claimReply = func(string) (*cpaclient.ProviderCallResult, error) {
		return &cpaclient.ProviderCallResult{StatusCode: 200, Body: []byte(`{"result":"cooldown"}`)}, nil
	}
	ctx, client, svc, _, credential := newClaudeResetFixture(t, "claude_reset_refusal", &now, provider)

	outcome, err := svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.NoError(t, err)
	require.Equal(t, objects.CPAClaudeResetResultCooldown, *outcome.Result)
	// A settled refusal proves nothing was spent, so the use stays claimable.
	require.Zero(t, client.CPAResetAttempt.Query().CountX(ctx))
	provider.claimReply = nil
	outcome, err = svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.NoError(t, err)
	require.Equal(t, objects.CPAClaudeResetResultReset, *outcome.Result)
}

func TestCPAClaudeResetUncertainRetriesWithOriginalRequest(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	provider := &claudeResetProvider{resetsLeft: 2, endsAt: now.Add(48 * time.Hour)}
	// The first claim is spent upstream but its answer is lost.
	provider.claimReply = func(requestID string) (*cpaclient.ProviderCallResult, error) {
		provider.spend(requestID)
		return nil, errors.New("tunnel dropped")
	}
	ctx, client, svc, instance, credential := newClaudeResetFixture(t, "claude_reset_uncertain", &now, provider)

	outcome, err := svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.NoError(t, err)
	require.Nil(t, outcome.Result)
	require.True(t, outcome.Uncertain)
	require.NotNil(t, outcome.RetryUntil)

	// The post-claim refresh already saw resets_left drop and settled the claim.
	require.Equal(t, cparesetattempt.StateRedeemed, client.CPAResetAttempt.Query().OnlyX(ctx).State)
	grant := claudeGrantView(t, ctx, svc, instance.ID)
	require.False(t, grant.Uncertain)
	require.Equal(t, 1, grant.ResetsLeft)
}

func TestCPAClaudeResetUncertainRetryWindow(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	provider := &claudeResetProvider{resetsLeft: 2, endsAt: now.Add(48 * time.Hour)}
	// Lost before reaching the provider: nothing is spent, the outcome is unknown.
	provider.claimReply = func(string) (*cpaclient.ProviderCallResult, error) { return nil, errors.New("tunnel dropped") }
	ctx, client, svc, instance, credential := newClaudeResetFixture(t, "claude_reset_retry", &now, provider)

	_, err := svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.NoError(t, err)
	grant := claudeGrantView(t, ctx, svc, instance.ID)
	require.True(t, grant.Uncertain)
	require.NotNil(t, grant.RetryUntil)

	// A retry inside the window reuses the original request ID.
	provider.claimReply = nil
	outcome, err := svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.NoError(t, err)
	require.Equal(t, objects.CPAClaudeResetResultReset, *outcome.Result)
	require.Len(t, provider.posts, 2)
	require.Equal(t, provider.posts[0], provider.posts[1])
	require.Equal(t, cparesetattempt.StateRedeemed, client.CPAResetAttempt.Query().OnlyX(ctx).State)

	// Once the retry window passes, an unknown claim stays locked.
	provider.claimReply = func(string) (*cpaclient.ProviderCallResult, error) { return nil, errors.New("tunnel dropped") }
	_, err = svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.NoError(t, err)
	now = now.Add(claudeResetRetryWindow + time.Second)
	_, err = svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.ErrorContains(t, err, "retry window expired")
	require.Len(t, provider.posts, 3)
	grant = claudeGrantView(t, ctx, svc, instance.ID)
	require.True(t, grant.Uncertain)
	require.Nil(t, grant.RetryUntil)
}

func TestCPAClaudeResetRejectsGrantThatNeedsALimit(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	provider := &claudeResetProvider{resetsLeft: 1, endsAt: now.Add(48 * time.Hour), requireLim: true}
	ctx, _, svc, _, credential := newClaudeResetFixture(t, "claude_reset_needs_limit", &now, provider)

	_, err := svc.ResetClaudeCredential(ctx, credential.ID, "launch")
	require.ErrorContains(t, err, "not_limited")
	require.Empty(t, provider.posts)
}

func TestCPAClaudeAutoResetUsesOnlyExpiringGrants(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	provider := &claudeResetProvider{resetsLeft: 1, endsAt: now.Add(31 * time.Minute)}
	ctx, client, svc, instance, _ := newClaudeResetFixture(t, "claude_reset_auto", &now, provider)
	instance = client.CPAInstance.UpdateOneID(instance.ID).SetAutoManageEnabled(true).SetAutoResetEnabled(true).SaveX(ctx)

	svc.autoResetClaudeInstance(ctx, instance)
	require.Empty(t, provider.posts)

	now = now.Add(2 * time.Minute)
	svc.autoResetClaudeInstance(ctx, instance)
	require.Len(t, provider.posts, 1)
	svc.autoResetClaudeInstance(ctx, instance)
	require.Len(t, provider.posts, 1)
}
