// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"errors"
	"strconv"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func newTestInboxStore(t *testing.T) *MessageInboxStore {
	t.Helper()
	return NewMessageInboxStore(t.TempDir())
}

func questionMsg(t *testing.T, sessionID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	auth := generated.SessionMessageQuestionAuthority("self_ok")
	if err := sm.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId:      messageID,
		SessionId:      sessionID,
		CreatedAt:      time.Now(),
		Depth:          1,
		SenderIdentity: "child-agent",
		Text:           "May I proceed?",
		Wait:           true,
		CorrelationId:  messageID + "-corr",
		Authority:      &auth,
	}); err != nil {
		t.Fatalf("FromSessionMessageQuestion failed: %v", err)
	}
	return sm
}

func progressMsg(t *testing.T, sessionID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId:      messageID,
		SessionId:      sessionID,
		CreatedAt:      time.Now(),
		Depth:          1,
		SenderIdentity: "child-agent",
		Text:           "still working",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress failed: %v", err)
	}
	return sm
}

func TestMessageInboxStore_AppendAndDrain(t *testing.T) {
	s := newTestInboxStore(t)
	msg := progressMsg(t, "child-1", "m1")
	res, err := s.Append("owner-1", msg)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if !res.Accepted || res.Deduped {
		t.Errorf("unexpected AppendResult: %+v", res)
	}

	msgs, _, hasMore, err := s.Drain("owner-1", "child-1", "", 10)
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	if hasMore {
		t.Error("hasMore should be false")
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	kind, err := msgs[0].Discriminator()
	if err != nil || kind != "progress" {
		t.Errorf("expected kind=progress, got %q (err=%v)", kind, err)
	}
}

func TestMessageInboxStore_DedupeByMessageID(t *testing.T) {
	s := newTestInboxStore(t)
	msg := progressMsg(t, "child-1", "dup-1")

	first, err := s.Append("owner-1", msg)
	if err != nil {
		t.Fatalf("first Append failed: %v", err)
	}
	if first.Deduped {
		t.Fatal("first Append should not be marked deduped")
	}

	second, err := s.Append("owner-1", msg)
	if err != nil {
		t.Fatalf("second Append (duplicate) should succeed as a no-op, got error: %v", err)
	}
	if !second.Deduped {
		t.Error("second Append with the same message_id should be marked Deduped=true")
	}

	msgs, _, _, err := s.Drain("owner-1", "child-1", "", 10)
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 stored message after a duplicate append, got %d", len(msgs))
	}
}

// TestMessageInboxStore_PerChildCeiling_FailsBackNeverDrops proves D15: a
// child hitting the 20-open-question+blocker ceiling gets a CLEAR ERROR
// (never a silent drop), and a SIBLING child under the same owner key is
// unaffected.
func TestMessageInboxStore_PerChildCeiling_FailsBackNeverDrops(t *testing.T) {
	s := newTestInboxStore(t)
	s.InboxPerTypeCeiling = 20      // explicit, matches D15 default
	s.ChildSendRatePerMinute = 1000 // disable the UNRELATED rate cap for this test

	for i := 0; i < 20; i++ {
		msg := questionMsg(t, "child-noisy", questionID(i))
		if _, err := s.Append("owner-1", msg); err != nil {
			t.Fatalf("Append #%d (within ceiling) failed: %v", i, err)
		}
	}

	// The 21st open question must be REJECTED with a clear, typed error —
	// never silently dropped.
	overCeiling := questionMsg(t, "child-noisy", "q-overflow")
	_, err := s.Append("owner-1", overCeiling)
	if err == nil {
		t.Fatal("expected the 21st open question to be rejected, got nil error")
	}
	if !errors.Is(err, ErrInboxPerChildCeiling) {
		t.Errorf("expected ErrInboxPerChildCeiling, got: %v", err)
	}

	// A SIBLING child under the SAME owner key must be entirely unaffected.
	siblingMsg := questionMsg(t, "child-sibling", "q-sibling-1")
	if _, err := s.Append("owner-1", siblingMsg); err != nil {
		t.Fatalf("sibling child's Append should succeed, got: %v", err)
	}
}

func questionID(i int) string {
	return "q-" + strconv.Itoa(i)
}

