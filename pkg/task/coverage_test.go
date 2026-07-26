// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// coverage_test.go fills the behavioral gaps left by store_test.go.
// Every test asserts on real behavior — state changes, returned values,
// error identities — never just "no crash".
//
// Behaviors covered:
//   - validateID path-traversal rejections (/, \, .., NUL)
//   - IsTerminal, EffectivePriority (unset→3), EffectiveSurface (empty→user),
//     CreatedTime (valid and unparseable)
//   - Dir(), Lock(), Exists()
//   - lifecycle transitions: legal accepted, done→* frozen, blocked→* wire
//     rejected, failed→inbox allowed, partial-todo/title update guards
//   - ErrBlockedNotSettable: direct wire set of status=blocked rejected
//   - AdvanceUnblocked: no-op on non-blocked, transitions blocked→next
//   - SpawnReset: in-progress guard (ErrAlreadyRunning), clears run fields,
//     sets status=next
//   - ClaimForRun concurrent N-goroutine race: exactly one wins
//   - ClaimParentFollowUp N-goroutine race: exactly one wins
//   - AppendTodo: atomic append, validates text limits, persists
//   - AddDependency: idempotent, same-graph cycle guard, cross-workspace allowed
//   - DAG depth-50 chain triggers ErrBlockedByDepthExceeded
//   - DropOrphanEdges: flocked write path (flock branch in DropOrphanEdges)
//   - cascadeDeleteEdges: multi-dep where remaining deps are all done → reports
//     unblocked; remaining dep NOT done → not reported
//   - AdvanceBlockedDependents: dep-deleted (gone from graph) path
//   - Filter: AgentID, PlanID, CreatedBy, BlockedByID, ParentTaskIDSet
//   - priority sort order and List differentiation
//   - normalize length limits (title 200, description 2000, prompt 10000,
//     result 50000)
//   - trigger: recurring cron expr with < 60s interval rejected; bad cron string
//     rejected; manual trigger with no config accepted
//   - validateTransition internal hatch: internal=true permits blocked↔*
//   - updateLocked partial update fields: Trigger clear, Artifacts, SessionID,
//     StartedAt, CompletedAt, SourceChannel, SourceChatID, PlanID, Due

package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- helpers ---------------------------------------------------------------

func mustCreate(t *testing.T, s *Store, tk *Task) {
	t.Helper()
	require.NoError(t, s.Create(tk), "Create")
}

func ptr[T any](v T) *T { return &v }

// newBlockedTask creates a task in StatusBlocked with given deps.
func newBlockedTask(title, ws string, deps []string) *Task {
	return &Task{
		Title:       title,
		Action:      ActionLLM,
		WorkspaceID: ws,
		Status:      StatusBlocked,
		BlockedBy:   deps,
	}
}

// ---- task.go helpers -------------------------------------------------------

func TestIsTerminal(t *testing.T) {
	// Traces to: task.go line 60 — IsTerminal
	assert.True(t, IsTerminal(StatusDone), "done is terminal")
	assert.True(t, IsTerminal(StatusFailed), "failed is terminal")
	assert.False(t, IsTerminal(StatusInbox), "inbox is not terminal")
	assert.False(t, IsTerminal(StatusNext), "next is not terminal")
	assert.False(t, IsTerminal(StatusInProgress), "in_progress is not terminal")
	assert.False(t, IsTerminal(StatusBlocked), "blocked is not terminal")
}

func TestEffectivePriorityUnset(t *testing.T) {
	// Traces to: task.go line 201 — EffectivePriority defaults 0 → 3
	tk := &Task{Priority: 0}
	assert.Equal(t, 3, tk.EffectivePriority(), "unset priority defaults to 3")

	tk2 := &Task{Priority: 1}
	assert.Equal(t, 1, tk2.EffectivePriority(), "set priority preserved")

	tk5 := &Task{Priority: 5}
	assert.Equal(t, 5, tk5.EffectivePriority(), "priority 5 preserved")
}

func TestEffectiveSurfaceEmpty(t *testing.T) {
	// Traces to: task.go line 210 — EffectiveSurface defaults empty → user
	tk := &Task{}
	assert.Equal(t, SurfaceUser, tk.EffectiveSurface(), "empty surface defaults to user")

	tk2 := &Task{Surface: SurfaceHeartbeat}
	assert.Equal(t, SurfaceHeartbeat, tk2.EffectiveSurface(), "heartbeat surface preserved")
}

func TestCreatedTime(t *testing.T) {
	// Traces to: task.go line 219 — CreatedTime
	tk := &Task{CreatedAt: "2026-01-15T10:30:00Z"}
	ct := tk.CreatedTime()
	assert.Equal(t, 2026, ct.Year(), "parsed year")
	assert.Equal(t, time.January, ct.Month(), "parsed month")
	assert.Equal(t, 15, ct.Day(), "parsed day")

	// Unparseable falls back to zero time.
	bad := &Task{CreatedAt: "not-a-date"}
	assert.True(t, bad.CreatedTime().IsZero(), "bad date yields zero time")

	// Empty string also yields zero time.
	empty := &Task{}
	assert.True(t, empty.CreatedTime().IsZero(), "empty CreatedAt yields zero time")
}

// ---- validateID ------------------------------------------------------------

func TestValidateID_PathTraversalRejection(t *testing.T) {
	// Traces to: store.go line 72 — validateID path-safety
	bad := []string{
		"a/b",
		"a\\b",
		"../secret",
		"a\x00b",
		"../../etc/passwd",
	}
	for _, id := range bad {
		t.Run(fmt.Sprintf("id=%q", id), func(t *testing.T) {
			err := validateID(id)
			require.Error(t, err, "expected rejection of %q", id)
		})
	}

	// Valid IDs must pass.
	good := []string{"abc", "task-123", "uuid-like-abc-def"}
	for _, id := range good {
		t.Run(fmt.Sprintf("valid=%q", id), func(t *testing.T) {
			require.NoError(t, validateID(id), "valid id %q", id)
		})
	}
}

// ---- Store.Dir / Lock / Exists ---------------------------------------------

func TestDirReturnsStoreDir(t *testing.T) {
	// Traces to: store.go line 69 — Dir()
	s := newStore(t)
	d := s.Dir()
	assert.NotEmpty(t, d, "Dir must not be empty")
	// Two different stores should have different dirs.
	s2 := newStore(t)
	assert.NotEqual(t, d, s2.Dir(), "dirs differ per store")
}

func TestLockSameIDSameMutex(t *testing.T) {
	// Traces to: store.go line 125 — Lock() returns per-id mutex
	s := newStore(t)
	m1 := s.Lock("task-aaa")
	m2 := s.Lock("task-aaa")
	// They must be the same pointer (same shard).
	assert.Equal(t, m1, m2, "same id → same mutex shard")

	// We can actually acquire and release it without deadlock.
	m1.Lock()
	m1.Unlock()
}

func TestExists(t *testing.T) {
	// Traces to: store.go line 798 — Exists()
	s := newStore(t)
	tk := mkTask("x", "ws")
	require.NoError(t, s.Create(tk))

	assert.True(t, s.Exists(tk.ID), "task exists after Create")
	assert.False(t, s.Exists("no-such-task"), "missing task does not exist")

	// Invalid ID (path traversal) must return false, not panic.
	assert.False(t, s.Exists("../etc"), "invalid id → false")
}

// ---- lifecycle transitions -------------------------------------------------

func TestValidateTransition_LegalTransitions(t *testing.T) {
	// Traces to: store.go line 452 — validateTransition legal paths
	legal := []struct{ from, to Status }{
		{StatusInbox, StatusNext},
		{StatusNext, StatusInProgress},
		{StatusInProgress, StatusDone},
		{StatusInProgress, StatusFailed},
		{StatusFailed, StatusInbox},      // retry path
		{StatusFailed, StatusNext},       // retry path
		{StatusFailed, StatusInProgress}, // direct re-run path (SPA ▶ Run on a genuinely-failed task; ADR-052 §6.8)
		{StatusInbox, StatusInbox},       // no-op
		{StatusDone, StatusDone},         // no-op
	}
	for _, tc := range legal {
		t.Run(fmt.Sprintf("%s→%s", tc.from, tc.to), func(t *testing.T) {
			err := validateTransition(tc.from, tc.to, false)
			require.NoError(t, err, "%s→%s should be legal", tc.from, tc.to)
		})
	}
}

func TestValidateTransition_DoneIsFrozen(t *testing.T) {
	// Traces to: store.go line 459 — done is terminal, no transition out
	frozen := []Status{StatusInbox, StatusNext, StatusInProgress, StatusFailed}
	for _, to := range frozen {
		t.Run(fmt.Sprintf("done→%s", to), func(t *testing.T) {
			err := validateTransition(StatusDone, to, false)
			require.Error(t, err, "done→%s should be forbidden", to)
			assert.True(t, errors.Is(err, ErrIllegalTransition), "must be ErrIllegalTransition")
		})
	}
}

func TestValidateTransition_BlockedClearsOnlyViaInternal(t *testing.T) {
	// Traces to: store.go line 465 — leaving blocked requires internal hatch
	// External (internal=false): blocked→next is rejected.
	err := validateTransition(StatusBlocked, StatusNext, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrIllegalTransition))

	// Internal (internal=true): blocked→next is permitted.
	err = validateTransition(StatusBlocked, StatusNext, true)
	require.NoError(t, err, "internal hatch allows leaving blocked")

	// Internal hatch also allows entering blocked.
	err = validateTransition(StatusNext, StatusBlocked, true)
	require.NoError(t, err, "internal hatch allows entering blocked")
}

