package biz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	opencodezen "github.com/looplj/axonhub/llm/transformer/opencode/zen"
)

func openCodeZenChannel() *ent.Channel {
	return &ent.Channel{
		ID:               1,
		Name:             "OpenCode Zen",
		Type:             channel.TypeOpencodeZen,
		BaseURL:          opencodezen.DefaultBaseURL,
		Credentials:      objects.ChannelCredentials{},
		SupportedModels:  []string{opencodezen.DefaultModel},
		DefaultTestModel: opencodezen.DefaultModel,
	}
}

func TestOpenCodeZenChannelBuildsWithoutCredentials(t *testing.T) {
	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}

	built, err := svc.buildChannelWithTransformer(openCodeZenChannel())
	require.NoError(t, err)
	require.NotNil(t, built)
	require.IsType(t, &opencodezen.OutboundTransformer{}, built.Outbound)
	require.Equal(t, llm.APIFormatOpenAIChatCompletion, built.Outbound.APIFormat())
}

func TestOpenCodeZenChannelUsesConfiguredAPIKey(t *testing.T) {
	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}
	entity := openCodeZenChannel()
	entity.Credentials = objects.ChannelCredentials{Mode: objects.APIKeyModeSingle, APIKeys: []string{"user-key"}}

	built, err := svc.buildChannelWithTransformer(entity)
	require.NoError(t, err)

	request, err := built.Outbound.TransformRequest(context.Background(), &llm.Request{
		Model: opencodezen.DefaultModel,
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: ptrTo("hello")}},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, request.Auth)
	require.Equal(t, "user-key", request.Auth.APIKey)
	require.Equal(t, "Bearer user-key", request.Headers.Get("Authorization"))
}

func TestOpenCodeZenChannelAPIKeyOverrideTakesPriority(t *testing.T) {
	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}
	entity := openCodeZenChannel()
	entity.Credentials = objects.ChannelCredentials{Mode: objects.APIKeyModeSingle, APIKeys: []string{"stored-key"}}

	built, err := svc.buildChannelWithTransformer(entity, "override-key")
	require.NoError(t, err)

	request, err := built.Outbound.TransformRequest(context.Background(), &llm.Request{
		Model: opencodezen.DefaultModel,
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: ptrTo("hello")}},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, request.Auth)
	require.Equal(t, "override-key", request.Auth.APIKey)
	require.Equal(t, "Bearer override-key", request.Headers.Get("Authorization"))
}

func TestOpenCodeZenDefaultEndpointsExposeNativeProtocols(t *testing.T) {
	endpoints := DefaultEndpointsForChannelType(channel.TypeOpencodeZen)
	require.Equal(t, []objects.ChannelEndpoint{
		{APIFormat: llm.APIFormatOpenAIChatCompletion.String()},
		{APIFormat: llm.APIFormatOpenAIResponse.String()},
		{APIFormat: llm.APIFormatAnthropicMessage.String()},
	}, endpoints)
	require.NoError(t, validateEndpointsForChannelType(channel.TypeOpencodeZen, endpoints))
}

