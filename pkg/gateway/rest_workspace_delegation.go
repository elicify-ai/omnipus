// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// delegationDepthCeilingFallback mirrors agent.defaultMaxSubTurnDepth (the
// subturn depth cap, default 3) for the case where the live config does not set
// agents.defaults.subturn.max_depth. The agent package's constant is unexported
// and pkg/gateway must not import pkg/agent's internals, so the ceiling is taken
// from the configured SubTurn.MaxDepth (see delegationDepthCeiling) and only
// falls back to this literal when that knob is unset (<= 0) — the exact same
// fallback the agent loop applies in getSubTurnConfig.
//
// Relocated from the now-deleted rest_agent_delegation.go (ADR-037, Wave 2) —
// this helper is not delegation-*policy*-specific, it is a shared depth-ceiling
// lookup consumed by handleWorkspaceDelegationPut (this file, validating an
// operator-submitted edge's depth) and by workspace create/team validation
// (rest_workspaces.go, x2). NOT consumed by defaultWorkspaceDelegationEdges
// below — that function seeds edges from the fixed coreagent matrix and
// copies each seeded Depth verbatim, with no ceiling clamp (the coreagent
// seed data is trusted, hardcoded Go, not operator input).
const delegationDepthCeilingFallback = 3

// delegationDepthCeiling returns the effective maximum delegation chain depth a
// caller may request, reusing the global subturn depth cap rather than inventing
// a new constant. It tracks getSubTurnConfig: the configured
// agents.defaults.subturn.max_depth when set (> 0), else the default of 3.
func delegationDepthCeiling(cfg *config.Config) int {
	if cfg != nil && cfg.Agents.Defaults.SubTurn.MaxDepth > 0 {
		return cfg.Agents.Defaults.SubTurn.MaxDepth
	}
	return delegationDepthCeilingFallback
}

// delegationEdgeToWire converts a storedDelegationEdge to the generated wire type.
func delegationEdgeToWire(e storedDelegationEdge) gen.WorkspaceDelegationEdge {
	out := gen.WorkspaceDelegationEdge{
		FromAgent: e.FromAgent,
		ToAgent:   e.ToAgent,
	}
	if len(e.Modes) > 0 {
		// []DelegationMode → the generated wire enum. DelegationMode is a string
		// type, so this is a plain string cast — the wire shape stays a string array.
		modes := make([]gen.WorkspaceDelegationEdgeModes, 0, len(e.Modes))
		for _, m := range e.Modes {
			modes = append(modes, gen.WorkspaceDelegationEdgeModes(string(m)))
		}
		out.Modes = &modes
	}
	if e.Depth != nil {
		d := *e.Depth
		out.Depth = &d
	}
	return out
}

// workspaceDelegationToWire builds the GET/PUT response body for a workspace's
// delegation graph. The team node set is the union of the workspace core_team and
// every agent referenced by an edge, sorted for stable output. defaultDepth is
// the already-computed depth ceiling (delegationDepthCeiling(cfg)) an edge
// inherits when its own depth is unset — no new depth logic here, purely
// exposing that existing value so the frontend can always pre-fill/display a
// concrete number instead of an ambiguous blank/"∞" state.
//
// FIX 4 (7-reviewer gate, TeamSet derivation drift): the team union used to be
// re-derived inline here (no TrimSpace, no empty-guard), which could diverge
// from validateTaskAgentID's team check (rest_tasks.go), the ONLY other
// consumer of workspace team membership — that one already goes through the
// canonical workspaceTeamSet(ws) wrapper over workspace.TeamSet. The frontend
// now treats this wire response's team[] as load-bearing (Team tab), so a
// silent divergence here would show a team list that doesn't match what
// task-assignment validation actually enforces. Sourcing from the same
// wrapper closes that gap — this is now the SAME derivation as
// validateTaskAgentID's, not a parallel one.
//
// stored is the workspace's edge list as read from the DELEGATION STORE
// (workspace.LoadDelegation), never a field on the workspace record — see
// pkg/workspace/delegationstore.go. It is passed in rather than loaded here so
// the PUT handler can render the set it just persisted without a re-read.
func workspaceDelegationToWire(
	ws storedWorkspace, stored []storedDelegationEdge, defaultDepth int,
) gen.WorkspaceDelegation {
	edges := make([]gen.WorkspaceDelegationEdge, 0, len(stored))
	for _, e := range stored {
		edges = append(edges, delegationEdgeToWire(e))
	}
	teamSet := workspace.TeamSet(ws.CoreTeam, stored)
	team := make([]string, 0, len(teamSet))
	for id := range teamSet {
		team = append(team, id)
	}
	sort.Strings(team)

	out := gen.WorkspaceDelegation{
		WorkspaceId:  ws.ID,
		Edges:        edges,
		DefaultDepth: defaultDepth,
	}
	if len(team) > 0 {
		out.Team = &team
	}
	return out
}

