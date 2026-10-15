package zen

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/llm"
)

// Official OpenCode tool definitions, not executable tools. See OPENCODE_LICENSE.md
// for their MIT license and pinned source. The gateway never executes them.
//
//go:embed opencode_tools.json
var officialToolsJSON []byte

var coreToolNames = []string{"shell", "edit", "glob", "grep", "read"}

var officialTools = func() map[string]llm.Function {
	var definitions map[string]llm.Function
	if err := json.Unmarshal(officialToolsJSON, &definitions); err != nil {
		panic(err)
	}
	return definitions
}()

// reconcileTools renders the same definitions in each provider's native shape.
// Client tools with reserved names are aliased before serialization, so their
// descriptions and argument schemas are never silently replaced.
func reconcileTools(body []byte, format llm.APIFormat) ([]byte, error) {
	result := gjson.GetBytes(body, "tools")
	if result.Exists() && result.Type != gjson.Null && !result.IsArray() {
		return nil, errors.New("tools must be an array")
	}
	tools := make([]json.RawMessage, 0, len(result.Array())+len(coreToolNames))
	for _, tool := range result.Array() {
		// A Responses namespace or custom tool is not an official function,
		// even when its name matches one of the required definitions.
		if format == llm.APIFormatOpenAIResponse && tool.Get("type").String() != llm.ToolTypeFunction {
			tools = append(tools, json.RawMessage(tool.Raw))
			continue
		}
		name := tool.Get("name").String()
		if format == llm.APIFormatOpenAIChatCompletion {
			name = tool.Get("function.name").String()
		}
		if _, known := officialTools[name]; known {
			// Reapply genuine definitions after channel body overrides too.
			continue
		}
		tools = append(tools, json.RawMessage(tool.Raw))
	}
	for _, name := range coreToolNames {
		definition := officialTools[name]
		var tool any
		switch format {
		case llm.APIFormatOpenAIChatCompletion:
			definition.Name = name
			tool = llm.Tool{Type: llm.ToolTypeFunction, Function: definition}
		case llm.APIFormatAnthropicMessage:
			tool = map[string]any{"name": name, "description": definition.Description, "input_schema": definition.Parameters}
		default:
			tool = map[string]any{"type": "function", "name": name, "description": definition.Description, "parameters": definition.Parameters}
		}
		encoded, err := json.Marshal(tool)
		if err != nil {
			return nil, err
		}
		tools = append(tools, encoded)
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "tools", encoded)
}
