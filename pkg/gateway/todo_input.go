package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// decodeTaskTodoBody checks supplied active forms before generated *string
// decoding can collapse null into omission. Normalization precedes optional
// schema validation, whose length bound must see the normalized phrase.
func decodeTaskTodoBody(w http.ResponseWriter, r *http.Request, schemaName string, dst any, validateEnabled bool) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return false
	}
	var normalized []byte
	if schemaName == "" {
		normalized, err = normalizeTaskTodoArray(raw)
	} else {
		normalized, err = rewriteTaskTodoJSONField(raw, "todos", normalizeTaskTodoArray)
	}
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(normalized))
	if schemaName == "" {
		return decodeTaskJSONBody(w, request, dst)
	}
	return decodeAndValidate(w, request, schemaName, dst, validateEnabled)
}

func normalizeTaskTodoArray(raw []byte) ([]byte, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	for i, item := range items {
		normalized, err := rewriteTaskTodoJSONField(item, "active_form", normalizeTaskTodoActiveFormJSON)
		if err != nil {
			return nil, err
		}
		items[i] = normalized
	}
	return json.Marshal(items)
}

func normalizeTaskTodoActiveFormJSON(raw []byte) ([]byte, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("active_form must be a string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(task.NormalizeTodoActiveForm(value))
}

// rewriteTaskTodoJSONField preserves every object key and all unrelated values.
// Token/RawMessage parsing observes duplicate, escaped and case-folded field
// names without decoding unrelated numbers or changing their precedence.
func rewriteTaskTodoJSONField(raw []byte, field string, normalize func([]byte) ([]byte, error)) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if opening != json.Delim('{') {
		return raw, nil // Let the generated decoder reject other non-object types.
	}
	var out []byte
	last := 0
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("object key must be a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if !strings.EqualFold(key, field) {
			continue
		}
		normalized, err := normalize(value)
		if err != nil {
			return nil, err
		}
		end := int(decoder.InputOffset())
		start := end - len(value)
		out = append(out, raw[last:start]...)
		out = append(out, normalized...)
		last = end
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return append(out, raw[last:]...), nil
}
