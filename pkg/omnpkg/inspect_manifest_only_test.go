package omnpkg_test

// RED T-2 — TestInspect_ManifestOnly (spec TDD plan order 7).
// Spec: FR-052 ("inspect reads only the bounded manifest and makes no
// integrity claim"), SC-006 (inspect/verify leave an empty OMNIPUS_HOME
// empty); BDD "Inspect reads only the manifest" (corrupt payload, inspect
// still OK, nothing written) and "Newer package refused offline".

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

func TestInspect_ManifestOnly(t *testing.T) {
	t.Run("BDD corrupt payload, intact manifest: inspect still succeeds", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		files[soulPath(testSourceID)] = append([]byte(nil), files[soulPath(testSourceID)]...)
		files[soulPath(testSourceID)][0] ^= 0xFF // corrupt the payload, keep the manifest's hashes
		pkg := buildArchive(t, []tarEntry{
			manifestEntry(t, m, nil, nil, 0),
			{Name: recordPath(testSourceID), Body: files[recordPath(testSourceID)]},
			{Name: soulPath(testSourceID), Body: files[soulPath(testSourceID)]},
			{Name: instructionsPath(testSourceID), Body: files[instructionsPath(testSourceID)]},
		})

		home := t.TempDir() // empty OMNIPUS_HOME (SC-006)
		t.Setenv(config.EnvHome, home)

		got, err := omnpkg.Inspect(bytes.NewReader(pkg))
		require.NoError(t, err, "inspect reads only the manifest and makes no integrity claim")
		require.Equal(t, omnpkg.FormatName, got.Format)
		require.Equal(t, omnpkg.FormatVersion, got.FormatVersion)
		require.Equal(t, "custom", got.Scope)
		require.Len(t, got.Elements, 1)
		require.Equal(t, "agent", got.Elements[0].Type)
		require.Equal(t, omnpkg.AgentSchemaVersion, got.Elements[0].Schema)
		require.Equal(t, testSourceID, got.Elements[0].SourceID)
		require.Equal(t, testRecordName, got.Elements[0].Name)
		require.Equal(t, 1, got.Counts["agent"])
		require.Len(t, got.Files, 3, "manifest lists every payload file with its checksums")
		require.Equal(t, int64(len(testSoulBody)), got.Files[soulPath(testSourceID)].Size)
		require.Equal(t, sha256hex([]byte(testSoulBody)), got.Files[soulPath(testSourceID)].SHA256)
		require.False(t, got.IncludesSessions)
		require.Empty(t, got.NeedsSecrets, "S1a writes no needs_secrets")

		// SC-006: nothing was written — the empty home stays empty.
		entries, err := os.ReadDir(home)
		require.NoError(t, err)
		require.Empty(t, entries, "inspect must not write anything into OMNIPUS_HOME")
	})

	t.Run("BDD newer package refused offline by inspect", func(t *testing.T) {
		pkg := manifestWithVersion(t, omnpkg.FormatVersion+1, true)
		_, err := omnpkg.Inspect(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooNew)
	})

	t.Run("inspect reports manifest-only details with empty home untouched", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		pkg := buildPackage(t, m, files)
		home := t.TempDir()
		t.Setenv(config.EnvHome, home)
		got, err := omnpkg.Inspect(bytes.NewReader(pkg))
		require.NoError(t, err)
		require.Equal(t, testBuild, got.SourceBuild)
		require.NotNil(t, got.CreatedAt)
		_, err = os.Stat(filepath.Join(home, "backups"))
		require.ErrorIs(t, err, os.ErrNotExist, "inspect creates no staging or other folders")
	})
}
