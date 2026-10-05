package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// These adapters preserve the existing record/tool assertions while requiring
// successful reads explicitly. A read fault must not satisfy an absence test.
func mustActiveGoalForSession(t testing.TB, sessionID string) *goal.Goal {
	t.Helper()
	rec, err := activeGoalForSession(sessionID)
	if err != nil {
		t.Fatalf("activeGoalForSession(%q): %v", sessionID, err)
	}
	return rec
}

func mustBuildCompressedToolDefs(t testing.TB, al *AgentLoop, ts *turnState, filtered []tools.Tool) []providers.ToolDefinition {
	t.Helper()
	defs, err := al.buildCompressedToolDefs(ts, filtered)
	if err != nil {
		t.Fatalf("buildCompressedToolDefs: %v", err)
	}
	return defs
}

func mustGoalRoute(t testing.TB, state *goalTriggerState, sessionID string) goalRoute {
	t.Helper()
	route, err := state.routeFor(sessionID)
	if err != nil {
		t.Fatalf("routeFor(%q): %v", sessionID, err)
	}
	return route
}

func mustAfterGoalRecordWrite(t testing.TB, al *AgentLoop, sessionID, recordJSON, diffSummary string) {
	t.Helper()
	if err := al.afterGoalRecordWrite(sessionID, recordJSON, diffSummary); err != nil {
		t.Fatalf("afterGoalRecordWrite(%q): %v", sessionID, err)
	}
}
