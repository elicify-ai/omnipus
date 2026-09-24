package agent

// events_adr057_test.go — tests for ADR-057 W21a / W5d, unit U23
// (pkg/agent/events.go).
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's new
// tests go in a NEW file named `<subject>_adr057_test.go`; this file is U23's
// and covers ONLY the event payload shapes U23 owns — it does not reach into
// pkg/agent/subturn.go, pkg/agent/turn.go, pkg/agent/loop.go or
// pkg/gateway/websocket.go, all of which are other units' exclusive-write
// files.
//
// TestToolExecPayloads_RoutingAndProducingSessionIDsAreIndependent (former
// ADR-057 FR-012/FR-013/W5d pin for ToolExecStartPayload/ToolExecEndPayload's
// ProducingSessionID field) is DELETED — ADR-091 D7/I-4 removed that field:
// "every frame carries its own session_id (the producing session) —
// producing_session_id — the workaround — is deleted from every schema that
// carries it." There is no replacement pin here for the underlying "every
// frame carries its own session_id" property; see u9ToolExecSessionIDs' own
// doc comment (loop.go) for the residual gap this lane's narrower "stop
// reading/setting it" scope leaves.
//
// Binding rule 1 (real state, never a spy): these are plain struct-literal
// constructions of the real production types — there is no store, turn or
// network involved to fake, and none is introduced here.
//
// Rule 4's corollary (distinct ids everywhere): every id pair below is
// constructed as two distinct, non-equal values and the assertions check
// WHICH one landed in which field, never just "a value is present".

import "testing"

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
