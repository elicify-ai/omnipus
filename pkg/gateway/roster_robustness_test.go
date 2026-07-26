//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for two verified boot/reload robustness bugs:
//
//  1. persistSeededCoreAgents (extracted from RunContextWithOptions) must not
//     abort boot when a single core-agent entity record is corrupt/
//     unparseable — a store.Get() parse error must never be misclassified as
//     "record does not exist" (which previously drove a doomed
//     store.Create -> entity.ErrAlreadyExists -> hard boot abort, inverting
//     ADR-054 D7's "unparseable record -> skip + ERROR + mark degraded").
//  2. populateAgentsListFromEntityStoreStrict must REJECT (never silently
//     empty) the in-memory agent roster on a genuine entity-store failure,
//     because pkg/agent/registry.go's always-registered "main" sentinel
//     AgentConfig (no Tools/Policies at all) combined with
//     pkg/tools/compositor.go's global×agent policy merge means an empty
//     roster silently promotes ALL routed traffic to the permissive global
//     tool-policy floor (pkg/config/defaults.go) instead of any real agent's
//     own deny-heavy policy — a verified privilege-escalation chain, not a
//     theoretical one.
package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- Bug 1: a single corrupt core-agent entity record must not abort boot ---

// TestPersistSeededCoreAgents_CorruptRecordDoesNotAbortBoot proves the fix:
// when one seeded core agent's entity record on disk is corrupt/unparseable,
// persistSeededCoreAgents skips re-seeding THAT agent (logs and continues)
// instead of returning an error that would abort the whole boot sequence.
// The historical behavior treated store.Get's parse error identically to
// "does not exist", driving a store.Create that failed with
// entity.ErrAlreadyExists (the file DOES exist, it just didn't parse) — one
// corrupt file made the entire gateway unbootable.
func TestPersistSeededCoreAgents_CorruptRecordDoesNotAbortBoot(t *testing.T) {
	home := t.TempDir()
	entitiesDir := filepath.Join(home, "entities", "agents")
	require.NoError(t, os.MkdirAll(entitiesDir, 0o700))

	// mia.json exists but is corrupt (not valid JSON) — simulates a
	// truncated write, disk corruption, or a hand-edit gone wrong.
	corruptPath := filepath.Join(entitiesDir, "mia.json")
	corruptBytes := []byte("{not valid json at all")
	require.NoError(t, os.WriteFile(corruptPath, corruptBytes, 0o600))

	seeded := []config.AgentConfig{
		{ID: "mia", Name: "Mia"},
		{ID: "jim", Name: "Jim"}, // a normal, brand-new core agent alongside it
	}

	err := persistSeededCoreAgents(home, seeded)
	require.NoError(t, err, "a single corrupt entity record must not abort boot")

	// The corrupt file must be left exactly as it was — no clobbering
	// attempt, no silent overwrite.
	after, readErr := os.ReadFile(corruptPath)
	require.NoError(t, readErr)
	assert.Equal(t, corruptBytes, after, "corrupt record must be left untouched, not overwritten")

	// The healthy sibling agent must still have been created normally.
	jimCfg, getErr := agentstore.New(home).Get("jim")
	require.NoError(t, getErr, "a healthy sibling agent must still persist normally")
	assert.Equal(t, "jim", jimCfg.ID)
}

// TestPersistSeededCoreAgents_NotFoundCreatesNormally is a negative control
// proving the ErrNotFound path (genuinely new agent, no file yet) still
// creates the record — the fix didn't turn EVERY Get error into a silent
// skip, only the non-ErrNotFound ones.
func TestPersistSeededCoreAgents_NotFoundCreatesNormally(t *testing.T) {
	home := t.TempDir()
	err := persistSeededCoreAgents(home, []config.AgentConfig{{ID: "ava", Name: "Ava"}})
	require.NoError(t, err)

	got, getErr := agentstore.New(home).Get("ava")
	require.NoError(t, getErr)
	assert.Equal(t, "ava", got.ID)
}

// --- Bug 2 / verified privilege-escalation fix: strict roster population ---

// TestPopulateAgentsListFromEntityStoreStrict_GenuineListErrorRejectsAndPreservesRoster
// proves a genuine entity.Store.List() failure (here: entities/agents/
// shadowed by a regular file, so os.ReadDir returns ENOTDIR — NOT
// os.IsNotExist) is propagated as an error, and cfg.Agents.List is left
// completely untouched rather than silently emptied. This is the exact class
// the legacy log-and-return behavior mishandled: a transient EMFILE/EACCES/
// EIO List() failure looked identical to "nothing configured yet".
func TestPopulateAgentsListFromEntityStoreStrict_GenuineListErrorRejectsAndPreservesRoster(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "entities"), 0o700))
	// Shadow entities/agents with a FILE, not a directory, so os.ReadDir
	// fails with a genuine (non-NotExist) error regardless of process UID
	// (unlike an EACCES-via-chmod approach, which is a no-op for root).
	require.NoError(t, os.WriteFile(filepath.Join(home, "entities", "agents"), []byte("not a directory"), 0o600))

	preexisting := []config.AgentConfig{{ID: "sentinel-preexisting", Name: "should survive"}}
	cfg := &config.Config{Agents: config.AgentsConfig{List: append([]config.AgentConfig{}, preexisting...)}}

	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.Error(t, err, "a genuine List() failure must be reported, not swallowed")
	assert.Equal(t, preexisting, cfg.Agents.List,
		"on a genuine store failure, cfg.Agents.List must be left exactly as it was — never silently emptied")
}

