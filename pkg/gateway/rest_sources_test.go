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

// readRestSourcesForTest concatenates every non-test rest*.go source in
// pkg/gateway into a single string, each file preceded by a
// "// ---- file: <name> ----" marker so a failure message can still say where
// a match came from.
//
// # WHY THIS EXISTS
//
// A source-scanning test that opens "rest.go" by name is pinned to a filename,
// not to the code it means to guard. The moment rest.go is split by domain —
// which is the whole direction of docs/internal/architecture/draft-module-map.md
// — such a scan silently narrows to whatever happens to be left in that one
// file. It keeps passing, and it is no longer covering the handlers it was
// written for. Nothing fails, so nobody looks: the exact shape catalogued in
// docs/internal/false-green-patterns.md, where a guard test passed 673/673
// with the feature it guarded deleted.
//
// Scanning the whole rest*.go family instead means a function moving between
// sibling files in the same package — a no-op for behaviour, since Go resolves
// identifiers per package and not per file — is also a no-op for the guard.
//
// Mirrors readLoopSourcesForTest in pkg/agent, added for the same reason when
// pkg/agent/loop.go was split.
func readRestSourcesForTest(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("rest*.go")
	require.NoError(t, err, "readRestSourcesForTest: glob rest*.go")

	var b strings.Builder
	n := 0
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		require.NoErrorf(t, err, "readRestSourcesForTest: read %s", name)
		b.WriteString("// ---- file: " + name + " ----\n")
		b.Write(data)
		b.WriteString("\n")
		n++
	}
	// A glob that matches nothing returns no error, so without this the whole
	// family of scans below would pass vacuously on a broken checkout.
	require.Greaterf(t, n, 0, "readRestSourcesForTest: no non-test rest*.go sources found in %s", mustGetwdForTest(t))
	return b.String()
}

// stripLineCommentsForTest blanks every line whose first non-space characters
// are "//", leaving line numbering and all other bytes intact.
//
// Source scans that grep for a call expression cannot otherwise tell a real
// call from one QUOTED IN A DOC COMMENT, and this package has both: today
// rest_library.go documents two `cm.RegisterHTTPHandler("/api/v1/library"…)`
// registrations in a comment above the handler. Scanning the rest*.go family
// without this filter reports those as duplicate route registrations, i.e. a
// false RED on code that is correct.
func stripLineCommentsForTest(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// mustGetwdForTest reports the working directory for a failure message; tests
// run with the package directory as cwd, so this names the directory actually
// scanned.
func mustGetwdForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}
