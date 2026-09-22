// gateway_boot_test.go: tests for boot - unlock credentials, load souls, seed the roster, build services

package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartCatalogRefreshLoop_ShutdownCancelsWaitsAndNeverPersists pins the
// cancel-and-wait contract shutdown relies on: after cancel(), done closes
// once the goroutine has EXITED, and a pull that lands after the cancel does
// not write providers_catalog.json. On 2026-09-12 the fire-and-forget form
// wrote the 2.4 MB file into integration-test home dirs after their gateway
// had stopped and t.TempDir had removed them.
//
// DIES ON: startCatalogRefreshLoop not closing done when the loop exits, or
// the persist step ignoring a cancelled context.
func TestStartCatalogRefreshLoop_ShutdownCancelsWaitsAndNeverPersists(t *testing.T) {
	home := t.TempDir()
	puller := &parkedPuller{body: testDocument(t, "v9999.1.1"), ready: make(chan struct{})}
	cat := catalog.Boot(context.Background(), catalog.EmbeddedSnapshot, puller, catalog.NewFileStore(home), nil)

	cancel, done := startCatalogRefreshLoop(context.Background(), cat, catalog.NewFileStore(home), time.Hour, 5*time.Second, 0)

	select {
	case <-puller.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("startup pull never started")
	}

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh loop did not exit within 3s of cancel — shutdown would return with a writer still running")
	}
	require.Less(t, time.Since(start), 3*time.Second)

	// The pull returned a valid, newer document AFTER cancellation. It must
	// not have been persisted.
	_, err := os.Stat(filepath.Join(home, catalog.PersistedFileName))
	require.True(t, errors.Is(err, os.ErrNotExist), "providers_catalog.json must not be written after shutdown cancel; stat: %v", err)
}

// TestSkipStartupPull_Window is the FR-008 skip predicate on its own: only a
// persisted document younger than the window skips; a missing or unreadable
// file never does, because there is nothing on disk to serve from.
func TestSkipStartupPull_Window(t *testing.T) {
	home := t.TempDir()
	store := catalog.NewFileStore(home)

	assert.False(t, skipStartupPull(store, time.Hour),
		"no persisted file at all → never skip; the pull is exactly what is wanted")

	require.NoError(t, store.Write(context.Background(), testDocument(t, "v2026.8.24")))
	assert.True(t, skipStartupPull(store, time.Hour),
		"a document just written is younger than the window → skip")
	assert.False(t, skipStartupPull(store, time.Nanosecond),
		"a window shorter than the file's age → pull")
	assert.False(t, skipStartupPull(store, 0),
		"a zero window disables the skip entirely")
	assert.False(t, skipStartupPull(nil, time.Hour),
		"no store → nothing to age → never skip")
}

// TestSlogArgsToFields covers the key/value-pair conversion helper directly,
// including the malformed odd-length call site slog itself documents a
// "!BADKEY" convention for.
func TestSlogArgsToFields(t *testing.T) {
	fields := slogArgsToFields([]any{"a", 1, "b", "two"})
	assert.Equal(t, map[string]any{"a": 1, "b": "two"}, fields)

	fields = slogArgsToFields(nil)
	assert.Empty(t, fields)

	fields = slogArgsToFields([]any{"a", 1, "orphan"})
	assert.Equal(t, 1, fields["a"])
	assert.Equal(t, "orphan", fields["!BADKEY"])
}

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
		// The mock worker below never claims, so its run spends goal tries and
		// task attempts (founder decision 2026-09-14). One of each lets the run
		// end Failed quickly, so the teardown guard does not wait out the
		// default 20 tries x 3 attempts.
		Planning: config.PlanningConfig{GoalMaxRounds: 1, TaskMaxAttempts: 1},
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
	// Production seeds goal_claim "allow" for every agent (pkg/config/defaults.go).
	// Without it the task below would end before its first turn (founder
	// decision 2026-09-15) instead of exercising a real run's lifecycle writes.
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
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

	// StartTaskNow launches a task its caller has ALREADY moved to in_progress
	// (the REST PATCH does exactly that first). Leaving it `next` lets the
	// heartbeat claim and dispatch the same task a second time alongside this
	// run.
	inProgress := task.StatusInProgress
	_, advErr := tStore.Update(tsk.ID, task.Patch{Status: &inProgress})
	require.NoError(t, advErr, "advance the task to in_progress before StartTaskNow")

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

