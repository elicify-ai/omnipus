// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const (
	steeredFirstAnswer  = "FIRST-DRAFT: the complete original answer"
	steeredFollowUp     = "follow-up sent while the first answer was still being written"
	steeredSecondAnswer = "SECOND-REPLY: the addendum for the follow-up"
)

// answerThenSteerProvider answers the first call with a complete, tool-free
// answer, and while that call is in flight a person sends a follow-up into
// the running turn (Send into a running reply). The second call answers the
// follow-up. It records every request and the running turn's history key.
type answerThenSteerProvider struct {
	mu         sync.Mutex
	al         *AgentLoop
	calls      int
	requests   [][]providers.Message
	sessionKey string
}

func (p *answerThenSteerProvider) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, append([]providers.Message(nil), msgs...))
	if p.calls == 1 {
		if ts := p.al.getAnyActiveTurnState(); ts != nil {
			p.sessionKey = ts.sessionKey
		}
		if err := p.al.Steer(providers.Message{Role: "user", Content: steeredFollowUp}); err != nil {
			return nil, err
		}
		return &providers.LLMResponse{Content: steeredFirstAnswer}, nil
	}
	return &providers.LLMResponse{Content: steeredSecondAnswer}, nil
}

func (p *answerThenSteerProvider) GetDefaultModel() string { return "scripted-model" }

// TestRunTurn_SteeringAfterACompletedAnswer_KeepsThatAnswer (UAT-A on bundle
// #1370 at 1380a6d66): a follow-up sent into a running reply arrived just as
// the reply completed. The turn continued for the follow-up but dropped the
// completed reply: it was never added to the model history (the model
// redrafted it from scratch) and never saved (after a reload only the second
// reply was left). The completed answer must be kept - in the next request,
// ahead of the follow-up, and in the saved history - and the turn then
// answers the follow-up.
func TestRunTurn_SteeringAfterACompletedAnswer_KeepsThatAnswer(t *testing.T) {
	provider := &answerThenSteerProvider{}
	al := newRepeatStopLoop(t, provider)
	provider.al = al

	finalContent, err := al.processDirectPrimaryForTest(context.Background(),
		"draft the full plan; I may add something while you write", "test-session-steer-after-final")
	require.NoError(t, err)
	assert.Equal(t, steeredSecondAnswer, finalContent, "the turn answers the follow-up last")

	provider.mu.Lock()
	defer provider.mu.Unlock()
	require.Equal(t, 2, provider.calls, "one call for the answer, one for the follow-up")
	second := provider.requests[1]
	firstAt, followAt := -1, -1
	for i, m := range second {
		if m.Role == "assistant" && strings.Contains(m.Content, steeredFirstAnswer) {
			firstAt = i
		}
		if m.Role == "user" && strings.Contains(m.Content, steeredFollowUp) {
			followAt = i
		}
	}
	require.NotEqual(t, -1, firstAt,
		"the completed first answer must be in the model's next request, or the model redrafts it")
	require.NotEqual(t, -1, followAt, "the follow-up must reach the model")
	assert.Less(t, firstAt, followAt, "the first answer comes before the follow-up")

	require.NotEmpty(t, provider.sessionKey, "SETUP: the running turn's history key was captured")
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	var saved []string
	for _, m := range inst.Sessions.GetHistory(provider.sessionKey) {
		if m.Role == "assistant" && m.Content != "" {
			saved = append(saved, m.Content)
		}
	}
	assert.Equal(t, []string{steeredFirstAnswer, steeredSecondAnswer}, saved,
		"both completed answers must be saved, in order, so a reload shows both")
}

// TestRunTurn_SteeringAfterACompletedAnswer_BothAnswersInTheSavedTranscript:
// the same scenario through a real chat session's transcript - what a reload
// or reopen renders. Both completed answers must be saved as chat entries, in
// order, around the follow-up.
func TestRunTurn_SteeringAfterACompletedAnswer_BothAnswersInTheSavedTranscript(t *testing.T) {
	provider := &answerThenSteerProvider{}
	al := newRepeatStopLoop(t, provider)
	provider.al = al
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)

	store := al.GetSessionStore()
	require.NotNil(t, store, "SETUP: shared session store")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", inst.ID)
	require.NoError(t, err)

	_, err = al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "steer-after-final-transcript",
		Channel:             "webchat",
		ChatID:              meta.ID,
		UserMessage:         "draft the full plan; I may add something while you write",
		DefaultResponse:     defaultResponse,
		TranscriptSessionID: meta.ID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var assistants []string
	for _, e := range entries {
		if e.Role == "assistant" && e.Content != "" {
			assistants = append(assistants, e.Content)
		}
	}
	assert.Equal(t, []string{steeredFirstAnswer, steeredSecondAnswer}, assistants,
		"after a reload the saved chat must show both completed answers, in order")
}
