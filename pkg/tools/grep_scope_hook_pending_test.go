// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !grep_scope_hook

// #920 RED pack — isolation shim for the one new production symbol the
// Go-level tests cannot avoid naming: the test-only seam
// grepScopeStatOpenHook / setGrepScopeStatOpenHook
// (read-boundary-consistency-spec.md FR-010, MIN-006).
//
// Why a shim: a test file that names an undefined identifier breaks the
// compilation of the WHOLE pkg/tools test binary, which would turn every
// existing pkg/tools test red for the wrong reason. So the real helper lives
// in grep_scope_hook_test.go behind the `grep_scope_hook` build tag, and this
// file — compiled by default — makes every test that needs the seam fail
// LOUDLY with BLOCKED (never t.Skip) until GREEN lands it.
//
// GREEN hand-over (two mechanical edits, no test logic changes):
//  1. delete this file;
//  2. delete the `//go:build grep_scope_hook` line from grep_scope_hook_test.go.
package tools

import "testing"

// installGrepScopeLostHook would install a one-shot hook that removes target
// between the existence check and the root open (FR-010).
func installGrepScopeLostHook(t *testing.T, target string) {
	t.Helper()
	t.Fatalf("BLOCKED: grepScopeStatOpenHook / setGrepScopeStatOpenHook not implemented — required by "+
		"read-boundary-consistency-spec.md FR-010 (tests 4a, 32, 32a); cannot remove %q between the "+
		"existence check and the root open. GREEN: add the seam in pkg/tools/grep.go, delete "+
		"grep_scope_hook_pending_test.go and drop the build constraint of grep_scope_hook_test.go", target)
}
