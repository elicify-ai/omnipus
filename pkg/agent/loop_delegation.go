// loop_delegation.go: Workspace delegation edges and deny checkers

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// currentDelegationDepth reports the delegation-chain depth of the turn that is
// about to delegate, read from the turnState carried on ctx. The root user turn
// has depth 0; each nested sub-turn increments it. Returns 0 when no turnState is
// present (e.g. ad-hoc/raw invocations or tests) — a conservative default that
// never spuriously trips the depth cap.
func currentDelegationDepth(ctx context.Context) int {
	if ts := turnStateFromContext(ctx); ts != nil {
		return ts.depth
	}
	return 0
}

// agentExistsChecker builds the read-only registry existence probe threaded
// through the delegation-deny checkers so a denial can say "agent not found"
// instead of the generic trust-set message when the named target was never a
// real agent at all. Consulted for message text ONLY — never for the
// allow/deny decision.
//
// A nil registry (should not happen in production; defensive only) returns
// nil rather than a closure that would falsely report every id as
// nonexistent — a probe that lies "does not exist" about an agent that may
// in fact exist is worse than no probe at all. The nil return collapses the
// nil-registry path onto the SAME generic "not permitted" fallback the
// caller would have hit by omitting the variadic arg entirely, instead of
// fabricating a misleading "does not exist" message.
func agentExistsChecker(registry *AgentRegistry) func(id string) bool {
	if registry == nil {
		return nil
	}
	return func(id string) bool {
		if _, ok := registry.GetAgent(id); ok {
			return true
		}
		// Fall back to the durable entity store before concluding the agent
		// is genuinely nonexistent. The in-memory registry this probe
		// consults is only refreshed by the reload pipeline (the async,
		// fire-and-forget gateway.go reloadTrigger for a plain hot-reload;
		// UpsertAgentFast for create/update's fast path, which itself
		// defers to that same async reload when one is already in flight —
		// see UpsertAgentFastFunc's own doc comment), so an agent whose
		// entity record was JUST durably written (agentstore.Store.Create
		// always runs synchronously before either publish path — see
		// UpsertAgentFast's DEFECT 1 fix comment in registry.go, which
		// establishes this exact "ask the durable entity store, not the
		// possibly-stale in-memory view" precedent) can be real on disk
		// before the registry catches up. Without this fallback, a
		// delegate/switch_agent call landing in that window reports the
		// misleading "agent %q does not exist" — masking the actual denial
		// reason (e.g. a missing trust edge) a UAT run observed when the
		// target agent, in fact, existed. Best-effort: a store read error
		// here is treated the same as "not found" (the pre-existing
		// behavior for a target that genuinely never existed) rather than
		// failing the whole delegation check — this probe is message-only
		// and never controls the allow/deny outcome (see this function's
		// own callers' doc comments).
		_, err := agentstore.New(omnipusHome()).Get(id)
		return err == nil
	}
}

// resolveEffectiveWorkspaceID resolves the workspace whose delegation graph
// governs the current turn. Every delegation check resolves to exactly one
// workspace:
//
//  1. the workspace bound to the turn (tools.ToolWorkspaceID), when present; else
//  2. the is_default workspace ("My Workspace"), resolved fresh from disk.
//
// It returns ("", denial) when NO workspace can be resolved at all — neither a
// bound workspace nor an is_default one exists (should not happen post-seed).
// This is a FAIL-CLOSED path: a delegation check with no governing graph DENIES
// rather than falling open. The returned denial carries a trust_set reason and
// the requested target (when one was named).
func resolveEffectiveWorkspaceID(ctx context.Context, targetAgentID string) (string, *tools.DelegationDenial) {
	wsID := tools.ToolWorkspaceID(ctx)
	if wsID == "" {
		// Default to My Workspace (the is_default workspace) so the delegation
		// graph is ALWAYS consulted — never an implicit allow.
		def, err := workspace.ResolveDefaultID(omnipusHome())
		if err != nil || def == "" {
			logger.WarnCF("agent", "delegation denied: no workspace to evaluate against", map[string]any{
				"target": targetAgentID, "error": errString(err),
			})
			return "", &tools.DelegationDenial{
				Reason: "delegation cannot be authorized: no workspace is bound to this turn " +
					"and no default workspace exists to consult its delegation graph",
				Policy:        tools.DenyTrustSet,
				TargetAgentID: targetAgentID,
			}
		}
		wsID = def
	}
	return wsID, nil
}

