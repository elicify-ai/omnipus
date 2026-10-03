// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// boot_outbox_allgen_bootred_test.go — W3a RED (qa-lead), ADR-20260928
// sub-agent control plane (frozen asset cd20cf8b): D2 "Discovery after a
// newer-generation RESUME", D8.1/D8.5/D8.9, and T11's W3a delivery-only
// slice. The committed final of an OLDER generation G must be published at
// boot while the session's current tail is a newer working G+1.
//
// Test plan (elicify-test-writing step 1)
//
// Behaviour under test: SteerBootRecovery.Run discovers a committed but
// unpublished final for an older generation G, delivers its exact committed
// payload to the direct parent's durable inbox under <child>:G:final, and
// records the delivery facts through the delivery-only
// LifecycleStore.UpdateFinalDelivery — without publishing any uncommitted
// G+1 final, without rewriting G+1's identity, and without promoting any
// record from an inbox final alone.
//
// Specification source (the oracles; nothing below is derived from observed
// behaviour of the code under test):
//
//   - D2, "Discovery after a newer-generation RESUME": boot discovery
//     includes every pending committed final "independently of the current
//     execution"; the published bytes are the committed exact upward
//     message; delivery facts go through UpdateFinalDelivery, never a
//     terminal mutation; "updating G's delivery metadata never changes
//     G+1's state or identity".
//   - D8.1: boot pass two "retries publication of every pending committed
//     final, even if its session's current tail is G+1".
//   - D8.5: "An old G final remains pending/retryable when explicit RESUME
//     has created G+1"; an inbox final without a matching committed outcome
//     cannot repair a lifecycle record.
//   - D8.9: boot retries G's result while the uncommitted current run is
//     stopped; "neither G+1 nor payload retirement hides G's commit".
//   - T11 (W3a slice): "G's exact pending final remains discoverable after
//     G+1 admission and boot, delivers once, and its metadata updates never
//     regress G+1".
//
// Unit boundary — REAL: LifecycleStore (journal, Mutate, UpdateFinalDelivery,
// FinalDeliveryState, ListPendingFinalDeliveries), MessageInboxStore,
// UnifiedStore, SteerRecordClassifier, SteerCanceller.Revive (the real
// RESUME mint on a committed terminal record), SteerBootRecovery.Run (the
// real boot recovery entry), and the production payload encoders
// (withDeterministicMessageID plus the commit boundary's own sha256
// formula). SEAM (process edge): steer.UpwardDeliverer — the shared
// bootRecordingDeliverer appends to the REAL parent inbox and reports the
// wake, standing in for the production SteerUpwardDeliverer exactly as the
// rest of the boot suite does. The fixture commits G through the same ONE
// real LifecycleStore.Mutate the production commit boundary
// (steer_completion_commit.go::commitSteeredCompletion, winning branch)
// performs: terminal state + protected FinalDeliveryCommit in a single
// mutation. The production-wired completion instrument, the
// crash-interval fault injection and the UpdateFinalDelivery negative
// controls remain T11's separate pack (W2a owner) — no green is transferred.
//
// What would break these tests (CHECK mutations): all-generation discovery
// narrowed to the current tail; publish without UpdateFinalDelivery facts;
// published payload diverging from the committed bytes; promotion from an
// inbox final alone; re-wake after acknowledgement; an outbox commit minted
// for the uncommitted G+1.
//
// Known gaps (deliberate, not W3a): the stopped(restart) LANDING of an
// interrupted G+1 — cause note, boot_seq, stop_seq — and D6 direct-parent
// notices (W3b; W2a/W2b dependencies): this pack pins only D8.9's own
// restated bound ("the session must never be `working` by itself after the
// reboot") and neither asserts nor endorses this branch's legacy
// failed(interrupted) outcome as the boot result; payload
// retirement/compaction and the UpdateFinalDelivery refusal matrix (T11
// negative pack); frames as a distinct durable artifact (absorbed by the
// deliverer seam here — asserting a mock's internals would be a fake
// oracle); root→C→D notice batching and wake coalescing (D8.4, W3b); the
// D2 execution-identity seam (run_id/boot_seq on the record — W2b;
// FinalDeliveryCommit's own doc comment: commit_id is a stand-in for
// run_id).
//
// Oracle correction (founder-side dispatcher, 2026-10-03 — the ONE bounded
// W3a qa correction): the original primary RED counted G's publication but
// would not reject a phantom `<child>:G+1:final` published alongside it
// (ListPendingFinalDeliveries sees only committed outbox descriptors; the
// old tail check only forbade `completed`). CORRECTION 1 adds
// assertNoUncommittedFinalPublished: no `<child>:<generation>:final` may
// reach the parent's REAL inbox or the delivered wake seam without the
// matching committed lifecycle+outbox result — D2 "Publish only a committed
// outbox", D8.9 "before → `stopped`, with no final id published" — while a
// genuine committed G+1 final stays legal (the store's own descriptor is
// the witness; the id is never blanket-banned). The deliverer's event
// counter alone is not receipt proof: the inbox read is the same durable
// store the parent reads, cross-checked against the lifecycle outbox.
// CORRECTION 2 is the delivery-only isolation test below: it must ride the
// boot-consumed delivery pass (SteerBootRecovery.Run's D8.1 pass two), NOT
// the live-completion publisher driven directly — without proving the boot
// consumer calls the same method, that path proves nothing. The pass does
// not exist on this branch, so the test is an explicit BLOCKED naming the
// requested production signature; the G+1 pre/post identity-state oracle it
// will pin is specified in its doc comment and lands once, on the merged
// test branch, when the backend adds the method.

