// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
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
// alike, since all three resolve the acting agent's ID the same way. System
// Agents (coreagent.IsSystemAgentID; today: the Judge, ADR-049 D3) are
// IMPLICIT members of EVERY workspace — that positive rule lives entirely in
// pkg/workspace (FindForAgent/FindForAgentPreferring's isImplicitMember), NOT
// here: this function calls FindForAgentPreferring uniformly for every
// agent, System or not, and never branches on agent identity before that
// call (operator decision, 2026-07-21: "make the judge a member of every
// workspace, keep it simple" — one positive membership rule, stated where
// membership is decided, not a bypass at this call site).
//
// optWorkspaceID is the current turn's own channel-bound workspace id (may be
// empty). It is used to break a tie / select among the agent's memberships —
// FindForAgentPreferring prefers this id over its own arbitrary sorted-first
// pick when the agent is actually a member of it. For an ordinary agent this
// only matters when it belongs to MORE than one workspace's CoreTeam. For a
// System Agent — implicitly a member of every workspace that exists — it is
// effectively ALWAYS "more than one membership", so optWorkspaceID is what
// picks WHICH workspace a given turn roots in; see the ctx-merge below for
// how the Judge's verifier turn supplies it.
//
// This intentionally does NOT touch tools.WithWorkspaceID/memory-room routing
// (FR-030) — callers apply that separately, and this refusal runs
// independently of it either way.
//
// Return contract:
//   - not a member of any workspace, and not a System Agent -> "", error wrapping ErrAgentNotWorkspaceMember
//   - not a member of any workspace, but a System Agent (only possible when
//     genuinely NO workspace exists yet at all — see the branch below) -> the
//     agent's own home directory, nil
//   - member, but the workspace id fails the traversal guard (SafeWorkDir) -> "", wrapped error
//   - member, but the work/ directory cannot be created (MkdirAll) -> "", wrapped error
//   - success -> the workspace's work/ directory (already created), nil
//
// ctx carries the (optional) verifier-turn workspace selector — see
// WithSystemAgentWorkspaceOverride below. agentHome is the caller's already-
// resolved AgentInstance.Home (ts.agent.Home / childTS.agent.Home at the two
// call sites), needed only by the pre-onboarding fallback branch; passed in
// rather than re-resolved here to keep this a plain, dependency-light
// function (no AgentLoop/registry access).
func resolveTurnWorkDirOrRefuse(ctx context.Context, agentID, agentHome, optWorkspaceID string) (string, error) {
	home := omnipusHome()

	// The verifier turn's work-under-review workspace (ADR-052
	// JudgeCriteriaInput.WorkspaceID) has no other channel into this
	// function — processTaskDirect's fixed signature carries no
	// workspace-id parameter, unlike a normal turn's ts.opts.WorkspaceID —
	// so it rides in via ctx (set by runVerifierAdjudication) and is merged
	// here with the SAME precedence optWorkspaceID already carries for
	// every other turn: prefer it when present, otherwise fall through
	// unchanged. For an ordinary turn this override is never set, so the
	// merge is a no-op and optWorkspaceID passes straight through.
	preferredWsID := optWorkspaceID
	if override := systemAgentWorkspaceOverrideFromContext(ctx); override != "" {
		preferredWsID = override
	}

	wsID, found := workspace.FindForAgentPreferring(home, agentID, preferredWsID)
	if !found {
		// A System Agent is an IMPLICIT member of every workspace
		// (pkg/workspace's FindForAgent/FindForAgentPreferring), so !found
		// for one can ONLY mean genuinely NO workspace exists yet at all —
		// the pre-onboarding edge, before a default workspace is seeded.
		// Fall back to its own agent home rather than refuse; every other
		// agent keeps the hard refusal below unconditionally.
		if coreagent.IsSystemAgentID(coreagent.CoreAgentID(agentID)) {
			agentHome = strings.TrimSpace(agentHome)
			if agentHome == "" {
				return "", fmt.Errorf("system agent has no resolvable work directory: agent_id=%s", agentID)
			}
			if mkErr := os.MkdirAll(agentHome, 0o700); mkErr != nil {
				return "", fmt.Errorf("system agent home dir unavailable for agent_id=%s: %w", agentID, mkErr)
			}
			return agentHome, nil
		}

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

// --- System Agent workspace selector (ADR-052 FR-011/012) -------------------

// systemAgentWorkspaceOverrideCtxKey is the unexported ctx key backing
// WithSystemAgentWorkspaceOverride/systemAgentWorkspaceOverrideFromContext.
// Package-local by design — this signal is produced by exactly one call site
// (verifier_adjudication.go's runVerifierAdjudication) and consumed by
// exactly one (resolveTurnWorkDirOrRefuse above), so it has no reason to
// live in pkg/tools alongside WithVerifierSessionScope (which IS consumed
// outside pkg/agent, by the inspect_session tool).
type systemAgentWorkspaceOverrideCtxKey struct{}

// WithSystemAgentWorkspaceOverride carries the WORK-UNDER-REVIEW's own
// workspace id into a System Agent's turn ctx: runVerifierAdjudication is
// the sole producer, sourcing it from JudgeCriteriaInput.WorkspaceID (itself
// populated by JudgeCriteria's three callers from task.Task.WorkspaceID /
// plan.Plan.WorkspaceID / the chat turn's own channel-bound
// processOptions.WorkspaceID — see each call site's own comment).
// resolveTurnWorkDirOrRefuse is the sole consumer, where it is merged into
// the SAME preferredWsID/optWorkspaceID selector every other turn already
// uses — the Judge, now an implicit member of every workspace (pkg/workspace's
// isImplicitMember), needs this to pick WHICH one, exactly like an ordinary
// agent that belongs to more than one workspace's CoreTeam picks via its
// turn's own channel-bound workspace id.
//
// An empty workspaceID is a no-op — ctx is returned unmodified — mirroring
// tools.WithVerifierSessionScope's own "empty is unset" contract
// (pkg/tools/base.go), so a caller with nothing to thread (e.g. an unbound
// chat /goal) never needs its own branch: it simply falls through to
// FindForAgentPreferring's ordinary sorted-first pick among the Judge's
// (every) workspace, same as any other multi-membership agent.
func WithSystemAgentWorkspaceOverride(ctx context.Context, workspaceID string) context.Context {
	if strings.TrimSpace(workspaceID) == "" {
		return ctx
	}
	return context.WithValue(ctx, systemAgentWorkspaceOverrideCtxKey{}, workspaceID)
}

// systemAgentWorkspaceOverrideFromContext reads back the value
// WithSystemAgentWorkspaceOverride set, or "" if none was set.
func systemAgentWorkspaceOverrideFromContext(ctx context.Context) string {
	v, _ := ctx.Value(systemAgentWorkspaceOverrideCtxKey{}).(string)
	return v
}
