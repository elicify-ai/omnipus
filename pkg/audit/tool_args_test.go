package audit

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UAT 2026-09-13 D-85: the identifying subset of a tool call's arguments.
func TestSalientToolArgs_KeepsIdentityDropsContent(t *testing.T) {
	got := SalientToolArgs("knowledge_edit", map[string]any{
		"op":       "set_property",
		"path":     "Projects/Alpha.md",
		"property": "status",
		"value":    "active",
		"content":  "a whole document body that must never reach the audit log",
		"paths":    []any{"a.md", "b.md", 3, map[string]any{"nested": true}},
		"api_key":  "sk-live-123",
		"nested":   map[string]any{"path": "x"},
	})
	assert.Equal(t, "set_property", got["op"])
	assert.Equal(t, "Projects/Alpha.md", got["path"])
	assert.Equal(t, "status", got["property"])
	assert.NotContains(t, got, "value", "payload keys are not identity")
	assert.NotContains(t, got, "content")
	assert.NotContains(t, got, "nested")
	assert.NotContains(t, got, "api_key")
	assert.Equal(t, []any{"a.md", "b.md", 3}, got["paths"], "lists keep scalars only")
}

func TestSalientToolArgs_EmptyAndTruncation(t *testing.T) {
	assert.Nil(t, SalientToolArgs("x", nil))
	assert.Nil(t, SalientToolArgs("x", map[string]any{"content": "only content"}))
	long := make([]byte, salientToolArgMaxValueLen+50)
	for i := range long {
		long[i] = 'p'
	}
	got := SalientToolArgs("x", map[string]any{"path": string(long)})
	assert.Len(t, got["path"], salientToolArgMaxValueLen+len("…"))
	assert.Equal(t, "op=set path=a.md", SalientToolArgsSummary("x", map[string]any{"path": "a.md", "op": "set", "content": "z"}))
}

// Codex review 2026-09-14 finding #10: a URL copied into a durable audit
// entry must not carry credentials. Userinfo, the query string and the
// fragment are stripped from every URL-shaped string value, under every
// key and inside lists — a bearer token in ?access_token=, a signed S3
// URL's X-Amz-Signature, or user:password@ in the authority never reach
// the log. The scheme, host and path (what the call touched) are kept.
func TestSalientToolArgs_StripsCredentialsFromURLs(t *testing.T) {
	got := SalientToolArgs("web_fetch", map[string]any{
		"url":    "https://example.org/private?access_token=abcdef123456#frag",
		"target": "https://user:s3cret@files.example.com/a/b.pdf?sig=zzz",
		"paths": []any{
			"https://bucket.s3.amazonaws.com/reports/q3.csv?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIA%2F20260914&X-Amz-Signature=deadbeef",
			"Projects/Alpha.md",
		},
		"path": "notes/not-a-url.md?keep=this",
	})
	assert.Equal(t, "https://example.org/private", got["url"])
	assert.Equal(t, "https://files.example.com/a/b.pdf", got["target"])
	assert.Equal(t, []any{"https://bucket.s3.amazonaws.com/reports/q3.csv", "Projects/Alpha.md"}, got["paths"])
	assert.Equal(t, "notes/not-a-url.md?keep=this", got["path"], "a plain path is not a URL and is kept as written")

	summary := SalientToolArgsSummary("web_fetch", map[string]any{"url": "https://h.example/x?token=T0K3N"})
	assert.Equal(t, "url=https://h.example/x", summary)
	for _, leak := range []string{"abcdef123456", "s3cret", "deadbeef", "AKIA", "T0K3N", "sig=zzz", "#frag"} {
		assert.NotContains(t, summary, leak)
		for _, v := range got {
			assert.NotContains(t, fmt.Sprint(v), leak, "credential material must never reach the audit entry")
		}
	}
}

