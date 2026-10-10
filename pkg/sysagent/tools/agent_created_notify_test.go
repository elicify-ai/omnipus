// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// Tests for the agent-picker-freshness fix (#1009)'s sysagent-side half:
// create_agent (an agent creating another agent mid-conversation) persists
// straight to the entity store and never reaches the gateway's REST
// createAgent handler, so it needs its OWN notification hook
// (systools.Deps.NotifyAgentCreated) to reach the same agent_created WS
// broadcast the REST path fires — otherwise the tab watching the
// conversation that ran create_agent would never see the new agent appear
// in its Agent Picker until the query's 30s staleTime elapsed or a reload
// happened.
//
// These tests reuse buildSysagentFastUpsertTestLoop /
// newSysagentFastUpsertDeps from agent_fast_upsert_test.go (same package,
// same file set) so the harness composes with the fast-upsert path rather
// than duplicating a second AgentLoop/Deps builder.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestAgentCreate_NotifiesAgentCreated proves create_agent invokes
// Deps.NotifyAgentCreated exactly once, with the newly created agent's ID,
// after the agent is durably persisted.
func TestAgentCreate_NotifiesAgentCreated(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var notifiedIDs []string
	deps.NotifyAgentCreated = func(agentID string) {
		notifiedIDs = append(notifiedIDs, agentID)
	}

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Notify Create Agent",
		"description": "proves create_agent invokes NotifyAgentCreated",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
	})
	if result.IsError {
		t.Fatalf("create_agent failed: %s", result.ForLLM)
	}
	created := parseSuccess(t, result.ForLLM)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create_agent response missing id: %+v", created)
	}

	if len(notifiedIDs) != 1 {
		t.Fatalf("NotifyAgentCreated must be called exactly once per landed create; got %v", notifiedIDs)
	}
	if notifiedIDs[0] != id {
		t.Fatalf("NotifyAgentCreated called with agent id %q, want %q (the just-created agent)", notifiedIDs[0], id)
	}
}

// TestAgentCreate_ValidationFailure_DoesNotNotifyAgentCreated proves a
// request that never persists an agent (missing required field) does not
// call NotifyAgentCreated — mirroring the REST path's "never on a 4xx"
// rule: the hook means "a new agent now exists", and a refused create
// created nothing.
func TestAgentCreate_ValidationFailure_DoesNotNotifyAgentCreated(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var notifyCalls atomic.Int32
	deps.NotifyAgentCreated = func(string) { notifyCalls.Add(1) }

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		// name deliberately omitted — validate() rejects this before persistAndJoin runs.
		"description": "missing name must error, never persist",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
	})
	if !result.IsError {
		t.Fatalf("create_agent with no name must fail validation, got success: %s", result.ForLLM)
	}
	if got := notifyCalls.Load(); got != 0 {
		t.Fatalf("a request that never persisted an agent must not call NotifyAgentCreated; got %d calls", got)
	}
}

