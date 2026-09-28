// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// continuation_target_failure_scope_test.go: RED test for the design note's
// addendum exit (c) (coordination/logs/continuescope-architect-note.md,
// 2026-09-27): processTurn's buildContinuationTarget call failing with a
// genuine (non-ErrNoContinuationTarget) error.
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestSessionWorker_ContinuationTargetBuildFailure_NoticesWithoutTouchingQueue
// covers exit (c): buildContinuationTarget's only possible non-sentinel
// failure is resolveMessageRoute(msg) itself erroring (the note traced this
// exhaustively — no other fallible step exists in that function). The note's
// two conclusions this test pins:
//
//  1. w.scope is NOT a safe substitute for the steering queue's real key at
//     this exit (it can carry a ":"+msg.SessionID suffix the queue's actual
//     key never has), and the real key cannot be safely re-derived here
//     either (deriving it requires the exact resolveMessageRoute(msg) call
//     that just failed) — so the fix must NOT attempt to check or touch the
//     steering queue at all at this exit. This test seeds an UNRELATED
//     scope's queue before the failing turn runs and asserts its count is
//     byte-for-byte unchanged afterward — true both before and after the
//     fix, and a regression test against ever touching it later.
//  2. The existing deferred response guard already publishes the turn's own
//     answer to msg.Channel/msg.ChatID on this exact failure path (unaffected
//     by this fix) — the fix ADDS a second, distinct notice about the
//     steering-queue side, which is what today's code is missing.
//
// Mechanism: a registry with zero registered agents makes
// resolveMessageRoute(msg) fail deterministically for an ordinary (non
// "system"-channel) message — the same "no agent available for route"
// failure buildContinuationTarget's own resolveMessageRoute call surfaces
// (pkg/agent/loop_inbound.go, ~line 483-497), which is exactly what the note
// traces as buildContinuationTarget's one possible non-ErrNoContinuationTarget
// failure cause.
//
// TODAY (bug): processTurn's exit-(c) branch (session_worker.go, current
// code, ~line 579-585) only logs a WarnCF and returns — no second notice is
// ever published. Exactly one outbound message appears (the turn's own
// error response, via the pre-existing deferred guard); this test wants two.
func TestSessionWorker_ContinuationTargetBuildFailure_NoticesWithoutTouchingQueue(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// Zero agents registered: registry.GetDefaultAgent() returns nil, so
			// resolveMessageRoute's final fallback (loop_inbound.go ~line
			// 483-497) returns a genuine, non-sentinel
			// "no agent available for route" error for an ordinary message —
			// buildContinuationTarget propagates that error verbatim (it is not
			// ErrNoContinuationTarget, which only fires for msg.Channel=="system").
			List: []config.AgentConfig{},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	// Precondition: confirm buildContinuationTarget actually fails this way
	// for this message, and that the failure is not the ErrNoContinuationTarget
	// sentinel (which takes a completely different, already-correct code path).
	probeMsg := bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-c"},
		ChatID:  "chat-c",
		Content: "hello",
		Peer:    bus.Peer{Kind: bus.PeerDirect, ID: "user-c"},
	}
	if _, err := al.buildContinuationTarget(probeMsg); err == nil {
		t.Fatal("precondition failed: buildContinuationTarget must fail when the registry has zero agents")
	}

	// Seed an UNRELATED scope's queue — exit (c) must never touch ANY bucket,
	// since it cannot safely identify which one (if any) belongs to this
	// message's own session.
	const unrelatedScope = "unrelated-scope-exit-c"
	if _, err := al.EnqueueSteeringMessage(unrelatedScope, "", providers.Message{
		Role: "user", Content: "must remain exactly where it is",
	}, ""); err != nil {
		t.Fatalf("EnqueueSteeringMessage(unrelatedScope) unexpected error: %v", err)
	}
	totalBefore := al.steering.len()
	if totalBefore != 1 {
		t.Fatalf("precondition failed: steering queue total = %d before the turn, want 1", totalBefore)
	}

	w := newSessionWorker("exit-c-worker-scope", al, func() {})
	w.processTurn(context.Background(), probeMsg)

	// Invariant (true both before and after the fix): no bucket was touched.
	if got := al.steering.len(); got != totalBefore {
		t.Fatalf("steering queue total after exit (c) = %d, want unchanged %d — exit (c) must never "+
			"check or touch the steering queue (the real key cannot be safely re-derived here)",
			got, totalBefore)
	}
	if got := al.pendingSteeringCountForScope(unrelatedScope); got != 1 {
		t.Fatalf("pending count for the unrelated scope = %d, want 1 (untouched)", got)
	}

	// The turn's own answer must still reach msg.Channel/msg.ChatID via the
	// existing (unaffected) deferred response guard.
	var sawOwnResponse, sawDistinctNotice bool
	var firstContent, secondContent string
	select {
	case out := <-msgBus.OutboundChan():
		firstContent = out.Content
		if out.Channel != probeMsg.Channel || out.ChatID != probeMsg.ChatID {
			t.Fatalf("first outbound (channel=%q chat=%q) does not target the original message (channel=%q chat=%q)",
				out.Channel, out.ChatID, probeMsg.Channel, probeMsg.ChatID)
		}
		if out.Content != "" {
			sawOwnResponse = true
		}
	default:
		t.Fatal("expected at least one outbound message (the turn's own error response) — got none")
	}

	// The FIX adds a second, distinct notice about the steering-queue side —
	// this is what today's code is missing (RED).
	select {
	case out := <-msgBus.OutboundChan():
		secondContent = out.Content
		if out.Channel != probeMsg.Channel || out.ChatID != probeMsg.ChatID {
			t.Fatalf("second outbound (channel=%q chat=%q) does not target the original message (channel=%q chat=%q)",
				out.Channel, out.ChatID, probeMsg.Channel, probeMsg.ChatID)
		}
		if out.Content == "" {
			t.Fatal("second outbound notice is empty")
		}
		if out.Content == firstContent {
			t.Fatalf("second outbound notice is identical to the turn's own response (%q) — "+
				"want a DISTINCT notice about the steering-queue check, not a re-publish", firstContent)
		}
		sawDistinctNotice = true
	default:
		t.Fatal("expected a SECOND, distinct outbound message (the fail-loud notice about the " +
			"steering-queue check) — got none: today's code has no such notice at exit (c), it only logs")
	}

	if !sawOwnResponse || !sawDistinctNotice {
		t.Fatalf("sawOwnResponse=%v sawDistinctNotice=%v (contents: %q, %q)",
			sawOwnResponse, sawDistinctNotice, firstContent, secondContent)
	}
}
