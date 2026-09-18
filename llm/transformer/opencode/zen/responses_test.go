package zen

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer"
)

const museSparkContributorFreeModel = "muse-spark-1.3-contributor-free"

func newResponsesRequest(model string) *llm.Request {
	return &llm.Request{
		Model:  model,
		Stream: lo.ToPtr(false),
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("hello")}},
		},
	}
}

func newTestResponsesOutbound(t *testing.T) *ResponsesOutboundTransformer {
	t.Helper()

	outbound, err := NewResponsesOutboundTransformerWithConfig(&ResponsesConfig{BaseURL: DefaultBaseURL})
	require.NoError(t, err)

	zenOutbound, ok := outbound.(*ResponsesOutboundTransformer)
	require.True(t, ok)
	return zenOutbound
}

func TestResponsesTransformRequestAppliesZenIdentity(t *testing.T) {
	outbound := newTestResponsesOutbound(t)
	httpRequest, err := outbound.TransformRequest(context.Background(), newResponsesRequest("gpt-5-nano"))
	require.NoError(t, err)

	assert.Equal(t, DefaultBaseURL+"/responses", httpRequest.URL)
	assert.Equal(t, llm.APIFormatOpenAIResponse.String(), httpRequest.APIFormat)
	require.NotNil(t, httpRequest.Auth)
	assert.Equal(t, httpclient.AuthTypeBearer, httpRequest.Auth.Type)
	assert.Equal(t, PublicAPIKey, httpRequest.Auth.APIKey)
	assert.Equal(t, "Bearer public", httpRequest.Headers.Get("Authorization"))
	assert.Equal(t, OfficialUA, httpRequest.Headers.Get("User-Agent"))
	assert.Equal(t, "cli", httpRequest.Headers.Get(clientHeader))
	assert.Equal(t, "global", httpRequest.Headers.Get(projectHeader))

	idPattern := regexp.MustCompile(`^(ses|msg)_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
	assert.Regexp(t, idPattern, httpRequest.Headers.Get(sessionHeader))
	assert.Regexp(t, idPattern, httpRequest.Headers.Get(requestHeader))
	assert.NotEqual(t, httpRequest.Headers.Get(sessionHeader), httpRequest.Headers.Get(requestHeader))

	assert.Equal(t, "gpt-5-nano", gjson.GetBytes(httpRequest.Body, "model").String())
	assert.True(t, gjson.GetBytes(httpRequest.Body, "input").Exists())
	assert.True(t, gjson.GetBytes(httpRequest.Body, "stream").Exists())
	assert.True(t, gjson.GetBytes(httpRequest.Body, "stream").Bool())
	assert.False(t, gjson.GetBytes(httpRequest.Body, "messages").Exists())

	body := decodeBody(t, httpRequest.Body)
	assert.Equal(t, true, body["stream"])
	assert.Equal(t, []string{"bash", "read"}, toolNames(t, body))
	_, hasChoice := body["tool_choice"]
	assert.False(t, hasChoice)
}

func TestResponsesTransformRequestUsesConfiguredAPIKey(t *testing.T) {
	outbound, err := NewResponsesOutboundTransformerWithConfig(&ResponsesConfig{
		BaseURL:        DefaultBaseURL,
		APIKeyProvider: auth.NewStaticKeyProvider("user-key"),
	})
	require.NoError(t, err)

	httpRequest, err := outbound.TransformRequest(context.Background(), newResponsesRequest(museSparkContributorFreeModel))
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

func TestResponsesUsesPublicCredentialForMuseContributorFree(t *testing.T) {
	outbound := newTestResponsesOutbound(t)

	httpRequest, err := outbound.TransformRequest(context.Background(), newResponsesRequest(museSparkContributorFreeModel))
	require.NoError(t, err)
	require.NotNil(t, httpRequest.Auth)
	assert.Equal(t, PublicAPIKey, httpRequest.Auth.APIKey)
	assert.Equal(t, "Bearer public", httpRequest.Headers.Get("Authorization"))
	assert.Equal(t, museSparkContributorFreeModel, gjson.GetBytes(httpRequest.Body, "model").String())
}

func TestResponsesFinalizeTransportRequestRestoresZenIdentity(t *testing.T) {
	outbound := newTestResponsesOutbound(t)
	httpRequest, err := outbound.TransformRequest(context.Background(), newResponsesRequest("gpt-5-nano"))
	require.NoError(t, err)
	originalSessionID := httpRequest.Headers.Get(sessionHeader)
	originalRequestID := httpRequest.Headers.Get(requestHeader)

	httpRequest.Headers.Set("Authorization", "Bearer overridden")
	httpRequest.Headers.Set("User-Agent", "client/1.0")
	httpRequest.Headers.Set(clientHeader, "other")
	httpRequest.Headers.Set(projectHeader, "other")
	httpRequest.Headers.Set(sessionHeader, "other")
	httpRequest.Headers.Set(requestHeader, "other")
	httpRequest.Headers.Set("X-Custom", "kept")
	httpRequest.Headers.Set("OpenAI-Beta", "responses_websockets=2026-02-06, other_beta=v1")
	httpRequest.Body = []byte(`{"model":"gpt-5-nano","stream":false,"tools":[{"type":"function","name":"weather"}]}`)

	finalized := outbound.FinalizeTransportRequest(httpRequest)
	require.NotSame(t, httpRequest, finalized)
	assert.Equal(t, "Bearer public", finalized.Headers.Get("Authorization"))
	assert.Equal(t, OfficialUA, finalized.Headers.Get("User-Agent"))
	assert.Equal(t, "cli", finalized.Headers.Get(clientHeader))
	assert.Equal(t, "global", finalized.Headers.Get(projectHeader))
	assert.Equal(t, originalSessionID, finalized.Headers.Get(sessionHeader))
	assert.Equal(t, originalRequestID, finalized.Headers.Get(requestHeader))
	assert.Equal(t, "kept", finalized.Headers.Get("X-Custom"))
	assert.Equal(t, "other_beta=v1", finalized.Headers.Get("OpenAI-Beta"))

	body := decodeBody(t, finalized.Body)
	assert.Equal(t, true, body["stream"])
	assert.Equal(t, []string{"weather", "bash", "read"}, toolNames(t, body))
}

func TestResponsesReconcileRequestBody(t *testing.T) {
	outbound := newTestResponsesOutbound(t)

	t.Run("forces stream and injects stub tools", func(t *testing.T) {
		req := newResponsesRequest("gpt-5-nano")
		req.Stream = lo.ToPtr(false)

		httpRequest, err := outbound.TransformRequest(context.Background(), req)
		require.NoError(t, err)

		body := decodeBody(t, httpRequest.Body)
		assert.Equal(t, true, body["stream"])
		assert.Equal(t, []string{"bash", "read"}, toolNames(t, body))
		_, hasChoice := body["tool_choice"]
		assert.False(t, hasChoice)
	})

	t.Run("removes none tool_choice", func(t *testing.T) {
		req := newResponsesRequest("gpt-5-nano")
		req.ToolChoice = &llm.ToolChoice{ToolChoice: lo.ToPtr("none")}

		httpRequest, err := outbound.TransformRequest(context.Background(), req)
		require.NoError(t, err)

		body := decodeBody(t, httpRequest.Body)
		_, hasChoice := body["tool_choice"]
		assert.False(t, hasChoice)
	})

	t.Run("preserves custom tools and appends missing stubs", func(t *testing.T) {
		req := newResponsesRequest("gpt-5-nano")
		req.Tools = []llm.Tool{
			{
				Type: "function",
				Function: llm.Function{
					Name:        "custom_calc",
					Description: "Calculate math expressions",
				},
			},
		}

		httpRequest, err := outbound.TransformRequest(context.Background(), req)
		require.NoError(t, err)

		body := decodeBody(t, httpRequest.Body)
		assert.Equal(t, []string{"custom_calc", "bash", "read"}, toolNames(t, body))
	})

	t.Run("preserves existing bash and appends read", func(t *testing.T) {
		req := newResponsesRequest("gpt-5-nano")
		req.Tools = []llm.Tool{
			{
				Type: "function",
				Function: llm.Function{
					Name:        "bash",
					Description: "user bash tool",
				},
			},
		}

		httpRequest, err := outbound.TransformRequest(context.Background(), req)
		require.NoError(t, err)

		body := decodeBody(t, httpRequest.Body)
		assert.Equal(t, []string{"bash", "read"}, toolNames(t, body))
		tools := body["tools"].([]any)
		bash := tools[0].(map[string]any)
		assert.Equal(t, "user bash tool", bash["description"])
	})

	t.Run("does not duplicate when both bash and read are present", func(t *testing.T) {
		req := newResponsesRequest("gpt-5-nano")
		req.Tools = []llm.Tool{
			{Type: "function", Function: llm.Function{Name: "bash"}},
			{Type: "function", Function: llm.Function{Name: "read"}},
		}

		httpRequest, err := outbound.TransformRequest(context.Background(), req)
		require.NoError(t, err)

		body := decodeBody(t, httpRequest.Body)
		assert.Equal(t, []string{"bash", "read"}, toolNames(t, body))
	})
}

func TestResponsesRejectsPassThroughBody(t *testing.T) {
	outbound := newTestResponsesOutbound(t)
	assert.False(t, outbound.AllowPassThroughBody(context.Background(), newResponsesRequest("gpt-5-nano"), &httpclient.Request{}))
	require.Implements(t, (*transformer.PassThroughBodyPolicy)(nil), outbound)
	require.Implements(t, (*transformer.TransportRequestFinalizer)(nil), outbound)
}

func TestResponsesOutboundForwardsExecutorCustomization(t *testing.T) {
	outbound := newTestResponsesOutbound(t)
	require.Implements(t, (*pipeline.ChannelCustomizedExecutor)(nil), outbound)
	require.Implements(t, (*transformer.TransportRequestFinalizer)(nil), outbound)

	executor := httpclient.NewHttpClient()
	assert.NotSame(t, executor, outbound.CustomizeExecutor(executor))
}

func TestNewResponsesOutboundTransformerValidation(t *testing.T) {
	_, err := NewResponsesOutboundTransformerWithConfig(nil)
	require.ErrorContains(t, err, "config is nil")

	_, err = NewResponsesOutboundTransformerWithConfig(&ResponsesConfig{})
	require.ErrorContains(t, err, "base URL is required")
}

func TestResponsesApplyIdentityFingerprintSynchronizesEmptyAPIKey(t *testing.T) {
	outbound := newTestResponsesOutbound(t)
	request := &httpclient.Request{
		Headers: http.Header{},
		Body:    []byte(`{"model":"gpt-5-nano","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`),
		Auth:    &httpclient.AuthConfig{APIKey: "   "},
	}

	finalized := outbound.FinalizeTransportRequest(request)
	require.NotNil(t, finalized.Auth)
	assert.Equal(t, httpclient.AuthTypeBearer, finalized.Auth.Type)
	assert.Equal(t, PublicAPIKey, finalized.Auth.APIKey)
	assert.Equal(t, "Bearer public", finalized.Headers.Get("Authorization"))
}

func TestResponsesFinalizeTransportRequestClonesTransformerMetadata(t *testing.T) {
	outbound := newTestResponsesOutbound(t)
	httpRequest, err := outbound.TransformRequest(context.Background(), newResponsesRequest("gpt-5-nano"))
	require.NoError(t, err)

	finalized := outbound.FinalizeTransportRequest(httpRequest)
	require.NotSame(t, httpRequest, finalized)
	require.NotNil(t, httpRequest.TransformerMetadata)
	require.NotNil(t, finalized.TransformerMetadata)

	finalized.TransformerMetadata["new_key"] = "test"
	assert.NotContains(t, httpRequest.TransformerMetadata, "new_key")
}

