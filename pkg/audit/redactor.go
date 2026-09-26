package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
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

// defaultPatternLabels are human-readable labels aligned 1:1 with
// defaultPatterns (same index). They let the secrets scanner (secretscan.go)
// report WHICH class of secret it found without ever echoing the secret value.
// A compile-time length check lives in NewSecretScanner.
var defaultPatternLabels = []string{
	"OpenAI/OpenRouter API key (sk-…)",
	"generic API key (key-…)",
	bearerPatternLabel,
	"GitHub personal access token (ghp_…)",
	"GitHub OAuth token (gho_…)",
	"Slack bot token (xoxb-…)",
	"Slack user token (xoxp-…)",
	"AWS access-key ID (AKIA…)",
	"AWS temporary access-key ID (ASIA…)",
	"JSON Web Token (JWT)",
	"Google OAuth access token (ya29.…)",
	emailPatternLabel,
}

// emailPatternLabel is the defaultPatternLabels entry of the email-address
// pattern. newAuditRedactor drops the pattern carrying this label.
const emailPatternLabel = "email address"

// bearerPatternLabel is the defaultPatternLabels entry of the Bearer-token
// pattern. The audit set makes that one pattern case-insensitive.
const bearerPatternLabel = "HTTP Bearer token"

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
	rules   []redactRule
	enabled bool
}

// redactRule is one compiled pattern plus its ReplaceAllString template.
// Plain patterns replace the whole match with [REDACTED]; the audit set's
// anchored and URL-userinfo patterns keep a captured prefix ("${1}[REDACTED]").
type redactRule struct {
	re   *regexp.Regexp
	repl string
}

// NewRedactor creates a Redactor with default and optional custom patterns.
// Pass nil for customPatterns to use only default patterns.
// Returns an error if a custom pattern is invalid.
func NewRedactor(customPatterns []string) (*Redactor, error) {
	return newRedactorFrom(plainRules(defaultPatterns), customPatterns)
}

// auditKeyBoundaryPrefix makes a credential pattern match only when the
// credential starts the string or follows a separator, so
// "project-task-management" or "risk-assessment" is not read as an "sk-"
// key. A separator is a character that is not an ASCII letter or digit, or
// an encoded one whose last character is alphanumeric: a percent-encoded
// byte ("%3D", "%20"), a JSON/Go "\uXXXX" escape, or a literal "\n", "\r",
// "\t". Without those, "?q%3Dsk-…" or "\u0022sk-…" would read as a letter
// before the key and slip through. RE2 has no lookbehind, so the separator
// is captured as group 1 and written back by the replacement template.
const auditKeyBoundaryPrefix = `(^|%[0-9A-Fa-f]{2}|\\u[0-9A-Fa-f]{4}|\\[nrt]|[^A-Za-z0-9])(?:`

// auditUserinfoPattern matches the password in a URL's userinfo
// (scheme://user:PASSWORD@host, user may be empty as in redis://:pw@host).
// Group 1 keeps "scheme://user:" and the trailing "@" is re-emitted, so only
// the password becomes [REDACTED]. It needs "://" and a ":" before the "@",
// so a plain email address or a Message-ID never matches. Known limits: a
// raw "/" or "@" inside the password, and scheme-less DSNs such as
// user:pw@tcp(host), are not matched. This replaces the incidental cover the
// email pattern used to give such URLs, which the audit set drops.
const auditUserinfoPattern = `([A-Za-z][A-Za-z0-9+.\-]*://[^\s:/@]*:)[^\s@/]+@`

// newAuditRedactor creates the Redactor the audit Logger uses (issue #914):
// see auditCredentialPatterns for the set. customPatterns are appended as
// plain whole-match patterns.
func newAuditRedactor(customPatterns []string) (*Redactor, error) {
	return newRedactorFrom(auditCredentialRules(), customPatterns)
}

// auditCredentialPatterns returns the audit logger's pattern set: every
// defaultPatterns credential pattern, anchored with auditKeyBoundaryPrefix,
// except the email-address pattern, plus auditUserinfoPattern. The email
// pattern is dropped because the audit log must record mail recipients and
// Message-IDs in full (founder ruling MC-19). defaultPatterns itself stays
// untouched: secretscan.go depends on its order and on defaultPatternLabels,
// and NewRedactor's callers keep the full, unanchored set. The email pattern
// is located by its label so a reorder of defaultPatterns cannot drop the
// wrong entry. Panics on a hardcoded-table defect (labels out of step with
// patterns, or not exactly one email label) — the same class of bug as an
// invalid hardcoded pattern in newRedactorFrom.
func auditCredentialPatterns() []string {
	if len(defaultPatterns) != len(defaultPatternLabels) {
		panic(fmt.Sprintf("BUG: defaultPatterns (%d) and defaultPatternLabels (%d) length mismatch",
			len(defaultPatterns), len(defaultPatternLabels)))
	}
	patterns := make([]string, 0, len(defaultPatterns))
	dropped := 0
	for i, p := range defaultPatterns {
		if defaultPatternLabels[i] == emailPatternLabel {
			dropped++
			continue
		}
		if defaultPatternLabels[i] == bearerPatternLabel {
			// "authorization: bearer …" is common; match any case here
			// only — the shared default stays case-sensitive.
			p = `(?i)` + p
		}
		patterns = append(patterns, auditKeyBoundaryPrefix+p+`)`)
	}
	if dropped != 1 {
		panic(fmt.Sprintf("BUG: expected exactly one %q pattern in defaultPatterns, found %d", emailPatternLabel, dropped))
	}
	return append(patterns, auditUserinfoPattern)
}

