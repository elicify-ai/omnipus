package internal

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const Logo = pkg.Logo

// GetOmnipusHome returns the omnipus home directory.
// Priority: $OMNIPUS_HOME > ~/.omnipus
//
// Delegates entirely to config.OmnipusHomeDir() — the single canonical
// home-directory resolver. Do not reimplement env/HOME resolution here: a
// prior independent reimplementation silently dropped os.UserHomeDir()
// errors (falling through to a bare relative path with no secure fallback)
// and never resolved a relative $OMNIPUS_HOME to an absolute path against
// CWD, unlike OmnipusHomeDir()'s safety nets.
func GetOmnipusHome() string {
	return config.OmnipusHomeDir()
}

func GetConfigPath() string {
	if configPath := os.Getenv(config.EnvConfig); configPath != "" {
		return configPath
	}
	return filepath.Join(GetOmnipusHome(), "config.json")
}

func LoadConfig() (*config.Config, error) {
	cfg, err := config.LoadConfig(GetConfigPath())
	if err != nil {
		return nil, err
	}
	logger.SetLevelFromString(cfg.Gateway.LogLevel)
	// ADR-054 D2/D3: agents are per-entity records under entities/agents/
	// now, not config.json's agents.list — config.LoadConfig strips any
	// legacy agents.list content on every load (legacy_agents_list.go), so
	// cfg.Agents.List is repopulated here from the agent store directly
	// (GetOmnipusHome(), not a derivation from cfg — see loadConfigWithAgents
	// in cmd/omnipus/main.go for why the derivation-based
	// agent.LoadAgentRosterFromEntityStore helper was tried and reverted).
	// Best-effort: a store-list failure only warns — callers still get every
	// other config field.
	agents, skipped, listErr := agentstore.New(GetOmnipusHome()).List()
	if listErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not list agent entity records: %v\n", listErr)
		return cfg, nil
	}
	cfg.Agents.List = agents
	cfg.SkippedAgentIDs = skipped
	return cfg, nil
}
