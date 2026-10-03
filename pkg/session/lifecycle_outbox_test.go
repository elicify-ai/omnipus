// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-20260928 sub-agent control plane (asset cd20cf8b), D2/T11: REAL-STORE
// negative controls for the committed final-delivery outbox and its
// delivery-only journal (pkg/session/lifecycle_outbox.go).
//
// Every expected value below derives from the ADR, never from observed
// behavior of the code:
//
//   - D2 "Legal post-terminal delivery writes": UpdateFinalDelivery is the
//     only legal post-terminal writer; its commands are advance_progress and
//     retire_payload only; ordinary Persist/Mutate still refuse a
//     same-generation terminal lifecycle append through
//     persistLocked/ErrLifecycleTerminalImmutable.
//   - D2 "Payload retirement": retire_payload is legal only after the exact
//     parent inbox append is durable AND either its required frames/wake are
//     durably recorded (both — frames AND wake) or the matching inbox id is
//     durably acknowledged; i.e. the earned predicate is
//     inbox && ((frames && wake) || ack). A wake without frames has NOT
//     earned retirement. Retirement first appends a durable marker, then
//     atomically compacts the journal so the exact payload bytes are gone
//     while the commit/hash/replay identity survives.
//   - D2 "Discovery after a newer-generation RESUME":
//     ListPendingFinalDeliveries scans all generations; delivery envelopes
//     join only to their matching protected commit — an orphan, corrupt or
//     mismatching envelope is a visible consistency error that can never
//     authorize publication or retirement.
//   - D2 "Negative controls (T11)": every refusal is visible, with the
//     protected commit and the current newer-generation tail unchanged.
//
// Scope boundary, stated precisely: these tests exercise STORE validation
// against PROVIDED progress snapshots. A frame receipt is a fact only a
// real publisher can observe — no test here claims frames persisted merely
// because a snapshot asserts it, and the runtime's duty to check actual
// frame persistence before recording FramesPersisted is a separate runtime
// T11 concern (pkg/agent lane), deliberately out of this file. The store
// API itself accepts progress snapshots as recorded facts; that is the
// surface under test here.
package session

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"testing"
)

// qaOutboxAgent is the fixture ParentAgentID (realistic mint shape; the
// store does not validate this field, the mint site owns it).
const qaOutboxAgent = "qa-lead-agent"

// seedTerminalFinal commits one terminal record (done/failed) carrying its
// protected final-delivery tuple through the public Persist path — the same
// atomic commit D2 requires ("the same atomic lifecycle mutation that
// commits done/failed stores {generation, commit_id, ..., payload_hash,
// exact_upward_message}"). It returns the committed tuple so tests assert
// against the spec-derived identity, never against a re-read of internal
// state.
func seedTerminalFinal(t *testing.T, s *LifecycleStore, sessionID string, generation int, state LifecycleState, parentID, payload, payloadHash string) FinalDeliveryCommit {
	t.Helper()
	if !IsTerminalLifecycleState(state) {
		t.Fatalf("seedTerminalFinal: state %q is not terminal", state)
	}
	commit := FinalDeliveryCommit{
		Generation:      generation,
		CommitID:        fmt.Sprintf("run-%s-g%d", sessionID, generation),
		MessageID:       fmt.Sprintf("%s:%d:final", sessionID, generation),
		Outcome:         string(state),
		ParentSessionID: parentID,
		PayloadHash:     payloadHash,
		Payload:         []byte(payload),
	}
	rec := &LifecycleRecord{
		SessionID:      sessionID,
		Generation:     generation,
		State:          state,
		FailedReason:   qaOutboxFailedReason(state),
		OwnerScopeKind: OwnerScopeParentSession,
		OwnerScopeID:   parentID,
		ParentAgentID:  qaOutboxAgent,
		Title:          "qa outbox fixture",
		FinalDelivery:  &commit,
	}
	if err := s.Persist(rec); err != nil {
		t.Fatalf("seedTerminalFinal: Persist committed terminal record for %s g%d: %v", sessionID, generation, err)
	}
	return commit
}

// qaOutboxFailedReason satisfies persistLocked's failed-requires-reason rule.
func qaOutboxFailedReason(state LifecycleState) string {
	if state == LifecycleFailed {
		return "qa-fixture-failure"
	}
	return ""
}

