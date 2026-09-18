// loop_wire.go: Attach tools and deps onto agents

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/captureext"
)

// wireExecToolDeps replaces each agent's bash tool with one constructed via
// NewExecToolWithDeps, injecting the policy auditor (SEC-05), the ADR-035
// god-mode/egress-proxy hardening deps, and the deny-pattern configuration
// (ADR-036 — this is now the ONE registration path for `bash`, folding in what
// used to be the separate workspace_shell/workspace_shell_bg wiring in
// WireTier13Deps). This runs after NewAgentInstance has created the default
// bash tool so that all other tool setup (allow paths) is preserved — we only
// add the security deps on top.
//
// No-op when the agent has bash disabled or when the registry lookup fails.
func (al *AgentLoop) wireExecToolDeps() {
	al.wireExecToolDepsOn(al.registry)
}

// wireExecToolDepsOn is the registry-parameterized form of wireExecToolDeps,
// used by hot-reload to wire the new registry before the atomic swap.
func (al *AgentLoop) wireExecToolDepsOn(registry *AgentRegistry) {
	if registry == nil {
		return
	}
	// Read al.cfg under al.mu.RLock (GetConfig), NOT bare. This helper runs
	// inside UpsertAgentFast's and ReloadProviderAndConfig's wiring pass with
	// NO al.mu held, so a bare `al.cfg` read races every pointer-swap publisher
	// (SwapConfig, ReloadProviderAndConfig, and MutateConfig's copy-then-swap)
	// writing the al.cfg slot under al.mu.Lock. The locked read establishes the
	// happens-before edge the bare read lacked.
	cfg := al.GetConfig()
	if cfg == nil {
		return
	}
	allowReadPaths := buildAllowReadPatterns(cfg)

	// O14 god-mode: the single source of truth for the sandbox escape hatch
	// (ADR-035). When active: full host fs + syscalls, network egress open,
	// shell guard / deny-patterns off, regardless of per-agent shell policy.
	godMode := GodModeActive(cfg)

	globalShellDenyPatterns := cfg.Sandbox.ShellDenyPatterns
	if godMode {
		globalShellDenyPatterns = nil
	}

	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok || agent == nil || agent.Tools == nil {
			continue
		}

		var agentShellPolicy *config.AgentShellPolicy
		for i := range cfg.Agents.List {
			entry := &cfg.Agents.List[i]
			if entry.ID == agentID {
				agentShellPolicy = entry.ShellPolicy
				break
			}
		}
		if godMode {
			agentShellPolicy = nil // drop per-agent deny patterns under god mode
		}

		deps := tools.ExecToolDeps{
			GodMode:                 godMode,
			AuditFailClosed:         resolveBoolWithDefault(cfg.Sandbox.PathGuardAuditFailClosed, cfg.Sandbox.AuditLog),
			GlobalShellDenyPatterns: globalShellDenyPatterns,
			AgentShellPolicy:        agentShellPolicy,
		}
		// Plumb the kernel-sandbox egress proxy into the bash tool so the
		// hardened path (non-god-mode) injects HTTP_PROXY pointing at the
		// allow-listed proxy. Nil-guarded so bash gracefully degrades to
		// no-proxy when the boot-time NewEgressProxy call failed.
		if al.sandboxEgressProxy != nil {
			deps.Proxy = al.sandboxEgressProxy
		}
		// Nil-guarded to avoid the typed-nil-in-interface trap: storing a nil
		// *policy.PolicyAuditor in an interface field would create a non-nil
		// interface holding a nil pointer, defeating downstream `!= nil` checks.
		if al.policyAuditor != nil {
			deps.PolicyAuditor = al.policyAuditor
		}

		restrict := cfg.Agents.Defaults.RestrictToWorkspace
		execTool, err := tools.NewExecToolWithDeps(agent.Home, restrict, cfg, deps, allowReadPaths)
		if err != nil {
			// Fail closed: if security wiring fails, remove the bash tool from the
			// registry entirely. The agent will lose bash capability but
			// cannot run commands without the security layer.
			logger.ErrorCF("agent", "Failed to wire bash tool deps; removing bash tool (fail closed)",
				map[string]any{"agent_id": agentID, "error": err.Error()})
			agent.Tools.Unregister("bash")
			continue
		}
		agent.Tools.RegisterReplacing(execTool)
	}

	// ADR-090: the environment_setup tool rides the same registry pass —
	// god mode, egress proxy and the production storage adapter land with
	// each exec-deps refresh, and hot-reload re-applies them identically.
	al.wireEnvironmentSetupDepsOn(registry)
}

// WireTier13Deps registers the web_serve, workspace.shell, and
// workspace.shell_bg tools into every non-system agent's tool registry using
// the shared infrastructure instances created once at gateway boot. Called
// from gateway.go after NewAgentLoop and after the Tier13Deps registries
// (DevServerRegistry, ServedSubdirs, EgressProxy) are constructed. The
// "Tier13" name is historical — Tier 1 (static serve) and Tier 3 (dev-server
// proxy) used to live in two separate tools (serve_workspace and
// run_in_workspace); both are now subsumed by web_serve, but the deps struct
// keeps the legacy name for cross-package callers.
//
// Mirrors the wireExecToolDeps pattern: post-creation injection so the heavy
// singleton objects (EgressProxy, DevServerRegistry) are not re-created per
// agent. Nil fields in deps skip the corresponding tool registration
// (graceful degradation when preview is disabled or Tier 3 unsupported).
func (al *AgentLoop) WireTier13Deps(deps Tier13Deps) {
	// Stash a copy so hot-reload can re-apply the wiring on the rebuilt registry.
	depsCopy := deps
	al.tier13Deps = &depsCopy

	// Stash the sandbox egress proxy for the exec tool. wireExecToolDeps
	// (which originally ran during NewAgentLoop, before the gateway had
	// constructed the proxy) is re-run below so each agent's exec tool now
	// picks up the proxy address on the sandbox-on path.
	if deps.EgressProxy != nil {
		al.sandboxEgressProxy = deps.EgressProxy
	}

	al.wireTier13DepsLocked(al.registry, deps)

	// Re-wire exec deps now that we have the egress proxy. Without this,
	// the exec tool's hardened path runs without HTTP_PROXY env vars.
	al.wireExecToolDeps()
}

// wireTier13DepsLocked is the actual wiring logic, factored out so hot-reload
// can re-apply it against a freshly-built registry without re-stashing.
func (al *AgentLoop) wireTier13DepsLocked(registry *AgentRegistry, deps Tier13Deps) {
	if registry == nil {
		return
	}
	// Read al.cfg under al.mu.RLock (GetConfig), NOT bare — see the matching
	// comment in wireExecToolDepsOn: this helper likewise runs in the unlocked
	// wiring pass of UpsertAgentFast/ReloadProviderAndConfig, and a bare al.cfg
	// read races every pointer-swap publisher of al.cfg.
	cfg := al.GetConfig()
	if cfg == nil {
		return
	}

	minDurSec := cfg.Tools.ServeWorkspace.MinDurationSeconds
	maxDurSec := cfg.Tools.ServeWorkspace.MaxDurationSeconds

	for _, agentID := range registry.ListAgentIDs() {
		ag, ok := registry.GetAgent(agentID)
		if !ok || ag == nil || ag.Tools == nil {
			continue
		}

		// web_serve — unified Tier 1 (static) + Tier 3 (dev server) tool.
		// Registered whenever ServedSubdirs is available; dev mode is gated to
		// Linux at runtime inside the tool itself (Tier3UnsupportedMessage).
		if deps.ServedSubdirs != nil {
			portRange := cfg.Sandbox.DevServerPortRange
			webServeCfg := tools.WebServeDevConfig{
				Tier3Commands:   cfg.Sandbox.Tier3Commands,
				PortRange:       [2]int32{portRange[0], portRange[1]},
				MaxConcurrent:   cfg.Sandbox.MaxConcurrentDevServers,
				EgressAllowList: cfg.Sandbox.EgressAllowList,
				AuditFailClosed: resolveBoolWithDefault(cfg.Sandbox.PathGuardAuditFailClosed, cfg.Sandbox.AuditLog),
			}
			// preview-on-main-listener v5 (FR-005/FR-006): web_serve no longer
			// takes a constructor-frozen preview base URL. It gets al.GetConfig
			// (thread-safe, RLock-protected) so every serve_web call builds its
			// URL from the LIVE canonical gateway origin and reads
			// gateway.preview_enabled live — no restart, no re-wiring on hot
			// reload required for the toggle to take effect.
			webServeTool := tools.NewWebServeTool(
				ag.Home,
				ag.ID,
				al.GetConfig,
				deps.ServedSubdirs,
				deps.DevServerRegistry, // nil on non-Linux; tool guards internally
				webServeCfg,
				deps.EgressProxy,
				al.auditLogger,
				minDurSec,
				maxDurSec,
			)
			ag.Tools.RegisterReplacing(webServeTool)
		}

		// bash (ADR-036): the unified shell tool used to be wired here as
		// three separate tools (exec via wireExecToolDeps, workspace.shell /
		// workspace.shell_bg gated behind experimental.workspace_shell_enabled
		// right here). All three are now ONE tool, registered universally via
		// wireExecToolDeps alone (called again below, after this function
		// returns, and by WireTier13Deps once the egress proxy is available)
		// — governed exclusively by ToolPolicyCfg, no experimental flag.
	}

	logger.InfoCF("agent", "Tier 1/3 tools wired into agent registry", map[string]any{
		// preview-on-main-listener v5: no more boot-frozen preview_base_url —
		// web_serve now derives its URL live from al.GetConfig on every call.
		"served_subdirs_ready":      deps.ServedSubdirs != nil,
		"dev_server_registry_ready": deps.DevServerRegistry != nil,
		"egress_proxy_ready":        deps.EgressProxy != nil,
	})
}

// wireMemoryAuditLoggerOn wires auditLogger into every agent's tool registry
// in registry (SEC-15), including the direct RememberTool/RetrospectiveTool
// cast so memory tools can emit their own structured per-entry audit events
// (content_sha256 etc.). Factored out of NewAgentLoop's boot-time wiring so
// ReloadProviderAndConfig can re-apply the identical wiring against a
// freshly-built registry on hot reload — without this, NewAgentRegistry's
// brand-new RememberTool/RetrospectiveTool instances would silently lose
// audit logging the first time config reloads (e.g. onboarding completion,
// any agent PUT, token rotation — every TriggerReload call site).
func (al *AgentLoop) wireMemoryAuditLoggerOn(registry *AgentRegistry, auditLogger *audit.Logger) {
	if registry == nil || auditLogger == nil {
		return
	}
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			agent.Tools.SetAuditLogger(auditLogger)
			// Memory tools carry their own audit-logger reference for
			// structured per-entry events (content_sha256 etc.).
			// SetAuditLogger on the registry propagates via auditLoggerAware,
			// but the explicit cast below documents the dependency clearly.
			if t, ok := agent.Tools.Get("remember"); ok {
				if rt, ok := t.(*tools.RememberTool); ok {
					rt.SetAuditLogger(auditLogger)
				} else {
					// SF4: wrong type registered for "remember" — surface the
					// registration-order bug so it doesn't silently skip audit wiring.
					logger.WarnCF("agent",
						"'remember' tool is not a *tools.RememberTool; audit wiring skipped",
						map[string]any{
							"agent_id":  agentID,
							"tool_type": fmt.Sprintf("%T", t),
						})
				}
			}
			if t, ok := agent.Tools.Get("run_retrospective"); ok {
				if rt, ok := t.(*tools.RetrospectiveTool); ok {
					rt.SetAuditLogger(auditLogger)
				} else {
					logger.WarnCF("agent",
						"'run_retrospective' tool is not a *tools.RetrospectiveTool; audit wiring skipped",
						map[string]any{
							"agent_id":  agentID,
							"tool_type": fmt.Sprintf("%T", t),
						})
				}
			}
		}
	}
}

// wireMemoryRateLimiterOn propagates the shared MemoryRateLimiter (v0.2 #155
// item 6) onto every agent's tool registry in registry. Factored out of
// NewAgentLoop's boot-time wiring so ReloadProviderAndConfig can re-apply the
// SAME limiter instance against a freshly-built registry on hot reload —
// see the al.memoryRateLimiter field comment for why the limiter itself must
// never be reconstructed here (that would reset every agent's sliding-window
// rate-limit buckets on any unrelated config change).
func (al *AgentLoop) wireMemoryRateLimiterOn(registry *AgentRegistry, limiter *tools.MemoryRateLimiter) {
	if registry == nil || limiter == nil {
		return
	}
	for _, agentID := range registry.ListAgentIDs() {
		if agentInst, ok := registry.GetAgent(agentID); ok {
			agentInst.Tools.SetMemoryRateLimiter(limiter)
		}
	}
}

// resolveBoolWithDefault returns the bool value from a *bool, falling back to
// defaultVal when the pointer is nil. Used by WireTier13Deps for config fields
// that default true when absent.
func resolveBoolWithDefault(p *bool, defaultVal bool) bool {
	if p == nil {
		return defaultVal
	}
	return *p
}

// registerSharedToolsState carries the shared state of registerSharedTools across its stages.
type registerSharedToolsState struct {
	al              *AgentLoop
	registry        *AgentRegistry
	liveBrowserKeys map[string]bool
}

