// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_ask_user_test.go — askuserquestion-tool-spec v3 test 8 (gateway wire
// half): toAskUserCard maps pkg/askuser.PendingSet onto the generated card
// shape (default_safe_at materialization, auto_resolved marks, answer echo
// incl. auto_default origin), and the resume dispatcher's origin heuristic.
package gateway

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func askSetFixture() *askuser.PendingSet {
	return &askuser.PendingSet{
		CardID:              "ask_1",
		TranscriptSessionID: "session_owner_1",
		AgentID:             "mia",
		Channel:             "webchat",
		ChatID:              "chat-1",
		Owner:               "daniel",
		CreatedAt:           time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		Status:              askuser.StatusPending,
		Questions: []askuser.Question{
			{
				Header:   "Scope",
				Question: "Which emails?",
				Options: []askuser.Option{
					{Label: "Only unanswered", Description: "the backlog"},
					{Label: "All"},
				},
				Recommended: "Only unanswered",
				DefaultSafe: true,
				Context:     "- every question answered",
			},
			{
				Header:      "Deploy",
				Question:    "Deploy where?",
				MultiSelect: true,
				Options:     []askuser.Option{{Label: "Staging"}, {Label: "Prod"}},
			},
		},
	}
}

func TestToAskUserCard_PendingMapsFaithfully(t *testing.T) {
	set := askSetFixture()
	set.AutoResolved = map[string]time.Time{"Scope": set.CreatedAt.Add(30 * time.Minute)}

	card := toAskUserCard(set, 30*time.Minute)
	assert.Equal(t, "ask_1", card.CardId)
	assert.Equal(t, "session_owner_1", card.SessionId)
	assert.Equal(t, "mia", card.AgentId)
	assert.Equal(t, "pending", card.Status)
	assert.Equal(t, "2026-09-06T12:00:00Z", card.CreatedAt)

	// default_safe_at = created_at + delay, present because a default-safe
	// question exists.
	require.NotNil(t, card.DefaultSafeAt)
	assert.Equal(t, "2026-09-06T12:30:00Z", *card.DefaultSafeAt)
	assert.Equal(t, []string{"Scope"}, card.AutoResolved)

	require.Len(t, card.Questions, 2)
	q0 := card.Questions[0]
	assert.Equal(t, "Scope", q0.Header)
	require.NotNil(t, q0.Recommended)
	assert.Equal(t, "Only unanswered", *q0.Recommended)
	require.NotNil(t, q0.DefaultSafe)
	require.NotNil(t, q0.Context)
	require.Len(t, q0.Options, 2)
	require.NotNil(t, q0.Options[0].Description)
	assert.Nil(t, q0.Options[1].Description, "empty description stays absent")

	q1 := card.Questions[1]
	require.NotNil(t, q1.MultiSelect)
	assert.Nil(t, q1.Recommended)
	assert.Nil(t, q1.DefaultSafe)
	assert.Nil(t, q1.Context)
}

func TestToAskUserCard_NoDefaultSafeNoDeadline(t *testing.T) {
	set := askSetFixture()
	set.Questions[0].DefaultSafe = false
	card := toAskUserCard(set, 30*time.Minute)
	assert.Nil(t, card.DefaultSafeAt, "no default-safe question -> no deadline on the wire")
}

func TestToAskUserCard_TerminalCarriesAnswers(t *testing.T) {
	set := askSetFixture()
	set.Status = askuser.StatusAnswered
	ft := "something else"
	set.Answers = []askuser.Answer{
		{Header: "Scope", QuestionText: "Which emails?", Selected: []string{"Only unanswered"}, AutoDefault: true},
		{Header: "Deploy", QuestionText: "Deploy where?", FreeText: &ft},
	}
	card := toAskUserCard(set, 30*time.Minute)
	assert.Equal(t, "answered", card.Status)
	assert.Nil(t, card.DefaultSafeAt, "terminal card carries no countdown deadline")
	require.Len(t, card.Answers, 2)
	assert.True(t, card.Answers[0].AutoDefault, "auto-default origin marker crosses the wire")
	assert.Equal(t, []string{"Only unanswered"}, card.Answers[0].Selected)
	require.NotNil(t, card.Answers[1].FreeText)
	assert.Equal(t, "something else", *card.Answers[1].FreeText)
	assert.Equal(t, "Deploy where?", card.Answers[1].Question, "question-text echo (o-R2-1) preserved")
}

// The resume origin heuristic: a human submission/cancel is user-initiated;
// the server's all-default auto-submit is not.
func TestAskUserResumeDispatcher_OriginHeuristic(t *testing.T) {
	d := &askUserResumeDispatcher{} // no bus — only the heuristic matters here
	_ = d

	humanSet := askSetFixture()
	humanSet.Status = askuser.StatusAnswered
	humanSet.Answers = []askuser.Answer{
		{Header: "Scope", Selected: []string{"Only unanswered"}, AutoDefault: true},
		{Header: "Deploy", Selected: []string{"Staging"}}, // one human answer
	}
	autoSet := askSetFixture()
	autoSet.Status = askuser.StatusAnswered
	autoSet.Answers = []askuser.Answer{
		{Header: "Scope", Selected: []string{"Only unanswered"}, AutoDefault: true},
	}
	cancelledSet := askSetFixture()
	cancelledSet.Status = askuser.StatusCancelled

	assert.True(t, resumeIsUserInitiated(humanSet))
	assert.False(t, resumeIsUserInitiated(autoSet))
	assert.True(t, resumeIsUserInitiated(cancelledSet))
}
