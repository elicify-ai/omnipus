// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RELEASE BLOCKER regression tests: POST /agents returned 201 for an agent that
// POST /tasks then rejected as "agent not found".
//
// The chain (all four links verified before this fix landed):
//
//  1. createAgent persists the agent via agentstore.Create inside
//     updateConfigJSONLocked, which synchronously SwapConfigs — so the LIVE
//     config is immediately correct.
//  2. createAgent then called the BARE agentLoop.TriggerReload(), turned any
//     error into a "warning" string, and still answered 201.
//  3. The reload trigger CAS'd a flag and, on CAS failure, returned
//     "reload already in progress" — the request was DROPPED with nothing to
//     re-queue it.
//  4. The in-memory AgentRegistry is rebuilt by exactly ONE path
//     (executeReload → restartServices → ReloadProviderAndConfig →
//     NewAgentRegistry); AgentRegistry.UpsertAgent has no production callers.
//
// So two validators on the same request chain read different-freshness
// sources: validatePlanOwnerAgent reads cfg.Agents.List and PASSES, while
// validateTaskAgentID reads the registry and fails with `agent %q not found`.
// Under load a reload takes tens of seconds, so every create landed mid-reload
// and was silently dropped — deterministic, 9/9 in CI.
//
// The fix has two halves and NEITHER is sufficient alone, which is what these
// tests are built to prove:
//
//   - Part 1 (gateway.go): the trigger COALESCES instead of dropping —
//     services.beginReload records the request and runReloadCycle runs a
//     follow-up reload, re-reading config from disk, before releasing the
//     agent loop's reload-pending flag.
//   - Part 2 (rest.go): createAgent WAITS via triggerReloadAndWait. Alone this
//     only waits out the IN-FLIGHT reload, whose config snapshot predates the
//     create's own write.
//
// Every assertion here is on the user-visible outcome — "is the agent this
// handler just 201'd resolvable by the registry the moment the handler
// returns" — never on the coalescing bookkeeping itself. This branch has
// shipped controls that passed while broken; a mechanism assertion would do it
// again.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// reloadHarness wires the REAL production reload machinery — newReloadTrigger,
// services.beginReload/finishReload, runReloadCycle — to a real AgentLoop and a
// real restAPI, and drives it with a consumer goroutine that mirrors the
// manualReloadChan arm of the gateway's reload loop verbatim.
//
// The only substitution is the reload EXECUTOR: instead of the full
// executeReload (which stops and restarts channels, cron, media, devices and
// rebuilds the provider), the harness runs the one step that matters to this
// bug — agentLoop.ReloadProviderAndConfig, the sole call that rebuilds
// AgentRegistry — plus a test-controlled delay standing in for the tens of
// seconds a reload takes under load. loadNext re-reads config.json and the
// agent entity store from disk exactly as the production loadReloadConfig does.
type reloadHarness struct {
	t        *testing.T
	api      *restAPI
	al       *agent.AgentLoop
	svc      *services
	provider providers.LLMProvider
	homePath string
	cfgPath  string

	// beforeExec runs at the top of every reload, before the registry rebuild.
	// Tests use it to hold a reload "in flight" for a controlled window.
	beforeExec func(execIndex int)

	execCount atomic.Int32

	mu sync.Mutex
	// rosterPerExec records, for each reload, the agent IDs its config snapshot
	// contained. Used to prove a coalesced successor really did re-read from
	// disk rather than reusing the predecessor's stale snapshot.
	rosterPerExec [][]string
}

