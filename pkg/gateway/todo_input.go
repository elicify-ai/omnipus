package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

var errTodoActiveFormType = errors.New("active_form must be a string or omitted")

// todoInputValidationError is controlled validation metadata, not a wire type.
// not-wire-format: only its safe message is placed in the generated error envelope.
type todoInputValidationError struct {
	index int
}

func (e *todoInputValidationError) Error() string {
	return fmt.Sprintf("todos[%d].active_form must be a string or omitted", e.index)
}

// decodeTaskTodoBody checks supplied active forms before generated *string
// decoding can collapse null into omission. Limit incoming bytes once, then
// validate/decode all normalized bytes even when JSON escaping expands them.
func decodeTaskTodoBody(w http.ResponseWriter, r *http.Request, schemaName string, dst any, validateEnabled bool) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return false
	}
	if !json.Valid(raw) {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	validateSchema := schemaName != "" && validateEnabled
	var normalized []byte
	if schemaName == "" {
		normalized, err = normalizeTaskTodoArray(raw)
	} else {
		normalized, err = rewriteTaskTodoJSONField(raw, "todos", normalizeTaskTodoArray)
	}
	if err != nil {
		var inputErr *todoInputValidationError
		if errors.As(err, &inputErr) {
			// Preserve the detailed schema diagnostic when it rejects the raw
			// input. Even if it accepts a duplicate-key echo, retain our refusal.
			if validateSchema && !decodeAndValidateBytes(w, raw, schemaName, dst, true, inputErr.Error()) {
				return false
			}
			jsonErr(w, http.StatusBadRequest, inputErr.Error())
		} else {
			jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}
	return decodeAndValidateBytes(w, normalized, schemaName, dst, validateSchema, "")
}

func normalizeTaskTodoArray(raw []byte) ([]byte, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	for i, item := range items {
		normalized, err := rewriteTaskTodoJSONField(item, "active_form", normalizeTaskTodoActiveFormJSON)
		if err != nil {
			if errors.Is(err, errTodoActiveFormType) {
				return nil, &todoInputValidationError{index: i}
			}
			return nil, err
		}
		items[i] = normalized
	}
	return json.Marshal(items)
}

func normalizeTaskTodoActiveFormJSON(raw []byte) ([]byte, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errTodoActiveFormType
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return nil, errTodoActiveFormType
		}
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
		if decodeErr := decoder.Decode(&value); decodeErr != nil {
			return nil, decodeErr
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
