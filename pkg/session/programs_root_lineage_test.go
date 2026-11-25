// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramProvenance_RootLineage (order 12, Integration) —
// S-137 (one standing chat, two origins, separate turns, with restart),
// S-138 (delegated child keeps its restriction; classification And-clause),
// S-143 (parentless task session keeps its restriction on re-entry and
// restart) and S-144 (every work kind is lineage; unknown identity fails
// closed).
//
// The classification table, record fields and attached-child runtime now
// exist. This package keeps real identity/store-reopen checks. Actual root
// turns and seeded-lineage child re-entry are in pkg/agent's runtime suite.
// The remaining final fatals name incomplete actual-producer/dispatch fixtures;
// seeded records cannot certify those producers or a whole gateway restart.
// CHECK remains a separate instance from this test author.
//
// Oracle: I-2, FR-050 (root = identified standing conversation: origin kind
// chat/channel/main, no parent, no task evidence, not plan-owned; every
// work session parentless or not is lineage), R5-M1, R4-M2, S-143/S-144's
// per-kind expectations. Expected values come from the spec, never from the
// loader.

package session

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A missing table is still loud, but fields/runtime that now exist are not
// falsely described as absent. This precondition is not a producer audit.
func s1RootLineageSeamsReady(t *testing.T) {
	t.Helper()
	if len(ProvenanceModeRows()) == 0 {
		t.Fatal("BLOCKED: session.ProvenanceModeRows returned no classification rows — required by FR-050/S-83/S-143/S-144; the table must exist before its identity/store assertions can run")
	}
}

// s1ReopenStore reopens a UnifiedStore over the same home — the restart
// axis of S-137/S-143 (a revived session reads the same records).
func s1ReopenStore(t *testing.T, home string) *UnifiedStore {
	t.Helper()
	reopened, err := NewUnifiedStore(home)
	require.NoError(t, err, "instrument: the store reopens over the same home")
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened
}