func newReloadHarness(t *testing.T) *reloadHarness {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	// A real on-disk config: loadNext re-reads this file on every coalesced
	// follow-up reload, so it must carry the agent defaults (agents.list is
	// json:"-" and always comes from the entity store — ADR-054 D2/D3).
	diskCfg := fmt.Sprintf(`{
  "version": 1,
  "agents": {"defaults": {"home": %q, "model_name": "test-model", "max_tokens": 4096}},
  "providers": []
}`, tmpDir)
	require.NoError(t, os.WriteFile(cfgPath, []byte(diskCfg), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				// The fully-enumerated deny-by-default tool policy every custom
				// agent gets. Without it, createAgent's tool-policy coverage
				// guard (CLAUDE.md hard constraint 6) rejects the whole request
				// 400 because the PRE-EXISTING roster has gaps.
				{
					ID:    "seed-agent",
					Name:  "Seed Agent",
					Type:  config.AgentTypeCustom,
					Tools: coreagent.NewCustomAgentToolsCfg(),
				},
			},
		},
	}
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	provider := providers.LLMProvider(&restMockProvider{})
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), provider)

	h := &reloadHarness{
		t:        t,
		al:       al,
		provider: provider,
		homePath: tmpDir,
		cfgPath:  cfgPath,
		svc:      &services{},
	}
	h.api = &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
	}

	// Production wiring, verbatim: cap-1 channel, the real trigger closure, and
	// the trigger installed on the agent loop so restAPI.triggerReloadAndWait →
	// AgentLoop.TriggerReload reaches it exactly as it does in the gateway.
	h.svc.manualReloadChan = make(chan struct{}, 1)
	h.svc.reloadTrigger = newReloadTrigger(h.svc, al)
	al.SetReloadFunc(h.svc.reloadTrigger)

	stop := make(chan struct{})
	drained := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		select {
		case <-drained:
		case <-time.After(10 * time.Second):
			t.Error("reload consumer goroutine did not exit within 10s")
		}
	})
	go func() {
		defer close(drained)
		for {
			select {
			case <-stop:
				return
			case <-h.svc.manualReloadChan:
				// Mirrors RunContextWithOptions' manualReloadChan arm: the slot
				// is already claimed by the trigger, so no beginReload here.
				runReloadCycle(h.al, h.svc, nil, h.exec, h.loadNext)
			}
		}
	}()

	return h
}

// exec stands in for executeReload. It performs the ONE step that makes a
// config change visible to validateTaskAgentID — the AgentRegistry rebuild —
// and records which agents the snapshot it was handed contained.
func (h *reloadHarness) exec(c *config.Config) error {
	idx := int(h.execCount.Add(1))

	ids := make([]string, 0, len(c.Agents.List))
	for i := range c.Agents.List {
		ids = append(ids, c.Agents.List[i].ID)
	}
	h.mu.Lock()
	h.rosterPerExec = append(h.rosterPerExec, ids)
	h.mu.Unlock()

	if h.beforeExec != nil {
		h.beforeExec(idx)
	}
	return h.al.ReloadProviderAndConfig(context.Background(), h.provider, c)
}

// loadNext mirrors the production loadReloadConfig: re-read config.json from
// disk, then repopulate the roster from the agent entity store (config.LoadConfig*
// strips agents.list on every load).
func (h *reloadHarness) loadNext() (*config.Config, error) {
	c, err := config.LoadConfigWithStore(h.cfgPath, nil)
	if err != nil {
		return nil, fmt.Errorf("loading config for reload: %w", err)
	}
	if err = populateAgentsListFromEntityStoreStrict(c, h.homePath); err != nil {
		return nil, fmt.Errorf("agent roster population failed: %w", err)
	}
	return c, nil
}

func (h *reloadHarness) reloadsRun() int { return int(h.execCount.Load()) }

func (h *reloadHarness) snapshotRosters() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]string, len(h.rosterPerExec))
	copy(out, h.rosterPerExec)
	return out
}

// createAgentResult is everything a client can observe about a POST /agents,
// captured at the instant the handler returned — including whether the agent it
// reported is actually resolvable. Capturing inside the handler goroutine is
// essential: a later reload will eventually make the agent visible, so an
// assertion made after the test's own sequencing would pass even on a broken
// build.
type createAgentResult struct {
	status  int
	body    string
	id      string
	warning string
	// resolvedAtReturn is registry visibility sampled immediately after the
	// handler returned — literally validateTaskAgentID's own predicate
	// (rest_tasks.go: `if _, ok := reg.GetAgent(agentID); !ok`).
	resolvedAtReturn bool
	// taskValidationErr is the real validateTaskAgentID verdict at that same
	// instant, so the assertion is the actual production code path and not a
	// re-implementation of it.
	taskValidationErr error
	reloadsAtReturn   int
}

