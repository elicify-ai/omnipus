// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readManagerSourcesForTest concatenates every non-test manager*.go source in
// pkg/tools/browser into a single string, each file preceded by a
// "// ---- file: <name> ----" marker so a failure message can still say where
// a match came from.
//
// WHY THIS EXISTS: a source-scanning test that opens "manager.go" by name is
// pinned to a filename, not to the code it means to guard. manager.go is split
// by job (docs/internal/architecture/draft-module-map.md's browser rows), so a
// by-name scan silently narrows to whatever happens to be left in that one
// file — it keeps passing while no longer covering the function it was written
// for. Scanning the whole manager*.go family instead means a function moving
// between siblings in the same package — a no-op for behaviour, since Go
// resolves identifiers per package — is also a no-op for the guard.
//
// Mirrors readRestSourcesForTest in pkg/gateway and readLoopSourcesForTest in
// pkg/agent, added for the same reason when those families were split.
func readManagerSourcesForTest(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("manager*.go")
	require.NoError(t, err, "readManagerSourcesForTest: glob manager*.go")

	var b strings.Builder
	n := 0
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		require.NoErrorf(t, err, "readManagerSourcesForTest: read %s", name)
		b.WriteString("// ---- file: " + name + " ----\n")
		b.Write(data)
		b.WriteString("\n")
		n++
	}
	// A glob that matches nothing returns no error, so without this the whole
	// family of scans would pass vacuously on a broken checkout.
	require.Greaterf(t, n, 0, "readManagerSourcesForTest: no non-test manager*.go sources found")
	return b.String()
}

// sliceFromMarkerForTest returns src from the first occurrence of marker. It
// fails the calling test when the marker is absent instead of panicking the
// whole test binary with a negative slice bound, which is what a bare
// src[strings.Index(src, marker):] does when a scanned function moves file.
// Same helper as pkg/agent's window_trim_test.go, which was written for the
// loop.go split this family's split mirrors.
func sliceFromMarkerForTest(t *testing.T, src, marker string) string {
	t.Helper()
	i := strings.Index(src, marker)
	require.GreaterOrEqual(t, i, 0, "marker %q not found in scanned source", marker)
	return src[i:]
}
