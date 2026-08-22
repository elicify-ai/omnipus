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

// withActiveRules swaps the GOOS-selected rule set for the duration of a
// test and restores it afterwards, so a single Linux or macOS runner can
// exercise BOTH verdicts of an exported function whose signature does not
// take a rule set. No test using it may call t.Parallel: activeRules is
// package state.
//
// This is the whole point of Stage 0's "rule set is a VALUE" mechanism.
// Had the split been a compile-time fork of the rule bodies, everything
// below the exported surface would be untestable anywhere CI runs — no CI
// job runs Go tests on Windows, so half of Stage 0 would ship with zero
// executed assertions while the dashboard stayed green.
func withActiveRules(t *testing.T, rs RuleSet) {
	t.Helper()
	saved := activeRules
	activeRules = rs
	t.Cleanup(func() { activeRules = saved })
}

// --- 0e: the pre-Stage-0 assertions, verbatim, under the Windows set ---

// TestPathsafeRegression_WindowsUnchanged is the ORIGINAL ValidateComponent
// table, unchanged case for case, run against WindowsRules. Traces to
// ADR-067 test 0e / AS-3: Stage 0 relaxes the read path on POSIX, and it
// must not have quietly changed what Windows refuses along the way.
//
// It calls RuleSet.ValidateComponent rather than the package-level
// ValidateComponent so it asserts the Windows verdict on every runner —
// including the Linux and macOS ones that are the only runners CI has.
func TestPathsafeRegression_WindowsUnchanged(t *testing.T) {
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
			err := WindowsRules.ValidateComponent(tc.in)
			if tc.wantErr == nil {
				assert.NoError(t, err, "input=%q", tc.in)
				return
			}
			require.Error(t, err, "input=%q", tc.in)
			assert.ErrorIs(t, err, tc.wantErr, "input=%q", tc.in)
		})
	}
}

// --- FR-0001: the POSIX verdict on the same corpus ---

// TestValidateComponent_POSIXAcceptsNameShapeButNotAddressing states, case
// by case, exactly what the POSIX rule set stops refusing and what it
// still refuses. Every name in the "accepted" half is one an operator can
// legally have on a Linux or macOS disk today and could not previously
// address through Omnipus — including the two real failure shapes measured
// on the reference vault (an illegal character, and a 106-rune basename).
//
// Written from the requirement rather than from the code: FR-0001 says
// name shape stops applying to reads, FR-0002/FR-0002a/FR-0002b say
// addressing safety never does.
func TestValidateComponent_POSIXAcceptsNameShapeButNotAddressing(t *testing.T) {
	accepted := []struct{ name, in string }{
		{"colon — the measured vault failure", "Meeting: 2026-01-01.md"},
		{"question mark", "Why?.md"},
		{"angle brackets", "a<b>c.md"},
		{"pipe", "a|b.md"},
		{"asterisk", "draft*.md"},
		{"double quote", `He said "hi".md`},
		{"reserved device name", "CON"},
		{"reserved device name with extension", "nul.txt"},
		{"trailing dot", "report."},
		{"trailing space", "report "},
		{"106 runes — the measured vault failure", strings.Repeat("a", 106)},
		{"one over the Windows rune cap", strings.Repeat("a", MaxComponentNameLength+1)},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			assert.NoError(t, POSIXRules.ValidateComponent(tc.in), "input=%q", tc.in)
		})
	}

	refused := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"empty", "", ErrEmptyName},
		{"bare dot", ".", ErrEmptyName},
		{"bare dotdot", "..", ErrEmptyName},
		{"NUL", "bad\x00name.txt", ErrIllegalChar},
		{"CR", "bad\rname.txt", ErrIllegalChar},
		{"LF", "bad\nname.txt", ErrIllegalChar},
		// FR-0004: POSIX counts BYTES, so a name inside the 100-rune
		// Windows budget can still be over NAME_MAX. 90 CJK runes is 270
		// bytes and cannot be created on ext4, xfs, btrfs or APFS.
		{"90 CJK runes is 270 bytes, over NAME_MAX", strings.Repeat("测", 90), ErrNameTooLong},
	}
	for _, tc := range refused {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			err := POSIXRules.ValidateComponent(tc.in)
			require.Error(t, err, "input=%q", tc.in)
			assert.ErrorIs(t, err, tc.wantErr, "input=%q", tc.in)
		})
	}
}

