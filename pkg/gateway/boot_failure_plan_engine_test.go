package gateway

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
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

// ownedServiceFrames name the packages of the background services
// stopAndCleanupServices owns (cron, task drain / mail watch, channel workers,
// media store, plan engine, task trigger, loop scheduler). A goroutine running
// any of them that a failed boot left behind is a leak.
var ownedServiceFrames = []string{
	"github.com/elicify-ai/omnipus/pkg/cron.",
	"github.com/elicify-ai/omnipus/pkg/heartbeat.",
	"github.com/elicify-ai/omnipus/pkg/channels.",
	"github.com/elicify-ai/omnipus/pkg/media.",
	"github.com/elicify-ai/omnipus/pkg/agent.(*PlanEngine).",
	"github.com/elicify-ai/omnipus/pkg/agent.(*TaskTrigger",
	"github.com/elicify-ai/omnipus/pkg/agent.(*LoopScheduler",
}

// ownedServiceGoroutines returns, by goroutine header line, every goroutine
// whose stack runs one of ownedServiceFrames. It fails the test rather than
// under-report when the dump does not fit its buffer.
func ownedServiceGoroutines(t *testing.T) map[string]string {
	t.Helper()
	buf := make([]byte, 32<<20)
	n := runtime.Stack(buf, true)
	if n == len(buf) {
		t.Fatalf("INSTRUMENT: the goroutine dump filled its %d-byte buffer and may be truncated", len(buf))
	}
	out := map[string]string{}
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		id, _, _ := strings.Cut(strings.TrimPrefix(header, "goroutine "), " ")
		for _, frame := range ownedServiceFrames {
			if strings.Contains(g, frame) {
				out[id] = g
				break
			}
		}
	}
	return out
}

// bootExpectingFailure runs the real setupAndStartServices, which must fail:
// it fails the test on a panic, on a success, or when the failed boot (and
// the cleanup it now runs) does not return within 60 s.
func bootExpectingFailure(t *testing.T, cfg *config.Config, homePath string, credStore *credentials.Store) error {
	t.Helper()
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	type result struct {
		rs       *services
		err      error
		panicked any
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{panicked: r}
			}
		}()
		rs, err := setupAndStartServices(context.Background(), cfg, credentials.SecretBundle{}, al, msgBus,
			homePath, credStore, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
		done <- result{rs: rs, err: err}
	}()
	select {
	case r := <-done:
		if r.panicked != nil {
			t.Fatalf("the failed boot panicked during its cleanup: %v", r.panicked)
		}
		if r.err == nil {
			stopAndCleanupServices(r.rs, 5*time.Second, false)
			t.Fatal("SETUP: boot was expected to fail")
		}
		return r.err
	case <-time.After(60 * time.Second):
		t.Fatal("the failed boot did not return within 60s: its cleanup hangs")
		return nil
	}
}

func requireNoNewOwnedServiceGoroutines(t *testing.T, before map[string]string) {
	t.Helper()
	var leaked []string
	for id, g := range ownedServiceGoroutines(t) {
		if _, existed := before[id]; !existed {
			leaked = append(leaked, g)
		}
	}
	if len(leaked) != 0 {
		t.Fatalf("the failed boot left %d service goroutine(s) running after it returned:\n%s",
			len(leaked), strings.Join(leaked, "\n\n"))
	}
}

func minimalBootConfig(home string) *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
}

// TestSetupAndStartServices_FailedEpochMintBeforeAnyServiceReturnsCleanly
// covers the earliest failed boot: the boot-epoch mint fails before
// startSchedulers has allocated the service set, so the teardown registered
// at the top of setupAndStartServices must neither panic on the absent set
// nor hang, and the original error must come back unchanged.
func TestSetupAndStartServices_FailedEpochMintBeforeAnyServiceReturnsCleanly(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	credStore := newUnlockedStore(t, tmpDir)
	before := ownedServiceGoroutines(t)

	// An empty data dir is the mint's own refusal (BootEpochStore.Mint).
	err := bootExpectingFailure(t, minimalBootConfig(tmpDir), "", credStore)

	require.Contains(t, err.Error(), "boot epoch mint failed", "the original boot error must survive the cleanup")
	requireNoNewOwnedServiceGoroutines(t, before)
}

// TestSetupAndStartServices_FailedIntentLogKeyAfterChannelManagerStopsStartedServices
// covers a failed boot in the middle: setupPlans cannot derive the intent-log
// HMAC key from a locked credential store. By then startSchedulers has
// started the cron and task-drain services and setupMediaAndChannels has
// built the channel manager, but StartAll has not run. The cleanup must stop
// what was started, handle the never-started channel manager, and return the
// original error.
func TestSetupAndStartServices_FailedIntentLogKeyAfterChannelManagerStopsStartedServices(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	locked := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	before := ownedServiceGoroutines(t)

	err := bootExpectingFailure(t, minimalBootConfig(tmpDir), tmpDir, locked)

	require.Contains(t, err.Error(), "failed to derive intent log HMAC chain key",
		"the original boot error must survive the cleanup")
	requireNoNewOwnedServiceGoroutines(t, before)
}