// seedNextGeneration mints a newer running generation on top of a terminal
// tail — the store-level shape of D2's explicit RESUME of a committed
// done/failed G creating G+1 while G's result is still pending.
func seedNextGeneration(t *testing.T, s *LifecycleStore, sessionID string, generation int, parentID string) {
	t.Helper()
	if err := s.Persist(&LifecycleRecord{
		SessionID:      sessionID,
		Generation:     generation,
		ResumedFrom:    fmt.Sprintf("%d", generation-1),
		State:          LifecycleRunning,
		OwnerScopeKind: OwnerScopeParentSession,
		OwnerScopeID:   parentID,
		ParentAgentID:  qaOutboxAgent,
	}); err != nil {
		t.Fatalf("seedNextGeneration: Persist generation %d for %s: %v", generation, sessionID, err)
	}
}

// mustAdvance records durable delivery facts and fails the test when the
// store refuses them (advances in these fixtures only ever carry facts the
// scenario treats as genuinely observed).
func mustAdvance(t *testing.T, s *LifecycleStore, sessionID string, generation int, commitID string, expectedRevision int64, progress FinalDeliveryProgress) {
	t.Helper()
	if err := s.UpdateFinalDelivery(sessionID, generation, commitID, expectedRevision, FinalDeliveryCommand{Advance: &progress}); err != nil {
		t.Fatalf("advance %+v at expected revision %d: %v", progress, expectedRevision, err)
	}
}

// mustRetire retires the payload through the public command and fails the
// test when the store refuses it.
func mustRetire(t *testing.T, s *LifecycleStore, sessionID string, generation int, commitID string, expectedRevision int64) {
	t.Helper()
	if err := s.UpdateFinalDelivery(sessionID, generation, commitID, expectedRevision, FinalDeliveryCommand{Retire: true}); err != nil {
		t.Fatalf("retire_payload at expected revision %d: %v", expectedRevision, err)
	}
}

// findPending returns the ListPendingFinalDeliveries item for (sessionID,
// generation), failing the test when the scan itself errors or the identity
// is missing from the results.
func findPending(t *testing.T, s *LifecycleStore, sessionID string, generation int) PendingFinalDelivery {
	t.Helper()
	items, err := s.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries: %v", err)
	}
	for _, item := range items {
		if item.SessionID == sessionID && item.Generation == generation {
			return item
		}
	}
	t.Fatalf("ListPendingFinalDeliveries: no final for session %q generation %d; got %+v", sessionID, generation, items)
	return PendingFinalDelivery{}
}

// ---------------------------------------------------------------------------
// Expected-RED negative controls (T11)
// ---------------------------------------------------------------------------

// RED (T11 "premature payload retirement"): the publisher has a durable
// inbox append and a recorded wake, but the frames receipt never arrived.
// D2 "Payload retirement" earns retirement only via
// inbox && ((frames && wake) || ack) — a wake without frames has NOT earned
// it — so Retire must be refused with ErrFinalDeliveryRetireNotEarned and
// the committed payload, identity and delivery revision must survive
// untouched.
func TestFinalDelivery_RetireRefusedBeforeFramesPersisted(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-n1"
	parent := "qa-parent-n1"
	payload := "omnipus-final-payload-n1"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, parent, payload, "sha256-n1")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true, WakeRecorded: true})

	err := s.UpdateFinalDelivery(sid, 1, commit.CommitID, 1, FinalDeliveryCommand{Retire: true})
	if !errors.Is(err, ErrFinalDeliveryRetireNotEarned) {
		t.Fatalf("premature retire without frames: got err %v, want ErrFinalDeliveryRetireNotEarned", err)
	}

	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState after refused retire: %v", err)
	}
	if retired {
		t.Fatalf("refused retire nonetheless marked the payload retired")
	}
	if want := (FinalDeliveryProgress{InboxAppended: true, WakeRecorded: true}); progress != want {
		t.Fatalf("progress = %+v, want %+v (a refused command must not change the summary)", progress, want)
	}
	if revision != 1 {
		t.Fatalf("revision = %d, want 1 (a refused command must not append an envelope)", revision)
	}
	item := findPending(t, s, sid, 1)
	if !bytes.Equal(item.Commit.Payload, []byte(payload)) {
		t.Fatalf("payload after refused retire = %q, want %q (refusal must keep the payload recoverable)", item.Commit.Payload, payload)
	}
	if item.Commit.PayloadHash != "sha256-n1" || item.Commit.MessageID != commit.MessageID || item.Commit.ParentSessionID != parent {
		t.Fatalf("commit identity changed after refused retire: %+v", item.Commit)
	}
}

