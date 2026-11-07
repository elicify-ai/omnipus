// gateway_reload_test.go: tests for live reload - config watcher and service restart

package gateway

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from gateway.go tests 2026-09-15 ---

// TestExecuteReload_MarksDegradedOnCredInjectionFailure verifies that when
// executeReload rejects a reload due to a locked credential store, it:
//   - sets reloadDegraded = true
//   - sets reloadError != nil
//   - restores the previous ChannelManager and bundle (snapshot rollback)
//   - returns a non-nil error
//
// A subsequent successful reload (with a nil credStore so the injection path
// is skipped) must then clear the degraded flag.
func TestExecuteReload_MarksDegradedOnCredInjectionFailure(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	msgBus := bus.NewMessageBus()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19988},
		Providers: []*config.ModelConfig{
			{Name: "test", APIKeyRef: "SOME_KEY", Provider: "anthropic"},
		},
	}

	p := providers.LLMProvider(&restMockProvider{})
	al := mustAgentLoop(t, cfg, msgBus, p)

	// Build a locked credStore — InjectFromConfig will fail because the store
	// is not unlocked, triggering markDegraded.
	credStore := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	// Do NOT call Unlock — store remains locked.

	// Create a sentinel ChannelManager to verify rollback restores it.
	sentinelCM, err := channels.NewManager(cfg, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}
	sentinelBundle := credentials.SecretBundle{"sentinel": "value"}

	svc := &services{
		ChannelManager: sentinelCM,
		bundle:         sentinelBundle,
		credStore:      credStore,
	}
	// Simulate the single-flight slot being held by the caller. executeReload no
	// longer releases it — runReloadCycle owns the release, so that it can run a
	// coalesced follow-up reload before letting triggerReloadAndWait pollers go.
	svc.reloadInFlight = true

	// Execute the reload with a config that requires credential injection.
	// This should fail because the store is locked.
	err = executeReload(context.Background(), al, cfg, &p, svc, msgBus, true)
	if err == nil {
		t.Fatal("expected executeReload to return an error, got nil")
	}

	// Assert degraded state is set.
	svc.reloadMu.Lock()
	isDegraded := svc.reloadDegraded
	reloadErr := svc.reloadError
	svc.reloadMu.Unlock()

	if !isDegraded {
		t.Error("expected reloadDegraded == true after failed reload")
	}
	if reloadErr == nil {
		t.Error("expected reloadError != nil after failed reload")
	}

	// Assert rollback: ChannelManager must be the sentinel (not overwritten).
	if svc.ChannelManager != sentinelCM {
		t.Error("expected ChannelManager to be rolled back to sentinel after reload failure")
	}
	// Bundle must be restored.
	if svc.bundle["sentinel"] != "value" {
		t.Errorf("expected bundle to be rolled back; got %v", svc.bundle)
	}

	// Verify clearDegraded resets the degraded fields to zero values.
	// ClearDegradedForTest calls the real clearDegraded logic (defined in
	// export_test.go) so this assertion exercises the production code path,
	// not a reimplementation of it.
	svc.ClearDegradedForTest()

	svc.reloadMu.Lock()
	clearedDegraded := svc.reloadDegraded
	clearedErr := svc.reloadError
	svc.reloadMu.Unlock()

	if clearedDegraded {
		t.Error("expected reloadDegraded == false after ClearDegradedForTest")
	}
	if clearedErr != nil {
		t.Errorf("expected reloadError == nil after ClearDegradedForTest, got %v", clearedErr)
	}
}

// #1277 founder decision 2026-10-12: a single unreadable channel secret never rejects reload.
func TestExecuteReload_UnreadableChannelCredentialAppliesWithScopedDegradation(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-before-tamper"))
	cfg, bundle, _ := f.boot(t)
	api, svc, msgBus, provider := f.startRuntime(t, cfg, bundle)
	issue1277TamperSingleEntry(t, f, issue1277BrokenRef)
	value, err := f.store.Get(issue1277ProviderRef)
	require.NoError(t, err, "positive control: this is not whole-store failure")
	require.Equal(t, issue1277ProviderValue, value)
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	candidate, err := config.LoadConfigWithStore(f.configPath, f.store)
	require.NoError(t, err)
	require.NoError(t, executeReload(context.Background(), api.agentLoop, candidate, &provider, svc, msgBus, false),
		"one unreadable channel entry must apply reload with only that connection degraded")
	degraded, reason := svc.reloadDegradedStatus()
	require.False(t, degraded)
	require.Empty(t, reason)
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	require.NotContains(t, svc.bundle, issue1277BrokenRef)
	issue1277RequireDegradedChannel(t, api, svc.ChannelManager)
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
}

