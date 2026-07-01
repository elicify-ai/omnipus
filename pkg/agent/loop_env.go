package agent

import (
	"github.com/dapicom-ai/omnipus/pkg/agent/envcontext"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// wireEnvProviders injects an envcontext.DefaultProvider into every registered
// agent's ContextBuilder and registers each ContextBuilder in the loop's
// ContextBuilderRegistry for config-change invalidation (FR-057, FR-061).
// It also wires the per-turn delegation injector for each agent (see
// wireDelegationInjectorsLocked for the per-turn freshness guarantee).
//
// This is called at the end of NewAgentLoop, after the sandbox backend has
// been selected, so NewDefaultProvider receives the live backend reference.
//
// An agent without a ContextBuilder is a wiring bug — the resulting agent
// would never render an env preamble and silently lie to the LLM about its
// surroundings. We WARN loudly rather than continue silently.
func (al *AgentLoop) wireEnvProviders(cfg *config.Config, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInstance, ok := registry.GetAgent(agentID)
		if !ok || agentInstance == nil {
			logger.WarnCF("agent.env", "wireEnvProviders: agent missing from registry during wire",
				map[string]any{"agent_id": agentID})
			continue
		}
		cb := agentInstance.ContextBuilder
		if cb == nil {
			logger.WarnCF("agent.env", "wireEnvProviders: agent has nil ContextBuilder; env preamble will be absent",
				map[string]any{"agent_id": agentID})
			continue
		}

		provider := envcontext.NewDefaultProvider(cfg, al.sandboxBackend, agentInstance.Workspace)
		cb.WithEnvironmentProvider(provider)

		al.contextBuilderRegistry.Register(agentID, cb)
	}

	// Wire the per-turn delegation injector for every agent in this registry.
	wireDelegationInjectorsLocked(al, registry)
}

// wireDelegationInjectorsLocked installs a delegation-context callback on every
// agent's ContextBuilder in registry. The callback is invoked on each turn from
// buildDynamicContext (the UN-CACHED path) so a runtime change to the delegation
// policy — which happens via TriggerReload → full registry rebuild followed by
// this call — appears on the agent's VERY NEXT turn.
//
// Per-turn freshness guarantee: each closure captures al (the AgentLoop pointer)
// and agentID. On every invocation it calls al.GetRegistry() to fetch the CURRENT
// live registry (which may have been swapped by ReloadProviderAndConfig since the
// closure was created), then looks up the agent's current DelegationPolicy from
// the fresh instance. This means the delegation block always reflects the state
// of the live registry, not the registry that existed at closure-creation time.
//
// Note: AgentInstance.DelegationPolicy is set once at construction and never
// mutated thereafter. The runtime-refresh guarantee is achieved by reading from
// the CURRENT registry (al.GetRegistry()) on each call, not by mutating the field
// in place. A TriggerReload builds a new instance with the new policy and calls
// wireEnvProviders again — that re-installs a new closure pointing at the new
// instance, so subsequent turns see the updated policy.
func wireDelegationInjectorsLocked(al *AgentLoop, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInst, ok := registry.GetAgent(agentID)
		if !ok || agentInst == nil || agentInst.ContextBuilder == nil {
			continue
		}

		// Capture by value so the closure refers to this specific agentID string.
		id := agentID
		agentInst.ContextBuilder.WithDelegationInjector(func() string {
			// Read the CURRENT live registry — not the one captured at wire time.
			// After a hot-reload, al.registry is the new registry containing an
			// instance built from the updated config. This ensures the very next
			// turn after a policy change picks up the new DelegationPolicy.
			liveRegistry := al.GetRegistry()
			if liveRegistry == nil {
				return ""
			}
			inst, exists := liveRegistry.GetAgent(id)
			if !exists || inst == nil {
				return ""
			}
			return buildDelegationContext(inst.DelegationPolicy, func(targetID string) (string, bool) {
				return resolveDelegationLabel(liveRegistry, targetID)
			})
		})
	}
}

// resolveDelegationLabel looks up an agent in registry and returns a human
// label like "Ava (Builder)" suitable for the Delegation block. Falls back
// gracefully: core-agent Subtitle → agent Name → agentID. Returns ok=false
// when the agent is not registered (unknown or not yet available).
func resolveDelegationLabel(registry *AgentRegistry, agentID string) (string, bool) {
	inst, ok := registry.GetAgent(agentID)
	if !ok || inst == nil {
		return "", false
	}
	name := inst.Name
	if name == "" {
		name = agentID
	}
	// Augment with core-agent Subtitle when available (Mia→Assistant,
	// Jim→Planner & Orchestrator, Ava→Builder, Ray→Scout, etc.).
	if ca := coreagent.ByID(coreagent.CoreAgentID(agentID)); ca != nil && ca.Subtitle != "" {
		return name + " (" + ca.Subtitle + ")", true
	}
	return name, true
}
