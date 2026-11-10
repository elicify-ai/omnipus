package gateway

// Backend-authored supplemental proof (effort-levels S-95, FR-058): the
// stored answer carries the agent's turn reasoning sum over EVERY call of the
// turn, not only the calls still pending at Finalize. A no-text tool-call
// response books its accounting the moment it completes, so the streamer then
// holds only the answering call. The real wsStreamer and UnifiedStore run.

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestBackendStreamer_TurnReasoningCoversCallsPersistedMidTurn(t *testing.T) {
	intPtr := func(n int) *int { return &n }
	for _, tc := range []struct {
		name    string
		handOff bool
		turnSum *int
		want    *int
	}{
		// The tool-call call (30) was persisted mid-turn; the answer (0) is pending.
		{"agent turn sum handed over: 30 + 0", true, intPtr(30), intPtr(30)},
		// No call of the turn reported a count: absence stays absence.
		{"agent reports no count", true, nil, nil},
		// A streamer with no agent turn behind it sums the calls it holds.
		{"no hand-over: sum of the held calls", false, nil, intPtr(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, openErr := session.NewUnifiedStore(t.TempDir())
			if openErr != nil {
				t.Fatal(openErr)
			}
			t.Cleanup(func() { _ = store.Close() })
			meta, createErr := store.NewSession(session.SessionTypeChat, "webchat", "mia")
			if createErr != nil {
				t.Fatal(createErr)
			}
			stream := &wsStreamer{agentStore: store, sessionID: meta.ID, agentID: "mia"}
			stream.SetMessageID("answer-t2")
			stream.SetTurnID("turn-t2")
			stream.SetCallAccounting([]session.TranscriptEntry{{
				ID: "call-answer", Role: "assistant", AgentID: "mia", TurnID: "turn-t2", Timestamp: time.Now().UTC(),
				ProviderID: "openai", Model: "B", ModelID: "B", BillingClass: "unpriced", Tokens: 85,
				PromptTokens: 5, CompletionTokens: 80, ReasoningTokens: intPtr(0),
			}})
			if tc.handOff {
				stream.SetTurnReasoningTokens(tc.turnSum)
			}
			if err := stream.Finalize(t.Context(), "ANSWER-T2"); err != nil {
				t.Fatal(err)
			}
			entries, err := store.ReadTranscript(meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			var answer *session.TranscriptEntry
			for i := range entries {
				if entries[i].Content == "ANSWER-T2" {
					answer = &entries[i]
				}
			}
			if answer == nil {
				t.Fatalf("positive control: the answer was not persisted: %+v", entries)
			}
			switch {
			case tc.want == nil && answer.TurnReasoningTokens != nil:
				t.Errorf("turn_reasoning_tokens = %d, want absent", *answer.TurnReasoningTokens)
			case tc.want != nil && answer.TurnReasoningTokens == nil:
				t.Errorf("turn_reasoning_tokens absent, want %d", *tc.want)
			case tc.want != nil && *answer.TurnReasoningTokens != *tc.want:
				t.Errorf("turn_reasoning_tokens = %d, want %d", *answer.TurnReasoningTokens, *tc.want)
			}
		})
	}
}
