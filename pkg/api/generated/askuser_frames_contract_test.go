// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// askuser_frames_contract_test.go — askuserquestion-tool-spec v3 test 8
// (contract half): the ask_user_question / ask_user_answer frames and the
// session_state pending_asks snapshot round-trip schema-valid JSON against
// BOTH canonical copies — the asyncapi.yaml inline schema (codegen source)
// AND the contracts/components/schemas/*.yaml file (process +
// inboundschemas source) — which is exactly the hand-sync obligation the
// frame headers declare (the third copy, pkg/gateway/inboundschemas/, is
// machine-synced from the schema file by gen-contracts step 5 and guarded
// by verify-contracts' git-diff gate).
package generated

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func fixtureAskUserCardPending() AskUserQuestionCard {
	desc := "The 14 currently waiting."
	rec := "Only unanswered"
	defSafe := true
	multi := true
	ctxMd := "Proposed criteria:\n\n- every question answered\n- one concrete next step"
	at := "2026-09-06T12:30:00Z"
	card := AskUserQuestionCard{
		CardId:        "ask_0123456789abcdef0123456789abcdef",
		SessionId:     "session_owner_1",
		AgentId:       "mia",
		Status:        "pending",
		CreatedAt:     "2026-09-06T12:00:00Z",
		DefaultSafeAt: &at,
		AutoResolved:  []string{"Sending"},
	}
	card.Questions = []struct {
		Context     *string `json:"context,omitempty"`
		DefaultSafe *bool   `json:"default_safe,omitempty"`
		Header      string  `json:"header"`
		MultiSelect *bool   `json:"multi_select,omitempty"`
		Options     []struct {
			Description *string `json:"description,omitempty"`
			Label       string  `json:"label"`
		} `json:"options"`
		Question    string  `json:"question"`
		Recommended *string `json:"recommended,omitempty"`
	}{
		{
			Header:   "Scope",
			Question: "Which emails should this goal cover?",
			Context:  &ctxMd,
			Options: []struct {
				Description *string `json:"description,omitempty"`
				Label       string  `json:"label"`
			}{
				{Label: "Only unanswered", Description: &desc},
				{Label: "All customer email"},
			},
			Recommended: &rec,
		},
		{
			Header:      "Sending",
			Question:    "Draft or send directly?",
			MultiSelect: &multi,
			DefaultSafe: &defSafe,
			Recommended: &rec,
			Options: []struct {
				Description *string `json:"description,omitempty"`
				Label       string  `json:"label"`
			}{
				{Label: "Only unanswered"},
				{Label: "Send directly"},
			},
		},
	}
	return card
}

func fixtureAskUserCardAnswered() AskUserQuestionCard {
	card := fixtureAskUserCardPending()
	card.Status = "answered"
	card.DefaultSafeAt = nil
	ft := "something custom"
	card.Answers = []struct {
		AutoDefault bool     `json:"auto_default"`
		FreeText    *string  `json:"free_text,omitempty"`
		Header      string   `json:"header"`
		Question    string   `json:"question"`
		Selected    []string `json:"selected,omitempty"`
	}{
		{Header: "Scope", Question: "Which emails should this goal cover?", FreeText: &ft},
		{Header: "Sending", Question: "Draft or send directly?", Selected: []string{"Only unanswered"}, AutoDefault: true},
	}
	return card
}

// mustPassBothCopies validates v against the asyncapi inline schema AND the
// component schema file of the same name — the dual-copy sync guard.
func mustPassBothCopies(t *testing.T, schemaName string, v any) {
	t.Helper()
	require.NoError(t, validateAgainstAsyncAPISchema(t, schemaName, v),
		"%s: asyncapi.yaml inline copy rejected the fixture", schemaName)
	require.NoError(t, validateAgainstComponentSchema(t, schemaName, v),
		"%s: contracts/components/schemas copy rejected the fixture", schemaName)
}

func TestContract_AskUserQuestionFrame_PendingAndTerminal(t *testing.T) {
	mustPassBothCopies(t, "AskUserQuestionFrame", AskUserQuestionFrame{
		Type: "ask_user_question",
		Card: fixtureAskUserCardPending(),
	})
	mustPassBothCopies(t, "AskUserQuestionFrame", AskUserQuestionFrame{
		Type: "ask_user_question",
		Card: fixtureAskUserCardAnswered(),
	})
}

func TestContract_AskUserQuestionCard_RejectsMalformed(t *testing.T) {
	// Zero questions violates minItems: 1 on both copies.
	card := fixtureAskUserCardPending()
	card.Questions = card.Questions[:0]
	require.Error(t, validateAgainstAsyncAPISchema(t, "AskUserQuestionCard", card))
	require.Error(t, validateAgainstComponentSchema(t, "AskUserQuestionCard", card))
}

func TestContract_AskUserAnswerFrame_SubmitAndCancel(t *testing.T) {
	ft := "my own answer"
	auto := true
	submit := AskUserAnswerFrame{
		Type:      "ask_user_answer",
		CardId:    "ask_0123456789abcdef0123456789abcdef",
		SessionId: "session_owner_1",
	}
	submit.Answers = []struct {
		AutoDefault *bool    `json:"auto_default,omitempty"`
		FreeText    *string  `json:"free_text,omitempty"`
		Header      string   `json:"header"`
		Selected    []string `json:"selected,omitempty"`
	}{
		{Header: "Scope", FreeText: &ft},
		{Header: "Sending", Selected: []string{"Only unanswered"}, AutoDefault: &auto},
	}
	mustPassBothCopies(t, "AskUserAnswerFrame", submit)

	cancelFlag := true
	mustPassBothCopies(t, "AskUserAnswerFrame", AskUserAnswerFrame{
		Type:      "ask_user_answer",
		CardId:    "ask_0123456789abcdef0123456789abcdef",
		SessionId: "session_owner_1",
		Cancel:    &cancelFlag,
	})
}

func TestContract_SessionStateFrame_PendingAsksSnapshot(t *testing.T) {
	frame := SessionStateFrame{
		Type:             "session_state",
		UserId:           "daniel",
		PendingApprovals: []SessionStatePendingApproval{},
		EmittedAt:        "2026-09-06T12:00:00Z",
		PendingAsks:      []AskUserQuestionCard{fixtureAskUserCardPending()},
	}
	mustPassBothCopies(t, "SessionStateFrame", frame)

	// Absent pending_asks stays valid (older-gateway compatibility).
	frame.PendingAsks = nil
	mustPassBothCopies(t, "SessionStateFrame", frame)
}
