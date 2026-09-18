package zen

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

const (
	sessionMetadataKey = "opencode_zen_session_id"
	requestMetadataKey = "opencode_zen_request_id"
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

	return &OutboundTransformer{Outbound: outbound}, nil
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

	if err := ensureIdentityMetadata(httpRequest); err != nil {
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

	cloned := *request
	cloned.Body = append([]byte(nil), request.Body...)
	cloned.Headers = request.Headers.Clone()
	if cloned.Headers == nil {
		cloned.Headers = make(http.Header)
	}
	if request.TransformerMetadata != nil {
		cloned.TransformerMetadata = maps.Clone(request.TransformerMetadata)
	}

	if err := applyFingerprint(&cloned); err != nil {
		return request
	}

	return &cloned
}

func applyFingerprint(request *httpclient.Request) error {
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}

	body, err := reconcileRequestBody(request.Body)
	if err != nil {
		return fmt.Errorf("reconcile OpenCode Zen request body: %w", err)
	}
	request.Body = body
	request.JSONBody = append([]byte(nil), body...)

	return applyIdentityFingerprint(request)
}

func ensureIdentityMetadata(request *httpclient.Request) error {
	if request.TransformerMetadata == nil {
		request.TransformerMetadata = make(map[string]any)
	}

	if sessionID, _ := request.TransformerMetadata[sessionMetadataKey].(string); sessionID == "" {
		generated, err := generateOpenCodeID("ses")
		if err != nil {
			return err
		}
		request.TransformerMetadata[sessionMetadataKey] = generated
	}
	if requestID, _ := request.TransformerMetadata[requestMetadataKey].(string); requestID == "" {
		generated, err := generateOpenCodeID("msg")
		if err != nil {
			return err
		}
		request.TransformerMetadata[requestMetadataKey] = generated
	}

	return nil
}

func applyIdentityFingerprint(request *httpclient.Request) error {
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}
	if err := ensureIdentityMetadata(request); err != nil {
		return err
	}

	apiKey := PublicAPIKey
	if request.Auth != nil {
		if configuredKey := strings.TrimSpace(request.Auth.APIKey); configuredKey != "" {
			apiKey = configuredKey
		}
	}
	request.Auth = &httpclient.AuthConfig{
		Type:   httpclient.AuthTypeBearer,
		APIKey: apiKey,
	}
	request.Headers.Set("Authorization", "Bearer "+apiKey)
	request.Headers.Set("Content-Type", "application/json")
	request.Headers.Set("Accept", "application/json")
	request.Headers.Set("User-Agent", OfficialUA)
	request.Headers.Set(clientHeader, "cli")
	request.Headers.Set(projectHeader, "global")
	sessionID, _ := request.TransformerMetadata[sessionMetadataKey].(string)
	requestID, _ := request.TransformerMetadata[requestMetadataKey].(string)
	request.Headers.Set(sessionHeader, sessionID)
	request.Headers.Set(requestHeader, requestID)

	return nil
}

func reconcileRequestBody(body []byte) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, errors.New("request body is not valid JSON")
	}

	next, err := sjson.SetBytes(body, "stream", true)
	if err != nil {
		return nil, err
	}

	toolsResult := gjson.GetBytes(next, "tools")
	tools := toolsResult.Array()
	if !toolsResult.Exists() || len(tools) == 0 {
		next, err = sjson.SetRawBytes(next, "tools", []byte(stubToolsJSON))
		if err != nil {
			return nil, err
		}
		return sjson.SetBytes(next, "tool_choice", "none")
	}

	toolsJSON, appended, err := appendMissingStubTools(tools, "function.name", preparsedChatStubs)
	if err != nil {
		return nil, err
	}
	if !appended {
		return next, nil
	}

	return sjson.SetRawBytes(next, "tools", toolsJSON)
}
