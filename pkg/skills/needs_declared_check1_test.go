package skills

// CHECK1 CK6 (skills spec r15 FR-003, §5.7): metadata.omnipus-requires
// entries are checked by PATH lookup when they are plain program names, and
// every other declaration is shown "declared, not checked" — never MISSING.
//
// Failability: against the code before commit 380dec186 every whitespace
// separated token was looked up, so `pypdf>=4` rendered as
// `pypdf>=4 — MISSING` and a quoted span was split into broken tokens; the
// assertions on the declared rows fail there.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadHeader_NeedsMarksNonProgramDeclarationsNotChecked(t *testing.T) {
	root := t.TempDir()
	dir := writeSkillFixture(t, root, "needs-mix")
	md := "---\nname: needs-mix\ndescription: mixed requirements\nmetadata:\n" +
		"  omnipus-requires: 'sh \"pypdf >= 4\" pkg>=2 ck6-definitely-missing'\n---\n\nBODY\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))

	header := loadHeader(t, root, "needs-mix", LoadActivationOptions{EditAllowed: true})

	assert.Contains(t, header, "sh — present (", "a plain program name is PATH-checked")
	assert.Contains(t, header, "ck6-definitely-missing — MISSING", "an absent plain program is MISSING")
	assert.Contains(t, header, `"pypdf >= 4" — declared, not checked`,
		"a quoted declaration is one entry, shown declared, not checked")
	assert.Contains(t, header, `"pkg>=2" — declared, not checked`,
		"a version-constraint declaration is declared, not checked")
	assert.NotContains(t, header, "pkg>=2 — MISSING", "a non-program declaration is never reported MISSING")
	assert.NotContains(t, header, "pypdf — MISSING", "a quoted declaration is never split and looked up")
	assert.NotContains(t, header, ">= — MISSING")
}

func TestLoadHeader_NeedsUsesTheGivenPATH(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("executable bit fixture is POSIX")
	}
	root := t.TempDir()
	dir := writeSkillFixture(t, root, "needs-path")
	md := "---\nname: needs-path\ndescription: path requirement\nmetadata:\n  omnipus-requires: ck18-only-here\n---\n\nBODY\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "ck18-only-here"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	onPath := loadHeader(t, root, "needs-path", LoadActivationOptions{PATHEnv: bin})
	assert.Contains(t, onPath, "ck18-only-here — present ("+filepath.Join(bin, "ck18-only-here")+")",
		"needs is resolved over the PATH it is given (the agent bash PATH), not the process PATH")

	offPath := loadHeader(t, root, "needs-path", LoadActivationOptions{PATHEnv: t.TempDir()})
	assert.Contains(t, offPath, "ck18-only-here — MISSING")
}
