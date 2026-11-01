package documentruntime

// RED tests — landing/skills (T9): the document-worker skills hand-off
// (S-1.5's app part). After T9 the document worker does NOT get a second
// copy of any skill: the skills env name is gone from the child environment,
// provision installs no embedded skills copy, and the manifest is bumped to
// the new revision so an old cached layout is never served.
// Traces to: skills-spec-r15.md S-1.5 (app part), §15 item 3
// (OMNIPUS_DOCUMENT_SKILLS / InstallEmbeddedSkills are gone-forever names),
// §17.1 row 10.
//
// RED expectation: ChildEnvironment still injects OMNIPUS_DOCUMENT_SKILLS
// and provision still installs the embedded copy — the assertions below
// fail on the baseline.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChildEnvironment_NoDocumentSkillsEnv (S-1.5 app part, §15 item 3).
// BDD: the document child's environment carries no OMNIPUS_DOCUMENT_SKILLS
// entry — the worker reads the same registry folders through the §6.2
// per-skill kernel grants instead of a copied tree.
// Traces to: skills-spec-r15.md S-1.5, §15 item 3.
func TestChildEnvironment_NoDocumentSkillsEnv(t *testing.T) {
	layout := Layout{
		Prefix:   t.TempDir(),
		Bin:      filepath.Join(t.TempDir(), "bin"),
		Lib:      filepath.Join(t.TempDir(), "lib"),
		Manifest: filepath.Join(t.TempDir(), "manifest.json"),
		Cache:    t.TempDir(),
	}
	env := ChildEnvironment([]string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}, layout)
	require.NotEmpty(t, env, "positive control: child environment must exist")
	values := map[string]string{}
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		require.True(t, ok)
		values[key] = value
		assert.False(t, strings.HasPrefix(strings.ToUpper(kv), "OMNIPUS_DOCUMENT_SKILLS="),
			"RED: the child env still carries OMNIPUS_DOCUMENT_SKILLS (%q) — the second copy is served through it", kv)
	}
	assert.Equal(t, layout.Bin+string(os.PathListSeparator)+"/usr/bin:/bin", values["PATH"])
	assert.Equal(t, filepath.Join(layout.Lib, "python"), values["PYTHONPATH"])
	// No LANG-preservation oracle: r15 S-1.5 does not require one. The
	// exact PATH/PYTHONPATH controls above already reject an empty environment.
	assert.Equal(t, layout.Cache, values["XDG_CACHE_HOME"])
}

// TestProvision_NoEmbeddedSkillsCopy (S-1.5 app part; §15 item 3).
// BDD: provision installs no embedded skills copy under the layout — the
// registry is the only copy (no skill tree is materialized under Prefix).
// Traces to: skills-spec-r15.md S-1.5, §15 item 3.
func TestProvision_NoEmbeddedSkillsCopy(t *testing.T) {
	dataRoot := t.TempDir()
	layout, err := ResolveLayout(dataRoot, manifestRevisionForTest(), "worker-1")
	require.NoError(t, err)

	_, err = ProvisionFirstParty(layout)
	require.NoError(t, err, "RED: provision with the skills copy removed is not implemented")

	files := 0
	require.NoError(t, filepath.WalkDir(layout.Prefix, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files++
		assert.NotEqual(t, "SKILL.md", d.Name(), "no skill instructions copied anywhere under document runtime: %s", p)
		return nil
	}))
	require.Greater(t, files, 0, "positive control: provision actually wrote runtime assets")
}

// manifestRevisionForTest returns a valid lowercase-hex revision for
// ResolveLayout (the current ManifestRevision constant).
func manifestRevisionForTest() string {
	return ManifestRevision
}
