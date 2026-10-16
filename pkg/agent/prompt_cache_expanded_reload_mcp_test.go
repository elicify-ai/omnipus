// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

// expanded_reload_mcp_test.go — CR-03 RED pack (prompt caching), REV 3.
//
// REV 3 harness corrections (parent review of REV 2, both accepted):
//
//  1. Session identity is now REAL and stable. REV 2's run helper minted a
//     FRESH scheduled session on every call while markToolsLoaded kept using
//     a constant session label — the first request could never own the
//     loaded bucket and the same-session reload premise was false. The
//     fixture now creates the actual owner and other scheduled sessions
//     BEFORE markToolsLoaded, stores their real ids on the fixture, marks
//     the load under the owner's real bucket, and the run helper uses the
//     provided session id directly (it never mints one). The owner's pre-
//     and post-reload requests are the SAME session; B runs under its own
//     real id.
//
//  2. The transport now persists across the reload. REV 2 passed
//     &mockProvider{} to every ReloadProviderAndConfig, replacing the
//     fixture's capture transport — later requests were neither recorded
//     nor answered with the canned reply. Every reload (the unrelated one,
//     the control helper, and the disconnect setup) now passes the SAME
//     fx.capture instance; the only config field that changes is
//     Agents.Defaults.MaxTokens. The agent instance is re-fetched after
//     each reload so policy/registry reads are the live ones, and the
//     capture ordinals reflect the real requests in order.
//
// REV 2 corrections (retained): the advertised-surface oracle is the REAL
// next provider request through prepareToolSurface (the restore seam sits at
// its beginning, before GetAll — a hand-derived surface would have stayed red
// after the correct fix), and the authority (per-agent allow policy + MCP
// server assignment) is seeded in CONFIG on the seeded worker entry before
// loop creation, with expandedReloadRequireAuthority gating every availability
// assertion. The only config field any unrelated reload changes is MaxTokens.
//
// Spec oracle (independent of the implementation): FR-008's session-long
// loaded-tool promise — a tool a session loaded stays advertised and
// executable for that session; time passing alone never removes it, and only
// a real revocation (Deny, MCP assignment removal, server disconnect) or the
// session ending does. Sources: the prompt-caching spec's FR-008, the
// loaded-tool lifetime rows the committed pack already covers
// (prompt_cache_tool_lifetime_test.go), and CR-03 in
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/caching-gate/code-reviewer.md.
//
// The reload is the production path (AgentLoop.ReloadProviderAndConfig — the
// same call the gateway makes on any settings save) and the MCP server is a
// REAL connected stdio stub (the same one the reconcile tests use), so the
// post-reload registration is the real reconciliation pass registering
// unchanged deferred tools with TTL zero and no retention. The loop-level
// shared session store is untouched by the reload, so the pre-created
// sessions resolve identically before and after it.
//
// Expected RED reason on the pre-fix tree: after the unrelated reload the
// loaded MCP tool is absent from the owning session's NEXT REAL REQUEST's
// tool defs, no longer executable, and the session's loaded-name membership
// is pruned as if authorization had been revoked. The revocation subtests are
// CONTROLS, not RED tests: they must pass before AND after the fix — they
// guard that the retention restoration never weakens a real revocation, and
// that session B still never inherits.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	// expandedReloadServer is the configured stdio MCP server name (the stub
	// binary advertises stub.echo and stub.noop).
	expandedReloadServer = "expanded-widgetsrv"
	// expandedReloadStubTool is the stub tool the fixture session loads.
	expandedReloadStubTool = "stub.noop"
	// expandedReloadNoopReply is the stub's documented noop reply.
	expandedReloadNoopReply = "noop"
	// expandedReloadOwnerLabel / expandedReloadOtherLabel are failure-message
	// labels only. The real session ids are created by the fixture's session
	// store and carried on the fixture — never these strings.
	// expandedReloadTurnReply is the capture provider's canned final answer.
	expandedReloadTurnReply = "expanded-reload-turn-done"
	// expandedReloadAgent is the seeded agent the fixture drives.
	expandedReloadAgent = "worker"
)

// expandedReloadCaptureProvider answers every real turn with a plain final
// answer and records the tool definitions each request carried — the
// advertised surface exactly as prepareToolSurface handed it to the wire.
// ONE instance serves the whole fixture, including every reload, so its
// request ordinals are the real turn order.
type expandedReloadCaptureProvider struct {
	mu   sync.Mutex
	defs [][]providers.ToolDefinition
}