// TestAgentCreate_NotifyFiresOnlyAfterAgentIsListable is the ordering-fix
// regression proof for #1009's reappearance: it asserts NotifyAgentCreated
// fires only AFTER publishAgentActivation has actually landed the new agent
// in the live, in-memory config/registry that pkg/gateway/rest_agents.go::
// listAgents (GET /api/v1/agents) reads — not merely after persistAndJoin's
// durable disk write. It checks this synchronously, from INSIDE the notify
// callback itself: al.GetConfig().Agents.List must already contain the new
// agent's ID at the exact moment the callback runs. Against the pre-fix call
// order (notify right after persistAndJoin, before publishAndRespond /
// publishAgentActivation), this would fail — see this task's report for the
// red run captured by temporarily reverting the ordering change.
//
// This test uses the synchronous harness (newSysagentFastUpsertDeps) and so
// proves the FAST-PATH half of the fix only: that publication that lands
// inline never races the notify. The async-reload half — publication that
// ONLY enqueues a reload (the production wiring, where the gateway's
// UpsertAgentFastFunc closure falls back to reloadTrigger on any failure
// and the bare ReloadFunc is also fire-and-forget) — is exercised by
// TestAgentCreate_NotifyFiresOnlyAfterAsyncReloadLands below, which uses a
// custom double whose publish hook returns nil immediately and whose
// registry update applies later on a goroutine, exactly like the real
// gateway_reload.go::newReloadTrigger path.
func TestAgentCreate_NotifyFiresOnlyAfterAgentIsListable(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var (
		notifyCalls     int
		listedAtNotify  bool
		notifiedAgentID string
	)
	deps.NotifyAgentCreated = func(agentID string) {
		notifyCalls++
		notifiedAgentID = agentID
		for _, a := range al.GetConfig().Agents.List {
			if a.ID == agentID {
				listedAtNotify = true
				break
			}
		}
	}

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Ordering Proof Agent",
		"description": "proves NotifyAgentCreated fires only once the agent is live-listable",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
	})
	if result.IsError {
		t.Fatalf("create_agent failed: %s", result.ForLLM)
	}
	created := parseSuccess(t, result.ForLLM)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create_agent response missing id: %+v", created)
	}

	if notifyCalls != 1 {
		t.Fatalf("NotifyAgentCreated must be called exactly once per landed create; got %d calls", notifyCalls)
	}
	if notifiedAgentID != id {
		t.Fatalf("NotifyAgentCreated called with agent id %q, want %q", notifiedAgentID, id)
	}
	if !listedAtNotify {
		t.Fatalf("NotifyAgentCreated fired BEFORE the agent was live-listable: " +
			"al.GetConfig().Agents.List did not yet contain the new agent's ID at notify time — " +
			"this is the #1009 ordering race (notify racing publishAgentActivation)")
	}
}

