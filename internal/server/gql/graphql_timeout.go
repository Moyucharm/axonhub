package gql

import (
	"context"
	"time"

	"github.com/99designs/gqlgen/graphql"
)

// OperationTimeouts configures the short administrative timeout and the longer
// timeout reserved for GraphQL operations that perform real LLM requests.
type OperationTimeouts struct {
	RequestTimeout    time.Duration
	LLMRequestTimeout time.Duration
}

var llmOperationFields = map[string]struct{}{
	"checkChannelAPIKeys": {},
	"testChannel":         {},
	"testChannelAPIKey":   {},
	"testChannelAPIKeys":  {},
}

func operationTimeout(ctx context.Context, timeouts OperationTimeouts) time.Duration {
	opCtx := graphql.GetOperationContext(ctx)
	for _, field := range graphql.CollectFields(opCtx, opCtx.Operation.SelectionSet, nil) {
		if _, ok := llmOperationFields[field.Name]; ok {
			return timeouts.LLMRequestTimeout
		}
	}

	return timeouts.RequestTimeout
}

func withOperationTimeouts(timeouts OperationTimeouts) graphql.OperationMiddleware {
	return func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		timeout := operationTimeout(ctx, timeouts)
		if timeout <= 0 {
			return next(ctx)
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		responseHandler := next(timeoutCtx)

		return func(context.Context) *graphql.Response {
			defer cancel()
			return responseHandler(timeoutCtx)
		}
	}
}
