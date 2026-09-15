// rest_workspace_team.go: Workspace team over REST — built-in roster back-fill, core_team validation and dangling-member repair, workspaceless-agent diagnostics, and the member_configs wire translation

package gateway

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ensureBuiltinRosterPresent unions any built-in-roster member
// (defaultWorkspaceTeam(cfg) — coreagent.All() ∩ configured agents) missing
// from the existing default workspace w's CoreTeam, and persists the change
// ONLY if the set actually grew (ADR-046 P1, FR-008 / US-3 AS-4). This keeps
// an upgraded install's pre-existing default workspace current with the
// installed built-in roster (e.g. a coreagent added post-upgrade) every
// boot, idempotently and safely:
//   - NEVER removes an existing member (including a custom agent an operator
//     added to the team by hand).
//   - NEVER adds a non-built-in ID — only defaultWorkspaceTeam(cfg)'s own
//     coreagent.All() ∩ configured-agents set is eligible.
//   - Does NOT touch w.Delegation. Expanding or seeding a workspace team must
//     never create or imply a Delegation[] trust edge (FR-038) — trust stays
//     workspace-scoped and explicit (ADR-037).
func ensureBuiltinRosterPresent(home string, w storedWorkspace, cfg *config.Config) error {
	builtin := defaultWorkspaceTeam(cfg)
	if len(builtin) == 0 {
		return nil
	}
	existing := make(map[string]bool, len(w.CoreTeam))
	for _, id := range w.CoreTeam {
		existing[id] = true
	}
	grew := false
	for _, id := range builtin {
		if !existing[id] {
			w.CoreTeam = append(w.CoreTeam, id)
			existing[id] = true
			grew = true
		}
	}
	if !grew {
		return nil
	}
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(home, w); err != nil {
		return fmt.Errorf("ensureBuiltinRosterPresent: write: %w", err)
	}
	slog.Info("rest: ensureDefaultWorkspace: back-filled built-in roster into existing default workspace",
		"workspace_id", w.ID, "team_size", len(w.CoreTeam))
	return nil
}

// logWorkspacelessAgents is a boot-time diagnostic (ADR-046 P1, FR-007/008):
// it enumerates every configured agent (cfg.Agents.List) that is a member of
// NO workspace's CoreTeam and emits ONE WARN naming all of them, if any
// exist. Called once at boot, after ensureDefaultWorkspace has run, so an
// operator upgrading an install with pre-existing custom agents sees the
// full list up front — rather than discovering it only as per-turn
// ErrAgentNotWorkspaceMember refusals, one agent at a time, as those agents
// happen to be invoked.
//
// This is diagnostic ONLY: it never mutates any workspace or agent — FR-008
// forbids auto-adding a pre-existing/custom agent to any team, and that rule
// applies here too. The operator remedy is manual: add the agent to a
// workspace's Team tab.
func logWorkspacelessAgents(home string, cfg *config.Config) {
	if cfg == nil || len(cfg.Agents.List) == 0 {
		return
	}
	var workspaceless []string
	for i := range cfg.Agents.List {
		id := cfg.Agents.List[i].ID
		if id == "" {
			continue
		}
		if _, found := workspace.FindForAgent(home, id); !found {
			workspaceless = append(workspaceless, id)
		}
	}
	if len(workspaceless) == 0 {
		return
	}
	sort.Strings(workspaceless)
	slog.Warn(
		"gateway: configured agents are members of no workspace — they cannot execute a turn until added to a workspace's Team tab (ADR-046 P1, FR-007/008)",
		"agent_ids",
		strings.Join(workspaceless, ","),
		"count",
		len(workspaceless),
	)
}

// workspaceMemberConfigsFromWire translates the generated member_configs map
// (gen.WorkspaceMemberConfig, pointer fields) into the internal
// workspace.MemberConfig map (value fields). It returns mcPresent=false when the
// wire field is absent (nil) so callers preserve merge semantics (absent →
// unchanged). The server-managed session_id (FR-010, readOnly in the contract)
// is intentionally NOT read from client input.
func workspaceMemberConfigsFromWire(
	wire *map[string]gen.WorkspaceMemberConfig,
) (map[string]workspace.MemberConfig, bool) {
	if wire == nil {
		return nil, false
	}
	out := make(map[string]workspace.MemberConfig, len(*wire))
	for agentID, wmc := range *wire {
		var mc workspace.MemberConfig
		if hb := wmc.Heartbeat; hb != nil {
			mh := &workspace.MemberHeartbeat{}
			if hb.Enabled != nil {
				mh.Enabled = *hb.Enabled
			}
			if hb.IntervalMinutes != nil {
				mh.IntervalMinutes = *hb.IntervalMinutes
			}
			if hb.Body != nil {
				mh.Body = *hb.Body
			}
			mc.Heartbeat = mh
		}
		out[agentID] = mc
	}
	return out, true
}

