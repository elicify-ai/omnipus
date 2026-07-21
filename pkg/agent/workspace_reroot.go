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
// every caller — EXCEPT for a System Agent (see the branch below), whose
// exemption is a deliberate, documented departure from that rule, not an
// oversight.
//
// ctx carries the (optional) System Agent workspace override — see
// WithSystemAgentWorkspaceOverride below — and is otherwise unused by the
// ordinary membership-scan branch. agentHome is the caller's already-
// resolved AgentInstance.Home (ts.agent.Home / childTS.agent.Home at the two
// call sites), needed only by the System Agent branch's fallback; passed in
// rather than re-resolved here to keep this a plain, dependency-light
// function (no AgentLoop/registry access).
func resolveTurnWorkDirOrRefuse(ctx context.Context, agentID, agentHome, optWorkspaceID string) (string, error) {
	home := omnipusHome()

	// --- System Agent exemption (ADR-052 FR-011/012 product-blocker fix) ---
	//
	// System Agents (today: coreagent.IDJudge — ADR-049 D3) are
	// DELIBERATELY, PERMANENTLY excluded from every workspace's core_team
	// roster: pkg/gateway/rest_workspaces.go's validateCoreTeamMembers 400s
	// any attempt to add one ("... is a System Agent and cannot be added to
	// a workspace team roster"), and the default-workspace seeder applies
	// the identical rule. This makes the membership scan below
	// DEFINITIONALLY UNSATISFIABLE for a System Agent — not a gap that a
	// future "auto-ensure a default workspace per agent" generalization of
	// this resolver (ADR-046 §10 F2, written for CUSTOM agents that simply
	// have no membership yet) could ever close, because the exclusion here
	// is by AGENT IDENTITY/TYPE, never by omission of seeding. Before this
	// fix, EVERY verifier turn (runVerifierAdjudication's
	// al.processTaskDirect dispatch, verifier_adjudication.go) hit this gate
	// and was refused, for all three JudgeCriteria callers (task/plan/goal)
	// — a live fresh-install smoke test observed exactly this: "turn
	// refused: agent is not a member of any workspace" followed by
	// "verifier: turn failed; pausing (D7 unavailability)", meaning
	// ADR-052's whole autonomous-verification pipeline was dead in
	// production. See resolveSystemAgentTurnWorkDir's own doc comment for
	// why this is NOT simply "root at the agent's own home" (Option (i)
	// alone would silently defeat FR-012(c)'s read-only tool grant).
	if coreagent.IsSystemAgentID(coreagent.CoreAgentID(agentID)) {
		return resolveSystemAgentTurnWorkDir(ctx, home, agentID, agentHome)
	}

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

// --- System Agent workspace override (ADR-052 FR-011/012) ------------------

// systemAgentWorkspaceOverrideCtxKey is the unexported ctx key backing
// WithSystemAgentWorkspaceOverride/systemAgentWorkspaceOverrideFromContext.
// Package-local by design — this signal is produced by exactly one call site
// (verifier_adjudication.go's runVerifierAdjudication) and consumed by
// exactly one (resolveSystemAgentTurnWorkDir below), so it has no reason to
// live in pkg/tools alongside WithVerifierSessionScope (which IS consumed
// outside pkg/agent, by the inspect_session tool).
type systemAgentWorkspaceOverrideCtxKey struct{}

// WithSystemAgentWorkspaceOverride carries the WORK-UNDER-REVIEW's own
// workspace id into a System Agent's turn ctx: runVerifierAdjudication is
// the sole producer, sourcing it from JudgeCriteriaInput.WorkspaceID (itself
// populated by JudgeCriteria's three callers from task.Task.WorkspaceID /
// plan.Plan.WorkspaceID / the chat turn's own channel-bound
// processOptions.WorkspaceID — see each call site's own comment).
// resolveSystemAgentTurnWorkDir is the sole consumer.
//
// An empty workspaceID is a no-op — ctx is returned unmodified — mirroring
// tools.WithVerifierSessionScope's own "empty is unset" contract
// (pkg/tools/base.go), so a caller with nothing to thread (e.g. an unbound
// chat /goal) never needs its own branch.
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

// resolveSystemAgentTurnWorkDir resolves the work directory for a System
// Agent's turn. ADR-052's Judge/verifier conversion (runVerifierAdjudication)
// is, at present, the only real dispatcher of a System Agent's turn through
// this path; resolveTurnWorkDirOrRefuse's caller-side doc comment covers WHY
// the ordinary membership scan is skipped entirely for this agentID rather
// than retried/generalized. Two things follow from that:
//
//  1. When the engine threaded a target workspace (the ctx override set by
//     WithSystemAgentWorkspaceOverride), the turn is rooted THERE. This is
//     load-bearing, not cosmetic: ADR-052 FR-012(c) grants the verifier
//     read_file/list_directory specifically so its rubric-gated escalation
//     (FR-032) can inspect artifacts beyond the transcript window when the
//     window alone can't confirm a criterion — and those artifacts live
//     under workspaces/<wsid>/work/ (ADR-046 §1's "shared workspace" —
//     Project Instructions/shared memory room live one level up, in
//     workspaces/<wsid>/ itself, structurally unreachable from work/ per
//     the same os.Root confinement runTurn's own re-root block relies on),
//     never under the Judge's own agents/judge/ home (identity/sessions/
//     private memory only — ADR-046 §1's "per-agent directory"). Rooting
//     the turn at the Judge's own home unconditionally (Option (i) alone,
//     without this) would leave those tools structurally unable to EVER
//     reach the work being judged — silently defeating FR-012(c)'s entire
//     purpose the first time a verifier's rubric actually tried to escalate.
//  2. When no target workspace is known/resolvable (override unset — a
//     goal-scope adjudication for an unbound chat — or a stale/deleted
//     workspace id), the turn falls back to the System Agent's own home
//     directory, never a hard refusal. A verifier turn with no reachable
//     workspace still needs SOME writable scratch root for the turn
//     machinery to start at all (transcript/session bookkeeping); its
//     read-only seeded tool policy (Constraint #6 — read_file/
//     list_directory/inspect_session allow, everything else deny,
//     re-enforced every boot per ADR-052's redefined seedSystemAgents
//     invariant) is what keeps this branch safe, not the directory choice.
func resolveSystemAgentTurnWorkDir(ctx context.Context, home, agentID, agentHome string) (string, error) {
	if override := systemAgentWorkspaceOverrideFromContext(ctx); override != "" {
		wsDir, idErr := workspace.SafeWorkDir(home, override)
		if idErr != nil {
			logger.WarnCF(
				"agent",
				"system agent turn: threaded workspace override failed the traversal guard; falling back to agent home",
				map[string]any{"agent_id": agentID, "workspace_id": override, "error": idErr.Error()},
			)
		} else if mkErr := os.MkdirAll(wsDir, 0o700); mkErr != nil {
			logger.WarnCF(
				"agent",
				"system agent turn: could not create the threaded workspace's work dir; falling back to agent home",
				map[string]any{"agent_id": agentID, "workspace_id": override, "dir": wsDir, "error": mkErr.Error()},
			)
		} else {
			return wsDir, nil
		}
	}

	agentHome = strings.TrimSpace(agentHome)
	if agentHome == "" {
		logger.WarnCF(
			"agent",
			"turn refused: system agent has no resolvable home directory (and no usable workspace override)",
			map[string]any{"agent_id": agentID},
		)
		return "", fmt.Errorf("system agent has no resolvable work directory: agent_id=%s", agentID)
	}
	if mkErr := os.MkdirAll(agentHome, 0o700); mkErr != nil {
		logger.WarnCF(
			"agent",
			"turn refused: could not create system agent's home directory",
			map[string]any{"agent_id": agentID, "dir": agentHome, "error": mkErr.Error()},
		)
		return "", fmt.Errorf("system agent home dir unavailable for agent_id=%s: %w", agentID, mkErr)
	}
	return agentHome, nil
}
