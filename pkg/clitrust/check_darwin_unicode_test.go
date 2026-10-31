//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package clitrust_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestCheckProgramPath_UnicodeEquivalentSpellingOfForbiddenRoot is round 6,
// S8 (security round 5; spec §3.2). macOS volumes fold canonically equivalent
// Unicode spellings of a name onto one folder, as they fold letter case: a
// program inside a forbidden root must be refused under an equivalent
// spelling of that root exactly as at the configured spelling — for the
// final file, for a symlink to it, and when a containing folder carries the
// equivalent spelling (security round 5 S8: forbidden-root membership must
// agree with filesystem identity, including equivalent Unicode names). The
// volume property is measured with os.SameFile and stated in the log; on a
// filesystem that keeps the spellings apart the equivalence cases cannot
// exist here, and the test says so explicitly (never a silent pass). The
// planted program is never launched.
func TestCheckProgramPath_UnicodeEquivalentSpellingOfForbiddenRoot(t *testing.T) {
	check, rootsFn := loadT11CLITrust(t)
	// The spellings are written as explicit Go escapes so the byte forms
	// cannot be silently normalized by an editor or tool round-trip: the
	// composed and decomposed forms must stay byte-distinct in this source.
	base := t.TempDir()
	root := filepath.Join(base, "Caf\u00e9")            // composed NFC: 43 61 66 c3 a9
	decomposed := filepath.Join(base, "Cafe\u0301")     // canonically equivalent NFD: 43 61 66 65 cc 81
	decomposedCaps := filepath.Join(base, "CAFE\u0301") // decomposed NFD with case varied: 43 41 46 45 cc 81
	require.NotEqual(t, root, decomposed, "fixture: the composed and decomposed spellings are byte-distinct strings")

	// Measured volume property (never assumed): the decomposed spelling must
	// reach the composed folder, or the equivalence cases cannot exist here.
	probeNFC := filepath.Join(base, "Probe\u00e9")
	probeNFD := filepath.Join(base, "Probee\u0301")
	require.NoError(t, os.MkdirAll(probeNFC, 0o700))
	p1, err1 := os.Lstat(probeNFC)
	p2, err2 := os.Lstat(probeNFD)
	folds := err1 == nil && err2 == nil && os.SameFile(p1, p2)
	t.Logf("volume property: os.SameFile(composed Probe\u00e9, decomposed Probee\u0301)=%v", folds)
	if !folds {
		t.Skip("measured Unicode-normalization-sensitive volume (os.SameFile is false): the composed and decomposed spellings are different files on this volume, so the equivalence refusal cannot be exercised here")
	}

	prog := t11WriteExe(t, root, "fakecli", 0o755)
	info, err := os.Lstat(prog)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular(), "fixture: the planted program is a regular file and is never launched")

	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{{ID: "t11unicode", Home: root}}
	roots, err := rootsFn(t.TempDir(), cfg)
	require.NoError(t, err)
	require.Truef(t, t11PathListed(roots, root), "the composed root is a forbidden root; got %v", roots)

	// Fixture identity, measured: the two spellings name one folder, and the
	// decomposed program spelling names the planted file.
	r1, err := os.Lstat(root)
	require.NoError(t, err)
	r2, err := os.Lstat(decomposed)
	require.NoError(t, err)
	require.True(t, os.SameFile(r1, r2), "fixture: the decomposed root spelling names the configured folder")
	f1, err := os.Lstat(prog)
	require.NoError(t, err)
	f2, err := os.Lstat(filepath.Join(decomposed, "fakecli"))
	require.NoError(t, err)
	require.True(t, os.SameFile(f1, f2), "fixture: the decomposed program spelling names the planted file")

	t.Run("configured composed spelling is refused", func(t *testing.T) {
		resolved, err := check(prog, roots)
		require.Error(t, err, "spec §3.2: the configured forbidden-root spelling is refused; resolved=%q", resolved)
		require.Empty(t, resolved, "a refusal returns no file to launch")
	})

	t.Run("decomposed spelling of the final file is refused", func(t *testing.T) {
		resolved, err := check(filepath.Join(decomposed, "fakecli"), roots)
		require.Error(t, err, "S8: an equivalent Unicode spelling of the forbidden root must be refused like the configured one (os.SameFile-verified same folder); resolved=%q", resolved)
		require.Empty(t, resolved, "a refusal returns no file to launch")
		require.Contains(t, strings.ToLower(err.Error()), "inside", "the refusal names the forbidden root")
	})

	t.Run("decomposed case-varied spelling of the final file is refused", func(t *testing.T) {
		resolved, err := check(filepath.Join(decomposedCaps, "fakecli"), roots)
		require.Error(t, err, "S8: an equivalent decomposed-and-case-varied spelling must be refused like the configured one; resolved=%q", resolved)
		require.Empty(t, resolved, "a refusal returns no file to launch")
	})

	t.Run("symlink spelled through the decomposed root is refused", func(t *testing.T) {
		link := filepath.Join(base, "t11-unicode-link")
		require.NoError(t, os.Symlink(filepath.Join(decomposed, "fakecli"), link))
		resolved, err := check(link, roots)
		require.Error(t, err, "S8: a link whose target lies inside the forbidden root under an equivalent spelling must be refused; resolved=%q", resolved)
		require.Empty(t, resolved, "a refusal returns no file to launch")
	})

	t.Run("containing folder spelled decomposed is refused", func(t *testing.T) {
		inner := t11WriteExe(t, filepath.Join(root, "bin"), "fakecli", 0o755)
		ii, err := os.Lstat(inner)
		require.NoError(t, err)
		require.True(t, ii.Mode().IsRegular(), "fixture: the contained program is a regular file and is never launched")
		resolved, err := check(filepath.Join(decomposed, "bin", "fakecli"), roots)
		require.Error(t, err, "S8: a path whose containing folder is an equivalent spelling inside the root must be refused; resolved=%q", resolved)
		require.Empty(t, resolved, "a refusal returns no file to launch")
	})

	t.Run("genuinely different folder outside the roots is allowed", func(t *testing.T) {
		other := filepath.Join(base, "Cafe") // no accent: byte-distinct from the root name
		otherProg := t11WriteExe(t, other, "fakecli", 0o755)
		o1, err := os.Lstat(root)
		require.NoError(t, err)
		o2, err := os.Lstat(other)
		require.NoError(t, err)
		require.False(t, os.SameFile(o1, o2), "fixture: the positive-control folder is a genuinely different folder")
		final, err := filepath.EvalSymlinks(otherProg)
		require.NoError(t, err)
		resolved, err := check(otherProg, roots)
		require.NoError(t, err, "spec §3.2: a program outside every forbidden root is allowed")
		require.Equal(t, final, resolved)
	})
}
