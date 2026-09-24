package agent

import (
	"strings"
	"testing"
)

const expectedDelegateGoalGuidance = "No goal is the default; set one for multi-step work or work you must verify before relying on it, and leave it off for a quick lookup or a single action."

func TestDelegationContext_NoAwait_GoalGuidanceOnce(t *testing.T) {
	got := buildDelegationContext([]delegationTarget{{
		ID:    "worker",
		Label: "General Purpose",
	}}, 3)

	for _, retired := range []string{"async=false", "synchronously", "await mode"} {
		if strings.Contains(got, retired) {
			t.Fatalf("rendered prompt advertises retired wait-inline surface %q:\n%s", retired, got)
		}
	}
	if count := strings.Count(got, expectedDelegateGoalGuidance); count != 1 {
		t.Fatalf("goal guidance count = %d, want 1\n%s", count, got)
	}
}
