// turnend_order_1651_test.go — RED tests for issue #1651.
//
// Issue #1651: the race-enabled gateway test
// TestStop_WhilePendingApproval_RealStack_EndsAbortedAndCancelsCard failed at
// approval_stop_gateway_test.go:113 — "no foreground turn may remain after
// the Stop" — with ActiveForegroundTurnInfo still reporting the just-ended
// turn as in flight.
//
// Defect mechanism (read from pkg/agent/loop.go::runTurn BEFORE these tests
// were written; the RED runs below are the proof): runTurn's defers run LIFO
// and the EventKindTurnEnd emit closure is registered LAST — after the
// clearActiveTurn defer and the deferred ts.Finish — so on every return path
// it runs FIRST. An observer that receives TurnEnd and immediately asks
// ActiveForegroundTurnInfo for the session still finds the turnState in
// al.activeTurnStates with IsAlive() true: clearActiveTurn has not removed it
// and Finish has not set isFinished. The gateway's hubSyncTap
// (pkg/gateway/websocket_forward_hub.go) is exactly such an observer: it
// translates EventKindTurnEnd into the browser's "done" frame on the emitting
// goroutine, and a client reacting to "done" can hit the same window, as the
// CI failure did through the lossy async fan-out.
//
// Requirement under test (issue #1651): once a root turn's TurnEnd event has
// been emitted for a session, ActiveForegroundTurnInfo for that session must
// report no running turn (for that turn).
//
// Chief's condition (guard on the fix, GREEN controls here): the reorder must
// not let a slow or failing cleanup delay or drop the "done" frame — the
// TurnEnd emission must stay exactly once, with the correct status, even when
// the turn's finalize/persist step blocks or errors.
//
// Oracle sources (expected values derived from the spec, never from observed
// behaviour): issue #1651's foreground-turn-gone requirement and its aborted
// status for a Stop over a pending approval (as the issue's own failing test
// asserts); pkg/agent/events.go::TurnEndStatus — "completed" indicates the
// turn finished normally; the chief's condition for the control tests.
//
// Seam note (chief's condition): the injection point for the slow/failing
// cleanup is turnState.setLastStreamer — the final bus.Streamer whose
// Finalize is called by the deferred finalizeStreamer cleanup step
// (pkg/agent/turn_stream.go). No production change was needed: the fake is
// installed while the turn is provably mid-flight (its provider call is
// blocked on a test gate), and neither the attempt-streaming path (the
// provider here is not a providers.StreamingProvider) nor
// ensureFinalWebchatStreamer (which respects an already-attached streamer)
// overwrites it.
//
// RED evidence: failing runs on the pre-change tree, saved under
// /workspace/logs/stop-race-1651/ta/ (see ta-report.md for receipts).

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Harness — mirrors approval_stop_test.go's proven webchat-turn shape.
// ---------------------------------------------------------------------------

// turnendTapObservation is what the sync tap recorded at the instant it
// delivered a root TurnEnd for the session: the frame's status and the
// answer ActiveForegroundTurnInfo gave at that exact moment, plus a raw
// registry probe proving the query id was the right one (anti-vacuous-pass
// instrument: if the probe cannot see the turn that provably just ran, the
// observation says nothing about the defect).
type turnendTapObservation struct {
	status   TurnEndStatus
	fgOK     bool
	fgTurnID string

	probeFound    bool   // an entry with transcriptSessionID==sessionID was in activeTurnStates
	probeTurnID   string // that entry's turnID ("" when not found)
	probeAlive    bool   // that entry's IsAlive() at tap time
	probeByKeyHit bool   // an entry with sessionKey==sessionID was in activeTurnStates
}

// turnendTapRecorder is the synchronous TurnEnd observer. It installs via
// AgentLoop.SetEventSyncTap — the same mechanism the gateway's hubSyncTap
// uses (pkg/agent/loop_events.go), which runs once per Emit on the emitting
// goroutine, before the lossy per-subscriber fan-out. Unlike the gateway
// test's async subscription (whose receive races the emitting goroutine's
// remaining defers — that race IS issue #1651's flake), the sync tap observes
// the window deterministically: no scheduling luck involved.
type turnendTapRecorder struct {
	al        *AgentLoop
	sessionID string

	mu            sync.Mutex
	observations  []turnendTapObservation
	firstObserved chan struct{}
	observedOnce  sync.Once
}

