// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for UnifiedStore.DeleteSession — Milestone 2.
//
// BDD scenarios:
//   Scenario: Delete existing session — verify directory removal
//   Scenario: Delete non-existent session — verify "not found" error
//   Scenario: Path traversal rejected — "../evil" returns validation error
//   Scenario: Empty session ID rejected — returns validation error
//   Scenario: ".." session ID rejected — returns validation error
//
// Traces to: pkg/session/unified.go — DeleteSession method (Milestone 2)

package session

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestStore creates a UnifiedStore rooted at t.TempDir() and returns it.
// Closed automatically via t.Cleanup — see issue #634: the constructor
// unconditionally starts a background stats-flusher goroutine that, left
// running past this test's return, keeps calling through the package-level
// FR-101 lock-recorder seam and can pollute a later test's recorded trace.
func newTestStore(t *testing.T) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "NewUnifiedStore must succeed")
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// --- ClearAll tests ---
//
// ClearAll is a destructive, irreversible, bulk operation (DELETE
// /api/v1/sessions/all → pkg/gateway/rest_settings.go:567 HandleClearSessions,
// which calls it once per registered agent's store). Before this file it had
// zero test coverage anywhere in the repo (repo-wide grep for "ClearAll" in
// *_test.go returned no hits).
//
// TODO: BDD scenario missing in spec — no wave-spec/BRD section documents
// ClearAll's Given/When/Then explicitly; scenarios below are inferred from
// the ClearAll doc comment and code at pkg/session/unified.go:833-869, plus
// the DeleteSession cascade-delete precedent it mirrors. [INFERRED]

// newTestStoreWithHome creates a UnifiedStore rooted at <home>/sessions with
// an explicit home directory, so cascade-deleted uploads (ClearAll, like
// DeleteSession) land at a path the test controls: <home>/uploads/<sessionID>.
func newTestStoreWithHome(t *testing.T) (store *UnifiedStore, home string) {
	t.Helper()
	home = t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err, "NewUnifiedStoreWithHome must succeed")
	// See newTestStore's doc comment / issue #634.
	t.Cleanup(func() { _ = store.Close() })
	return store, home
}