// findDelegationEdge loads the effective workspace's delegation graph and returns
// the edge authorizing caller→target, or a *DelegationDenial on any failure.
// FAIL-CLOSED: a graph load error, a missing/unreadable workspace, or the absence
// of a caller→target edge all DENY (trust_set). The graph is read per-call, so an
// edit to the workspace graph takes effect on the next turn with no agent rebuild.
//
// agentExists is an OPTIONAL trailing arg (variadic so the many pre-existing
// call sites — production and test — that predate this distinction keep
// compiling unchanged), consulted ONLY to distinguish the no-edge denial's
// MESSAGE. Without it, delegating to an agent that EXISTS but has no trust
// edge from the caller, and delegating to a genuinely NONEXISTENT agent,
// both returned byte-identical generic denial text — making a typo'd
// agent_id indistinguishable from a real permissions gap. It never changes
// the allow/deny OUTCOME — both cases still deny with Policy: DenyTrustSet —
// it only selects which of the two messages below is returned. Omitting it
// (or passing nil) falls back to the pre-existing generic "not permitted"
// message — every production wiring site (registerSharedTools,
// NewSysagentDelegationDeny, both in loop.go) passes a real checker.
//
// Returns (edge, nil) when an authorizing edge exists; (nil, denial) otherwise.
func findDelegationEdge(
	ctx context.Context,
	callerAgentID, targetAgentID string,
	mode config.DelegationMode,
	agentExists ...func(id string) bool,
) (*workspace.DelegationEdge, *tools.DelegationDenial) {
	var exists func(string) bool
	if len(agentExists) > 0 {
		exists = agentExists[0]
	}
	wsID, denial := resolveEffectiveWorkspaceID(ctx, targetAgentID)
	if denial != nil {
		return nil, denial
	}

	edges, err := workspace.ReadDelegation(omnipusHome(), wsID)
	if err != nil {
		// FAIL-CLOSED: never fall open on a security check. An unreadable graph
		// is a closed graph.
		logger.WarnCF("agent", "delegation denied: workspace delegation graph unreadable", map[string]any{
			"agent_id": callerAgentID, "target": targetAgentID, "workspace_id": wsID,
			"mode": string(mode), "error": err.Error(),
		})
		return nil, &tools.DelegationDenial{
			Reason: fmt.Sprintf(
				"delegation cannot be authorized: workspace %q delegation graph is unreadable",
				wsID,
			),
			Policy:        tools.DenyTrustSet,
			TargetAgentID: targetAgentID,
		}
	}

	for i := range edges {
		if edges[i].FromAgent == callerAgentID && edges[i].ToAgent == targetAgentID {
			e := edges[i]
			return &e, nil
		}
	}

	if exists != nil && !exists(targetAgentID) {
		logger.WarnCF("agent", "delegation denied: target agent does not exist", map[string]any{
			"agent_id": callerAgentID, "target": targetAgentID, "workspace_id": wsID, "mode": string(mode),
		})
		return nil, &tools.DelegationDenial{
			Reason:        fmt.Sprintf("agent %q does not exist", targetAgentID),
			Policy:        tools.DenyTrustSet,
			TargetAgentID: targetAgentID,
		}
	}

	logger.WarnCF("agent", "delegation denied: no edge in workspace graph", map[string]any{
		"agent_id": callerAgentID, "target": targetAgentID, "workspace_id": wsID, "mode": string(mode),
	})
	return nil, &tools.DelegationDenial{
		Reason: fmt.Sprintf(
			"delegation to agent %q is not permitted in this workspace",
			targetAgentID,
		),
		Policy:        tools.DenyTrustSet,
		TargetAgentID: targetAgentID,
	}
}

