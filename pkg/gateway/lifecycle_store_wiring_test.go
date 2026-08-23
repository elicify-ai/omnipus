// lifecycle_store_wiring_test.go — regression guard for the FR-118/G-13 gap
// where TaskExecutor.SetLifecycleStore was defined (task_executor.go) and
// called by every dispatch chokepoint (mintTaskLifecycleRecord,
// transitionTaskLifecycle, finalizeTaskLifecycle), but the REAL gateway boot
// path (setupAndStartServices) never called the setter — it was nil for the
// entire life of the real gateway, silently no-oping every one of those
// methods (they all nil-guard via getLifecycleStore). PlanEngine got the same
// *session.LifecycleStore via its own SetLifecycleStore call right next to
// where the TaskExecutor's was missing; only the plan engine's boot sweep
// could ever see a durable record, and only for plan OWNER/member sessions it
// itself minted — never for a task/plan-member dispatch session, which is
// exactly the crash-recovery gap 99d4e729 set out to close.
//
// Every existing unit test of TaskExecutor.SetLifecycleStore calls the setter
// directly on a hand-built TaskExecutor — which proves the setter works, but
// proves NOTHING about whether the real boot sequence ever calls it. That is
// exactly how this gap hid. The test below does not call
// tExecutor.SetLifecycleStore anywhere: it invokes the REAL production boot
// function (setupAndStartServices), dispatches a real task through the real
// StartTaskNow dispatch chokepoint, and then reads back the durable record
// through a SECOND, independent *session.LifecycleStore instance pointed at
// the same on-disk directory — exactly what the boot sweep does on the next
// boot after a crash. If the `tExecutor.SetLifecycleStore(lifecycleStore)`
// line is ever removed from setupAndStartServices, mintTaskLifecycleRecord's
// nil-guard makes the mint a silent no-op and the Load below returns
// session.ErrLifecycleNotFound — this test goes red.
package gateway

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSetupAndStartServices_TaskExecutorLifecycleStoreWiring boots the gateway
// through the exact production function (setupAndStartServices) on a minimal,
// no-network config, dispatches a standalone task through
// TaskExecutor.StartTaskNow (the second of the two documented
// mintTaskLifecycleRecord chokepoints, alongside createTaskSessionSync/
// ExecuteTask), and asserts a durable session_lifecycle record was persisted
// for the resulting session.
func TestSetupAndStartServices_TaskExecutorLifecycleStoreWiring(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			// A real, chat-target agent ("mia") so registry.GetAgent("mia")
			// resolves for the standalone task dispatched below. The retired
			// "main" sentinel used to be registered implicitly regardless of
			// cfg (pkg/agent/registry.go's old always-on fallback); it is gone
			// with no back-compat, so this harness must seed a real agent.
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// FIX (14-reviewer sign-off finding #5): setupAndStartServices now aborts
	// boot when it cannot derive the intent-log HMAC chain key (previously a
	// WARN-and-continue that let plan.NewIntentLog silently install a public
	// dev-only key in production — see gateway.go's boot wiring). A locked
	// store (credentials.NewStore without Unlock) used to be tolerated here
	// only because that derivation failure was non-fatal; use the real
	// unlocked-store helper so this test still exercises the intended boot
	// path rather than the now-fatal locked-store one.
	credStore := newUnlockedStore(t, tmpDir)
	builtinReg := tools.NewBuiltinRegistry()
	mcpReg := tools.NewMCPRegistry()

	rs, err := setupAndStartServices(
		cfg,
		credentials.SecretBundle{},
		al,
		msgBus,
		tmpDir,
		credStore,
		&SandboxApplyResult{},
		builtinReg,
		mcpReg,
		false, // allowGodMode
	)
	require.NoError(t, err, "setupAndStartServices must boot cleanly on a minimal config")
	t.Cleanup(func() {
		stopAndCleanupServices(rs, 5*time.Second, false)
	})
	require.NotNil(t, rs.PlanEngine, "plan engine must be constructed and started by the real boot path")

	tExecutor := agent.GetTaskExecutor(al)
	require.NotNil(t, tExecutor, "boot must construct a task executor")
	tStore := agent.GetTaskStore(al)
	require.NotNil(t, tStore, "boot must construct a task store")

	// A standalone task (PlanID == "") assigned to "mia" — the one real,
	// chat-target agent seeded into cfg.Agents.List above, so
	// registry.GetAgent("mia") resolves without any workspace/team setup.
	// WorkspaceID only needs to be non-empty (task.Store.normalize enforces
	// presence, not FK existence — the workspace-membership check is a
	// REST/tool-layer concern this Go-level dispatch bypasses entirely).
	tsk := &task.Task{
		Title:       "lifecycle-wiring-smoke",
		AgentID:     "mia",
		Status:      task.StatusNext,
		WorkspaceID: "lifecycle-wiring-smoke-ws",
	}
	require.NoError(t, tStore.Create(tsk))

	sessionID, startErr := tExecutor.StartTaskNow(context.Background(), tsk.ID)
	require.NoError(t, startErr, "StartTaskNow must succeed for a valid standalone task with a registered agent")
	require.NotEmpty(t, sessionID, "StartTaskNow must mint and persist a session id")

	// Teardown race guard (mirrors TestHandleTaskPatch_InProgress_WithKnownAgent
	// in rest_tasks_start_test.go): StartTaskNow launched runTaskFromInProgress
	// in a background goroutine that keeps writing session/task files after
	// this call returns. Registered AFTER t.TempDir()'s own cleanup so it runs
	// first (t.Cleanup is LIFO) and the goroutine's writes are done before the
	// temp dir is removed. The mock LLM returns immediately so this clears in
	// well under a second; the bound is a generous safety margin.
	taskID := tsk.ID
	t.Cleanup(func() {
		require.Eventually(t, func() bool {
			fresh, getErr := tStore.Get(taskID)
			return getErr == nil && (fresh.Status == task.StatusDone || fresh.Status == task.StatusFailed)
		}, 10*time.Second, 20*time.Millisecond,
			"task goroutine must reach a terminal state before test teardown")
	})

	// Read the durable S2 record back through a FRESH, independent
	// LifecycleStore instance pointed at the SAME directory
	// setupAndStartServices used (<homePath>/session_lifecycle) — this is
	// exactly what boot_sweep.go does on the NEXT boot after a crash. It
	// deliberately does NOT reach into tExecutor's private lifecycleStore
	// field (there is no exported accessor, and reaching in would just be
	// the same side door this test exists to avoid) — it verifies the
	// observable, on-disk effect of the wiring instead.
	lifecycleStore := session.NewLifecycleStore(filepath.Join(tmpDir, "session_lifecycle"))
	rec, loadErr := lifecycleStore.Load(sessionID)
	require.NoError(t, loadErr,
		"a durable session_lifecycle record must exist for the dispatched task session "+
			"(session.ErrLifecycleNotFound means TaskExecutor.SetLifecycleStore was never "+
			"called from the boot path, so mintTaskLifecycleRecord silently no-op'd)")
	assert.Equal(t, sessionID, rec.SessionID)
	assert.NotEmpty(t, rec.State, "lifecycle record must carry a non-empty state")
}
