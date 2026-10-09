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

// EdgeModeCategory maps the delegate tool's background/task runtime parameter
// down to the trust edge's direct/task vocabulary. Task maps 1:1 to
// workspace.ModeTask; Background maps to workspace.ModeDirect. The inverse
// translation for system-prompt advertising lives in
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
	case config.DelegationModeBackground:
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
//     inherits — no per-edge cap. The performance depth ceiling (passed as
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
	// cap (edge.Depth, nil = inherit) and the performance depth ceiling,
	// falling back to the safety-backstop default when NEITHER source
	// expresses an explicit value. resolveEffectiveDelegationDepth is also used
	// by the session launcher and delegation system-prompt builder, so the gate,
	// durable depth budget, and advertised cap are never computed independently
	// (#477, FR-D9/FR-D10).
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
//     performance depth ceiling always applies as an additional cap.
//
// FAIL-CLOSED: a graph load error, a missing workspace, or no default workspace
// all DENY — a delegation check with no readable governing graph never falls
// open.
//
// Delegation ALWAYS requires an explicit target. An empty targetAgentID (the
// LLM omitted agent_id) is REFUSED here — there is no default target and no
// implicit caller substitution (the retired FR-014 normalization). The delegate
// tool enforces the same rule at its own argument-validation step; this is
// defense-in-depth for any other caller.
//
// A self-target (target == caller) is an ORDINARY delegation: it is authorized
// by the same caller→caller edge lookup, mode and depth checks as any other
// target. A self-edge is an ordinary edge — the identity allowlist
// (workspace.PermittedSelfDelegationID, literally jim||worker) was deleted, so a
// self-edge authorizes iff it EXISTS in the workspace graph, exactly like every
// other edge. Self-delegation forks a NEW session running the same agent; the
// depth cap and turn admission remain the recursion bounds.
//
// selfAssignmentExempt controls the TASK-tool self-target case only.
//
// DO NOT call this function directly at a wiring site. Use one of the two
// intent-named wrappers instead, so the exempt value can never be flipped
// wrong (a delegate-tool site with exempt=true would wrongly allow a delegate
// self-reassignment — a security regression; a task-tool site with exempt=false
// merely denies a legitimate self-reassignment — loud and harmless, but still
// wrong):
//
//   - buildDelegationDenyCheckerForDelegate         (exempt=false) — the
//     `delegate` tool's background + await gates. delegate(agent_id=self) spawns
//     a real session, so it IS delegation and is gated by the caller→self edge
//     exactly like any other target.
//   - buildDelegationDenyCheckerForTaskReassignment (exempt=true)  — the task
//     tools: create_task / update_task. Reassigning a task to the agent that
//     already owns it is NOT delegation (no new instance is spawned), so the
//     self-target is allowed without consulting the graph.
//
// The exempt choice is a property of the CALLER (which tool wired this checker),
// NOT of `mode`. NEVER derive selfAssignmentExempt from `mode` — the two happen
// to correlate today (delegate uses background/await, tasks use task), but that
// is coincidence, not an invariant: a future delegate-in-task-mode would reopen
// the bypass if someone "simplified" by deriving the flag from the mode.
//
// performance supplies the process-wide depth cap. There is no per-agent
// delegation policy to read; the per-workspace graph is the sole authority.
//
// agentExists is an optional trailing arg (variadic, same rationale as
// findDelegationEdge's own doc comment) forwarded to findDelegationEdge
// purely to distinguish the no-edge denial's message — message-only, never
// affects the allow/deny decision itself.
func buildDelegationDenyChecker(
	currentAgentID string,
	performance config.PerformanceConfig,
	mode config.DelegationMode,
	selfAssignmentExempt bool,
	agentExists ...func(id string) bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	globalDepthCap, depthOK := configuredDelegationDepth(performance, "delegation gate (agent "+currentAgentID+")")
	if !depthOK {
		// An invalid depth limit fails CLOSED, like an unreadable graph: the
		// same error makes the delegation-awareness block render
		// cannot-delegate (wireDelegationInjectors), so block and gate agree.
		return func(_ context.Context, targetAgentID string) *tools.DelegationDenial {
			return &tools.DelegationDenial{
				Reason:        "delegation is disabled: performance.max_delegation_depth is invalid (must be >= 0)",
				Policy:        tools.DenyDepth,
				TargetAgentID: targetAgentID,
			}
		}
	}

	return func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
		// Delegation ALWAYS requires an explicit target (settled design): there
		// is no default target and no implicit caller substitution, so an empty
		// target is refused here rather than resolved to the caller. The delegate
		// tool enforces the same rule at its own argument-validation step; this is
		// defense-in-depth for any other caller.
		if targetAgentID == "" {
			return &tools.DelegationDenial{
				Reason:        "delegation requires an explicit target agent_id",
				Policy:        tools.DenyTrustSet,
				TargetAgentID: "",
			}
		}
		// Task tools only: reassigning a task to the agent that already owns it
		// is not delegation (no new instance is spawned) — allowed WITHOUT
		// consulting the graph. This is a property of the CALLER (which tool
		// wired this checker), never of `mode`.
		if selfAssignmentExempt && targetAgentID == currentAgentID {
			return nil
		}
		// Every target — including the caller's own id — is authorized by the
		// ORDINARY per-workspace edge lookup, then its mode and depth checks. A
		// self-edge is an ordinary edge: self-delegation forks a NEW session
		// running the same agent and is gated exactly like any other delegation.
		// There is no identity allowlist (PermittedSelfDelegationID was deleted);
		// a self-edge authorizes iff it EXISTS in the workspace graph.
		edge, denial := findDelegationEdge(ctx, currentAgentID, targetAgentID, mode, agentExists...)
		if denial != nil {
			return denial
		}
		return enforceEdgeModeAndDepth(ctx, edge, currentAgentID, targetAgentID, mode, globalDepthCap)
	}
}

