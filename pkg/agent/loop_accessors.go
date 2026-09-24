// loop_accessors.go: Thin accessors for the loop's fields and dependencies

package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

// ContextBuilderRegistry returns the registry used to broadcast system-prompt
// cache invalidation when operator config changes (FR-061). Always non-nil
// after NewAgentLoop.
func (al *AgentLoop) ContextBuilderRegistry() *ContextBuilderRegistry {
	if al == nil {
		return nil
	}
	return al.contextBuilderRegistry
}

// AuditLogger returns the audit logger, or nil if audit logging is disabled.
// Used by gateway handlers that need to log policy changes.
func (al *AgentLoop) AuditLogger() *audit.Logger {
	if al == nil {
		return nil
	}
	return al.auditLogger
}

// ExecProxy returns the SEC-28 SSRF proxy for exec child processes, or nil
// when the proxy is disabled or failed to bind. Used by gateway handlers that
// report the proxy status and by tests that exercise the proxy lifecycle.
func (al *AgentLoop) ExecProxy() *security.ExecProxy {
	if al == nil {
		return nil
	}
	return al.execProxy
}

// PromptGuard returns the SEC-25 prompt-injection guard. Always non-nil after
// NewAgentLoop — even when no config field is set, the factory returns a
// medium-strictness guard. Used by runTurn and by gateway status handlers.
func (al *AgentLoop) PromptGuard() *security.PromptGuard {
	if al == nil {
		return nil
	}
	return al.promptGuard
}

// RateLimiter returns the SEC-26 rate limiter registry. Always non-nil after
// NewAgentLoop. Used by runTurn for per-agent limit checks and by gateway
// handlers that report the current rate limit / cost status.
func (al *AgentLoop) RateLimiter() *security.RateLimiterRegistry {
	if al == nil {
		return nil
	}
	return al.rateLimiter
}

// ApprovalGrants returns the session-scoped "Always Allow" tool-approval
// grant store. Always non-nil after NewAgentLoop (a nil AgentLoop returns
// nil). Every ApprovalGrantStore method is itself nil-receiver-safe and
// fails closed (IsAllowed => false, i.e. "ask"), so callers never need an
// extra nil check before chaining a call onto this accessor's result.
func (al *AgentLoop) ApprovalGrants() *security.ApprovalGrantStore {
	if al == nil {
		return nil
	}
	return al.approvalGrants
}

// SessionModes returns the ADR-092 per-chat Auto-approve modifier store
// written by the gateway's session_mode_update WS handler. Always non-nil
// after NewAgentLoop (a nil AgentLoop returns nil; every SessionModeStore
// method is nil-receiver-safe).
func (al *AgentLoop) SessionModes() *SessionModeStore {
	if al == nil {
		return nil
	}
	return al.sessionModes
}

// SessionAutoApprove reports the resolved Auto-approve setting for
// sessionID's chat driven by agentID: the global default, the agent's
// off-switch, then the chat's own modifier (ResolveAutoApprove). This is the
// value the session_mode_updated frame reports; it does not fold in whether
// a kernel sandbox is active (the SPA combines it with
// SandboxStatus.kernel_sandbox_active).
func (al *AgentLoop) SessionAutoApprove(agentID, sessionID string) bool {
	if al == nil {
		return false
	}
	var chat *bool
	if v, ok := al.sessionModes.Get(sessionID); ok {
		chat = &v
	}
	return ResolveAutoApprove(al.GetConfig(), agentID, chat)
}

// SandboxBackend returns the active sandbox backend, or nil if sandboxing is
// disabled. Used by gateway handlers that report sandbox status.
func (al *AgentLoop) SandboxBackend() sandbox.SandboxBackend {
	if al == nil {
		return nil
	}
	return al.sandboxBackend
}

// SetAppliedSandboxMode stores the mode that the kernel sandbox actually applied
// at boot (from SandboxApplyResult.Mode). Must be called from the gateway boot
// path after applySandbox returns successfully, before WireTier13Deps and
// wireExecToolDeps run, so that ExecToolDeps.SandboxMode reflects the true
// runtime enforcement level rather than the config file value.
func (al *AgentLoop) SetAppliedSandboxMode(mode sandbox.Mode) {
	if al == nil {
		return
	}
	al.appliedSandboxMode = mode
}