func TestOpenCodeZenBuildsNativeProtocolOutbounds(t *testing.T) {
	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}
	built, err := svc.buildChannelWithOutbounds(openCodeZenChannel())
	require.NoError(t, err)

	chatOutbound := built.Outbounds[llm.APIFormatOpenAIChatCompletion.String()]
	require.IsType(t, &opencodezen.OutboundTransformer{}, chatOutbound)

	responsesOutbound := built.Outbounds[llm.APIFormatOpenAIResponse.String()]
	require.IsType(t, &opencodezen.ResponsesOutboundTransformer{}, responsesOutbound)
	responsesRequest, err := responsesOutbound.TransformRequest(context.Background(), &llm.Request{
		Model: "gpt-5-nano",
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: ptrTo("hello")}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, opencodezen.DefaultBaseURL+"/responses", responsesRequest.URL)
	require.NotNil(t, responsesRequest.Auth)
	require.Equal(t, httpclient.AuthTypeBearer, responsesRequest.Auth.Type)
	require.Equal(t, opencodezen.PublicAPIKey, responsesRequest.Auth.APIKey)
	require.Equal(t, opencodezen.OfficialUA, responsesRequest.Headers.Get("User-Agent"))
	require.Equal(t, "cli", responsesRequest.Headers.Get("X-Opencode-Client"))
	require.NotEmpty(t, responsesRequest.Headers.Get("X-Opencode-Session"))
	require.NotEmpty(t, responsesRequest.Headers.Get("X-Opencode-Request"))

	messagesOutbound := built.Outbounds[llm.APIFormatAnthropicMessage.String()]
	require.IsType(t, &anthropic.OutboundTransformer{}, messagesOutbound)
	messagesRequest, err := messagesOutbound.TransformRequest(context.Background(), &llm.Request{
		Model: "claude-sonnet-4-5",
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: ptrTo("hello")}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, opencodezen.DefaultBaseURL+"/messages", messagesRequest.URL)
	require.NotNil(t, messagesRequest.Auth)
	require.Equal(t, httpclient.AuthTypeAPIKey, messagesRequest.Auth.Type)
	require.Equal(t, "X-API-Key", messagesRequest.Auth.HeaderKey)
	require.Equal(t, opencodezen.PublicAPIKey, messagesRequest.Auth.APIKey)
}

func TestOpenCodeZenNativeProtocolOutboundsUseConfiguredKey(t *testing.T) {
	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}
	entity := openCodeZenChannel()
	entity.Credentials = objects.ChannelCredentials{Mode: objects.APIKeyModeSingle, APIKeys: []string{"user-key"}}

	built, err := svc.buildChannelWithOutbounds(entity)
	require.NoError(t, err)

	for _, apiFormat := range []llm.APIFormat{
		llm.APIFormatOpenAIResponse,
		llm.APIFormatAnthropicMessage,
	} {
		request, err := built.Outbounds[apiFormat.String()].TransformRequest(context.Background(), &llm.Request{
			Model: "configured-model",
			Messages: []llm.Message{
				{Role: "user", Content: llm.MessageContent{Content: ptrTo("hello")}},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, request.Auth)
		require.Equal(t, "user-key", request.Auth.APIKey)
	}
}

func TestOpenCodeZenCustomEndpointUsesZenTransformer(t *testing.T) {
	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}
	entity := openCodeZenChannel()
	entity.Endpoints = []objects.ChannelEndpoint{
		{
			APIFormat: llm.APIFormatOpenAIChatCompletion.String(),
			BaseURL:   "https://example.com/zen/v1",
			Path:      "/custom/chat",
		},
	}

	built, err := svc.buildChannelWithOutbounds(entity)
	require.NoError(t, err)

	outbound := built.Outbounds[llm.APIFormatOpenAIChatCompletion.String()]
	require.IsType(t, &opencodezen.OutboundTransformer{}, outbound)

	request, err := outbound.TransformRequest(context.Background(), &llm.Request{
		Model: opencodezen.DefaultModel,
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: ptrTo("hello")}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://example.com/zen/v1/custom/chat", request.URL)
	require.Equal(t, "Bearer public", request.Headers.Get("Authorization"))
}

func TestOpenCodeZenRejectsUnsupportedCustomEndpoint(t *testing.T) {
	endpoint := objects.ChannelEndpoint{APIFormat: llm.APIFormatOpenAIEmbedding.String()}
	require.ErrorContains(
		t,
		validateEndpointsForChannelType(channel.TypeOpencodeZen, []objects.ChannelEndpoint{endpoint}),
		"does not support api_format",
	)

	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}
	entity := openCodeZenChannel()
	built := buildChannel(entity, svc.httpClient)

	_, err := svc.buildNonDefaultEndpointOutbound(entity, built, endpoint)
	require.ErrorContains(t, err, "does not support api_format")
}

func TestOpenCodeZenRejectsResponsesWebSocket(t *testing.T) {
	endpoint := objects.ChannelEndpoint{
		APIFormat: llm.APIFormatOpenAIResponse.String(),
		Transport: objects.ChannelEndpointTransportWebSocket,
	}
	require.ErrorContains(
		t,
		validateEndpointsForChannelType(channel.TypeOpencodeZen, []objects.ChannelEndpoint{endpoint}),
		"does not support websocket transport",
	)
}

