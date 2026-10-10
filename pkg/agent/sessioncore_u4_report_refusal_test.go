// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U4 — refused report arrival (FR-013, BDD-04.3, T06/T16; #1211).
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md
// FR-013 ("Refused report arrival MUST give child typed refusal/applicable
// retry hint and parent rejected/not-delivered observation (#1211). Preserve
// report body/rate/unacked/type ceilings ... no auto retry/overflow
// store/service."), BDD-04.3, and issue #1211 D3 ("the rejection the child
// receives is not a machine-readable rate_limited result, so a child cannot
// back off reliably"). The C-INBOX seam (spec §Seams) fixes the SHAPE: "Typed
// refusal/retry hint ... use current result/status surfaces" — the child-side
// result surface is the message_parent tool result.

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// u4NewMessageParentTool wires the real message_parent tool to the real
// deliverer and returns a context carrying the child's own delegate session id
// (mirrors the production wiring; no fake deliverer).
func u4NewMessageParentTool(lifecycle *session.LifecycleStore, deliverer *SteerUpwardDeliverer, childID string) (*tools.MessageParentTool, context.Context) {
	tool := tools.NewMessageParentTool(deliverer, lifecycle)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	return tool, ctx
}

// u4LooksLikeRetryHint reports whether a refusal message carries an
// actionable retry/back-off hint. FR-013 requires an "applicable retry hint";
// the spec does not fix its exact wording (C-INBOX: reuse the current result
// surface), so this asserts the minimal derivable property — the refusal
// tells the child it may retry — rather than guessing a literal string.
func u4LooksLikeRetryHint(text string) bool {
	lower := strings.ToLower(text)
	for _, token := range []string{"retry", "try again", "back off", "backoff"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// TestMessageParent_RateCapRefusal_TypedWithRetryHint covers FR-013 /
// BDD-04.3 (#1211 D3): when a report is refused at the preserved rate cap, the
// child gets (a) a TYPED refusal and (b) an applicable retry hint on the
// current result surface, and the refused content is NOT admitted.
//
// RED today: the typed half already holds (the sentinel wraps through the
// Deliver chain), but the refusal carries no retry hint — the current text is
// only "session: inbox: child send rate exceeded (N/min for session X)", which
// is exactly the D3 defect ("a child cannot back off reliably").
func TestMessageParent_RateCapRefusal_TypedWithRetryHint(t *testing.T) {
	_, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	tool, ctx := u4NewMessageParentTool(lifecycle, deliverer, childID)

	// Fill the (owner, child) rate window so the tool's own delivery is refused.
	const cap = session.DefaultChildSendRatePerMinute
	for i := 0; i < cap; i++ {
		if _, err := inbox.Append(parentID, u4GeneratedProgress(t, childID, fmt.Sprintf("pre-%02d", i))); err != nil {
			t.Fatalf("pre-fill Append #%d: %v", i, err)
		}
	}

	res := tool.Execute(ctx, map[string]any{"kind": "progress", "text": "the refused report"})
	if !res.IsError {
		t.Fatalf("message_parent at the rate cap: IsError = false, want a visible refusal (FR-013)")
	}
	if !errors.Is(res.Err, session.ErrInboxRateLimited) {
		t.Fatalf("refusal error = %v, want a TYPED refusal wrapping session.ErrInboxRateLimited "+
			"(FR-013 / #1211 D3)", res.Err)
	}
	if !u4LooksLikeRetryHint(res.ForLLM) {
		t.Fatalf("refusal text = %q carries no retry/back-off hint — FR-013 requires an applicable "+
			"retry hint so the child can back off (#1211 D3)", res.ForLLM)
	}

	// The refused content must never have been admitted.
	msgs, _, _, derr := inbox.Drain(parentID, childID, "", 100)
	if derr != nil {
		t.Fatalf("Drain: %v", derr)
	}
	for _, m := range msgs {
		if p, aerr := m.AsSessionMessageProgress(); aerr == nil && p.Text == "the refused report" {
			t.Fatalf("refused report content reached the parent inbox — BDD-04.3 forbids claiming " +
				"rejected content delivered")
		}
	}
}

// TestParent_RejectedArrivalObservation_Visible covers the second half of
// FR-013 / BDD-04.3 (#1211 D2): the parent must have a rejected/not-delivered
// OBSERVATION of a refused arrival — "not accepted content or loss-free
// has_more inference".
//
// BLOCKED: the spec mandates the observation but commissions no frame or field
// for it (C-INBOX: "use current result/status surfaces"), and no current
// result/status surface carries a rejected-arrival observation (checked:
// MessageParentResponse is child-side only; DelegateStatusResponse carries
// session/last_checkpoint/last_progress/unacked_count and no rejection field;
// no SessionMessage variant records a refusal). Per the RED contract this test
// fails loudly rather than guessing a shape; the missing surface is reported
// alongside it.
func TestParent_RejectedArrivalObservation_Visible(t *testing.T) {
	t.Fatal("BLOCKED: parent rejected/not-delivered observation surface not implemented — required by " +
		"FR-013 / BDD-04.3 (#1211). The spec mandates parent visibility of a refused arrival but " +
		"commissions no frame or field (C-INBOX: 'use current result/status surfaces'), and no existing " +
		"result/status surface carries it. A shape decision is needed before this test can assert one.")
}
