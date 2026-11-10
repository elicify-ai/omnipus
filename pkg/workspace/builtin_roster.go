// builtin_roster.go: the shared D8 built-in-roster rule (founder ruling
// 2026-10-11) for EVERY writer that can change a workspace's core_team — the
// REST PUT /workspaces/{id} and the agent tool pkg/sysagent/tools
// update_workspace alike: a built-in roster member cannot leave the DEFAULT
// workspace, because pkg/gateway's ensureBuiltinRosterPresent re-adds every
// missing built-in to the default workspace at the next boot, so a successful
// removal would make the writer lie about a removal the restart silently
// undoes. The refusal must run before any effect of the change on both paths.

package workspace

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// BuiltinRemovalMessage is the exact user-facing refusal text (founder-fixed
// 2026-10-11); both writers surface it verbatim.
const BuiltinRemovalMessage = "Built-in agents can't be removed from this workspace"

// BuiltinRemovalCode is the machine-readable error code the REST refusal
// carries (ErrorResponse.code; documented on contracts/openapi.yaml
// updateWorkspace '409'). The update_workspace tool renders it in its own
// SCREAMING_SNAKE convention (BUILTIN_ROSTER_DEFAULT) per its error shape.
const BuiltinRemovalCode = "builtin_roster_default"

// DefaultWorkspaceTeamIDs derives the built-in roster for the boot DEFAULT
// workspace: every agent the product delivers (coreagent.All()) that is
// present in configuredIDs, minus the ids barred from workspace teams by role
// (coreagent.ExcludedFromWorkspaceTeams — the Admin standalone operator and
// the hidden System Agents). This is the ONE derivation both writers feed
// RefuseBuiltinRemoval from; pkg/gateway's defaultWorkspaceTeam delegates to
// it so the Team tab's display order and the refusal decide "built-in" from
// the same list.
func DefaultWorkspaceTeamIDs(configuredIDs []string) []string {
	present := make(map[string]bool, len(configuredIDs))
	for _, id := range configuredIDs {
		present[id] = true
	}
	all := coreagent.All()
	team := make([]string, 0, len(all))
	for _, a := range all {
		id := string(a.ID)
		if present[id] && !coreagent.ExcludedFromWorkspaceTeams(a.ID) {
			team = append(team, id)
		}
	}
	return team
}

// RefuseBuiltinRemoval returns an error carrying BuiltinRemovalMessage when
// newTeam removes any built-in roster member that currentTeam has, on the
// default workspace (isDefault). Nil when the change is allowed: a
// non-default workspace (nothing re-adds built-ins there), or a change that
// removes no built-in. Both workspace writers call this BEFORE any effect of
// the change — no record write, no delegation work, no plan side effect.
func RefuseBuiltinRemoval(isDefault bool, currentTeam, newTeam, configuredIDs []string) error {
	if !isDefault {
		return nil
	}
	newSet := make(map[string]bool, len(newTeam))
	for _, id := range newTeam {
		newSet[id] = true
	}
	current := make(map[string]bool, len(currentTeam))
	for _, id := range currentTeam {
		current[id] = true
	}
	for _, id := range DefaultWorkspaceTeamIDs(configuredIDs) {
		if current[id] && !newSet[id] {
			return errors.New(BuiltinRemovalMessage)
		}
	}
	return nil
}
