// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package pathsafe

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the ADR-067 Stage 0 rule-set VALUE (rules.go and its two
// GOOS-selected companions). They cover the value itself — not the
// delegation from the package-level functions, and not pkg/library's
// create-path enforcement, both of which are separate work.
//
// Every test below names, in its own comment, the mutation it dies on.
// The whole point of the value-not-build-tag mechanism is that both
// verdicts are reachable from one Linux runner; a test here that could not
// distinguish the two sets would be evidence the mechanism failed.

// allSets is the table that makes "unconditional" mean something. Any
// assertion that must hold on every platform iterates it, so a rule that
// quietly becomes platform-conditional is caught on a Linux runner rather
// than in a Windows deployment nobody's CI executes.
var allSets = []RuleSet{POSIXRules, WindowsRules}

// TestRuleSet_ControlCharsRejectedUnderEverySet is the FR-0002a guard.
// Before Stage 0, control characters and NTFS-illegal characters were one
// fused predicate (firstIllegalRune); the obvious way to relax the
// characters is to relax the predicate, which relaxes control characters
// with it.
//
// Dies on: moving the FirstControlRune call out of
// ValidateAddressingSafety and into RuleSet.ValidateNameShape behind the
// IllegalRunes check (i.e. making control characters a Windows-only rule)
// — every POSIXRules row then returns nil.
func TestRuleSet_ControlCharsRejectedUnderEverySet(t *testing.T) {
	cases := map[string]string{
		"NUL":  "bad\x00name.txt",
		"CR":   "bad\rname.txt",
		"LF":   "bad\nname.txt",
		"tab":  "bad\tname.txt",
		"0x01": "bad\x01name.txt",
		"0x1F": "bad\x1fname.txt",
	}
	for _, set := range allSets {
		for label, in := range cases {
			t.Run(set.Name+"/"+label, func(t *testing.T) {
				err := set.ValidateComponent(in)
				require.Error(t, err, "input=%q must be refused under %s", in, set.Name)
				assert.ErrorIs(t, err, ErrIllegalChar)
			})
			t.Run(set.Name+"/"+label+"/shape-rule-does-not-claim-it", func(t *testing.T) {
				// The shape half must NOT be what caught it, on either
				// set — otherwise the rejection is only as durable as the
				// character list it hitched a ride on.
				_, isShape := set.FirstIllegalRune(in)
				assert.False(t, isShape,
					"control characters must be addressing safety, not a shape rule (%s)", set.Name)
			})
		}
	}
}

// TestRuleSet_DotAndDotDotRejectedUnderEverySet is the FR-0002b guard.
// Before Stage 0, ValidateComponent("..") failed ONLY through
// hasTrailingDotOrSpace — a Windows-shape rule. Switching that rule off
// for POSIX with no replacement would have let ".." through here.
//
// Dies on: deleting the `name == "." || name == ".."` branch from
// ValidateAddressingSafety — the POSIX rows go green because
// TrailingDotOrSpace is false there. (The Windows rows survive that
// mutation, which is exactly why the POSIX rows have to exist.)
func TestRuleSet_DotAndDotDotRejectedUnderEverySet(t *testing.T) {
	for _, set := range allSets {
		for _, in := range []string{".", ".."} {
			t.Run(set.Name+"/"+in, func(t *testing.T) {
				err := set.ValidateComponent(in)
				require.Error(t, err, "%q must be refused under %s", in, set.Name)
				assert.ErrorIs(t, err, ErrEmptyName)
			})
		}
	}
	// And the guarantee must not be an accident of the trailing-dot rule
	// even on Windows: with shape rules alone, ".." is a shape violation
	// there and nothing at all on POSIX.
	assert.NoError(t, POSIXRules.ValidateNameShape(".."),
		"precondition: POSIX shape rules must be silent about \"..\" — "+
			"if this ever fails, the test above stopped proving anything")
}

// TestRuleSet_PosixAcceptsOperatorNames is US-0 itself: the operator's own
// files, named years before Omnipus existed, open on their own machine.
// Each name is one of the three classes measured as unreachable on the
// reference vault, plus the shape rules that produce them.
//
// Dies on: setting any Windows-shape field true in POSIXRules (e.g.
// IllegalRunes: illegalRunes) — the matching POSIX row flips to an error.
// Also dies on clearing the same field in WindowsRules, which is what
// makes it a two-sided assertion rather than a one-sided relaxation.
func TestRuleSet_PosixAcceptsOperatorNames(t *testing.T) {
	cases := []struct {
		label      string
		in         string
		windowsErr error
	}{
		{"colon in a meeting note", "Meeting: 2026-01-01.md", ErrIllegalChar},
		{"question mark", "Why?.md", ErrIllegalChar},
		{"angle brackets", "a<b>c.md", ErrIllegalChar},
		{"double quote", `He said "hi".md`, ErrIllegalChar},
		{"pipe", "a|b.md", ErrIllegalChar},
		{"asterisk", "TODO *.md", ErrIllegalChar},
		{"trailing dot", "report.", ErrTrailingDotOrSpace},
		{"trailing space", "report ", ErrTrailingDotOrSpace},
		{"reserved device stem", "nul.txt", ErrReservedName},
		{"106 runes, the vault's longest note", strings.Repeat("a", 106) + ".md", ErrNameTooLong},
	}
	for _, tc := range cases {
		t.Run("posix/"+tc.label, func(t *testing.T) {
			assert.NoError(t, POSIXRules.ValidateComponent(tc.in),
				"the operator named this file; Omnipus is only reading it")
		})
		t.Run("windows/"+tc.label, func(t *testing.T) {
			err := WindowsRules.ValidateComponent(tc.in)
			require.Error(t, err, "input=%q", tc.in)
			assert.ErrorIs(t, err, tc.windowsErr)
		})
	}
}

// TestRuleSet_LengthUnitsAreNotInterchangeable is FR-0004. The two cases
// are chosen so that each one is legal under the OTHER set's rule: a
// 106-rune Latin name is 106 bytes (fine on POSIX, over on Windows), and a
// 93-rune CJK name is 273 bytes (fine on Windows, over on POSIX). A test
// using only ASCII could not tell a correct byte rule from a rune rule, or
// from no rule at all.
//
// Dies on: swapping MaxComponentRunes and MaxComponentBytes between the
// two sets; on setting either to 0; and on measuring the byte cap with
// utf8.RuneCountInString instead of len (the CJK row goes green).
func TestRuleSet_LengthUnitsAreNotInterchangeable(t *testing.T) {
	latin := strings.Repeat("a", 106) + ".md" // 109 runes, 109 bytes
	cjk := strings.Repeat("测", 90) + ".md"    // 93 runes, 273 bytes

	require.Equal(t, 273, len(cjk), "fixture drift: the CJK name must be 273 bytes")
	require.Equal(t, 93, len([]rune(cjk)), "fixture drift: the CJK name must be 93 runes")

	t.Run("posix measures bytes", func(t *testing.T) {
		assert.NoError(t, POSIXRules.ValidateComponent(latin),
			"109 bytes is inside NAME_MAX; a POSIX filesystem creates this happily")
		err := POSIXRules.ValidateComponent(cjk)
		require.Error(t, err, "273 bytes is over NAME_MAX and cannot be created")
		assert.ErrorIs(t, err, ErrNameTooLong)
		assert.Contains(t, err.Error(), "bytes", "a byte-limit refusal must say bytes")
	})

	t.Run("windows measures runes", func(t *testing.T) {
		assert.NoError(t, WindowsRules.ValidateComponent(cjk),
			"93 runes is inside the MAX_PATH budget")
		err := WindowsRules.ValidateComponent(latin)
		require.Error(t, err, "109 runes is over the 100-rune budget")
		assert.ErrorIs(t, err, ErrNameTooLong)
		assert.Contains(t, err.Error(), "characters", "a rune-limit refusal must say characters")
	})

	t.Run("whole-path budget is windows only", func(t *testing.T) {
		long := strings.Repeat("dir/", 60) + "file.txt" // 248 runes
		require.Greater(t, len([]rune(long)), MaxRelPathLength)
		assert.NoError(t, POSIXRules.ValidateRelPathLength(long))
		assert.ErrorIs(t, WindowsRules.ValidateRelPathLength(long), ErrNameTooLong)
	})
}

// TestRuleSet_SanitizeRulesIsAlwaysStrict is FR-0001d. The sanitizing path
// serves remote, attacker-chosen attachment names from four chat channels
// and must not move on any platform. It is bound to SanitizeRules, not to
// ActiveRules, and the difference is invisible on a Windows runner — which
// is where it would be discovered if this test did not exist.
//
// Dies on: `var SanitizeRules = POSIXRules`, and on `= activeRules`
// (which is POSIXRules on this runner). Both leave every other test in
// this package green.
func TestRuleSet_SanitizeRulesIsAlwaysStrict(t *testing.T) {
	assert.Equal(t, WindowsRules, SanitizeRules,
		"the sanitizer's rule set is the strict one on every platform")

	// The remote-attachment corpus: what a hostile sender can put in a
	// filename field. Every one must be rewritten, on this runner.
	corpus := []string{
		`report<>:"|?*.txt`,
		"nul.txt",
		"trailing. ",
		"a\x00b\nc.txt",
	}
	for _, raw := range corpus {
		got := ReplaceControlRunes(SanitizeRules.ReplaceIllegalRunes(raw))
		assert.NotContains(t, got, "\x00")
		for _, r := range illegalRunes {
			assert.NotContains(t, got, string(r),
				"illegal rune %q survived the sanitizing pass for %q", r, raw)
		}
	}
}

// TestRuleSet_SanitizingSplitReproducesTheFusedPass proves the FR-0002a
// split is behaviour-preserving where it must be. The pre-Stage-0
// sanitizer replaced control runes and NTFS-illegal runes in ONE pass;
// splitting it into two must produce byte-identical output, or Stage 0
// silently renames every attachment that ever arrives.
//
// The expectation is computed from the specification of the fused pass
// (map r to '_' if r <= 0x1F or r is in illegalRunes), not read off the
// implementation.
//
// Dies on: making ReplaceControlRunes skip any control rune, and on
// having either half DELETE rather than replace (the lengths diverge).
func TestRuleSet_SanitizingSplitReproducesTheFusedPass(t *testing.T) {
	inputs := []string{
		`a<b>c:d"e|f?g*h`,
		"tab\there",
		"\x00\x01\x1f",
		"完全に無害な名前.txt",
		"",
		"already_safe.txt",
	}
	fused := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r <= 0x1F || strings.ContainsRune(`<>:"|?*`, r) {
				b.WriteByte('_')
				continue
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	for _, in := range inputs {
		// Both orderings, because the split is only safe if the two halves
		// commute — neither may produce a character the other rejects.
		assert.Equal(t, fused(in), ReplaceControlRunes(SanitizeRules.ReplaceIllegalRunes(in)),
			"control-then-shape composition must equal the fused pass for %q", in)
		assert.Equal(t, fused(in), SanitizeRules.ReplaceIllegalRunes(ReplaceControlRunes(in)),
			"shape-then-control composition must equal the fused pass for %q", in)
	}
}

// TestRuleSet_ActiveSetMatchesGOOS is the one fact that genuinely needs a
// Windows machine to confirm, which is why it is written to assert
// something on every platform rather than skipping away from home. On the
// Linux and macOS runners it proves the relaxation is actually in force;
// on the narrow windows-latest job it proves the strict set is.
//
// Dies on: swapping the two one-line platform files, and on either file
// assigning a locally-constructed RuleSet literal that has drifted from
// the named one.
func TestRuleSet_ActiveSetMatchesGOOS(t *testing.T) {
	if runtime.GOOS == "windows" {
		assert.Equal(t, WindowsRules, ActiveRules())
		return
	}
	assert.Equal(t, POSIXRules, ActiveRules())
	assert.Empty(t, ActiveRules().IllegalRunes,
		"a non-Windows build must not apply NTFS character rules to the operator's own disk")
}

// TestRuleSet_SetsDifferOnlyInNameShape pins the whole value. Stage 0's
// safety argument is "the two sets differ only in name shape"; if a
// non-shape difference is ever added, that argument stops holding and this
// test is where it should be re-derived rather than discovered.
//
// Dies on: any field edit in either set.
func TestRuleSet_SetsDifferOnlyInNameShape(t *testing.T) {
	assert.Equal(t, RuleSet{
		Name:                "posix",
		IllegalRunes:        "",
		ReservedDeviceNames: false,
		TrailingDotOrSpace:  false,
		MaxComponentRunes:   0,
		MaxComponentBytes:   255,
		MaxRelPathRunes:     0,
	}, POSIXRules)

	assert.Equal(t, RuleSet{
		Name:                "windows",
		IllegalRunes:        `<>:"|?*`,
		ReservedDeviceNames: true,
		TrailingDotOrSpace:  true,
		MaxComponentRunes:   100,
		MaxComponentBytes:   0,
		MaxRelPathRunes:     200,
	}, WindowsRules)
}

// TestRuleSet_WindowsSetReproducesPreStage0Verdicts is the AS-3 / test-0e
// half that belongs to the value: for workspace storage on a Windows
// build, nothing moved. Each row is lifted from the pre-Stage-0 table in
// pathsafe_test.go, with the same expected sentinel.
//
// Dies on: reordering the checks in ValidateNameShape so a name that used
// to fail as ErrIllegalChar now fails as ErrTrailingDotOrSpace, and on
// dropping any single shape rule from WindowsRules.
func TestRuleSet_WindowsSetReproducesPreStage0Verdicts(t *testing.T) {
	cases := []struct {
		in      string
		wantErr error
	}{
		{"report.txt", nil},
		{"Copy of My Deck.pptx", nil},
		{"My Report (final) — résumé 测试 🎉.txt", nil},
		{".gitignore", nil},
		{"archive.tar.gz", nil},
		{" leading.txt", nil},
		{"报告.txt", nil},
		{"COM0", nil},
		{"CONsole", nil},
		{"PRNfoo.txt", nil},
		{"", ErrEmptyName},
		{"CoN", ErrReservedName},
		{"com1.log", ErrReservedName},
		{"con.tar.gz", ErrReservedName},
		{"bad:name.txt", ErrIllegalChar},
		{"bad\x00name.txt", ErrIllegalChar},
		{"report...", ErrTrailingDotOrSpace},
		{"report.txt.", ErrTrailingDotOrSpace},
		{strings.Repeat("a", MaxComponentNameLength), nil},
		{strings.Repeat("a", MaxComponentNameLength+1), ErrNameTooLong},
		{strings.Repeat("测", MaxComponentNameLength), nil},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
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
