package tools

import (
	"context"
	"testing"
)

// TestDelegateRun_SnapshotReachesLauncher: issue #1212 — snapshot.notes and
// snapshot.references were validated and then dropped; the launch request
// the launcher receives must carry both.
func TestDelegateRun_SnapshotReachesLauncher(t *testing.T) {
	tool, launcher := u14PermissiveTool(t)
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "worker")
	result := tool.Execute(ctx, map[string]any{
		"action":   "run",
		"agent_id": "worker",
		"label":    "Inspect",
		"task":     "Inspect the checkout flow",
		"snapshot": map[string]any{
			"notes":      "MARKER-NOTES-7731",
			"references": []any{"docs/marker-ref-7731.md", "pkg/checkout/cart.go"},
		},
	})
	if result.IsError {
		t.Fatalf("delegate(run) returned error: %s", result.ForLLM)
	}
	if got, want := launcher.launchReq.ContextNotes, "MARKER-NOTES-7731"; got != want {
		t.Fatalf("launch ContextNotes = %q, want %q", got, want)
	}
	refs := launcher.launchReq.ContextReferences
	if len(refs) != 2 || refs[0] != "docs/marker-ref-7731.md" || refs[1] != "pkg/checkout/cart.go" {
		t.Fatalf("launch ContextReferences = %v, want the two named references in order", refs)
	}
}

// TestDelegateRun_NoSnapshotLeavesLaunchContextEmpty: no snapshot argument
// means no context on the launch request.
func TestDelegateRun_NoSnapshotLeavesLaunchContextEmpty(t *testing.T) {
	tool, launcher := u14PermissiveTool(t)
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "worker")
	result := tool.Execute(ctx, map[string]any{
		"action": "run", "agent_id": "worker", "label": "Inspect", "task": "Inspect",
	})
	if result.IsError {
		t.Fatalf("delegate(run) returned error: %s", result.ForLLM)
	}
	if launcher.launchReq.ContextNotes != "" || len(launcher.launchReq.ContextReferences) != 0 {
		t.Fatalf("launch carried context without a snapshot: %q %v", launcher.launchReq.ContextNotes, launcher.launchReq.ContextReferences)
	}
}
