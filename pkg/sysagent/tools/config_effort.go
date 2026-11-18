package systools

// config_effort.go — the set_config effort checks and the exact-path
// non-secret classification of the recap token-count keys (effort-levels
// spec FR-060, FR-076, S-83, S-92, S-94).

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	effortNullMessage   = `reasoning_effort must be a string; send "" to clear it`
	effortLengthMessage = "reasoning_effort must be at most 32 characters"
	effortMaxChars      = 32
)

// nonSecretConfigPaths are config paths whose names contain a credential
// fragment ("token") but hold plain recap limits (FR-076). Matched exactly,
// case-insensitively — never by prefix.
var nonSecretConfigPaths = []string{
	"agents.defaults.recap_max_output_tokens",
	"agents.defaults.recap_input_tokens",
	"agents.defaults.recap_chars_per_token",
}

func isNonSecretConfigPath(path string) bool {
	for _, p := range nonSecretConfigPaths {
		if strings.EqualFold(path, p) {
			return true
		}
	}
	return false
}

// isSensitiveConfigPath is the one read/write/redaction decision of the
// config tools: false for a listed non-secret path, otherwise the credential
// name check on the path.
func isSensitiveConfigPath(path string) bool {
	if isNonSecretConfigPath(path) {
		return false
	}
	return isSensitiveConfigName(path)
}

// effortCompositeKeys are the destinations whose value carries effort members
// inside an object or array.
var effortCompositeKeys = []string{"recap_fallback_models", "fallback_models", "default_model"}

func isEffortMemberName(name string) bool {
	lower := strings.ToLower(name)
	return lower == "reasoning_effort" || lower == "recap_reasoning_effort"
}

func lastConfigSegment(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 {
		return key[i+1:]
	}
	return key
}

// checkConfigEffortValues refuses a set_config write whose effort values are
// invalid: the leaf value when the key is an effort key, and every effort
// member nested at any depth in an object, array or section value — null,
// a non-string, or more than 32 characters. A string aimed at an
// effort-bearing composite destination is refused as a type error and never
// decoded. It returns the refusal message, or "" when the write may go on.
func checkConfigEffortValues(key string, value any) string {
	last := lastConfigSegment(key)
	if isEffortMemberName(last) {
		return checkEffortValue(value)
	}
	if _, isString := value.(string); isString {
		for _, k := range effortCompositeKeys {
			if strings.EqualFold(last, k) {
				return fmt.Sprintf("%s must be an actual JSON object or array, not a string containing JSON", last)
			}
		}
		return ""
	}
	return checkNestedEffortValues(value)
}

func checkEffortValue(v any) string {
	s, ok := v.(string)
	if !ok {
		return effortNullMessage
	}
	if utf8.RuneCountInString(s) > effortMaxChars {
		return effortLengthMessage
	}
	return ""
}

func checkNestedEffortValues(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for name, sub := range typed {
			if isEffortMemberName(name) {
				if msg := checkEffortValue(sub); msg != "" {
					return msg
				}
				continue
			}
			if msg := checkNestedEffortValues(sub); msg != "" {
				return msg
			}
		}
	case []any:
		for _, sub := range typed {
			if msg := checkNestedEffortValues(sub); msg != "" {
				return msg
			}
		}
	}
	return ""
}
