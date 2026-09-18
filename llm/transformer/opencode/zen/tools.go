package zen

import (
	"encoding/json"

	"github.com/tidwall/gjson"
)

var (
	preparsedChatStubs      []json.RawMessage
	preparsedResponsesStubs []json.RawMessage
)

func init() {
	if err := json.Unmarshal([]byte(stubToolsJSON), &preparsedChatStubs); err != nil {
		panic(err)
	}
	if err := json.Unmarshal([]byte(stubResponsesToolsJSON), &preparsedResponsesStubs); err != nil {
		panic(err)
	}
}

// appendMissingStubTools checks if the given tools contain bash and read,
// and appends the missing stub tool definitions.
// Returns the marshaled tools JSON, a boolean indicating if stubs were appended, and any error.
func appendMissingStubTools(tools []gjson.Result, namePath string, stubs []json.RawMessage) ([]byte, bool, error) {
	hasBash := false
	hasRead := false
	rawTools := make([]json.RawMessage, 0, len(tools)+2)
	for _, tool := range tools {
		rawTools = append(rawTools, json.RawMessage(tool.Raw))
		switch tool.Get(namePath).String() {
		case "bash":
			hasBash = true
		case "read":
			hasRead = true
		}
	}

	if hasBash && hasRead {
		return nil, false, nil
	}

	if !hasBash {
		rawTools = append(rawTools, stubs[0])
	}
	if !hasRead {
		rawTools = append(rawTools, stubs[1])
	}

	toolsJSON, err := json.Marshal(rawTools)
	if err != nil {
		return nil, false, err
	}

	return toolsJSON, true, nil
}