// handleWorkspaceDelegationGet returns the workspace's delegation graph.
// GET /api/v1/workspaces/{id}/delegation
func (a *restAPI) handleWorkspaceDelegationGet(w http.ResponseWriter, _ *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	ws, ok := a.loadWorkspace(w, id)
	if !ok {
		return
	}
	// Edges come from the delegation store, not the workspace record (see
	// pkg/workspace/delegationstore.go). An untrusted store record is a 500,
	// never an empty graph: rendering "no edges" for a corrupt/tampered record
	// would invite the operator to "fix" it by saving over it, silently
	// destroying the real graph and hiding the tampering.
	stored, storeOK := workspace.LoadDelegation(a.homePath, id)
	if !storeOK {
		slog.Error("rest: read workspace delegation: store record unreadable or untrusted", "id", id)
		jsonErr(w, http.StatusInternalServerError, "workspace delegation record is unreadable")
		return
	}
	cfg := a.agentLoop.GetConfig()
	jsonOK(w, workspaceDelegationToWire(ws, stored, delegationDepthCeiling(cfg)))
}

// handleWorkspaceDelegationPut replaces the workspace's delegation edge set.
// PUT /api/v1/workspaces/{id}/delegation
//
// Validation (all reject with 400):
//   - every from_agent / to_agent must be a member of the workspace team
//     (core_team ∪ existing-edge endpoints) — an edge write may NOT silently
//     expand the team with an off-team agent
//   - self-edges (from_agent == to_agent) are rejected
//   - the resulting graph must be acyclic (no A→B→A delegation cycle)
//   - modes ⊆ {direct, task}
//   - depth must be >= 0 and <= the global subturn depth ceiling
//
// Edges are deduplicated by (from_agent, to_agent); the last writer wins.
//
// The edge set is persisted to (and read from) the per-workspace DELEGATION
// STORE, $OMNIPUS_HOME/entities/delegation/<id>.json — NOT the workspace
// record. An authorization must not be writable by the principal it
// constrains, and the workspace record is (see
// pkg/workspace/delegationstore.go). This handler is one of the two sanctioned
// writers; the other is the update_workspace tool's new-member auto-seed.
//
// This graph IS the runtime authority — the ONLY delegation-enforcement
// mechanism (ADR-037). Per-workspace delegation enforcement reads these edges
// directly at delegation time (see workspace.ReadDelegation +
// buildDelegationDenyChecker in pkg/agent/loop.go): a delegation caller→target is
// permitted ONLY when a matching edge exists in the governing workspace's graph
// (with the edge's modes/depth applied). coreagent's seeded trust graph
// (coreagent.SeedDelegationEdges) is consulted ONLY to bootstrap each new
// workspace's initial graph via defaultWorkspaceDelegationEdges — it plays no
// role at delegation-enforcement time. Because the checker reads the graph
// per-call, editing this graph takes effect on the NEXT turn — no agent
// rebuild or reload is required.
func (a *restAPI) handleWorkspaceDelegationPut(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	var req gen.WorkspaceDelegationUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "WorkspaceDelegationUpdateRequest", &req, validateEnabled) {
		return
	}

	// Serialize the full load-modify-write cycle against this workspace
	// ID (see the matching comment on handleWorkspacePut) so a racing kickoff
	// consume, PUT, or delete cannot interleave with this delegation write.
	unlock := workspace.LockID(id)
	defer unlock()

	ws, ok := a.loadWorkspace(w, id)
	if !ok {
		return
	}

	// The EXISTING edge set comes from the delegation store, not the workspace
	// record. An untrusted store record fails the write closed rather than
	// deriving the team from a graph we cannot read (which would silently
	// narrow the team to core_team and reject legitimate edges for a reason the
	// operator can neither see nor act on). Repair is to remove the corrupt
	// entities/delegation/<id>.json and re-save from the Team tab.
	existing, storeOK := workspace.LoadDelegation(a.homePath, id)
	if !storeOK {
		slog.Error("rest: update workspace delegation: store record unreadable or untrusted", "id", id)
		jsonErr(w, http.StatusInternalServerError, "workspace delegation record is unreadable")
		return
	}

	cfg := a.agentLoop.GetConfig()
	// Validate edge endpoints against the workspace TEAM (core_team ∪ existing-edge
	// endpoints), not the whole config roster — an edge write must not silently
	// add an off-team agent to the workspace team. team ⊆ roster by construction.
	team := workspace.TeamSet(ws.CoreTeam, existing)
	ceiling := delegationDepthCeiling(cfg)

	edges, errMsg := buildWorkspaceDelegationEdges(req.Edges, team, ceiling)
	if errMsg != "" {
		jsonErr(w, http.StatusBadRequest, errMsg)
		return
	}

	// Persist to the delegation store. LockID(id) is already held above, which
	// is SaveDelegation's stated caller contract.
	if err := workspace.SaveDelegation(a.homePath, id, edges); err != nil {
		slog.Error("rest: update workspace delegation", "error", err, "id", id)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(a.homePath, ws); err != nil {
		slog.Error("rest: update workspace delegation: touch record", "error", err, "id", id)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    "workspace.delegation.update",
			Decision: audit.DecisionAllow,
			Details:  map[string]any{"id": ws.ID, "edge_count": len(edges)},
		}); err != nil {
			slog.Warn("audit write failed", "event", "workspace.delegation.update", "id", ws.ID, "error", err)
		}
	}

	jsonOK(w, workspaceDelegationToWire(ws, edges, ceiling))
}

