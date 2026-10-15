package zen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

const toolAliasesMetadataKey = "opencode_zen_tool_aliases"

// Reserve optional official tools as well as the required core, since Zen
// validates any recognized definition even when that tool is not required.
var reservedToolNames = map[string]bool{
	"bash": true, "shell": true, "edit": true, "glob": true, "grep": true,
	"read": true, "write": true, "execute": true, "question": true,
	"skill": true, "subagent": true, "webfetch": true, "websearch": true,
}

// toolAliasOutbound isolates reserved OpenCode names from third-party tools.
// In particular, Pi's read/edit schemas differ from OpenCode's; substituting
// official schemas under those names would break client-side execution.
type toolAliasOutbound struct {
	transformer.Outbound
}

func (t *toolAliasOutbound) TransformRequest(ctx context.Context, request *llm.Request) (*httpclient.Request, error) {
	if t.APIFormat() == llm.APIFormatOpenAIResponse {
		// Serialize first so aliasing does not invalidate the Responses
		// transformer's raw namespace/tool-choice extension signatures.
		cloned := *request
		httpRequest, err := t.Outbound.TransformRequest(ctx, &cloned)
		if err != nil {
			return nil, err
		}
		body, aliases, err := aliasResponsesRequestBody(httpRequest.Body)
		if err != nil {
			return nil, fmt.Errorf("alias OpenCode Zen Responses tools: %w", err)
		}
		httpRequest.Body = body
		setToolAliases(httpRequest, aliases)
		return httpRequest, nil
	}

	cloned := *request
	cloned.Tools = slices.Clone(request.Tools)
	aliases := make(map[string]string)
	used := make(map[string]bool)
	for _, tool := range request.Tools {
		used[tool.Function.Name] = true
	}
	for i := range cloned.Tools {
		name := cloned.Tools[i].Function.Name
		if cloned.Tools[i].Type != llm.ToolTypeFunction {
			continue
		}
		if !reservedToolNames[name] {
			continue
		}
		alias := "axonhub_" + name
		for used[alias] {
			alias = "_" + alias
		}
		used[alias] = true
		aliases[alias] = name
		cloned.Tools[i].Function.Name = alias
	}
	if len(aliases) > 0 {
		renames := make(map[string]string, len(aliases))
		for alias, name := range aliases {
			renames[name] = alias
		}
		cloned.Messages = slices.Clone(request.Messages)
		for i := range cloned.Messages {
			if name := cloned.Messages[i].Name; name != nil && renames[*name] != "" {
				alias := renames[*name]
				cloned.Messages[i].Name = &alias
			}
			if name := cloned.Messages[i].ToolCallName; name != nil && renames[*name] != "" {
				alias := renames[*name]
				cloned.Messages[i].ToolCallName = &alias
			}
			cloned.Messages[i].ToolCalls = slices.Clone(request.Messages[i].ToolCalls)
			for j := range cloned.Messages[i].ToolCalls {
				call := &cloned.Messages[i].ToolCalls[j]
				if alias := renames[call.Function.Name]; alias != "" {
					call.Function.Name = alias
				}
			}
		}
		if request.ToolChoice != nil {
			choice := *request.ToolChoice
			if choice.NamedToolChoice != nil {
				named := *choice.NamedToolChoice
				if alias := renames[named.Function.Name]; alias != "" {
					named.Function.Name = alias
				}
				choice.NamedToolChoice = &named
			}
			choice.Tools = slices.Clone(choice.Tools)
			for i := range choice.Tools {
				if alias := renames[choice.Tools[i].Name]; alias != "" {
					choice.Tools[i].Name = alias
				}
			}
			cloned.ToolChoice = &choice
		}
	}
	httpRequest, err := t.Outbound.TransformRequest(ctx, &cloned)
	if err != nil {
		return nil, err
	}
	setToolAliases(httpRequest, aliases)
	return httpRequest, nil
}

