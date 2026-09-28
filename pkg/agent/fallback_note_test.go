// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// fallback_note_test.go — provider-messages spec §11 row 33 (fallback-note
// edges) plus the D17 multi-turn ruling. Oracles (read from the spec before
// the implementation, per elicify-test-writing oracle independence):
//
//   - §6 note template: "Answered by the Fallback model ({X}) because {Y}
//     was unavailable." — the D12 hint ("Pick a new model in the agent's
//     settings.") appears ONLY when unavailable_code is model_retired.
//   - §7.4 (D17): the once-per-pair cap is scoped per CHAT: within one turn
//     a later same-pair iteration re-frames but does not re-note; a NEW turn
//     in the same chat for the SAME pair does not re-note either. Only a
//     pair CHANGE produces a new note.
//   - §10 Set FB: FB-2 (note AFTER the assistant answer entry in the
//     persisted transcript), FB-4 (once per pair until the pair changes;
//     transcript-derived so it survives a restart).
//   - MIN-102 (within-turn note rate), MIN-103 ({unavailable_model} names
//     the PRIMARY — first candidate — in a multi-hop chain).
//
// Seam: the feature's integration seam (assemble_provider_message_test.go):
// a real providers.FallbackChain classifies scripted provider failures; the
// production entry points turnState.queueProviderFallbackNote and
// writePendingFallbackNotes run over a real session.UnifiedStore (JSONL on
// disk under t.TempDir()); assertions read the persisted transcript back.
// The LLM provider is faked at the process edge only. FB-2's ordering is
// additionally pinned end-to-end through runTurn's real defer chain
// (loop.go::runTurn registers writePendingFallbackNotes BEFORE the Finish
// and finalizeStreamer defers, so LIFO runs it after the assistant answer
// entry is persisted).
package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// The FB-1 chain cast: Y the primary, X the fallback (FB-1); X1 the
// intermediate and X2 the answering fallback of row 33(c)'s multi-hop chain.
const (
	fbNotePrimary = "primary-y" // §10 FB-1 "model Y"
	fbNoteMid     = "mid-x1"    // row 33(c) intermediate candidate
	fbNoteFinal   = "final-x2"  // the answering fallback (FB-1 "model X")
)

// fbNoteScriptedProvider — the process-edge fake. The unavailable model
// returns a 429 whose Retry-After (3600 s) exceeds the §7.4 120 s auto-retry
// ceiling: per §7.4 the chain then skips the candidate's retries (no
// countdown, no wait) and moves to the fallback immediately — the C-18
// cooldown-skip shape — and the test never sleeps. Every other candidate
// answers at once. The 429 body carries no C-24 retirement phrase, so the
// note's unavailable_code is rate_limited and the D12 hint must NOT appear.
type fbNoteScriptedProvider struct {
	mu    sync.Mutex
	calls []string // every candidate model the chain drove, in call order
}

func (p *fbNoteScriptedProvider) Chat(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, model)
	p.mu.Unlock()
	if model != fbNoteFinal {
		// Every candidate except the final fallback fails: primary-y and
		// (for the multi-hop test) mid-x1 both hit the unavailable path.
		// No C-24 retirement phrase → rate_limited, no D12 hint.
		return nil, &common.ProviderError{
			Status:            429,
			Body:              "rate limit exceeded for tenant",
			RetryAfterSeconds: 3600,
		}
	}
	return &providers.LLMResponse{Content: "answered by " + model, FinishReason: "stop"}, nil
}

func (p *fbNoteScriptedProvider) GetDefaultModel() string { return fbNotePrimary }

// runFallbackChain drives the REAL FallbackChain over the given candidates —
// the same chain loop_run_turn.go::callProviderOnce drives — and returns the
// FallbackResult in the exact shape queueProviderFallbackNote receives.
func runFallbackChain(t *testing.T, candidates []providers.FallbackCandidate) (*providers.FallbackResult, *fbNoteScriptedProvider) {
	t.Helper()
	p := &fbNoteScriptedProvider{}
	chain := providers.NewFallbackChain(providers.NewCooldownTracker())
	res, err := chain.Execute(context.Background(), candidates, func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
		return p.Chat(ctx, nil, nil, model, nil)
	})
	if err != nil {
		t.Fatalf("chain.Execute over %v: %v", candidates, err)
	}
	return res, p
}

