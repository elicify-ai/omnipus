// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// errSteerUpwardDelivererNotWired is returned by every SteerUpwardDeliverer
// method until SetSteerAudienceDeps (steer_boundary.go) back-wires its
// *AgentLoop dependency. Callers reach this only if steering was never
// wired at all (a bare test AgentLoop, or a boot ordering bug).
var errSteerUpwardDelivererNotWired = errors.New("agent: steer: UpwardDeliverer not wired (SetSteerAudienceDeps was never called)")

// SteerAudienceResolver implements steer.AudienceResolver with I-5's real
// rule: ordinary_root -> the human user, steered -> the steering session,
// everything else -> none. D3 says answer none on any doubt, so a
// classifier error and every unrecognised class both fall to none rather
// than publishing.
type SteerAudienceResolver struct {
	// Classifier supplies the Class half of the answer (I-8 — see
	// steer_classify.go).
	Classifier steer.RecordClassifier
}

var _ steer.AudienceResolver = (*SteerAudienceResolver)(nil)

// NewSteerAudienceResolver returns an AudienceResolver backed by classifier.
func NewSteerAudienceResolver(classifier steer.RecordClassifier) *SteerAudienceResolver {
	return &SteerAudienceResolver{Classifier: classifier}
}

// Audience implements steer.AudienceResolver (I-5, real body): a steered
// session's audience is always its steering session; an ordinary root's is
// the user; every other class, an unreadable record, or a classifier error
// answers none — "any doubt" never falls back to the user (D3).
func (r *SteerAudienceResolver) Audience(ctx context.Context, sessionID string) (steer.Audience, steer.Class, error) {
	if r.Classifier == nil {
		return steer.AudienceNone, steer.ClassUnreadable, errors.New("steer: audience: no classifier configured")
	}
	class, err := r.Classifier.Classify(ctx, sessionID)
	if err != nil {
		return steer.AudienceNone, class, err
	}
	switch class {
	case steer.ClassOrdinaryRoot:
		return steer.AudienceUser, class, nil
	case steer.ClassSteered:
		return steer.AudienceSteeringSession, class, nil
	default:
		// damaged_child, legacy_delegate, unreadable, invalid_edge: no
		// audience at all (D3's "answers none on any doubt").
		return steer.AudienceNone, class, nil
	}
}

// SteerUpwardDeliverer implements steer.UpwardDeliverer (I-5, real body):
// the durable-inbox-then-wake path pkg/tools/message_parent.go already
// runs (Append -> WakeParent), made the ONLY upward path (landing order
// I-5's "Reuse" paragraph). Every turn Outcome maps onto an existing
// SessionMessage kind (I-5's table) — there is no second vocabulary.
type SteerUpwardDeliverer struct {
	// agentLoop supplies the lifecycle/inbox stores, the live-turn check,
	// the steering-queue enqueue and the wake transport. Back-wired by
	// SetSteerAudienceDeps (steer_boundary.go) — pkg/steer.Deps carries no
	// *AgentLoop reference, since pkg/steer imports nothing from pkg/agent
	// (landing order §2). Nil until wired; Deliver then refuses with
	// errSteerUpwardDelivererNotWired.
	agentLoop *AgentLoop
}

var _ steer.UpwardDeliverer = (*SteerUpwardDeliverer)(nil)

// NewSteerUpwardDeliverer returns an UpwardDeliverer with no *AgentLoop yet
// — SetSteerAudienceDeps back-wires it (landing order §7's "CP-0
// publication": this constructor's shape does not change across
// checkpoints, only what it is wired to).
func NewSteerUpwardDeliverer() *SteerUpwardDeliverer { return &SteerUpwardDeliverer{} }

// isTerminalOutcome reports whether o is one of the five turn outcomes I-5's
// table gives a deterministic `<child>:<gen>:final` id (a completion,
// success or not) — the pair the write-order/crash-repair guarantee (I-3,
// §7) covers.
func isTerminalOutcome(o steer.Outcome) bool {
	switch o {
	case steer.OutcomeFinalAnswer, steer.OutcomeEmptyAnswer,
		steer.OutcomeInterrupted, steer.OutcomeTimedOut, steer.OutcomeFailed:
		return true
	default:
		return false
	}
}