// EdgeModeCategory maps the delegate tool's real 3-value runtime parameter
// (config.DelegationMode: Await/Background/Task) down to the trust edge's
// collapsed 2-value vocabulary (workspace.DelegationMode: Direct/Task). Task
// maps 1:1 to workspace.ModeTask; both Await and Background — the sync-vs-async
// choice is a delegate-tool call parameter, not something the trust edge gates
// separately — map to workspace.ModeDirect. This is the single authority for
// that collapse at the enforcement gate; the inverse expansion (Direct back to
// both Await and Background for system-prompt advertising) lives in
// wireDelegationInjectors (pkg/agent/loop_env.go), and defaultWorkspaceDelegationEdges
// (pkg/gateway/rest_workspace_delegation.go) calls this function directly for
// the collapse-on-seed case — pkg/gateway already imports pkg/agent extensively
// (gateway.go, rest.go, rest_auth.go, ...), so there is no package-boundary
// reason to duplicate this logic there; exported (not unexported) specifically
// so that call site can reuse it instead of re-implementing the collapse.
//
// The switch below is deliberately exhaustive over the CLOSED 3-value
// config.DelegationMode enum (Await/Background/Task) rather than an if/else on
// the Task case with an implicit "else -> Direct" fallthrough: an if/else
// fallback is invisible to golangci's exhaustive linter (which only inspects
// real switch statements over enum-typed values) and would silently collapse
// any future 4th mode value into ModeDirect with no signal anywhere. The
// default case below keeps that same safe collapse (ModeDirect is the
// currently-correct behavior per the closed 3-value enum) but makes an
// unrecognized value NOISY via a warning log instead of silent, matching this
// file's existing convention of logging every denial/exceptional path.
func EdgeModeCategory(mode config.DelegationMode) workspace.DelegationMode {
	switch mode {
	case config.DelegationModeTask:
		return workspace.ModeTask
	case config.DelegationModeAwait, config.DelegationModeBackground:
		return workspace.ModeDirect
	default:
		logger.WarnCF("agent",
			"EdgeModeCategory: unrecognized config.DelegationMode value, defaulting to ModeDirect category",
			map[string]any{"mode": string(mode)},
		)
		return workspace.ModeDirect
	}
}

