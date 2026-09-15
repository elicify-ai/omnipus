// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

// subturn_sources_test.go — the shared source-scanning helper for the subturn
// file family. subturn.go was split into subturn*.go siblings by job on
// 2026-09-15 (identity resolution, result collection/delivery), so a guard
// test that used to read "subturn.go" alone could silently stop covering the
// siblings. Everything that scans the sub-turn production sources by name
// reads this family view instead.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readSubturnSourcesForTest returns the concatenated non-test sources of the
// sub-turn machinery: every subturn*.go file in pkg/agent/, in name order,
// each preceded by a "// ---- file: <name> ----" banner. Guard tests that
// used to read "subturn.go" alone must read this instead: subturn.go was
// split into subturn_*.go siblings by job on 2026-09-15, so a symbol scanned
// by name may live in any of them. Mirrors readLoopSourcesForTest
// (window_trim_test.go), which covers the loop*.go family for the same
// reason.
func readSubturnSourcesForTest(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("subturn*.go")
	require.NoError(t, err, "readSubturnSourcesForTest: glob")
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
	require.Greater(t, n, 0, "readSubturnSourcesForTest: no subturn*.go sources found")
	return b.String()
}
