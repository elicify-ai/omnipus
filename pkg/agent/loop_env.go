package agent

import (
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/agent/envcontext"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// wireEnvProviders injects an envcontext.DefaultProvider into every registered
// agent's ContextBuilder and registers each ContextBuilder in the loop's
// ContextBuilderRegistry for config-change invalidation (FR-057, FR-061).
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
}