// TestAgentCreate_InitAgentHomeFailure_DoesNotNotifyAgentCreated proves
// NotifyAgentCreated does not fire when persistAndJoin's InitAgentHome step
// fails AFTER agentstore.CreateState has already durably persisted the
// entity — a LATER failure gate than
// TestAgentCreate_ValidationFailure_DoesNotNotifyAgentCreated's earliest
// field-validation rejection. This closes the gap that test leaves open: a
// mutation that hoisted the notify call to fire right after persistAndJoin's
// CreateState succeeds (the #1009 bug's exact original position, before this
// fix moved the call into publishAndRespond gated on publishAgentActivation)
// would still pass the validation-failure test, because that test's request
// never reaches persistAndJoin at all. This test does reach it, and fails at
// a step strictly after CreateState.
//
// Setup deliberately hits InitAgentHome's os.MkdirAll call — NOT
// agentstore.CreateState's stage() MkdirAll (which shares the
// home/agents/<id> directory path). The fix-round-2 version of this test
// pre-created a regular file at home/agents/<id>, but stage() ALSO does
// os.MkdirAll(filepath.Dir(soulPath)) = home/agents/<id> — and fails there
// FIRST, before persistAndJoin ever reaches InitAgentHome. The test was
// therefore asserting "CreateState failure ⇒ no notify" (a case covered by
// the early validation gate above), not the InitAgentHome branch it was
// named for. The corrected setup:
//
//  1. Pre-create home/agents/<id> as a DIRECTORY (so CreateState's
//     stage() succeeds for both the entity and SOUL files).
//  2. Pre-create a REGULAR FILE at home/agents/<id>/sessions — the FIRST
//     subdirectory InitAgentHome's loop tries to os.MkdirAll (see
//     pkg/datamodel/init.go::InitAgentHome, "subdirs = []string{"sessions",
//     ...}"). MkdirAll on a path whose child already exists as a non-directory
//     fails deterministically and portably (no reliance on permission bits,
//     which behave inconsistently as root or across platforms).
//
// toSlug("Init Home Fail Agent") deterministically yields
// "init-home-fail-agent" (lowercase, spaces to hyphens). The test asserts
// the PARTIAL envelope (persistence_status="partial", error_stage="init_home",
// the "agent home could not be initialized" message — the shape
// createHomePartialResult emits), not just a generic IsError + zero-notify
// proof, so it actually proves the InitAgentHome branch was reached.
func TestAgentCreate_InitAgentHomeFailure_DoesNotNotifyAgentCreated(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var notifyCalls atomic.Int32
	deps.NotifyAgentCreated = func(string) { notifyCalls.Add(1) }

	const agentID = "init-home-fail-agent"
	agentsRoot := filepath.Join(home, "agents")
	agentHome := filepath.Join(agentsRoot, agentID)
	sessionsCollision := filepath.Join(agentHome, "sessions")

	if err := os.MkdirAll(agentHome, 0o700); err != nil {
		t.Fatalf("setup: mkdir agent home dir: %v", err)
	}
	if err := os.WriteFile(sessionsCollision, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: write colliding file at %s: %v", sessionsCollision, err)
	}

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Init Home Fail Agent",
		"description": "proves an InitAgentHome failure after entity save never notifies",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
	})
	if !result.IsError {
		t.Fatalf("create_agent must fail when InitAgentHome cannot create the agent's sessions/ subdir "+
			"(collision at %s), got success: %s", sessionsCollision, result.ForLLM)
	}
	if got := notifyCalls.Load(); got != 0 {
		t.Fatalf("a persistAndJoin failure AFTER the entity is already durable must not call "+
			"NotifyAgentCreated (the agent is not yet listable); got %d calls", got)
	}

	// PARTIAL envelope shape — assert the specifics of the
	// createHomePartialResult branch, not just a generic IsError, so a
	// regression that re-routed the failure to a different stop=true path
	// would still be caught by THIS test (rather than silently passing on
	// a different failure with the same generic zero-notify outcome).
	partial := createIntegrityPartialBody(t, result.ForLLM)
	if partial["persistence_status"] != string(agentstore.PersistencePartial) {
		t.Errorf("persistence_status=%v, want %s (the InitAgentHome-failure partial envelope)",
			partial["persistence_status"], agentstore.PersistencePartial)
	}
	if partial["error_stage"] != "init_home" {
		t.Errorf("error_stage=%v, want init_home (proves the InitAgentHome branch was actually reached, "+
			"not a CreateState-stage failure earlier in persistAndJoin)", partial["error_stage"])
	}
	if partial["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("activation_status=%v, want %s (InitAgentHome failure never attempts live publish)",
			partial["activation_status"], agentstore.ActivationNotAttempted)
	}
	if msg, _ := partial["message"].(string); !strings.Contains(msg, "agent home could not be initialized") {
		t.Errorf("message=%q, want it to contain 'agent home could not be initialized' (createHomePartialResult's shape)", msg)
	}
	if rev, _ := partial["revision"].(string); rev == "" {
		t.Errorf("revision missing on partial envelope: %#v — caller needs this to read the durable entity", partial["revision"])
	}

	// The entity record itself IS durable (CreateState succeeded before the
	// InitAgentHome collision): this is what the PARTIAL envelope asserts —
	// read the persisted entity so the test fails loudly if a regression
	// somehow dropped the durable write.
	if _, err := agentstore.New(home).Get(agentID); err != nil {
		t.Fatalf("entity record must remain after partial InitAgentHome failure: %v", err)
	}
}

// TestAgentCreate_NilNotifyAgentCreatedIsSafe proves create_agent survives a
// nil Deps.NotifyAgentCreated (the field's documented "nil in tests or when
// not wired" contract) rather than nil-dereferencing.
func TestAgentCreate_NilNotifyAgentCreatedIsSafe(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)
	// deps.NotifyAgentCreated left nil deliberately.

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Nil Notify Agent",
		"description": "proves a nil NotifyAgentCreated never panics",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
	})
	if result.IsError {
		t.Fatalf("create_agent failed: %s", result.ForLLM)
	}
}

