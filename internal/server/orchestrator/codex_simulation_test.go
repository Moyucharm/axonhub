package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func newSimulationOutbound(t *testing.T, settings *objects.ChannelSettings) *PersistentOutboundTransformer {
	t.Helper()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:       7,
			Name:     "sim-channel",
			Type:     channel.TypeOpenaiResponses,
			Settings: settings,
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest:       &llm.Request{},
		},
	}

	return outbound
}

func simulationSettings(preset objects.CodexSimulationPreset) *objects.CodexSimulationSettings {
	return &objects.CodexSimulationSettings{
		Enabled:  true,
		Preset:   preset,
		Options:  lo.ToPtr(objects.PresetDefaultOptions(preset)),
		Strategy: objects.NewCodexSimulationStrategy(),
	}
}

func runSimulationBody(t *testing.T, outbound *PersistentOutboundTransformer, body string) (string, http.Header) {
	t.Helper()

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(body),
		Headers:   make(http.Header),
	}

	bodyMiddleware := applyCodexSimulationBody(outbound)
	processed, err := bodyMiddleware.OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)

	headerMiddleware := applyCodexSimulationHeaders(outbound)
	processed, err = headerMiddleware.OnOutboundRawRequest(context.Background(), processed)
	require.NoError(t, err)

	return string(processed.Body), processed.Headers
}

// 仅 UA：只修改 User-Agent，不改 body、不加任何其它身份请求头。
func TestCodexSimulationUAOnly(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetUA),
	})

	body, headers := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)

	require.JSONEq(t, `{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`, body)
	require.Equal(t, "codex_cli_rs/0.144.2 (Windows 10; x86_64)", headers.Get("User-Agent"))
	require.Equal(t, "", headers.Get("originator"))
	require.Equal(t, "", headers.Get("version"))
	require.Equal(t, "", headers.Get("x-codex-beta-features"))
	require.Equal(t, "", headers.Get("x-codex-turn-metadata"))
	require.Equal(t, "", headers.Get("x-client-request-id"))
	require.Equal(t, "", headers.Get("thread-id"))
}

// 仅 UA 对 Lite 模型也使用 exec UA 变体。
func TestCodexSimulationUAOnlyLiteUserAgent(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetUA),
	})

	_, headers := runSimulationBody(t, outbound, `{"model":"gpt-5.6-sol","input":"hi"}`)

	require.Equal(t, "codex_exec/0.144.2 (Windows 10; x86_64) tmux/3.5a (codex_exec; 0.144.2)", headers.Get("User-Agent"))
}

func TestCodexSimulationUserAgentsUseSamePlatform(t *testing.T) {
	sim := simulationSettings(objects.CodexSimulationPresetUA)
	sim.Platform = objects.CodexSimulationPlatformMacOS
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{CodexSimulation: sim})

	_, standardHeaders := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":"hi"}`)
	_, liteHeaders := runSimulationBody(t, outbound, `{"model":"gpt-5.6-sol","input":"hi"}`)

	require.Equal(t, "codex_cli_rs/0.144.2 (macOS 15.0; x86_64)", standardHeaders.Get("User-Agent"))
	require.Equal(t, "codex_exec/0.144.2 (macOS 15.0; x86_64) tmux/3.5a (codex_exec; 0.144.2)", liteHeaders.Get("User-Agent"))
}

func TestCodexSimulationCustomVersionAndUserAgentsRemainStable(t *testing.T) {
	sim := simulationSettings(objects.CodexSimulationPresetNormal)
	sim.Version = "0.145.0-custom"
	sim.StandardUserAgent = "my-codex-standard/0.145.0"
	sim.LiteUserAgent = "my-codex-lite/0.145.0"
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{CodexSimulation: sim})

	_, firstHeaders := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":"hi"}`)
	_, secondHeaders := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":"hi"}`)
	require.Equal(t, "my-codex-standard/0.145.0", firstHeaders.Get("User-Agent"))
	require.Equal(t, "my-codex-standard/0.145.0", secondHeaders.Get("User-Agent"))
	require.Equal(t, "0.145.0-custom", firstHeaders.Get("version"))

	_, liteHeaders := runSimulationBody(t, outbound, `{"model":"gpt-5.6-sol","input":"hi"}`)
	require.Equal(t, "my-codex-lite/0.145.0", liteHeaders.Get("User-Agent"))
	require.Empty(t, liteHeaders.Get("version"))
}

