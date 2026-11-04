package biz

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

func TestMigratedZenUsesOrdinaryOpenAIChat(t *testing.T) {
	svc := &ChannelService{httpClient: httpclient.NewHttpClient()}
	entity := &ent.Channel{ID: 1, Name: "Migrated Zen", Type: channel.TypeOpenai, BaseURL: "https://opencode.ai/zen/v1", Credentials: objects.ChannelCredentials{APIKeys: []string{"public"}}, SupportedModels: []string{"model"}, DefaultTestModel: "model", Endpoints: []objects.ChannelEndpoint{{APIFormat: "openai/chat_completions"}}, Settings: &objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "model", APIFormats: []string{"openai/chat_completions"}}}}}
	built, err := svc.buildChannelWithOutbounds(entity)
	require.NoError(t, err)
	outbound, err := BuildOutboundByAPIFormat(built, "openai/chat_completions")
	require.NoError(t, err)
	require.IsType(t, &openai.OutboundTransformer{}, outbound)
	request, err := outbound.TransformRequest(context.Background(), &llm.Request{Model: "model", Stream: lo.ToPtr(false), Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("hello")}}}})
	require.NoError(t, err)
	require.Equal(t, "https://opencode.ai/zen/v1/chat/completions", request.URL)
	require.Equal(t, "public", request.Auth.APIKey)
	require.Empty(t, request.Headers.Get("X-Opencode-Client"))
	require.NotContains(t, request.Headers.Get("User-Agent"), "opencode/")
	var body map[string]any
	require.NoError(t, json.Unmarshal(request.Body, &body))
	require.NotEqual(t, true, body["stream"])
	require.NotContains(t, body, "tools")
	require.Equal(t, []string{"openai/chat_completions"}, built.ForcedAPIFormats("model"))
}