// A URL that does not parse cannot be sanitised, so it is dropped rather
// than logged as-is.
func TestSalientToolArgs_UnparseableURLIsDropped(t *testing.T) {
	got := SalientToolArgs("web_fetch", map[string]any{
		"url":  "https://bad host/with?token=abc",
		"path": "ok.md",
	})
	assert.NotContains(t, got, "url")
	assert.Equal(t, "ok.md", got["path"])
}

// Claude review 2026-09-14 (cut-list): truncateSalient sliced bytes mid-rune,
// writing mojibake into durable audit records whenever a multi-byte value
// crossed the 256-byte cap, and skipped bearerTokenValuePattern entirely —
// the old ArgsPreview surface redacted bearer-shaped VALUES under innocent
// keys and capped them at 32 bytes, while this one logged up to 256.
func TestSalientToolArgs_TruncationIsRuneSafe(t *testing.T) {
	// 100 three-byte runes = 300 bytes: the 256-byte cap falls mid-rune.
	multibyte := strings.Repeat("日", 100)
	got := SalientToolArgs("records", map[string]any{"path": multibyte})
	v, ok := got["path"].(string)
	require.True(t, ok)
	assert.True(t, utf8.ValidString(v), "truncation must not split a rune: got %q", v)
	assert.Len(t, v, 255+len("…"), "256-byte cap lands after 85 whole runes (255 bytes) plus the ellipsis")
	assert.True(t, strings.HasSuffix(v, "…"))
	assert.Equal(t, strings.Repeat("日", 85), strings.TrimSuffix(v, "…"),
		"the kept prefix must be a whole number of runes")

	// Under the cap, a multi-byte value passes through untouched.
	short := strings.Repeat("é", 10)
	got = SalientToolArgs("records", map[string]any{"path": short})
	assert.Equal(t, short, got["path"])
}

// A multi-byte URL is BOTH redacted (credential parts dropped, round-2 rule)
// and truncated on a rune boundary — the two rules must compose, and neither
// may resurface half a rune or the dropped query.
func TestSalientToolArgs_MultiByteURLTruncatesOnRuneBoundaryAndStaysRedacted(t *testing.T) {
	longURL := "https://example.com/" + strings.Repeat("文", 200) + "?access_token=abcdef123456"
	got := SalientToolArgs("web_fetch", map[string]any{"url": longURL})
	v, ok := got["url"].(string)
	require.True(t, ok)
	assert.True(t, utf8.ValidString(v), "URL truncation must not split a rune: got %q", v)
	assert.True(t, strings.HasPrefix(v, "https://example.com/"))
	assert.NotContains(t, v, "access_token", "the round-2 URL redaction must still run before the cap")
	assert.NotContains(t, v, "abcdef123456")
	assert.True(t, strings.HasSuffix(v, "…"))
	assert.LessOrEqual(t, len(v), salientToolArgMaxValueLen+len("…"))
}

// Bearer-shaped VALUES under innocent allowlisted keys are redacted by the
// same pattern ArgsPreview uses — the salient copy used to skip it and log
// the token (up to 256 bytes) under `key`, `id`, `name`, `target`.
func TestSalientToolArgs_BearerShapedValuesRedacted(t *testing.T) {
	got := SalientToolArgs("connector", map[string]any{
		"key":    "sk-live-1234567890abcdef12",
		"id":     "Bearer abcdef123456",
		"target": "xoxb-1234567890abcdefghij",
		"name":   "ordinary-name", // not token-shaped: kept
	})
	assert.Equal(t, "<redacted>", got["key"])
	assert.Equal(t, "<redacted>", got["id"])
	assert.Equal(t, "<redacted>", got["target"])
	assert.Equal(t, "ordinary-name", got["name"])

	// Inside lists too (paths/names) — every string routes through the same
	// salientString.
	got = SalientToolArgs("connector", map[string]any{
		"paths": []any{"a.md", "sk-live-1234567890abcdef12"},
	})
	assert.Equal(t, []any{"a.md", "<redacted>"}, got["paths"])
}