// RED (D2 "Discovery after a newer-generation RESUME": an orphan, corrupt
// or MISMATCHING envelope is a visible consistency error and cannot
// authorize publication or retirement). A forged final_delivery_update
// envelope — right generation and commit id, advancing revision, but a
// Protected tuple contradicting the committed identity (message id,
// outcome, parent, payload hash) — is appended straight onto the real
// journal file. Every read path must fail visibly, nothing may be
// replaced, and retirement must not become authorized through the forged
// envelope.
func TestFinalDelivery_ForgedEnvelopeIsVisibleConsistencyError(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-n2"
	parent := "qa-parent-n2"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, parent, "omnipus-final-payload-n2", "sha256-n2")

	// Positive control first: the freshly seeded store reports exactly one
	// clean, unretired, untouched final.
	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil || progress != (FinalDeliveryProgress{}) || revision != 0 || retired {
		t.Fatalf("seeded store not clean: progress %+v revision %d retired %v err %v", progress, revision, retired, err)
	}
	if item := findPending(t, s, sid, 1); !item.Pending() {
		t.Fatalf("freshly committed final is not pending: %+v", item)
	}

	forged := &finalDeliveryUpdateEnvelope{
		Kind:             JournalKindFinalDeliveryUpdate,
		SessionID:        sid,
		Generation:       1,
		CommitID:         commit.CommitID,
		DeliveryRevision: 1,
		DeliveryProgress: FinalDeliveryProgress{InboxAppended: true, AckObserved: true},
		Protected: &FinalDeliveryCommit{
			Generation:      1,
			CommitID:        commit.CommitID,
			MessageID:       "forged-child:1:final",
			Outcome:         "failed",
			ParentSessionID: "forged-parent",
			PayloadHash:     "forged-payload-hash",
		},
	}
	if err := s.appendJournalEnvelope(sid, forged); err != nil {
		t.Fatalf("append forged envelope to the real journal: %v", err)
	}

	if _, _, _, err := s.FinalDeliveryState(sid, 1, commit.CommitID); !errors.Is(err, ErrFinalDeliveryOrphanEnvelope) {
		t.Fatalf("FinalDeliveryState accepted a mismatching envelope: err %v, want ErrFinalDeliveryOrphanEnvelope", err)
	}
	items, err := s.ListPendingFinalDeliveries()
	if !errors.Is(err, ErrFinalDeliveryOrphanEnvelope) {
		t.Fatalf("ListPendingFinalDeliveries accepted a mismatching envelope: err %v (items %+v), want ErrFinalDeliveryOrphanEnvelope", err, items)
	}
	if err := s.UpdateFinalDelivery(sid, 1, commit.CommitID, 1, FinalDeliveryCommand{Retire: true}); err == nil {
		t.Fatalf("retire_payload succeeded through a forged envelope; want a visible refusal")
	}

	// Nothing replaced: the durable tail record keeps the committed identity.
	tail, err := s.Load(sid)
	if err != nil {
		t.Fatalf("Load after forged envelope: %v", err)
	}
	if tail.State != LifecycleCompleted || tail.Generation != 1 {
		t.Fatalf("tail = %s g%d, want completed g1 (an envelope must never become session state)", tail.State, tail.Generation)
	}
	if tail.FinalDelivery == nil {
		t.Fatalf("tail lost its final_delivery commit")
	}
	got := tail.FinalDelivery
	if got.MessageID != commit.MessageID || got.Outcome != commit.Outcome || got.ParentSessionID != commit.ParentSessionID || got.PayloadHash != commit.PayloadHash || got.CommitID != commit.CommitID {
		t.Fatalf("committed identity mutated behind the forged envelope: got %+v want %+v", got, commit)
	}
	if !bytes.Equal(got.Payload, commit.Payload) {
		t.Fatalf("committed payload mutated: got %q want %q", got.Payload, commit.Payload)
	}
}

