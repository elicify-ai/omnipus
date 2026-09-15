// gateway_reload_test.go: tests for live reload - config watcher and service restart

package gateway

import (
	"context"
	"path/filepath"
	"strings"
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

// TestExecuteReload_RejectsOnCorruptedEnabledChannelCredential verifies that
// executeReload rejects (marks degraded, does NOT apply) a reload when an
// ENABLED channel's credential ref is present in the store but fails to
// decrypt — a corrupted store entry / wrong-master-key scenario, distinct
// from (and worse than) a simple missing ref. This pins the
// enabledRefFromBundleError escalation branch in executeReload's channel
// credential re-resolution step (the reload-side counterpart to
// TestGatewayBoot_CorruptedCredentialForEnabledChannelFailsFast in
// boot_order_test.go, which covers the equivalent bootCredentials path).
//
// Providers is left empty so credentials.InjectFromConfig (the provider-key
// step immediately before the channel-credential step) trivially succeeds —
// this test isolates the channel-credential rejection branch specifically.
func TestExecuteReload_RejectsOnCorruptedEnabledChannelCredential(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "TELEGRAM_TOKEN")

	credStore := credentials.NewStore(credsPath)
	if err := credentials.Unlock(credStore); err != nil {
		t.Fatalf("credentials.Unlock: %v", err)
	}

	msgBus := bus.NewMessageBus()

	baseCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19987},
	}

	p := providers.LLMProvider(&restMockProvider{})
	al := mustAgentLoop(t, baseCfg, msgBus, p)

	// The new config being reloaded to: an ENABLED telegram channel pointing
	// at the corrupted credential ref.
	newCfg := &config.Config{
		Agents:  baseCfg.Agents,
		Gateway: baseCfg.Gateway,
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {
				Enabled:  true,
				TokenRef: "TELEGRAM_TOKEN",
			},
		},
	}

	sentinelCM, err := channels.NewManager(baseCfg, credentials.SecretBundle{}, msgBus, nil)
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

	err = executeReload(context.Background(), al, newCfg, &p, svc, msgBus, true)
	if err == nil {
		t.Fatal(
			"expected executeReload to reject the reload when an enabled channel's credential fails to decrypt, got nil error",
		)
	}
	if !strings.Contains(err.Error(), "TELEGRAM_TOKEN") {
		t.Errorf("reload error must mention the failing ref TELEGRAM_TOKEN; got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "failed to resolve") {
		t.Errorf(
			"reload error must indicate the ref failed to resolve (decrypt failure), not merely be missing; got: %q",
			err.Error(),
		)
	}

	// Assert degraded state is set — this is how an operator discovers the
	// rejection via GET /health (SetDegradedFunc surfaces reloadDegraded/
	// reloadError as a 503 "reason").
	svc.reloadMu.Lock()
	isDegraded := svc.reloadDegraded
	reloadErr := svc.reloadError
	svc.reloadMu.Unlock()

	if !isDegraded {
		t.Error("expected reloadDegraded == true after rejected reload")
	}
	if reloadErr == nil {
		t.Error("expected reloadError != nil after rejected reload")
	}

	// Assert rollback: the previous ChannelManager/bundle must be restored,
	// not replaced by anything derived from the rejected newCfg.
	if svc.ChannelManager != sentinelCM {
		t.Error("expected ChannelManager to be rolled back to sentinel after rejected reload")
	}
	if svc.bundle["sentinel"] != "value" {
		t.Errorf("expected bundle to be rolled back; got %v", svc.bundle)
	}
}

// TestRestartServices_LoopAndTaskTriggerStorePaths_MatchBootHomePath boots
// the real gateway service set with agents.defaults.home pointed at a nested
// path whose PARENT directory is NOT the real OMNIPUS_HOME, then drives a
// reload through the real restartServices function and asserts the loop
// scheduler and task-trigger scheduler are (re)built against the SAME
// on-disk store paths boot used — never against
// filepath.Dir(cfg.AgentHomeBasePath()), which would land in a sibling
// directory under this fixture.
func TestRestartServices_LoopAndTaskTriggerStorePaths_MatchBootHomePath(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	// A customized agents workspace whose parent is NOT tmpDir: reproduces
	// the exact divergence the bug depended on. If restartServices ever goes
	// back to deriving the home dir via filepath.Dir(cfg.AgentHomeBasePath()),
	// this resolves to <tmpDir>/custom, not <tmpDir>.
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

	bootLoopStore := filepath.Join(tmpDir, "loops", "jobs.json")
	bootTriggerStore := filepath.Join(tmpDir, "tasks_triggers", "jobs.json")
	require.FileExists(t, bootLoopStore, "boot must create the loop scheduler store under the real homePath")
	require.FileExists(t, bootTriggerStore, "boot must create the task-trigger store under the real homePath")

	// The wrong, pre-fix derivation would have written here instead — assert
	// the fixture genuinely creates the divergence this test exists to catch.
	wrongLoopStore := filepath.Join(filepath.Dir(cfg.AgentHomeBasePath()), "loops", "jobs.json")
	wrongTriggerStore := filepath.Join(filepath.Dir(cfg.AgentHomeBasePath()), "tasks_triggers", "jobs.json")
	require.NotEqual(t, bootLoopStore, wrongLoopStore,
		"fixture bug: customAgentsHome must make filepath.Dir(cfg.AgentHomeBasePath()) diverge from tmpDir")
	require.NotEqual(t, bootTriggerStore, wrongTriggerStore,
		"fixture bug: customAgentsHome must make filepath.Dir(cfg.AgentHomeBasePath()) diverge from tmpDir")
	require.NoFileExists(t, wrongLoopStore, "boot itself must not have used the wrong derivation")
	require.NoFileExists(t, wrongTriggerStore, "boot itself must not have used the wrong derivation")

	// Drive a reload through the REAL production function.
	require.NoError(t, restartServices(al, rs, msgBus))

	assert.FileExists(t, bootLoopStore,
		"restartServices must still write the loop scheduler store under the SAME real homePath boot used")
	assert.FileExists(t, bootTriggerStore,
		"restartServices must still write the task-trigger store under the SAME real homePath boot used")
	assert.NoFileExists(t, wrongLoopStore,
		"restartServices must NOT re-derive homePath via filepath.Dir(cfg.AgentHomeBasePath()) — "+
			"that silently re-homes the loop scheduler to a divergent, likely-empty directory whenever "+
			"agents.defaults.home is customized")
	assert.NoFileExists(t, wrongTriggerStore,
		"restartServices must NOT re-derive homePath via filepath.Dir(cfg.AgentHomeBasePath()) "+
			"for the task-trigger store either")
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