func TestBlockedNotSettableFromWire(t *testing.T) {
	// Traces to: store.go line 43 — ErrBlockedNotSettable
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusNext
	mustCreate(t, s, tk)

	_, err := s.Update(tk.ID, Patch{Status: ptr(StatusBlocked)})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBlockedNotSettable),
		"wire set of blocked must return ErrBlockedNotSettable, got: %v", err)
	// ErrBlockedNotSettable wraps ErrValidation.
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestDoneTransitionFrozenViaUpdate(t *testing.T) {
	// Differentiation: done→inbox rejected, done→done (no-op) accepted.
	// Traces to: store.go line 530 — Update validates transition
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusInProgress
	mustCreate(t, s, tk)

	// Transition to done.
	mustUpdate(t, s, tk.ID, Patch{Status: ptr(StatusDone)})

	// Attempt to move out of done — must fail.
	_, err := s.Update(tk.ID, Patch{Status: ptr(StatusInbox)})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrIllegalTransition), "done→inbox must be ErrIllegalTransition")

	// No-op update on done is OK.
	got, err := s.Update(tk.ID, Patch{Status: ptr(StatusDone)})
	require.NoError(t, err)
	assert.Equal(t, StatusDone, got.Status)
}

func TestFailedCanBeRetried(t *testing.T) {
	// Traces to: validateTransition — failed is NOT frozen
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusInProgress
	mustCreate(t, s, tk)

	mustUpdate(t, s, tk.ID, Patch{Status: ptr(StatusFailed)})

	// failed→inbox is permitted (retry).
	got, err := s.Update(tk.ID, Patch{Status: ptr(StatusInbox)})
	require.NoError(t, err, "failed→inbox retry should be allowed")
	assert.Equal(t, StatusInbox, got.Status)
}

// ---- AdvanceUnblocked ------------------------------------------------------

func TestAdvanceUnblocked_NoopOnNonBlocked(t *testing.T) {
	// Traces to: store.go line 778 — AdvanceUnblocked no-op when not blocked
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusNext
	mustCreate(t, s, tk)

	got, err := s.AdvanceUnblocked(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusNext, got.Status, "no-op on next status")
}

func TestAdvanceUnblocked_TransitionsBlockedToNext(t *testing.T) {
	// Traces to: store.go line 778 — AdvanceUnblocked transitions blocked→next
	s := newStore(t)
	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)
	// Mark dep done first so the blocked_by edge is valid at Create time.
	mustUpdate(t, s, dep.ID, Patch{Status: ptr(StatusDone)})

	// Blocked task: its dep is now done so recompute won't flip it back.
	tk := newBlockedTask("blocked-task", "ws", []string{dep.ID})
	mustCreate(t, s, tk)

	got, err := s.AdvanceUnblocked(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusNext, got.Status, "AdvanceUnblocked must move blocked→next")
}

// ---- SpawnReset ------------------------------------------------------------

func TestSpawnReset_ClearsRunFields(t *testing.T) {
	// Traces to: store.go line 743 — SpawnReset clears session/result/timestamps
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusDone
	mustCreate(t, s, tk)

	// Direct: write the run fields directly to bypass the public API.
	const at = "2026-01-01T00:00:00Z"
	tk2, _ := s.Get(tk.ID)
	tk2.SessionID = "sess-abc"
	tk2.Result = "old-result"
	tk2.Artifacts = []string{"a.txt"}
	tk2.StartedAt = at
	tk2.CompletedAt = at
	tk2.FollowedUp = true
	require.NoError(t, s.write(tk2))

	// SpawnReset should succeed on non-in_progress task.
	reset, err := s.SpawnReset(tk.ID)
	require.NoError(t, err, "SpawnReset on done task")
	assert.Equal(t, StatusNext, reset.Status, "status reset to next")
	assert.Empty(t, reset.SessionID, "SessionID cleared")
	assert.Empty(t, reset.Result, "Result cleared")
	assert.Nil(t, reset.Artifacts, "Artifacts cleared")
	assert.Empty(t, reset.StartedAt, "StartedAt cleared")
	assert.Empty(t, reset.CompletedAt, "CompletedAt cleared")
	assert.False(t, reset.FollowedUp, "FollowedUp cleared")

	// Verify persistence.
	got, _ := s.Get(tk.ID)
	assert.Equal(t, StatusNext, got.Status)
	assert.Empty(t, got.Result)
}

func TestSpawnReset_RejectsInProgress(t *testing.T) {
	// Traces to: store.go line 755 — ErrAlreadyRunning guard
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusNext
	mustCreate(t, s, tk)

	// Claim it so it's in_progress.
	_, err := s.ClaimForRun(tk.ID, time.Now())
	require.NoError(t, err)

	_, err = s.SpawnReset(tk.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRunning), "in_progress → ErrAlreadyRunning")
}

// ---- AppendTodo ------------------------------------------------------------

func TestAppendTodo_AtomicAppend(t *testing.T) {
	// Traces to: store.go line 647 — AppendTodo atomic mutator
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	// Start empty — append two todos and verify both are persisted.
	got1, err := s.AppendTodo(tk.ID, Todo{Text: "first item", Status: TodoPending})
	require.NoError(t, err)
	assert.Len(t, got1.Todos, 1, "one todo after first append")
	assert.Equal(t, "first item", got1.Todos[0].Text)

	got2, err := s.AppendTodo(tk.ID, Todo{Text: "second item", Status: TodoCompleted})
	require.NoError(t, err)
	assert.Len(t, got2.Todos, 2, "two todos after second append")
	assert.Equal(t, "second item", got2.Todos[1].Text)
	assert.Equal(t, TodoCompleted, got2.Todos[1].Status)

	// Persistence: re-read from disk.
	persisted, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Len(t, persisted.Todos, 2)
}

