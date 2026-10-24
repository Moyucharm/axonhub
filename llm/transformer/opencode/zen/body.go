package zen

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
)

// normalizeJSONBody removes HTML escaping from every JSON string without
// interpreting string contents as JSON or converting numbers through float64.
// In particular, serialized tool arguments and literal backslash-u sequences
// remain strings with exactly the same contents.
func normalizeJSONBody(body []byte) ([]byte, error) {
	value, err := decodeJSONBody(body)
	if err != nil {
		return nil, err
	}
	return marshalJSONWithoutHTMLEscaping(value)
}

func decodeJSONBody(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("request body contains multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func marshalJSONWithoutHTMLEscaping(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes()[:buffer.Len()-1], nil
}

// marshalChatBody keeps the CLI's leading envelope fields in wire order. All
// other top-level keys, and all nested object keys, are deterministic too.
func marshalChatBody(body []byte) ([]byte, error) {
	value, err := decodeJSONBody(body)
	if err != nil {
		return nil, err
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("request body must be a JSON object")
	}

	keys := make([]string, 0, len(payload))
	for _, key := range []string{"model", "messages", "tools", "stream", "stream_options", "max_completion_tokens"} {
		if _, exists := payload[key]; exists {
			keys = append(keys, key)
		}
	}
	leading := len(keys)
	for key := range payload {
		switch key {
		case "model", "messages", "tools", "stream", "stream_options", "max_completion_tokens":
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys[leading:])

	var buffer bytes.Buffer
	buffer.Grow(len(body))
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	buffer.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buffer.WriteByte(',')
		}
		if err := encoder.Encode(key); err != nil {
			return nil, err
		}
		buffer.Truncate(buffer.Len() - 1)
		buffer.WriteByte(':')
		if err := encoder.Encode(payload[key]); err != nil {
			return nil, err
		}
		buffer.Truncate(buffer.Len() - 1)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}