// SetSandboxBackend replaces the active sandbox backend and re-runs
// wireEnvProviders so every agent's environment-context provider observes the
// new object. Call from the gateway boot path only, immediately after
// applySandbox returns, where no HTTP listener or turn is running yet —
// degradeAfterLandlockFailure swaps the backend, and this threads that swap
// into both the sandbox-status handler and each agent's system preamble.
func (al *AgentLoop) SetSandboxBackend(backend sandbox.SandboxBackend) {
	if al == nil || backend == nil {
		return
	}
	al.sandboxBackend = backend
	cfg := al.GetConfig()
	registry := al.GetRegistry()
	if cfg != nil && registry != nil {
		al.wireEnvProviders(cfg, registry)
	}
}

// RegisterTool installs tool into every currently-registered agent's tool
// registry. It is exported test/instrumentation surface only (58 call sites,
// all in _test.go files as of this writing) — the standard pattern is a test
// building a real AgentLoop (whose normal boot wiring, e.g. wireExecToolDeps,
// already registers a real "bash" tool) and then calling RegisterTool with a
// wrapper/capturing/scripted double under the SAME name to observe or
// control behavior (e.g. bash_async_completion_test.go's sessionCapturingBash
// wrapping the real ExecTool). This is a deliberate, intentional override —
// not a hijack — so it uses RegisterReplacing, not Register: issue #278's
// collision-rejection in Register/RegisterHidden exists to stop an
// MCP-supplied tool from silently squatting on a trusted name (see
// pkg/agent/loop_mcp.go's registerServerTools, the only caller that ever
// registers untrusted/MCP-origin tools); this method is never called with an
// MCP-origin tool, so collision rejection here would only ever block the
// test/instrumentation override it exists to perform.
func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	registry := al.GetRegistry()
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			agent.Tools.RegisterReplacing(tool)
		}
	}
}

func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.channelManagerMu.Lock()
	al.channelManager = cm
	al.channelManagerMu.Unlock()
}

// getChannelManager returns the current channel manager under the read lock.
// Internal callers on the hot turn path use this to avoid the N2 data race.
func (al *AgentLoop) getChannelManager() *channels.Manager {
	al.channelManagerMu.RLock()
	cm := al.channelManager
	al.channelManagerMu.RUnlock()
	return cm
}

// GetChannelManager returns the current channel manager under the read lock
// (may be nil before channels start, e.g. during onboarding). Exported for
// pkg/gateway: REST handlers inspect runtime channel state (e.g. FailedChannels),
// and the scheduled runner validates that a deliver=true target channel is
// registered before publishing (M2). Set after construction via
// SetChannelManager, so callers must tolerate nil and re-fetch at use time.
func (al *AgentLoop) GetChannelManager() *channels.Manager {
	return al.getChannelManager()
}

// GetRegistry returns the current registry (thread-safe)
func (al *AgentLoop) GetRegistry() *AgentRegistry {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.registry
}

// GetConfig returns the current config (thread-safe)
// contextSettings returns the live ADR-066 ContextSettings (caps, trigger,
// ingest bound) — read per call, so a settings write applies to the next
// tool result without a restart (US-3.AC11). Satisfies the
// contextSettingsSource the recall_conversation tool type-asserts.
func (al *AgentLoop) contextSettings() config.ContextSettings {
	if cfg := al.GetConfig(); cfg != nil {
		return cfg.Context
	}
	return config.ContextSettings{}
}

func (al *AgentLoop) GetConfig() *config.Config {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.cfg
}

// GetSessionActiveAgent returns the agent that the handoff tool last switched
// the given session to. Returns ("", false) if no handoff override is active
// for this session_id.
func (al *AgentLoop) GetSessionActiveAgent(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	if v, ok := al.sessionActiveAgent.Load("session:" + sessionID); ok {
		s, ok := v.(string)
		if !ok {
			logger.ErrorCF("agent", "sessionActiveAgent: invariant violated — unexpected value type",
				map[string]any{"session_id": sessionID, "got_type": fmt.Sprintf("%T", v)})
			return "", false
		}
		return s, true
	}
	return "", false
}