// defaultWorkspaceDelegationEdges derives the seed delegation graph for a new
// workspace directly from coreagent's seeded trust graph (ADR-037, Wave 2).
// Each core agent ID's coreagent.SeedDelegationEdges result becomes one edge
// per target, carrying that policy's modes and depth. This keeps a single
// source of truth: coreagent's seeded trust graph (Jim→Ava/Ray/worker,
// Mia/Ray/Ava→worker, the Planner→Explorer/Researcher specialist edges) is
// replayed onto the workspace graph so a fresh workspace works out of the box.
// Remote-a2a refs and wildcard ("*") refs are skipped — they have no concrete
// in-roster node to draw an edge to.
//
// Before ADR-037 this read ac.DelegationPolicy off cfg.Agents.List — a field
// that could, in principle, have been hand-edited via the now-retired global
// /agents/trust screen. That screen never affected runtime enforcement (the
// per-workspace graph always has), so reading the fixed coreagent seed data
// directly here (rather than a field that no longer exists) produces the
// IDENTICAL bootstrap graph for any config that has not gone through that
// screen — i.e. every fresh install — which is the only case this function's
// output is required to match byte-for-byte (see
// TestDefaultWorkspaceDelegationEdges_MatchesCoreagentSeed).
//
// KNOWN, NARROW EXCEPTION (7-reviewer-gate follow-up to ADR-037,
// silent-failure-hunter): an operator who, pre-upgrade, actually hand-edited
// an agent's DelegationPolicy.To via the (decorative-for-enforcement, but
// NOT decorative-for-seeding) /agents/trust screen — e.g. widened or
// narrowed Jim's default targets before this upgrade — will silently lose
// that customization's effect on any workspace created AFTER upgrading:
// this function now always seeds from the fixed coreagent matrix, never from
// whatever the operator last wrote to that field. This is a real, if narrow,
// deviation from ADR-037's "no behavior change" framing, which covered
// runtime *enforcement* only — it did not account for this seed-into-
// NEW-workspace path specifically. Existing workspaces created before the
// upgrade are entirely unaffected (their persisted Delegation[] edges are
// untouched by this function, which only runs at workspace-creation time).
// See ADR-037 §7 (Post-decision review) for the acknowledgment.
func defaultWorkspaceDelegationEdges(cfg *config.Config) []storedDelegationEdge {
	if cfg == nil {
		return nil
	}
	var edges []storedDelegationEdge
	// TestDefaultWorkspaceDelegationEdges_MatchesCoreagentSeed deliberately replays
	// this exact loop independently (not via a shared helper) so it can catch a
	// regression in THIS transformation logic; sharing a helper would make that test
	// tautological (see the test's own doc comment for the full rationale).
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		dp := coreagent.SeedDelegationEdges(coreagent.CoreAgentID(ac.ID))
		if dp == nil || len(dp.To) == 0 {
			continue
		}
		// Collapse+dedupe the coreagent seed's real 3-value tool-call vocabulary
		// down to the trust edge's 2-value vocabulary via agent.EdgeModeCategory —
		// the SAME function the enforcement gate uses (pkg/agent/loop.go) — deduped
		// so a seed listing both Await and Background (e.g. Jim's [Task,
		// Background, Await]) yields a single Direct entry, not two. pkg/gateway
		// already imports pkg/agent extensively, so there is no package-boundary
		// reason to maintain a separate copy of this collapse here.
		modes := make([]workspace.DelegationMode, 0, len(dp.Modes))
		seenMode := make(map[workspace.DelegationMode]bool, len(dp.Modes))
		for _, m := range dp.Modes {
			wm := agent.EdgeModeCategory(m)
			if seenMode[wm] {
				continue
			}
			seenMode[wm] = true
			modes = append(modes, wm)
		}
		var depth *int
		if dp.Depth != nil {
			d := *dp.Depth
			depth = &d
		}
		for _, ref := range dp.To {
			if ref.Kind != config.AgentRefKindLocal || ref.ID == "*" || ref.ID == ac.ID {
				continue
			}
			edges = append(edges, storedDelegationEdge{
				FromAgent: ac.ID,
				ToAgent:   ref.ID,
				Modes:     append([]workspace.DelegationMode(nil), modes...),
				Depth:     depth,
			})
		}
	}
	return edges
}

