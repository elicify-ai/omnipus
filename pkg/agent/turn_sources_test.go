// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readTurnSourcesForTest returns the concatenated non-test sources of the turn
// family: every turn*.go file in pkg/agent/, in name order, each preceded by a
// "// ---- file: <name> ----" banner. Guard tests that used to read "turn.go"
// alone must read this instead: turn.go was split into turn_*.go files by job
// on 2026-09-15, so a symbol scanned by name may live in any of them.
func readTurnSourcesForTest(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("turn*.go")
	require.NoError(t, err, "readTurnSourcesForTest: glob")
	var b strings.Builder
	n := 0
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b.WriteString("// ---- file: " + name + " ----\n")
		b.WriteString(readOwnedFileForTest(t, name))
		b.WriteString("\n")
		n++
	}
	require.Greater(t, n, 0, "readTurnSourcesForTest: no turn*.go sources found")
	return b.String()
}