// TestRestartServices_LoopAndTaskTriggerStorePaths_MatchBootHomePath boots
// the real gateway with agents.defaults.home pointed at a nested path whose
// parent is NOT the gateway home, then reloads through restartServices.
// FR-04: the only scheduler store is the copy-1 path,
// <AgentHomeBasePath>/cron/jobs.json, and reload does not re-home it via
// filepath.Dir(AgentHomeBasePath()). FR-01/FR-14: tasks_triggers/jobs.json and
// loops/jobs.json are retired and are never created.
func TestRestartServices_LoopAndTaskTriggerStorePaths_MatchBootHomePath(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	// A customized agents workspace whose parent is NOT tmpDir: reproduces
	// the exact divergence the old reload bug depended on. If restartServices
	// ever goes back to filepath.Dir(cfg.AgentHomeBasePath()), that resolves
	// to <tmpDir>/custom, not the copy-1 workspace.
	customAgentsHome := filepath.Join(tmpDir, "custom", "agents_workspace")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         customAgentsHome,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// A real unlocked store is required: setupAndStartServices now aborts
	// boot when it cannot derive the intent-log HMAC chain key (14-reviewer
	// sign-off finding #5) — a locked store would make every boot in this
	// file fail before restartServices is ever reached.
	credStore := newUnlockedStore(t, tmpDir)
	builtinReg := tools.NewBuiltinRegistry()
	mcpReg := tools.NewMCPRegistry()

	rs, err := setupAndStartServices(
		context.Background(),
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
	require.NoError(t, err, "setupAndStartServices must boot cleanly with a customized agents.defaults.home")
	t.Cleanup(func() {
		stopAndCleanupServices(rs, 5*time.Second, false)
	})

	require.Equal(t, tmpDir, rs.homePath,
		"services.homePath must be the real OMNIPUS_HOME the gateway was booted with, "+
			"not derived from agents.defaults.home")
	assertT10SchedulerStores(t, tmpDir, cfg)

	// Drive a reload through the REAL production function.
	require.NoError(t, restartServices(al, rs, msgBus))
	assert.Equal(t, tmpDir, rs.homePath, "reload must keep the booted gateway home")
	assertT10SchedulerStores(t, tmpDir, cfg)
}

// assertT10SchedulerStores checks the merged store rule (FR-01, FR-04, FR-14,
// S-A.1). The shared store is the copy-1 path. The retired auxiliary files
// are absent under the gateway home, the agents workspace, and the pre-fix
// parent-directory derivation.
func assertT10SchedulerStores(t *testing.T, gatewayHome string, cfg *config.Config) {
	t.Helper()
	agentsHome := cfg.AgentHomeBasePath()
	wrongHome := filepath.Dir(agentsHome)
	shared := filepath.Join(agentsHome, "cron", "jobs.json")
	require.NotEqual(t, gatewayHome, wrongHome,
		"fixture bug: customAgentsHome must make filepath.Dir(cfg.AgentHomeBasePath()) diverge from the gateway home")
	require.NotEqual(t, shared, filepath.Join(wrongHome, "cron", "jobs.json"))
	require.FileExists(t, shared, "the shared store is <AgentHomeBasePath>/cron/jobs.json (FR-04 copy-1 path)")
	require.NoFileExists(t, filepath.Join(wrongHome, "cron", "jobs.json"),
		"reload must not re-home the shared store via filepath.Dir(AgentHomeBasePath())")
	if gatewayShared := filepath.Join(gatewayHome, "cron", "jobs.json"); gatewayShared != shared {
		require.NoFileExists(t, gatewayShared,
			"the shared store is the copy-1 path, not a second file under the gateway home")
	}
	for _, root := range []string{gatewayHome, agentsHome, wrongHome} {
		require.NoFileExists(t, filepath.Join(root, "loops", "jobs.json"),
			"FR-01/FR-14: loops/jobs.json is retired and must never be created under "+root)
		require.NoFileExists(t, filepath.Join(root, "tasks_triggers", "jobs.json"),
			"FR-01/FR-14: tasks_triggers/jobs.json is retired and must never be created under "+root)
	}
}

// TestRestartServices_ReappliesSessionMessagingCaps guards against the caps
// only ever being applied at boot: it edits session_messaging.child_send_rate
// on the live agent-loop config (mirroring what a real config reload
// publishes), drives restartServices, and asserts the durable
// MessageInboxStore's cap field picked up the NEW value rather than the
// boot-time one.
func TestRestartServices_ReappliesSessionMessagingCaps(t *testing.T) {
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
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// A real unlocked store is required: setupAndStartServices now aborts
	// boot when it cannot derive the intent-log HMAC chain key (14-reviewer
	// sign-off finding #5) — a locked store would make every boot in this
	// file fail before restartServices is ever reached.
	credStore := newUnlockedStore(t, tmpDir)
	builtinReg := tools.NewBuiltinRegistry()
	mcpReg := tools.NewMCPRegistry()

	rs, err := setupAndStartServices(
		context.Background(),
		cfg,
		credentials.SecretBundle{},
		al,
		msgBus,
		tmpDir,
		credStore,
		&SandboxApplyResult{},
		builtinReg,
		mcpReg,
		false,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		stopAndCleanupServices(rs, 5*time.Second, false)
	})

	inbox := al.GetMessageInboxStore()
	require.NotNil(t, inbox, "boot must wire a durable message inbox store")

	bootDefault := config.SessionMessagingConfig{}.EffectiveChildSendRatePerMinute()
	require.Equal(t, bootDefault, inbox.ChildSendRatePerMinute,
		"boot must apply the (default, since none was configured) session_messaging cap")

	// Simulate an operator editing session_messaging.child_send_rate and the
	// gateway publishing the reloaded config onto the live agent loop —
	// MutateConfig is the same atomic publish path a real config reload uses.
	const newRate = 4242
	require.NotEqual(t, newRate, bootDefault, "fixture bug: new value must actually differ from the default")
	require.NoError(t, al.MutateConfig(func(c *config.Config) error {
		c.SessionMessaging.ChildSendRatePerMinute = newRate
		return nil
	}))
	require.Equal(t, newRate, al.GetConfig().SessionMessaging.ChildSendRatePerMinute)

	// Drive a reload through the REAL production function.
	require.NoError(t, restartServices(al, rs, msgBus))

	assert.Equal(t, newRate, inbox.ChildSendRatePerMinute,
		"restartServices must re-apply the live session_messaging caps onto the SAME durable inbox "+
			"store on every reload, not only once at boot — otherwise an operator's edit has no "+
			"effect until the next full process restart")
}
