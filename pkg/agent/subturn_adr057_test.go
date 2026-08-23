// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// subturn_adr057_test.go — tests for ADR-057 U7 (Wave F): the delegation
// spawn itself (pkg/agent/subturn.go). Per the session-unification spec's
// binding Rule 5, every unit's new tests go in a NEW file named
// `<subject>_adr057_test.go`; this is U7's. Unexported package-level
// helpers are prefixed `u7` per binding Rule 6.
//
// Binding rule 1 (real state, never a spy/mock that records its argument):
// every test below drives the REAL spawnSubTurn against a REAL
// *session.UnifiedStore rooted at an os.MkdirTemp-backed AgentLoop (via
// u7NewTestAgentLoopWithProvider's real NewAgentLoop boot path) and a REAL,
// executing turn — never a derived id, never an assertion on "the function
// was called". Where a live child turnState's own field must be observed
// (obligation 2 / FR-011), u7RoutingCapture reads it via
// turnStateFromContext from inside a REAL provider.Chat call on the REAL
// execution path, rather than a spy standing in for spawnSubTurn itself.
//
// Corollary "distinct ids everywhere" (spec, "the governing constraint"
// section): every fixture below constructs parent and child ids as
// distinct, store-minted values and asserts on WHICH one was used —
// u7RequireDistinctIDs enforces this at the fixture layer, mirroring
// pkg/security/approvalgrants_adr057_test.go's requireDistinct.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// u7NewTestAgentLoopWithProvider mirrors loop_test.go's newTestAgentLoop
// (same real NewAgentLoop boot path via mustNewAgentLoop —
// test_helpers_test.go), substituting a caller-supplied providers.LLMProvider
// so obligation 2's tests can observe a live turnState's own fields from
// inside a real Chat call. Declared here (rather than editing the shared
// loop_test.go helper, which this unit does not own) to keep this file
// self-contained per ownership Rule 5/6.
//
// FOUND WHILE VERIFYING (real-store test-harness gap, not a subturn.go
// defect): newTestAgentLoop sets cfg.Agents.Defaults.Home = tmpDir directly.
// AgentLoop's shared-session-store init computes
// homePath = filepath.Dir(cfg.AgentHomeBasePath()) (loop.go, "Initialize
// shared session store at $OMNIPUS_HOME/sessions/"), i.e. it walks UP one
// level from Home expecting Home to be an AGENT's own subdirectory (e.g.
// <OMNIPUS_HOME>/agents/mia) — correct in production, but when Home IS the
// tmpDir itself, filepath.Dir(tmpDir) resolves to tmpDir's PARENT (the OS
// temp root, e.g. /tmp), so every test using that exact pattern shares ONE
// real, never-cleaned "<temp-root>/sessions" directory on disk — verified:
// `ls /tmp/sessions` on this box shows session directories dating back
// weeks across unrelated prior test runs. This was invisible before this
// unit because nothing previously exercised real session creation from
// spawnSubTurn; FR-096's collision refusal (CreateSessionWithID) now
// surfaces it immediately (deterministic ids like "subturn-1" collide
// across separate test processes/runs sharing that one real directory).
// Nesting Home one level deeper here (tmpDir/agents/main) makes
// filepath.Dir(...) resolve back to tmpDir itself, giving each test run a
// genuinely isolated, cleaned-up store — the fix is contained to this
// test's OWN fixture, not a change to the shared helper or to
// pkg/session/pkg/agent production code.
func u7NewTestAgentLoopWithProvider(t *testing.T, provider providers.LLMProvider) (al *AgentLoop, cleanup func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "agent-u7-test-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	agentHome := filepath.Join(tmpDir, "agents", "main")
	if err := os.MkdirAll(agentHome, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q): %v", agentHome, err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              agentHome,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: agentHome}},
		},
	}
	msgBus := bus.NewMessageBus()
	al = mustNewAgentLoop(t, cfg, msgBus, provider)
	return al, func() { os.RemoveAll(tmpDir) }
}