// 普通级别 - 非 Lite 模型：身份请求头 + input 数组化 + include + client_metadata。
func TestCodexSimulationNormalNonLite(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})

	body, headers := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":"1 + 2 equals 3, right?","max_output_tokens":4096}`)

	require.Equal(t, "codex_cli_rs/0.144.2 (Windows 10; x86_64)", headers.Get("User-Agent"))
	require.Equal(t, "codex_cli_rs", headers.Get("originator"))
	require.Equal(t, "0.144.2", headers.Get("version"))
	require.Equal(t, "terminal_resize_reflow", headers.Get("x-codex-beta-features"))
	require.Equal(t, "text/event-stream", headers.Get("accept"))
	require.Equal(t, "", headers.Get("x-openai-internal-codex-responses-lite"))
	require.NotEmpty(t, headers.Get("x-codex-installation-id"))
	require.NotEmpty(t, headers.Get("x-codex-window-id"))
	require.NotEmpty(t, headers.Get("x-codex-turn-metadata"))
	require.NotEmpty(t, headers.Get("thread-id"))
	require.Equal(t, headers.Get("thread-id"), headers.Get("x-client-request-id"))

	parsed := gjson.Parse(body)
	require.Equal(t, "message", parsed.Get("input.0.type").String())
	require.Equal(t, "user", parsed.Get("input.0.role").String())
	require.Equal(t, "1 + 2 equals 3, right?", parsed.Get("input.0.content.0.text").String())
	require.True(t, parsed.Get("include").Array()[0].String() == "reasoning.encrypted_content")
	require.NotEmpty(t, parsed.Get("client_metadata.x-codex-installation-id").String())
	require.NotEmpty(t, parsed.Get("client_metadata.x-codex-window-id").String())
	// 非 Lite：不强制 stream/store，不动 max_output_tokens，不生成 prompt_cache_key。
	require.False(t, parsed.Get("stream").Exists())
	require.Equal(t, float64(4096), parsed.Get("max_output_tokens").Float())
	require.False(t, parsed.Get("prompt_cache_key").Exists())
}

// 普通级别 - Lite 模型：完整 Lite 请求形态。
func TestCodexSimulationNormalLite(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})

	body, headers := runSimulationBody(t, outbound, `{"model":"gpt-5.6-sol","instructions":"you are codex","input":"1 + 2?","tools":[{"type":"function","name":"exec","description":"run"}],"max_output_tokens":4096,"service_tier":"flex"}`)

	// 请求头：Lite 变体。
	require.Equal(t, "codex_exec/0.144.2 (Windows 10; x86_64) tmux/3.5a (codex_exec; 0.144.2)", headers.Get("User-Agent"))
	require.Equal(t, "codex_exec", headers.Get("originator"))
	require.Equal(t, "", headers.Get("version"))
	require.Equal(t, "remote_compaction_v2", headers.Get("x-codex-beta-features"))
	require.Equal(t, "true", headers.Get("x-openai-internal-codex-responses-lite"))

	parsed := gjson.Parse(body)
	// additional_tools 位于 input 首项，收纳顶层 tools；instructions 的 system 消息紧随其后。
	require.Equal(t, "additional_tools", parsed.Get("input.0.type").String())
	require.Equal(t, "developer", parsed.Get("input.0.role").String())
	require.Equal(t, "exec", parsed.Get("input.0.tools.0.name").String())
	require.Equal(t, "message", parsed.Get("input.1.type").String())
	require.Equal(t, "system", parsed.Get("input.1.role").String())
	require.Equal(t, "you are codex", parsed.Get("input.1.content.0.text").String())
	// 顶层字段被移除/强制。
	require.False(t, parsed.Get("instructions").Exists())
	require.False(t, parsed.Get("tools").Exists())
	require.False(t, parsed.Get("max_output_tokens").Exists())
	require.False(t, parsed.Get("service_tier").Exists())
	require.True(t, parsed.Get("stream").Bool())
	require.False(t, parsed.Get("store").Bool())
	require.Equal(t, "auto", parsed.Get("tool_choice").String())
	require.False(t, parsed.Get("parallel_tool_calls").Bool())
	require.Equal(t, "medium", parsed.Get("text.verbosity").String())
	require.Equal(t, "medium", parsed.Get("reasoning.effort").String())
	require.Equal(t, "all_turns", parsed.Get("reasoning.context").String())
	require.True(t, len(parsed.Get("prompt_cache_key").String()) == 36)
	require.NotNil(t, outbound.state.LlmRequest.Stream)
	require.True(t, *outbound.state.LlmRequest.Stream)
	// client_metadata 携带渠道身份。
	require.Equal(t, headers.Get("x-codex-installation-id"), parsed.Get("client_metadata.x-codex-installation-id").String())
	require.NotEmpty(t, parsed.Get("client_metadata.thread_id").String())
	require.NotEmpty(t, parsed.Get("client_metadata.turn_id").String())
	require.NotEmpty(t, parsed.Get("client_metadata.x-codex-turn-metadata").String())
}

