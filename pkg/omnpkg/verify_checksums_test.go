package omnpkg_test

// RED T-2 — TestVerify_Checksums (spec TDD plan order 6).
// Spec: FR-003 (every non-manifest entry listed with SHA-256 and size; refuse
// mismatches "naming the file"), SC-002 (a one-byte change in any packaged
// file fails verify and upload, naming the file); D1 rows 3–6; BDD "Verify an
// unchanged package" and "Verify a changed package".

import (
	"bytes"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// errDetail returns the given details.* entry of a coded error.
func errDetail(t *testing.T, err error, key string) string {
	t.Helper()
	require.Error(t, err)
	var pe *omnpkg.PackageError
	require.ErrorAs(t, err, &pe)
	return pe.Detail[key]
}

func TestVerify_Checksums(t *testing.T) {
	t.Run("BDD Verify an unchanged package: every file confirmed", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		pkg := buildPackage(t, m, files)
		report, err := omnpkg.Verify(bytes.NewReader(pkg))
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{recordPath(testSourceID), soulPath(testSourceID), instructionsPath(testSourceID)},
			report.FilesVerified,
			"verify confirms every payload file (D1 1 positive control)")
	})

	// D1 3 / SC-002 / BDD "Verify a changed package": one byte changed in
	// SOUL.md, repacked with the old manifest → checksum_mismatch naming the
	// file, at verify AND at upload.
	t.Run("D1-3 one byte of SOUL.md changed named by verify and upload", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		tampered := append([]byte(nil), files[soulPath(testSourceID)]...)
		tampered[0] ^= 0x20
		files[soulPath(testSourceID)] = tampered // manifest keeps the old hash
		pkg := buildPackage(t, m, files)

		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeChecksumMismatch)
		require.Contains(t, err.Error(), soulPath(testSourceID),
			"verify names the failing file (BDD: fails naming omnipus/agents/<id>/SOUL.md)")

		_, _, err = omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeChecksumMismatch)
	})

	// D1 4: manifest size wrong, hash right → checksum_mismatch (size is
	// part of the per-file integrity row, FR-003).
	t.Run("D1-4 manifest size wrong refused as checksum_mismatch", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		fi := m.Files[soulPath(testSourceID)]
		fi.Size = fi.Size + 1
		m.Files[soulPath(testSourceID)] = fi
		pkg := buildPackage(t, m, files)
		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeChecksumMismatch)
		require.Equal(t, soulPath(testSourceID), errDetail(t, err, "file"))
	})

	// D1 6: a listed AGENT.md missing from the payload → manifest_mismatch
	// (FR-003 "missing listed entries"), naming the file.
	t.Run("D1-6 listed AGENT.md missing refused as manifest_mismatch", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		delete(files, instructionsPath(testSourceID))
		pkg := buildPackage(t, m, files)
		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeManifestMismatch)
		require.Equal(t, instructionsPath(testSourceID), errDetail(t, err, "file"))
	})

	// SC-002 across every packaged file: the one-byte change must fail for
	// agent.json and AGENT.md too, naming each file.
	t.Run("SC-002 one-byte change in agent.json named", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		tampered := append([]byte(nil), files[recordPath(testSourceID)]...)
		tampered[len(tampered)-1] ^= 0x01
		files[recordPath(testSourceID)] = tampered
		pkg := buildPackage(t, m, files)
		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeChecksumMismatch)
		require.Contains(t, err.Error(), recordPath(testSourceID))
	})
	t.Run("SC-002 one-byte change in AGENT.md named", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		tampered := append([]byte(nil), files[instructionsPath(testSourceID)]...)
		tampered[0] ^= 0x80
		files[instructionsPath(testSourceID)] = tampered
		pkg := buildPackage(t, m, files)
		_, err := omnpkg.Verify(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodeChecksumMismatch)
		require.Contains(t, err.Error(), instructionsPath(testSourceID))
	})
}
