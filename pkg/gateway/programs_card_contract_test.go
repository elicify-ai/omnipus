// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramCard_Contract (order 34, Integration) — S-140 (the
// program card while the check is slow, fails, or the page reloads) and
// S-141 (replaced before the card's check finished).
//
// The card contract fields — program_id and program_generation on the
// request state — and the check barriers do not exist at RED: the card
// itself cannot even be raised (the program recipient is missing, proven by
// the card-gate pack's BLOCKED rows). Each scenario here reports BLOCKED
// naming its seam — never a skip — with the spec's verbatim card texts
// recorded for the same test author's GREEN reconciliation.
//
// Oracle: US-8 scenario texts, FR-051, the message table. Expected values
// come from the spec, never from the handlers.

package gateway

import "testing"

// s1CardContractSeam is the missing seam both scenarios share.
const s1CardContractSeam = "the program card cannot be raised (no program recipient, FR-051) and the request state carries no program_id/program_generation, and the check barrier does not exist (FR-047)"

func TestProgramCard_Contract(t *testing.T) {
	t.Run("S140_card_while_check_slow_fails_or_page_reloads", func(t *testing.T) {
		// GREEN asserts here (spec S-140): a program card submitted with a
		// token whose check waits on a barrier → Dev continues once, at
		// once, saying the token is stored and being checked; after the page
		// reload the card still shows "Stored — checking the account…"
		// derived from program_id and program_generation; after the check is
		// released with exit 1 the card shows the failed-check message and
		// Settings → Programs shows the same; Dev's continuation never
		// claimed a result.
		t.Fatalf("BLOCKED: %s — required by S-140's card texts and never-claim assertion", s1CardContractSeam)
	})

	t.Run("S141_replaced_before_the_cards_check_finished", func(t *testing.T) {
		// GREEN asserts here (spec S-141): a card submitted with generation
		// N whose check waits on a barrier; the person pastes a different
		// token in Settings (generation N+1); both checks released → the
		// card shows "Stored — replaced by a newer token" and never shows
		// generation N+1's account; Settings shows N+1's result; the request
		// state still carries program_generation: N.
		t.Fatalf("BLOCKED: %s — required by S-141's replaced-card text and program_generation pin", s1CardContractSeam)
	})
}
