package agent

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// registerInstance is a small test helper that wires a fully-formed AgentInstance
// (with workspace + context builder) into the loop's registry under id.
func registerInstance(t *testing.T, al *AgentLoop, cfg *config.Config, home, id string, typ config.AgentType) {
	t.Helper()
	ag := NewAgentInstance(&config.AgentConfig{ID: id, Name: id, Type: typ},
		&cfg.Agents.Defaults, cfg, &mockProvider{})
	ag.Home = filepath.Join(home, "agents", id)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
	al.registry.mu.Lock()
	al.registry.agents[id] = ag
	al.registry.mu.Unlock()
}