// 加强级别：普通级别 + wait 工具；已存在 wait 时不重复注入。
func TestCodexSimulationEnhancedWaitTool(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetEnhanced),
	})

	body, _ := runSimulationBody(t, outbound, `{"model":"gpt-5.6-terra","input":"hi"}`)

	parsed := gjson.Parse(body)
	waitTool := parsed.Get("input.0.tools.#(name==\"wait\")")
	require.True(t, waitTool.Exists())
	require.Equal(t, "function", waitTool.Get("type").String())
	require.Equal(t, "cell_id", waitTool.Get("parameters.required.0").String())
	require.False(t, waitTool.Get("strict").Bool())
}

func TestCodexSimulationEnhancedDoesNotDuplicateWaitTool(t *testing.T) {
	customTool := map[string]any{"type": "function", "name": "wait", "description": "existing"}
	existingBody, err := json.Marshal(map[string]any{
		"model": "gpt-5.6-luna",
		"input": []any{
			map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{customTool}},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		},
	})
	require.NoError(t, err)

	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetEnhanced),
	})

	body, _ := runSimulationBody(t, outbound, string(existingBody))

	parsed := gjson.Parse(body)
	require.True(t, parsed.Get("input.0.tools.#(name==\"wait\")").Exists())
	require.Equal(t, int64(1), parsed.Get("input.0.tools.#").Int())
}

// 加强级别对非 Lite 模型不注入工具（与 codex-disguise 一致：Lite 专属）。
func TestCodexSimulationEnhancedNonLiteNoWaitTool(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetEnhanced),
	})

	body, _ := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":"hi","tools":[{"type":"function","name":"exec","description":"run"}]}`)

	parsed := gjson.Parse(body)
	require.True(t, parsed.Get("tools").Exists())
	require.False(t, parsed.Get("input.0.tools").Exists())
}

func TestCodexSimulationAdditionalToolWithoutPrompt(t *testing.T) {
	sim := simulationSettings(objects.CodexSimulationPresetEnhanced)
	sim.Options = lo.ToPtr(objects.CodexSimulationOptions{AdditionalTool: true})
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{CodexSimulation: sim})

	body, headers := runSimulationBody(t, outbound, `{"model":"gpt-5.6-sol","input":"hi","tools":[{"type":"function","name":"exec"}]}`)

	parsed := gjson.Parse(body)
	require.Equal(t, "additional_tools", parsed.Get("input.0.type").String())
	require.Equal(t, "wait", parsed.Get("input.0.tools.0.name").String())
	require.Equal(t, "message", parsed.Get("input.1.type").String())
	require.Equal(t, "exec", parsed.Get("tools.0.name").String())
	require.Empty(t, headers)
}