// TestMessageInboxStore_RateCap_FakeClock proves the 10/min child-send rate
// cap using an injectable clock (D15 message-caps dataset: 10 accepted, the
// 11th within the same 60s window rejected — never silently dropped).
func TestMessageInboxStore_RateCap_FakeClock(t *testing.T) {
	s := newTestInboxStore(t)
	s.ChildSendRatePerMinute = 10

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	for i := 0; i < 10; i++ {
		msg := progressMsg(t, "child-rate", "rate-"+strconv.Itoa(i))
		if _, err := s.Append("owner-1", msg); err != nil {
			t.Fatalf("Append #%d (within rate cap) failed: %v", i, err)
		}
	}

	eleventh := progressMsg(t, "child-rate", "rate-10")
	_, err := s.Append("owner-1", eleventh)
	if err == nil {
		t.Fatal("expected the 11th send within 60s to be rate-limited, got nil error")
	}
	if !errors.Is(err, ErrInboxRateLimited) {
		t.Errorf("expected ErrInboxRateLimited, got: %v", err)
	}

	// Advance the fake clock past the 60s window — sends resume.
	now = now.Add(61 * time.Second)
	twelfth := progressMsg(t, "child-rate", "rate-11")
	if _, err := s.Append("owner-1", twelfth); err != nil {
		t.Fatalf("expected send to succeed after the rate window elapsed, got: %v", err)
	}
}

func TestMessageInboxStore_DepthCapRejected(t *testing.T) {
	s := newTestInboxStore(t)
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId:      "deep-1",
		SessionId:      "child-1",
		CreatedAt:      time.Now(),
		Depth:          6, // over the 5-hop cap
		SenderIdentity: "child-agent",
		Text:           "too deep",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress failed: %v", err)
	}
	_, err := s.Append("owner-1", sm)
	if !errors.Is(err, ErrInboxDepthExceeded) {
		t.Errorf("expected ErrInboxDepthExceeded, got: %v", err)
	}
}

func TestMessageInboxStore_UnackedCountAndAck(t *testing.T) {
	s := newTestInboxStore(t)
	msg1 := questionMsg(t, "child-1", "uc-1")
	msg2 := questionMsg(t, "child-1", "uc-2")
	if _, err := s.Append("owner-1", msg1); err != nil {
		t.Fatalf("Append msg1 failed: %v", err)
	}
	if _, err := s.Append("owner-1", msg2); err != nil {
		t.Fatalf("Append msg2 failed: %v", err)
	}

	count, err := s.UnackedCount("owner-1", "child-1")
	if err != nil {
		t.Fatalf("UnackedCount failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected UnackedCount=2, got %d", count)
	}

	if err := s.Ack("owner-1", []string{"uc-1"}); err != nil {
		t.Fatalf("Ack failed: %v", err)
	}
	count, err = s.UnackedCount("owner-1", "child-1")
	if err != nil {
		t.Fatalf("UnackedCount after ack failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected UnackedCount=1 after acking one message, got %d", count)
	}

	msgs, _, _, err := s.Drain("owner-1", "child-1", "", 10)
	if err != nil {
		t.Fatalf("Drain after ack failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 unacked message left in Drain, got %d", len(msgs))
	}
}

func TestMessageInboxStore_Peek_NoSideEffects(t *testing.T) {
	s := newTestInboxStore(t)
	var cp generated.SessionMessage
	if err := cp.FromSessionMessageCheckpoint(generated.SessionMessageCheckpoint{
		MessageId:      "cp-1",
		SessionId:      "child-1",
		CreatedAt:      time.Now(),
		Depth:          1,
		SenderIdentity: "child-agent",
		Summary:        "halfway there",
	}); err != nil {
		t.Fatalf("FromSessionMessageCheckpoint failed: %v", err)
	}
	if _, err := s.Append("owner-1", cp); err != nil {
		t.Fatalf("Append checkpoint failed: %v", err)
	}

	snap, err := s.Peek("owner-1", "child-1")
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if !snap.HasCheckpoint || snap.LatestCheckpointSummary != "halfway there" {
		t.Errorf("unexpected peek snapshot: %+v", snap)
	}

	// Peek must NOT consume the ceiling or ack anything — verify by
	// draining and confirming the checkpoint is still there unacked.
	msgs, _, _, err := s.Drain("owner-1", "child-1", "", 10)
	if err != nil {
		t.Fatalf("Drain after Peek failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected Peek to have no side effects on the unacked inbox, got %d messages", len(msgs))
	}
}

