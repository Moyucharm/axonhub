package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

// Codex 指纹模拟：将 codex-disguise 的默认模拟行为移植为渠道级配置。
// 仅对 openai_responses 渠道（endpoint transport 为 http 或 websocket）生效，
// 普通级别对齐 codex-disguise 默认行为，加强级别额外注入最小 wait 工具，
// 仅 UA 级别只修改 User-Agent。

const (
	codexOriginator              = "codex_cli_rs"
	codexLiteOriginator          = "codex_exec"
	requiredIncludeField         = "reasoning.encrypted_content"
	codexSimulationBodyFailedKey = "__axonhub_codex_simulation_body_failed"
)

var errCodexSimulationInvalidClientMetadata = errors.New("codex simulation: client_metadata must be a JSON object")

// codexPassthroughRequestHeaders copies inbound Codex headers onto the upstream request.
var codexPassthroughRequestHeaders = []string{
	"x-oai-attestation",
	"x-openai-subagent",
	"x-codex-parent-thread-id",
	"x-codex-turn-metadata",
	"x-codex-turn-state",
}

// codexMetadataHeaderKeys are copied into client_metadata from the inbound request.
var codexMetadataHeaderKeys = []string{
	"x-openai-subagent",
	"x-codex-parent-thread-id",
	"x-codex-turn-metadata",
}

// codexWaitTool is the exact Codex CLI 0.144.2 wait tool schema captured by codex-disguise.
var codexWaitTool = map[string]any{
	"type":        "function",
	"name":        "wait",
	"description": "Waits on a yielded `exec` cell and returns new output or completion.\n- Use `wait` only after `exec` returns `Script running with cell ID ...`.\n- `cell_id` identifies the running `exec` cell to resume.\n- `yield_time_ms` controls how long to wait for more output before yielding again. Defaults to 10000 ms.\n- `max_tokens` limits how much new output this wait call returns. Defaults to 10000 tokens.\n- `terminate: true` stops the running cell; false or omitted waits for output.\n- `wait` returns only the new output since the last yield, or the final completion or termination result for that cell.\n- If the cell is still running, `wait` may yield again with the same `cell_id`.\n- If the cell has already finished, `wait` returns the completed result and closes the cell.",
	"strict":      false,
	"parameters": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cell_id": map[string]any{
				"type":        "string",
				"description": "Identifier of the running exec cell.",
			},
			"max_tokens": map[string]any{
				"type":        "number",
				"description": "Output token budget for this wait call. Defaults to 10000 tokens.",
			},
			"terminate": map[string]any{
				"type":        "boolean",
				"description": "True stops the running exec cell; false or omitted waits for output.",
			},
			"yield_time_ms": map[string]any{
				"type":        "number",
				"description": "Wait before yielding more output. Defaults to 10000 ms.",
			},
		},
		"required":             []any{"cell_id"},
		"additionalProperties": false,
	},
}

// isResponsesLiteModel reports whether the model uses the Responses Lite upstream shape.
// 与 codex-disguise 一致：仅 gpt-5.6- 前缀模型走 Lite 变换。
func isResponsesLiteModel(model string) bool {
	return strings.HasPrefix(model, "gpt-5.6-")
}

// newPrefixedID builds a deterministic-style prefixed id: "<prefix>_<32 hex>".
func newPrefixedID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// codexSimulationEnabledFor reports whether the current channel has codex simulation
// enabled for the request. The API format is checked by the caller.
func codexSimulationEnabledFor(outbound *PersistentOutboundTransformer) (*objects.CodexSimulationSettings, bool) {
	currentChannel := outbound.GetCurrentChannel()
	if currentChannel == nil || currentChannel.Type != channel.TypeOpenaiResponses ||
		currentChannel.Settings == nil || currentChannel.Settings.CodexSimulation == nil {
		return nil, false
	}

	sim := currentChannel.Settings.CodexSimulation
	if !sim.Enabled {
		return nil, false
	}

	return sim, true
}

// codexSimTurn returns the per-request turn identity, cached so failover attempts of the
// same request share identical turn metadata.
func (outbound *PersistentOutboundTransformer) codexSimTurn() (string, int64) {
	if outbound.codexSimTurnID == "" {
		outbound.codexSimTurnID = newPrefixedID("turn")
		outbound.codexSimTurnAtMs = time.Now().UnixMilli()
	}

	return outbound.codexSimTurnID, outbound.codexSimTurnAtMs
}

