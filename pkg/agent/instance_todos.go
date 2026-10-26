package agent

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// configureSetTodosGuidance snapshots registration and effective policy after
// instance construction and shared task-tool wiring. Boot, full reload and fast
// agent updates use those same paths, before publishing the rebuilt instance.
// It deliberately does not inspect final per-request offered tool definitions.
func (a *AgentInstance) configureSetTodosGuidance() {
	if a.ContextBuilder == nil {
		return
	}
	enabled := false
	if a.Tools != nil {
		if tool, registered := a.Tools.Get("set_todos"); registered {
			enabled = tools.EffectiveToolPolicy(a.LoadToolPolicy(), tool.Scope(), a.AgentType, tool.Name()) != string(config.ToolPolicyDeny)
		}
	}
	a.ContextBuilder.WithSetTodosEnabled(enabled)
}
