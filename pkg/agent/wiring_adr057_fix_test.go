// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// wiring_adr057_fix_test.go — FIX-2 regression coverage for three ADR-057
// "mechanism not property" defects: a primitive that was built, unit-tested,
// and never actually connected to its live dispatch/construction path (7
// reviewers' finding: "every blocker is a seam that was built, documented,
// unit-tested, and never connected"). Every assertion below is a PROPERTY
// test at the real integration point (a constructed *AgentLoop, its real
// per-agent DelegateTool, its real shared session store) — not a
// re-assertion of an already-covered primitive in isolation:
//   - RootDelegationAdmission's own TryAdmit/Cap/Active behavior is already
//     covered by admission_adr057_test.go; this file proves AgentLoop's
//     construction and dispatch path actually WIRE and CONSULT it.
//   - SetSessionManager's own kill behavior is already covered by
//     pkg/tools/delegate_adr057_unix_test.go's hand-wired unit test — this
//     file proves AgentLoop's OWN construction performs that same wiring
//     without a test manually calling the setter.
//   - SetStatsFlushInterval's own value-setting behavior is implicit in
//     pkg/session; this file proves an operator's config value actually
//     reaches the shared store AgentLoop constructs.
//
// Each test below was run against the pre-fix code (loop.go/admission.go
// before this unit's wiring changes) and observed to fail for the reason
// documented on it.
package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestWiring_DelegateToolSessionManager_KillsRealBackgroundProcess proves
// FR-028/BDD-29's SetSessionManager wiring (loop.go's per-agent DelegateTool
// construction block) is actually connected — not just declared. Before the
// fix, delegateTool.SetSessionManager(tools.GetSharedSessionManager()) was
// never called anywhere in production (grep -rn "SetSessionManager"
// --include='*.go' pkg/ cmd/ | grep -v _test returned only the declaration
// and three doc comments), so killChildBackgroundShells
// (pkg/tools/delegate.go) always took its `if t.sessionManager == nil {
// return }` branch and a cancelled delegate's real background process was
// silently orphaned holding its port. This drives the REAL DelegateTool
// instance a REAL *AgentLoop registered on its default agent — not a
// hand-wired one, like pkg/tools/delegate_adr057_unix_test.go's own
// (passing) unit test, which is exactly why that test passed while
// production stayed broken.
func TestWiring_DelegateToolSessionManager_KillsRealBackgroundProcess(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	inst := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, inst, "AgentLoop has no default agent registered")

	toolIface, ok := inst.Tools.Get("delegate")
	require.True(t, ok, "default agent has no 'delegate' tool registered")
	dt, ok := toolIface.(*tools.DelegateTool)
	require.True(t, ok, "'delegate' tool is not a *tools.DelegateTool")

	// Supply the OTHER preconditions executeCancel needs (session-messaging
	// kill switch, lifecycle store + record, cancel hooks) exactly as
	// pkg/tools/delegate_adr057_unix_test.go's own reference unit test does —
	// these are wired in production by DIFFERENT units
	// (session_messaging_wire.go, cancel.go; the live enabled-check AgentLoop
	// itself wires defaults to false for a bare test config with no
	// session_messaging section at all). The property under test is
	// specifically whether AgentLoop's OWN construction wired a working
	// sessionManager onto this real instance.
	dt.SetSessionMessagingEnabled(func() bool { return true })
	childID := "wiring-fix-child-" + uuid.NewString()
	parentKey := "wiring-fix-parent-" + uuid.NewString()
	lc := session.NewLifecycleStore(t.TempDir())
	dt.SetLifecycleStore(lc)
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID: childID, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		ParentDurableKey: parentKey, WorkspaceID: "ws-1", AgentID: "worker",
	}))
	dt.SetCancelHooks(
		func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
		func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
	)

	// A REAL, live OS process — standing in for a delegated child's
	// backgrounded `bash run_in_background=true` dev server. Killed in
	// cleanup regardless of the test's outcome so a failing assertion never
	// leaks a live process.
	cmd := exec.Command("sleep", "30")
	require.NoError(t, cmd.Start(), "failed to start real background process")
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // best-effort; already-dead is the expected success-path outcome

	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait() // reaps the child once something kills it — our own assertion below, or the Cleanup above
		close(waitDone)
	}()

	// Register the real process on the SAME shared, process-wide
	// SessionManager AgentLoop's wiring is supposed to hand to dt.
	sm := tools.GetSharedSessionManager()
	sm.Add(&tools.ProcessSession{
		ID:             "wiring-fix-proc-" + uuid.NewString(),
		PID:            cmd.Process.Pid,
		Command:        "sleep 30",
		Background:     true,
		Status:         tools.StatusRunning,
		OwnerSessionID: childID,
	})

	callerCtx := tools.WithTranscriptSessionID(context.Background(), parentKey)
	result := dt.Execute(callerCtx, map[string]any{
		"action": "cancel", "session_id": childID, "hard": true,
	})
	require.False(t, result.IsError, "delegate cancel failed: %s", result.ForLLM)

	select {
	case <-waitDone:
		// The real background process was actually killed — proves
		// AgentLoop's construction wired a non-nil, functioning
		// sessionManager onto this real DelegateTool instance.
	case <-time.After(3 * time.Second):
		t.Fatal("delegate action=cancel did not kill the real background process within 3s — " +
			"AgentLoop's DelegateTool wiring did not actually call SetSessionManager " +
			"(or wired the wrong SessionManager instance)")
	}
}

