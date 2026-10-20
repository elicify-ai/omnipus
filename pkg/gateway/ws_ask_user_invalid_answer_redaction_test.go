// Omnipus — #1329 CHECK Minor F-1: genuine invalid-answer redaction
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAskUserInvalidAnswerUsesFixedMessageWithoutReflectionOrCorrelationChange
// traces its exact message to the 2026-10-17 team-lead dispatch, not observed
// implementation output. ErrorFrame's contract requires the submitted card and
// session identifiers and forbids first-send fields on a card-action error.
// The real registry rejects an option absent from the real pending card; no
// storage fault or fabricated rejection stands in for answer validation.
// GREEN, independent mutations and the proof-of-failability gate are deferred
// to CHECK. Existing tests and their fixtures are deliberately unchanged.
func TestAskUserInvalidAnswerUsesFixedMessageWithoutReflectionOrCorrelationChange(t *testing.T) {
	const wantMessage = "Could not accept this question-card action. Review the current card and try again."
	const invalidSelection = "PRIVATE-INVALID-SELECTED-ANSWER-1329"
	const privateFreeText = "PRIVATE-SUBMITTED-FREE-TEXT-1329"

	f := qaNewRejectionFixture(t)
	h := &WSHandler{askUserReg: f.reg}
	client, other := qaAskConnPair()
	h.sessions = map[string]*wsConn{"other": other}

	var submitted generated.AskUserAnswerFrame
	require.NoError(t, json.Unmarshal(qaValidAnswerFrame(f.cardA, f.sid, false), &submitted))
	submitted.Answers[0].Selected = []string{invalidSelection}
	submitted.Answers[1].Selected = nil
	freeText := privateFreeText
	submitted.Answers[1].FreeText = &freeText

	h.handleAskUserAnswer(client, submitted)

	// This also checks both exact correlation values, absence of first-send
	// fields, and that an unrelated connection receives nothing.
	response := qaAssertCorrelatedError(t, client, other, f.cardA, f.sid)
	assert.Equal(t, wantMessage, response["message"], "a genuinely invalid answer must use the dispatch-approved fixed message")
	wire, err := json.Marshal(response)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), invalidSelection, "no part of the client error may reflect the submitted invalid selection")
	assert.NotContains(t, string(wire), privateFreeText, "no part of the client error may reflect submitted free text")
	qaAssertQueueUntouched(t, f)
	select {
	case extra := <-client.sendCh:
		t.Errorf("one invalid answer must produce exactly one rejection frame, got extra: %s", extra)
	default:
	}
}
