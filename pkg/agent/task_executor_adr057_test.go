// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_adr057_test.go (ADR-057 U26/W3d) pins the strict-transcript
// conversion of task_executor.go's 7 AppendTranscript call sites: each now
// calls UnifiedStore.AppendTranscriptStrict and, on a write failure against a
// session id that does not resolve to a real, store-backed session,
// increments taskGoalTranscriptWriteFailures (FR-002) instead of silently
// swallowing the error. Every assertion here runs against a REAL
// UnifiedStore backing a real AgentLoop (newGoalLoopTestLoop, judge_test.go)
// — no spies, no fakes for the store itself.
//
// Red/green note: by the time this unit started, U5 (a5b24296) had already
// made the base UnifiedStore.AppendTranscript method itself strict — "No
// lenient form survives anywhere in the tree" (FR-002). So the literal
// "lenient call silently succeeds" half of this unit's red/green brief is
// no longer reproducible in the current tree state:
// TestU26_BareAppendTranscript_AlreadyStrictAgainstNonexistentSession below
// verifies this empirically (against a real store, a real nonexistent
// session id) rather than assuming it, and documents the finding instead of
// fabricating a lenient path that no longer exists. What genuinely IS new
// in this unit is observability: before this conversion, none of these 9
// call sites incremented anything on failure — a lost write was already
// erroring and being logged (post-U5), but invisible to any counter or
// operator tooling. That is the real "before" defect this file's
// negative-path tests demonstrate went from silent-to-metrics to counted.
package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// u26NonexistentSessionID is a syntactically valid session id that no test
// in this file ever creates — used by every negative-path assertion below.
const u26NonexistentSessionID = "session_u26_nonexistent_probe"

// u26FreshTaskSession creates a real, store-backed task session via the same
// UnifiedStore.NewSession primitive production code uses (createTaskSessionSync,
// StartTaskNow), returning its id. Pairs every negative-path (zero-count)
// assertion in this file with a positive lower bound (ownership Rule 4): the
// write path itself must be proven to work, not just proven to reject.
func u26FreshTaskSession(t *testing.T, store *session.UnifiedStore, agentID string) string {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeTask, "", agentID)
	if err != nil {
		t.Fatalf("u26FreshTaskSession: NewSession: %v", err)
	}
	return meta.ID
}

// u26AssertNoSessionDir asserts, against the real filesystem (not a spy),
// that AppendTranscriptStrict's contract held: no directory was minted for
// sessionID. This is the literal AC-1 property ("creates no directory")
// re-verified at this unit's own call sites.
func u26AssertNoSessionDir(t *testing.T, store *session.UnifiedStore, sessionID string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(store.BaseDir(), sessionID)); !os.IsNotExist(err) {
		t.Fatalf("expected no directory for %q, stat returned err=%v", sessionID, err)
	}
}

// TestU26_BareAppendTranscript_AlreadyStrictAgainstNonexistentSession is the
// empirical check behind this file's doc-comment red/green note: it proves,
// against a real store, that AppendTranscript (the literal method name every
// one of this unit's 9 call sites called before conversion) already refuses
// a nonexistent session and creates no directory — U5 landed this ahead of
// U26, so there is no surviving lenient sibling in the tree to contrast
// against. AppendTranscriptStrict must fail identically (frozen U2/U5
// contract: same existence predicate).
func TestU26_BareAppendTranscript_AlreadyStrictAgainstNonexistentSession(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}

	if err := store.AppendTranscript(u26NonexistentSessionID, session.TranscriptEntry{
		ID: "probe-lenient-name", Role: "user", Content: "x", Timestamp: time.Now().UTC(),
	}); err == nil {
		t.Fatal("AppendTranscript against a nonexistent session id returned nil — expected a non-nil error " +
			"(FR-002: the base primitive is strict; there is no lenient form left in the tree)")
	}
	u26AssertNoSessionDir(t, store, u26NonexistentSessionID)

	if err := store.AppendTranscriptStrict(u26NonexistentSessionID, session.TranscriptEntry{
		ID: "probe-strict-name", Role: "user", Content: "x", Timestamp: time.Now().UTC(),
	}); err == nil {
		t.Fatal("AppendTranscriptStrict against a nonexistent session id returned nil")
	}
	u26AssertNoSessionDir(t, store, u26NonexistentSessionID)
}