// validateCoreTeamMembers rejects a core_team containing an id that is not a
// registered agent, or that IS registered but is a System Agent
// (AgentConfig.IsSystem, Type=="system", ADR-049 D3) — review r1 major
// M4/Gap #6. System Agents (e.g. the Judge) are seeded, locked, no-tools
// internal-LLM agents that are NEVER chat targets and are documented as
// "excluded from ... team rosters" by AgentConfig.IsSystem's own doc comment
// (pkg/config/config.go), but neither handleWorkspacePost nor
// handleWorkspacePut enforced that at the write path before this fix — a
// caller could silently add a System Agent (or a typo'd/nonexistent id) to a
// workspace's core_team with zero validation. Returns nil for an empty
// coreTeam (nothing to validate).
//
// This rejection stays exactly as it was even after ADR-052's Judge/verifier
// fix made System Agents IMPLICIT members of EVERY workspace (operator
// decision, 2026-07-21: "make the judge a member of every workspace, keep it
// simple" — pkg/workspace's isImplicitMember, consulted by
// FindForAgent/FindForAgentPreferring). Implicit membership everywhere makes
// an EXPLICIT core_team entry for a System Agent strictly redundant, never
// necessary — so this validation continues to reject one on write, rather
// than being relaxed or repurposed.
func validateCoreTeamMembers(cfg *config.Config, coreTeam []string) error {
	if len(coreTeam) == 0 {
		return nil
	}
	if cfg == nil {
		return fmt.Errorf("core_team member %q is not a registered agent", coreTeam[0])
	}
	byID := make(map[string]*config.AgentConfig, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		byID[cfg.Agents.List[i].ID] = &cfg.Agents.List[i]
	}
	for _, id := range coreTeam {
		ac, ok := byID[id]
		if !ok {
			return fmt.Errorf("core_team member %q is not a registered agent", id)
		}
		if ac.IsSystem() {
			return fmt.Errorf(
				"core_team member %q is a System Agent and cannot be added to a workspace team roster", id)
		}
	}
	return nil
}

// RepairDanglingCoreTeamMembers is the first-class "drop dangling members"
// repair operation ADR-054 D6 rule 3 requires: an explicit way to clean up a
// core_team whose members no longer all resolve to a registered agent,
// without hand-editing the workspace's JSON file. It is the counterpart to
// validateCoreTeamMembers' reject-on-write behavior — where that function
// stops a write from INTRODUCING a dangling reference, this function
// removes ones that already got in (e.g. a workspace hand-edited outside the
// API, or created before this write path existed).
//
// Only true dangling references — an id with no matching entry in
// cfg.Agents.List at all — are dropped. A member id that resolves but is a
// System Agent (which validateCoreTeamMembers separately rejects on write) is
// NOT touched here: it is not "dangling" (the agent exists), so silently
// removing it would be a different, unrelated correction this operation does
// not claim to make. repaired preserves the original member order; dropped
// lists exactly which ids were removed (also in original order) so the
// caller can report/log/audit what changed. A nil/empty coreTeam, or a nil
// cfg (nothing to resolve against), returns the input unchanged with a nil
// dropped slice.
//
// Wiring a REST endpoint around this function (contract-first per
// Constraint #8: a new wire shape needs an openapi.yaml schema + regenerated
// types before any handler can use it) is left to the wave that owns the
// REST conversion — this function is the tested, ready-to-call primitive
// that endpoint would call.
func RepairDanglingCoreTeamMembers(cfg *config.Config, coreTeam []string) (repaired []string, dropped []string) {
	if len(coreTeam) == 0 {
		return coreTeam, nil
	}
	if cfg == nil {
		return nil, append([]string(nil), coreTeam...)
	}
	registered := make(map[string]struct{}, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		registered[cfg.Agents.List[i].ID] = struct{}{}
	}
	repaired = make([]string, 0, len(coreTeam))
	for _, id := range coreTeam {
		if _, ok := registered[id]; ok {
			repaired = append(repaired, id)
		} else {
			dropped = append(dropped, id)
		}
	}
	return repaired, dropped
}