// registerSharedToolsWire3 carries the shared state of registerSharedTools across its stages.
type registerSharedToolsWire3 struct {
	cfg             *config.Config
	msgBus          *bus.MessageBus
	rs              *registerSharedToolsState
	allowReadPaths  []*regexp.Regexp
	seenBrowserKeys map[string]bool
}

// registerSharedTools registers tools that are shared across all agents.
func registerSharedTools(
	al *AgentLoop,
	cfg *config.Config,
	msgBus *bus.MessageBus,
	registry *AgentRegistry,
	provider providers.LLMProvider,
) {
	rw := &registerSharedToolsWire3{cfg: cfg, msgBus: msgBus}

	rw.rs = &registerSharedToolsState{al: al, registry: registry}

	rw.allowReadPaths = buildAllowReadPatterns(rw.cfg)

	// FR-026b. Browser managers are per BROWSING KEY, and N agents commonly
	// share one workspace — so the per-agent loop below must do the
	// create/Release/Shutdown cycle exactly ONCE per key, not once per agent.
	// Without this set, five agents on one workspace would tear down and
	// rebuild the same browser five times on every Settings save, and the
	// fifth pass would Release a manager the fourth had just installed.
	rw.seenBrowserKeys = make(map[string]bool)
	rw.rs.liveBrowserKeys = make(map[string]bool)

	for _, agentID := range rw.rs.registry.ListAgentIDs() {
		agent, ok := rw.rs.registry.GetAgent(agentID)
		if !ok {
			continue
		}

		// Web search tool — always registered; policy decides invocation.
		// Per-provider Enabled sub-flags (Brave, Tavily, etc.) are retained because
		// they select which upstream API is used, not whether the tool exists.

		rw.registerCoreTools(agent)

		// Handoff tools — always registered (ScopeCore).
		getRegistryReader := func() tools.AgentRegistryReader {
			return rw.rs.al.GetRegistry()
		}
		onHandoffFrontend := func(evt tools.HandoffEvent) {
			// The next turn resolves the active agent via sessionScopeKey(msg):
			//   webchat inbound carries a SessionID          → "session:"+SessionID
			//   channel inbound (whatsapp/telegram/…) has NO → "chat:"+channel+":"+chatID
			// Store the override under the SAME key(s) the inbound path will read.
			// The session-scoped key backs GetSessionActiveAgent + the in-turn
			// active-agent resolver; the chat-scoped key is what channel inbound
			// messages actually look up — without it a channel handoff is silently
			// dropped and routing falls back to ResolveRoute (the "agent stays" bug).
			var keys []string
			if evt.SessionID != "" {
				keys = append(keys, "session:"+evt.SessionID)
			}
			if evt.Channel != "" && evt.Channel != "webchat" && evt.ChatID != "" {
				keys = append(keys, "chat:"+evt.Channel+":"+evt.ChatID)
			}
			for _, k := range keys {
				if evt.AgentID == "" {
					rw.rs.al.sessionActiveAgent.Delete(k)
				} else {
					rw.rs.al.sessionActiveAgent.Store(k, evt.AgentID)
				}
			}
			// Record the tool's own toDefault intent, keyed the same way
			// GetSessionActiveAgent is (evt.SessionID, "session:" prefix) so
			// the WS agent_switched frame builder can read it back via the
			// exact evtSID it already uses to look up the active agent.
			if evt.SessionID != "" {
				rw.rs.al.lastSwitchToDefault.Store("session:"+evt.SessionID, evt.ToDefault)
			}
		}
		// The handoff target's window is the one its own instance resolved
		// through the ADR-066 D2 ladder (its provider, its model, its
		// override) — never a config default. An unknown target, an exempt
		// one or an unknown window yields 0: the handoff then transfers no
		// recent context and the summary line names what was left out.
		getContextWindow := func(targetAgentID string) int {
			liveRegistry := rw.rs.al.GetRegistry()
			if liveRegistry == nil {
				return 0
			}
			target, ok := liveRegistry.GetAgent(targetAgentID)
			if !ok || target == nil {
				return 0
			}
			window, _, _ := target.windowSnapshot()
			return window
		}
		getDefaultAgent := func() string {
			currentCfg := rw.rs.al.GetConfig()
			if currentCfg.Agents.Defaults.DefaultAgentID != "" {
				return currentCfg.Agents.Defaults.DefaultAgentID
			}
			// No configured override — fall through to the registry's own
			// resolution ladder (lexicographically-first non-worker agent)
			// rather than a hardcoded name; SwitchAgentTool.Execute's
			// target:"default" branch already handles an empty result as
			// "no default agent configured" rather than silently switching
			// to a name that doesn't exist.
			// liveRegistry, not the `registry` parameter this closure could
			// capture: that one is the boot-time instance, and a full registry
			// rebuild (TriggerReload, e.g. after the default agent changes)
			// REPLACES al.registry. This closure runs long after construction,
			// so reading the captured parameter would resolve the default
			// against a stale roster. The name difference is deliberate — it
			// used to shadow, which read as an accident rather than intent.
			if liveRegistry := rw.rs.al.GetRegistry(); liveRegistry != nil {
				if def := liveRegistry.GetDefaultAgent(); def != nil {
					return def.ID
				}
			}
			return ""
		}
		// sharedStore is the shared session store; tools handle a nil store by
		// skipping transcript ops (nil only occurs in tests without a store).
		sharedStore := rw.rs.al.GetSessionStore()

		rw.registerHandoffAndSkills(agentID, agent, getRegistryReader, onHandoffFrontend, getContextWindow, getDefaultAgent, sharedStore)

		// `delegate` (ADR-036 merge of the former spawn / run_subagent /
		// check_spawn_status trio into one tool — docs/internal/specs/
		// agent-delegation-spec.md) is registered unconditionally — never
		// gated by a user-visible toggle. The legacy SubagentManager (and its
		// entirely-dead runTask/Spawn/SpawnSubTurnFunc closure path — nothing
		// in production ever called SubagentManager.Spawn, which is why
		// check_spawn_status always reported "no subagents have been spawned
		// yet" for anything spawn created) is retired: DelegateTool now owns
		// its own task-state store, written by its own async path and read by
		// action:"status" — a single, connected piece of state (FR-D2).

		rw.registerDelegationTools(agentID, agent, sharedStore)

		// Task tools — require a task store (available after first NewAgentLoop call).

		rw.registerTaskAndPlanTools(agentID, agent)

		// Browser automation tools (US-4/US-6/US-7).
		// Tools are always registered; whether an agent can actually invoke them
		// is determined by the policy engine. Chromium presence/download is now
		// preprovisioned in the background at gateway boot (BrowserManager.
		// Preprovision, kicked off from RunContextWithOptions right after
		// NewAgentLoop returns) so a fresh install's managed download starts
		// immediately instead of at an agent's first browser tool call; a first
		// tool call still resolves (and, in the rare case preprovisioning hasn't
		// finished yet, blocks on) the same resolution logic and produces a
		// clear error if a binary genuinely cannot be found/installed.
		// browser.evaluate is denied by default via its executeEnabled gate
		// (cfg.Sandbox.BrowserEvaluateEnabled); see pkg/tools/browser. (#438: the
		// pkg/policy.builtinToolPolicies entry is advisory — that path is test-only.)

		rw.registerBrowserTools(agentID, agent)

		// recall_conversation (FR-008, FR-013, FR-019): session-scoped archive
		// paging. The archive reader MUST be agent.Sessions — the same store
		// windowTrim evicts from and assembleMessages reads breadcrumbs from
		// (turn.go's ts.session = agent.Sessions) — NOT al.GetSessionStore()'s
		// shared store (rooted at $OMNIPUS_HOME/sessions/, used only for
		// session routing metadata). The two are different UnifiedStore
		// instances rooted at different directories for the same sessionKey;
		// registering against the shared store means ReadArchive always finds
		// an empty/unrelated file and recall_conversation can never reach the
		// turns the breadcrumb just told the model about. The span setter is
		// the AgentLoop itself (setRecallSpan / dropRecallSpan on
		// al.recallSpans). The routing session key is read from ctx at
		// Execute time (tools.ToolSessionKey) — no per-agent construction
		// needed beyond binding the correct archive reader here.
		// Registered for every agent (mirrors instance.go's remember/
		// recall_memory/run_retrospective registration): this used to be
		// gated on `agentID != "main"`, excluding the retired sentinel. That
		// was a hardcoded identity check, not a capability one — the gate
		// went with the sentinel, same as the other memory tools. Whether an
		// agent may recall its own conversation history is its tool policy,
		// like every other tool.
		//
		// RegisterReplacing, not Register: registerSharedTools re-runs on
		// every reload and builds a fresh tool each time; #278 made Register
		// discard a same-name newcomer, so plain Register would keep the
		// stale instance. See docs/internal/false-green-patterns.md §5.

		rw.registerRecallTool(agentID, agent)

		// Register the unified `ToolSearch` infra tool (search + load paths).
		// Replaces the former search_tools_bm25 + search_tools_regex + standalone load_tool trio.
		// The resolver uses context-aware closures so per-session and per-agent state
		// is read from the tool ctx at call time, avoiding data races on the shared
		// instance across concurrent turns on the same agent.
		//
		// ALWAYS registered unconditionally — regardless of cfg.Tools.Manifest.Compressed
		// or MCP discovery settings. Registration is cheap and harmless when unused.
		//
		// Why unconditional: the tools_on_demand PUT endpoint flips Compressed live via
		// SwapConfig without re-running agent registration. If ToolSearch was only registered
		// when Compressed=true at boot, a false→true live toggle would leave ToolSearch absent
		// from the registry, causing Get("ToolSearch") to return !ok in buildCompressedToolDefs
		// and ensureInfraToolsExecutable — every lazy tool silently unreachable, no error logged.
		// The "no restart needed" promise the UI makes becomes false.
		//
		// When Compressed is OFF at turn time, the per-turn gates (cfg.Tools.Manifest.Compressed
		// at lines ~5049, ~5115, ~5026) skip the compressed paths entirely: ToolSearch is never
		// sent to the model and never force-added to policyFiltered. For an agent whose tools
		// mostly resolve to deny it is also stripped by FilterToolsByPolicy in the uncompressed
		// path (not in allow-list), so no spurious callable appears. For an agent whose tools
		// mostly resolve to allow it may appear in the uncompressed defs, which is harmless
		// (the model has all tools anyway).
		//
		// Guard against double-registration in case the MCP init path already added it.
		// Derives the name(s) to check from tools.InfraManifestToolNames() rather than a
		// hardcoded literal, so this guard cannot silently stop guarding on a future rename.

		rw.registerToolSearch(agentID, agent)

		// Register the ADR-072 D1 `Skill` tool (load-by-slug + search-by-query
		// paths) — this codebase's second instance of the "index in context,
		// content on demand" pattern ADR-071 established for ToolSearch
		// immediately above, one layer up for skills. ALWAYS registered
		// unconditionally, mirroring ToolSearch's own registration exactly:
		// Constraint #6 seeds "Skill": allow for every agent
		// (pkg/coreagent/core.go, pkg/config/defaults.go), so the tool must
		// exist to be governed by that policy regardless of whether this
		// installation has any skills installed yet. Resolver closures read
		// per-call state from ctx at call time, avoiding data races on the
		// shared instance across concurrent turns on the same agent.

		rw.registerSkillTool(agentID, agent)
	}

	rw.rs.pruneRemovedBrowsers()
}