// postAgent runs the real createAgent handler and samples the outcome the
// instant it returns.
func (h *reloadHarness) postAgent(name string) createAgentResult {
	body := fmt.Sprintf(
		`{"type":"Main","name":%q,"soul":"Reload coalescing regression agent."}`, name,
	)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	h.api.createAgent(w, r)

	res := createAgentResult{status: w.Code, body: w.Body.String()}
	var decoded struct {
		ID      string `json:"id"`
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err == nil {
		res.id = decoded.ID
		res.warning = decoded.Warning
	}
	if res.id != "" {
		if reg := h.al.GetRegistry(); reg != nil {
			_, res.resolvedAtReturn = reg.GetAgent(res.id)
		}
		// Empty workspace on purpose: the workspace-membership branch sits
		// AFTER the registry lookup, so "no workspace_id to validate team
		// membership against" proves the registry check was passed, while
		// `agent %q not found` is the blocker's exact signature.
		res.taskValidationErr = h.api.validateTaskAgentID(res.id, "")
	}
	res.reloadsAtReturn = h.reloadsRun()
	return res
}

// assertUsable is the single outcome assertion these tests exist for: whatever
// POST /agents reported, the agent must be immediately assignable via POST
// /tasks.
func assertUsable(t *testing.T, res createAgentResult) {
	t.Helper()
	require.Equal(t, http.StatusCreated, res.status, "POST /agents body: %s", res.body)
	require.NotEmpty(t, res.id, "201 must carry an agent id; body: %s", res.body)

	assert.True(t, res.resolvedAtReturn,
		"RELEASE BLOCKER: POST /agents returned 201 for agent %q but AgentRegistry.GetAgent "+
			"does not resolve it — POST /tasks would reject the agent this call just created. "+
			"(%d reload(s) had run when the handler returned)", res.id, res.reloadsAtReturn)

	if res.taskValidationErr != nil {
		assert.NotContains(t, res.taskValidationErr.Error(), "not found",
			"validateTaskAgentID must not report agent %q as not found immediately after "+
				"POST /agents answered 201; got: %v", res.id, res.taskValidationErr)
	}

	// A 201 that carries a warning must never mean "the agent is unusable".
	// With coalescing, a create landing mid-reload is a normal, fully-served
	// request — there is nothing to warn about.
	assert.Empty(t, res.warning,
		"a create that landed mid-reload must not degrade to a warning-carrying 201; "+
			"the request is served by a coalesced follow-up reload. body: %s", res.body)
}

// blockFirstReload holds reload #1 inside the executor until the returned
// release func is called, simulating the tens-of-seconds reload that makes this
// bug deterministic under load. entered is closed once reload #1 is executing,
// so callers can be sure the slot is genuinely claimed before they proceed.
func (h *reloadHarness) blockFirstReload() (entered <-chan struct{}, release func()) {
	enteredCh := make(chan struct{})
	releaseCh := make(chan struct{})
	var once, releaseOnce sync.Once
	release = func() { releaseOnce.Do(func() { close(releaseCh) }) }
	// Always release on teardown: a failing assertion aborts the test goroutine
	// via runtime.Goexit, and without this the parked reload would hold the
	// consumer goroutine forever and the whole package would hang instead of
	// reporting the failure.
	h.t.Cleanup(release)
	h.beforeExec = func(idx int) {
		if idx != 1 {
			return
		}
		once.Do(func() { close(enteredCh) })
		<-releaseCh
	}
	return enteredCh, release
}

// waitForCoalescedRequest blocks until a reload request has been recorded
// against the in-flight reload. This is SEQUENCING only — it guarantees the
// test exercises the mid-reload interleaving rather than a trivially-ordered
// one. The assertions that follow never read this state.
func (h *reloadHarness) waitForCoalescedRequest(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.svc.reloadCoalesceMu.Lock()
		got := h.svc.reloadRequested
		h.svc.reloadCoalesceMu.Unlock()
		if got {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// TestCreateAgent_DuringInFlightReload_IsImmediatelyTaskAssignable is the DoD
// test for the blocker. It goes RED with either half of the fix reverted:
//
//   - revert part 1 (trigger drops instead of coalescing): createAgent's
//     triggerReloadAndWait returns when the IN-FLIGHT reload ends, and that
//     reload's config snapshot predates the create's write, so the registry
//     never learns about the agent.
//   - revert part 2 (bare TriggerReload): the trigger coalesces and returns nil
//     immediately, so the handler answers 201 before the follow-up reload has
//     run at all.
func TestCreateAgent_DuringInFlightReload_IsImmediatelyTaskAssignable(t *testing.T) {
	h := newReloadHarness(t)

	entered, release := h.blockFirstReload()

	// Reload #1 starts and parks inside the executor: its config snapshot is
	// taken NOW, before the agent below exists anywhere on disk.
	require.NoError(t, h.svc.reloadTrigger(), "starting the first reload must succeed")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("the first reload never started executing")
	}

	done := make(chan createAgentResult, 1)
	go func() { done <- h.postAgent("Coalesce Target") }()

	// Only release reload #1 once the create's own reload request has landed
	// against it — otherwise the test would exercise a boring sequential
	// ordering instead of the mid-reload interleaving that breaks.
	coalesced := h.waitForCoalescedRequest(3 * time.Second)
	release()

	var res createAgentResult
	select {
	case res = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("createAgent never returned")
	}

	assert.True(t, coalesced,
		"the reload POST /agents requested while another reload was in flight must be "+
			"recorded for a follow-up reload, not dropped")
	assertUsable(t, res)

	// The follow-up reload must have re-read from disk, not replayed the
	// snapshot reload #1 already used.
	rosters := h.snapshotRosters()
	require.GreaterOrEqual(t, len(rosters), 2,
		"a coalesced request must produce a SECOND reload; only %d ran", len(rosters))
	assert.NotContains(t, rosters[0], res.id,
		"precondition: reload #1's snapshot must predate the create (else the test proves nothing)")
	assert.Contains(t, rosters[len(rosters)-1], res.id,
		"the coalesced follow-up reload must re-read config from disk and see the new agent")
}

// TestCreateAgent_TwoRapidCreatesDuringOneReload_BothTaskAssignable proves the
// fix is real coalescing and not a single retry: two creates that both land
// inside one long reload must BOTH be registry-visible when their handlers
// return. A one-shot retry would serve the first and lose the second.
func TestCreateAgent_TwoRapidCreatesDuringOneReload_BothTaskAssignable(t *testing.T) {
	h := newReloadHarness(t)

	entered, release := h.blockFirstReload()

	require.NoError(t, h.svc.reloadTrigger(), "starting the first reload must succeed")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("the first reload never started executing")
	}

	first := make(chan createAgentResult, 1)
	second := make(chan createAgentResult, 1)
	go func() { first <- h.postAgent("Coalesce Rapid One") }()
	// Sequencing only, and deliberately non-fatal: failing here would abandon
	// two in-flight handler goroutines mid-test. The outcome assertions below
	// are what decide the verdict.
	coalescedFirst := h.waitForCoalescedRequest(3 * time.Second)
	go func() { second <- h.postAgent("Coalesce Rapid Two") }()
	time.Sleep(100 * time.Millisecond)

	// Both creates are now outstanding against a single blocked reload.
	release()

	var resA, resB createAgentResult
	for i := 0; i < 2; i++ {
		select {
		case resA = <-first:
			first = nil
		case resB = <-second:
			second = nil
		case <-time.After(15 * time.Second):
			t.Fatal("a createAgent call never returned")
		}
	}

	assert.True(t, coalescedFirst,
		"the first create's reload request must be recorded against the in-flight reload")
	assertUsable(t, resA)
	assertUsable(t, resB)
	assert.NotEqual(t, resA.id, resB.id, "the two creates must produce distinct agents")
}

// TestReloadCycle_DoesNotReleasePollersBetweenCoalescedReloads pins the caveat
// that makes part 1 correct: the agent loop's reload-pending flag — the ONLY
// thing restAPI.triggerReloadAndWait polls — must stay set across the boundary
// between a reload and its coalesced successor. Clearing it in between would
// let a poller return against a registry rebuilt from the older snapshot,
// reintroducing the blocker through a different door.
//
// The outcome is asserted the way a caller experiences it: a waiter that
// requested a reload mid-flight must not be released until a reload that
// STARTED AFTER its request has finished.
func TestReloadCycle_DoesNotReleasePollersBetweenCoalescedReloads(t *testing.T) {
	h := newReloadHarness(t)

	entered, release := h.blockFirstReload()

	require.NoError(t, h.svc.reloadTrigger(), "starting the first reload must succeed")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("the first reload never started executing")
	}

	// A second reload request, coalesced into the running one.
	type waitOutcome struct {
		err     error
		reloads int
	}
	waiterDone := make(chan waitOutcome, 1)
	go func() {
		err := h.api.triggerReloadAndWait()
		waiterDone <- waitOutcome{err: err, reloads: h.reloadsRun()}
	}()
	// Sequencing only, non-fatal — see the note in the two-create test.
	coalesced := h.waitForCoalescedRequest(3 * time.Second)

	// The waiter must still be blocked: reload #1 has not even finished.
	returnedEarly := -1
	select {
	case got := <-waiterDone:
		returnedEarly = got.reloads
	case <-time.After(200 * time.Millisecond):
	}

	release()

	assert.True(t, coalesced,
		"the second request must be recorded against the in-flight reload")
	assert.Equal(t, -1, returnedEarly,
		"triggerReloadAndWait returned after only %d reload(s), while reload #1 was still "+
			"executing and its coalesced successor had not started", returnedEarly)
	if returnedEarly != -1 {
		return
	}

	select {
	case got := <-waiterDone:
		require.NoError(t, got.err)
		assert.GreaterOrEqual(t, got.reloads, 2,
			"triggerReloadAndWait must not return between a reload and its coalesced "+
				"successor; it returned after %d reload(s)", got.reloads)
	case <-time.After(15 * time.Second):
		t.Fatal("triggerReloadAndWait never returned")
	}
}