func TestAppendTodo_ValidatesTextLimit(t *testing.T) {
	// Traces to: store.go line 282 — validateTodos, 500-rune text limit
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	long := strings.Repeat("x", 501)
	_, err := s.AppendTodo(tk.ID, Todo{Text: long})
	require.Error(t, err, "501-char todo text must be rejected")
	assert.True(t, errors.Is(err, ErrValidation))

	// Empty text must be rejected.
	_, err = s.AppendTodo(tk.ID, Todo{Text: ""})
	require.Error(t, err, "empty todo text must be rejected")
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestAppendTodo_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.AppendTodo("nonexistent-task", Todo{Text: "x"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

// ---- SetTodos ---------------------------------------------------------------

func TestSetTodos_FullReplace(t *testing.T) {
	// SetTodos with 3 items, then replace with 2 → exactly 2 remain.
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	three := []Todo{
		{Text: "step one", Status: TodoPending},
		{Text: "step two", Status: TodoInProgress},
		{Text: "step three", Status: TodoCompleted},
	}
	got, err := s.SetTodos(tk.ID, three)
	require.NoError(t, err, "SetTodos with 3 items must succeed")
	require.Len(t, got.Todos, 3, "3 todos after first SetTodos")

	two := []Todo{
		{Text: "only one", Status: TodoPending},
		{Text: "only two", Status: TodoCompleted},
	}
	got, err = s.SetTodos(tk.ID, two)
	require.NoError(t, err, "SetTodos with 2 items must succeed")
	require.Len(t, got.Todos, 2, "exactly 2 todos after second SetTodos (full-replace)")
	assert.Equal(t, "only one", got.Todos[0].Text)
	assert.Equal(t, TodoPending, got.Todos[0].Status)
	assert.Equal(t, "only two", got.Todos[1].Text)
	assert.Equal(t, TodoCompleted, got.Todos[1].Status)

	// Persistence: re-read from disk.
	persisted, err := s.Get(tk.ID)
	require.NoError(t, err)
	require.Len(t, persisted.Todos, 2, "exactly 2 todos must persist to disk")
}

func TestSetTodos_ClearsChecklist(t *testing.T) {
	// SetTodos with an empty slice clears the checklist.
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Todos = []Todo{{Text: "existing", Status: TodoPending}}
	mustCreate(t, s, tk)

	got, err := s.SetTodos(tk.ID, []Todo{})
	require.NoError(t, err)
	assert.Empty(t, got.Todos, "checklist must be empty after SetTodos([])")
}

func TestSetTodos_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.SetTodos("nonexistent-task", []Todo{{Text: "x", Status: TodoPending}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

// ---- UnmarshalJSON legacy compatibility ------------------------------------

func TestTodoUnmarshalJSON_LegacyDoneTrue(t *testing.T) {
	// Legacy disk format: {"text":"x","done":true} → Status == completed.
	var td Todo
	require.NoError(t, json.Unmarshal([]byte(`{"text":"x","done":true}`), &td))
	assert.Equal(t, "x", td.Text)
	assert.Equal(t, TodoCompleted, td.Status)
}

func TestTodoUnmarshalJSON_LegacyDoneFalse(t *testing.T) {
	// Legacy disk format: {"text":"y","done":false} → Status == pending.
	var td Todo
	require.NoError(t, json.Unmarshal([]byte(`{"text":"y","done":false}`), &td))
	assert.Equal(t, "y", td.Text)
	assert.Equal(t, TodoPending, td.Status)
}

func TestTodoUnmarshalJSON_NewStatus(t *testing.T) {
	// New format: {"text":"z","status":"in_progress"} → Status == in_progress.
	var td Todo
	require.NoError(t, json.Unmarshal([]byte(`{"text":"z","status":"in_progress"}`), &td))
	assert.Equal(t, "z", td.Text)
	assert.Equal(t, TodoInProgress, td.Status)
}

func TestTodoUnmarshalJSON_NoFields_DefaultsPending(t *testing.T) {
	// Neither done nor status → defaults to pending.
	var td Todo
	require.NoError(t, json.Unmarshal([]byte(`{"text":"w"}`), &td))
	assert.Equal(t, TodoPending, td.Status)
}

func TestTodoUnmarshalJSON_InvalidStatus_DefaultsPending(t *testing.T) {
	// Fix 5: an unrecognized status value (e.g. "WIP" written by a corrupt writer)
	// must default to TodoPending rather than producing an invalid in-memory status
	// that escapes write-time validation and makes the SPA Zod schema drop the payload.
	var td Todo
	require.NoError(t, json.Unmarshal([]byte(`{"text":"corrupt","status":"WIP"}`), &td))
	assert.Equal(t, "corrupt", td.Text)
	assert.Equal(t, TodoPending, td.Status,
		"invalid status %q must be clamped to pending on unmarshal", "WIP")
}

// ---- validateTodos status validation ---------------------------------------

func TestValidateTodos_RejectsEmptyStatus(t *testing.T) {
	// Empty status is invalid at tool/REST input time.
	err := validateTodos([]Todo{{Text: "x", Status: ""}})
	require.Error(t, err, "empty status must be rejected")
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestValidateTodos_RejectsUnknownStatus(t *testing.T) {
	// Unknown status string is invalid.
	err := validateTodos([]Todo{{Text: "x", Status: "done"}})
	require.Error(t, err, `"done" is not a valid TodoStatus`)
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestValidateTodos_AcceptsAllValidStatuses(t *testing.T) {
	// All three canonical statuses must be accepted.
	for _, st := range []TodoStatus{TodoPending, TodoInProgress, TodoCompleted} {
		err := validateTodos([]Todo{{Text: "x", Status: st}})
		assert.NoError(t, err, "status %q must be valid", st)
	}
}

// ---- AddDependency ---------------------------------------------------------

func TestAddDependency_Idempotent(t *testing.T) {
	// Traces to: store.go line 678 — AddDependency idempotent
	s := newStore(t)
	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)
	tk := mkTask("dependent", "ws")
	mustCreate(t, s, tk)

	_, added1, err := s.AddDependency(tk.ID, dep.ID)
	require.NoError(t, err)
	assert.True(t, added1, "first add returns added=true")

	_, added2, err := s.AddDependency(tk.ID, dep.ID)
	require.NoError(t, err)
	assert.False(t, added2, "re-add returns added=false (idempotent)")

	// Only one edge in blocked_by.
	got, _ := s.Get(tk.ID)
	assert.Len(t, got.BlockedBy, 1)
}

func TestAddDependency_CycleGuard(t *testing.T) {
	// Traces to: store.go line 678 — AddDependency validates DAG
	s := newStore(t)
	a := mkTask("a", "ws")
	b := mkTask("b", "ws")
	mustCreate(t, s, a)
	mustCreate(t, s, b)

	// a depends on b.
	_, _, err := s.AddDependency(a.ID, b.ID)
	require.NoError(t, err)

	// b depends on a — cycle: must be rejected.
	_, _, err = s.AddDependency(b.ID, a.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBlockedByCycle), "AddDependency cycle guard")
}

func TestAddDependency_UpdatesBlockedState(t *testing.T) {
	// Traces to: store.go line 701 — recomputeBlockedStateLocked via AddDependency
	s := newStore(t)
	dep := mkTask("dep", "ws")
	dep.Status = StatusInbox // not done
	mustCreate(t, s, dep)

	tk := mkTask("tk", "ws")
	tk.Status = StatusNext
	mustCreate(t, s, tk)

	// Adding an unmet dependency should flip status to blocked.
	updated, _, err := s.AddDependency(tk.ID, dep.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusBlocked, updated.Status, "adding unmet dep should set status=blocked")
}

func TestAddDependency_NonexistentTarget(t *testing.T) {
	s := newStore(t)
	tk := mkTask("tk", "ws")
	mustCreate(t, s, tk)

	_, _, err := s.AddDependency(tk.ID, "ghost-id")
	require.Error(t, err, "should reject reference to missing task")
}

// ---- DAG depth-50 chain ----------------------------------------------------

func TestBlockedByDepthExceeded(t *testing.T) {
	// Traces to: blocked_by.go line 43 — ErrBlockedByDepthExceeded (max 50)
	//
	// forwardDepth(newDeps, 0, ...) walks the BlockedBy chains of the proposed
	// deps starting at depth=0. A chain tasks[0]←tasks[1]←…←tasks[50] has 50
	// links. When we try to add tasks[51]→tasks[50], forwardDepth([tasks[50]], 0)
	// walks to tasks[49]…tasks[0]: that is 50 levels → depth 50 returned.
	// 50 > 50 is false so it passes. We need one more level.
	//
	// With a chain of 52 tasks (tasks[0..51]) and 51 links:
	//   tasks[1]→tasks[0], …, tasks[51]→tasks[50]  ← we try to add this last one
	// forwardDepth([tasks[50]], 0) walks tasks[50]→tasks[49]→…→tasks[0]: 51 levels
	// → depth 51 > 50 → ErrBlockedByDepthExceeded.
	s := newStore(t)

	const n = 52
	tasks := make([]*Task, n)
	for i := 0; i < n; i++ {
		tasks[i] = mkTask(fmt.Sprintf("chain-%d", i), "ws")
		mustCreate(t, s, tasks[i])
	}

	// Build 50 links: tasks[1]→tasks[0] through tasks[50]→tasks[49].
	// This is a depth-50 chain (forwardDepth from tasks[50]'s deps = 50).
	for i := 1; i <= 50; i++ {
		deps := []string{tasks[i-1].ID}
		_, err := s.Update(tasks[i].ID, Patch{BlockedBy: &deps})
		require.NoError(t, err, "link tasks[%d]→tasks[%d] should succeed", i, i-1)
	}

	// tasks[51]→tasks[50]: forwardDepth([tasks[50]], 0) = 51 > 50 → rejected.
	deps := []string{tasks[50].ID}
	_, err := s.Update(tasks[51].ID, Patch{BlockedBy: &deps})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBlockedByDepthExceeded),
		"depth-51 chain must return ErrBlockedByDepthExceeded, got: %v", err)
}

// ---- DropOrphanEdges (flock path covered) ----------------------------------

func TestDropOrphanEdges_MultipleOrphans(t *testing.T) {
	// Traces to: blocked_by.go line 347 — DropOrphanEdges
	// Multiple edges from different tasks referencing the same orphan.
	s := newStore(t)
	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)

	a := mkTask("a", "ws")
	a.BlockedBy = []string{dep.ID}
	mustCreate(t, s, a)

	b := mkTask("b", "ws")
	b.BlockedBy = []string{dep.ID}
	mustCreate(t, s, b)

	// Remove dep's file out-of-band — both a and b have orphan edges now.
	require.NoError(t, removeFileRaw(s, dep.ID))

	removed, err := s.DropOrphanEdges()
	require.NoError(t, err)
	assert.Equal(t, 2, removed, "two orphan edges removed (one per task)")

	got, _ := s.Get(a.ID)
	assert.Empty(t, got.BlockedBy, "a.BlockedBy cleared")

	got2, _ := s.Get(b.ID)
	assert.Empty(t, got2.BlockedBy, "b.BlockedBy cleared")
}

func TestDropOrphanEdges_EmptyDir(t *testing.T) {
	// Traces to: blocked_by.go line 347 — DropOrphanEdges on empty dir
	s := newStore(t)
	// Dir doesn't exist yet.
	removed, err := s.DropOrphanEdges()
	require.NoError(t, err, "DropOrphanEdges on nonexistent dir is not an error")
	assert.Equal(t, 0, removed)
}

func TestDropOrphanEdges_WriteFailureSurfacesError(t *testing.T) {
	// Traces to: blocked_by.go line 385 — DropOrphanEdges write-failure
	// aggregation (same failedFiles/aggregated-error pattern as
	// cascadeDeleteEdges and AdvanceBlockedDependents, since this is the same
	// silent-continue-on-write-failure bug shape). A forced write failure on
	// the one file whose orphan edge needs dropping must not be silently
	// swallowed, and the returned removed-count must not claim an edge was
	// dropped when it was never actually persisted to disk.
	if os.Geteuid() == 0 {
		t.Skip("running as root — directory permissions are bypassed; cannot trigger write failure")
	}
	s := newStore(t)

	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)

	a := mkTask("a", "ws")
	a.BlockedBy = []string{dep.ID}
	mustCreate(t, s, a)

	// Remove dep's file out-of-band so `a`'s edge becomes an orphan.
	require.NoError(t, removeFileRaw(s, dep.ID))

	// Force every subsequent write in the store dir to fail: WriteFileAtomic
	// stages its temp file in the same directory as the target, so removing
	// write permission on the dir blocks os.CreateTemp regardless of the
	// target file's own permissions.
	require.NoError(t, os.Chmod(s.dir, 0o500))
	t.Cleanup(func() {
		// Restore writable mode so t.TempDir cleanup can remove it.
		_ = os.Chmod(s.dir, 0o700)
	})

	removed, err := s.DropOrphanEdges()
	require.Error(t, err, "a forced write failure must not silently succeed")
	assert.ErrorIs(t, err, ErrOrphanEdgeCleanupFailed,
		"error must wrap ErrOrphanEdgeCleanupFailed so callers can identify a partial cleanup failure")
	assert.Contains(t, err.Error(), a.ID+".json", "error must name the file whose write failed")
	assert.Equal(t, 0, removed, "an edge whose write failed must not be counted as removed")

	// Restore permissions so we can verify the on-disk state directly: the
	// orphan edge must still be present since the write never landed.
	require.NoError(t, os.Chmod(s.dir, 0o700))
	got, gerr := s.Get(a.ID)
	require.NoError(t, gerr)
	assert.Equal(t, []string{dep.ID}, got.BlockedBy,
		"the orphan edge must remain on disk since the write never landed")
}

// ---- cascadeDeleteEdges multi-dep logic ------------------------------------

func TestCascadeDeleteEdges_MultiDepRemainingDone(t *testing.T) {
	// Traces to: blocked_by.go line 264 — cascadeDeleteEdges unblock check
	// Scenario: task C depends on [A, B]. B is deleted. A is already done.
	// After deleting B, C's remaining deps = [A] which is done → C is unblocked.
	s := newStore(t)

	a := mkTask("a", "ws")
	b := mkTask("b", "ws")
	mustCreate(t, s, a)
	mustCreate(t, s, b)

	// Mark A done.
	mustUpdate(t, s, a.ID, Patch{Status: ptr(StatusDone)})

	// C depends on both A and B.
	c := mkTask("c", "ws")
	c.Status = StatusBlocked
	c.BlockedBy = []string{a.ID, b.ID}
	mustCreate(t, s, c)

	// Delete B — cascade should report C as unblocked (A is done).
	unblocked, err := s.Delete(b.ID)
	require.NoError(t, err)
	assert.Contains(t, unblocked, c.ID, "C should be reported as unblocked")
}

func TestCascadeDeleteEdges_MultiDepRemainingNotDone(t *testing.T) {
	// Traces to: blocked_by.go line 264 — cascadeDeleteEdges, remaining dep NOT done
	// Scenario: task C depends on [A, B]. B is deleted. A is NOT done.
	// After deleting B, C still has an unmet dep → NOT reported as unblocked.
	s := newStore(t)

	a := mkTask("a", "ws")
	b := mkTask("b", "ws")
	mustCreate(t, s, a)
	mustCreate(t, s, b)
	// A remains inbox (not done).

	c := mkTask("c", "ws")
	c.Status = StatusBlocked
	c.BlockedBy = []string{a.ID, b.ID}
	mustCreate(t, s, c)

	unblocked, err := s.Delete(b.ID)
	require.NoError(t, err)
	assert.NotContains(t, unblocked, c.ID, "C should NOT be unblocked when A is still unmet")
}

func TestCascadeDeleteEdges_WriteFailureSurfacesError(t *testing.T) {
	// Traces to: blocked_by.go line 271 — cascadeDeleteEdges write-failure
	// aggregation (sibling fix to AdvanceBlockedDependents's failedIDs/
	// aggregated-error pattern). A forced write failure on the one dependent
	// whose blocked_by edge needs rewriting must NOT be silently swallowed:
	// cascadeDeleteEdges must return a non-nil error wrapping
	// ErrCascadeEdgeCleanupFailed, and must NOT report that dependent as
	// unblocked, since its edge rewrite never actually landed on disk.
	if os.Geteuid() == 0 {
		t.Skip("running as root — directory permissions are bypassed; cannot trigger write failure")
	}
	s := newStore(t)

	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)

	// C depends solely on dep; deleting dep would normally leave C fully
	// unblocked (empty blocked_by) — IF the edge rewrite succeeds.
	c := mkTask("c", "ws")
	c.Status = StatusBlocked
	c.BlockedBy = []string{dep.ID}
	mustCreate(t, s, c)

	// Simulate the state cascadeDeleteEdges runs in when called from
	// Store.Delete: dep's own file has already been removed by the time this
	// fires.
	require.NoError(t, removeFileRaw(s, dep.ID))

	// Force every subsequent write in the store dir to fail: WriteFileAtomic
	// stages its temp file in the same directory as the target, so removing
	// write permission on the dir blocks os.CreateTemp regardless of the
	// target file's own permissions.
	require.NoError(t, os.Chmod(s.dir, 0o500))
	t.Cleanup(func() {
		// Restore writable mode so t.TempDir cleanup can remove it.
		_ = os.Chmod(s.dir, 0o700)
	})

	unblocked, err := s.cascadeDeleteEdges(dep.ID)
	require.Error(t, err, "a forced write failure must not silently succeed")
	assert.ErrorIs(t, err, ErrCascadeEdgeCleanupFailed,
		"error must wrap ErrCascadeEdgeCleanupFailed so callers can distinguish it from a hard delete failure")
	assert.Contains(t, err.Error(), c.ID, "error must name the dependent whose write failed")
	assert.NotContains(t, unblocked, c.ID,
		"a dependent whose edge write failed must not be reported as unblocked")
}