// 子选项联动：只开 prompt 时仅做 input 数组化，其它一律不动。
func TestCodexSimulationOptionGating(t *testing.T) {
	sim := simulationSettings(objects.CodexSimulationPresetNormal)
	sim.Options = lo.ToPtr(objects.CodexSimulationOptions{Prompt: true})

	outbound := newSimulationOutbound(t, &objects.ChannelSettings{CodexSimulation: sim})

	body, headers := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":"hi"}`)

	parsed := gjson.Parse(body)
	require.Equal(t, "message", parsed.Get("input.0.type").String())
	require.False(t, parsed.Get("include").Exists())
	require.False(t, parsed.Get("client_metadata").Exists())
	require.Equal(t, "", headers.Get("User-Agent"))
	require.Equal(t, "", headers.Get("originator"))
}

func TestCodexSimulationAllOptionsDisabledIsNoop(t *testing.T) {
	sim := simulationSettings(objects.CodexSimulationPresetNormal)
	sim.Options = &objects.CodexSimulationOptions{}
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{CodexSimulation: sim})
	originalBody := `{"model":"gpt-5.6-sol","input":"hi"}`

	body, headers := runSimulationBody(t, outbound, originalBody)

	require.Equal(t, originalBody, body)
	require.Empty(t, headers)
}

func TestCodexSimulationNonTargetChannelTypeIsNoop(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})
	outbound.state.CurrentCandidate.Channel.Type = channel.TypeOpenai
	originalBody := `{"model":"gpt-5.6-sol","input":"hi"}`

	body, headers := runSimulationBody(t, outbound, originalBody)

	require.Equal(t, originalBody, body)
	require.Empty(t, headers)
}

// 无效 JSON body 时安全降级：保留原 body 不报错。
func TestCodexSimulationInvalidJSONBodyKeepsOriginal(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte("not-json"),
		Headers:   make(http.Header),
	}

	processed, err := applyCodexSimulationBody(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "not-json", string(processed.Body))
}

// PassThroughBody 生效时跳过 body 变换（headers 仍可模拟）。
func TestCodexSimulationSkipsBodyWhenPassThroughApplied(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})
	outbound.state.PassThroughApplied = true

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"model":"gpt-5.5","input":"raw"}`),
		Headers:   make(http.Header),
	}

	processed, err := applyCodexSimulationBody(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-5.5","input":"raw"}`, string(processed.Body))
}

// 同一请求的 failover 尝试共享 turn metadata。
func TestCodexSimulationFailoverSharesTurnMetadata(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"model":"gpt-5.5","input":"hi"}`),
		Headers:   make(http.Header),
	}

	headerMiddleware := applyCodexSimulationHeaders(outbound)

	for attempt := 0; attempt < 3; attempt++ {
		processed, err := headerMiddleware.OnOutboundRawRequest(context.Background(), request)
		require.NoError(t, err)

		turnMetadata := processed.Headers.Get("x-codex-turn-metadata")
		require.NotEmpty(t, turnMetadata)

		var meta map[string]any
		require.NoError(t, json.Unmarshal([]byte(turnMetadata), &meta))
		require.Equal(t, "user", meta["thread_source"])
		require.Equal(t, "seccomp", meta["sandbox"])
		require.Equal(t, "turn", meta["request_kind"])
		require.NotEmpty(t, meta["turn_id"])
		require.NotEmpty(t, meta["turn_started_at_unix_ms"])
		require.NotEmpty(t, meta["window_id"])
	}
}

// 每个渠道独立策略：installation_id/thread_id 来自各自的 strategy。
func TestCodexSimulationPerChannelStrategyIsolated(t *testing.T) {
	outboundA := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})
	outboundB := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})

	requestA := &httpclient.Request{APIFormat: string(llm.APIFormatOpenAIResponse), Body: []byte(`{"model":"gpt-5.5","input":"hi"}`), Headers: make(http.Header)}
	requestB := &httpclient.Request{APIFormat: string(llm.APIFormatOpenAIResponse), Body: []byte(`{"model":"gpt-5.5","input":"hi"}`), Headers: make(http.Header)}

	headerMiddleware := applyCodexSimulationHeaders(outboundA)
	processedA, err := headerMiddleware.OnOutboundRawRequest(context.Background(), requestA)
	require.NoError(t, err)

	headerMiddleware = applyCodexSimulationHeaders(outboundB)
	processedB, err := headerMiddleware.OnOutboundRawRequest(context.Background(), requestB)
	require.NoError(t, err)

	require.NotEqual(t, processedA.Headers.Get("x-codex-installation-id"), processedB.Headers.Get("x-codex-installation-id"))
	require.NotEqual(t, processedA.Headers.Get("thread-id"), processedB.Headers.Get("thread-id"))
}

// 未启用模拟（或未配置）时完全不动。
func TestCodexSimulationDisabledIsNoop(t *testing.T) {
	settings := simulationSettings(objects.CodexSimulationPresetNormal)
	settings.Enabled = false

	outbound := newSimulationOutbound(t, &objects.ChannelSettings{CodexSimulation: settings})

	body, headers := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":"hi"}`)

	require.JSONEq(t, `{"model":"gpt-5.5","input":"hi"}`, body)
	require.Equal(t, "", headers.Get("User-Agent"))
	require.Equal(t, "", headers.Get("originator"))

	// 未配置也是 no-op。
	outboundNil := newSimulationOutbound(t, nil)

	body, headers = runSimulationBody(t, outboundNil, `{"model":"gpt-5.5","input":"hi"}`)
	require.JSONEq(t, `{"model":"gpt-5.5","input":"hi"}`, body)
	require.Equal(t, "", headers.Get("User-Agent"))
}

