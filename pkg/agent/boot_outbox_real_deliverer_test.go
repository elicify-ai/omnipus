// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// boot_outbox_real_deliverer_test.go — W3 RED round 2 (fresh-context
// qa-lead), ADR-20260928 sub-agent control plane (frozen asset cd20cf8b).
// The first W3a pack (boot_outbox_allgen_bootred_test.go) drove the boot
// delivery pass through the shared bootRecordingDeliverer seam, which
// appends to the real parent inbox but skips the production
// SteerUpwardDeliverer's own generation guard — so its local greens are not
// a real-delivery proof. THIS pack wires the PRODUCTION deliverer exactly as
// gateway_boot.go does (NewSteerUpwardDeliverer + AgentLoop.SetSteerAudienceDeps
// + AgentLoop.SetSessionMessagingStores, all pointed at the same real
// LifecycleStore/MessageInboxStore the fixture committed into) and then
// demands the spec outcomes through it.
//
// Test plan (elicify-test-writing step 1)
//
// Behaviour under test: SteerBootRecovery.runFinalDeliveryPass — the
// production-used delivery-only seam (boot_final_delivery.go) — publishes a
// committed terminal G final exactly once byte-identically through the real
// SteerUpwardDeliverer (including when an explicit RESUME has moved the
// tail to a working G+1), never consumes a committed final on a stray
// acknowledgement recorded against an inbox that never held the message,
// and refuses a commit whose stored payload bytes no longer match its
// protected payload_hash visibly, before any deliverer call or inbox append.
//
// Specification source (every oracle; nothing below is derived from
// observed behaviour of the code under test):
//
//   - D2, "Publish only a committed outbox": append the exact committed
//     message to the direct parent's inbox with the deterministic id; "a
//     genuinely acknowledged matching id means this committed result was
//     already consumed"; a same-id/different-payload collision is a visible
//     consistency error, never an acknowledgment or silent suppression;
//     failures retain a retryable outbox and surface the failure.
//   - D2, "Protected commit and allowed metadata": payload_hash identifies
//     the exact stored upward envelope; facts are recorded on real receipts
//     only. A corrupt/mismatching envelope "is a visible consistency error
//     and cannot authorize publication or retirement".
//   - D2, "Discovery after a newer-generation RESUME": all-generation
//     discovery; explicit RESUME of committed G "neither copies G's outbox
//     into G+1 nor hides, supersedes, acknowledges or retires it"; retry
//     happens independently of the current execution.
//   - D8.1: boot pass two "retries publication of every pending committed
//     final, even if its session's current tail is G+1".
//
// Unit boundary — REAL: LifecycleStore (journal, Mutate, UpdateFinalDelivery,
// FinalDeliveryState, ListPendingFinalDeliveries), MessageInboxStore
// (Append/Entries/AckDetailed), UnifiedStore, SteerRecordClassifier,
// SteerCanceller.Revive (the production RESUME mint), the production
// SteerUpwardDeliverer with its real AgentLoop back-wire, and
// SteerBootRecovery.runFinalDeliveryPass. SEAMS (process edges only): the
// LLM provider (scenario fake, never invoked — no turn runs) and the wake
// transport (asyncNotifier absent → stored-not-woken; wake outcomes are
// therefore NOT asserted — delivery-only facts are). The committed G fixture
// is the first W3a pack's real-Mutate helpers, reused read-only.
//
// Case table:
//
//  1. Positive control (instrument, expected GREEN on the pinned tree):
//     committed G at the current tail delivers once, byte-identical, facts
//     recorded, no notices — proves the production deliverer is wired and
//     the instrument can see a delivery. If THIS fails, the RED cases below
//     are not interpretable (setup problem, never mislabelled as behaviour).
//  2. RED case A: committed G + explicit RESUME to working G+1 — the pass
//     must still publish G exactly once through the real deliverer and leave
//     the G+1 tail record byte-identical.
//  3. RED case B: committed G, then AckDetailed on the empty inbox — the
//     pass must publish once OR refuse visibly and stay retryable; it must
//     never stamp ack_observed without a real append (the open policy pick
//     between the two admissible branches goes to the founder; the test
//     asserts the invariant both readings share).
//  4. RED case C: committed G, payload bytes corrupted on disk under an
//     untouched payload_hash — the pass must refuse visibly BEFORE any
//     deliverer call or inbox append and keep the commit pending.
//
// What would break these tests (CHECK mutations — deferred to CHECK, not
// run in RED): narrowing discovery to the tail; publishing bytes that
// differ from the commit; consuming on a stray ack; skipping the hash check;
// writing the tail record from the delivery pass.
//
// Known gaps (deliberate): FramesPersisted is never recorded by the pinned
// tree's delivery pass (audited and reported separately — no fake boolean
// fact is asserted here); W3b's automatic boot stop, D6 notices, payload
// retirement/compaction and the T11 crash-interval matrix stay in their own
// packs; case C keeps the tail at G because a working G+1 would refuse via
// the generation guard for an unrelated reason and mask the missing
// payload-integrity check.

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// newRealDelivererLoopFixture wires a real AgentLoop exactly as the gateway
// boot does (gateway_boot.go: deliverer := NewSteerUpwardDeliverer();
// SetSessionMessagingStores(inbox, lifecycle); SetSteerAudienceDeps(resolver,
// observer, deliverer)) — pointed at the borrowed boot-recovery harness's
// real stores, so the deliverer the recovery pass uses is the PRODUCTION
// SteerUpwardDeliverer driving the same LifecycleStore/MessageInboxStore the
// fixture committed into. No fake deliverer, no manual dispatch.
func newRealDelivererLoopFixture(t *testing.T, h *bootRecoveryHarness) (*AgentLoop, *SteerUpwardDeliverer) {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "agent-1", Home: home}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)
	al.SetSessionMessagingStores(h.inbox, h.lifecycle)
	deliverer := NewSteerUpwardDeliverer()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(h.lifecycle, h.sessions)),
		steer.NopBoundaryObserver{},
		deliverer,
	)
	if al.GetMessageInboxStore() != h.inbox {
		t.Fatal("fixture control: the loop's inbox store is not the harness inbox store")
	}
	if al.GetSessionLifecycleStore() != h.lifecycle {
		t.Fatal("fixture control: the loop's lifecycle store is not the harness lifecycle store")
	}
	return al, deliverer
}

