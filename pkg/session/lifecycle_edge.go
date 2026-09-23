// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 I-1 — the steered-by edge persisted on
// LifecycleRecord, plus the small value types it is built from (Origin,
// SteeredBy, ReportingTarget, Authorization, Limits, Stop, Principal).
//
// These live in pkg/session, not pkg/steer, even though `pkg/steer`
// presents them as part of its own neutral, published shape. Reason
// (stated once here): they are
// PERSISTED fields of session.LifecycleRecord, and I-9 has this same
// package's LifecycleIndex return a report type to `pkg/steer` — so if
// pkg/steer defined these types itself, pkg/session would have to import
// pkg/steer to reference them on LifecycleRecord, while pkg/steer already
// must import pkg/session (for exactly this record shape). That is a
// cycle. The one-way resolution: pkg/session owns every type that is
// PERSISTED on the record (or returned by the index), and pkg/steer
// imports pkg/session and re-exports each one with a Go type alias (e.g.
// `type Origin = session.Origin`) so callers
// still resolve `steer.Origin` etc. for every consumer outside this
// package. pkg/steer's own, non-persisted types (LaunchRequest, WakeInput,
// Boundary, ...) are defined directly in pkg/steer.
package session

import "time"

// OriginKind discriminates what created a lifecycle record — a session's
// record-level Origin.Kind (I-1). One value per
// UnifiedSessionType plus the two kinds that have no session type of their
// own (plan, human).
type OriginKind string

const (
	OriginKindDelegate  OriginKind = "delegate"
	OriginKindTask      OriginKind = "task"
	OriginKindChat      OriginKind = "chat"
	OriginKindChannel   OriginKind = "channel"
	OriginKindScheduled OriginKind = "scheduled"
	OriginKindHeartbeat OriginKind = "heartbeat"
	OriginKindVerifier  OriginKind = "verifier"
	OriginKindPlan      OriginKind = "plan"
	OriginKindHuman     OriginKind = "human"
)

// IsValidOriginKind reports whether k is one of the nine canonical origin
// kinds (I-1).
func IsValidOriginKind(k OriginKind) bool {
	switch k {
	case OriginKindDelegate, OriginKindTask, OriginKindChat, OriginKindChannel,
		OriginKindScheduled, OriginKindHeartbeat, OriginKindVerifier, OriginKindPlan, OriginKindHuman:
		return true
	default:
		return false
	}
}

// Origin is the record-level discriminator every lifecycle record written
// by ADR-091 code carries (I-1). Nil on a record written before ADR-091 (a
// "legacy" record) — see LifecycleRecord.Origin's own doc comment and I-8's
// classifier, which treats a nil Origin as one of the signals that a
// delegate-typed record predates this ADR (legacy_delegate).
type Origin struct {
	Kind OriginKind `json:"kind"`
	// CallID is the originating `delegate` or `create_task` tool-call id —
	// the I-4 span key — for Kind == delegate or task; empty otherwise.
	CallID string `json:"call_id,omitempty"`
	// TaskID names the task record, for Kind == task; empty otherwise.
	TaskID string `json:"task_id,omitempty"`
}

// PrincipalKind discriminates who performed a steering action (I-1
// Stop.By; I-6 Canceller.CancelSubtree/Revive `by`).
type PrincipalKind string

const (
	PrincipalKindAgent PrincipalKind = "agent"
	PrincipalKindHuman PrincipalKind = "human"
)

// Principal identifies who acts on a steering action — an agent (by agent
// id) or a human (the authenticated gateway identity of the WebSocket or
// REST caller). Never constructed by a tool for the human case; the
// gateway passes it down (I-5 "Bus route authority").
type Principal struct {
	Kind PrincipalKind `json:"kind"`
	ID   string        `json:"id"`
}

// ReportingTarget is the steering session's own address — where a steered
// session's completion wakes it (I-1 SteeredBy.ReportingTarget).
type ReportingTarget struct {
	SessionID string `json:"session_id"`
	Channel   string `json:"channel,omitempty"`
	ChatID    string `json:"chat_id,omitempty"`
}

// AuthorizationMode is the gate verdict recorded at launch (I-1
// SteeredBy.Authorization.Mode).
type AuthorizationMode string

const (
	AuthorizationModeDirect AuthorizationMode = "direct"
	AuthorizationModeTask   AuthorizationMode = "task"
)

// Authorization is the delegation-authority verdict captured at launch
// (I-1 SteeredBy.Authorization).
type Authorization struct {
	Mode           AuthorizationMode `json:"mode"`
	RemainingDepth int               `json:"remaining_depth"`
}

// Limits is the creator-set resource ceiling on a steered session's
// lifetime across re-entries (I-1 SteeredBy.Limits).
type Limits struct {
	// TimeoutSeconds is the creator-set timeout; 0 means "the configured
	// default" (D9) — never a literal zero-second timeout.
	TimeoutSeconds int `json:"timeout_seconds"`
}

// SteeredBy is the durable edge naming who steers a session (I-1). Nil on
// LifecycleRecord for a session nobody steers (an ordinary_root).
type SteeredBy struct {
	// SteeringSessionID is the direct parent — the inbox owner key.
	SteeringSessionID string `json:"steering_session_id"`
	// RootSessionID is the cascade root, verified by walking the chain at
	// launch; equal to SteeringSessionID at depth 1.
	RootSessionID   string          `json:"root_session_id"`
	ReportingTarget ReportingTarget `json:"reporting_target"`
	Authorization   Authorization   `json:"authorization"`
	Limits          Limits          `json:"limits"`
	// ToolExclusions names tools this steered session may not call —
	// `switch_agent` today.
	ToolExclusions []string `json:"tool_exclusions,omitempty"`
}

// SteeringSessionID returns the direct parent named by the durable steering
// edge. Ordinary roots and damaged records have no steering session.
func (r *LifecycleRecord) SteeringSessionID() string {
	if r == nil || r.SteeredBy == nil {
		return ""
	}
	return r.SteeredBy.SteeringSessionID
}

// Stop is the durable Stop marker on a session's own record (D8). A nil
// *Stop on LifecycleRecord means no Stop has been stamped for the record.
// Written by the cascade (I-6 Canceller.CancelSubtree) on the stopped node
// and every reachable non-terminal descendant, one atomic write each.
type Stop struct {
	At         time.Time `json:"at"`
	Generation int       `json:"generation"`
	By         Principal `json:"by"`
}

// Stopped reports whether r is stopped RIGHT NOW — the live-stop predicate
// every dispatch/delivery/completion path must refuse against. r.Stop
// encodes a tri-state (D8) that no other method names, so this is the one
// place that spells out all three shapes plus the fourth,
// unreachable-by-design one:
//
//   - r.Stop == nil            -> never stopped. false.
//   - r.Stop.Generation == r.Generation -> stopped for the record's CURRENT
//     generation. Live. true.
//   - r.Stop.Generation <  r.Generation -> stopped once, on an EARLIER
//     generation, since revived (I-6 Canceller.Revive deliberately keeps
//     the old Stop marker around as inert history rather than clearing it).
//     Not live. false.
//   - r.Stop.Generation >  r.Generation -> unreachable in a record
//     persistLocked accepted (it rejects Stop.Generation > Generation at
//     the write choke point), but still representable in the Go struct —
//     e.g. via a hand-built value that never went through Persist/Mutate.
//     Treated as not-live (false): a Stop naming a generation that has not
//     happened yet cannot be "stopping" the record's current generation.
func (r *LifecycleRecord) Stopped() bool {
	if r == nil || r.Stop == nil {
		return false
	}
	return r.Stop.Generation == r.Generation
}
