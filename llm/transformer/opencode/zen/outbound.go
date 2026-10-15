package zen

import (
	"context"
	"errors"
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// Config holds the transport configuration for OpenCode Zen.
type Config struct {
	BaseURL        string              `json:"base_url,omitempty"`
	EndpointPath   string              `json:"endpoint_path,omitempty"`
	APIKeyProvider auth.APIKeyProvider `json:"-"`
}

// OutboundTransformer applies the OpenCode Zen free-tier fingerprint on top of
// the standard OpenAI Chat Completions transformer.
type OutboundTransformer struct {
	transformer.Outbound
}

func NewOutboundTransformerWithConfig(config *Config) (transformer.Outbound, error) {
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

	outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{
		PlatformType:   openai.PlatformOpenAI,
		BaseURL:        config.BaseURL,
		EndpointPath:   config.EndpointPath,
		APIKeyProvider: apiKeyProvider,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid OpenCode Zen transformer configuration: %w", err)
	}

	return &OutboundTransformer{Outbound: &toolAliasOutbound{Outbound: outbound}}, nil
}

func NewOutboundTransformer(baseURL string) (transformer.Outbound, error) {
	return NewOutboundTransformerWithConfig(&Config{BaseURL: baseURL})
}

// TransformRequest upgrades every provider request to streaming. The pipeline
// preserves the caller-facing mode and auto-aggregates SSE for non-stream clients.
func (t *OutboundTransformer) TransformRequest(ctx context.Context, request *llm.Request) (*httpclient.Request, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	stream := true
	request.Stream = &stream

	httpRequest, err := t.Outbound.TransformRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	if err := initializeIdentity(ctx, request, httpRequest); err != nil {
		return nil, err
	}

	if err := applyFingerprint(httpRequest); err != nil {
		return nil, err
	}

	return httpRequest, nil
}

// AllowPassThroughBody rejects raw request pass-through because Zen requires
// transport fields to be reconciled after normal request transformation.
func (t *OutboundTransformer) AllowPassThroughBody(context.Context, *llm.Request, *httpclient.Request) bool {
	return false
}

// FinalizeTransportRequest runs after generic pass-through and override
// middleware, making provider-required fields authoritative at transport time.
func (t *OutboundTransformer) FinalizeTransportRequest(request *httpclient.Request) *httpclient.Request {
	if request == nil {
		return nil
	}

	cloned := cloneTransportRequest(request)
	if err := applyFingerprint(cloned); err != nil {
		return request
	}
	return cloned
}

func applyFingerprint(request *httpclient.Request) error {
	body, err := reconcileRequestBody(request.Body)
	if err != nil {
		return fmt.Errorf("reconcile OpenCode Zen request body: %w", err)
	}
	request.Body = body
	request.JSONBody = append([]byte(nil), body...)

	return applyIdentityFingerprint(request)
}

func reconcileRequestBody(body []byte) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, errors.New("request body is not valid JSON")
	}

	next, err := sjson.SetBytes(body, "stream", true)
	if err != nil {
		return nil, err
	}

	next, err = sjson.SetBytes(next, "stream_options.include_usage", true)
	if err != nil {
		return nil, err
	}
	if len(gjson.GetBytes(next, "tools").Array()) == 0 {
		next, err = sjson.SetBytes(next, "tool_choice", "none")
		if err != nil {
			return nil, err
		}
	}
	return reconcileTools(next, llm.APIFormatOpenAIChatCompletion)
}