// auditCredentialRules pairs auditCredentialPatterns with their replacement
// template. Every audit pattern captures a kept prefix as group 1; the
// userinfo pattern also consumed the "@" that ends the password, so its
// template writes it back.
func auditCredentialRules() []ruleSource {
	patterns := auditCredentialPatterns()
	out := make([]ruleSource, len(patterns))
	for i, p := range patterns {
		repl := "${1}" + redactedValue
		if p == auditUserinfoPattern {
			repl += "@"
		}
		out[i] = ruleSource{pattern: p, repl: repl}
	}
	return out
}

// ruleSource is an uncompiled redactRule.
type ruleSource struct {
	pattern string
	repl    string
}

// plainRules wraps patterns as whole-match [REDACTED] rules.
func plainRules(patterns []string) []ruleSource {
	out := make([]ruleSource, len(patterns))
	for i, p := range patterns {
		out[i] = ruleSource{pattern: p, repl: redactedValue}
	}
	return out
}

// newRedactorFrom compiles base (hardcoded, so a compile failure is a bug and
// panics) followed by customPatterns (operator input, so a compile failure is
// returned as an error).
func newRedactorFrom(base []ruleSource, customPatterns []string) (*Redactor, error) {
	rules := make([]redactRule, 0, len(base)+len(customPatterns))

	for _, src := range base {
		re, err := regexp.Compile(src.pattern)
		if err != nil {
			panic(fmt.Sprintf("BUG: invalid hardcoded redaction pattern %q: %v", src.pattern, err))
		}
		rules = append(rules, redactRule{re: re, repl: src.repl})
	}

	for _, p := range customPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid redaction pattern %q: %w", p, err)
		}
		rules = append(rules, redactRule{re: re, repl: redactedValue})
	}

	return &Redactor{rules: rules, enabled: true}, nil
}

// processAuditRedactor is the audit credential set with no custom patterns,
// built once for RedactCredentials.
var processAuditRedactor = func() *Redactor {
	r, err := newAuditRedactor(nil)
	if err != nil {
		panic(fmt.Sprintf("BUG: audit redactor with no custom patterns failed: %v", err))
	}
	return r
}()

// RedactCredentials applies the audit logger's credential patterns (no
// email redaction, anchored key prefixes, URL userinfo passwords) to s. Use
// it wherever a command or other agent-supplied string that is also
// audited is written somewhere else — for example the gateway log line on
// an audit-failure path — so that copy is no less redacted than the audit
// entry (issue #914).
func RedactCredentials(s string) string {
	return processAuditRedactor.Redact(s)
}

// DisabledRedactor returns a Redactor that passes through all values unchanged.
func DisabledRedactor() *Redactor {
	return &Redactor{enabled: false}
}

// Redact replaces all matching patterns in a string with [REDACTED].
func (r *Redactor) Redact(s string) string {
	if r == nil || !r.enabled || len(r.rules) == 0 {
		return s
	}
	for _, rule := range r.rules {
		s = rule.re.ReplaceAllString(s, rule.repl)
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
		// Already redacted? Leave it to avoid double-wrapping. The
		// security-setting-change sentinel counts too, so that record keeps
		// its own "***redacted***" marker when the logger also redacts.
		if s, ok := value.(string); ok && (s == redactedValue || s == redactedSentinel) {
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

// redactValue redacts one value. Strings go through the patterns; the
// container shapes audit callers actually build ([]any, []string,
// map[string]any, map[string]string, []map[string]any) are walked with the
// field-name layer applied to map keys; numbers, booleans and nil pass
// through. Any other type (a struct, a pointer, a named slice) is never
// passed through unexamined: it is converted through its JSON form into
// generic maps and slices and walked like the rest, which writes the same
// JSON the audit line would have carried. A value JSON cannot encode is
// replaced by a marker naming its type.
func (r *Redactor) redactValue(v any) any {
	switch val := v.(type) {
	case nil, bool, json.Number,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return v
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
	case []string:
		result := make([]string, len(val))
		for i, item := range val {
			result[i] = r.Redact(item)
		}
		return result
	case map[string]string:
		result := make(map[string]string, len(val))
		for k, item := range val {
			if _, sensitive := sensitiveFieldNames[normalizeKey(k)]; sensitive {
				result[k] = redactedValue
				continue
			}
			result[k] = r.Redact(item)
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, len(val))
		for i, item := range val {
			result[i] = r.redactMap(item)
		}
		return result
	default:
		return r.redactViaJSON(v)
	}
}

// redactViaJSON handles a type redactValue has no case for: named string
// kinds are redacted as strings, other numeric/bool kinds pass through, and
// everything else is round-tripped through JSON into generic values and
// walked. A marshal/unmarshal failure yields a type-naming marker rather
// than the raw value.
func (r *Redactor) redactViaJSON(v any) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return r.Redact(rv.String())
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return v
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%s (unserialisable %T)", redactedValue, v)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return fmt.Sprintf("%s (unserialisable %T)", redactedValue, v)
	}
	return r.redactValue(generic)
}
