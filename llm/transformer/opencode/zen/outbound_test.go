package zen

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

func newTestOutbound(t *testing.T) *OutboundTransformer {
	t.Helper()

	outbound, err := NewOutboundTransformer(DefaultBaseURL)
	require.NoError(t, err)

	zenOutbound, ok := outbound.(*OutboundTransformer)
	require.True(t, ok)
	return zenOutbound
}

func newChatRequest() *llm.Request {
	return &llm.Request{
		Model: DefaultModel,
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("hello")}},
		},
	}
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	return decoded
}

func toolNames(t *testing.T, body map[string]any) []string {
	t.Helper()

	tools, ok := body["tools"].([]any)
	require.True(t, ok)

	names := make([]string, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		require.True(t, ok)
		if function, ok := tool["function"].(map[string]any); ok {
			name, ok := function["name"].(string)
			require.True(t, ok)
			names = append(names, name)
			continue
		}
		name, ok := tool["name"].(string)
		require.True(t, ok)
		names = append(names, name)
	}
	return names
}

func TestTransformRequestAppliesZenFingerprint(t *testing.T) {
	outbound := newTestOutbound(t)
	request := newChatRequest()

	httpRequest, err := outbound.TransformRequest(context.Background(), request)
	require.NoError(t, err)

	require.NotNil(t, request.Stream)
	assert.True(t, *request.Stream)
	assert.Equal(t, "https://opencode.ai/zen/v1/chat/completions", httpRequest.URL)
	assert.Equal(t, "Bearer public", httpRequest.Headers.Get("Authorization"))
	require.NotNil(t, httpRequest.Auth)
	assert.Equal(t, httpclient.AuthTypeBearer, httpRequest.Auth.Type)
	assert.Equal(t, PublicAPIKey, httpRequest.Auth.APIKey)
	assert.Equal(t, OfficialUA, httpRequest.Headers.Get("User-Agent"))
	assert.Equal(t, "cli", httpRequest.Headers.Get(clientHeader))
	assert.Equal(t, "global", httpRequest.Headers.Get(projectHeader))

	idPattern := regexp.MustCompile(`^(ses|msg)_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
	assert.Regexp(t, idPattern, httpRequest.Headers.Get(sessionHeader))
	assert.Regexp(t, idPattern, httpRequest.Headers.Get(requestHeader))
	assert.NotEqual(t, httpRequest.Headers.Get(sessionHeader), httpRequest.Headers.Get(requestHeader))

	body := decodeBody(t, httpRequest.Body)
	assert.Equal(t, true, body["stream"])
	assert.Equal(t, "none", body["tool_choice"])
	assert.Equal(t, []string{"bash", "read"}, toolNames(t, body))
}

func TestTransformRequestUsesConfiguredAPIKey(t *testing.T) {
	outbound, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        DefaultBaseURL,
		APIKeyProvider: auth.NewStaticKeyProvider("user-key"),
	})
	require.NoError(t, err)

	httpRequest, err := outbound.TransformRequest(context.Background(), newChatRequest())
	require.NoError(t, err)
	require.NotNil(t, httpRequest.Auth)
	assert.Equal(t, "user-key", httpRequest.Auth.APIKey)
	assert.Equal(t, "Bearer user-key", httpRequest.Headers.Get("Authorization"))

	httpRequest.Headers.Set("Authorization", "Bearer overridden")
	finalized := outbound.(transformer.TransportRequestFinalizer).FinalizeTransportRequest(httpRequest)
	require.NotNil(t, finalized.Auth)
	assert.Equal(t, "user-key", finalized.Auth.APIKey)
	assert.Equal(t, "Bearer user-key", finalized.Headers.Get("Authorization"))
}

func TestTransformRequestFallsBackToPublicForBlankConfiguredAPIKey(t *testing.T) {
	outbound, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        DefaultBaseURL,
		APIKeyProvider: auth.NewStaticKeyProvider("   "),
	})
	require.NoError(t, err)

	httpRequest, err := outbound.TransformRequest(context.Background(), newChatRequest())
	require.NoError(t, err)
	require.NotNil(t, httpRequest.Auth)
	assert.Equal(t, httpclient.AuthTypeBearer, httpRequest.Auth.Type)
	assert.Equal(t, PublicAPIKey, httpRequest.Auth.APIKey)
	assert.Equal(t, "Bearer public", httpRequest.Headers.Get("Authorization"))
}

func TestTransformRequestPreservesExistingToolsAndChoice(t *testing.T) {
	outbound := newTestOutbound(t)
	auto := "auto"
	request := newChatRequest()
	request.Tools = []llm.Tool{
		{
			Type: llm.ToolTypeFunction,
			Function: llm.Function{
				Name:        "bash",
				Description: "custom bash",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`),
			},
		},
		{
			Type: llm.ToolTypeFunction,
			Function: llm.Function{
				Name:       "weather",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		},
	}
	request.ToolChoice = &llm.ToolChoice{ToolChoice: &auto}

	httpRequest, err := outbound.TransformRequest(context.Background(), request)
	require.NoError(t, err)

	body := decodeBody(t, httpRequest.Body)
	assert.Equal(t, "auto", body["tool_choice"])
	assert.Equal(t, []string{"bash", "weather", "read"}, toolNames(t, body))

	tools := body["tools"].([]any)
	bash := tools[0].(map[string]any)["function"].(map[string]any)
	assert.Equal(t, "custom bash", bash["description"])
	parameters := bash["parameters"].(map[string]any)
	properties := parameters["properties"].(map[string]any)
	assert.Contains(t, properties, "cmd")
}

