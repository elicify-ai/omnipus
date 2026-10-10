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

import (
	"strings"
	"time"
)

// OriginKind discriminates what created a lifecycle record — a session's
// record-level Origin.Kind (I-1). One value per
// UnifiedSessionType plus the two kinds that have no session type of their
// own (plan, human). A new UnifiedSessionType needs its kind here too
// (guard: TestEverySessionTypeMapsToAValidOriginKind).
type OriginKind string

const (
	OriginKindDelegate OriginKind = "delegate"
	OriginKindTask     OriginKind = "task"
	OriginKindChat     OriginKind = "chat"
	OriginKindChannel  OriginKind = "channel"
	// OriginKindMain is the standing main session of an eligible
	// (workspace, agent) pair (session-core FR-002/C-MAIN).
	OriginKindMain      OriginKind = "main"
	OriginKindScheduled OriginKind = "scheduled"
	OriginKindHeartbeat OriginKind = "heartbeat"
	OriginKindVerifier  OriginKind = "verifier"
	OriginKindPlan      OriginKind = "plan"
	OriginKindHuman     OriginKind = "human"
)

// IsValidOriginKind reports whether k is one of the ten canonical origin
// kinds (I-1).
func IsValidOriginKind(k OriginKind) bool {
	switch k {
	case OriginKindDelegate, OriginKindTask, OriginKindChat, OriginKindChannel, OriginKindMain,
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

// Stopped reports whether r is stopped RIGHT NOW — the predicate every
// dispatch/delivery/completion/revival path must treat as "durably stopped,"
// whether the stop is still in flight or has already landed and cleared.
// Stopped checks landed state OR the current fence — two independent ways in:
//
//   - r.State == LifecycleStopped -> LANDED. TransitionSession
//     (lifecycle_bridge.go) clears r.Stop the instant it lands this state,
//     keeping only the retained StopNote — so this check alone must catch
//     the landed-and-cleared shape; it does not require r.Stop to be set.
//   - the live Stop fence, below -> not yet landed, still in flight.
//
// r.Stop itself encodes a tri-state (D8) that no other method names, so
// this is the one place that spells out all three shapes plus the fourth,
// unreachable-by-design one:
//
//   - r.Stop == nil            -> no live fence. false (falls through to
//     the landed-state check above).
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
	if r == nil {
		return false
	}
	if r.State == LifecycleStopped {
		return true
	}
	return r.Stop != nil && r.Stop.Generation == r.Generation
}

// StopCause is the closed vocabulary naming WHY a session last landed
// LifecycleStopped (sub-agent control plane ADR, D2/D6/Vocabulary lines
// 133/137). These five values are the ONLY legal ones — persistLocked
// rejects any other string.
type StopCause string

const (
	// StopCauseStop is a direct single-session stop — the session named was
	// itself the target (a human's Stop button on a steered session, or the
	// agent's own delegate cancel naming this session_id), not swept in as
	// someone else's descendant.
	StopCauseStop StopCause = "stop"
	// StopCauseRedirectPause is a fresh instruction pausing the CURRENT
	// generation (D2 line 261's `redirect` control) rather than stopping the
	// session outright.
	StopCauseRedirectPause StopCause = "redirect_pause"
	// StopCauseCascade is a descendant reached (and stamped) by an ancestor's
	// Stop/Stop-all cascade — never the cascade's own direct target.
	StopCauseCascade StopCause = "cascade"
	// StopCauseRestart is boot recovery reverting an interrupted
	// queued/running record, or a goal loop retiring an attempt's session in
	// favor of a freshly-minted one.
	StopCauseRestart StopCause = "restart"
	// StopCauseTimeout is a session's own lifetime execution budget
	// (SteeredBy.Limits.TimeoutSeconds) running out mid-turn.
	StopCauseTimeout StopCause = "timeout"
)

// validStopCauses is the set backing IsValidStopCause.
var validStopCauses = map[StopCause]bool{
	StopCauseStop:          true,
	StopCauseRedirectPause: true,
	StopCauseCascade:       true,
	StopCauseRestart:       true,
	StopCauseTimeout:       true,
}

// IsValidStopCause reports whether c is one of the five canonical stop
// causes (D2/D6).
func IsValidStopCause(c StopCause) bool { return validStopCauses[c] }

// StopNote is the durable, LASTING record of who stopped a session, when,
// and why (sub-agent control plane ADR D2/D6, Vocabulary lines 133/137;
// CRIT-001 line ~209). It is distinct from Stop (the in-flight dispatch
// fence above): Stop is cleared the instant the stop it names is carried
// out, while StopNote is written at the SAME moment (for a cause that
// stamps a fence — stop/cascade/redirect_pause) or at landing time (for a
// cause with no fence step — restart/timeout), and then RETAINED across the
// fence's own clearing — a direct parent's stopped-child notice, and any
// later observer, reads StopNote for who/why/when, never Stop. nil means
// this record has never landed LifecycleStopped for any generation.
//
// Seq is stamped from the record's OWN Generation at the moment of write —
// a documented stand-in until the per-session control ledger (MAJ-009,
// "Controls") exists and can supply a true per-control monotonic sequence;
// every site that sets StopNote today derives Seq the same way, so swapping
// in the real ledger later is a one-line change per site, not a shape
// change.
type StopNote struct {
	At    time.Time `json:"at"`
	By    string    `json:"by"`
	Seq   uint64    `json:"seq"`
	Cause StopCause `json:"cause"`
	// BootSeq is the boot epoch of the boot that WROTE this note — the
	// monotonic boot counter persisted in the data dir (sub-agent control
	// plane ADR D8.3). Present (>= 1) on restart notes; zero — omitted on
	// the wire — otherwise, matching StopNote.yaml's optional
	// absent-not-null integer/int64/minimum-1 shape. No writer stamps it
	// yet: the boot-epoch store, persist-time range/restart validation and
	// the first-act-of-boot mint are separate later units, so every note
	// written today still omits the key.
	BootSeq uint64 `json:"boot_seq,omitempty"`
}

// StopActorSystem names a stop_note.by actor with no session.Principal
// behind it — a lifetime-budget timeout or a goal-loop attempt
// supersession, neither of which is performed BY a human or agent principal
// the way stop/cascade/redirect_pause are. A boot-recovery restart is NOT a
// StopActorSystem note: per sub-agent control plane ADR D8.3 it carries the
// StopActorRestart literal below.
const StopActorSystem = "system"

// StopActorRestart is the stop_note.by actor literal for a boot-recovery
// restart note (sub-agent control plane ADR D8.3: the note is
// {at, by: "restart", seq, cause: "restart", boot_seq}). Distinct from
// StopActorSystem: the goal loop's non-boot attempt supersession also
// writes cause=restart today but keeps StopActorSystem — that split is
// deliberate (no writer stamps boot_seq on it), and the eventual
// boot-sequence validation must not read cause=restart as proof of a boot
// restart while that writer exists.
const StopActorRestart = "restart"

// StopActorFromPrincipal formats p as a stop_note.by display string
// ("human:<id>" / "agent:<id>"). Used at every site that has a real
// session.Principal (steer_cancel.go's cascade, via steer.Principal — a
// type alias for this same type) rather than only a looser identity.
func StopActorFromPrincipal(p Principal) string {
	kind := string(p.Kind)
	id := strings.TrimSpace(p.ID)
	switch {
	case kind == "" && id == "":
		return ""
	case id == "":
		return kind
	case kind == "":
		return id
	default:
		return kind + ":" + id
	}
}

// StopActorAgent formats an agent id as a stop_note.by display string for a
// call site that has only a bare agent id (e.g. tools.ToolAgentID), not a
// full Principal.
func StopActorAgent(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "agent"
	}
	return "agent:" + agentID
}

// StopActorHumanUser formats a gateway/channel user id as a stop_note.by
// display string for a call site that has only a bare user id (e.g.
// CancelCanceller.UserID), not a full Principal.
func StopActorHumanUser(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "human"
	}
	return "human:" + userID
}
