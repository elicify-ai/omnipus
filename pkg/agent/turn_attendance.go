// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// attendanceMaxHops bounds the ParentSessionID walk (#1263 R1: the same
// 16-hop, cycle-guarded walk the gateway's approval workspace derivation
// uses). Real chains are a handful of levels deep; the bound only guarantees
// termination over corrupt or cyclic on-disk meta.
const attendanceMaxHops = 16

// originUnresolved marks a chain whose root could not be read: unreadable
// meta, a missing lifecycle record with no task evidence, a cycle or the hop
// bound. It is classified unattended (#1263 R1, "fail toward the visible
// refusal") and gets the task/plan refusal wording.
const originUnresolved session.OriginKind = ""

// turnAttendance is the #1263 classification of one turn's session chain.
type turnAttendance struct {
	// Unattended is true when nobody is on the other end of the chain: an
	// Ask-policy call that Auto, an operator rule or a standing grant does
	// not settle is refused at once, never raised as an approval.
	Unattended bool
	// RootKind is the origin kind that decided the class (originUnresolved
	// when the walk failed). Only meaningful when Unattended is true.
	RootKind session.OriginKind
	// Cause explains an unresolved walk: the ancestor that failed and why
	// (gate round 1, SF-05). Empty for every resolved class.
	Cause string
}

// unattendedOrigin reports whether an origin kind starts an unattended chain
// (#1263 R1 table): task, plan, scheduled, heartbeat and verifier (Judge)
// sessions never have a person answering approvals.
func unattendedOrigin(kind session.OriginKind) bool {
	switch kind {
	case session.OriginKindTask, session.OriginKindPlan, session.OriginKindScheduled,
		session.OriginKindHeartbeat, session.OriginKindVerifier:
		return true
	}
	return false
}

// classifySessionChain walks sessionID's chain upward and classifies it by
// its root origin (#1263 R1). A helper (delegate origin) is a link, never a
// root: the walk continues to its parent, so a helper of a chat asks and a
// helper of a task does not (R3). The first non-delegate session decides —
// which also makes a task run started as a child of its assignee's main
// session (MAIN mode) a task root, not an interactive one (R5). A peer, room
// or channel message is not a child of its sender: the receiver's own main
// session is its root, so it stays interactive (R2).
//
// Persisted task evidence (meta TaskID) on any session of the chain makes it
// a task root even when its lifecycle record is gone. Every read failure,
// cycle or hop-bound overrun is unattended (R1 last row).
func (al *AgentLoop) classifySessionChain(sessionID string) turnAttendance {
	sessions := al.GetSessionStore()
	lifecycle := al.GetSessionLifecycleStore()
	if sessionID == "" || sessions == nil || lifecycle == nil {
		// No persisted session (a direct, store-less turn): nothing to
		// classify, the caller's own AutoDenyAsk stands.
		return turnAttendance{}
	}
	// unresolved fails toward the visible refusal (R1 last row) and keeps the
	// cause: it is logged at error level and carried into the refusal's audit
	// context, so an operator can tell a damaged ancestry from an intended
	// task restriction (gate round 1, SF-05).
	unresolved := func(ancestorID, reason string, err error) turnAttendance {
		cause := fmt.Sprintf("ancestor %s: %s", ancestorID, reason)
		if err != nil {
			cause += ": " + err.Error()
		}
		logger.ErrorCF("agent", "approval ancestry unresolved; the turn is refused like a task run",
			map[string]any{"session_id": sessionID, "ancestor_session_id": ancestorID, "reason": reason, "cause": cause})
		return turnAttendance{Unattended: true, RootKind: originUnresolved, Cause: cause}
	}
	seen := make(map[string]struct{}, 4)
	sid := sessionID
	for hop := 0; hop < attendanceMaxHops; hop++ {
		if _, dup := seen[sid]; dup {
			return unresolved(sid, "parent chain has a cycle", nil)
		}
		seen[sid] = struct{}{}
		meta, err := sessions.GetMeta(sid)
		if err != nil || meta == nil {
			return unresolved(sid, "session meta unreadable", err)
		}
		if meta.TaskID != "" {
			return turnAttendance{Unattended: true, RootKind: session.OriginKindTask}
		}
		rec, err := lifecycle.Load(sid)
		if err != nil || rec == nil {
			return unresolved(sid, "lifecycle record unreadable or missing", err)
		}
		if rec.Origin == nil {
			return unresolved(sid, "lifecycle record has no origin", nil)
		}
		kind := rec.Origin.Kind
		if kind == session.OriginKindDelegate {
			if meta.ParentSessionID == "" {
				return unresolved(sid, "helper lifecycle record has no parent session", nil)
			}
			sid = meta.ParentSessionID
			continue
		}
		if unattendedOrigin(kind) {
			return turnAttendance{Unattended: true, RootKind: kind}
		}
		return turnAttendance{}
	}
	return unresolved(sid, fmt.Sprintf("parent chain longer than %d hops", attendanceMaxHops), nil)
}

// applyTurnAttendance stamps the chain's class onto a turn's options before
// the turn is built (#1263 R1, R5). It only ever tightens: an engine
// dispatcher's AutoDenyAsk (schedule fire, queued drain, plan member, Judge)
// is never cleared, and a persisted root origin makes every re-entry —
// system wake, a person's message into a task session, revival after a
// restart — unattended whatever the entry point stamped. It also picks the
// refusal wording (R4): task and plan chains, and chains whose root could
// not be read, get the task/plan reason; scheduled, heartbeat and Judge
// (verifier) runs, and a dispatcher-stamped run with no persisted unattended
// root, keep the headless scheduled wording.
func (al *AgentLoop) applyTurnAttendance(opts *processOptions) {
	if opts == nil {
		return
	}
	att := al.classifySessionChain(attendanceSessionID(opts))
	if att.Unattended {
		opts.AutoDenyAsk = true
	}
	if !opts.AutoDenyAsk {
		return
	}
	switch {
	case !att.Unattended, att.RootKind == session.OriginKindScheduled,
		att.RootKind == session.OriginKindHeartbeat, att.RootKind == session.OriginKindVerifier:
		opts.askRefusalReason = autoDenyHeadlessReason
	default:
		opts.askRefusalReason = autoDenyTaskPlanReason
	}
	opts.askRefusalCause = att.Cause
}

// attendanceSessionID is the concrete session a turn's chain is classified
// from: its transcript session, or — for a turn built without one, such as
// the ordinary worker's post-turn steering drain (AgentLoop.Continue) — the
// session named by its session-scoped key "agent:<agent>:session:<id>".
// Without this, a person's late instruction into a task conversation ran its
// continuation attended (gate round 1, code-reviewer F01). A chat-scoped key
// (a channel conversation with no session yet) names no session and is left
// to the caller's own AutoDenyAsk.
func attendanceSessionID(opts *processOptions) string {
	if opts.TranscriptSessionID != "" {
		return opts.TranscriptSessionID
	}
	return sessionIDFromScopeKey(opts.SessionKey)
}