// RED (T11 "a progress regression ... must be refused visibly"): after the
// store durably recorded inbox_appended=true, a later snapshot claiming
// inbox_appended=false — a recorded fact going backwards, the exact
// opposite of the monotonic false→true fact model — must be refused
// visibly, not silently OR-merged away while its other facts are absorbed.
// The ADR names no sentinel for this refusal, so the oracle is a non-nil
// error plus an unchanged summary; the specific error type is the fix's
// choice.
func TestFinalDelivery_AdvanceRefusesRegressedFact(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-n3"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-n3", "omnipus-final-payload-n3", "sha256-n3")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true})

	regressed := FinalDeliveryProgress{InboxAppended: false, FramesPersisted: true}
	err := s.UpdateFinalDelivery(sid, 1, commit.CommitID, 1, FinalDeliveryCommand{Advance: &regressed})
	if err == nil {
		t.Fatalf("progress regression (inbox_appended true->false) accepted silently; want a visible refusal")
	}

	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState after refused regression: %v", err)
	}
	if progress != (FinalDeliveryProgress{InboxAppended: true}) {
		t.Fatalf("progress = %+v, want {inbox only} (a refused command must not change the summary)", progress)
	}
	if revision != 1 || retired {
		t.Fatalf("revision = %d retired = %v, want 1/false (a refused command must not append an envelope)", revision, retired)
	}
}

// RED (D2 "Payload retirement": first a durable retirement marker, THEN an
// atomic compaction removing ONLY this final's exact payload bytes, with
// the outcome/outbox descriptor and the complete delivery summary
// retained). Once retire_payload reports success, the retirement — marker
// plus compaction — is durable: a fresh read of the journal file must no
// longer contain the exact payload bytes, while every identity field, the
// replay id and the delivery summary survive.
//
// The fixture payload is ASCII-only, and the needle is its BASE64 form:
// the journal writes records through fileutil.AppendJSONL → json.Marshal,
// and encoding/json encodes a []byte field as a base64 string on disk. The
// instrument is proven before it is trusted: the needle must be found in
// the journal BEFORE retirement, so its absence afterwards can only mean
// compaction removed the bytes (an encoding change fails loudly at the
// pre-check instead of passing vacuously).
func TestFinalDelivery_RetireRemovesPayloadBytesFromJournal(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-n4"
	payload := "omnipus-final-payload-n4-ASCII"
	needle := []byte(base64.StdEncoding.EncodeToString([]byte(payload)))
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-n4", payload, "sha256-n4")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true, FramesPersisted: true, WakeRecorded: true})

	raw, err := os.ReadFile(s.path(sid))
	if err != nil {
		t.Fatalf("read journal before retirement: %v", err)
	}
	if !bytes.Contains(raw, needle) {
		t.Fatalf("instrument check: base64 payload needle %q not found in the journal BEFORE retirement — the on-disk encoding assumption is stale, this test cannot see the failure", needle)
	}

	mustRetire(t, s, sid, 1, commit.CommitID, 1)

	raw, err = os.ReadFile(s.path(sid))
	if err != nil {
		t.Fatalf("read journal after retirement: %v", err)
	}
	if bytes.Contains(raw, needle) {
		t.Fatalf("payload bytes still present in the journal after successful retire_payload (compaction did not run)")
	}
	if !bytes.Contains(raw, []byte(commit.MessageID)) {
		t.Fatalf("journal lost the replay id %q across retirement", commit.MessageID)
	}

	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState after retirement: %v", err)
	}
	if !retired {
		t.Fatalf("retirement marker missing after successful retire_payload")
	}
	if want := (FinalDeliveryProgress{InboxAppended: true, FramesPersisted: true, WakeRecorded: true}); progress != want {
		t.Fatalf("delivery summary = %+v, want %+v (compaction must retain the complete delivery summary)", progress, want)
	}
	if revision != 2 {
		t.Fatalf("revision = %d, want 2", revision)
	}
	item := findPending(t, s, sid, 1)
	if item.Pending() {
		t.Fatalf("retired final still reports pending (republication risk): %+v", item)
	}
	if item.Commit.Payload != nil {
		t.Fatalf("retired final still exposes payload bytes: %q", item.Commit.Payload)
	}
	if item.Commit.PayloadHash != commit.PayloadHash || item.Commit.MessageID != commit.MessageID || item.Commit.ParentSessionID != commit.ParentSessionID || item.Commit.Outcome != commit.Outcome {
		t.Fatalf("identity mutated by retirement: got %+v want (minus payload) %+v", item.Commit, commit)
	}
}

