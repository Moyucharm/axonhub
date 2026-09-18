package zen

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

var (
	_ transformer.Outbound                  = (*ResponsesOutboundTransformer)(nil)
	_ transformer.TransportRequestFinalizer = (*ResponsesOutboundTransformer)(nil)
	_ pipeline.ChannelCustomizedExecutor    = (*ResponsesOutboundTransformer)(nil)
	_ transformer.PassThroughBodyPolicy     = (*ResponsesOutboundTransformer)(nil)
)

// ResponsesConfig holds the transport configuration for OpenCode Zen Responses.
type ResponsesConfig struct {
	BaseURL        string              `json:"base_url,omitempty"`
	EndpointPath   string              `json:"endpoint_path,omitempty"`
	APIKeyProvider auth.APIKeyProvider `json:"-"`
	Transport      string              `json:"transport,omitempty"`
}

// ResponsesOutboundTransformer applies the OpenCode client identity on top of
// the standard OpenAI Responses transformer without changing Responses payload semantics.
type ResponsesOutboundTransformer struct {
	transformer.Outbound
	responses *responses.OutboundTransformer
}

func NewResponsesOutboundTransformerWithConfig(config *ResponsesConfig) (transformer.Outbound, error) {
	if config == nil {
		return nil, errors.New("config is nil")
	}
	if config.BaseURL == "" {
		return nil, errors.New("base URL is required")
	}

	apiKeyProvider := config.APIKeyProvider
	if apiKeyProvider == nil {
		apiKeyProvider = auth.NewStaticKeyProvider(PublicAPIKey)
	}

	outbound, err := responses.NewOutboundTransformerWithConfig(&responses.Config{
		BaseURL:        config.BaseURL,
		EndpointPath:   config.EndpointPath,
		APIKeyProvider: apiKeyProvider,
		Transport:      config.Transport,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid OpenCode Zen Responses transformer configuration: %w", err)
	}

	return &ResponsesOutboundTransformer{
		Outbound:  outbound,
		responses: outbound,
	}, nil
}

func (t *ResponsesOutboundTransformer) TransformRequest(ctx context.Context, request *llm.Request) (*httpclient.Request, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	stream := true
	request.Stream = &stream

	httpRequest, err := t.Outbound.TransformRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := applyResponsesFingerprint(httpRequest); err != nil {
		return nil, err
	}

	return httpRequest, nil
}

// AllowPassThroughBody rejects raw request pass-through because Zen requires
// transport fields and tool stubs to be reconciled after normal request transformation.
func (t *ResponsesOutboundTransformer) AllowPassThroughBody(context.Context, *llm.Request, *httpclient.Request) bool {
	return false
}

// FinalizeTransportRequest preserves the standard Responses HTTP cleanup and
// then makes the OpenCode identity and reconciled request body authoritative after generic overrides.
func (t *ResponsesOutboundTransformer) FinalizeTransportRequest(request *httpclient.Request) *httpclient.Request {
	if request == nil {
		return nil
	}

	prepared := t.responses.FinalizeTransportRequest(request)
	cloned := *prepared
	cloned.Body = append([]byte(nil), prepared.Body...)
	cloned.Headers = prepared.Headers.Clone()
	if cloned.Headers == nil {
		cloned.Headers = make(http.Header)
	}
	if prepared.TransformerMetadata != nil {
		cloned.TransformerMetadata = maps.Clone(prepared.TransformerMetadata)
	}
	if err := applyResponsesFingerprint(&cloned); err != nil {
		return prepared
	}

	return &cloned
}

func applyResponsesFingerprint(request *httpclient.Request) error {
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}

	body, err := reconcileResponsesRequestBody(request.Body)
	if err != nil {
		return fmt.Errorf("reconcile OpenCode Zen Responses request body: %w", err)
	}
	request.Body = body
	request.JSONBody = append([]byte(nil), body...)

	return applyIdentityFingerprint(request)
}

func reconcileResponsesRequestBody(body []byte) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, errors.New("request body is not valid JSON")
	}

	next, err := sjson.SetBytes(body, "stream", true)
	if err != nil {
		return nil, err
	}

	if gjson.GetBytes(next, "tool_choice").String() == "none" {
		next, err = sjson.DeleteBytes(next, "tool_choice")
		if err != nil {
			return nil, err
		}
	}

	toolsResult := gjson.GetBytes(next, "tools")
	tools := toolsResult.Array()
	if !toolsResult.Exists() || len(tools) == 0 {
		return sjson.SetRawBytes(next, "tools", []byte(stubResponsesToolsJSON))
	}

	toolsJSON, appended, err := appendMissingStubTools(tools, "name", preparsedResponsesStubs)
	if err != nil {
		return nil, err
	}
	if !appended {
		return next, nil
	}

	return sjson.SetRawBytes(next, "tools", toolsJSON)
}

// CustomizeExecutor forwards Responses HTTP/SSE executor customization.
func (t *ResponsesOutboundTransformer) CustomizeExecutor(executor pipeline.Executor) pipeline.Executor {
	return t.responses.CustomizeExecutor(executor)
}

// Stop releases resources owned by the wrapped Responses transformer.
func (t *ResponsesOutboundTransformer) Stop() {
	t.responses.Stop()
}
