// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT fix (fix/uat-defects-2026-08-22, Defect 2) regression coverage: the
// system.* tool surface (pkg/sysagent/tools) has its OWN, separate
// dependency-injection path — AgentLoop.WireSysagentDeps — distinct from the
// plain-tool late-binding SetPlanStore already covers (see
// plan_tool_wiring_test.go, one file over). In the real gateway boot
// sequence (pkg/gateway/gateway.go's RunContextWithOptions) WireSysagentDeps
// runs BEFORE the real *plan.Store is even constructed — sysAgentDeps is
// built and wired with a nil PlanStore, and only much later does
// plan.New(...) + AgentLoop.SetPlanStore(...) run. Before this fix,
// SetPlanStore re-wired the plain create_task tool (pkg/tools) but never
// touched al.sysagentDeps at all, so create_task_in_workspace
// (pkg/sysagent/tools) stayed permanently wired to a nil PlanStore and
// failed closed with "plan store is not configured" FOREVER — for every
// agent, on every install — even against a plan that had just been created
// in the very same workspace by the very same turn. This is exactly what
// the live UAT hit.
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sysWiringSeedWorkspace writes a minimal on-disk workspace JSON file so
// create_task_in_workspace's workspace_id existence check finds it — mirrors
// pkg/sysagent/tools/task_status_guard_test.go's seedWorkspace exactly (that
// helper is unexported to its own package, so this is a same-shape local
// copy rather than a cross-package reach-in).
func sysWiringSeedWorkspace(t *testing.T, home, id string) {
	t.Helper()
	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatalf("mkdir workspaces dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ws := map[string]any{
		"id": id, "name": "Test Workspace", "status": "active",
		"created_at": now, "updated_at": now,
	}
	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("marshal workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, id+".json"), data, 0o600); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
}

// containsAll reports whether s contains every one of subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
