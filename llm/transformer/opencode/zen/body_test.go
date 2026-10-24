package zen

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
)

func TestChatCompletionLimitPreservesExplicitFields(t *testing.T) {
	for _, test := range []struct {
		name       string
		fields     string
		completion string
		legacy     string
	}{
		{name: "default", completion: "32000"},
		{name: "completion", fields: `,"max_completion_tokens":123`, completion: "123"},
		{name: "completion zero", fields: `,"max_completion_tokens":0`, completion: "0"},
		{name: "completion null", fields: `,"max_completion_tokens":null`, completion: "null"},
		{name: "legacy", fields: `,"max_tokens":456`, legacy: "456"},
		{name: "legacy zero", fields: `,"max_tokens":0`, legacy: "0"},
		{name: "legacy null", fields: `,"max_tokens":null`, legacy: "null"},
		{name: "both", fields: `,"max_tokens":0,"max_completion_tokens":null`, completion: "null", legacy: "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := reconcileRequestBody([]byte(`{"model":"test","messages":[]` + test.fields + `}`))
			require.NoError(t, err)
			assert.Equal(t, test.completion, gjson.GetBytes(body, "max_completion_tokens").Raw)
			assert.Equal(t, test.legacy, gjson.GetBytes(body, "max_tokens").Raw)
		})
	}
}

func TestChatBodyLayoutPreservesPrecisionAndStringContents(t *testing.T) {
	arguments := `{"literal":"\\u003c","markup":"<argument>&","number":9007199254740993}`
	body, err := json.Marshal(map[string]any{
		"model": "<model>&",
		"messages": []any{map[string]any{
			"role":    "assistant",
			"content": "<message>&",
			"tool_calls": []any{map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":      "client",
					"arguments": arguments,
				},
			}},
		}},
		"zeta":  "<last>&",
		"alpha": json.RawMessage(`{"large":123456789012345678901234567890,"html":"\u003craw\u003e\u0026","literal":"\\u003c"}`),
	})
	require.NoError(t, err)

	body, err = reconcileRequestBody(body)
	require.NoError(t, err)
	var keys []string
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		keys = append(keys, key.String())
		return true
	})
	assert.Equal(t, []string{"model", "messages", "tools", "stream", "stream_options", "max_completion_tokens", "alpha", "tool_choice", "zeta"}, keys)
	assert.Equal(t, "123456789012345678901234567890", gjson.GetBytes(body, "alpha.large").Raw)
	assert.Equal(t, `<raw>&`, gjson.GetBytes(body, "alpha.html").String())
	assert.Equal(t, `\u003c`, gjson.GetBytes(body, "alpha.literal").String())
	assert.Equal(t, arguments, gjson.GetBytes(body, "messages.0.tool_calls.0.function.arguments").String())
	assert.Contains(t, string(body), `"model":"<model>&"`)
	assert.Contains(t, string(body), `"content":"<message>&"`)
	assert.Contains(t, string(body), `"html":"<raw>&"`)
	assert.Contains(t, string(body), `<argument>&`)
	assert.Contains(t, string(body), `"zeta":"<last>&"`)
}

func TestNormalizeJSONBodyPreservesNestedRawValues(t *testing.T) {
	body := []byte(`{"nested":[{"html":"\u003cspan\u003e\u0026","literal":"\\u003c","integer":9007199254740993}],"arguments":"{\"html\":\"\\u003c\",\"text\":\"<>&\"}"}`)
	normalized, err := normalizeJSONBody(body)
	require.NoError(t, err)
	assert.Contains(t, string(normalized), `"html":"<span>&"`)
	assert.Equal(t, `\u003c`, gjson.GetBytes(normalized, "nested.0.literal").String())
	assert.Equal(t, "9007199254740993", gjson.GetBytes(normalized, "nested.0.integer").Raw)
	assert.Equal(t, gjson.GetBytes(body, "arguments").String(), gjson.GetBytes(normalized, "arguments").String())
	second, err := normalizeJSONBody(normalized)
	require.NoError(t, err)
	assert.Equal(t, normalized, second)
}

