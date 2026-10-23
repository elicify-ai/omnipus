// #1431 gate T3 / CHECK F4. Caller instructions are part of this tool's
// contract, not a prose snapshot. Oracles: ADR-084 section 13 / E-28a.
package tools

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestGoalClaimTool_InstructionsDescribeRequiredButPossiblyEmptyEvidence(t *testing.T) {
	tool := newGoalClaimTool(newFakeGoalRecordAccess())
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatal("goal_claim parameters must publish properties")
	}
	evidence, ok := properties["evidence"].(map[string]any)
	if !ok {
		t.Fatal("goal_claim must publish the evidence parameter")
	}
	parameterDescription, ok := evidence["description"].(string)
	if !ok {
		t.Fatal("evidence must have caller-facing instructions")
	}
	for name, text := range map[string]string{"tool description": tool.Description(), "evidence parameter": parameterDescription} {
		t.Run(name, func(t *testing.T) {
			// Require the separate semantic obligations, not a complete fixed
			// paragraph. Wording may vary; the retired refusal must not survive.
			lower := strings.ToLower(text)
			obligations := map[string]string{
				"mandatory evidence presence": `(?s)(required|mandatory|always include|must include).*(evidence|field|schema)|evidence.*(required|mandatory)`,
				"empty evidence is accepted":  `(?s)(empty|no physical evidence|no physical proof).*(accepted|claim|adjudicat|judge|met)|(?:accept|may be).*empty`,
				"conversation evidence":       `conversation`,
				"tool-result evidence":        `tool[ -]results`,
				"unmet ruling consumes round": `(?s)unmet.*(round|try|attempt)|(round|try|attempt).*unmet`,
			}
			for obligation, pattern := range obligations {
				if !regexp.MustCompile(pattern).MatchString(lower) {
					t.Errorf("ADR-084 section 13: %s does not explain %s: %q", name, obligation, text)
				}
			}
			for _, retired := range []string{"non-empty evidence argument", "required and non-empty", "before anything is recorded", "it is optional", "call is refused"} {
				if strings.Contains(lower, retired) {
					t.Errorf("ADR-084 section 13: %s still advertises retired empty-evidence refusal/optional presence %q: %q", name, retired, text)
				}
			}
		})
	}
}

func TestGoalClaimRegistry_EvidencePresenceIsRequiredForEveryStatus(t *testing.T) {
	for _, status := range []string{"met", "blocked", "waiting_on_user"} {
		for _, value := range []struct {
			name    string
			present bool
			text    string
		}{
			{name: "omitted"},
			{name: "present-empty", present: true},
			{name: "whitespace", present: true, text: " \n\t "},
			{name: "reason", present: true, text: "  report.md was checked  "},
		} {
			t.Run(status+"/"+value.name, func(t *testing.T) {
				const sid, gid = "session-gap1431-registry", "goal-gap1431-registry"
				access := newFakeGoalRecordAccess()
				access.goalID[sid], access.condition[sid] = gid, "check the report"
				registry := NewToolRegistry()
				registry.Register(newGoalClaimTool(access))
				args := map[string]any{"status": status}
				if value.present {
					args["evidence"] = value.text
				}
				res := registry.ExecuteWithContext(setGoalCtx(sid, "mia"), GoalClaimToolName, args, "webchat", "c1", nil)
				if res == nil {
					t.Fatal("real registry returned nil")
				}
				if !value.present {
					const want = `invalid arguments for tool "goal_claim": missing required property "evidence"`
					if !res.IsError || res.ForLLM != want {
						t.Fatalf("omitted evidence for %s: error=%t text=%q, want dispatch error %q", status, res.IsError, res.ForLLM, want)
					}
					return
				}
				if res.IsError {
					t.Fatalf("present evidence %q for %s must reach Execute: %q", value.text, status, res.ForLLM)
				}
				var payload map[string]any
				if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
					t.Fatalf("decode successful claim: %v", err)
				}
				want := map[string]any{"status": status, "goal_id": gid}
				trimmed := strings.TrimSpace(value.text)
				// Section 13 retains blocked/waiting reason handling; their
				// empty reason is absent, while met always records its evidence.
				if status == "met" || trimmed != "" {
					want["evidence"] = trimmed
				}
				if !reflect.DeepEqual(payload, want) {
					t.Errorf("recorded %s claim=%#v, want %#v", status, payload, want)
				}
				if access.writes != 0 {
					t.Errorf("goal_claim performed %d direct durable writes, want zero (the engine owns recording)", access.writes)
				}
			})
		}
	}
}