package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// allGenFinalID is the deterministic replay id D2 fixes for generation 1.
func allGenFinalID(child string) string {
	return fmt.Sprintf("%s:1:final", child)
}

// commitAllGenDoneFinal commits generation 1 done PLUS the protected,
// unpublished outbox tuple in ONE real LifecycleStore.Mutate — the same
// single-mutation boundary steer_completion_commit.go::commitSteeredCompletion
// performs on its winning path — and leaves the outbox unpublished (the
// crash-before-append cut). The payload bytes are fixed by this fixture and
// returned so the test can demand their exact publication; no expected value
// is read back off the code under test.
func commitAllGenDoneFinal(t *testing.T, h *bootRecoveryHarness, child, parent, commitID, answer string) (finalID string, payload []byte, payloadHash string) {
	t.Helper()
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))

	gen := 1
	var built generated.SessionMessage
	err := built.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: child, SessionId: child, ParentSessionId: &parent,
		CreatedAt: time.Now().UTC(), Depth: 1, Direction: "child_to_parent",
		Generation: &gen, Kind: "handback", Mode: generated.SessionMessageHandbackModeFinal,
		ResultSoFar: answer, SenderIdentity: "agent-1",
		Artifacts: []string{}, OpenQuestions: []string{}, UntrustedOrigin: true,
	})
	if err != nil {
		t.Fatalf("build committed handback: %v", err)
	}
	finalID = allGenFinalID(child)
	stamped, err := withDeterministicMessageID(built, finalID)
	if err != nil {
		t.Fatalf("stamp committed final id: %v", err)
	}
	payload, err = stamped.MarshalJSON()
	if err != nil {
		t.Fatalf("encode committed final: %v", err)
	}
	payloadHash = fmt.Sprintf("%x", sha256.Sum256(payload))

	if err := h.lifecycle.Mutate(child, func(cur *session.LifecycleRecord) error {
		if cur == nil || cur.Generation != 1 {
			return fmt.Errorf("fixture: generation 1 record not current (cur=%v)", cur)
		}
		cur.State = session.LifecycleCompleted
		cur.NeedsInput = nil
		cur.FinalDelivery = &session.FinalDeliveryCommit{
			Generation: 1, CommitID: commitID, MessageID: finalID,
			Outcome: string(steer.OutcomeFinalAnswer), ParentSessionID: parent,
			PayloadHash: payloadHash, Payload: payload,
		}
		return nil
	}); err != nil {
		t.Fatalf("commit G done+outbox in one Mutate: %v", err)
	}
	return finalID, payload, payloadHash
}