func validateOutcomeMessage(outcome steer.Outcome, class session.SessionMessageDeliveryClass) error {
	var wantKind string
	var wantFatal bool
	switch outcome {
	case steer.OutcomeFinalAnswer:
		wantKind = "handback"
	case steer.OutcomeEmptyAnswer, steer.OutcomeInterrupted, steer.OutcomeTimedOut, steer.OutcomeFailed:
		wantKind, wantFatal = "error", true
	case steer.OutcomeParkedQuestion:
		wantKind = "question"
	case steer.OutcomeBlocker:
		wantKind = "blocker"
	case steer.OutcomeGoalVerdict:
		wantKind = "goal_status"
	case steer.OutcomeProgress:
		wantKind = "progress"
	case steer.OutcomeCheckpoint:
		wantKind = "checkpoint"
	case steer.OutcomeLifecycleNotice:
		wantKind = "error"
	default:
		return fmt.Errorf("outcome %q has no deliverable message variant", outcome)
	}
	if class.Kind != wantKind || (wantKind == "error" && class.Fatal != wantFatal) {
		return fmt.Errorf("outcome %q does not match message kind %q (fatal=%v)", outcome, class.Kind, class.Fatal)
	}
	return nil
}

// subagentMessageKindForOutcome maps an I-5 turn Outcome onto the
// generated.SubagentMessageFrame.Kind value for ADR-091 D7/I-4's persisted
// side-panel status line — narration/report content only (progress,
// checkpoint, blocker, a goal verdict). Lifecycle TRANSITIONS (parked,
// completed, failed, ...) are subagent_state's job
// (subagentStateForOutcome), matching the WP-B spec's own AS-2 sequence
// ("subagent_message(progress)" for a progress report vs.
// "subagent_state(needs_input)" for a park — never
// "subagent_message(question)"). Empty return means "no subagent_message
// for this outcome."
//
// ADR-091 fix lane RX-HANG: steer.OutcomeLifecycleNotice used to return ""
// here too — paired with subagentStateForOutcome ALSO returning "" for the
// same outcome, the side panel showed literally nothing when a steered
// child exhausted its tool-iteration budget, on top of the parent never
// even being woken (message_inbox.go's classifyEnvelope, fixed
// separately). "error" is the correct kind, not a new invented one: it is
// the underlying SessionMessage's OWN discriminator
// (completionMessage/steer_completion.go builds this outcome as a kind
// "error", fatal:false SessionMessage) and is already a valid member of
// SubagentMessageFrame.Kind's enum (contracts/components/schemas/
// SubagentMessageFrame.yaml) — no contract change needed. The frame's Text
// carries deliverySummary's existing generic "error" formatting, which
// renders the message's own failureReason text (the
// "max_tool_iterations: ..." notice), so the panel shows plainly that the
// tool-step budget was exhausted, not a bare unexplained ping.
func subagentMessageKindForOutcome(o steer.Outcome) string {
	switch o {
	case steer.OutcomeProgress:
		return "progress"
	case steer.OutcomeCheckpoint:
		return "checkpoint"
	case steer.OutcomeBlocker:
		return "blocker"
	case steer.OutcomeGoalVerdict:
		return "goal_status"
	case steer.OutcomeLifecycleNotice:
		return "error"
	default:
		return ""
	}
}

// subagentStateForOutcome maps an I-5 turn Outcome onto the
// generated.SubagentStateFrame.State value for ADR-091 D7/I-4's persisted
// side-panel status — every outcome that represents a LIFECYCLE transition
// this lane can observe from Deliver alone (a park, or a terminal
// disposition). Mid-flight transitions this lane cannot observe
// (queued -> running, a Stop) are not covered here — see this lane's final
// report. Empty return means "no subagent_state for this outcome."
//
// ADR-091 fix lane RX-HANG: deliberately still "" for
// steer.OutcomeLifecycleNotice, unlike subagentMessageKindForOutcome above.
// The state value would be session.LifecycleRunning — the record's own
// nextState for this outcome — but steer_frames.go::deliverSubagentState
// keys its persisted frame id purely on (originCallID, childRec.Generation,
// state): "<call_id>:<generation>:state:running". That is the EXACT SAME
// id steer_launcher.go's dispatch path already wrote once, at the moment
// this same child first went queued->running, for this same generation —
// so a second `running` ping here would silently collide and be dropped by
// AppendTranscriptStrict's id-dedupe (Deliver's own doc comment above),
// not actually reach the panel. subagentMessageKindForOutcome's "error"
// message (a fresh, nanosecond-id'd frame every time) is what makes the
// panel show something for this outcome; adding a guaranteed-dropped state
// ping here would only be dead code. A real per-notice "running-idle"
// state distinct from ordinary "running" would need a new
// SubagentStateFrame.state enum value and a non-colliding id scheme —
// out of this lane's owned files (steer_frames.go).
func subagentStateForOutcome(o steer.Outcome) string {
	switch o {
	case steer.OutcomeParkedQuestion:
		return string(session.LifecycleNeedsInput)
	case steer.OutcomeFinalAnswer:
		return string(session.LifecycleCompleted)
	case steer.OutcomeEmptyAnswer, steer.OutcomeFailed:
		return string(session.LifecycleFailed)
	case steer.OutcomeInterrupted:
		return string(session.LifecycleCancelled)
	case steer.OutcomeTimedOut:
		return string(session.LifecycleTimedOut)
	default:
		return ""
	}
}

