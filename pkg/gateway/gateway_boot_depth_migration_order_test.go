// gateway_boot_depth_migration_order_test.go — round 5, CHECK finding R3 on
// fix/delegation-depth-default-10: the one-time delegation-depth migration
// (stored edge depth 3 → 10, founder decision 2026-11-08 Q8 B) must have
// completed before ANY autonomous reader of a delegation store can start.
// startSchedulers starts the first such producers (cron engine, queued-task
// drain, trigger scheduler → scheduled agent turns → workspace.ReadDelegation),
// so the row boots the REAL setupAndStartServices and reads the store through
// the runtime's store loader at the instant before startSchedulers.

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupAndStartServices_DelegationDepthMigrationRunsBeforeSchedulers(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()
	cfg := &config.Config{
		Gateway:  config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Planning: config.PlanningConfig{GoalMaxRounds: 1, TaskMaxAttempts: 1},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// An upgraded install: a delegation store record saved under the old
	// shipped default 3. Deliberately NO workspace record for it: every
	// workspace record makes the boot's background library-association repair
	// (startBackgroundServices → repairLibraryAssociationsOnce, not joined at
	// shutdown) write workspaces/<id>/media/manifest.json, which races this
	// fast test's temp-dir removal. The store is read with LoadDelegation —
	// the same loader (loadDelegationStore) the runtime gate ReadDelegation
	// delegates to after its workspace-existence probe.
	const wsID = "ws-depth-boot-order"
	three := 3
	unlock := workspace.LockID(wsID)
	err := workspace.SaveDelegation(home, wsID, []workspace.DelegationEdge{
		{FromAgent: "mia", ToAgent: "mia", Depth: &three},
	})
	unlock()
	require.NoError(t, err)

	hookRan := false
	var depthSeen *int
	readOK := false
	SetBeforeStartSchedulersHookForTest(func(*agent.AgentLoop) {
		hookRan = true
		edges, ok := workspace.LoadDelegation(home, wsID)
		readOK = ok
		if ok && len(edges) == 1 && edges[0].Depth != nil {
			d := *edges[0].Depth
			depthSeen = &d
		}
	})
	t.Cleanup(func() { SetBeforeStartSchedulersHookForTest(nil) })

	rs, err := setupAndStartServices(context.Background(), cfg, credentials.SecretBundle{}, al, msgBus, home,
		newUnlockedStore(t, home), &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
	require.NoError(t, err, "setupAndStartServices must boot cleanly on a minimal config")
	t.Cleanup(func() { stopAndCleanupServices(rs, 5*time.Second, false) })

	require.True(t, hookRan, "the boot must pass the point right before startSchedulers")
	require.True(t, readOK, "the store record must be readable and trusted before schedulers start")
	require.NotNil(t, depthSeen, "the record must carry its single explicit-depth edge")
	assert.Equal(t, 10, *depthSeen,
		"R3: a scheduler-started reader must never see the pre-migration 3; the migration must complete before startSchedulers")
}