// ---- AdvanceBlockedDependents dep-deleted path -----------------------------

func TestAdvanceBlockedDependents_DepDeleted(t *testing.T) {
	// Traces to: blocked_by.go line 183 — dep gone from graph
	// When the completed dep was already removed from disk, readStatus returns
	// (_, false) and the task stays blocked. This tests the "ok=false → not done"
	// branch.
	s := newStore(t)

	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)

	blocked := newBlockedTask("blocked", "ws", []string{dep.ID})
	mustCreate(t, s, blocked)

	// Remove dep's file directly WITHOUT calling Delete (so no cascade-clean).
	require.NoError(t, removeFileRaw(s, dep.ID))

	// AdvanceBlockedDependents with dep gone: readStatus returns false → not done
	// → blocked task must NOT be advanced.
	advanced, err := s.AdvanceBlockedDependents(dep.ID)
	require.NoError(t, err)
	assert.Empty(t, advanced, "deleted dep = not done → blocked task must not advance")
}

// ---- Filter fields not covered by existing tests --------------------------

func TestListFilterByAgentID(t *testing.T) {
	// Traces to: store.go line 150 — Filter.AgentID match
	s := newStore(t)
	for _, ag := range []string{"agent-1", "agent-1", "agent-2"} {
		tk := mkTask("t", "ws")
		tk.AgentID = ag
		mustCreate(t, s, tk)
	}
	got, err := s.List(Filter{AgentID: "agent-1"})
	require.NoError(t, err)
	assert.Len(t, got, 2, "agent-1 filter returns 2 tasks")

	got2, _ := s.List(Filter{AgentID: "agent-2"})
	assert.Len(t, got2, 1, "agent-2 filter returns 1 task")
	// Differentiation: different agent IDs return different results.
	assert.NotEqual(t, got[0].AgentID, got2[0].AgentID)
}

func TestListFilterByPlanID(t *testing.T) {
	// Traces to: store.go — Filter.PlanID match (ADR-049 D1, replaces the
	// removed Filter.MilestoneID)
	s := newStore(t)
	ms := mkTask("t", "ws")
	ms.PlanID = "plan-1"
	mustCreate(t, s, ms)

	other := mkTask("other", "ws")
	mustCreate(t, s, other)

	got, _ := s.List(Filter{PlanID: "plan-1"})
	require.Len(t, got, 1)
	assert.Equal(t, "plan-1", got[0].PlanID)
}

func TestListFilterByTag(t *testing.T) {
	// Traces to: store.go — Filter.Tag match (ADR-049 D1)
	s := newStore(t)
	tagged := mkTask("t", "ws")
	tagged.Tags = []string{"release-42"}
	mustCreate(t, s, tagged)

	other := mkTask("other", "ws")
	mustCreate(t, s, other)

	got, _ := s.List(Filter{Tag: "release-42"})
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Tags, "release-42")
}

func TestListFilterByCreatedBy(t *testing.T) {
	// Traces to: store.go line 150 — Filter.CreatedBy match
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.CreatedBy = "user-42"
	mustCreate(t, s, tk)

	other := mkTask("other", "ws")
	other.CreatedBy = "user-99"
	mustCreate(t, s, other)

	got, _ := s.List(Filter{CreatedBy: "user-42"})
	require.Len(t, got, 1)
	assert.Equal(t, "user-42", got[0].CreatedBy)

	// Differentiation: different user returns different result.
	got2, _ := s.List(Filter{CreatedBy: "user-99"})
	assert.NotEqual(t, got[0].ID, got2[0].ID)
}

func TestListFilterByBlockedByID(t *testing.T) {
	// Traces to: store.go line 150 — Filter.BlockedByID match
	s := newStore(t)
	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)

	blocked := newBlockedTask("blocked", "ws", []string{dep.ID})
	mustCreate(t, s, blocked)

	unrelated := mkTask("unrelated", "ws")
	mustCreate(t, s, unrelated)

	got, err := s.List(Filter{BlockedByID: dep.ID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, blocked.ID, got[0].ID)
}

