// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package pathsafe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ValidateComponent: exhaustive table ---

func TestValidateComponent(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr error // nil means "must pass"
	}{
		// --- legitimate names (must pass) ---
		{"plain name", "report.txt", nil},
		{"real-world UAT name 1", "Copy of My Deck.pptx", nil},
		{"real-world UAT name 2 (unicode/emoji)", "My Report (final) — résumé 测试 🎉.txt", nil},
		{"dotfile", ".gitignore", nil},
		{"no extension", "Dockerfile", nil},
		{"internal dots", "archive.tar.gz", nil},
		{"internal spaces", "my great report.docx", nil},
		{"leading space allowed (only TRAILING is checked)", " leading.txt", nil},
		{"single char", "a", nil},
		{"cjk name", "报告.txt", nil},

		// --- empty ---
		{"empty", "", ErrEmptyName},

		// --- reserved device names (case-insensitive, any extension) ---
		{"reserved CON bare", "CON", ErrReservedName},
		{"reserved con lowercase", "con", ErrReservedName},
		{"reserved CoN mixed case", "CoN", ErrReservedName},
		{"reserved PRN", "PRN", ErrReservedName},
		{"reserved AUX", "AUX", ErrReservedName},
		{"reserved NUL", "NUL", ErrReservedName},
		{"reserved NUL with extension", "nul.txt", ErrReservedName},
		{"reserved COM1", "COM1", ErrReservedName},
		{"reserved COM9", "COM9", ErrReservedName},
		{"reserved com1 lowercase with ext", "com1.log", ErrReservedName},
		{"reserved LPT1", "LPT1", ErrReservedName},
		{"reserved LPT9", "LPT9", ErrReservedName},
		{"reserved with multiple extensions", "con.tar.gz", ErrReservedName},
		{"not reserved: COM0", "COM0", nil},
		{"not reserved: COM10", "COM10", nil},
		{"not reserved: CONsole", "CONsole", nil},
		{"not reserved: prefix match only, not the whole stem", "PRNfoo.txt", nil},

		// --- illegal characters ---
		{"illegal <", "bad<name.txt", ErrIllegalChar},
		{"illegal >", "bad>name.txt", ErrIllegalChar},
		{"illegal :", "bad:name.txt", ErrIllegalChar},
		{"illegal double-quote", `bad"name.txt`, ErrIllegalChar},
		{"illegal |", "bad|name.txt", ErrIllegalChar},
		{"illegal ?", "bad?name.txt", ErrIllegalChar},
		{"illegal *", "bad*name.txt", ErrIllegalChar},
		{"control char NUL", "bad\x00name.txt", ErrIllegalChar},
		{"control char tab", "bad\tname.txt", ErrIllegalChar},
		{"control char newline", "bad\nname.txt", ErrIllegalChar},
		{"control char 0x1F", "bad\x1fname.txt", ErrIllegalChar},
		{"control char 0x01", "bad\x01name.txt", ErrIllegalChar},

		// --- trailing dot/space ---
		{"trailing dot", "report.", ErrTrailingDotOrSpace},
		{"trailing space", "report ", ErrTrailingDotOrSpace},
		{"trailing multiple dots", "report...", ErrTrailingDotOrSpace},
		{"trailing dot after extension", "report.txt.", ErrTrailingDotOrSpace},

		// --- length ---
		{"exactly at cap", strings.Repeat("a", MaxComponentNameLength), nil},
		{"one over cap", strings.Repeat("a", MaxComponentNameLength+1), ErrNameTooLong},
		{
			"legacy 210-char name now rejected (used to pass under the old 256 cap)",
			strings.Repeat("a", 210), ErrNameTooLong,
		},
		{
			"unicode name within cap counts runes not bytes",
			strings.Repeat("测", MaxComponentNameLength), nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateComponent(tc.in)
			if tc.wantErr == nil {
				assert.NoError(t, err, "input=%q", tc.in)
				return
			}
			require.Error(t, err, "input=%q", tc.in)
			assert.ErrorIs(t, err, tc.wantErr, "input=%q", tc.in)
		})
	}
}

// --- ValidateRelPathLength ---

func TestValidateRelPathLength(t *testing.T) {
	assert.NoError(t, ValidateRelPathLength(strings.Repeat("a", MaxRelPathLength)))
	err := ValidateRelPathLength(strings.Repeat("a", MaxRelPathLength+1))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNameTooLong)

	// Many short, individually-valid segments can still sum past the total
	// budget even though no single segment would trip ValidateComponent.
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("dir/")
	}
	deep := b.String() + "file.txt"
	require.Greater(t, len(deep), MaxRelPathLength)
	assert.ErrorIs(t, ValidateRelPathLength(deep), ErrNameTooLong)
}