// registerCoreTools registers the shared core tools.
func (rw *registerSharedToolsWire3) registerCoreTools(agent *AgentInstance) {
	searchTool, err := tools.NewWebSearchTool(tools.WebSearchToolOptions{
		IngestBoundBytes:      rw.cfg.Context.IngestBoundBytes, // ADR-066 D10
		BraveAPIKeys:          braveKeys(rw.cfg.Tools.Web.Brave.APIKey()),
		BraveMaxResults:       rw.cfg.Tools.Web.Brave.MaxResults,
		BraveEnabled:          rw.cfg.Tools.Web.Brave.Enabled,
		TavilyAPIKeys:         tavilyKeys(rw.cfg.Tools.Web.Tavily.APIKey()),
		TavilyBaseURL:         rw.cfg.Tools.Web.Tavily.BaseURL,
		TavilyMaxResults:      rw.cfg.Tools.Web.Tavily.MaxResults,
		TavilyEnabled:         rw.cfg.Tools.Web.Tavily.Enabled,
		DuckDuckGoMaxResults:  rw.cfg.Tools.Web.DuckDuckGo.MaxResults,
		DuckDuckGoEnabled:     rw.cfg.Tools.Web.DuckDuckGo.Enabled,
		PerplexityAPIKeys:     perplexityKeys(rw.cfg.Tools.Web.Perplexity.APIKey()),
		PerplexityMaxResults:  rw.cfg.Tools.Web.Perplexity.MaxResults,
		PerplexityEnabled:     rw.cfg.Tools.Web.Perplexity.Enabled,
		SearXNGBaseURL:        rw.cfg.Tools.Web.SearXNG.BaseURL,
		SearXNGMaxResults:     rw.cfg.Tools.Web.SearXNG.MaxResults,
		SearXNGEnabled:        rw.cfg.Tools.Web.SearXNG.Enabled,
		GLMSearchAPIKey:       rw.cfg.Tools.Web.GLMSearch.APIKey(),
		GLMSearchBaseURL:      rw.cfg.Tools.Web.GLMSearch.BaseURL,
		GLMSearchEngine:       rw.cfg.Tools.Web.GLMSearch.SearchEngine,
		GLMSearchMaxResults:   rw.cfg.Tools.Web.GLMSearch.MaxResults,
		GLMSearchEnabled:      rw.cfg.Tools.Web.GLMSearch.Enabled,
		BaiduSearchAPIKey:     rw.cfg.Tools.Web.BaiduSearch.APIKey(),
		BaiduSearchBaseURL:    rw.cfg.Tools.Web.BaiduSearch.BaseURL,
		BaiduSearchMaxResults: rw.cfg.Tools.Web.BaiduSearch.MaxResults,
		BaiduSearchEnabled:    rw.cfg.Tools.Web.BaiduSearch.Enabled,
		Proxy:                 rw.cfg.Tools.Web.Proxy,
		SSRFChecker:           rw.rs.al.ssrfChecker, // SEC-24: nil when SSRF disabled
	})
	if err != nil {
		logger.ErrorCF("agent", "Failed to create web search tool", map[string]any{"error": err.Error()})
	} else if searchTool != nil {
		agent.Tools.RegisterReplacing(searchTool)
	}

	fetchTool, err := tools.NewWebFetchToolWithProxy(
		50000,
		rw.cfg.Tools.Web.Proxy,
		rw.cfg.Tools.Web.Format,
		rw.cfg.Tools.Web.FetchLimitBytes,
		rw.cfg.Tools.Web.PrivateHostWhitelist)
	if err != nil {
		logger.ErrorCF("agent", "Failed to create web fetch tool", map[string]any{"error": err.Error()})
	} else {
		agent.Tools.RegisterReplacing(fetchTool)
	}

	// Message tool — outbound inter-agent message via bus.
	messageTool := tools.NewMessageTool()
	messageTool.SetSendCallback(func(channel, chatID, content string, origin tools.SendOrigin) error {
		pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pubCancel()
		// Origin travels with the message (ADR-065 spec FR-6) so a send can
		// be attributed, and so dispatch can re-check ownership at the last
		// common point before the wire. System-originated publishes leave
		// these empty and are exempt by that emptiness.
		return rw.msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
			Channel:          channel,
			ChatID:           chatID,
			Content:          content,
			AgentID:          origin.AgentID,
			WorkspaceID:      origin.WorkspaceID,
			OwnershipChecked: origin.OwnershipChecked,
		})
	})
	// Re-apply the stored resolver: this runs on every reload, and the
	// MessageTool above is brand new each time (ADR-065).
	if own := rw.rs.al.ChannelOwnership(); own != nil {
		messageTool.SetChannelOwnership(own)
	}
	// RegisterReplacing, not Register: #278 hardened Register to KEEP the
	// incumbent and DISCARD a same-name newcomer. Since ADR-065 rebuilds
	// messageTool on every reload, plain Register would silently throw the
	// fresh instance away and leave the stale ChannelOwnership resolver
	// live — the same defect that made browser tool re-wiring drop the
	// operator's current security state. See
	// docs/internal/false-green-patterns.md §5.
	agent.Tools.RegisterReplacing(messageTool)

	// AskUserQuestion (askuserquestion-tool-spec v3, ADR-074 D4b): the
	// owner-session structured clarification tool. The registry is
	// resolved LIVE per call via the closure (the gateway wires it with
	// SetAskUserRegistry after boot), so this registration needs no
	// re-wire pass; an unwired registry fails closed inside Execute with
	// a clear "ask conversationally" error, never a silent park.
	agent.Tools.RegisterReplacing(tools.NewAskUserQuestionTool(func() tools.AskUserQuestionRegistry {
		return rw.rs.al.getAskUserRegistry()
	}))

	// set_goal (ADR-088 D2, work-first-goal-flow-spec FR-004..FR-006):
	// the validated write-path over this session's goal record.
	// wireGoalToolsForAgent (goal_record_wiring.go) resolves the
	// session-store-backed access/diff/feasibility seams LIVE per call —
	// no external gateway wiring needed, unlike AskUserQuestion's
	// registry, since goal state lives in the SAME session store this
	// package already owns.
	wireGoalToolsForAgent(rw.rs.al, agent)
}

// registerHandoffAndSkills registers handoff and skill tools.
func (rw *registerSharedToolsWire3) registerHandoffAndSkills(agentID string, agent *AgentInstance, getRegistryReader func() tools.AgentRegistryReader, onHandoffFrontend func(evt tools.HandoffEvent), getContextWindow func(targetAgentID string) int, getDefaultAgent func() string, sharedStore *session.UnifiedStore) {
	agent.Tools.RegisterReplacing(tools.NewSwitchAgentTool(getRegistryReader, sharedStore, getContextWindow, getDefaultAgent, onHandoffFrontend))

	// Send file tool (outbound media via MediaStore — store injected later by SetMediaStore).
	sendFileTool := tools.NewSendFileTool(
		agent.Home,
		rw.cfg.Agents.Defaults.RestrictToWorkspace,
		rw.cfg.Agents.Defaults.GetMaxMediaSize(),
		nil,
		rw.allowReadPaths,
	)
	agent.Tools.RegisterReplacing(sendFileTool)

	// Skill discovery and installation tools — always registered.
	// Runtime failures (ClawHub unreachable, no auth token) surface at call
	// time with clear errors.
	{
		// Skill marketplaces are configured as a unified list (FR-10.1):
		// ClawHub + GitHub (+ future "omnipus") entries under
		// tools.skills.marketplaces. Credential refs are resolved via
		// os.Getenv (populated by credentials.InjectFromConfig, SEC-23).
		// GitHub entries get the agent workspace injected (the persisted
		// shape carries no workspace field).
		registryMgr := skills.NewRegistryManagerFromConfig(skills.RegistryConfig{
			Marketplaces: skills.MarketplacesFromConfig(
				rw.cfg, os.Getenv, nil /* SSRF handled per-registry at the gateway */, agent.Home,
			),
			MaxConcurrentSearches: rw.cfg.Tools.Skills.MaxConcurrentSearches,
		})

		searchCache := skills.NewSearchCache(
			rw.cfg.Tools.Skills.SearchCache.MaxSize,
			time.Duration(rw.cfg.Tools.Skills.SearchCache.TTLSeconds)*time.Second,
		)
		agent.Tools.RegisterReplacing(tools.NewFindSkillsTool(registryMgr, searchCache))
		// ADR-046 FR-009: install_skill targets the fixed, install-wide
		// GLOBAL skills directory ($OMNIPUS_HOME/skills) — the SAME
		// directory every agent's own SkillsLoader searches (see
		// globalSkillsDir's doc comment in context.go) — never
		// agent.Home, so a skill installed by one agent is discoverable
		// by every other agent.
		agent.Tools.RegisterReplacing(tools.NewInstallSkillTool(registryMgr, globalSkillsDir()))
		// remove_skill is NOT registered here: it is a ScopeCore
		// management tool (systools.SkillRemoveTool, "remove_skill"),
		// wired onto every agent's Tools registry by WireSysagentDeps
		// (pkg/gateway/gateway.go), which shares its SkillInstaller/
		// SkillsLoader with this same skill engine. A prior version of
		// this block registered a second, competing ScopeGeneral
		// implementation here, constructed against the agent's own
		// per-agent workspace root (the field ADR-057 FR-001/FR-002
		// renamed to .Home) — a root that predates ADR-046 FR-009's move
		// to a single global skills directory and that install_skill
		// above no longer targets. Do not reintroduce it.
	}

	// Email tools (M11) — registered ONLY for the agent that owns a configured,
	// enabled mailbox with a resolvable password. Email is a TOOL surface
	// (read_inbox · search_email · read_message · send_email · reply) over the
	// pure-Go IMAP/SMTP transport, not a conversational channel. The tools flow
	// through the normal per-agent tool policy, so god-mode / O7 policy applies.
	registerEmailToolsForAgent(rw.cfg, agentID, agent)
}

// registerDelegationTools registers delegation tools.
func (rw *registerSharedToolsWire3) registerDelegationTools(agentID string, agent *AgentInstance, sharedStore *session.UnifiedStore) {
	{
		delegateTool := tools.NewDelegateTool(agent.Model, agent.MaxTokens, agent.Temperature)
		// ADR-057 W17 (FR-069/FR-070/FR-095): wrap the real spawner with
		// the root-delegation admission gate (admission.go) so a
		// ROOT-level `delegate` fan-out from this agent is actually
		// capped by al.rootDelegationAdmission — the SAME shared,
		// process-wide instance every other agent's DelegateTool is
		// wrapped with, so the cap applies once across the whole running
		// gateway, not per agent. See rootDelegationAdmittingSpawner's
		// doc comment (admission.go) for why wrapping SpawnSubTurn here
		// is the correct choke point for both sync and async delegation.
		delegateTool.SetSpawner(newRootDelegationAdmittingSpawner(NewSubTurnSpawner(rw.rs.al), rw.rs.al.rootDelegationAdmission, agentID))
		// Retain it so Close() can drain its background delegations before
		// the stores they write through are torn down. See delegateTools.
		rw.rs.al.delegateToolsMu.Lock()
		rw.rs.al.delegateTools = append(rw.rs.al.delegateTools, delegateTool)
		rw.rs.al.delegateToolsMu.Unlock()
		// FR-196 kill switch — wire it HERE, at construction, not only in
		// SetSessionMessagingStores' later re-wire. This is a PER-AGENT
		// DelegateTool: the session_messaging_wire.go re-wire walks the
		// shared registry, so an agent-scoped instance built here would
		// otherwise never be wired at all. An unwired tool fails CLOSED
		// (delegate.go sessionMessagingPlaneEnabled), which would deny the
		// whole gated action set — cancel/steer/respond/inbox/inbox_ack/
		// follow_up/peek — for every agent. The closure re-reads config per
		// call, so a live kill-switch flip is still honored.
		delegateTool.SetSessionMessagingEnabled(rw.rs.al.sessionMessagingEnabledLive())
		// R2-MAJ-015 — the operator kill switch for delegate's FR-015
		// fail-closed parent-agent-id guard
		// (tools.delegate.require_parent_agent_id). Same live-closure
		// discipline as the FR-196 switch immediately above, and for a
		// sharper reason: this guard's failure mode is "delegation stops
		// entirely across the install", so the escape hatch is worthless
		// if escaping it needs a restart. al.GetConfig() is re-read per
		// call rather than captured here — an eagerly-read value would
		// freeze at whatever this wiring pass saw, which for a dependency
		// the gateway assigns AFTER tool wiring means frozen at nil while
		// registration still looks correct.
		delegateTool.SetRequireParentAgentID(func() bool {
			return rw.rs.al.GetConfig().Tools.Delegate.EffectiveRequireParentAgentID()
		})
		// W2: action:"status" live-progress snapshot for a running native
		// task. sharedStore mirrors the exact store wiring the
		// tools.NewSwitchAgentTool(...) call above already uses — the same
		// *session.UnifiedStore delegated children's transcript entries
		// are actually written to. It is a plain value captured once at
		// registration time (NOT a live func()), so it does not itself
		// reflect a later hot reload; SetAgentRegistry below is the one
		// that gets the live func() treatment, since al.GetRegistry() is
		// invoked fresh on every call.
		// Typed-nil guard: al.GetSessionStore() can return a nil
		// *session.UnifiedStore on a degraded boot (loop.go:609-620). Boxing
		// a nil concrete pointer into the DelegateSessionStore interface
		// yields a NON-nil interface, so SetSessionStore's own `== nil`
		// graceful-degrade guard would never fire and recentActivityLines
		// would panic on a nil receiver. Only wire the store when non-nil so
		// a running-native status snapshot degrades to prompt-only instead
		// of crashing the whole action:"status" call. (The sibling
		// NewSwitchAgentTool wiring above shares this pre-existing latent
		// pattern; tracked separately.)
		if sharedStore != nil {
			delegateTool.SetSessionStore(sharedStore)
		}
		// G1 fix: wire the live tool-call-argument progress reader so
		// action:"status" can tell "still generating a large tool-call
		// argument" apart from "hung" for a running native child — see
		// tools.DelegateProgressReader's doc comment. al itself
		// implements the interface (AgentLoop.ProgressForSession,
		// turn.go), reading straight from al.activeTurnStates — the
		// same live-turn registry GetActiveTurnHookForSession/
		// claimAnyTurnForSession already use for cancellation — so no
		// typed-nil guard is needed here (unlike sharedStore above):
		// al is the *AgentLoop this tool is being registered on, never
		// nil at this point in construction.
		delegateTool.SetProgressReader(rw.rs.al)
		delegateTool.SetAgentRegistry(func() tools.DelegateAgentRegistry { return rw.rs.al.GetRegistry() })
		// FR-028/BDD-29 (ADR-057 U14): wire the shared, process-wide
		// SessionManager so `delegate action="cancel"` actually kills the
		// TARGET child's own background bash/exec shells, not just its
		// turn. Without this, killChildBackgroundShells (delegate.go)
		// starts with `if t.sessionManager == nil { return }` — always
		// taken — and a cancelled delegate's background dev server (or
		// any other backgrounded shell) is silently orphaned holding its
		// port. tools.GetSharedSessionManager() is the SAME process-wide
		// singleton ExecTool/bash register their background sessions
		// with (session.go/session_manager_export.go), so this ties the
		// cancel path to the actual live session registry rather than a
		// fresh, empty one.
		delegateTool.SetSessionManager(tools.GetSharedSessionManager())
		currentAgentID := agentID
		// ADR-037: the legacy DelegationPolicy.To / SubagentsConfig.AllowAgents
		// allowlist checkers (SetAllowlistChecker / SetDelegateChecker) are
		// retired — config.AgentConfig.DelegationPolicy no longer exists, and
		// those checkers were only ever consulted as a fallback when the graph
		// checkers below were nil, which never happens in production wiring.
		// The per-workspace delegation graph (buildDelegationDenyChecker) is
		// now the ONLY delegation gate.
		//
		// FR-6.2: full-policy gate for the background (async=true, the
		// default) mode — trust set + mode("background") + depth.
		delegateTool.SetDelegationDenyCheckerBackground(
			// ForDelegate bakes in exempt=false: delegate(agent_id=self) spawns a
			// real sub-turn and MUST be graph-gated (and thus denied), never exempted.
			buildDelegationDenyCheckerForDelegate(
				currentAgentID,
				rw.cfg.Agents.Defaults,
				config.DelegationModeBackground,
				agentExistsChecker(rw.rs.registry),
			),
		)
		// FR-6.2: full-policy gate for the await (async=false) mode. Uses
		// the same buildDelegationDenyChecker as the background gate
		// above but with DelegationModeAwait, so a targeted
		// delegate(agent_id="X", async=false) is checked against the
		// caller→X edge for the "await" mode, and an untargeted call
		// falls back to evalUntargetedDelegation.
		delegateTool.SetDelegationDenyCheckerAwait(
			// ForDelegate bakes in exempt=false: same reasoning as the background
			// gate — a self-targeted await delegate() is real delegation, graph-gated.
			buildDelegationDenyCheckerForDelegate(
				currentAgentID, rw.cfg.Agents.Defaults, config.DelegationModeAwait, agentExistsChecker(rw.rs.registry),
			),
		)
		// #477 / FR-D9-FR-D10: thread the SAME effective depth cap the
		// gates above just authorized against into spawnSubTurn's own
		// depth check — the resolver is mode-agnostic (sourced only from
		// the matched edge's own Depth, shared by both the background and
		// await gates) — so the spawn-time backstop does not
		// independently re-derive (and silently override) an explicit
		// per-edge Depth.
		delegateTool.SetDelegationDepthResolver(buildDelegationDepthResolver(
			currentAgentID, rw.cfg.Agents.Defaults,
		))

		// ADR-057: derive the ownership-walk bound from the SAME operator
		// setting that bounds delegation depth. Left unwired, the walk used
		// a hardcoded 3 while delegation depth stayed configurable — so
		// raising max_depth made cancel/steer/peek on a legitimate depth-4+
		// child fail with an ownership error indistinguishable from a real
		// cross-tenant attempt. Zero/unset is ignored by the setter, which
		// keeps its own default.
		delegateTool.SetOwnershipWalkMaxDepth(rw.cfg.Agents.Defaults.SubTurn.MaxDepth)

		agent.Tools.RegisterReplacing(delegateTool)
	}

	// ADR-053 Phase 2 on-ramp (session_messaging_wire.go): wire the S2/S3
	// session-control hooks onto THIS agent's delegate + message_parent
	// tools. On the FIRST registerSharedTools pass (inside NewAgentLoop,
	// before the gateway constructs the stores) the stores are nil → both
	// tools register fail-closed (Execute returns "not configured"). The
	// gateway's later SetSessionMessagingStores call re-runs this wiring
	// with the real stores, mirroring SetPlanStore's late-binding
	// discipline exactly. Safe on hot-reload (idempotent re-wire).
	rw.rs.al.wireSessionMessagingForAgent(agent)
}