// codexSimIdentity resolves the per-channel strategy (falling back to an ephemeral one when
// the persisted fingerprint is missing) plus the per-request turn metadata JSON.
func (outbound *PersistentOutboundTransformer) codexSimIdentity(sim *objects.CodexSimulationSettings) (objects.CodexSimulationStrategy, string) {
	strategy := sim.Strategy
	if !biz.IsValidCodexSimulationStrategy(strategy) {
		strategy = objects.NewCodexSimulationStrategy()
	}

	turnID, turnAtMs := outbound.codexSimTurn()

	return strategy, codexTurnMetadata(strategy, turnID, turnAtMs)
}

// codexTurnMetadata builds the x-codex-turn-metadata JSON payload.
func codexTurnMetadata(strategy objects.CodexSimulationStrategy, turnID string, turnAtMs int64) string {
	meta := map[string]any{
		"thread_id":               strategy.ThreadID,
		"thread_source":           "user",
		"turn_id":                 turnID,
		"workspaces":              map[string]any{},
		"sandbox":                 "seccomp",
		"turn_started_at_unix_ms": turnAtMs,
		"request_kind":            "turn",
		"window_id":               codexWindowID(strategy),
	}

	data, err := json.Marshal(meta)
	if err != nil {
		return ""
	}

	return string(data)
}

// codexWindowID derives the window id from the per-channel strategy.
func codexWindowID(strategy objects.CodexSimulationStrategy) string {
	return fmt.Sprintf("%s:%d", strategy.ThreadID, strategy.WindowGeneration)
}

// applyCodexSimulationBody runs after pass-through body substitution and before override
// operations, so user overrides always win. When PassThroughBody is effective the original
// inbound body is forwarded untouched and simulation body changes are skipped.
func applyCodexSimulationBody(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("codex-simulation-body", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		if outbound.state.PassThroughApplied {
			return request, nil
		}

		if request.APIFormat != string(llm.APIFormatOpenAIResponse) {
			return request, nil
		}

		sim, ok := codexSimulationEnabledFor(outbound)
		if !ok {
			return request, nil
		}

		opts := sim.EffectiveOptions()
		if !opts.Prompt && !opts.ClientMetadata && !opts.ResponsesShape && !opts.AdditionalTool {
			return request, nil
		}

		body, err := transformCodexSimulationBody(request.Body, opts, sim, outbound)
		if err != nil {
			if errors.Is(err, errCodexSimulationInvalidClientMetadata) {
				if request.TransformerMetadata == nil {
					request.TransformerMetadata = make(map[string]any)
				}
				request.TransformerMetadata[codexSimulationBodyFailedKey] = true
			}
			log.Warn(ctx, "failed to apply codex simulation body, keeping original body",
				log.String("channel", outbound.GetCurrentChannel().Name),
				log.Cause(err),
			)

			return request, nil
		}

		request.Body = body
		if opts.ResponsesShape && isResponsesLiteModel(gjson.GetBytes(body, "model").String()) {
			forceStreaming := true
			outbound.state.LlmRequest.Stream = &forceStreaming
		}

		return request, nil
	})
}

// applyCodexSimulationHeaders runs after User-Agent pass-through and before header override
// operations, so simulation headers win over pass-through and user overrides win over simulation.
func applyCodexSimulationHeaders(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("codex-simulation-headers", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		if request.APIFormat != string(llm.APIFormatOpenAIResponse) {
			return request, nil
		}

		if request.TransformerMetadata != nil {
			if failed, _ := request.TransformerMetadata[codexSimulationBodyFailedKey].(bool); failed {
				return request, nil
			}
		}

		sim, ok := codexSimulationEnabledFor(outbound)
		if !ok {
			return request, nil
		}

		opts := sim.EffectiveOptions()
		if !opts.UserAgent && !opts.CodexHeaders {
			return request, nil
		}

		transformCodexSimulationHeaders(request, opts, sim, outbound)

		return request, nil
	})
}