// deliverOwnerKey resolves the durable inbox owner key (D16) exclusively
// from the authoritative I-1 SteeredBy edge.
func deliverOwnerKey(rec *session.LifecycleRecord) string {
	if rec == nil {
		return ""
	}
	return strings.TrimSpace(rec.SteeringSessionID())
}

// deliverEntryIsAcked reports whether messageID is a genuinely ACKNOWLEDGED
// entry under ownerKey (Finding C, above) — the durable signal that
// distinguishes "a previous delivery ran this one to completion" from "this
// exact content happens to already be in the inbox", which a mere Append
// dedup cannot tell apart. Built entirely on MessageInboxStore.Drain's own
// documented contract ("Drain returns up to maxMessages UNACKED messages"),
// no new pkg/session surface: a message still present in Drain's output is
// still unacked; one Drain no longer returns is acked (or never existed,
// which Deliver's caller cannot reach here since res.MessageID was the id
// Append itself just resolved).
func deliverEntryIsAcked(inbox *session.MessageInboxStore, ownerKey, childSessionID, messageID string) (bool, error) {
	cursor := ""
	for {
		msgs, next, more, err := inbox.Drain(ownerKey, childSessionID, cursor, session.DefaultInboxUnackedMax)
		if err != nil {
			return false, err
		}
		for _, m := range msgs {
			if messageIDOf(m) == messageID {
				return false, nil
			}
		}
		if !more || next == cursor {
			return true, nil
		}
		cursor = next
	}
}

// messageIDOf extracts the message_id field common to every SessionMessage
// variant via the same JSON round-trip pkg/session/message_inbox.go's own
// envelope peek uses internally, without importing that unexported helper.
func messageIDOf(msg generated.SessionMessage) string {
	raw, err := msg.MarshalJSON()
	if err != nil {
		return ""
	}
	var envelope struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	return envelope.MessageID
}

// withDeterministicMessageID returns msg with its MessageId field
// overwritten to id. Only the two SessionMessage kinds a terminal Outcome
// ever maps to (handback, error) need this — every other kind's id is
// caller-supplied and left untouched.
func withDeterministicMessageID(msg generated.SessionMessage, id string) (generated.SessionMessage, error) {
	kind, err := msg.Discriminator()
	if err != nil {
		return msg, fmt.Errorf("steer: deliver: discriminator: %w", err)
	}
	switch kind {
	case "handback":
		v, aerr := msg.AsSessionMessageHandback()
		if aerr != nil {
			return msg, aerr
		}
		v.MessageId = id
		var out generated.SessionMessage
		if ferr := out.FromSessionMessageHandback(v); ferr != nil {
			return msg, ferr
		}
		return out, nil
	case "error":
		v, aerr := msg.AsSessionMessageError()
		if aerr != nil {
			return msg, aerr
		}
		v.MessageId = id
		var out generated.SessionMessage
		if ferr := out.FromSessionMessageError(v); ferr != nil {
			return msg, ferr
		}
		return out, nil
	default:
		return msg, nil
	}
}