// resumeToGPlusOne performs the explicit RESUME of the committed done
// generation through the real production mint (SteerCanceller.Revive), so the
// session's current tail is a working generation 2 that copied nothing from
// G's outbox (D2). Its checks are fixture controls, not behaviour claims.
func resumeToGPlusOne(t *testing.T, h *bootRecoveryHarness, child string) {
	t.Helper()
	reviver := &SteerCanceller{Lifecycle: h.lifecycle}
	generation, err := reviver.Revive(context.Background(), child, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"})
	if err != nil {
		t.Fatalf("Revive (explicit RESUME of committed done G): %v", err)
	}
	if generation != 2 {
		t.Fatalf("RESUME minted generation %d, want 2", generation)
	}
	resumed, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load after RESUME: %v", err)
	}
	if resumed.Generation != 2 || resumed.State != session.LifecycleRunning || resumed.FinalDelivery != nil {
		t.Fatalf("post-RESUME fixture control: gen %d state %q outbox %v, want gen 2 running with G's outbox NOT copied (D2)",
			resumed.Generation, resumed.State, resumed.FinalDelivery)
	}
}

// allGenFinalGeneration reports the generation N of a `<child>:<N>:final`
// replay id and whether the id has that shape at all. The shape is fixed by
// D2/the commit boundary (ReplayID), never read off observed output.
func allGenFinalGeneration(child, id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, child+":")
	if !ok {
		return 0, false
	}
	digits, ok := strings.CutSuffix(rest, ":final")
	if !ok {
		return 0, false
	}
	generation, err := strconv.Atoi(digits)
	if err != nil || generation <= 0 {
		return 0, false
	}
	return generation, true
}

// assertNoUncommittedFinalPublished is CORRECTION 1's strong negative
// (oracle correction 2026-10-03): no `<child>:<generation>:final` entry may
// appear on the parent's REAL inbox or the delivered wake seam unless the
// lifecycle journal holds the matching committed terminal+outbox result for
// that exact generation — D2: "Publish only a committed outbox"; D8.9: a
// restart stop that lands before the commit lands "with no final id
// published". This closes the hole a committed-descriptor-only count leaves
// open: an UNCOMMITTED generation's final in the actual inbox/frame/wake
// seam is invisible to ListPendingFinalDeliveries, because no commit was
// ever minted for it. A genuine committed G+1 final is allowed — the
// witness is the store's own outbox descriptor joined by generation, and
// the observed bytes are hash-checked against its PayloadHash (D2: a
// same-id/different-payload collision is a visible consistency error,
// never an acknowledgment or silent suppression). fixtureCommitted pins
// the generations this test fixture itself committed, so generation 1's
// bytes are guarded even if the pending scan drifted; the expected id and
// hashes derive from the ADR and the fixture's commit, never from what the
// boot happened to emit.
func assertNoUncommittedFinalPublished(t *testing.T, h *bootRecoveryHarness, child, seam string, observed []generated.SessionMessage, fixtureCommitted map[int]session.FinalDeliveryCommit) {
	t.Helper()
	pending, err := h.lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries (phantom cross-check on %s): %v", seam, err)
	}
	backed := make(map[int]session.FinalDeliveryCommit, len(pending)+len(fixtureCommitted))
	for _, entry := range pending {
		if entry.SessionID == child {
			backed[entry.Generation] = entry.Commit
		}
	}
	for generation, commit := range fixtureCommitted {
		backed[generation] = commit
	}
	for _, message := range observed {
		envelope := bootEnvelope(t, message)
		generation, isFinal := allGenFinalGeneration(child, envelope.MessageID)
		if !isFinal {
			continue
		}
		commit, committed := backed[generation]
		if !committed {
			t.Fatalf("%s published %q with NO committed lifecycle+outbox result for generation %d (committed generations for %s: %d pending entries) — D2: publish only a committed outbox, an uncommitted record is stopped; D8.9: before the commit a restart lands with no final id published",
				seam, envelope.MessageID, generation, child, len(pending))
		}
		got, marshalErr := message.MarshalJSON()
		if marshalErr != nil {
			t.Fatalf("re-encode %s final %s: %v", seam, envelope.MessageID, marshalErr)
		}
		if hash := fmt.Sprintf("%x", sha256.Sum256(got)); hash != commit.PayloadHash {
			t.Fatalf("%s final %s bytes do not hash to its committed payload (published %s, committed %s) — D2: a same-id/different-payload collision is a visible consistency error, never an acknowledgment",
				seam, envelope.MessageID, hash, commit.PayloadHash)
		}
	}
}

