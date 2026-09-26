package biz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
	"github.com/stretchr/testify/require"
)

func TestCPACodexClaimAcrossInstancesAndServices(t *testing.T) {
	clientA := enttest.NewEntClient(t, "sqlite3", "file:cpa_reset_claim?mode=memory&cache=shared&_fk=1&_busy_timeout=5000")
	defer clientA.Close()
	clientB := enttest.NewEntClient(t, "sqlite3", "file:cpa_reset_claim?mode=memory&cache=shared&_fk=1&_busy_timeout=5000")
	defer clientB.Close()
	ctxA := authz.WithTestBypass(ent.NewContext(t.Context(), clientA))
	ctxB := authz.WithTestBypass(ent.NewContext(t.Context(), clientB))
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			URL  string `json:"url"`
			Data string `json:"data"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&call))
		body := `{"credits":[{"id":"shared","status":"available","expires_at":"2099-01-01T00:10:00Z"},{"id":"next","status":"available","expires_at":"2099-01-01T00:20:00Z"}]}`
		if call.URL == "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume" {
			var payload map[string]string
			require.NoError(t, json.Unmarshal([]byte(call.Data), &payload))
			require.Equal(t, "shared", payload["credit_id"])
			posts.Add(1)
			body = `{"code":"reset","credit":{"id":"shared","status":"redeemed"}}`
		} else if call.URL == "https://chatgpt.com/backend-api/wham/usage" {
			body = `{"rate_limit":{"primary_window":{"used_percent":10}}}`
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": body}))
	}))
	defer server.Close()
	now := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	services := []*CPAService{newCPAServiceForTest(clientA, func() time.Time { return now }), newCPAServiceForTest(clientB, func() time.Time { return now })}
	contexts := []context.Context{ctxA, ctxB}
	ids := make([]int, 2)
	for i := range services {
		instance := clientA.CPAInstance.Create().SetName(string(rune('a' + i))).SetBaseURL(server.URL + "/" + string(rune('a'+i))).SetEncryptedSecret("encrypted").SaveX(ctxA)
		cred := clientA.CPACredential.Create().SetCpaInstanceID(instance.ID).SetExternalKey("codex").SetRemoteName("codex.json").SetDisplayName("Codex").SetProvider("codex").SetAuthIndex("auth").SetQuotaContext(objects.CPAQuotaContext{CodexAccountID: "same-account"}).SaveX(ctxA)
		ids[i] = cred.ID
		services[i].connections.openInstanceClient = func(context.Context, *ent.CPAInstance) (cpaclient.ManagementClient, error) {
			return cpaclient.NewClient(cpaclient.Config{BaseURL: server.URL, ManagementSecret: "secret"})
		}
	}
	var wg sync.WaitGroup
	for i := range services {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, _ = services[i].ResetCodexCredential(contexts[i], ids[i], "shared") }(i)
	}
	wg.Wait()
	require.Equal(t, int32(1), posts.Load())
	for i := range services {
		_, err := services[i].ResetCodexCredential(contexts[i], ids[i], "shared")
		require.Error(t, err)
	}
	require.Equal(t, int32(1), posts.Load())
}