// Responses custom tools and namespaces must retain their native contracts.
// Only top-level callable tools share names with the injected official functions.
func aliasResponsesRequestBody(body []byte) ([]byte, map[string]string, error) {
	payload := gjson.ParseBytes(body)
	tools := payload.Get("tools").Array()
	used := make(map[string]bool, len(tools))
	for _, tool := range tools {
		used[tool.Get("name").String()] = true
		if tool.Get("type").String() == "namespace" {
			// Done events may omit namespace, so keep aliases distinct from
			// local member names as well as top-level tool names.
			for _, member := range tool.Get("tools").Array() {
				used[member.Get("name").String()] = true
			}
		}
	}
	aliases := make(map[string]string)
	renames := make(map[string]string)
	for i, tool := range tools {
		kind := tool.Get("type").String()
		name := tool.Get("name").String()
		if (kind != "function" && kind != "custom") || !reservedToolNames[name] {
			continue
		}
		alias := "axonhub_" + name
		for used[alias] {
			alias = "_" + alias
		}
		used[alias] = true
		aliases[alias] = name
		renames[name] = alias
		var err error
		body, err = sjson.SetBytes(body, fmt.Sprintf("tools.%d.name", i), alias)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(aliases) == 0 {
		return body, aliases, nil
	}

	paths := []string{"tool_choice"}
	for i := range payload.Get("input").Array() {
		paths = append(paths, fmt.Sprintf("input.%d", i))
	}
	for i := range payload.Get("tool_choice.tools").Array() {
		paths = append(paths, fmt.Sprintf("tool_choice.tools.%d", i))
	}
	for _, path := range paths {
		item := payload.Get(path)
		if item.Get("namespace").String() != "" {
			continue
		}
		switch item.Get("type").String() {
		case "function", "custom", "function_call", "custom_tool_call":
			if alias := renames[item.Get("name").String()]; alias != "" {
				var err error
				body, err = sjson.SetBytes(body, path+".name", alias)
				if err != nil {
					return nil, nil, err
				}
			}
		}
	}
	return body, aliases, nil
}

func setToolAliases(request *httpclient.Request, aliases map[string]string) {
	request.TransformerMetadata = maps.Clone(request.TransformerMetadata)
	if request.TransformerMetadata == nil {
		request.TransformerMetadata = make(map[string]any)
	}
	request.TransformerMetadata[toolAliasesMetadataKey] = aliases
}

func toolAliases(request *httpclient.Request) map[string]string {
	if request == nil {
		return nil
	}
	aliases, _ := request.TransformerMetadata[toolAliasesMetadataKey].(map[string]string)
	return aliases
}

// restoreToolNames only changes name fields in structured protocol envelopes;
// serialized argument strings and user-visible text remain untouched.
func restoreToolNames(body []byte, aliases map[string]string) []byte {
	if len(aliases) == 0 {
		return body
	}
	found := false
	for alias := range aliases {
		if strings.Contains(string(body), alias) {
			found = true
			break
		}
	}
	if !found {
		return body
	}
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil {
		return body
	}
	rename := func(value map[string]any) {
		if namespace, _ := value["namespace"].(string); namespace != "" {
			return
		}
		if name, ok := value["name"].(string); ok {
			if original := aliases[name]; original != "" {
				value["name"] = original
			}
		}
	}
	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			switch value["type"] {
			case "namespace":
				return // Namespace members were never aliased.
			case "function", "custom", "function_call", "custom_tool_call", "tool_use", "server_tool_use",
				"response.function_call_arguments.done", "response.custom_tool_call_input.done":
				rename(value)
			}
			if function, ok := value["function"].(map[string]any); ok {
				rename(function)
			}
			for key, item := range value {
				switch key {
				case "input", "arguments", "metadata", "parameters", "input_schema":
					// Tool inputs are user data, not protocol envelopes.
					continue
				}
				walk(item)
			}
		case []any:
			for _, item := range value {
				walk(item)
			}
		}
	}
	walk(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

func (t *toolAliasOutbound) TransformResponse(ctx context.Context, response *httpclient.Response) (*llm.Response, error) {
	cloned := *response
	cloned.Body = restoreToolNames(response.Body, toolAliases(response.Request))
	return t.Outbound.TransformResponse(ctx, &cloned)
}

func (t *toolAliasOutbound) TransformStream(ctx context.Context, request *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	aliases := toolAliases(request)
	if len(aliases) > 0 {
		headers := httpclient.GetResponseHeaders(stream)
		stream = streams.Map(stream, func(event *httpclient.StreamEvent) *httpclient.StreamEvent {
			cloned := *event
			cloned.Data = restoreToolNames(event.Data, aliases)
			return &cloned
		})
		stream = httpclient.WithResponseHeaders(stream, headers)
	}
	return t.Outbound.TransformStream(ctx, request, stream)
}

func (t *toolAliasOutbound) AggregateStreamChunks(ctx context.Context, request *httpclient.Request, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	body, meta, err := t.Outbound.AggregateStreamChunks(ctx, request, chunks)
	if err != nil {
		return nil, meta, err
	}
	return restoreToolNames(body, toolAliases(request)), meta, nil
}