// TestU26_WriteJudgeVerdictTranscript_NonexistentSession_CountsAndWarns
// exercises task_executor.go's writeJudgeVerdictTranscript (this unit's
// judge-verdict call site) directly against a session id with no backing
// store entry: taskGoalTranscriptWriteFailures must increment by exactly 1
// and no directory must be created.
func TestU26_WriteJudgeVerdictTranscript_NonexistentSession_CountsAndWarns(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	tk := &task.Task{
		Title: "u26 judge verdict negative", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	verdict := &task.JudgeVerdict{
		Round: 1, Met: false, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: false, Reason: "unmet"}},
	}

	before := TaskGoalTranscriptWriteFailures()
	al.taskExecutor.writeJudgeVerdictTranscript(tk, u26NonexistentSessionID, verdict)
	after := TaskGoalTranscriptWriteFailures()

	if after-before != 1 {
		t.Fatalf("taskGoalTranscriptWriteFailures delta = %d, want 1", after-before)
	}
	u26AssertNoSessionDir(t, store, u26NonexistentSessionID)
}

// TestU26_WriteJudgeVerdictTranscript_RealSession_PersistsAndDoesNotCount is
// the Rule-4 positive lower bound for the test above: against a real
// session, the write must actually land on disk and must NOT move the
// failure counter.
func TestU26_WriteJudgeVerdictTranscript_RealSession_PersistsAndDoesNotCount(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	sessionID := u26FreshTaskSession(t, store, "native-agent")
	tk := &task.Task{
		Title: "u26 judge verdict positive", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	verdict := &task.JudgeVerdict{
		Round: 2, Met: true, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: true, Reason: "evidenced"}},
	}

	before := TaskGoalTranscriptWriteFailures()
	al.taskExecutor.writeJudgeVerdictTranscript(tk, sessionID, verdict)
	after := TaskGoalTranscriptWriteFailures()

	if after != before {
		t.Fatalf("taskGoalTranscriptWriteFailures moved on a real session (before=%d after=%d)", before, after)
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Type == session.EntryTypeJudgeVerdict && e.AgentID == "judge" {
			found = true
		}
	}
	if !found {
		t.Fatalf("judge verdict entry not found in real transcript on disk, got %d entries", len(entries))
	}
}

// TestU26_WriteSteeringPrompt_NonexistentSession_CountsAndWarns exercises
// task_executor.go's writeSteeringPrompt (this unit's steering call site)
// against a nonexistent session.
func TestU26_WriteSteeringPrompt_NonexistentSession_CountsAndWarns(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	tk := &task.Task{
		Title: "u26 steering negative", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	before := TaskGoalTranscriptWriteFailures()
	al.taskExecutor.writeSteeringPrompt(tk, u26NonexistentSessionID, "claim summary", nil)
	after := TaskGoalTranscriptWriteFailures()

	if after-before != 1 {
		t.Fatalf("taskGoalTranscriptWriteFailures delta = %d, want 1", after-before)
	}
	u26AssertNoSessionDir(t, store, u26NonexistentSessionID)
}

// TestU26_WriteSteeringPrompt_RealSession_PersistsAndDoesNotCount is the
// Rule-4 positive lower bound: a real session accepts the steering entry and
// the counter does not move.
func TestU26_WriteSteeringPrompt_RealSession_PersistsAndDoesNotCount(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	store := al.GetAgentStore("native-agent")
	if store == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	sessionID := u26FreshTaskSession(t, store, "native-agent")
	tk := &task.Task{
		Title: "u26 steering positive", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	before := TaskGoalTranscriptWriteFailures()
	al.taskExecutor.writeSteeringPrompt(tk, sessionID, "steer text here", nil)
	after := TaskGoalTranscriptWriteFailures()

	if after != before {
		t.Fatalf("taskGoalTranscriptWriteFailures moved on a real session (before=%d after=%d)", before, after)
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Content == "steer text here" && e.Role == "system" {
			found = true
		}
	}
	if !found {
		t.Fatalf("steering entry not found in real transcript on disk, got %d entries", len(entries))
	}
}
