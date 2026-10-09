// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT fix regression coverage (fix/uat-defects-2026-08-22, Defect 2), restored
// on the session-core U9 branch after its original consumer was retired.
//
// The system.* tool surface (pkg/sysagent/tools) has its OWN dependency path:
// AgentLoop.WireSysagentDeps, distinct from the plain-tool late-binding that
// SetPlanStore also covers (see plan_tool_wiring_test.go, one file over). In the
// real gateway boot sequence WireSysagentDeps runs BEFORE the real *plan.Store
// is constructed — sysAgentDeps is built and wired with a nil PlanStore, and
// only much later does plan.New(...) + AgentLoop.SetPlanStore(...) run.
//
// The deleted TestSetPlanStore_ReWiresSystoolsCreateTaskInWorkspace was the
// ONLY coverage that SetPlanStore re-wires al.sysagentDeps.PlanStore; its
// consumer (create_task_in_workspace) was retired by DEL-23. This test restores
// that coverage through the REMAINING live consumer: delete_agent's
// active-plan ownership guard (pkg/sysagent/tools/agent.go, agentOwnsActivePlan),
// which is SKIPPED — fails OPEN — whenever t.deps.PlanStore is nil.
//
// Oracle (derived from the guard's documented contract, pkg/sysagent/tools/
// agent.go::validateAndLoad): with a nil PlanStore the guard is skipped, so
// delete_agent proceeds; once SetPlanStore re-wires sysagentDeps.PlanStore, the
// SAME already-registered delete_agent tool must consult the real store and
// refuse an agent that owns a running plan with AGENT_OWNS_ACTIVE_PLANS.

package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// u9rAgentRevision returns the current revision of an agent entity record, the
// value delete_agent requires.
func u9rAgentRevision(t *testing.T, store *agentstore.Store, id string) string {
	t.Helper()
	state, err := store.ReadState(id)
	if err != nil {
		t.Fatalf("read revision of agent %q: %v", id, err)
	}
	return state.Revision
}

// TestSetPlanStore_ReWiresSysagentPlanStoreForDeleteAgentActivePlanGuard
// reproduces the real gateway boot ORDER — WireSysagentDeps with a nil
// PlanStore, THEN SetPlanStore once the real store exists — and proves the
// sysagent delete_agent tool's active-plan guard goes from FAIL-OPEN (skipped,
// nil store) to genuinely enforcing, without ever re-registering the tool by
// hand (exactly what a live turn experiences: the tool instance already sitting
// in the registry starts working once boot finishes wiring it).
func TestSetPlanStore_ReWiresSysagentPlanStoreForDeleteAgentActivePlanGuard(t *testing.T) {
	al, agentInst, home := newPlanToolWiringTestLoop(t)

	// Mirrors gateway.go's sysAgentDeps construction: PlanStore is NOT set here,
	// because in production it does not exist yet at this point in boot.
	sysDeps := &systools.Deps{
		Home:             home,
		ConfigPath:       filepath.Join(home, "config.json"),
		GetCfg:           al.GetConfig,
		MutateConfig:     al.MutateConfig,
		SaveConfigLocked: func(*config.Config) error { return nil },
	}
	al.WireSysagentDeps(sysDeps)

	// delete_agent resolves its victim through the on-disk agent entity store.
	store := agentstore.New(home)
	for _, id := range []string{"u9r-before-victim", "u9r-after-subject"} {
		if err := store.Create(id, &config.AgentConfig{ID: id, Name: id}); err != nil {
			t.Fatalf("seed agent entity %q: %v", id, err)
		}
	}

	// The real plan store exists only AFTER boot; both agents own an ACTIVE
	// (State=running) plan.
	planStore := plan.New(filepath.Join(home, "plans"))
	for _, owner := range []string{"u9r-before-victim", "u9r-after-subject"} {
		p := &plan.Plan{
			Title:        "active plan for " + owner,
			WorkspaceID:  "u9r-workspace",
			OwnerAgentID: owner,
			CreatedBy:    owner,
			State:        plan.StateRunning,
		}
		if err := planStore.Create(p); err != nil {
			t.Fatalf("seed running plan for %q: %v", owner, err)
		}
	}

	ctx := tools.WithAgentID(context.Background(), "planner-agent")

	// BEFORE the re-wire: nil PlanStore => the active-plan guard is SKIPPED
	// (fails OPEN). The agent is not default and not locked, so nothing else
	// refuses it — the delete proceeds to success.
	beforeRev := u9rAgentRevision(t, store, "u9r-before-victim")
	before := agentInst.Tools.Execute(ctx, "delete_agent", map[string]any{
		"id":       "u9r-before-victim",
		"confirm":  true,
		"revision": beforeRev,
	})
	if before == nil || before.IsError {
		t.Fatalf("precondition: with a nil PlanStore the active-plan guard is documented to fail OPEN, "+
			"so delete_agent must not be blocked by plan ownership; got %+v", before)
	}
	if strings.Contains(before.ForLLM, "owns active plans") {
		t.Fatal("precondition: a nil PlanStore must skip the active-plan guard, but it fired anyway")
	}

	// The gateway's later boot step: install the real store. This is the exact
	// AgentLoop method the UAT fix extends to also re-wire sysagentDeps.
	al.SetPlanStore(planStore)
	if al.GetPlanStore() != planStore {
		t.Fatal("SetPlanStore did not install the store on AgentLoop")
	}

	// AFTER the re-wire: the SAME registered delete_agent tool now consults the
	// real store and must REFUSE the agent that owns a running plan.
	afterRev := u9rAgentRevision(t, store, "u9r-after-subject")
	after := agentInst.Tools.Execute(ctx, "delete_agent", map[string]any{
		"id":       "u9r-after-subject",
		"confirm":  true,
		"revision": afterRev,
	})
	if after == nil || !after.IsError {
		t.Fatalf("after SetPlanStore re-wired sysagentDeps.PlanStore, delete_agent on an agent owning a "+
			"running plan must be REFUSED; got %+v", after)
	}
	if !strings.Contains(after.ForLLM, "AGENT_OWNS_ACTIVE_PLANS") ||
		!strings.Contains(after.ForLLM, "owns active plans") {
		t.Fatalf("expected the AGENT_OWNS_ACTIVE_PLANS refusal from the re-wired plan store, got %q", after.ForLLM)
	}

	// The refusal happens BEFORE any destructive action: the agent must survive.
	if _, err := store.Get("u9r-after-subject"); err != nil {
		t.Fatalf("a refused delete must leave the agent record intact; Get = %v", err)
	}
}