// GetLastSwitchToDefault returns whether the most recent switch_agent call
// on the given session was a return-to-default (true) or a named-agent
// hand-off (false), as reported by the tool itself
// (tools.HandoffEvent.ToDefault) rather than re-derived from the resulting
// agent id. Returns (false, false) if no such record is pending — e.g. no
// switch_agent has run yet for this session, or it has already been
// consumed.
//
// One-shot: this LoadAndDeletes the entry, since it exists only to answer
// "was the switch that just completed a return-to-default" once, at the WS
// agent_switched frame builder that reads it right after the matching
// ToolExecEnd event fires. Leaving stale entries around risks a later,
// unrelated switch_agent call on the same session silently reusing a value
// it never itself observed.
func (al *AgentLoop) GetLastSwitchToDefault(sessionID string) (bool, bool) {
	if sessionID == "" {
		return false, false
	}
	v, ok := al.lastSwitchToDefault.LoadAndDelete("session:" + sessionID)
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	if !ok {
		logger.ErrorCF("agent", "lastSwitchToDefault: invariant violated — unexpected value type",
			map[string]any{"session_id": sessionID, "got_type": fmt.Sprintf("%T", v)})
		return false, false
	}
	return b, true
}

// SetPlanEngine installs the single hybrid plan-coordinator instance
// (ADR-049 D4) so command handlers and REST handlers can reach its Admit/
// Release admission authority and PausePlansOwnedBy/ResumePlansOwnedBy/
// HasActivePlansOwnedBy owner-lifecycle hooks. Called once at boot by the
// gateway (setupAndStartServices), before any /goal or /loop admission can
// occur. Idempotent.
func (al *AgentLoop) SetPlanEngine(pe *PlanEngine) {
	al.mu.Lock()
	al.planEngine = pe
	al.mu.Unlock()
}

// GetPlanEngine returns the installed PlanEngine (may be nil in tests or
// before boot wiring completes — Wave 2-C2's /goal and /loop admission paths
// MUST nil-check before calling Admit). Mirrors GetTaskStore/GetTaskExecutor's
// free-function-with-al-parameter convention.
func GetPlanEngine(al *AgentLoop) *PlanEngine {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.planEngine
}

