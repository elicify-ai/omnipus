// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for the native `delegate follow_up` warm-resume defect
// (UAT 2026-07-31/08-03, reproduced independently by two agents via 20-40s
// REST polls and two live attach_session WS drains): `delegate(action=
// "follow_up")` on a TERMINAL (finished) session returned a well-formed
// success with the correct session_id, and NOTHING ran — message count
// frozen, zero new wire activity.
//
// Causal chain (verified against this exact code before writing the fix):
//  1. Native follow_up reuses the terminal session's own id verbatim
//     (pkg/tools/delegate.go's spawnCorrectiveFollowUp: newSessionID :=
//     sessionID for a non-3P record).
//  2. spawnSubTurn (this package) unconditionally called
//     sharedStore.CreateSessionWithID(childID, ...) — with childID equal to
//     that SAME terminal session's id.
//  3. CreateSessionWithID refuses any session id whose directory already
//     exists (FR-096/BDD-107, pkg/session/unified_api.go) — for a terminal
//     session this directory ALWAYS already exists (it was created by that
//     session's own first generation), so the create ALWAYS collided.
//  4. spawnSubTurn returned a non-nil error, but the caller (delegate
//     follow_up) had already returned a synchronous, well-formed
//     AsyncResult ack before that error surfaced — see
//     pkg/tools/delegate_followup_resume_test.go for the "kill the silent
//     swallow" half of this fix at that boundary.
//
// FR-096's collision guard itself is CORRECT and must not be weakened — it
// exists to stop a session being silently created over an existing
// directory, and TestSpawnSubTurn_FollowUpResume_PlainCreateStillCollides
// below re-pins it. follow_up was an unconsidered casualty: it is a RESUME,
// never a CREATE, and must never be routed through that guard. The fix
// (SubTurnConfig.IsResume) gives spawnSubTurn an explicit resume-vs-create
// distinction at exactly this boundary: IsResume:true verifies the session
// already exists (GetMeta) instead of calling CreateSessionWithID.
package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newFollowUpResumeTestAgentLoop builds a real AgentLoop for this file's
// tests — mustNewAgentLoop directly (mirroring subturn_key_reuse_race_test.go's
// own setup) rather than newTestAgentLoop's 5-value return, which this file
// has no use for beyond the *AgentLoop itself.
func newFollowUpResumeTestAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "followup-resume-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)
	return al
}

// newFollowUpResumeTestParent builds a real turnState rooted in a real,
// store-backed chat session — the same shape TestSpawnSubTurn and
// TestSpawnSubTurn_TranscriptSessionIDIsChildsOwn_RoutingSessionIDInheritsFromParent
// (subturn_test.go) already use, reused here rather than reinvented.
func newFollowUpResumeTestParent(t *testing.T, al *AgentLoop, store *session.UnifiedStore, turnID string) *turnState {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	require.NoError(t, err)
	return &turnState{
		ctx:                 context.Background(),
		turnID:              turnID,
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		session:             &ephemeralSessionStore{},
		agent:               al.registry.GetDefaultAgent(),
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		transcriptStore:     store,
	}
}

// TestSpawnSubTurn_FollowUpResume_PlainCreateStillCollides is the "FR-096
// still guards a genuine create" regression check the task requires. A
// childID reused verbatim WITHOUT IsResume set (every caller's existing,
// unchanged behavior — delegate.run, team, evaluator-optimizer, and this
// exact code path BEFORE the follow_up fix) must still be refused by
// CreateSessionWithID's collision guard. If this test ever starts passing
// with no error, FR-096 has been hollowed out.
func TestSpawnSubTurn_FollowUpResume_PlainCreateStillCollides(t *testing.T) {
	al := newFollowUpResumeTestAgentLoop(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "test harness did not wire a shared session store")

	parent := newFollowUpResumeTestParent(t, al, store, "parent-plain-create")
	childID := "subturn-followup-collision-" + parent.transcriptSessionID

	// Generation 1: a genuine create — mints the child session for real.
	cfg1 := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}, DelegateSessionID: childID}
	_, err := spawnSubTurn(withSpawnToolCallID(context.Background(), "call-1"), al, parent, cfg1)
	require.NoError(t, err, "generation 1 (real create) must succeed")

	// Generation 2 attempt: the SAME childID reused verbatim — exactly what
	// native follow_up does — but WITHOUT IsResume set. This is the
	// regression's own reproduction: it MUST still fail via FR-096, proving
	// the guard was never weakened by this fix.
	cfg2 := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}, DelegateSessionID: childID}
	_, err = spawnSubTurn(withSpawnToolCallID(context.Background(), "call-2"), al, parent, cfg2)
	require.Error(t, err, "a plain create over an existing session directory must still be refused")
	assert.Contains(t, err.Error(), "already exists",
		"must fail via FR-096's collision guard specifically, not some other error")
}

