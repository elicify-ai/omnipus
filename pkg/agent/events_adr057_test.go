package agent

// events_adr057_test.go — tests for ADR-057 W21a / W5d, unit U23
// (pkg/agent/events.go).
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's new
// tests go in a NEW file named `<subject>_adr057_test.go`; this file is U23's
// and covers ONLY the event payload shapes U23 owns — it does not reach into
// pkg/agent/subturn.go, pkg/agent/turn.go, pkg/agent/loop.go or
// pkg/gateway/websocket.go, all of which are other units' exclusive-write
// files that will ASSIGN into the fields pinned/added here in later waves
// (U7, U3/U9, U11 respectively). U23's own scope is the struct shape: that
// the routing id (SessionID) and the producing id (ProducingSessionID) are
// two independently-settable fields that can carry genuinely different
// values at the same time — the defect this migration exists to fix is that
// today a single `session_id` conflates both.
//
// Binding rule 1 (real state, never a spy): these are plain struct-literal
// constructions of the real production types — there is no store, turn or
// network involved to fake, and none is introduced here.
//
// Rule 4's corollary (distinct ids everywhere): every id pair below is
// constructed as two distinct, non-equal values and the assertions check
// WHICH one landed in which field, never just "a value is present".

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestToolExecPayloads_RoutingAndProducingSessionIDsAreIndependent pins
// ADR-057 FR-012/FR-013 (W5d) for ToolExecStartPayload and
// ToolExecEndPayload — tool_call_start/tool_call_result are class (a) per
// the W5 audit (FR-089, BDD-16): a child turn genuinely emits them, so both
// ids must be carriable simultaneously and distinctly.
//
//	Given  a tool call executing several delegation levels deep
//	When   the emitting code sets SessionID to the delegation's routing id
//	       and ProducingSessionID to the executing turn's own real session
//	Then   both fields read back their own distinct value — setting one did
//	       not alias or overwrite the other
//	And    a root-turn call (producing == routing) leaves ProducingSessionID
//	       at its zero value, which is what lets a consumer of this payload
//	       implement FR-013's "present iff it differs from session_id" rule
//	       with a simple non-empty-and-unequal check
func TestToolExecPayloads_RoutingAndProducingSessionIDsAreIndependent(t *testing.T) {
	const (
		routingID   = "sess_routing_root_a1b2"
		producingID = "sess_producing_child_c3d4"
	)
	if routingID == producingID {
		t.Fatal("fixture defect: routing and producing ids must be distinct")
	}

	t.Run("ToolExecStartPayload, delegated (producing differs from routing)", func(t *testing.T) {
		p := ToolExecStartPayload{
			SessionID:          routingID,
			ProducingSessionID: session.SessionID(producingID),
		}
		if p.SessionID != routingID {
			t.Fatalf("SessionID: got %q, want the routing id %q", p.SessionID, routingID)
		}
		if string(p.ProducingSessionID) != producingID {
			t.Fatalf("ProducingSessionID: got %q, want the producing id %q", p.ProducingSessionID, producingID)
		}
		if p.SessionID == string(p.ProducingSessionID) {
			t.Fatal("routing and producing ids collapsed to the same value — the fields are not independent")
		}
		// FR-013's "present iff it differs" predicate, as a consumer (U11)
		// would evaluate it: non-empty and unequal to the routing key.
		if present := p.ProducingSessionID != "" && string(p.ProducingSessionID) != p.SessionID; !present {
			t.Fatal("FR-013 predicate: producing_session_id should be present for a delegated call")
		}
	})

	t.Run("ToolExecStartPayload, root turn (producing == routing, left absent)", func(t *testing.T) {
		p := ToolExecStartPayload{
			SessionID: routingID,
			// ProducingSessionID intentionally left at its zero value.
		}
		if p.SessionID != routingID {
			t.Fatalf("SessionID: got %q, want %q", p.SessionID, routingID)
		}
		if p.ProducingSessionID != "" {
			t.Fatalf("ProducingSessionID: got %q, want the zero value for a root turn", p.ProducingSessionID)
		}
		if present := p.ProducingSessionID != "" && string(p.ProducingSessionID) != p.SessionID; present {
			t.Fatal("FR-013 predicate: producing_session_id should be absent when producing == routing")
		}
	})

	t.Run("ToolExecEndPayload, delegated (producing differs from routing)", func(t *testing.T) {
		p := ToolExecEndPayload{
			SessionID:          routingID,
			ProducingSessionID: session.SessionID(producingID),
		}
		if p.SessionID != routingID {
			t.Fatalf("SessionID: got %q, want the routing id %q", p.SessionID, routingID)
		}
		if string(p.ProducingSessionID) != producingID {
			t.Fatalf("ProducingSessionID: got %q, want the producing id %q", p.ProducingSessionID, producingID)
		}
		if p.SessionID == string(p.ProducingSessionID) {
			t.Fatal("routing and producing ids collapsed to the same value — the fields are not independent")
		}
	})

	t.Run("ToolExecEndPayload, root turn (producing == routing, left absent)", func(t *testing.T) {
		p := ToolExecEndPayload{
			SessionID: routingID,
		}
		if p.SessionID != routingID {
			t.Fatalf("SessionID: got %q, want %q", p.SessionID, routingID)
		}
		if p.ProducingSessionID != "" {
			t.Fatalf("ProducingSessionID: got %q, want the zero value for a root turn", p.ProducingSessionID)
		}
	})
}

// TestSubTurnPayloads_SessionIDIsRoutingScopedDistinctFromChildLabel pins
// ADR-057 FR-017 (W21a) for SubTurnSpawnPayload and SubTurnEndPayload:
// SessionID MUST carry the delegation's routing id (the parent's), never the
// child's own session, even though the child's own identity is available
// elsewhere on the very same payload (Label). subagent_start/subagent_end
// are class (b) per the W5 audit (FR-089, BDD-98) — producing always equals
// routing for these two frame types by construction (FR-017), which is why
// neither struct carries a ProducingSessionID sibling field.
//
//	Given  a spawn/end event for a child turn whose own session id differs
//	       from the delegation's routing id
//	When   the payload is constructed with SessionID set to the routing id
//	       and Label set to the child's own identifier
//	Then   SessionID reads back the routing id, not the child's
//	And    Label independently reads back the child's own identifier —
//	       proving the two concepts coexist on this payload without either
//	       field being repointed to carry the other's value
func TestSubTurnPayloads_SessionIDIsRoutingScopedDistinctFromChildLabel(t *testing.T) {
	const (
		routingID = "sess_routing_root_e5f6"
		childID   = "child_delegate_g7h8"
	)
	if routingID == childID {
		t.Fatal("fixture defect: routing id and child id must be distinct")
	}

	t.Run("SubTurnSpawnPayload", func(t *testing.T) {
		p := SubTurnSpawnPayload{
			SessionID: routingID,
			Label:     childID,
		}
		if p.SessionID != routingID {
			t.Fatalf("SessionID: got %q, want the routing id %q — must not be repointed to the child", p.SessionID, routingID)
		}
		if p.Label != childID {
			t.Fatalf("Label: got %q, want the child id %q", p.Label, childID)
		}
		if p.SessionID == p.Label {
			t.Fatal("SessionID and Label collapsed to the same value — fixture does not discriminate")
		}
	})

	t.Run("SubTurnEndPayload", func(t *testing.T) {
		p := SubTurnEndPayload{
			SessionID: routingID,
		}
		if p.SessionID != routingID {
			t.Fatalf("SessionID: got %q, want the routing id %q — must not be repointed to the child", p.SessionID, routingID)
		}
	})
}
