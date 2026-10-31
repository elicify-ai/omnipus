package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// WithAgents makes its agents real, runnable agents of the test gateway.
//
// Since ADR-054 the gateway reads its roster only from the entity store
// (entities/agents/<id>.json): config.json's agents.list is stripped on load
// (pkg/config/legacy_agents_list.go), so an agent that only appears in
// cfg.Agents.List is never registered. And since ADR-046 P1 an agent runs a
// turn only as a member of a workspace team; boot back-fills only the
// built-in roster into the default workspace (FR-008). The two helpers below
// do what an operator does by hand: create the agent record before boot,
// then add each chat-target agent to the default workspace's team.

// seedTestAgents writes every WithAgents agent as an entity record before
// boot. A core-agent id is written too; boot's SeedConfig re-enforces its
// identity fields, exactly as on a real install that already had the record.
func seedTestAgents(homeDir string, agents []config.AgentConfig) error {
	if len(agents) == 0 {
		return nil
	}
	store := agentstore.New(homeDir)
	for i := range agents {
		ac := agents[i]
		if ac.ID == "" {
			return fmt.Errorf("WithAgents entry %d has no id", i)
		}
		if err := store.Create(ac.ID, &ac); err != nil {
			return err
		}
	}
	return nil
}

// joinTestAgentsToDefaultWorkspace adds every chat-target WithAgents agent
// (not a worker, not a system agent — those are never team members) to the
// default workspace's core team, after boot created that workspace. It never
// removes a member.
func joinTestAgentsToDefaultWorkspace(homeDir string, agents []config.AgentConfig) error {
	var want []string
	for i := range agents {
		if agents[i].IsChatTarget() {
			want = append(want, agents[i].ID)
		}
	}
	if len(want) == 0 {
		return nil
	}
	id, err := workspace.ResolveDefaultID(homeDir)
	if err != nil {
		return fmt.Errorf("resolve default workspace: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(homeDir, "workspaces", id+".json"))
	if err != nil {
		return fmt.Errorf("read default workspace %s: %w", id, err)
	}
	var w workspace.Workspace
	if err := json.Unmarshal(raw, &w); err != nil {
		return fmt.Errorf("parse default workspace %s: %w", id, err)
	}
	present := make(map[string]bool, len(w.CoreTeam))
	for _, m := range w.CoreTeam {
		present[m] = true
	}
	grew := false
	for _, a := range want {
		if !present[a] {
			w.CoreTeam = append(w.CoreTeam, a)
			present[a] = true
			grew = true
		}
	}
	if !grew {
		return nil
	}
	if err := workspace.SaveRecord(homeDir, w); err != nil {
		return fmt.Errorf("save default workspace %s: %w", id, err)
	}
	return nil
}