// u7RequireDistinctIDs fails the test if two session ids coincide — the
// spec's "distinct ids everywhere" corollary: a fixture whose parent and
// child ids happen to be equal cannot discriminate real inheritance from an
// accidental self-reference.
func u7RequireDistinctIDs(t *testing.T, a, b string) {
	t.Helper()
	if a == "" || b == "" {
		t.Fatalf("fixture defect: session ids must be non-empty, got a=%q b=%q", a, b)
	}
	if a == b {
		t.Fatalf("fixture defect: session ids must be distinct, both were %q", a)
	}
}

// u7NewRealRootTurnState mints a REAL root session in store (via
// NewSession — no parent, matching a genuine chat/task root) and returns a
// turnState built with the real constructor (newTurnState), so
// transcriptSessionID/routingSessionID/session binding all match a real
// root turn's actual construction path — never a bare struct literal
// standing in for one.
func u7NewRealRootTurnState(t *testing.T, al *AgentLoop, store *session.UnifiedStore) (*turnState, string) {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "u7-test-channel", "mia")
	if err != nil {
		t.Fatalf("store.NewSession: %v", err)
	}
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent registered")
	}
	opts := processOptions{
		SessionKey:          meta.ID,
		Channel:             "u7-test-channel",
		ChatID:              "u7-chat-" + meta.ID,
		TranscriptSessionID: meta.ID,
		TranscriptStore:     store,
	}
	scope := al.newTurnEventScope(agent.ID, meta.ID)
	ts := newTurnState(agent, opts, scope)
	ts.pendingResults = make(chan *tools.ToolResult, 4)
	return ts, meta.ID
}

// TestU7_ParentChildEdge_ChildCountBecomesNonZero is the primary, positive
// proof of obligation 1 (SetMeta(childID, MetaPatch{ParentSessionID:
// &parentID}) immediately after CreateSessionWithID): the spec's own words
// are that skipping this call "renders the entire nested-session hierarchy
// empty with a green build" — ChildCount is the exact, real, store-backed
// artefact that call wires (FR-097's in-memory parent index,
// u5WriteIdentityLocked).
//
// Positive lower bound (binding rule 4): asserts ChildCount(parentID) == 0
// BEFORE the spawn (proving the counter is live and not vacuously
// non-zero), then == 1 after — never a bare "non-zero" without the
// before/after transition, which is exactly the class of test that stays
// green when the counter itself is broken.
//
// RED evidence (obligation 1, separate from obligation 2's): with
// subturn.go's `sharedStore.SetMeta(childID, session.MetaPatch{ParentSessionID:
// &childParentSessionID})` call commented out, this test fails with
// "ChildCount(...) after spawn = 0, want 1". Restoring the call makes it
// pass. See the U7 completion report for the actual before/after run.
func TestU7_ParentChildEdge_ChildCountBecomesNonZero(t *testing.T) {
	al, cleanup := u7NewTestAgentLoopWithProvider(t, &mockProvider{})
	defer cleanup()
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("test harness did not wire a shared session store")
	}

	parentTS, parentID := u7NewRealRootTurnState(t, al, store)

	if got := store.ChildCount(parentID); got != 0 {
		t.Fatalf("precondition violated: ChildCount(%q) = %d before any child exists, want 0", parentID, got)
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "u7-spawn-call-1")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:        "mock-model",
		SystemPrompt: "do the thing",
	})
	if err != nil {
		t.Fatalf("spawnSubTurn: unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("spawnSubTurn: expected non-nil result")
	}

	if len(parentTS.childTurnIDs) != 1 {
		t.Fatalf("expected exactly 1 registered child, got %d: %v", len(parentTS.childTurnIDs), parentTS.childTurnIDs)
	}
	childID := parentTS.childTurnIDs[0]
	u7RequireDistinctIDs(t, parentID, childID)

	// THE assertion this test exists for.
	if got := store.ChildCount(parentID); got != 1 {
		t.Fatalf("ChildCount(%q) after spawn = %d, want 1 — obligation 1's SetMeta(ParentSessionID) call is missing or broken", parentID, got)
	}

	// Corroborating, independently-readable evidence: the child's own
	// meta.json actually carries the edge, read back from real disk state
	// via GetMeta (not the in-memory parent index alone).
	childMeta, err := store.GetMeta(childID)
	if err != nil {
		t.Fatalf("store.GetMeta(%q): %v", childID, err)
	}
	if childMeta.ParentSessionID != parentID {
		t.Errorf("child meta.ParentSessionID = %q, want %q", childMeta.ParentSessionID, parentID)
	}
	if childMeta.Type != session.SessionTypeDelegate {
		t.Errorf("child meta.Type = %q, want %q (FR-008 subordinate value)", childMeta.Type, session.SessionTypeDelegate)
	}

	// FR-006: the child's Owner is copied from the parent verbatim. The
	// fixture parent has no Owner set (zero value ""), so this asserts the
	// documented "absent owner is not silently swallowed, child still
	// created" behaviour rather than a positive owner copy — see
	// TestU7_ParentChildEdge_OwnerCopiedVerbatim below for the non-empty case.
	if childMeta.Owner != "" {
		t.Errorf("child meta.Owner = %q, want empty (fixture parent has no owner)", childMeta.Owner)
	}
}

