// Omnipus — Provider definitions shape test (M5, FR-053 / SC-008).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools_test

// TestProviderDefs_ShapeUnchanged verifies that the BuiltinRegistry's set of
// registered builtins (as reported by Describe()) has not changed shape since
// the golden file was generated. Run with -update to regenerate.
//
// The golden file captures the sorted list of builtin tool entries: name,
// description, scope, category, source. Any change to the set of builtins
// (add/remove/rename) will fail this test, prompting a deliberate golden
// update (go test ./pkg/tools/... -run TestProviderDefs_ShapeUnchanged -update).
//
// FR-053: provider defs format matches spec.
// SC-008: golden-file guard prevents accidental tool registration changes.

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

var updateGolden = flag.Bool("update", false, "update golden files")

// buildTestBuiltinRegistry creates a BuiltinRegistry populated with all sysagent
// builtins, matching the production boot path.
func buildTestBuiltinRegistry() *tools.BuiltinRegistry {
	reg := tools.NewBuiltinRegistry()
	for _, t := range systools.AllTools(nil, nil) {
		// Ignore duplicate-registration errors — some tools may share a name if
		// AllTools is called twice; we only care about the final shape.
		_ = reg.RegisterBuiltin(t)
	}
	return reg
}

// goldenFilePath returns the absolute path to the golden file regardless of
// the working directory from which `go test` is invoked.
func goldenFilePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "provider_defs.golden.json")
}

// TestProviderDefs_ShapeUnchanged diffs the current BuiltinRegistry shape
// against the golden file. Fails with a human-readable diff on mismatch.
// Regenerate the golden file by passing -update to `go test`.
//
// BDD: Given a BuiltinRegistry populated with production builtins,
//
//	When Describe() is called and the result is serialized to JSON,
//	Then the output must match pkg/tools/testdata/provider_defs.golden.json byte-for-byte.
//	If -update is passed, the golden file is written instead of compared.
//
// Traces to: FR-053 (provider defs format), SC-008 (shape guard).
func TestProviderDefs_ShapeUnchanged(t *testing.T) {
	reg := buildTestBuiltinRegistry()
	entries := reg.Describe()

	got, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent failed: %v", err)
	}
	got = append(got, '\n') // trailing newline for POSIX compliance

	goldenPath := goldenFilePath(t)

	if *updateGolden {
		if writeErr := os.WriteFile(goldenPath, got, 0o644); writeErr != nil {
			t.Fatalf("writing golden file %s: %v", goldenPath, writeErr)
		}
		t.Logf("golden file updated: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file %s: %v — run with -update to create it", goldenPath, err)
	}

	if string(got) != string(want) {
		t.Errorf("provider defs shape changed (FR-053 / SC-008).\n"+
			"Re-run with -update to accept the new shape.\n\n"+
			"Got (%d bytes):\n%s\n\nWant (%d bytes):\n%s",
			len(got), got, len(want), want)
	}
}
