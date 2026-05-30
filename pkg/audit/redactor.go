package audit

import (
	"fmt"
	"regexp"
	"strings"
)

// Default redaction patterns for SEC-16.
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
	`eyJ[A-Za-z0-9_=\-]+\.eyJ[A-Za-z0-9_=\-]+\.[A-Za-z0-9_\-\+/=]*`, // JWTs (header.payload.signature)
	`ya29\.[0-9A-Za-z_\-]+`,                            // Google OAuth access tokens
	`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`, // Email addresses
}

const redactedValue = "[REDACTED]"

// sensitiveFieldNames is the set of normalized field names whose values are always
// replaced with [REDACTED] regardless of value content (SEC-16 field-name layer).
//
// Normalization: lowercase the key, then strip all '_' and '-' characters.
// This means "API_KEY", "api-key", "ApiKey", and "apikey" all collapse to "apikey"
// and match the same entry. Build the set once at package init for O(1) lookup.
var sensitiveFieldNames = func() map[string]struct{} {
	raw := []string{
		// Passwords
		"password", "pwd", "passwd", "passphrase",
		// Secrets
		"secret", "secrets",
		// Tokens
		"token", "accesstoken", "refreshtoken", "idtoken", "csrftoken", "xsrftoken",
		// API keys (normalized — original forms like `api_key`, `api-key`, `API_KEY` all collapse here after normalizeKey strips `_`/`-` and lowercases)
		"apikey",
		// Authorization
		"authorization", "auth", "bearer",
		// Private/signing keys
		"privatekey", "signingkey",
		// Client secrets
		"clientsecret",
	}
	m := make(map[string]struct{}, len(raw))
	for _, k := range raw {
		m[k] = struct{}{}
	}
	return m
}()

// normalizeKey lowercases s and strips all '-' and '_' characters so that
// "API_KEY", "api-key", "ApiKey", and "apikey" all map to the same normalized form.
func normalizeKey(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

// Redactor replaces sensitive patterns in audit log entries (SEC-16).
type Redactor struct {
	patterns []*regexp.Regexp
	enabled  bool
}

// NewRedactor creates a Redactor with default and optional custom patterns.
// Pass nil for customPatterns to use only default patterns.
// Returns an error if a custom pattern is invalid.
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

// DisabledRedactor returns a Redactor that passes through all values unchanged.
func DisabledRedactor() *Redactor {
	return &Redactor{enabled: false}
}

// Redact replaces all matching patterns in a string with [REDACTED].
func (r *Redactor) Redact(s string) string {
	if !r.enabled || len(r.patterns) == 0 {
		return s
	}
	for _, re := range r.patterns {
		s = re.ReplaceAllString(s, redactedValue)
	}
	return s
}

// redactField returns [REDACTED] if the field name is in the sensitive set (first
// layer), otherwise falls through to value-pattern redaction on string values
// (second layer). Non-string values for non-sensitive keys are walked recursively.
//
// Layering order:
//  1. Field-name match (case-insensitive, separator-stripped) → always [REDACTED]
//  2. Value-pattern match on string → [REDACTED] if a known secret pattern matches
//  3. Structural walk (map, slice) → recurse with key context from the parent map
func (r *Redactor) redactField(key string, value any) any {
	if !r.enabled {
		return value
	}
	if _, sensitive := sensitiveFieldNames[normalizeKey(key)]; sensitive {
		// Already redacted? Leave it to avoid double-wrapping.
		if s, ok := value.(string); ok && s == redactedValue {
			return value
		}
		return redactedValue
	}
	// Not a sensitive field name — fall through to value-level redaction.
	return r.redactValue(value)
}

// redactMap recursively redacts values in a map, applying field-name detection
// at each key before falling back to value-pattern redaction.
func (r *Redactor) redactMap(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = r.redactField(k, v)
	}
	return result
}

func (r *Redactor) redactValue(v any) any {
	switch val := v.(type) {
	case string:
		return r.Redact(val)
	case map[string]any:
		return r.redactMap(val)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = r.redactValue(item)
		}
		return result
	default:
		return v
	}
}