// TestU7_ParentChildEdge_OwnerCopiedVerbatim is FR-006's positive case: a
// parent with a real, non-empty Owner produces a child whose Owner is the
// SAME value, read back from real store state.
func TestU7_ParentChildEdge_OwnerCopiedVerbatim(t *testing.T) {
	al, cleanup := u7NewTestAgentLoopWithProvider(t, &mockProvider{})
	defer cleanup()
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("test harness did not wire a shared session store")
	}

	parentTS, parentID := u7NewRealRootTurnState(t, al, store)
	const owner = "u7-real-owner@example.com"
	if err := store.SetMeta(parentID, session.MetaPatch{Owner: u7StrPtr(owner)}); err != nil {
		t.Fatalf("SetMeta(parent owner): %v", err)
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "u7-spawn-call-owner")
	if _, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:        "mock-model",
		SystemPrompt: "do the thing",
	}); err != nil {
		t.Fatalf("spawnSubTurn: unexpected error: %v", err)
	}
	if len(parentTS.childTurnIDs) != 1 {
		t.Fatalf("expected exactly 1 registered child, got %d", len(parentTS.childTurnIDs))
	}
	childID := parentTS.childTurnIDs[0]

	childMeta, err := store.GetMeta(childID)
	if err != nil {
		t.Fatalf("store.GetMeta(%q): %v", childID, err)
	}
	if childMeta.Owner != owner {
		t.Errorf("child meta.Owner = %q, want %q (FR-006)", childMeta.Owner, owner)
	}
}

// u7StrPtr returns a pointer to a copy of s — MetaPatch fields are pointers
// so a nil/non-nil distinction can be made. Prefixed per ownership Rule 6
// (a bare strPtr already exists in plan_engine_g3_fixwave_test.go).
func u7StrPtr(s string) *string { return &s }

// u7RoutingCapture is a providers.LLMProvider that captures the LIVE
// turnState's routingSessionID (via turnStateFromContext) the instant its
// Chat method is invoked — the only way to observe a child turnState's own
// field from outside spawnSubTurn for a synchronous delegation, since the
// child's activeTurnStates entry is cleared (clearActiveTurnStateEntry) by
// the time spawnSubTurn returns to its caller. This reads REAL state off
// the REAL execution path (runTurn -> the provider Chat call) rather than
// recording an argument standing in for the state under test (binding
// rule 1) — the turnState it reads is the one actually registered in
// al.activeTurnStates and actually running the turn.
type u7RoutingCapture struct {
	mu       sync.Mutex
	captured []capturedTurnIdentity
}

