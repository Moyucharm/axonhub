package zen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

func protocolOutbound(t *testing.T, format llm.APIFormat, baseURL, key string) transformer.Outbound {
	t.Helper()
	config := &Config{BaseURL: baseURL, APIKeyProvider: auth.NewStaticKeyProvider(key)}
	var outbound transformer.Outbound
	var err error
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		outbound, err = NewOutboundTransformerWithConfig(config)
	case llm.APIFormatOpenAIResponse:
		outbound, err = NewResponsesOutboundTransformerWithConfig(&ResponsesConfig{BaseURL: baseURL, APIKeyProvider: config.APIKeyProvider})
	case llm.APIFormatAnthropicMessage:
		outbound, err = NewMessagesOutboundTransformerWithConfig(config)
	default:
		t.Fatalf("unexpected format %s", format)
	}
	require.NoError(t, err)
	return outbound
}

var nativeFormats = []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage}

func TestNativeProtocolsPreserveSelectedKeyAfterAuthFinalization(t *testing.T) {
	for _, format := range nativeFormats {
		for _, key := range []string{PublicAPIKey, "user-key"} {
			t.Run(format.String()+"/"+key, func(t *testing.T) {
				outbound := protocolOutbound(t, format, DefaultBaseURL, key)
				request, err := outbound.TransformRequest(t.Context(), newChatRequest())
				require.NoError(t, err)
				originalSession := request.Headers.Get(sessionHeader)
				request, err = httpclient.FinalizeAuthHeaders(request)
				require.NoError(t, err)
				require.Nil(t, request.Auth)
				request.Headers.Set("Authorization", "Bearer override")
				request.Headers.Set("X-API-Key", "override")
				request.Headers.Set(sessionIDHeader, "foreign-session")
				request.Headers.Set(requestHeader, "legacy-request")
				request.Headers["x-opencode-request"] = []string{"legacy-lowercase"}
				request.Headers["authorization"] = []string{"Bearer lowercase-override"}
				request.Headers.Set("User-Agent", "third-party")
				finalized := outbound.(transformer.TransportRequestFinalizer).FinalizeTransportRequest(request)
				require.NotNil(t, finalized.Auth)
				assert.Equal(t, key, finalized.Auth.APIKey)
				assert.Equal(t, originalSession, finalized.Headers.Get(sessionIDHeader))
				assert.Empty(t, finalized.Headers.Get(requestHeader))
				assert.NotContains(t, finalized.Headers, "x-opencode-request")
				assert.NotContains(t, finalized.Headers, "authorization")
				assert.Equal(t, "legacy-request", request.Headers.Get(requestHeader))
				assert.Equal(t, OfficialUA, finalized.Headers.Get("User-Agent"))
				if format == llm.APIFormatAnthropicMessage {
					assert.Equal(t, key, finalized.Headers.Get("X-API-Key"))
					assert.Empty(t, finalized.Headers.Get("Authorization"))
				} else {
					assert.Equal(t, "Bearer "+key, finalized.Headers.Get("Authorization"))
					assert.Empty(t, finalized.Headers.Get("X-API-Key"))
				}
				if key == PublicAPIKey {
					assert.Contains(t, finalized.URL, AnonymousBaseURL)
				} else {
					assert.Contains(t, finalized.URL, DefaultBaseURL)
				}
				metadata, err := json.Marshal(finalized.TransformerMetadata)
				require.NoError(t, err)
				assert.NotContains(t, string(metadata), "user-key")
				assert.Equal(t, finalized.Body, []byte(finalized.JSONBody))
				body := decodeBody(t, finalized.Body)
				assert.Equal(t, coreToolNames, toolNames(t, body))
				for _, tool := range body["tools"].([]any) {
					definition := tool.(map[string]any)
					if format == llm.APIFormatOpenAIChatCompletion {
						definition = definition["function"].(map[string]any)
					}
					name := definition["name"].(string)
					assert.Equal(t, officialTools[name].Description, definition["description"])
					schemaKey := "parameters"
					if format == llm.APIFormatAnthropicMessage {
						schemaKey = "input_schema"
					}
					encoded, err := json.Marshal(definition[schemaKey])
					require.NoError(t, err)
					assert.JSONEq(t, string(officialTools[name].Parameters), string(encoded))
				}
				second := outbound.(transformer.TransportRequestFinalizer).FinalizeTransportRequest(finalized)
				assert.JSONEq(t, string(finalized.Body), string(second.Body))
			})
		}
	}
}