// deliverySummary renders a short human-readable summary for the wake's
// content field, extending pkg/tools/message_parent.go's
// summarizeForWake with the two kinds only a turn-outcome event (never the
// message_parent tool) ever produces: error and goal_status.
func deliverySummary(msg generated.SessionMessage) string {
	kind, _ := msg.Discriminator()
	switch kind {
	case "handback":
		if v, err := msg.AsSessionMessageHandback(); err == nil {
			return fmt.Sprintf("A delegated session handed back (%s): %s", v.Mode, v.ResultSoFar)
		}
	case "question":
		if v, err := msg.AsSessionMessageQuestion(); err == nil {
			return fmt.Sprintf("A delegated session is asking: %s", v.Text)
		}
	case "blocker":
		if v, err := msg.AsSessionMessageBlocker(); err == nil {
			return fmt.Sprintf("A delegated session reported a %s-severity blocker: %s", v.Severity, v.Text)
		}
	case "error":
		if v, err := msg.AsSessionMessageError(); err == nil {
			return fmt.Sprintf("A delegated session reported an error: %s", v.Text)
		}
	case "goal_status":
		if v, err := msg.AsSessionMessageGoalStatus(); err == nil {
			return fmt.Sprintf("A delegated session's goal verdict: %s", v.Condition)
		}
	}
	return "A delegated session sent a message."
}

