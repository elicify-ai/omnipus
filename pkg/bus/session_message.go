// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-053 §Contract Surface (S3) — the typed SessionMessage transport rides
// the EXISTING MessageBus (this file), not a new bus/channel. It is the
// fourth in-process channel this struct carries (alongside inbound/outbound/
// outboundMedia) — same struct, same publish/close discipline — never a
// second, parallel messaging mechanism (delivery brief DoD-11 anti-drift).
package bus

import (
	"context"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// sessionMessageBufferSize mirrors defaultBusBufferSize — the session-
// message volume is lower than chat inbound/outbound traffic (rate-capped
// at the sender, ADR-053 §Contract Surface "Caps") but the same headroom
// keeps a burst of children reporting in from blocking a publisher.
const sessionMessageBufferSize = defaultBusBufferSize

// SessionMessageEvent is the bus-level delivery unit for the typed
// SessionMessage transport (S3). It wraps the generated, discriminated-union
// SessionMessage — the SAME shape delegate.inbox/status and the SPA consume
// (DoD-11, never a second/narrower envelope) — with the bus-routing
// metadata needed to get it to the right consumer.
type SessionMessageEvent struct {
	// TargetSessionID is the durable session_id this event is addressed to
	// FOR DELIVERY purposes:
	//   - parent_to_child kinds (steer, respond): the CHILD's session_id —
	//     the consumer injects this at the child's next tool boundary via
	//     the (generalized) steering queue.
	//   - child_to_parent kinds (progress/checkpoint/artifact/blocker/
	//     question/decision_request/error/handback): the PARENT's owner key
	//     (D16 — durable chat/plan id) the durable inbox is keyed to. The
	//     child's OWN session_id still lives on Message itself
	//     (Message.session_id, via the envelope).
	//   - session_to_ui / engine kinds (goal_status, revision_entry): the
	//     session_id the frame is reporting on, for WS since-cursor replay
	//     routing (Phase-2 consumer).
	TargetSessionID string
	Message         generated.SessionMessage
}

// PublishSessionMessage publishes evt on the bus's dedicated session-message
// channel. Mirrors PublishInbound's blocking-with-ctx/done-guard semantics
// exactly (same publish[T] helper, same TOCTOU-safe closed check).
func (mb *MessageBus) PublishSessionMessage(ctx context.Context, evt SessionMessageEvent) error {
	return publish(ctx, mb, mb.sessionMessages, evt)
}

// SessionMessageChan returns the read side of the session-message channel.
// A consumer (pkg/agent's SessionMessage router — see steering.go's
// DeliverSessionMessage/async_notifier.go's WakeParent, the two hooks this
// wave built to consume a drained event) ranges over this exactly like the
// existing InboundChan/OutboundChan consumers do.
func (mb *MessageBus) SessionMessageChan() <-chan SessionMessageEvent {
	return mb.sessionMessages
}
