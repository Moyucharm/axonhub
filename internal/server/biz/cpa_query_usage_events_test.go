package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
)

func TestQueryCPAUsageEventsPaginatesNewestFirst(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:cpa_usage_events_query?mode=memory&_fk=1")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	svc := &CPAService{AbstractService: &AbstractService{db: client}}
	instance, err := client.CPAInstance.Create().
		SetName("Primary CPA").
		SetBaseURL("http://127.0.0.1:8317").
		SetEncryptedSecret("encrypted").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.CPACredential.Create().
		SetCpaInstanceID(instance.ID).
		SetExternalKey("a1").
		SetAuthIndex("a1").
		SetRemoteName("a1.json").
		SetEmail("u@x").
		SetDisplayName("Display").
		SetProvider("codex").
		Save(ctx)
	require.NoError(t, err)

	older := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Minute)
	oldest, err := client.CpaUsageEvent.Create().
		SetCpaInstanceID(instance.ID).SetAuthIndex("a1").SetModel("gpt-6-astra").SetRequestedAt(older.Add(-time.Minute)).
		Save(ctx)
	require.NoError(t, err)
	tiedLow, err := client.CpaUsageEvent.Create().
		SetCpaInstanceID(instance.ID).SetAuthIndex("missing").SetModel("gpt-6-astra").SetRequestedAt(newer).
		Save(ctx)
	require.NoError(t, err)
	tiedHigh, err := client.CpaUsageEvent.Create().
		SetCpaInstanceID(instance.ID).SetAuthIndex("a1").
		SetModel("gpt-6-astra").SetResponseModel("gpt-5.6-luna").SetRequestedAt(newer).
		Save(ctx)
	require.NoError(t, err)

	first, err := svc.QueryUsageEvents(ctx, QueryCPAUsageEventsInput{InstanceID: instance.ID, First: 2})
	require.NoError(t, err)
	require.Equal(t, 3, first.TotalCount)
	require.True(t, first.PageInfo.HasNextPage)
	require.Len(t, first.Edges, 2)
	require.Equal(t, tiedHigh.ID, first.Edges[0].Node.ID)
	require.Equal(t, tiedLow.ID, first.Edges[1].Node.ID)
	require.Equal(t, "gpt-5.6-luna", first.Edges[0].Node.ResponseModel)
	require.Equal(t, "u@x", first.Edges[0].Node.CredentialName)
	require.Empty(t, first.Edges[1].Node.CredentialName)

	second, err := svc.QueryUsageEvents(ctx, QueryCPAUsageEventsInput{
		InstanceID: instance.ID,
		First:      2,
		After:      first.PageInfo.EndCursor,
	})
	require.NoError(t, err)
	require.False(t, second.PageInfo.HasNextPage)
	require.Len(t, second.Edges, 1)
	require.Equal(t, oldest.ID, second.Edges[0].Node.ID)
	require.Equal(t, "u@x", second.Edges[0].Node.CredentialName)
}
