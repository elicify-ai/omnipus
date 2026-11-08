package logger

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
)

// sensitiveValueReplacer is the process-wide replacer for the credential
// plaintexts this process has registered. pkg/config publishes it from
// Config.RegisterSensitiveValues — boot step 6 of the credential boot contract
// (ADR-004) and every re-registration after it (config reload, REST config
// writes, provider sign-in) — so it always carries the most recently registered
// complete set. It lives in this package because pkg/logger is the lowest
// package both the registrar (pkg/config) and every consumer import: one value
// set, one replacer, applied at two choke points — this package's own log
// output (logMessage) and ScrubSensitiveValues, which the agent applies to the
// Verbose-chat error detail. nil means no credential is registered.
//
// Threat addressed: a provider or tool echoes a configured credential inside
// text the process then logs or forwards as diagnostic detail (for example an
// error body "Incorrect API key provided: <key>"). gateway.log is a plain file
// on disk; this keeps a registered credential out of it. It cannot recognise a
// value that was never registered, or one a provider masked or cut short — so a
// surface a person or a model reads must never carry raw provider text in the
// first place; this is the net under the operator-only diagnostics.
var sensitiveValueReplacer atomic.Pointer[strings.Replacer]

// SetSensitiveValueReplacer installs r as the process-wide credential replacer
// (nil removes it) and returns a func that restores the previous one.
// Production installs it only through config.Config.RegisterSensitiveValues;
// tests use the restore func to leave the process as they found it.
func SetSensitiveValueReplacer(r *strings.Replacer) (restore func()) {
	prev := sensitiveValueReplacer.Swap(r)
	return func() { sensitiveValueReplacer.Store(prev) }
}

// ScrubSensitiveValues returns s with every registered credential plaintext
// replaced, at any length of s. With nothing registered it returns s unchanged.
func ScrubSensitiveValues(s string) string {
	r := sensitiveValueReplacer.Load()
	if r == nil || s == "" {
		return s
	}
	return r.Replace(s)
}

// scrubFields returns a copy of fields with every registered credential
// replaced in its keys and values. The caller's map is never modified: log
// call sites build a map literal per call, but nothing stops one from reusing
// a map it still reads afterwards.
func scrubFields(r *strings.Replacer, fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return fields
	}
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		out[r.Replace(k)] = scrubFieldValue(r, v)
	}
	return out
}

// maxScrubDecodeDepth bounds how many quoting or escaping layers the scrubber
// reads through. Composed errors nest a few %q layers; eight leaves margin.
const maxScrubDecodeDepth = 8

// scrubFailClosedText is logged in place of a field whose registered
// credential could not be removed any other way. It is the mask
// pkg/config/security.go::sensitiveReplacementPairs uses.
const scrubFailClosedText = "[FILTERED]"

// scrubString replaces registered credentials in one string field (#1666).
// A registered credential must not reach a log sink in plain form or in any
// form a reader can decode back to it: Go quoting (pkg/gateway's safeLogValue,
// %q fragments inside composed errors, nested quoting) and Go escape
// sequences (\" \\ \n \xHH \uNNNN ...). Doing it here, at the one scrub choke
// point, covers every call site and works with the plaintext-only replacer
// pkg/config publishes.
//
//   - No credential in the field, decoded or not: the field is returned
//     byte-for-byte (an operator compares these lines with real files).
//   - Only plain occurrences: the plain replacement, as before.
//   - Otherwise the field is decoded layer by layer, masked at every layer,
//     and logged quoted once, so the record stays control-free. If a decodable
//     credential still remained, the whole field is replaced (fail closed).
func scrubString(r *strings.Replacer, s string) string {
	masked := r.Replace(s)
	// Without a backslash or a quote nothing in the field can be decoded.
	if !strings.ContainsAny(masked, `\"`) || !hasDecodableCredential(r, masked) {
		return masked
	}
	current := masked
	for depth := 0; depth < maxScrubDecodeDepth; depth++ {
		next, ok := decodeLogLayer(current)
		if !ok {
			break
		}
		current = r.Replace(next)
	}
	out := strconv.Quote(current)
	if hasDecodableCredential(r, out) {
		return scrubFailClosedText
	}
	return out
}

