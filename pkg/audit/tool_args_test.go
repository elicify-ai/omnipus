package audit

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
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