func (p *expandedReloadCaptureProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	toolDefs []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.defs = append(p.defs, append([]providers.ToolDefinition(nil), toolDefs...))
	return &providers.LLMResponse{Content: expandedReloadTurnReply}, nil
}

func (p *expandedReloadCaptureProvider) GetDefaultModel() string { return "mock-model" }

// requestDefs returns the tool defs of the n-th real request (0-based).
func (p *expandedReloadCaptureProvider) requestDefs(n int) []providers.ToolDefinition {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 || n >= len(p.defs) {
		return nil
	}
	return p.defs[n]
}

// expandedReloadFixture is the real-server analogue of loadedLifetime: one
// loop, one agent, one deferred MCP tool loaded by exactly one REAL session,
// with the tool's authority (allow policy + server assignment) seeded in
// CONFIG and one capture transport that survives every reload.
type expandedReloadFixture struct {
	al      *AgentLoop
	capture *expandedReloadCaptureProvider
	agent   *AgentInstance // the PRE-reload instance; setup self-checks only
	tool    string
	ttl     int
	ownerID string // REAL scheduled-session id of the loading session
	otherID string // REAL scheduled-session id of the second session
}

func newExpandedReloadFixture(t *testing.T) *expandedReloadFixture {
	t.Helper()

	ttl := config.DefaultConfig().Tools.MCP.Discovery.TTL
	if ttl != 5 {
		t.Fatalf("shipped discovery TTL = %d, ADR-071 documents 5", ttl)
	}

	// The wrapped tool name is derived from the same production composer the
	// reconcile pass uses — never a hand-typed mcp_ string (the stub tool's
	// dot makes sanitization lossy, so the name carries a hash suffix).
	toolName := tools.NewMCPTool(nil, expandedReloadServer, &sdkmcp.Tool{Name: expandedReloadStubTool}).Name()

	binPath := buildStubMCPServer(t)

	cfg := newCompressedCfg(t)

	// Authority seeded in CONFIG, on the seeded worker entry, BEFORE loop
	// creation: this is the data a reload's rebuild-from-cfg legitimately
	// keeps. A runtime-only StoreToolPolicy would not survive the reload and
	// would turn this fixture into a real-revocation red.
	seeded := false
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != expandedReloadAgent {
			continue
		}
		seeded = true
		entry := &cfg.Agents.List[i]
		if entry.Tools == nil {
			entry.Tools = &config.AgentToolsCfg{}
		}
		if entry.Tools.Builtin.Policies == nil {
			entry.Tools.Builtin.Policies = map[string]config.ToolPolicy{}
		}
		entry.Tools.Builtin.Policies[toolName] = config.ToolPolicyAllow
		entry.Tools.MCP.Servers = append(entry.Tools.MCP.Servers, config.AgentMCPServerBinding{ID: expandedReloadServer})
	}
	if !seeded {
		t.Fatalf("fixture: agent %q is not in the seeded config roster", expandedReloadAgent)
	}

	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Discovery.Enabled = true
	cfg.Tools.MCP.Discovery.UseRegex = true // discovery on requires one matcher; regex keeps the fixture hermetic
	cfg.Tools.MCP.Discovery.TTL = ttl
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		expandedReloadServer: {Enabled: true, Command: binPath},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	capture := &expandedReloadCaptureProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, capture)
	t.Cleanup(al.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("fixture: ReconcileMCP (connect %s): %v", expandedReloadServer, err)
	}

	// Fixture self-checks against the LIVE pieces: real connection, real
	// registration, real config-derived authority.
	mgr := al.mcp.getManager()
	if mgr == nil {
		t.Fatal("fixture: no MCP manager after a successful reconcile")
	}
	conn, ok := mgr.GetServer(expandedReloadServer)
	if !ok {
		t.Fatalf("fixture: server %q is not live after reconcile", expandedReloadServer)
	}
	advertised := false
	for _, advertisedTool := range conn.Tools {
		if advertisedTool.Name == expandedReloadStubTool {
			advertised = true
		}
	}
	if !advertised {
		t.Fatalf("fixture: stub server does not advertise %q", expandedReloadStubTool)
	}

	agent, ok := al.GetRegistry().GetAgent(expandedReloadAgent)
	if !ok {
		t.Fatalf("fixture: agent %q is not registered", expandedReloadAgent)
	}
	if _, present := agent.Tools.GetIncludingHidden(toolName); !present {
		t.Fatalf("fixture: deferred tool %q was not registered for %s by the real reconcile", toolName, expandedReloadAgent)
	}
	expandedReloadRequireAuthority(t, agent, toolName)

	// The two REAL sessions, created before the load so the owning session
	// actually owns its bucket. The loop-level shared store is untouched by
	// ReloadProviderAndConfig, so these ids resolve identically across it.
	ownerID := expandedReloadNewSession(t, al)
	otherID := expandedReloadNewSession(t, al)

	// The load action itself: runtime promotion + the owning session's
	// loaded-name bucket, exactly what ToolSearch performs for a session that
	// loads a deferred tool. bucketFor mirrors manifestBucketKey for the real
	// turn (ProcessScheduled sets TranscriptSessionID = session id).
	agent.Tools.PromoteTools([]string{toolName}, ttl)
	al.markToolsLoaded(bucketFor(agent, ownerID), []string{toolName})

	return &expandedReloadFixture{
		al: al, capture: capture, agent: agent, tool: toolName, ttl: ttl,
		ownerID: ownerID, otherID: otherID,
	}
}