// runRealDelivererPass invokes the production-used delivery-only seam
// (boot_final_delivery.go::runFinalDeliveryPass — the whole delivery half of
// boot) and returns every operator notice emitted plus the scan-level error.
func runRealDelivererPass(
	t *testing.T,
	h *bootRecoveryHarness,
	al *AgentLoop,
	deliverer *SteerUpwardDeliverer,
) ([]string, error) {
	t.Helper()
	notices := []string{}
	recovery := &SteerBootRecovery{
		Lifecycle:  h.lifecycle,
		Sessions:   h.sessions,
		Inbox:      al.GetMessageInboxStore(),
		Classifier: NewSteerRecordClassifier(h.lifecycle, h.sessions),
		Deliverer:  deliverer,
		OperatorNotice: func(message string) {
			notices = append(notices, message)
		},
	}
	return notices, recovery.runFinalDeliveryPass(context.Background())
}

// realDelivererInboxMessages returns id→bytes for every MESSAGE entry
// durably present under ownerKey — the whole-file read
// inboxFinalSighting reconciles against, acked entries included.
func realDelivererInboxMessages(t *testing.T, inbox *session.MessageInboxStore, ownerKey string) map[string]string {
	t.Helper()
	entries, err := inbox.Entries(ownerKey)
	if err != nil {
		t.Fatalf("Entries(%s): %v", ownerKey, err)
	}
	messages := map[string]string{}
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		envelope := bootEnvelope(t, *entry.Message)
		raw, err := entry.Message.MarshalJSON()
		if err != nil {
			t.Fatalf("re-encode stored message %s: %v", envelope.MessageID, err)
		}
		messages[envelope.MessageID] = string(raw)
	}
	return messages
}

// realDelivererMessageIDs is the deterministic id list for failure messages.
func realDelivererMessageIDs(messages map[string]string) []string {
	ids := make([]string, 0, len(messages))
	for id := range messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// realDelivererRecordSnapshot is the byte snapshot of a session's current
// tail LifecycleRecord: the delivery-only pass may append delivery envelopes,
// but must never change the tail record's bytes (D2: the only writer is the
// delivery-only UpdateFinalDelivery, never a LifecycleRecord).
func realDelivererRecordSnapshot(t *testing.T, lifecycle *session.LifecycleStore, sessionID string) []byte {
	t.Helper()
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		t.Fatalf("Load(%s): %v", sessionID, err)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("snapshot record %s: %v", sessionID, err)
	}
	return raw
}