// TestValidateComponent_DelegatesToActiveRules pins the delegation itself:
// the exported, 17-dependent-symbol entry point must read the build's rule
// set and nothing else. Without this, both rule sets could be perfectly
// correct while ValidateComponent still hard-coded one of them.
func TestValidateComponent_DelegatesToActiveRules(t *testing.T) {
	const windowsIllegalOnly = "Meeting: notes.md"

	withActiveRules(t, WindowsRules)
	assert.ErrorIs(t, ValidateComponent(windowsIllegalOnly), ErrIllegalChar,
		"under the Windows set the exported entry point must refuse a colon")

	withActiveRules(t, POSIXRules)
	assert.NoError(t, ValidateComponent(windowsIllegalOnly),
		"under the POSIX set the exported entry point must accept the operator's own filename")
}

// --- 0g: control characters, every rule set, no exceptions ---

// TestPathsafe_ControlCharsRejectedEveryPlatform is the guard for FR-0002a.
//
// Before Stage 0 a single predicate tested `r <= 0x1F || strings.
// ContainsRune(illegalRunes, r)`. Relaxing "illegal characters" for POSIX
// by relaxing that one predicate would have dropped NUL, CR and LF
// rejection with it — silently, on every path, with no test failing.
//
// MUTATION THIS DIES ON: move the control-character check inside the
// rule-set gate, i.e. delete the ValidateAddressingSafety call from
// RuleSet.ValidateComponent (rules.go) or add the control runes to
// WindowsRules.IllegalRunes and drop FirstControlRune from the
// unconditional half. Every POSIX subtest below goes red.
func TestPathsafe_ControlCharsRejectedEveryPlatform(t *testing.T) {
	controls := []struct{ name, in string }{
		{"NUL", "bad\x00name.txt"},
		{"CR", "bad\rname.txt"},
		{"LF", "bad\nname.txt"},
		{"CRLF", "bad\r\nname.txt"},
		{"tab", "bad\tname.txt"},
		{"0x01", "bad\x01name.txt"},
		{"0x1F — the top of the C0 range", "bad\x1fname.txt"},
		{"leading NUL", "\x00report.txt"},
		{"trailing NUL", "report.txt\x00"},
		{"control character alone", "\x00"},
	}
	for _, rs := range []RuleSet{POSIXRules, WindowsRules} {
		for _, tc := range controls {
			t.Run(rs.Name+"/"+tc.name, func(t *testing.T) {
				err := rs.ValidateComponent(tc.in)
				require.Error(t, err, "input=%q must be refused under the %s rule set", tc.in, rs.Name)
				assert.ErrorIs(t, err, ErrIllegalChar, "input=%q", tc.in)
			})
		}
	}

	// The zero RuleSet applies no name-shape rule at all. Control-character
	// rejection must survive even that, because it is not a shape rule and
	// is not reachable from any RuleSet field.
	for _, tc := range controls {
		t.Run("zero-rule-set/"+tc.name, func(t *testing.T) {
			assert.ErrorIs(t, RuleSet{}.ValidateComponent(tc.in), ErrIllegalChar, "input=%q", tc.in)
		})
	}

	// Structural half of the same guard: the two classes must stay in two
	// separate places. If a future edit folds the control runes back into
	// the name-shape character set, relaxing that set relaxes them again.
	for _, rs := range []RuleSet{POSIXRules, WindowsRules, SanitizeRules} {
		for r := rune(0); r <= 0x1F; r++ {
			require.NotContains(t, rs.IllegalRunes, string(r),
				"%s.IllegalRunes must not carry control rune %#U — control characters are addressing safety, not name shape", rs.Name, r)
		}
	}
}

