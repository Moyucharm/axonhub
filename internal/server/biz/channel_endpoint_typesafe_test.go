package biz

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
)

func TestChannelEndpoint_TypesafeStrictness(t *testing.T) {
	// 1. Typesafe channel with typesafe/systemone endpoint should pass
	validEndpoints := []objects.ChannelEndpoint{
		{APIFormat: llm.APIFormatTypeSafeSystemOne.String()},
	}
	require.NoError(t, validateEndpointsForChannelType(channel.TypeTypesafe, validEndpoints))

	// 2. Typesafe channel with openai/chat_completions endpoint should FAIL
	invalidEndpoints := []objects.ChannelEndpoint{
		{APIFormat: llm.APIFormatOpenAIChatCompletion.String()},
	}
	err := validateEndpointsForChannelType(channel.TypeTypesafe, invalidEndpoints)
	require.Error(t, err)
	require.Contains(t, err.Error(), "only supports api_format")

	// 3. Non-typesafe channels may expose a Jev-compatible typesafe/systemone endpoint.
	openaiWithSystemOne := []objects.ChannelEndpoint{
		{APIFormat: llm.APIFormatTypeSafeSystemOne.String()},
	}
	require.NoError(t, validateEndpointsForChannelType(channel.TypeOpenai, openaiWithSystemOne))

	// 4. Default endpoints for TypeSafe channel must only be typesafe/systemone
	defaults := DefaultEndpointsForChannelType(channel.TypeTypesafe)
	require.Len(t, defaults, 1)
	require.Equal(t, llm.APIFormatTypeSafeSystemOne.String(), defaults[0].APIFormat)
}