// --- SameName / FindFold: case-insensitive collision detection ---

func TestSameName(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Report.txt", "report.txt", true},
		{"REPORT.TXT", "report.txt", true},
		{"Report.txt", "Report.txt", true},
		{"Report.txt", "report.doc", false},
		{"résumé.txt", "RÉSUMÉ.txt", true},
		{"a", "b", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, SameName(tc.a, tc.b), "SameName(%q, %q)", tc.a, tc.b)
	}
}

func TestFindFold(t *testing.T) {
	existing := []string{"Report.txt", "notes.md", "IMAGE.png"}

	match, found := FindFold(existing, "report.txt")
	require.True(t, found)
	assert.Equal(t, "Report.txt", match)

	match, found = FindFold(existing, "image.PNG")
	require.True(t, found)
	assert.Equal(t, "IMAGE.png", match)

	_, found = FindFold(existing, "missing.txt")
	assert.False(t, found)
}

// --- SanitizeComponent: never rejects, always returns something safe ---

func TestSanitizeComponent(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		wantChanged   bool
		wantSanitized string // "" means "don't assert exact value, just safety"
	}{
		{"already safe name unchanged", "Copy of My Deck.pptx", false, "Copy of My Deck.pptx"},
		{"unicode/emoji unchanged", "My Report (final) — résumé 测试 🎉.txt", false, ""},

		{"illegal chars replaced", `bad<>:"|?*name.txt`, true, ""},
		{"control chars replaced", "bad\x00\nname.txt", true, ""},
		{"trailing dot trimmed", "report.", true, "report"},
		{"trailing space trimmed", "report ", true, "report"},

		{"reserved name defused bare", "con", true, "con_"},
		{"reserved name defused with extension", "CON.txt", true, "CON_.txt"},
		{"reserved name defused multi-extension", "nul.tar.gz", true, "nul_.tar.gz"},
		{"not reserved, unchanged", "console.txt", false, "console.txt"},

		{"embedded forward slash traversal stripped to last element", "../../etc/passwd", true, "passwd"},
		{"embedded backslash traversal stripped to last element", `..\..\windows\system32`, true, "system32"},
		// The exact adversarial case this package's doc calls out: naive
		// iterative ".." substring removal on "...." leaves "..", which a
		// SEPARATE later replacement of "/" would not re-examine. This
		// implementation never does substring removal at all — it extracts
		// the last path element ONCE, so there is nothing left to
		// reconstitute.
		{"four-dots-then-slashes does not reconstitute a traversal", "....//", true, "file"},
		{"pure dots", "....", true, "file"},
		{"bare dot", ".", true, "file"},
		{"bare dotdot", "..", true, "file"},
		{"empty input", "", true, "file"},
		{"all separators", "///", true, "file"},

		{"over-long truncated", strings.Repeat("a", 300) + ".txt", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := SanitizeComponent(tc.in)
			assert.Equal(t, tc.wantChanged, changed, "input=%q got=%q", tc.in, got)
			if tc.wantSanitized != "" {
				assert.Equal(t, tc.wantSanitized, got)
			}
			// Whatever SanitizeComponent returns must itself pass
			// ValidateComponent — the whole point of this function is that
			// its output is always safe.
			assert.NoError(t, ValidateComponent(got), "sanitized output %q must itself be valid", got)
		})
	}
}

func TestSanitizeComponent_TruncationPreservesExtension(t *testing.T) {
	long := strings.Repeat("a", 300) + ".txt"
	got, changed := SanitizeComponent(long)
	assert.True(t, changed)
	assert.True(t, strings.HasSuffix(got, ".txt"), "got=%q", got)
	assert.LessOrEqual(t, len([]rune(got)), MaxComponentNameLength)
}

func TestSanitizeComponent_PathologicalLongExtension(t *testing.T) {
	// The extension itself already exceeds the cap — truncateComponent must
	// fall back to a flat truncation rather than looping or panicking.
	in := "a." + strings.Repeat("x", 300)
	got, changed := SanitizeComponent(in)
	assert.True(t, changed)
	assert.LessOrEqual(t, len([]rune(got)), MaxComponentNameLength)
	assert.NoError(t, ValidateComponent(got))
}

func TestSanitizeComponent_NeverReturnsEmpty(t *testing.T) {
	inputs := []string{"", ".", "..", "...", "....", "/", "\\", "///", "\\\\\\", ". . .", "   "}
	for _, in := range inputs {
		got, _ := SanitizeComponent(in)
		assert.NotEmpty(t, got, "input=%q", in)
		assert.NoError(t, ValidateComponent(got), "input=%q got=%q", in, got)
	}
}