// --- 0h: "." and ".." do not depend on the trailing-dot rule ---

// TestPathsafe_DotAndDotDotRejectedWithoutTrailingDotRule is the guard for
// FR-0002b, and it is written to be non-vacuous under BOTH rule sets.
//
// The defect: before Stage 0, ValidateComponent("..") failed only via
// hasTrailingDotOrSpace — a Windows-shape rule. Turn that rule off, as the
// POSIX set does, and both "." and ".." validated clean. ErrEmptyName's
// own doc has always promised to cover "a bare . or ..", and the code did
// not deliver it.
//
// Asserting the SENTINEL, not merely "some error", is what keeps this
// honest on Windows: under WindowsRules the trailing-dot rule would also
// refuse "..", so an errors-out assertion would pass with the independent
// check deleted. ErrEmptyName can only come from the independent check —
// the trailing-dot rule raises ErrTrailingDotOrSpace.
//
// MUTATION THIS DIES ON: delete the `name == "." || name == ".."` branch
// from ValidateAddressingSafety (rules.go). Every subtest below goes red,
// including the WindowsRules ones, which then report
// ErrTrailingDotOrSpace instead.
func TestPathsafe_DotAndDotDotRejectedWithoutTrailingDotRule(t *testing.T) {
	// A rule set with the trailing-dot rule explicitly off, but everything
	// else Windows. This is the exact configuration in which the old code
	// let ".." through, expressed as a permanent test rather than a
	// one-off hand mutation.
	windowsMinusTrailingDot := WindowsRules
	windowsMinusTrailingDot.TrailingDotOrSpace = false
	windowsMinusTrailingDot.Name = "windows-minus-trailing-dot"
	require.False(t, windowsMinusTrailingDot.HasTrailingDotOrSpace(".."),
		"precondition: the trailing-dot rule must actually be off in this set, or the test proves nothing")

	zeroSet := RuleSet{Name: "zero-rule-set"}

	sets := []RuleSet{POSIXRules, WindowsRules, windowsMinusTrailingDot, zeroSet}
	for _, rs := range sets {
		for _, in := range []string{".", ".."} {
			t.Run(rs.Name+"/"+in, func(t *testing.T) {
				err := rs.ValidateComponent(in)
				require.Error(t, err, "%q must be refused under every rule set", in)
				assert.ErrorIs(t, err, ErrEmptyName,
					"%q must be refused for BEING a directory reference, not as a side effect of the trailing-dot rule; got %v", in, err)
				assert.NotErrorIs(t, err, ErrTrailingDotOrSpace,
					"%q must not be reported as a trailing-dot violation — that is the reason that disappears when the rule is off", in)
			})
		}
	}

	// The exported entry point, under whichever set this build selected,
	// must reach the same verdict — the 17 dependents call this one.
	for _, rs := range []RuleSet{POSIXRules, WindowsRules} {
		withActiveRules(t, rs)
		assert.ErrorIs(t, ValidateComponent("."), ErrEmptyName)
		assert.ErrorIs(t, ValidateComponent(".."), ErrEmptyName)
	}

	// Names that merely CONTAIN dots, or start with them, are ordinary
	// names and must not be caught by the new branch — a rejection that
	// over-reaches here would break every dotfile in the Library.
	for _, rs := range []RuleSet{POSIXRules, WindowsRules} {
		for _, in := range []string{".gitignore", "..hidden", "a.b", "...", "a..b"} {
			// "..." ends in a dot, so WindowsRules refuses it — but for the
			// trailing-dot reason, never for the addressing-safety one.
			err := rs.ValidateComponent(in)
			assert.NotErrorIs(t, err, ErrEmptyName,
				"%q is a name, not a directory reference (%s rule set)", in, rs.Name)
		}
	}
}

// --- 0b: traversal components stay refused ---