// registerTaskAndPlanTools registers task and plan tools.
func (rw *registerSharedToolsWire3) registerTaskAndPlanTools(agentID string, agent *AgentInstance) {
	if rw.rs.al.taskStore != nil {
		currentAgentID := agentID

		agent.Tools.RegisterReplacing(tools.NewTaskListTool(rw.rs.al.taskStore))

		taskCreate := tools.NewTaskCreateTool(rw.rs.al.taskStore)
		// Resolve the real default workspace (is_default ULID) when a
		// chat-delegated task has no workspace bound to the turn — never the
		// literal "default" (which would land it in an invisible workspace).
		taskCreate.SetHome(filepath.Dir(rw.cfg.AgentHomeBasePath()))
		// ADR-052 FR-002: wire the plan store so the optional plan_id
		// linkage arg can be validated (validateTaskPlanLinkage,
		// pkg/tools/plan.go) instead of failing closed with "plan store is
		// not configured" on every call. al.GetPlanStore() may still be nil
		// on this very first registerSharedTools pass (it runs inside
		// NewAgentLoop, before the gateway's setupAndStartServices
		// constructs the real plan.Store) — that is fine, SetPlanStore's
		// per-agent loop below re-wires this tool with the real store once
		// it exists, exactly like wirePlanToolsForAgent's own create_plan/
		// execute_plan late-binding discipline.
		taskCreate.SetPlanStore(rw.rs.al.GetPlanStore())
		// Founder decision 2026-09-14 (D-D/D-E): the paired goal record an
		// agent-created task gets carries the LIVE Settings -> Performance
		// goal try limit, read at create time — not the shipped default.
		taskCreate.SetGoalMaxRoundsFn(func() int { return goalTryLimit(rw.rs.al) })
		// ADR-037: the legacy boolean delegateCheck (SetDelegateChecker,
		// backed by config.ResolveDelegationTo) is retired — the field it
		// read no longer exists. The graph-based deny checker below is the
		// only gate now (it was already the sole gate in production).
		// FR-6.2: full-policy gate — trust set + mode("task") + depth.
		taskCreate.SetDelegationDenyChecker(
			// ForTaskReassignment bakes in exempt=true: assigning a NEW task to
			// oneself is not delegation (no new instance spawned), not graph-gated.
			buildDelegationDenyCheckerForTaskReassignment(
				currentAgentID,
				rw.cfg.Agents.Defaults,
				config.DelegationModeTask,
				agentExistsChecker(rw.rs.registry),
			),
		)
		// Task-mode recursion bound: reject a task_create issued from within a
		// task run whose delegation generation already sits at the ceiling. The
		// per-agent depth gate cannot bound task mode on its own because every
		// task run starts a fresh turn at depth 0 (see processTaskDirect depth
		// seeding); this hard ceiling closes that gap.
		taskCreate.SetMaxDelegationDepth(maxTaskDepth)
		// Founder decision 2026-09-15: refuse assigning a task to an agent
		// that cannot finish it (task_assignee_readiness.go).
		taskCreate.SetAssigneeReadinessChecker(rw.rs.al.TaskAssigneeCannotFinish)
		// D2 rule 5 (FR-017/052, review r1 major M5): reject an all-check
		// criteria create outright when the assignee's effective bash
		// policy is deny or ask — structurally unsatisfiable, mirrors
		// judge.go's runMachineCheck policy resolution exactly (same
		// registry, same EffectiveToolPolicy call, ScopeCore).
		taskCreate.SetBashPolicyChecker(func(assigneeAgentID string) (policy string, ok bool) {
			agentInst, found := rw.rs.al.GetRegistry().GetAgent(assigneeAgentID)
			if !found || agentInst == nil {
				return "", false
			}
			return tools.EffectiveToolPolicy(agentInst.LoadToolPolicy(), tools.ScopeCore, agentInst.AgentType, "bash"), true
		})
		// subagent_3p (external-CLI) worker task assignment is no longer
		// guarded here: processTaskDirect (this file) now branches on
		// runner.ResolveDispatch and routes an external-CLI worker's task
		// run through runExternalCLISubTurn instead of the native engine —
		// see its doc comment for the dispatch design. The former
		// SetExternalCLIWorkerChecker rejection (mirrored on the REST path's
		// validateTaskAgentID) is retired now that the engine gap it
		// papered over is closed.
		taskCreate.SetOnCreate(func(entity *task.Task) {
			rw.rs.al.EmitTaskStatusChanged(TaskStatusChangedPayload{
				TaskID:    entity.ID,
				Status:    string(entity.Status),
				SessionID: "task:" + entity.ID,
				AgentID:   entity.AgentID,
			})
			// Register the task's time trigger (no-op for manual/heartbeat).
			rw.rs.al.NotifyTaskUpserted(entity)
		})
		agent.Tools.RegisterReplacing(taskCreate)

		taskUpdate := tools.NewTaskUpdateTool(rw.rs.al.taskStore)
		// Same live goal try limit as taskCreate above, for the goal record
		// update_task creates when a legacy task gets criteria/dod.
		taskUpdate.SetGoalMaxRoundsFn(func() int { return goalTryLimit(rw.rs.al) })
		taskUpdate.SetAssigneeReadinessChecker(rw.rs.al.TaskAssigneeCannotFinish)
		taskUpdate.SetOnComplete(func(t *task.Task) {
			if rw.rs.al.taskExecutor != nil {
				rw.rs.al.taskExecutor.onTaskComplete(t)
			}
			// A terminal update removes the task's trigger job UNLESS the
			// trigger repeats (recurring/every), whose series survives past a
			// per-run terminal status (OnTaskUpserted, pkg/agent/task_trigger.go);
			// a non-terminal update re-syncs it (a no-op if it is already
			// correctly armed for the current trigger content).
			rw.rs.al.NotifyTaskUpserted(t)
		})
		// ADR-037: legacy SetDelegateChecker retired here too — see the
		// taskCreate comment above.
		// FR-6.2: reassignment is re-delegation — gate agent_id changes through
		// the same trust-set + mode("task") + depth policy as task_create.
		taskUpdate.SetDelegationDenyChecker(
			// ForTaskReassignment bakes in exempt=true: reassigning a task to its
			// existing owner is a no-op reassignment, not delegation — not graph-gated.
			buildDelegationDenyCheckerForTaskReassignment(
				currentAgentID,
				rw.cfg.Agents.Defaults,
				config.DelegationModeTask,
				agentExistsChecker(rw.rs.registry),
			),
		)
		// Same rationale as taskCreate above: the subagent_3p reassignment
		// guard is retired now that processTaskDirect dispatches an
		// external-CLI worker's task run through runExternalCLISubTurn.
		agent.Tools.RegisterReplacing(taskUpdate)

		setTodos := tools.NewSetTodosTool(rw.rs.al.taskStore)
		setTodos.SetHome(filepath.Dir(rw.cfg.AgentHomeBasePath()))
		agent.Tools.RegisterReplacing(setTodos)
		agent.Tools.RegisterReplacing(tools.NewTaskDeleteTool(rw.rs.al.taskStore))
		agent.Tools.RegisterReplacing(tools.NewAgentListTool(func() []tools.AgentInfo {
			var infos []tools.AgentInfo
			for _, id := range rw.rs.registry.ListAgentIDs() {
				if a, ok := rw.rs.registry.GetAgent(id); ok {
					// ADR-049 D3: System Agents (the Judge) are excluded from
					// list_agents — it is the delegation picker ("resolve agent
					// names to IDs before delegating"), and a System Agent is
					// never a delegation target (nor a chat target). Excluding it
					// here keeps the picker consistent with the workspace
					// delegation graph, which never contains a System Agent.
					if a.AgentType == string(config.AgentTypeSystem) {
						continue
					}
					infos = append(infos, tools.AgentInfo{ID: a.ID, Name: a.Name, Type: "custom"})
				}
			}
			return infos
		}))
	}

	// ADR-052 plan/task tool surface (create_plan, execute_plan,
	// run_task, inspect_session) — the single wiring site Wave 1 left
	// unwired for Wave 2 (see pkg/tools/plan.go / run_task.go /
	// inspect_session.go's "another wave's job" doc comments). Not
	// nested inside the `al.taskStore != nil` guard above —
	// wirePlanToolsForAgent does its own nil-checks per dependency
	// (taskStore, taskExecutor, planStore, session store) and logs
	// loudly (Error) on any gap rather than silently skipping the whole
	// surface. al.GetPlanStore() may still return nil here on the very
	// FIRST pass — this call runs inside NewAgentLoop, before the
	// gateway constructs the real plan.Store in setupAndStartServices —
	// so create_plan/execute_plan register with a nil store and fail
	// closed at Execute() (Wave-1 discipline) until SetPlanStore
	// re-wires every agent with the real store once it exists. Read via
	// the accessor (not the bare al.planStore field) since SetPlanStore
	// writes it under al.mu — a bare field read here would race that
	// writer (7-reviewer gate NIT).
	rw.rs.al.wirePlanToolsForAgent(agent, rw.rs.al.GetPlanStore())

	// list_jobs (the unified background-job roster). Separate from the
	// plan surface above because it spans plans, standalone tasks AND
	// delegated sessions, and because it needs no late re-bind — every
	// store is read through a live adapter (see wireJobRosterForAgent).
	rw.rs.al.wireJobRosterForAgent(agent)
}

// registerSharedToolsWire3RegisterBrowserTools carries the shared state of registerBrowserTools across its stages.
type registerSharedToolsWire3RegisterBrowserTools struct {
	rw           *registerSharedToolsWire3
	agentID      string
	pool         *browser.BrowserPool
	cfgSnapshot  browser.BrowserConfig
	ssrfSnapshot *security.SSRFChecker
	factory      func(key browser.BrowsingKey) (*browser.BrowserManager, error)
	key          browser.BrowsingKey
	keyErr       error
}

