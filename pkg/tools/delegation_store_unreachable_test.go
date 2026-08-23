// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// The delegation edge list is an AUTHORIZATION — per ADR-037 it is the SOLE
// control governing who may delegate to whom — so it must live somewhere the
// constrained principal cannot write. This test asserts that from the direction
// that actually decides it: the store's position relative to the two
// enforcement layers' own denied sets.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'DelegationStore' -p 1 ./pkg/tools/

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestDelegationStorePathIsInTheDeniedSet is the assertion that makes issue
// #636's fix durable rather than incidental.
//
// The delegation store's protection is not a property of the store — it is a
// property of WHERE the store sits. It lives under $OMNIPUS_HOME/entities, and
// `entities` is in fspolicy.SecretEntriesAlways: the context-free half of the
// secret set, emitted unconditionally by DeniedPathsFor for every turn with no
// own-tree exception (no work dir is ever inside entities/), and included in
// SecretPaths, which is what the app layer's carve-out list is.
//
// So this test asserts the containment relationship directly against fspolicy's
// own output. If someone later moves the store — back to workspaces/, to a new
// top-level dir, anywhere outside the denied set — this fails immediately,
// instead of the self-authorization silently coming back.
//
// It deliberately mirrors TestMountStorePathIsInTheDeniedSet, because the two
// stores exist for the same reason and share the same protection.
func TestDelegationStorePathIsInTheDeniedSet(t *testing.T) {
	home := t.TempDir()
	const wsID = "w-denied"

	storePath, err := workspace.DelegationStorePath(home, wsID)
	if err != nil {
		t.Fatalf("delegation store path: %v", err)
	}

	// (1) KERNEL layer: SecretPathsAlways is what the boot policy denies, and
	// DeniedPathsFor emits every one of those entries for every turn.
	var kernelAncestor string
	for _, denied := range fspolicy.SecretPathsAlways(home) {
		if isWithinOrEqualForTest(storePath, denied) {
			kernelAncestor = denied
			break
		}
	}
	if kernelAncestor == "" {
		t.Fatalf("the delegation store %q is NOT under any path in fspolicy.SecretPathsAlways(%q) = %v — "+
			"a sandboxed child could write its own delegation edge list and authorize itself, "+
			"which is the escalation this store exists to close (issue #636)",
			storePath, home, fspolicy.SecretPathsAlways(home))
	}
	if want := filepath.Join(home, "entities"); kernelAncestor != want {
		t.Errorf("expected the delegation store to be protected by the %q root, got %q", want, kernelAncestor)
	}

	// (2) Same thing through the per-turn function the kernel derivation
	// actually calls, with the work dir of a real re-rooted workspace turn —
	// the exact shape that re-admits `workspaces` and therefore made the
	// workspace record writable. entities/ must NOT be re-admitted here.
	workDir := filepath.Join(home, "workspaces", wsID, "work")
	denied := fspolicy.DeniedPathsFor(home, workDir)
	found := false
	for _, d := range denied {
		if isWithinOrEqualForTest(storePath, d) {
			found = true
		}
	}
	if !found {
		t.Errorf("DeniedPathsFor(%q, %q) = %v does not cover the delegation store %q",
			home, workDir, denied, storePath)
	}

	// The contrast that motivates the whole change: the workspace record IS
	// reachable under that same per-turn denied set, which is why the edge list
	// cannot live in it.
	recordPath := filepath.Join(home, "workspaces", wsID+".json")
	for _, d := range denied {
		if isWithinOrEqualForTest(recordPath, d) {
			t.Fatalf("the workspace record %q is unexpectedly denied by %q — if this ever becomes true, "+
				"re-read this test's premise before relaxing anything elsewhere", recordPath, d)
		}
	}

	// (3) APP layer: the carve-out list a real turn is built with.
	//
	// EffectiveFSPolicy realpaths $OMNIPUS_HOME, and IsCarveOut's contract is
	// that its argument is already realpath'd (ResolvePath does that for every
	// real call). On macOS t.TempDir() hands back a /var/... path that is
	// really /private/var/..., so both sides are resolved here — otherwise this
	// would compare two spellings of the same directory and report a bypass
	// that does not exist.
	if mkErr := os.MkdirAll(workDir, 0o700); mkErr != nil {
		t.Fatalf("mkdir work: %v", mkErr)
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("realpath home: %v", err)
	}
	realStorePath, err := workspace.DelegationStorePath(realHome, wsID)
	if err != nil {
		t.Fatalf("delegation store path: %v", err)
	}
	policy, err := fspolicy.EffectiveFSPolicy(
		context.Background(), workDir, workDir, true, home, "agent-1", wsID)
	if err != nil {
		t.Fatalf("effective policy: %v", err)
	}
	if !fspolicy.IsCarveOut(realStorePath, policy) {
		t.Errorf("the delegation store %q is not an app-layer carve-out under a real workspace turn policy (carve-outs=%v)",
			realStorePath, policy.CarveOuts)
	}
}