func TestZenIdentityKeepsConversationStable(t *testing.T) {
	outbound := newTestOutbound(t)
	request := newChatRequest()
	request.RawRequest = &httpclient.Request{Headers: http.Header{sessionIDHeader: []string{"third-party-session"}}}
	first, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	request.Messages = append(request.Messages, llm.Message{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("next turn")}})
	second, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, first.Headers.Get(sessionHeader), second.Headers.Get(sessionHeader))
	assert.Regexp(t, canonicalSessionPattern, second.Headers.Get(sessionHeader))
	request.RawRequest.Headers.Set(sessionIDHeader, "another-session")
	third, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	assert.NotEqual(t, first.Headers.Get(sessionHeader), third.Headers.Get(sessionHeader))

	canonical, err := generateOpenCodeID("ses")
	require.NoError(t, err)
	assert.Equal(t, canonical, canonicalSessionID(canonical))
	ctx := shared.WithSessionScope(shared.WithSessionID(t.Context(), "context-session"), "project-1")
	request.RawRequest = nil
	fourth, err := outbound.TransformRequest(ctx, request)
	require.NoError(t, err)
	fifth, err := outbound.TransformRequest(shared.WithSessionScope(ctx, "project-2"), request)
	require.NoError(t, err)
	assert.NotEqual(t, fourth.Headers.Get(sessionHeader), fifth.Headers.Get(sessionHeader))
}

func TestZenCustomBaseIsNotMigrated(t *testing.T) {
	for _, format := range nativeFormats {
		outbound := protocolOutbound(t, format, "https://gateway.example/zen/v1", PublicAPIKey)
		request, err := outbound.TransformRequest(t.Context(), newChatRequest())
		require.NoError(t, err)
		assert.Contains(t, request.URL, "https://gateway.example/zen/v1/")
	}
}

func TestNativeProtocolsAliasClientToolsAndHistory(t *testing.T) {
	for _, format := range nativeFormats {
		t.Run(format.String(), func(t *testing.T) {
			outbound := protocolOutbound(t, format, DefaultBaseURL, PublicAPIKey)
			request := newChatRequest()
			request.Tools = []llm.Tool{{Type: llm.ToolTypeFunction, Function: llm.Function{Name: "read", Description: "client read", Parameters: json.RawMessage(`{"type":"object","properties":{"filePath":{"type":"string"}},"required":["filePath"]}`)}}}
			request.Messages = append(request.Messages,
				llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "read", Arguments: `{"filePath":"a"}`}}}},
				llm.Message{Role: "tool", ToolCallID: lo.ToPtr("call_1"), Content: llm.MessageContent{Content: lo.ToPtr("contents")}},
			)
			request.ToolChoice = &llm.ToolChoice{NamedToolChoice: &llm.NamedToolChoice{Type: "function", Function: llm.ToolFunction{Name: "read"}}}
			wire, err := outbound.TransformRequest(t.Context(), request)
			require.NoError(t, err)
			assert.Equal(t, "read", request.Tools[0].Function.Name)
			assert.Equal(t, "read", request.Messages[1].ToolCalls[0].Function.Name)
			assert.Equal(t, "read", request.ToolChoice.NamedToolChoice.Function.Name)
			body := decodeBody(t, wire.Body)
			assert.Equal(t, append([]string{"axonhub_read"}, coreToolNames...), toolNames(t, body))
			assert.Contains(t, string(wire.Body), `"filePath"`)
			assert.Contains(t, string(wire.Body), `"axonhub_read"`)
			assert.Equal(t, map[string]string{"axonhub_read": "read"}, toolAliases(wire))
		})
	}
}