func TestListPrioritySort(t *testing.T) {
	// Traces to: store.go line 209 — priority ASC sort
	s := newStore(t)
	for _, p := range []int{5, 1, 3} {
		tk := mkTask(fmt.Sprintf("p%d", p), "ws")
		tk.Priority = p
		mustCreate(t, s, tk)
		// Small sleep to force distinct CreatedAt.
		time.Sleep(time.Millisecond)
	}
	got, err := s.List(Filter{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	// Priority 1 first, then 3, then 5.
	assert.Equal(t, 1, got[0].Priority, "highest-priority task first")
	assert.Equal(t, 3, got[1].Priority)
	assert.Equal(t, 5, got[2].Priority)
}

func TestListFilterStatus(t *testing.T) {
	// Differentiation test: different status filters return different sets.
	s := newStore(t)
	for _, st := range []Status{StatusInbox, StatusNext, StatusDone} {
		tk := mkTask("t", "ws")
		tk.Status = st
		if st == StatusDone {
			// Done tasks need to go through in_progress first — just seed directly.
			tk.Status = StatusInProgress
			mustCreate(t, s, tk)
			mustUpdate(t, s, tk.ID, Patch{Status: ptr(StatusDone)})
		} else {
			mustCreate(t, s, tk)
		}
	}
	inbox, _ := s.List(Filter{Status: StatusInbox})
	next, _ := s.List(Filter{Status: StatusNext})
	done, _ := s.List(Filter{Status: StatusDone})

	assert.Len(t, inbox, 1)
	assert.Len(t, next, 1)
	assert.Len(t, done, 1)
	// Differentiation: different status → different task IDs.
	assert.NotEqual(t, inbox[0].ID, next[0].ID)
	assert.NotEqual(t, next[0].ID, done[0].ID)
}

// ---- normalize length limits -----------------------------------------------

func TestNormalizeTitleTooLong(t *testing.T) {
	// Traces to: store.go line 234 — title 200-rune limit
	s := newStore(t)
	tk := mkTask(strings.Repeat("a", 201), "ws")
	err := s.Create(tk)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestNormalizeDescriptionTooLong(t *testing.T) {
	// Traces to: store.go line 261 — description 2000-char limit
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Description = strings.Repeat("x", 2001)
	err := s.Create(tk)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestNormalizePromptTooLong(t *testing.T) {
	// Traces to: store.go line 264 — prompt 10000-char limit
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Prompt = strings.Repeat("x", 10001)
	err := s.Create(tk)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestUpdateResultTooLong(t *testing.T) {
	// Traces to: store.go line 590 — result 50000-char limit via Update
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	_, err := s.Update(tk.ID, Patch{Result: ptr(strings.Repeat("x", 50001))})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestUpdateDescriptionTooLong(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	_, err := s.Update(tk.ID, Patch{Description: ptr(strings.Repeat("x", 2001))})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestUpdatePromptTooLong(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	_, err := s.Update(tk.ID, Patch{Prompt: ptr(strings.Repeat("x", 10001))})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestUpdateTitleEmpty(t *testing.T) {
	// Traces to: store.go line 496 — title must not be empty on update
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	_, err := s.Update(tk.ID, Patch{Title: ptr("")})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

// TestCreateTitleWhitespaceOnlyRejected is a regression test for the task-title
// sibling of S2 UAT finding B (an untrimmed `== ""` required-field check let a
// whitespace-only Plan title through as 201; the exact same pattern existed
// here — `t.Title == ""` never matched " \t "). A whitespace-only title must
// be rejected exactly like an empty one.
func TestCreateTitleWhitespaceOnlyRejected(t *testing.T) {
	s := newStore(t)
	tk := mkTask("   \t  ", "ws")
	err := s.Create(tk)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

// TestCreateTitleTrimmed proves the flip side: a legitimate title with
// incidental leading/trailing whitespace is accepted (not rejected) and
// persisted trimmed, not verbatim.
func TestCreateTitleTrimmed(t *testing.T) {
	s := newStore(t)
	tk := mkTask("  Analyze logs  ", "ws")
	require.NoError(t, s.Create(tk))
	assert.Equal(t, "Analyze logs", tk.Title, "in-memory Title must be trimmed")

	got, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, "Analyze logs", got.Title, "persisted Title must be trimmed")
}

// TestUpdateTitleWhitespaceOnlyRejected mirrors TestCreateTitleWhitespaceOnlyRejected
// for the Patch path (patch.Title's own untrimmed `== ""` check had the same gap).
func TestUpdateTitleWhitespaceOnlyRejected(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	_, err := s.Update(tk.ID, Patch{Title: ptr("   \t  ")})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

// TestUpdateTitleTrimmed mirrors TestCreateTitleTrimmed for the Patch path.
func TestUpdateTitleTrimmed(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	updated, err := s.Update(tk.ID, Patch{Title: ptr("  Renamed Task  ")})
	require.NoError(t, err)
	assert.Equal(t, "Renamed Task", updated.Title)

	got, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, "Renamed Task", got.Title, "persisted Title must be trimmed")
}

func TestUpdatePriorityOutOfRange(t *testing.T) {
	// Traces to: store.go line 539 — priority 1-5 on update
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	_, err := s.Update(tk.ID, Patch{Priority: ptr(0)})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))

	_, err = s.Update(tk.ID, Patch{Priority: ptr(6)})
	require.Error(t, err)
}

// ---- updateLocked patch fields ---------------------------------------------

func TestUpdatePatchAllScalarFields(t *testing.T) {
	// Traces to: store.go line 489 — updateLocked covers all patch fields
	s := newStore(t)
	tk := mkTask("original", "ws")
	mustCreate(t, s, tk)

	cron := "0 9 * * MON"
	trigger := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{CronExpr: &cron}}

	got, err := s.Update(tk.ID, Patch{
		Title:         ptr("updated"),
		Description:   ptr("desc"),
		Prompt:        ptr("prompt text"),
		AgentID:       ptr("agent-1"),
		Priority:      ptr(2),
		Due:           ptr("2026-12-31"),
		PlanID:        ptr("plan-1"),
		Surface:       ptr(SurfaceHeartbeat),
		SessionID:     ptr("sess-1"),
		StartedAt:     ptr("2026-01-01T00:00:00Z"),
		CompletedAt:   ptr("2026-01-02T00:00:00Z"),
		SourceChannel: ptr("telegram"),
		SourceChatID:  ptr("chat-999"),
		Trigger:       &trigger,
		Artifacts:     &[]string{"file1.txt", "file2.txt"},
		Result:        ptr("task completed"),
	})
	require.NoError(t, err)

	assert.Equal(t, "updated", got.Title)
	assert.Equal(t, "desc", got.Description)
	assert.Equal(t, "prompt text", got.Prompt)
	assert.Equal(t, "agent-1", got.AgentID)
	assert.Equal(t, 2, got.Priority)
	assert.Equal(t, "2026-12-31", got.Due)
	assert.Equal(t, "plan-1", got.PlanID)
	assert.Equal(t, SurfaceHeartbeat, got.Surface)
	assert.Equal(t, "sess-1", got.SessionID)
	assert.Equal(t, "2026-01-01T00:00:00Z", got.StartedAt)
	assert.Equal(t, "2026-01-02T00:00:00Z", got.CompletedAt)
	assert.Equal(t, "telegram", got.SourceChannel)
	assert.Equal(t, "chat-999", got.SourceChatID)
	assert.Len(t, got.Artifacts, 2)
	assert.Equal(t, "task completed", got.Result)
	require.NotNil(t, got.Trigger)
	assert.Equal(t, TriggerRecurring, got.Trigger.Type)

	// Verify persistence.
	reload, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated", reload.Title)
	assert.Equal(t, "agent-1", reload.AgentID)
}

func TestUpdateClearsTrigger(t *testing.T) {
	// Traces to: store.go line 565 — outer-nil trigger patch clears the field
	// Trigger: **Trigger where outer=non-nil, inner=nil means "clear".
	s := newStore(t)
	cron := "0 9 * * MON"
	tk := mkTask("t", "ws")
	tk.Trigger = &Trigger{Type: TriggerRecurring, Config: TriggerConfig{CronExpr: &cron}}
	mustCreate(t, s, tk)

	var nilTrigger *Trigger
	got, err := s.Update(tk.ID, Patch{Trigger: &nilTrigger})
	require.NoError(t, err)
	assert.Nil(t, got.Trigger, "trigger should be cleared by nil inner pointer")
}

func TestUpdateClearsArtifacts(t *testing.T) {
	// Traces to: store.go line 594 — empty artifacts slice cleared to nil
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Artifacts = []string{"a.txt"}
	mustCreate(t, s, tk)

	got, err := s.Update(tk.ID, Patch{Artifacts: &[]string{}})
	require.NoError(t, err)
	assert.Nil(t, got.Artifacts, "empty artifacts patch should clear field to nil")
}

func TestUpdateInvalidSurface(t *testing.T) {
	// Traces to: store.go line 581 — invalid surface rejected on update
	s := newStore(t)
	tk := mkTask("t", "ws")
	mustCreate(t, s, tk)

	_, err := s.Update(tk.ID, Patch{Surface: ptr(Surface("invalid-surface"))})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation))
}

// ---- trigger edge cases ----------------------------------------------------

func TestValidateTriggerRecurringTooFrequent(t *testing.T) {
	// Traces to: store.go line 338 — validateCronExpr < 60s interval rejected
	// A 6-field cron "* * * * * *" fires every second (sub-minute) → rejected.
	tooFrequent := "* * * * * *"
	tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{CronExpr: &tooFrequent}}
	err := ValidateTrigger(tr)
	require.Error(t, err, "sub-minute cron must be rejected")
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestValidateTriggerRecurringBadCronExpr(t *testing.T) {
	// Traces to: store.go line 338 — validateCronExpr invalid expression
	bad := "not a cron"
	tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{CronExpr: &bad}}
	err := ValidateTrigger(tr)
	require.Error(t, err, "invalid cron expression must be rejected")
	assert.True(t, errors.Is(err, ErrValidation))
}

func TestValidateTriggerManualNoConfig(t *testing.T) {
	// Traces to: store.go line 329 — manual trigger with no config is valid
	tr := &Trigger{Type: TriggerManual}
	err := ValidateTrigger(tr)
	require.NoError(t, err, "manual trigger with empty config must be valid")
}

func TestValidateTriggerEveryAtFloor(t *testing.T) {
	// Traces to: store.go line 319 — every_ms exactly 1000 is valid
	ms1000 := int64(1000)
	tr := &Trigger{Type: TriggerEvery, Config: TriggerConfig{EveryMs: &ms1000}}
	err := ValidateTrigger(tr)
	require.NoError(t, err, "every_ms=1000 is at the floor, must be valid")

	ms999 := int64(999)
	tr2 := &Trigger{Type: TriggerEvery, Config: TriggerConfig{EveryMs: &ms999}}
	err2 := ValidateTrigger(tr2)
	require.Error(t, err2, "every_ms=999 is below floor, must be rejected")
}

// ---- ClaimForRun concurrent N-goroutine race -------------------------------

func TestClaimForRun_ConcurrentRace(t *testing.T) {
	// Traces to: claim.go line 33 — ClaimForRun CAS: exactly one winner
	// Run with -race to verify no data races.
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusNext
	mustCreate(t, s, tk)

	const goroutines = 20
	var wins int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := s.ClaimForRun(tk.ID, time.Now())
			if err == nil {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), wins, "exactly one goroutine must win the claim")

	// Verify the task is in_progress on disk.
	got, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusInProgress, got.Status)
	assert.NotEmpty(t, got.StartedAt)
}

// ---- ClaimParentFollowUp concurrent N-goroutine race -----------------------

func TestClaimParentFollowUp_ConcurrentRace(t *testing.T) {
	// Traces to: claim.go line 61 — ClaimParentFollowUp CAS: exactly one winner
	s := newStore(t)
	p := mkTask("parent", "ws")
	mustCreate(t, s, p)

	const goroutines = 20
	var wins int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			won, err := s.ClaimParentFollowUp(p.ID)
			if err == nil && won {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), wins, "exactly one goroutine must win the follow-up claim")

	// FollowedUp must be persisted as true.
	got, err := s.Get(p.ID)
	require.NoError(t, err)
	assert.True(t, got.FollowedUp)
}

// ---- Get with invalid ID ---------------------------------------------------

func TestGetInvalidID(t *testing.T) {
	// Traces to: store.go line 220 — Get rejects bad IDs
	s := newStore(t)
	_, err := s.Get("../traversal")
	require.Error(t, err, "Get with path-traversal id must error")
}

// ---- List on nonexistent dir -----------------------------------------------

func TestListNonExistentDir(t *testing.T) {
	// Traces to: store.go line 188 — List on missing dir returns empty slice
	s := New("/tmp/nonexistent-omnipus-test-dir-xyz-" + fmt.Sprint(time.Now().UnixNano()))
	tasks, err := s.List(Filter{})
	require.NoError(t, err, "List on nonexistent dir must not error")
	assert.Empty(t, tasks)
}

// ---- checkParentAcyclicLocked — parent chain depth exceeded ----------------

