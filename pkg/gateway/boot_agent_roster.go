// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// seedAndPersistAgentRoster is the boot sequence that brings the persisted
// agent roster into cfg, seeds the core agents and runs SeedConfig's one-time
// migrations, then persists the result. It is called once, from
// RunContextWithOptions, before the agent loop is built. Extracted so the
// upgrade path (an install seeded by an older build) can be tested against a
// real on-disk fixture with exactly the calls boot makes, in exactly this
// order.
//
// Steps:
//
//  1. ADR-054 D2/D3: bring in any agents already persisted as entity records
//     (entities/agents/<id>.json) from a previous run BEFORE SeedConfig looks
//     at cfg.Agents.List to decide which core agents are "already present" —
//     otherwise every boot would look like a fresh install (cfg.Agents.List
//     starts empty: config.LoadConfig strips config.json's legacy agents.list
//     unconditionally, see legacy_agents_list.go) and SeedConfig would
//     re-create core agents from their seed defaults on every restart,
//     discarding any operator customization. Strict variant: a roster-
//     population failure here (genuine store error, every on-disk record
//     unparseable, or a same-process non-empty→empty regression) must abort
//     boot rather than silently proceed with an empty/partial roster — see
//     populateAgentsListFromEntityStoreStrict's doc for the verified
//     privilege-escalation chain an empty roster otherwise opens up.
//
//  2. Seed core agents into config on first boot. Core agents are stored in
//     cfg.Agents.List with Locked=true so they appear alongside custom agents
//     in the REST API with type "core". SeedConfig is idempotent — it only
//     adds agents that are not already present (checked by ID) — and it also
//     runs the one-time, marker-keyed migrations (ADR-074 D4 skills, ADR-080
//     D-SKILL rename, and the 2026-09-15 Worker goal_claim update).
//
//     ADR-054 D2/§11: core agents now persist as entity records
//     (entities/agents/<id>.json) — never back into config.json's
//     agents.list. config.SaveConfig here would be a double violation:
//     (a) it is the full-struct save CLAUDE.md forbids for exactly this
//     reason ("corrupts API keys" via SecureString round-trip), and (b)
//     anything it wrote to agents.list would be stripped again on the
//     very next config.LoadConfig call, so it would not even survive.
//     persistSeededCoreAgents persists every agent SeedConfig
//     added-or-touched (its own "re-enforce identity fields on existing
//     core agents" pass, any brand-new core agent it appended, and any value
//     a one-time migration changed) via the agent store — see its own doc
//     comment for the corrupt-record handling that makes this safe against a
//     single bad entity file.
//
//  3. ADR-074 D4: durably record the one-shot skills-migration markers
//     SeedConfig checked/wrote in memory (e.g. the define-done allowlist
//     append). ADR-080 D-SKILL's own marker (adr080-define-goal-rename,
//     the "define-done"→"define-goal" allowlist REWRITE — see
//     coreagent.applyDefineGoalRenameMigration) rides the exact same
//     cfg.SeededSkillGrants slice and is persisted by this same call; the
//     matching skill-DIRECTORY cleanup (deleting the orphaned
//     $OMNIPUS_HOME/skills/define-done/) is a separate, later step — see the
//     call to skills.SeedDefaults in RunContextWithOptions. The agent-side
//     appends were just persisted by persistSeededCoreAgents; this writes the
//     marker into config.json so the migration never re-runs. Best-effort:
//     a failure only means the (idempotent, additive) check runs again next
//     boot — not a boot-time fatal. The helper skips the write entirely when
//     the on-disk key already matches, so a settled install's boot performs
//     no config.json write here at all.
//
//  4. The same for cfg.SeededToolPolicyUpdates (the one-time seeded
//     tool-policy updates, e.g. coreagent.ToolPolicyUpdateWorkerGoalClaimAllow).
//     Also best-effort, with one difference worth stating: that update CHANGES
//     a stored value rather than adding a missing one, so if this write fails
//     the update runs again on the next boot, and a goal_claim deny an
//     operator set on the Worker in between would be flipped once more. The
//     warning says so, so the operator can re-apply it.
func seedAndPersistAgentRoster(cfg *config.Config, homePath, configPath string) error {
	if rosterErr := populateAgentsListFromEntityStoreStrict(cfg, homePath); rosterErr != nil {
		return fmt.Errorf("gateway: could not populate agent roster from entity store at boot: %w", rosterErr)
	}

	if coreagent.SeedConfig(cfg) {
		if seedErr := persistSeededCoreAgents(homePath, cfg.Agents.List); seedErr != nil {
			return seedErr
		}
	}

	if len(cfg.SeededSkillGrants) > 0 {
		if persistErr := persistSeededSkillGrants(configPath, cfg.SeededSkillGrants); persistErr != nil {
			slog.Warn("gateway: could not persist seeded_skill_grants to config.json; "+
				"the additive skills migration will be re-checked on the next boot",
				"error", persistErr)
		}
	}

	if len(cfg.SeededToolPolicyUpdates) > 0 {
		if persistErr := persistSeededToolPolicyUpdates(configPath, cfg.SeededToolPolicyUpdates); persistErr != nil {
			slog.Warn("gateway: could not persist seeded_tool_policy_updates to config.json; "+
				"the one-time seeded tool-policy updates will run again on the next boot, which would "+
				"move a goal_claim deny set on the Worker since this boot back to allow",
				"error", persistErr)
		}
	}
	return nil
}