func TestModelFetcherFetchesOpenCodeZenModelsDynamically(t *testing.T) {
	tests := []struct {
		name         string
		apiKey       *string
		expectedAuth string
	}{
		{name: "public fallback", expectedAuth: "Bearer " + opencodezen.PublicAPIKey},
		{name: "configured key", apiKey: ptrTo("user-key"), expectedAuth: "Bearer user-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/v1/models", r.URL.Path)
				require.Equal(t, tt.expectedAuth, r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"dynamic-model-a"},{"id":"dynamic-model-b"}]}`))
			}))
			defer server.Close()

			fetcher := NewModelFetcher(httpclient.NewHttpClientWithClient(server.Client()), nil)
			result, err := fetcher.FetchModels(context.Background(), FetchModelsInput{
				ChannelType: channel.TypeOpencodeZen.String(),
				BaseURL:     server.URL + "/v1",
				APIKey:      tt.apiKey,
			})
			require.NoError(t, err)
			require.Nil(t, result.Error)
			require.Equal(t, []ModelIdentify{{ID: "dynamic-model-a"}, {ID: "dynamic-model-b"}}, result.Models)
		})
	}
}

func TestBulkImportOpenCodeZenUsesConfiguredAPIKey(t *testing.T) {
	svc, client := setupTestChannelService(t)
	t.Cleanup(func() { client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))

	baseURL := opencodezen.DefaultBaseURL
	apiKey := "imported-key"
	result, err := svc.BulkImportChannels(ctx, []*BulkImportChannelItem{
		{
			Type:             channel.TypeOpencodeZen.String(),
			Name:             "Zen keyed import",
			BaseURL:          &baseURL,
			APIKey:           &apiKey,
			SupportedModels:  []string{},
			DefaultTestModel: opencodezen.DefaultModel,
		},
	})
	require.NoError(t, err)
	require.True(t, result.Success, "bulk import errors: %v", result.Errors)
	require.Equal(t, []string{apiKey}, result.Channels[0].Credentials.GetAllAPIKeys())

	built, err := svc.buildChannelWithTransformer(result.Channels[0])
	require.NoError(t, err)
	request, err := built.Outbound.TransformRequest(context.Background(), &llm.Request{
		Model: opencodezen.DefaultModel,
		Messages: []llm.Message{
			{Role: "user", Content: llm.MessageContent{Content: ptrTo("hello")}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "Bearer "+apiKey, request.Headers.Get("Authorization"))
}

func TestBulkImportAllowsEmptyAPIKeyOnlyForOpenCodeZen(t *testing.T) {
	svc, client := setupTestChannelService(t)
	t.Cleanup(func() { client.Close() })
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))

	baseURL := opencodezen.DefaultBaseURL
	emptyKey := ""
	result, err := svc.BulkImportChannels(ctx, []*BulkImportChannelItem{
		{
			Type:             channel.TypeOpencodeZen.String(),
			Name:             "Zen import",
			BaseURL:          &baseURL,
			APIKey:           &emptyKey,
			SupportedModels:  []string{},
			DefaultTestModel: opencodezen.DefaultModel,
		},
	})
	require.NoError(t, err)
	require.True(t, result.Success, "bulk import errors: %v", result.Errors)
	require.Equal(t, 1, result.Created)
	require.Empty(t, result.Channels[0].Credentials.GetAllAPIKeys())

	openAIBaseURL := "https://api.openai.com/v1"
	failed, err := svc.BulkImportChannels(ctx, []*BulkImportChannelItem{
		{
			Type:             channel.TypeOpenai.String(),
			Name:             "OpenAI import",
			BaseURL:          &openAIBaseURL,
			APIKey:           &emptyKey,
			SupportedModels:  []string{"gpt-4o"},
			DefaultTestModel: "gpt-4o",
		},
	})
	require.NoError(t, err)
	require.False(t, failed.Success)
	require.Equal(t, 1, failed.Failed)
}
