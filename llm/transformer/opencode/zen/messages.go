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
	"github.com/looplj/axonhub/llm/transformer/anthropic"
)

var (
	_ transformer.Outbound                  = (*MessagesOutboundTransformer)(nil)
	_ transformer.TransportRequestFinalizer = (*MessagesOutboundTransformer)(nil)
	_ transformer.PassThroughBodyPolicy     = (*MessagesOutboundTransformer)(nil)
)

// MessagesOutboundTransformer upgrades Zen's Anthropic Messages requests to streaming.
// The pipeline aggregates the upstream events for non-streaming callers.
type MessagesOutboundTransformer struct {
	transformer.Outbound
}

func NewMessagesOutboundTransformerWithConfig(config *Config) (transformer.Outbound, error) {
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

	outbound, err := anthropic.NewOutboundTransformerWithConfig(&anthropic.Config{
		Type:           anthropic.PlatformDirect,
		BaseURL:        config.BaseURL,
		APIKeyProvider: apiKeyProvider,
		EndpointPath:   config.EndpointPath,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid OpenCode Zen Messages transformer configuration: %w", err)
	}

	return &MessagesOutboundTransformer{Outbound: &toolAliasOutbound{Outbound: outbound}}, nil
}

func (t *MessagesOutboundTransformer) TransformRequest(ctx context.Context, request *llm.Request) (*httpclient.Request, error) {
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
	if err := applyMessagesFingerprint(httpRequest); err != nil {
		return nil, err
	}
	return httpRequest, nil
}

func (t *MessagesOutboundTransformer) AllowPassThroughBody(context.Context, *llm.Request, *httpclient.Request) bool {
	return false
}

func (t *MessagesOutboundTransformer) FinalizeTransportRequest(request *httpclient.Request) *httpclient.Request {
	if request == nil || !gjson.ValidBytes(request.Body) {
		return request
	}

	cloned := cloneTransportRequest(request)
	if err := applyMessagesFingerprint(cloned); err != nil {
		return request
	}
	return cloned
}

func applyMessagesFingerprint(request *httpclient.Request) error {
	body, err := sjson.SetBytes(request.Body, "stream", true)
	if err != nil {
		return err
	}
	if len(gjson.GetBytes(body, "tools").Array()) == 0 {
		body, err = sjson.SetBytes(body, "tool_choice", map[string]string{"type": "none"})
		if err != nil {
			return err
		}
	}
	body, err = reconcileTools(body, llm.APIFormatAnthropicMessage)
	if err != nil {
		return err
	}
	body, err = normalizeJSONBody(body)
	if err != nil {
		return err
	}
	request.Body = body
	request.JSONBody = append([]byte(nil), body...)
	return applyIdentityFingerprint(request)
}
