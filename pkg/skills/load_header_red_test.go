package skills

// RED tests — landing/skills (T9): the §5.7 `Skill` load result (the
// activation header). Scenarios S-1.1 (full registry header), S-1.2
// (built-in header), S-1.3 (200-file cap), S-1.4 (allowed-tools spellings
// regression pair), S-1.6 (outside change).
// Traces to: skills-spec-r13.md §5.7, §16 US-1, §17.1 rows 9 and 18.
//
// RED expectation: LoadForActivation is a scaffold that refuses — every
// header assertion below fails on the baseline. S-1.4 is a named regression
// pair (SC-001): it asserts the unchanged frontmatter parsing through the
// LIVE loader, so it must pass on the baseline and is never cited as RED.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureWithFiles writes a valid skill folder plus helper files and
// returns its directory.
func fixtureWithFiles(t *testing.T, root, slug string, helpers ...string) string {
	t.Helper()
	dir := writeSkillFixture(t, root, slug)
	for _, h := range helpers {
		full := filepath.Join(dir, h)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("helper: "+h+"\n"), 0o644))
	}
	return dir
}

// loadHeader calls LoadForActivation with the process PATH.
func loadHeader(t *testing.T, skillsRoot, slug string, opts LoadActivationOptions) string {
	t.Helper()
	if opts.PATHEnv == "" {
		opts.PATHEnv = os.Getenv("PATH")
	}
	out, err := LoadForActivation(skillsRoot, slug, opts)
	require.NoError(t, err, "RED: the §5.7 activation reader is not implemented")
	return out
}

// TestLoadHeader_RegistrySkillFullFormat (S-1.1).
// BDD: Skill(pdf-tools) for a granted user skill with SKILL.md,
// reference/forms.md, scripts/extract.py and metadata.omnipus-requires
// "sh definitely-missing-xyz" — the header carries the absolute real folder,
// 3 files, revision, version, the needs line with one present and one
// MISSING entry, and edit per policy; dot-files are absent from the list.
// Traces to: skills-spec-r13.md §5.7, S-1.1.
func TestLoadHeader_RegistrySkillFullFormat(t *testing.T) {
	root := t.TempDir()
	dir := fixtureWithFiles(t, root, "pdf-tools", "reference/forms.md", "scripts/extract.py")
	// omnipus-requires over the frontmatter via a second write (validSkillMD
	// has no metadata block): rewrite SKILL.md with a metadata map.
	md := "---\nname: pdf-tools\ndescription: read PDFs\nmetadata:\n  omnipus-requires: \"sh definitely-missing-xyz\"\n---\n\nPDF BODY\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
	// A nested dot-entry must never appear in the files list (display-hidden,
	// §6.1 item 7).
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".hidden", "x.md"), []byte("x"), 0o644))

	// A committed change gives the header its version and last-change lines.
	commitWrite(t, root, "pdf-tools", "SKILL.md", md, readLiveRevision(t, dir))

	header := loadHeader(t, root, "pdf-tools", LoadActivationOptions{EditAllowed: true})

	assert.Contains(t, header, `<skill slug="pdf-tools" shelf="registry" revision="`,
		"header opens with slug, shelf and a content revision")
	assert.Contains(t, header, `version="`, "the newest version id is named")
	assert.Contains(t, header, "folder: "+dir, "folder is the absolute real path")
	assert.Contains(t, header, "files (3, ", "three displayable files are counted")
	assert.Contains(t, header, "SKILL.md", "the instruction file is listed")
	assert.Contains(t, header, "reference/forms.md", "helpers are listed")
	assert.Contains(t, header, "scripts/extract.py", "scripts are listed")
	assert.Contains(t, header, "needs: sh — present (", "a present requirement is PATH-resolved")
	assert.Contains(t, header, "definitely-missing-xyz — MISSING",
		"a missing requirement says MISSING")
	assert.NotContains(t, header, ".hidden", "dot-entries are hidden from the files list")
	assert.Contains(t, header, "edit: yes", "edit follows the effective policy")
	assert.Contains(t, header, "<instructions>", "instructions follow the header")
	assert.Contains(t, header, "PDF BODY", "the body is served without frontmatter")
	assert.NotContains(t, header, "omnipus-requires", "frontmatter keys are not echoed raw")
}

// TestLoadHeader_EditDeniedClause (S-1.1's `edit:` per policy leg).
// BDD: with edit_skill denied the header's edit clause flips.
// Traces to: skills-spec-r13.md §5.7, S-1.1.
func TestLoadHeader_EditDeniedClause(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "plain")
	header := loadHeader(t, root, "plain", LoadActivationOptions{EditAllowed: false})
	assert.Contains(t, header, "edit: no", "a denied editor reads edit: no")
	assert.NotContains(t, header, "edit: yes", "the yes clause must not leak")
}

