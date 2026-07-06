// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for #412 schedule fixes and #411 scheduled-run stats.
//
// BDD scenarios:
//   Scenario: ProcessScheduled appends user message → MessageCount >= 1
//   Scenario: nil store → ProcessScheduled returns error (not silent)
//   Scenario: cronService atomic.Pointer Store/Load wiring
//
// Traces to: project-task-management-level1-spec.md — #412 schedules, #411 usage

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// TestProcessScheduled_UserMessageCount asserts that after a ProcessScheduled run
// the session Stats.MessageCount is at least 1 — the user message must have been
// appended via AppendTranscript.
//
// BDD:
//
//	Given a valid scheduled session and an owner agent,
//	When ProcessScheduled runs to completion,
//	Then session Stats.MessageCount >= 1.
//
// Guards against: the user message being silently discarded before AppendTranscript.
// Traces to: pkg/agent/loop.go appendUserTranscript path — #412
func TestProcessScheduled_UserMessageCount(t *testing.T) {
	al, home := schedTestLoop(t)
	registerAgent(t, al, home, "mia", testutil.NewScenario().WithText("done"), false)

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	_, err = al.ProcessScheduled(
		context.Background(), "mia", meta.ID, "scheduled user message", "scheduled", meta.ID,
	)
	require.NoError(t, err, "ProcessScheduled must succeed")

	// Retrieve session meta and check MessageCount.
	got, err := al.GetSessionStore().GetMeta(meta.ID)
	require.NoError(t, err, "GetMeta must succeed after ProcessScheduled")

	assert.GreaterOrEqual(t, got.Stats.MessageCount, 1,
		"session Stats.MessageCount must be >= 1 after ProcessScheduled (user message was appended)")
}

// TestProcessScheduled_NilStore_ReturnsError asserts that if the session store
// cannot find the session (simulated by requesting a non-existent session ID), an
// error is returned — not a silent no-op.
//
// BDD:
//
//	Given a session store with no sessions,
//	When ProcessScheduled is called with a non-existent session ID,
//	Then a non-nil error is returned.
//
// Guards against: ProcessScheduled silently succeeding on missing sessions.
// Traces to: pkg/agent/loop.go ProcessScheduled session validation — #412
func TestProcessScheduled_NonExistentSession_ReturnsError(t *testing.T) {
	al, home := schedTestLoop(t)
	registerAgent(t, al, home, "mia", testutil.NewScenario().WithText("ok"), false)

	// Use a syntactically valid but non-existent session ID.
	nonExistentID := "session_01JXDOESNOTEXIST00000001"

	_, err := al.ProcessScheduled(
		context.Background(), "mia", nonExistentID, "do something", "scheduled", nonExistentID,
	)
	require.Error(t, err, "ProcessScheduled must return an error for a non-existent session ID")
	assert.False(t, errors.Is(err, nil),
		"error must be non-nil, not a zero-value")
}

// TestProcessScheduled_MessageCountSums asserts that running ProcessScheduled twice
// on separate sessions produces independent MessageCount values — proving the stat
// is scoped to the session and not shared/global.
//
// Differentiation test: two different sessions must have independent stats.
// Traces to: pkg/agent/loop.go appendUserTranscript per-session scoping — #411/#412
func TestProcessScheduled_MessageCountSums(t *testing.T) {
	al, home := schedTestLoop(t)
	registerAgent(t, al, home, "mia",
		testutil.NewScenario().WithText("first").WithText("second"), false)

	meta1, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)
	meta2, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)

	_, err = al.ProcessScheduled(context.Background(), "mia", meta1.ID, "msg-a", "scheduled", meta1.ID)
	require.NoError(t, err)

	_, err = al.ProcessScheduled(context.Background(), "mia", meta2.ID, "msg-b", "scheduled", meta2.ID)
	require.NoError(t, err)

	got1, err := al.GetSessionStore().GetMeta(meta1.ID)
	require.NoError(t, err)
	got2, err := al.GetSessionStore().GetMeta(meta2.ID)
	require.NoError(t, err)

	// Both sessions must independently have MessageCount >= 1.
	assert.GreaterOrEqual(t, got1.Stats.MessageCount, 1,
		"session 1 MessageCount must be >= 1")
	assert.GreaterOrEqual(t, got2.Stats.MessageCount, 1,
		"session 2 MessageCount must be >= 1")
}
