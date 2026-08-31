// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent

// tool_policy_catalog_drift.go — closing the upgrade hole in CLAUDE.md hard
// constraint 6.
//
// # The hole
//
// The constraint says every static builtin tool must resolve, for every
// agent, from an explicit, literal policy entry — "never a silent runtime
// default". Two separate mechanisms were believed to guarantee that:
//
//   - the per-agent seed (coreAgentSeed / systemAgentSeed /
//     NewCustomAgentToolsCfg), which enumerates the WHOLE catalog for every
//     agent it writes; and
//   - config.ValidateToolPolicyCoverage, which aborts boot on any gap.
//
// Neither one covers an UPGRADE, and their gap overlaps exactly:
//
//  1. A new tool ships. pkg/config/defaults.go adds its global entry (in
//     practice "allow" — 87 of the 93 global entries are), and every seed
//     literal gains its per-agent posture.
//  2. On a FRESH install both halves are written together, so the global
//     entry behaves as the ceiling pkg/config/defaults.go's comments describe.
//  3. On an UPGRADE the agents already exist — in config.json before
//     ADR-054, in entities/agents/<id>.json since. SeedConfig's
//     existing-agent re-enforcement pass repairs identity, type, lock state
//     and the skill allowlist, but deliberately never touched
//     tools.builtin.policies, so the new tool's name stays absent from every
//     pre-existing agent's map.
//  4. pkg/tools.resolveEffectivePolicyWith resolves a tool with a global
//     entry and NO agent entry to the GLOBAL value (`case a == "": return
//     g`). The global map is a ceiling only for an agent that also has an
//     entry of its own; for a silent agent it is a GRANT.
//  5. config.ValidateToolPolicyCoverage counts a global entry as coverage, so
//     it reports no gap and RepairIncompleteToolPolicyCoverage — which would
//     have backfilled "deny" — is never given anything to repair.
//
// Net effect on an upgraded install: every agent that predates the new tool
// silently receives the global ceiling's value for it, overriding whatever
// its own seed says. ADR-068's six knowledge tools made that concrete —
// knowledge_edit (writes the operator's notes), knowledge_restructure
// (cascading rename/move/trash) and knowledge_configure (a schema edit
// reclassifies every note of a type) all resolved "allow" for the four
// delegation-only subagents whose seed denies them outright, and for
// Mia/Ava/Ray, whose seed gates all three behind "ask".
//
// # The close
//
// backfillToolPolicyCatalogDrift runs at the end of SeedConfig, i.e. on every
// boot, and fills GAPS ONLY — it never rewrites an entry that already exists,
// so an operator's own tool decisions survive every upgrade untouched.
//
// The value it fills a gap with depends on what the agent IS, because that is
// what determines whose intent the missing entry should express:
//
//   - A seeded core/worker/specialist agent takes its own seed's value
//     (coreAgentSeed). This is the whole point: the upgraded install converges
//     on the same posture a fresh install of the same binary would have had.
//   - A System Agent is skipped entirely. seedSystemAgents already re-enforces
//     its EXACT seeded map on every boot (a System Agent's tool surface is a
//     role invariant, not an operator preference), so it was never exposed and
//     there is nothing here to add.
//   - Any other agent — operator-created, from the REST handler, the
//     create_agent tool, or a hand-edited record — has no seed to consult, so
//     a name its enumeration never covered takes "deny": the same
//     deny-by-default baseline NewCustomAgentToolsCfg writes for every tool an
//     operator has not opted into, and the fail-closed direction the whole
//     subsystem is built in.
//
// # What it deliberately does NOT do
//
// It never invents an entry the seed does not name. The Worker's seed map is
// SPARSE by deliberate, operator-confirmed design (coreAgentSeed's IDWorker
// branch, via tightenGlobalCeiling): every tool it does not name is MEANT to
// track the global ceiling, which is why the same seed spells out an explicit
// "deny" for each name it wants below that ceiling. Backfilling every absent
// catalog name for the Worker would silently retire that design, so the seed
// branch iterates the SEED's keys, not the catalog's.
//
// It also leaves an operator-created agent with no policy map at all (nil or
// empty Tools) alone. A non-empty map is evidence of an enumeration — every
// creation path in this codebase writes the full catalog — so a name missing
// from one is catalog drift with a knowable intent. An empty map is not
// evidence of anything, and config.ValidateToolPolicyCoverage deliberately
// accepts global-only coverage, so tightening it here would be this file
// inventing a policy rather than restoring one.