// enforceEdgeModeAndDepth applies the modes and depth constraints of a matched
// delegation edge. Returns nil when the delegation is permitted, or a
// *DelegationDenial (mode / depth) otherwise.
//
//   - modes: empty edge.Modes ⇒ all modes allowed; otherwise the current mode's
//     CATEGORY (via EdgeModeCategory — Await/Background both collapse to
//     Direct, Task stays Task) MUST be in edge.Modes. This is category
//     membership, not a raw string/cast comparison: edge.Modes uses the
//     collapsed 2-value workspace.DelegationMode vocabulary while mode is the
//     tool's real 3-value config.DelegationMode parameter, so the two are never
//     directly comparable.
//   - depth: edge.Depth (when non-nil) is the per-edge onward-delegation cap; nil
//     inherits — no per-edge cap. The global SubTurn.MaxDepth ceiling (passed as
//     globalDepthCap, 0 = none) ALWAYS applies as an additional, independent cap.
func enforceEdgeModeAndDepth(
	ctx context.Context,
	edge *workspace.DelegationEdge,
	callerAgentID, targetAgentID string,
	mode config.DelegationMode,
	globalDepthCap int,
) *tools.DelegationDenial {
	// Modes. Empty edge.Modes ⇒ all modes allowed (handled by the len > 0 guard).
	// Otherwise compare the edge's collapsed vocabulary against mode's category,
	// not mode itself.
	if len(edge.Modes) > 0 {
		category := EdgeModeCategory(mode)
		allowed := false
		for _, m := range edge.Modes {
			if m == category {
				allowed = true
				break
			}
		}
		if !allowed {
			logger.WarnCF("agent", "delegation denied: mode not permitted by edge", map[string]any{
				"agent_id": callerAgentID, "target": targetAgentID, "mode": string(mode),
				"edge_modes": edge.Modes,
			})
			return &tools.DelegationDenial{
				Reason: fmt.Sprintf(
					"delegation mode %q is not permitted for this delegation edge in this workspace",
					string(mode),
				),
				Policy:        tools.DenyMode,
				TargetAgentID: targetAgentID,
			}
		}
	}

	// Depth. This is the runtime half of the DEPTH INVARIANT documented once on
	// workspace.DelegationEdge (the single authority): depth <= 0 ⇒ this edge
	// grants NO onward delegation; depth > 0 ⇒ onward delegation is capped at that
	// chain depth.
	//
	// A per-edge cap of 0 means "no onward delegation" — the strictest possible
	// bound. A NEGATIVE cap is never a valid "uncapped" signal: an edge that
	// reached runtime with depth < 0 (e.g. one that bypassed write-time
	// validation) MUST fail closed, not silently remove the per-edge cap. So the
	// invariant is "depth <= 0 ⇒ this edge grants no further onward delegation":
	// reject unconditionally through this edge.
	if edge.Depth != nil && *edge.Depth <= 0 {
		logger.WarnCF("agent", "delegation denied: edge forbids onward delegation (depth <= 0)", map[string]any{
			"agent_id": callerAgentID, "target": targetAgentID, "mode": string(mode),
			"edge_depth": *edge.Depth,
		})
		return &tools.DelegationDenial{
			Reason: fmt.Sprintf(
				"this delegation edge forbids onward delegation (edge depth %d)",
				*edge.Depth,
			),
			Policy:        tools.DenyDepth,
			TargetAgentID: targetAgentID,
		}
	}

	// Otherwise enforce the effective depth cap: the tighter of the per-edge
	// cap (edge.Depth, nil = inherit) and the global SubTurn.MaxDepth ceiling,
	// falling back to the safety-backstop default when NEITHER source
	// expresses an explicit value. Resolved via resolveEffectiveDelegationDepth
	// — the SAME shared function spawnSubTurn's own depth check
	// (SubTurnConfig.ResolvedMaxDepth, threaded via buildDelegationDepthResolver)
	// and the delegation system-prompt builder (wireDelegationInjectors) use, so
	// this gate's decision and the eventual spawn-time enforcement are never
	// computed independently (#477, FR-D9/FR-D10).
	depthCap := resolveEffectiveDelegationDepth(edge.Depth, globalDepthCap)
	if d := currentDelegationDepth(ctx); d >= depthCap {
		logger.WarnCF("agent", "delegation denied: max delegation depth exceeded", map[string]any{
			"agent_id": callerAgentID, "target": targetAgentID, "mode": string(mode),
			"current_depth": d, "max_depth": depthCap,
		})
		return &tools.DelegationDenial{
			Reason: fmt.Sprintf(
				"maximum delegation depth (%d) reached — cannot delegate further",
				depthCap,
			),
			Policy:        tools.DenyDepth,
			TargetAgentID: targetAgentID,
		}
	}
	return nil
}

