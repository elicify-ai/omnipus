package gateway

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// planEngineLoopGoroutines returns the stacks of every goroutine still running
// one of pe's own background loops (runTickLoop / runEventLoop), matched by
// the receiver pointer the runtime prints in the frame.
func planEngineLoopGoroutines(pe *agent.PlanEngine) []string {
	buf := make([]byte, 8<<20)
	dump := string(buf[:runtime.Stack(buf, true)])
	ptr := fmt.Sprintf("%p", pe)
	var hits []string
	for _, g := range strings.Split(dump, "\n\n") {
		if strings.Contains(g, ".(*PlanEngine).runTickLoop("+ptr) ||
			strings.Contains(g, ".(*PlanEngine).runEventLoop("+ptr) {
			hits = append(hits, g)
		}
	}
	return hits
}

// TestSetupAndStartServices_BootFailureAfterPlanEngineStopsIt pins the boot
// leak behind the goal-store writes into $HOME/.omnipus seen across later
// tests: when a boot step AFTER startPlanEngine fails (here prepareListener's
// HTTP bind, on a port already in use), setupAndStartServices returned the
// error with the plan engine still running — its tick loop kept sweeping
// goals (goalIdleExpirySweep -> resolveGoalRecordStore) for the life of the
// process, because the started-services teardown was registered only after
// the listener bound.
func TestSetupAndStartServices_BootFailureAfterPlanEngineStopsIt(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "SETUP: occupy a port")
	t.Cleanup(func() { _ = occupied.Close() })
	tcpAddr, ok := occupied.Addr().(*net.TCPAddr)
	require.True(t, ok, "SETUP: listener address %T is not a TCP address", occupied.Addr())
	port := tcpAddr.Port

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: port},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	credStore := newUnlockedStore(t, tmpDir)

	rs, bootErr := setupAndStartServices(
		context.Background(),
		cfg,
		credentials.SecretBundle{},
		al,
		msgBus,
		tmpDir,
		credStore,
		&SandboxApplyResult{},
		tools.NewBuiltinRegistry(),
		tools.NewMCPRegistry(),
		false, // allowGodMode
	)
	pe := agent.GetPlanEngine(al)
	// Whatever the verdict, never leave this engine ticking into later tests.
	t.Cleanup(func() {
		if pe != nil {
			pe.Stop()
		}
	})
	if bootErr == nil {
		stopAndCleanupServices(rs, 5*time.Second, false)
		t.Fatal("SETUP: boot must fail when the gateway port is already in use")
	}
	require.Contains(t, bootErr.Error(), "bind shared HTTP server",
		"SETUP: the failure must come from the listener bind, the step after startPlanEngine")
	require.NotNil(t, pe, "SETUP: boot must have started the plan engine before the bind failed")

	if leaked := planEngineLoopGoroutines(pe); len(leaked) != 0 {
		t.Fatalf("boot failed after startPlanEngine but the plan engine is still running (%d loop goroutine(s)); "+
			"its tick keeps writing the goal store after the failed boot returned:\n%s",
			len(leaked), strings.Join(leaked, "\n\n"))
	}
}