func TestChatBodyFinalizationIsByteIdempotent(t *testing.T) {
	outbound := newTestOutbound(t)
	request, err := outbound.TransformRequest(t.Context(), newChatRequest())
	require.NoError(t, err)
	request.Body = []byte(`{"model":"test","messages":[{"role":"user","content":"\u003cuser\u003e\u0026"}],"max_tokens":null,"tools":[{"type":"function","function":{"name":"axonhub_read","strict":true,"parameters":{"type":"object","const":9007199254740993}}}]}`)

	first := outbound.FinalizeTransportRequest(request)
	require.NotSame(t, request, first)
	assert.Equal(t, first.Body, []byte(first.JSONBody))
	second := outbound.FinalizeTransportRequest(first)
	require.NotSame(t, first, second)
	assert.Equal(t, first.Body, second.Body)
	assert.Equal(t, second.Body, []byte(second.JSONBody))
	assert.Equal(t, "null", gjson.GetBytes(second.Body, "max_tokens").Raw)
	assert.False(t, gjson.GetBytes(second.Body, "max_completion_tokens").Exists())
	assert.Len(t, gjson.GetBytes(second.Body, "tools").Array(), len(coreToolNames)+1)
	assert.Equal(t, "9007199254740993", gjson.GetBytes(second.Body, "tools.0.function.parameters.const").Raw)
}

func TestOfficialChatStrictDoesNotChangeClientDefinitions(t *testing.T) {
	body, err := reconcileRequestBody([]byte(`{"model":"test","messages":[],"tools":[{"type":"function","function":{"name":"client_true","strict":true,"parameters":{"type":"object","properties":{"value":{"type":"integer","const":9007199254740993}}}}},{"type":"function","function":{"name":"client_false","strict":false}},{"type":"function","function":{"name":"client_null","strict":null}},{"type":"function","function":{"name":"client_absent"}}]}`))
	require.NoError(t, err)
	for i, expected := range []string{"true", "false", "null", ""} {
		tools := gjson.GetBytes(body, "tools").Array()
		assert.Equal(t, expected, tools[i].Get("function.strict").Raw)
	}
	assert.Equal(t, "9007199254740993", gjson.GetBytes(body, "tools.0.function.parameters.properties.value.const").Raw)
	for _, tool := range gjson.GetBytes(body, "tools").Array()[4:] {
		assert.Equal(t, "false", tool.Get("function.strict").Raw)
	}
}

func TestChatAliasedClientStrictAndSchemaSurviveFinalization(t *testing.T) {
	outbound := newTestOutbound(t)
	request := newChatRequest()
	strict := true
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"<client>&"},"id":{"type":"integer","const":9007199254740993}},"required":["path"]}`)
	request.Tools = []llm.Tool{{
		Type: llm.ToolTypeFunction,
		Function: llm.Function{
			Name:        "read",
			Description: "<client read>&",
			Parameters:  schema,
			Strict:      &strict,
		},
	}}
	wire, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	wire = outbound.FinalizeTransportRequest(wire)
	client := gjson.GetBytes(wire.Body, "tools.0.function")
	assert.Equal(t, "axonhub_read", client.Get("name").String())
	assert.Equal(t, "true", client.Get("strict").Raw)
	assert.JSONEq(t, string(schema), client.Get("parameters").Raw)
	assert.Equal(t, "9007199254740993", client.Get("parameters.properties.id.const").Raw)
	assert.Contains(t, string(wire.Body), `"description":"<client>&"`)
	assert.Equal(t, map[string]string{"axonhub_read": "read"}, toolAliases(wire))
}

func TestResponsesToolReconciliationPreservesCustomAndNamespace(t *testing.T) {
	custom := `{"type":"custom","name":"read","format":{"type":"text"}}`
	namespace := `{"type":"namespace","name":"shell","tools":[{"type":"function","name":"read","parameters":{"const":9007199254740993}}]}`
	body, err := reconcileTools([]byte(`{"tools":[`+custom+`,`+namespace+`]}`), llm.APIFormatOpenAIResponse)
	require.NoError(t, err)
	tools := gjson.GetBytes(body, "tools").Array()
	require.Len(t, tools, len(coreToolNames)+2)
	assert.JSONEq(t, custom, tools[0].Raw)
	assert.JSONEq(t, namespace, tools[1].Raw)
	assert.Equal(t, "9007199254740993", tools[1].Get("tools.0.parameters.const").Raw)
	for _, tool := range tools[2:] {
		assert.False(t, tool.Get("strict").Exists())
	}
}

func TestShellDescriptionMatchesHostPlatform(t *testing.T) {
	base := "Commands run on Linux using bash."
	assert.Equal(t, "Commands run on Windows using powershell.", shellDescriptionForOS(base, "windows"))
	assert.Equal(t, base, shellDescriptionForOS(base, "linux"))
	assert.Equal(t, base, shellDescriptionForOS(base, "darwin"))
}