// hasDecodableCredential reports whether s, or any string reachable from it by
// unquoting a whole quoted string or decoding Go escape sequences (up to
// maxScrubDecodeDepth layers, both decodings tried at every layer), still
// contains a registered credential.
func hasDecodableCredential(r *strings.Replacer, s string) bool {
	seen := map[string]bool{s: true}
	frontier := []string{s}
	for depth := 0; depth <= maxScrubDecodeDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, v := range frontier {
			if r.Replace(v) != v {
				return true
			}
			if depth == maxScrubDecodeDepth {
				continue
			}
			if u, ok := unquoteWhole(v); ok && !seen[u] {
				seen[u] = true
				next = append(next, u)
			}
			if u, ok := unescapeGo(v); ok && !seen[u] {
				seen[u] = true
				next = append(next, u)
			}
		}
		frontier = next
	}
	return false
}

// decodeLogLayer removes one layer: the quotes of a whole quoted string, or
// else the Go escape sequences in the text. ok is false when nothing changed.
func decodeLogLayer(s string) (string, bool) {
	if u, ok := unquoteWhole(s); ok {
		return u, true
	}
	return unescapeGo(s)
}

// unquoteWhole unquotes s when the whole of s is one Go double-quoted string.
func unquoteWhole(s string) (string, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s, false
	}
	u, err := strconv.Unquote(s)
	if err != nil {
		return s, false
	}
	return u, true
}

// unescapeGo decodes every valid Go escape sequence in s, wherever it occurs
// (inside a quoted fragment of a longer text too). Invalid sequences are kept
// as written. ok is false when s had no valid escape.
func unescapeGo(s string) (string, bool) {
	if !strings.Contains(s, `\`) {
		return s, false
	}
	var b strings.Builder
	changed := false
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			value, multibyte, tail, err := strconv.UnquoteChar(s[i:], '"')
			if err != nil {
				// \' is valid only with a single-quote delimiter.
				value, multibyte, tail, err = strconv.UnquoteChar(s[i:], '\'')
			}
			if err == nil {
				if multibyte {
					b.WriteRune(value)
				} else {
					b.WriteByte(byte(value))
				}
				i = len(s) - len(tail)
				changed = true
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), changed
}

// scrubFieldValue replaces registered credentials inside one field value while
// keeping the shape appendFields writes for it: strings and errors are written
// as strings, numbers and booleans pass through, the containers log call sites
// actually pass are walked, and anything else is scrubbed in the JSON form
// zerolog would have written for it — kept as JSON when the scrubbed text still
// parses, otherwise written as a string, so the scrub is never dropped.
func scrubFieldValue(r *strings.Replacer, v any) any {
	switch val := v.(type) {
	case nil:
		return nil
	case string:
		return scrubString(r, val)
	case error:
		return scrubString(r, val.Error())
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return val
	case []byte:
		if s := string(val); r.Replace(s) != s {
			return r.Replace(s)
		}
		return val
	case []string:
		out := make([]string, len(val))
		for i, s := range val {
			out[i] = scrubString(r, s)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, x := range val {
			out[i] = scrubFieldValue(r, x)
		}
		return out
	case map[string]any:
		return scrubFields(r, val)
	case map[string]string:
		out := make(map[string]string, len(val))
		for k, s := range val {
			out[r.Replace(k)] = scrubString(r, s)
		}
		return out
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// A credential containing <, > or & must still match its encoded form.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return v
	}
	encoded := strings.TrimRight(buf.String(), "\n")
	scrubbed := r.Replace(encoded)
	if scrubbed == encoded {
		return v
	}
	if json.Valid([]byte(scrubbed)) {
		return json.RawMessage(scrubbed)
	}
	return scrubbed
}