// TestBootRealDeliverer_PositiveControl_CommittedGAtTailDeliversOnce — the
// instrument check: with the committed generation still the current tail,
// the production deliverer (wired through the normal loop setters) publishes
// the committed final exactly once, byte-identical, with durable facts and
// no refusal noise. A failure HERE means the harness or wiring is broken —
// never a behavioural RED for the cases below.
func TestBootRealDeliverer_PositiveControl_CommittedGAtTailDeliversOnce(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	commitID := "commit-g1-real-deliverer-positive"
	finalID, payload, _ := commitAllGenDoneFinal(t, h, child, parent, commitID,
		"committed, never published: the tail is still this generation")
	al, deliverer := newRealDelivererLoopFixture(t, h)
	tailBefore := realDelivererRecordSnapshot(t, h.lifecycle, child)

	notices, runErr := runRealDelivererPass(t, h, al, deliverer)
	if runErr != nil {
		t.Fatalf("runFinalDeliveryPass: %v", runErr)
	}

	messages := realDelivererInboxMessages(t, h.inbox, parent)
	got, ok := messages[finalID]
	if !ok {
		t.Fatalf("the committed final %s never reached the parent's real inbox through the production deliverer (ids %v; notices %q) — D2: the exact committed message is appended to the direct parent's inbox; if the deliverer never ran, this is a wiring failure, not a pass verdict",
			finalID, realDelivererMessageIDs(messages), strings.Join(notices, "\n"))
	}
	if got != string(payload) {
		t.Fatalf("published final %s bytes diverge from the committed payload — D2: the exact committed upward message is published", finalID)
	}
	if len(messages) != 1 {
		t.Fatalf("parent inbox holds %d message entries (%v), want exactly the one committed final — publish once, the replay id is the duplicate guard (D2)",
			len(messages), realDelivererMessageIDs(messages))
	}
	progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState(G): %v", err)
	}
	if retired || revision < 1 || !progress.InboxAppended {
		t.Fatalf("delivery facts after the pass = %+v revision %d retired %v — the pass must record the durable inbox append through UpdateFinalDelivery (D2)",
			progress, revision, retired)
	}
	tailAfter := realDelivererRecordSnapshot(t, h.lifecycle, child)
	if string(tailAfter) != string(tailBefore) {
		t.Fatal("the delivery pass mutated the tail record — delivery-only means no LifecycleRecord write (D2)")
	}
	if len(notices) != 0 {
		t.Fatalf("a healthy committed final delivered with operator notices %q — notices are the failure/refusal surface (D2), a clean delivery emits none", notices)
	}
}

// TestBootRealDeliverer_ResumedGPlusOne_PublishesCommittedGThroughRealDeliverer —
// RED case A: the committed final of generation G must still be published
// through the PRODUCTION deliverer after an explicit RESUME minted a working
// G+1 — D8.1 pass two retries "every pending committed final, even if its
// session's current tail is G+1"; D2: the RESUME "neither copies G's outbox
// into G+1 nor hides, supersedes, acknowledges or retires it", and G's
// delivery facts advance delivery-only while the G+1 tail record stays
// byte-identical.
func TestBootRealDeliverer_ResumedGPlusOne_PublishesCommittedGThroughRealDeliverer(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	commitID := "commit-g1-real-deliverer-gplus1"
	finalID, payload, _ := commitAllGenDoneFinal(t, h, child, parent, commitID,
		"committed before the restart; the RESUME already minted a working G+1")
	resumeToGPlusOne(t, h, child)
	al, deliverer := newRealDelivererLoopFixture(t, h)
	tailBefore := realDelivererRecordSnapshot(t, h.lifecycle, child)

	pending, err := h.lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries (fixture control): %v", err)
	}
	if len(pending) != 1 || pending[0].Generation != 1 || !pending[0].Pending() {
		t.Fatalf("fixture control before the pass: pending finals = %+v, want exactly G's unpublished commit discoverable across all generations (D2)", pending)
	}

	notices, runErr := runRealDelivererPass(t, h, al, deliverer)
	if runErr != nil {
		t.Fatalf("runFinalDeliveryPass: %v", runErr)
	}
	for _, n := range notices {
		t.Logf("operator notice: %s", n)
	}

	messages := realDelivererInboxMessages(t, h.inbox, parent)
	got, ok := messages[finalID]
	if !ok {
		t.Fatalf("the committed final %s never reached the parent's real inbox through the production deliverer (ids %v; notices %d) — D8.1: boot retries publication of every pending committed final even if the tail is G+1; D2: the RESUME neither hides nor supersedes G's outbox",
			finalID, realDelivererMessageIDs(messages), len(notices))
	}
	if got != string(payload) {
		t.Fatalf("published final %s bytes diverge from the committed payload — D2: the exact committed upward message is published", finalID)
	}
	if len(messages) != 1 {
		t.Fatalf("parent inbox holds %d message entries (%v), want exactly the one committed final — publish once (D2)",
			len(messages), realDelivererMessageIDs(messages))
	}
	progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState(G): %v", err)
	}
	if retired || revision < 1 || !progress.InboxAppended {
		t.Fatalf("G delivery facts after the pass = %+v revision %d retired %v — the pass must record the durable inbox append through the delivery-only writer (D2)",
			progress, revision, retired)
	}
	tailAfter := realDelivererRecordSnapshot(t, h.lifecycle, child)
	if string(tailAfter) != string(tailBefore) {
		t.Fatal("G's delivery retry mutated the G+1 tail record — D2: UpdateFinalDelivery may advance G after the resume without changing G+1's state or identity")
	}
}