// TestReloadTrigger_MidFlightRequestIsAcceptedNotRejected records the contract
// change at the trigger itself: a reload requested while another is running is
// ACCEPTED (nil), because it will be served. It used to return
// "reload already in progress", which callers turned into a user-visible
// warning on an otherwise-successful 201 — while the request itself evaporated.
func TestReloadTrigger_MidFlightRequestIsAcceptedNotRejected(t *testing.T) {
	h := newReloadHarness(t)

	entered, release := h.blockFirstReload()
	defer release()

	require.NoError(t, h.svc.reloadTrigger())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first reload never started executing")
	}

	require.NoError(t, h.svc.reloadTrigger(),
		"a reload requested mid-flight must be accepted for a follow-up run, not rejected")

	// And the agent-loop wrapper must not surface it as ErrReloadAlreadyInProgress
	// either — that is the error createAgent used to convert into a warning.
	require.NoError(t, h.al.TriggerReload(),
		"AgentLoop.TriggerReload must not report a mid-flight request as an error")

	release()
	require.Eventually(t, func() bool { return h.reloadsRun() >= 2 }, 10*time.Second, 10*time.Millisecond,
		"the coalesced request must actually run a second reload")
}

// TestCreateAgent_ReloadSlowerThanWaitDeadline_IsStillTaskAssignable is the
// regression test for the SECOND half of this release blocker — the half the
// coalescing fix did not touch, and which kept the llm-conformance e2e shard
// failing 3/9 with the original signature after coalescing had shipped.
//
// triggerReloadAndWait polled for a hard-coded 5 SECONDS and then `return nil`
// — reporting success it had not observed. A reload restarts every channel,
// cron, the plan engine and the provider before rebuilding the AgentRegistry;
// idle that is milliseconds, but under the conformance shard's load (live LLM
// turns + a busy plan engine) it runs to tens of seconds. Every reload that
// overran 5s therefore produced a 201 with NO warning and a registry that still
// had no such agent, so the next POST /tasks answered `agent "x" not found` and
// POST /workspaces answered `core_team member "x" is not a registered agent`.
//
// This is why coalescing alone was not enough: it guaranteed the right reload
// was QUEUED and would run, but the caller stopped waiting for it and lied
// about the result.
//
// The reload here takes 6s — longer than the old deadline, far inside the new
// one — and the assertion is the same user-visible outcome as every other test
// in this file.
func TestCreateAgent_ReloadSlowerThanWaitDeadline_IsStillTaskAssignable(t *testing.T) {
	h := newReloadHarness(t)

	// Every reload takes longer than the retired 5s deadline. Deliberately a
	// wall-clock sleep: the property under test IS a wall-clock deadline.
	const slowReload = 6 * time.Second
	require.Greater(t, slowReload, 5*time.Second,
		"the simulated reload must exceed the deadline this test exists to retire")
	require.Less(t, slowReload, reloadWaitTimeout,
		"...and must sit inside the current deadline, or this asserts the wrong thing")
	h.beforeExec = func(int) { time.Sleep(slowReload) }

	res := h.postAgent("Slow Reload Target")
	assertUsable(t, res)
}