// fbNoteStore builds a real UnifiedStore (JSONL on disk) plus one chat
// session, the sibling-test harness (assemble_provider_message_test.go).
func fbNoteStore(t *testing.T) (*session.UnifiedStore, string) {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return store, meta.ID
}

// fbNoteTurnState is the minimal turnState the queue/write seam needs — the
// sibling-test shape (nil al is safe: AgentLoop.emitEvent no-ops on a nil
// receiver, so the frames drop but the note machinery runs for real).
func fbNoteTurnState(store *session.UnifiedStore, sessionID, turnID string) *turnState {
	return &turnState{
		turnID:              turnID,
		agentID:             "main",
		transcriptSessionID: sessionID,
		transcriptStore:     store,
	}
}

// fbNoteEntries returns the note entries in the persisted transcript.
func fbNoteEntries(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	var notes []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeProviderFallback {
			notes = append(notes, e)
		}
	}
	return notes
}

// ── Row 33(a) / MIN-102: 3 iterations of one turn → 1 note ────────────────
//
// Each turn-loop iteration that falls back to the same (unavailable,
// answered) pair calls queueProviderFallbackNote — the loop_run_turn.go::
// callProviderOnce call shape — and the §7.4 D17 text says a later iteration
// "neither re-announces the retry line nor re-notes the pair". Row 33(a)
// pins exactly one note for the whole turn. The frame count is deliberately
// NOT asserted: §7.4 D17 says a later iteration "re-frames" while FB-4's
// last line says "adds no second frame" — a spec-internal tension reported
// to team-lead for adjudication, not silently resolved into an assertion.
func TestFallbackNote_ThreeIterationsOneTurn_SingleNote_MIN102(t *testing.T) {
	store, sid := fbNoteStore(t)
	ts := fbNoteTurnState(store, sid, "turn-fb-a")

	candidates := []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteFinal},
	}
	for i := 0; i < 3; i++ {
		fbResult, _ := runFallbackChain(t, candidates)
		require.Equal(t, fbNoteFinal, fbResult.Model, "iteration %d must answer from the fallback", i+1)
		// The production call shape (loop_run_turn.go::callProviderOnce).
		ts.queueProviderFallbackNote(fbResult.Model, fbResult.Attempts)
	}
	ts.writePendingFallbackNotes()

	notes := fbNoteEntries(t, store, sid)
	require.Len(t, notes, 1,
		"3 iterations hitting the same pair must produce exactly ONE note (MIN-102/row 33(a)); got %d", len(notes))
	require.Equal(t, "Answered by the Fallback model (final-x2) because primary-y was unavailable.",
		notes[0].Content, "the note text is the §6 template with the FB-1 pair substituted")
	require.Equal(t, fbNotePrimary, notes[0].UnavailableModel, "the note must name the unavailable primary")
	require.Equal(t, fbNoteFinal, notes[0].Model, "the note must name the answering fallback")
	require.Equal(t, "rate_limited", notes[0].UnavailableCode,
		"a 429 body without a C-24 retirement phrase must read rate_limited — no D12 hint")
	require.NotContains(t, notes[0].Content, "Pick a new model",
		"the D12 hint fires only for model_retired (§6/D12)")
}

