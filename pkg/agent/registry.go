package agent

import (
	"sort"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/routing"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// DefaultAgentID is the registry key for the default agent. It is the internal
// identifier for the generic default agent instance used when no specific agent
// is targeted (e.g., unrouted channel messages).
const DefaultAgentID = "main"

// AgentRegistry manages multiple agent instances and routes messages to them.
type AgentRegistry struct {
	agents               map[string]*AgentInstance
	resolver             *routing.RouteResolver
	mu                   sync.RWMutex
	defaultAgentOverride string // from config.Agents.Defaults.DefaultAgentID
}

// SetDefaultAgentOverride sets the agent ID to use as the default agent.
// When set, GetDefaultAgent returns this agent instead of the "main" agent.
func (r *AgentRegistry) SetDefaultAgentOverride(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultAgentOverride = agentID
}

// NewAgentRegistry creates a registry from config, instantiating all agents.
func NewAgentRegistry(
	cfg *config.Config,
	provider providers.LLMProvider,
) *AgentRegistry {
	registry := &AgentRegistry{
		agents:   make(map[string]*AgentInstance),
		resolver: routing.NewRouteResolver(cfg),
	}

	// Always register the default/system agent. This handles messages that
	// don't target a specific custom agent (e.g., system agent in webchat,
	// unrouted channel messages). Uses the default workspace.
	// Note: Default is intentionally false here — this sentinel is found via the
	// DefaultAgentID fallback in GetDefaultAgent (priority 3), not via the
	// routing-default flag (priority 1). Setting it true would shadow any
	// per-agent Default=true flag (e.g. Mia's) and break F3.
	defaultAgent := &config.AgentConfig{
		ID:      DefaultAgentID,
		Default: false,
	}
	defaultInstance := NewAgentInstance(defaultAgent, &cfg.Agents.Defaults, cfg, provider)
	// The default "main" agent is a core agent (it runs system.* tools seeded by the registry).
	defaultInstance.SetAgentType("core")
	registry.agents[DefaultAgentID] = defaultInstance
	logger.InfoCF("agent", "Registered default agent (main)", map[string]any{
		"workspace": defaultInstance.Workspace,
		"model":     defaultInstance.Model,
	})

	// Register agents from config (core agents seeded by coreagent.SeedConfig are
	// stored in cfg.Agents.List alongside custom agents).
	// Protect the DefaultAgentID ("main") — a custom/core agent using that ID would
	// silently overwrite the generic default agent instance.
	reservedIDs := map[string]bool{
		DefaultAgentID: true,
	}
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		id := routing.NormalizeAgentID(ac.ID)
		if reservedIDs[id] {
			logger.ErrorCF("agent", "Custom agent uses reserved ID; skipping registration",
				map[string]any{"agent_id": id, "name": ac.Name})
			continue
		}
		instance := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg, provider)
		// Upgrade agent type for runtime-seeded core agents whose config may not
		// have Type field set (e.g., agents seeded before the Type field was introduced).
		if instance.AgentType == "custom" && coreagent.IsCoreAgent(id) {
			instance.SetAgentType("core")
		}
		registry.agents[id] = instance
		logger.InfoCF("agent", "Registered agent",
			map[string]any{
				"agent_id":  id,
				"name":      ac.Name,
				"workspace": instance.Workspace,
				"model":     instance.Model,
			})
	}

	return registry
}

// GetAgent returns the agent instance for a given ID.
func (r *AgentRegistry) GetAgent(agentID string) (*AgentInstance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id := routing.NormalizeAgentID(agentID)
	agent, ok := r.agents[id]
	return agent, ok
}

// ResolveRoute determines which agent handles the message.
func (r *AgentRegistry) ResolveRoute(input routing.RouteInput) routing.ResolvedRoute {
	return r.resolver.ResolveRoute(input)
}

// ListAgentIDs returns all registered agent IDs.
func (r *AgentRegistry) ListAgentIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	return ids
}

// CanSpawnSubagent checks if parentAgentID is allowed to spawn targetAgentID.
//
// FR-6.3 (Spec-3 keystone): consults the unified DelegationPolicy.To first
// when non-nil, falling back to the legacy SubagentsConfig.AllowAgents path.
// Deny-by-default is preserved in both paths.
func (r *AgentRegistry) CanSpawnSubagent(parentAgentID, targetAgentID string) bool {
	parent, ok := r.GetAgent(parentAgentID)
	if !ok {
		return false
	}

	// Unified DelegationPolicy.To takes precedence when set.
	if parent.DelegationPolicy != nil {
		targetNorm := routing.NormalizeAgentID(targetAgentID)
		for _, ref := range parent.DelegationPolicy.To {
			if ref.Kind != "local" && ref.Kind != "" {
				// remote-a2a and unknown kinds are not resolved locally in v0.1.0.
				continue
			}
			if ref.ID == "*" {
				return true
			}
			if routing.NormalizeAgentID(ref.ID) == targetNorm {
				return true
			}
		}
		// Policy was set explicitly — deny (empty To = deny all).
		return false
	}

	// Legacy fallback: SubagentsConfig.AllowAgents (deny when nil).
	if parent.Subagents == nil || parent.Subagents.AllowAgents == nil {
		return false
	}
	targetNorm := routing.NormalizeAgentID(targetAgentID)
	for _, allowed := range parent.Subagents.AllowAgents {
		if allowed == "*" {
			return true
		}
		if routing.NormalizeAgentID(allowed) == targetNorm {
			return true
		}
	}
	return false
}