// SetPlanStore installs the shared *plan.Store (ADR-052) so the
// create_plan/execute_plan agent tools can read/write it, and re-wires the
// full plan/task tool surface (create_plan, execute_plan, run_task,
// inspect_session) for every CURRENTLY-registered agent with the real
// store — mirrors SetPlanEngine's late-binding discipline exactly. Called
// once at boot by the gateway (setupAndStartServices), right where
// plan.New(...) constructs the store — see gateway.go's boot wiring
// region. Idempotent; safe to call again on hot-reload (registerSharedTools
// already re-runs wirePlanToolsForAgent on every reload, so this mainly
// matters for the initial boot gap between NewAgentLoop and
// setupAndStartServices).
func (al *AgentLoop) SetPlanStore(store *plan.Store) {
	al.mu.Lock()
	al.planStore = store
	al.mu.Unlock()

	if store == nil {
		logger.ErrorCF("agent", "SetPlanStore: installed a nil plan store — "+
			"create_plan/execute_plan will remain fail-closed", nil)
		return
	}

	reg := al.GetRegistry()
	if reg == nil {
		logger.ErrorCF("agent", "SetPlanStore: no agent registry available — "+
			"plan tool surface not re-wired", nil)
		return
	}
	for _, agentID := range reg.ListAgentIDs() {
		inst, ok := reg.GetAgent(agentID)
		if !ok || inst == nil {
			continue
		}
		al.wirePlanToolsForAgent(inst, store)

		// create_task is NOT part of the wirePlanToolsForAgent surface (that
		// function's own doc comment enumerates create_plan/execute_plan/
		// run_task/inspect_session/plan_correct/stop_plan only) — it is
		// constructed separately in registerSharedTools's "Task tools" block.
		// Re-wire its plan store here too, on the same late-binding pass, so
		// create_task(plan_id=...) stops failing closed with "plan store is
		// not configured" once the real store exists. A missing or wrong-typed
		// tool is not an error here — task tools are gated behind
		// al.taskStore != nil in registerSharedTools, so an agent with no task
		// store never registered create_task at all.
		if inst.Tools == nil {
			continue
		}
		if raw, ok := inst.Tools.Get("create_task"); ok {
			if taskCreate, ok := raw.(*tools.TaskCreateTool); ok {
				taskCreate.SetPlanStore(store)
			}
		}
	}

	// UAT fix (fix/uat-defects-2026-08-22): re-wire the system.* tool surface
	// (create_task_in_workspace, pkg/sysagent/tools) with the real plan store
	// too. WireSysagentDeps runs at boot BEFORE this store exists — the
	// gateway constructs sysAgentDeps and calls WireSysagentDeps well ahead
	// of plan.New/SetPlanStore (see gateway.go's boot wiring region) — so
	// every system.* tool instance registered by then was built with a nil
	// deps.PlanStore. Without this, create_task_in_workspace(plan_id=...)
	// fails closed with "plan store is not configured" FOREVER, for every
	// agent, even against a plan that was just created in the very same
	// workspace by the very same turn (the plain create_task tool above was
	// already re-wired here; the system.* twin was not).
	//
	// al.sysagentDeps is read-modify-written under al.mu (mirrors the
	// al.planStore guard a few lines up in this same function) because the
	// gateway listener is already live by the time this runs (boot wires
	// sysAgentDeps and starts serving well before constructing planStore),
	// so a concurrent hot-reload's ReloadProviderAndConfig could in
	// principle race the field. wireSysagentDepsLocked itself is called
	// OUTSIDE the lock — it does not touch al.mu, and holding al.mu across
	// it would only widen the critical section for no benefit (mirrors the
	// wirePlanToolsForAgent loop above, which does the same).
	al.mu.Lock()
	var sysDeps *systools.Deps
	if al.sysagentDeps != nil {
		depsCopy := *al.sysagentDeps
		depsCopy.PlanStore = store
		al.sysagentDeps = &depsCopy
		sysDeps = al.sysagentDeps
	}
	al.mu.Unlock()
	if sysDeps != nil {
		al.wireSysagentDepsLocked(reg, sysDeps)
	}
}

// GetPlanStore returns the installed plan.Store (may be nil in tests or
// before boot wiring completes). Mirrors GetTaskStore/GetPlanEngine's
// free-function-with-al-parameter convention is intentionally NOT followed
// here since every other Set/Get pair on AgentLoop that is read from
// pkg/tools construction sites (SetMediaStore/GetMediaStore) uses the
// method form; kept consistent with that sibling pair.
func (al *AgentLoop) GetPlanStore() *plan.Store {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.planStore
}

// validatePlanOwnerAgentForTool mirrors pkg/gateway/rest_plans.go's
// validatePlanOwnerAgent EXACTLY (same rule, same error text shape) for
// create_plan's SetOwnerValidator seam. pkg/agent cannot import pkg/gateway
// (gateway already imports agent — that would be a cycle), so this is a
// same-behavior local copy consumed only here. Rejects an owner_agent_id
// that is not a registered agent, or that IS registered but is a System
// Agent or worker — OwnerAgentID's contract ("the agent woken at plan
// decision points", ADR-049 D4) requires a real, addressable agent.
func validatePlanOwnerAgentForTool(cfg *config.Config, ownerAgentID string) error {
	if cfg == nil {
		return fmt.Errorf("owner_agent_id %q is not a registered agent", ownerAgentID)
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != ownerAgentID {
			continue
		}
		if !cfg.Agents.List[i].IsChatTarget() {
			return fmt.Errorf(
				"owner_agent_id %q is a System Agent or worker and cannot own a plan", ownerAgentID)
		}
		return nil
	}
	return fmt.Errorf("owner_agent_id %q is not a registered agent", ownerAgentID)
}