// Deliver implements steer.UpwardDeliverer (I-5, real body). See the type
// doc comment above for the reuse statement; the write order below (Append
// first, terminal lifecycle write elsewhere — I-3/WP-A) is I-5's own
// "Write order for a terminal outcome" paragraph.
func (d *SteerUpwardDeliverer) Deliver(ctx context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	al := d.agentLoop
	if al == nil {
		return steer.Delivery{}, errSteerUpwardDelivererNotWired
	}
	if event.Outcome == steer.OutcomeWaitingChildren {
		// I-5's table: "nothing yet" — the parent's handback is written
		// later, by the LAST child's own completion once the subtree is
		// quiet. A caller reaching Deliver with this outcome is a misuse.
		return steer.Delivery{}, fmt.Errorf("steer: deliver: outcome %q produces no upward event", event.Outcome)
	}

	lifecycle := al.GetSessionLifecycleStore()
	inbox := al.GetMessageInboxStore()
	if lifecycle == nil || inbox == nil {
		return steer.Delivery{}, fmt.Errorf("steer: deliver: session-messaging stores not configured")
	}

	childRec, err := lifecycle.Load(event.ChildSessionID)
	if err != nil {
		return steer.Delivery{}, fmt.Errorf("steer: deliver: load child %q: %w", event.ChildSessionID, err)
	}
	ownerKey := deliverOwnerKey(childRec)
	if ownerKey == "" {
		return steer.Delivery{}, fmt.Errorf("steer: deliver: child %q has no steering session (no edge)", event.ChildSessionID)
	}

	msg := event.Message
	class, classErr := session.ClassifySessionMessage(msg)
	if classErr != nil {
		return steer.Delivery{}, fmt.Errorf("steer: deliver: classify message: %w", classErr)
	}
	if matchErr := validateOutcomeMessage(event.Outcome, class); matchErr != nil {
		return steer.Delivery{}, fmt.Errorf("steer: deliver: %w", matchErr)
	}
	if isTerminalOutcome(event.Outcome) {
		id := fmt.Sprintf("%s:%d:final", event.ChildSessionID, childRec.Generation)
		msg, err = withDeterministicMessageID(msg, id)
		if err != nil {
			return steer.Delivery{}, fmt.Errorf("steer: deliver: stamp deterministic id: %w", err)
		}
	}

	res, appendErr := inbox.Append(ownerKey, msg)
	if appendErr != nil {
		return steer.Delivery{}, fmt.Errorf("steer: deliver: append: %w", appendErr)
	}

	// Finding C (ADR-091 fix lane 1, CRITICAL): a deterministic duplicate at
	// Append ONLY means this exact content already exists in the inbox — it
	// does NOT mean a previous delivery's downstream effects (the frames,
	// the wake) ever actually ran. boot_sweep.go::unacknowledged reads a
	// still-pending entry straight OUT of the inbox and hands it back to
	// this same Deliver unchanged, so Deduped is true BY CONSTRUCTION for
	// every entry boot recovery can find — the short-circuit below used to
	// make that re-delivery a total no-op: the wake never fired, and
	// because Deliver returns err == nil, no operator notice either. A
	// worker that finished right before a restart left its parent stalled
	// on every subsequent boot too, contradicting this file's own retired
	// claim that "a wake failure is never fatal — the boot re-nudge covers
	// it" (the boot re-nudge did not cover it).
	//
	// The short-circuit is legitimate ONLY when the stored entry is
	// genuinely ACKED (a real previous delivery ran to completion and the
	// recipient consumed it) or the message was never wake-eligible to
	// begin with. A still-unacked, wake-eligible duplicate falls through to
	// the SAME frames + wake path a first delivery takes — safe to repeat:
	// deliverSubagentMessage/State/End's frame ids are deterministic and
	// AppendTranscriptStrict rejects (id-dedupes) a repeat write.
	if res.Deduped {
		shortCircuit := !class.WakeEligible
		if !shortCircuit {
			acked, ackedErr := deliverEntryIsAcked(inbox, ownerKey, event.ChildSessionID, res.MessageID)
			if ackedErr != nil {
				return steer.Delivery{}, fmt.Errorf("steer: deliver: check acknowledgement: %w", ackedErr)
			}
			shortCircuit = acked
		}
		if shortCircuit {
			return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryStoredNotWoken}, nil
		}
	}

	// ADR-091 D7/I-4: the parent's side-panel status line, persisted as an
	// event in the parent's OWN transcript so it survives a reload
	// (steer_frames.go). Best-effort — see deliverSubagentMessage/State's
	// own doc comments for why a failure here never fails Deliver itself.
	if kind := subagentMessageKindForOutcome(event.Outcome); kind != "" {
		al.deliverSubagentMessage(ownerKey, childRec, kind, deliverySummary(msg), nil)
	}
	if state := subagentStateForOutcome(event.Outcome); state != "" {
		al.deliverSubagentState(ownerKey, childRec, state)
	}
	if isTerminalOutcome(event.Outcome) {
		al.deliverSubagentEnd(ownerKey, childRec, event.Outcome)
	}

	if !class.WakeEligible {
		return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryStoredNotWoken}, nil
	}

	ownerRec, ownerErr := lifecycle.Load(ownerKey)
	if ownerErr != nil || ownerRec == nil {
		// Edge case: the steering session's own record is gone (deleted
		// while the child was running). The entry is still durably stored
		// under the edge's owner key; surfaced to the operator, never a
		// hard failure — the child's own outcome must still land.
		logger.WarnCF("agent", "steer: deliver: steering session record missing — entry stored, wake skipped",
			map[string]any{"owner_key": ownerKey, "child_session_id": event.ChildSessionID})
		return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryStoredNotWoken}, nil
	}

	// FR-B-013: a recipient carrying a Stop marker for its CURRENT
	// generation is not woken; the entry is stored and acknowledged at
	// revival (I-6 Revive).
	if ownerRec.Stop != nil && ownerRec.Stop.Generation == ownerRec.Generation {
		return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryStoredNotWoken}, nil
	}

	sessionKey := ownerKey
	if ts := al.getActiveTurnState(sessionKey); ts != nil && ts.IsAlive() {
		pm := providers.Message{Role: "user", Content: deliverySummary(msg)}
		if enqErr := al.EnqueueSteeringWake(sessionKey, ownerRec.AgentID, ownerKey, res.MessageID, pm); enqErr != nil {
			return steer.Delivery{}, fmt.Errorf("steer: deliver: enqueue steering message: %w", enqErr)
		}
		return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryQueuedIntoLiveTurn}, nil
	}

	kindStr, _ := msg.Discriminator()
	if al.asyncNotifier != nil {
		target := childRec.SteeredBy.ReportingTarget
		wakeEvent := tools.MessageParentWakeEvent{
			Channel:             target.Channel,
			ChatID:              target.ChatID,
			AgentID:             ownerRec.AgentID,
			TranscriptSessionID: ownerKey,
			Content:             deliverySummary(msg),
			MessageID:           res.MessageID,
			Generation:          ownerRec.Generation,
		}
		if werr := al.asyncNotifier.WakeParentAlways(ctx, kindStr, wakeEvent); werr != nil {
			// Best-effort (matches message_parent.go's own contract): the
			// message is already durably stored; a wake failure is never
			// fatal to Deliver — the boot re-nudge (WP-D) covers it.
			logger.WarnCF("agent", "steer: deliver: wake failed (message is durable)",
				map[string]any{"kind": kindStr, "error": werr.Error()})
			return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryStoredNotWoken}, nil
		}
		return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryWoke}, nil
	}
	return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryStoredNotWoken}, nil
}
