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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
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

// TestSetPlanStore_ReWiresSystoolsCreateTaskInWorkspace reproduces the real
// gateway boot ORDER — WireSysagentDeps with a nil PlanStore, THEN
// SetPlanStore once the real store exists — and proves create_task_in_
// workspace goes from permanently fail-closed to genuinely working, without
// ever re-registering the tool by hand (exactly what a live agent turn
// experiences: the tool instance already sitting in the registry starts
// working once boot finishes wiring it).
func TestSetPlanStore_ReWiresSystoolsCreateTaskInWorkspace(t *testing.T) {
	al, agentInst, home := newPlanToolWiringTestLoop(t)

	const workspaceID = "01JXTEST_SYSWORKSPACE0001"
	sysWiringSeedWorkspace(t, home, workspaceID)

	// Mirrors gateway.go's sysAgentDeps construction: PlanStore is NOT set
	// here, because in production it does not exist yet at this point in
	// boot (plan.New runs much later). WireSysagentDeps registers
	// create_task_in_workspace on "planner-agent" with this nil-PlanStore
	// deps snapshot — exactly like every agent gets at real boot.
	sysDeps := &systools.Deps{
		Home:             home,
		ConfigPath:       filepath.Join(home, "config.json"),
		GetCfg:           al.GetConfig,
		MutateConfig:     al.MutateConfig,
		SaveConfigLocked: func(*config.Config) error { return nil },
	}
	al.WireSysagentDeps(sysDeps)

	// Sanity: the gap really exists before the fix runs — the tool is wired
	// (present on the agent), but its own copy of Deps still has a nil
	// PlanStore, matching production's boot-order gap exactly.
	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	before := agentInst.Tools.Execute(ctx, "create_task_in_workspace", map[string]any{
		"name":         "member task",
		"workspace_id": workspaceID,
		"agent_id":     "planner-agent",
		"plan_id":      "does-not-matter",
		"criteria":     []any{map[string]any{"kind": "prose", "text": "the work is done"}},
	})
	if before == nil || !before.IsError {
		t.Fatalf("create_task_in_workspace(plan_id=...) before SetPlanStore must fail closed, got %+v", before)
	}
	if got := before.ForLLM; !containsAll(got, "plan store", "not configured") {
		t.Errorf("expected the nil-store fail-closed message, got %q", got)
	}

	// Now mirror gateway.go's later boot step: the real store is
	// constructed and installed via SetPlanStore. This is the exact
	// AgentLoop method the UAT fix extends to also re-wire sysagentDeps.
	planStore := plan.New(filepath.Join(home, "plans"))
	p := &plan.Plan{Title: "Linkage Plan", WorkspaceID: workspaceID, OwnerAgentID: "planner-agent", CreatedBy: "planner-agent"}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	al.SetPlanStore(planStore)

	// The SAME agent, the SAME already-registered tool instance from the
	// SAME registry: create_task_in_workspace(plan_id=...) must now
	// actually work, against the actual plan created above.
	after := agentInst.Tools.Execute(ctx, "create_task_in_workspace", map[string]any{
		"name":         "member task",
		"workspace_id": workspaceID,
		"agent_id":     "planner-agent",
		"plan_id":      p.ID,
		"criteria":     []any{map[string]any{"kind": "prose", "text": "the work is done"}},
	})
	if after == nil || after.IsError {
		t.Fatalf("create_task_in_workspace(plan_id=...) after SetPlanStore must succeed, got %+v", after)
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(after.ForLLM), &out); err != nil {
		t.Fatalf("could not parse create_task_in_workspace result %q: %v", after.ForLLM, err)
	}
	if out.ID == "" {
		t.Fatal("expected a non-empty created task id")
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