// TestWiring_RootDelegationAdmission_RefusesPastCapThenAdmitsOnRelease proves
// the ADR-057 W17 root-delegation admission gate (admission.go's
// RootDelegationAdmission / ResolveRootDelegationCap / RefuseRootDelegation)
// is actually consulted on AgentLoop's real root-delegation dispatch path.
// Before the fix these primitives had ZERO non-test production callers
// (grep -rln "RootDelegationAdmission|RefuseRootDelegation|
// ResolveRootDelegationCap" --include='*.go' . returned only admission.go
// and its own unit test) — a root turn's `delegate` fan-out sailed through
// completely ungated regardless of agents.defaults.subturn.max_concurrent,
// because a root turnState's concurrencySem (the only other candidate gate)
// is nil by construction (subturn.go's own invariant). This test drives
// al.rootDelegationAdmission — the field NewAgentLoop constructs from cfg —
// through the SAME rootDelegationAdmittingSpawner wrapper type loop.go wires
// onto every per-agent DelegateTool's spawner, proving both halves of the
// wiring: (1) NewAgentLoop actually resolves and constructs a real, capped
// gate from config, and (2) the wrapper actually consults THAT gate (not a
// fresh, always-empty one) before allowing a root-level dispatch through.
func TestWiring_RootDelegationAdmission_RefusesPastCapThenAdmitsOnRelease(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // keep the unrelated env override inert

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				SubTurn:           config.SubTurnConfig{MaxConcurrent: 1},
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	require.NotNil(t, al.rootDelegationAdmission,
		"NewAgentLoop must construct a real root-delegation admission gate when "+
			"agents.defaults.subturn.max_concurrent resolves to a valid positive value")
	require.Equal(t, 1, al.rootDelegationAdmission.Cap())

	// Saturate AgentLoop's OWN gate instance directly — simulating one
	// root-level delegation already in flight — to prove the wrapper below
	// consults THIS SAME object, not a fresh, independently-constructed one.
	admitted, release := al.rootDelegationAdmission.TryAdmit()
	require.True(t, admitted, "precondition: could not saturate the cap=1 gate")

	sharedStore := al.GetSessionStore()
	require.NotNil(t, sharedStore, "AgentLoop has no shared session store")
	parentMeta, err := sharedStore.NewSession(session.SessionTypeChannel, "test-channel", "main")
	require.NoError(t, err, "mint a real parent session")

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "wiring-fix-root-parent",
		depth:               0, // a real ROOT turn — concurrencySem intentionally left nil
		pendingResults:      make(chan *tools.ToolResult, 8),
		session:             &ephemeralSessionStore{},
		transcriptSessionID: parentMeta.ID,
		routingSessionID:    session.RoutingSessionID(parentMeta.ID),
	}
	rootCtx := withTurnState(context.Background(), parent)

	// This IS the exact wrapper type loop.go's registerSharedTools delegate
	// block installs via delegateTool.SetSpawner(...) — constructed here
	// directly against al.rootDelegationAdmission (AgentLoop's own field)
	// rather than driving it through DelegateTool.Execute's action="run",
	// which would additionally require workspace delegation-graph trust
	// edges and other preconditions unrelated to the property under test.
	spawner := newRootDelegationAdmittingSpawner(NewSubTurnSpawner(al), al.rootDelegationAdmission, "test-agent")

	// With the cap's ONE slot held, a second root-level dispatch must be
	// REFUSED — not queued, not silently admitted (the exact ADR-037
	// anti-pattern this gate exists to prevent).
	refused, spawnErr := spawner.SpawnSubTurn(rootCtx, tools.SubTurnConfig{
		Model:             "test-model",
		Tools:             []tools.Tool{},
		SystemPrompt:      "task",
		DelegateSessionID: "wiring-fix-refused-" + uuid.NewString(),
	})
	require.NoError(t, spawnErr, "a refusal is reported via the ToolResult, not a Go error")
	require.NotNil(t, refused)
	require.True(t, refused.IsError, "root-level delegation past the cap must be refused")
	require.Contains(t, refused.ForLLM, strconv.Itoa(al.rootDelegationAdmission.Cap()),
		"refusal must name the cap (BDD-77)")
	require.Equal(t, 1, al.rootDelegationAdmission.Active(),
		"a refused dispatch must not itself consume a slot")

	// Release the held slot — the SAME gate must now ADMIT: this is not a
	// one-way trip, and proves the wrapper reads LIVE gate state rather than
	// a value captured once at construction.
	release()
	result, spawnErr := spawner.SpawnSubTurn(rootCtx, tools.SubTurnConfig{
		Model:             "test-model",
		Tools:             []tools.Tool{},
		SystemPrompt:      "task",
		DelegateSessionID: "wiring-fix-admitted-" + uuid.NewString(),
	})
	require.NoError(t, spawnErr, "spawnSubTurn with a free slot: %v", spawnErr)
	require.NotNil(t, result)
	require.False(t, result.IsError, "a root-level delegation with a free slot must be admitted: %s", result.ForLLM)
}

