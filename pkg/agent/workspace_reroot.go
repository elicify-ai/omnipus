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

	// Sign-off 14 MINOR-1 / architect F4: an explicit agent-home request
	// (WithSystemAgentAgentHomeOverride) short-circuits straight to the same
	// agent-home rooting the "genuinely no workspace exists yet" branch below
	// already expresses — requested by runVerifierAdjudication for an UNBOUND
	// chat /goal verifier turn (Scope==goal, no WorkspaceID), which has no
	// work-under-review workspace to prefer among the Judge's (every)
	// implicit memberships at all. Checked BEFORE FindForAgentPreferring so
	// it applies even when one or more workspaces DO exist (the ordinary
	// "no workspace at all" branch below only fires when none do). Gated on
	// coreagent.IsSystemAgentID exactly like that branch — an ordinary agent
	// never gets this override (WithSystemAgentAgentHomeOverride has exactly
	// one producer, runVerifierAdjudication, which only ever dispatches
	// System Agent turns).
	if systemAgentAgentHomeOverrideFromContext(ctx) && coreagent.IsSystemAgentID(coreagent.CoreAgentID(agentID)) {
		return systemAgentHomeDir(agentID, agentHome)
	}

	// D13/G-12 (E.5): a Play-resumed plan member runs in the tree that was
	// materialized from its last boundary commit, not the workspace's shared
	// work/ dir. Checked BEFORE the workspace resolution below because the
	// resume tree IS the answer for this turn — the member's workspace
	// membership was already validated when Play resolved and materialized it,
	// so re-deriving work/ here would only discard it.
	//
	// Deliberately placed AFTER the System Agent agent-home override: a
	// verifier turn adjudicating a resumed member must still root at its own
	// agent home, not at the member's resume tree.
	if resumeDir := resumeWorkDirOverrideFromContext(ctx); resumeDir != "" {
		return resumeDir, nil
	}

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
		// (pkg/workspace's FindForAgent/FindForAgentPreferring). !found for
		// one is USUALLY the pre-onboarding edge — genuinely NO workspace
		// exists yet, before a default workspace is seeded — but is not
		// exclusively that: FindForAgent's own enumeration silently skips an
		// unreadable or malformed workspace record (a corrupt/partially
		// written JSON file, a permissions problem), so a directory that
		// technically contains workspace files but none of them PARSE can
		// reach this same branch. Either way the correct response is
		// identical — fall back to the System Agent's own agent home rather
		// than refuse; every other agent keeps the hard refusal below
		// unconditionally.
		if coreagent.IsSystemAgentID(coreagent.CoreAgentID(agentID)) {
			return systemAgentHomeDir(agentID, agentHome)
		}

		logger.WarnCF(
			"agent",
			"turn refused: agent is not a member of any workspace",
			map[string]any{"agent_id": agentID},
		)
		return "", fmt.Errorf("%w: agent_id=%s", ErrAgentNotWorkspaceMember, agentID)
	}

	// workspace.EnsureWorkDir (SafeWorkDir + MkdirAll + idempotent git-evidence
	// auto-init, see its own doc comment) replaces the former two-step
	// SafeWorkDir-then-MkdirAll here so the work/-dir auto-init hook actually
	// fires on the native + external-cli dispatch path this function gates
	// for both. Both former failure branches (invalid id / MkdirAll failure)
	// already produced byte-identical wrapped-error text
	// ("workspace work dir unavailable for agent_id=%s workspace_id=%s: %w"),
	// so collapsing them to one call + one WARN loses no caller-visible
	// distinction — no call site inspects the underlying error type (both
	// resolveTurnWorkDirOrRefuse callers, loop.go and external_dispatch.go,
	// treat wsErr opaquely), and %w still chains through EnsureWorkDir's own
	// wrapping for errors.Is(err, workspace.ErrInvalidWorkspaceID) if a
	// future caller ever needs it.
	wsDir, dirErr := workspace.EnsureWorkDir(home, wsID)
	if dirErr != nil {
		logger.WarnCF(
			"agent",
			"turn refused: workspace work dir unavailable",
			map[string]any{"agent_id": agentID, "workspace_id": wsID, "error": dirErr.Error()},
		)
		return "", fmt.Errorf(
			"workspace work dir unavailable for agent_id=%s workspace_id=%s: %w",
			agentID,
			wsID,
			dirErr,
		)
	}

	return wsDir, nil
}

// systemAgentHomeDir materializes and returns a System Agent's own private
// home directory as its turn's work dir — the shared body behind BOTH
// agent-home fallback branches in resolveTurnWorkDirOrRefuse: the explicit
// WithSystemAgentAgentHomeOverride request (sign-off 14 MINOR-1 / architect
// F4) and the pre-existing "genuinely no workspace resolvable" branch. A
// single body keeps the two call sites from silently drifting apart (e.g. one
// gaining a permissions fix the other misses).
func systemAgentHomeDir(agentID, agentHome string) (string, error) {
	agentHome = strings.TrimSpace(agentHome)
	if agentHome == "" {
		return "", fmt.Errorf("system agent has no resolvable work directory: agent_id=%s", agentID)
	}
	if mkErr := os.MkdirAll(agentHome, 0o700); mkErr != nil {
		return "", fmt.Errorf("system agent home dir unavailable for agent_id=%s: %w", agentID, mkErr)
	}
	return agentHome, nil
}