func newTurnendTapRecorder(al *AgentLoop, sessionID string) *turnendTapRecorder {
	return &turnendTapRecorder{
		al:            al,
		sessionID:     sessionID,
		firstObserved: make(chan struct{}),
	}
}

func (r *turnendTapRecorder) tap(evt Event) {
	if evt.Kind != EventKindTurnEnd {
		return
	}
	payload, ok := evt.Payload.(TurnEndPayload)
	if !ok || payload.SessionID != r.sessionID || !payload.IsRoot {
		return
	}
	// The requirement under test: at the moment the root TurnEnd event is
	// delivered, the session's foreground turn must already be gone — the
	// same query a client (or the gateway test at
	// approval_stop_gateway_test.go:112) makes on receiving the done frame.
	turnID, _, _, fgOK := r.al.ActiveForegroundTurnInfo(payload.SessionID)

	// Anti-vacuous-pass probe: scan the registry directly with the SAME id,
	// so a false answer can never be an id mismatch in disguise.
	probe := turnendRegistryProbe1651(r.al, r.sessionID)

	r.mu.Lock()
	r.observations = append(r.observations, turnendTapObservation{
		status:        payload.Status,
		fgOK:          fgOK,
		fgTurnID:      turnID,
		probeFound:    probe.found,
		probeTurnID:   probe.turnID,
		probeAlive:    probe.alive,
		probeByKeyHit: probe.keyHit,
	})
	r.mu.Unlock()
	r.observedOnce.Do(func() { close(r.firstObserved) })
}

// turnendRegistryProbeResult is the raw registry view at one instant.
type turnendRegistryProbeResult struct {
	found  bool
	turnID string
	alive  bool
	keyHit bool
}

// turnendRegistryProbe1651 scans activeTurnStates for entries belonging to
// transcriptSessionID (and, separately, sessionKey) — the same map
// ActiveForegroundTurnInfo reads — without using that accessor, so the tap's
// observation is cross-checked against an independent read of the same state.
func turnendRegistryProbe1651(al *AgentLoop, transcriptSessionID string) turnendRegistryProbeResult {
	var res turnendRegistryProbeResult
	al.activeTurnStates.Range(func(key, value any) bool {
		ts, ok := value.(*turnState)
		if !ok {
			return true
		}
		if ks, _ := key.(string); ks == transcriptSessionID {
			res.keyHit = true
		}
		if ts.transcriptSessionID != transcriptSessionID || ts.depth != 0 || ts.parentTurnID != "" {
			return true
		}
		if !res.found || !ts.IsAlive() { // prefer a live match if several
			if !res.found {
				res.found = true
				res.turnID = ts.turnID
				res.alive = ts.IsAlive()
			}
		}
		return true
	})
	return res
}

// observationsSnapshot returns every recorded root TurnEnd observation.
func (r *turnendTapRecorder) observationsSnapshot() []turnendTapObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]turnendTapObservation(nil), r.observations...)
}

// turnend1651GatedProvider blocks its single Chat call on a test gate, so the test can
// pin setup steps to a moment the turn is provably mid-flight, then let it
// complete with a plain, tool-free answer.
type turnend1651GatedProvider struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newTurnend1651GatedProvider() *turnend1651GatedProvider {
	return &turnend1651GatedProvider{entered: make(chan struct{}), release: make(chan struct{})}
}