// TestAgentCreate_DefaultSingletonWriteFailure_DoesNotNotifyAgentCreated
// proves NotifyAgentCreated does not fire when persistAndJoin's
// writeDefaultSingleton step fails AFTER agentstore.CreateState has already
// durably persisted the entity — the SECOND of persistAndJoin's three
// post-CreateState failure branches. Companion to
// TestAgentCreate_InitAgentHomeFailure_DoesNotNotifyAgentCreated above and
// TestAgentCreate_HeartbeatWriteFailure_DoesNotNotifyAgentCreated below.
//
// The pre-existing TestUpdateAgent_DefaultSingletonSaveFailureReportsPartial
// (pkg/sysagent/tools/agent_adr090_config_test.go) exercises the matching
// update_agent branch and asserts the partial envelope, but its deps (from
// newTestDeps()) don't wire an observable NotifyAgentCreated — so a
// regression that hoisted the notify call above persistAndJoin's
// default-singleton failure gate would pass that test. This test wires the
// spy and asserts zero calls, mirroring the InitAgentHome test's shape.
//
// Trigger: writeDefaultSingleton calls deps.WithConfig, which calls
// deps.SaveConfigLocked after the fn returns nil. A
// SaveConfigLocked returning error surfaces back up through WithConfig and
// persistAndJoin's gate as the partial envelope, with publishAndRespond
// (and therefore NotifyAgentCreated) never reached.
func TestAgentCreate_DefaultSingletonWriteFailure_DoesNotNotifyAgentCreated(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)
	// Overwrite SaveConfigLocked AFTER newSysagentFastUpsertDeps sets it to
	// a no-op: this is the failure the test is exercising — writeDefaultSingleton
	// calls deps.WithConfig, which calls SaveConfigLocked, which fails here.
	deps.SaveConfigLocked = func(*config.Config) error { return errors.New("disk full") }

	var notifyCalls atomic.Int32
	deps.NotifyAgentCreated = func(string) { notifyCalls.Add(1) }

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Default Singleton Fail Agent",
		"description": "proves a default-singleton write failure after entity save never notifies",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"default":     true,
	})
	if !result.IsError {
		t.Fatalf("create_agent with default=true must fail when writeDefaultSingleton cannot persist the "+
			"singleton, got success: %s", result.ForLLM)
	}
	if got := notifyCalls.Load(); got != 0 {
		t.Fatalf("a persistAndJoin failure at the default-singleton gate must not call "+
			"NotifyAgentCreated (the agent is durable but not yet listable); got %d calls", got)
	}

	// PARTIAL envelope shape — assert the specifics of the
	// defaultSingletonPartialResult branch, not just a generic IsError, so a
	// regression that re-routed the failure to a different stop=true path
	// would still be caught by THIS test.
	partial := createIntegrityPartialBody(t, result.ForLLM)
	if partial["persistence_status"] != string(agentstore.PersistencePartial) {
		t.Errorf("persistence_status=%v, want %s (the default-singleton-failure partial envelope)",
			partial["persistence_status"], agentstore.PersistencePartial)
	}
	if partial["error_stage"] != "defaults_singleton" {
		t.Errorf("error_stage=%v, want defaults_singleton (proves the default-singleton branch was actually "+
			"reached, not a different persistAndJoin failure)", partial["error_stage"])
	}
	if partial["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("activation_status=%v, want %s (default-singleton failure never attempts live publish)",
			partial["activation_status"], agentstore.ActivationNotAttempted)
	}
	if msg, _ := partial["message"].(string); !strings.Contains(msg, "default-agent singleton could not be updated") {
		t.Errorf("message=%q, want it to contain 'default-agent singleton could not be updated' "+
			"(defaultSingletonPartialResult's shape)", msg)
	}
}

