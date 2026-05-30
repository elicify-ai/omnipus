// Package audit — args_hash and args_preview helpers (FR-080).
//
// `args_hash` is sha256(canonicaljson(args)) rendered as 64-char lowercase
// hex. canonicaljson here is RFC 8785-compatible: sorted object keys, no
// whitespace, UTF-8, JSON.stringify-style number serialization. We do NOT
// implement the full RFC 8785 number-canonicalisation algorithm (ECMA-262
// Number.prototype.toString); we implement the subset needed for tool
// arguments, which originate from JSON unmarshal and therefore traverse a
// finite, well-typed shape. Specifically:
//
//   - map[string]any  → JSON object with sorted keys
//   - []any           → JSON array, element order preserved
//   - string          → JSON string, RFC 8259 escapes
//   - bool            → "true" / "false"
//   - nil             → "null"
//   - json.Number     → emitted verbatim (caller's canonical form preserved)
//   - float64         → strconv.FormatFloat(v, 'g', -1, 64), with
//     fast-path integer-valued floats rendered without a
//     trailing ".0" — this matches the RFC 8785 case for
//     integers that fit in float64.
//   - int*, uint*     → strconv.FormatInt / FormatUint
//
// Determinism guarantee: for inputs that survive a json.Marshal+Unmarshal
// round-trip, ArgsHash returns byte-equal output across Go map-iteration
// noise (this is the contract `TestAuditArgsHash_Deterministic` asserts
// over 100 runs). The hash is an identity/correlation field, not a
// confidentiality protection — the spec is explicit about this in FR-080.
//
// `args_preview` is a redacted, length-capped projection used to give
// operators a sense of what an approval was for without leaking secrets.
// The algorithm is documented in the `ArgsPreview` doc-comment.

