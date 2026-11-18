// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the one-time delegation-depth default migration (founder decision
// 2026-11-08, Q8 B): stored explicit edge depth 3 (the retired shipped default)
// becomes 10 exactly once per install, one log line; every other stored value,
// an inherit edge, and a 3 saved AFTER the migration are never rewritten. All
// expected numbers are LITERALS from the founder decision, never read back from
// the code under test, so a moved constant or predicate fails loudly.

package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// migrationLogLine is the exact message MigrateDelegationDepthDefaultOnce logs.
// Tests count occurrences of this substring in a captured log file, so "one log
// line" is asserted against the real sink, not a mock.
const migrationLogLine = "one-time delegation depth default migration"

func ip(n int) *int { return &n }

// captureMigrationLog runs fn with the global file sink pointed at a temp file
// and returns the file's contents. The same pattern pkg/media and pkg/gateway
// tests use (logger.EnableFileLogging + cleanup disable).
func captureMigrationLog(t *testing.T, fn func()) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "migration.log")
	require.NoError(t, logger.EnableFileLogging(logPath))
	t.Cleanup(logger.DisableFileLogging)
	fn()
	data, err := os.ReadFile(logPath)
	require.NoError(t, err, "read captured log file")
	return string(data)
}

// writeDelegationStore persists edges through the canonical writer, so a test
// seed is byte-identical to what production saves.
func writeDelegationStore(t *testing.T, home, id string, edges []DelegationEdge) {
	t.Helper()
	require.NoError(t, SaveDelegation(home, id, edges))
}

// readDelegationStoreRaw reads a workspace's record straight off disk.
func readDelegationStoreRaw(t *testing.T, home, id string) (delegationStoreRecord, []byte) {
	t.Helper()
	path, err := DelegationStorePath(home, id)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "delegation store for %s must exist", id)
	var rec delegationStoreRecord
	require.NoError(t, json.Unmarshal(data, &rec), "record must stay parseable")
	return rec, data
}

// migrationMarkerPath is the once-guard marker for a test home.
func migrationMarkerPath(home string) string {
	return filepath.Join(DelegationStoreDir(home), delegationDepthMigrationMarker)
}

// TestDelegationDepthMigrationPin pins the migration's two literals: the
// retired default 3 it rewrites, and the shipped default 10 it raises to
// (founder decision 2026-11-08, Q8 B, on top of the 2026-10-27 ruling). A
// changed constant must fail here loudly, not silently re-scope the migration.
func TestDelegationDepthMigrationPin(t *testing.T) {
	if legacyDelegationDepthDefault != 3 {
		t.Fatalf("legacyDelegationDepthDefault = %d, want the retired default 3", legacyDelegationDepthDefault)
	}
	if delegationDepthDefaultRaisedTo != 10 {
		t.Fatalf("delegationDepthDefaultRaisedTo = %d, want the shipped default 10 (founder ruling 2026-10-27)", delegationDepthDefaultRaisedTo)
	}
}

// TestDelegationDepthMigration_ThreeBecomesTenOnceWithOneLogLine (criterion a):
// a stored explicit 3 becomes 10 on the first run, the record's other fields
// are untouched, the marker is written, and the real log sink carries EXACTLY
// ONE migration line — never one per edge or per workspace.
func TestDelegationDepthMigration_ThreeBecomesTenOnceWithOneLogLine(t *testing.T) {
	home := t.TempDir()
	writeDelegationStore(t, home, "ws-three", []DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Modes: []DelegationMode{ModeDirect}, Depth: ip(3)},
		{FromAgent: "ava", ToAgent: "jim", Depth: ip(3)},
		{FromAgent: "jim", ToAgent: "jim", Modes: []DelegationMode{ModeDirect, ModeTask}, Depth: ip(3)},
	})
	writeDelegationStore(t, home, "ws-five", []DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Depth: ip(5)},
	})

	var res DelegationDepthMigrationResult
	logText := captureMigrationLog(t, func() {
		var err error
		res, err = MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
	})

	// Every stored 3 became 10; the 5 stayed 5.
	three, _ := readDelegationStoreRaw(t, home, "ws-three")
	require.Len(t, three.Delegation, 3)
	for i, e := range three.Delegation {
		require.NotNil(t, e.Depth, "edge %d must keep an explicit depth", i)
		assert.Equal(t, 10, *e.Depth, "stored edge depth 3 must be migrated to the shipped default 10")
	}
	assert.Equal(t, []DelegationMode{ModeDirect}, three.Delegation[0].Modes,
		"migration must not touch anything but the depth value")
	assert.Equal(t, "jim", three.Delegation[0].FromAgent, "endpoints must be untouched")
	assert.Equal(t, "ava", three.Delegation[0].ToAgent, "endpoints must be untouched")
	assert.Equal(t, "ws-three", three.WorkspaceID, "the record's workspace_id must be untouched")

	five, _ := readDelegationStoreRaw(t, home, "ws-five")
	require.Len(t, five.Delegation, 1)
	require.NotNil(t, five.Delegation[0].Depth)
	assert.Equal(t, 5, *five.Delegation[0].Depth, "a stored depth other than 3 must stay exactly as saved")

	// Run summary and the marker.
	assert.True(t, res.Ran, "the first call must run the migration")
	assert.Equal(t, 2, res.WorkspacesScanned)
	assert.Equal(t, 1, res.WorkspacesChanged, "only the workspace holding 3s counts as changed")
	assert.Equal(t, 3, res.EdgesMigrated)
	assert.Equal(t, 0, res.RecordsSkipped)
	_, statErr := os.Stat(migrationMarkerPath(home))
	require.NoError(t, statErr, "the once-marker must exist after the migration ran")

	// Exactly ONE line in the real sink — one line per migration run, not per edge.
	lines := strings.Split(strings.TrimSpace(logText), "\n")
	count := 0
	for _, l := range lines {
		if strings.Contains(l, migrationLogLine) {
			count++
		}
	}
	assert.Equal(t, 1, count, "the migration must log exactly one line, got %d in:\n%s", count, logText)
}