// TestPopulateAgentsListFromEntityStoreStrict_AllRecordsUnparseableRejected
// proves the roster-emptiness invariant: when on-disk agent records exist
// but EVERY one of them fails to parse (List() succeeds with err == nil, but
// agents comes back empty and skipped covers every id — e.g. a breaking
// schema change), that must be rejected as a hard failure, never silently
// accepted as "fresh install, zero agents".
func TestPopulateAgentsListFromEntityStoreStrict_AllRecordsUnparseableRejected(t *testing.T) {
	home := t.TempDir()
	entitiesDir := filepath.Join(home, "entities", "agents")
	require.NoError(t, os.MkdirAll(entitiesDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(entitiesDir, "mia.json"), []byte("{bad"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(entitiesDir, "jim.json"), []byte("also bad"), 0o600))

	cfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.Error(t, err, "every on-disk record failing to parse must be a hard failure")
	assert.Empty(t, cfg.Agents.List, "cfg.Agents.List must stay untouched (was already empty here)")
}

// TestPopulateAgentsListFromEntityStoreStrict_FreshInstallIsNotAnError is the
// negative control for the invariant above: a genuinely fresh install (no
// entities/agents/ directory at all yet) must NOT be treated as a failure —
// entity.Store.List() maps a missing directory to (nil, nil, nil).
func TestPopulateAgentsListFromEntityStoreStrict_FreshInstallIsNotAnError(t *testing.T) {
	home := t.TempDir() // entities/agents/ never created
	cfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.NoError(t, err, "a genuinely fresh install (zero on-disk records) must not error")
	assert.Empty(t, cfg.Agents.List)
}

// TestPopulateAgentsListFromEntityStoreStrict_RegressionGuardRejectsEmptyAfterNonEmpty
// proves the same-process regression guard (the BUG2 defense): a previously
// non-empty roster observed for this home going empty on a later call — e.g.
// because homePath momentarily resolved to the wrong directory — is
// rejected rather than silently wiping the live roster.
func TestPopulateAgentsListFromEntityStoreStrict_RegressionGuardRejectsEmptyAfterNonEmpty(t *testing.T) {
	home := t.TempDir()
	store := agentstore.New(home)
	seed := config.AgentConfig{ID: "mia", Name: "Mia"}
	require.NoError(t, store.Create("mia", &seed))

	firstCfg := &config.Config{}
	require.NoError(t, populateAgentsListFromEntityStoreStrict(firstCfg, home))
	require.Len(t, firstCfg.Agents.List, 1, "first call should observe the real, non-empty roster")

	// Now simulate the roster disappearing for this SAME home (e.g. the
	// directory was wiped, or — the real-world BUG2 case — homePath
	// resolution glitched to an empty sibling directory momentarily).
	require.NoError(t, os.RemoveAll(store.Dir()))

	secondCfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(secondCfg, home)
	require.Error(t, err, "a non-empty roster going empty for the same home must be rejected")
	assert.Empty(t, secondCfg.Agents.List, "the fresh (never-populated) cfg for this failed call must stay untouched")
}

// TestEmptyAgentRosterWouldResolveToPermissiveGlobalFloor documents, using
// the REAL production merge logic (pkg/tools.ResolveEffectivePolicy) and the
// REAL seeded global defaults (pkg/config.DefaultConfig), exactly why an
// empty/wiped agent roster is a privilege-escalation risk and not merely a
// UX gap: an agent with no per-agent tool-policy entries at all (nil
// Policies map — precisely what pkg/agent/registry.go's always-registered
// "main" sentinel AgentConfig has, since it is built fresh with no Tools
// field ever set) resolves security-sensitive tools to "allow" via the
// global floor alone, because there is no agent-side entry to out-rank it.
// This is the scenario populateAgentsListFromEntityStoreStrict exists to
// prevent a live gateway from ever silently falling into (see its own doc
// comment for the full causal chain: NewAgentRegistry's sentinel ->
// compositor's global×agent merge -> defaults.go's seeded floor ->
// repairAndValidateToolPolicyCoverage's vacuous pass on an empty roster).
func TestEmptyAgentRosterWouldResolveToPermissiveGlobalFloor(t *testing.T) {
	defaultCfg := config.DefaultConfig()
	globalPolicies := make(map[string]config.ToolPolicy, len(defaultCfg.Sandbox.ToolPolicies))
	for k, v := range defaultCfg.Sandbox.ToolPolicies {
		globalPolicies[k] = config.ToolPolicy(v)
	}

	// No per-agent Policies at all — mirrors registry.go's sentinel
	// AgentConfig{ID: DefaultAgentID}, which never has Tools set.
	polCfg := &tools.ToolPolicyCfg{GlobalPolicies: globalPolicies}

	for _, sensitive := range []string{"bash", "write_file", "edit_file", "delegate", "send_email"} {
		got := tools.ResolveEffectivePolicy(polCfg, sensitive)
		assert.Equal(t, "allow", got,
			"tool %q: an agent with no per-agent policy entry falls through to the seeded global "+
				"floor — this is exactly why populateAgentsListFromEntityStoreStrict must never let "+
				"the roster go silently empty", sensitive)
	}
}