// --- Play-from-commit resume tree (D13/G-12, E.5) ---------------------------

// resumeWorkDirOverrideCtxKey is the unexported ctx key backing
// WithResumeWorkDirOverride/resumeWorkDirOverrideFromContext. Package-local for
// the same reason the System Agent override keys are: one producer
// (task_executor.go's dispatch path, when the member carries a
// ResumeFromCommit) and one consumer (resolveTurnWorkDirOrRefuse above).
type resumeWorkDirOverrideCtxKey struct{}

// WithResumeWorkDirOverride carries the materialized Play-from-commit resume
// tree into a plan member's turn ctx, so the resumed turn actually RUNS in the
// restored tree instead of the workspace's shared work/ dir.
//
// This is the consumer half of D13/G-12 (E.5). PlanEngine.Play already resolves
// the member's last boundary commit and materializes a checkout at
// workspaces/<ws>/resume/<taskID> — but before this override existed nothing
// read that path back, so the directory was created and then ignored: the
// resumed member ran against the shared tree and the Judge diffed the wrong
// baseline. The override is consumed inside resolveTurnWorkDirOrRefuse, the
// gate BOTH the native and external-cli dispatch paths share, so neither can
// silently diverge from the other again.
//
// An empty dir is a no-op — ctx is returned unmodified — matching
// tools.WithTurnWorkspaceDir's own "empty is unset" contract. That is the
// ordinary case: a member with no boundary commit resumes as a fresh attempt.
func WithResumeWorkDirOverride(ctx context.Context, dir string) context.Context {
	if strings.TrimSpace(dir) == "" {
		return ctx
	}
	return context.WithValue(ctx, resumeWorkDirOverrideCtxKey{}, dir)
}

// resumeWorkDirOverrideFromContext reads back the value
// WithResumeWorkDirOverride set, or "" if none was set.
func resumeWorkDirOverrideFromContext(ctx context.Context) string {
	v, _ := ctx.Value(resumeWorkDirOverrideCtxKey{}).(string)
	return v
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
// (pkg/tools/base.go). For most scopes (task/plan, and a goal bound to a
// workspace) a caller with nothing to thread simply falls through to
// FindForAgentPreferring's ordinary sorted-first pick among the Judge's
// (every) workspace, same as any other multi-membership agent. The ONE
// exception is an UNBOUND chat /goal (Scope==goal with no WorkspaceID at
// all): runVerifierAdjudication does NOT fall through to the sorted-first
// pick for that case — it requests WithSystemAgentAgentHomeOverride instead
// (sign-off 14 MINOR-1 / architect F4), because an unbound /goal has no
// work-under-review workspace to prefer among in the first place.
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

// systemAgentAgentHomeOverrideCtxKey is the unexported ctx key backing
// WithSystemAgentAgentHomeOverride/systemAgentAgentHomeOverrideFromContext.
// Package-local for the same reason systemAgentWorkspaceOverrideCtxKey is:
// exactly one producer (verifier_adjudication.go's runVerifierAdjudication),
// exactly one consumer (resolveTurnWorkDirOrRefuse above).
type systemAgentAgentHomeOverrideCtxKey struct{}

// WithSystemAgentAgentHomeOverride marks a System Agent's turn ctx as
// explicitly agent-home-rooted rather than workspace-rooted (sign-off 14
// MINOR-1 / architect F4). runVerifierAdjudication is the sole producer,
// setting this exactly when an adjudication genuinely has no work-under-
// review workspace to prefer among the Judge's (every) implicit workspace
// memberships — an unbound chat /goal (JudgeCriteriaInput.Scope == goal,
// WorkspaceID == ""). Without this override that case would fall through to
// FindForAgentPreferring's ordinary sorted-first pick, which is a wrong
// answer in kind (there is no work-under-review workspace at all for an
// unbound /goal), not merely a low-risk one — even though it happens to be
// harmless in today's single-tenant deployments. resolveTurnWorkDirOrRefuse
// is the sole consumer: it checks this override BEFORE calling
// FindForAgentPreferring at all, so it applies even when one or more
// workspaces exist (unlike the pre-existing "no workspace exists yet"
// fallback, which only fires when FindForAgentPreferring finds none).
func WithSystemAgentAgentHomeOverride(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemAgentAgentHomeOverrideCtxKey{}, true)
}

// systemAgentAgentHomeOverrideFromContext reports whether
// WithSystemAgentAgentHomeOverride was set on ctx.
func systemAgentAgentHomeOverrideFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(systemAgentAgentHomeOverrideCtxKey{}).(bool)
	return v
}
