package omnpkg_test

// RED T-2 — TestExportSourceRead (spec TDD plan order 8).
// Spec: FR-018 (source files read without following links, regular files only,
// bounded to 1 MiB, refused with agent_file_unreadable otherwise; an unreadable
// existing file is refused, never omitted; hashes computed from the same
// captured bytes that are packaged), FR-015 (SOUL.md / AGENT.md packaged
// byte-identical; AGENT.md only when present); Dataset D11 rows 1–7; BDD
// "Export an agent with no AGENT.md", "Unsafe or oversized source files are
// refused at export".

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// exportOne runs Export for a single agent and returns the raw package bytes.
func exportOne(t *testing.T, src *fakeSource, id string) []byte {
	t.Helper()
	var out bytes.Buffer
	err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{id}}, &out)
	require.NoError(t, err)
	require.NotEmpty(t, out.Bytes(), "export produces package bytes")
	return out.Bytes()
}

func exportErrDetail(t *testing.T, err error, key string) string {
	t.Helper()
	var pe *omnpkg.PackageError
	require.ErrorAs(t, err, &pe)
	return pe.Detail[key]
}

func TestExportSourceRead(t *testing.T) {
	t.Run("D11-1 absent AGENT.md: package without it", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, baseRecord(testSourceID), []byte(testSoulBody), nil)
		pkg := exportOne(t, src, testSourceID)
		dec := decodePackagePlain(t, pkg)
		require.Equal(t, []string{
			omnpkg.ManifestEntry, recordPath(testSourceID), soulPath(testSourceID),
		}, dec.Order, "manifest first, then exactly agent.json and SOUL.md (BDD: two agent files, no AGENT.md)")
	})

	t.Run("D11-2 AGENT.md symlink to credentials refused, link not followed", func(t *testing.T) {
		src := newFakeSource()
		home := src.withAgent(t, testSourceID, baseRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		// Replace AGENT.md with a symlink pointing at a secret-shaped file.
		secret := filepath.Join(home, "credentials.json")
		require.NoError(t, os.WriteFile(secret, []byte(`{"api_key":"sk-TEST-SECRET"}`), 0o600))
		require.NoError(t, os.Remove(filepath.Join(home, omnpkg.AgentInstructionsNm)))
		require.NoError(t, os.Symlink(secret, filepath.Join(home, omnpkg.AgentInstructionsNm)))

		var out bytes.Buffer
		err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out)
		wantCode(t, err, omnpkg.CodeAgentFileUnreadable)
		require.Equal(t, "link", exportErrDetail(t, err, "check"), "check names the link refusal (D11 2)")
		require.Equal(t, omnpkg.AgentInstructionsNm, exportErrDetail(t, err, "file"))
		require.Empty(t, out.Bytes(), "no package bytes on refusal")
		require.NotContains(t, out.String(), "sk-TEST-SECRET", "the link target must not be read")
	})

	t.Run("D11-3 SOUL.md is a folder refused as not_regular", func(t *testing.T) {
		src := newFakeSource()
		home := src.withAgent(t, testSourceID, baseRecord(testSourceID), []byte(testSoulBody), nil)
		require.NoError(t, os.Remove(filepath.Join(home, omnpkg.AgentSoulName)))
		require.NoError(t, os.Mkdir(filepath.Join(home, omnpkg.AgentSoulName), 0o755))

		err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &bytes.Buffer{})
		wantCode(t, err, omnpkg.CodeAgentFileUnreadable)
		require.Equal(t, "not_regular", exportErrDetail(t, err, "check"))
		require.Equal(t, omnpkg.AgentSoulName, exportErrDetail(t, err, "file"))
	})

	// D11 4: an unreadable existing file is refused, never silently omitted.
	// The exact spec row (AGENT.md mode 000 → check "unreadable") is asserted
	// whenever the suite can reproduce it; as root the mode is bypassed, so
	// there the subtest asserts only that the export is refused and writes no
	// bytes, and says so in the log.
	t.Run("D11-4 unreadable file refused, never omitted", func(t *testing.T) {
		src := newFakeSource()
		home := src.withAgent(t, testSourceID, baseRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		soulPathOnDisk := filepath.Join(home, omnpkg.AgentInstructionsNm)
		require.NoError(t, os.Chmod(soulPathOnDisk, 0o000))
		t.Cleanup(func() { _ = os.Chmod(soulPathOnDisk, 0o644) })

		var out bytes.Buffer
		err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out)
		require.Error(t, err, "an unreadable source file must refuse the export")
		require.Empty(t, out.Bytes(), "no package bytes on refusal")
		if os.Geteuid() == 0 {
			t.Log("running as root: mode 000 is bypassed, only the refusal was asserted")
			return
		}
		wantCode(t, err, omnpkg.CodeAgentFileUnreadable)
		require.Equal(t, "unreadable", exportErrDetail(t, err, "check"))
		require.Equal(t, omnpkg.AgentInstructionsNm, exportErrDetail(t, err, "file"))
	})

	t.Run("D11-5 SOUL.md exactly 1 MiB exports", func(t *testing.T) {
		src := newFakeSource()
		soul := bytes.Repeat([]byte("s"), specAgentFileBytes)
		src.withAgent(t, testSourceID, baseRecord(testSourceID), soul, nil)
		pkg := exportOne(t, src, testSourceID)
		dec := decodePackagePlain(t, pkg)
		require.Len(t, dec.Payload[soulPath(testSourceID)], specAgentFileBytes)
	})

	t.Run("D11-6 SOUL.md 1 MiB + 1 refused as too_large", func(t *testing.T) {
		src := newFakeSource()
		soul := bytes.Repeat([]byte("s"), specAgentFileBytes+1)
		src.withAgent(t, testSourceID, baseRecord(testSourceID), soul, nil)
		var out bytes.Buffer
		err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out)
		wantCode(t, err, omnpkg.CodeAgentFileUnreadable)
		require.Equal(t, "too_large", exportErrDetail(t, err, "check"))
		require.Empty(t, out.Bytes())
	})

	// D11 7 / FR-018: hashes come from the captured bytes — the package must
	// be self-consistent: the packaged bytes hash to the manifest's SHA-256
	// and equal the source bytes.
	t.Run("D11-7 package is consistent: hash matches packaged and source bytes", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, baseRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		pkg := exportOne(t, src, testSourceID)
		dec := decodePackagePlain(t, pkg)
		m := readManifestFromPackage(t, dec.Payload[omnpkg.ManifestEntry])
		for _, name := range []string{soulPath(testSourceID), instructionsPath(testSourceID)} {
			require.Equal(t, sha256hex(dec.Payload[name]), m.Files[name].SHA256,
				"manifest hash must cover exactly the packaged bytes (FR-018)")
			require.Equal(t, int64(len(dec.Payload[name])), m.Files[name].Size)
		}
		require.Equal(t, []byte(testSoulBody), dec.Payload[soulPath(testSourceID)],
			"SOUL.md packaged byte-identical (FR-015)")
		require.Equal(t, []byte(testAgentBody), dec.Payload[instructionsPath(testSourceID)],
			"AGENT.md packaged byte-identical (FR-015)")
	})
}

// readManifestFromPackage parses the manifest entry of a package with plain
// encoding/json into the shared Manifest type (test-side; no engine call).
func readManifestFromPackage(t *testing.T, raw []byte) *omnpkg.Manifest {
	t.Helper()
	var m omnpkg.Manifest
	require.NoError(t, json.Unmarshal(raw, &m), "test-side manifest parse")
	return &m
}