func TestProgramProvenance_RootLineage(t *testing.T) {
	t.Run("S137_root_stores_no_provenance_two_origins_separate_turns", func(t *testing.T) {
		s1RootLineageSeamsReady(t)
		home := filepath.Join(t.TempDir(), "sessions")
		store, err := NewUnifiedStore(home)
		require.NoError(t, err)
		t.Cleanup(func() { _ = store.Close() })

		// Given: Dev's main chat is reached from the web and from Telegram —
		// one standing session, a `root` record. The record's identity
		// evidence: a main session, no parent, no task evidence.
		main, err := store.GetOrCreateMainSession("sales", "dev")
		require.NoError(t, err, "instrument: the main session exists")
		require.Equal(t, SessionTypeMain, main.Type, "identity evidence: origin kind main")
		require.Empty(t, main.ParentSessionID, "identity evidence: no parent")

		// Restart: the reopened store classifies the same record on load.
		reopened := s1ReopenStore(t, home)
		after, err := reopened.GetMeta(main.ID)
		require.NoError(t, err, "the record survives the restart")
		require.Equal(t, SessionTypeMain, after.Type, "identity evidence survives")

		// Runtime and actual root-record assertions migrated intact to
		// pkg/agent::TestProgramProvenance_RootLineageRuntime/S137...:
		// two actual origins in both orders, actual child counts 0/16 and a
		// closed/reopened loop. Keep this independent store-identity check.
		require.Equal(t, ProvenanceModeRoot, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: OriginKindMain, ParentSessionID: after.ParentSessionID, TaskID: after.TaskID}), "S137: the identified main record is root, not sticky lineage")
	})

	t.Run("S138_child_record_keeps_restriction_and_classification_and_clause", func(t *testing.T) {
		s1RootLineageSeamsReady(t)
		home := filepath.Join(t.TempDir(), "sessions")
		store, err := NewUnifiedStore(home)
		require.NoError(t, err)
		t.Cleanup(func() { _ = store.Close() })

		// Given: a delegated child of Dev (Telegram turn) — a child record,
		// provenance_mode `lineage`, {connector:telegram}.
		child, err := store.NewSession(SessionTypeDelegate, "telegram", "ops")
		require.NoError(t, err, "instrument: the delegated child exists")
		parent := "parent-session"
		require.NoError(t, store.SetMeta(child.ID, MetaPatch{ParentSessionID: &parent}),
			"instrument: the child carries its parent")

		// And: a top-level record with origin `main`, no parent, no task
		// evidence and not plan-owned is classified `root` on load; a
		// top-level record of any work kind is `lineage` (S-143, S-144).
		main, err := store.GetOrCreateMainSession("sales", "dev")
		require.NoError(t, err, "instrument: the main record exists")

		// Restart: both records are re-classified from stored evidence.
		reopened := s1ReopenStore(t, home)
		childAfter, err := reopened.GetMeta(child.ID)
		require.NoError(t, err)
		require.Equal(t, parent, childAfter.ParentSessionID, "identity evidence survives")
		mainAfter, err := reopened.GetMeta(main.ID)
		require.NoError(t, err)
		require.Equal(t, SessionTypeMain, mainAfter.Type, "identity evidence survives")

		// GREEN asserts here (spec S-138): the child's record is
		// `provenance_mode: lineage` with {connector:telegram}; steered from
		// the web its provenance is {connector:telegram, web} and both runs
		// print `0`; a child record of the same shape without stored
		// provenance (written before this feature) is `unresolved` and
		// prints `0`. The main record is classified `root` on load. Runtime
		// BLOCK until the record provenance and the join exist:
		t.Fatal("BLOCKED: the actual delegated-helper producer and web-steering join fixture is not yet implemented here — required by FR-041/FR-050/S-138; seeded lineage re-entry in pkg/agent does not certify that producer")
	})

	t.Run("S143_parentless_task_session_lineage_survives_reentry_and_restart", func(t *testing.T) {
		s1RootLineageSeamsReady(t)
		home := filepath.Join(t.TempDir(), "sessions")
		store, err := NewUnifiedStore(home)
		require.NoError(t, err)
		t.Cleanup(func() { _ = store.Close() })

		// Given: T's own task session — no parent, TaskID set (S-143).
		task, err := store.NewSession(SessionTypeTask, "", "ops")
		require.NoError(t, err, "instrument: the task session exists")
		taskID := "task-tele-1"
		require.NoError(t, store.SetMeta(task.ID, MetaPatch{TaskID: &taskID}),
			"instrument: the task evidence is stamped")
		meta, err := store.GetMeta(task.ID)
		require.NoError(t, err)
		require.Empty(t, meta.ParentSessionID, "identity evidence: parentless")
		require.Equal(t, taskID, meta.TaskID, "identity evidence: task evidence set")

		// Restart: the task session is revived from the same records.
		reopened := s1ReopenStore(t, home)
		after, err := reopened.GetMeta(task.ID)
		require.NoError(t, err, "the task session revives after the restart")
		require.Equal(t, taskID, after.TaskID, "task evidence survives")
		require.Empty(t, after.ParentSessionID, "parentlessness survives")

		// GREEN asserts here (spec S-143): the record is `lineage` with
		// {connector:telegram}; every run prints `0`; the web re-entry turn
		// is {connector:telegram, web}, still withheld. Runtime BLOCK:
		t.Fatal("BLOCKED: the actual Telegram-created task producer/dispatch fixture is not yet implemented here — required by FR-041/FR-010/S-143; seeded task-lineage re-entry/reopen in pkg/agent does not establish that original writer")
	})

	t.Run("S144_every_work_kind_lineage_unknown_identity_fails_closed", func(t *testing.T) {
		s1RootLineageSeamsReady(t)
		home := filepath.Join(t.TempDir(), "sessions")
		store, err := NewUnifiedStore(home)
		require.NoError(t, err)
		t.Cleanup(func() { _ = store.Close() })

		// Given: one session of each work kind through its real producer —
		// the identity evidence per kind: scheduled (isolated), heartbeat,
		// verifier, plan owner; plus stored records written without
		// provenance_mode: a parentless task session, a `main` session, and
		// a parentless record whose origin is missing.
		kindEvidence := map[string]UnifiedSessionType{
			"isolated scheduled": SessionTypeScheduled,
			"heartbeat":          SessionTypeHeartbeat,
			"verifier":           SessionTypeVerifier,
		}
		for name, kind := range kindEvidence {
			s, newErr := store.NewSession(kind, "", "dev")
			require.NoError(t, newErr, "instrument: the %s session exists", name)
			meta, metaErr := store.GetMeta(s.ID)
			require.NoError(t, metaErr)
			require.Equal(t, kind, meta.Type, "%s: identity evidence", name)
		}
		legacyTask, err := store.NewSession(SessionTypeTask, "", "dev")
		require.NoError(t, err, "instrument: the legacy parentless task session exists")

		// Restart: the work kinds are re-classified from stored evidence.
		reopened := s1ReopenStore(t, home)
		for _, kind := range kindEvidence {
			found := false
			sessions, lerr := reopened.ListSessions()
			require.NoError(t, lerr, "instrument: the reopened store lists")
			for _, m := range sessions {
				if m.Type == kind {
					found = true
				}
			}
			require.True(t, found, "the %s record survives the restart", kind)
		}
		legacyAfter, err := reopened.GetMeta(legacyTask.ID)
		require.NoError(t, err)
		require.Empty(t, legacyAfter.ParentSessionID, "the legacy task record stays parentless")

		// GREEN asserts here (spec S-144): the four work sessions are
		// `lineage` and print `0` (heartbeat follows founder Q1 via A-1:
		// `16` with web on); the old task session and the record with no
		// origin are `lineage` and `unresolved` and print `0`; the old
		// `main` session is `root`, and a web turn in it prints `16`.
		// Runtime BLOCK:
		t.Fatal("BLOCKED: actual per-kind producer/dispatch fixtures for scheduled, heartbeat, verifier and plan work plus unknown-origin records are not yet complete — required by FR-041/FR-010/S-144; seeded scheduled/verifier/heartbeat re-entry in pkg/agent is narrower")
	})
}