// defaultWorkspaceTeam returns the seed roster for the auto-created BOOT
// DEFAULT workspace ("My Workspace", ensureDefaultWorkspace) ONLY: every agent
// the product delivers on install (coreagent.All — 4 base chat agents +
// general Worker + Planner/Explorer/Researcher specialists), filtered to IDs
// that actually exist in the live config so a lite/custom roster never
// produces a dangling team member.
//
// This is NOT used for ordinary user-created workspaces any more — those seed
// via newWorkspaceSetupTeam (Ava-only + setup_pending) so Ava can interview
// the user and build out the team herself. defaultWorkspaceTeam remains
// reserved for ensureDefaultWorkspace's one-time boot seed, which still needs
// a working full roster out of the box with no interview step.
//
// Worker is intentionally included. Omitting it used to drop every →worker
// seed edge in seedEdgesForTeam (both endpoints must be on-team), so Jim/Mia/
// Ava/Ray could not delegate to Worker on a pristine default workspace even
// though coreagent.SeedDelegationEdges defines those edges. That was UAT
// DEF-001 (2026-07-13).
func defaultWorkspaceTeam(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	present := make(map[string]bool, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		present[cfg.Agents.List[i].ID] = true
	}
	// Display order follows coreagent.All() so the Team tab matches the
	// Agents library seed order (Mia first, then Jim/Ava/Ray, Worker, then
	// the three specialists).
	all := coreagent.All()
	team := make([]string, 0, len(all))
	for _, a := range all {
		id := string(a.ID)
		if present[id] {
			team = append(team, id)
		}
	}
	return team
}

