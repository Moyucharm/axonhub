package zen

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestResponsesAliasesPreserveCustomToolsAndNamespaces(t *testing.T) {
	for _, choice := range []string{
		`{"type":"custom","name":"edit"}`,
		`{"type":"allowed_tools","mode":"required","tools":[{"type":"custom","name":"edit"},{"type":"custom","name":"axonhub_edit"}]}`,
	} {
		t.Run(choice, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{
				"model":"test","stream":true,
				"input":[
					{"role":"user","content":"literal _axonhub_edit"},
					{"type":"custom_tool_call","id":"ct_old","call_id":"custom_old","name":"edit","input":"literal _axonhub_edit"},
					{"type":"custom_tool_call_output","call_id":"custom_old","output":"edited"},
					{"type":"function_call","id":"fc_old","call_id":"function_old","namespace":"edit","name":"read","arguments":"{\"path\":\"a\"}"},
					{"type":"function_call_output","call_id":"function_old","output":"contents"}
				],
				"tools":[
					{"type":"custom","name":"edit","description":"client editor","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}},
					{"type":"custom","name":"axonhub_edit","format":{"type":"text"}},
					{"type":"namespace","name":"edit","description":"client namespace","tools":[
						{"type":"function","name":"read","parameters":{"type":"object","properties":{"path":{"type":"string"}}},"strict":true},
						{"type":"custom","name":"render","format":{"type":"text"}}
					]}
				],
				"tool_choice":%s
			}`, choice))
			request, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: raw})
			require.NoError(t, err)
			before, err := json.Marshal(request)
			require.NoError(t, err)
			extensions := llm.CloneProviderExtensions(request.ProviderExtensions)
			outbound := newTestResponsesOutbound(t)
			wire, err := outbound.TransformRequest(t.Context(), request)
			require.NoError(t, err)

			assert.Equal(t, map[string]string{"_axonhub_edit": "edit"}, toolAliases(wire))
			assert.Equal(t, "custom", gjson.GetBytes(wire.Body, "tools.0.type").String())
			assert.Equal(t, "_axonhub_edit", gjson.GetBytes(wire.Body, "tools.0.name").String())
			assert.Equal(t, "client editor", gjson.GetBytes(wire.Body, "tools.0.description").String())
			assert.JSONEq(t, gjson.GetBytes(raw, "tools.0.format").Raw, gjson.GetBytes(wire.Body, "tools.0.format").Raw)
			assert.Equal(t, "axonhub_edit", gjson.GetBytes(wire.Body, "tools.1.name").String())
			assert.JSONEq(t, gjson.GetBytes(raw, "tools.2").Raw, gjson.GetBytes(wire.Body, "tools.2").Raw)
			assert.Equal(t, append([]string{"_axonhub_edit", "axonhub_edit", "edit"}, coreToolNames...), toolNames(t, decodeBody(t, wire.Body)))

			if gjson.Get(choice, "type").String() == "custom" {
				assert.Equal(t, "_axonhub_edit", gjson.GetBytes(wire.Body, "tool_choice.name").String())
			} else {
				assert.Equal(t, "required", gjson.GetBytes(wire.Body, "tool_choice.mode").String())
				assert.Equal(t, "_axonhub_edit", gjson.GetBytes(wire.Body, "tool_choice.tools.0.name").String())
				assert.Equal(t, "axonhub_edit", gjson.GetBytes(wire.Body, "tool_choice.tools.1.name").String())
			}
			customHistory, functionHistory := false, false
			for _, item := range gjson.GetBytes(wire.Body, "input").Array() {
				switch item.Get("type").String() {
				case "custom_tool_call":
					customHistory = true
					assert.Equal(t, "_axonhub_edit", item.Get("name").String())
					assert.Equal(t, "literal _axonhub_edit", item.Get("input").String())
				case "function_call":
					functionHistory = true
					assert.Equal(t, "edit", item.Get("namespace").String())
					assert.Equal(t, "read", item.Get("name").String())
					assert.JSONEq(t, `{"path":"a"}`, item.Get("arguments").String())
				}
			}

			assert.True(t, customHistory)
			assert.True(t, functionHistory)
			assert.Contains(t, gjson.GetBytes(wire.Body, "input.0.content").Raw, "literal _axonhub_edit")

			finalized := outbound.FinalizeTransportRequest(wire)
			assert.JSONEq(t, string(wire.Body), string(finalized.Body))
			assert.Equal(t, finalized.Body, finalized.JSONBody)
			second := outbound.FinalizeTransportRequest(finalized)
			assert.JSONEq(t, string(finalized.Body), string(second.Body))
			after, err := json.Marshal(request)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after))
			assert.Equal(t, extensions, llm.CloneProviderExtensions(request.ProviderExtensions))
		})
	}
}

func TestResponsesToolAliasesAvoidOtherToolTypes(t *testing.T) {
	raw := []byte(`{
		"model":"test",
		"input":[
			{"role":"user","content":"hello"},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"contents"}
		],
		"tools":[
			{"type":"function","name":"read","parameters":{"type":"object","properties":{}}},
			{"type":"custom","name":"axonhub_read","format":{"type":"text"}},
			{"type":"custom","name":"edit","format":{"type":"text"}},
			{"type":"namespace","name":"axonhub_edit","tools":[{"type":"function","name":"read","parameters":{"type":"object","properties":{}}}]}
		],
		"tool_choice":{"type":"function","name":"read"}
	}`)
	request, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: raw})
	require.NoError(t, err)
	wire, err := newTestResponsesOutbound(t).TransformRequest(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"_axonhub_read": "read", "_axonhub_edit": "edit"}, toolAliases(wire))
	assert.Equal(t, append([]string{"_axonhub_read", "axonhub_read", "_axonhub_edit", "axonhub_edit"}, coreToolNames...), toolNames(t, decodeBody(t, wire.Body)))
	assert.JSONEq(t, gjson.GetBytes(raw, "tools.3").Raw, gjson.GetBytes(wire.Body, "tools.3").Raw)
	assert.Equal(t, "_axonhub_read", gjson.GetBytes(wire.Body, `input.#(type=="function_call").name`).String())
	assert.Equal(t, "_axonhub_read", gjson.GetBytes(wire.Body, "tool_choice.name").String())
}

func TestResponsesToolAliasesAvoidNamespaceMemberNames(t *testing.T) {
	raw := []byte(`{
		"model":"test","input":"hello",
		"tools":[
			{"type":"custom","name":"edit","format":{"type":"text"}},
			{"type":"namespace","name":"client","tools":[
				{"type":"function","name":"axonhub_edit","parameters":{"type":"object","properties":{}}},
				{"type":"custom","name":"_axonhub_edit","format":{"type":"text"}}
			]}
		]
	}`)
	request, err := responses.NewInboundTransformer().TransformRequest(t.Context(), &httpclient.Request{Body: raw})
	require.NoError(t, err)
	wire, err := newTestResponsesOutbound(t).TransformRequest(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"__axonhub_edit": "edit"}, toolAliases(wire))
	assert.Equal(t, "__axonhub_edit", gjson.GetBytes(wire.Body, "tools.0.name").String())
	assert.JSONEq(t, gjson.GetBytes(raw, "tools.1").Raw, gjson.GetBytes(wire.Body, "tools.1").Raw)

	// A namespaced function's done event need not repeat its namespace.
	done := []byte(`{"type":"response.function_call_arguments.done","name":"axonhub_edit","arguments":"{}"}`)
	assert.JSONEq(t, string(done), string(restoreToolNames(done, toolAliases(wire))))
}

func TestReconcileResponsesToolsPreservesNonFunctionNames(t *testing.T) {
	body := []byte(`{"tools":[{"type":"custom","name":"edit","format":{"type":"text"}},{"type":"namespace","name":"read","tools":[{"type":"function","name":"file","parameters":{"type":"object","properties":{}}}]}]}`)
	first, err := reconcileTools(body, llm.APIFormatOpenAIResponse)
	require.NoError(t, err)
	assert.Equal(t, append([]string{"edit", "read"}, coreToolNames...), toolNames(t, decodeBody(t, first)))
	for i := range 2 {
		path := fmt.Sprintf("tools.%d", i)
		assert.JSONEq(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(first, path).Raw)
	}
	second, err := reconcileTools(first, llm.APIFormatOpenAIResponse)
	require.NoError(t, err)
	assert.JSONEq(t, string(first), string(second))
}

func TestRestoreResponsesToolNamesRespectsNamespaces(t *testing.T) {
	body := []byte(`{
		"type":"response.completed","sequence_number":9007199254740993,
		"response":{
			"tool_choice":{"type":"custom","name":"axonhub_edit"},
			"tools":[{"type":"namespace","name":"edit","tools":[{"type":"function","name":"axonhub_edit"}]}],
			"output":[
				{"type":"custom_tool_call","name":"axonhub_edit","input":"literal axonhub_edit"},
				{"type":"function_call","namespace":"edit","name":"axonhub_edit","arguments":"{\"name\":\"axonhub_edit\"}"}
			]
		}
	}`)
	restored := restoreToolNames(body, map[string]string{"axonhub_edit": "edit"})
	assert.Equal(t, "edit", gjson.GetBytes(restored, "response.tool_choice.name").String())
	assert.Equal(t, "edit", gjson.GetBytes(restored, "response.output.0.name").String())
	assert.Equal(t, "literal axonhub_edit", gjson.GetBytes(restored, "response.output.0.input").String())
	assert.JSONEq(t, gjson.GetBytes(body, "response.output.1").Raw, gjson.GetBytes(restored, "response.output.1").Raw)
	assert.JSONEq(t, gjson.GetBytes(body, "response.tools").Raw, gjson.GetBytes(restored, "response.tools").Raw)
	assert.Contains(t, string(restored), "9007199254740993")

	done := []byte(`{"type":"response.custom_tool_call_input.done","name":"axonhub_edit","input":"literal axonhub_edit"}`)
	assert.JSONEq(t, `{"type":"response.custom_tool_call_input.done","name":"edit","input":"literal axonhub_edit"}`, string(restoreToolNames(done, map[string]string{"axonhub_edit": "edit"})))
}

func TestResponsesCustomToolNamesRestoreAcrossResponsePaths(t *testing.T) {
	outbound := newTestResponsesOutbound(t)
	request := newResponsesRequest("test")
	request.Tools = []llm.Tool{{Type: llm.ToolTypeResponsesCustomTool, ResponseCustomTool: &llm.ResponseCustomTool{Name: "edit", Format: &llm.ResponseCustomToolFormat{Type: "text"}}}}
	wire, err := outbound.TransformRequest(t.Context(), request)
	require.NoError(t, err)
	body := []byte(`{"id":"resp_1","object":"response","model":"test","status":"completed","output":[{"type":"custom_tool_call","id":"ct_1","call_id":"call_1","name":"axonhub_edit","input":"literal axonhub_edit"}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`)

	t.Run("response", func(t *testing.T) {
		response, err := outbound.TransformResponse(t.Context(), &httpclient.Response{StatusCode: 200, Request: wire, Body: body})
		require.NoError(t, err)
		require.Len(t, response.Choices, 1)
		require.NotNil(t, response.Choices[0].Message)
		require.Len(t, response.Choices[0].Message.ToolCalls, 1)
		call := response.Choices[0].Message.ToolCalls[0].ResponseCustomToolCall
		require.NotNil(t, call)
		assert.Equal(t, "edit", call.Name)
		assert.Equal(t, "literal axonhub_edit", call.Input)
	})

	var events []*httpclient.StreamEvent
	for _, data := range []string{
		`{"type":"response.created","response":{"id":"resp_1","object":"response","model":"test","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"ct_1","call_id":"call_1","name":"axonhub_edit","input":""}}`,
		`{"type":"response.custom_tool_call_input.delta","item_id":"ct_1","output_index":0,"delta":"literal axonhub_edit"}`,
		`{"type":"response.custom_tool_call_input.done","item_id":"ct_1","output_index":0,"name":"axonhub_edit","input":"literal axonhub_edit"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","id":"ct_1","call_id":"call_1","name":"axonhub_edit","input":"literal axonhub_edit"}}`,
		fmt.Sprintf(`{"type":"response.completed","response":%s}`, body),
	} {
		events = append(events, &httpclient.StreamEvent{Data: []byte(data)})
	}

	t.Run("stream", func(t *testing.T) {
		stream, err := outbound.TransformStream(t.Context(), wire, streams.SliceStream(events))
		require.NoError(t, err)
		defer stream.Close()
		found := false
		for stream.Next() {
			for _, choice := range stream.Current().Choices {
				if choice.Delta == nil {
					continue
				}
				for _, call := range choice.Delta.ToolCalls {
					if call.ResponseCustomToolCall != nil && call.ResponseCustomToolCall.Name != "" {
						assert.Equal(t, "edit", call.ResponseCustomToolCall.Name)
						found = true
					}
				}
			}
		}
		require.NoError(t, stream.Err())
		assert.True(t, found)
	})

	t.Run("aggregate", func(t *testing.T) {
		body, _, err := outbound.AggregateStreamChunks(t.Context(), wire, events)
		require.NoError(t, err)
		assert.Equal(t, "edit", gjson.GetBytes(body, "output.0.name").String())
		assert.Equal(t, "literal axonhub_edit", gjson.GetBytes(body, "output.0.input").String())
	})
}