// transformCodexSimulationHeaders rewrites the outbound request headers.
func transformCodexSimulationHeaders(
	request *httpclient.Request,
	opts objects.CodexSimulationOptions,
	sim *objects.CodexSimulationSettings,
	outbound *PersistentOutboundTransformer,
) {
	model := gjson.GetBytes(request.Body, "model").String()
	lite := isResponsesLiteModel(model)

	if request.Headers == nil {
		request.Headers = make(http.Header)
	}

	if opts.UserAgent {
		request.Headers.Set("User-Agent", sim.EffectiveUserAgent(lite))
	}

	if !opts.CodexHeaders {
		return
	}

	strategy, turnMetadata := outbound.codexSimIdentity(sim)

	request.Headers.Set("accept", "text/event-stream")
	request.Headers.Set("x-codex-installation-id", strategy.InstallationID)
	request.Headers.Set("x-codex-window-id", codexWindowID(strategy))
	request.Headers.Set("x-codex-turn-metadata", turnMetadata)
	request.Headers.Set("thread-id", strategy.ThreadID)
	request.Headers.Set("thread_id", strategy.ThreadID)
	request.Headers.Set("x-client-request-id", strategy.ThreadID)

	if lite {
		request.Headers.Set("originator", codexLiteOriginator)
		request.Headers.Del("version")
		request.Headers.Set("x-codex-beta-features", "remote_compaction_v2")
		request.Headers.Set("x-openai-internal-codex-responses-lite", "true")
	} else {
		request.Headers.Set("originator", codexOriginator)
		request.Headers.Set("version", sim.EffectiveVersion())
		request.Headers.Set("x-codex-beta-features", "terminal_resize_reflow")
		request.Headers.Del("x-openai-internal-codex-responses-lite")
	}

	// Propagate inbound Codex headers.
	if llmReq := outbound.state.LlmRequest; llmReq != nil && llmReq.RawRequest != nil {
		inboundHeaders := llmReq.RawRequest.Headers
		for _, name := range codexPassthroughRequestHeaders {
			if value := inboundHeaders.Get(name); value != "" {
				request.Headers.Set(name, value)
			}
		}
	}
}

// transformCodexSimulationBody rewrites the outbound request body according to the
// channel simulation options. Non-JSON bodies are rejected with an error so the caller
// can fall back to the original body safely.
func transformCodexSimulationBody(
	body []byte,
	opts objects.CodexSimulationOptions,
	sim *objects.CodexSimulationSettings,
	outbound *PersistentOutboundTransformer,
) ([]byte, error) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("codex simulation: request body is not valid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("codex simulation: request body contains multiple JSON values")
		}
		return nil, fmt.Errorf("codex simulation: request body is not valid JSON: %w", err)
	}

	model, _ := payload["model"].(string)
	lite := isResponsesLiteModel(model)
	strategy, turnMetadata := outbound.codexSimIdentity(sim)

	if opts.Prompt || (lite && opts.AdditionalTool) {
		ensureInputArray(payload)
		if lite {
			if opts.Prompt {
				moveLiteInstructionsToInput(payload)
			}
			ensureLiteAdditionalToolsItem(payload, opts.Prompt)
		}
	}

	if opts.AdditionalTool && lite {
		appendWaitTool(payload)
	}

	if opts.ResponsesShape {
		ensureResponsesInclude(payload)
		if lite {
			applyLiteShape(payload)
		}
	}

	if opts.ClientMetadata {
		if err := ensureClientMetadata(payload, strategy, turnMetadata, outbound, lite); err != nil {
			return nil, fmt.Errorf("codex simulation: %w", err)
		}
	}

	return json.Marshal(payload)
}

// ensureInputArray converts a string input into a message array item, matching
// codex-disguise; some third-party proxies reject a non-array input.
func ensureInputArray(payload map[string]any) {
	input, ok := payload["input"].(string)
	if !ok {
		return
	}

	payload["input"] = []any{
		map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": input}},
		},
	}
}

// toItemList returns the input as a list of items.
func toItemList(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}

	return nil
}

// moveLiteInstructionsToInput migrates the top-level Lite instructions into a system
// message prepended to the input items, matching codex-disguise.
func moveLiteInstructionsToInput(payload map[string]any) {
	instructions, ok := payload["instructions"].(string)
	if !ok || len(instructions) == 0 {
		return
	}

	delete(payload, "instructions")

	item := map[string]any{
		"type":    "message",
		"role":    "system",
		"content": []any{map[string]any{"type": "input_text", "text": instructions}},
	}

	payload["input"] = append([]any{item}, toItemList(payload["input"])...)
}

