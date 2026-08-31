// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// restart_services_homepath_test.go — regression guards for two 14-reviewer
// sign-off findings on the reload path:
//
//  1. restartServices used to re-derive the Omnipus home directory as
//     filepath.Dir(cfg.AgentHomeBasePath()) for the notification store, the
//     task-trigger scheduler and the /loop scheduler — silently assuming
//     agents.defaults.home is always exactly "<OMNIPUS_HOME>/agents".
//     setupAndStartServices (boot) never made that assumption: it takes the
//     real homePath as an explicit parameter and stores it on
//     runningServices.homePath for exactly this reason. Whenever an operator
//     customizes agents.defaults.home to a path whose parent isn't
//     OMNIPUS_HOME, the two derivations diverged and a hot reload silently
//     re-homed those services onto a different (likely empty) directory.
//
//  2. session_messaging's five inbox caps were applied to the durable
//     MessageInboxStore once, at boot, despite a comment promising a
//     hot-reloaded edit "is reflected on the next Append" — restartServices
//     never revisited them, so an operator's session_messaging.* edit had no
//     effect until the next full process restart.
package gateway

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

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