// registerBrowserTools registers browser tools and manages browser lifecycle.
func (rw *registerSharedToolsWire3) registerBrowserTools(agentID string, agent *AgentInstance) {
	bw := &registerSharedToolsWire3RegisterBrowserTools{rw: rw, agentID: agentID}

	{
		browserCfg, cfgErr := browser.DefaultConfig()
		if cfgErr != nil {
			logger.ErrorCF("agent", "Browser tools: cannot determine defaults — skipping",
				map[string]any{"error": cfgErr.Error()})
		} else {
			// DefaultConfig sets Headless=true; only override if config explicitly sets fields.
			if bw.rw.cfg.Tools.Browser.CDPURL != "" {
				browserCfg.CDPURL = bw.rw.cfg.Tools.Browser.CDPURL
			}
			if bw.rw.cfg.Tools.Browser.PageTimeoutSec > 0 {
				browserCfg.PageTimeout = time.Duration(bw.rw.cfg.Tools.Browser.PageTimeoutSec) * time.Second
			}
			// FR-023a: the lease wait is CLAMPED against page_timeout at
			// load AND here on every reload — EffectiveLeaseWaitSec is the
			// one function that does both, so the two can never disagree.
			browserCfg.LeaseWait = time.Duration(
				bw.rw.cfg.Tools.Browser.EffectiveLeaseWaitSec(),
			) * time.Second
			if bw.rw.cfg.Tools.Browser.ProfileDir != "" {
				browserCfg.ProfileDir = bw.rw.cfg.Tools.Browser.ProfileDir
			}
			if bw.rw.cfg.Tools.Browser.ExecPath != "" {
				browserCfg.ExecPath = bw.rw.cfg.Tools.Browser.ExecPath
			}
			// Idle reaping: 0 = unset, keep browser.DefaultIdleTTL;
			// negative = operator explicitly disables reaping (mapped to 0,
			// which ReapIdleSessions treats as "never reap").
			if bw.rw.cfg.Tools.Browser.IdleTTLSec > 0 {
				browserCfg.IdleTTL = time.Duration(bw.rw.cfg.Tools.Browser.IdleTTLSec) * time.Second
			} else if bw.rw.cfg.Tools.Browser.IdleTTLSec < 0 {
				browserCfg.IdleTTL = 0
			}
			// ADR-075 FR-040a / FR-072: the whole-browser idle window and
			// the closed-profile cache-trim schedule. Both are documented
			// operator keys, and both were unreachable until this line —
			// the value was parsed into nothing and the pool silently ran
			// its built-in constants, so an operator who changed the number
			// saw exactly what one who had not saw. Assigned
			// UNCONDITIONALLY (0 means "unset", which is what the pool's
			// own default fallback expects) and on the reload pass as well
			// as the fresh-seed one, so a Settings save takes effect
			// without a restart. Zero and negative both mean "use the
			// default" — there is no value that switches idle close off
			// (FR-061). Regression coverage:
			// pkg/tools/browser/pool_ttl_config_reachability_test.go.
			browserCfg.IdleCloseTTL = bw.rw.cfg.Tools.Browser.EffectiveIdleCloseTTL()
			browserCfg.CacheTrimInterval = bw.rw.cfg.Tools.Browser.EffectiveCacheTrimInterval()
			// ADR-085 BROWSER-FR-031a/FR-052: the LiveViewRegistry
			// idle-release sweeper's window and the take-control
			// enablement flag it reads on every tick — see
			// browser.BrowserConfig's doc comments on both fields.
			browserCfg.ControlIdleRelease = time.Duration(
				bw.rw.cfg.Tools.Browser.EffectiveControlIdleReleaseSec(),
			) * time.Second
			browserCfg.TakeControlEnabled = bw.rw.cfg.Tools.Browser.TakeControlEnabled
			// Start page: an operator override wins; otherwise default to
			// the gateway's own served start page so a fresh tab lands
			// somewhere branded and legible instead of about:blank (a blank
			// void is indistinguishable from a broken panel on this
			// surface). Addressed over LOOPBACK deliberately — the client
			// is the managed headless Chrome running on this same host, so
			// localhost is reachable even when the gateway binds a wildcard
			// address (where the canonical public origin is empty) and even
			// with no public URL configured at all. The same
			// localhost:port origin is already granted through the SSRF
			// checker just above.
			if bw.rw.cfg.Tools.Browser.StartPageURL != "" {
				browserCfg.StartPageURL = bw.rw.cfg.Tools.Browser.StartPageURL
			} else if bw.rw.cfg.Gateway.Port > 0 {
				browserCfg.StartPageURL = fmt.Sprintf("http://localhost:%d/browser-start", bw.rw.cfg.Gateway.Port)
			}
			// ADR-052 D2/M1: PreferPackaged and TrustPathChrome are
			// ALWAYS copied (bool fields, no "unset vs explicit false"
			// distinction needed — the default-config zero value IS
			// the security-hardened default). Without these the runtime
			// resolver stays on its own defaults and the operator's
			// config flips have no effect (SPEC-002). Both are wired
			// every reload, not just at first-seed, so a Settings save
			// takes effect without a gateway restart.
			browserCfg.PreferPackaged = bw.rw.cfg.Tools.Browser.PreferPackaged
			browserCfg.TrustPathChrome = bw.rw.cfg.Tools.Browser.TrustPathChrome
			// Headless is intentionally NOT copied from
			// cfg.Tools.Browser.Headless here: browser.DefaultConfig()
			// always sets Headless=true, and a bare bool config field
			// can't distinguish "operator explicitly set false" from
			// "unset" — honoring a zero-value false would silently break
			// every display-less server deployment (the common case).
			// There is no supported way to run non-headless today.
			browserCfg.PersistSession = bw.rw.cfg.Tools.Browser.PersistSession

			// WebRTC build (ADR-047, wave-plan W2-A): seed the gateway-owned
			// tabCapture capture extension into $OMNIPUS_HOME/browser/
			// (config.OmnipusHomeDir()/browser — the helper, never an ad-hoc
			// join) and wire ExtensionDir/ExtensionID onto browserCfg so the
			// coordinator's launch flags (--allowlisted-extension-id,
			// --enable-unsafe-extension-debugging — exec_resolver.go) apply
			// and its post-launch auto-load (coordinator.go's launchChrome)
			// picks it up. Seed is atomic/idempotent (captureext.Seed) and
			// harmless even when WebRTC ends up gated off at request time
			// (WebRTCEnabled=false, lite build, or ClassifyVideoCapability
			// not_capable) — the extension simply never gets used. Best-effort:
			// a seed failure only means the WebRTC capture path degrades to
			// "not_capable"-equivalent (no ExtensionDir set, so LoadExtension
			// is never attempted) — it must never abort ordinary browser-tool
			// registration for this agent.
			if extDir, seedErr := captureext.Seed(
				filepath.Join(config.OmnipusHomeDir(), "browser"),
			); seedErr != nil {
				logger.WarnCF(
					"agent",
					"WebRTC capture extension seed failed — live-view WebRTC will report not_capable",
					map[string]any{"error": seedErr.Error()},
				)
			} else {
				browserCfg.ExtensionDir = extDir
				browserCfg.ExtensionID = captureext.ExtensionID
			}

			// preview-on-main-listener v5 (FR-018/US-10, S21): let the agent's
			// built-in browser reach the gateway's OWN preview origin.
			// serve_web mints http://localhost:<gateway.port>/preview/...
			// when gateway.public_url is unset (US-1 AS-2); CheckHost/CheckIP
			// are otherwise port-blind, so without a scoped exception the
			// gateway's own preview would either need a blanket loopback
			// allow (rejected by the ADR — opens every local dev port) or
			// stay blocked entirely. The exception scopes to exactly this
			// host:port pair — passing "localhost" (its documented expected
			// caller usage) also accepts the resolved "127.0.0.1"/"::1"
			// loopback forms for the SAME port, per r4 OBS-003.
			//
			// CRITICAL (code-review M2): the exception MUST live on a checker
			// dedicated to the browser tool, NOT on al.ssrfChecker. That
			// singleton is shared with provider base_url and skill-installer
			// URL validation (rest.go/rest_onboarding.go/gateway.go CheckURL
			// callers); mutating it in place would silently allow
			// localhost:<gateway.port> there too — the blanket-loopback
			// widening the ADR rejected. CloneWithGatewayOrigin returns an
			// independent checker sharing the singleton's block-lists/allowlist
			// but carrying its own exception. The SSRF-disabled branch already
			// mints a fresh per-agent checker, so it takes the exception directly.
			var browserSSRF *security.SSRFChecker
			if bw.rw.rs.al.ssrfChecker != nil {
				browserSSRF = bw.rw.rs.al.ssrfChecker.CloneWithGatewayOrigin("localhost", bw.rw.cfg.Gateway.Port)
			} else {
				browserSSRF = security.NewSSRFChecker(nil)
				browserSSRF.AllowGatewayOrigin("localhost", bw.rw.cfg.Gateway.Port)
			}

			// browser_evaluate registration: the tool is ALWAYS registered,
			// on every agent, regardless of this flag — registration has
			// never been conditional. What the flag gates is EXECUTION, at
			// EvaluateTool.Execute.
			//
			// sandbox.browser_evaluate_enabled is now SEEDED TRUE
			// (ADR D1.9b ruling 2), so on a fresh install the tool works and
			// which agents may call it is decided by tool policy. This
			// remains the operator's runtime kill switch.
			//
			// nil resolves to FALSE, not true: a construction that skips
			// DefaultConfig() must not silently turn arbitrary in-page
			// JavaScript on. The default lives in the seed, which is data,
			// never in this resolution. (#438: the
			// pkg/policy.builtinToolPolicies entry is advisory; that path is
			// test-only, not a live dispatch gate.)
			evaluateEnabled := config.ResolveBool(bw.rw.cfg.Sandbox.BrowserEvaluateEnabled, false)
			// ADR-043: ensure the gateway-scoped shared-Chrome coordinator
			// exists (constructed once; reused across hot-reload so the
			// per-agent browser contexts it owns — and thus agents' login
			// state — survive a Settings save). An agent configured with an
			// explicit tools.browser.cdp_url bypasses the coordinator: its
			// ensureStarted takes the CDPURL branch first.
			//
			// MED-1: on a RELOAD (coordinator already exists), apply the
			// runtime-cheap config deltas.
			// headless/exec_path/profile_dir are launch-time properties of
			// the already-running Chrome and cannot hot-apply —
			// ApplyRuntimeConfig warn-logs those so an operator isn't
			// silently misled. CRIT-002 stays intact: the coordinator is
			// never rebuilt on reload.
			bw.rw.rs.al.mu.Lock()
			if bw.rw.rs.al.browserPool == nil {
				bw.rw.rs.al.browserPool = browser.NewBrowserPool(bw.rw.rs.al.homePath, browserCfg)
				// FR-042a: before this gateway launches anything, settle
				// what a PREVIOUS run left behind — stale markers cleared,
				// orphaned Chromes terminated, keys another live gateway
				// still owns refused. Discriminated by the launch lock, not
				// by the marker's pid; see ReconcileMarkers for why that
				// distinction is what stops one gateway killing another's
				// browser.
				if refused := bw.rw.rs.al.browserPool.ReconcileMarkers(); len(refused) > 0 {
					logger.WarnCF("agent", "another gateway owns some workspaces' browsers — this one will not start them",
						map[string]any{"workspaces": refused})
				}
			} else {
				bw.rw.rs.al.browserPool.ApplyRuntimeConfig(browserCfg)
			}
			// FR-034: push tools.browser.actionability_gate into the
			// actionability gate's single chokepoint. It runs on the
			// fresh-seed pass AND on every config reload — the revert
			// switch takes effect without a restart, which is the whole
			// reason it exists.
			browser.SetActionabilityGate(bw.rw.cfg.Tools.Browser.ActionabilityGate)
			// ADR-075 D2 FR-027: browser_snapshot renders field VALUES by
			// operator ruling, so its rendered outline is run through the
			// credential replacer before it is returned. Wired at this
			// call site, and for the same reason as the line above: it
			// runs on the fresh-seed pass AND on every config reload, so a
			// secret the operator registers after boot is covered without
			// a restart. Defence in depth, not the control that makes the
			// tool safe — it substitutes registered credential plaintexts
			// and does nothing for arbitrary form values.
			browser.SetSensitiveDataReplacer(bw.rw.cfg.SensitiveDataReplacer())
			bw.pool = bw.rw.rs.al.browserPool
			bw.rw.rs.al.mu.Unlock()
			// fs-workspace: browser tools (browser_screenshot) get agent.Home +
			// RestrictToWorkspace so screenshot paths resolve through the same
			// workspace root as the other file tools (FR-009).
			// FR-002a: the tools take a RESOLVER, not a manager. The
			// browser a tool drives is now a property of the TURN
			// (ResolveBrowsingKey + BrowserManagerForKey), never of
			// whichever agent it was registered under — which is the
			// reported defect ADR-075 §1.1 records.
			if regErr := browser.RegisterTools(
				agent.Tools, bw.rw.rs.al.browserResolver(), evaluateEnabled,
				agent.Home, bw.rw.cfg.Agents.Defaults.RestrictToWorkspace,
			); regErr != nil {
				logger.ErrorCF("agent", "Failed to register browser tools",
					map[string]any{"error": regErr.Error(), "agent_id": bw.agentID})
			} else {
				bw.rw.rs.al.mu.Lock()
				bw.rw.rs.al.browserRegisteredAgents[bw.agentID] = true
				// The factory carries THIS reload's config + SSRF checker,
				// so a lazily-created manager gets the operator's current
				// security state rather than boot-time state.
				bw.cfgSnapshot = browserCfg
				bw.ssrfSnapshot = browserSSRF
				bw.rw.rs.al.browserFactory = func(key browser.BrowsingKey) (*browser.BrowserManager, error) {
					return bw.createBrowserManager(key)
				}
				bw.factory = bw.rw.rs.al.browserFactory
				bw.rw.rs.al.mu.Unlock()

				// FR-026b: one register/release cycle per BROWSING KEY per
				// reload, not per agent. N agents on one workspace resolve
				// to ONE key, and doing this per agent would tear the same
				// browser down and back up N times per Settings save.
				bw.key, bw.keyErr = browser.ResolveBrowsingKeyForAgent(omnipusHome(), bw.agentID, "")
				switch {
				case bw.keyErr != nil:
					bw.handleBrowserKeyErrNil()
				case bw.rw.seenBrowserKeys[bw.key.String()]:
					bw.handleBrowserCase()
				default:
					bw.handleBrowserDefault()
				}
			}
		}
	}
}