// TestTriggerReloadAndWait_TimeoutIsReportedNotSwallowed pins the honesty half
// of the same fix: when the rebuild genuinely does not land inside the
// deadline, the caller must be TOLD. Returning nil there is what let handlers
// answer 201/200 for a change that was only on disk — and it is what made the
// e2e failures surface several steps downstream, as a mystifying
// "agent not found", instead of at the create that caused them.
func TestTriggerReloadAndWait_TimeoutIsReportedNotSwallowed(t *testing.T) {
	h := newReloadHarness(t)

	// A reload function that accepts the request but never completes it, so the
	// pending flag stays set and the wait must hit its deadline.
	h.al.SetReloadFunc(func() error { return nil })

	// Model a genuinely outstanding reload: the gateway's own trigger sets this
	// flag under reloadCoalesceMu when it queues a cycle. TriggerReload does not
	// set it, precisely so a fake reload function cannot leave it stuck on.
	h.al.MarkReloadPending()

	// Assert the behaviour at expiry, not the size of the production constant —
	// waiting the real 60s would add a minute to the suite for no extra signal.
	restore := reloadWaitTimeout
	reloadWaitTimeout = 300 * time.Millisecond
	t.Cleanup(func() { reloadWaitTimeout = restore })

	start := time.Now()
	err := h.api.triggerReloadAndWait()
	elapsed := time.Since(start)

	require.Error(t, err,
		"a reload that does not complete within the deadline must be reported, never "+
			"swallowed as success — callers turn a nil here into a 201/200 for a change "+
			"that is not live")
	assert.Contains(t, err.Error(), "not yet active",
		"the error must say the change is saved but not active, so the operator can tell "+
			"it from a failed write")
	assert.GreaterOrEqual(t, elapsed, reloadWaitTimeout,
		"the wait must actually honour its full deadline before giving up")
}