func (p *turnend1651GatedProvider) Chat(
	ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &providers.LLMResponse{Content: "gated mock response", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *turnend1651GatedProvider) GetDefaultModel() string { return "gated-1651-mock" }

// releaseNow lets the blocked Chat call proceed. Safe to call twice (an
// explicit release and the failure-path cleanup).
func (p *turnend1651GatedProvider) releaseNow() {
	select {
	case <-p.release:
	default:
		close(p.release)
	}
}

// gateFinalizeStreamer stands in for the turn's final streamer — the cleanup
// seam the deferred finalizeStreamer step drives (bus.Streamer). Finalize is
// the finalize/persist step of the "done" path: either it blocks until the
// test releases it (slow-cleanup variant) or it returns an error at once
// (failing-cleanup variant).
type gateFinalizeStreamer struct {
	entered     chan struct{}
	enteredOnce sync.Once
	release     chan struct{} // nil (with failWith set) = return immediately
	failWith    error

	mu            sync.Mutex
	finalizeCalls int
	updateCalls   int
}

func newGateFinalizeStreamer() *gateFinalizeStreamer {
	return &gateFinalizeStreamer{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *gateFinalizeStreamer) Update(_ context.Context, _ string) error {
	s.mu.Lock()
	s.updateCalls++
	s.mu.Unlock()
	return nil
}

func (s *gateFinalizeStreamer) Finalize(ctx context.Context, _ string) error {
	s.mu.Lock()
	s.finalizeCalls++
	s.mu.Unlock()
	s.enteredOnce.Do(func() { close(s.entered) })
	if s.failWith != nil {
		return s.failWith
	}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *gateFinalizeStreamer) Cancel(_ context.Context) {}

func (s *gateFinalizeStreamer) finalizeCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalizeCalls
}

// newTurnend1651Harness builds a webchat turn harness shaped like
// approval_stop_test.go's: a real AgentLoop over the real message bus, one
// fresh store-backed session, the loop running, and an isolated home
// directory (the os.MkdirTemp pattern newCancelTestAgentLoop documents —
// NOT t.TempDir, whose RemoveAll cleanup can fail the test on a straggling
// background writer).
func newTurnend1651Harness(
	t *testing.T, provider providers.LLMProvider, cfgMutators ...func(*config.Config),
) (*AgentLoop, *bus.MessageBus, string) {
	t.Helper()

	tmpDirOuter, err := os.MkdirTemp("", "agent-turnend-1651-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDirOuter) })
	tmpDir := filepath.Join(tmpDirOuter, "home")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))

	const testAgentID = "mia"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testAgentID, Name: "Mia"}},
		},
	}
	for _, mutate := range cfgMutators {
		mutate(cfg)
	}

	busRef := bus.NewMessageBus()
	t.Cleanup(func() { busRef.Close() })
	al := mustNewAgentLoop(t, cfg, busRef, provider)
	t.Cleanup(func() { al.Close() })

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "test-1651-turnend-order", testAgentID)
	require.NoError(t, err)
	sessionID := meta.ID

	runDone := make(chan error, 1)
	t.Cleanup(func() {
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
		}
	})
	loopCtx, loopCancel := context.WithCancel(context.Background())
	// Registered LAST so cleanup runs it FIRST (LIFO): cancel the loop before
	// the runDone join above waits on it.
	t.Cleanup(loopCancel)
	go func() { runDone <- al.Run(loopCtx) }()

	return al, busRef, sessionID
}

// publishTurnend1651Message publishes the one user message that drives the
// harness's turn, the same shape the gateway publishes for a webchat turn.
func publishTurnend1651Message(t *testing.T, busRef *bus.MessageBus, sessionID string, content string) {
	t.Helper()
	require.NoError(t, busRef.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:       "webchat",
		Sender:        bus.SenderInfo{CanonicalID: "webchat_user_1651"},
		ChatID:        "chat-1651",
		Content:       content,
		SessionID:     sessionID,
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": "mia"},
	}))
}

// findLiveRootTurnState1651 grabs the session's live root turnState while the
// turn is provably mid-flight (its provider call is blocked on a gate). Used
// to (a) install the gate streamer on the real turn and (b) hold a Finished()
// handle for deterministic settle waits. With the fix applied this must keep
// working: mid-flight the entry is registered either way.
func findLiveRootTurnState1651(t *testing.T, al *AgentLoop, transcriptSessionID string) *turnState {
	t.Helper()
	var found *turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if !ok || ts.transcriptSessionID != transcriptSessionID ||
			ts.depth != 0 || ts.parentTurnID != "" || !ts.IsAlive() {
			return true
		}
		found = ts
		return false
	})
	require.NotNil(t, found, "no live root turnState registered for session %q", transcriptSessionID)
	return found
}

// waitForTurnend1651Channel waits for a test signal with a bounded deadline.
// The deadline is a failure bound, not the oracle: the oracle is always the
// recorded observation.
func waitForTurnend1651Channel(t *testing.T, ch chan struct{}, timeout time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("%s within %s", what, timeout)
	}
}

// requireExactlyOneRootTurnEnd1651 asserts the chief's exactly-once contract
// and returns the single observation.
func requireExactlyOneRootTurnEnd1651(t *testing.T, rec *turnendTapRecorder) turnendTapObservation {
	t.Helper()
	obs := rec.observationsSnapshot()
	require.Len(t, obs, 1,
		"the root TurnEnd event must be emitted exactly once for the session (chief's condition), got %d", len(obs))
	return obs[0]
}