// createBrowserManager creates a browser manager from the current configuration snapshot.
func (bw *registerSharedToolsWire3RegisterBrowserTools) createBrowserManager(key browser.BrowsingKey) (*browser.BrowserManager, error) {
	m, err := browser.NewBrowserManager(bw.cfgSnapshot, bw.ssrfSnapshot)
	if err != nil {
		return nil, err
	}
	m.AttachPool(bw.pool, key)
	return m, nil
}

// handleBrowserKeyErrNil handles the `keyErr != nil` case of registerBrowserTools.
func (bw *registerSharedToolsWire3RegisterBrowserTools) handleBrowserKeyErrNil() {
	// No workspace (or an ambiguous membership, FR-033).
	// The tools stay registered and each call reports
	// ErrNoBrowsingContext by name — never a shared browser.
	logger.DebugCF("agent", "no browser for this agent yet — it is not rooted in one workspace",
		map[string]any{"agent_id": bw.agentID, "reason": bw.keyErr.Error()})
}

// handleBrowserCase handles the `rw.seenBrowserKeys[key.String()]` case of registerBrowserTools.
func (bw *registerSharedToolsWire3RegisterBrowserTools) handleBrowserCase() {
	bw.rw.rs.liveBrowserKeys[bw.key.String()] = true
}

// handleBrowserDefault handles the `default` case of registerBrowserTools.
func (bw *registerSharedToolsWire3RegisterBrowserTools) handleBrowserDefault() {
	bw.rw.seenBrowserKeys[bw.key.String()] = true
	bw.rw.rs.liveBrowserKeys[bw.key.String()] = true
	bw.rw.rs.al.rewireBrowserManagerForKey(bw.key, bw.factory)
}

// registerRecallTool registers the conversation recall tool.
func (rw *registerSharedToolsWire3) registerRecallTool(agentID string, agent *AgentInstance) {
	if agent.Sessions != nil {
		agent.Tools.RegisterReplacing(NewRecallConversationTool(agent.Sessions, rw.rs.al))
	} else {
		logger.WarnCF("agent",
			"recall_conversation not registered — agent.Sessions is nil",
			map[string]any{"agent_id": agentID})
	}
}

// registerToolSearch registers tool discovery and loading support.
func (rw *registerSharedToolsWire3) registerToolSearch(agentID string, agent *AgentInstance) {
	{
		alreadyTools := true
		for _, infraName := range tools.InfraManifestToolNames() {
			if _, ok := agent.Tools.Get(infraName); !ok {
				alreadyTools = false
				break
			}
		}
		if !alreadyTools {
			capturedAgentID := agentID

			ttl := rw.cfg.Tools.MCP.Discovery.TTL
			if ttl <= 0 {
				ttl = 5
			}
			maxResults := rw.cfg.Tools.MCP.Discovery.MaxSearchResults
			if maxResults <= 0 {
				maxResults = 5
			}

			toolsTool := tools.NewToolsTool(agent.Tools, ttl, maxResults)
			toolsTool.SetResolver(
				// canLoad returns (true, "") when name is a policy-allowed LAZY tool for
				// the calling agent. Full/infra tools are handled before this call (they
				// return a no-op success in execLoad). When denied, the returned reason
				// string is surfaced verbatim in the ToolSearch error message.
				func(ctx context.Context, name string) (bool, string) {
					callerID := tools.ToolAgentID(ctx)
					if callerID == "" {
						callerID = capturedAgentID
					}
					callerAgent, ok := rw.rs.al.registry.GetAgent(callerID)
					if !ok {
						return false, name + " — agent not found"
					}
					// UAT 2026-09-13 D-84: a policy deny that bites at
					// tool-LOAD time used to leave no deny row at all — the
					// refusal surfaced only as a ToolSearch error, so an
					// auditor searching for denied writes to a tool found
					// nothing. Every policy-denied load below goes through
					// this one closure so the audit row is never forgotten.
					deniedByPolicy := func() (bool, string) {
						rw.rs.al.emitToolLoadPolicyDenyAudit(ctx, callerID, name)
						return false, name + " — denied by this agent's policy"
					}
					allAgentTools := callerAgent.Tools.GetAll()
					policyFiltered, policyVerdicts := tools.FilterToolsByPolicy(
						allAgentTools,
						callerAgent.AgentType,
						callerAgent.LoadToolPolicy(),
					)
					// Tier gate: full/infra tools are already callable — they never
					// need to be loaded. Check policy FIRST so a denied full-tier tool
					// gets a clear "denied" signal rather than a false "already available".
					// If policy allows a full-tier tool, return the sentinel
					// "already available — just call it directly" reason so execLoad can
					// treat it as a no-op success rather than a load.
					if tools.ToolManifestTier(name) != tools.ManifestLazy {
						for _, t := range policyFiltered {
							if t.Name() == name {
								// Policy-allowed full-tier: signal as "already available"
								// using the typed sentinel so execLoad can distinguish
								// this from a policy denial without substring matching.
								return false, tools.CanLoadAlreadyAvailablePrefix + " — just call it directly"
							}
						}
						// Policy-denied full-tier (or genuinely not found for this tier).
						return deniedByPolicy()
					}
					for _, t := range policyFiltered {
						if t.Name() == name {
							// ADR-071 §3.2's ambiguity band needs to know when a
							// loadable tool's resolved policy is "ask" (requires
							// user confirmation) so it can exclude such tools from
							// the speculative cross-category promotion clause.
							// FilterToolsByPolicy already resolved this per-tool
							// verdict; surface it via the typed sentinel reason
							// rather than a second lookup.
							if policyVerdicts[name] == "ask" {
								return true, tools.CanLoadAskPolicyPrefix
							}
							return true, ""
						}
					}
					// Hidden tools (deferred MCP tools registered via RegisterHidden)
					// are NOT in GetAll() until promoted, so the visible check above
					// misses them. They ARE loadable: the load path promotes (un-hides)
					// them before fetching the schema. Resolve the hidden tool directly
					// and evaluate its policy so an allowed hidden MCP tool can be loaded
					// by search/auto-load. (Without this, search surfaces the MCP tool
					// but load rejects it as "unknown" — the chicken-and-egg the MCP UAT
					// caught: search uses the hidden corpus, canLoad used only GetAll.)
					if hiddenTool, hok := callerAgent.Tools.GetIncludingHidden(name); hok {
						hiddenAllowed, hiddenVerdicts := tools.FilterToolsByPolicy(
							[]tools.Tool{hiddenTool},
							callerAgent.AgentType,
							callerAgent.LoadToolPolicy(),
						)
						if len(hiddenAllowed) > 0 {
							if hiddenVerdicts[name] == "ask" {
								return true, tools.CanLoadAskPolicyPrefix
							}
							return true, ""
						}
						// Tool exists (visible or hidden) but policy denies it.
						return deniedByPolicy()
					}
					// Tool is not in GetAll() and not hidden — check if it's in the full
					// registered set but policy-filtered out (i.e. registered but denied).
					for _, t := range allAgentTools {
						if t.Name() == name {
							return deniedByPolicy()
						}
					}
					// Genuinely unknown: suggest the closest registered name so the model
					// can correct a hallucinated or transposed name (C4 fix). Match
					// against policyFiltered (the POLICY-ALLOWED set), not allAgentTools
					// (the pre-policy set) — a typo suggestion must never point at a tool
					// this agent's policy denies. Cost: hidden MCP tools aren't in
					// policyFiltered, so a near-miss typo of a hidden MCP tool's name
					// gets a bare "unknown tool" with no "did you mean" hint. That's the
					// correct tradeoff (never suggest a name the agent can't call).
					//
					// D-96 (UAT 2026-09-13): the ranking is suggestUnknownToolName's,
					// not raw edit distance — asked for `knowledge_create`, raw
					// distance answered `knowledge_read`, the one knowledge tool that
					// cannot create anything. The suffix the caller named is matched
					// against each tool's `op` enum first, so the hint names the tool
					// AND the op that does what the caller asked for.
					if suggestion, opHint := suggestUnknownToolName(policyFiltered, name); suggestion != "" {
						msg := name + " — unknown tool (did you mean '" + suggestion + "'?"
						if opHint != "" {
							msg += " with " + opHint
						}
						return false, msg + "?)"
					}
					return false, name + " — unknown tool name"
				},
				// markLoaded: fetches schemas FIRST, marks only successfully resolved
				// names as loaded, and returns any names that could not be resolved in
				// the rejected slice. This ensures the model's loaded-set is always
				// consistent with what it can actually call: a name that canLoad
				// accepted but whose registry lookup or schema extraction fails is
				// reported as rejected and never marked loaded.
				func(ctx context.Context, names []string) (map[string]any, []string) {
					callerID := tools.ToolAgentID(ctx)
					if callerID == "" {
						callerID = capturedAgentID
					}
					callerAgent, ok := rw.rs.al.registry.GetAgent(callerID)
					if !ok {
						// No agent — reject everything so the caller can surface the error.
						rejected := make([]string, 0, len(names))
						for _, n := range names {
							rejected = append(rejected, n+" — agent not found at load time")
						}
						return map[string]any{}, rejected
					}

					// Fetch schemas first; separate names into resolved and rejected.
					schemas := make(map[string]any, len(names))
					loadedOK := make([]string, 0, len(names))
					var rejected []string
					for _, n := range names {
						t, tOK := callerAgent.Tools.Get(n)
						if !tOK {
							rejected = append(rejected, n+" — not registered for agent at load time")
							continue
						}
						schema := tools.ToolToSchema(t)
						fn, fnOK := schema["function"]
						if !fnOK {
							rejected = append(rejected, n+" — schema has no function key")
							continue
						}
						schemas[n] = fn
						loadedOK = append(loadedOK, n)
					}

					// Mark only the successfully resolved names as loaded.
					// ADR-071 D3 §4.6: the bucket is (agent, session), not
					// session alone — callerID is the same value already
					// resolved above (tools.ToolAgentID(ctx), falling back
					// to capturedAgentID), matching what the readers
					// (buildCompressedToolDefs/buildToolManifestNote) derive
					// from ts.agent.ID.
					bucket := manifestBucketKey(
						callerID,
						tools.ToolTranscriptSessionID(ctx),
						tools.ToolSessionKey(ctx),
					)
					rw.rs.al.markToolsLoaded(bucket, loadedOK)

					// ADR-071 §4.3.1(a) FR-038/FR-038a: record a pending
					// search-follow-up entry for each newly-promoted name,
					// but ONLY on the query (by-description) path — an
					// exact-name `names` load is the model deliberately
					// naming a tool it already knows about, and recording
					// it would reintroduce the false-positive floor r3/r4
					// diagnosed and corrected (see the ADR's MIN-001 note).
					if tools.IsSearchPromotion(ctx) {
						rw.rs.al.recordPendingSearchPromotions(bucket, loadedOK)
					}
					return schemas, rejected
				},
			)
			agent.Tools.RegisterReplacing(toolsTool)
		}
	}
}

