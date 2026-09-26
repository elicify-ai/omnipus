// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// fallback_note.go — provider-messages spec §7.4 fallback note (D7, MAJ-010,
// MIN-102/MIN-103, C-17/C-18). On fallback success the agent emits one
// provider_fallback frame per fallback event and queues ONE transcript note
// per (session/answered pair). Notes are written at turn end AFTER the
// assistant answer entry (MIN-103/FB-2) — the queue is registered in
// runTurn's defer chain (loop.go) to run after finalizeStreamer.
package agent

import (
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

// fallbackNoteText — the §6 fallback-note sentence. Never "backup". The D12
// hint appears ONLY when unavailable_code is model_retired.
func fallbackNoteText(answeredModel, unavailableModel, unavailableCode string) string {
	text := fmt.Sprintf("Answered by the Fallback model (%s) because %s was unavailable.", answeredModel, unavailableModel)
	if unavailableCode == "model_retired" {
		text += " Pick a new model in the agent's settings."
	}
	return text
}

// fallbackUnavailableCode maps a failed primary attempt to the frame's
// unavailable_code enum {rate_limited, model_retired} (contract
// ProviderFallbackFrame.yaml). model_retired is read off the attempt's own
// error chain (a *common.ProviderError with a 404 + retirement phrase — the
// user-side C-24 detector); a cooldown-skip (C-18) and every other condition
// read rate_limited — the enum has no third value and unavailable_code's only
// consumer is the D12 hint gate.
func fallbackUnavailableCode(attemptErr error) string {
	var pe *common.ProviderError
	if errors.As(attemptErr, &pe) && isModelRetiredBody(pe.Body, pe.Status) {
		return "model_retired"
	}
	return "rate_limited"
}

// fallbackAttemptPair returns the note's pair facts from the chain's FIRST
// attempt record — the primary (first candidate) per MIN-103, whether it
// hard-failed or was cooldown-skipped (C-18).
func fallbackAttemptPair(attempts []providers.FallbackAttempt) (unavailableProvider, unavailableModel string, attemptErr error) {
	if len(attempts) == 0 {
		return "", "", nil
	}
	a := attempts[0]
	return a.Provider, a.Model, a.Error
}

// queueProviderFallbackNote is the §7.4 fallback-success hook, called from
// callProviderOnce on every fallback success. Always emits the
// EventKindProviderFallback frame (§7.4: a later iteration re-frames); queues
// the transcript note only when the (unavailable, answered) pair is fresh —
// checked against this turn's queued set (MIN-102) and the session transcript
// (restart-safe once-per-pair, C-17/C-18).
func (ts *turnState) queueProviderFallbackNote(answeredModel string, attempts []providers.FallbackAttempt) {
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return
	}
	unavailableProvider, unavailableModel, attemptErr := fallbackAttemptPair(attempts)
	if unavailableProvider == "" && unavailableModel == "" {
		return
	}
	code := fallbackUnavailableCode(attemptErr)
	ts.al.emitEvent(
		EventKindProviderFallback,
		ts.eventMeta("runTurn", "turn.llm.fallback"),
		ProviderFallbackPayload{
			SessionID:        string(ts.routingSessionID),
			TurnID:           ts.turnID,
			AnsweredModel:    answeredModel,
			UnavailableModel: unavailableModel,
			UnavailableCode:  code,
		},
	)

	// Within-turn check first (MIN-102): a pair already queued this turn is
	// not re-queued.
	ts.mu.Lock()
	for _, e := range ts.pendingFallbackNotes {
		if e.UnavailableModel == unavailableModel && e.Model == answeredModel {
			ts.mu.Unlock()
			return
		}
	}
	ts.mu.Unlock()

	// Session-level check (restart-safe once-per-pair): the transcript is
	// the authority.
	noted, err := sessionTranscriptHasFallbackNote(ts.transcriptStore, ts.transcriptSessionID, unavailableModel, answeredModel)
	if err != nil {
		// Read failure: suppress the note (fail toward silence, never toward
		// duplicate notes — a chat must not fill with notes, C-17).
		logger.WarnCF("agent", "fallback note: transcript read failed; note suppressed",
			map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
		return
	}
	if noted {
		return
	}
	note := session.TranscriptEntry{
		ID:               uuid.New().String(),
		Type:             session.EntryTypeProviderFallback,
		Role:             "system",
		Content:          fallbackNoteText(answeredModel, unavailableModel, code),
		Timestamp:        time.Now().UTC(),
		AgentID:          ts.resolveActiveAgentID(),
		TurnID:           ts.turnID,
		Model:            answeredModel,
		UnavailableModel: unavailableModel,
		UnavailableCode:  code,
	}
	ts.mu.Lock()
	// Re-check under the lock: two candidates may queue in the same turn.
	for _, e := range ts.pendingFallbackNotes {
		if e.UnavailableModel == unavailableModel && e.Model == answeredModel {
			ts.mu.Unlock()
			return
		}
	}
	ts.pendingFallbackNotes = append(ts.pendingFallbackNotes, note)
	ts.mu.Unlock()
}

// writePendingFallbackNotes writes the turn's queued fallback notes to the
// session transcript. Registered in runTurn's defer chain BEFORE the
// finalizeStreamer defer, so LIFO runs it AFTER finalizeStreamer — the note
// lands after the assistant answer entry (MIN-103/FB-2), on the streaming
// and non-streaming paths alike.
func (ts *turnState) writePendingFallbackNotes() {
	ts.mu.Lock()
	pending := ts.pendingFallbackNotes
	ts.pendingFallbackNotes = nil
	ts.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	for _, note := range pending {
		if ts.abandoned.Load() {
			return
		}
		if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, note); err != nil {
			transcriptWriteFailures.Add(1)
			logger.WarnCF("agent", "could not record fallback note to transcript",
				map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
		}
	}
}

// sessionTranscriptHasFallbackNote reports whether the session transcript
// already holds a provider_fallback note for the pair.
func sessionTranscriptHasFallbackNote(store *session.UnifiedStore, sessionID, unavailableModel, answeredModel string) (bool, error) {
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Type == session.EntryTypeProviderFallback &&
			e.UnavailableModel == unavailableModel &&
			e.Model == answeredModel {
			return true, nil
		}
	}
	return false, nil
}

// turnWaitBudgetForChain returns this turn's D14 wait budget, creating it on
// first use — one budget per TURN, shared by every chain call of the turn.
func (ts *turnState) turnWaitBudgetForChain() *providers.WaitBudget {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.turnWaitBudget == nil {
		ts.turnWaitBudget = providers.NewWaitBudget(providers.MaxWaitBudgetPerTurn)
	}
	return ts.turnWaitBudget
}