// ---------------------------------------------------------------------------
// Controls: earned paths and refusals the store must keep making
// ---------------------------------------------------------------------------

// Positive control (T11 instrument check): a freshly seeded real store
// reports exactly one unretired, still-pending final with the exact
// committed payload and identity — the baseline every negative control
// below relies on.
func TestFinalDelivery_SeededCommitIsDiscoverableAndPending(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c0"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c0", "omnipus-final-payload-c0", "sha256-c0")

	items, err := s.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d finals, want exactly 1: %+v", len(items), items)
	}
	item := items[0]
	if item.SessionID != sid || item.Generation != 1 {
		t.Fatalf("identity = %s g%d, want %s g1", item.SessionID, item.Generation, sid)
	}
	if !item.Pending() {
		t.Fatalf("unadvanced final must be pending: %+v", item)
	}
	if item.Retired || item.Revision != 0 || item.Progress != (FinalDeliveryProgress{}) {
		t.Fatalf("fresh commit has delivery state %+v rev %d retired %v; want zero state", item.Progress, item.Revision, item.Retired)
	}
	if !bytes.Equal(item.Commit.Payload, commit.Payload) {
		t.Fatalf("payload = %q, want %q", item.Commit.Payload, commit.Payload)
	}
	if item.Commit.MessageID != commit.MessageID || item.Commit.CommitID != commit.CommitID || item.Commit.PayloadHash != commit.PayloadHash || item.Commit.ParentSessionID != commit.ParentSessionID || item.Commit.Outcome != commit.Outcome {
		t.Fatalf("commit identity = %+v, want %+v", item.Commit, commit)
	}
}

// Control (D2 earned path 1): inbox + frames + wake earns retirement
// exactly once; the marker preserves the replay tuple; a repeat retire is
// an idempotent no-op, not a second marker; further facts are refused with
// ErrFinalDeliveryRetired.
func TestFinalDelivery_RetireOnceAfterFramesAndWake(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c1"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c1", "omnipus-final-payload-c1", "sha256-c1")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true, FramesPersisted: true, WakeRecorded: true})
	mustRetire(t, s, sid, 1, commit.CommitID, 1)

	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil || !retired || revision != 2 {
		t.Fatalf("after retire: progress %+v rev %d retired %v err %v; want rev 2 retired true", progress, revision, retired, err)
	}
	if progress != (FinalDeliveryProgress{InboxAppended: true, FramesPersisted: true, WakeRecorded: true}) {
		t.Fatalf("summary = %+v; retirement must retain the complete delivery summary", progress)
	}
	if err := s.UpdateFinalDelivery(sid, 1, commit.CommitID, 2, FinalDeliveryCommand{Retire: true}); err != nil {
		t.Fatalf("repeat retire: %v (an identical already-earned retire is an idempotent no-op)", err)
	}
	if _, revAfter, _, err := s.FinalDeliveryState(sid, 1, commit.CommitID); err != nil || revAfter != 2 {
		t.Fatalf("revision = %d err %v after idempotent repeat, want 2 (no second marker)", revAfter, err)
	}
	late := FinalDeliveryProgress{AckObserved: true}
	if err := s.UpdateFinalDelivery(sid, 1, commit.CommitID, 2, FinalDeliveryCommand{Advance: &late}); !errors.Is(err, ErrFinalDeliveryRetired) {
		t.Fatalf("advance after retire: err %v, want ErrFinalDeliveryRetired", err)
	}

	item := findPending(t, s, sid, 1)
	if item.Pending() {
		t.Fatalf("retired final must not be pending: %+v", item)
	}
	if item.Commit.Payload != nil {
		t.Fatalf("retired final must not expose payload bytes: %q", item.Commit.Payload)
	}
	if item.Commit.MessageID != commit.MessageID {
		t.Fatalf("replay tuple lost: MessageID %q, want %q", item.Commit.MessageID, commit.MessageID)
	}
	if commit.ReplayID(sid) != commit.MessageID {
		t.Fatalf("ReplayID drifted: %q != %q", commit.ReplayID(sid), commit.MessageID)
	}
}

