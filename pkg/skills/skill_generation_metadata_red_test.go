package skills

// r15 §5.13a/§7.2/§7.3: generation is non-content control metadata.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerationMetadata_ExcludedFromRevisionCaptureRestoreAndSecretScan(t *testing.T) {
	root := t.TempDir()
	out, v1 := createSkillViaPipeline(t, root, "foo", "identity exclusions", "body")
	dir := filepath.Join(root, "foo")
	generation := generationOf(t, dir)
	rev, err := ContentRevision(dir)
	require.NoError(t, err)
	assert.Equal(t, out.Revision, rev)
	// Rotating metadata alone cannot change content identity. The synthetic
	// value is also presented as a stored secret: it must never enter the scan.
	const next = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	require.NoError(t, os.WriteFile(filepath.Join(dir, SkillIdentityFile), []byte(next+"\n"), 0o644))
	after, err := ContentRevision(dir)
	require.NoError(t, err)
	assert.Equal(t, rev, after)
	old := SecretValuesProvider
	defer func() { SecretValuesProvider = old }()
	SecretValuesProvider = func() ([]credentials.NamedValue, error) {
		return []credentials.NamedValue{{Name: "SYNTHETIC_IDENTITY", Value: next}}, nil
	}
	result, v2 := commitWrite(t, root, "foo", "notes.md", "safe note\n", rev)
	for _, id := range []string{v1, v2} {
		raw, err := os.ReadFile(filepath.Join(SkillHistoryRoot(root), "foo", "versions", id+".json"))
		require.NoError(t, err)
		var manifest map[string]any
		require.NoError(t, json.Unmarshal(raw, &manifest))
		files, ok := manifest["files"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, files)
		for _, entry := range files {
			m, ok := entry.(map[string]any)
			require.True(t, ok)
			assert.NotEqual(t, SkillIdentityFile, m["path"])
		}
		assert.NotContains(t, string(raw), generation)
		assert.NotContains(t, string(raw), next)
	}
	_, err = ApplyChange(root, ChangeRequest{Kind: KindRestore, Slug: "foo", Version: v1, Expect: Expectation{Revision: result.Revision}, Actor: agentActor("agent-a")})
	require.NoError(t, err)
	assert.Equal(t, next, generationOf(t, dir), "live restore preserves live metadata, never snapshot generation")
	_, err = ApplyChange(root, ChangeRequest{Kind: KindEdit, Slug: "foo", Path: SkillIdentityFile, Content: []byte("forged\n"), Expect: Expectation{Revision: readLiveRevision(t, dir)}, Actor: agentActor("agent-a")})
	require.Error(t, err)
	assert.Equal(t, next, generationOf(t, dir), "generation is never an agent-writable path")
}

func TestGenerationFreshInstallOntoAbsenceNeverReusesDeletedStamp(t *testing.T) {
	root := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		staged := stagedWithSkillMD(t, "foo", "installed")
		out, err := ApplyChange(root, ChangeRequest{Kind: KindInstall, Slug: "foo", StagedDir: staged, Expect: Expectation{Absent: true}, Actor: ChangeActor{Kind: "person", Tool: "rest-install"}})
		require.NoError(t, err)
		got := generationOf(t, filepath.Join(root, "foo"))
		assert.False(t, seen[got], "install into absence must not reuse any previously deleted generation")
		seen[got] = true
		_, err = ApplyChange(root, ChangeRequest{Kind: KindDelete, Slug: "foo", Expect: Expectation{Revision: out.Revision}, Actor: agentActor("agent-a")})
		require.NoError(t, err)
	}
}
