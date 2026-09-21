package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestPopulateAPIFormatFiltersSystemOneUnsupportedChannels(t *testing.T) {
	unsupported := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeOpenai}}}
	supported := &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{
		Type: channel.TypeOpenai,
		Endpoints: []objects.ChannelEndpoint{{
			APIFormat: llm.APIFormatTypeSafeSystemOne.String(),
		}},
	}}}

	candidates := populateAPIFormat(context.Background(), []*ChannelModelsCandidate{unsupported, supported}, &llm.Request{
		RequestType: llm.RequestTypeSystemOne,
		APIFormat:   llm.APIFormatTypeSafeSystemOne,
	})

	require.Equal(t, []*ChannelModelsCandidate{supported}, candidates)
	require.Equal(t, llm.APIFormatTypeSafeSystemOne.String(), supported.APIFormat)
}

func TestPopulateAPIFormatDropsSystemOneModelsWithIncompatibleOverrides(t *testing.T) {
	candidate := &ChannelModelsCandidate{
		Channel: &biz.Channel{Channel: &ent.Channel{
			Type: channel.TypeOpenai,
			Endpoints: []objects.ChannelEndpoint{{
				APIFormat: llm.APIFormatTypeSafeSystemOne.String(),
			}},
			Settings: &objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{
				{Model: "chat-only", APIFormats: []string{llm.APIFormatOpenAIChatCompletion.String()}},
				{Model: "jev-latest", APIFormats: []string{llm.APIFormatTypeSafeSystemOne.String()}},
			}},
		}},
		Models: []biz.ChannelModelEntry{
			{RequestModel: "chat-only", ActualModel: "chat-only"},
			{RequestModel: "jev-latest", ActualModel: "jev-latest"},
		},
	}

	result := populateAPIFormat(context.Background(), []*ChannelModelsCandidate{candidate}, &llm.Request{
		Model:       "requested-model",
		RequestType: llm.RequestTypeSystemOne,
		APIFormat:   llm.APIFormatTypeSafeSystemOne,
	})

	require.Len(t, result, 1)
	require.Equal(t, []biz.ChannelModelEntry{{RequestModel: "jev-latest", ActualModel: "jev-latest"}}, candidate.Models)
	require.Equal(t, []string{llm.APIFormatTypeSafeSystemOne.String()}, candidate.modelAPIFormats)
}
