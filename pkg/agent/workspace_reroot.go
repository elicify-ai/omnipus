// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// resolveTurnWorkDirOrRefuse resolves the workspace-scoped work directory a
// turn MUST execute in (ADR-046 P1, FR-007/008: execution is always
// workspace-scoped), or refuses the turn with a typed error.
//
// This is the SINGLE, SHARED gate for both dispatch kinds — the native path
// (loop.go's runTurn re-root block) and the external-cli path
// (external_dispatch.go's runExternalCLISubTurn) both call this exact
// function so the membership refusal cannot silently diverge between them
// again. It previously existed only in runTurn; runExternalCLISubTurn had its
// own, weaker copy that fell through to the agent's private home directory
// instead of refusing (the BLOCK/HIGH this helper fixes).
//
// agentID is the identity FindForAgentPreferring keys off primarily (CoreTeam
// membership) — this is what makes the gate apply uniformly to top-level
// turns, delegated (spawnSubTurn) children, and external-CLI dispatches
// alike, since all three resolve the acting agent's ID the same way.
//
// optWorkspaceID is the current turn's own channel-bound workspace id (may be
// empty). It is used ONLY to break a tie when the agent is a member of MORE
// THAN ONE workspace's CoreTeam — FindForAgentPreferring prefers this id over
// its own arbitrary sorted-first pick when the agent is actually a member of
// it. It never widens or bypasses the membership check itself: an empty
// optWorkspaceID falls straight through to FindForAgentPreferring's
// identity-only resolution, and an agent that is a member of NO workspace is
// refused regardless of what optWorkspaceID contains.
//
// This intentionally does NOT touch tools.WithWorkspaceID/memory-room routing
// (FR-030) — callers apply that separately, and this refusal runs
// independently of it either way.
//
// Return contract:
//   - not a member of any workspace -> "", error wrapping ErrAgentNotWorkspaceMember
//   - member, but the workspace id fails the traversal guard (SafeWorkDir) -> "", wrapped error
//   - member, but the work/ directory cannot be created (MkdirAll) -> "", wrapped error
//   - success -> the workspace's work/ directory (already created), nil
//
// None of these error paths fall back to the agent's own private home
// directory — a no-fallthrough guarantee FR-007/008 requires uniformly for
// every caller.
func resolveTurnWorkDirOrRefuse(agentID, optWorkspaceID string) (string, error) {
	home := omnipusHome()

	wsID, found := workspace.FindForAgentPreferring(home, agentID, optWorkspaceID)
	if !found {
		logger.WarnCF(
			"agent",
			"turn refused: agent is not a member of any workspace",
			map[string]any{"agent_id": agentID},
		)
		return "", fmt.Errorf("%w: agent_id=%s", ErrAgentNotWorkspaceMember, agentID)
	}

	wsDir, idErr := workspace.SafeWorkDir(home, wsID)
	if idErr != nil {
		logger.WarnCF(
			"agent",
			"turn refused: invalid workspace id resolving work dir",
			map[string]any{"agent_id": agentID, "workspace_id": wsID, "error": idErr.Error()},
		)
		return "", fmt.Errorf(
			"workspace work dir unavailable for agent_id=%s workspace_id=%s: %w",
			agentID,
			wsID,
			idErr,
		)
	}

	if mkErr := os.MkdirAll(wsDir, 0o700); mkErr != nil {
		logger.WarnCF(
			"agent",
			"turn refused: could not create workspace work dir",
			map[string]any{"agent_id": agentID, "workspace_id": wsID, "dir": wsDir, "error": mkErr.Error()},
		)
		return "", fmt.Errorf(
			"workspace work dir unavailable for agent_id=%s workspace_id=%s: %w",
			agentID,
			wsID,
			mkErr,
		)
	}

	return wsDir, nil
}
