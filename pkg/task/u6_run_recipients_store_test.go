// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U6 — FR-019: the run's captured recipients are stored on the
// TaskRun record, fixed at the first open.
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md
// FR-019 + BDD-06.3, and the architect's U6 seam decision D-U6.2
// (ARCHITECT-ANSWER-U6-U7.md), read from the spec/decision text, never from the
// implementation.
//
// FR-019: "Run recipients MUST be captured at actual start: starter MAIN's main
// (not creator) plus MAIN assignee's main, deduped. Missing/hidden capture
// leaves result in task/run view; no substitute."
//
// D-U6.2 storage contract:
//
//	task.TaskRun.RecipientSessionIDs []string `json:"recipient_session_ids,omitempty"`
//	Store.OpenRun(taskID, occurrenceMs, kind, sessionID, recipients []string)
//	Store.OpenRunForSession(taskID, sessionID string) (*TaskRun, error)
//	The first open fixes the list; a later idempotent re-open returns the stored
//	run unchanged.
//
// THIS FILE COMPILES ONLY AFTER THE C-TASK CONTRACT CHANGE AND THE pkg/task
// STORE CHANGE (D-U6.7 steps 1–2): RecipientSessionIDs, the new OpenRun arity
// and OpenRunForSession do not exist yet. It is expected not to compile on the
// pre-change tree — that compile error IS its RED, naming exactly the missing
// seam (D-U6.3 anchor row: "Store.OpenRun with recipients").

package task

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestU6_FR019_OpenRunFixesRecipientsOnFirstOpen asserts the run's captured
// recipients are stored at the first open and never rewritten by a later
// (idempotent) re-open — so a recipient reassigned or removed after the run
// started cannot change what the run recorded.
func TestU6_FR019_OpenRunFixesRecipientsOnFirstOpen(t *testing.T) {
	s := newStore(t)
	const taskID = "u6-recipients-1"
	// The capture rule (starter main + assignee main, deduped) is pinned in
	// pkg/agent's captureRunRecipients test; here the store just holds whatever
	// list the first open was given.
	recipients := []string{"main-ws1-b", "main-ws1-mia"}

	run, created, err := s.OpenRun(taskID, nil, RunKindManual, "sess-u6-1", recipients)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, run)
	assert.Equal(t, recipients, run.RecipientSessionIDs,
		"FR-019: OpenRun must store the captured recipients on the run record")

	// A second open is idempotent on (taskID, occurrenceMs) and must keep the
	// FIRST list even when handed a different one — the capture is fixed for the
	// life of the run (D-U6.2).
	run2, created2, err := s.OpenRun(taskID, nil, RunKindManual, "sess-u6-1", []string{"main-other"})
	require.NoError(t, err)
	assert.False(t, created2, "second OpenRun for the same key must not create a new run")
	require.NotNil(t, run2)
	assert.Equal(t, recipients, run2.RecipientSessionIDs,
		"FR-019: the first open fixes the recipient list; a later re-open must not rewrite it")
}

// TestU6_FR019_OpenRunForSessionFindsCapturedRecipients asserts the lookup the
// run-loop entry needs (D-U6.3 step 4) returns the run bound to a session with
// its recipients intact.
func TestU6_FR019_OpenRunForSessionFindsCapturedRecipients(t *testing.T) {
	s := newStore(t)
	const taskID = "u6-recipients-2"
	recipients := []string{"main-ws1-b", "main-ws1-mia"}

	opened, _, err := s.OpenRun(taskID, nil, RunKindManual, "sess-u6-2", recipients)
	require.NoError(t, err)

	found, err := s.OpenRunForSession(taskID, "sess-u6-2")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, opened.RunID, found.RunID)
	assert.Equal(t, recipients, found.RecipientSessionIDs,
		"FR-019: OpenRunForSession must return the run's captured recipients")

	// A session with no open run is a real "not found", not a zero-value run.
	_, err = s.OpenRunForSession(taskID, "sess-does-not-exist")
	assert.Error(t, err, "OpenRunForSession must error for a session with no open run")
}

// TestU6_FR019_OpenRunOmitsEmptyRecipients asserts the optional contract: a run
// with no captured recipient (a skipped fire, or a person/scheduler-started
// worker run) stores an absent/empty list rather than a zero-length non-nil
// slice that would serialise as present-but-empty.
func TestU6_FR019_OpenRunOmitsEmptyRecipients(t *testing.T) {
	s := newStore(t)
	run, _, err := s.OpenRun("u6-recipients-3", nil, RunKindManual, "sess-u6-3", nil)
	require.NoError(t, err)
	assert.Empty(t, run.RecipientSessionIDs,
		"FR-019: a run with no recipient must carry an empty recipient list")
}