// ---------------------------------------------------------------------------
// RED test 1 — normal completed turn.
// ---------------------------------------------------------------------------

// Test1651_TurnEndSyncTap_ForegroundTurnAlreadyGone_CompletedTurn drives a
// normal, tool-free webchat turn to completion and, at the instant the sync
// tap delivers the root TurnEnd event, asks ActiveForegroundTurnInfo for the
// session — the same question the gateway answers with the browser "done"
// frame (hubSyncTap) and the same one issue #1651's failing assertion makes.
//
// Requirement (issue #1651): the answer must be "no running turn".
func Test1651_TurnEndSyncTap_ForegroundTurnAlreadyGone_CompletedTurn(t *testing.T) {
	provider := newTurnend1651GatedProvider()
	t.Cleanup(provider.releaseNow) // unblock a stuck Chat on failure paths
	al, busRef, sessionID := newTurnend1651Harness(t, provider)

	rec := newTurnendTapRecorder(al, sessionID)
	al.SetEventSyncTap(rec.tap)
	t.Cleanup(func() { al.SetEventSyncTap(nil) })

	publishTurnend1651Message(t, busRef, sessionID, "please answer plainly")

	// The turn is provably mid-flight once its provider call is reached (the
	// turnState is registered at admission, before the call); grab the settle
	// handle while alive.
	waitForTurnend1651Channel(t, provider.entered, 10*time.Second,
		"timeout waiting for the turn to reach its provider call")
	ts := findLiveRootTurnState1651(t, al, sessionID)

	provider.releaseNow() // let the turn complete normally

	waitForTurnend1651Channel(t, rec.firstObserved, 15*time.Second,
		"the root TurnEnd event was not emitted for the session")
	waitForTurnend1651Channel(t, ts.Finished(), 15*time.Second,
		"the turn did not finish after its provider call was released")

	obs := requireExactlyOneRootTurnEnd1651(t, rec)
	assert.Equal(t, TurnEndStatusCompleted, obs.status,
		"a normal completed turn's done event must carry status completed")

	// Anti-vacuous-pass instrument proof (logged for the receipt): if the
	// probe could not see the turn that provably just ran, the fgOK answer
	// would say nothing about the defect.
	t.Logf("tap-time registry probe: found=%v turnID=%q alive=%v sessionKeyHit=%v; fgOK=%v fgTurnID=%q",
		obs.probeFound, obs.probeTurnID, obs.probeAlive, obs.probeByKeyHit, obs.fgOK, obs.fgTurnID)

	// THE requirement (issue #1651), expected=false from the spec, not from
	// the current behaviour:
	assert.False(t, obs.fgOK,
		"BUG #1651: at the moment the root TurnEnd event is delivered, ActiveForegroundTurnInfo must report "+
			"no running turn for the session — it still reported turn %q as in flight", obs.fgTurnID)
}

// ---------------------------------------------------------------------------
// RED test 2 — Stop over a pending approval (issue #1651's own scenario).
// ---------------------------------------------------------------------------

