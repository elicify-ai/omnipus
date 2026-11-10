package generated

import "testing"

// Gate round 1, code-reviewer F04: bundle B3 made tool_call_id, turn_id and
// args required on SessionStatePendingApproval. The positive fixtures carry
// them; these negative cases keep proving the requiredness is enforced, so
// the positive fixtures cannot pass by the schema quietly loosening.
func TestTrain3Fix1_SessionStatePendingApprovalB3FieldsRequired(t *testing.T) {
	cases := map[string]func(*SessionStatePendingApproval){
		"missing tool_call_id": func(p *SessionStatePendingApproval) { p.ToolCallId = "" },
		"missing turn_id":      func(p *SessionStatePendingApproval) { p.TurnId = "" },
		"null args":            func(p *SessionStatePendingApproval) { p.Args = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			frame := FixtureSessionStateFrame_Populated()
			mutate(&frame.PendingApprovals[0])
			mustFailComponent(t, "SessionStateFrame", frame, "B3: "+name+" must be refused")
		})
	}
}
