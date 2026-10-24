// badmodel_fallback_note_test.go — founder decision 2026-10-24 (Q70, train 3),
// rules 1 and 3: when an agent's model fails with bad_model (the provider
// says the model was not found or not offered), the turn must complete on the
// configured backup model and emit the fallback notice — live and persisted,
// like the existing 429 path — with exactly one notice naming BOTH models and
// saying plainly that the first model was not found or not offered by its
// provider, so the user still learns that their setting is wrong.
//
// Oracle: the Q70 rule text above, written before any implementation. The
// notice's wire unavailable_code enum value is deliberately NOT pinned — the
// decision fixes the user-visible wording, not the machine enum (flagged for
// CHECK). Harness reuses the §7.4 fallback-note sibling tests' proven seams:
// a real FallbackChain, a real UnifiedStore (JSONL on disk), the real
// runTurn/event bus; only the LLM transport is scripted at the process edge.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// badModelNoteBody classifies as bad_model (structured code wins over any
// prose, per the routing_1340 subject-oracle controls).
const badModelNoteBody = `{"error":{"code":"model_not_found","message":"the model does not exist"}}`

// badModelNoteProvider fails the primary with bad_model; the backup answers.
type badModelNoteProvider struct {
	mu    sync.Mutex
	calls []string
}

func (p *badModelNoteProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, model)
	p.mu.Unlock()
	if model == fbNotePrimary {
		return nil, &common.ProviderError{Status: 400, Body: badModelNoteBody}
	}
	return &providers.LLMResponse{Content: "answered by " + model, FinishReason: "stop"}, nil
}

func (p *badModelNoteProvider) GetDefaultModel() string { return fbNotePrimary }

// runBadModelFallbackChain drives the REAL FallbackChain over the given
// candidates — the same chain loop_run_turn.go::callProviderOnce drives —
// and returns the FallbackResult in the exact shape queueProviderFallbackNote
// receives.
func runBadModelFallbackChain(t *testing.T, candidates []providers.FallbackCandidate, p *badModelNoteProvider) *providers.FallbackResult {
	t.Helper()
	chain := providers.NewFallbackChain(providers.NewCooldownTracker())
	res, err := chain.Execute(context.Background(), candidates, func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
		return p.Chat(ctx, nil, nil, model, nil)
	})
	require.NoError(t, err,
		"Q70 rule 1: a bad_model primary must move to the next candidate instead of stopping (candidates=%v)", candidates)
	return res
}

// badModelNoticeMatches reports whether the notice text satisfies Q70 rule 1:
// it must say plainly that the unavailable model was not found or not offered
// by its provider — the old generic "was unavailable" wording hides the
// misconfiguration, so it no longer satisfies the rule.
func badModelNoticeMatches(content string) bool {
	return strings.Contains(content, "not found") || strings.Contains(content, "not offered")
}

// Q70 rule 1, persisted notice: a bad_model fallback queues exactly ONE note
// per (unavailable, answered) pair — the MIN-102 cap — whose text names both
// models and plainly states the not-found/not-offered cause.
func TestBadModelFallbackNote_NotFoundWording_OncePerPair(t *testing.T) {
	store, sid := fbNoteStore(t)
	ts := fbNoteTurnState(store, sid, "turn-badmodel-note")
	p := &badModelNoteProvider{}
	candidates := []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteFinal},
	}
	for i := 0; i < 2; i++ { // two same-turn iterations on the same pair
		res := runBadModelFallbackChain(t, candidates, p)
		require.Equal(t, fbNoteFinal, res.Model, "iteration %d must answer from the backup", i+1)
		// The production call shape (loop_run_turn.go::callProviderOnce).
		ts.queueProviderFallbackNote(res.Model, res.Attempts)
	}
	ts.writePendingFallbackNotes()

	notes := fbNoteEntries(t, store, sid)
	require.Len(t, notes, 1,
		"two same-pair iterations must produce exactly ONE persisted note (MIN-102); got %d", len(notes))
	require.Equal(t, fbNotePrimary, notes[0].UnavailableModel, "the note must name the unavailable primary")
	require.Equal(t, fbNoteFinal, notes[0].Model, "the note must name the answering backup")
	require.True(t, badModelNoticeMatches(notes[0].Content),
		"Q70 rule 1: the bad_model fallback notice must say plainly that the first model was not found or not offered by its provider; got %q", notes[0].Content)
	require.Contains(t, notes[0].Content, fbNotePrimary, "the notice must name BOTH models")
	require.Contains(t, notes[0].Content, fbNoteFinal, "the notice must name BOTH models")
}

