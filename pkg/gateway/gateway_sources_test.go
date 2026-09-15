// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gatewayFamilyFilesForTest lists every non-test gateway*.go source in
// pkg/gateway, in glob order — the set of files gateway.go may be split into.
//
// # WHY THIS EXISTS
//
// A source-scanning test that opens "gateway.go" by name is pinned to a
// filename, not to the code it means to guard. The moment gateway.go is split
// by job — which is the whole direction of
// docs/internal/architecture/draft-module-map.md — such a scan silently
// narrows to whatever happens to be left in that one file. It keeps passing,
// and it is no longer covering the boot wiring it was written for. Nothing
// fails, so nobody looks: the exact shape catalogued in
// docs/internal/false-green-patterns.md, where a guard test passed 673/673
// with the feature it guarded deleted.
//
// Scanning the whole gateway*.go family instead means a function moving
// between sibling files in the same package — a no-op for behaviour, since Go
// resolves identifiers per package and not per file — is also a no-op for the
// guard.
//
// Mirrors readRestSourcesForTest in rest_sources_test.go, added for the same
// reason when rest.go was split by domain.
func gatewayFamilyFilesForTest(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob("gateway*.go")
	require.NoError(t, err, "gatewayFamilyFilesForTest: glob gateway*.go")

	files := make([]string, 0, len(matches))
	for _, name := range matches {
		if isGatewayFamilySource(name) {
			files = append(files, name)
		}
	}
	// A glob that matches nothing returns no error, so without this the
	// guards below would pass vacuously on a broken checkout.
	require.Greaterf(t, len(files), 0,
		"gatewayFamilyFilesForTest: no non-test gateway*.go sources found in %s", mustGetwdForTest(t))
	return files
}

// isGatewayFamilySource reports whether name is a non-test member of the
// gateway*.go family.
func isGatewayFamilySource(name string) bool {
	return strings.HasPrefix(name, "gateway") &&
		strings.HasSuffix(name, ".go") &&
		!strings.HasSuffix(name, "_test.go")
}