// TestReloadCycle_FollowUpConfigLoadFailure_LeavesPendingSetAndMarksDegraded
// guards the FIXED abnormal exit (14-reviewer sign-off finding #4). This test
// used to assert the OPPOSITE — that a failed follow-up load clears the
// reload-pending flag "or every triggerReloadAndWait caller hangs for its
// full deadline" — but that was itself the bug: clearing the flag here made
// every triggerReloadAndWaitOutcome poller still waiting on this cycle
// observe IsReloadPending()==false and report confirmed=true, a false "your
// change is live" for a request that beginReload/markPending had recorded
// but that this cycle never actually served (no exec ever ran with a valid
// config for it — loadNext failed before exec was reached).
//
// The single-flight SLOT must still be released (that part of the old
// assertion was correct) or every future reload is wedged forever — see
// abandonReload's own doc comment. What must NOT happen is treating "slot
// released" and "pending cleared" as one inseparable unit: they are
// deliberately decoupled here, mirroring newReloadTrigger's own
// abandonReload(nil) fail-safe a few dozen lines above (used for the
// identical reason — a request recorded but never served).
func TestReloadCycle_FollowUpConfigLoadFailure_LeavesPendingSetAndMarksDegraded(t *testing.T) {
	h := newReloadHarness(t)

	require.True(t, h.svc.beginReload(h.al.MarkReloadPending), "slot must be free at the start of the test")

	loadCalls := 0
	failingLoad := func() (*config.Config, error) {
		loadCalls++
		return nil, fmt.Errorf("simulated config load failure")
	}
	// Request a follow-up reload from inside the first one, so the cycle takes
	// the coalesced branch and then hits the failing load.
	exec := func(c *config.Config) error {
		h.svc.reloadCoalesceMu.Lock()
		h.svc.reloadRequested = true
		h.svc.reloadCoalesceMu.Unlock()
		return nil
	}

	cfg := h.al.GetConfig()
	h.al.TriggerReload() //nolint:errcheck // sets reloadPending; the slot is already held above
	runReloadCycle(h.al, h.svc, cfg, exec, failingLoad)

	assert.Equal(t, 2, loadCalls,
		"the coalesced follow-up must retry the load once (self-heals a transient read race) "+
			"before giving up — failingLoad always errors, so exactly 2 attempts are expected")
	assert.True(t, h.al.IsReloadPending(),
		"a follow-up load failure (even after the retry) must LEAVE the reload-pending flag "+
			"SET — clearing it reports a false confirmed=true to every "+
			"triggerReloadAndWaitOutcome poller still waiting on this cycle, for a request that "+
			"was never actually served")
	assert.True(t, h.svc.beginReload(h.al.MarkReloadPending),
		"a failed follow-up load must still release the single-flight slot, "+
			"or every future reload is wedged")

	h.svc.reloadMu.Lock()
	degraded, reloadErr := h.svc.reloadDegraded, h.svc.reloadError
	h.svc.reloadMu.Unlock()
	assert.True(t, degraded,
		"a follow-up load failure must mark the service degraded so GET /health surfaces it "+
			"immediately rather than only through the (now honestly-still-set) pending flag")
	assert.Error(t, reloadErr)
}