import (
	"log/slog"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// toolPolicyBackfill is one (agent, tool) entry backfillToolPolicyCatalogDrift
// wrote, and the value it wrote. Returned as structured pairs rather than a
// formatted string so tests and future callers can assert on them directly.
type toolPolicyBackfill struct {
	AgentID string
	Tool    string
	Policy  config.ToolPolicy
}

// backfillToolPolicyCatalogDrift writes an explicit per-agent tool-policy
// entry for every static builtin tool an existing agent's persisted map does
// not name, so no agent resolves a tool from the global ceiling by silence.
// See this file's header for the full rationale, the per-agent-class value
// rule, and the two cases it deliberately leaves alone.
//
// Mutates cfg.Agents.List in place. Returns every pair it wrote, sorted by
// (AgentID, Tool); nil when there was nothing to do. Idempotent — a second
// call over the same config finds no gaps.
func backfillToolPolicyCatalogDrift(cfg *config.Config) []toolPolicyBackfill {
	if cfg == nil {
		return nil
	}

	var written []toolPolicyBackfill

	for i := range cfg.Agents.List {
		agentCfg := &cfg.Agents.List[i]
		id := CoreAgentID(agentCfg.ID)

		// A System Agent's map is re-enforced verbatim on every boot by
		// seedSystemAgents — it can never drift, so it can never need this.
		if IsSystemAgentID(id) {
			continue
		}

		// What the missing entries should say, keyed by tool name. For a
		// seeded agent that is its own seed (sparse for the Worker, fully
		// enumerated for everyone else). For an operator-created agent it is
		// the deny baseline over the whole catalog.
		var want map[string]config.ToolPolicy
		if ByID(id) != nil {
			want = coreAgentSeed(id)
		} else {
			if agentCfg.Tools == nil || len(agentCfg.Tools.Builtin.Policies) == 0 {
				// No enumeration to have drifted from — see the header.
				continue
			}
			want = make(map[string]config.ToolPolicy, len(allStaticToolNames))
			for _, name := range allStaticToolNames {
				want[name] = config.ToolPolicyDeny
			}
		}

		if agentCfg.Tools == nil {
			agentCfg.Tools = &config.AgentToolsCfg{}
		}
		if agentCfg.Tools.Builtin.Policies == nil {
			agentCfg.Tools.Builtin.Policies = make(map[string]config.ToolPolicy, len(want))
		}
		for name, policy := range want {
			if _, present := agentCfg.Tools.Builtin.Policies[name]; present {
				continue // an existing entry is an operator/seed decision — never overwrite
			}
			agentCfg.Tools.Builtin.Policies[name] = policy
			written = append(written, toolPolicyBackfill{
				AgentID: agentCfg.ID,
				Tool:    name,
				Policy:  policy,
			})
		}
	}

	if len(written) == 0 {
		return nil
	}
	sort.Slice(written, func(a, b int) bool {
		if written[a].AgentID != written[b].AgentID {
			return written[a].AgentID < written[b].AgentID
		}
		return written[a].Tool < written[b].Tool
	})
	logToolPolicyBackfill(written)
	return written
}

// logToolPolicyBackfill emits one WARN per agent naming every tool that was
// backfilled and the value written, so an upgrade that changes an agent's
// effective tool surface says so in the log rather than only in the resulting
// config. `written` must already be sorted by (AgentID, Tool).
func logToolPolicyBackfill(written []toolPolicyBackfill) {
	type entry struct {
		agentID string
		tools   []string
	}
	var perAgent []entry
	for _, w := range written {
		if len(perAgent) == 0 || perAgent[len(perAgent)-1].agentID != w.AgentID {
			perAgent = append(perAgent, entry{agentID: w.AgentID})
		}
		last := &perAgent[len(perAgent)-1]
		last.tools = append(last.tools, w.Tool+"="+string(w.Policy))
	}
	for _, e := range perAgent {
		slog.Warn(
			"coreagent: agent predates one or more static builtin tools; wrote an explicit policy "+
				"entry for each so none resolves from the global sandbox.tool_policies ceiling by "+
				"silence (CLAUDE.md hard constraint 6). A seeded agent takes its own seed's posture; "+
				"any other agent takes the deny baseline — review/loosen in Agents → Tools",
			"agent_id", e.agentID,
			"count", len(e.tools),
			"tools", e.tools,
		)
	}
}