// Test1651_TurnEndSyncTap_ForegroundTurnAlreadyGone_StopAbortedTurn is the
// CI failure's scenario, pinned deterministically: a webchat turn parked on a
// pending "ask" approval, the real StopSession fired, and the observation
// taken at the instant the sync tap delivers the root TurnEnd event.
//
// Requirement (issue #1651): once that aborted turn's TurnEnd is emitted, the
// session must report no running turn — and the status must be aborted.
func Test1651_TurnEndSyncTap_ForegroundTurnAlreadyGone_StopAbortedTurn(t *testing.T) {
	al, _, sessionID := newTurnend1651StopApprovalTurn(t)

	rec := newTurnendTapRecorder(al, sessionID)
	al.SetEventSyncTap(rec.tap)
	t.Cleanup(func() { al.SetEventSyncTap(nil) })

	// Grab the settle handle while the turn is parked (alive), then Stop via
	// the real path: polite then forced.
	ts := findLiveRootTurnState1651(t, al, sessionID)

	stopTurnend1651Turn(t, al, sessionID)

	waitForTurnend1651Channel(t, rec.firstObserved, 15*time.Second,
		"the root TurnEnd event was not emitted after the Stop")
	waitForTurnend1651Channel(t, ts.Finished(), 15*time.Second,
		"the parked turn did not finish after the Stop")

	obs := requireExactlyOneRootTurnEnd1651(t, rec)
	assert.Equal(t, TurnEndStatusAborted, obs.status,
		"a Stop over a parked approval must end the turn aborted, not completed/error")

	// Anti-vacuous-pass instrument proof (logged for the receipt): unlike the
	// completed path (finalizeTurn retires the turn inline before runTurn
	// returns), an aborted turn's retirement is runTurn's DEFERRED
	// clearActiveTurn — so the probe should still see the entry, alive, at
	// emit time. That is the defect window, not an id mismatch.
	t.Logf("tap-time registry probe: found=%v turnID=%q alive=%v sessionKeyHit=%v; fgOK=%v fgTurnID=%q",
		obs.probeFound, obs.probeTurnID, obs.probeAlive, obs.probeByKeyHit, obs.fgOK, obs.fgTurnID)

	// THE requirement (issue #1651) in the exact scenario that failed in CI:
	assert.False(t, obs.fgOK,
		"BUG #1651: after the Stop, at the moment the root TurnEnd event is delivered, "+
			"ActiveForegroundTurnInfo must report no running turn for the session — it still reported turn %q",
		obs.fgTurnID)
}

// ---------------------------------------------------------------------------
// GREEN controls — the chief's condition on the fix.
// ---------------------------------------------------------------------------

// Test1651_TurnEnd_EmittedOnceWithCorrectStatus_WhileFinalizeStillSlow guards
// the chief's condition: however the TurnEnd-vs-cleanup ordering is fixed,
// a SLOW finalize/persist step must not delay the TurnEnd emission. The
// emission must already be observable while the turn's finalize step — the
// final streamer's Finalize, driven here by runTurn's DEFERRED
// finalizeStreamer on the aborted path — is provably still blocked.
//
// Seam (abort path, no production change): the gate streamer is installed on
// the live turnState while it is parked on a pending approval, before the
// Stop. An aborted turn never runs finalizeTurn, so nothing finalizes inline
// — the deferred finalizeStreamer is the turn's only finalize, and it runs
// AFTER the deferred TurnEnd emit in runTurn's LIFO chain. (On the COMPLETED
// path this seam is unusable: finalizeTurn calls finalizeStreamer INLINE
// before runTurn returns — loop_run_turn.go — so a completed turn's
// finalize/publish precedes its TurnEnd event by construction.)
//
// GREEN control on today's code; must stay green after the fix.
func Test1651_TurnEnd_EmittedOnceWithCorrectStatus_WhileFinalizeStillSlow(t *testing.T) {
	al, _, sessionID := newTurnend1651StopApprovalTurn(t)

	rec := newTurnendTapRecorder(al, sessionID)
	al.SetEventSyncTap(rec.tap)
	t.Cleanup(func() { al.SetEventSyncTap(nil) })

	// Install the gate streamer while the turn is parked on the approval, so
	// the Stop's unwind runs the gate's Finalize in the deferred cleanup.
	ts := findLiveRootTurnState1651(t, al, sessionID)
	gate := newGateFinalizeStreamer()
	ts.setLastStreamer(gate)

	stopTurnend1651Turn(t, al, sessionID)

	// The emission must fire while Finalize is STILL blocked on the gate —
	// the release below is held until after this observation. If a reorder
	// ever places the emission behind the deferred cleanup step, this wait
	// times out and fails: the done frame would be delayed by a slow
	// finalize.
	waitForTurnend1651Channel(t, rec.firstObserved, 15*time.Second,
		"the TurnEnd emission was delayed behind the still-blocked finalize step (chief's condition violated)")

	// Instrument check: the seam genuinely engaged — Finalize was entered and
	// is blocked (it has not been released yet).
	waitForTurnend1651Channel(t, gate.entered, 10*time.Second,
		"the gate streamer's Finalize was never entered — the seam did not engage")
	require.Equal(t, 1, gate.finalizeCallCount(),
		"the finalize/persist step must run exactly once")

	close(gate.release) // let the blocked finalize finish
	waitForTurnend1651Channel(t, ts.Finished(), 15*time.Second,
		"the turn did not finish after the finalize gate was released")

	obs := requireExactlyOneRootTurnEnd1651(t, rec)
	assert.Equal(t, TurnEndStatusAborted, obs.status,
		"the done event must carry the turn's own status (aborted) even when cleanup is slow")
}

