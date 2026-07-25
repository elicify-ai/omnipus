package agent

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// RegistryNotifier adapts an AgentRegistry into the shape
// pkg/agentstore.Notifier expects (AgentUpserted(id, *config.AgentConfig),
// AgentDeleted(id)), so a pkg/agentstore write — already durably verified by
// the time the notifier fires, see agentstore.Store.Create/Update's doc
// comments — can construct a fresh AgentInstance and drop it into the
// registry via UpsertAgent/RemoveAgent (ADR-054 §5).
//
// pkg/agentstore cannot import pkg/agent (pkg/agent already depends on
// pkg/config, and pkg/agentstore depends on pkg/config too — importing
// pkg/agent from pkg/agentstore would cycle back), so agentstore.Notifier is
// defined structurally there and satisfied here instead. Wire it once at
// boot, after both the registry and the store exist:
//
//	notifier := &agent.RegistryNotifier{Registry: registry, Defaults: &cfg.Agents.Defaults, Config: cfg, Provider: provider}
//	agentStore.SetNotifier(notifier)
//
// Scope note: NewAgentInstance alone does not perform the extra shared-tool
// wiring (registerSharedTools, tier1/3 deps, sysagent deps, delegation
// injectors, the memory-audit-logger/rate-limiter re-wiring) that a full
// AgentLoop.ReloadProviderAndConfig performs for every agent on a config
// reload — those depend on runtime singletons that live on AgentLoop, not
// AgentRegistry, and are out of this wave's scope to duplicate per-agent.
// A newly-upserted agent therefore gets correct routing/read visibility
// immediately (GetAgent, ResolveRoute, GetDefaultAgent all observe it on the
// very next call, at zero extra disk I/O), but shared-tool wiring for it
// completes on the next full reload. This is an accepted, documented gap;
// closing it fully means teaching AgentLoop to re-wire a single instance,
// which touches loop.go's tier13Deps/sysagentDeps plumbing directly.
type RegistryNotifier struct {
	Registry *AgentRegistry
	Defaults *config.AgentDefaults
	Config   *config.Config
	Provider providers.LLMProvider
}

// AgentUpserted implements agentstore.Notifier.
func (n *RegistryNotifier) AgentUpserted(id string, cfg *config.AgentConfig) {
	if n == nil || n.Registry == nil || cfg == nil {
		return
	}
	instance := NewAgentInstance(cfg, n.Defaults, n.Config, n.Provider)
	// Mirrors NewAgentRegistry's own upgrade for runtime-seeded core agents
	// whose config may not have Type set yet.
	if instance.AgentType == "custom" && coreagent.IsCoreAgent(id) {
		instance.SetAgentType("core")
	}
	n.Registry.UpsertAgent(instance)
}

// AgentDeleted implements agentstore.Notifier.
func (n *RegistryNotifier) AgentDeleted(id string) {
	if n == nil || n.Registry == nil {
		return
	}
	n.Registry.RemoveAgent(id)
}