func TestParentChainDepthExceeded(t *testing.T) {
	// Traces to: blocked_by.go line 147 — maxParentDepth check
	// Build a deep parent chain: t0 ← t1 ← … ← t50 (50 levels deep).
	s := newStore(t)

	tasks := make([]*Task, 51)
	tasks[0] = mkTask("root", "ws")
	mustCreate(t, s, tasks[0])

	for i := 1; i < 51; i++ {
		tasks[i] = mkTask(fmt.Sprintf("child-%d", i), "ws")
		tasks[i].ParentTaskID = tasks[i-1].ID
		mustCreate(t, s, tasks[i])
	}

	// Adding one more level should exceed maxParentDepth (50).
	deepChild := mkTask("too-deep", "ws")
	deepChild.ParentTaskID = tasks[50].ID
	err := s.Create(deepChild)
	require.Error(t, err, "parent chain depth=51 must be rejected")
	assert.True(t, errors.Is(err, ErrParentCycle), "must return ErrParentCycle")
}

// ---- recomputeBlockedStateLocked direct paths ------------------------------

func TestRecomputeBlockedState_NextWithAllDepsDone_StaysNext(t *testing.T) {
	// Traces to: store.go line 716 — recomputeBlockedStateLocked
	// When a next task's every dep is done, AddDependency should not flip to blocked.
	s := newStore(t)
	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)
	mustUpdate(t, s, dep.ID, Patch{Status: ptr(StatusDone)})

	tk := mkTask("tk", "ws")
	tk.Status = StatusNext
	mustCreate(t, s, tk)

	// Adding a done dep: next task should STAY next (all deps are done).
	updated, _, err := s.AddDependency(tk.ID, dep.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusNext, updated.Status, "next task with all-done deps stays next")
}

func TestRecomputeBlockedState_BlockedWithAllDepsDone_ClearsToNext(t *testing.T) {
	// Traces to: store.go line 728 — !anyUnmet && blocked → next
	s := newStore(t)
	dep := mkTask("dep", "ws")
	mustCreate(t, s, dep)

	// Manually create a blocked task.
	tk := newBlockedTask("tk", "ws", []string{dep.ID})
	mustCreate(t, s, tk)

	// Mark dep done.
	mustUpdate(t, s, dep.ID, Patch{Status: ptr(StatusDone)})

	// Now add another dep that IS done (to trigger recompute via AddDependency).
	dep2 := mkTask("dep2", "ws")
	mustCreate(t, s, dep2)
	mustUpdate(t, s, dep2.ID, Patch{Status: ptr(StatusDone)})

	// Adding a done dep to a blocked task with all-done existing deps → next.
	updated, _, err := s.AddDependency(tk.ID, dep2.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusNext, updated.Status, "blocked→next when all deps are done via AddDependency")
}

// ---- Persistence test (write-then-read-back) --------------------------------

func TestCreatePersistsAllFields(t *testing.T) {
	// Differentiation + persistence: two tasks with different data → different reads.
	s := newStore(t)

	cron := "0 9 * * MON"
	tk1 := &Task{
		Title:         "Task Alpha",
		Action:        ActionLLM,
		WorkspaceID:   "ws-1",
		Priority:      1,
		AgentID:       "agent-a",
		Description:   "desc alpha",
		Prompt:        "prompt alpha",
		Surface:       SurfaceHeartbeat,
		PlanID:        "plan-1",
		Due:           "2026-12-01",
		SourceChannel: "telegram",
		SourceChatID:  "chat-1",
		Trigger:       &Trigger{Type: TriggerRecurring, Config: TriggerConfig{CronExpr: &cron}},
		Todos:         []Todo{{Text: "alpha todo", Status: TodoPending}},
	}
	require.NoError(t, s.Create(tk1))

	tk2 := &Task{
		Title:       "Task Beta",
		Action:      ActionLLM,
		WorkspaceID: "ws-2",
		Priority:    5,
		AgentID:     "agent-b",
		Description: "desc beta",
	}
	require.NoError(t, s.Create(tk2))

	// IDs must differ.
	require.NotEqual(t, tk1.ID, tk2.ID)

	// Read back both and assert different content.
	r1, err := s.Get(tk1.ID)
	require.NoError(t, err)
	assert.Equal(t, "Task Alpha", r1.Title)
	assert.Equal(t, "ws-1", r1.WorkspaceID)
	assert.Equal(t, 1, r1.Priority)
	assert.Equal(t, "agent-a", r1.AgentID)
	assert.Equal(t, SurfaceHeartbeat, r1.Surface)
	assert.Equal(t, "plan-1", r1.PlanID)
	assert.Len(t, r1.Todos, 1)
	require.NotNil(t, r1.Trigger)
	assert.Equal(t, TriggerRecurring, r1.Trigger.Type)

	r2, err := s.Get(tk2.ID)
	require.NoError(t, err)
	assert.Equal(t, "Task Beta", r2.Title)
	assert.Equal(t, "ws-2", r2.WorkspaceID)
	assert.Equal(t, 5, r2.Priority)
	assert.Equal(t, "agent-b", r2.AgentID)
	assert.Nil(t, r2.Trigger)

	// Differentiation: key fields differ between the two.
	assert.NotEqual(t, r1.Title, r2.Title)
	assert.NotEqual(t, r1.WorkspaceID, r2.WorkspaceID)
	assert.NotEqual(t, r1.Priority, r2.Priority)
}

// ---- CancelReason (ADR-052 FR-028) ------------------------------------------

// TestCancelReason_SetClearRoundTrip exercises the full user-Stop → restart
// lifecycle: a failed task is stopped by a user (CancelReason set to
// stopped_by_user, mirroring handleTaskStop's own future patch), persists,
// then a restart-style patch (failed→next + CancelReason cleared) succeeds
// and the clear persists too (FR-028: "restart MUST clear it").
func TestCancelReason_SetClearRoundTrip(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusInProgress
	mustCreate(t, s, tk)

	// User Stop: in_progress -> failed, cancel_reason set.
	stopped, err := s.Update(tk.ID, Patch{
		Status:       ptr(StatusFailed),
		CancelReason: ptr(CancelReasonStoppedByUser),
	})
	require.NoError(t, err, "stop patch must be accepted")
	assert.Equal(t, StatusFailed, stopped.Status)
	assert.Equal(t, CancelReasonStoppedByUser, stopped.CancelReason)

	// Persistence check: reload from disk.
	got, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, CancelReasonStoppedByUser, got.CancelReason, "cancel_reason must persist")

	// Restart-style patch: failed -> next, cancel_reason cleared, in the SAME call.
	empty := CancelReason("")
	restarted, err := s.Update(tk.ID, Patch{
		Status:       ptr(StatusNext),
		CancelReason: &empty,
	})
	require.NoError(t, err, "failed->next with cancel_reason clear must be accepted")
	assert.Equal(t, StatusNext, restarted.Status)
	assert.Empty(t, restarted.CancelReason, "cancel_reason must be cleared")

	got2, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Empty(t, got2.CancelReason, "cleared cancel_reason must persist")
}

func TestCancelReason_InvalidValueRejected(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusFailed
	mustCreate(t, s, tk)

	bogus := CancelReason("bogus_reason")
	_, err := s.Update(tk.ID, Patch{CancelReason: &bogus})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation), "invalid cancel_reason must wrap ErrValidation")
}

// TestCancelReason_RequiresFailedStatus locks in the FR-028 coupling
// invariant (mirrors plan.Plan's FailedReason/State coupling): a non-empty
// CancelReason is only valid when Status is failed, checked against the
// FULLY merged patch (not just whichever field was touched).
func TestCancelReason_RequiresFailedStatus(t *testing.T) {
	s := newStore(t)

	t.Run("set on a non-failed task is rejected", func(t *testing.T) {
		tk := mkTask("t1", "ws")
		tk.Status = StatusNext
		mustCreate(t, s, tk)

		_, err := s.Update(tk.ID, Patch{CancelReason: ptr(CancelReasonStoppedByUser)})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("leaving failed without clearing cancel_reason auto-clears (fix-wave #3)", func(t *testing.T) {
		tk := mkTask("t2", "ws")
		tk.Status = StatusInProgress
		mustCreate(t, s, tk)
		mustUpdate(t, s, tk.ID, Patch{
			Status:       ptr(StatusFailed),
			CancelReason: ptr(CancelReasonStoppedByUser),
		})

		// A status-only patch leaving failed (e.g. run_task resuming a
		// stopped task) now auto-clears the stale cancel_reason instead of
		// being rejected — see TestCancelReason_AutoClearOnStatusLeavingFailed
		// for the full dedicated coverage.
		updated, err := s.Update(tk.ID, Patch{Status: ptr(StatusNext)})
		require.NoError(t, err, "status leaving failed must auto-clear a stale cancel_reason")
		assert.Equal(t, StatusNext, updated.Status)
		assert.Empty(t, updated.CancelReason, "cancel_reason must be auto-cleared")

		got, gerr := s.Get(tk.ID)
		require.NoError(t, gerr)
		assert.Equal(t, StatusNext, got.Status)
		assert.Empty(t, got.CancelReason, "auto-cleared cancel_reason must persist")
	})

	t.Run("create-time coupling is also enforced", func(t *testing.T) {
		bad := &Task{
			Title: "x", Action: ActionLLM, WorkspaceID: "ws",
			Status: StatusNext, CancelReason: CancelReasonStoppedByUser,
		}
		err := s.Create(bad)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrValidation))
	})
}