// TestBootRealDeliverer_StrayAckWithoutMessage_MustNotConsumeCommitSilently —
// RED case B: MessageInboxStore.AckDetailed durably records an Ack entry even
// for an id that was never appended (and reports it as Unknown). A committed
// final whose replay id carries such a stray ack has NOT been "genuinely
// acknowledged" (D2: acknowledgment consumes a committed result only when a
// matching delivered id was consumed). The pass must therefore publish the
// exact committed final once — the admissible policy branch A — or refuse
// visibly and keep the commit pending/retryable — admissible branch B — and
// under BOTH branches it must never stamp ack_observed without a durable
// inbox append. Which of the two admissible branches is the product policy
// is an open founder question; the shared invariant below fails on either.
func TestBootRealDeliverer_StrayAckWithoutMessage_MustNotConsumeCommitSilently(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	commitID := "commit-g1-stray-ack"
	finalID, payload, _ := commitAllGenDoneFinal(t, h, child, parent, commitID,
		"committed; no message was ever appended to the parent's inbox")
	al, deliverer := newRealDelivererLoopFixture(t, h)

	// Stray-ack state through the REAL store API on an EMPTY inbox. These
	// are instrument checks on the store's documented M1 behaviour.
	ackRes, err := h.inbox.AckDetailed(parent, []string{finalID})
	if err != nil {
		t.Fatalf("AckDetailed(%s) on an empty inbox: %v", finalID, err)
	}
	if !slices.Contains(ackRes.Unknown, finalID) {
		t.Fatalf("fixture control: stray ack reported Unknown=%v, want %q listed — the store itself knows no message with this id exists", ackRes.Unknown, finalID)
	}
	entries, err := h.inbox.Entries(parent)
	if err != nil {
		t.Fatalf("Entries(parent): %v", err)
	}
	ackEntries, messageEntries := 0, 0
	for _, entry := range entries {
		switch entry.Kind {
		case session.InboxEntryAck:
			ackEntries++
			if !slices.Contains(entry.AckedIDs, finalID) {
				t.Fatalf("fixture control: ack entry covers %v, want %q", entry.AckedIDs, finalID)
			}
		case session.InboxEntryMessage:
			messageEntries++
		}
	}
	if ackEntries != 1 || messageEntries != 0 {
		t.Fatalf("fixture control: parent inbox = %d ack / %d message entries, want 1 ack / 0 messages — the stray-ack state was not reached", ackEntries, messageEntries)
	}

	notices, runErr := runRealDelivererPass(t, h, al, deliverer)
	for _, n := range notices {
		t.Logf("operator notice: %s", n)
	}
	if runErr != nil {
		t.Logf("runFinalDeliveryPass scan error: %v", runErr)
	}

	messages := realDelivererInboxMessages(t, h.inbox, parent)
	progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState(G): %v", err)
	}
	// The invariant both admissible policy branches share.
	if !progress.InboxAppended && progress.AckObserved {
		t.Fatalf("the pass stamped ack_observed with NO durable inbox append — D2: a genuinely acknowledged matching id consumes the commit only when the id was really delivered; progress = %+v revision %d — the stray ack was silently consumed",
			progress, revision)
	}
	if got, ok := messages[finalID]; ok {
		// Admissible branch A: publish the committed final once.
		if got != string(payload) {
			t.Fatalf("published final %s bytes diverge from the committed payload — D2: the exact committed upward message is published", finalID)
		}
		if !progress.InboxAppended || revision < 1 {
			t.Fatalf("the final was published but the delivery facts were not recorded (%+v revision %d) — D2 requires the durable receipt via UpdateFinalDelivery", progress, revision)
		}
		return
	}
	// Admissible branch B: a visible consistency error, commit stays retryable.
	joined := strings.Join(notices, "\n")
	visiblyRefused := strings.Contains(joined, finalID) || (runErr != nil && strings.Contains(runErr.Error(), finalID))
	if !visiblyRefused {
		t.Fatalf("the pass neither published %s (inbox ids %v) nor refused visibly (notices %q, scan error %v) while the stray ack stands — D2: an unacknowledged-in-truth commit is published once or held retryable behind a visible consistency error, never silently absorbed",
			finalID, realDelivererMessageIDs(messages), joined, runErr)
	}
	if retired {
		t.Fatal("G's payload was retired on a pass that never appended the message — retirement requires the durable delivery prerequisites (D2)")
	}
	pendingAfter, err := h.lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries after the pass: %v", err)
	}
	stillPending := false
	for _, item := range pendingAfter {
		if item.Generation == 1 && item.Commit.CommitID == commitID && item.Pending() {
			stillPending = true
		}
	}
	if !stillPending {
		t.Fatalf("the commit is no longer pending/retryable after the visible refusal (pending %d entries) — the outbox entry stays retryable, never consumed by a stray ack (D2)", len(pendingAfter))
	}
}