// Control (D2 earned path 2): a durably acknowledged inbox id earns
// retirement without frames or wake — inbox + ack is the alternative path.
func TestFinalDelivery_RetireOnceAfterAcknowledgment(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c2"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c2", "omnipus-final-payload-c2", "sha256-c2")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true, AckObserved: true})
	mustRetire(t, s, sid, 1, commit.CommitID, 1)

	_, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil || !retired || revision != 2 {
		t.Fatalf("after ack-path retire: rev %d retired %v err %v; want rev 2 retired true", revision, retired, err)
	}
	item := findPending(t, s, sid, 1)
	if item.Pending() || item.Commit.Payload != nil || item.Commit.PayloadHash != commit.PayloadHash {
		t.Fatalf("retired final wrong state: %+v", item)
	}
}

// Boundary control: frames WITHOUT wake is not delivery either — retiring
// on {inbox, frames} alone must be refused, both before and after the
// frames/wake conjunction fix (guards against overcorrecting the earned
// predicate to inbox && (frames || wake || ack)).
func TestFinalDelivery_RetireRefusedWithFramesButNoWake(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c10"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c10", "omnipus-final-payload-c10", "sha256-c10")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true, FramesPersisted: true})

	err := s.UpdateFinalDelivery(sid, 1, commit.CommitID, 1, FinalDeliveryCommand{Retire: true})
	if !errors.Is(err, ErrFinalDeliveryRetireNotEarned) {
		t.Fatalf("retire with frames but no wake: got err %v, want ErrFinalDeliveryRetireNotEarned", err)
	}
	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil || progress != (FinalDeliveryProgress{InboxAppended: true, FramesPersisted: true}) || revision != 1 || retired {
		t.Fatalf("state after refused retire: progress %+v rev %d retired %v err %v; want unchanged {inbox,frames} rev 1", progress, revision, retired, err)
	}
}

// Control (D2 idempotent retry: "retry idempotent frames/wake"): re-advancing
// the exact facts already on record is an idempotent no-op — no second
// envelope, revision unchanged.
func TestFinalDelivery_AdvanceIdempotentRepeatAppendsNothing(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c11"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c11", "omnipus-final-payload-c11", "sha256-c11")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true})
	mustAdvance(t, s, sid, 1, commit.CommitID, 1, FinalDeliveryProgress{InboxAppended: true})

	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil || revision != 1 || retired {
		t.Fatalf("after idempotent repeat: rev %d retired %v err %v, want rev 1 (no second envelope)", revision, retired, err)
	}
	if progress != (FinalDeliveryProgress{InboxAppended: true}) {
		t.Fatalf("progress = %+v, want {inbox only}", progress)
	}
}

// Control (T11 "stale revision"): a changed fact presented under a stale
// expected revision is refused with ErrFinalDeliveryRevisionConflict and
// the summary stays at its current state.
func TestFinalDelivery_AdvanceWithStaleRevisionRefused(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c8"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c8", "omnipus-final-payload-c8", "sha256-c8")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true})

	stale := FinalDeliveryProgress{FramesPersisted: true}
	err := s.UpdateFinalDelivery(sid, 1, commit.CommitID, 0, FinalDeliveryCommand{Advance: &stale})
	if !errors.Is(err, ErrFinalDeliveryRevisionConflict) {
		t.Fatalf("stale-revision advance: got err %v, want ErrFinalDeliveryRevisionConflict", err)
	}
	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil || progress != (FinalDeliveryProgress{InboxAppended: true}) || revision != 1 || retired {
		t.Fatalf("state after stale refusal: progress %+v rev %d retired %v err %v; want unchanged {inbox} rev 1", progress, revision, retired, err)
	}
}

// Control (T11 "an update without a matching commit"): advancing or
// retiring a (generation, commit) with no committed final is
// ErrFinalDeliveryUnknownCommit — never a silent envelope — and the real
// commit stays untouched.
func TestFinalDelivery_UpdateWithoutMatchingCommitRefused(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c5"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c5", "omnipus-final-payload-c5", "sha256-c5")

	adv := FinalDeliveryProgress{InboxAppended: true}
	if err := s.UpdateFinalDelivery(sid, 7, "run-never-happened", 0, FinalDeliveryCommand{Advance: &adv}); !errors.Is(err, ErrFinalDeliveryUnknownCommit) {
		t.Fatalf("unknown-generation advance: got err %v, want ErrFinalDeliveryUnknownCommit", err)
	}
	if err := s.UpdateFinalDelivery(sid, 1, "not-"+commit.CommitID, 0, FinalDeliveryCommand{Retire: true}); !errors.Is(err, ErrFinalDeliveryUnknownCommit) {
		t.Fatalf("unknown-commit retire: got err %v, want ErrFinalDeliveryUnknownCommit", err)
	}
	progress, revision, retired, err := s.FinalDeliveryState(sid, 1, commit.CommitID)
	if err != nil || progress != (FinalDeliveryProgress{}) || revision != 0 || retired {
		t.Fatalf("real commit disturbed by unknown-identity updates: progress %+v rev %d retired %v err %v", progress, revision, retired, err)
	}
}