// Q70 rules 1+3, live turn: the real ordinary turn completes on the backup,
// emits exactly one live provider_fallback event (subscriber and gateway tap)
// with both models, and persists exactly one note with the plain not-found
// wording AFTER the assistant answer entry (FB-2).
func TestBadModelFallbackLiveTurn(t *testing.T) {
	provider := &badModelNoteProvider{}
	al, _ := newGoalLoopTestLoop(t, provider, func(cfg *config.Config) {
		// Ordinary chat addresses a colleague, never a delegation-only worker.
		cfg.Agents.List[0].Type = config.AgentTypeCustom
	})
	installTestSessionLauncher(t, al)
	agent, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	agent.Candidates = []providers.FallbackCandidate{
		{Provider: "prov", Model: fbNotePrimary},
		{Provider: "prov", Model: fbNoteFinal},
	}
	agent.StoreProviderPool(map[string]providers.LLMProvider{"prov": provider})
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agent.ID)
	require.NoError(t, err)
	key := fmt.Sprintf("agent:%s:session:%s", agent.ID, meta.ID)

	sub := al.SubscribeEvents(128)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	var tapMu sync.Mutex
	var tapped []Event
	al.SetEventSyncTap(func(evt Event) {
		if evt.Kind == EventKindProviderFallback {
			tapMu.Lock()
			tapped = append(tapped, evt)
			tapMu.Unlock()
		}
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })

	// The gateway saves the user entry before submitting this inbound message.
	// Do the same, without constructing or patching a turnState.
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID: "badmodel-live-input", Role: "user", Content: "answer me", AgentID: agent.ID, Timestamp: time.Now().UTC(),
	}))
	answer, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: "badmodel-live", SessionID: meta.ID, SessionKey: key,
		Content: "answer me", GatewayUserID: "badmodel-live-user", OperatorPrompt: true,
		Metadata: map[string]string{"agent_id": agent.ID},
	})
	require.NoError(t, err, "the real ordinary turn must complete on the backup after a bad_model primary")
	require.Equal(t, "answered by "+fbNoteFinal, answer)

	provider.mu.Lock()
	calls := append([]string(nil), provider.calls...)
	provider.mu.Unlock()
	require.Equal(t, []string{fbNotePrimary, fbNoteFinal}, calls,
		"the real fallback chain must drive the primary, then the backup, in order")

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	noteCount, noteIndex, answerIndex := 0, -1, -1
	for i, entry := range entries {
		if entry.Role == "assistant" && entry.Content == answer {
			answerIndex = i
		}
		if entry.Type == session.EntryTypeProviderFallback {
			noteCount++
			noteIndex = i
		}
	}
	require.GreaterOrEqual(t, answerIndex, 0, "the answer must be saved, not just returned")
	require.Equal(t, 1, noteCount, "exactly one persisted fallback note (MIN-102)")
	require.Greater(t, noteIndex, answerIndex, "FB-2: the saved note follows the assistant answer")
	require.Equal(t, fbNotePrimary, entries[noteIndex].UnavailableModel)
	require.Equal(t, fbNoteFinal, entries[noteIndex].Model)
	require.True(t, badModelNoticeMatches(entries[noteIndex].Content),
		"Q70 rule 1: the persisted notice must state the not-found/not-offered cause; got %q", entries[noteIndex].Content)

	events := drainEvents(sub.C)
	counts := make(map[EventKind]int)
	var live []Event
	var start Event
	for _, evt := range events {
		counts[evt.Kind]++
		if evt.Kind == EventKindTurnStart {
			start = evt
		}
		if evt.Kind == EventKindProviderFallback {
			live = append(live, evt)
		}
	}
	// Positive controls: the subscriber observes the real turn lifecycle, so a
	// missing fallback event is a missing event, not a disconnected bus.
	for _, kind := range []EventKind{EventKindTurnStart, EventKindLLMRequest, EventKindLLMResponse, EventKindTurnEnd} {
		require.Equal(t, 1, counts[kind], "positive control: %s", kind)
	}
	require.Zero(t, al.EventDrops(EventKindProviderFallback), "subscriber overflow is not this defect")
	require.Len(t, live, 1, "exactly one live provider_fallback event must reach the ordinary turn's subscriber")
	tapMu.Lock()
	tapEvents := append([]Event(nil), tapped...)
	tapMu.Unlock()
	require.Len(t, tapEvents, 1, "the gateway's synchronous bus tap must receive the same event")
	require.NotEmpty(t, start.Meta.TurnID)
	require.Equal(t, EventMeta{
		AgentID: agent.ID, TurnID: start.Meta.TurnID, SessionKey: key, Iteration: 1,
		Source: "runTurn", TracePath: "turn.llm.fallback",
	}, live[0].Meta)
	// Spec change (chief ruling, 2026-10-24): the RED literal above
	// accidentally pinned UnavailableCode empty; the ruling sets the bad_model
	// hop's enum value to model_not_found (contract-first, developer adds it).
	require.Equal(t, ProviderFallbackPayload{
		SessionID: meta.ID, TurnID: start.Meta.TurnID, AnsweredModel: fbNoteFinal,
		UnavailableModel: fbNotePrimary, UnavailableCode: "model_not_found",
	}, live[0].Payload)
	require.Equal(t, live[0], tapEvents[0])
}