// isRegisteredAgentID reports whether id resolves to a known agent in the
// live registry — mirrors pkg/gateway/rest_plans.go's restAPI.isAgentID
// exactly, and drives execute_plan's SD-A7 tiered-DoD gate via
// SetIsAgentIDChecker (a plan whose CreatedBy resolves to an agent — strict
// tier — must carry >=1 DoD criterion).
func (al *AgentLoop) isRegisteredAgentID(id string) bool {
	if id == "" {
		return false
	}
	reg := al.GetRegistry()
	if reg == nil {
		return false
	}
	_, ok := reg.GetAgent(id)
	return ok
}

// GetSSRFChecker returns the singleton SSRFChecker built from the SSRF policy
// config at startup (SEC-24). Returns nil when SSRF protection is disabled
// (sandbox.ssrf.enabled = false in config.json). Gateway handlers that make
// outbound HTTP calls (e.g. the skills installer) should pass this to their
// HTTP client constructors so allow_internal is honored consistently.
func GetSSRFChecker(al *AgentLoop) *security.SSRFChecker {
	return al.ssrfChecker
}

// SetMediaStore injects a MediaStore for media lifecycle management.
func (al *AgentLoop) SetMediaStore(s media.MediaStore) {
	al.mediaStoreMu.Lock()
	al.mediaStore = s
	al.mediaStoreMu.Unlock()

	// Propagate store to all registered tools that can emit media.
	registry := al.GetRegistry()
	for _, agentID := range registry.ListAgentIDs() {
		if agent, ok := registry.GetAgent(agentID); ok {
			agent.Tools.SetMediaStore(s)
		}
	}
}

// GetMediaStore returns the currently injected media store. Callers that serve
// media over HTTP must use this getter (not a cached reference) because the
// store is replaced on every restartServices — a cached pointer goes stale.
func (al *AgentLoop) GetMediaStore() media.MediaStore {
	if al == nil {
		return nil
	}
	al.mediaStoreMu.RLock()
	s := al.mediaStore
	al.mediaStoreMu.RUnlock()
	return s
}

// GetMediaRefsDropped returns the cumulative count of media refs that were
// dropped because they could not be resolved (unknown ref or file missing
// on disk). Safe for concurrent access; incremented on the hot turn path.
func (al *AgentLoop) GetMediaRefsDropped() int64 {
	return al.mediaRefsDropped.Load()
}

// GetDriftDropped returns the cumulative count of bound-instance drift drops:
// inbound messages on a workspace-bound channel instance whose configured agent
// was unresolvable (deleted or a worker). Safe for concurrent access;
// incremented atomically in resolveMessageRoute (ADR-029 FR-028 / MAJ-003).
func (al *AgentLoop) GetDriftDropped() int64 {
	return al.driftDropped.Load()
}

// SetTranscriber injects a voice transcriber for agent-level audio transcription.
func (al *AgentLoop) SetTranscriber(t voice.Transcriber) {
	al.transcriber = t
}

// GetTranscriber returns the currently configured voice transcriber, or nil
// when none is configured. The gateway's POST /voice/transcribe handler uses
// this to serve the composer-mic flow with the same transcriber the agent loop
// uses for inbound audio messages (Spec-6 FR-12.1).
func (al *AgentLoop) GetTranscriber() voice.Transcriber {
	return al.transcriber
}