// expandedReloadNewSession creates one real scheduled session for the fixture
// agent and returns its id.
func expandedReloadNewSession(t *testing.T, al *AgentLoop) string {
	t.Helper()
	meta, err := al.GetSessionStore().NewScheduledSession(expandedReloadAgent)
	if err != nil {
		t.Fatalf("NewScheduledSession(%s): %v", expandedReloadAgent, err)
	}
	return meta.ID
}

// expandedReloadRequireAuthority is the authority sanity gate: the agent's
// live policy snapshot must still carry the config-seeded allow AND the MCP
// server assignment. Called before every availability assertion so the test
// can never confuse "production revoked it" with "the fixture never had the
// right authority".
func expandedReloadRequireAuthority(t *testing.T, agent *AgentInstance, toolName string) {
	t.Helper()
	policy := agent.LoadToolPolicy()
	if policy == nil {
		t.Fatalf("authority sanity: agent %q has no policy snapshot", agent.ID)
	}
	if got := policy.Policies[toolName]; got != config.ToolPolicyAllow {
		t.Fatalf("authority sanity: policy for %q = %q, want the config-seeded \"allow\" — the fixture's "+
			"authority was not in place, so any visibility failure below would be a real "+
			"revocation, not the retention regression this file exists to catch", toolName, got)
	}
	assigned := false
	for _, binding := range policy.MCPServers {
		if binding.ID == expandedReloadServer {
			assigned = true
		}
	}
	if !assigned {
		t.Fatalf("authority sanity: MCP server %q is not assigned to agent %q — the fixture's "+
			"assignment was not in place, so any visibility failure below would be a real "+
			"revocation, not the retention regression this file exists to catch",
			expandedReloadServer, agent.ID)
	}
}

// expandedReloadRunTurn drives ONE real scheduled turn for the fixture agent
// in the PROVIDED session (through prepareToolSurface — never a hand-derived
// surface) and requires the canned reply. It never mints a session: the
// pre- and post-reload owner requests run under the SAME session id.
func expandedReloadRunTurn(t *testing.T, fx *expandedReloadFixture, sessionID string) {
	t.Helper()
	if sessionID == "" {
		t.Fatal("run helper: empty session id — the fixture must pass the real session it created")
	}
	reply, turnErr := fx.al.ProcessScheduled(
		context.Background(), expandedReloadAgent, sessionID, "expanded: next turn", "scheduled", sessionID)
	if turnErr != nil {
		t.Fatalf("real turn for session %q failed: %v", sessionID, turnErr)
	}
	if reply != expandedReloadTurnReply {
		t.Fatalf("turn reply = %q, want %q — the turn did not run on the fixture's capture transport",
			reply, expandedReloadTurnReply)
	}
}

// expandedReloadRequestDefNames is the advertised surface of the n-th real
// request, failing loudly when that request never happened.
func expandedReloadRequestDefNames(t *testing.T, capture *expandedReloadCaptureProvider, n int) map[string]bool {
	t.Helper()
	defs := capture.requestDefs(n)
	if defs == nil {
		t.Fatalf("real request #%d never happened; the fixture cannot read its advertised defs", n+1)
	}
	names := make(map[string]bool, len(defs))
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	return names
}

