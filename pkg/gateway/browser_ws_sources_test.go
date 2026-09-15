// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readBrowserWsSourcesForTest concatenates every non-test browser_ws*.go
// source in pkg/gateway into a single string, each file preceded by a
// "// ---- file: <name> ----" marker so a failure message can still say where
// a match came from.
//
// # WHY THIS EXISTS
//
// A source-scanning test that opens "browser_ws.go" by name is pinned to a
// filename, not to the code it means to guard. The moment browser_ws.go is
// split by job — which is the whole direction of
// docs/internal/architecture/draft-module-map.md — such a scan silently
// narrows to whatever happens to be left in that one file. It keeps passing,
// and it is no longer covering the socket wiring it was written for. Nothing
// fails, so nobody looks: the exact shape catalogued in
// docs/internal/false-green-patterns.md, where a guard test passed 673/673
// with the feature it guarded deleted.
//
// Scanning the whole browser_ws*.go family instead means a function moving
// between sibling files in the same package — a no-op for behaviour, since Go
// resolves identifiers per package and not per file — is also a no-op for the
// guard.
//
// Mirrors readRestSourcesForTest in rest_sources_test.go, added for the same
// reason when rest.go was split by domain.
func readBrowserWsSourcesForTest(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("browser_ws*.go")
	require.NoError(t, err, "readBrowserWsSourcesForTest: glob browser_ws*.go")

	var b strings.Builder
	n := 0
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		require.NoErrorf(t, err, "readBrowserWsSourcesForTest: read %s", name)
		b.WriteString("// ---- file: " + name + " ----\n")
		b.Write(data)
		b.WriteString("\n")
		n++
	}
	// A glob that matches nothing returns no error, so without this the
	// source-slicing scans would pass vacuously on a broken checkout.
	require.Greaterf(t, n, 0,
		"readBrowserWsSourcesForTest: no non-test browser_ws*.go sources found in %s", mustGetwdForTest(t))
	return b.String()
}

// browserWsFamilyFilesForTest lists every non-test browser_ws*.go source in
// pkg/gateway, in glob order — the set of files browser_ws.go may be split
// into. Guards that report a finding as file:line need the per-file list, not
// the concatenation readBrowserWsSourcesForTest builds.
//
// Mirrors gatewayFamilyFilesForTest in gateway_sources_test.go, added for the
// same reason when gateway.go was split by job.
func browserWsFamilyFilesForTest(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob("browser_ws*.go")
	require.NoError(t, err, "browserWsFamilyFilesForTest: glob browser_ws*.go")

	files := make([]string, 0, len(matches))
	for _, name := range matches {
		if isBrowserWsFamilySource(name) {
			files = append(files, name)
		}
	}
	// A glob that matches nothing returns no error, so without this the
	// guards below would pass vacuously on a broken checkout.
	require.Greaterf(t, len(files), 0,
		"browserWsFamilyFilesForTest: no non-test browser_ws*.go sources found in %s", mustGetwdForTest(t))
	return files
}

// isBrowserWsFamilySource reports whether name is a non-test member of the
// browser_ws*.go family.
func isBrowserWsFamilySource(name string) bool {
	return strings.HasPrefix(name, "browser_ws") &&
		strings.HasSuffix(name, ".go") &&
		!strings.HasSuffix(name, "_test.go")
}

// sliceFromMarkerForTest returns the source text from the first occurrence of
// marker to the next top-level "\n}\n", failing the test (rather than
// panicking on a negative slice bound) when either bound is missing. Every
// source-scanning assertion that slices a declaration out of the
// browser_ws*.go family goes through this helper so a rename or a move out of
// the family reads as a named failure, not a panic two frames away.
//
// Mirrors sliceFromMarkerForTest in pkg/agent/window_trim_test.go, added for
// the same reason when loop.go was split.
func sliceFromMarkerForTest(t *testing.T, src, marker string) string {
	t.Helper()
	start := strings.Index(src, marker)
	require.GreaterOrEqual(t, start, 0,
		"%q not found in the browser_ws*.go family — renamed, or moved to a file outside the family?", marker)
	rest := src[start:]
	end := strings.Index(rest, "\n}\n")
	require.GreaterOrEqual(t, end, 0,
		"the declaration starting at %q is not delimited by a top-level closing brace", marker)
	return rest[:end]
}