// TestCancelReason_AutoClearOnStatusLeavingFailed is the ADR-052 fix-wave
// regression lock for finding #3 ("run_task on a stopped task breaks"): when
// a patch moves Status OFF failed and does NOT also touch CancelReason in
// the same call, the store auto-clears the stale CancelReason before the
// merged cross-field check runs (mirrors RestartReset's clear-on-restart),
// rather than rejecting the patch outright. An explicit CancelReason
// supplied in the SAME patch as a status leaving failed is still rejected —
// the auto-clear is a convenience for the common "forgot to also clear it"
// case, not a license to silently drop explicit conflicting data.
func TestCancelReason_AutoClearOnStatusLeavingFailed(t *testing.T) {
	s := newStore(t)

	t.Run("run_task-shaped patch: failed+stopped_by_user -> in_progress succeeds, reason cleared", func(t *testing.T) {
		tk := mkTask("t1", "ws")
		tk.Status = StatusInProgress
		mustCreate(t, s, tk)
		mustUpdate(t, s, tk.ID, Patch{
			Status:       ptr(StatusFailed),
			CancelReason: ptr(CancelReasonStoppedByUser),
		})

		updated, err := s.Update(tk.ID, Patch{Status: ptr(StatusInProgress)})
		require.NoError(t, err, "run_task resuming a stopped task must succeed")
		assert.Equal(t, StatusInProgress, updated.Status)
		assert.Empty(t, updated.CancelReason, "cancel_reason must be auto-cleared")

		got, gerr := s.Get(tk.ID)
		require.NoError(t, gerr)
		assert.Equal(t, StatusInProgress, got.Status)
		assert.Empty(t, got.CancelReason, "auto-cleared cancel_reason must persist")
	})

	t.Run("failed -> next auto-clears too (no explicit cancel_reason in the patch)", func(t *testing.T) {
		tk := mkTask("t2", "ws")
		tk.Status = StatusInProgress
		mustCreate(t, s, tk)
		mustUpdate(t, s, tk.ID, Patch{
			Status:       ptr(StatusFailed),
			CancelReason: ptr(CancelReasonStoppedByUser),
		})

		restarted, err := s.Update(tk.ID, Patch{Status: ptr(StatusNext)})
		require.NoError(t, err)
		assert.Equal(t, StatusNext, restarted.Status)
		assert.Empty(t, restarted.CancelReason)
	})

	t.Run("explicit cancel_reason supplied alongside a status leaving failed is still rejected (backstop)", func(t *testing.T) {
		tk := mkTask("t3", "ws")
		tk.Status = StatusInProgress
		mustCreate(t, s, tk)
		mustUpdate(t, s, tk.ID, Patch{
			Status:       ptr(StatusFailed),
			CancelReason: ptr(CancelReasonStoppedByUser),
		})

		_, err := s.Update(tk.ID, Patch{
			Status:       ptr(StatusInProgress),
			CancelReason: ptr(CancelReasonStoppedByUser),
		})
		require.Error(t, err, "explicit conflicting cancel_reason must never be silently dropped")
		assert.True(t, errors.Is(err, ErrValidation))

		got, gerr := s.Get(tk.ID)
		require.NoError(t, gerr)
		assert.Equal(t, StatusFailed, got.Status, "rejected patch must not mutate the task")
		assert.Equal(t, CancelReasonStoppedByUser, got.CancelReason)
	})

	t.Run("a patch that does not touch Status at all is unaffected (no auto-clear)", func(t *testing.T) {
		tk := mkTask("t4", "ws")
		tk.Status = StatusInProgress
		mustCreate(t, s, tk)
		mustUpdate(t, s, tk.ID, Patch{
			Status:       ptr(StatusFailed),
			CancelReason: ptr(CancelReasonStoppedByUser),
		})

		updated, err := s.Update(tk.ID, Patch{Priority: ptr(3)})
		require.NoError(t, err)
		assert.Equal(t, StatusFailed, updated.Status)
		assert.Equal(t, CancelReasonStoppedByUser, updated.CancelReason, "cancel_reason survives an unrelated patch")
	})
}

// TestValidateTransition_FailedToNext_AnyReason confirms (ADR-052 §7) that
// task-level failed→next is un-frozen unconditionally — i.e. NOT gated on
// CancelReason the way the plan-level restart guard is gated on
// FailedReason==stopped_by_user. A task that failed for a reason OTHER than
// a user Stop (CancelReason empty — e.g. attempt-limit exhaustion) can still
// retry/restart via failed→next.
func TestValidateTransition_FailedToNext_AnyReason(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusInProgress
	mustCreate(t, s, tk)

	// Genuine failure: no CancelReason set at all.
	mustUpdate(t, s, tk.ID, Patch{Status: ptr(StatusFailed)})
	got, err := s.Get(tk.ID)
	require.NoError(t, err)
	require.Empty(t, got.CancelReason, "genuine failure leaves cancel_reason empty")

	// failed (no reason) -> next must still be legal.
	restarted, err := s.Update(tk.ID, Patch{Status: ptr(StatusNext)})
	require.NoError(t, err, "failed->next must be legal for a genuine (reason-less) failure")
	assert.Equal(t, StatusNext, restarted.Status)
}

// ---- RestartReset (ADR-052 FR-016/017/028) ----------------------------------

func TestRestartReset_HappyPath(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusFailed
	tk.CancelReason = CancelReasonStoppedByUser
	mustCreate(t, s, tk)

	// Simulate a prior run's leftovers directly (bypassing the public API,
	// mirroring TestSpawnReset_ClearsRunFields's own pattern).
	const at = "2026-01-01T00:00:00Z"
	loaded, err := s.Get(tk.ID)
	require.NoError(t, err)
	loaded.AttemptCount = 2
	loaded.Result = "old-result"
	loaded.Artifacts = []string{"a.txt"}
	loaded.SessionID = "sess-abc"
	loaded.StartedAt = at
	loaded.CompletedAt = at
	loaded.FollowedUp = true
	loaded.PendingJudgeClaim = "claim text"
	require.NoError(t, s.write(loaded))

	reset, err := s.RestartReset(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusNext, reset.Status, "no unmet deps -> next")
	assert.Equal(t, 0, reset.AttemptCount, "attempt_count reset to 0")
	assert.Empty(t, reset.CancelReason, "cancel_reason cleared")
	assert.Empty(t, reset.Result)
	assert.Nil(t, reset.Artifacts)
	assert.Empty(t, reset.SessionID)
	assert.Empty(t, reset.StartedAt)
	assert.Empty(t, reset.CompletedAt)
	assert.False(t, reset.FollowedUp)
	assert.Empty(t, reset.PendingJudgeClaim)

	// Persistence check.
	got, err := s.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusNext, got.Status)
	assert.Equal(t, 0, got.AttemptCount)
	assert.Empty(t, got.CancelReason)
}

// TestRestartReset_ToBlockedWhenDepUnmet confirms the "reset to next/blocked"
// half of DS-5: a failed member whose blocked_by dependency is not yet done
// lands on the derived `blocked` side-state, not `next`.
func TestRestartReset_ToBlockedWhenDepUnmet(t *testing.T) {
	s := newStore(t)
	dep := mkTask("dep", "ws")
	dep.Status = StatusNext
	mustCreate(t, s, dep)

	member := mkTask("member", "ws")
	member.Status = StatusFailed
	member.CancelReason = CancelReasonStoppedByUser
	member.BlockedBy = []string{dep.ID}
	mustCreate(t, s, member)

	reset, err := s.RestartReset(member.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusBlocked, reset.Status, "unmet dep -> blocked, not next")
	assert.Equal(t, 0, reset.AttemptCount)
	assert.Empty(t, reset.CancelReason)

	// Once the dep completes, AdvanceBlockedDependents should later move it —
	// out of scope here, this test only asserts RestartReset's own landing state.
}

// TestRestartReset_PreservesDoneMember confirms restart is a no-op error on
// an already-`done` member (FR-017: "preserve done members" — the restart
// orchestrator must skip done members rather than calling RestartReset on
// them; this locks in that RestartReset itself refuses to touch one).
func TestRestartReset_PreservesDoneMember(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusDone
	tk.Result = "final result"
	mustCreate(t, s, tk)

	_, err := s.RestartReset(tk.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotRestartable))

	got, gerr := s.Get(tk.ID)
	require.NoError(t, gerr)
	assert.Equal(t, StatusDone, got.Status, "done member must be untouched")
	assert.Equal(t, "final result", got.Result, "done member's result must be preserved")
}

func TestRestartReset_RejectsNonFailed(t *testing.T) {
	s := newStore(t)
	cases := []Status{StatusInbox, StatusNext, StatusInProgress, StatusBlocked}
	for _, st := range cases {
		t.Run(string(st), func(t *testing.T) {
			var tk *Task
			if st == StatusBlocked {
				dep := mkTask("dep-"+string(st), "ws")
				mustCreate(t, s, dep)
				tk = newBlockedTask("t-"+string(st), "ws", []string{dep.ID})
			} else {
				tk = mkTask("t-"+string(st), "ws")
				tk.Status = st
			}
			mustCreate(t, s, tk)

			_, err := s.RestartReset(tk.ID)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrNotRestartable), "status %q must be rejected", st)
		})
	}
}

func TestRestartReset_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.RestartReset("does-not-exist")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

// TestValidateStandaloneRestart table-drives the standalone-task restart gate
// (ADR-052 §6.7/§6.8, spec FR-026) in isolation — the pkg/task mirror of
// plan.TestPlan_ValidateRestartTransition (pkg/plan/plan_test.go). Legal
// ONLY from Status==failed with CancelReason==stopped_by_user; every other
// (status, reason) pair is rejected with ErrStandaloneRestartNotPermitted
// (wrapping ErrIllegalTransition, wrapping ErrValidation). This does NOT test
// RestartReset itself (which stays reason-agnostic — see its doc comment) —
// only the caller-side gate the REST handler (rest_tasks.go's
// handleTaskRestart) applies in front of it.
func TestValidateStandaloneRestart(t *testing.T) {
	cases := []struct {
		name         string
		status       Status
		cancelReason CancelReason
		wantLegal    bool
	}{
		{"failed_stopped_by_user_ok", StatusFailed, CancelReasonStoppedByUser, true},
		{"failed_empty_reason_rejected", StatusFailed, "", false},
		{"failed_other_reason_rejected", StatusFailed, CancelReason("attempts_exhausted"), false},
		{"inbox_rejected", StatusInbox, CancelReasonStoppedByUser, false},
		{"next_rejected", StatusNext, CancelReasonStoppedByUser, false},
		{"in_progress_rejected", StatusInProgress, CancelReasonStoppedByUser, false},
		{"blocked_rejected", StatusBlocked, CancelReasonStoppedByUser, false},
		{"done_rejected_not_a_cancel", StatusDone, CancelReasonStoppedByUser, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateStandaloneRestart(c.status, c.cancelReason)
			if c.wantLegal {
				if err != nil {
					t.Fatalf("expected restart to be legal from %s reason %q, got error: %v", c.status, c.cancelReason, err)
				}
				return
			}
			require.Error(t, err, "expected restart to be rejected from %s reason %q", c.status, c.cancelReason)
			assert.True(t, errors.Is(err, ErrStandaloneRestartNotPermitted), "must be ErrStandaloneRestartNotPermitted, got %v", err)
			assert.True(t, errors.Is(err, ErrIllegalTransition), "must wrap ErrIllegalTransition, got %v", err)
			assert.True(t, errors.Is(err, ErrValidation), "must wrap ErrValidation (400-mapping), got %v", err)
		})
	}
}

