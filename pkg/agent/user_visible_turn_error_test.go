// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the 2026-10-04 founder contract on the person-facing text of a
// refused turn ("the fence speaks like a person, not like a shrug"), pkg/agent
// side — the text half of the in-flight fence work. The refusal rows (a stop
// in flight must refuse, touch nothing, dispatch nothing) are already pinned
// by inbound_fence_contract_test.go — this pack does NOT duplicate them; it
// pins what the person is SHOWN when that refusal happens.
//
// Contract under test (founder decision, 2026-10-04):
//
//   - When a chat turn is refused because a stop is still in flight, the text
//     a person is shown contains both "retry" and "stop", and does not
//     contain "can't tell why".
//   - Any other turn error keeps today's generic or typed catalogue text.
//
// The function under test is userVisibleTurnError(err error) string, which the
// production fix (landing separately) adds to translate_error.go and
// session_worker.go::processTurn publishes in place of
// TranslateTurnError(err).Message. Today processTurn publishes the catalogue
// message for the classifier's verdict, and the in-flight refusal — a plain
// fmt.Errorf from revive_inbound.go::inboundStopFenceInFlight, carrying no
// sentinel — classifies as CodeUnknown, so a person asking "why can't I send
// anything?" is told "This turn didn't finish, and we can't tell why." The
// tests below are RED until the fix lands.
//
// Expected RED at this base, deterministic: the in-flight row fails on the
// generic sentence actually shown (no "stop" in it, and "can't tell why"
// present) — not on setup. The preservation rows (typed and opaque errors)
// encode today's behaviour and are expected GREEN once the function exists;
// they are the mutation-killers for a fix that rewrites every error's text.
//
// Oracle provenance: the in-flight row's expected values are the founder
// contract's three properties, nothing more (the shipped copy is not pinned
// word-for-word — the contract pins word presence and the banned sentence).
// The preservation rows' expected values are the contract catalogue
// (contracts/components/schemas/LLMError.yaml x-user-messages), read through
// the generated map via UserMessageForCode — the same source
// session_worker.go::processTurn serves from today, never an observed output
// of the code under test.
//
// Production code is untouched here — RED only (elicify-test-writing skill,
// qa-lead RED duty; green and mutation proofs are CHECK's).

package agent

import (
	"errors"
	"strings"
	"testing"
)

// typographicApostrophe is U+2019, which the contract catalogue uses in its
// copy ("didn’t", "can’t" — pkg/api/generated/llm_error_messages.gen.go,
// "unknown" row). normalizeApostrophes folds it to the ASCII form before any
// substring assertion, so the banned-sentence check can actually SEE today's
// generic sentence — an ASCII-only needle ("can't tell why") never matches
// the catalogue's "can’t tell why" and the negative assertion would be blind
// to exactly the failure it exists to catch.
const typographicApostrophe = "’"

// normalizeApostrophes folds typographic apostrophes to ASCII so substring
// assertions match either spelling of the catalogue copy.
func normalizeApostrophes(s string) string {
	return strings.ReplaceAll(s, typographicApostrophe, "'")
}