// TestBootAllGen_CommittedFinalPublishedAtBootDespiteNewerGeneration — the
// W3a behavioural RED: G is committed (done + exact upward outbox, never
// published — the crash-before-append cut), an explicit RESUME has already
// created working G+1, and at boot the pending committed G final must be
// discovered and delivered to the direct parent exactly once, byte-identical,
// with its durable delivery facts recorded through the delivery-only writer —
// while the tail stays G+1, unmutated by G's retry (D2/D8.1/D8.5/D8.9, T11).
func TestBootAllGen_CommittedFinalPublishedAtBootDespiteNewerGeneration(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	commitID := "commit-g1-crash-before-append"
	finalID, payload, payloadHash := commitAllGenDoneFinal(t, h, child, parent, commitID, "result committed before the restart")
	resumeToGPlusOne(t, h, child)

	pending, err := h.lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries (fixture control): %v", err)
	}
	if len(pending) != 1 || pending[0].Generation != 1 || !pending[0].Pending() {
		t.Fatalf("fixture control before boot: pending finals = %+v, want exactly G's unpublished commit", pending)
	}

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run: %v", err)
	}

	// CORRECTION 1 (oracle correction 2026-10-03), checked FIRST so the red
	// names the phantom and not only the missing G publication: the REAL
	// parent inbox and the delivered wake seam must hold no
	// <child>:<generation>:final entry that no committed outbox backs — not
	// even alongside a correctly published G. The deliverer's event counter
	// alone is not receipt proof; the inbox read below is the same durable
	// store the parent reads, cross-checked against the lifecycle outbox.
	inboxMessages, _, _, err := h.inbox.Drain(parent, child, "", 200)
	if err != nil {
		t.Fatalf("Drain(parent inbox): %v", err)
	}
	committedFixture := map[int]session.FinalDeliveryCommit{1: {
		Generation: 1, CommitID: commitID, MessageID: finalID, PayloadHash: payloadHash,
	}}
	assertNoUncommittedFinalPublished(t, h, child, "parent inbox", inboxMessages, committedFixture)
	woken := make([]generated.SessionMessage, 0, len(h.deliverer.snapshot()))
	for _, event := range h.deliverer.snapshot() {
		woken = append(woken, event.Message)
	}
	assertNoUncommittedFinalPublished(t, h, child, "delivered wake seam", woken, committedFixture)

	// D8.1/D8.9: the committed G final reaches the direct parent's durable
	// inbox under <child>:G:final — exactly once, byte-identical to the
	// committed payload.
	messages := inboxMessages
	if err != nil {
		t.Fatalf("Drain(parent inbox): %v", err)
	}
	published := 0
	for _, message := range messages {
		if bootEnvelope(t, message).MessageID != finalID {
			continue
		}
		published++
		got, marshalErr := message.MarshalJSON()
		if marshalErr != nil {
			t.Fatalf("re-encode published final %s: %v", finalID, marshalErr)
		}
		if string(got) != string(payload) {
			t.Fatalf("published final %s bytes diverge from the committed payload — D2: the exact committed upward message is published (committed %d bytes, published %d bytes)",
				finalID, len(payload), len(got))
		}
	}
	if published == 0 {
		ids := make([]string, 0, len(messages))
		for _, message := range messages {
			ids = append(ids, bootEnvelope(t, message).MessageID)
		}
		t.Fatalf("boot published no %s entry: parent inbox ids = %v — D8.1: boot retries publication of every pending committed final even if the tail is G+1", finalID, ids)
	}
	if published != 1 {
		t.Fatalf("parent inbox holds %d entries for %s, want exactly 1 — D2: publish once, the replay id is the duplicate guard", published, finalID)
	}

	// D2: the delivery facts are durable, recorded through the one legal
	// post-terminal writer — never a terminal mutation, never merely implicit.
	progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState(G): %v", err)
	}
	if retired {
		t.Fatalf("G's payload retired at a boot that never published it — retirement requires the durable delivery prerequisites (D2)")
	}
	if revision < 1 {
		t.Fatalf("no delivery envelope was journaled for G's commit (revision %d) — boot must record progress via LifecycleStore.UpdateFinalDelivery (D2)", revision)
	}
	if !progress.InboxAppended || !progress.WakeRecorded {
		t.Fatalf("delivery progress after boot = %+v, want inbox_appended and wake_recorded recorded via UpdateFinalDelivery (D2)", progress)
	}

	// T11 W3a slice: exactly the G commit remains discoverable — no final was
	// minted or published for the uncommitted working G+1.
	pendingAfter, err := h.lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries after boot: %v", err)
	}
	if len(pendingAfter) != 1 {
		t.Fatalf("pending finals after boot = %d entries (%+v), want exactly G's commit — an uncommitted G+1 final must never exist (D2/D8.9)", len(pendingAfter), pendingAfter)
	}
	if pendingAfter[0].Generation != 1 || pendingAfter[0].Commit.CommitID != commitID || pendingAfter[0].Commit.PayloadHash != payloadHash {
		t.Fatalf("pending final after boot = generation %d commit %q hash %q, want G's exact commit identity",
			pendingAfter[0].Generation, pendingAfter[0].Commit.CommitID, pendingAfter[0].Commit.PayloadHash)
	}

	// D2/D8.5: G's retry never rewrote the newer generation — the tail is
	// still G+1, and no inbox final promoted it to done.
	tail, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load(child) after boot: %v", err)
	}
	if tail.Generation != 2 {
		t.Fatalf("tail generation after boot = %d, want 2 — updating G's delivery metadata must never change G+1's identity (D2)", tail.Generation)
	}
	// D8.9: "Either way the session must never be `working` by itself after
	// the reboot." The exact landing — stopped with a cause:restart note,
	// boot_seq and stop_seq — is W3b's pack (W2a/W2b dependencies); this
	// pack does not adjudicate it and does NOT read this branch's legacy
	// failed(interrupted) outcome as the intended boot result. A completed
	// tail stays the promotion defect the next check pins.
	if tail.State == session.LifecycleRunning || tail.State == session.LifecycleQueued {
		t.Fatalf("G+1 tail after boot = %q — the session must never be working by itself after the reboot (D8.9); a boot stops an uncommitted current run without executing it", tail.State)
	}
	if tail.State == session.LifecycleCompleted {
		t.Fatalf("G+1 landed completed at a boot that only retried G's committed final — an old generation's delivery must not promote the newer generation (D2/D8.5)")
	}
	if tail.ResumedFrom != child {
		t.Fatalf("positive control lost: G+1 ResumedFrom = %q, want %q — the RESUME mint must survive the boot untouched", tail.ResumedFrom, child)
	}
}