// ---- Stop guarantee TOCTOU fix (ADR-052 FR-014/§6.4(b)) --------------------
//
// validateStopGuard (store.go) closes interleaving (b) from the FR-014
// TOCTOU postmortem: a judge verdict computed just before a Stop landed
// could otherwise resolve failed[stopped_by_user] -> done via a plain
// Update, silently marking a user-cancelled task as successfully DONE — the
// lifecycle matrix's own "failed is not frozen" rule (TestFailedCanBeRetried
// above) exists precisely so a GENUINE failure stays retryable, and cannot
// by itself distinguish that from "complete a cancelled one". UpdateIfStatus
// closes the companion EXECUTOR-side gap: a stale in-memory task object plus
// a separate later write can revive or clobber a task the store's own
// CancelReason-only guard does not block (e.g. failed[stopped_by_user] ->
// next, which must stay legal for the restart/re-run routes).

// TestValidateStopGuard_RejectsDoneAfterUserStop is a direct unit test of
// the extracted guard: failed[stopped_by_user] -> done is rejected,
// regardless of what validateTransition alone would have permitted.
func TestValidateStopGuard_RejectsDoneAfterUserStop(t *testing.T) {
	stopped := &Task{Status: StatusFailed, CancelReason: CancelReasonStoppedByUser}
	err := validateStopGuard(stopped, StatusDone)
	require.Error(t, err, "failed[stopped_by_user] -> done must be rejected")
	assert.True(t, errors.Is(err, ErrIllegalTransition))
	assert.True(t, errors.Is(err, ErrValidation))
}

// TestValidateStopGuard_AllowsRestartAndRerunDirections proves the guard is
// narrowly scoped to `-> done` only: the Play/restart direction (-> next)
// and the Run re-run direction (-> in_progress) both stay legal, and a
// GENUINE failure (no stopped_by_user reason) is never touched by this
// guard at all — it is validateTransition's ordinary "failed is not frozen"
// rule that governs those, unchanged.
func TestValidateStopGuard_AllowsRestartAndRerunDirections(t *testing.T) {
	stopped := &Task{Status: StatusFailed, CancelReason: CancelReasonStoppedByUser}
	for _, to := range []Status{StatusNext, StatusInProgress} {
		if err := validateStopGuard(stopped, to); err != nil {
			t.Errorf("failed[stopped_by_user] -> %s must remain legal, got: %v", to, err)
		}
	}
	genuine := &Task{Status: StatusFailed}
	if err := validateStopGuard(genuine, StatusDone); err != nil {
		t.Errorf("a GENUINE failure (no stopped_by_user reason) -> done must not be blocked by the stop "+
			"guard (it has nothing to guard here), got: %v", err)
	}
}

// TestUpdate_StopGuard_RejectsDoneOverwriteAfterStop is the store-level,
// end-to-end proof of interleaving (b): a task the user Stopped (failed +
// stopped_by_user, exactly what PlanEngine.cancelMemberLocked / handleTaskStop
// write) can never be moved to done via a plain Update, even though nothing
// else in the lifecycle matrix would have blocked failed -> done. This is
// the PRE-FIX bug reproduced literally: before validateStopGuard existed,
// this exact call succeeded and silently marked a cancelled task "done".
func TestUpdate_StopGuard_RejectsDoneOverwriteAfterStop(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusInProgress
	mustCreate(t, s, tk)

	mustUpdate(t, s, tk.ID, Patch{
		Status:       ptr(StatusFailed),
		CancelReason: ptr(CancelReasonStoppedByUser),
	})

	_, err := s.Update(tk.ID, Patch{Status: ptr(StatusDone), Result: ptr("claims success")})
	require.Error(t, err, "a stale MET verdict must not be able to overwrite a user Stop as done")
	assert.True(t, errors.Is(err, ErrIllegalTransition))

	got, gerr := s.Get(tk.ID)
	require.NoError(t, gerr)
	assert.Equal(t, StatusFailed, got.Status, "status must remain the Stop's own failed outcome")
	assert.Equal(t, CancelReasonStoppedByUser, got.CancelReason, "cancel_reason must survive the rejected write")
	assert.NotEqual(t, "claims success", got.Result, "the rejected write's Result must never land")
}

// ---- UpdateIfStatus (executor CAS primitive) --------------------------------

func TestUpdateIfStatus_SucceedsWhenStatusMatches(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusInProgress
	mustCreate(t, s, tk)

	newAttempt := 1
	nextStatus := StatusNext
	got, err := s.UpdateIfStatus(tk.ID, StatusInProgress, Patch{AttemptCount: &newAttempt, Status: &nextStatus})
	require.NoError(t, err)
	assert.Equal(t, StatusNext, got.Status)
	assert.Equal(t, 1, got.AttemptCount)

	reread, gerr := s.Get(tk.ID)
	require.NoError(t, gerr)
	assert.Equal(t, StatusNext, reread.Status, "the write must persist")
}

// TestUpdateIfStatus_ConflictWhenStatusDiffers is the direct unit proof of
// the CAS primitive's core contract: when the on-disk status no longer
// matches `expected` (simulating a concurrent Stop that already landed —
// "driving the store directly to simulate the interleaving"), NOTHING is
// written and ErrStatusConflict is returned.
func TestUpdateIfStatus_ConflictWhenStatusDiffers(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusInProgress
	mustCreate(t, s, tk)

	// Simulate a concurrent Stop landing between the caller's stale read and
	// this write attempt.
	mustUpdate(t, s, tk.ID, Patch{
		Status:       ptr(StatusFailed),
		CancelReason: ptr(CancelReasonStoppedByUser),
	})

	newAttempt := 1
	nextStatus := StatusNext
	_, err := s.UpdateIfStatus(tk.ID, StatusInProgress, Patch{AttemptCount: &newAttempt, Status: &nextStatus})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrStatusConflict))
	assert.False(t, errors.Is(err, ErrValidation), "a CAS conflict is a race outcome, not a validation error")

	got, gerr := s.Get(tk.ID)
	require.NoError(t, gerr)
	assert.Equal(t, StatusFailed, got.Status, "the dropped write must leave the Stop outcome untouched")
	assert.Equal(t, CancelReasonStoppedByUser, got.CancelReason)
	assert.Equal(t, 0, got.AttemptCount, "a dropped conflict write must not consume an attempt")
}

func TestUpdateIfStatus_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.UpdateIfStatus("does-not-exist", StatusInProgress, Patch{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestUpdateIfStatus_InvalidID(t *testing.T) {
	s := newStore(t)
	_, err := s.UpdateIfStatus("../escape", StatusInProgress, Patch{})
	require.Error(t, err)
}

// TestUpdateIfStatus_StillHonorsStopGuard proves UpdateIfStatus routes
// through the SAME updateLocked validation path as Update — a CAS success
// on the status check does not bypass the Stop guard (belt-and-suspenders:
// even if a caller mistakenly passed StatusFailed as `expected` against an
// already-stopped task, the done-overwrite is still rejected).
func TestUpdateIfStatus_StillHonorsStopGuard(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusInProgress
	mustCreate(t, s, tk)
	mustUpdate(t, s, tk.ID, Patch{
		Status:       ptr(StatusFailed),
		CancelReason: ptr(CancelReasonStoppedByUser),
	})

	_, err := s.UpdateIfStatus(tk.ID, StatusFailed, Patch{Status: ptr(StatusDone)})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrIllegalTransition), "UpdateIfStatus must still honor the Stop guard")
}

// ---- Run-route AttemptCount decision (item iii) -----------------------------

// TestAttemptCount_NotResetOnRunRoute pins the deliberate decision
// documented at updateLocked's CancelReason auto-clear site: a genuinely-
// failed task's AttemptCount survives a plain Update-based re-run (the
// "Run" PATCH route, failed -> in_progress), unlike RestartReset (the
// "Play" route) which always zeroes it. This is the "resuming-the-budget is
// defensible" branch — Run continues the existing budget, it is not amnesty.
func TestAttemptCount_NotResetOnRunRoute(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusFailed
	mustCreate(t, s, tk)
	// Simulate a genuine attempt-exhaustion failure (no cancel_reason) that
	// already consumed its full budget.
	newAttempt := 3
	mustUpdate(t, s, tk.ID, Patch{AttemptCount: &newAttempt})

	got, err := s.Update(tk.ID, Patch{Status: ptr(StatusInProgress)})
	require.NoError(t, err, "Run route (failed -> in_progress) must remain legal for a genuine failure")
	assert.Equal(t, 3, got.AttemptCount, "AttemptCount must NOT be reset by the plain Run route")
	assert.Empty(t, got.CancelReason, "cancel_reason auto-clear is unaffected by this decision")

	reread, gerr := s.Get(tk.ID)
	require.NoError(t, gerr)
	assert.Equal(t, 3, reread.AttemptCount, "the unreset AttemptCount must persist")
}

// TestAttemptCount_IsResetByRestartReset contrasts the above: the Play
// route (RestartReset) DOES reset AttemptCount to 0 — the two routes are
// deliberately asymmetric, not an oversight.
func TestAttemptCount_IsResetByRestartReset(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusFailed
	tk.CancelReason = CancelReasonStoppedByUser
	mustCreate(t, s, tk)
	newAttempt := 2
	mustUpdate(t, s, tk.ID, Patch{AttemptCount: &newAttempt})

	got, err := s.RestartReset(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, got.AttemptCount, "RestartReset (Play) always resets AttemptCount to 0")
}
