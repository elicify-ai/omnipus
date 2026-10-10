package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type bootRecordingDeliverer struct {
	mu        sync.Mutex
	inbox     *session.MessageInboxStore
	lifecycle *session.LifecycleStore
	events    []steer.UpwardEvent
}

func (d *bootRecordingDeliverer) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, err := d.lifecycle.Load(event.ChildSessionID)
	if err != nil {
		return steer.Delivery{}, err
	}
	result, err := d.inbox.Append(rec.SteeredBy.SteeringSessionID, event.Message)
	if err != nil {
		return steer.Delivery{}, err
	}
	d.events = append(d.events, event)
	return steer.Delivery{MessageID: result.MessageID, Outcome: steer.DeliveryWoke}, nil
}

func (d *bootRecordingDeliverer) snapshot() []steer.UpwardEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]steer.UpwardEvent(nil), d.events...)
}

type bootRecoveryHarness struct {
	lifecycle *session.LifecycleStore
	sessions  *session.UnifiedStore
	inbox     *session.MessageInboxStore
	deliverer *bootRecordingDeliverer
	// writingBoot is the actual BootEpochStore minted once for this simulated
	// writing boot over a real directory (never a hard-coded epoch). Recovery
	// built by recovery() reads the restart note's boot_seq from it.
	writingBoot *session.BootEpochStore
	notices     []string
}

// mintWritingBootForTest mints ONE genuine boot epoch over a fresh real
// directory under root and returns the minted store, as the gateway does at
// startup before recovery runs. The value is whatever Mint persisted.
func mintWritingBootForTest(t *testing.T, root string) *session.BootEpochStore {
	t.Helper()
	dir := filepath.Join(root, "boot_epoch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("SETUP create boot epoch dir: %v", err)
	}
	store := session.NewBootEpochStore(dir)
	epoch, err := store.Mint()
	if err != nil {
		t.Fatalf("SETUP Mint writing boot epoch: %v", err)
	}
	if epoch == 0 || store.Current() != epoch {
		t.Fatalf("SETUP: minted epoch=%d but Current()=%d", epoch, store.Current())
	}
	return store
}

func newBootRecoveryHarness(t *testing.T) *bootRecoveryHarness {
	t.Helper()
	root := t.TempDir()
	sessions, err := session.NewUnifiedStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sessions.Close(); closeErr != nil {
			t.Errorf("UnifiedStore.Close: %v", closeErr)
		}
	})
	h := &bootRecoveryHarness{
		lifecycle: session.NewLifecycleStore(filepath.Join(root, "lifecycle")),
		sessions:  sessions,
		inbox:     session.NewMessageInboxStore(filepath.Join(root, "inbox")),
	}
	h.writingBoot = mintWritingBootForTest(t, root)
	h.deliverer = &bootRecordingDeliverer{inbox: h.inbox, lifecycle: h.lifecycle}
	return h
}

func (h *bootRecoveryHarness) newSession(t *testing.T, typ session.UnifiedSessionType, parentID string) string {
	t.Helper()
	meta, err := h.sessions.NewSession(typ, "web", "agent-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if parentID != "" {
		if err := h.sessions.SetMeta(meta.ID, session.MetaPatch{ParentSessionID: &parentID}); err != nil {
			t.Fatalf("SetMeta parent: %v", err)
		}
	}
	return meta.ID
}

// rootSession creates a chat session together with its ordinary_root record,
// as the launcher does for every session that steers a child (landing order
// I-1, founder decision round 9). A child whose steering session has no record
// is an invalid edge, so every fixture tree starts from a recorded root.
func (h *bootRecoveryHarness) rootSession(t *testing.T) string {
	t.Helper()
	id := h.newSession(t, session.SessionTypeChat, "")
	h.persist(t, &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: session.LifecycleRunning,
		Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws", AgentID: "agent-1",
	})
	return id
}

func (h *bootRecoveryHarness) persist(t *testing.T, rec *session.LifecycleRecord) {
	t.Helper()
	if err := h.lifecycle.Persist(rec); err != nil {
		t.Fatalf("Persist(%s): %v", rec.SessionID, err)
	}
}