// 非 Responses API 格式不生效。
func TestCodexSimulationNonResponsesAPIFormatIsNoop(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIChatCompletion),
		Body:      []byte(`{"model":"gpt-5.5","input":"hi"}`),
		Headers:   make(http.Header),
	}

	processed, err := applyCodexSimulationBody(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-5.5","input":"hi"}`, string(processed.Body))

	processed, err = applyCodexSimulationHeaders(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "", processed.Headers.Get("originator"))
}

// 入站 Codex 请求头透传。
func TestCodexSimulationPassthroughInboundHeaders(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:       7,
			Name:     "sim-channel",
			Type:     channel.TypeOpenaiResponses,
			Settings: &objects.ChannelSettings{CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal)},
		},
		Outbound: &mockTransformer{},
	}

	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
			LlmRequest: &llm.Request{
				RawRequest: &httpclient.Request{
					Headers: http.Header{
						"X-Openai-Subagent":     {"sub-1"},
						"X-Codex-Turn-State":    {"state-1"},
						"X-Codex-Turn-Metadata": {`{"turn_id":"turn_abc"}`},
					},
				},
			},
		},
	}

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"model":"gpt-5.5","input":"hi"}`),
		Headers:   make(http.Header),
	}

	processed, err := applyCodexSimulationHeaders(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "sub-1", processed.Headers.Get("x-openai-subagent"))
	require.Equal(t, "state-1", processed.Headers.Get("x-codex-turn-state"))

	// client_metadata 也透传 metadata 头。
	processed, err = applyCodexSimulationBody(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(processed.Body), `"x-openai-subagent":"sub-1"`))
	require.Equal(t, `{"turn_id":"turn_abc"}`, gjson.GetBytes(processed.Body, "client_metadata.x-codex-turn-metadata").String())
}

// client_metadata 若非对象时安全降级（保留原 body）。
func TestCodexSimulationInvalidClientMetadataDegrades(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})

	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"model":"gpt-5.5","input":"hi","client_metadata":"oops"}`),
		Headers:   make(http.Header),
	}

	processed, err := applyCodexSimulationBody(outbound).OnOutboundRawRequest(context.Background(), request)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-5.5","input":"hi","client_metadata":"oops"}`, string(processed.Body))

	processed, err = applyCodexSimulationHeaders(outbound).OnOutboundRawRequest(context.Background(), processed)
	require.NoError(t, err)
	require.Empty(t, processed.Headers)
}

func TestCodexSimulationPreservesLargeJSONNumbers(t *testing.T) {
	outbound := newSimulationOutbound(t, &objects.ChannelSettings{
		CodexSimulation: simulationSettings(objects.CodexSimulationPresetNormal),
	})

	body, _ := runSimulationBody(t, outbound, `{"model":"gpt-5.5","input":"hi","metadata":{"large":9007199254740993}}`)

	require.Equal(t, "9007199254740993", gjson.Get(body, "metadata.large").Raw)
}
