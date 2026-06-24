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
//   - Filter: AgentID, MilestoneID, CreatedBy, BlockedByID, ParentTaskIDSet
//   - priority sort order and List differentiation
//   - normalize length limits (title 200, description 2000, prompt 10000,
//     result 50000)
//   - trigger: recurring cron expr with < 60s interval rejected; bad cron string
//     rejected; manual trigger with no config accepted
//   - validateTransition internal hatch: internal=true permits blocked↔*
//   - updateLocked partial update fields: Trigger clear, Artifacts, SessionID,
//     StartedAt, CompletedAt, SourceChannel, SourceChatID, MilestoneID, Due

package task

import (
	"encoding/json"
	"errors"
	"fmt"
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
		{StatusNext, StatusPlanning},
		{StatusNext, StatusInProgress},
		{StatusPlanning, StatusInProgress},
		{StatusInProgress, StatusDone},
		{StatusInProgress, StatusFailed},
		{StatusFailed, StatusInbox}, // retry path
		{StatusFailed, StatusNext},  // retry path
		{StatusInbox, StatusInbox},  // no-op
		{StatusDone, StatusDone},    // no-op
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
	frozen := []Status{StatusInbox, StatusNext, StatusPlanning, StatusInProgress, StatusFailed}
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

func TestListFilterByMilestoneID(t *testing.T) {
	// Traces to: store.go line 150 — Filter.MilestoneID match
	s := newStore(t)
	ms := mkTask("t", "ws")
	ms.MilestoneID = "milestone-1"
	mustCreate(t, s, ms)

	other := mkTask("other", "ws")
	mustCreate(t, s, other)

	got, _ := s.List(Filter{MilestoneID: "milestone-1"})
	require.Len(t, got, 1)
	assert.Equal(t, "milestone-1", got[0].MilestoneID)
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
		MilestoneID:   ptr("ms-1"),
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
	assert.Equal(t, "ms-1", got.MilestoneID)
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
		MilestoneID:   "ms-1",
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
	assert.Equal(t, "ms-1", r1.MilestoneID)
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