// Control (D2 "an orphan ... envelope is a visible consistency error"):
// an envelope naming a commit that was never committed fails the whole
// scan — and FinalDeliveryState — with ErrFinalDeliveryOrphanEnvelope.
func TestFinalDelivery_OrphanEnvelopeFailsScanVisibly(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c9"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c9", "omnipus-final-payload-c9", "sha256-c9")

	orphan := &finalDeliveryUpdateEnvelope{
		Kind:             JournalKindFinalDeliveryUpdate,
		SessionID:        sid,
		Generation:       9,
		CommitID:         "run-orphan",
		DeliveryRevision: 1,
		DeliveryProgress: FinalDeliveryProgress{InboxAppended: true},
	}
	if err := s.appendJournalEnvelope(sid, orphan); err != nil {
		t.Fatalf("append orphan envelope: %v", err)
	}

	items, err := s.ListPendingFinalDeliveries()
	if !errors.Is(err, ErrFinalDeliveryOrphanEnvelope) {
		t.Fatalf("scan with an orphan envelope: err %v (items %+v), want ErrFinalDeliveryOrphanEnvelope", err, items)
	}
	if _, _, _, err := s.FinalDeliveryState(sid, 1, commit.CommitID); !errors.Is(err, ErrFinalDeliveryOrphanEnvelope) {
		t.Fatalf("FinalDeliveryState with an orphan envelope: err %v, want ErrFinalDeliveryOrphanEnvelope", err)
	}
}

// Control (D2 "Legal post-terminal delivery writes": ordinary Persist/Mutate
// still refuse a same-generation terminal lifecycle append through
// persistLocked/ErrLifecycleTerminalImmutable — the delivery-only journal
// must never become a loophole for an outcome or payload content switch).
func TestFinalDelivery_TerminalRecordRefusesSameGenerationReplacement(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c3"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c3", "omnipus-final-payload-c3", "sha256-c3")

	replacement := &LifecycleRecord{
		SessionID:      sid,
		Generation:     1,
		State:          LifecycleFailed,
		FailedReason:   "replaced-after-the-fact",
		OwnerScopeKind: OwnerScopeParentSession,
		OwnerScopeID:   "qa-parent-c3",
		ParentAgentID:  qaOutboxAgent,
		FinalDelivery: &FinalDeliveryCommit{
			Generation:      1,
			CommitID:        commit.CommitID,
			MessageID:       commit.MessageID,
			Outcome:         "failed",
			ParentSessionID: commit.ParentSessionID,
			PayloadHash:     "sha256-forged-replacement",
			Payload:         []byte("omnipus-forged-replacement-payload"),
		},
	}
	if err := s.Persist(replacement); !errors.Is(err, ErrLifecycleTerminalImmutable) {
		t.Fatalf("same-generation terminal Persist: err %v, want ErrLifecycleTerminalImmutable", err)
	}

	err := s.Mutate(sid, func(r *LifecycleRecord) error {
		r.State = LifecycleFailed
		r.FailedReason = "replaced-after-the-fact"
		r.FinalDelivery.PayloadHash = "sha256-forged-replacement"
		r.FinalDelivery.Payload = []byte("omnipus-forged-replacement-payload")
		return nil
	})
	if !errors.Is(err, ErrLifecycleTerminalImmutable) {
		t.Fatalf("same-generation terminal Mutate: err %v, want ErrLifecycleTerminalImmutable", err)
	}

	tail, err := s.Load(sid)
	if err != nil {
		t.Fatalf("Load after refused replacements: %v", err)
	}
	if tail.State != LifecycleCompleted {
		t.Fatalf("tail state = %q, want completed (no content switch)", tail.State)
	}
	if tail.FinalDelivery == nil || tail.FinalDelivery.PayloadHash != commit.PayloadHash || !bytes.Equal(tail.FinalDelivery.Payload, commit.Payload) {
		t.Fatalf("committed payload/hash switched: %+v", tail.FinalDelivery)
	}
}

