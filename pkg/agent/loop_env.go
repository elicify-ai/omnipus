package agent

import (
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/agent/envcontext"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// wireEnvProviders injects an envcontext.DefaultProvider into every registered
// agent's ContextBuilder and registers each ContextBuilder in the loop's
// ContextBuilderRegistry for config-change invalidation (FR-057, FR-061).
// It also wires the per-turn delegation injector for each agent (see
// wireDelegationInjectors for the per-turn freshness guarantee).
//
// This is called at the end of NewAgentLoop, after the sandbox backend has
// been selected, so NewDefaultProvider receives the live backend reference.
// On hot-reload, ReloadProviderAndConfig calls wireDelegationInjectors directly
// on the new registry (after wireEnvProviders is only called once at boot).
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
	wireDelegationInjectors(al, registry)
}

// wireDelegationInjectors installs a delegation-context callback on every
// agent's ContextBuilder in registry. The callback is invoked on each turn from
// buildDynamicContext (the UN-CACHED path) so a runtime change to the delegation
// policy — which happens via TriggerReload → full registry rebuild followed by
// this call — appears on the agent's VERY NEXT turn.
//
// Must NOT be called while holding al.mu: the closures it installs call
// al.GetRegistry() and al.GetConfig(), both of which acquire al.mu.RLock.
// wireDelegationInjectors operates on the new/unpublished registry before
// the atomic swap in ReloadProviderAndConfig; that is safe because no other
// goroutine holds a reference to registry yet.
//
// Per-turn freshness guarantee: each closure captures al (the AgentLoop
// pointer) and agentID. On every invocation it calls al.GetRegistry() and
// al.GetConfig() to fetch the CURRENT live config and registry (which may
// have been swapped since the closure was created). The effective delegation
// policy is resolved the same way the enforcement gate does (loop.go:1588),
// via config.ResolveDelegationTo(agentCfg, cfg.Agents.Defaults), so the
// context block always matches what the gate will allow.
func wireDelegationInjectors(al *AgentLoop, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInst, ok := registry.GetAgent(agentID)
		if !ok || agentInst == nil || agentInst.ContextBuilder == nil {
			continue
		}

		// Capture by value so the closure refers to this specific agentID string.
		id := agentID
		agentInst.ContextBuilder.WithDelegationInjector(func() string {
			// Read the CURRENT live registry and config — not the ones captured
			// at wire time. After a hot-reload, al.registry and al.cfg are the
			// new values built from the updated config.
			liveRegistry := al.GetRegistry()
			if liveRegistry == nil {
				logger.WarnCF("agent.env",
					"wireDelegationInjectors: registry is nil in delegation injector — invariant break",
					map[string]any{"error_id": "DW-001", "agent_id": id})
				return ""
			}
			_, exists := liveRegistry.GetAgent(id)
			if !exists {
				logger.WarnCF("agent.env",
					"wireDelegationInjectors: agent not found in live registry — invariant break",
					map[string]any{"error_id": "DW-002", "agent_id": id})
				return ""
			}

			// Resolve the EFFECTIVE policy the same way the enforcement gate does
			// (loop.go:1588). ResolveDelegationTo consults:
			//   1. agentCfg.DelegationPolicy.To   (canonical per-agent policy)
			//   2. agentCfg.CanDelegateTo          (legacy per-agent allowlist)
			//   3. defaults.DelegationPolicy.To    (global default canonical policy)
			//   4. defaults.CanDelegateTo           (global default legacy allowlist)
			// Returns nil only when none of the above is set.
			liveCfg := al.GetConfig()
			agentCfg := findAgentConfig(liveCfg, id)
			toList := config.ResolveDelegationTo(agentCfg, liveCfg.Agents.Defaults)

			if toList == nil {
				// Final fallback: Subagents.AllowAgents (legacy spawn-tool path).
				// Mirror the CanSpawnSubagent logic (registry.go:155-168) so the
				// context block is never "cannot delegate" when the legacy allowlist
				// permits delegation.
				if agentCfg != nil && agentCfg.Subagents != nil && len(agentCfg.Subagents.AllowAgents) > 0 {
					refs := make([]config.AgentRef, len(agentCfg.Subagents.AllowAgents))
					for i, aid := range agentCfg.Subagents.AllowAgents {
						refs[i] = config.AgentRef{Kind: "local", ID: aid}
					}
					toList = refs
				}
			}

			// Construct the effective policy for rendering. Modes and Depth come
			// from the canonical policy (ResolveDelegationPolicy); the To list is
			// the fully resolved one from above (covers legacy paths too).
			effectivePolicy := config.ResolveDelegationPolicy(agentCfg, liveCfg.Agents.Defaults)
			var renderPolicy *config.DelegationPolicy
			if toList != nil {
				if effectivePolicy != nil {
					// Clone so we don't mutate the shared config struct.
					clone := *effectivePolicy
					clone.To = toList
					renderPolicy = &clone
				} else {
					renderPolicy = &config.DelegationPolicy{To: toList}
				}
			}
			// If toList is still nil, renderPolicy stays nil → "cannot delegate".

			return buildDelegationContext(renderPolicy, func(targetID string) (string, bool) {
				return resolveDelegationLabel(liveRegistry, targetID)
			})
		})
	}
}

// resolveDelegationLabel looks up an agent in registry and returns a human
// label suitable for the Delegation block, e.g. "Ava — Builder".
// Fallback order: inst.Name (which already encodes the core name + role for
// core agents) → agentID. Core-agent Subtitle is appended only when the Name
// does not already contain it (avoids the duplicate "Ava — Builder (Builder)").
// Returns ok=false when the agent is not registered (unknown or not yet available).
func resolveDelegationLabel(registry *AgentRegistry, agentID string) (string, bool) {
	inst, ok := registry.GetAgent(agentID)
	if !ok || inst == nil {
		return "", false
	}
	name := inst.Name
	if name == "" {
		name = agentID
	}
	// Augment with core-agent Subtitle only when Name does not already contain it
	// (e.g. "Ava — Builder" already encodes "Builder"; appending again is redundant
	// and produces "Ava — Builder (Builder)"). For agents whose Name is just a bare
	// first name (e.g. custom agents named "Ava"), the Subtitle adds useful context.
	if ca := coreagent.ByID(coreagent.CoreAgentID(agentID)); ca != nil && ca.Subtitle != "" {
		if !strings.Contains(name, ca.Subtitle) {
			return name + " (" + ca.Subtitle + ")", true
		}
	}
	return name, true
}