// SSE fixtures deliberately return a colliding client tool name, exercising
// both streaming translation and downstream non-streaming auto-aggregation.
func toolCallSSE(format llm.APIFormat) string {
	var events []string
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		events = []string{
			`{"id":"chat_1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"axonhub_read","arguments":"{\"filePath\":\"a\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chat_1","object":"chat.completion.chunk","model":"test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			`[DONE]`,
		}
	case llm.APIFormatOpenAIResponse:
		events = []string{
			`{"type":"response.created","response":{"id":"resp_1","object":"response","model":"test","status":"in_progress","output":[]}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"axonhub_read","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"filePath\":\"a\"}"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"axonhub_read","arguments":"{\"filePath\":\"a\"}"}}`,
			`{"type":"response.completed","response":{"id":"resp_1","object":"response","model":"test","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"axonhub_read","arguments":"{\"filePath\":\"a\"}"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`,
		}
	case llm.APIFormatAnthropicMessage:
		events = []string{
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"test","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"axonhub_read","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"filePath\":\"a\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
			`{"type":"message_stop"}`,
		}
	}
	var sse strings.Builder
	for _, event := range events {
		if kind := gjson.Get(event, "type").String(); kind != "" {
			fmt.Fprintf(&sse, "event: %s\n", kind)
		}
		fmt.Fprintf(&sse, "data: %s\n\n", event)
	}
	return sse.String()
}

func TestZenPipelineToolCallsStreamAndAggregate(t *testing.T) {
	for _, format := range nativeFormats {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", format, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, OfficialUA, r.UserAgent())
					assert.Empty(t, r.Header.Get(requestHeader))
					assert.Equal(t, r.Header.Get(sessionHeader), r.Header.Get(sessionIDHeader))
					if format == llm.APIFormatAnthropicMessage {
						assert.Equal(t, "user-key", r.Header.Get("X-API-Key"))
					} else {
						assert.Equal(t, "Bearer user-key", r.Header.Get("Authorization"))
					}
					body, err := io.ReadAll(r.Body)
					assert.NoError(t, err)
					assert.True(t, gjson.GetBytes(body, "stream").Bool())
					assert.Contains(t, string(body), "axonhub_read")
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, toolCallSSE(format))
				}))
				defer server.Close()
				outbound := protocolOutbound(t, format, server.URL+"/v1", "user-key")
				result, err := zenTestPipeline(outbound).Process(t.Context(), &httpclient.Request{
					Headers: http.Header{"Content-Type": []string{"application/json"}},
					Body:    []byte(fmt.Sprintf(`{"model":"test","stream":%t,"messages":[{"role":"user","content":"read a"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object","properties":{"filePath":{"type":"string"}}}}}]}`, stream)),
				})
				require.NoError(t, err)
				assert.Equal(t, stream, result.Stream)
				if !stream {
					assert.Equal(t, "read", gjson.GetBytes(result.Response.Body, "choices.0.message.tool_calls.0.function.name").String())
					assert.JSONEq(t, `{"filePath":"a"}`, gjson.GetBytes(result.Response.Body, "choices.0.message.tool_calls.0.function.arguments").String())
					assert.Equal(t, int64(10), gjson.GetBytes(result.Response.Body, "usage.prompt_tokens").Int())
					return
				}
				defer result.EventStream.Close()
				found := false
				for result.EventStream.Next() {
					data := result.EventStream.Current().Data
					assert.NotContains(t, string(data), "axonhub_read")
					if gjson.GetBytes(data, "choices.0.delta.tool_calls.0.function.name").String() == "read" {
						found = true
					}
				}
				require.NoError(t, result.EventStream.Err())
				assert.True(t, found)
			})
		}
	}
}

// Mirrors the orchestrator's post-override transport finalizer, after the
// pipeline's FinalizeAuthHeaders step. No production server or database needed.
func zenTestPipeline(outbound transformer.Outbound) interface {
	Process(context.Context, *httpclient.Request) (*pipeline.Result, error)
} {
	return pipeline.NewFactory(httpclient.NewHttpClient()).Pipeline(openai.NewInboundTransformer(), outbound,
		pipeline.WithMiddlewares(pipeline.OnRawRequest("zen-finalizer", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
			return outbound.(transformer.TransportRequestFinalizer).FinalizeTransportRequest(request), nil
		})))
}

func TestZenLiveAnonymous(t *testing.T) {
	if os.Getenv("AXONHUB_ZEN_LIVE_TEST") != "1" {
		t.Skip("set AXONHUB_ZEN_LIVE_TEST=1 to send fixed public-credential probes")
	}
	for _, tc := range []struct {
		format llm.APIFormat
		model  string
	}{
		{llm.APIFormatOpenAIChatCompletion, DefaultModel},
		{llm.APIFormatOpenAIResponse, museSparkContributorFreeModel},
	} {
		t.Run(tc.format.String(), func(t *testing.T) {
			outbound := protocolOutbound(t, tc.format, DefaultBaseURL, PublicAPIKey)
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			result, err := zenTestPipeline(outbound).Process(ctx, &httpclient.Request{
				Headers: http.Header{"Content-Type": []string{"application/json"}},
				Body:    []byte(fmt.Sprintf(`{"model":%q,"stream":false,"max_tokens":128,"messages":[{"role":"user","content":"Reply with OK only. Do not use tools."}]}`, tc.model)),
			})
			require.NoError(t, err)
			require.False(t, result.Stream)
			assert.Equal(t, http.StatusOK, result.Response.StatusCode)
			// A reasoning model may complete entirely in its reasoning channel.
			// Check protocol output, not stochastic final-answer wording. Never
			// synthesize assistant content from reasoning to make this pass.
			content := gjson.GetBytes(result.Response.Body, "choices.0.message.content").String()
			reasoning := gjson.GetBytes(result.Response.Body, "choices.0.message.reasoning_content").String()
			assert.NotEmpty(t, content+reasoning)
			assert.Positive(t, gjson.GetBytes(result.Response.Body, "usage.total_tokens").Int())
			t.Logf("model=%s response=%s", tc.model, result.Response.Body)
		})
	}
}