type capturedTurnIdentity struct {
	routingSessionID    session.RoutingSessionID
	transcriptSessionID string
}

func (p *u7RoutingCapture) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	ts := turnStateFromContext(ctx)
	if ts != nil {
		p.mu.Lock()
		p.captured = append(p.captured, capturedTurnIdentity{
			routingSessionID:    ts.routingSessionID,
			transcriptSessionID: ts.transcriptSessionID,
		})
		p.mu.Unlock()
	}
	return &providers.LLMResponse{Content: "mock response", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *u7RoutingCapture) GetDefaultModel() string { return "mock-model" }

func (p *u7RoutingCapture) snapshot() []capturedTurnIdentity {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]capturedTurnIdentity, len(p.captured))
	copy(out, p.captured)
	return out
}

// TestU7_RoutingSessionID_ChildInheritsParentVerbatim is the primary,
// positive proof of obligation 2 (childTS.routingSessionID =
// parentTS.routingSessionID immediately after newTurnState): reads the
// LIVE child turnState's own routingSessionID field via a real provider
// Chat call on the real execution path and asserts it equals the root
// parent's session id — NOT the child's own (distinct) transcriptSessionID.
//
// RED evidence (obligation 2, separate from obligation 1's): with
// subturn.go's `childTS.routingSessionID = parentTS.routingSessionID` line
// commented out, this test fails because the captured routingSessionID
// equals the CHILD's own session id (newTurnState's own FR-011 default,
// correct only for a root) instead of the parent's. Restoring the line
// makes it pass. See the U7 completion report for the actual before/after
// run.
func TestU7_RoutingSessionID_ChildInheritsParentVerbatim(t *testing.T) {
	provider := &u7RoutingCapture{}
	al, cleanup := u7NewTestAgentLoopWithProvider(t, provider)
	defer cleanup()
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("test harness did not wire a shared session store")
	}

	parentTS, parentID := u7NewRealRootTurnState(t, al, store)
	// Root turn sanity check: routingSessionID defaults to the turn's own
	// session id (FR-011's root case) — asserted here as the fixture's own
	// precondition, not the thing under test.
	if string(parentTS.routingSessionID) != parentID {
		t.Fatalf("fixture defect: root parentTS.routingSessionID = %q, want %q", parentTS.routingSessionID, parentID)
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "u7-spawn-call-routing")
	if _, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:        "mock-model",
		SystemPrompt: "do the thing",
	}); err != nil {
		t.Fatalf("spawnSubTurn: unexpected error: %v", err)
	}
	if len(parentTS.childTurnIDs) != 1 {
		t.Fatalf("expected exactly 1 registered child, got %d", len(parentTS.childTurnIDs))
	}
	childID := parentTS.childTurnIDs[0]
	u7RequireDistinctIDs(t, parentID, childID)

	captured := provider.snapshot()
	if len(captured) == 0 {
		t.Fatal("provider.Chat was never invoked — the child turn never reached the LLM call, nothing was captured")
	}
	last := captured[len(captured)-1]

	// FR-007 sanity: the child's OWN transcriptSessionID is its OWN id, not
	// the parent's — proves the two fields (routing vs transcript) have
	// genuinely diverged in this test, which is the whole point of testing
	// routingSessionID separately from transcriptSessionID.
	if last.transcriptSessionID != childID {
		t.Fatalf("child transcriptSessionID = %q, want %q (FR-007)", last.transcriptSessionID, childID)
	}

	// THE assertion this test exists for.
	if string(last.routingSessionID) != parentID {
		t.Errorf("child routingSessionID = %q, want %q (parent's, inherited verbatim per FR-011) — "+
			"obligation 2's childTS.routingSessionID = parentTS.routingSessionID overwrite is missing or broken",
			last.routingSessionID, parentID)
	}
}
