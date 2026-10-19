package gql

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/middleware"
)

// TestAdminGraphqlServiceAccountExecutionVisibility checks that a service
// account key cannot read another project's request executions through the
// channel.executions edge, while its own project's executions stay readable.
func TestAdminGraphqlServiceAccountExecutionVisibility(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	t.Cleanup(func() { _ = db.Close() })

	setupCtx := authz.WithTestBypass(ent.NewContext(context.Background(), db))

	hashed, err := biz.HashPassword("test-password")
	require.NoError(t, err)

	owner := db.User.Create().
		SetEmail(fmt.Sprintf("owner-%d@example.com", time.Now().UnixNano())).
		SetPassword(hashed).
		SetFirstName("Owner").
		SetLastName("User").
		SetIsOwner(true).
		SetStatus(user.StatusActivated).
		SaveX(setupCtx)

	projA := db.Project.Create().SetName("project-a").SetStatus(project.StatusActive).SaveX(setupCtx)
	projB := db.Project.Create().SetName("project-b").SetStatus(project.StatusActive).SaveX(setupCtx)

	ch := db.Channel.Create().
		SetName("shared").
		SetType(channel.TypeOpenai).
		SetBaseURL("https://shared.example").
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-shared"}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SaveX(setupCtx)

	newExecution := func(projectID int, marker string) {
		req := db.Request.Create().
			SetProjectID(projectID).
			SetModelID("gpt-4").
			SetChannelID(ch.ID).
			SetRequestBody([]byte(`{}`)).
			SetStatus(request.StatusCompleted).
			SaveX(setupCtx)
		db.RequestExecution.Create().
			SetProjectID(projectID).
			SetRequestID(req.ID).
			SetChannelID(ch.ID).
			SetModelID("gpt-4").
			SetRequestBody([]byte(`{}`)).
			SetRequestURL("https://shared.example/" + marker).
			SetRequestHeaders(objects.JSONRawMessage(`{"X-Marker":"` + marker + `"}`)).
			SetStatus(requestexecution.StatusCompleted).
			SaveX(setupCtx)
	}

	newExecution(projA.ID, "EXEC-PROJECT-A")
	newExecution(projB.ID, "EXEC-PROJECT-B")

	mustKey := func(name string, keyScopes []string) string {
		value, err := biz.GenerateAPIKey("ah")
		require.NoError(t, err)

		db.APIKey.Create().
			SetName(name).
			SetKey(value).
			SetUserID(owner.ID).
			SetProjectID(projA.ID).
			SetType(apikey.TypeServiceAccount).
			SetStatus(apikey.StatusEnabled).
			SetScopes(keyScopes).
			SaveX(setupCtx)

		return value
	}

	channelsOnlyKey := mustKey("channels-only", []string{string(scopes.ScopeReadChannels)})
	requestsKey := mustKey("channels-and-requests", []string{string(scopes.ScopeReadChannels), string(scopes.ScopeWriteRequests)})

	channelSvc := biz.NewChannelServiceForTest(db)
	t.Cleanup(channelSvc.Stop)

	cacheCfg := xcache.Config{Mode: xcache.ModeMemory}
	projectSvc := &biz.ProjectService{
		ProjectCache: xcache.NewFromConfig[xcache.Entry[ent.Project]](cacheCfg),
	}
	apiKeySvc := biz.NewAPIKeyService(biz.APIKeyServiceParams{
		CacheConfig: cacheCfg, Ent: db, ProjectService: projectSvc, KeyPrefix: "ah",
	})
	t.Cleanup(apiKeySvc.Stop)
	systemSvc := biz.NewSystemService(biz.SystemServiceParams{CacheConfig: cacheCfg, Ent: db})
	require.NoError(t, systemSvc.SetSecretKey(setupCtx, "test-admin-graphql-secret"))
	authSvc := biz.NewAuthService(biz.AuthServiceParams{
		SystemService: systemSvc, APIKeyService: apiKeySvc, Ent: db,
	})

	handler := NewGraphqlHandlers(Dependencies{Ent: db, ChannelService: channelSvc})

	engine := gin.New()
	engine.Use(middleware.WithEntClient(db))
	engine.POST("/admin/graphql", middleware.WithAdminGraphqlAuth(authSvc), func(c *gin.Context) {
		handler.Graphql.ServeHTTP(c.Writer, c.Request)
	})

	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)

	const executions = `query { channels(first: 10) { edges { node { name
		executions(first: 10) { edges { node { requestURL requestHeaders } } } } } } }`

	query := func(t *testing.T, bearer string) string {
		t.Helper()

		code, body := adminGraphqlPost(t, server.URL, bearer, executions)
		require.Equal(t, http.StatusOK, code, "body: %s", body)

		return string(body)
	}

	// Same as channel.requests: the edge is denied, so the whole query is forbidden.
	t.Run("read_channels alone cannot read any execution", func(t *testing.T) {
		body := query(t, channelsOnlyKey)
		require.Contains(t, body, "FORBIDDEN", body)
		require.NotContains(t, body, "EXEC-PROJECT-A")
		require.NotContains(t, body, "EXEC-PROJECT-B")
	})

	t.Run("write_requests only reads executions of the key's own project", func(t *testing.T) {
		body := query(t, requestsKey)
		require.NotContains(t, body, `"errors"`, body)
		require.Contains(t, body, "EXEC-PROJECT-A")
		require.NotContains(t, body, "EXEC-PROJECT-B")
	})
}
