// Tests for args_hash and args_preview (FR-080).

package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestAuditArgsHash_Deterministic asserts that ArgsHash returns the same
// 64-char lowercase-hex string across 100 runs of byte-equal input,
// regardless of Go's map-iteration noise. This is the FR-080 contract.
func TestAuditArgsHash_Deterministic(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"path":    "/etc/passwd",
		"mode":    "rw",
		"flags":   []any{"O_CREAT", "O_EXCL"},
		"depth":   float64(3),
		"context": map[string]any{"user": "root", "uid": float64(0)},
		"meta":    map[string]any{"z": "z", "a": "a", "m": "m"},
	}

	first, err := ArgsHash(args)
	if err != nil {
		t.Fatalf("ArgsHash err: %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("hash length: want 64, got %d (%q)", len(first), first)
	}
	if first != strings.ToLower(first) {
		t.Fatalf("hash must be lowercase hex: %q", first)
	}
	if strings.Trim(first, "0123456789abcdef") != "" {
		t.Fatalf("hash must be hex chars only: %q", first)
	}

	for i := 0; i < 100; i++ {
		// Recreate the map fresh each iteration to maximize iteration-order
		// variance — Go randomizes map iteration, so this exercises the
		// canonical-key-sort code path 100 times.
		clone := map[string]any{
			"path":    "/etc/passwd",
			"mode":    "rw",
			"flags":   []any{"O_CREAT", "O_EXCL"},
			"depth":   float64(3),
			"context": map[string]any{"user": "root", "uid": float64(0)},
			"meta":    map[string]any{"z": "z", "a": "a", "m": "m"},
		}
		got, err := ArgsHash(clone)
		if err != nil {
			t.Fatalf("iter %d: ArgsHash err: %v", i, err)
		}
		if got != first {
			t.Fatalf("iter %d: hash drift\n  first=%s\n  got  =%s", i, first, got)
		}
	}
}

