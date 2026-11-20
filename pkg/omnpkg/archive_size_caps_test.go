package omnpkg_test

// TestArchive_SizeCaps oracles are the FR-008 numbers, not the engine constants.
// Manifest and each agent file: 1 MiB (1,048,576 bytes) accepted, one byte over
// refused. Entries: 10,000 accepted as a size limit, 10,001 refused.
// Dataset D1 rows 14–16. The 64 MiB transport cap is the route's, not this file.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// FR-008 sizes. 1 MiB is 2^20 bytes, the same unit as the route's
// 67,108,864-byte (64 MiB) body. These are not the engine's constants.
const (
	specMiB            = 1 << 20
	specManifestBytes  = specMiB
	specAgentFileBytes = specMiB
	specDecodedBytes   = 256 * specMiB
	specWindowBytes    = 8 * specMiB
	specMaxEntries     = 10000
)

func TestArchive_SizeCaps(t *testing.T) {
	t.Run("D1-16 manifest 1 MiB + 1 byte refused as too large", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		pkg := buildPackagePadded(t, m, files, specManifestBytes+1)
		require.Equal(t, specManifestBytes+1, manifestLen(t, pkg),
			"fixture manifest must sit exactly one byte over 1 MiB")
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "manifest", limitDetail(t, err))
	})

	t.Run("manifest of exactly 1 MiB accepted", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		pkg := buildPackagePadded(t, m, files, specManifestBytes)
		require.Equal(t, specManifestBytes, manifestLen(t, pkg), "fixture must sit exactly on 1 MiB")
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		require.NoError(t, err, "exactly 1 MiB is accepted (D1 15 note)")
	})

	t.Run("D1-15 agent.json 1 MiB + 1 byte refused as too large", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		raw := jsonPadded(t, files[recordPath(testSourceID)], specAgentFileBytes+1)
		files[recordPath(testSourceID)] = raw
		m.Files[recordPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(raw), Size: int64(len(raw))}
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(buildPackage(t, m, files)))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "agent_file", limitDetail(t, err))
	})

	t.Run("SOUL.md of exactly 1 MiB accepted at upload", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		soul := bytes.Repeat([]byte("x"), specAgentFileBytes)
		files[soulPath(testSourceID)] = soul
		m.Files[soulPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(soul), Size: int64(len(soul))}
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(buildPackage(t, m, files)))
		require.NoError(t, err, "exactly 1 MiB is accepted")
	})

	t.Run("SOUL.md 1 MiB + 1 byte refused at upload", func(t *testing.T) {
		m, files := validManifest(testSourceID, false)
		soul := bytes.Repeat([]byte("x"), specAgentFileBytes+1)
		files[soulPath(testSourceID)] = soul
		m.Files[soulPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(soul), Size: int64(len(soul))}
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(buildPackage(t, m, files)))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "agent_file", limitDetail(t, err))
	})

	// 10,000 unlisted extras still pass the size cap and fail the listing
	// rule. 10,001 is the size cap. Both counts are fixture lengths, not the
	// engine constant (the old fixture was 10,004).
	t.Run("10,000 entries are not over the entry cap", func(t *testing.T) {
		pkg := packageWithEntries(t, specMaxEntries)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "manifest_mismatch")
	})

	t.Run("D1-14 10,001 entries refused as too large", func(t *testing.T) {
		pkg := packageWithEntries(t, specMaxEntries+1)
		_, _, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
		wantCode(t, err, "package_too_large")
		require.Equal(t, "entries", limitDetail(t, err))
	})
}

func manifestLen(t *testing.T, pkg []byte) int {
	t.Helper()
	return len(decodePackagePlain(t, pkg).Payload[omnpkg.ManifestEntry])
}

func jsonPadded(t *testing.T, raw []byte, target int) []byte {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	base, err := json.Marshal(doc)
	require.NoError(t, err)
	const padOverhead = 20 // ,"vendor_padding":""
	n := target - len(base) - padOverhead
	require.Greater(t, n, 0, "record is already past the target size")
	doc["vendor_padding"] = strings.Repeat("p", n)
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.Equal(t, target, len(out), "padding must land on the spec size")
	return out
}

func packageWithEntries(t *testing.T, n int) []byte {
	t.Helper()
	m, files := validManifest(testSourceID, false)
	entries := []tarEntry{manifestEntry(t, m, nil, nil, 0)}
	for _, name := range sortedKeys(files) {
		entries = append(entries, tarEntry{Name: name, Body: files[name]})
	}
	for len(entries) < n {
		entries = append(entries, tarEntry{
			Name: "omnipus/agents/" + testSourceID + "/f/" + fmt.Sprintf("%05d", len(entries)),
			Body: []byte("x"),
		})
	}
	require.Equal(t, n, len(entries), "fixture entry count must be exact")
	return buildArchive(t, entries)
}
