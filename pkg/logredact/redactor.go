// Package logredact provides a low-dependency redactor used by both the audit
// pipeline and the runtime logger. It has no dependencies on other Omnipus
// packages, which is what allows pkg/logger and pkg/audit to share it
// without an import cycle (pkg/audit → pkg/config → pkg/logger).
//
// The redactor applies two layers of redaction (SEC-16):
//  1. Field-name match — if the key normalises (lowercase, strip '_' and '-')
//     to a known sensitive name (token, apikey, secret, bearer, ...), the
//     value is replaced with [REDACTED] regardless of content.
//  2. Value-pattern match — strings are checked against a set of regex
//     patterns for known secret shapes (sk-, key-, Bearer, ghp_, gho_, JWTs,
//     AKIA/ASIA, ya29., emails).
//
// Maps and slices are walked recursively, with key context preserved at
// each level so a `parameters.api_key` field nested inside a map is
// detected.
package logredact

import (
	"fmt"
	"regexp"
	"strings"
)

// defaultPatterns is the built-in set of value-level regexes. Callers can
// add custom patterns via NewRedactor.
var defaultPatterns = []string{
	`sk-[a-zA-Z0-9\-]{20,}`,          // OpenAI API keys (also matches sk-or-v1-...)
	`key-[a-zA-Z0-9]{20,}`,           // Generic API keys
	`Bearer\s+[a-zA-Z0-9\-._~+/]+=*`, // Bearer tokens
	`ghp_[a-zA-Z0-9]{36}`,            // GitHub personal access tokens
	`gho_[a-zA-Z0-9]{36}`,            // GitHub OAuth tokens
	`xoxb-[0-9]{10,}-[a-zA-Z0-9]+`,   // Slack bot tokens
	`xoxp-[0-9]{10,}-[a-zA-Z0-9]+`,   // Slack user tokens
	`AKIA[0-9A-Z]{16}`,               // AWS access-key IDs
	`ASIA[0-9A-Z]{16}`,               // AWS temporary access-key IDs (STS)
	`eyJ[A-Za-z0-9_=\-]+\.eyJ[A-Za-z0-9_=\-]+\.[A-Za-z0-9_\-\+/=]*`, // JWTs
	`ya29\.[0-9A-Za-z_\-]+`,                            // Google OAuth access tokens
	`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`, // Email addresses
}

// RedactedValue is the sentinel returned for any redacted field. Exported
// so callers can detect already-redacted payloads and avoid double-wrapping.
const RedactedValue = "[REDACTED]"

// sensitiveFieldNames is the set of normalized field names whose values are
// always replaced with RedactedValue regardless of value content. Normalisation
// (lowercase + strip '_'/'-') means "API_KEY", "api-key", "ApiKey", and
// "apikey" all match the single `apikey` entry.
var sensitiveFieldNames = func() map[string]struct{} {
	raw := []string{
		"password", "pwd", "passwd", "passphrase",
		"secret", "secrets",
		"token", "accesstoken", "refreshtoken", "idtoken", "csrftoken", "xsrftoken",
		"apikey",
		"authorization", "auth", "bearer",
		"privatekey", "signingkey",
		"clientsecret",
	}
	m := make(map[string]struct{}, len(raw))
	for _, k := range raw {
		m[k] = struct{}{}
	}
	return m
}()

// normalizeKey lowercases s and strips '-' and '_' so all separator
// variants collapse to the same key.
func normalizeKey(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

// Redactor replaces sensitive patterns. Safe for concurrent use after
// construction; do not mutate the patterns slice post-construction.
type Redactor struct {
	patterns []*regexp.Regexp
	enabled  bool
}

// NewRedactor creates a Redactor with default and optional custom patterns.
// Pass nil for customPatterns to use only the defaults. Returns an error if
// a custom pattern is invalid.
func NewRedactor(customPatterns []string) (*Redactor, error) {
	var patterns []*regexp.Regexp

	for _, p := range defaultPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			panic(fmt.Sprintf("BUG: invalid hardcoded redaction pattern %q: %v", p, err))
		}
		patterns = append(patterns, re)
	}

	for _, p := range customPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid redaction pattern %q: %w", p, err)
		}
		patterns = append(patterns, re)
	}

	return &Redactor{patterns: patterns, enabled: true}, nil
}

// DisabledRedactor returns a Redactor that passes all values through.
func DisabledRedactor() *Redactor {
	return &Redactor{enabled: false}
}

// Redact replaces all matching patterns in a string with RedactedValue.
func (r *Redactor) Redact(s string) string {
	if !r.enabled || len(r.patterns) == 0 {
		return s
	}
	for _, re := range r.patterns {
		s = re.ReplaceAllString(s, RedactedValue)
	}
	return s
}

// RedactField is the primary entry point. It returns RedactedValue if the
// field name matches the sensitive set (layer 1); otherwise it falls through
// to value-pattern redaction on string values (layer 2). Non-string values
// for non-sensitive keys are walked recursively.
func (r *Redactor) RedactField(key string, value any) any {
	if !r.enabled {
		return value
	}
	if _, sensitive := sensitiveFieldNames[normalizeKey(key)]; sensitive {
		if s, ok := value.(string); ok && s == RedactedValue {
			return value
		}
		return RedactedValue
	}
	return r.redactValue(value)
}

// RedactMap recursively redacts values in a map, applying field-name
// detection at each key.
func (r *Redactor) RedactMap(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = r.RedactField(k, v)
	}
	return result
}

// RedactValue walks strings, maps, and slices applying value-pattern
// redaction. Top-level maps should use RedactMap (which provides key
// context for the field-name layer).
func (r *Redactor) RedactValue(v any) any {
	switch val := v.(type) {
	case string:
		return r.Redact(val)
	case map[string]any:
		return r.RedactMap(val)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = r.RedactValue(item)
		}
		return result
	default:
		return v
	}
}

// redactValue is the unexported version retained for the same internal
// call shape used by RedactField when falling through from a non-sensitive
// field name. Equivalent to RedactValue.
func (r *Redactor) redactValue(v any) any {
	return r.RedactValue(v)
}