// newWorkspaceSetupTeam returns the seed roster for a NEW user-created
// workspace (POST /api/v1/workspaces with no explicit core_team): just Ava,
// filtered to whether her ID is actually present in the live config (same
// presence-filter pattern as defaultWorkspaceTeam, so a lite/custom install
// that omitted Ava never produces a dangling team member).
//
// This is deliberately NOT the full install roster. New workspaces now start
// with only Ava on the team; she interviews the user and builds out the rest
// of the team herself (the setup_pending flow — handleWorkspacePost sets
// ws.SetupPending=true alongside this seed, cleared once the setup kickoff
// turn is accepted). defaultWorkspaceTeam above remains unchanged and is used
// exclusively by ensureDefaultWorkspace for the auto-created boot default
// workspace ("My Workspace"), which still gets the full roster.
func newWorkspaceSetupTeam(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	present := make(map[string]bool, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		present[cfg.Agents.List[i].ID] = true
	}
	avaID := string(coreagent.IDAva)
	if !present[avaID] {
		return nil
	}
	return []string{avaID}
}

// seedEdgesForTeam filters a set of default edges down to those whose endpoints
// are BOTH members of the given team. Used on workspace create so a custom
// core_team only inherits the default edges relevant to the agents it actually
// includes (no dangling edge to an agent the operator left out). A nil/empty team
// yields no edges.
//
// FIX 7 (7-reviewer gate, silent edge drop): a dropped edge used to leave no
// trace at all — an operator who left an agent off a fresh workspace's team
// (the exact DEF-001 scenario this function's own defaultWorkspaceTeam
// doc-comment references) would see fewer seeded edges with no logged reason.
// slog.Debug here at DROP time (not the caller) so every drop is accounted
// for regardless of which call site invokes this — this is intentionally
// routine/expected (an operator choosing a partial roster on purpose is not
// an anomaly), so Debug matches the package's existing idiom for
// "expected, narrowing" filtering decisions rather than Warn, which this
// package reserves for actual failures (e.g. audit-log write errors, file
// write errors below).
func seedEdgesForTeam(edges []storedDelegationEdge, team []string) []storedDelegationEdge {
	if len(team) == 0 || len(edges) == 0 {
		return nil
	}
	member := make(map[string]bool, len(team))
	for _, id := range team {
		member[id] = true
	}
	out := make([]storedDelegationEdge, 0, len(edges))
	for _, e := range edges {
		if member[e.FromAgent] && member[e.ToAgent] {
			out = append(out, e)
			continue
		}
		slog.Debug("seedEdgesForTeam: dropping default delegation edge — endpoint not on team",
			"from_agent", e.FromAgent, "to_agent", e.ToAgent,
			"from_on_team", member[e.FromAgent], "to_on_team", member[e.ToAgent])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// workspaceTeamSet computes the workspace team membership set against which a
// delegation edge's endpoints are validated: the union of the workspace
// core_team and the endpoints of the workspace's EXISTING (already-stored)
// delegation edges. This matches the WorkspaceDelegationEdge schema contract
// ("from_agent / to_agent must be a member of the workspace team — present in
// core_team or referenced by another edge"). A PUT may rewire edges among team
// members but may NOT silently introduce a brand-new agent that is neither in
// core_team nor already an endpoint — that would expand the team as a side
// effect of an edge write, which the schema forbids.
//
// It is a thin adapter over the canonical workspace.TeamSet, which is the SINGLE
// derivation shared with the update_workspace tool — the two write paths can no
// longer diverge (they previously differed on whitespace trimming).
//
// The existing-edge half is loaded from the DELEGATION STORE (home is needed
// for that), never from a field on the workspace record — the record is
// writable by the sandboxed child the delegation decision constrains, so
// deriving team membership from it would let that child widen its own team.
// An unreadable/untrusted store contributes NO endpoints (deny-by-default);
// the two callers that need to distinguish that case from "no edges" — the
// delegation GET/PUT handlers — call workspace.LoadDelegation directly and
// fail closed on !ok rather than going through this helper.
func workspaceTeamSet(home string, ws storedWorkspace) map[string]bool {
	stored, _ := workspace.LoadDelegation(home, ws.ID)
	return workspace.TeamSet(ws.CoreTeam, stored)
}

// buildWorkspaceDelegationEdges validates and normalises the incoming edge list
// into the stored form. Returns (edges, "") on success or (nil, errMsg) on the
// first validation failure (the caller returns 400 with errMsg).
//
// Endpoints are validated against `team` (the workspace team set — core_team ∪
// existing-edge endpoints, see workspaceTeamSet), NOT the whole config roster:
// an edge endpoint must already be a member of the workspace team, so an edge
// write cannot silently expand the team with an off-team agent. The roster check
// is subsumed because team ⊆ roster by construction.
//
// A DFS cycle check rejects any multi-hop delegation cycle (A→B→A): although the
// runtime depth cap bounds an actual delegation chain, a cyclic config graph is a
// footgun, so it is rejected at write time with a clear 400.
func buildWorkspaceDelegationEdges(
	in []gen.WorkspaceDelegationEdge,
	team map[string]bool,
	depthCeiling int,
) ([]storedDelegationEdge, string) {
	out := make([]storedDelegationEdge, 0, len(in))
	// index maps (from,to) → position in out, so a later duplicate overwrites the
	// earlier edge (last-writer-wins) rather than appending a second copy.
	index := make(map[string]int, len(in))
	// adjacency for cycle detection (built from the deduplicated edge set).
	adj := make(map[string][]string, len(in))

	for _, e := range in {
		from := strings.TrimSpace(e.FromAgent)
		to := strings.TrimSpace(e.ToAgent)

		// Normalise the modes (trim already done by the wire layer; dedup here)
		// BEFORE validation so the stored edge carries the canonical, deduped set.
		// Incoming wire enum ([]string under the hood) → typed []DelegationMode;
		// the typed set is what the stored edge and Validate() consume.
		var modes []workspace.DelegationMode
		if e.Modes != nil {
			modes = make([]workspace.DelegationMode, 0, len(*e.Modes))
			seenMode := make(map[workspace.DelegationMode]bool, len(*e.Modes))
			for _, m := range *e.Modes {
				dm := workspace.DelegationMode(string(m))
				if seenMode[dm] {
					continue
				}
				seenMode[dm] = true
				modes = append(modes, dm)
			}
		}

		var depth *int
		if e.Depth != nil {
			d := *e.Depth
			depth = &d
		}

		// Single shared authority for the per-edge invariants (non-empty, no
		// self-edge, endpoints ∈ team, modes ⊆ {direct,task}, depth in
		// [0, ceiling]). Validate returns the canonical wire messages verbatim, so
		// the 400 body is byte-identical to the previous inline checks. The
		// whole-graph acyclicity check stays below (it is graph-level, not
		// per-edge). Build the candidate stored edge and validate it.
		edge := storedDelegationEdge{FromAgent: from, ToAgent: to, Modes: modes, Depth: depth}
		if err := edge.Validate(team, depthCeiling); err != nil {
			return nil, err.Error()
		}

		key := from + "\x00" + to
		if pos, dup := index[key]; dup {
			out[pos] = edge
			continue
		}
		index[key] = len(out)
		out = append(out, edge)
	}

	// Rebuild adjacency from the deduplicated edge set, then reject cycles.
	for _, e := range out {
		adj[e.FromAgent] = append(adj[e.FromAgent], e.ToAgent)
	}
	if cycleNode := detectDelegationCycle(adj); cycleNode != "" {
		return nil, "delegation graph contains a cycle (a delegation chain that loops back, e.g. through " +
			cycleNode + "); cycles are not allowed"
	}

	return out, ""
}

// detectDelegationCycle runs a DFS over the directed adjacency map and returns a
// node that participates in a cycle, or "" when the graph is acyclic. Uses the
// classic white/grey/black coloring: a back-edge to a grey (on-stack) node is a
// cycle.
func detectDelegationCycle(adj map[string][]string) string {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(adj))

	var visit func(node string) string
	visit = func(node string) string {
		color[node] = grey
		for _, next := range adj[node] {
			switch color[next] {
			case grey:
				return next // back-edge → cycle
			case white:
				if hit := visit(next); hit != "" {
					return hit
				}
			}
		}
		color[node] = black
		return ""
	}

	for node := range adj {
		if color[node] == white {
			if hit := visit(node); hit != "" {
				return hit
			}
		}
	}
	return ""
}