// buildDelegationDenyChecker returns the per-workspace, graph-authoritative
// delegation gate for a targeted delegation tool (delegate with async=true =
// "background", create_task / update_task = "task"). The per-workspace
// delegation graph (workspaces/<id>.json → Delegation[] edges) is the SOLE
// runtime authority (ADR-037) — there is no separate per-agent delegation
// policy at all; it is never read here.
//
// It enforces, in order, returning the first violation (nil = allowed):
//
//  1. trust set — an edge caller→target MUST exist in the effective workspace's
//     delegation graph. No edge ⇒ DENY (trust_set). The workspace is the one
//     bound to the turn, defaulting to the is_default workspace when none is
//     bound — so a graph is ALWAYS consulted (never an implicit allow).
//  2. mode      — the tool's delegation mode's CATEGORY (via EdgeModeCategory)
//     must be in the edge's Modes (empty Modes = all allowed).
//  3. depth     — the current delegation-chain depth must be below the edge's
//     Depth cap (nil = inherit; 0 = no onward delegation). The global
//     SubTurn.MaxDepth ceiling always applies as an additional cap.
//
// FAIL-CLOSED: a graph load error, a missing workspace, or no default workspace
// all DENY — a delegation check with no readable governing graph never falls
// open.
//
// An empty targetAgentID means "no explicit target" (the LLM omitted agent_id);
// the trust check is then skipped here — untargeted spawns resolve to the default
// agent — while mode and depth (against any one of the caller's outgoing edges)
// still apply.
//
// selfAssignmentExempt controls the self-target (target == caller) case.
//
// DO NOT call this function directly at a wiring site. Use one of the two
// intent-named wrappers instead, so the exempt value can never be flipped
// wrong (a delegate-tool site with exempt=true silently reopens the
// self-delegation bypass — a security regression; a task-tool site with
// exempt=false merely denies a legitimate self-reassignment — loud and
// harmless, but still wrong):
//
//   - buildDelegationDenyCheckerForDelegate         (exempt=false) — the
//     `delegate` tool's background + await gates. delegate(agent_id=self) spawns
//     a real sub-turn instance, so it IS delegation and is graph-gated (and, for
//     self, ALWAYS denied — see below).
//   - buildDelegationDenyCheckerForTaskReassignment (exempt=true)  — the task
//     tools: create_task / update_task AND the cross-workspace
//     create_task_in_workspace / update_task_in_workspace via
//     NewSysagentDelegationDeny. Reassigning a task to the agent that already
//     owns it is NOT delegation (no new instance is spawned), so it is allowed
//     without consulting the graph.
//
// The exempt choice is a property of the CALLER (which tool wired this checker),
// NOT of `mode`. NEVER derive selfAssignmentExempt from `mode` — the two happen
// to correlate today (delegate uses background/await, tasks use task), but that
// is coincidence, not an invariant: a future delegate-in-task-mode would reopen
// the bypass if someone "simplified" by deriving the flag from the mode.
//
// When exempt is false, a self-target is DENIED directly here (defense-in-depth),
// without relying solely on workspace.DelegationEdge.Validate's self-edge
// prohibition as the guard, and with a distinct reason so the caught bypass
// attempt is distinguishable from a routine trust_set denial.
//
// defaults is only consulted for its SubTurn.MaxDepth global depth cap — there
// is no per-agent config.DelegationPolicy to read anymore (ADR-037); the
// per-workspace graph is the sole authority.
//
// agentExists is an optional trailing arg (variadic, same rationale as
// findDelegationEdge's own doc comment) forwarded to findDelegationEdge
// purely to distinguish the no-edge denial's message — message-only, never
// affects the allow/deny decision itself.
func buildDelegationDenyChecker(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
	selfAssignmentExempt bool,
	agentExists ...func(id string) bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	globalDepthCap := defaults.SubTurn.MaxDepth

	return func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
		if targetAgentID == currentAgentID {
			// Self-target. For the task tools (exempt=true) this is a no-op
			// reassignment to the task's existing owner, not delegation — allow.
			if selfAssignmentExempt {
				return nil
			}
			// Jim and General Purpose may fork bounded helpers only through the
			// same explicit workspace edge, mode and depth checks as other targets.
			if workspace.PermittedSelfDelegationID(currentAgentID) {
				edge, denial := findDelegationEdge(ctx, currentAgentID, targetAgentID, mode, agentExists...)
				if denial != nil {
					return denial
				}
				return enforceEdgeModeAndDepth(ctx, edge, currentAgentID, targetAgentID, mode, globalDepthCap)
			}
			// Every other identity remains denied. Deny directly instead of
			// falling through to findDelegationEdge and relying on the graph's
			// self-edge prohibition (DelegationEdge.Validate) as the sole guard. A
			// distinct reason + log distinguishes this caught self-delegation
			// bypass attempt from a routine "target not trusted" trust_set denial.
			logger.WarnCF("agent", "delegation denied: self-delegation is not permitted", map[string]any{
				"agent_id": currentAgentID, "target": targetAgentID, "mode": string(mode),
			})
			return &tools.DelegationDenial{
				Reason: fmt.Sprintf(
					"an agent cannot delegate to itself (%q): self-delegation is never permitted",
					currentAgentID,
				),
				Policy:        tools.DenyTrustSet,
				TargetAgentID: targetAgentID,
			}
		}

		if targetAgentID != "" {
			// Targeted delegation: require an authorizing edge, then enforce its
			// modes + depth.
			edge, denial := findDelegationEdge(ctx, currentAgentID, targetAgentID, mode, agentExists...)
			if denial != nil {
				return denial
			}
			return enforceEdgeModeAndDepth(ctx, edge, currentAgentID, targetAgentID, mode, globalDepthCap)
		}

		// Untargeted (agent_id omitted): trust is "can delegate at all" — the
		// caller must have at least one outgoing edge that permits this mode.
		// Mode + depth still apply against that edge.
		return evalUntargetedDelegation(ctx, currentAgentID, mode, globalDepthCap)
	}
}

