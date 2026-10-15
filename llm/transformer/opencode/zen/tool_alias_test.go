package zen

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
)

func TestToolAliasesAvoidExistingClientNames(t *testing.T) {
	outbound := newTestOutbound(t)
	request := newChatRequest()
	request.Tools = []llm.Tool{
		{Type: llm.ToolTypeFunction, Function: llm.Function{Name: "read"}},
		{Type: llm.ToolTypeFunction, Function: llm.Function{Name: "axonhub_read"}},
		{Type: llm.ToolTypeFunction, Function: llm.Function{Name: "write"}},
	}
	wire, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"_axonhub_read": "read", "axonhub_write": "write"}, toolAliases(wire))
	assert.Equal(t, append([]string{"_axonhub_read", "axonhub_read", "axonhub_write"}, coreToolNames...), toolNames(t, decodeBody(t, wire.Body)))
}

func TestRestoreToolNamesPreservesArgumentsTextAndNumbers(t *testing.T) {
	body := []byte(`{"type":"response.completed","sequence_number":9007199254740993,"response":{"output":[{"type":"function_call","name":"axonhub_read","arguments":"{\"name\":\"axonhub_read\"}"},{"type":"tool_use","name":"axonhub_read","input":{"name":"axonhub_read","function":{"name":"axonhub_read"}}},{"type":"message","content":[{"type":"output_text","text":"axonhub_read"}]}]}}`)
	restored := restoreToolNames(body, map[string]string{"axonhub_read": "read"})
	assert.JSONEq(t, `{"type":"response.completed","sequence_number":9007199254740993,"response":{"output":[{"type":"function_call","name":"read","arguments":"{\"name\":\"axonhub_read\"}"},{"type":"tool_use","name":"read","input":{"name":"axonhub_read","function":{"name":"axonhub_read"}}},{"type":"message","content":[{"type":"output_text","text":"axonhub_read"}]}]}}`, string(restored))
	assert.Contains(t, string(restored), "9007199254740993")
	assert.Contains(t, string(body), `"name":"axonhub_read"`)
}

func TestReconcileToolsIsIdempotentAndRejectsInvalidLists(t *testing.T) {
	for _, format := range nativeFormats {
		first, err := reconcileTools([]byte(`{"tools":[]}`), format)
		require.NoError(t, err)
		second, err := reconcileTools(first, format)
		require.NoError(t, err)
		assert.JSONEq(t, string(first), string(second))
		_, err = reconcileTools([]byte(`{"tools":{"bad":"shape"}}`), format)
		require.ErrorContains(t, err, "tools must be an array")
	}
}

func TestOfficialToolsContainCompleteSchemas(t *testing.T) {
	for _, name := range coreToolNames {
		definition, exists := officialTools[name]
		require.True(t, exists)
		assert.NotEmpty(t, definition.Description)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(definition.Parameters, &schema))
		assert.Equal(t, false, schema["additionalProperties"])
		assert.NotEmpty(t, schema["required"])
	}
}
