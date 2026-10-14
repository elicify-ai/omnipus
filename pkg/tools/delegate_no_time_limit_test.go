package tools

import (
	"context"
	"strings"
	"testing"
)

// TestDelegateRun_IgnoresRetiredTimeoutSeconds pins issue #1306 at the tool
// boundary: a delegation has no time limit, but models (and replayed
// transcripts) still pass timeout_seconds out of habit — including the 120 s
// and 900 s values seen live and values the old bounds check refused. Every
// such call must be ACCEPTED and the value ignored: the launch goes ahead and
// nothing about a time limit reaches the launcher.
func TestDelegateRun_IgnoresRetiredTimeoutSeconds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"jarvis_120", float64(120)},
		{"jarvis_900", float64(900)},
		{"zero", float64(0)},
		{"above_old_max", float64(99999)},
		{"negative", float64(-5)},
		{"not_a_number", "soon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, launcher := u14PermissiveTool(t)
			ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "mia")
			res := tool.Execute(ctx, map[string]any{
				"task":            "long running work",
				"agent_id":        "worker",
				"timeout_seconds": tc.value,
			})
			if res == nil || res.IsError {
				t.Fatalf("delegate run with timeout_seconds=%v was refused: %+v (issue #1306: accept and ignore)", tc.value, res)
			}
			if launcher.launchReq.Task != "long running work" {
				t.Fatalf("launcher was not reached with the task: %+v", launcher.launchReq)
			}
		})
	}
}

// TestDelegateTool_DescriptionHasNoTimeLimit: the model-facing description no
// longer promises a forced stop after a timeout.
func TestDelegateTool_DescriptionHasNoTimeLimit(t *testing.T) {
	desc := NewDelegateTool("test-model", 0, 0).Description()
	// The deleted sentence must leave the surrounding text intact.
	if !strings.Contains(desc, "stop it with delegate stop_all. A run has two modes.") {
		t.Error("delegate description is broken around the removed time-limit sentence")
	}
	for _, stale := range []string{"timeout_seconds", "force-stopped", "1800s"} {
		if strings.Contains(desc, stale) {
			t.Errorf("delegate description still mentions %q; delegations have no time limit (issue #1306)", stale)
		}
	}
}