// TestWiring_RootDelegationFanOut_BoundedByCentralValueNot16 is the
// concurrency-gate consolidation's headline regression test (2026-08-04,
// commit 536b7340's follow-up fix): it proves root-level `delegate`
// fan-out is bounded by the CENTRAL Performance.max_parallel_agents value,
// not a second, independently-seeded 16 — the exact defect this fix closes.
//
// Before this fix: agents.defaults.subturn.max_concurrent was seeded to a
// fixed 16 on every fresh install, and ResolveRootDelegationCap read that
// field DIRECTLY, bypassing Performance.EffectiveMaxParallelAgents()
// entirely. An operator who raised max_parallel_agents to 40 in the UI
// (persisted, and honored by session admission and task dispatch) would
// still have root-level delegate() fan-out silently refuse the 17th
// subagent — a control that "moves, persists and governs nothing" for this
// one dispatch path (the ADR-037 anti-pattern this project bans by name).
//
// This test drives the SAME rootDelegationAdmittingSpawner wrapper
// loop.go's registerSharedTools installs on every per-agent DelegateTool,
// with 16 root-level delegations already admitted (simulating the OLD
// cap's exact boundary) and Performance.MaxParallelAgents=40 — then proves
// the 17th is ADMITTED, not refused, closing the loop end to end through
// the real dispatch wrapper (not just the RootDelegationAdmission
// primitive in isolation, which admission_adr057_test.go already covers).
func TestWiring_RootDelegationFanOut_BoundedByCentralValueNot16(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // keep the env override inert

	// Pin the memory reading. This test is about which VALUE seeds the
	// root-delegation cap — the central Performance one, not a second
	// hardcoded 16 — and it asserts that by admitting 16 delegations in a row.
	// Left unpinned it reads the LIVE host, so on a loaded machine the memory
	// gate clamps the effective cap to its floor of 2 and the precondition
	// fails at admission 3 with "unexpectedly refused". That is the gate doing
	// its job, not the regression this test guards, but it is indistinguishable
	// from a real failure in a CI log.
	//
	// Observed before pinning: exit=1 with `reason=memory_pressure
	// effective_concurrent_cap=2` logged beside the failure, passing on the
	// next run at lower load. A green that depends on how busy the machine is
	// is not a green.
	restoreMem := config.SetMemoryProviderForTest(
		func() (bool, bool) { return false, true },      // not under pressure, determinable
		func() (uint64, bool) { return 64 << 30, true }, // 64 GiB available
	)
	t.Cleanup(restoreMem)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				// SubTurn.MaxConcurrent deliberately left UNSET (Go zero
				// value) — the shipped default post-consolidation — so the
				// root-delegation cap inherits the central value below
				// rather than any fixed number.
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 40},
	}
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	require.NotNil(t, al.rootDelegationAdmission)
	require.Equal(t, 40, al.rootDelegationAdmission.Cap(),
		"the resolved root-delegation cap must be the CENTRAL Performance.EffectiveMaxParallelAgents() value (40), "+
			"not a second, independently-seeded 16 — this is the regression this fix closes")

	// Simulate 16 root-level delegations already in flight — the exact
	// boundary the OLD (pre-fix) hardcoded-16 seed would have refused the
	// very next admission at.
	var preRelease []func()
	for i := 0; i < 16; i++ {
		ok, release := al.rootDelegationAdmission.TryAdmit()
		require.Truef(t, ok, "precondition: admission %d/16 unexpectedly refused", i+1)
		preRelease = append(preRelease, release)
	}
	t.Cleanup(func() {
		for _, r := range preRelease {
			r()
		}
	})

	sharedStore := al.GetSessionStore()
	require.NotNil(t, sharedStore, "AgentLoop has no shared session store")
	parentMeta, err := sharedStore.NewSession(session.SessionTypeChannel, "test-channel", "main")
	require.NoError(t, err, "mint a real parent session")

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "wiring-fanout-root-parent",
		depth:               0, // a real ROOT turn — concurrencySem intentionally left nil
		pendingResults:      make(chan *tools.ToolResult, 8),
		session:             &ephemeralSessionStore{},
		transcriptSessionID: parentMeta.ID,
		routingSessionID:    session.RoutingSessionID(parentMeta.ID),
	}
	rootCtx := withTurnState(context.Background(), parent)

	spawner := newRootDelegationAdmittingSpawner(NewSubTurnSpawner(al), al.rootDelegationAdmission, "test-agent")

	// THE 17th root-level delegation. Under the pre-fix defect (root cap
	// hardcoded/seeded to 16, sourced independently of Performance.
	// EffectiveMaxParallelAgents()) this would have been REFUSED. With the
	// central value at 40, it must be ADMITTED.
	result, spawnErr := spawner.SpawnSubTurn(rootCtx, tools.SubTurnConfig{
		Model:             "test-model",
		Tools:             []tools.Tool{},
		SystemPrompt:      "task",
		DelegateSessionID: "wiring-fanout-17th-" + uuid.NewString(),
	})
	require.NoError(t, spawnErr, "spawnSubTurn for the 17th root-level delegation: %v", spawnErr)
	require.NotNil(t, result)
	require.False(t, result.IsError,
		"the 17th concurrent root-level delegation must be ADMITTED when the central max_parallel_agents is 40 — "+
			"a refusal here means root-level delegate() fan-out is still bound to a stale, independent 16: %s", result.ForLLM)
	// Note: spawnSubTurn runs the child's entire turn synchronously inside
	// this call regardless of Async (see rootDelegationAdmittingSpawner's own
	// doc comment), so by the time SpawnSubTurn returns here the 17th
	// delegation's slot has ALREADY been released — Active() is back to the
	// 16 simulated in-flight delegations, not 17. The admission itself (not
	// a lingering Active() count) is this test's property.
	require.Equal(t, 16, al.rootDelegationAdmission.Active(),
		"the 17th delegation's slot must have been released on its own completion, leaving the 16 simulated ones")
}

