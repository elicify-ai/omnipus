package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// rawJSONFields retains every occurrence, including identical duplicate keys.
// It is validation metadata, not a wire type or a replacement request decoder.
type rawJSONFields map[string][]json.RawMessage

func decodeRawJSONFields(raw []byte) (rawJSONFields, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON body")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if opening != json.Delim('{') {
		return nil, fmt.Errorf("request body must be a JSON object")
	}
	fields := make(rawJSONFields)
	for decoder.More() {
		token, tokenErr := decoder.Token()
		if tokenErr != nil {
			return nil, tokenErr
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("request body key must be a string")
		}
		var value json.RawMessage
		if decodeErr := decoder.Decode(&value); decodeErr != nil {
			return nil, decodeErr
		}
		fields[key] = append(fields[key], value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return fields, nil
}

// EqualFold uses the same Unicode simple folding as encoding/json's field-name
// lookup. Token has already unescaped keys. Do not trim keys: a trailing space
// is not a decoder-recognised field. Retaining occurrences prevents duplicate
// keys (of either the same or different case) from overwriting an explicit null.
func (fields rawJSONFields) hasNull(name string) bool {
	for key, values := range fields {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, value := range values {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return true
			}
		}
	}
	return false
}

func (fields rawJSONFields) has(name string) bool {
	for key := range fields {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func readAgentUpdateFields(w http.ResponseWriter, raw []byte) (rawJSONFields, bool) {
	fields, err := decodeRawJSONFields(raw)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return nil, false
	}
	return fields, true
}