// registerSkillTool registers the skill tool.
func (rw *registerSharedToolsWire3) registerSkillTool(agentID string, agent *AgentInstance) {
	{
		capturedAgentID := agentID

		skillMaxResults := rw.cfg.Tools.MCP.Discovery.MaxSearchResults
		if skillMaxResults <= 0 {
			// ADR-072 D1.2/MIN-003: Skill's search mode deliberately
			// inherits ToolSearch's own result cap rather than
			// introducing a second number to reason about.
			skillMaxResults = 5
		}

		if _, already := agent.Tools.Get("Skill"); !already {
			skillTool := tools.NewSkillTool(skillMaxResults)
			if agent.DocumentRuntime != nil {
				skillTool.SetDocumentRuntime(*agent.DocumentRuntime)
			}
			skillTool.SetResolver(
				// load resolves slug for the acting agent through the full
				// per-shelf grant model (ADR-072 D4/D4.1, via
				// ContextBuilder.ResolveSkillFullForWorkspace) and loads its
				// body directly from the resolved shelf's own on-disk path
				// for this turn only — every Skill call audited (D3.1).
				func(ctx context.Context, slug string) tools.SkillLoadOutcome {
					callerID := tools.ToolAgentID(ctx)
					if callerID == "" {
						callerID = capturedAgentID
					}
					workspaceID := tools.ToolWorkspaceID(ctx)
					callerAgent, ok := rw.rs.al.registry.GetAgent(callerID)
					if !ok {
						audit.EmitSkillCall(rw.rs.al.auditLogger, callerID, workspaceID, slug,
							audit.SkillCallModeLoad, audit.SkillCallOutcomeNotFound, "")
						return tools.SkillLoadOutcome{Status: tools.SkillLoadNotFound}
					}

					if resolved, resolvedOK := callerAgent.ContextBuilder.ResolveSkillFullForWorkspace(workspaceID, slug); resolvedOK {
						if content, readOK := skills.LoadSkillFile(resolved.Path); readOK {
							audit.EmitSkillCall(rw.rs.al.auditLogger, callerID, workspaceID, resolved.Slug,
								audit.SkillCallModeLoad, audit.SkillCallOutcomeLoaded, string(resolved.Shelf))
							return tools.SkillLoadOutcome{
								Status:        tools.SkillLoadLoaded,
								Content:       content,
								Shelf:         resolved.Shelf,
								CanonicalSlug: resolved.Slug,
							}
						}
						// Resolved but the file vanished or became unreadable
						// between resolution and read (rare race) — report
						// not-found rather than a silent empty load.
						logger.WarnCF("agent", "skill resolved but its content could not be read",
							map[string]any{"agent_id": callerID, "skill": slug, "path": resolved.Path})
						audit.EmitSkillCall(rw.rs.al.auditLogger, callerID, workspaceID, slug,
							audit.SkillCallModeLoad, audit.SkillCallOutcomeNotFound, "")
						return tools.SkillLoadOutcome{Status: tools.SkillLoadNotFound}
					}

					// Not resolved — distinguish "exists but this agent is
					// not granted it" (registry/builtin shelf, unfiltered
					// via ListSkillsDetailed) from "genuinely absent on any
					// shelf" (ADR-072 D4/FR-054's SkillNotFoundCode).
					for _, s := range callerAgent.ContextBuilder.ListSkillsDetailed() {
						if strings.EqualFold(s.ID, slug) || strings.EqualFold(s.Name, slug) {
							audit.EmitSkillCall(rw.rs.al.auditLogger, callerID, workspaceID, slug,
								audit.SkillCallModeLoad, audit.SkillCallOutcomeDenied, "")
							return tools.SkillLoadOutcome{Status: tools.SkillLoadDenied}
						}
					}
					audit.EmitSkillCall(rw.rs.al.auditLogger, callerID, workspaceID, slug,
						audit.SkillCallModeLoad, audit.SkillCallOutcomeNotFound, "")
					return tools.SkillLoadOutcome{Status: tools.SkillLoadNotFound}
				},
				// canUse reports whether the acting agent may load slug —
				// the SAME per-shelf grant model `load` consults, exposed
				// separately so the search path can filter the ranked
				// match list without loading every candidate's full body.
				func(ctx context.Context, slug string) bool {
					callerID := tools.ToolAgentID(ctx)
					if callerID == "" {
						callerID = capturedAgentID
					}
					callerAgent, ok := rw.rs.al.registry.GetAgent(callerID)
					if !ok {
						return false
					}
					workspaceID := tools.ToolWorkspaceID(ctx)
					_, resolvedOK := callerAgent.ContextBuilder.ResolveSkillFullForWorkspace(workspaceID, slug)
					return resolvedOK
				},
				// corpus returns every installed skill's slug+description
				// across every shelf visible to the acting agent's
				// workspace — registry+builtin (UNFILTERED by any grant)
				// plus that workspace's own project shelf — for BM25
				// ranking (ADR-071 §3.2.2, applied to skills by ADR-072
				// D1): the corpus must never be pre-filtered, only the
				// ranked match list (via canUse above).
				func(ctx context.Context) []tools.SkillSearchDoc {
					callerID := tools.ToolAgentID(ctx)
					if callerID == "" {
						callerID = capturedAgentID
					}
					callerAgent, ok := rw.rs.al.registry.GetAgent(callerID)
					if !ok {
						return nil
					}
					workspaceID := tools.ToolWorkspaceID(ctx)
					all := callerAgent.ContextBuilder.ListSkillsDetailed()
					projectShelf := callerAgent.ContextBuilder.ProjectShelfForWorkspace(workspaceID)

					docs := make([]tools.SkillSearchDoc, 0, len(all)+len(projectShelf))
					seen := make(map[string]struct{}, len(all)+len(projectShelf))
					for _, s := range all {
						docs = append(docs, tools.SkillSearchDoc{Slug: s.ID, Description: s.Description})
						seen[strings.ToLower(s.ID)] = struct{}{}
					}
					for key, ps := range projectShelf {
						if _, dup := seen[key]; dup {
							// D4.2 carve-out: a granted registry/builtin
							// slug already claims this name in the menu and
							// on resolution — the search corpus must not
							// offer it twice under two different shelves.
							continue
						}
						docs = append(docs, tools.SkillSearchDoc{Slug: ps.ID, Description: ps.Description})
					}
					return docs
				},
			)
			agent.Tools.RegisterReplacing(skillTool)
		}
	}
}

// pruneRemovedBrowsers removes stale browser registrations and closes browsers no live agent is rooted in.
func (rs *registerSharedToolsState) pruneRemovedBrowsers() {
	// FR-026a. A workspace whose last agent was removed (or whose team moved
	// off it) leaves a BrowserManager in al.browserMgrs and — worse — a
	// coordinator-owned browser context (cookie/localStorage partition)
	// leaking forever in c.contexts. Diff the LIVE BROWSING KEYS this pass
	// resolved against what the map holds, and dispose the difference via
	// coordinator.RemoveAgent (which cancels the OWNING chromedp context so
	// chromedp runs Target.disposeBrowserContext, unlike reload-Release which
	// preserves it).
	//
	// ⚠️ The liveness predicate is the set of live BROWSING KEYS, never
	// registry.ListAgentIDs(). This diff used to compare the map against agent
	// ids, which was correct only while the map WAS keyed by agent id: run
	// unchanged against a key-keyed map it matches nothing, so every browser
	// looks removed and every workspace's Chrome context is disposed on the
	// first Settings save — logins gone, silently, with a cheerful INFO line
	// per workspace saying it removed a manager for a "deleted agent".
	rs.al.mu.Lock()
	pool := rs.al.browserPool
	var removedKeys []string
	for k := range rs.al.browserMgrs {
		if !rs.liveBrowserKeys[k] {
			removedKeys = append(removedKeys, k)
			delete(rs.al.browserMgrs, k)
		}
	}
	registeredAgentIDs := rs.registry.ListAgentIDs()
	stillPresent := make(map[string]bool, len(registeredAgentIDs))
	for _, id := range registeredAgentIDs {
		stillPresent[id] = true
	}
	for id := range rs.al.browserRegisteredAgents {
		if !stillPresent[id] {
			delete(rs.al.browserRegisteredAgents, id)
		}
	}
	rs.al.mu.Unlock()
	for _, k := range removedKeys {
		// FR-026's roster-change half: a workspace that no longer has a single
		// browser-policy-allowed agent on its CoreTeam gets its Chrome CLOSED.
		//
		// Closed, not deleted. The workspace still exists and its user still
		// expects to be logged in when an agent is added back, so the profile
		// directory stays on disk (FR-043a: workspace DELETION is the only
		// trigger that removes it, and that path lives in the REST handler).
		if pool != nil {
			if key, kerr := browser.ParseBrowsingKeyString(k); kerr == nil {
				pool.Close(key)
			}
		}
		logger.InfoCF("agent", "closed the browser for a workspace no live agent is rooted in (its profile is kept)",
			map[string]any{"browsing_key": k})
	}
}

// agentLoopInspectSessionStore adapts AgentLoop.ResolveSessionStore to the
// tools.InspectSessionStore interface (GetMeta/ReadTranscript keyed purely
// on session ID — see that interface's own doc comment: "the store resolves
// the owning agent internally"). ResolveSessionStore already implements
// exactly that resolution (shared store fast path, then a scan across every
// per-agent store, cancel.go) — reused
// here rather than re-implemented, so inspect_session and RequestCancel
// agree on where any given session id actually lives.
type agentLoopInspectSessionStore struct {
	al *AgentLoop
}

// wirePlanToolsForAgent constructs and registers the ADR-052 create_plan /
// execute_plan / run_task / inspect_session tool surface, plus the ADR-055
// plan_correct / stop_plan supervision surface, for a single
// agent instance — the single clear wiring site Wave 1 left unwired for
// Wave 2 (pkg/tools/plan.go / run_task.go / inspect_session.go's "another
// wave's job" doc comments name pkg/agent/loop.go explicitly). Called from
// registerSharedTools's per-agent loop (planStore may be nil there on the
// very first pass) and again from SetPlanStore for every already-registered
// agent once the gateway installs the real store.
//
// Every dependency gap is logged LOUDLY (Error, never silently) at wiring
// time so an unwired seam is visible in the boot log — on top of, not
// instead of, each tool's own Wave-1 fail-closed Execute() behavior (nil
// store / nil checker / nil dispatcher => explicit error result, never an
// implicit allow or a silently-dead no-op tool).
// The six tools below (the ADR-052 four, plus ADR-055's plan_correct and
// stop_plan) are registered via RegisterReplacing, not Register:
// this function is called once per agent at registerSharedTools time AND
// again for every already-registered agent from SetPlanStore once the real
// plan.Store is installed (this function's own doc comment) — the second
// pass is an EXPECTED same-name re-registration, not an accidental
// collision, so it must not spam a WARN per tool per agent (7-reviewer gate
// item 5; see ToolRegistry.RegisterReplacing's own doc comment).
func (al *AgentLoop) wirePlanToolsForAgent(agent *AgentInstance, planStore *plan.Store) {
	if agent == nil || agent.Tools == nil {
		return
	}

	if planStore == nil {
		logger.WarnCF("agent", "wirePlanToolsForAgent: plan store not yet installed — "+
			"create_plan/execute_plan register but will fail closed until SetPlanStore runs",
			map[string]any{"agent_id": agent.ID})
	}

	// create_plan (FR-001, US-1): owner_agent_id validated against the live
	// config/registry (SetOwnerValidator — see the field doc on
	// tools.PlanCreateTool for the fail-closed-when-unwired discipline this
	// honors).
	planCreate := tools.NewPlanCreateTool(planStore)
	planCreate.SetHome(al.homePath)
	planCreate.SetOwnerValidator(func(ownerAgentID string) error {
		return validatePlanOwnerAgentForTool(al.GetConfig(), ownerAgentID)
	})
	agent.Tools.RegisterReplacing(planCreate)

	// execute_plan (FR-003/004/030, US-3): SD-A7 tiered-DoD gate's isAgentID
	// checker (SetIsAgentIDChecker) mirrors gateway's restAPI.isAgentID.
	if al.taskStore == nil {
		logger.ErrorCF("agent", "wirePlanToolsForAgent: task store unavailable — "+
			"execute_plan registered but will fail closed (no task store to list members)",
			map[string]any{"agent_id": agent.ID})
	}
	planExecute := tools.NewPlanExecuteTool(planStore, al.taskStore)
	planExecute.SetIsAgentIDChecker(al.isRegisteredAgentID)
	agent.Tools.RegisterReplacing(planExecute)

	// run_task (FR-019, US-10): dispatches via the real
	// TaskExecutor.StartTaskNow — the standalone-task full attempt loop.
	taskRun := tools.NewTaskRunTool(al.taskStore)
	if al.taskExecutor != nil {
		taskRun.SetStartTaskNow(al.taskExecutor.StartTaskNow)
	} else {
		logger.ErrorCF("agent", "wirePlanToolsForAgent: task executor unavailable — "+
			"run_task registered but will fail closed (no dispatcher installed)",
			map[string]any{"agent_id": agent.ID})
	}
	agent.Tools.RegisterReplacing(taskRun)

	// inspect_session (FR-033, US-13 Acceptance 3): verifier-role-only by
	// seeded tool policy (enforced outside this function); target-session
	// locked via the engine-set WithVerifierSessionScope ctx value
	// (verifier_adjudication.go), not by anything wired here.
	//
	// Deliberately NOT wired to al.GetSessionStore() alone: that is only
	// the SHARED store at $OMNIPUS_HOME/sessions/ (new webchat/channel
	// sessions), but a task's own session — the exact thing task-scope
	// verification targets — is created via createTaskSessionSync
	// (task_executor.go), which writes through al.GetAgentStore(t.AgentID):
	// the ASSIGNEE agent's own per-agent legacy store, a DIFFERENT
	// directory. A single fixed store can never cover both. agentLoopInspectSessionStore
	// (below) instead adapts al.ResolveSessionStore — the SAME
	// shared-store-first-then-per-agent-scan resolver cancel.go's
	// RequestCancel already uses to find an arbitrary session id's owning
	// store — so inspect_session finds a target session regardless of
	// which store actually holds it. Always non-nil (a value type over a
	// non-nil al): Execute()'s own not-found error (via GetMeta/
	// ReadTranscript, once VerifierSessionScopeAllows has already passed)
	// is the fail-closed signal, not a nil-store check.
	agent.Tools.RegisterReplacing(tools.NewInspectSessionTool(agentLoopInspectSessionStore{al: al}))

	// --- ADR-055 plan supervision surface: plan_correct + stop_plan --------
	//
	// Both engine hooks are installed as LATE-RESOLVING CLOSURES over
	// GetPlanEngine(al), never as a value captured here. This is load-bearing,
	// not a style choice: the gateway installs the plan engine LAST
	// (gateway.go's SetPlanEngine, after SetPlanStore), and SetPlanEngine only
	// assigns the field — it does not re-run this function. A hook capturing
	// al.planEngine at wiring time would therefore be nil on EVERY pass and
	// both tools would sit permanently in their fail-closed "engine is not
	// wired" branch, in a build where everything looks correctly registered.
	//
	// The closures preserve the fail-closed contract they replace: an absent
	// engine returns an explicit error, so the tool reports a failure and
	// never a silent success. Neither tool's authority is wired here — both
	// gate internally, and deliberately in opposite directions: plan_correct
	// admits only the PlanSupervisor identity (tools.PlanSupervisorAgentID),
	// stop_plan admits only the plan's own owner agent. The adjudicator
	// corrects; the owner contains.
	planCorrect := tools.NewPlanCorrectTool(planStore, al.taskStore)
	planCorrect.SetAppendCorrection(func(
		ctx context.Context, planID string, caller tools.CorrectionCaller, req tools.CorrectionRequest,
	) (string, bool, error) {
		pe := GetPlanEngine(al)
		if pe == nil {
			return "", false, errors.New(
				"plan engine is not installed — corrections cannot be applied")
		}
		// Passed through whole, NOT rebuilt field-by-field. FR-004 moved the
		// correction types into pkg/plan and left tools.CorrectionCaller /
		// agent.CorrectionCaller as type ALIASES of plan.CorrectionCaller
		// (likewise CorrectionRequest), so these are one type, not two that
		// happen to match — the compiler accepts the value directly and the
		// old CorrectionVerb() conversion became a no-op the linter flags.
		//
		// The rebuild also had to go on its own merits: enumerating the
		// fields here means the next field added to plan.CorrectionRequest is
		// silently dropped at this seam, with nothing failing to compile.
		// This branch has shipped that exact shape more than once.
		res, err := pe.AppendCorrection(ctx, planID, caller, req)
		if err != nil {
			return "", false, err
		}
		if res == nil {
			// Defensive: a nil result with a nil error would otherwise be
			// reported to the adjudicator as a successful correction carrying
			// an empty revision id.
			return "", false, errors.New(
				"plan engine returned no correction result")
		}
		return res.RevisionID, res.HonestExit, nil
	})
	agent.Tools.RegisterReplacing(planCorrect)

	// stop_plan (FR-042/FR-043, US-8): the containment control. StopPlan's
	// signature matches StopPlanFunc exactly, so the closure adds only the
	// late engine resolution described above.
	planStop := tools.NewPlanStopTool(planStore)
	planStop.SetStopPlan(func(ctx context.Context, planID, userID, channel string) (*plan.Plan, error) {
		pe := GetPlanEngine(al)
		if pe == nil {
			return nil, errors.New(
				"plan engine is not installed — the plan cannot be stopped")
		}
		return pe.StopPlan(ctx, planID, userID, channel)
	})
	agent.Tools.RegisterReplacing(planStop)
}

