package logger

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

// sensitiveValueReplacer is the process-wide replacer for the credential
// plaintexts this process has registered. pkg/config publishes it from
// Config.RegisterSensitiveValues — boot step 6 of the credential boot contract
// (ADR-004) and every re-registration after it (config reload, REST config
// writes, provider sign-in) — and again from EnsureSensitiveValues and
// RefreshAgentEnvSensitiveValues. Each publication carries the registered
// values plus the external-worker env values, and the most recent one is the
// set in force. It lives in this package because pkg/logger is the lowest
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
// Production installs it from pkg/config (RegisterSensitiveValues,
// EnsureSensitiveValues and RefreshAgentEnvSensitiveValues); tests use the
// restore func to leave the process as they found it.
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
// A field that can still be decoded at this depth is not read further and is
// replaced whole (see scrubString).
const maxScrubDecodeDepth = 8

// scrubFailClosedText is logged in place of a field that could not be shown
// free of a registered credential. It is the mask
// pkg/config/security.go::sensitiveReplacementPairs uses.
const scrubFailClosedText = "[FILTERED]"

// scrubString replaces registered credentials in one string field. Its aim is
// that a credential r replaces reaches the log sink neither as written nor
// in a form that the decodings of decodeScan turn back into it, within
// maxScrubDecodeDepth layers. logMessage calls it, through scrubFields and
// scrubFieldValue, for string and error field values and for the strings
// inside the containers scrubFieldValue walks, before both sinks.
//
//   - A field with no credential, as written or decoded, is returned byte for
//     byte, so log lines can be compared with real files.
//   - When the plain replacement leaves nothing that decodes to a
//     credential, that replacement is returned.
//   - Otherwise the field is decoded layer by layer, masked at every layer,
//     and logged quoted once, so the record holds no control characters. If
//     scanning that result again does not come back clean, the field is
//     replaced whole by scrubFailClosedText.
//   - A field that can still be decoded at maxScrubDecodeDepth, or whose
//     code-point reading (codePointReading) holds a credential, is replaced
//     whole as well.
//
// The work per field grows with the field's length and with the registered
// set, within the depth bound; its time and memory have not been measured.
func scrubString(r *strings.Replacer, s string) string {
	masked := r.Replace(s)
	// Without a backslash or a quote nothing in the field can be decoded.
	if !strings.ContainsAny(masked, `\"`) {
		return masked
	}
	switch decodeScan(r, masked) {
	case scanClean:
		return masked
	case scanTooDeep, scanCodePoint:
		return scrubFailClosedText
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
	if decodeScan(r, out) != scanClean {
		return scrubFailClosedText
	}
	return out
}

// decodeScanResult is the outcome of decodeScan.
type decodeScanResult int

const (
	// scanClean: every string reachable within the depth bound was read and
	// none holds a registered credential.
	scanClean decodeScanResult = iota
	// scanCredential: a reachable string holds a registered credential.
	scanCredential
	// scanTooDeep: no credential within the bound, but a string at the
	// bound can still be decoded further, so the field is not known clean.
	scanTooDeep
	// scanCodePoint: the code-point reading of a reachable string holds a
	// registered credential.
	scanCodePoint
)

// decodeScan reads s and every string reachable from it by unquoting a whole
// quoted string or decoding Go escape sequences, both decodings tried at
// every layer, up to maxScrubDecodeDepth layers. Each of those strings is
// also checked in its code-point reading (codePointReading), which is not
// decoded further. It returns at the first credential it finds.
func decodeScan(r *strings.Replacer, s string) decodeScanResult {
	seen := map[string]bool{s: true}
	frontier := []string{s}
	tooDeep := false
	for depth := 0; len(frontier) > 0; depth++ {
		var next []string
		for _, v := range frontier {
			if r.Replace(v) != v {
				return scanCredential
			}
			if cp, ok := codePointReading(v); ok && r.Replace(cp) != cp {
				return scanCodePoint
			}
			for _, decode := range [...]func(string) (string, bool){unquoteWhole, unescapeGo} {
				u, ok := decode(v)
				if !ok || seen[u] {
					continue
				}
				if depth == maxScrubDecodeDepth {
					// Not read further; every string at this depth is
					// still checked for a credential above.
					tooDeep = true
					continue
				}
				seen[u] = true
				next = append(next, u)
			}
		}
		frontier = next
	}
	if tooDeep {
		return scanTooDeep
	}
	return scanClean
}

// codePointReading returns s with every escape that readEscapedChar accepts
// replaced by the character it names, written in UTF-8, in one pass. A
// numeric escape names a code point here, where Go reads the hex and octal
// byte escapes as single bytes. Text that readEscapedChar does not accept is
// kept as written. ok is false when nothing was read.
//
// The result is never longer than s: every escape is longer than the UTF-8
// form of the character it names.
func codePointReading(s string) (string, bool) {
	if !strings.Contains(s, `\`) {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	changed := false
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			if c, n, ok := readEscapedChar(s[i:]); ok {
				b.WriteRune(c)
				i += n
				changed = true
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), changed
}

// readEscapedChar reads the escape at the start of s (s[0] is a backslash).
// It returns the character the escape names, the length of the escape in
// bytes, and false when s does not start with an escape it accepts. After
// the backslash it accepts:
//
//   - x, u or U followed by 2, 4 or 8 hex digits (readHexChar);
//   - three octal digits whose value is at most 0377 (255), naming the code
//     point with that value;
//   - one of the single-character escapes in simpleEscapes.
func readEscapedChar(s string) (rune, int, bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	switch s[1] {
	case 'x':
		return readHexChar(s, 2)
	case 'u':
		return readHexChar(s, 4)
	case 'U':
		return readHexChar(s, 8)
	case '0', '1', '2', '3':
		if len(s) < 4 {
			return 0, 0, false
		}
		var v rune
		for _, c := range []byte(s[1:4]) {
			if c < '0' || c > '7' {
				return 0, 0, false
			}
			v = v<<3 | rune(c-'0')
		}
		return v, 4, true
	}
	if c, ok := simpleEscapes[s[1]]; ok {
		return c, 2, true
	}
	return 0, 0, false
}

// readHexChar reads the escape at the start of s made of a backslash, one
// letter and the given number of hex digits of either case, and returns the
// code point the digits name. It returns false when a digit is missing or
// not hex, or when the value is not a valid code point (utf8.ValidRune).
func readHexChar(s string, digits int) (rune, int, bool) {
	if len(s) < 2+digits {
		return 0, 0, false
	}
	var v rune
	for i := 2; i < 2+digits; i++ {
		d, ok := hexDigit(s[i])
		if !ok {
			return 0, 0, false
		}
		v = v<<4 | rune(d)
	}
	if !utf8.ValidRune(v) {
		return 0, 0, false
	}
	return v, 2 + digits, true
}

// simpleEscapes are the single-character escapes and the characters they
// name.
var simpleEscapes = map[byte]rune{
	'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v',
	'\\': '\\', '\'': '\'', '"': '"',
}

// hexDigit returns the value of one hex digit of either case.
func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
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
// parses, otherwise written as the scrubbed string. A value that cannot be
// encoded as JSON is returned as it is.
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
