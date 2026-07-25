package agent

import (
	"fmt"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// LoadAgentRosterFromEntityStore populates cfg.Agents.List and
// cfg.SkippedAgentIDs from the per-entity agent store rooted at
// $OMNIPUS_HOME/entities/agents/ (ADR-054 D2), where $OMNIPUS_HOME is
// filepath.Dir(cfg.AgentHomeBasePath()) — the same derivation NewAgentLoop
// already uses for taskStore/sessionStore/auditDir (see loop.go's
// `homePath := filepath.Dir(cfg.AgentHomeBasePath())`).
//
// This is the boot-time wiring point config.Config.SkippedAgentIDs's own doc
// comment and legacy_agents_list.go's stripLegacyAgentsList anticipate:
// config loading (pkg/config) now unconditionally empties cfg.Agents.List of
// any legacy config.json content and can never read entities/ itself
// (pkg/config sits below pkg/agentstore in the import graph — pkg/agentstore
// imports pkg/config, so the reverse would cycle). Something above it must
// populate the real roster before any agent lookup happens, or every
// custom/core agent silently disappears after the very next config load —
// this function is that something.
//
// Call this ONCE per config load/reload, BEFORE constructing/rebuilding the
// AgentRegistry (NewAgentRegistry / AgentLoop.ReloadProviderAndConfig).
//
// Deliberately NOT wired automatically inside NewAgentRegistry itself:
// NewAgentRegistry and NewAgentLoop are constructed directly by roughly a
// hundred existing tests across this repo, the overwhelming majority of
// which build a *config.Config in memory (frequently with an empty or
// relative Agents.Defaults.Home) and expect zero disk I/O, with their
// in-memory cfg.Agents.List honored verbatim. Making the entity-store read
// implicit there would silently empty every such fixture (a nonexistent
// entities/agents/ dir legitimately returns zero agents per
// entity.Store.List) and risks creating stray "entities/agents/"
// directories relative to a misresolved/empty home path. Call this
// explicitly from the real boot path instead, where a genuine $OMNIPUS_HOME
// is guaranteed — see this function's own test for the isolated contract.
// The remaining call-site wiring (AgentLoop construction / reload,
// pkg/gateway boot) is a flagged, not-yet-done follow-up — see the ADR-054
// Wave 2 handoff notes.
//
// Returns the skipped-id set from the underlying List() call (ADR-054 D7) —
// the caller should pass it to AgentRegistry.EvaluateDefaultAgentHealth
// after constructing the registry, so the configured default agent's own
// entity record failing to load is surfaced as degraded (R3) rather than
// silently swallowed. An error here is NOT fatal to boot (D7: availability
// over strictness) — the caller may log it and continue; cfg.Agents.List is
// left however stripLegacyAgentsList already made it (empty), which
// resolves to the built-in "main" sentinel only, the same behavior as any
// other empty-roster boot.
func LoadAgentRosterFromEntityStore(cfg *config.Config) (skipped []string, err error) {
	if cfg == nil {
		return nil, fmt.Errorf("agent: LoadAgentRosterFromEntityStore: nil config")
	}
	homePath := filepath.Dir(cfg.AgentHomeBasePath())
	store := agentstore.New(homePath)
	agents, skippedIDs, listErr := store.List()
	if listErr != nil {
		return nil, fmt.Errorf("agent: load roster from entity store: %w", listErr)
	}
	cfg.Agents.List = agents
	cfg.SkippedAgentIDs = skippedIDs
	return skippedIDs, nil
}
