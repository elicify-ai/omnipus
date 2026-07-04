package agent

import (
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/agent/envcontext"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/workspace"
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
// buildDynamicContext (the UN-CACHED path), receives the turn's workspaceID, and
// reads the workspace delegation graph (workspaces/<id>.json) to render the
// "## Delegation" block — matching the SAME authority the enforcement gate uses.
//
// Advertisement == enforcement by construction: both the injector and
// buildDelegationDenyChecker call workspace.ReadDelegation for the same wsID,
// so what is advertised is exactly what the gate allows.
//
// Must NOT be called while holding al.mu: the closures it installs call
// al.GetRegistry() and al.GetConfig(), both of which acquire al.mu.RLock.
// wireDelegationInjectors operates on the new/unpublished registry before
// the atomic swap in ReloadProviderAndConfig; that is safe because no other
// goroutine holds a reference to registry yet.
//
// Per-turn freshness guarantee: the graph is read on every injector call
// (workspace.ReadDelegation is not cached), so a graph change (e.g. a PUT to
// /workspaces/:id/delegation) takes effect on the very next turn with no agent
// reload.
func wireDelegationInjectors(al *AgentLoop, registry *AgentRegistry) {
	for _, agentID := range registry.ListAgentIDs() {
		agentInst, ok := registry.GetAgent(agentID)
		if !ok || agentInst == nil || agentInst.ContextBuilder == nil {
			continue
		}

		// Capture by value so the closure refers to this specific agentID string.
		id := agentID
		agentInst.ContextBuilder.WithDelegationInjector(func(workspaceID string) string {
			// DW-001: read the CURRENT live registry — not the one captured at wire time.
			// After a hot-reload, al.registry is the new value.
			liveRegistry := al.GetRegistry()
			if liveRegistry == nil {
				logger.WarnCF("agent.env",
					"wireDelegationInjectors: registry is nil in delegation injector — invariant break",
					map[string]any{"error_id": "DW-001", "agent_id": id})
				return ""
			}
			// DW-002: the agent must still be present in the live registry.
			_, exists := liveRegistry.GetAgent(id)
			if !exists {
				logger.WarnCF("agent.env",
					"wireDelegationInjectors: agent not found in live registry — invariant break",
					map[string]any{"error_id": "DW-002", "agent_id": id})
				return ""
			}

			// Resolve the effective workspace, mirroring resolveEffectiveWorkspaceID
			// in loop.go exactly:
			//   1. use the turn's bound workspaceID when non-empty;
			//   2. fall back to the is_default workspace;
			//   3. fail-closed (render cannot-delegate) when neither resolves.
			wsID := workspaceID
			if wsID == "" {
				def, err := workspace.ResolveDefaultID(omnipusHome())
				if err != nil || def == "" {
					logger.WarnCF("agent.env",
						"wireDelegationInjectors: cannot resolve default workspace — rendering fail-closed delegation block",
						map[string]any{"agent_id": id, "error": errString(err)})
					return "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
				}
				wsID = def
			}

			// Read the workspace delegation graph — SAME call the enforcement gate makes.
			// Fail-closed on any error: an unreadable graph DENIES (matching the gate).
			edges, err := workspace.ReadDelegation(omnipusHome(), wsID)
			if err != nil {
				logger.WarnCF("agent.env",
					"wireDelegationInjectors: workspace delegation graph unreadable — rendering fail-closed delegation block",
					map[string]any{"agent_id": id, "workspace_id": wsID, "error": err.Error()})
				return "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
			}

			// Filter to outgoing edges from this agent.
			liveCfg := al.GetConfig()
			// #477 / FR-D9: advertise the EFFECTIVE cap (resolved via the SAME
			// shared function enforceEdgeModeAndDepth and spawnSubTurn's own
			// depth check use), not the raw config value — so this footer never
			// again says "uncapped" when the spawn-time backstop will actually
			// reject a hop at the resolved default. edgeDepth is nil here: this is
			// the GLOBAL-only footer number; a stricter per-edge override (when
			// one exists for a specific target) is enforced separately at
			// spawn-time via SubTurnConfig.ResolvedMaxDepth and does not change
			// this general-roster footer.
			globalDepthCap := resolveEffectiveDelegationDepth(nil, liveCfg.Agents.Defaults.SubTurn.MaxDepth)

			var targets []delegationTarget
			for _, e := range edges {
				if e.FromAgent != id {
					continue
				}
				label, ok := resolveDelegationLabel(liveRegistry, e.ToAgent)
				if !ok {
					// A graph edge points at an agent absent from the live registry
					// (deleted/renamed target, or a hand-authored graph inconsistency).
					// Skip it — advertising a target the model can't name is worse —
					// but WARN so the operator can reconcile the graph. The gate would
					// independently deny this target too, so under-advertising here is
					// fail-safe (advertise ⊆ enforce).
					logger.WarnCF("agent.env",
						"wireDelegationInjectors: graph delegation edge target not in live registry — skipping from advertised block",
						map[string]any{"agent_id": id, "target": e.ToAgent, "workspace_id": wsID})
					continue
				}
				// Convert workspace.DelegationMode to config.DelegationMode (same string
				// values, typed separately to avoid import cycle in pkg/workspace).
				modes := make([]config.DelegationMode, len(e.Modes))
				for i, m := range e.Modes {
					modes[i] = config.DelegationMode(m)
				}
				targets = append(targets, delegationTarget{
					ID:    e.ToAgent,
					Label: label,
					Modes: modes,
					Depth: e.Depth,
				})
			}

			return buildDelegationContext(targets, globalDepthCap)
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
