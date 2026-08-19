// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// seedWorkspaceRecord persists a minimal valid workspace so the mount engine has
// a record to attach mounts to. Uses the canonical exported writer rather than
// hand-writing the JSON, so this test cannot drift from the real storage shape.
func seedWorkspaceRecord(t *testing.T, home, id string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := workspace.SaveRecord(home, workspace.Workspace{
		ID: id, Name: "test", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed workspace %s: %v", id, err)
	}
}

// TestResolveTurnFSPolicy_MountsBecomeWriteRoots is the end-to-end proof that a
// mount actually reaches a live turn.
//
// Every layer beneath this was already tested in isolation — the mount engine
// creates and validates, AllowedRoots is consumed as a write grant, the kernel
// derivation carries it — and all of that could be green while a mount still
// did nothing, because nothing populated AllowedRoots for a real turn. This is
// the join.
func TestResolveTurnFSPolicy_MountsBecomeWriteRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	const wsID = "w-mounts"
	seedWorkspaceRecord(t, home, wsID)
	work, err := workspace.EnsureWorkDir(home, wsID)
	if err != nil {
		t.Fatalf("ensure work dir: %v", err)
	}

	// A folder outside the workspace entirely — the case mounts exist for.
	target := t.TempDir()
	if seedErr := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("x"), 0o600); seedErr != nil {
		t.Fatalf("seed target: %v", seedErr)
	}

	ctx := WithWorkspaceID(context.Background(), wsID)
	ctx = WithTurnWorkspaceDir(ctx, work)

	// Before the mount: the outside folder is not a write root.
	before, err := ResolveTurnFSPolicy(ctx, work, true)
	if err != nil {
		t.Fatalf("resolve before: %v", err)
	}
	if len(before.AllowedRoots) != 0 {
		t.Fatalf("no mounts configured, want no AllowedRoots, got %v", before.AllowedRoots)
	}
	if _, resolveErr := ResolvePath(ctx, before, "test", "", FSOpWrite, filepath.Join(target, "new.txt")); resolveErr == nil {
		t.Fatal("write outside the work dir must be denied before a mount exists")
	}

	// Create the mount.
	if _, warn, mountErr := workspace.CreateMount(home, wsID, "repo", target); mountErr != nil {
		t.Fatalf("create mount: %v", mountErr)
	} else if warn != "" {
		t.Logf("mount warning (expected empty for an ordinary folder): %s", warn)
	}

	after, err := ResolveTurnFSPolicy(ctx, work, true)
	if err != nil {
		t.Fatalf("resolve after: %v", err)
	}
	if len(after.AllowedRoots) == 0 {
		t.Fatal("after mounting, the target must appear as an AllowedRoot — otherwise the " +
			"mount engine, the grant plumbing and the kernel derivation are all correct " +
			"and a mount still does nothing")
	}
	if _, err := ResolvePath(ctx, after, "test", "", FSOpWrite, filepath.Join(target, "new.txt")); err != nil {
		t.Errorf("write into the mounted folder must now succeed, got %v", err)
	}
}

// TestResolveTurnFSPolicy_MountCannotReopenTheSecretSet is FR-7.7 at the turn
// level, and it is what makes the operator's warn-and-allow decision safe.
//
// The structural argument lives in pkg/fspolicy — the carve-out check takes no
// AllowedRoots parameter, so a mount cannot influence it. This asserts the same
// thing through the REAL resolver with a mount that literally contains
// $OMNIPUS_HOME, which is the case an operator would actually create by
// mounting their home directory.
func TestResolveTurnFSPolicy_MountCannotReopenTheSecretSet(t *testing.T) {
	parent := t.TempDir() // stands in for $HOME
	home := filepath.Join(parent, ".omnipus")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv(config.EnvHome, home)
	for _, secret := range []string{"master.key", "cli.token", "config.json"} {
		if err := os.WriteFile(filepath.Join(home, secret), []byte("SECRET"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", secret, err)
		}
	}

	const wsID = "w-broad"
	seedWorkspaceRecord(t, home, wsID)
	work, err := workspace.EnsureWorkDir(home, wsID)
	if err != nil {
		t.Fatalf("ensure work dir: %v", err)
	}
	// Mount the PARENT — the folder that contains $OMNIPUS_HOME. Allowed by
	// FR-7.6 with a warning; only $OMNIPUS_HOME itself is refused.
	if _, warn, mountErr := workspace.CreateMount(home, wsID, "everything", parent); mountErr != nil {
		t.Fatalf("create mount: %v", mountErr)
	} else if warn == "" {
		t.Error("mounting a folder that CONTAINS $OMNIPUS_HOME must warn (FR-7.4/7.6); " +
			"silently allowing it gives the operator no signal about what they just granted")
	}

	ctx := WithWorkspaceID(context.Background(), wsID)
	ctx = WithTurnWorkspaceDir(ctx, work)
	policy, err := ResolveTurnFSPolicy(ctx, work, true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	for _, secret := range []string{"master.key", "cli.token", "config.json"} {
		p := filepath.Join(home, secret)
		if _, err := ResolvePath(ctx, policy, "test", "", FSOpRead, p); err == nil {
			t.Errorf("READ of %s succeeded through a mount containing $OMNIPUS_HOME — "+
				"the secret set must be subtracted from every grant regardless of source, "+
				"and if this fails the warn-and-allow decision must revert to refusal", secret)
		}
		if _, err := ResolvePath(ctx, policy, "test", "", FSOpWrite, p); err == nil {
			t.Errorf("WRITE of %s succeeded through a broad mount — an agent could disable "+
				"its own sandbox on the next boot", secret)
		}
	}

	// The mount must still work for everything that is NOT a secret, or the
	// protection has simply broken the feature.
	ordinary := filepath.Join(parent, "notes.txt")
	if _, err := ResolvePath(ctx, policy, "test", "", FSOpWrite, ordinary); err != nil {
		t.Errorf("an ordinary file inside the broad mount must stay writable, got %v", err)
	}
}
