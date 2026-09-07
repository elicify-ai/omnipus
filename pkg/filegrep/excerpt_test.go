// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestFileGrep_UTF8SafeExcerpts pins MV-6: excerpts are <= ExcerptCapBytes
// and always valid UTF-8, including through the full Search path over
// multibyte content (CJK, emoji).
func TestFileGrep_UTF8SafeExcerpts(t *testing.T) {
	t.Run("excerpt around a match in a long line stays under the cap and valid UTF-8", func(t *testing.T) {
		pad := strings.Repeat("あ", 2000) // 3 bytes/rune in UTF-8
		content := pad + "needle" + pad + "\n"
		res := mustSearch(t, oneRoot(buildFS(map[string]string{"f.txt": content})), Options{Query: "needle"})
		if len(res.Hits) != 1 {
			t.Fatalf("want 1 hit, got %d", len(res.Hits))
		}
		e := res.Hits[0].Excerpt
		if len(e) > ExcerptCapBytes {
			t.Fatalf("excerpt length %d exceeds ExcerptCapBytes %d", len(e), ExcerptCapBytes)
		}
		if !utf8.ValidString(e) {
			t.Fatalf("excerpt is not valid UTF-8: %q", e)
		}
		if !strings.Contains(e, "needle") {
			t.Fatalf("excerpt should contain the match: %q", e)
		}
	})

	t.Run("full-width CJK content with a Japanese literal query", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"f.txt": "これはテストです。日本語の文字列。\n",
		})), Options{Query: "日本語"})
		if len(res.Hits) != 1 {
			t.Fatalf("want 1 hit, got %d", len(res.Hits))
		}
		if !utf8.ValidString(res.Hits[0].Excerpt) {
			t.Fatalf("excerpt not valid UTF-8: %q", res.Hits[0].Excerpt)
		}
		if !strings.Contains(res.Hits[0].Excerpt, "日本語") {
			t.Fatalf("excerpt should contain the match, got %q", res.Hits[0].Excerpt)
		}
	})

	t.Run("emoji content", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"f.txt": "status update 🎉 needle 🚀 done\n",
		})), Options{Query: "needle"})
		if len(res.Hits) != 1 {
			t.Fatalf("want 1 hit, got %d", len(res.Hits))
		}
		if !utf8.ValidString(res.Hits[0].Excerpt) {
			t.Fatalf("excerpt not valid UTF-8: %q", res.Hits[0].Excerpt)
		}
	})

	t.Run("excerpt helper directly: every rune boundary snap stays valid", func(t *testing.T) {
		line := []byte(strings.Repeat("日本語テスト文字列あいうえお", 60))
		for pos := 0; pos < len(line); pos += 7 { // odd stride to hit mid-rune bytes
			e := excerpt(line, pos)
			if !utf8.ValidString(e) {
				t.Fatalf("excerpt(line, %d) not valid UTF-8: %q", pos, e)
			}
			if len(e) > ExcerptCapBytes {
				t.Fatalf("excerpt(line, %d) length %d exceeds ExcerptCapBytes", pos, len(e))
			}
		}
	})

	t.Run("empty line", func(t *testing.T) {
		if got := excerpt(nil, 0); got != "" {
			t.Fatalf("excerpt(nil, 0) = %q, want empty", got)
		}
	})

	t.Run("rune-boundary snap on both edges never exceeds the cap", func(t *testing.T) {
		line := []byte(strings.Repeat("あ", 1000)) // 3-byte runes, cap not a multiple of 3
		e := excerpt(line, 0)
		if len(e) > ExcerptCapBytes {
			t.Fatalf("excerpt length %d exceeds ExcerptCapBytes %d", len(e), ExcerptCapBytes)
		}
		if !utf8.ValidString(e) {
			t.Fatalf("excerpt is not valid UTF-8: %q", e)
		}
	})
}

// FuzzExcerpt seeds go test -fuzz with representative multibyte content and
// asserts the excerpt is always valid UTF-8 and within the size cap,
// regardless of where pos lands (including mid-rune and out-of-range).
func FuzzExcerpt(f *testing.F) {
	seeds := []struct {
		line string
		pos  int
	}{
		{"", 0},
		{"needle", 0},
		{"needle", 3},
		{"日本語の文字列です", 0},
		{"日本語の文字列です", 3},
		{"日本語の文字列です", 100},
		{"🎉🚀🔥 emoji line with needle inside 🎉", 10},
		{strings.Repeat("あ", 1000), 500},
		{"café résumé naïve", 4},
		{"Привет мир", 5},
		{"line with\ttab and\r stray cr", 8},
	}
	for _, s := range seeds {
		f.Add(s.line, s.pos)
	}
	f.Fuzz(func(t *testing.T, line string, pos int) {
		b := []byte(line)
		got := excerpt(b, pos)
		if !utf8.ValidString(got) {
			t.Fatalf("excerpt(%q, %d) produced invalid UTF-8: %q", line, pos, got)
		}
		if len(got) > ExcerptCapBytes {
			t.Fatalf("excerpt(%q, %d) length %d exceeds ExcerptCapBytes", line, pos, len(got))
		}
	})
}
