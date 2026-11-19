package omnpkg_test

// RED T-2 — TestArchive_SizeCaps (spec TDD plan order 5).
// Spec: FR-008 (≤ 10,000 entries; manifest ≤ 1 MiB; each agent file ≤ 1 MiB —
// "1 MiB exactly accepted"); Datasets D1 rows 14–16 and the at-limit halves of
// rows 15/16; BDD "Damaged…" (D1 11–16). The 64 MiB transport boundary itself
// is the route's (T-5), tested through the registered route.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// manifestLen returns the size of the manifest entry inside a built package.
func manifestLen(t *testing.T, pkg []byte) int {
	t.Helper()
	return len(decodePackagePlain(t, pkg).Payload[omnpkg.ManifestEntry])
}

func TestArchive_SizeCaps(t *testing.T) {
	// D1 16: manifest 1 MiB + 1 → 413. The padding is an unknown manifest
	// field, so the manifest stays semantically valid (FR-004) and only its
	// size is at fault. The fixture sits exactly one byte over the cap.
	t.Run("D1-16 manifest 1 MiB + 1 byte refused as too large", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		pkg := buildPackagePadded(t, m, files, omnpkg.MaxManifestBytes+1)
		require.Equal(t, omnpkg.MaxManifestBytes+1, manifestLen(t, pkg),
			"fixture manifest must sit exactly one byte over the 1 MiB cap")
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooLarge)
	})

	// At-limit control: a manifest of exactly 1 MiB is accepted.
	t.Run("manifest of exactly 1 MiB accepted", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		pkg := buildPackagePadded(t, m, files, omnpkg.MaxManifestBytes)
		require.Equal(t, omnpkg.MaxManifestBytes, manifestLen(t, pkg), "fixture must sit exactly on the limit")
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err, "exactly at the limit is accepted (D1 15 note)")
	})

	// D1 15: agent.json 1 MiB + 1 → 413.
	t.Run("D1-15 agent.json 1 MiB + 1 byte refused as too large", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		var rec map[string]any
		require.NoError(t, json.Unmarshal(files[recordPath(testSourceID)], &rec))
		rec["vendor_padding"] = string(bytes.Repeat([]byte("p"), omnpkg.MaxAgentFileBytes)) // unknown record field
		raw, err := json.Marshal(rec)
		require.NoError(t, err)
		require.Greater(t, len(raw), omnpkg.MaxAgentFileBytes, "fixture must exceed the agent-file cap")
		files[recordPath(testSourceID)] = raw
		m.Files[recordPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(raw), Size: int64(len(raw))}
		_, _, rerr := omnpkg.ReadPackage(bytes.NewReader(buildPackage(t, m, files)))
		wantCode(t, rerr, omnpkg.CodePackageTooLarge)
	})

	// At-limit: SOUL.md of exactly 1 MiB is accepted at upload (D1 15 note;
	// D1 25 uses the same boundary through the real route, T-5).
	t.Run("SOUL.md of exactly 1 MiB accepted at upload", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		soul := bytes.Repeat([]byte("x"), omnpkg.MaxAgentFileBytes)
		files[soulPath(testSourceID)] = soul
		m.Files[soulPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(soul), Size: int64(len(soul))}
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(buildPackage(t, m, files)))
		require.NoError(t, err, "exactly 1 MiB is accepted")
	})

	// Limit + 1 on the same boundary.
	t.Run("SOUL.md 1 MiB + 1 byte refused at upload", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		soul := bytes.Repeat([]byte("x"), omnpkg.MaxAgentFileBytes+1)
		files[soulPath(testSourceID)] = soul
		m.Files[soulPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(soul), Size: int64(len(soul))}
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(buildPackage(t, m, files)))
		wantCode(t, err, omnpkg.CodePackageTooLarge)
	})

	// D1 14: 10,001 entries → 413. The entries are unlisted on purpose: the
	// entry-count limit fires while decoding, before listing reconciliation
	// (FR-008 decoder limits).
	t.Run("D1-14 10,001 entries refused as too large", func(t *testing.T) {
		m, files := validManifest(testSourceID, true)
		entries := []tarEntry{manifestEntry(t, m, nil, nil, 0)}
		for _, name := range sortedKeys(files) {
			entries = append(entries, tarEntry{Name: name, Body: files[name]})
		}
		for i := 0; i < omnpkg.MaxEntries; i++ { // +10,000 → 10,004 entries in total
			entries = append(entries, tarEntry{
				Name: "omnipus/agents/" + testSourceID + "/f/" + fmt.Sprintf("%05d", i),
				Body: []byte("x"),
			})
		}
		require.Greater(t, len(entries)-1, omnpkg.MaxEntries, "fixture must exceed the entry cap")
		pkg := buildArchive(t, entries)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, omnpkg.CodePackageTooLarge)
	})
}