// TestAgentCreate_HeartbeatWriteFailure_DoesNotNotifyAgentCreated proves
// NotifyAgentCreated does not fire when persistAndJoin's HEARTBEAT.md write
// step fails AFTER agentstore.CreateState + InitAgentHome have both
// durably persisted the entity and its home — the THIRD of persistAndJoin's
// three post-CreateState failure branches.
//
// The pre-existing TestCreateAgent_HeartbeatWriteFailureReportsPartial
// (pkg/sysagent/tools/agent_create_integrity_test.go) checks the response
// envelope's persistence_status / error_stage / revision / entity-still-readable
// but uses newTestDeps() which doesn't wire an observable NotifyAgentCreated
// — so a regression that hoisted the notify call above this failure gate
// would pass that test. This test wires the spy and asserts zero calls,
// mirroring the InitAgentHome and DefaultSingleton tests above.
//
// Setup: pre-create the EXACT path persistAndJoin will try to WriteFile
// (home/agents/<id>/HEARTBEAT.md) as a DIRECTORY — os.WriteFile on a
// directory path fails deterministically and portably (no reliance on
// permission bits). toSlug("Hb Notify Fail Agent") deterministically yields
// "hb-notify-fail-agent".
func TestAgentCreate_HeartbeatWriteFailure_DoesNotNotifyAgentCreated(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	var notifyCalls atomic.Int32
	deps.NotifyAgentCreated = func(string) { notifyCalls.Add(1) }

	const agentID = "hb-notify-fail-agent"
	hbPath := filepath.Join(home, "agents", agentID, "HEARTBEAT.md")
	if err := os.MkdirAll(hbPath, 0o700); err != nil {
		t.Fatalf("setup: mkdir %s as a directory so WriteFile will fail: %v", hbPath, err)
	}

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Hb Notify Fail Agent",
		"description": "proves a HEARTBEAT.md write failure after entity save never notifies",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"heartbeat":   "ping every morning",
	})
	if !result.IsError {
		t.Fatalf("create_agent with heartbeat must fail when HEARTBEAT.md cannot be written "+
			"(collision at %s), got success: %s", hbPath, result.ForLLM)
	}
	if got := notifyCalls.Load(); got != 0 {
		t.Fatalf("a persistAndJoin failure at the HEARTBEAT.md gate must not call "+
			"NotifyAgentCreated (the agent is durable but not yet listable); got %d calls", got)
	}

	// PARTIAL envelope shape — assert the specifics of the
	// createHeartbeatPartialResult branch, not just a generic IsError, so a
	// regression that re-routed the failure to a different stop=true path
	// would still be caught by THIS test.
	partial := createIntegrityPartialBody(t, result.ForLLM)
	if partial["persistence_status"] != string(agentstore.PersistencePartial) {
		t.Errorf("persistence_status=%v, want %s (the HEARTBEAT.md-failure partial envelope)",
			partial["persistence_status"], agentstore.PersistencePartial)
	}
	if partial["error_stage"] != "heartbeat" {
		t.Errorf("error_stage=%v, want heartbeat (proves the HEARTBEAT.md branch was actually reached)",
			partial["error_stage"])
	}
	if partial["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("activation_status=%v, want %s (HEARTBEAT.md failure never attempts live publish)",
			partial["activation_status"], agentstore.ActivationNotAttempted)
	}
	if msg, _ := partial["message"].(string); !strings.Contains(msg, "HEARTBEAT.md could not be written") {
		t.Errorf("message=%q, want it to contain 'HEARTBEAT.md could not be written' "+
			"(createHeartbeatPartialResult's shape)", msg)
	}
}

