package zen

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func TestZenProviderFamilyRouting(t *testing.T) {
	for _, format := range nativeFormats {
		for _, key := range []string{PublicAPIKey, "selected-user-key"} {
			for _, base := range []string{DefaultBaseURL, InferenceBaseURL + "/v1"} {
				t.Run(format.String()+"/"+key+"/"+base, func(t *testing.T) {
					outbound := protocolOutbound(t, format, base, key)
					request, err := outbound.TransformRequest(t.Context(), newChatRequest())
					require.NoError(t, err)
					parsed, err := url.Parse(request.URL)
					require.NoError(t, err)
					path := "/inference/openai/v1/chat/completions"
					switch format {
					case llm.APIFormatOpenAIResponse:
						path = "/inference/openai/v1/responses"
					case llm.APIFormatAnthropicMessage:
						path = "/inference/anthropic/v1/messages"
						assert.Equal(t, "true", parsed.Query().Get("beta"))
					}
					assert.Equal(t, path, parsed.Path)
					assert.Equal(t, key, request.Auth.APIKey)
					assert.Empty(t, request.Headers.Values("Accept"))
				})
			}
		}
	}
}

func TestZenCustomInferenceEndpointRetainsRouting(t *testing.T) {
	for _, format := range nativeFormats {
		for _, target := range []string{
			"https://gateway.example/zen/v1/custom",
			"https://opencode.ai/zen/v1/custom",
			"https://opencode.ai:443/zen/v1/custom",
		} {
			t.Run(format.String()+"/"+target, func(t *testing.T) {
				request := &httpclient.Request{
					URL: target + "?route=private", APIFormat: format.String(),
					Auth: &httpclient.AuthConfig{APIKey: PublicAPIKey},
				}
				require.NoError(t, applyIdentityFingerprint(request))
				parsed, err := url.Parse(request.URL)
				require.NoError(t, err)
				assert.Equal(t, "private", parsed.Query().Get("route"))
				assert.Equal(t, target, parsed.Scheme+"://"+parsed.Host+parsed.Path)
			})
		}
	}
}

func TestZenSubagentIdentitySurvivesOverrides(t *testing.T) {
	outbound := newTestOutbound(t)
	source := newChatRequest()
	source.RawRequest = &httpclient.Request{Headers: make(http.Header)}
	source.RawRequest.Headers.Set(sessionHeader, "conversation")
	source.RawRequest.Headers.Set(sessionIDHeader, "child-session")
	source.RawRequest.Headers.Set("X-Parent-Session-Id", "parent-session")
	ctx := shared.WithSessionScope(t.Context(), "caller-1")
	request, err := outbound.TransformRequest(ctx, source)
	require.NoError(t, err)
	root := request.Headers.Get(sessionHeader)
	child := request.Headers.Get(sessionIDHeader)
	assert.NotEqual(t, root, child)
	assert.Regexp(t, canonicalSessionPattern, root)
	assert.Regexp(t, canonicalSessionPattern, child)
	assert.Equal(t, root, request.Headers.Get("X-Parent-Session-Id"))
	assert.Equal(t, root, request.Headers.Get("X-Opencode-Parent-Session-Id"))
	assert.Equal(t, root, request.Headers.Get("X-Session-Affinity"))
	request.Headers.Set(sessionHeader, "spoofed-root")
	request.Headers.Set(sessionIDHeader, "spoofed-child")
	request.Headers.Set("X-Parent-Session-Id", "spoofed-parent")
	request.Headers.Set("Accept", "application/json, text/event-stream")
	finalized := outbound.FinalizeTransportRequest(request)
	assert.Equal(t, root, finalized.Headers.Get(sessionHeader))
	assert.Equal(t, child, finalized.Headers.Get(sessionIDHeader))
	assert.Equal(t, root, finalized.Headers.Get("X-Parent-Session-Id"))
	assert.Empty(t, finalized.Headers.Get("Accept"))
	assert.Equal(t, "spoofed-root", request.Headers.Get(sessionHeader))

	other, err := outbound.TransformRequest(shared.WithSessionScope(ctx, "caller-2"), source)
	require.NoError(t, err)
	assert.NotEqual(t, root, other.Headers.Get(sessionHeader))
	assert.NotEqual(t, child, other.Headers.Get(sessionIDHeader))
	assert.NotEqual(t, finalized.Headers.Get(projectHeader), other.Headers.Get(projectHeader))
}

func TestZenOfficialSessionsRemainStableAcrossScopes(t *testing.T) {
	root, err := generateOpenCodeID("ses")
	require.NoError(t, err)
	child, err := generateOpenCodeID("ses")
	require.NoError(t, err)
	outbound := newTestOutbound(t)
	request := newChatRequest()
	request.RawRequest = &httpclient.Request{Headers: make(http.Header)}
	request.RawRequest.Headers.Set(sessionHeader, root)
	request.RawRequest.Headers.Set(sessionIDHeader, child)
	project := strings.Repeat("a", 40)
	request.RawRequest.Headers.Set(projectHeader, project)
	for _, scope := range []string{"caller-1", "caller-2"} {
		wire, err := outbound.TransformRequest(shared.WithSessionScope(t.Context(), scope), request)
		require.NoError(t, err)
		assert.Equal(t, root, wire.Headers.Get(sessionHeader))
		assert.Equal(t, child, wire.Headers.Get(sessionIDHeader))
		assert.Equal(t, project, wire.Headers.Get(projectHeader))
	}
}

func TestZenConversationPreludeSeparatesAffinity(t *testing.T) {
	outbound := newTestOutbound(t)
	request := newChatRequest()
	request.Messages = append([]llm.Message{{Role: "system", Content: llm.MessageContent{Content: lo.ToPtr("project A")}}}, request.Messages...)
	first, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	request.Messages = append(request.Messages, llm.Message{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("next turn")}})
	next, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, first.Headers.Get(sessionHeader), next.Headers.Get(sessionHeader))
	request.Messages[0].Content.Content = lo.ToPtr("project B")
	other, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	assert.NotEqual(t, first.Headers.Get(sessionHeader), other.Headers.Get(sessionHeader))
}

func TestZenMessagesBetaSurvivesQueryAndHeaderOverrides(t *testing.T) {
	outbound := protocolOutbound(t, llm.APIFormatAnthropicMessage, DefaultBaseURL, "user-key")
	request, err := outbound.TransformRequest(t.Context(), newChatRequest())
	require.NoError(t, err)
	request.Query = url.Values{"beta": {"false"}, "route": {"kept"}}
	request.Headers.Set("Anthropic-Beta", "spoofed")
	request.Headers.Set("Anthropic-Version", "spoofed")
	finalized := outbound.(transformer.TransportRequestFinalizer).FinalizeTransportRequest(request)
	raw, err := httpclient.BuildHttpRequest(t.Context(), finalized)
	require.NoError(t, err)
	assert.Equal(t, []string{"true"}, raw.URL.Query()["beta"])
	assert.Equal(t, "kept", raw.URL.Query().Get("route"))
	assert.Equal(t, "interleaved-thinking-2025-05-14", finalized.Headers.Get("Anthropic-Beta"))
	assert.Equal(t, "2023-06-01", finalized.Headers.Get("Anthropic-Version"))
	assert.Equal(t, "false", request.Query.Get("beta"))
	assert.Equal(t, "spoofed", request.Headers.Get("Anthropic-Beta"))
}
