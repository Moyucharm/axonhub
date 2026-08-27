package gql

import (
	"context"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func TestGraphQLOperationTimeout_SelectsTimeoutByRootField(t *testing.T) {
	timeouts := OperationTimeouts{
		RequestTimeout:    5 * time.Second,
		LLMRequestTimeout: 30 * time.Second,
	}

	tests := []struct {
		name     string
		query    string
		expected time.Duration
	}{
		{
			name:     "ordinary query uses admin timeout",
			query:    `query Projects { projects { totalCount } }`,
			expected: timeouts.RequestTimeout,
		},
		{
			name:     "channel test uses LLM timeout",
			query:    `mutation TestChannel { testChannel(input: {channelID: "1"}) { success } }`,
			expected: timeouts.LLMRequestTimeout,
		},
		{
			name:     "all API key test uses LLM timeout",
			query:    `mutation TestKeys { testChannelAPIKeys(channelID: "1") { total } }`,
			expected: timeouts.LLMRequestTimeout,
		},
		{
			name:     "single API key test uses LLM timeout",
			query:    `mutation TestKey { testChannelAPIKey(channelID: "1", key: "key") { success } }`,
			expected: timeouts.LLMRequestTimeout,
		},
		{
			name:     "API key health check uses LLM timeout",
			query:    `mutation CheckKeys { checkChannelAPIKeys(channelID: "1", status: ALL) { success } }`,
			expected: timeouts.LLMRequestTimeout,
		},
		{
			name:     "aliased fragment field uses LLM timeout",
			query:    `mutation TestViaFragment { ...ChannelTests } fragment ChannelTests on Mutation { result: testChannel(input: {channelID: "1"}) { success } }`,
			expected: timeouts.LLMRequestTimeout,
		},
		{
			name:     "mixed operation uses LLM timeout",
			query:    `mutation Mixed { updateChannelStatus(id: "1", status: enabled) { id } testChannel(input: {channelID: "1"}) { success } }`,
			expected: timeouts.LLMRequestTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := operationContext(t, tt.query)
			require.Equal(t, tt.expected, operationTimeout(ctx, timeouts))
		})
	}
}

func TestGraphQLOperationTimeout_PropagatesDeadlineThroughResponse(t *testing.T) {
	timeouts := OperationTimeouts{RequestTimeout: time.Second, LLMRequestTimeout: time.Minute}
	ctx := operationContext(t, `query Projects { projects { totalCount } }`)

	var operationCtx context.Context
	responseHandler := withOperationTimeouts(timeouts)(ctx, func(ctx context.Context) graphql.ResponseHandler {
		operationCtx = ctx
		return func(ctx context.Context) *graphql.Response {
			operationDeadline, operationOK := operationCtx.Deadline()
			responseDeadline, responseOK := ctx.Deadline()
			require.True(t, operationOK)
			require.True(t, responseOK)
			require.Equal(t, operationDeadline, responseDeadline)
			return &graphql.Response{}
		}
	})

	require.NotNil(t, responseHandler(context.Background()))
	select {
	case <-operationCtx.Done():
		require.ErrorIs(t, operationCtx.Err(), context.Canceled)
	default:
		t.Fatal("operation context was not canceled after the response completed")
	}
}

func operationContext(t *testing.T, query string) context.Context {
	t.Helper()

	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	require.NoError(t, err)
	require.Len(t, doc.Operations, 1)

	opCtx := &graphql.OperationContext{
		RawQuery:  query,
		Variables: map[string]any{},
		Doc:       doc,
		Operation: doc.Operations[0],
	}
	return graphql.WithOperationContext(context.Background(), opCtx)
}