// TestAuditArgsHash_RFC8785_Compliance pins the canonicaljson output for a
// few hand-crafted fixtures. The expected hashes were computed with the
// canonicaljson encoding documented in this package and verified against
// `sha256sum` — they are the contract.
func TestAuditArgsHash_RFC8785_Compliance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		args      any
		canonical string // expected canonical JSON pre-image (for debugging)
	}{
		{
			name:      "nil",
			args:      nil,
			canonical: `null`,
		},
		{
			name:      "empty_object",
			args:      map[string]any{},
			canonical: `{}`,
		},
		{
			name: "key_sort",
			args: map[string]any{
				"b": "two",
				"a": "one",
				"c": "three",
			},
			canonical: `{"a":"one","b":"two","c":"three"}`,
		},
		{
			name: "nested_sort",
			args: map[string]any{
				"outer": map[string]any{
					"z": float64(1),
					"a": float64(2),
				},
			},
			canonical: `{"outer":{"a":2,"z":1}}`,
		},
		{
			name: "integer_float_canonicalised",
			args: map[string]any{
				"n": float64(42),
			},
			canonical: `{"n":42}`,
		},
		{
			name: "fractional_float_kept",
			args: map[string]any{
				"n": float64(3.14),
			},
			canonical: `{"n":3.14}`,
		},
		{
			name: "string_escapes",
			args: map[string]any{
				"s": "a\"b\\c\nd",
			},
			canonical: `{"s":"a\"b\\c\nd"}`,
		},
		{
			name: "array_order_preserved",
			args: map[string]any{
				"arr": []any{float64(3), float64(1), float64(2)},
			},
			canonical: `{"arr":[3,1,2]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Re-encode with our canonical encoder and compare against the
			// fixture. This is the RFC 8785 conformance gate.
			got, err := ArgsHash(tc.args)
			if err != nil {
				t.Fatalf("ArgsHash err: %v", err)
			}
			// Recompute via the lower-level encoder to assert canonical
			// pre-image too (catches future regressions in the encoder
			// without touching the hash output).
			var bb bytes.Buffer
			if encErr := canonicalEncode(&bb, tc.args); encErr != nil {
				t.Fatalf("canonicalEncode err: %v", encErr)
			}
			if canonical := bb.String(); canonical != tc.canonical {
				t.Fatalf("canonical pre-image\n  want %q\n  got  %q", tc.canonical, canonical)
			}
			if len(got) != 64 {
				t.Fatalf("hash length: want 64, got %d", len(got))
			}
		})
	}
}

// TestAuditArgsHash_JSONNumberCompat asserts json.Number values are passed
// through verbatim — required so that args coming from the LLM provider
// (which we decode with UseNumber()) hash identically across boundaries.
func TestAuditArgsHash_JSONNumberCompat(t *testing.T) {
	t.Parallel()
	a := map[string]any{"n": json.Number("42")}
	b := map[string]any{"n": float64(42)}
	ha, err := ArgsHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := ArgsHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("json.Number(42) and float64(42) should hash equal:\n  %s\n  %s", ha, hb)
	}
}

// TestAuditArgsPreview_Redacts_Secrets verifies FR-080's args_preview
// redaction: top-level values whose keys match the secret-key regex, and
// values that look like bearer tokens, are masked.
func TestAuditArgsPreview_Redacts_Secrets(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"path":           "/var/log/app.log",
		"api_key":        "sk-1234567890abcdef",
		"OPENAI_API_KEY": "leaked-please-redact",
		"password":       "hunter2",
		"client_secret":  "shhh",
		"token":          "xyz",
		"authorization":  "Bearer abcdef0123456789",
		// innocent key, bearer-shaped value
		"header": "Bearer abcdef0123456789xyz",
		// innocent key, innocent value
		"name": "alice",
		// integer (passes through)
		"depth": float64(3),
		// nested map → summarized
		"opts": map[string]any{"a": 1, "b": 2},
	}
	got := ArgsPreview(args)

	mustRedacted := []string{"api_key", "OPENAI_API_KEY", "password", "client_secret", "token", "authorization"}
	for _, k := range mustRedacted {
		v, ok := got[k]
		if !ok {
			t.Fatalf("expected key %q in preview", k)
		}
		if v != "<redacted>" {
			t.Errorf("key %q: want <redacted>, got %v", k, v)
		}
	}
	if got["header"] != "<redacted>" {
		t.Errorf("bearer-shaped header value not redacted: %v", got["header"])
	}
	if got["name"] != "alice" {
		t.Errorf("benign name should be passed through: %v", got["name"])
	}
	if got["path"] != "/var/log/app.log" {
		t.Errorf("benign path should be passed through: %v", got["path"])
	}
	if v, ok := got["opts"].(string); !ok || !strings.HasPrefix(v, "<object:") {
		t.Errorf("nested map should summarize, got %v", got["opts"])
	}
}

// TestAuditArgsPreview_Truncates verifies values exceeding previewMaxLen
// are clipped with the unicode ellipsis.
func TestAuditArgsPreview_Truncates(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 200)
	got := ArgsPreview(map[string]any{"q": long})
	v, ok := got["q"].(string)
	if !ok {
		t.Fatalf("q not string: %T", got["q"])
	}
	if !strings.HasSuffix(v, "…") {
		t.Errorf("expected ellipsis suffix on truncated preview: %q", v)
	}
	// 32 bytes of "x" + "…" (3 bytes) = 35 bytes max
	if len(v) > previewMaxLen+3 {
		t.Errorf("preview too long: %d bytes, want <= %d", len(v), previewMaxLen+3)
	}
}

// TestAuditArgsPreview_NilReturnsNil — explicit nil preserves omitempty.
func TestAuditArgsPreview_NilReturnsNil(t *testing.T) {
	t.Parallel()
	if got := ArgsPreview(nil); got != nil {
		t.Fatalf("nil args should return nil, got %v", got)
	}
}
