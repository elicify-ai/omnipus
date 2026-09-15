// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

// RoutingLostReason is GOAL-FR-035's persisted note (S-39): when a keeper
// follow-up has no routing resolvable anywhere — neither the in-memory
// trigger state nor this record — the goal gets this exact LatestReason
// value written once. It is exported so the engine wave that dispatches the
// follow-up (E12's re-point of pkg/agent/goal_triggers.go::routeFor) and
// this package's own RecordRoutingLost share ONE string, never two literals
// that can drift apart.
const RoutingLostReason = "keeper cannot reach the goal's channel — routing lost"

// SetRoute persists channel/chatID as the goal's keeper follow-up delivery
// address (GOAL-FR-034). dispatchGoalAsyncFollowUp aborts without either,
// so the two fields are always set together, never independently.
//
// Two fields from the pre-existing four-field session-meta shape do NOT
// survive onto this record:
//   - the session key (GOAL-FR-032) — persisted by the old
//     goal_route_session_key meta field but read by nothing; deleted, not
//     migrated.
//   - the routing agent id (GOAL-FR-033) — folded into the goal's own
//     owner reference (OwnerKind/OwnerID, GOAL-FR-002) instead of being
//     persisted a second time. The engine derives the agent that should
//     carry a keeper follow-up from the goal's owner (the session or task
//     it is bound to), not from a redundant field on the routing record.
func (g *Goal) SetRoute(channel, chatID string) {
	g.RouteChannel = channel
	g.RouteChatID = chatID
}

// HasRoute reports whether the goal carries a resolvable keeper follow-up
// address (GOAL-FR-034). Both RouteChannel and RouteChatID are required
// together — a goal with only one set is not resolvable, matching
// dispatchGoalAsyncFollowUp's own "aborts without either" rule.
func (g *Goal) HasRoute() bool {
	return g.RouteChannel != "" && g.RouteChatID != ""
}

// RecordRoutingLost is GOAL-FR-035's persisted half (S-39): a keeper
// follow-up whose routing cannot be resolved on either side gets a one-line
// reason written to the record, with two deliberate exceptions carried over
// unchanged from the pre-existing session-meta behaviour this replaces
// (pkg/agent/goal_triggers.go::routeFor's review-round-1 finding #10):
//
//   - it never overwrites a fresher, more informative reason already there
//     (e.g. a real judge verdict reason written earlier in the same settle
//     pass) — it writes ONLY when LatestReason is currently empty;
//   - it never re-writes the identical note on every idle cycle — calling
//     it again while LatestReason already equals RoutingLostReason is a
//     no-op.
//
// It reports whether it actually wrote, so the caller (the engine's
// routeFor) can gate its own one-time WARN log on a genuine state change
// rather than warning on every tick.
func (g *Goal) RecordRoutingLost() (wrote bool) {
	switch g.LatestReason {
	case RoutingLostReason:
		return false // already says routing-lost — no churn.
	case "":
		g.LatestReason = RoutingLostReason
		return true
	default:
		return false // holds a real, more informative reason — never stomp.
	}
}