// commitAllGenFailedFinal is the failed-outcome twin of commitAllGenDoneFinal:
// generation 1 failed (real error reason) + the protected unpublished error
// outbox, in ONE real LifecycleStore.Mutate, left unpublished.
func commitAllGenFailedFinal(t *testing.T, h *bootRecoveryHarness, child, parent, commitID, failureReason string) (finalID string, payload []byte, payloadHash string) {
	t.Helper()
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))

	gen := 1
	var built generated.SessionMessage
	err := built.FromSessionMessageError(generated.SessionMessageError{
		MessageId: child, SessionId: child, ParentSessionId: &parent,
		CreatedAt: time.Now().UTC(), Depth: 1, Direction: "child_to_parent",
		Generation: &gen, Kind: "error", Fatal: true, Text: failureReason,
		SenderIdentity: "agent-1", UntrustedOrigin: true,
	})
	if err != nil {
		t.Fatalf("build committed error final: %v", err)
	}
	finalID = allGenFinalID(child)
	stamped, err := withDeterministicMessageID(built, finalID)
	if err != nil {
		t.Fatalf("stamp committed final id: %v", err)
	}
	payload, err = stamped.MarshalJSON()
	if err != nil {
		t.Fatalf("encode committed final: %v", err)
	}
	payloadHash = fmt.Sprintf("%x", sha256.Sum256(payload))

	if err := h.lifecycle.Mutate(child, func(cur *session.LifecycleRecord) error {
		if cur == nil || cur.Generation != 1 {
			return fmt.Errorf("fixture: generation 1 record not current (cur=%v)", cur)
		}
		cur.State = session.LifecycleFailed
		cur.FailedReason = failureReason
		cur.NeedsInput = nil
		cur.FinalDelivery = &session.FinalDeliveryCommit{
			Generation: 1, CommitID: commitID, MessageID: finalID,
			Outcome: string(steer.OutcomeFailed), ParentSessionID: parent,
			PayloadHash: payloadHash, Payload: payload,
		}
		return nil
	}); err != nil {
		t.Fatalf("commit G failed+outbox in one Mutate: %v", err)
	}
	return finalID, payload, payloadHash
}