// TestMessageInboxStore_DrainCursor_NoRedeliveryAcrossPages proves
// Correctness-MAJOR-1: the Drain pagination cursor MUST be the entry index of
// the last emitted candidate + 1, NOT an output-count offset (sinceIdx+max).
// An inbox shaped [ack,ack,msg1,msg2,msg3] with max=2 must page as
// [msg1,msg2] then [msg3] with NO redelivery. The prior sinceIdx+max cursor
// under-pointed (skipped acked lines don't advance an output-count cursor), so
// page 2 re-delivered msg1+msg2 until they were acked.
func TestMessageInboxStore_DrainCursor_NoRedeliveryAcrossPages(t *testing.T) {
	s := newTestInboxStore(t)
	s.ChildSendRatePerMinute = 1000 // isolate from the rate cap

	// Build the entry sequence [ack, ack, msg1, msg2, msg3] for child-1.
	// The two leading acks reference ids that aren't in this inbox — they're
	// "skip me" non-message lines the cursor must hop over without
	// under-pointing.
	if err := s.Ack("owner-drain", []string{"acked-early-1"}); err != nil {
		t.Fatalf("ack#1: %v", err)
	}
	if err := s.Ack("owner-drain", []string{"acked-early-2"}); err != nil {
		t.Fatalf("ack#2: %v", err)
	}
	m1 := progressMsg(t, "child-1", "drain-1")
	m2 := progressMsg(t, "child-1", "drain-2")
	m3 := progressMsg(t, "child-1", "drain-3")
	for _, m := range []generated.SessionMessage{m1, m2, m3} {
		if _, err := s.Append("owner-drain", m); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// Page 1: max=2 → [drain-1, drain-2], hasMore=true.
	page1, cursor, hasMore, err := s.Drain("owner-drain", "child-1", "", 2)
	if err != nil {
		t.Fatalf("Drain page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: expected 2 messages, got %d", len(page1))
	}
	if !hasMore {
		t.Fatal("page1: expected hasMore=true (3 total, max 2)")
	}
	if id := messageIDOf(t, page1[0]); id != "drain-1" || messageIDOf(t, page1[1]) != "drain-2" {
		t.Errorf("page1 ids = [%s, %s], want [drain-1, drain-2]", messageIDOf(t, page1[0]), messageIDOf(t, page1[1]))
	}

	// Page 2: resume from cursor → MUST be exactly [drain-3], NOT a
	// redelivery of drain-1/drain-2.
	page2, cursor2, hasMore2, err := s.Drain("owner-drain", "child-1", cursor, 2)
	if err != nil {
		t.Fatalf("Drain page2: %v", err)
	}
	if len(page2) != 1 || messageIDOf(t, page2[0]) != "drain-3" {
		ids := make([]string, len(page2))
		for i, m := range page2 {
			ids[i] = messageIDOf(t, m)
		}
		t.Fatalf("page2: expected exactly [drain-3], got %v (REDelivery of page1 — the sinceIdx+max cursor bug)", ids)
	}
	if hasMore2 {
		t.Error("page2: expected hasMore=false after draining all 3 messages")
	}

	// A third page from the exhausted cursor must return nothing (no
	// backwards motion, no redelivery).
	page3, _, hasMore3, err := s.Drain("owner-drain", "child-1", cursor2, 2)
	if err != nil {
		t.Fatalf("Drain page3: %v", err)
	}
	if len(page3) != 0 || hasMore3 {
		t.Errorf("page3: expected empty/no-more, got %d messages hasMore=%v", len(page3), hasMore3)
	}
}

func messageIDOf(t *testing.T, msg generated.SessionMessage) string {
	t.Helper()
	p, _, err := peekEnvelope(msg)
	if err != nil {
		t.Fatalf("peekEnvelope: %v", err)
	}
	return p.MessageID
}