func (h *bootRecoveryHarness) steeredRecord(id, parent string, state session.LifecycleState) *session.LifecycleRecord {
	return &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: state,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parent, RootSessionID: parent},
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parent,
		WorkspaceID: "ws", AgentID: "agent-1", ParentAgentID: "agent-1",
	}
}

func (h *bootRecoveryHarness) recovery() *SteerBootRecovery {
	return &SteerBootRecovery{
		Lifecycle:      h.lifecycle,
		Sessions:       h.sessions,
		Inbox:          h.inbox,
		BootEpoch:      h.writingBoot,
		Classifier:     NewSteerRecordClassifier(h.lifecycle, h.sessions),
		Deliverer:      h.deliverer,
		OperatorNotice: func(message string) { h.notices = append(h.notices, message) },
	}
}

type bootMessageEnvelope struct {
	MessageID string `json:"message_id"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Fatal     bool   `json:"fatal"`
	Mode      string `json:"mode"`
}

func bootEnvelope(t *testing.T, message generated.SessionMessage) bootMessageEnvelope {
	t.Helper()
	raw, err := message.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var envelope bootMessageEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("Unmarshal envelope: %v", err)
	}
	return envelope
}

func bootHandback(t *testing.T, child, parent, id string) generated.SessionMessage {
	t.Helper()
	gen := 1
	var message generated.SessionMessage
	err := message.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: id, SessionId: child, ParentSessionId: &parent, CreatedAt: time.Now(),
		Depth: 1, Direction: "child_to_parent", Generation: &gen, Kind: "handback",
		Mode: "final", ResultSoFar: "finished", SenderIdentity: "agent-1",
		Artifacts: []string{}, OpenQuestions: []string{}, UntrustedOrigin: true,
	})
	if err != nil {
		t.Fatalf("encode handback: %v", err)
	}
	return message
}

func bootProgress(t *testing.T, child, parent, id string) generated.SessionMessage {
	t.Helper()
	gen := 1
	var message generated.SessionMessage
	err := message.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: id, SessionId: child, ParentSessionId: &parent, CreatedAt: time.Now(),
		Depth: 1, Direction: "child_to_parent", Generation: &gen, Kind: "progress",
		Text: "working", SenderIdentity: "agent-1", UntrustedOrigin: true,
	})
	if err != nil {
		t.Fatalf("encode progress: %v", err)
	}
	return message
}

func bootQuestion(t *testing.T, child, parent, id string) generated.SessionMessage {
	t.Helper()
	gen := 1
	var message generated.SessionMessage
	err := message.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId: id, SessionId: child, ParentSessionId: &parent, CreatedAt: time.Now(),
		Depth: 1, Direction: "child_to_parent", Generation: &gen, Kind: "question",
		CorrelationId: "corr", Text: "choose", SenderIdentity: "agent-1", UntrustedOrigin: true,
	})
	if err != nil {
		t.Fatalf("encode question: %v", err)
	}
	return message
}

// TestBoot_FailureDeliveredUpward — ADR-20260928 sub-agent control plane
// (frozen asset cd20cf8b) D8.3 supersedes the retired failed(interrupted)
// oracle this test used to pin ("record = failed/interrupted" plus a fatal
// "interrupted:" error delivered upward). D8.3: a restart-interrupted
// `running`/`queued` steered session becomes `stopped` — an ordinary,
// non-terminal stop — with a stop note {at, by: "restart", seq, cause:
// "restart", boot_seq}; no more failed(interrupted) record. D8.4: the parent
// is told through the persisted D6 stop notice of that ledgered transition,
// never through the retired second verdict, a fatal "interrupted: ..." final
// under <child>:<generation>:final.
//
// Scope: this fixture's recording deliverer is not a *SteerUpwardDeliverer, so
// the production notice publisher (SteerBootRecovery.noticeLoop) is not wired
// here and the notice's inbox bytes are NOT asserted — they are pinned against a
// real AgentLoop by TestBoot_PauseHandbackRedeliveredAsBlockerNotFinal and
// TestFencelessLedger_BootStopsResumedRun_OldUntakenNoticeStillOwed. What this
// test pins is the D8.3 landing itself and that nothing of the retired verdict
// is delivered upward.
//
// Epoch: by=restart requires the WRITING boot's minted epoch (architect
// BOOT-EPOCH-RULING D1/D2). The fixture's recovery() carries one genuinely
// minted BootEpochStore for the simulated boot (harness commits 73c8f8b38 /
// 709fd4dfa on work/a-boot-qa-harness-20261005); this test never supplies an
// epoch of its own and asserts only that the note carries a nonzero boot_seq (a
// minted epoch starts at 1) — the exact boot_seq-equals-Current pins live in
// the harness's own assertions, which need h.writingBoot.
func TestBoot_FailureDeliveredUpward(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rec, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != session.LifecycleStopped || rec.Terminal() || rec.FailedReason != "" {
		t.Fatalf("record = state %q (terminal=%v) failed_reason %q, want stopped non-terminal with no failed reason — D8.3: a restart-interrupted session becomes an ordinary stop, never failed(interrupted)",
			rec.State, rec.Terminal(), rec.FailedReason)
	}
	if rec.StopNote == nil {
		t.Fatal("stopped record carries no stop note — D8.3: the restart stop note {by restart, cause restart, boot_seq}")
	}
	if rec.StopNote.Cause != session.StopCauseRestart || rec.StopNote.By != session.StopActorRestart {
		t.Fatalf("stop note = (cause %q, by %q), want (%q, %q) — D8.3: by \"restart\", cause \"restart\"",
			rec.StopNote.Cause, rec.StopNote.By, session.StopCauseRestart, session.StopActorRestart)
	}
	if rec.StopNote.BootSeq == 0 {
		t.Fatalf("stop note boot_seq = 0 — D8.3: the restart note carries the writing boot's persisted (nonzero) epoch; note %+v", rec.StopNote)
	}

	// D6/D8.4: the ledgered transition is the stop notice's authority — one
	// landed restart stop, the child's first accepted stop (D4: per-child seq
	// from 1), naming its direct parent.
	transitions, err := h.lifecycle.ListStoppedTransitions(child)
	if err != nil {
		t.Fatalf("ListStoppedTransitions: %v", err)
	}
	if len(transitions) != 1 {
		t.Fatalf("landed history = %+v, want exactly one restart transition", transitions)
	}
	tr := transitions[0]
	if tr.Cause != session.StopCauseRestart || tr.Actor != session.StopActorRestart || tr.Generation != 1 ||
		tr.ParentSessionID != parent || tr.StopSeq != 1 {
		t.Fatalf("landed transition = %+v, want {cause restart, actor restart, generation 1, parent %q, stop_seq 1}", tr, parent)
	}

	// The retired second verdict (a fatal "interrupted: ..." final) is
	// delivered neither through the upward deliverer nor into the parent's inbox.
	finalID := child + ":1:final"
	for _, event := range h.deliverer.snapshot() {
		envelope := bootEnvelope(t, event.Message)
		t.Errorf("recovery delivered upward %+v — D8.3/D8.4: a restart stop is told by its D6 stop notice, never by an upward fatal verdict", envelope)
	}
	entries, err := h.inbox.Entries(parent)
	if err != nil {
		t.Fatalf("Entries(parent): %v", err)
	}
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		envelope := bootEnvelope(t, *entry.Message)
		if envelope.MessageID == finalID || envelope.Fatal || strings.HasPrefix(envelope.Text, "interrupted:") {
			t.Errorf("parent inbox holds the retired interrupted verdict %+v — D8.3/D8.4", envelope)
		}
	}
}

func TestBoot_StoppedStaysStoppedUnlessNewerInstruction(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	rec.Stop = &session.Stop{Generation: 1, At: time.Now(), By: session.Principal{Kind: session.PrincipalKindHuman, ID: "owner"}}
	h.persist(t, rec)

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after, _ := h.lifecycle.Load(child)
	if after.State != session.LifecycleRunning || after.Stop == nil || after.Stop.Generation != 1 {
		t.Fatalf("stopped record changed: %+v", after)
	}
	if len(h.deliverer.snapshot()) != 0 {
		t.Fatal("stamped session was woken or reported as interrupted")
	}
}

func TestBoot_RenudgesUnconsumedEntriesOnce_EligibleOnly(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleNeedsInput)
	rec.NeedsInput = &session.NeedsInput{CorrelationID: "corr", TTLDeadline: time.Now().Add(time.Hour)}
	h.persist(t, rec)

	consumedHandback := bootHandback(t, child, parent, "handback-consumed")
	for _, message := range []generated.SessionMessage{
		bootHandback(t, child, parent, "handback-open"),
		bootProgress(t, child, parent, "progress-open"),
		bootQuestion(t, child, parent, "question-open"),
		consumedHandback,
	} {
		if _, err := h.inbox.Append(parent, message); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := h.sessions.AppendTranscriptStrict(parent, session.TranscriptEntry{
		ID: "consumed-handback", Type: session.EntryTypeSystem, Role: "system", Content: "consumed handback-consumed",
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict: %v", err)
	}

	// A genuinely delivered handback, not the crash gap: the delivery rule
	// (boot_sweep.go::instructionArchived) counts the consumed marker as
	// delivery only when the CHILD session's durable context archive holds
	// the user-role instruction the live wake injected —
	// steer_audience.go::deliverySummary of the SAME inbox message. Archive
	// it the way the turn does (window_runtime.go::turnState.appendWindowMessage),
	// child session only; the parent transcript keeps only the consumed
	// marker. Without this line the fixture is the crash gap and the
	// handback is legitimately re-woken.
	archived := providers.Message{Role: "user", Content: deliverySummary(consumedHandback)}
	if _, err := h.sessions.AppendWindowMessage(context.Background(), child, archived); err != nil {
		t.Fatalf("AppendWindowMessage archived handback instruction: %v", err)
	}
	// Instrument check: the archive write must read back from the child
	// archive recovery's accepted-drain oracle reads — a silently swallowed
	// append would leave the fixture asserting the very crash-gap shape it
	// exists to rule out.
	snap, err := h.sessions.SnapshotWindow(context.Background(), child)
	if err != nil {
		t.Fatalf("SnapshotWindow archived handback instruction: %v", err)
	}
	archivedVisible := false
	for _, line := range snap.Archive {
		if line.Role == "user" && line.Content == deliverySummary(consumedHandback) {
			archivedVisible = true
			break
		}
	}
	if !archivedVisible {
		t.Fatalf("archived handback instruction %q is not readable back from session %q's durable context archive", deliverySummary(consumedHandback), child)
	}

	if runErr := h.recovery().Run(context.Background()); runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	snapshot := h.deliverer.snapshot()
	got := make([]string, 0, len(snapshot))
	for _, event := range snapshot {
		got = append(got, bootEnvelope(t, event.Message).MessageID)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"handback-open", "question-open"}) {
		t.Fatalf("re-woken = %v", got)
	}
	remaining, _, _, err := h.inbox.Drain(parent, child, "", 20)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	for _, message := range remaining {
		if bootEnvelope(t, message).MessageID == "handback-consumed" {
			t.Fatal("consumed entry was not acknowledged")
		}
	}
}

// TestBoot_RepairsHalfWrittenCompletion — W3 RED reconcile (fresh-context
// qa-lead), ADR-20260928 sub-agent control plane (frozen asset cd20cf8b).
// The pre-migration oracle demanded that boot promote a steered record to
// completed from an inbox final alone in every crash cut — the inbox-first
// repair D2 retired: "finishFromFinal must not turn a non-terminal record
// into done/failed merely because an inbox final exists: require the
// matching lifecycle/outbox commit (otherwise report the inconsistent record
// visibly and do not consume the id). Parent consumption/ack never acts as
// the lifecycle winner." The three crash cuts are migrated INDIVIDUALLY:
//
//  1. inbox final WITHOUT a committed outcome — reported visibly, never
//     promoted, the id never consumed (no re-wake as a final answer, no
//     acknowledgement), and the entry left recoverable exactly once for a
//     correct boot repair (D2/D8.5).
//  2. a COMMITTED terminal/outbox cut off before the inbox append — the
//     legitimate positive: the exact committed payload publishes once
//     through the deliverer with its durable delivery facts (D2 "Publish
//     only a committed outbox", D8.1). Built through the same ONE real
//     LifecycleStore.Mutate the production commit boundary performs
//     (commitAllGenDoneFinal) — never a hand-seeded terminal record, which
//     in production cannot exist without its protected commit tuple.
//  3. a committed outbox whose parent-inbox append already landed — the
//     existing id retries the downstream wake without a second final (D2).
//
// Unit boundary: the same real stores and SteerBootRecovery.Run the boot
// suite always uses; the recording deliverer appends to the REAL parent
// inbox. Specification source: the frozen ADR text quoted above — no
// expected value below is derived from observed behaviour of the code under
// test. Known gaps (deliberate): no FramesPersisted fact is asserted (the
// pinned tree has no frames writer — audited and reported separately, no
// fake boolean fact); the D8.3 stopped(restart) landing of the uncommitted
// run is W3b, so case 1 pins non-promotion, not the landing state;
// CHECK mutations (promote-on-inbox-presence, dropped phantom notice,
// byte-divergent publication, duplicate append on the already-appended id,
// skipped UpdateFinalDelivery facts) are deferred to CHECK.
func TestBoot_RepairsHalfWrittenCompletion(t *testing.T) {
	t.Run("inbox final without commit is reported, never promoted or consumed", func(t *testing.T) {
		h := newBootRecoveryHarness(t)
		parent := h.rootSession(t)
		child := h.newSession(t, session.SessionTypeDelegate, parent)
		h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))
		finalID := child + ":1:final"
		if _, err := h.inbox.Append(parent, bootHandback(t, child, parent, finalID)); err != nil {
			t.Fatalf("Append final: %v", err)
		}

		if err := h.recovery().Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}

		rec, err := h.lifecycle.Load(child)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if rec.State == session.LifecycleCompleted {
			t.Fatal("an inbox final with NO committed lifecycle/outbox outcome promoted the record to completed — D2: the promotion requires the matching commit; an inbox final alone is never a lifecycle winner")
		}
		if rec.FinalDelivery != nil {
			t.Fatalf("a protected commit was minted from inbox presence alone (commit %q) — D2: recovery never mints an outbox commit from a phantom final", rec.FinalDelivery.CommitID)
		}
		if rec.Generation != 1 {
			t.Fatalf("tail generation = %d, want 1 — reconciliation from a phantom final must not mint a newer generation (D8.5)", rec.Generation)
		}
		joined := strings.Join(h.notices, "\n")
		if !strings.Contains(joined, child) || !strings.Contains(joined, finalID) {
			t.Fatalf("the inconsistent record was not reported visibly (notices %q) — D2: report the inconsistent record visibly, naming the session and the unconsumed final %s", joined, finalID)
		}
		for _, event := range h.deliverer.snapshot() {
			envelope := bootEnvelope(t, event.Message)
			if envelope.MessageID == finalID && event.Outcome == steer.OutcomeFinalAnswer {
				t.Fatalf("the phantom final %s was re-woken as a final answer — D2: do not consume the id; the inbox final is never re-published as the completion", finalID)
			}
		}
		entries, err := h.inbox.Entries(parent)
		if err != nil {
			t.Fatalf("Entries(parent): %v", err)
		}
		for _, entry := range entries {
			if entry.Kind == session.InboxEntryAck && slices.Contains(entry.AckedIDs, finalID) {
				t.Fatalf("the phantom final %s was acknowledged — D2: parent consumption/ack never acts as the lifecycle winner, and a consumed id could never be repaired", finalID)
			}
		}
		messages, _, _, err := h.inbox.Drain(parent, child, "", 20)
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
		count := 0
		for _, message := range messages {
			if bootEnvelope(t, message).MessageID == finalID {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("phantom final entries in the parent inbox = %d, want exactly 1 — the id is not consumed and the record stays recoverable for a correct boot repair (D2)", count)
		}
	})

	t.Run("committed outbox cut before append publishes exactly once at the terminal tail", func(t *testing.T) {
		h := newBootRecoveryHarness(t)
		parent := h.rootSession(t)
		child := h.newSession(t, session.SessionTypeDelegate, parent)
		commitID := "commit-g1-repairs-terminal-first"
		finalID, payload, payloadHash := commitAllGenDoneFinal(t, h, child, parent, commitID,
			"committed, never appended: the crash cut before the parent inbox append")

		if err := h.recovery().Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}

		rec, err := h.lifecycle.Load(child)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if rec.State != session.LifecycleCompleted {
			t.Fatalf("state = %q, want completed — the committed terminal outcome stands; a delivery retry never blanket-fails a committed generation (D2)", rec.State)
		}
		if rec.FinalDelivery == nil || rec.FinalDelivery.CommitID != commitID ||
			rec.FinalDelivery.MessageID != finalID || rec.FinalDelivery.PayloadHash != payloadHash {
			t.Fatalf("the protected commit identity changed: %+v — D2: outcome, final id and payload identity are immutable once committed", rec.FinalDelivery)
		}
		messages := realDelivererInboxMessages(t, h.inbox, parent)
		got, ok := messages[finalID]
		if !ok {
			t.Fatalf("the committed final %s never reached the parent inbox (ids %v) — D2/D8.1: the exact committed message is published to the direct parent", finalID, realDelivererMessageIDs(messages))
		}
		if got != string(payload) {
			t.Fatalf("published final %s bytes diverge from the committed payload — D2: the exact committed upward message is published", finalID)
		}
		if len(messages) != 1 {
			t.Fatalf("parent inbox holds %d message entries (%v), want exactly the one committed final — publish once, the replay id is the duplicate guard (D2)", len(messages), realDelivererMessageIDs(messages))
		}
		wakes := 0
		for _, event := range h.deliverer.snapshot() {
			if bootEnvelope(t, event.Message).MessageID == finalID && event.Outcome == steer.OutcomeFinalAnswer {
				wakes++
			}
		}
		if wakes != 1 {
			t.Fatalf("committed final %s woke the parent %d times, want exactly 1 (D2)", finalID, wakes)
		}
		progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
		if err != nil {
			t.Fatalf("FinalDeliveryState(G): %v", err)
		}
		if retired || revision < 1 || !progress.InboxAppended || !progress.WakeRecorded {
			t.Fatalf("delivery facts after boot = %+v revision %d retired %v — the pass must record the durable inbox append and wake through UpdateFinalDelivery (D2)", progress, revision, retired)
		}
	})

	t.Run("committed outbox with the inbox append already durable does not duplicate", func(t *testing.T) {
		h := newBootRecoveryHarness(t)
		parent := h.rootSession(t)
		child := h.newSession(t, session.SessionTypeDelegate, parent)
		commitID := "commit-g1-repairs-both-written"
		finalID, payload, payloadHash := commitAllGenDoneFinal(t, h, child, parent, commitID,
			"committed and already appended: the crash cut before the wake")
		var committed generated.SessionMessage
		if err := committed.UnmarshalJSON(payload); err != nil {
			t.Fatalf("decode committed payload: %v", err)
		}
		if _, err := h.inbox.Append(parent, committed); err != nil {
			t.Fatalf("Append committed final: %v", err)
		}
		if seeded := realDelivererInboxMessages(t, h.inbox, parent); len(seeded) != 1 {
			t.Fatalf("fixture control: parent inbox holds %d entries before boot, want exactly the appended committed final", len(seeded))
		}

		if err := h.recovery().Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}

		rec, err := h.lifecycle.Load(child)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if rec.State != session.LifecycleCompleted {
			t.Fatalf("state = %q, want completed — the committed terminal outcome stands (D2)", rec.State)
		}
		if rec.FinalDelivery == nil || rec.FinalDelivery.CommitID != commitID ||
			rec.FinalDelivery.MessageID != finalID || rec.FinalDelivery.PayloadHash != payloadHash {
			t.Fatalf("the protected commit identity changed: %+v — D2: the commit identity is immutable once committed", rec.FinalDelivery)
		}
		messages := realDelivererInboxMessages(t, h.inbox, parent)
		got, ok := messages[finalID]
		if !ok {
			t.Fatalf("the committed final %s vanished from the parent inbox (ids %v) — the already-appended id stays", finalID, realDelivererMessageIDs(messages))
		}
		if got != string(payload) {
			t.Fatalf("stored final %s bytes diverge from the committed payload — D2: an existing id with the same committed identity is retried, never replaced", finalID)
		}
		if len(messages) != 1 {
			t.Fatalf("parent inbox holds %d message entries (%v), want exactly 1 — an existing id with the same committed payload is never appended a second time (D2)", len(messages), realDelivererMessageIDs(messages))
		}
		wakes := 0
		for _, event := range h.deliverer.snapshot() {
			if bootEnvelope(t, event.Message).MessageID == finalID && event.Outcome == steer.OutcomeFinalAnswer {
				wakes++
			}
		}
		if wakes != 1 {
			t.Fatalf("committed final %s woke the parent %d times, want exactly 1 — the downstream wake retries once for the already-appended id (D2)", finalID, wakes)
		}
		progress, revision, retired, err := h.lifecycle.FinalDeliveryState(child, 1, commitID)
		if err != nil {
			t.Fatalf("FinalDeliveryState(G): %v", err)
		}
		if retired || revision < 1 || !progress.InboxAppended || !progress.WakeRecorded {
			t.Fatalf("delivery facts after boot = %+v revision %d retired %v — the durable receipts must be recorded through UpdateFinalDelivery (D2)", progress, revision, retired)
		}
	})
}

func TestBoot_ClassifiesAllClasses(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.newSession(t, session.SessionTypeChat, "")
	h.persist(t, &session.LifecycleRecord{
		SessionID: parent, Generation: 1, State: session.LifecycleRunning,
		Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws", AgentID: "agent-1", ParentAgentID: "agent-1",
	})

	steered := h.newSession(t, session.SessionTypeDelegate, parent)
	steeredRec := h.steeredRecord(steered, parent, session.LifecycleNeedsInput)
	steeredRec.NeedsInput = &session.NeedsInput{CorrelationID: "corr", TTLDeadline: time.Now().Add(time.Hour)}
	h.persist(t, steeredRec)

	legacy := h.newSession(t, session.SessionTypeDelegate, "")
	h.persist(t, &session.LifecycleRecord{
		SessionID: legacy, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws", AgentID: "agent-1", ParentAgentID: "agent-1",
	})

	damaged := h.newSession(t, session.SessionTypeDelegate, parent)
	invalid := h.newSession(t, session.SessionTypeDelegate, parent)
	invalidRec := h.steeredRecord(invalid, "different-parent", session.LifecycleRunning)
	h.persist(t, invalidRec)

	if err := os.MkdirAll(h.lifecycle.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.lifecycle.Dir(), "unreadable.jsonl"), []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ordinaryAfter, _ := h.lifecycle.Load(parent)
	steeredAfter, _ := h.lifecycle.Load(steered)
	legacyAfter, _ := h.lifecycle.Load(legacy)
	invalidAfter, _ := h.lifecycle.Load(invalid)
	if ordinaryAfter.State != session.LifecycleRunning {
		t.Fatalf("ordinary root state = %q", ordinaryAfter.State)
	}
	if steeredAfter.State != session.LifecycleNeedsInput {
		t.Fatalf("steered parked state = %q", steeredAfter.State)
	}
	// DEL-13 (session-core): boot no longer fails a legacy delegate; it is
	// refused with a notice (asserted below) and its record is left untouched.
	if legacyAfter.State != session.LifecycleRunning || legacyAfter.FailedReason != "" {
		t.Fatalf("legacy record was rewritten: state %q reason %q", legacyAfter.State, legacyAfter.FailedReason)
	}
	if invalidAfter.State != session.LifecycleRunning {
		t.Fatalf("invalid edge was resumed or rewritten: %q", invalidAfter.State)
	}
	if h.lifecycle.Exists(damaged) {
		t.Fatal("damaged metadata-only child unexpectedly gained a lifecycle record")
	}
	joined := strings.Join(h.notices, "\n")
	for _, expected := range []string{legacy, damaged, invalid, "unreadable"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("operator notices %q do not name %q", joined, expected)
		}
	}
}

// TestBoot_TimeoutStoppedStaysStopped pins ADR-20260928 D8 (founder
// decision, 2026-10-04): a steered session whose lifetime budget expired —
// stop_note.cause=timeout, the shape steer_completion.go::completeSteeredTurn
// lands live — and that never stored its final before the gateway restart
// STAYS stopped across the restart. The boot neither fails it, nor rewrites
// its retained timeout note, nor delivers the old "timeout:" fatal to the
// parent. The merged "stopped" state deliberately erased the
// cancelled/timed-out state distinction (founder ruling: timed out =
// stopped); the RETAINED stop_note cause is what the parent's decide-offers
// notice is composed from (D6), never a boot rewrite. Supersedes the U1-era
// oracle TestBoot_TimeoutStoppedRecoversAsTimedOut, which pinned the
// failed(timeout) conversion that boot_sweep.go::recoverSteered and
// failInterrupted no longer perform.
func TestBoot_TimeoutStoppedStaysStopped(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleStopped)
	note := &session.StopNote{
		At: time.Now().UTC(), By: session.StopActorSystem,
		Seq: uint64(rec.Generation), Cause: session.StopCauseTimeout,
	}
	rec.StopNote = note
	h.persist(t, rec)

	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if events := h.deliverer.snapshot(); len(events) != 0 {
		t.Fatalf("deliveries = %d, want 0 — D8: the restart sends no old timeout fatal for an already-stopped helper", len(events))
	}
	after, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if after.State != session.LifecycleStopped || after.Terminal() {
		t.Fatalf("record = state %q (terminal=%v), want stopped non-terminal — D8: an already-stopped helper stays stopped across a restart", after.State, after.Terminal())
	}
	if after.FailedReason != "" {
		t.Fatalf("FailedReason = %q, want empty — D8: the restart marks nothing failed", after.FailedReason)
	}
	if after.StopNote == nil || after.StopNote.Cause != session.StopCauseTimeout || after.StopNote.Seq != note.Seq || after.StopNote.By != note.By {
		t.Fatalf("stop note after boot = %+v, want the retained timeout note unchanged (cause %q seq %d by %q)", after.StopNote, note.Cause, note.Seq, note.By)
	}
}

// TestBoot_StoppedNonTimeoutStaysStopped keeps the D8 pin broad across the
// retained-note vocabulary: a stop-caused stop (a human Stop, current
// generation) and a STALE timeout note (Seq from an older generation a
// Revive kept) both stay stopped across the restart — no interrupted fatal,
// no failed(interrupted), each note kept exactly as stored. Supersedes the
// U1-era oracle TestBoot_StoppedNonTimeoutStillInterrupted, which pinned the
// failed(interrupted) conversion that boot_sweep.go::recoverSteered and
// failInterrupted no longer perform.
func TestBoot_StoppedNonTimeoutStaysStopped(t *testing.T) {
	for name, note := range map[string]*session.StopNote{
		"stop_cause_current_gen": {
			At: time.Now().UTC(), By: session.StopActorSystem,
			Seq: 1, Cause: session.StopCauseStop,
		},
		"stale_timeout_note": {
			At: time.Now().UTC(), By: session.StopActorSystem,
			Seq: 0, Cause: session.StopCauseTimeout, // Seq 0 predates generation 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newBootRecoveryHarness(t)
			parent := h.rootSession(t)
			child := h.newSession(t, session.SessionTypeDelegate, parent)
			rec := h.steeredRecord(child, parent, session.LifecycleStopped)
			rec.StopNote = note
			h.persist(t, rec)

			if err := h.recovery().Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if events := h.deliverer.snapshot(); len(events) != 0 {
				t.Fatalf("deliveries = %d, want 0 — D8: the restart sends no old interrupted/timeout fatal for an already-stopped helper", len(events))
			}
			after, err := h.lifecycle.Load(child)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if after.State != session.LifecycleStopped || after.Terminal() {
				t.Fatalf("record = state %q (terminal=%v), want stopped non-terminal — D8: an already-stopped helper stays stopped across a restart", after.State, after.Terminal())
			}
			if after.FailedReason != "" {
				t.Fatalf("FailedReason = %q, want empty — D8: the restart marks nothing failed", after.FailedReason)
			}
			if after.StopNote == nil || after.StopNote.Cause != note.Cause || after.StopNote.Seq != note.Seq || after.StopNote.By != note.By {
				t.Fatalf("stop note after boot = %+v, want the retained note unchanged (cause %q seq %d by %q)", after.StopNote, note.Cause, note.Seq, note.By)
			}
		})
	}
}

func TestIndexReport_SurfacedToOperator(t *testing.T) {
	h := newBootRecoveryHarness(t)
	if err := os.MkdirAll(h.lifecycle.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.lifecycle.Dir(), "broken.jsonl"), []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(h.notices, "\n")
	if !strings.Contains(joined, "broken") || !strings.Contains(joined, "unreadable") {
		t.Fatalf("operator notices = %q", joined)
	}
}