// TestDelegationDepthMigration_SecondRunIsNoOp (criterion b): a second run —
// the next boot — rewrites nothing, logs nothing, and does not even rewrite
// the files it no longer examines: on-disk bytes are identical.
func TestDelegationDepthMigration_SecondRunIsNoOp(t *testing.T) {
	home := t.TempDir()
	writeDelegationStore(t, home, "ws-a", []DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Depth: ip(3)},
	})

	_, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)

	_, before := readDelegationStoreRaw(t, home, "ws-a")

	var res DelegationDepthMigrationResult
	logText := captureMigrationLog(t, func() {
		var err error
		res, err = MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
	})

	assert.False(t, res.Ran, "the second call must be a no-op once the marker exists")
	assert.Zero(t, res.WorkspacesScanned, "a no-op run must not even scan")
	assert.Zero(t, res.WorkspacesChanged)
	assert.Zero(t, res.EdgesMigrated)

	_, after := readDelegationStoreRaw(t, home, "ws-a")
	assert.Equal(t, string(before), string(after), "the second run must not touch the stored record")

	assert.NotContains(t, logText, migrationLogLine,
		"a no-op run must stay silent — the one log line belongs to the run that migrated")
}

// TestDelegationDepthMigration_UserChosenThreeAfterMigrationSurvives (criterion
// c): once the migration has run, a 3 an operator saves is a DELIBERATE choice
// — a later boot's migration run (marker present) must leave it exactly as
// saved. This is the once-only guarantee that makes the unavoidable rewrite of
// pre-migration 3s acceptable.
func TestDelegationDepthMigration_UserChosenThreeAfterMigrationSurvives(t *testing.T) {
	home := t.TempDir()
	writeDelegationStore(t, home, "ws-user", []DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Depth: ip(5)},
	})

	// First boot: the migration runs (nothing to migrate here) and writes the marker.
	res, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	require.True(t, res.Ran)

	// After the migration: the operator deliberately saves an explicit 3.
	writeDelegationStore(t, home, "ws-user", []DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Depth: ip(3)},
	})

	// A later boot: the migration is a no-op; the deliberate 3 survives it.
	res2, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	assert.False(t, res2.Ran, "the marker must make every later run a no-op")

	rec, _ := readDelegationStoreRaw(t, home, "ws-user")
	require.Len(t, rec.Delegation, 1)
	require.NotNil(t, rec.Delegation[0].Depth)
	assert.Equal(t, 3, *rec.Delegation[0].Depth,
		"a 3 saved after the migration ran is a deliberate choice and must never be rewritten")
}