// TestReloadCycle_FollowUpConfigLoadFailure_TransientRetrySucceeds proves the
// self-heal half of the same fix: a load that fails once (e.g. a concurrent
// config.json writer racing safeUpdateConfigJSON's atomic rename against this
// read) but succeeds on the immediate retry must complete the coalesced
// reload NORMALLY — pending cleared, slot released, not degraded — rather
// than being treated as the genuine, request-never-served failure the sibling
// test above covers.
func TestReloadCycle_FollowUpConfigLoadFailure_TransientRetrySucceeds(t *testing.T) {
	h := newReloadHarness(t)

	require.True(t, h.svc.beginReload(h.al.MarkReloadPending), "slot must be free at the start of the test")

	loadCalls := 0
	flakyLoad := func() (*config.Config, error) {
		loadCalls++
		if loadCalls == 1 {
			return nil, fmt.Errorf("simulated transient config load failure")
		}
		return h.al.GetConfig(), nil
	}
	execCalls := 0
	exec := func(c *config.Config) error {
		execCalls++
		if execCalls == 1 {
			// First reload requests a follow-up, so the cycle takes the
			// coalesced branch and reaches flakyLoad.
			h.svc.reloadCoalesceMu.Lock()
			h.svc.reloadRequested = true
			h.svc.reloadCoalesceMu.Unlock()
		}
		return nil
	}

	cfg := h.al.GetConfig()
	h.al.TriggerReload() //nolint:errcheck // sets reloadPending; the slot is already held above
	runReloadCycle(h.al, h.svc, cfg, exec, flakyLoad)

	assert.Equal(t, 2, loadCalls, "the retry must have fired exactly once, and it must have succeeded")
	assert.Equal(t, 2, execCalls, "the coalesced reload must have run exec once the retried load succeeded")
	assert.False(t, h.al.IsReloadPending(),
		"a coalesced reload that completes normally (after a self-healed transient load "+
			"failure) must clear the reload-pending flag exactly like any other successful cycle")
	assert.True(t, h.svc.beginReload(h.al.MarkReloadPending), "the slot must be released on normal completion")

	h.svc.reloadMu.Lock()
	degraded := h.svc.reloadDegraded
	h.svc.reloadMu.Unlock()
	assert.False(t, degraded, "a self-healed transient load failure must not mark the service degraded")
}
