package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type repairGoldenEntry struct { // not-wire-format: independent private test dataset, not a product or manifest type.
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	Mtime   string `json:"mtime"`
}

type repairGolden struct { // not-wire-format: independent frozen test vector.
	Name     string              `json:"name"`
	Entries  []repairGoldenEntry `json:"entries"`
	Expected string              `json:"expected_fingerprint"`
}

// T22/T30, D13: independently frozen Python hashlib + uint64-BE length
// vectors. Expected hashes never call the producer or its Go codec/reducer.
func TestHistoryRepairCommand_ManifestFingerprintFrozenVectors(t *testing.T) {
	binary := repairBinary(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "chat-history-paging-be", "manifest-vectors.json"))
	require.NoError(t, err)
	var vectors []repairGolden
	require.NoError(t, json.Unmarshal(b, &vectors))
	require.Len(t, vectors, 3)
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			source := t.TempDir()
			source, err := filepath.EvalSymlinks(source)
			require.NoError(t, err)
			for _, e := range v.Entries {
				path := filepath.Join(source, filepath.FromSlash(e.Path))
				if e.Kind == "directory" {
					require.NoError(t, os.MkdirAll(path, 0o700))
					continue
				}
				require.NoError(t, os.WriteFile(path, []byte(e.Content), 0o600))
				ns, parseErr := strconv.ParseInt(e.Mtime, 10, 64)
				require.NoError(t, parseErr)
				require.NoError(t, os.Chtimes(path, time.Unix(0, ns), time.Unix(0, ns)))
				info, statErr := os.Stat(path)
				require.NoError(t, statErr)
				require.Equal(t, ns, info.ModTime().UnixNano(), "instrument: native fixture preserves frozen file mtime")
			}
			backup := t.TempDir()
			backup, err = filepath.EvalSymlinks(backup)
			require.NoError(t, err)
			manifest := repairManifest(t, binary, source, backup)
			b, err := os.ReadFile(manifest)
			require.NoError(t, err)
			var result map[string]any
			require.NoError(t, json.Unmarshal(b, &result))
			require.Equal(t, v.Expected, result["canonical_fingerprint"], "D13 path/kind/size/digest/file-mtime length-prefixed tuple stream; directory mtime empty")
		})
	}
}