// buildDelegationDenyCheckerForDelegate is the wiring-site constructor for the
// `delegate` tool's background and await gates. It bakes in selfAssignmentExempt=false:
// delegate(agent_id=self) spawns a real sub-turn instance, so it IS delegation and a
// self-target is ALWAYS denied. Use this — never the raw core with a literal false —
// so the security-critical exempt value can never be flipped wrong at a call site.
// agentExists is an optional trailing arg (variadic — see findDelegationEdge's
// own doc comment for why): a read-only existence probe against the live
// agent registry, consulted only to distinguish "target agent doesn't exist"
// from "target agent exists but has no trust edge" in the denial MESSAGE —
// it never affects the allow/deny outcome. Omit it only in tests that don't
// care about the distinction; every production wiring site passes a real
// checker (see registerSharedTools / NewSysagentDelegationDeny).
func buildDelegationDenyCheckerForDelegate(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
	agentExists ...func(id string) bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	return buildDelegationDenyChecker(currentAgentID, defaults, mode, false, agentExists...)
}

// buildDelegationDenyCheckerForTaskReassignment is the wiring-site constructor for the
// task tools: create_task / update_task and the cross-workspace
// create_task_in_workspace / update_task_in_workspace (via NewSysagentDelegationDeny).
// It bakes in selfAssignmentExempt=true: reassigning a task to the agent that already
// owns it is NOT delegation (no new instance is spawned), so a self-target is allowed
// without consulting the graph. Non-self targets are still fully graph-gated.
//
// agentExists: see buildDelegationDenyCheckerForDelegate's doc comment — same
// optional-trailing-arg, message-only distinction, same "pass a real checker
// in production" expectation.
func buildDelegationDenyCheckerForTaskReassignment(
	currentAgentID string,
	defaults config.AgentDefaults,
	mode config.DelegationMode,
	agentExists ...func(id string) bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	return buildDelegationDenyChecker(currentAgentID, defaults, mode, true, agentExists...)
}

// evalUntargetedDelegation gates an untargeted delegation (no explicit target)
// against the caller's outgoing edges in the effective workspace graph. It allows
// iff the caller has AT LEAST ONE outgoing edge whose modes permit the current
// mode (and whose depth cap is not exceeded). FAIL-CLOSED on graph load failure.
func evalUntargetedDelegation(
	ctx context.Context,
	callerAgentID string,
	mode config.DelegationMode,
	globalDepthCap int,
) *tools.DelegationDenial {
	wsID, denial := resolveEffectiveWorkspaceID(ctx, "")
	if denial != nil {
		return denial
	}
	edges, err := workspace.ReadDelegation(omnipusHome(), wsID)
	if err != nil {
		logger.WarnCF("agent", "delegation denied: workspace delegation graph unreadable", map[string]any{
			"agent_id": callerAgentID, "workspace_id": wsID, "mode": string(mode), "error": err.Error(),
		})
		return &tools.DelegationDenial{
			Reason: fmt.Sprintf(
				"delegation cannot be authorized: workspace %q delegation graph is unreadable",
				wsID,
			),
			Policy: tools.DenyTrustSet,
		}
	}

	// Find any outgoing edge that permits this mode and whose depth is OK.
	var firstModeDenial, firstDepthDenial *tools.DelegationDenial
	for i := range edges {
		if edges[i].FromAgent != callerAgentID {
			continue
		}
		e := edges[i]
		if d := enforceEdgeModeAndDepth(ctx, &e, callerAgentID, "", mode, globalDepthCap); d != nil {
			switch d.Policy {
			case tools.DenyMode:
				if firstModeDenial == nil {
					firstModeDenial = d
				}
			case tools.DenyDepth:
				if firstDepthDenial == nil {
					firstDepthDenial = d
				}
			}
			continue
		}
		return nil // an edge permits this delegation
	}

	// No edge permitted it. Surface the most specific reason: a mode/depth
	// denial if an edge existed but was constrained, else trust_set (no edge).
	if firstModeDenial != nil {
		return firstModeDenial
	}
	if firstDepthDenial != nil {
		return firstDepthDenial
	}
	logger.WarnCF("agent", "delegation denied: caller has no outgoing edge", map[string]any{
		"agent_id": callerAgentID, "workspace_id": wsID, "mode": string(mode),
	})
	return &tools.DelegationDenial{
		Reason: "this agent has no permitted delegation target in this workspace",
		Policy: tools.DenyTrustSet,
	}
}