// buildDelegationDenyCheckerForDelegate is the wiring-site constructor for the
// `delegate` tool's direct gate. Self-targeting creates a new session and is
// permitted; depth and concurrency remain the recursion bounds.
// agentExists is an optional trailing arg (variadic — see findDelegationEdge's
// own doc comment for why): a read-only existence probe against the live
// agent registry, consulted only to distinguish "target agent doesn't exist"
// from "target agent exists but has no trust edge" in the denial MESSAGE —
// it never affects the allow/deny outcome. Omit it only in tests that don't
// care about the distinction; every production wiring site passes a real
// checker (see registerSharedTools / NewSysagentDelegationDeny).
func buildDelegationDenyCheckerForDelegate(
	currentAgentID string,
	performance config.PerformanceConfig,
	mode config.DelegationMode,
	agentExists ...func(id string) bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	return buildDelegationDenyChecker(currentAgentID, performance, mode, false, agentExists...)
}

// buildDelegationDenyCheckerForTaskReassignment is the wiring-site constructor for the
// task tools: create_task / update_task. It bakes in selfAssignmentExempt=true:
// reassigning a task to the agent that already owns it is NOT delegation (no new
// instance is spawned), so a self-target is allowed without consulting the graph.
// Non-self targets are still fully graph-gated. (The cross-workspace
// create_task_in_workspace / update_task_in_workspace path that once shared this
// constructor was retired by DEL-23.)
//
// agentExists: see buildDelegationDenyCheckerForDelegate's doc comment — same
// optional-trailing-arg, message-only distinction, same "pass a real checker
// in production" expectation.
func buildDelegationDenyCheckerForTaskReassignment(
	currentAgentID string,
	performance config.PerformanceConfig,
	mode config.DelegationMode,
	agentExists ...func(id string) bool,
) func(ctx context.Context, targetAgentID string) *tools.DelegationDenial {
	return buildDelegationDenyChecker(currentAgentID, performance, mode, true, agentExists...)
}

// NewSysagentDelegationDeny and NewSysagentBashPolicyResolver were DELETED with
// the four *_in_workspace task tools (DEL-23): each existed ONLY to feed
// systools.Deps.DelegationDeny / Deps.ResolveBashPolicy for those tools. The
// canonical task family (pkg/tools create_task/update_task/list_tasks) enforces
// the same delegation and bash-policy gates at construction (loop_wire.go), so
// there is no sysagent-side producer left to build.
