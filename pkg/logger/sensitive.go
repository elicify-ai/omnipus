package logger

import (
	"bytes"
	"encoding/json"
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
		return r.Replace(val)
	case error:
		return r.Replace(val.Error())
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
			out[i] = r.Replace(s)
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
			out[r.Replace(k)] = r.Replace(s)
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