func TestTransformRequestLeavesCompleteToolSetUnchanged(t *testing.T) {
	outbound := newTestOutbound(t)
	request := newChatRequest()
	request.Tools = []llm.Tool{
		{Type: llm.ToolTypeFunction, Function: llm.Function{Name: "bash", Description: "custom bash"}},
		{Type: llm.ToolTypeFunction, Function: llm.Function{Name: "read", Description: "custom read"}},
	}

	httpRequest, err := outbound.TransformRequest(context.Background(), request)
	require.NoError(t, err)

	body := decodeBody(t, httpRequest.Body)
	assert.Equal(t, []string{"bash", "read"}, toolNames(t, body))
	_, hasChoice := body["tool_choice"]
	assert.False(t, hasChoice)
}

func TestFinalizeTransportRequestRestoresRequiredFields(t *testing.T) {
	outbound := newTestOutbound(t)
	httpRequest, err := outbound.TransformRequest(context.Background(), newChatRequest())
	require.NoError(t, err)

	httpRequest.Headers.Set("Authorization", "Bearer overridden")
	httpRequest.Headers.Set("User-Agent", "client/1.0")
	httpRequest.Headers.Set(clientHeader, "other")
	httpRequest.Headers.Set("X-Custom", "kept")
	httpRequest.Body = []byte(`{"model":"mimo-v2.5-free","messages":[{"role":"user","content":"hello"}],"stream":false,"tools":[{"type":"function","function":{"name":"weather"}}],"tool_choice":"auto"}`)

	finalized := outbound.FinalizeTransportRequest(httpRequest)
	require.NotSame(t, httpRequest, finalized)
	assert.Equal(t, "Bearer public", finalized.Headers.Get("Authorization"))
	assert.Equal(t, OfficialUA, finalized.Headers.Get("User-Agent"))
	assert.Equal(t, "cli", finalized.Headers.Get(clientHeader))
	assert.Equal(t, "kept", finalized.Headers.Get("X-Custom"))

	body := decodeBody(t, finalized.Body)
	assert.Equal(t, true, body["stream"])
	assert.Equal(t, "auto", body["tool_choice"])
	assert.Equal(t, []string{"weather", "bash", "read"}, toolNames(t, body))
}

func TestOutboundRejectsPassThroughBody(t *testing.T) {
	outbound := newTestOutbound(t)
	assert.False(t, outbound.AllowPassThroughBody(context.Background(), newChatRequest(), &httpclient.Request{}))
	require.Implements(t, (*transformer.PassThroughBodyPolicy)(nil), outbound)
	require.Implements(t, (*transformer.TransportRequestFinalizer)(nil), outbound)
}

func TestNewOutboundTransformerValidation(t *testing.T) {
	_, err := NewOutboundTransformerWithConfig(nil)
	require.ErrorContains(t, err, "config is nil")

	_, err = NewOutboundTransformerWithConfig(&Config{})
	require.ErrorContains(t, err, "base URL is required")
}

func TestFinalizeTransportRequestKeepsOriginalOnInvalidJSON(t *testing.T) {
	outbound := newTestOutbound(t)
	request := &httpclient.Request{
		Headers: http.Header{"User-Agent": []string{"unchanged"}},
		Body:    []byte("not-json"),
	}

	assert.Same(t, request, outbound.FinalizeTransportRequest(request))
}

func TestApplyIdentityFingerprintSynchronizesEmptyAPIKey(t *testing.T) {
	outbound := newTestOutbound(t)
	request := &httpclient.Request{
		Headers: http.Header{},
		Body:    []byte(`{"model":"mimo-v2.5-free","messages":[{"role":"user","content":"hello"}]}`),
		Auth:    &httpclient.AuthConfig{APIKey: "   "},
	}

	finalized := outbound.FinalizeTransportRequest(request)
	require.NotNil(t, finalized.Auth)
	assert.Equal(t, httpclient.AuthTypeBearer, finalized.Auth.Type)
	assert.Equal(t, PublicAPIKey, finalized.Auth.APIKey)
	assert.Equal(t, "Bearer public", finalized.Headers.Get("Authorization"))
}

func TestFinalizeTransportRequestClonesTransformerMetadata(t *testing.T) {
	outbound := newTestOutbound(t)
	httpRequest, err := outbound.TransformRequest(context.Background(), newChatRequest())
	require.NoError(t, err)

	finalized := outbound.FinalizeTransportRequest(httpRequest)
	require.NotSame(t, httpRequest, finalized)
	require.NotNil(t, httpRequest.TransformerMetadata)
	require.NotNil(t, finalized.TransformerMetadata)

	finalized.TransformerMetadata["new_key"] = "test"
	assert.NotContains(t, httpRequest.TransformerMetadata, "new_key")
}
