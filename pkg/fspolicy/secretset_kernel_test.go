// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildKernelHome lays out a realistic $OMNIPUS_HOME and returns its realpath.
func buildKernelHome(t *testing.T) string {
	t.Helper()
	raw := t.TempDir()
	home, err := filepath.EvalSymlinks(raw)
	require.NoError(t, err)

	for _, dir := range []string{
		filepath.Join(home, "agents", "self", "sessions"),
		filepath.Join(home, "agents", "victim"),
		filepath.Join(home, "agents", "third"),
		filepath.Join(home, "workspaces", "w1", "work", "src"),
		filepath.Join(home, "workspaces", "w2", "work"),
		filepath.Join(home, "entities", "agents"),
		filepath.Join(home, "system"),
		filepath.Join(home, "backups"),
		filepath.Join(home, "logs"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	for _, f := range []string{
		"master.key", "credentials.json", "config.json", "cli.token", "auth.json",
		filepath.Join("agents", "self", "AGENT.md"),
		filepath.Join("agents", "victim", "SOUL.md"),
		filepath.Join("workspaces", "w1", "mounts.json"),
		filepath.Join("workspaces", "w1", "work", "src", "main.go"),
		filepath.Join("workspaces", "w2", "record.json"),
		filepath.Join("logs", "gateway.log"),
	} {
		require.NoError(t, os.WriteFile(filepath.Join(home, f), []byte("x"), 0o600))
	}
	return home
}

// kernelDenies reports whether the enumerated kernel deny list covers path,
// using the same on-or-under containment both backends apply to a deny entry.
func kernelDenies(t *testing.T, denied []string, path string) bool {
	t.Helper()
	clean := filepath.Clean(path)
	for _, d := range denied {
		if isWithinOrEqual(clean, filepath.Clean(d)) {
			return true
		}
	}
	return false
}

// walkHome returns every path under home, directories included.
func walkHome(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.Walk(home, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p != home {
			out = append(out, p)
		}
		return nil
	}))
	return out
}

// TestKernelDeniedPaths_MatchIsCarveOutPathForPath is the structural guard for
// ADR-061 FR-3.3, and it is the test that would have caught the shipped bug.
//
// The two layers answer "may this turn touch this path" by different means: the
// app layer runs a rule per resolved path (IsCarveOut), the kernel is handed a
// finite list before any path exists. FR-3.3 requires the answers to be the
// same, and asserting that BY EXAMPLE is what let them drift — every example
// anyone wrote happened to be one of the paths both layers agreed on.
//
// So this asserts it EXHAUSTIVELY: every path in a realistic $OMNIPUS_HOME, for
// every turn shape the product produces. Before KernelDeniedPathsFor existed
// this failed on every agents/<other> path for the agent-home shape — the
// kernel granted what the app layer denied.
func TestKernelDeniedPaths_MatchIsCarveOutPathForPath(t *testing.T) {
	home := buildKernelHome(t)
	all := walkHome(t, home)

	for _, shape := range []struct {
		name    string
		workDir string
	}{
		{"agent-home-rooted", filepath.Join(home, "agents", "self")},
		{"agent-home-rooted (other agent)", filepath.Join(home, "agents", "victim")},
		{"re-rooted workspace turn", filepath.Join(home, "workspaces", "w1", "work")},
		{"re-rooted workspace turn (other workspace)", filepath.Join(home, "workspaces", "w2", "work")},
	} {
		t.Run(shape.name, func(t *testing.T) {
			policy := FSPolicy{
				WorkDir:   shape.workDir,
				Scope:     FSScopeConfined,
				CarveOuts: SecretPaths(home),
			}
			denied, err := KernelDeniedPathsFor(home, shape.workDir)
			require.NoError(t, err)

			for _, p := range all {
				app := IsCarveOut(p, policy)
				kernel := kernelDenies(t, denied, p)
				if app == kernel {
					continue
				}
				// KNOWN, BOUNDED RESIDUAL — the coarse root node itself.
				//
				// Measured, not assumed: the ONLY path the two layers disagree
				// on is the coarse root directory (`agents`, `workspaces`) in
				// the shape where the caller's work dir is inside it. Every
				// path BELOW it agrees — the caller's own subtree is permitted
				// by both, and every other agent's or workspace's subtree is
				// denied by both.
				//
				// Practical effect: a child can list the directory and learn
				// the NAMES of other agents/workspaces. It cannot read or write
				// anything inside them. The app layer refuses even the listing.
				//
				// Why it is not closed: on macOS the kernel deny list would
				// have to deny the root and then re-allow the caller's own
				// subtree AFTER it — and "nothing is emitted after the deny
				// block" is the single invariant that stops a stray filtered
				// allow re-opening every secret (see
				// TestSeatbelt_DenyPrecedenceIsMeasuredNotAssumed). Trading
				// that invariant to hide a list of directory names is a bad
				// exchange. On Linux the grant-based walk never grants the root
				// node, so Linux does not have this residual at all.
				//
				// This is asserted narrowly ON PURPOSE: only the root node, only
				// in the kernel-wider direction. If the disagreement ever spreads
				// to a path BELOW the root, or flips direction, the test fails.
				if !kernel && isAncestorOfWorkDirUnderCoarseRoot(home, shape.workDir, p) {
					t.Logf("known residual (macOS deny-list shape): %s is listable by a child "+
						"but its contents are not reachable; app layer denies the listing too", p)
					continue
				}
				direction := "the KERNEL is WIDER than the app layer — a child reaches what the tools refuse"
				if kernel {
					direction = "the KERNEL is NARROWER than the app layer — the tools permit what a child cannot do"
				}
				t.Errorf("layers disagree on %s\n  app layer (IsCarveOut) denies: %v\n  kernel deny list covers:      %v\n  %s",
					p, app, kernel, direction)
			}
		})
	}
}

