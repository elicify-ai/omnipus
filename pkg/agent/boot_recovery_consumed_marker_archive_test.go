// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// RED (founder Q2 crash item): a consumed marker can be saved before the
// accepted instruction reaches the durable context archive. The two writes
// are ordered marker-first in production — pkg/agent/steering.go::
// consumeDequeuedSteeringResult persists "consumed <messageID>" through
// writeSteeringConsumedMarker AT DEQUEUE, while the instruction itself is
// only seeded as pendingMessages (pkg/agent/loop_run_turn.go, from
// InitialSteeringMessages) and admitted into the TURN'S DURABLE CONTEXT
// ARCHIVE later, inside the turn iteration, through
// pkg/agent/window_runtime.go::turnState.appendWindowMessage ->
// session.ContextWindowStore.AppendWindowMessage. The transcript only ever
// gains the consumed marker, never the instruction. A crash in between
// leaves the marker in the steering session's transcript with no archived
// instruction.
//
// Specification (the dispatch brief, the oracle for both tests below — not
// observed behaviour): "A crash in that gap must not acknowledge the parent
// message or skip it permanently." Recovery treats the marker as delivery
// only WITH the archived instruction (the brief's own contrast, and the I-3
// idempotency boundary documented on
// pkg/agent/loop_inbound.go::processSteeredSystemWake: a replayed,
// already-executed wake is acknowledged without starting another turn).
//
// The recovery seam under test is pkg/agent/boot_sweep.go::SteerBootRecovery
// — its deliverIfUnconsumed/ackConsumed decide exactly this question at boot
// from the steering session's transcript markers versus the parent inbox.

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// bootArchiveRecordingDeliverer is a recording-only double at the published
// steer.UpwardDeliverer edge. Unlike the shared bootRecordingDeliverer it
// does NOT re-append the delivered message into the inbox — these tests must
// observe the ORIGINAL entry's acknowledgement state, which a re-append
// would mask with a fresh unacked copy under the same message id.
type bootArchiveRecordingDeliverer struct {
	events []steer.UpwardEvent
}

func (d *bootArchiveRecordingDeliverer) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	d.events = append(d.events, event)
	envelope, err := decodeBootMessage(event.Message)
	if err != nil {
		return steer.Delivery{}, err
	}
	return steer.Delivery{MessageID: envelope.MessageID, Outcome: steer.DeliveryWoke}, nil
}

// archiveRecovery wires the harness's real reopened stores to the
// recording-only deliverer (same wiring the harness's own recovery() uses).
func (h *bootRecoveryHarness) archiveRecovery(deliverer *bootArchiveRecordingDeliverer) *SteerBootRecovery {
	return &SteerBootRecovery{
		Lifecycle:      h.lifecycle,
		Sessions:       h.sessions,
		Inbox:          h.inbox,
		BootEpoch:      h.writingBoot,
		Classifier:     NewSteerRecordClassifier(h.lifecycle, h.sessions),
		Deliverer:      deliverer,
		OperatorNotice: func(message string) { h.notices = append(h.notices, message) },
	}
}

// persistedCrashGap builds the post-crash disk state both tests share: a
// steered child mid-flight, its accepted question still unacknowledged in
// the parent inbox, and the consumed marker already saved in the steering
// session's transcript — exactly the shape writeSteeringConsumedMarker
// leaves (id "consumed-<id>", system/system, "consumed <id>").
func persistedCrashGap(t *testing.T, h *bootRecoveryHarness) (parent, child, messageID string) {
	t.Helper()
	parent = h.rootSession(t)
	child = h.newSession(t, session.SessionTypeDelegate, parent)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))

	messageID = "question-crash-gap"
	message := bootQuestion(t, child, parent, messageID)
	if _, err := h.inbox.Append(parent, message); err != nil {
		t.Fatalf("Append inbox message: %v", err)
	}
	if err := h.sessions.AppendTranscriptStrict(parent, session.TranscriptEntry{
		ID: "consumed-" + messageID, Type: session.EntryTypeSystem, Role: "system",
		Content: "consumed " + messageID,
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict consumed marker: %v", err)
	}
	return parent, child, messageID
}

// deliveriesOf counts recorded deliveries carrying the given message id.
func deliveriesOf(t *testing.T, deliverer *bootArchiveRecordingDeliverer, messageID string) int {
	t.Helper()
	count := 0
	for _, event := range deliverer.events {
		if bootEnvelope(t, event.Message).MessageID == messageID {
			count++
		}
	}
	return count
}