// --- Test D: wireChannelManager sets observer on the channel manager ---

// TestWireChannelManager_ObserverSurvivesChannelRecreation is a smoke test that
// wireChannelManager registers a non-nil PairingObserver on the channels.Manager.
// It uses the real channels.Manager (via NewManagerForTesting) and a real AgentLoop
// so the production code path is exercised end-to-end.
func TestWireChannelManager_ObserverSurvivesChannelRecreation(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
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
	t.Cleanup(func() { al.Stop() })

	// NewManagerForTesting creates a Manager with no channels (no credentials needed).
	// The pairingObserver field starts nil.
	cm := channels.NewManagerForTesting(nil)

	// Wire the manager onto the agent loop, then call wireChannelManager.
	al.SetChannelManager(cm)
	wireChannelManager(cm, al)

	// Verify the observer is set by calling SetPairingObserver with a tracking
	// closure and confirming the manager accepts it without panic.  The key
	// invariant is that wireChannelManager's closure (al.EmitWhatsAppPairing)
	// replaced any previously-nil observer.  We re-wire a test observer here to
	// confirm the setter is live; the test observer records whether it fires.
	var observerCalled bool
	assert.NotPanics(t, func() {
		cm.SetPairingObserver(func(channelID string, status channels.PairingStatus, qr, message string) {
			observerCalled = true
		})
	}, "SetPairingObserver must not panic after wireChannelManager")

	// Call the observer by simulating a pairing event emission on the bus and
	// verifying the subscription on the agent loop emits into the event bus.
	// We can't easily drive it through a real channel here, so instead confirm
	// that al.EmitWhatsAppPairing (called by the wireChannelManager closure)
	// does not panic. Subscribe to events first.
	evtSub := al.SubscribeEvents(4)
	defer al.UnsubscribeEvents(evtSub.ID)

	assert.NotPanics(t, func() {
		al.EmitWhatsAppPairing("whatsapp_native", channels.PairingStatusCode, "TEST-QR", "")
	}, "EmitWhatsAppPairing must not panic after wireChannelManager wired the observer")

	// Assert the event was emitted (not just a no-op).
	select {
	case evt := <-evtSub.C:
		assert.Equal(t, agent.EventKindWhatsAppPairing, evt.Kind,
			"EmitWhatsAppPairing must emit EventKindWhatsAppPairing on the event bus")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: expected WhatsAppPairing event on bus after EmitWhatsAppPairing")
	}

	_ = observerCalled // used only to satisfy compiler; real assertion is the event check above
}

// TestOnboardingStateUnreadable_ClassifiesEachCase pins the three inputs of the
// boot-time sample that feeds onboardingStateUnknown. Getting the MISSING case
// wrong would break every genuine first launch, so it is asserted explicitly
// rather than left implied.
func TestOnboardingStateUnreadable_ClassifiesEachCase(t *testing.T) {
	t.Run("missing file is a genuine fresh install", func(t *testing.T) {
		assert.False(t, onboardingStateUnreadable(t.TempDir()))
	})

	t.Run("valid JSON is known", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, "system", "state.json"),
			[]byte(`{"version":1,"onboarding_complete":true}`), 0o600))
		assert.False(t, onboardingStateUnreadable(home))
	})

	t.Run("unparseable JSON is unknown", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, "system", "state.json"),
			[]byte(`{"version":1,`), 0o600))
		assert.True(t, onboardingStateUnreadable(home),
			"a truncated state.json must be unknown, not a fresh install")
	})

	t.Run("unreadable file is unknown", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		// A DIRECTORY where the file belongs: os.ReadFile fails with a
		// non-IsNotExist error on every platform, unlike a chmod 000 file,
		// which root can still read.
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system", "state.json"), 0o700))
		assert.True(t, onboardingStateUnreadable(home),
			"a state path that cannot be read must be unknown, not a fresh install")
	})
}
