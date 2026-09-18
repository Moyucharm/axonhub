package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	opencodezen "github.com/looplj/axonhub/llm/transformer/opencode/zen"
)

func openCodeZenChannel() *ent.Channel {
	return &ent.Channel{
		ID:               1,
		Name:             "OpenCode Zen",
		Type:             channel.TypeOpencodeZen,
		BaseURL:          opencodezen.DefaultBaseURL,
		Credentials:      objects.ChannelCredentials{},
		SupportedModels:  opencodezen.DefaultModels(),
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

func TestOpenCodeZenDefaultEndpointIsChatCompletions(t *testing.T) {
	endpoints := DefaultEndpointsForChannelType(channel.TypeOpencodeZen)
	require.Equal(t, []objects.ChannelEndpoint{{APIFormat: llm.APIFormatOpenAIChatCompletion.String()}}, endpoints)
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

func TestOpenCodeZenRejectsNonChatCustomEndpoint(t *testing.T) {
	endpoint := objects.ChannelEndpoint{APIFormat: llm.APIFormatOpenAIResponse.String()}
	require.ErrorContains(
		t,
		validateEndpointsForChannelType(channel.TypeOpencodeZen, []objects.ChannelEndpoint{endpoint}),
		"only supports api_format",
	)

	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}
	entity := openCodeZenChannel()
	built := buildChannel(entity, svc.httpClient)

	_, err := svc.buildNonDefaultEndpointOutbound(entity, built, endpoint)
	require.ErrorContains(t, err, "only supports api_format")
}

func TestModelFetcherReturnsOpenCodeZenDefaultsWithoutAPIKey(t *testing.T) {
	fetcher := NewModelFetcher(httpclient.NewHttpClient(), nil)

	result, err := fetcher.FetchModels(context.Background(), FetchModelsInput{
		ChannelType: channel.TypeOpencodeZen.String(),
		BaseURL:     opencodezen.DefaultBaseURL,
	})
	require.NoError(t, err)
	require.Nil(t, result.Error)
	require.Len(t, result.Models, len(opencodezen.DefaultModels()))
	for i, model := range opencodezen.DefaultModels() {
		require.Equal(t, model, result.Models[i].ID)
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