// TestAgentCreate_NotifyFiresOnlyAfterAsyncReloadLands is the
// ASYNC-RELOAD half of the ordering-fix regression proof for #1009's
// reappearance (companion to TestAgentCreate_NotifyFiresOnlyAfterAgentIsListable
// above, which covers only the fast-path / inline-publication half).
//
// The production wiring this test mirrors (pkg/gateway/gateway.go's
// sysAgentDeps) is NOT guaranteed synchronous on its nil return:
//  1. The UpsertAgentFastFunc closure falls back to rc.reloadTrigger() —
//     which is ASYNCHRONOUS (only enqueues onto manualReloadChan and
//     returns nil; the actual registry rebuild happens on a separate
//     goroutine, pkg/gateway/gateway_reload.go::newReloadTrigger) — on any
//     step's failure (IsReloadPending at entry, MutateConfig error, or
//     UpsertAgentFast error).
//  2. The plain ReloadFunc fallback is the SAME reloadTrigger closure, also
//     fire-and-forget.
//
// A nil return from either hook therefore means "publication was accepted, but
// may still be in flight on the reload goroutine" — NOT "the agent is already
// live in the live, in-memory config/registry that pkg/gateway/rest_agents.go
// ::listAgents reads". NotifyAgentCreated firing on the strength of that nil
// return (pre-Finding-1 behaviour) recreates the exact race this fix exists
// to close: a tab's agent_created handler (src/store/chat/slices/frames.ts)
// refetching ['agents'] and getting a response that still lacks the new agent
// because the queued reload has not actually landed yet.
//
// This test uses a custom harness whose publish hook returns nil immediately
// (the production async-enqueue shape) and whose "reload actually applied"
// step — the live, in-memory cfg.Agents.List + registry repopulation the
// real reload goroutine performs — runs LATER on a goroutine, gated by a
// channel the test controls. Without the Finding-1 fix (publishAgentActivation
// calling deps.WaitForPendingReloadFunc after the publish hook's nil return),
// the notify would fire the instant the publish hook returns — i.e. before
// the goroutine has actually applied the reload, which the test observes by
// checking al.GetConfig().Agents.List from INSIDE the notify callback
// itself: it would be empty there.
//
// With the fix, publishAgentActivation's WaitForPendingReloadFunc call blocks
// until the goroutine has actually applied the reload, so the notify sees the
// freshly-listed agent — the property the gate inside publishAndRespond
// promises.
//
// This test MUST FAIL against the pre-Finding-1 code (notify fires before
// the agent is live) and PASS after the fix; see the task report for the
// captured red-then-green proof (Finding 1's own revert + this test's exit
// status).
func TestAgentCreate_NotifyFiresOnlyAfterAsyncReloadLands(t *testing.T) {
	home := t.TempDir()
	provider := &reloadTestProvider{}
	al := buildSysagentFastUpsertTestLoop(t, home, provider, nil)

	var reloadCalls atomic.Int32
	deps := newSysagentFastUpsertDeps(al, provider, home, &reloadCalls)

	// releaseReload is the test-controlled signal that mimics the real
	// reload goroutine actually finishing its work — closing it is the
	// "registry rebuild has now landed in memory" event the production
	// gateway's waitForReload polls IsReloadPending for. We construct the
	// channel unbuffered so the goroutine suspends deterministically on
	// the send until the test signals, rather than racing into the
	// MutateConfig before the test even starts Execute().
	releaseReload := make(chan struct{})
	var reloadStarted atomic.Bool
	// guard ensures the deferred close in the goroutine doesn't fire after
	// the test's main path returns (releasing a channel more than once
	// panics). One-shot channel + atomic.Bool, mirrored from the
	// fast-path branch above.
	var reloadApplied atomic.Bool

	// Override the production-shape hooks with the async-enqueue model:
	// UpsertAgentFastFunc returns nil IMMEDIATELY (mirroring production's
	// fire-and-forget reloadTrigger), and the actual cfg.Agents.List +
	// registry update runs LATER on a goroutine the test unblocks via
	// releaseReload. WaitForPendingReloadFunc blocks until that goroutine
	// signals completion — exactly the synchronization the real
	// pkg/gateway/rest_auth.go::waitForPendingReload performs on
	// IsReloadPending.
	deps.UpsertAgentFastFunc = func(agentID string) error {
		reloadStarted.Store(true)
		// Mirrors the production closure's "enqueue and return nil" shape;
		// the real rebuild happens later on the reload goroutine below.
		go func() {
			<-releaseReload
			defer reloadApplied.Store(true)
			if err := al.MutateConfig(func(cfg *config.Config) error {
				agents, skipped, listErr := agentstore.New(home).List()
				if listErr != nil {
					return listErr
				}
				cfg.Agents.List = agents
				cfg.SkippedAgentIDs = skipped
				return nil
			}); err != nil {
				// MutateConfig failure here would otherwise be silently
				// dropped — reloadApplied.Store(true) below would still
				// fire, the test would proceed to assert the notify saw the
				// agent listable, and the failure would surface as a
				// misleading "ordering" message instead of naming the real
				// cause. t.Errorf is safe in this goroutine because the
				// outer test blocks on resultCh / select-with-timeout
				// before returning, so the goroutine finishes before the
				// test function does.
				t.Errorf("test double: MutateConfig failed: %v", err)
				return
			}
			_, _ = al.UpsertAgentFast(al.GetConfig(), agentID)
		}()
		return nil
	}
	deps.WaitForPendingReloadFunc = func() error {
		// Mirror the production waitForPendingReload contract: block until the
		// queued reload has actually landed (the goroutine above closes
		// releaseReload, then sets reloadApplied). The receive below
		// returns exactly when the reload finishes; if the channel is
		// never closed, the test's own timeout (t.Cleanup below) will
		// fire instead and fail the test with the goroutine still stuck.
		<-releaseReload
		// Wait for the goroutine to finish its MutateConfig + UpsertAgentFast
		// before returning — loadable by polling reloadApplied so we do not
		// race the registry publish against the gate's Agents.List check.
		deadline := time.Now().Add(2 * time.Second)
		for !reloadApplied.Load() {
			if time.Now().After(deadline) {
				return errors.New("test double: reload goroutine did not apply within 2s")
			}
			time.Sleep(time.Millisecond)
		}
		return nil
	}

	var (
		notifyCalls     int
		listedAtNotify  bool
		notifiedAgentID string
	)
	deps.NotifyAgentCreated = func(agentID string) {
		notifyCalls++
		notifiedAgentID = agentID
		for _, a := range al.GetConfig().Agents.List {
			if a.ID == agentID {
				listedAtNotify = true
				break
			}
		}
	}

	// Drive Execute on a goroutine so we can interleave: first assert that
	// BEFORE we release the reload, Execute is still blocked on
	// WaitForReloadFunc (i.e. notify has not fired) — this is the live
	// "instrument could have seen the failure" check for the async race.
	type execResult struct {
		result *tools.ToolResult
	}
	resultCh := make(chan execResult, 1)
	go func() {
		resultCh <- execResult{
			result: systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
				"name":        "Async Reload Agent",
				"description": "proves NotifyAgentCreated waits out the async reload",
				"soul":        "You are a test agent.",
				"model":       "test-model",
				"color":       "#22C55E",
			}),
		}
	}()

	// Wait briefly for Execute to reach WaitForPendingReloadFunc — without
	// the Finding-1 fix, the notify would have already fired by now.
	// 100ms is generous enough to cover scheduler jitter on a quiet box
	// and short enough to keep the test fast.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if reloadStarted.Load() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !reloadStarted.Load() {
		t.Fatal("UpsertAgentFastFunc was never invoked — Execute may have errored before reaching the publish step; " +
			"this is the test's precondition, not the gate under test")
	}
	if notifyCalls != 0 {
		t.Fatalf("NotifyAgentCreated fired BEFORE the async reload landed — notify must wait for WaitForPendingReloadFunc; "+
			"got %d call(s) before release. This is the #1009 async-reload race the Finding-1 fix closes.", notifyCalls)
	}

	// Release the reload; Execute should unblock, fire the notify, and return.
	close(releaseReload)

	var exec execResult
	select {
	case exec = <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return within 5s after the reload was released — " +
			"publishAgentActivation may not be calling WaitForPendingReloadFunc, or WaitForPendingReloadFunc is not signalling completion")
	}
	if exec.result.IsError {
		t.Fatalf("create_agent failed: %s", exec.result.ForLLM)
	}
	created := parseSuccess(t, exec.result.ForLLM)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create_agent response missing id: %+v", created)
	}

	if notifyCalls != 1 {
		t.Fatalf("NotifyAgentCreated must be called exactly once per landed create; got %d calls", notifyCalls)
	}
	if notifiedAgentID != id {
		t.Fatalf("NotifyAgentCreated called with agent id %q, want %q", notifiedAgentID, id)
	}
	if !listedAtNotify {
		t.Fatalf("NotifyAgentCreated fired BEFORE the agent was live-listable: " +
			"al.GetConfig().Agents.List did not yet contain the new agent's ID at notify time — " +
			"this is the #1009 async-reload race (notify firing while the reload is only queued)")
	}
	if !reloadApplied.Load() {
		t.Fatal("reload goroutine did not actually apply its MutateConfig + UpsertAgentFast before Execute returned")
	}
}