// TestSpawnSubTurn_FollowUpResume_WarmResumeSucceeds is the FIX proof: the
// SAME reused-childID scenario as above, but with IsResume:true — exactly
// what pkg/tools/delegate.go's spawnCorrectiveFollowUp now sets for a
// native follow_up. Before the fix (IsResume didn't exist / was ignored and
// spawnSubTurn always called CreateSessionWithID), this collided and
// returned an error identical to the sibling test above — a follow_up on a
// terminal session could NEVER succeed. After the fix, this must succeed,
// AND the resumed generation's own turn must actually execute (real new
// transcript entries appended to the SAME session, proving continuity —
// FR-075/AC-11 requires prior-generation history to remain visible on
// resume — not a fresh/reset session), AND the original FR-008
// ParentSessionID edge must survive untouched even when a DIFFERENT calling
// session issues the follow_up (mirrors delegate.go's own documented "the
// follow_up caller is not necessarily the agent that originally spawned the
// session").
func TestSpawnSubTurn_FollowUpResume_WarmResumeSucceeds(t *testing.T) {
	al := newFollowUpResumeTestAgentLoop(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "test harness did not wire a shared session store")

	origParent := newFollowUpResumeTestParent(t, al, store, "orig-parent")
	childID := "subturn-followup-resume-" + origParent.transcriptSessionID

	cfg1 := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}, DelegateSessionID: childID}
	_, err := spawnSubTurn(withSpawnToolCallID(context.Background(), "call-1"), al, origParent, cfg1)
	require.NoError(t, err, "generation 1 (real create) must succeed")

	transcriptAfterGen1, err := store.ReadTranscript(childID)
	require.NoError(t, err)
	require.NotEmpty(t, transcriptAfterGen1, "generation 1 must have written real transcript entries")
	gen1Count := len(transcriptAfterGen1)

	metaAfterGen1, err := store.GetMeta(childID)
	require.NoError(t, err)
	require.Equal(t, origParent.transcriptSessionID, metaAfterGen1.ParentSessionID,
		"FR-008 edge must point at the ORIGINAL parent after generation 1")

	// A DIFFERENT calling session issues the follow_up — the documented
	// "not necessarily the agent that originally spawned the session" case.
	otherParent := newFollowUpResumeTestParent(t, al, store, "followup-caller")
	require.NotEqual(t, origParent.transcriptSessionID, otherParent.transcriptSessionID,
		"test setup: the follow_up caller must be a genuinely different session")

	// Generation 2: the warm resume. Same childID, verbatim — IsResume:true.
	cfg2 := SubTurnConfig{
		Model:             "gpt-4o-mini",
		Tools:             []tools.Tool{},
		DelegateSessionID: childID,
		IsResume:          true,
	}
	result, err := spawnSubTurn(withSpawnToolCallID(context.Background(), "call-2"), al, otherParent, cfg2)
	require.NoError(t, err, "a warm resume (IsResume:true) on an existing terminal session must succeed — "+
		"this is the exact call that failed 100% of the time before the fix")
	require.NotNil(t, result, "expected the resumed generation's turn to actually run and return a result")

	transcriptAfterGen2, err := store.ReadTranscript(childID)
	require.NoError(t, err)
	assert.Greater(t, len(transcriptAfterGen2), gen1Count,
		"the resumed generation must append REAL new transcript entries to the SAME session — "+
			"a success-shaped no-op (no new entries) is exactly the defect this fix closes")

	metaAfterGen2, err := store.GetMeta(childID)
	require.NoError(t, err)
	assert.Equal(t, origParent.transcriptSessionID, metaAfterGen2.ParentSessionID,
		"a resume must NOT re-stamp the ParentSessionID edge from the follow_up caller's own session — "+
			"the original parent edge, stamped at generation 1, must survive untouched")
}

// TestSpawnSubTurn_FollowUpResume_MissingSessionSurfacesRealError proves the
// resume seam itself fails LOUDLY (never silently) when the thing it is
// meant to resume genuinely is not there — e.g. a session deleted between
// the follow_up caller's Load and this dispatch. IsResume:true must not
// crash, and must not silently "succeed" into nothing; GetMeta's not-found
// error must propagate as a real, wrapped, non-nil error.
func TestSpawnSubTurn_FollowUpResume_MissingSessionSurfacesRealError(t *testing.T) {
	al := newFollowUpResumeTestAgentLoop(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "test harness did not wire a shared session store")

	parent := newFollowUpResumeTestParent(t, al, store, "parent-missing-resume")

	// This childID was NEVER created (no generation 1 at all).
	childID := "subturn-followup-resume-missing-" + parent.transcriptSessionID
	cfg := SubTurnConfig{
		Model:             "gpt-4o-mini",
		Tools:             []tools.Tool{},
		DelegateSessionID: childID,
		IsResume:          true,
	}
	_, err := spawnSubTurn(withSpawnToolCallID(context.Background(), "call-missing"), al, parent, cfg)
	require.Error(t, err, "resuming a session that was never created must be a real, visible error, "+
		"never a silent success")
	assert.Contains(t, err.Error(), "resume child session",
		"the error must identify itself as a resume failure, not be mistaken for a create failure")
}
