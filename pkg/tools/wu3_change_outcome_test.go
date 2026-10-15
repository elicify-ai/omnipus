package tools

import (
	"encoding/json"
	"testing"
)

// FR-030 + ruling 2026-10-14T18:40Z: a refusal is "failed: nothing saved"
// with changed_fields [], for JSON and plain-text results alike; a result that
// already carries a verdict is left alone.
func TestWU3FinalizeChangeResult_Shapes(t *testing.T) {
	jsonRefusal := FinalizeChangeResult("t", ErrorResult(`{"success":false,"error":{"code":"X","message":"bad input"}}`))
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonRefusal.ForLLM), &m); err != nil {
		t.Fatalf("JSON refusal stays JSON: %v %s", err, jsonRefusal.ForLLM)
	}
	if m["outcome"] != OutcomeFailedNothingSaved || m["reason"] != "bad input" {
		t.Fatalf("JSON refusal verdict wrong: %s", jsonRefusal.ForLLM)
	}
	if cf, ok := m["changed_fields"].([]any); !ok || len(cf) != 0 {
		t.Fatalf("changed_fields must be [] on failed: nothing saved, got %v", m["changed_fields"])
	}

	text := FinalizeChangeResult("t", ErrorResult(`scope must be "workspace" or "shared"`))
	var tm map[string]any
	if err := json.Unmarshal([]byte(text.ForLLM), &tm); err != nil {
		t.Fatalf("a plain-text refusal becomes the structured FR-030 object: %v %s", err, text.ForLLM)
	}
	errBlock, _ := tm["error"].(map[string]any)
	if errBlock["message"] != `scope must be "workspace" or "shared"` || tm["outcome"] != OutcomeFailedNothingSaved {
		t.Fatalf("plain-text refusal must keep its text as error.message with the verdict: %s", text.ForLLM)
	}
	if cf, ok := tm["changed_fields"].([]any); !ok || len(cf) != 0 {
		t.Fatalf("changed_fields must be []: %s", text.ForLLM)
	}

	partial := ChangeErrorResult(map[string]any{"changed_fields": []string{"a"}}, ChangePartlySaved("r", "", []string{"b"}))
	before := partial.ForLLM
	if FinalizeChangeResult("t", partial).ForLLM != before {
		t.Fatal("a result that already carries a verdict must not change")
	}
}

// Chief ruling Q3 (2026-10-15): exit 0 is "working" (not readiness); any
// other terminal state is "partly saved"; a running session has no verdict.
func TestWU3EnvironmentSetupOutcome(t *testing.T) {
	o, ok := environmentSetupOutcome(string(StatusDone), 0)
	if !ok || o.Outcome != OutcomeWorking || o.CheckScope != "installer command exited 0" ||
		len(o.NotTested) != 1 || o.NotTested[0] != "readiness inside the agent's sandbox" {
		t.Fatalf("exit 0: %+v", o)
	}
	o, ok = environmentSetupOutcome(string(StatusDone), 3)
	if !ok || o.Outcome != OutcomePartlySaved || o.Reason != "installer exited 3; side effects not proven absent" {
		t.Fatalf("exit 3: %+v", o)
	}
	for _, s := range []SessionStatus{StatusKilled, StatusTimeout, StatusCanceled} {
		if o, ok := environmentSetupOutcome(string(s), -1); !ok || o.Outcome != OutcomePartlySaved {
			t.Fatalf("%s: %+v", s, o)
		}
	}
	if _, ok := environmentSetupOutcome(string(StatusRunning), 0); ok {
		t.Fatal("a running session has no verdict")
	}
}
