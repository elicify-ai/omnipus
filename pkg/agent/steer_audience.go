// Owner: WP-B (landing order §7: "the WP-A lane writes the compiled no-op
// bodies for the interfaces WP-B and WP-D later implement, in files named
// for their owners ... ownership of those files passes to WP-B and WP-D at
// CP-0"). WP-A (this lane) writes this file's CP-0 stub bodies only; WP-B
// replaces them with the real I-5 rule and Deliver path at CP-2. Do not
// add production logic here after CP-0 — that is WP-B's.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package agent

import (
	"context"
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

// SteerAudienceResolver is the CP-0 compiled stub for
// steer.AudienceResolver, owned by WP-B from CP-0 onward. It returns
// today's behaviour — every session's audience is the human user — until
// WP-B lands I-5's real rule (ordinary_root -> user, steered -> steering
// session, everything else -> none).
type SteerAudienceResolver struct {
	// Classifier supplies the Class half of the answer (I-8, already real
	// as of CP-0 — see steer_classify.go); only the Audience half is a
	// placeholder here.
	Classifier steer.RecordClassifier
}

var _ steer.AudienceResolver = (*SteerAudienceResolver)(nil)

// NewSteerAudienceResolver returns the CP-0 stub AudienceResolver.
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
	wantKind, wantFatal := "", false
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

	// A deterministic duplicate means a previous delivery already performed
	// every externally visible effect. Repeating frames or a wake would turn
	// inbox deduplication into at-least-once behavior at the actual sinks.
	if res.Deduped {
		return steer.Delivery{MessageID: res.MessageID, Outcome: steer.DeliveryStoredNotWoken}, nil
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