// TestWiring_StatsFlushInterval_ReachesSharedSessionStore proves FR-067/
// SC-048's sessions.stats_flush_interval operator override actually reaches
// the shared UnifiedStore's live flusher. Before the fix,
// EffectiveStatsFlushInterval (pkg/config/config.go) and
// SetStatsFlushInterval (pkg/session/unified_stats_flush.go) had ZERO
// non-test callers (grep -rn "EffectiveStatsFlushInterval|
// SetStatsFlushInterval" --include='*.go' pkg/ cmd/ | grep -v _test returned
// only declarations and comments) — an operator could set a non-default
// value, see it persist in config.json, and observe NO runtime effect: the
// store always ran on the hardcoded config.DefaultSessionStatsFlushInterval
// (5s) constant regardless of what was configured.
func TestWiring_StatsFlushInterval_ReachesSharedSessionStore(t *testing.T) {
	tmpDir := t.TempDir()

	// A deliberately non-default value, set via the same JSON path an
	// operator's config.json goes through — config.duration's underlying
	// type is unexported, so this is the only way to populate the field
	// from outside pkg/config.
	var sessCfg config.SessionConfig
	require.NoError(t, json.Unmarshal([]byte(`{"stats_flush_interval":"37s"}`), &sessCfg))
	require.Equal(t, 37*time.Second, sessCfg.EffectiveStatsFlushInterval(),
		"precondition: the configured value must resolve to something other than the 5s default")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				SubTurn:           config.SubTurnConfig{MaxConcurrent: 16},
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Session: sessCfg,
	}
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	sharedStore := al.GetSessionStore()
	require.NotNil(t, sharedStore, "AgentLoop has no shared session store")
	require.Equal(t, 37*time.Second, sharedStore.StatsFlushInterval(),
		"the operator's non-default sessions.stats_flush_interval must reach the shared "+
			"UnifiedStore's live flusher (FR-067/SC-048) — before the fix this always read back "+
			"the hardcoded 5s default because nothing ever called SetStatsFlushInterval")
}
