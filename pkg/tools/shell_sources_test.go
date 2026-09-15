// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — shell*.go family source reader for source-text tests.
//
// The bash tool's implementation is split by job across the shell*.go family
// (shell.go plus its guard/sweep/process siblings, and any further job files
// carved out of it), so a test that reads "shell.go" by filename pins a symbol
// to whichever fragment happens to keep it. Family scans read every shell*.go
// sibling instead, mirroring readLoopSourcesForTest
// (pkg/agent/window_trim_test.go) and readRestSourcesForTest
// (pkg/gateway/rest_sources_test.go), added for the same reason when loop.go
// and rest.go were split.

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readShellSourcesForTest concatenates every non-test shell*.go source in the
// package directory, each preceded by a "// ---- file: <name> ----" banner so
// a scan can still tell which file a hit came from.
func readShellSourcesForTest(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("shell*.go")
	require.NoError(t, err, "readShellSourcesForTest: glob shell*.go")

	var b strings.Builder
	n := 0
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		require.NoErrorf(t, err, "readShellSourcesForTest: read %s", name)
		b.WriteString("// ---- file: " + name + " ----\n")
		b.Write(data)
		b.WriteString("\n")
		n++
	}
	// A glob that matches nothing returns no error, so without this a family
	// scan would pass vacuously on a broken checkout.
	wd, _ := os.Getwd()
	require.Greaterf(t, n, 0, "readShellSourcesForTest: no non-test shell*.go sources found in %s", wd)
	return b.String()
}
