// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile_context_test.go covers ADR-079 D1 (bounded session-transcript
// window fed into the /goal compile) and ADR-080 D-CONTEXT2 (workspace/
// project instructions fed into the /goal compile ONLY, never the Judge) —
// the INPUT half of the goal-compile redesign. Mirrors the
// twoPhaseHarness/setGoal/compileJSON/questionJSON fixtures already
// established by goal_two_phase_test.go and the seedWorkspace fixture from
// workspace_instructions_test.go.
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// callSystemText / callUserText flatten a scripted call's messages by role
// for substring assertions.
func callTextByRole(msgs []providers.Message, role string) string {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Role == role {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// TestGoalCompile_SessionWindowFedOnInitialResumeAndRepair is ADR-079 D1's
// required regression #1: a prior-turn line in the session transcript rides
// the UNTRUSTED background-context window into the compile's USER message —
// on the initial compile, a resumed compile (after a clarifying question),
// AND that resumed compile's own repair call — since all three are separate
// compileGoalIntentLLM invocations / loop iterations that each re-render
// buildGoalCompileMessages.
func TestGoalCompile_SessionWindowFedOnInitialResumeAndRepair(t *testing.T) {
	const priorLine = "PRIOR-TURN-MARKER: we discussed rewriting the omnipus README earlier"

	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(call int, _ []providers.Message) (*providers.LLMResponse, error) {
			switch call {
			case 1:
				return questionJSON("Which repo do you mean?"), nil
			case 2:
				return compileJSON("feels good"), nil // vetoed → resume's own repair
			default:
				return compileJSON("the README covers installation end to end"), nil
			}
		}, nil)

	if err := store.AppendTranscript(sid, session.TranscriptEntry{
		Role: "user", Content: priorLine,
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	// (1) Initial compile.
	_, handled, reply := setGoal(t, al, agentInst, opts, "improve the readme")
	if !handled || !strings.Contains(reply, "Which repo do you mean?") {
		t.Fatalf("setup: want the clarifying question, got handled=%v reply=%q", handled, reply)
	}
	call1 := callTextByRole(provider.messagesOfCall(1), "user")
	if !strings.Contains(call1, priorLine) {
		t.Fatalf("initial compile's user message missing the session window:\n%s", call1)
	}
	if !strings.Contains(call1, "BACKGROUND CONTEXT ONLY") {
		t.Fatalf("initial compile's user message missing the untrusted-window framing:\n%s", call1)
	}

	// (2) Resumed compile (the clarification answer).
	handled2, _ := al.applyGoalPendingReply(context.Background(),
		bus.InboundMessage{Content: "the omnipus repo", UserInitiated: true}, agentInst, opts)
	if !handled2 {
		t.Fatal("setup: the clarification answer must be intercepted")
	}
	if got := provider.callCount(); got != 3 {
		t.Fatalf("setup: want 3 total LLM calls (question, resume, resume's repair), got %d", got)
	}
	call2 := callTextByRole(provider.messagesOfCall(2), "user")
	if !strings.Contains(call2, priorLine) {
		t.Fatalf("resumed compile's user message missing the session window:\n%s", call2)
	}

	// (3) The resumed compile's own repair call.
	call3 := callTextByRole(provider.messagesOfCall(3), "user")
	if !strings.Contains(call3, priorLine) {
		t.Fatalf("repair compile's user message missing the session window:\n%s", call3)
	}
	if !strings.Contains(call3, "rejected by the feasibility gate") {
		t.Fatalf("repair call must still carry the veto reason:\n%s", call3)
	}
}

// TestGoalCompile_SessionWindowEmptyOnMiss is ADR-079 D1's "byte-identical
// to no-window" guarantee: a session with no prior transcript entries (the
// ordinary case for every other test in this package) never renders the
// background-context heading at all.
func TestGoalCompile_SessionWindowEmptyOnMiss(t *testing.T) {
	al, agentInst, provider, _, _, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the report is saved"), nil
		}, nil)

	setGoal(t, al, agentInst, opts, "write the report")
	call1 := callTextByRole(provider.messagesOfCall(1), "user")
	if strings.Contains(call1, "BACKGROUND CONTEXT ONLY") {
		t.Fatalf("compile input must omit the window heading when the session has no prior transcript:\n%s", call1)
	}
}

// TestGoalCompile_WorkspaceInstructionsFedIntoCompile is ADR-080 D-CONTEXT2's
// required regression #9 (the compile half): the goal-bearing agent's
// resolved workspace's AGENT.md rides the AUTHORITATIVE-framed workspace-
// instructions note into the compile's SYSTEM message.
func TestGoalCompile_WorkspaceInstructionsFedIntoCompile(t *testing.T) {
	al, agentInst, provider, _, _, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the report is saved"), nil
		}, nil)

	home := config.OmnipusHomeDir()
	wsID := seedWorkspace(t, home, "Code goals: tests pass, no new lint errors.")
	opts.WorkspaceID = wsID

	setGoal(t, al, agentInst, opts, "write the report")
	if got := provider.callCount(); got != 1 {
		t.Fatalf("want 1 compile call, got %d", got)
	}
	sysText := callTextByRole(provider.messagesOfCall(1), "system")
	if !strings.Contains(sysText, "Code goals: tests pass, no new lint errors.") {
		t.Fatalf("compile system message missing the workspace instructions:\n%s", sysText)
	}
	if !strings.Contains(sysText, "AUTHORITATIVE workspace/project instructions") {
		t.Fatalf("compile system message missing the authoritative-context framing:\n%s", sysText)
	}
}

// TestGoalCompile_WorkspaceInstructionsAbsentWhenNoWorkspace proves the
// converse: with no resolvable workspace (twoPhaseHarness's default — no
// opts.WorkspaceID and no default workspace seeded), the compile input
// never renders the workspace-instructions heading.
func TestGoalCompile_WorkspaceInstructionsAbsentWhenNoWorkspace(t *testing.T) {
	al, agentInst, provider, _, _, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the report is saved"), nil
		}, nil)

	setGoal(t, al, agentInst, opts, "write the report")
	sysText := callTextByRole(provider.messagesOfCall(1), "system")
	if strings.Contains(sysText, "AUTHORITATIVE workspace/project instructions") {
		t.Fatalf("compile system message must omit the workspace-instructions heading when none resolves:\n%s", sysText)
	}
}
