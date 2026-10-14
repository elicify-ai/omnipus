// team_eligibility.go: which roster identities may sit on a workspace team

package coreagent

// ExcludedFromWorkspaceTeams reports whether the roster identity is barred
// from workspace-team membership by its ROLE, independent of any config or
// workspace state: the hidden System Agents (IsSystemAgentID: the Judge,
// PlanSupervisor) are engine-owned and never teammates.
//
// Every membership write surface consults this ONE predicate rather than
// hand-rolling its own check, so the rule cannot diverge between them: the
// boot default-team seed (pkg/gateway's defaultWorkspaceTeam), the REST team
// validator (rest_workspaces.go's validateCoreTeamMembers) and the sysagent
// team-write tools (pkg/sysagent/tools/workspace.go's
// create_workspace/update_workspace).
//
// Omnipus is NOT excluded here: it is an ordinary hidden member of the
// DEFAULT workspace only (omnipus-system-agent spec FR-003). That
// workspace-specific rule is OmnipusMembershipRefusal, which the same writers
// consult.
//
// An ordinary roster identity (mia/jim/omnipus, the staff workers, customs —
// and any unknown/non-roster id) returns false: this predicate expresses only
// roster-role exclusion, never a whitelist.
func ExcludedFromWorkspaceTeams(id CoreAgentID) bool {
	return IsSystemAgentID(id)
}

// OmnipusMembershipRefusalText is the exact refusal every membership writer
// returns when a change would put Omnipus outside the default workspace or
// take it out of the default workspace (spec FR-003, §6.2: 422 on REST, an
// error result on the agent tool).
const OmnipusMembershipRefusalText = "Omnipus belongs to the default workspace only; it cannot be added to another workspace or removed from the default workspace"

// OmnipusMembershipRefusal reports whether a team change violates FR-003:
// Omnipus present in the new team of a NON-default workspace, or present in
// the current team of the DEFAULT workspace and absent from the new one.
// Every other change (any team without Omnipus, the default team keeping it)
// is allowed.
func OmnipusMembershipRefusal(isDefault bool, currentTeam, newTeam []string) bool {
	inNew := containsID(newTeam, string(IDOmnipus))
	if !isDefault {
		return inNew
	}
	return containsID(currentTeam, string(IDOmnipus)) && !inNew
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