// realDelivererCorruptCommittedPayload corrupts the committed final's exact
// payload bytes IN THE PERSISTED JOURNAL FILE, leaving the protected
// payload_hash untouched — the file-level fault real bit-rot/storage damage
// produces, through normal file operations only (no production hook, no fake
// commit writer). Returns the corrupted payload bytes.
func realDelivererCorruptCommittedPayload(t *testing.T, h *bootRecoveryHarness, child, corruptedResult string) []byte {
	t.Helper()
	journalPath := filepath.Join(h.lifecycle.Dir(), child+".jsonl")
	raw, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read lifecycle journal %s: %v", journalPath, err)
	}
	lines := strings.Split(string(raw), "\n")
	corrupted := []byte(nil)
	touched := false
	for i, line := range lines {
		if !strings.Contains(line, `"final_delivery"`) {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("journal line %d does not parse: %v", i, err)
		}
		commit, ok := record["final_delivery"].(map[string]any)
		if !ok {
			t.Fatalf("journal line %d carries no final_delivery object", i)
		}
		encoded, ok := commit["payload"].(string)
		if !ok {
			t.Fatalf("journal line %d commit carries no payload bytes", i)
		}
		original, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("commit payload is not base64: %v", err)
		}
		var message map[string]any
		if unmarshalErr := json.Unmarshal(original, &message); unmarshalErr != nil {
			t.Fatalf("committed payload does not parse: %v", unmarshalErr)
		}
		message["result_so_far"] = corruptedResult
		corrupted, err = json.Marshal(message)
		if err != nil {
			t.Fatalf("re-encode corrupted payload: %v", err)
		}
		commit["payload"] = base64.StdEncoding.EncodeToString(corrupted)
		replaced, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("re-encode journal line: %v", err)
		}
		lines[i] = string(replaced)
		touched = true
		break
	}
	if !touched {
		t.Fatalf("no committed final_delivery record found in %s — the corruption fixture could not be applied", journalPath)
	}
	if err := os.WriteFile(journalPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("write corrupted journal: %v", err)
	}
	return corrupted
}