// TestUserVisibleTurnError_InFlightStopRefusal_NamesStopAndRetryWithoutGenericSentence
// pins the contract's in-flight row: the text a person is shown for a turn
// refused because a stop is still in flight contains both "retry" and "stop",
// and never the generic "can't tell why" sentence.
//
// The input is the REAL production error value processTurn receives on this
// path — inboundStopFenceInFlight's refusal, returned verbatim through
// runInboundTurnWithRevival — captured from the production constructor over a
// real seeded fence record, not a hand-built lookalike: whether the fix
// recognizes the refusal by sentinel or by text, the value it must translate
// is this one. (The refusal itself — that this error is returned at all, that
// nothing is appended or dispatched — is the fence pack's row,
// inbound_fence_contract_test.go; this test only consumes the error as input.)
func TestUserVisibleTurnError_InFlightStopRefusal_NamesStopAndRetryWithoutGenericSentence(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	sessionID := newTestSteeringSession(t, al, adr093Workspace)
	// Seeds exactly the in-flight shape: state running, Stop stamped for the
	// record's current generation, no landed stop — the record
	// lifecycleInFlightStopFence refuses.
	adr093StoppedRoot(t, al, sessionID)

	refusal := al.inboundStopFenceInFlight(sessionID)
	if refusal == nil {
		t.Fatalf("inboundStopFenceInFlight(%q) returned nil over a seeded in-flight fence — no refusal error to translate. "+
			"That is the fence pack's row (inbound_fence_contract_test.go), not this test's; this test needs the error value the "+
			"chat-turn path hands to userVisibleTurnError", sessionID)
	}

	text := userVisibleTurnError(refusal)
	lowered := normalizeApostrophes(strings.ToLower(text))

	// Contract property 1 of 2: the word "retry" reaches the person. The
	// contract writes it lowercase; sentence-position capitalization must not
	// decide the contract, so the match is case-insensitive.
	if !strings.Contains(lowered, "retry") {
		t.Fatalf("userVisibleTurnError for an in-flight stop refusal does not tell the person they can retry.\ntext: %q\ncontract: the reply must contain \"retry\"", text)
	}
	// Contract property 2 of 2: the word "stop" reaches the person — the one
	// thing that distinguishes this reply from today's generic shrug, which
	// names neither the stop nor anything else that happened.
	if !strings.Contains(lowered, "stop") {
		t.Fatalf("userVisibleTurnError for an in-flight stop refusal does not mention the stop.\ntext: %q\ncontract: the reply must contain \"stop\"", text)
	}
	// The banned sentence: today's generic CodeUnknown copy is exactly what
	// the person must stop being shown for this refusal. Checked on the
	// apostrophe-normalized lowercased text so the check can see the catalogue's
	// typographic-apostrophe spelling ("can’t tell why") — see
	// normalizeApostrophes above.
	if strings.Contains(lowered, "can't tell why") {
		t.Fatalf("userVisibleTurnError for an in-flight stop refusal still shows the generic unknown sentence.\ntext: %q\ncontract: the reply must not contain \"can't tell why\"", text)
	}
}

// TestUserVisibleTurnError_TypedTurnErrors_KeepCatalogueCopy pins the
// contract's preservation clause for typed turn errors: an error the
// classifier already attributes (agent on no workspace, agent with no model)
// keeps today's typed catalogue copy, byte for byte. Expected values are the
// contract catalogue (LLMError.yaml x-user-messages) read via
// UserMessageForCode — the spec's copy source, not an observed output. These
// rows kill a fix that appends the new fence copy to every error.
func TestUserVisibleTurnError_TypedTurnErrors_KeepCatalogueCopy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code LLMErrorCode
	}{
		{
			name: "agent on no workspace keeps agent_not_configured copy",
			err:  ErrAgentNotWorkspaceMember,
			code: CodeAgentNotConfigured,
		},
		{
			name: "agent with no model keeps model_unassigned copy",
			err:  ErrAgentModelUnassigned,
			code: CodeModelUnassigned,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := UserMessageForCode(tc.code)
			if got := userVisibleTurnError(tc.err); got != want {
				t.Fatalf("userVisibleTurnError for a typed turn error (%s) changed today's catalogue copy.\ngot:  %q\nwant: %q\ncontract: any other turn error keeps today's typed catalogue text", tc.code, got, want)
			}
		})
	}
}

// TestUserVisibleTurnError_OpaqueTurnError_KeepsGenericUnknownCopy pins the
// contract's preservation clause for the residual class: an error carrying no
// sentinel and no pinned substring keeps today's generic unknown sentence —
// the "can't tell why" copy stays correct for the errors that genuinely have
// nothing better to say. The opaque text matches no classifier substring
// (verified against every pinned list in translate_error.go), so today it
// classifies as CodeUnknown.
func TestUserVisibleTurnError_OpaqueTurnError_KeepsGenericUnknownCopy(t *testing.T) {
	opaque := errors.New("the flipdrive coil overheated mid-cycle")
	want := UserMessageForCode(CodeUnknown)
	if got := userVisibleTurnError(opaque); got != want {
		t.Fatalf("userVisibleTurnError for an unattributable turn error changed today's generic copy.\ngot:  %q\nwant: %q\ncontract: any other turn error keeps today's generic text", got, want)
	}
}