// wakeCountForID counts the deliverer's wake events for one message id.
func wakeCountForID(t *testing.T, h *bootRecoveryHarness, id string) int {
	t.Helper()
	count := 0
	for _, event := range h.deliverer.snapshot() {
		if bootEnvelope(t, event.Message).MessageID == id {
			count++
		}
	}
	return count
}

// TestBootAllGen_RepeatedBootNoDuplicateFinalOrWakeAfterAck — repeat
// boot/reopen over the same concrete stores: after the committed G final was
// published once and its parent inbox id durably acknowledged, a second boot
// must neither publish nor wake it again, and must record the acknowledged
// receipt as a durable fact (D2: "A genuinely acknowledged matching id means
// this committed result was already consumed; do not send a second final";
// the ack_observed progress fact).
func TestBootAllGen_RepeatedBootNoDuplicateFinalOrWakeAfterAck(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	commitID := "commit-g1-restart-replay"
	finalID, payload, _ := commitAllGenDoneFinal(t, h, child, parent, commitID, "committed once, acked once")
	resumeToGPlusOne(t, h, child)

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("boot 1 Run: %v", err)
	}
	if wakeCountForID(t, h, finalID) != 1 {
		t.Fatalf("boot 1 woke %s %d times, want exactly 1 — the committed final delivers once (D2)", finalID, wakeCountForID(t, h, finalID))
	}
	messages, _, _, err := h.inbox.Drain(parent, child, "", 200)
	if err != nil {
		t.Fatalf("Drain after boot 1: %v", err)
	}
	found := false
	for _, message := range messages {
		if bootEnvelope(t, message).MessageID == finalID {
			found = true
			if got, marshalErr := message.MarshalJSON(); marshalErr != nil || string(got) != string(payload) {
				t.Fatalf("boot 1 published payload diverges from the committed bytes")
			}
		}
	}
	if !found {
		t.Fatalf("boot 1 published no %s entry — the committed final must be published before its acknowledgement can mean anything (D8.1)", finalID)
	}
	if err := h.inbox.Ack(parent, []string{finalID}); err != nil {
		t.Fatalf("Ack(%s): %v", finalID, err)
	}

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("boot 2 Run: %v", err)
	}
	if wakes := wakeCountForID(t, h, finalID); wakes != 1 {
		t.Fatalf("across two boots the committed final was woken %d times, want exactly 1 — a genuinely acknowledged id is consumed; no second final, no repeated wake (D2)", wakes)
	}
	progress, _, _, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState after boot 2: %v", err)
	}
	if !progress.AckObserved {
		t.Fatalf("ack_observed fact not recorded after the acknowledged final's repeat boot = %+v — boot must record the acknowledged receipt through UpdateFinalDelivery so the retry stops (D2)", progress)
	}
}

