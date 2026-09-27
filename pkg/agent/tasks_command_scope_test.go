// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// tasks_command_scope_test.go: RED test for Bug 3 (design note
// coordination/logs/continuescope-architect-note.md, 2026-09-27, Question 2):
// commands.Runtime.GetActiveTurn — wired in
// pkg/agent/loop_slash.go::buildCommandsRuntime — calls the GLOBAL
// al.GetActiveTurn() instead of a closure over al.GetActiveTurnBySession
// (opts.SessionKey), so the /tasks command in one session can report an
// unrelated session's active turn instead of correctly reporting "nothing
// active in THIS session."
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestHandleCommand_Tasks_MustNotLeakUnrelatedSessionsActiveTurn drives
// al.handleCommand directly — the same entry point
// TestHandleCommand_UseTokenIsNormalMessage (loop_slash_test.go) already
// uses for this package's slash-command tests, so this reuses the
// established harness rather than inventing a new one.
//
// Spec (desired, from the note's Question 2 — the exact wording
// tasksCommand (pkg/commands/cmd_subagents.go) replies with when
// rt.GetActiveTurn() returns nil): a session with no active turn of its own
// must get "No active tasks running in this session." from /tasks,
// regardless of what any OTHER session's turn state looks like.
//
// TODAY (bug): buildCommandsRuntime's `GetActiveTurn: func() any { info :=
// al.GetActiveTurn(); ... }` (pkg/agent/loop_slash.go, ~line 269) calls the
// global, whole-map GetActiveTurn() — with session X's turn the only entry
// in activeTurnStates, session Y's /tasks command sees it and wrongly
// reports activity that has nothing to do with session Y.
func TestHandleCommand_Tasks_MustNotLeakUnrelatedSessionsActiveTurn(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent from newTestAgentLoop's fixture")
	}
	_ = cfg

	// Session X: a genuinely active turn, unrelated to session Y below.
	registerActiveTurn(t, al, "session-X-unrelated", "turn-X-unrelated")

	// Session Y issues /tasks. It has no active turn of its own — /tasks
	// runs BEFORE the issuing turn registers (handleCommand is called from
	// loop.go ahead of registerActiveTurn's own call site, per the note's
	// trace), so a nil lookup for Y's own key correctly means "nothing else
	// running in this session."
	opts := &processOptions{SessionKey: "session-Y-own-key"}
	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-y"},
		ChatID:  "chat-y",
		Content: "/tasks",
	}, agent, opts)

	if !handled {
		t.Fatal("/tasks must be handled (a registered builtin command)")
	}
	const want = "No active tasks running in this session."
	if reply != want {
		t.Fatalf("reply = %q, want %q — session Y's own /tasks must not report session X's "+
			"unrelated active turn (Bug 3: buildCommandsRuntime's GetActiveTurn closure calls the "+
			"global al.GetActiveTurn() instead of al.GetActiveTurnBySession(opts.SessionKey))",
			reply, want)
	}
}

// TestHandleCommand_Tasks_StillReportsOwnSessionsActiveTurn is the companion
// positive case: when the ISSUING session itself genuinely has another
// active turn registered under its own key (e.g. a previously delegated
// background task still running under the same session scope — the note's
// own example of what a legitimate non-nil result means), /tasks must still
// report it. This guards against a fix that overcorrects into always
// returning nil.
func TestHandleCommand_Tasks_StillReportsOwnSessionsActiveTurn(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent from newTestAgentLoop's fixture")
	}
	_ = cfg

	const ownSessionKey = "session-Y-own-key-with-a-running-child"
	registerActiveTurn(t, al, ownSessionKey, "turn-Y-child")

	opts := &processOptions{SessionKey: ownSessionKey}
	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-y"},
		ChatID:  "chat-y",
		Content: "/tasks",
	}, agent, opts)

	if !handled {
		t.Fatal("/tasks must be handled (a registered builtin command)")
	}
	if reply == "No active tasks running in this session." {
		t.Fatal("reply says nothing is active, but session Y's OWN key has a registered active turn " +
			"— a fix must not overcorrect into ignoring the issuing session's own state")
	}
}