// ── Row 33(b) / FB-2: note AFTER the assistant answer entry ───────────────
//
// Driven through the REAL runTurn: the primary 429s over-ceiling (no
// sleeps), the fallback answers, runTurn's real defer chain persists the
// assistant answer (turn_transcript.go::appendAssistantTranscript via
// finalizeStreamer) and then the note (loop.go registers
// writePendingFallbackNotes BEFORE the Finish/finalizeStreamer defers, so
// LIFO runs it after them).
func TestFallbackNote_NoteAfterAssistantEntry_FB2(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &fbNoteScriptedProvider{}, nil)
	workerInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok, "native-agent not registered")
	workerInst.Candidates = []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteFinal},
	}
	store, sid := fbNoteStore(t)

	opts := processOptions{
		SessionKey: "fb2-order-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "answer me",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(workerInst, opts, al.newTurnEventScope(workerInst.ID, opts.SessionKey))

	result, err := al.runTurn(context.Background(), ts)
	require.NoError(t, err)
	require.NotEqual(t, TurnEndStatusParked, result.status,
		"the fallback turn must complete, not park")

	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	noteIdx, answerIdx := -1, -1
	noteCount := 0
	for i, e := range entries {
		if e.Type == session.EntryTypeProviderFallback {
			noteCount++
			noteIdx = i
		}
		if e.Role == "assistant" {
			answerIdx = i
		}
	}
	if noteCount != 1 {
		t.Fatalf("note count = %d, want exactly 1 (MIN-102 even at the full-turn level)", noteCount)
	}
	require.Greater(t, noteIdx, answerIdx,
		"the note entry (index %d) must sit AFTER the assistant answer entry (index %d) in the persisted transcript (FB-2/MIN-103)", noteIdx, answerIdx)
	require.Equal(t, "Answered by the Fallback model (final-x2) because primary-y was unavailable.",
		entries[noteIdx].Content, "the persisted note is the §6 sentence")
}

// ── Row 33(c) / MIN-103: {unavailable_model} names the PRIMARY ────────────
//
// A real chain over three candidates: primary-y fails, mid-x1 fails too,
// final-x2 answers. The note must name the FIRST candidate (the primary),
// never the immediately-preceding failure (§6/MIN-103).
func TestFallbackNote_MultiHopChain_NamesPrimary_MIN103(t *testing.T) {
	store, sid := fbNoteStore(t)
	ts := fbNoteTurnState(store, sid, "turn-fb-c")

	candidates := []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteMid},
		{Provider: "prov", Model: fbNoteFinal},
	}
	fbResult, p := runFallbackChain(t, candidates)
	require.Equal(t, []string{fbNotePrimary, fbNoteMid, fbNoteFinal}, p.calls,
		"the chain must drive the candidates in order")
	require.Equal(t, fbNoteFinal, fbResult.Model, "the chain must answer from the last candidate")

	ts.queueProviderFallbackNote(fbResult.Model, fbResult.Attempts)
	ts.writePendingFallbackNotes()

	notes := fbNoteEntries(t, store, sid)
	require.Len(t, notes, 1)
	require.Equal(t, "Answered by the Fallback model (final-x2) because primary-y was unavailable.",
		notes[0].Content,
		"the note must name the PRIMARY (first candidate), not the immediately-preceding failure mid-x1 (MIN-103/§6)")
	require.Equal(t, fbNotePrimary, notes[0].UnavailableModel, "the persisted pair fact is the primary")
	require.Equal(t, fbNoteFinal, notes[0].Model, "the persisted pair fact is the answering fallback")
	require.NotContains(t, notes[0].Content, fbNoteMid,
		"the intermediate candidate must not appear anywhere in the note")
}