// inboxHasUnacknowledged reports whether the parent inbox still holds an
// unacknowledged entry for the message id (Drain returns unacked only).
func inboxHasUnacknowledged(t *testing.T, h *bootRecoveryHarness, parent, child, messageID string) bool {
	t.Helper()
	remaining, _, _, err := h.inbox.Drain(parent, child, "", session.DefaultInboxUnackedMax)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	for _, message := range remaining {
		if bootEnvelope(t, message).MessageID == messageID {
			return true
		}
	}
	return false
}

// TestBoot_ConsumedMarkerWithoutArchivedInstruction_IsRedeliveredAndUnacknowledged
// is the RED case: the marker was saved but the accepted instruction never
// reached the steering session's durable context archive (the crash gap).
// Boot recovery must NOT treat the marker as delivery — the parent's message
// must still be delivered upward and must remain unacknowledged. On the
// current source, deliverIfUnconsumed acknowledges and skips on the marker
// alone, so both assertions fail.
func TestBoot_ConsumedMarkerWithoutArchivedInstruction_IsRedeliveredAndUnacknowledged(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent, child, messageID := persistedCrashGap(t, h)
	deliverer := &bootArchiveRecordingDeliverer{}

	if err := h.archiveRecovery(deliverer).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := deliveriesOf(t, deliverer, messageID); got != 1 {
		t.Fatalf("crash-gap message delivered %d times, want exactly 1 — recovery skipped an instruction that never reached the durable context archive", got)
	}
	if !inboxHasUnacknowledged(t, h, parent, child, messageID) {
		t.Fatalf("crash-gap message %q was acknowledged at boot although its instruction was never archived", messageID)
	}
}

// TestBoot_ConsumedMarkerWithArchivedInstruction_StaysAcknowledgedWithoutRedelivery
// is the executed control: the same marker PLUS the archived instruction —
// the user-role message the continuation turn admits into its durable
// context archive once it actually runs (content is the wake's delivery
// summary, the text pkg/agent/steer_audience.go::deliverySummary injects
// for a question). The write goes through the EXACT production seam the
// turn drives for pendingMessages injection —
// pkg/agent/window_runtime.go::turnState.appendWindowMessage ->
// session.ContextWindowStore.AppendWindowMessage — keyed by the session id
// the reconstructed turn runs under: pkg/agent/steer_reconstruct.go sets
// SessionKey: rec.SessionID, so the archive lands on the CHILD session's
// own context-window archive — the id recovery's accepted-drain oracle
// reads back — never on the steering session's store. It deliberately does
// NOT append a user transcript line:
// the transcript holds only the consumed marker, and recovery never reads
// a user transcript entry.
// Marker + archived instruction is a genuine delivery: the idempotency
// boundary stands (acknowledged, no duplicate delivery). Green on the
// current source and required to stay green after the fix.
func TestBoot_ConsumedMarkerWithArchivedInstruction_StaysAcknowledgedWithoutRedelivery(t *testing.T) {
	h := newBootRecoveryHarness(t)
	parent, child, messageID := persistedCrashGap(t, h)
	archived := providers.Message{Role: "user", Content: "A delegated session is asking: choose"}
	if _, err := h.sessions.AppendWindowMessage(context.Background(), child, archived); err != nil {
		t.Fatalf("AppendWindowMessage archived instruction: %v", err)
	}
	// Instrument check: the archive write must read back from the child
	// archive recovery's accepted-drain oracle reads — a silently swallowed
	// append would leave this control's premise unrepresented.
	snap, err := h.sessions.SnapshotWindow(context.Background(), child)
	if err != nil {
		t.Fatalf("SnapshotWindow archived instruction: %v", err)
	}
	archivedVisible := false
	for _, line := range snap.Archive {
		if line.Role == "user" && line.Content == "A delegated session is asking: choose" {
			archivedVisible = true
			break
		}
	}
	if !archivedVisible {
		t.Fatalf("archived instruction is not readable back from session %q's durable context archive", child)
	}
	deliverer := &bootArchiveRecordingDeliverer{}

	if err := h.archiveRecovery(deliverer).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := deliveriesOf(t, deliverer, messageID); got != 0 {
		t.Fatalf("executed message delivered %d times after boot, want 0 — the consumed marker with its archived instruction must not re-deliver", got)
	}
	if inboxHasUnacknowledged(t, h, parent, child, messageID) {
		t.Fatalf("executed message %q was left unacknowledged — marker plus archived instruction is a delivery", messageID)
	}
}
