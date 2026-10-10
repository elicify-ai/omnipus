// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func attentionStoreWithMain(t *testing.T) (*UnifiedStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewUnifiedStore(dir)
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	main, err := store.GetOrCreateMainSession("01JXATTENTIONWS000000001", "mia")
	if err != nil {
		t.Fatalf("GetOrCreateMainSession: %v", err)
	}
	return store, main.ID
}

func appendOutcome(t *testing.T, store *UnifiedStore, id, goalID string, ending generated.GoalOutcomeEnding, at time.Time) {
	t.Helper()
	if err := store.AppendTranscriptStrict(id, TranscriptEntry{
		ID: "outcome-" + goalID, Type: EntryTypeSystem, Role: "system", Content: "Goal ended.",
		Timestamp: at, SystemSubtype: SystemSubtypeGoalOutcome,
		GoalOutcome: &generated.GoalOutcome{GoalId: goalID, GoalText: "x", Ending: ending, RoundsUsed: 1, MaxRounds: 5, EndedAt: at},
	}); err != nil {
		t.Fatalf("append outcome %s: %v", goalID, err)
	}
}

func attentionOf(t *testing.T, store *UnifiedStore, id string) AttentionMark {
	t.Helper()
	meta, err := store.GetMeta(id)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	return meta.Attention
}

// Oracle: architect U11 Q2 — the order is the outcome's saved timestamp in
// milliseconds, strictly increasing; a stopped_by_user ending never raises it.
func TestAttention_OutcomeRaisesOrderStrictlyAndStoppedByUserNever(t *testing.T) {
	store, id := attentionStoreWithMain(t)
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	appendOutcome(t, store, id, "g-stop", generated.GoalOutcomeEndingStoppedByUser, at)
	if got := attentionOf(t, store, id); got != (AttentionMark{}) {
		t.Fatalf("stopped_by_user raised the mark: %+v", got)
	}

	appendOutcome(t, store, id, "g1", generated.GoalOutcomeEndingMet, at)
	first := attentionOf(t, store, id).OutcomeOrder
	if first != at.UnixMilli() {
		t.Fatalf("OutcomeOrder = %d, want the outcome's timestamp in ms %d", first, at.UnixMilli())
	}
	// A later outcome stamped EARLIER (clock stepped back) still orders after.
	appendOutcome(t, store, id, "g2", generated.GoalOutcomeEndingRoundsExhausted, at.Add(-time.Hour))
	if second := attentionOf(t, store, id).OutcomeOrder; second != first+1 {
		t.Fatalf("OutcomeOrder after a backwards clock = %d, want strictly increasing %d", second, first+1)
	}
}

// Oracle: architect U11 Q2 — AckAttentionSeen = max(seen, min(n, outcomeOrder)),
// monotonic, never lowered, refuses negative n, and survives a restart.
func TestAttention_AckSemanticsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := NewUnifiedStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	main, err := store.GetOrCreateMainSession("01JXATTENTIONWS000000001", "mia")
	if err != nil {
		t.Fatal(err)
	}
	id := main.ID
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	if got, err := store.AckAttentionSeen(id, 5); err != nil || got != 0 {
		t.Fatalf("ack on a main with no outcome = (%d, %v), want (0, nil): nothing to see", got, err)
	}
	if _, err := store.AckAttentionSeen(id, -1); err == nil {
		t.Fatal("a negative ack must be refused")
	}

	appendOutcome(t, store, id, "g1", generated.GoalOutcomeEndingMet, at)
	g1 := attentionOf(t, store, id).OutcomeOrder
	appendOutcome(t, store, id, "g2", generated.GoalOutcomeEndingMet, at.Add(time.Minute))
	g2 := attentionOf(t, store, id).OutcomeOrder

	// Ack the bound the client was shown (g1) AFTER g2 was saved: g2 stays unseen.
	if got, err := store.AckAttentionSeen(id, g1); err != nil || got != g1 {
		t.Fatalf("ack(g1) = (%d, %v), want (%d, nil)", got, err, g1)
	}
	if m := attentionOf(t, store, id); m.SeenOrder >= m.OutcomeOrder {
		t.Fatalf("g2 must stay unseen after acking g1: %+v", m)
	}
	// A stale, smaller bound cannot lower the mark.
	if got, _ := store.AckAttentionSeen(id, g1-1000); got != g1 {
		t.Fatalf("a stale ack lowered the mark to %d, want %d", got, g1)
	}
	// A bound beyond the saved order is clamped to it.
	if got, _ := store.AckAttentionSeen(id, g2+999999); got != g2 {
		t.Fatalf("an over-large ack = %d, want it clamped to %d", got, g2)
	}

	// Restart: a new store over the same directory sees the same mark.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewUnifiedStore(filepath.Clean(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got := attentionOf(t, reopened, id); got.SeenOrder != g2 || got.OutcomeOrder != g2 {
		t.Fatalf("after restart mark = %+v, want seen=outcome=%d", got, g2)
	}
}

// Oracle: only a main carries the mark; a chat records nothing and refuses an ack.
func TestAttention_NonMainCarriesNoMark(t *testing.T) {
	store, _ := attentionStoreWithMain(t)
	chat, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatal(err)
	}
	appendOutcome(t, store, chat.ID, "g1", generated.GoalOutcomeEndingMet, time.Now().UTC())
	if got := attentionOf(t, store, chat.ID); got != (AttentionMark{}) {
		t.Fatalf("a non-main recorded an attention mark: %+v", got)
	}
	if _, err := store.AckAttentionSeen(chat.ID, 1); err == nil {
		t.Fatal("an ack on a non-main must be refused")
	}
}
