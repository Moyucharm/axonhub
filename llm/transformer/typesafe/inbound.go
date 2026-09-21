package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

// InboundTransformer accepts TypeSafe System One requests while preserving the
// provider-specific state, questions, and extension fields.
type InboundTransformer struct{}

func NewInboundTransformer() *InboundTransformer {
	return &InboundTransformer{}
}

func (t *InboundTransformer) TransformRequest(ctx context.Context, httpReq *httpclient.Request) (*llm.Request, error) {
	if httpReq == nil {
		return nil, fmt.Errorf("%w: http request is nil", transformer.ErrInvalidRequest)
	}
	if len(httpReq.Body) == 0 {
		return nil, fmt.Errorf("%w: request body is empty", transformer.ErrInvalidRequest)
	}
	contentType := httpReq.Headers.Get("Content-Type")
	if contentType != "" && !strings.Contains(strings.ToLower(contentType), "application/json") {
		return nil, fmt.Errorf("%w: unsupported content type: %s", transformer.ErrInvalidRequest, contentType)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(httpReq.Body, &envelope); err != nil || envelope == nil {
		return nil, fmt.Errorf("%w: failed to decode system one request: %v", transformer.ErrInvalidRequest, err)
	}

	var model string
	if raw, ok := envelope["model"]; ok {
		_ = json.Unmarshal(raw, &model)
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("%w: model is required for system one routing", transformer.ErrInvalidRequest)
	}

	return &llm.Request{
		Model:       model,
		Messages:    []llm.Message{},
		RawRequest:  httpReq,
		RequestType: llm.RequestTypeSystemOne,
		APIFormat:   llm.APIFormatTypeSafeSystemOne,
		SystemOne:   &llm.SystemOneRequest{Body: append([]byte(nil), httpReq.Body...)},
	}, nil
}

func (t *InboundTransformer) TransformResponse(ctx context.Context, resp *llm.Response) (*httpclient.Response, error) {
	if resp == nil || resp.SystemOne == nil || len(resp.SystemOne.Body) == 0 {
		return nil, fmt.Errorf("system one response is empty")
	}

	return &httpclient.Response{
		StatusCode: http.StatusOK,
		Body:       append([]byte(nil), resp.SystemOne.Body...),
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func (t *InboundTransformer) TransformStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return nil, fmt.Errorf("%w: system one does not support streaming", transformer.ErrInvalidRequest)
}

func (t *InboundTransformer) AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, fmt.Errorf("system one does not support streaming")
}

func (t *InboundTransformer) TransformError(ctx context.Context, rawErr error) *httpclient.Error {
	statusCode := http.StatusInternalServerError
	detail := llm.ErrorDetail{Message: http.StatusText(statusCode), Type: "api_error"}

	var respErr *llm.ResponseError
	if errors.As(rawErr, &respErr) && respErr != nil {
		if respErr.StatusCode >= 400 && respErr.StatusCode <= 599 {
			statusCode = respErr.StatusCode
		}
		detail = respErr.Detail
		if detail.Message == "" {
			detail.Message = http.StatusText(statusCode)
		}
		if detail.Type == "" {
			detail.Type = "api_error"
		}
	}

	body, err := json.Marshal(map[string]any{"error": detail})
	if err != nil {
		body = []byte(`{"error":{"message":"Internal Server Error","type":"api_error"}}`)
		statusCode = http.StatusInternalServerError
	}

	return &httpclient.Error{StatusCode: statusCode, Body: body}
}
