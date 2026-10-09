// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// WorkspaceSeedDefaultsConfig is the operator-facing, CONFIG-FILE-ONLY block
// that carries the shipped defaults for the workspace delegation seed
// (session-core C-DELEGATE, FR-014/015 and BDD-05.7).
//
// It is deliberately NOT exposed on the wire: see pkg/gateway's
// wireExcludedConfigFields (the GET sanitizer) and blockedPaths (the generic
// PUT refusal), and pkg/sysagent/tools' knownConfigPrefixes (set_config
// refuses any prefix it does not list). The operator edits config.json by hand;
// there is no UI, no endpoint and no agent tool for it, and adding another
// exclusion is a data edit that needs no Go change.
type WorkspaceSeedDefaultsConfig struct {
	// SelfEdge holds the self-delegation edge seed defaults.
	SelfEdge SelfEdgeSeedDefaults `json:"self_edge" yaml:"-"`
}

// SelfEdgeSeedDefaults holds the seed defaults for an agent's ordinary
// self-delegation row ({from_agent==to_agent}, modes direct|task, depth
// min(FreshSelfEdgeMaxDepth, ceiling)).
type SelfEdgeSeedDefaults struct {
	// ExcludeAgentIDs lists agent IDs that MUST NOT receive a seeded
	// self-delegation self-row. The shipped default is
	// DefaultSelfEdgeExcludeAgentIDs() — the two hidden type:system seed
	// records. Operator values win, INCLUDING an explicit empty array:
	//
	//   - absent (nil)  → the shipped default applies (a fresh install and an
	//     install that never wrote the key behave identically);
	//   - present []    → no exclusions apply (judge/plansupervisor gain a
	//     self-row);
	//   - present [ids] → exactly those ids are excluded.
	//
	// A nil slice therefore means "unset", not "empty" — the distinction is
	// load-bearing (see Config.SelfEdgeExcludeAgentIDs), so this field
	// deliberately has no `omitempty`: a nil marshals as `null` and an explicit
	// `[]` marshals as `[]`, keeping the two round-trip-distinguishable.
	ExcludeAgentIDs []string `json:"exclude_agent_ids" yaml:"-"`
}

// defaultWorkspaceSeedDefaults returns the shipped WorkspaceSeedDefaults block
// for a fresh install (DefaultConfig). Kept as one constructor so the shipped
// value has a single definition shared by DefaultConfig and the resolver's
// fallback.
func defaultWorkspaceSeedDefaults() *WorkspaceSeedDefaultsConfig {
	return &WorkspaceSeedDefaultsConfig{
		SelfEdge: SelfEdgeSeedDefaults{
			ExcludeAgentIDs: DefaultSelfEdgeExcludeAgentIDs(),
		},
	}
}

// DefaultSelfEdgeExcludeAgentIDs returns the exclusion list a fresh install
// ships: the two hidden type:system seed records (Judge and Plan Supervisor).
// Planner, Researcher and every other ordinary built-in are deliberately NOT
// excluded — they receive an ordinary self-row like any other agent.
func DefaultSelfEdgeExcludeAgentIDs() []string {
	return []string{"judge", "plansupervisor"}
}

// SelfEdgeExcludeAgentIDs resolves the effective self-edge seed exclusion list.
//
// An unset block (nil pointer) or an unset ExcludeAgentIDs (nil slice) yields
// the shipped default; a present-but-empty list yields an empty slice (the
// operator explicitly asked for no exclusions). A non-nil receiver is
// optional-safe: a nil *Config behaves as "unset". This is the ONE resolver
// every seed writer reads, so operator data — not a Go identity predicate —
// decides which agents get a seeded self-row.
//
// The returned slice is the caller's to read; callers that need a set build one
// via SelfEdgeExcludeAgentIDSet.
func (c *Config) SelfEdgeExcludeAgentIDs() []string {
	if c != nil && c.WorkspaceSeedDefaults != nil && c.WorkspaceSeedDefaults.SelfEdge.ExcludeAgentIDs != nil {
		return c.WorkspaceSeedDefaults.SelfEdge.ExcludeAgentIDs
	}
	return DefaultSelfEdgeExcludeAgentIDs()
}

// SelfEdgeExcludeAgentIDSet returns the effective exclusion list as a set, for
// the seed writers' lookup. A nil *Config is treated as "unset" (shipped
// default), matching SelfEdgeExcludeAgentIDs.
func (c *Config) SelfEdgeExcludeAgentIDSet() map[string]bool {
	ids := c.SelfEdgeExcludeAgentIDs()
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