// TestExpandedReload_LoadedMCPToolSurvivesUnrelatedConfigReload is the CR-03
// RED test. The owning session loads the deferred MCP tool; an UNRELATED
// global config change (MaxTokens only) triggers the real
// ReloadProviderAndConfig on the SAME transport; the SAME session's next real
// request must still advertise the tool, the tool must still execute, the
// loaded-name membership must survive the prune, and the second session must
// still neither see nor execute it.
func TestExpandedReload_LoadedMCPToolSurvivesUnrelatedConfigReload(t *testing.T) {
	fx := newExpandedReloadFixture(t)

	// Pre-reload authority and visibility, through a REAL request on the
	// owning session: proves the config-seeded authority genuinely advertises
	// the loaded tool before anything is reloaded.
	expandedReloadRequireAuthority(t, fx.agent, fx.tool)
	expandedReloadRunTurn(t, fx, fx.ownerID)
	before := expandedReloadRequestDefNames(t, fx.capture, 0)
	if !before[fx.tool] {
		t.Fatalf("fixture: the owning session's first real request does not advertise the loaded tool %q (defs %v) — "+
			"the config-seeded authority or the loaded bucket never reached the wire, so the fixture is broken",
			fx.tool, before)
	}

	// The unrelated reload: real production path, SAME capture transport,
	// MaxTokens is the ONLY changed field; MCP server config, assignment and
	// permission are byte-identical (they are the same cfg entries).
	newCfg, err := fx.al.cfg.Clone()
	if err != nil {
		t.Fatalf("clone config for the unrelated reload: %v", err)
	}
	newCfg.Agents.Defaults.MaxTokens = 2048
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := fx.al.ReloadProviderAndConfig(ctx, fx.capture, newCfg); err != nil {
		t.Fatalf("unrelated ReloadProviderAndConfig: %v", err)
	}

	// The reload replaced every agent instance; re-fetch the live one and
	// verify the authority survived BEFORE asserting any availability.
	agent2, ok := fx.al.GetRegistry().GetAgent(expandedReloadAgent)
	if !ok {
		t.Fatalf("agent %q missing from the reloaded registry", expandedReloadAgent)
	}
	expandedReloadRequireAuthority(t, agent2, fx.tool)

	// The SAME session's NEXT REAL REQUEST — through prepareToolSurface,
	// where the fix restores loaded-tool retention before GetAll — must still
	// advertise it.
	expandedReloadRunTurn(t, fx, fx.ownerID)
	after := expandedReloadRequestDefNames(t, fx.capture, 1)
	if !after[fx.tool] {
		t.Fatalf("after an unrelated config reload (only MaxTokens changed) the owning session's next real "+
			"request no longer advertises the loaded MCP tool %q (defs %v); a reload that changes neither "+
			"the server, nor the assignment, nor the permission must not revoke a loaded tool", fx.tool, after)
	}

	// Execution keeps working in the owning session.
	res := agent2.Tools.Execute(context.Background(), fx.tool, map[string]any{})
	if res == nil || res.IsError || !strings.Contains(res.ForLLM, expandedReloadNoopReply) {
		detail := ""
		if res != nil {
			detail = res.ForLLM
		}
		t.Errorf("loaded MCP tool %q is not executable after the unrelated reload (%q); a loaded tool must still run",
			fx.tool, detail)
	}

	// The prune must not have eaten the session's loaded-name membership.
	if !fx.al.sessionLoadedTools(bucketFor(agent2, fx.ownerID))[fx.tool] {
		t.Errorf("the owning session lost its loaded-name membership for %q across the unrelated reload; "+
			"membership may only be pruned by a real revocation", fx.tool)
	}

	// Session isolation is untouched: the second session (its own real id)
	// never inherits the loaded tool — asserted on its own real request.
	expandedReloadRunTurn(t, fx, fx.otherID)
	other := expandedReloadRequestDefNames(t, fx.capture, 2)
	if other[fx.tool] {
		t.Errorf("second session's real request advertises %q; a session that did not load it must not see it", fx.tool)
	}
	_, policyMap := tools.FilterToolsByPolicy(agent2.Tools.GetAll(), agent2.AgentType, agent2.LoadToolPolicy())
	otherTurn := fakeTurnState(agent2, fx.otherID)
	if policy := fx.al.resolveToolPolicyAtExec(otherTurn, fx.tool, policyMap); policy != "deny" {
		t.Errorf("second session can run %q (policy %q); a session that did not load it must be denied", fx.tool, policy)
	}
}