// ForEachTool calls fn for every tool registered under the given name
// across all agents. This is useful for propagating dependencies (e.g.
// MediaStore) to tools after registry construction.
func (r *AgentRegistry) ForEachTool(name string, fn func(tools.Tool)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, agent := range r.agents {
		if t, ok := agent.Tools.Get(name); ok {
			fn(t)
		}
	}
}

// GetAgentName returns the display name for agentID and true if the agent
// exists in the registry. It satisfies the tools.AgentRegistryReader interface
// used by HandoffTool to avoid an import cycle.
func (r *AgentRegistry) GetAgentName(agentID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[agentID]
	if !ok {
		return "", false
	}
	name := agent.Name
	if name == "" {
		name = agentID
	}
	return name, true
}

// IsWorker reports whether the agent identified by agentID is a sub-agent worker
// (the delegation-only labour tier). Returns false when the agent does not exist,
// so callers that have already validated existence get a definitive worker/not-worker
// answer. Satisfies the tools.AgentRegistryReader interface used by HandoffTool to
// reject worker handoff targets without an import cycle.
func (r *AgentRegistry) IsWorker(agentID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.agents[agentID]
	if !ok {
		return false
	}
	return agent.IsWorker()
}

// Close releases resources held by all registered agents and clears the map (M9).
func (r *AgentRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, agent := range r.agents {
		if err := agent.Close(); err != nil {
			logger.WarnCF("agent", "Failed to close agent",
				map[string]any{"agent_id": agent.ID, "error": err.Error()})
		}
	}
	// Replace with an empty map rather than nil so post-Close reads on GetAgent,
	// ListAgentIDs, etc. behave safely (empty result) rather than panicking on
	// a nil map lookup.
	r.agents = make(map[string]*AgentInstance)
}

// GetDefaultAgent returns the default agent instance.
//
// Resolution order (canonical — matches channel routing's resolveDefaultAgentID):
//  1. An agent whose config has Default==true (the per-agent routing-default
//     flag set by SeedConfig / the Agents-screen "star"). Deterministic: if
//     multiple agents somehow carry Default==true (operator error — F11 repairs
//     this at boot), the one with the lexicographically smallest ID wins.
//  2. The configurable override from config.Agents.Defaults.DefaultAgentID,
//     when the named agent exists in the registry and is not a worker.
//  3. The built-in "main" sentinel agent, when it is not a worker.
//  4. The lexicographically first registered non-worker agent (deterministic
//     fallback, M10). Workers are never chat targets, so every priority skips them.
func (r *AgentRegistry) GetDefaultAgent() *AgentInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Priority 1: agent explicitly marked as the routing default.
	// Collect all matching IDs and sort for deterministic selection when
	// operator misconfiguration leaves multiple agents with Default==true.
	var defaultIDs []string
	for id, ag := range r.agents {
		if ag.IsRoutingDefault {
			defaultIDs = append(defaultIDs, id)
		}
	}
	if len(defaultIDs) > 0 {
		sort.Strings(defaultIDs)
		return r.agents[defaultIDs[0]]
	}

	// Priority 2: explicit override from config.Agents.Defaults.DefaultAgentID.
	// A worker is never a chat target, so a hand-edited override pointing at one
	// is skipped (defense in depth; consistent with the Priority-4 hardening).
	if r.defaultAgentOverride != "" {
		if agent, ok := r.agents[r.defaultAgentOverride]; ok && !agent.IsWorker() {
			return agent
		}
	}

	// Priority 3: the "main" built-in sentinel — unless it is somehow a worker
	// (degenerate/tampered config), in which case fall through to Priority 4.
	if agent, ok := r.agents[DefaultAgentID]; ok && !agent.IsWorker() {
		return agent
	}

	// Priority 4: lexicographically first registered agent (M10) — but never a
	// worker. Workers are not chat targets and must not be resolved as the
	// default even in the last-resort fallback. Prefer the first non-worker; only
	// if EVERY registered agent is a worker do we fall back to the first overall
	// (degenerate config — better to return something than nil so callers that
	// require a default don't panic).
	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	for _, id := range ids {
		if ag := r.agents[id]; ag != nil && !ag.IsWorker() {
			return ag
		}
	}
	return r.agents[ids[0]]
}
