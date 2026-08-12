// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEscapeSeatbeltRegex_EscapesEveryMetacharacter is the guard on the one
// thing that can turn an anchored deny into a looser pattern SILENTLY.
//
// A home path containing `.`, `+`, `(`, `[` or `|` is ordinary — macOS accounts
// and temp directories produce them — and an unescaped metacharacter still
// renders, still reads as protection, and matches the wrong set. The escaper is
// therefore an ALLOW-list; this test pins that it stays one.
func TestEscapeSeatbeltRegex_EscapesEveryMetacharacter(t *testing.T) {
	for _, meta := range []string{".", "+", "*", "?", "(", ")", "[", "]", "{", "}", "|", "^", "$", " "} {
		got := escapeSeatbeltRegex("/a" + meta + "b")
		assert.Equal(t, `/a\`+meta+"b", got, "metacharacter %q must be escaped", meta)
	}

	// Safe bytes stay bare, or the pattern becomes unreadable for no benefit.
	assert.Equal(t, "/home/user_1-2/x", escapeSeatbeltRegex("/home/user_1-2/x"))

	// Multi-byte UTF-8 passes through untouched: a backslash before a
	// continuation byte is not an escape, it is an undefined sequence, and no
	// regex metacharacter is non-ASCII.
	assert.Equal(t, "/José/x", escapeSeatbeltRegex("/José/x"))
}

// TestSeatbeltProfile_PrefixDenyIsSingleEscapedAndAnchored pins the emission
// itself, because the bug it prevents was found by MEASUREMENT and not by
// reading: rendering the pattern through Go's %q verb double-escaped the
// backslashes (`\\.` where SBPL needs `\.`), and the resulting filter matched
// nothing. A real child read the backup while the profile looked correct.
func TestSeatbeltProfile_PrefixDenyIsSingleEscapedAndAnchored(t *testing.T) {
	profile, err := renderSeatbeltProfile(SandboxPolicy{
		FilesystemRules:    []PathRule{{Path: "/tmp/omnipus-home", Access: AccessRead | AccessWrite}},
		DeniedPathPrefixes: []string{"/tmp/omnipus-home/config.json."},
	})
	require.NoError(t, err)

	assert.Contains(t, profile, `(regex #"^/private/tmp/omnipus-home/config\.json\.")`,
		"the pattern must be anchored, symlink-resolved (/tmp -> /private/tmp on macOS) and "+
			"SINGLE escaped")
	assert.NotContains(t, profile, `\\.`,
		"a double-escaped pattern matches nothing; the profile would read as protecting the "+
			"backup and protect nothing")

	// Read AND write. A read-only deny on a secret is defeated by truncate,
	// which destroys the file without reading it — the same reasoning the
	// subpath deny block already applies.
	assert.Equal(t, 1, strings.Count(profile, `(deny file-read* (regex #"^/private/tmp/omnipus-home/config\.json\.")`))
	assert.Equal(t, 1, strings.Count(profile, `(deny file-write* (regex #"^/private/tmp/omnipus-home/config\.json\.")`))
}

// TestSeatbeltProfile_NodeDenyIsLiteralNotSubpath pins the other half of the
// same "the shape of the filter IS the fix" point. A subpath deny on the node
// would block the rename and also lock the agent out of its own home; a literal
// covers the directory entry and nothing beneath it.
func TestSeatbeltProfile_NodeDenyIsLiteralNotSubpath(t *testing.T) {
	profile, err := renderSeatbeltProfile(SandboxPolicy{
		FilesystemRules: []PathRule{{Path: "/tmp/omnipus-home", Access: AccessRead | AccessWrite}},
		DeniedNodes:     []string{"/tmp/omnipus-home/agents"},
	})
	require.NoError(t, err)

	assert.Contains(t, profile, `(deny file-write* (literal "/private/tmp/omnipus-home/agents"))`)
	assert.NotContains(t, profile, `(deny file-write* (subpath "/private/tmp/omnipus-home/agents"))`,
		"a subpath deny here would deny the agent its own home — the very tree the per-turn root "+
			"is re-admitted for")
	assert.NotContains(t, profile, `(deny file-read* (literal "/private/tmp/omnipus-home/agents"))`,
		"reads on the node must stay allowed: the child has to traverse it to reach its work dir")
}