// ── Row 33(d) / FB-4/MIN-103: once-per-pair survives a restart ────────────
//
// Turn 1 persists the (primary-y, final-x2) note. The store is then closed
// and RE-OPENED over the same data directory (a real restart: fresh
// in-memory state, transcript re-read from disk), a fresh turnState runs
// the same pair again — and must not re-note: the once-per-pair fact is
// derived from the persisted transcript (MIN-103), not from any in-memory
// state a restart would clear.
func TestFallbackNote_OncePerPairSurvivesRestart_TranscriptDerived(t *testing.T) {
	baseDir := t.TempDir() + "/sessions"
	store1, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	meta, err := store1.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sid := meta.ID

	// Turn 1 (pre-restart): queue + write the (primary-y, final-x2) note.
	ts1 := fbNoteTurnState(store1, sid, "turn-fb-d-1")
	fbResult, _ := runFallbackChain(t, []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteFinal},
	})
	ts1.queueProviderFallbackNote(fbResult.Model, fbResult.Attempts)
	ts1.writePendingFallbackNotes()
	require.Len(t, fbNoteEntries(t, store1, sid), 1, "turn 1 must persist its note")

	// The restart: close the store, re-open over the SAME data directory.
	require.NoError(t, store1.Close())
	store2, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store2.Close() })

	// Turn 2 (post-restart, fresh in-memory state): same pair again.
	ts2 := fbNoteTurnState(store2, sid, "turn-fb-d-2")
	fbResult2, _ := runFallbackChain(t, []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteFinal},
	})
	ts2.queueProviderFallbackNote(fbResult2.Model, fbResult2.Attempts)
	ts2.writePendingFallbackNotes()

	notes := fbNoteEntries(t, store2, sid)
	require.Len(t, notes, 1,
		"a restart must not lose the once-per-pair fact: the transcript is the authority (FB-4/MIN-103); got %d notes", len(notes))
	require.Equal(t, "Answered by the Fallback model (final-x2) because primary-y was unavailable.",
		notes[0].Content)
}

// ── D17 / row 33(e): a NEW turn, same chat, same pair → no re-note ────────
//
// The exact case the deleted contradictory clause got wrong: C-17/C-18 and
// FB-4 rule "at most once per pair until the pair changes" — regardless of
// turns. Two SEPARATE turns (fresh turnState each — a new turn is a fresh
// turnState in production), one store, no restart: exactly one note total.
func TestFallbackNote_NewTurnSamePair_NoSecondNote_D17(t *testing.T) {
	store, sid := fbNoteStore(t)

	// Turn 1.
	ts1 := fbNoteTurnState(store, sid, "turn-fb-e-1")
	fbResult1, _ := runFallbackChain(t, []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteFinal},
	})
	ts1.queueProviderFallbackNote(fbResult1.Model, fbResult1.Attempts)
	ts1.writePendingFallbackNotes()

	// Turn 2 — a new turn in the same chat, same (unavailable, answered)
	// pair, fresh turnState (pendingFallbackNotes empty), same process.
	ts2 := fbNoteTurnState(store, sid, "turn-fb-e-2")
	fbResult2, _ := runFallbackChain(t, []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteFinal},
	})
	ts2.queueProviderFallbackNote(fbResult2.Model, fbResult2.Attempts)
	ts2.writePendingFallbackNotes()

	notes := fbNoteEntries(t, store, sid)
	require.Len(t, notes, 1,
		"a NEW turn in the same chat for the SAME pair must not re-note (D17/C-17/C-18/FB-4); got %d notes", len(notes))
	require.Equal(t, "Answered by the Fallback model (final-x2) because primary-y was unavailable.",
		notes[0].Content, "the single note names the original pair")
}

// ── Gate finding F6 ─────────────────────────────────────────────────────────
//
// queueProviderFallbackNote's nil-transcript-wiring guard used to return
// silently — the live fallback note (and its provider_fallback frame)
// vanished with zero trace. The fix counts (fallbackNoteSuppressed) and
// WARN-logs every drop; this test pins the counter so a vanishing note can
// never go unseen again.
func TestFallbackNote_NilTranscriptWiring_SuppressedAndCounted_F6(t *testing.T) {
	ts := &turnState{} // no transcript store wired
	before := fallbackNoteSuppressed.Load()
	ts.queueProviderFallbackNote(fbNoteFinal, []providers.FallbackAttempt{{
		Provider: "prov",
		Model:    fbNotePrimary,
		Error:    errors.New("rate limit exceeded for tenant"),
		Reason:   providers.FailoverRateLimit,
	}})
	require.Equal(t, before+1, fallbackNoteSuppressed.Load(),
		"a fallback note dropped for nil transcript wiring must be counted (gate finding F6)")
}