// NewSysagentDelegationDeny returns a delegation-deny resolver suitable for the
// systools.Deps.DelegationDeny hook. The sysagent task tools are registered ONCE
// on a central registry (not per-agent), so they cannot bind a per-agent checker
// at construction the way the plain task tools do in NewAgentLoop. Instead this
// resolver builds the per-workspace, graph-authoritative task-mode delegation
// gate dynamically at Execute time and evaluates the requested target.
//
// This closes the §4 behavioral-parity gap: create_task_in_workspace /
// update_task_in_workspace must enforce the SAME delegation policy the plain
// create_task / update_task tools enforce. The cross-workspace surface is the
// PRIVILEGED Orchestrator path, so it must be at least as restrictive — never
// less — than the same-workspace path.
//
// The graph is the authority (workspaces/<id>.json → Delegation[] edges); the
// per-agent config is no longer consulted. A graph load failure or a missing
// workspace DENIES (fail-closed) inside buildDelegationDenyChecker.
func (al *AgentLoop) NewSysagentDelegationDeny() func(ctx context.Context, callerAgentID, targetAgentID string) *tools.DelegationDenial {
	return func(ctx context.Context, callerAgentID, targetAgentID string) *tools.DelegationDenial {
		// Self-assignment / untargeted is a no-op reassignment, not delegation, and
		// is allowed before touching the graph. This mirrors the exempt=true
		// self-target short-circuit inside buildDelegationDenyCheckerForTaskReassignment
		// (used below) and additionally covers the empty-target case (which the gate
		// would otherwise route through evalUntargetedDelegation). The cross-workspace
		// tools always supply a concrete target on a real reassignment.
		if targetAgentID == "" || targetAgentID == callerAgentID {
			return nil
		}
		var defaults config.AgentDefaults
		if cfg := al.GetConfig(); cfg != nil {
			defaults = cfg.Agents.Defaults
		}
		// ForTaskReassignment (exempt=true): these are the cross-workspace TASK tools
		// (create_task_in_workspace / update_task_in_workspace) — a self-target is a
		// no-op task reassignment, not delegation (also short-circuited above).
		gate := buildDelegationDenyCheckerForTaskReassignment(
			callerAgentID, defaults, config.DelegationModeTask, agentExistsChecker(al.GetRegistry()),
		)
		return gate(ctx, targetAgentID)
	}
}

// NewSysagentBashPolicyResolver builds the systools.Deps.ResolveBashPolicy
// closure (ADR-049 D2 rule 5, FR-017/052, review r1 major M5): resolves an
// assignee agent's effective "bash" tool policy from the SAME live registry
// judge.go's runMachineCheck and the plain create_task tool's own
// bashPolicyChecker use (tools.EffectiveToolPolicy, ScopeCore) — parity
// between the same-workspace and cross-workspace (create_task_in_workspace)
// task-creation surfaces.
func (al *AgentLoop) NewSysagentBashPolicyResolver() func(assigneeAgentID string) (policy string, ok bool) {
	return func(assigneeAgentID string) (policy string, ok bool) {
		agentInst, found := al.GetRegistry().GetAgent(assigneeAgentID)
		if !found || agentInst == nil {
			return "", false
		}
		return tools.EffectiveToolPolicy(agentInst.LoadToolPolicy(), tools.ScopeCore, agentInst.AgentType, "bash"), true
	}
}
