// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// The mount list is a WRITE GRANT, so it must live somewhere the granted
// principal cannot write. These tests assert that from the two directions that
// matter: the grant list is unreachable to a sandboxed child on BOTH
// enforcement layers, and a hostile entry planted in the old (child-writable)
// location reaches a real turn as nothing at all.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'MountStore' -p 1 ./pkg/tools/

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// isWithinOrEqualForTest mirrors the containment rule both enforcement layers
// apply (fspolicy.isWithinOrEqual, unexported), with the same trailing-
// separator guard so "/a/bc" is not read as a descendant of "/a/b".
func isWithinOrEqualForTest(candidate, root string) bool {
	if candidate == root {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(candidate, root)
}

// TestMountStorePathIsInTheDeniedSet is the assertion that makes the fix
// durable rather than incidental.
//
// The mount store's protection is not a property of the store — it is a
// property of WHERE the store sits. It lives under $OMNIPUS_HOME/entities,
// and `entities` is in fspolicy.SecretEntriesAlways: the context-free half of
// the secret set, emitted unconditionally by DeniedPathsFor for every turn
// with no own-tree exception (no work dir is ever inside entities/), and
// included in SecretPaths, which is what the app layer's carve-out list is.
//
// So this test asserts the containment relationship directly against
// fspolicy's own output. If someone later moves the store — to workspaces/, to
// a new top-level dir, anywhere outside the denied set — this fails
// immediately, instead of the escalation silently coming back.
func TestMountStorePathIsInTheDeniedSet(t *testing.T) {
	home := t.TempDir()
	const wsID = "w-denied"

	storePath, err := workspace.MountStorePath(home, wsID)
	if err != nil {
		t.Fatalf("mount store path: %v", err)
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
		t.Fatalf("the mount store %q is NOT under any path in fspolicy.SecretPathsAlways(%q) = %v — "+
			"a sandboxed child could write its own write-grant list, which is the escalation this store exists to close",
			storePath, home, fspolicy.SecretPathsAlways(home))
	}
	if want := filepath.Join(home, "entities"); kernelAncestor != want {
		t.Errorf("expected the mount store to be protected by the %q root, got %q", want, kernelAncestor)
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
		t.Errorf("DeniedPathsFor(%q, %q) = %v does not cover the mount store %q", home, workDir, denied, storePath)
	}

	// The contrast that motivates the whole change: the workspace record IS
	// reachable under that same per-turn denied set, which is why the grant
	// list cannot live in it.
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
	// really /private/var/..., so both sides are resolved here — otherwise
	// this would compare two spellings of the same directory and report a
	// bypass that does not exist.
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("realpath home: %v", err)
	}
	realStorePath, err := workspace.MountStorePath(realHome, wsID)
	if err != nil {
		t.Fatalf("mount store path: %v", err)
	}
	policy, err := fspolicy.EffectiveFSPolicy(
		context.Background(), workDir, workDir, true, home, "agent-1", wsID)
	if err != nil {
		t.Fatalf("effective policy: %v", err)
	}
	if !fspolicy.IsCarveOut(realStorePath, policy) {
		t.Errorf("the mount store %q is not an app-layer carve-out under a real workspace turn policy (carve-outs=%v)",
			realStorePath, policy.CarveOuts)
	}
}

// TestMountStore_HostileWorkspaceRecordGrantsNothingOnALiveTurn is the
// end-to-end half: the same hostile record pkg/workspace tests in isolation,
// driven through the REAL turn resolver and the REAL path resolver.
//
// The attack it replays: a sandboxed child writes its own workspace record
// (kernel-reachable, see the contrast assertion above) with
//
//	"mounts": [{"name": "pwn", "host_path": "<somewhere outside>"}]
//
// and then issues an ordinary write_file. Before the store move, that write
// succeeded. This asserts it is denied, and that the turn's AllowedRoots stay
// empty.
func TestMountStore_HostileWorkspaceRecordGrantsNothingOnALiveTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	const wsID = "w-hostile"
	seedWorkspaceRecord(t, home, wsID)
	work, err := workspace.EnsureWorkDir(home, wsID)
	if err != nil {
		t.Fatalf("ensure work dir: %v", err)
	}

	// Somewhere the turn has no business writing.
	victim := t.TempDir()

	// The child rewrites its own workspace record, planting the grant.
	recordPath := filepath.Join(home, "workspaces", wsID+".json")
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read workspace record: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("parse workspace record: %v", err)
	}
	rec["mounts"] = []map[string]any{{"name": "pwn", "host_path": victim}}
	tampered, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal tampered record: %v", err)
	}
	if err := os.WriteFile(recordPath, tampered, 0o600); err != nil {
		t.Fatalf("write tampered record: %v", err)
	}

	ctx := WithWorkspaceID(context.Background(), wsID)
	ctx = WithTurnWorkspaceDir(ctx, work)
	policy, err := ResolveTurnFSPolicy(ctx, work, true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(policy.AllowedRoots) != 0 {
		t.Fatalf("a mount planted in the child-writable workspace record became a write grant: AllowedRoots=%v", policy.AllowedRoots)
	}
	if _, err := ResolvePath(ctx, policy, "write_file", "", FSOpWrite, filepath.Join(victim, "owned.txt")); err == nil {
		t.Fatal("a write outside the work dir succeeded via a mount the agent granted itself — " +
			"this is the self-service write grant the mount store exists to close")
	}

	// Control: a mount created through the real lifecycle DOES grant, so the
	// test above is proving the source is distrusted, not that mounts are
	// broken outright.
	legit := t.TempDir()
	if _, _, err := workspace.CreateMount(home, wsID, "legit", legit); err != nil {
		t.Fatalf("create mount: %v", err)
	}
	policy, err = ResolveTurnFSPolicy(ctx, work, true)
	if err != nil {
		t.Fatalf("resolve after legit mount: %v", err)
	}
	if _, err := ResolvePath(ctx, policy, "write_file", "", FSOpWrite, filepath.Join(legit, "ok.txt")); err != nil {
		t.Errorf("a mount created through the real lifecycle must still grant write, got %v", err)
	}
	// ...and the tampered entry is STILL inert even alongside a real one.
	if _, err := ResolvePath(ctx, policy, "write_file", "", FSOpWrite, filepath.Join(victim, "owned.txt")); err == nil {
		t.Error("the planted entry became live once a legitimate mount existed — the two sources must never be merged")
	}
}