// Test1651_TurnEnd_EmittedOnceWithCorrectStatus_WhenFinalizeFails guards the
// other half of the chief's condition: a FAILING finalize/persist step must
// not drop the TurnEnd emission or change its status. The final streamer's
// Finalize returns an error; the emission must still arrive exactly once,
// with the status the turn itself computed.
//
// GREEN control on today's code; must stay green after the fix.
func Test1651_TurnEnd_EmittedOnceWithCorrectStatus_WhenFinalizeFails(t *testing.T) {
	al, _, sessionID := newTurnend1651StopApprovalTurn(t)

	rec := newTurnendTapRecorder(al, sessionID)
	al.SetEventSyncTap(rec.tap)
	t.Cleanup(func() { al.SetEventSyncTap(nil) })

	ts := findLiveRootTurnState1651(t, al, sessionID)

	gate := newGateFinalizeStreamer()
	gate.failWith = errors.New("simulated finalize/persist failure (issue #1651 control)")
	gate.release = nil // Finalize returns failWith immediately
	ts.setLastStreamer(gate)

	stopTurnend1651Turn(t, al, sessionID)

	waitForTurnend1651Channel(t, rec.firstObserved, 15*time.Second,
		"the TurnEnd emission was dropped by the failing finalize step (chief's condition violated)")
	waitForTurnend1651Channel(t, ts.Finished(), 15*time.Second,
		"the turn did not finish after the failing finalize")

	// Instrument check: the failing finalize genuinely ran (the error path was
	// exercised), and it ran exactly once.
	require.Equal(t, 1, gate.finalizeCallCount(),
		"the failing finalize step must have run exactly once")

	obs := requireExactlyOneRootTurnEnd1651(t, rec)
	assert.Equal(t, TurnEndStatusAborted, obs.status,
		"the done event must keep the turn's own status (aborted) even when the finalize step errors")
}

// ---------------------------------------------------------------------------
// Shared Stop-over-pending-approval turn steps (the RED test 2 scenario,
// factored so the chief's-condition controls drive the same aborted path).
// ---------------------------------------------------------------------------

// newTurnend1651StopApprovalTurn builds the approval_stop_test.go harness:
// the ask-policy ceiling at both layers, the real tool, the real parking
// approver, and the session launcher the durable Stop path requires.
func newTurnend1651StopApprovalTurn(t *testing.T) (*AgentLoop, *bus.MessageBus, string) {
	al, busRef, sessionID := newTurnend1651Harness(t, &approvalStopProvider{}, func(cfg *config.Config) {
		// The ask-policy ceiling entry must exist at BOTH layers (the d16
		// harness pattern, approval_stop_test.go): with no ceiling entry the
		// resolver fails closed to deny before the interactive approver is
		// ever consulted.
		cfg.Sandbox = config.OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"approval_stop_test_tool": "ask"},
		}
	})
	// Match gateway boot: durable Stop state requires the installed lifecycle
	// and inbox stores, plus the execution identity's genuine boot epoch.
	installTestSessionLauncher(t, al)

	al.RegisterTool(&interruptibleTool{name: "approval_stop_test_tool"})
	if inst, ok := al.GetRegistry().GetAgent("mia"); ok {
		inst.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"approval_stop_test_tool": "ask"},
		})
	}

	approver := &parkingApprover{entered: make(chan struct{}), release: make(chan struct{})}
	al.SetToolApprover(approver)
	t.Cleanup(func() {
		select {
		case <-approver.release:
		default:
			close(approver.release)
		}
	})

	publishTurnend1651Message(t, busRef, sessionID, "please run the ask tool")
	waitForTurnend1651Channel(t, approver.entered, 10*time.Second,
		"timeout waiting for the turn to park on the pending approval")
	return al, busRef, sessionID
}

// stopTurnend1651Turn fires the real Stop (polite then forced) over the
// parked turn.
func stopTurnend1651Turn(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	cancelOutcome, cancelErr := al.StopSession(
		context.Background(),
		StopRequest{
			SessionID: sessionID,
			By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "turnend-1651-test-user"},
			Channel:   "web",
		},
	)
	require.NoError(t, cancelErr)
	require.True(t, cancelOutcome.Fired, "Stop must select the parked turn")
}
