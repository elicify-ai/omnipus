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
// exist. This package keeps real identity/store-reopen checks and the
// classification clauses of each scenario. The runtime rows run against the
// real loop and the real producers in the two packages that can import them:
//   S-137  pkg/agent    TestProgramProvenance_RootLineageRuntime
//   S-138  pkg/agent    TestProgramProvenance_ActualHelperWebSteer
//   S-143  pkg/gateway  TestProgramProvenance_ActualTaskSessionReentry
//   S-144  pkg/agent    TestProgramProvenance_LegacyRecordsRuntime (stored records
//          without provenance_mode), TestProgramProvenance_ActualPlanOwnerSession
//          (plan owner) and TestProgramProvenance_ActualVerifierSession
//          (verifier, withheld only); pkg/gateway
//          TestProgramProvenance_ActualHeartbeatSession (heartbeat) and
//          TestProgramProvenance_ActualIsolatedScheduledSession (isolated
//          scheduled)
// Every S-144 work kind therefore has a runtime row; this package keeps the
// store identity and classification clauses. A "restart" in those tests is an
// in-process rebuild of the runtime over the same home, not a process restart.
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

		// And (spec S-138, R5-M1): the delegated child is `lineage` from its
		// parent edge; the top-level main record, with no parent, no task
		// evidence and not plan-owned, is `root` on load; a top-level record
		// of any work kind is `lineage`. The evidence is read back from the
		// reopened store, not typed in.
		require.Equal(t, ProvenanceModeLineage, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: OriginKindDelegate, ParentSessionID: childAfter.ParentSessionID}), "S138: a delegated child is lineage")
		require.Equal(t, ProvenanceModeRoot, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: OriginKindMain, ParentSessionID: mainAfter.ParentSessionID, TaskID: mainAfter.TaskID}), "S138: the top-level main record is root on load")
		require.Equal(t, ProvenanceModeLineage, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: OriginKindMain, ParentSessionID: mainAfter.ParentSessionID, TaskID: mainAfter.TaskID, PlanOwned: true}), "S138: a plan-owned top-level record is lineage")
		for _, work := range []OriginKind{OriginKindTask, OriginKindScheduled, OriginKindHeartbeat, OriginKindVerifier, OriginKindPlan} {
			require.Equal(t, ProvenanceModeLineage, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: work, ParentSessionID: mainAfter.ParentSessionID}), "S138: a top-level %s record is lineage", work)
		}

		// The runtime rows of spec S-138 run against the real delegate tool,
		// launcher and loop in pkg/agent::TestProgramProvenance_ActualHelperWebSteer:
		// the child's record is `lineage` with {connector:telegram}; steered
		// from the web it is {connector:telegram, web} and both runs print
		// `0`; a child record of the same shape without stored provenance
		// (written before this feature) is `unresolved` and prints `0`.
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

		// The classification of that revived record (R5-M1 row 3, and the
		// S-143 instrument): task evidence alone makes a parentless record
		// `lineage`, whatever its origin kind says. A classifier that called
		// every parentless non-delegate record `root` would fail here, and
		// would let the web re-entry print `16`.
		for _, origin := range []OriginKind{OriginKindTask, OriginKindChat, OriginKindChannel, OriginKindMain} {
			require.Equal(t, ProvenanceModeLineage, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: origin, ParentSessionID: after.ParentSessionID, TaskID: after.TaskID}), "S143: a parentless record with task evidence is lineage (origin %s)", origin)
		}

		// The runtime rows of spec S-143 run against the real create_task
		// tool, the real Board start and an in-process gateway-runtime rebuild
		// over the same home, in
		// pkg/gateway::TestProgramProvenance_ActualTaskSessionReentry: the
		// record is `lineage` with {connector:telegram}; every run prints
		// `0`; the web re-entry turn is {connector:telegram, web}, still
		// withheld; a task record the classifier called `root` prints `16`.
	})

	t.Run("S144_every_work_kind_lineage_unknown_identity_fails_closed", s1RootLineageS144WorkKinds)
}

// s1RootLineageS144WorkKinds is the S144 subtest of TestProgramProvenance_RootLineage,
// a named function so the parent test stays under the function size budget.
func s1RootLineageS144WorkKinds(t *testing.T) {
	s1RootLineageSeamsReady(t)
	home := filepath.Join(t.TempDir(), "sessions")
	store, err := NewUnifiedStore(home)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// Given (store identity only; the producers run in the packages named in
	// the header): one session of each work kind the store can mint, with
	// its identity evidence (scheduled, heartbeat, verifier), a parentless
	// task session and a `main` session — the stored records of S-144 that
	// carry no provenance_mode.
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
	oldMain, err := store.GetOrCreateMainSession("sales", "dev")
	require.NoError(t, err, "instrument: the old main session exists")

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
	mainAfter, err := reopened.GetMeta(oldMain.ID)
	require.NoError(t, err)
	require.Equal(t, SessionTypeMain, mainAfter.Type, "identity evidence survives")

	// Classification (spec S-144, R5-M1): every work kind is `lineage`
	// parentless or not, the plan owner included; the old parentless task
	// record is `lineage`; a record whose origin is missing is `lineage`
	// (it holds no stored provenance, so it is `unresolved` when woken);
	// the old `main` record, read back from the reopened store, is `root`.
	workKinds := map[string]OriginKind{
		"isolated scheduled": OriginKindScheduled,
		"heartbeat":          OriginKindHeartbeat,
		"verifier":           OriginKindVerifier,
		"plan owner":         OriginKindPlan,
	}
	for name, origin := range workKinds {
		require.Equal(t, ProvenanceModeLineage, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: origin}), "S144: a top-level %s session is lineage", name)
	}
	require.Equal(t, ProvenanceModeLineage, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: OriginKindTask, ParentSessionID: legacyAfter.ParentSessionID, TaskID: legacyAfter.TaskID}), "S144: the old parentless task record is lineage")
	require.Equal(t, ProvenanceModeLineage, ClassifyProvenanceMode(ProvenanceModeEvidence{}), "S144: a record whose origin is missing is lineage, never a permissive root")
	require.Equal(t, ProvenanceModeRoot, ClassifyProvenanceMode(ProvenanceModeEvidence{Origin: OriginKindMain, ParentSessionID: mainAfter.ParentSessionID, TaskID: mainAfter.TaskID}), "S144: the old main record is root")

	// The S-144 rows that need a producer run against the real producers
	// in the packages that can import them (see the header):
	//   heartbeat          pkg/gateway::TestProgramProvenance_ActualHeartbeatSession
	//   isolated scheduled pkg/gateway::TestProgramProvenance_ActualIsolatedScheduledSession
	//   plan owner         pkg/agent::TestProgramProvenance_ActualPlanOwnerSession
	//   verifier           pkg/agent::TestProgramProvenance_ActualVerifierSession
	//     (withheld only: no producer hands the verifier the verified
	//     work's provenance, so it prints 0 as dataset row 31 requires)
	//   stored records without provenance_mode
	//                      pkg/agent::TestProgramProvenance_LegacyRecordsRuntime
}