// TestExpandedReload_RealRevocationStillRevokesAfterReload is the control
// half: each REAL revocation still removes the tool when it is applied after
// an unrelated reload, and each is observed on a REAL request's advertised
// surface. These subtests must pass before the fix (where the reload itself
// already dropped the tool) and — the point — stay passing after the fix
// restores retention, proving the restoration checks current registration,
// permission and assignment instead of blanket-re-adding.
func TestExpandedReload_RealRevocationStillRevokesAfterReload(t *testing.T) {
	// reloadUnrelated re-runs the production reload — SAME capture transport,
	// only MaxTokens changed — and returns the fresh agent instance.
	reloadUnrelated := func(t *testing.T, fx *expandedReloadFixture) *AgentInstance {
		t.Helper()
		newCfg, err := fx.al.cfg.Clone()
		if err != nil {
			t.Fatalf("clone config: %v", err)
		}
		newCfg.Agents.Defaults.MaxTokens = 2048
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := fx.al.ReloadProviderAndConfig(ctx, fx.capture, newCfg); err != nil {
			t.Fatalf("unrelated ReloadProviderAndConfig: %v", err)
		}
		agent, ok := fx.al.GetRegistry().GetAgent(expandedReloadAgent)
		if !ok {
			t.Fatalf("agent %q missing from the reloaded registry", expandedReloadAgent)
		}
		expandedReloadRequireAuthority(t, agent, fx.tool)
		return agent
	}

	t.Run("deny", func(t *testing.T) {
		fx := newExpandedReloadFixture(t)
		agent := reloadUnrelated(t, fx)
		agent.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies:   map[string]config.ToolPolicy{fx.tool: config.ToolPolicyDeny},
			MCPServers: []config.AgentMCPServerBinding{{ID: expandedReloadServer}},
		})
		expandedReloadRunTurn(t, fx, fx.ownerID)
		defs := expandedReloadRequestDefNames(t, fx.capture, 0)
		if defs[fx.tool] {
			t.Errorf("Deny left %q in the owning session's real advertised surface after a reload", fx.tool)
		}
		_, policyMap := tools.FilterToolsByPolicy(agent.Tools.GetAll(), agent.AgentType, agent.LoadToolPolicy())
		turn := fakeTurnState(agent, fx.ownerID)
		if got := fx.al.resolveToolPolicyAtExec(turn, fx.tool, policyMap); got != "deny" {
			t.Errorf("Deny did not stop %q after a reload (policy %q)", fx.tool, got)
		}
	})

	t.Run("assignment", func(t *testing.T) {
		fx := newExpandedReloadFixture(t)
		agent := reloadUnrelated(t, fx)
		agent.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{fx.tool: config.ToolPolicyAllow},
			// No MCPServers: the MCP assignment is removed.
		})
		expandedReloadRunTurn(t, fx, fx.ownerID)
		defs := expandedReloadRequestDefNames(t, fx.capture, 0)
		if defs[fx.tool] {
			t.Errorf("removing the MCP assignment left %q in the owning session's real advertised surface after a reload", fx.tool)
		}
	})

	t.Run("disconnect", func(t *testing.T) {
		fx := newExpandedReloadFixture(t)
		reloadUnrelated(t, fx)
		// Real disconnect: the server leaves the desired config, so the next
		// reconcile's removal pass unregisters its tools — the same path a
		// deleted or disabled server takes in production. Same capture
		// transport again.
		cfg2, err := fx.al.cfg.Clone()
		if err != nil {
			t.Fatalf("clone config: %v", err)
		}
		cfg2.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := fx.al.ReloadProviderAndConfig(ctx, fx.capture, cfg2); err != nil {
			t.Fatalf("disconnect reload: %v", err)
		}
		agent2, ok := fx.al.GetRegistry().GetAgent(expandedReloadAgent)
		if !ok {
			t.Fatalf("agent %q missing from the reloaded registry", expandedReloadAgent)
		}
		expandedReloadRunTurn(t, fx, fx.ownerID)
		defs := expandedReloadRequestDefNames(t, fx.capture, 0)
		if defs[fx.tool] {
			t.Errorf("server disconnect left %q in the owning session's real advertised surface after a reload", fx.tool)
		}
		res := agent2.Tools.Execute(context.Background(), fx.tool, map[string]any{})
		if res == nil || !res.IsError {
			t.Errorf("disconnected MCP tool still ran after a reload")
		}
	})
}
