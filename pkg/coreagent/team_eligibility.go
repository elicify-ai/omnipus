// team_eligibility.go: which roster identities may sit on a workspace team

package coreagent

// ExcludedFromWorkspaceTeams reports whether the roster identity is barred
// from workspace-team membership by its ROLE, independent of any config or
// workspace state (ADR-090 FR-001/FR-006):
//
//   - the hidden System Agents (IsSystemAgentID: the Judge, PlanSupervisor)
//     are engine-owned and never teammates;
//   - Admin (IDAdmin) is the STANDALONE OPERATOR — a chat-able core agent
//     with "no team membership" (FR-001). It installs and probes document
//     prerequisites, holds connector/setup duties, and deliberately belongs
//     to no workspace, so an Admin membership is never legitimate.
//
// Every membership write surface consults this ONE predicate rather than
// hand-rolling its own check, so the rule cannot diverge between them:
// the boot default-team seed (pkg/gateway's defaultWorkspaceTeam), the REST
// team validator (rest_workspaces.go's validateCoreTeamMembers), the
// sysagent team-write tools (pkg/sysagent/tools/workspace.go's
// create_workspace/update_workspace), and the turn-admission gate
// (pkg/agent's resolveTurnWorkDirOrRefuse, which roots an Admin turn at its
// own agent home instead of any workspace).
//
// An ordinary roster identity (mia/jim/ava, the staff workers, customs — and
// any unknown/non-roster id) returns false: this predicate expresses only
// roster-role exclusion, never a whitelist.
func ExcludedFromWorkspaceTeams(id CoreAgentID) bool {
	return IsSystemAgentID(id) || id == IDAdmin
}