// TestLoadHeader_BuiltinShelf (S-1.2).
// BDD: Skill(plan) for a granted built-in — shelf="builtin", no revision or
// version attribute, "shipped with Omnipus" last change, edit refused with
// the copy advice.
// Traces to: skills-spec-r13.md §5.7, S-1.2.
func TestLoadHeader_BuiltinShelf(t *testing.T) {
	root := t.TempDir()
	builtinRoot := t.TempDir()
	writeSkillFixture(t, builtinRoot, "plan")

	header := loadHeader(t, root, "plan", LoadActivationOptions{
		BuiltinRoot: builtinRoot,
		EditAllowed: true,
	})

	assert.Contains(t, header, `<skill slug="plan" shelf="builtin">`,
		"the built-in header names no revision or version")
	assert.NotContains(t, header, `revision="`, "no revision attribute for a built-in")
	assert.NotContains(t, header, `version="`, "no version attribute for a built-in")
	assert.Contains(t, header, "last change: shipped with Omnipus",
		"the built-in's last change is its shipping")
	assert.Contains(t, header, "edit: no (built-in; copy it to change it)",
		"the edit clause carries the copy advice")
}

// TestLoadHeader_FileListCap (S-1.3).
// BDD: a skill with 250 files lists 200 and names the overflow with the
// list_directory pointer.
// Traces to: skills-spec-r13.md §5.7, S-1.3.
func TestLoadHeader_FileListCap(t *testing.T) {
	root := t.TempDir()
	helpers := make([]string, 0, 249)
	for i := 0; i < 249; i++ {
		helpers = append(helpers, filepath.Join("docs", fmt.Sprintf("doc%03d.md", i)))
	}
	dir := writeSkillFixture(t, root, "big")
	for _, h := range helpers {
		full := filepath.Join(dir, h)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("d"), 0o644))
	}

	header := loadHeader(t, root, "big", LoadActivationOptions{EditAllowed: true})
	assert.Contains(t, header, "files (200, ", "exactly 200 files are listed")
	assert.Contains(t, header, "… 50 more — use list_directory",
		"the overflow names the list_directory pointer")
}

// TestLoadHeader_AllowedToolsSpellings_Regression (S-1.4 — named regression
// pair, SC-001: must pass on the baseline; guards the parser consolidation).
// BDD: `allowed-tools` as a YAML sequence and as a space list parse to the
// identical tool set through the LIVE loader.
// Traces to: skills-spec-r13.md S-1.4, §17.1 row 9, SC-001.
func TestLoadHeader_AllowedToolsSpellings_Regression(t *testing.T) {
	root := t.TempDir()
	seqDir := writeSkillFixture(t, root, "seq-form")
	seqMD := "---\nname: seq-form\ndescription: seq\nallowed-tools:\n  - Read\n  - Bash(python3:*)\n---\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(seqDir, "SKILL.md"), []byte(seqMD), 0o644))

	spaceDir := writeSkillFixture(t, root, "space-form")
	spaceMD := "---\nname: space-form\ndescription: space\nallowed-tools: Read Bash(python3:*)\n---\n\nbody\n"
	require.NoError(t, os.WriteFile(filepath.Join(spaceDir, "SKILL.md"), []byte(spaceMD), 0o644))

	loader := NewSkillsLoader("", root, "")
	seqMeta, err := loader.getSkillMetadataChecked(filepath.Join(seqDir, "SKILL.md"))
	require.NoError(t, err)
	spaceMeta, err := loader.getSkillMetadataChecked(filepath.Join(spaceDir, "SKILL.md"))
	require.NoError(t, err)

	assert.ElementsMatch(t, seqMeta.AllowedTools, spaceMeta.AllowedTools,
		"both spellings yield the identical allowed-tools set (regression pair — passes on baseline)")
	assert.Len(t, seqMeta.AllowedTools, 2, "both tools parsed from the sequence form")
}

// TestLoadHeader_OutsideChange (S-1.6).
// BDD: after an outside edit, the header reads "unknown (changed outside
// Omnipus)" and an external_change version exists.
// Traces to: skills-spec-r13.md §5.7, §9.4, S-1.6, §17.1 row 18.
func TestLoadHeader_OutsideChange(t *testing.T) {
	root := t.TempDir()
	dir := writeSkillFixture(t, root, "foo")
	commitWrite(t, root, "foo", "SKILL.md", validSkillMD("foo", "v1", "v1 body"),
		readLiveRevision(t, dir))

	// The outside edit (holds no lock, no pipeline).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte(validSkillMD("foo", "edited outside", "outside body")), 0o644))

	header := loadHeader(t, root, "foo", LoadActivationOptions{EditAllowed: true})
	assert.Contains(t, header, "last change: unknown (changed outside Omnipus)",
		"the header names the outside change truthfully")

	// Detection recorded an external_change version.
	historyRoot := SkillHistoryRoot(root)
	ids := manifestIDs(t, historyRoot, "foo")
	require.NotEmpty(t, ids, "detection committed a version")
	foundExternal := false
	for _, id := range ids {
		if manifestField(t, historyRoot, "foo", id, "kind") == "external_change" {
			foundExternal = true
		}
	}
	assert.True(t, foundExternal, "an external_change version exists after the load")
}
