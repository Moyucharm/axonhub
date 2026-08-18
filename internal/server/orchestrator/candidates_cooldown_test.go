package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type staticCandidateSelector struct {
	candidates []*ChannelModelsCandidate
}

func (s staticCandidateSelector) Select(context.Context, *llm.Request) ([]*ChannelModelsCandidate, error) {
	return s.candidates, nil
}

func TestChannelCooldownFilterSelector_ExcludesActiveCooldownInProduction(t *testing.T) {
	until := time.Now().Add(30 * time.Minute)
	cooling := &biz.Channel{Channel: &ent.Channel{ID: 1, CooldownUntil: &until}}
	available := &biz.Channel{Channel: &ent.Channel{ID: 2}}
	selector := WithChannelCooldownFilterSelector(staticCandidateSelector{candidates: []*ChannelModelsCandidate{
		{Channel: cooling},
		{Channel: available},
	}})

	candidates, err := selector.Select(context.Background(), &llm.Request{Model: "gpt-4"})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, 2, candidates[0].Channel.ID)
}

func TestChannelCooldownFilterSelector_BypassesCooldownForTests(t *testing.T) {
	until := time.Now().Add(30 * time.Minute)
	cooling := &biz.Channel{Channel: &ent.Channel{ID: 1, CooldownUntil: &until}}
	selector := WithChannelCooldownFilterSelector(staticCandidateSelector{candidates: []*ChannelModelsCandidate{{Channel: cooling}}})
	ctx := contexts.WithSource(context.Background(), request.SourceTest)

	candidates, err := selector.Select(ctx, &llm.Request{Model: "gpt-4"})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, 1, candidates[0].Channel.ID)
}
