package typesafe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

type Config struct {
	BaseURL        string              `json:"base_url,omitempty"`
	EndpointPath   string              `json:"endpoint_path,omitempty"`
	APIKeyProvider auth.APIKeyProvider `json:"-"`
}

type OutboundTransformer struct {
	config *Config
}

func NewOutboundTransformerWithConfig(config *Config) (*OutboundTransformer, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if config.BaseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if config.APIKeyProvider == nil {
		return nil, fmt.Errorf("API key provider is required")
	}

	return &OutboundTransformer{config: config}, nil
}

func (t *OutboundTransformer) APIFormat() llm.APIFormat {
	return llm.APIFormatTypeSafeSystemOne
}

func (t *OutboundTransformer) TransformRequest(ctx context.Context, llmReq *llm.Request) (*httpclient.Request, error) {
	if llmReq == nil || llmReq.SystemOne == nil || len(llmReq.SystemOne.Body) == 0 {
		return nil, fmt.Errorf("%w: system one request is nil", transformer.ErrInvalidRequest)
	}
	if llmReq.RequestType != llm.RequestTypeSystemOne {
		return nil, fmt.Errorf("%w: request type %q is not supported", transformer.ErrInvalidRequest, llmReq.RequestType)
	}

	body, err := sjson.SetBytes(llmReq.SystemOne.Body, "model", llmReq.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to patch system one model: %w", err)
	}

	return &httpclient.Request{
		Method: http.MethodPost,
		URL: transformer.BuildRequestURL(
			t.config.BaseURL,
			"v1",
			"/systemone",
			t.config.EndpointPath,
			false,
		),
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
			"Accept":       []string{"application/json"},
		},
		Body:        body,
		Auth:        &httpclient.AuthConfig{Type: httpclient.AuthTypeBearer, APIKey: t.config.APIKeyProvider.Get(ctx)},
		RequestType: llm.RequestTypeSystemOne.String(),
		APIFormat:   llm.APIFormatTypeSafeSystemOne.String(),
	}, nil
}

func (t *OutboundTransformer) TransformResponse(ctx context.Context, httpResp *httpclient.Response) (*llm.Response, error) {
	if httpResp == nil {
		return nil, fmt.Errorf("http response is nil")
	}
	if httpResp.StatusCode >= 400 {
		return nil, t.TransformError(ctx, &httpclient.Error{StatusCode: httpResp.StatusCode, Body: httpResp.Body})
	}
	if len(httpResp.Body) == 0 {
		return nil, fmt.Errorf("system one response body is empty")
	}

	var payload struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(httpResp.Body, &payload); err != nil {
		return nil, fmt.Errorf("%w: failed to decode system one response: %v", transformer.ErrInvalidResponse, err)
	}

	resp := &llm.Response{
		Model:       payload.Model,
		RequestType: llm.RequestTypeSystemOne,
		APIFormat:   llm.APIFormatTypeSafeSystemOne,
		SystemOne:   &llm.SystemOneResponse{Body: append([]byte(nil), httpResp.Body...)},
	}
	if payload.Usage.InputTokens > 0 || payload.Usage.OutputTokens > 0 {
		resp.Usage = &llm.Usage{
			PromptTokens:     payload.Usage.InputTokens,
			CompletionTokens: payload.Usage.OutputTokens,
			TotalTokens:      payload.Usage.InputTokens + payload.Usage.OutputTokens,
		}
	}

	return resp, nil
}

func (t *OutboundTransformer) TransformStream(ctx context.Context, req *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	return nil, fmt.Errorf("%w: system one does not support streaming", transformer.ErrInvalidRequest)
}

func (t *OutboundTransformer) AggregateStreamChunks(ctx context.Context, req *httpclient.Request, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, fmt.Errorf("system one does not support streaming")
}

func (t *OutboundTransformer) TransformError(ctx context.Context, rawErr *httpclient.Error) *llm.ResponseError {
	if rawErr == nil {
		return &llm.ResponseError{
			StatusCode: http.StatusInternalServerError,
			Detail: llm.ErrorDetail{
				Message: http.StatusText(http.StatusInternalServerError),
				Type:    "api_error",
			},
		}
	}

	message := string(rawErr.Body)
	if message == "" {
		message = http.StatusText(rawErr.StatusCode)
	}

	return &llm.ResponseError{
		StatusCode: rawErr.StatusCode,
		Detail: llm.ErrorDetail{
			Message: message,
			Type:    "api_error",
		},
	}
}