// TestBootAllGen_InboxFinalWithoutCommitPromotesNothingAtBoot — the negative
// control the brief fixes for W3a: a handback final sitting in the parent's
// inbox with NO matching committed lifecycle/outbox outcome must not promote
// the working record (D8.5: finishFromFinal is replaced by commit-based
// reconciliation, not an inbox-first promotion) and must not mint any
// outbox commit from mere inbox presence, so the record's eventual
// stopped(restart) transition stays possible (W3b lands that transition).
func TestBootAllGen_InboxFinalWithoutCommitPromotesNothingAtBoot(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	finalID := allGenFinalID(child)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))
	if _, err := h.inbox.Append(parent, bootHandback(t, child, parent, finalID)); err != nil {
		t.Fatalf("Append false final: %v", err)
	}

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	tail, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load after boot: %v", err)
	}
	if tail.State == session.LifecycleCompleted {
		t.Fatalf("an inbox final with NO committed lifecycle/outbox outcome promoted the running record to completed — D8.5: recovery retries only committed outbox entries, never promotes an inbox final without a commit")
	}
	if tail.Generation != 1 {
		t.Fatalf("tail generation after boot = %d, want 1 — reconciliation from a phantom final must not mint a newer generation", tail.Generation)
	}
	pending, err := h.lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries after boot: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending finals after boot = %+v, want none — a commit may never be minted from inbox presence alone (D2: delivery envelopes join only their matching protected commit)", pending)
	}
}

// TestBootAllGen_CommittedFailedFinalPublishedAtBoot — the failed twin of the
// primary RED: a committed failed G with an unpublished fatal-error outbox is
// discovered and published byte-identically at boot despite working G+1, with
// the same delivery-only progress and no G+1 final (D2/D8.1).
func TestBootAllGen_CommittedFailedFinalPublishedAtBoot(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	commitID := "commit-g1-failed-crash-before-append"
	finalID, payload, payloadHash := commitAllGenFailedFinal(t, h, child, parent, commitID, "real: failure committed before the restart")
	resumeToGPlusOne(t, h, child)

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// CORRECTION 1 (oracle correction 2026-10-03): the failed twin carries
	// the same strong negative — a committed failed G never licenses a
	// phantom <child>:G+1:final on the real inbox or the wake seam.
	messages, _, _, err := h.inbox.Drain(parent, child, "", 200)
	if err != nil {
		t.Fatalf("Drain(parent inbox): %v", err)
	}
	committedFixture := map[int]session.FinalDeliveryCommit{1: {
		Generation: 1, CommitID: commitID, MessageID: finalID, PayloadHash: payloadHash,
	}}
	assertNoUncommittedFinalPublished(t, h, child, "parent inbox", messages, committedFixture)
	woken := make([]generated.SessionMessage, 0, len(h.deliverer.snapshot()))
	for _, event := range h.deliverer.snapshot() {
		woken = append(woken, event.Message)
	}
	assertNoUncommittedFinalPublished(t, h, child, "delivered wake seam", woken, committedFixture)
	published := 0
	for _, message := range messages {
		if bootEnvelope(t, message).MessageID != finalID {
			continue
		}
		published++
		got, marshalErr := message.MarshalJSON()
		if marshalErr != nil {
			t.Fatalf("re-encode published final %s: %v", finalID, marshalErr)
		}
		if string(got) != string(payload) {
			t.Fatalf("published failed final %s bytes diverge from the committed payload — D2: the exact committed upward message is published", finalID)
		}
	}
	if published != 1 {
		t.Fatalf("parent inbox holds %d entries for failed %s, want exactly 1 — D8.1: a committed failed final is pending/retryable exactly like a done one", published, finalID)
	}
	progress, revision, _, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState(G failed): %v", err)
	}
	if revision < 1 || !progress.InboxAppended || !progress.WakeRecorded {
		t.Fatalf("delivery progress for the committed failed final = %+v revision %d, want durable inbox_appended + wake_recorded via UpdateFinalDelivery (D2)", progress, revision)
	}
	pending, err := h.lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries after boot: %v", err)
	}
	if len(pending) != 1 || pending[0].Generation != 1 {
		t.Fatalf("pending finals after boot = %+v, want exactly the failed G commit — no uncommitted G+1 final may exist", pending)
	}
	tail, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load(child) after boot: %v", err)
	}
	if tail.Generation != 2 || tail.State == session.LifecycleCompleted {
		t.Fatalf("tail after boot = gen %d state %q, want gen 2 and not completed — G's failed delivery must not promote or rewrite G+1 (D2)", tail.Generation, tail.State)
	}
}