func (s agentLoopInspectSessionStore) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	store := s.al.ResolveSessionStore(sessionID)
	if store == nil {
		return nil, fmt.Errorf("session %q not found in any known session store", sessionID)
	}
	return store.GetMeta(sessionID)
}

func (s agentLoopInspectSessionStore) ReadTranscript(sessionID string) ([]session.TranscriptEntry, error) {
	store := s.al.ResolveSessionStore(sessionID)
	if store == nil {
		return nil, fmt.Errorf("session %q not found in any known session store", sessionID)
	}
	return store.ReadTranscript(sessionID)
}

func (l agentLoopJobPlanLister) List(filter plan.Filter) ([]plan.Plan, error) {
	store := l.al.GetPlanStore()
	if store == nil {
		return nil, errors.New("plan store is not installed")
	}
	return store.List(filter)
}

func (l agentLoopJobTaskLister) List(filter task.Filter) ([]task.Task, error) {
	store := GetTaskStore(l.al)
	if store == nil {
		return nil, errors.New("task store is not installed")
	}
	return store.List(filter)
}

func (l agentLoopJobLifecycleLister) List(
	filter session.LifecycleFilter,
) ([]session.LifecycleRecord, error) {
	store := l.al.GetSessionLifecycleStore()
	if store == nil {
		return nil, errors.New("session lifecycle store is not installed")
	}
	return store.List(filter)
}

func (n agentLoopJobAgentNamer) AgentDisplayName(agentID string) (string, bool) {
	reg := n.al.GetRegistry()
	if reg == nil {
		return "", false
	}
	return reg.GetAgentName(agentID)
}

// --- list_jobs wiring (the unified background-job roster) -----------------
//
// Every store below is reached through a LATE-RESOLVING adapter rather than a
// value captured at wiring time, because the three stores list_jobs reads are
// installed at three DIFFERENT points in gateway boot, all of which can follow
// this wiring: the task store exists from NewAgentLoop, the plan store arrives
// with SetPlanStore, and the lifecycle store arrives later still with
// SetSessionMessagingStores (which re-wires only the session-messaging tool
// surface, never this one). A captured lifecycle store would be nil forever
// and the `subagent` kind would silently report an empty roster on every call.
//
// Each adapter returns an explicit ERROR when its store is absent — never an
// empty slice. list_jobs turns that into a per-kind error entry, which is the
// whole point: "a short list that looks complete is the worst possible output"
// (NewListJobsTool's own doc). A nil-return adapter would produce exactly the
// silent-undercount failure the tool is built to prevent.
//
// DELIBERATELY NOT IMPLEMENTED HERE: the optional ListLenient siblings
// (tools.jobPlanLenientLister and friends). list_jobs picks those up by
// OPTIONAL type assertion against the value passed to NewListJobsTool — i.e.
// against these adapters. None of pkg/plan, pkg/task or pkg/session implements
// ListLenient yet, so defining a forwarding method here would make the
// assertion succeed while the count it feeds (`unreadable`) is structurally
// always 0 — an honest-looking zero that means "not measured", not "nothing
// was corrupt". The seam is left unsatisfied on purpose so it stays visibly
// unwired. WHEN ListLenient LANDS on the concrete stores, add the matching
// forwarding method to the adapter below; that is the only change needed.
type agentLoopJobPlanLister struct{ al *AgentLoop }

type agentLoopJobTaskLister struct{ al *AgentLoop }

type agentLoopJobLifecycleLister struct{ al *AgentLoop }

// agentLoopJobAgentNamer resolves a delegated agent's display name for a
// subagent row's label. AgentRegistry.GetAgentName already has exactly this
// contract (name+true when the agent exists, the raw id when its name is
// empty, ("",false) when it does not), so this forwards rather than
// re-deriving. A false return is a NORMAL case: durable lifecycle records
// outlive the agents they name, and the tool falls back to the raw agent id.
type agentLoopJobAgentNamer struct{ al *AgentLoop }

// wireJobRosterForAgent registers `list_jobs` for one agent.
//
// Called from registerSharedTools' per-agent loop (so it re-runs on every hot
// reload, picking up the fresh config). It is deliberately NOT called from
// SetPlanStore's re-wire loop the way the plan surface is: the adapters above
// read every store live, so there is nothing for a later pass to re-bind.
//
// Two of this tool's seven setters are left UNWIRED because the
// implementations they need do not exist yet. Each omission degrades honestly
// and is listed here so the gap is visible at the wiring site rather than
// inferred from behaviour:
//
//   - SetCapSnapshotSource — needs PlanEngine.CapSnapshot, a LOCK-FREE reader
//     over values the engine already published from inside its own admission
//     path. It must never be faked from Admit (which takes the engine mutex
//     exclusively and re-scans the plan store) nor re-derived independently.
//     Unwired, the cap fields are omitted as a pair, which is the designed
//     degradation; a fabricated source reporting 0 would be strictly worse
//     than absent.
//   - SetScanCeiling — the config key list_jobs' own doc names
//     (tools.list_jobs.max_records_scanned_per_kind) does not exist in
//     pkg/config. Unwired, the package default (5000/kind) applies and a
//     crossing is still REPORTED via notes.scan_truncated.
//
// SetAuditLogger is intentionally not called either, but for the opposite
// reason: it is already satisfied. ListJobsTool implements the registry's
// auditLoggerAware contract, and ToolRegistry propagates the logger on
// registration and on its own SetAuditLogger — wiring it by hand here would
// duplicate that with a value that goes stale.
func (al *AgentLoop) wireJobRosterForAgent(agent *AgentInstance) {
	if agent == nil || agent.Tools == nil {
		return
	}
	listJobs := tools.NewListJobsTool(
		agentLoopJobPlanLister{al: al},
		agentLoopJobTaskLister{al: al},
		agentLoopJobLifecycleLister{al: al},
	)
	// A LIVE closure, never al.GetConfig()'s value: this function is reached
	// from registerSharedTools, which the hot-reload path runs BEFORE it swaps
	// al.cfg. A value read here is therefore the PRE-reload config on every
	// reload, so the reload that enables tools.filter_sensitive_data would be
	// exactly the one list_jobs missed — it would keep emitting plan and task
	// titles unredacted until some unrelated later reload happened to run.
	// (Boot is unaffected, which is what made this invisible.) The setter takes
	// only a closure so the mistake cannot be re-made here.
	listJobs.SetConfig(func() *config.Config { return al.GetConfig() })
	listJobs.SetAgentNamer(func() tools.JobAgentNamer {
		return agentLoopJobAgentNamer{al: al}
	})
	// SetSessionResolver over THIS agent's own *tools.DelegateTool (now that
	// it implements tools.JobSessionResolver via ResolvableSessionIDs). A
	// LIVE closure, same discipline as SetConfig/SetAgentNamer above — it
	// re-resolves agent.Tools.Get("delegate") on every list_jobs call rather
	// than capturing a value at wiring time, so wiring order relative to the
	// delegate tool's own registration in this same per-agent pass does not
	// matter, and a hot-reload that replaces the delegate tool instance is
	// picked up automatically. A missing or
	// wrong-typed "delegate" registration (an agent with no delegate tool at
	// all) resolves to a nil JobSessionResolver, which collectSubagentRows
	// already treats as "nothing resolves" — the same honest degradation
	// this setter's absence used to produce, not a new failure mode.
	listJobs.SetSessionResolver(func() tools.JobSessionResolver {
		raw, ok := agent.Tools.Get("delegate")
		if !ok {
			return nil
		}
		delegateTool, ok := raw.(*tools.DelegateTool)
		if !ok {
			return nil
		}
		return delegateTool
	})
	// SetLabelResolver over the SAME *tools.DelegateTool (UAT M3, 2026-08-03
	// / #584): DelegateTool now also implements tools.JobLabelResolver via
	// ResolvableLabels, so list_jobs' label_contains filter can match a
	// subagent's custom delegate label instead of only its raw agent/session
	// identifiers. Same live-closure discipline as SetSessionResolver
	// immediately above — re-resolves on every call rather than capturing a
	// value at wiring time — for the identical reasons (wiring-order
	// independence, hot-reload safety, honest nil degradation when no
	// delegate tool is registered).
	listJobs.SetLabelResolver(func() tools.JobLabelResolver {
		raw, ok := agent.Tools.Get("delegate")
		if !ok {
			return nil
		}
		delegateTool, ok := raw.(*tools.DelegateTool)
		if !ok {
			return nil
		}
		return delegateTool
	})
	// RegisterReplacing, not Register: registerSharedTools re-runs on every hot
	// reload, so a same-name re-registration is EXPECTED and must not log a
	// WARN per agent per reload.
	agent.Tools.RegisterReplacing(listJobs)
}

// SetSysagentDeps stores the system.* tool dependencies for use by hot-reload.
// Call WireSysagentDeps after this to immediately register the tools.
func (al *AgentLoop) SetSysagentDeps(deps *systools.Deps) {
	al.sysagentDeps = deps
}

// WireSysagentDeps registers all 35 system.* tools on every agent in the
// current registry (FR-001, FR-002). Mirrors the WireTier13Deps pattern:
// called once at boot after NewAgentLoop, and again on hot-reload. The deps
// pointer is stashed so hot-reload can re-apply the wiring on the rebuilt registry.
//
// Per-agent policy (seeded via coreagent.SeedConfig) governs which agents may
// actually invoke these tools at LLM-call time — this registration is the
// supply side; policy is the demand filter.
func (al *AgentLoop) WireSysagentDeps(deps *systools.Deps) {
	depsCopy := *deps
	al.sysagentDeps = &depsCopy
	al.wireSysagentDepsLocked(al.registry, &depsCopy)
}

// wireSysagentDepsLocked registers system.* tools on all agents in registry.
// Factored out so hot-reload can re-apply against a freshly-built registry.
func (al *AgentLoop) wireSysagentDepsLocked(registry *AgentRegistry, deps *systools.Deps) {
	if registry == nil || deps == nil {
		return
	}
	sysToolList := systools.AllTools(deps)
	for _, agentID := range registry.ListAgentIDs() {
		ag, ok := registry.GetAgent(agentID)
		if !ok || ag == nil || ag.Tools == nil {
			continue
		}
		for _, t := range sysToolList {
			ag.Tools.RegisterReplacing(t)
		}
	}
	logger.InfoCF("agent", "system.* tools wired into agent registry",
		map[string]any{"tool_count": len(sysToolList)})
}
