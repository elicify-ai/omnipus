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

	if ackErr := s.Ack("owner-1", []string{"uc-1"}); ackErr != nil {
		t.Fatalf("Ack failed: %v", ackErr)
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

// TestMessageInboxStore_AckDetailed_MixedRealAndFakeIDs_ReportsTruthfulCount
// proves the M1 fix: AckDetailed's returned AckResult reports the REAL
// acknowledged count and surfaces unknown ids, rather than the bug this
// closes — pkg/tools/delegate.go's executeInboxAck used to report
// "Acknowledged N message(s)." using len(requested ids), so a wholly
// fabricated id was silently folded into a false success count.
//
// This is a genuine red-before/green-after regression guard: AckDetailed did
// not exist before this fix (a caller had no way to get this information at
// all — only Ack's bare `error`), so this test could not have compiled,
// let alone passed, against the pre-fix code. Real store-backed state (a
// temp-dir-rooted MessageInboxStore, not a spy) throughout.
func TestMessageInboxStore_AckDetailed_MixedRealAndFakeIDs_ReportsTruthfulCount(t *testing.T) {
	s := newTestInboxStore(t)
	s.ChildSendRatePerMinute = 1000 // isolate from the unrelated rate cap

	real1 := questionMsg(t, "child-1", "real-1")
	real2 := questionMsg(t, "child-1", "real-2")
	if _, err := s.Append("owner-mixed", real1); err != nil {
		t.Fatalf("Append real-1 failed: %v", err)
	}
	if _, err := s.Append("owner-mixed", real2); err != nil {
		t.Fatalf("Append real-2 failed: %v", err)
	}

	// A mixed batch: two REAL message ids plus one WHOLLY FABRICATED id that
	// was never sent — exactly the UAT repro (a fabricated id returned
	// "Acknowledged 1 message(s)." under the old code).
	result, err := s.AckDetailed("owner-mixed", []string{"real-1", "real-2", "totally-fabricated-id"})
	if err != nil {
		t.Fatalf("AckDetailed failed: %v", err)
	}

	// Binding Rule 4: pair the negative/zero-ish assertion (Unknown) with a
	// positive lower bound (Acknowledged) so this test cannot pass
	// identically if AckDetailed degenerated to "everything is unknown" or
	// "everything is acknowledged".
	if len(result.Acknowledged) != 2 {
		t.Fatalf("Acknowledged = %v, want exactly 2 real ids", result.Acknowledged)
	}
	for _, want := range []string{"real-1", "real-2"} {
		found := false
		for _, got := range result.Acknowledged {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in Acknowledged, got %v", want, result.Acknowledged)
		}
	}
	if len(result.Unknown) != 1 || result.Unknown[0] != "totally-fabricated-id" {
		t.Fatalf("Unknown = %v, want exactly [totally-fabricated-id]", result.Unknown)
	}

	// The underlying ack mechanism itself must be unaffected: both real ids
	// are genuinely acknowledged (UAT: "the real half of a mixed batch DID
	// get acknowledged") — verify via the real downstream effect (Drain),
	// not just the returned struct.
	msgs, _, _, derr := s.Drain("owner-mixed", "child-1", "", 10)
	if derr != nil {
		t.Fatalf("Drain after AckDetailed failed: %v", derr)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected both real messages to be acked (0 remaining unacked), got %d", len(msgs))
	}
}

// TestMessageInboxStore_AckDetailed_AllFake_ZeroAcknowledgedWithPositiveUnknown
// is the companion all-fake case: every id is unknown, Acknowledged must be
// empty (not just "not 3") AND Unknown must carry a real positive count —
// guards against a degenerate AckDetailed that always reports everything as
// acknowledged regardless of what's actually in the store.
func TestMessageInboxStore_AckDetailed_AllFake_ZeroAcknowledgedWithPositiveUnknown(t *testing.T) {
	s := newTestInboxStore(t)
	result, err := s.AckDetailed("owner-allfake", []string{"fake-a", "fake-b"})
	if err != nil {
		t.Fatalf("AckDetailed failed: %v", err)
	}
	if len(result.Acknowledged) != 0 {
		t.Errorf("Acknowledged = %v, want empty (no real messages ever appended)", result.Acknowledged)
	}
	if len(result.Unknown) != 2 {
		t.Fatalf("Unknown = %v, want exactly 2 (positive lower bound)", result.Unknown)
	}
}

// TestMessageInboxStore_Ack_UnaffectedByAckDetailedRefactor is a narrow
// regression guard that Ack's own historical signature/behavior (used today
// by pkg/tools/delegate.go's DelegateInboxStore interface) is byte-for-byte
// unchanged by routing through the shared ackDetailed helper: still returns
// only an error, still accepts a mixed real+fake batch without rejecting it,
// still durably acks the real id.
func TestMessageInboxStore_Ack_UnaffectedByAckDetailedRefactor(t *testing.T) {
	s := newTestInboxStore(t)
	realMsg := questionMsg(t, "child-1", "still-real")
	if _, err := s.Append("owner-compat", realMsg); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := s.Ack("owner-compat", []string{"still-real", "still-fake"}); err != nil {
		t.Fatalf("Ack should still succeed (error-only contract) for a mixed batch, got: %v", err)
	}
	msgs, _, _, derr := s.Drain("owner-compat", "child-1", "", 10)
	if derr != nil {
		t.Fatalf("Drain failed: %v", derr)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected the real id to still be genuinely acked, got %d remaining", len(msgs))
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

// TestMessageInboxStore_Append_SinglePassOverExistingMessages is the
// Perf-HIGH-1 call-count regression guard: Append used to do a full dedupe
// scan AND a separate full open-count scan over the stored message entries,
// each calling peekEnvelope (a Marshal+Unmarshal) once per message — 2
// peekEnvelope calls per existing message per Append, quadratic over a
// chat's lifetime. The fix combines both into one pass. With `existing`
// prior, non-duplicate message entries already stored, one more Append MUST
// make EXACTLY `existing` peekEnvelope calls — a regression back to the
// two-loop shape would double this to 2*existing.
func TestMessageInboxStore_Append_SinglePassOverExistingMessages(t *testing.T) {
	s := newTestInboxStore(t)
	s.ChildSendRatePerMinute = 100000 // isolate from the unrelated rate cap

	const existing = 50
	for i := 0; i < existing; i++ {
		msg := progressMsg(t, "child-bulk", "bulk-"+strconv.Itoa(i))
		if _, err := s.Append("owner-bulk", msg); err != nil {
			t.Fatalf("seed Append #%d failed: %v", i, err)
		}
	}

	before := messageInboxPeekEnvelopeCalls.Load()
	newMsg := progressMsg(t, "child-bulk", "bulk-new")
	if _, err := s.Append("owner-bulk", newMsg); err != nil {
		t.Fatalf("Append under test failed: %v", err)
	}
	delta := messageInboxPeekEnvelopeCalls.Load() - before

	// +1 accounts for the ONE unavoidable peekEnvelope call on the new
	// message itself (extracting its own envelope at the top of Append,
	// before the existing-entries scan even starts) — everything beyond
	// that must be exactly one call per existing message, not two.
	want := int64(existing) + 1
	if delta != want {
		t.Fatalf("Append made %d peekEnvelope calls scanning %d existing messages, want exactly %d "+
			"(one combined pass, not the old two-separate-loops shape)", delta, existing, want)
	}
}

// TestMessageInboxStore_Compaction_PreservesUnackedAndRetainsNewestAcked is
// the HIGH-finding compaction correctness test: every UNACKED message
// entry must survive a compaction untouched, and among ACKED entries only
// the newest AckedRetentionMax (by append order) are kept.
func TestMessageInboxStore_Compaction_PreservesUnackedAndRetainsNewestAcked(t *testing.T) {
	s := newTestInboxStore(t)
	s.ChildSendRatePerMinute = 100000
	s.AckedRetentionMax = 3
	s.CompactionAckedTrigger = 5 // fires once ackedMsgTotal > 5

	const ownerKey = "owner-compact"
	const child = "child-compact"

	// 10 messages, ids acked-0..acked-9.
	var ids []string
	for i := 0; i < 10; i++ {
		id := "acked-" + strconv.Itoa(i)
		ids = append(ids, id)
		if _, err := s.Append(ownerKey, progressMsg(t, child, id)); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	// Two messages left deliberately unacked.
	unacked1 := progressMsg(t, child, "stays-unacked-1")
	unacked2 := progressMsg(t, child, "stays-unacked-2")
	if _, err := s.Append(ownerKey, unacked1); err != nil {
		t.Fatalf("Append stays-unacked-1: %v", err)
	}
	if _, err := s.Append(ownerKey, unacked2); err != nil {
		t.Fatalf("Append stays-unacked-2: %v", err)
	}

	// Ack all 10 in one call — ackedMsgTotal becomes 10 > trigger(5), so this
	// Ack call itself opportunistically compacts.
	if _, err := s.AckDetailed(ownerKey, ids); err != nil {
		t.Fatalf("AckDetailed: %v", err)
	}

	// The two unacked messages must still be fully present and drainable —
	// safety-critical state, never touched by compaction regardless of
	// retention.
	msgs, _, hasMore, err := s.Drain(ownerKey, child, "", 10)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if hasMore {
		t.Error("hasMore should be false — only 2 unacked messages remain")
	}
	if len(msgs) != 2 {
		t.Fatalf("expected exactly the 2 unacked messages to survive compaction, got %d", len(msgs))
	}
	gotIDs := map[string]bool{messageIDOf(t, msgs[0]): true, messageIDOf(t, msgs[1]): true}
	if !gotIDs["stays-unacked-1"] || !gotIDs["stays-unacked-2"] {
		t.Fatalf("compaction dropped or corrupted an unacked message; got ids=%v", gotIDs)
	}

	// Re-acking the newest-retained acked ids (acked-7, acked-8, acked-9 —
	// the newest 3 by append order) must still be recognized as REAL known
	// messages (Acknowledged, not Unknown) — proving they survived
	// compaction, not just that the ack bookkeeping happens to tolerate
	// unknown ids.
	retained, err := s.AckDetailed(ownerKey, []string{"acked-7", "acked-8", "acked-9"})
	if err != nil {
		t.Fatalf("AckDetailed (retained re-ack): %v", err)
	}
	if len(retained.Acknowledged) != 3 {
		t.Fatalf("expected the 3 newest acked messages to still be known after compaction, got Acknowledged=%v Unknown=%v",
			retained.Acknowledged, retained.Unknown)
	}

	// The 7 OLDEST acked messages (acked-0..acked-6) are beyond the
	// retention cap of 3 and must have been purged — re-acking them now
	// reports Unknown, proving the file was actually rewritten smaller, not
	// merely that a cap was computed and ignored.
	purged, err := s.AckDetailed(ownerKey, []string{"acked-0", "acked-3", "acked-6"})
	if err != nil {
		t.Fatalf("AckDetailed (purged re-ack): %v", err)
	}
	if len(purged.Unknown) != 3 {
		t.Fatalf("expected the 3 oldest acked messages to have been purged by compaction (Unknown), got Acknowledged=%v Unknown=%v",
			purged.Acknowledged, purged.Unknown)
	}
}

// TestMessageInboxStore_Compaction_DrainCursorSurvivesCompaction is the
// critical cursor-safety regression guard for the compaction fix: a Drain
// cursor issued BEFORE a compaction that physically removes entries
// POSITIONED BEFORE the cursor's logical point must still resume correctly
// afterward — neither skipping nor redelivering. A naive array-index-based
// cursor breaks this exact scenario (removing earlier entries shifts every
// later entry's position); the Seq-based cursor must not.
func TestMessageInboxStore_Compaction_DrainCursorSurvivesCompaction(t *testing.T) {
	s := newTestInboxStore(t)
	s.ChildSendRatePerMinute = 100000
	s.AckedRetentionMax = 1
	s.CompactionAckedTrigger = 2 // fires once ackedMsgTotal > 2

	const ownerKey = "owner-cursor"
	const fillerChild = "filler-child"
	const targetChild = "target-child"

	// Two filler messages, acked together — ackedMsgTotal becomes 2, at
	// (not over) the trigger, so no compaction yet.
	if _, err := s.Append(ownerKey, progressMsg(t, fillerChild, "filler-1")); err != nil {
		t.Fatalf("Append filler-1: %v", err)
	}
	if _, err := s.Append(ownerKey, progressMsg(t, fillerChild, "filler-2")); err != nil {
		t.Fatalf("Append filler-2: %v", err)
	}
	if err := s.Ack(ownerKey, []string{"filler-1", "filler-2"}); err != nil {
		t.Fatalf("Ack fillers: %v", err)
	}

	// The 3 target messages this test drains, positioned AFTER the (still
	// present) filler entries.
	for _, id := range []string{"t1", "t2", "t3"} {
		if _, err := s.Append(ownerKey, progressMsg(t, targetChild, id)); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}

	// Page 1: drain exactly t1, capture the cursor BEFORE any compaction has
	// ever run against this file.
	page1, cursor1, hasMore1, err := s.Drain(ownerKey, targetChild, "", 1)
	if err != nil {
		t.Fatalf("Drain page1: %v", err)
	}
	if len(page1) != 1 || messageIDOf(t, page1[0]) != "t1" {
		t.Fatalf("page1: want [t1], got %v", page1)
	}
	if !hasMore1 {
		t.Fatal("page1: expected hasMore=true (t2, t3 remain)")
	}

	// Trigger compaction: one more filler message, acked, pushes
	// ackedMsgTotal to 3 > trigger(2). Compaction then keeps only the
	// NEWEST 1 acked filler (filler-3) and drops filler-1/filler-2 — which
	// are POSITIONED BEFORE t1/t2/t3 in the file, so this shifts t1/t2/t3's
	// array indices down by 2. A raw-index cursor from page1 would now
	// silently resume from the WRONG logical point.
	if _, err := s.Append(ownerKey, progressMsg(t, fillerChild, "filler-3")); err != nil {
		t.Fatalf("Append filler-3: %v", err)
	}
	if err := s.Ack(ownerKey, []string{"filler-3"}); err != nil {
		t.Fatalf("Ack filler-3 (triggers compaction): %v", err)
	}

	// Sanity: prove the compaction actually ran and actually dropped
	// filler-1/filler-2 (not a no-op) — re-acking them now reports Unknown.
	sanity, err := s.AckDetailed(ownerKey, []string{"filler-1", "filler-2"})
	if err != nil {
		t.Fatalf("AckDetailed sanity check: %v", err)
	}
	if len(sanity.Unknown) != 2 {
		t.Fatalf("fixture broken: expected filler-1/filler-2 to have been purged by compaction, got Acknowledged=%v Unknown=%v",
			sanity.Acknowledged, sanity.Unknown)
	}

	// Page 2: resume from the PRE-compaction cursor. Must be exactly
	// [t2, t3] — no redelivery of t1, no skipping either one.
	page2, _, hasMore2, err := s.Drain(ownerKey, targetChild, cursor1, 10)
	if err != nil {
		t.Fatalf("Drain page2: %v", err)
	}
	gotIDs := make([]string, len(page2))
	for i, m := range page2 {
		gotIDs[i] = messageIDOf(t, m)
	}
	if len(page2) != 2 || gotIDs[0] != "t2" || gotIDs[1] != "t3" {
		t.Fatalf("page2 (resumed across a compaction): got %v, want exactly [t2 t3] — "+
			"a mismatch here means the cursor did not survive compaction (redelivery or silent skip)", gotIDs)
	}
	if hasMore2 {
		t.Error("page2: expected hasMore=false after draining all remaining target messages")
	}
}

// TestMessageInboxStore_RateWindows_SweepsQuiescentKeys is the LOW-finding
// regression guard: rateWindows must not accumulate one permanent entry per
// ever-seen child forever. Once the map's key count crosses
// rateWindowSweepThreshold, the next call sweeps every OTHER key whose
// window has fully expired, deleting it.
func TestMessageInboxStore_RateWindows_SweepsQuiescentKeys(t *testing.T) {
	s := newTestInboxStore(t)

	oldThreshold := rateWindowSweepThreshold
	rateWindowSweepThreshold = 5
	t.Cleanup(func() { rateWindowSweepThreshold = oldThreshold })

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		msg := progressMsg(t, "quiescent-child-"+strconv.Itoa(i), "q-"+strconv.Itoa(i))
		if _, err := s.Append("owner-sweep", msg); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}
	if got := len(s.rateWindows); got != 5 {
		t.Fatalf("fixture broken: want 5 rate-window keys before advancing time, got %d", got)
	}

	// Advance well past the 1-minute rate window so all 5 existing keys are
	// now fully quiescent, then push the map size past the (lowered)
	// threshold with one more distinct child's send — the call that must
	// trigger the sweep.
	now = now.Add(2 * time.Minute)
	if _, err := s.Append("owner-sweep", progressMsg(t, "trigger-child", "trigger-1")); err != nil {
		t.Fatalf("trigger Append: %v", err)
	}

	// Positive lower bound alongside the near-zero assertion: exactly the
	// ONE key the triggering call itself just wrote must survive — proving
	// the sweep is targeted (only quiescent keys), not "delete everything".
	if got := len(s.rateWindows); got != 1 {
		t.Fatalf("want exactly 1 surviving rate-window key after the sweep (the triggering call's own), got %d: %v",
			got, s.rateWindows)
	}
}