// TestBootAllGen_DeliveryOnlyPublishLeavesGPlusOneUntouched — CORRECTION 2
// (oracle correction 2026-10-03, dispatcher-refined): isolate the
// DELIVERY-only phase from the whole boot, on the pass the boot ACTUALLY
// consumes. The boot-side delivery business pass does not exist on this
// branch (boot_sweep.go::SteerBootRecovery has no ListPendingFinalDeliveries
// caller), so this subassertion is an explicit, loud BLOCKED pending the W3a
// backend API — never t.Skip, never a test-only seam, never a fake branch.
// Driving the live-completion publisher
// (steer_completion_commit.go::AgentLoop.publishCommittedFinal) directly
// from here would test the wrong path: nothing proves the boot consumer
// calls it.
//
// Requested production business method (backend to add as NORMAL used code,
// called by SteerBootRecovery.Run's D8.1 pass two; the test then drives the
// SAME method once, on this merged test branch):
//
//	func (r *SteerBootRecovery) publishPendingCommittedFinals(ctx context.Context) error
//
// Contract: for every ListPendingFinalDeliveries entry, publish the exact
// committed payload to the direct parent's real inbox under the replay id,
// record durable facts through LifecycleStore.UpdateFinalDelivery only, and
// mutate NO LifecycleRecord (D2 "Publish only a committed outbox"; D8.1
// pass two; D8.9 "without changing that generation's outcome").
//
// When it lands, THIS test pins the isolation oracle: G committed (done +
// outbox, unpublished), explicit RESUME on G+1, one call of the method —
// then (a) the G final sits on the real parent inbox exactly once,
// byte-identical to the committed payload; (b) FinalDeliveryState shows
// inbox_appended+wake_recorded via UpdateFinalDelivery, payload not retired;
// (c) G+1's identity/state tuple is unchanged — generation, state,
// resumed_from, failed_reason, Stop fence, StopNote, NeedsInput, FinalDelivery
// (D8.5: "updating G's delivery metadata never changes G+1's state or
// identity"). The D2 execution-identity seam (run_id/boot_seq on the
// record) is not landed on this branch — FinalDeliveryCommit's doc comment:
// commit_id is a stand-in for run_id — so the tuple asserts every identity
// field the record carries today; W2b's identity fields join it when they
// exist. The whole boot's OWN effect on G+1 — D8's stopped(restart) landing
// with cause note, boot_seq and stop_seq — is another pass and W3b's pack;
// the primary test's D8.9 "never working after the reboot" bound stays the
// only whole-boot tail claim this pack makes.
func TestBootAllGen_DeliveryOnlyPublishLeavesGPlusOneUntouched(t *testing.T) {
	t.Fatal("BLOCKED: the W3a boot delivery-only pass does not exist on this branch — required production business method SteerBootRecovery.publishPendingCommittedFinals(ctx) (called by SteerBootRecovery.Run's D8.1 pass two, delivery-only via LifecycleStore.UpdateFinalDelivery, no LifecycleRecord mutation) is not implemented; required by ADR-20260928 D2 'Publish only a committed outbox', D8.1, D8.5, D8.9 and T11's W3a slice. The G+1 pre/post identity-state isolation oracle is specified in this test's doc comment and rides on that method — no test-only seam will stand in for it")
}