// RecordLastChannel records the last active channel for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChannel(channel string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChannel(channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChatID(chatID string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChatID(chatID)
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]any {
	info := make(map[string]any)

	registry := al.GetRegistry()
	// Tools and skills are install-wide facts that every agent sees the same
	// way — skills in particular load from ONE global directory
	// ($OMNIPUS_HOME/skills; see globalSkillsDir). Reading them "through the
	// default agent" was only ever a convenient handle, and it silently became
	// a dependency on a default EXISTING once the "main" sentinel was removed
	// (ADR-064): with no default, this returned an empty map, which made
	// restAPI.installedSkillIDs empty, which made validateSkillIDs skip
	// validation entirely and ACCEPT unknown skill ids. A fail-open reached
	// through three layers of indirection.
	//
	// Any agent answers these questions identically, so ask any.
	agent := registry.GetDefaultAgent()
	if agent == nil {
		for _, id := range registry.ListAgentIDs() {
			if ag, ok := registry.GetAgent(id); ok && ag != nil {
				agent = ag
				break
			}
		}
	}
	if agent == nil {
		return info
	}

	// Tools info
	toolsList := agent.Tools.List()
	info["tools"] = map[string]any{
		"count": len(toolsList),
		"names": toolsList,
	}

	// Skills info
	info["skills"] = agent.ContextBuilder.GetSkillsInfo()

	// Agents info
	info["agents"] = map[string]any{
		"count": len(registry.ListAgentIDs()),
		"ids":   registry.ListAgentIDs(),
	}

	return info
}

// ListSkillsDetailed returns the full per-skill metadata (name, source,
// description, author, version) for every installed skill, sourced from the
// default agent's ContextBuilder skills loader — the same loader that feeds
// GetStartupInfo's skills summary. This is the data path GET /api/v1/skills uses
// to enrich its response beyond bare names. Returns nil when there is no default
// agent or its ContextBuilder is unset (e.g. an uninitialized loop).
func (al *AgentLoop) ListSkillsDetailed() []skills.SkillInfo {
	registry := al.GetRegistry()
	if registry == nil {
		return nil
	}
	agent := registry.GetDefaultAgent()
	if agent == nil || agent.ContextBuilder == nil {
		return nil
	}
	return agent.ContextBuilder.ListSkillsDetailed()
}

// ChannelOwnership returns the stored resolver, or nil before wiring.
func (al *AgentLoop) ChannelOwnership() tools.ChannelOwnership {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.channelOwnership
}

// SetChannelOwnership installs the channel-ownership resolver on every
// registered agent's send_message tool (ADR-065).
//
// It is injected AFTER construction, following the SetPlanStore precedent,
// because the resolver reads live gateway config and pkg/agent cannot import
// pkg/gateway without a cycle. Until it runs, send_message has no ownership
// information: it will reply into the turn's own conversation and REFUSE any
// other target rather than assume the send is allowed. That is deliberate —
// an unresolvable ownership question must not read as permission.
func (al *AgentLoop) SetChannelOwnership(o tools.ChannelOwnership) {
	// STORE it, not just push it. registerSharedTools builds a FRESH
	// MessageTool per agent on every reload — and it re-runs on agent
	// create/update, tool-policy writes, god-mode toggles and mailbox changes
	// via ReloadProviderAndConfig and UpsertAgentFast. Pushing only into the
	// instances that exist right now meant every one of those silently reset
	// ownership to nil for the rest of the process lifetime. Because the tool
	// is fail-closed that degraded into "refuses every target except the
	// turn's own conversation" rather than a hole, but it was permanent and
	// silent. This mirrors what SetPlanStore actually does: al.planStore is
	// stored and re-read at registration.
	al.mu.Lock()
	al.channelOwnership = o
	al.mu.Unlock()

	if o == nil {
		logger.ErrorCF("agent", "SetChannelOwnership: installed a nil resolver — "+
			"send_message will refuse every target except the turn's own conversation", nil)
		return
	}
	reg := al.GetRegistry()
	if reg == nil {
		logger.WarnCF("agent", "SetChannelOwnership: no registry yet; ownership not installed", nil)
		return
	}
	installed := 0
	for _, id := range reg.ListAgentIDs() {
		ag, ok := reg.GetAgent(id)
		if !ok || ag == nil || ag.Tools == nil {
			continue
		}
		t, found := ag.Tools.Get("send_message")
		if !found {
			continue
		}
		mt, isMessageTool := t.(*tools.MessageTool)
		if !isMessageTool {
			continue
		}
		mt.SetChannelOwnership(o)
		installed++
	}
	logger.InfoCF("agent", "channel ownership installed on send_message",
		map[string]any{"agents": installed})
}