// TestDelegationDepthMigration_OtherDepthsAndInheritUntouched (criterion d):
// only exactly 3 is rewritten. 0 (forbids onward delegation), 1, 2, 4, 5, 10
// and an INHERIT edge (depth absent) keep their stored shape — an inherit edge
// must not be materialized into a number by the migration.
func TestDelegationDepthMigration_OtherDepthsAndInheritUntouched(t *testing.T) {
	home := t.TempDir()
	writeDelegationStore(t, home, "ws-mixed", []DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Depth: ip(0)},
		{FromAgent: "jim", ToAgent: "ray", Depth: ip(1)},
		{FromAgent: "ava", ToAgent: "ray", Depth: ip(2)},
		{FromAgent: "ava", ToAgent: "jim", Depth: ip(4)},
		{FromAgent: "ray", ToAgent: "jim", Depth: ip(5)},
		{FromAgent: "ray", ToAgent: "ava", Depth: ip(10)},
		{FromAgent: "jim", ToAgent: "jim"},                      // inherit: no depth key at all
		{FromAgent: "planner", ToAgent: "worker", Depth: ip(3)}, // the only rewrite
	})

	res, err := MigrateDelegationDepthDefaultOnce(home)
	require.NoError(t, err)
	require.True(t, res.Ran)
	assert.Equal(t, 1, res.EdgesMigrated, "only the single stored 3 may be rewritten")
	assert.Equal(t, 1, res.WorkspacesChanged)

	rec, _ := readDelegationStoreRaw(t, home, "ws-mixed")
	require.Len(t, rec.Delegation, 8)
	byPair := map[string]DelegationEdge{}
	for _, e := range rec.Delegation {
		byPair[e.FromAgent+"→"+e.ToAgent] = e
	}
	for pair, want := range map[string]*int{
		"jim→ava":        ip(0),
		"jim→ray":        ip(1),
		"ava→ray":        ip(2),
		"ava→jim":        ip(4),
		"ray→jim":        ip(5),
		"ray→ava":        ip(10),
		"jim→jim":        nil, // inherit stays inherit — no depth materialized
		"planner→worker": ip(10),
	} {
		got := byPair[pair]
		if want == nil {
			assert.Nil(t, got.Depth, "edge %s must stay inherit (no stored depth)", pair)
			continue
		}
		require.NotNil(t, got.Depth, "edge %s must keep an explicit depth", pair)
		assert.Equal(t, *want, *got.Depth, "edge %s depth must be untouched by the migration", pair)
	}
}

// TestDelegationDepthMigration_FreshHomeSkipsUnreadable (criterion e): a fresh
// install (no store dir) writes only the marker; an inherit-only workspace is
// scanned but never rewritten; a workspace with no store file gets none
// created; a malformed or unsafe-id record is skipped untouched and counted —
// the migration never deletes or truncates data it cannot understand.
func TestDelegationDepthMigration_FreshHomeSkipsUnreadable(t *testing.T) {
	t.Run("fresh home with no store dir", func(t *testing.T) {
		home := t.TempDir()
		res, err := MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
		assert.True(t, res.Ran)
		assert.Zero(t, res.WorkspacesScanned)
		assert.Zero(t, res.WorkspacesChanged)
		assert.Zero(t, res.EdgesMigrated)
		assert.Zero(t, res.RecordsSkipped)
		_, statErr := os.Stat(migrationMarkerPath(home))
		require.NoError(t, statErr, "even an empty install must mark the migration done")
	})

	t.Run("inherit-only workspace is scanned, not rewritten", func(t *testing.T) {
		home := t.TempDir()
		writeDelegationStore(t, home, "ws-inherit", []DelegationEdge{
			{FromAgent: "jim", ToAgent: "ava"}, // inherit — resolves to the shipped 10 elsewhere (gateway GET), stays unstored here
		})
		res, err := MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
		assert.Equal(t, 1, res.WorkspacesScanned)
		assert.Zero(t, res.WorkspacesChanged, "an inherit-only workspace has nothing to migrate")
		rec, _ := readDelegationStoreRaw(t, home, "ws-inherit")
		require.Len(t, rec.Delegation, 1)
		assert.Nil(t, rec.Delegation[0].Depth, "an inherit edge must stay inherit")
	})

	t.Run("malformed record skipped untouched", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(DelegationStoreDir(home), 0o700))
		broken := filepath.Join(DelegationStoreDir(home), "broken.json")
		require.NoError(t, os.WriteFile(broken, []byte("{not json at all"), 0o600))

		res, err := MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
		assert.Equal(t, 1, res.RecordsSkipped, "the malformed record must be counted, not crashed on")
		data, readErr := os.ReadFile(broken)
		require.NoError(t, readErr)
		assert.Equal(t, "{not json at all", string(data), "a skipped record must be left byte-untouched")
	})

	t.Run("unsafe-id record skipped", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(DelegationStoreDir(home), 0o700))
		traversal := filepath.Join(DelegationStoreDir(home), "bad..name.json")
		require.NoError(t, os.WriteFile(traversal, []byte(`{"workspace_id":"bad..name"}`), 0o600))

		res, err := MigrateDelegationDepthDefaultOnce(home)
		require.NoError(t, err)
		assert.Equal(t, 1, res.RecordsSkipped, "a record whose id fails safeID must never be rewritten")
	})
}
