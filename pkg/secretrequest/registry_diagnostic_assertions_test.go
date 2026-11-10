package secretrequest_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Accept semantic stage/category fields, not an incidental full message or a
// proposed private production error type. Category must be a structured value:
// mentioning an arbitrary cause's Error() text is not classification.
func assertRegistryDiagnostic(t *testing.T, entries []map[string]any, requestID, sessionID, category string, stageWords ...string) {
	t.Helper()
	var contextual []map[string]any
	for _, entry := range entries {
		flat := registryStringFields(entry)
		if requestID != "" && !registryHasField(flat, requestID, "request_id", "request") {
			continue
		}
		if sessionID != "" && !registryHasField(flat, sessionID, "session_id", "session") {
			continue
		}
		stage := registryFirstField(flat, "stage", "operation_stage", "operation")
		stageMatches := stage != ""
		for _, word := range stageWords {
			stageMatches = stageMatches && strings.Contains(strings.ToLower(stage), word)
		}
		if !stageMatches {
			continue
		}
		contextual = append(contextual, entry)
	}
	if !assert.NotEmpty(t, contextual, "SF-05/Q-PUBLISH: actual fault needs a live diagnostic with request/session and %v stage, not a silent generic error", stageWords) {
		return
	}
	matched := false
	for _, entry := range contextual {
		flat := registryStringFields(entry)
		cause := registryFirstField(flat, "cause_category", "error_category", "reason_code", "category", "cause", "reason", "code")
		level := strings.ToLower(registryFirstField(flat, "level", "severity"))
		if cause == category && level == "error" {
			matched = true
			text, err := json.Marshal(entry)
			assert.NoError(t, err)
			assertRegistryRepairGuidance(t, string(text))
		}
	}
	assert.True(t, matched, "fault must carry Error level and structured safe category %q; contextual rows: %v", category, contextual)
}

func assertRegistryRepairGuidance(t interface {
	Helper()
	Errorf(format string, args ...any)
}, text string) {
	t.Helper()
	// Require both a repair/retry action AND its storage-related subject. A
	// bare 'failed' or 'request is pending' does not tell an operator what to do.
	assert.Regexp(t, regexp.MustCompile(`(?i)\b(repair|fix|restore|unlock|free|check|correct|grant|retry|reopen|restart|remove|provide)\b`), text, "safe error must give an actionable next step")
	assert.Regexp(t, regexp.MustCompile(`(?i)\b(permission[s]?|access|director[yies]+|storage|store|metadata|state|file[s]?|disk|key|space|cleanup|receipt[s]?|saved)\b`), text, "repair guidance must identify what needs attention")
}

func registryStringFields(in map[string]any) map[string]string {
	out := make(map[string]string)
	var walk func(map[string]any, string)
	walk = func(values map[string]any, prefix string) {
		for key, value := range values {
			switch typed := value.(type) {
			case string:
				out[prefix+key] = typed
			case map[string]any:
				walk(typed, prefix+key+".")
			}
		}
	}
	walk(in, "")
	return out
}

func registryFirstField(fields map[string]string, names ...string) string {
	for _, name := range names {
		for key, value := range fields {
			if key == name || strings.HasSuffix(key, "."+name) {
				return value
			}
		}
	}
	return ""
}

func registryHasField(fields map[string]string, value string, names ...string) bool {
	for _, name := range names {
		for key, actual := range fields {
			if (key == name || strings.HasSuffix(key, "."+name)) && actual == value {
				return true
			}
		}
	}
	return false
}