// ensureLiteAdditionalToolsItem forces an additional_tools item at input index 0 and
// merges the top-level tools into it, matching codex-disguise.
func ensureLiteAdditionalToolsItem(payload map[string]any, mergeTopTools bool) {
	input := toItemList(payload["input"])

	additionalIndex := -1
	for i, item := range input {
		if m, ok := item.(map[string]any); ok && m["type"] == "additional_tools" {
			additionalIndex = i
			break
		}
	}

	var additional map[string]any
	if additionalIndex < 0 {
		additional = map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{}}
	} else {
		additional = input[additionalIndex].(map[string]any)
		input = append(input[:additionalIndex], input[additionalIndex+1:]...)
	}

	if _, ok := additional["role"]; !ok {
		additional["role"] = "developer"
	}

	tools, ok := additional["tools"].([]any)
	if !ok {
		tools = []any{}
	}

	if mergeTopTools {
		if topTools, ok := payload["tools"].([]any); ok {
			tools = append(tools, topTools...)
		}
	}

	additional["tools"] = tools

	payload["input"] = append([]any{additional}, input...)
}

// toolNames collects the names of the tools in an additional_tools item.
func toolNames(tools []any) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if m, ok := tool.(map[string]any); ok {
			if name, ok := m["name"].(string); ok && name != "" {
				names[name] = struct{}{}
			}
		}
	}

	return names
}

// appendWaitTool injects the minimal wait tool into the additional_tools item at input
// index 0 when absent, matching codex-disguise.
func appendWaitTool(payload map[string]any) {
	items := toItemList(payload["input"])
	if len(items) == 0 {
		return
	}

	first, ok := items[0].(map[string]any)
	if !ok || first["type"] != "additional_tools" {
		return
	}

	tools, ok := first["tools"].([]any)
	if !ok {
		tools = []any{}
	}

	if _, exists := toolNames(tools)["wait"]; exists {
		return
	}

	first["tools"] = append(tools, codexWaitTool)
}

// ensureResponsesInclude appends reasoning.encrypted_content to include when missing.
// Some upstreams reject requests without it (400 invalid_codex_request).
func ensureResponsesInclude(payload map[string]any) {
	include, ok := payload["include"].([]any)
	if !ok {
		include = []any{}
	}

	if !slices.Contains(include, requiredIncludeField) {
		payload["include"] = append(include, requiredIncludeField)
	}
}

// applyLiteShape forces the Responses Lite request shape for gpt-5.6-* models,
// matching codex-disguise.
func applyLiteShape(payload map[string]any) {
	delete(payload, "tools")
	delete(payload, "max_output_tokens")
	delete(payload, "service_tier")

	payload["stream"] = true
	payload["store"] = false
	payload["tool_choice"] = "auto"
	payload["parallel_tool_calls"] = false
	payload["include"] = []any{requiredIncludeField}
	payload["text"] = map[string]any{"verbosity": "medium"}

	if _, ok := payload["reasoning"]; !ok {
		payload["reasoning"] = map[string]any{"effort": "medium", "context": "all_turns"}
	}

	if cacheKey, ok := payload["prompt_cache_key"].(string); !ok || strings.TrimSpace(cacheKey) == "" {
		payload["prompt_cache_key"] = uuid.NewString()
	}
}

// setDefault sets the key only when it is absent.
func setDefault(metadata map[string]any, key string, value any) {
	if _, ok := metadata[key]; !ok {
		metadata[key] = value
	}
}

// ensureClientMetadata injects the per-channel identity into client_metadata, matching
// codex-disguise: non-Lite requests get installation/window ids plus inbound metadata
// headers; Lite requests additionally get thread/turn ids and the turn metadata.
func ensureClientMetadata(
	payload map[string]any,
	strategy objects.CodexSimulationStrategy,
	turnMetadata string,
	outbound *PersistentOutboundTransformer,
	lite bool,
) error {
	var metadata map[string]any

	switch raw := payload["client_metadata"].(type) {
	case nil:
		metadata = map[string]any{}
	case map[string]any:
		metadata = raw
	default:
		return errCodexSimulationInvalidClientMetadata
	}

	setDefault(metadata, "x-codex-installation-id", strategy.InstallationID)
	setDefault(metadata, "x-codex-window-id", codexWindowID(strategy))

	if lite {
		setDefault(metadata, "thread_id", strategy.ThreadID)
		turnID, _ := outbound.codexSimTurn()
		setDefault(metadata, "turn_id", turnID)
		if turnMetadata != "" {
			setDefault(metadata, "x-codex-turn-metadata", turnMetadata)
		}
	}

	if llmReq := outbound.state.LlmRequest; llmReq != nil && llmReq.RawRequest != nil {
		inboundHeaders := llmReq.RawRequest.Headers
		for _, name := range codexMetadataHeaderKeys {
			if value := inboundHeaders.Get(name); value != "" {
				setDefault(metadata, name, value)
			}
		}
	}

	payload["client_metadata"] = metadata

	return nil
}