// TestBootRealDeliverer_CorruptCommittedPayload_RefusedBeforeDelivery — RED
// case C: the committed payload's bytes are corrupted at rest under an
// untouched protected payload_hash (D2: payload_hash identifies the exact
// stored upward envelope; a corrupt/mismatching envelope "is a visible
// consistency error and cannot authorize publication"). The pass must refuse
// visibly BEFORE any deliverer call or inbox append, record no receipts, and
// keep the commit pending/retryable. The tail stays at G here on purpose:
// a working G+1 would refuse via the deliverer's generation guard for an
// unrelated reason and mask the missing payload-integrity check.
func TestBootRealDeliverer_CorruptCommittedPayload_RefusedBeforeDelivery(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)

	commitID := "commit-g1-corrupt-payload"
	finalID, payload, payloadHash := commitAllGenDoneFinal(t, h, child, parent, commitID,
		"the exact committed result")
	al, deliverer := newRealDelivererLoopFixture(t, h)

	corrupted := realDelivererCorruptCommittedPayload(t, h, child,
		"BIT-ROT: this text never came from the child's turn")

	// Instrument controls: the bytes really changed, still parse as the same
	// handback kind (so a decode refusal cannot be the reason they are
	// blocked), and their hash really differs from the protected hash.
	if string(corrupted) == string(payload) {
		t.Fatal("instrument: the corruption changed nothing — the fixture is broken")
	}
	var decoded generated.SessionMessage
	if err := decoded.UnmarshalJSON(corrupted); err != nil {
		t.Fatalf("instrument: corrupted payload no longer parses as a SessionMessage: %v", err)
	}
	if kind := bootEnvelope(t, decoded).Kind; kind != "handback" {
		t.Fatalf("instrument: the corruption changed the message kind to %q — corrupt the payload without changing its kind", kind)
	}
	if recomputed := fmt.Sprintf("%x", sha256.Sum256(corrupted)); recomputed == payloadHash {
		t.Fatal("instrument: the corrupted payload's hash equals the protected payload_hash — the fixture corrupted nothing")
	}
	pendingBefore, err := h.lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("ListPendingFinalDeliveries after corruption: %v", err)
	}
	hashStillProtected := false
	for _, item := range pendingBefore {
		if item.Generation == 1 && item.Commit.CommitID == commitID && item.Commit.PayloadHash == payloadHash {
			hashStillProtected = true
		}
	}
	if !hashStillProtected {
		t.Fatalf("instrument: the protected payload_hash no longer reads %q after the file-level corruption — only the payload bytes may change", payloadHash)
	}
	// Baseline AFTER the deliberate corruption, BEFORE the pass: a correct
	// refusal never rewrites the corrupt bytes, so the tail's pre-corruption
	// bytes can never come back — the snapshot the pass is held against is the
	// corrupted on-disk state it is invoked on. This proves the refusal itself
	// mutated neither the tail record nor the outbox (D2).
	tailBefore := realDelivererRecordSnapshot(t, h.lifecycle, child)

	notices, runErr := runRealDelivererPass(t, h, al, deliverer)
	for _, n := range notices {
		t.Logf("operator notice: %s", n)
	}

	messages := realDelivererInboxMessages(t, h.inbox, parent)
	if len(messages) != 0 {
		t.Fatalf("the pass published %v to the parent's real inbox while the committed payload's bytes no longer match its protected payload_hash — D2: a hash-diverged commit is refused visibly BEFORE any deliverer call or inbox append",
			realDelivererMessageIDs(messages))
	}
	joined := strings.Join(notices, "\n")
	if !strings.Contains(joined, finalID) && (runErr == nil || !strings.Contains(runErr.Error(), finalID)) {
		t.Fatalf("the hash-diverged commit %s was blocked with no visible refusal naming it (notices %q, scan error %v) — D2: the refusal is a visible consistency error, never silence",
			finalID, joined, runErr)
	}
	progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
	if err != nil {
		t.Fatalf("FinalDeliveryState(G): %v", err)
	}
	if progress.InboxAppended || progress.AckObserved || retired {
		t.Fatalf("the refused commit's durable facts moved to %+v (revision %d retired %v) — a refused publication records no receipts and stays pending/retryable (D2)",
			progress, revision, retired)
	}
	tailAfter := realDelivererRecordSnapshot(t, h.lifecycle, child)
	if string(tailAfter) != string(tailBefore) {
		t.Fatal("the refused pass mutated the tail record — the refusal must leave the record unchanged (D2)")
	}
}
