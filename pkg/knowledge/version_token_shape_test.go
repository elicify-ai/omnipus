// Omnipus — ADR-083 review F5 (NoteVersion.TokenIfPresent) and ADR-083
// §4.2a(c) (IsWellFormedVersionToken): two small primitives whose entire job
// is to make one specific mistake impossible to write. Both are new and had
// zero tests before this file.
//
// The oracle for the shape table is the ENCODING documented on
// IsWellFormedVersionToken's own doc comment (versionTokenPrefix = "v1:",
// versionTokenHexLen = 32 lower-case hex characters, or the literal
// TokenAbsent sentinel) — never the function's own output. The one row that
// legitimately needs the real minter (ComputeVersionToken) is called out
// explicitly as using a different, independent oracle: "the real minter
// produces something the shape-checker accepts", not "the shape-checker
// accepts what it accepts".
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^Test(NoteVersion_TokenIfPresent|IsWellFormedVersionToken)$' -p 1 ./pkg/knowledge/
package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoteVersion_TokenIfPresent is ADR-083 review F5's regression: reading
// v.Token directly, without checking v.Exists first, compiles and is wrong —
// it lets TokenAbsent leak out disguised as a real token. TokenIfPresent
// exists to make that check syntactically unavoidable.
func TestNoteVersion_TokenIfPresent(t *testing.T) {
	t.Run("Exists=false: ok is false and the token is empty, never TokenAbsent leaking out", func(t *testing.T) {
		// This is the whole point of the guard: a caller who forgets to
		// check ok and uses the returned token anyway must get an empty
		// string, not the TokenAbsent sentinel silently smuggled through
		// as if it were a real value.
		nv := NoteVersion{Path: "some/note.md", Exists: false, Token: TokenAbsent}

		tok, ok := nv.TokenIfPresent()

		assert.False(t, ok)
		assert.Equal(t, VersionToken(""), tok)
		assert.NotEqual(t, TokenAbsent, tok, "TokenAbsent must not leak out through the present-token return")
	})

	t.Run("Exists=true: the token and ok=true are returned", func(t *testing.T) {
		// The real minter, not a hand-typed literal: ties this branch to
		// whatever ComputeVersionToken actually produces today.
		minted := ComputeVersionToken([]byte("some content"))
		nv := NoteVersion{Path: "some/note.md", Exists: true, Token: minted}

		tok, ok := nv.TokenIfPresent()

		require.True(t, ok)
		assert.Equal(t, minted, tok)
	})
}

// TestIsWellFormedVersionToken is the ADR-083 §4.2a(c) shape table: a
// malformed token must be told apart from a stale one, because they carry
// different remedies (400 vs 409). The named failure case is a client that
// forgot to strip JSON quotes and resends `"v1:...32hex..."`, quotes and all
// — that must read as malformed, not as "changed on disk".
func TestIsWellFormedVersionToken(t *testing.T) {
	const validHex32 = "0123456789abcdef0123456789abcdef" // exactly 32 lower-case hex chars

	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"v1: prefix plus exactly 32 lower-case hex chars", "v1:" + validHex32, true},
		{"the literal TokenAbsent sentinel", string(TokenAbsent), true},
		{
			// ADR-083 §4.2a(c)'s named case: a client that forgot to strip
			// the JSON quotes around a token it received. The literal
			// double-quote characters are part of raw, not Go string
			// escaping noise — this is what actually arrives over the wire
			// in that failure mode.
			"JSON-quoted token (literal quote characters at both ends)",
			`"v1:` + validHex32 + `"`,
			false,
		},
		{"empty string", "", false},
		{"v1: prefix with only 16 hex chars (too short)", "v1:" + validHex32[:16], false},
		{"v1: prefix with 64 hex chars (too long)", "v1:" + validHex32 + validHex32, false},
		{
			"v1: prefix with an upper-case hex letter",
			"v1:" + "0123456789ABCDEF0123456789abcdef", // 32 chars, one block upper-cased
			false,
		},
		{"sha256: prefix instead of v1:", "sha256:" + validHex32, false},
		{"v2: prefix instead of v1:", "v2:" + validHex32, false},
		{"bare 32-hex string with no prefix at all", validHex32, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsWellFormedVersionToken(tc.raw))
		})
	}

	t.Run("a token from the real minter is accepted", func(t *testing.T) {
		// Independent oracle: the real minter, not a hand-typed literal.
		// This ties the shape-checker to what ComputeVersionToken actually
		// produces, rather than to this test's own assumptions about it.
		minted := string(ComputeVersionToken([]byte("some content")))
		assert.True(t, IsWellFormedVersionToken(minted))
	})
}
