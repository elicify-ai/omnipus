// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// costHome builds an install whose agents/ root holds `files` ordinary files
// spread over other agents' homes — the shape that makes the hard-link scan
// expensive, since a workspace turn excludes none of agents/.
func costHome(t *testing.T, files int) (home string, policy FSPolicy) {
	t.Helper()
	raw := t.TempDir()
	home, err := filepath.EvalSymlinks(raw)
	require.NoError(t, err)

	work := filepath.Join(home, "workspaces", "w1", "work")
	require.NoError(t, os.MkdirAll(work, 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "backups"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, "master.key"), []byte("k"), 0o600))

	const perDir = 500
	for i := 0; i < files; i += perDir {
		dir := filepath.Join(home, "agents", fmt.Sprintf("other%d", i/perDir), "sessions")
		require.NoError(t, os.MkdirAll(dir, 0o750))
		for j := 0; j < perDir && i+j < files; j++ {
			require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.jsonl", j)), []byte("x"), 0o600))
		}
	}

	return home, FSPolicy{
		WorkDir:   work,
		Scope:     FSScopeConfined,
		CarveOuts: SecretPaths(home),
	}
}

// TestHardlink_ScanCostIsBounded is the measurement behind hardlinkScanBudget,
// and behind the claim that an ORDINARY file pays nothing.
//
// Both halves matter. A scan that is merely correct but runs on every path
// check is an outage, and a budget picked from taste rather than measurement is
// how a "bounded" scan turns out to take seconds.
func TestHardlink_ScanCostIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a multi-thousand-file tree")
	}
	const files = 8_000
	_, policy := costHome(t, files)

	ordinary := filepath.Join(policy.WorkDir, "ordinary.txt")
	require.NoError(t, os.WriteFile(ordinary, []byte("plain"), 0o600))

	// An ordinary, single-linked file: gate 2 stops it, so no walk happens.
	start := time.Now()
	for i := 0; i < 200; i++ {
		IsCarveOut(ordinary, policy)
	}
	perOrdinary := time.Since(start) / 200
	t.Logf("ordinary single-linked file: %v per IsCarveOut (%d files under agents/)", perOrdinary, files)
	assert.Less(t, perOrdinary, 2*time.Millisecond,
		"an ordinary file must not pay for the scan; the Nlink gate is the only thing keeping "+
			"this off the hot path")

	// A genuinely multiply-linked file: the walk runs, over the whole of
	// agents/ (a workspace turn excludes none of it) plus backups/.
	linked := filepath.Join(policy.WorkDir, "linked.txt")
	require.NoError(t, os.WriteFile(linked, []byte("plain"), 0o600))
	alias := filepath.Join(policy.WorkDir, "linked-alias.txt")
	if err := os.Link(linked, alias); err != nil {
		t.Skipf("hard links unavailable on this filesystem: %v", err)
	}

	start = time.Now()
	got := IsCarveOut(linked, policy)
	scanCost := time.Since(start)
	t.Logf("multiply-linked file, full scan of %d entries: %v", files, scanCost)
	assert.False(t, got,
		"both ends of this link are inside the work dir, so it is the agent's own file")

	// Extrapolate to the budget so the constant's justification is a
	// measurement in this repository rather than a number in a comment.
	//
	// The per-entry figure is load-sensitive (8µs idle, 17µs with the rest of
	// the suite running), so the ceiling below is deliberately set against the
	// LOADED figure. When it trips, hardlinkScanBudget comes down — it already
	// did once, from 200_000 to 100_000, for exactly this reason.
	perEntry := scanCost / time.Duration(files)
	t.Logf("~%v per entry; %d-entry budget therefore caps a scan at ~%v",
		perEntry, hardlinkScanBudget, perEntry*time.Duration(hardlinkScanBudget))
	assert.Less(t, perEntry*time.Duration(hardlinkScanBudget), 3*time.Second,
		"the budget must cap the worst case at something a turn can absorb; if this fails the "+
			"constant needs lowering, not the assertion relaxing")
}

// TestHardlink_BudgetExhaustionDenies pins the fail-closed leg directly, with a
// budget small enough to be exhausted deterministically. A scan that gave up
// and returned "not a secret" would be the worst possible outcome: a leak that
// only appears on large installs.
func TestHardlink_BudgetExhaustionDenies(t *testing.T) {
	home, policy := costHome(t, 1_000)

	linked := filepath.Join(policy.WorkDir, "linked.txt")
	require.NoError(t, os.WriteFile(linked, []byte("plain"), 0o600))
	if err := os.Link(linked, filepath.Join(policy.WorkDir, "alias.txt")); err != nil {
		t.Skipf("hard links unavailable on this filesystem: %v", err)
	}

	info, err := os.Stat(linked)
	require.NoError(t, err)

	budget := 5
	found, decided := scanForSameFile(filepath.Join(home, "agents"), "", info, &budget)
	assert.False(t, found)
	assert.False(t, decided,
		"an exhausted budget means the scan did not rule the link out; IsCarveOut must read that "+
			"as deny, never as allow")
}