// Control (D2: delivery progress must never become a session state or reset
// a newer generation): after several delivery envelopes — including a
// retirement marker — Load and List still see the original terminal record
// and no phantom non-terminal state appears.
func TestFinalDelivery_EnvelopeLinesNeverBecomeSessionState(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c6"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, "qa-parent-c6", "omnipus-final-payload-c6", "sha256-c6")

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true, FramesPersisted: true, WakeRecorded: true})
	mustRetire(t, s, sid, 1, commit.CommitID, 1)

	tail, err := s.Load(sid)
	if err != nil {
		t.Fatalf("Load with envelopes present: %v", err)
	}
	if tail.State != LifecycleCompleted || tail.Generation != 1 {
		t.Fatalf("tail = %s g%d, want completed g1 (an envelope became session state)", tail.State, tail.Generation)
	}
	if tail.FinalDelivery == nil || tail.FinalDelivery.MessageID != commit.MessageID || tail.FinalDelivery.PayloadHash != commit.PayloadHash {
		t.Fatalf("tail lost the commit identity: %+v", tail.FinalDelivery)
	}
	nonTerminal, err := s.List(LifecycleFilter{NonTerminalOnly: true})
	if err != nil {
		t.Fatalf("List NonTerminalOnly: %v", err)
	}
	if len(nonTerminal) != 0 {
		t.Fatalf("envelopes produced phantom non-terminal records: %+v", nonTerminal)
	}
	all, err := s.List(LifecycleFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].State != LifecycleCompleted {
		t.Fatalf("List = %+v, want exactly the one completed record", all)
	}
}

// Control (D2 "Discovery after a newer-generation RESUME"): a committed
// done G's pending final stays discoverable — with its exact payload —
// after an explicit RESUME commits running G+1; advancing and retiring G
// afterwards never writes behind G+1 or changes G+1's queue/run, and the
// RESUME neither copied G's outbox into G+1 nor hid G's final.
func TestFinalDelivery_OlderGenerationFinalSurvivesNewerGenerationResume(t *testing.T) {
	s := newTestLifecycleStore(t)
	sid := "qa-outbox-c7"
	parent := "qa-parent-c7"
	payload := "omnipus-final-payload-c7"
	commit := seedTerminalFinal(t, s, sid, 1, LifecycleCompleted, parent, payload, "sha256-c7")

	seedNextGeneration(t, s, sid, 2, parent)

	items, err := s.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries after resume: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d finals after G2 exists, want exactly G1's: %+v", len(items), items)
	}
	if !items[0].Pending() || !bytes.Equal(items[0].Commit.Payload, []byte(payload)) {
		t.Fatalf("G1 final not pending-with-payload after G2 exists: %+v", items[0])
	}

	mustAdvance(t, s, sid, 1, commit.CommitID, 0, FinalDeliveryProgress{InboxAppended: true, FramesPersisted: true, WakeRecorded: true})
	tail, err := s.Load(sid)
	if err != nil {
		t.Fatalf("Load after advancing G1: %v", err)
	}
	if tail.Generation != 2 || tail.State != LifecycleRunning {
		t.Fatalf("tail = g%d %s after advancing G1, want g2 running (a delivery write landed behind the newer generation)", tail.Generation, tail.State)
	}

	mustRetire(t, s, sid, 1, commit.CommitID, 1)
	tail, err = s.Load(sid)
	if err != nil {
		t.Fatalf("Load after retiring G1: %v", err)
	}
	if tail.Generation != 2 || tail.State != LifecycleRunning {
		t.Fatalf("tail = g%d %s after retiring G1, want g2 running (retirement touched the newer generation)", tail.Generation, tail.State)
	}
	item := findPending(t, s, sid, 1)
	if item.Retired && item.Pending() {
		t.Fatalf("retired G1 final still pending: %+v", item)
	}
	if !item.Retired || item.Commit.Payload != nil || item.Commit.PayloadHash != commit.PayloadHash || item.Commit.MessageID != commit.MessageID {
		t.Fatalf("G1 final after retirement = %+v, want retired, payload nil, identity intact", item)
	}
}