package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ArgsHash returns the lowercase hex SHA-256 of the RFC 8785-style canonical
// JSON encoding of `args`. Returns ("", err) on a non-encodable input
// (e.g. unsupported types like channels). For nil input the hash is the
// hash of the literal `null` (deterministic, callers can use it to
// distinguish "no args" from "missing approval").
//
// FR-080: deterministic across map-iteration noise, 64 lowercase hex chars.
func ArgsHash(args any) (string, error) {
	var buf bytes.Buffer
	if err := canonicalEncode(&buf, args); err != nil {
		return "", fmt.Errorf("audit: args_hash canonical encode failed: %w", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

// canonicalEncode writes RFC 8785-style canonical JSON for v into buf.
// See the file-level comment for the supported type matrix.
func canonicalEncode(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case string:
		return writeJSONString(buf, x)
	case json.Number:
		// Caller's canonical numeric form is preserved verbatim.
		buf.WriteString(string(x))
		return nil
	case float32:
		return writeFloat(buf, float64(x))
	case float64:
		return writeFloat(buf, x)
	case int:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
		return nil
	case int8:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
		return nil
	case int16:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
		return nil
	case int32:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
		return nil
	case int64:
		buf.WriteString(strconv.FormatInt(x, 10))
		return nil
	case uint:
		buf.WriteString(strconv.FormatUint(uint64(x), 10))
		return nil
	case uint8:
		buf.WriteString(strconv.FormatUint(uint64(x), 10))
		return nil
	case uint16:
		buf.WriteString(strconv.FormatUint(uint64(x), 10))
		return nil
	case uint32:
		buf.WriteString(strconv.FormatUint(uint64(x), 10))
		return nil
	case uint64:
		buf.WriteString(strconv.FormatUint(x, 10))
		return nil
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := canonicalEncode(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case map[string]any:
		return writeMap(buf, x)
	}

	// Fallback: try a JSON round-trip to coerce the value into one of the
	// shapes above. This handles structs and named types (e.g. type Foo string)
	// without exploding the type switch.
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("audit: args_hash unsupported type %T: %w", v, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var coerced any
	if err := dec.Decode(&coerced); err != nil {
		return fmt.Errorf("audit: args_hash decode coerced %T: %w", v, err)
	}
	return canonicalEncode(buf, coerced)
}

// writeMap emits a map with keys sorted in lexicographic byte order
// (RFC 8785 § 3.2.3 — the same order used by `sort.Strings`).
func writeMap(buf *bytes.Buffer, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeJSONString(buf, k); err != nil {
			return err
		}
		buf.WriteByte(':')
		if err := canonicalEncode(buf, m[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

// writeFloat emits a float using Go's shortest-round-trip 'g' format.
// Integer-valued floats are emitted without a fractional part to match
// RFC 8785's integer canonicalisation case.
func writeFloat(buf *bytes.Buffer, v float64) error {
	if v != v { // NaN
		return fmt.Errorf("audit: args_hash refuses NaN (RFC 8785 incompatible)")
	}
	if v > 1.7976931348623157e308 || v < -1.7976931348623157e308 {
		return fmt.Errorf("audit: args_hash refuses ±Inf (RFC 8785 incompatible)")
	}
	if v == float64(int64(v)) && v >= -9.007199254740992e15 && v <= 9.007199254740992e15 {
		// Safe-integer range — emit as integer.
		buf.WriteString(strconv.FormatInt(int64(v), 10))
		return nil
	}
	buf.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	return nil
}

// writeJSONString writes s as an RFC 8259 JSON string, escaping the
// minimum set of characters mandated by the spec. RFC 8785 forbids
// the long \u00XX escape when a one-letter form exists.
func writeJSONString(buf *bytes.Buffer, s string) error {
	buf.WriteByte('"')
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"':
			buf.WriteString(`\"`)
			i++
		case c == '\\':
			buf.WriteString(`\\`)
			i++
		case c == '\b':
			buf.WriteString(`\b`)
			i++
		case c == '\f':
			buf.WriteString(`\f`)
			i++
		case c == '\n':
			buf.WriteString(`\n`)
			i++
		case c == '\r':
			buf.WriteString(`\r`)
			i++
		case c == '\t':
			buf.WriteString(`\t`)
			i++
		case c < 0x20:
			fmt.Fprintf(buf, `\u%04x`, c)
			i++
		case c < utf8.RuneSelf:
			buf.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				return fmt.Errorf("audit: args_hash invalid UTF-8 at offset %d", i)
			}
			buf.WriteString(s[i : i+size])
			i += size
		}
	}
	buf.WriteByte('"')
	return nil
}

// ---------------------------------------------------------------------------
// args_preview redaction (FR-080).
// ---------------------------------------------------------------------------

// secretKeyPattern matches top-level keys whose values must be redacted in
// args_preview. Case-insensitive substring match — `api_key`, `apiKey`,
// `OPENAI_API_KEY`, `password_hash`, `bearer_token`, `client_secret` all hit.
var secretKeyPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|password|passwd|secret|credential|authorization)`)

// bearerTokenValuePattern matches strings that look like bearer-token-shaped
// secrets even when the key is innocent (`headers.authorization = "Bearer ..."`).
var bearerTokenValuePattern = regexp.MustCompile(`(?i)^bearer\s+\S{8,}$|^[a-z]{2,4}-[a-z0-9_-]{20,}$`)

// previewMaxLen caps each top-level value in the preview output to 32
// characters (FR-080). Strings longer than this are truncated with `…`.
const previewMaxLen = 32

// ArgsPreview returns a redacted, length-capped projection of `args` suitable
// for inclusion in audit records (FR-080).
//
// Algorithm:
//   - If args is not a map (e.g. a scalar), the whole value is rendered as
//     a single string and length-capped.
//   - For each top-level (key, value) pair:
//   - If `key` matches secretKeyPattern → value becomes "<redacted>".
//   - Otherwise the value is JSON-encoded, and if the resulting string
//     matches bearerTokenValuePattern → "<redacted>".
//   - Otherwise the value is truncated to 32 chars (UTF-8 safe) with
//     a trailing `…` marker if truncation occurred.
//   - Nested maps/arrays are summarized as `<object:N keys>` /
//     `<array:N elems>` rather than recursed; the goal is operator-readable
//     posture, not a full dump (the full args lives in `args_hash`'s
//     pre-image, which is hashed not stored, so deep recursion would blow
//     audit-log size for no security benefit).
//
// Returns nil for nil input (omitted from JSON via the caller's `omitempty`).
func ArgsPreview(args any) map[string]any {
	if args == nil {
		return nil
	}
	m, ok := args.(map[string]any)
	if !ok {
		// Scalar / array / unknown — wrap so the audit reader still gets
		// SOME information without a panic.
		return map[string]any{"_value": redactPreviewValue("_value", args)}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = redactPreviewValue(k, v)
	}
	return out
}

// redactPreviewValue applies the per-value redaction rules. Exported only
// for tests; not part of the public API.
func redactPreviewValue(key string, v any) any {
	if secretKeyPattern.MatchString(key) {
		return "<redacted>"
	}
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		if bearerTokenValuePattern.MatchString(x) {
			return "<redacted>"
		}
		return truncate(x, previewMaxLen)
	case bool, float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, json.Number:
		return v
	case map[string]any:
		return fmt.Sprintf("<object:%d keys>", len(x))
	case []any:
		return fmt.Sprintf("<array:%d elems>", len(x))
	}
	// Fallback: marshal + truncate.
	data, err := json.Marshal(v)
	if err != nil {
		return "<unencodable>"
	}
	return truncate(string(data), previewMaxLen)
}

// truncate clamps s to at most maxLen bytes on a UTF-8 rune boundary, appending
// the unicode-aware ellipsis "…" when truncation occurred.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find the last rune boundary <= maxLen.
	cut := maxLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	var b strings.Builder
	b.Grow(cut + 3)
	b.WriteString(s[:cut])
	b.WriteString("…")
	return b.String()
}