// TestPathsafe_TraversalStillRefused is the guard that must not regress
// (AS-6, FR-0002). It is deliberately narrow to this package's share of
// the job — a single component — because root confinement and symlink
// escape belong to pkg/library. What it pins here is that no rule set,
// including one with every shape rule switched off, can make a traversal
// component look like a valid name.
func TestPathsafe_TraversalStillRefused(t *testing.T) {
	for _, rs := range []RuleSet{POSIXRules, WindowsRules, {}} {
		for _, in := range []string{"..", "."} {
			assert.Error(t, rs.ValidateComponent(in), "input=%q", in)
		}
	}
	// The sanitizing path cannot reject, so it must neutralize instead:
	// a traversal input may never survive as a traversal.
	for _, in := range []string{"..", ".", "../../etc/passwd", `..\..\windows\system32`, "....//"} {
		got, _ := SanitizeComponent(in)
		assert.NotEqual(t, "..", got, "input=%q", in)
		assert.NotEqual(t, ".", got, "input=%q", in)
		assert.NotContains(t, got, "/", "input=%q", in)
		assert.NotContains(t, got, `\`, "input=%q", in)
		assert.NoError(t, WindowsRules.ValidateComponent(got), "input=%q got=%q", in, got)
	}
}

// --- ValidateRelPathLength ---

func TestValidateRelPathLength(t *testing.T) {
	// The whole-path budget descends from Windows' MAX_PATH, so it is a
	// name-shape rule: present under WindowsRules, absent under POSIXRules
	// (FR-0004 — a path already on a POSIX disk is inside that disk's own
	// limits by construction).
	assert.NoError(t, WindowsRules.ValidateRelPathLength(strings.Repeat("a", MaxRelPathLength)))
	err := WindowsRules.ValidateRelPathLength(strings.Repeat("a", MaxRelPathLength+1))
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
	assert.ErrorIs(t, WindowsRules.ValidateRelPathLength(deep), ErrNameTooLong)

	// A 240-rune mounted path is addressable on POSIX.
	assert.NoError(t, POSIXRules.ValidateRelPathLength(strings.Repeat("a", 240)))
	assert.NoError(t, POSIXRules.ValidateRelPathLength(deep))

	// And the exported wrapper follows the build's set, both ways.
	withActiveRules(t, WindowsRules)
	assert.ErrorIs(t, ValidateRelPathLength(deep), ErrNameTooLong)
	withActiveRules(t, POSIXRules)
	assert.NoError(t, ValidateRelPathLength(deep))
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
			// Whatever SanitizeComponent returns must itself pass the
			// STRICTEST rule set — the whole point of this function is that
			// its output is safe everywhere, not merely safe here. Checking
			// against the build's active set would make this assertion
			// weaker on POSIX than it was before Stage 0.
			assert.NoError(t, WindowsRules.ValidateComponent(got),
				"sanitized output %q must itself be valid under the strictest rule set", got)
		})
	}
}

// TestSanitizeComponent_UnchangedOnEveryBuild is the guard for FR-0001d
// and NB-18 (ADR-067 test 0l).
//
// SanitizeComponent is the ENTIRE defence at pkg/utils/media.go's
// SanitizeFilename, which handles the filename an attachment carries from
// Discord, Telegram, Feishu or QQ — chosen by whoever sent the message.
// That path cannot reject, only rewrite, so nothing about it may relax on
// any platform. Stage 0 relaxes the operator's READ path and nothing else.
//
// The likely implementation mistake is precisely what this catches: the
// validating and sanitizing paths shared one illegal-character predicate,
// so making that predicate depend on the build relaxes the remote-ingest
// path as a side effect.
//
// Expected values are derived from the requirement — the pre-Stage-0
// rewrite, which FR-0001d says must be preserved byte for byte — not read
// off the current implementation.
//
// MUTATION THIS DIES ON: change replaceIllegalRunes (or
// defuseReservedName) to consult ActiveRules()/activeRules instead of
// SanitizeRules. Under the POSIX set the colon, question mark and reserved
// names stop being rewritten and the "identical across rule sets" and
// golden-value assertions both go red.
func TestSanitizeComponent_UnchangedOnEveryBuild(t *testing.T) {
	// A remote-attachment corpus: the shapes an attacker actually controls
	// on an inbound chat attachment, plus the two vault-realistic names
	// Stage 0 newly ADDRESSES on the read path — those are the ones a
	// build-dependent sanitizer would silently stop rewriting.
	corpus := []struct{ in, want string }{
		{`bad<>:"|?*name.txt`, "bad_______name.txt"},
		{"Meeting: notes.md", "Meeting_ notes.md"},
		{"Why?.md", "Why_.md"},
		{"a<b>c.md", "a_b_c.md"},
		{`He said "hi".md`, "He said _hi_.md"},
		{"draft*.md", "draft_.md"},
		{"a|b.md", "a_b.md"},
		{"bad\x00name.txt", "bad_name.txt"},
		{"report\r\n.txt", "report__.txt"},
		{"a\x1fb", "a_b"},
		// A name whose only content is a control character survives as the
		// replacement character, NOT as "file": lastPathElement sees a
		// non-empty, non-dot segment and keeps it, and "_" is a perfectly
		// legal filename. Derived from the pre-Stage-0 pipeline, which
		// FR-0001d freezes.
		{"\x00", "_"},
		{"report.", "report"},
		{"report ", "report"},
		{"con", "con_"},
		{"CON.txt", "CON_.txt"},
		{"nul.tar.gz", "nul_.tar.gz"},
		{"console.txt", "console.txt"},
		{"../../etc/passwd", "passwd"},
		{`..\..\windows\system32`, "system32"},
		{"....//", "file"},
		{"..", "file"},
		{".", "file"},
		{"", "file"},
		{"///", "file"},
		{"My Report (final) — résumé 测试 🎉.txt", "My Report (final) — résumé 测试 🎉.txt"},
		{strings.Repeat("a", 300) + ".txt", strings.Repeat("a", MaxComponentNameLength-4) + ".txt"},
		// The rune budget is preserved on the sanitizing path on EVERY
		// build — it is 96 CJK runes plus ".txt", not 96 bytes.
		{strings.Repeat("测", 120) + ".txt", strings.Repeat("测", MaxComponentNameLength-4) + ".txt"},
	}

	perSet := map[string][]string{}
	for _, rs := range []RuleSet{POSIXRules, WindowsRules, {}} {
		label := rs.Name
		if label == "" {
			label = "zero-rule-set"
		}
		withActiveRules(t, rs)
		out := make([]string, 0, len(corpus))
		for _, tc := range corpus {
			got, changed := SanitizeComponent(tc.in)
			assert.Equal(t, tc.want, got,
				"active=%s: SanitizeComponent(%q) must be byte-identical on every build", label, tc.in)
			assert.Equal(t, tc.want != tc.in, changed,
				"active=%s: changed flag for %q", label, tc.in)
			out = append(out, got)
		}
		perSet[label] = out
	}

	// Cross-check, so the assertion survives even if someone "fixes" the
	// golden values: whatever the outputs are, all three runs must agree.
	baseline := perSet["windows"]
	require.NotEmpty(t, baseline)
	for label, out := range perSet {
		assert.Equal(t, baseline, out,
			"SanitizeComponent output under the %s rule set diverged from the windows one — the sanitizing path must not read ActiveRules (FR-0001d)", label)
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
	assert.NoError(t, WindowsRules.ValidateComponent(got))
}

func TestSanitizeComponent_NeverReturnsEmpty(t *testing.T) {
	inputs := []string{"", ".", "..", "...", "....", "/", "\\", "///", "\\\\\\", ". . .", "   "}
	for _, in := range inputs {
		got, _ := SanitizeComponent(in)
		assert.NotEmpty(t, got, "input=%q", in)
		assert.NoError(t, WindowsRules.ValidateComponent(got), "input=%q got=%q", in, got)
	}
}