// TestKernelDeniedPaths_AgentTurnDeniesSiblingsAndAdmitsOwn is the specific
// claim in isolation, so a failure names the defect rather than a path.
func TestKernelDeniedPaths_AgentTurnDeniesSiblingsAndAdmitsOwn(t *testing.T) {
	home := buildKernelHome(t)
	self := filepath.Join(home, "agents", "self")

	denied, err := KernelDeniedPathsFor(home, self)
	require.NoError(t, err)

	assert.True(t, kernelDenies(t, denied, filepath.Join(home, "agents", "victim", "SOUL.md")),
		"another agent's home must be denied; DeniedPathsFor alone re-admits the whole agents/ "+
			"root here, which is the hole a reviewer demonstrated with a real child")
	assert.True(t, kernelDenies(t, denied, filepath.Join(home, "agents", "third")),
		"every sibling, not just the one someone thought to test")
	assert.False(t, kernelDenies(t, denied, filepath.Join(self, "AGENT.md")),
		"the agent's OWN home must stay reachable — a deny here breaks the product")
	assert.False(t, kernelDenies(t, denied, filepath.Join(self, "sessions")),
		"…including subdirectories of its own home")
}

// TestKernelDeniedPaths_WorkspaceTurnDeniesTheWorkspaceRecordItself: the
// own-tree exception is anchored on the WORK DIR, so a workspace's own metadata
// — sitting beside work/, not inside it — stays denied. A turn that could
// rewrite mounts.json would grant itself new write roots for the next turn.
func TestKernelDeniedPaths_WorkspaceTurnDeniesTheWorkspaceRecordItself(t *testing.T) {
	home := buildKernelHome(t)
	work := filepath.Join(home, "workspaces", "w1", "work")

	denied, err := KernelDeniedPathsFor(home, work)
	require.NoError(t, err)

	assert.True(t, kernelDenies(t, denied, filepath.Join(home, "workspaces", "w1", "mounts.json")),
		"the turn's own workspace record must be denied")
	assert.True(t, kernelDenies(t, denied, filepath.Join(home, "workspaces", "w2", "record.json")),
		"another workspace's record must be denied")
	assert.True(t, kernelDenies(t, denied, filepath.Join(home, "agents", "self", "AGENT.md")),
		"during a workspace turn the agent's own home is as unreachable as anyone else's")
	assert.False(t, kernelDenies(t, denied, filepath.Join(work, "src", "main.go")),
		"the work dir must stay usable")
}

// TestKernelDeniedPaths_NoWorkDirIsTheFullSet: with nothing to compare against,
// no root is re-admitted. The safe direction, and the boot-policy shape.
func TestKernelDeniedPaths_NoWorkDirIsTheFullSet(t *testing.T) {
	home := buildKernelHome(t)
	denied, err := KernelDeniedPathsFor(home, "")
	require.NoError(t, err)

	for _, p := range []string{
		filepath.Join(home, "agents"),
		filepath.Join(home, "workspaces"),
		filepath.Join(home, "master.key"),
	} {
		assert.True(t, kernelDenies(t, denied, p), "%s must be denied when there is no turn", p)
	}
}

// TestKernelDeniedPaths_UnlistableRootIsAnError pins the fail-closed contract:
// if the layout cannot be enumerated, the caller must NOT be handed
// DeniedPathsFor's re-admission, which is the wider answer.
func TestKernelDeniedPaths_UnlistableRootIsAnError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions, so the unreadable case cannot be staged")
	}
	home := buildKernelHome(t)
	agents := filepath.Join(home, "agents")
	require.NoError(t, os.Chmod(agents, 0o000))
	t.Cleanup(func() { _ = os.Chmod(agents, 0o750) })

	_, err := KernelDeniedPathsFor(home, filepath.Join(agents, "self"))
	require.Error(t, err,
		"an unreadable agents/ must be an error: without the listing there is no way to tell "+
			"this agent's home from the others, and the only wrong answer is to guess wide")
}

// isAncestorOfWorkDirUnderCoarseRoot reports whether p is a STRICT ANCESTOR of
// the turn's work dir that also sits at or under a per-turn coarse root
// (<home>/agents, <home>/workspaces).
//
// These are the only nodes where the two layers legitimately differ: the
// directories a child must traverse to reach its own work dir. The kernel
// cannot deny a directory it must walk through; the app layer, which resolves
// the full path in one step, can.
//
// The predicate is deliberately this narrow. A SIBLING — another agent's home,
// another workspace — is not an ancestor of this turn's work dir, so a
// divergence there still fails the test. That is the distinction between "a
// child can see the names of the directories above its own" and "a child can
// reach another agent's files", and only the first is acceptable.
func isAncestorOfWorkDirUnderCoarseRoot(home, workDir, p string) bool {
	cleanP := filepath.Clean(p)
	cleanWork := filepath.Clean(workDir)
	if cleanP == cleanWork || !isProperDescendant(cleanWork, cleanP) {
		return false // not a strict ancestor of the work dir
	}
	cleanHome := filepath.Clean(home)
	for _, name := range SecretEntriesPerTurn {
		root := filepath.Join(cleanHome, name)
		if cleanP == root || isProperDescendant(cleanP, root) {
			return true
		}
	}
	return false
}
