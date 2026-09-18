package gateway

import (
	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// sysagentAgentConfigInventory snapshots installed skills and live connector
// inventory for create_agent/update_agent. Called before agentstore locks.
func sysagentAgentConfigInventory(rc *runContextWithOptions) systools.AgentConfigInventory {
	inv := systools.AgentConfigInventory{
		SkillsListed:  true,
		Skills:        map[string]struct{}{},
		ConfiguredMCP: map[string]struct{}{},
		LiveMCPTools:  map[string]map[string]struct{}{},
	}
	workspace := ""
	if rc.agentLoop != nil {
		if cfg := rc.agentLoop.GetConfig(); cfg != nil {
			workspace = cfg.Agents.Defaults.Home
		}
		for id := range rc.agentLoop.MCPServersSnapshot() {
			inv.ConfiguredMCP[id] = struct{}{}
		}
	}
	if rc.centralMCPReg != nil {
		for _, entry := range rc.centralMCPReg.Describe() {
			live := inv.LiveMCPTools[entry.ServerID]
			if live == nil {
				live = map[string]struct{}{}
				inv.LiveMCPTools[entry.ServerID] = live
			}
			live[entry.Name] = struct{}{}
			if tool, ok := rc.centralMCPReg.Get(entry.Name); ok {
				for _, name := range systools.CollectLiveMCPNames(tool) {
					live[name] = struct{}{}
				}
			}
		}
	}
	for _, id := range agent.InstalledSkillIDs(workspace) {
		inv.Skills[id] = struct{}{}
	}
	return inv
}

func sysagentAgentIsLive(rc *runContextWithOptions, id string) bool {
	if rc.agentLoop == nil {
		return false
	}
	reg := rc.agentLoop.GetRegistry()
	if reg == nil {
		return false
	}
	inst, ok := reg.GetAgent(id)
	return ok && inst != nil
}

func sysagentAgentActiveRevision(agentLoop *agent.AgentLoop, homePath, id string) string {
	if agentLoop == nil {
		return ""
	}
	registry := agentLoop.GetRegistry()
	if registry == nil {
		return ""
	}
	instance, ok := registry.GetAgent(id)
	if !ok || instance == nil {
		return ""
	}
	state, err := agentstore.New(homePath).ReadState(id)
	if err != nil {
		return ""
	}
	if !instance.MatchesSourceConfig(state.Agent) {
		return ""
	}
	return state.Revision
}
