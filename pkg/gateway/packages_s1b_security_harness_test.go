package gateway

// Security regression fixtures preserve raw JSON spellings and duplicate member
// order. They use the existing test-owned tar/zstd writer, never package import
// or export output as an oracle (security report F1/F2/F4).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

func s1bFix3RecordParts(raw []byte) (*omnpkg.Manifest, []tarPart) {
	path := agentRecordPath(specSourceID)
	manifest := &omnpkg.Manifest{
		Format: omnpkg.FormatName, FormatVersion: omnpkg.FormatVersion, Scope: omnpkg.ScopeCustom,
		Elements: []omnpkg.ManifestElement{{
			Type: omnpkg.ElementTypeAgent, Schema: omnpkg.AgentSchemaVersion,
			SourceID: specSourceID, Name: "Coder", Files: []string{path},
		}},
		Counts:      map[string]int{"agent": 1},
		Files:       map[string]omnpkg.FileInfo{path: {SHA256: sha256hex(raw), Size: int64(len(raw))}},
		SourceBuild: "v0.1.1-test",
		CreatedAt:   time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
	}
	return manifest, []tarPart{{name: path, body: raw}}
}

func s1bFix3RecordWithMembers(t *testing.T, record map[string]any, members string) []byte {
	t.Helper()
	base, err := json.Marshal(record)
	require.NoError(t, err)
	// Append raw members without decoding them into a Go map: doing so would
	// erase exact duplicates and could hide the order-dependent F2 input.
	raw := []byte(strings.TrimSuffix(string(base), "}") + "," + members + "}")
	require.True(t, json.Valid(raw), "the intended alias/duplicate fixture must be JSON, not a syntax-error refusal")
	return raw
}
