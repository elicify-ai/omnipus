//go:build !cgo

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

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// delegationEdgeToWire converts a storedDelegationEdge to the generated wire type.
func delegationEdgeToWire(e storedDelegationEdge) gen.WorkspaceDelegationEdge {
	out := gen.WorkspaceDelegationEdge{
		FromAgent: e.FromAgent,
		ToAgent:   e.ToAgent,
	}
	if len(e.Modes) > 0 {
		modes := make([]gen.WorkspaceDelegationEdgeModes, 0, len(e.Modes))
		for _, m := range e.Modes {
			modes = append(modes, gen.WorkspaceDelegationEdgeModes(m))
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
// every agent referenced by an edge, sorted for stable output.
func workspaceDelegationToWire(ws storedWorkspace) gen.WorkspaceDelegation {
	edges := make([]gen.WorkspaceDelegationEdge, 0, len(ws.Delegation))
	teamSet := make(map[string]struct{}, len(ws.CoreTeam)+2*len(ws.Delegation))
	for _, id := range ws.CoreTeam {
		teamSet[id] = struct{}{}
	}
	for _, e := range ws.Delegation {
		edges = append(edges, delegationEdgeToWire(e))
		teamSet[e.FromAgent] = struct{}{}
		teamSet[e.ToAgent] = struct{}{}
	}
	team := make([]string, 0, len(teamSet))
	for id := range teamSet {
		team = append(team, id)
	}
	sort.Strings(team)

	out := gen.WorkspaceDelegation{
		WorkspaceId: ws.ID,
		Edges:       edges,
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
	jsonOK(w, workspaceDelegationToWire(ws))
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
//   - modes ⊆ {await, background, task}
//   - depth must be >= 0 and <= the global subturn depth ceiling
//
// Edges are deduplicated by (from_agent, to_agent); the last writer wins.
//
// This graph IS the runtime authority. Per-workspace delegation enforcement reads
// these edges directly at delegation time (see workspace.ReadDelegation +
// buildDelegationDenyChecker in pkg/agent/loop.go): a delegation caller→target is
// permitted ONLY when a matching edge exists in the governing workspace's graph
// (with the edge's modes/depth applied). The per-agent config.DelegationPolicy is
// SEED-ONLY now — it seeds each new workspace's initial graph via
// defaultWorkspaceDelegationEdges and is no longer consulted at runtime. Because
// the checker reads the graph per-call, editing this graph takes effect on the
// NEXT turn — no agent rebuild or reload is required.
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

	ws, ok := a.loadWorkspace(w, id)
	if !ok {
		return
	}

	cfg := a.agentLoop.GetConfig()
	// Validate edge endpoints against the workspace TEAM (core_team ∪ existing-edge
	// endpoints), not the whole config roster — an edge write must not silently
	// add an off-team agent to the workspace team. team ⊆ roster by construction.
	team := workspaceTeamSet(ws)
	ceiling := delegationDepthCeiling(cfg)

	edges, errMsg := buildWorkspaceDelegationEdges(req.Edges, team, ceiling)
	if errMsg != "" {
		jsonErr(w, http.StatusBadRequest, errMsg)
		return
	}

	ws.Delegation = edges
	ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(a.homePath, ws); err != nil {
		slog.Error("rest: update workspace delegation", "error", err, "id", id)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if a.auditor != nil {
		_ = a.auditor.Log(&audit.Entry{
			Event:    "workspace.delegation.update",
			Decision: audit.DecisionAllow,
			Details:  map[string]any{"id": ws.ID, "edge_count": len(edges)},
		})
	}

	jsonOK(w, workspaceDelegationToWire(ws))
}

// defaultWorkspaceDelegationEdges derives the seed delegation graph for a new
// workspace from the per-agent delegation policies already seeded in config (M5).
// Each agent's DelegationPolicy.To becomes one edge per target, carrying that
// policy's modes and depth. This keeps a single source of truth: coreagent's
// seeded trust graph (Jim→Ava/Ray/worker, Mia/Ray/Ava→worker, the Planner→
// Explorer/Researcher specialist edges) is replayed onto the workspace graph so a
// fresh workspace works out of the box. Remote-a2a refs and wildcard ("*") refs
// are skipped — they have no concrete in-roster node to draw an edge to.
func defaultWorkspaceDelegationEdges(cfg *config.Config) []storedDelegationEdge {
	if cfg == nil {
		return nil
	}
	var edges []storedDelegationEdge
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		dp := ac.DelegationPolicy
		if dp == nil || len(dp.To) == 0 {
			continue
		}
		modes := make([]string, 0, len(dp.Modes))
		for _, m := range dp.Modes {
			modes = append(modes, string(m))
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
				Modes:     append([]string(nil), modes...),
				Depth:     depth,
			})
		}
	}
	return edges
}

// defaultWorkspaceTeam returns the default workspace roster (M6): the 4 base
// agents plus the seeded specialist subagents (Planner / Explorer / Researcher).
// Only agents that actually exist in the config are included, so a lite/custom
// roster never produces a dangling team member.
func defaultWorkspaceTeam(cfg *config.Config) []string {
	want := []string{"mia", "jim", "ava", "ray", "planner", "explorer", "researcher"}
	if cfg == nil {
		return nil
	}
	present := make(map[string]bool, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		present[cfg.Agents.List[i].ID] = true
	}
	team := make([]string, 0, len(want))
	for _, id := range want {
		if present[id] {
			team = append(team, id)
		}
	}
	return team
}

// seedEdgesForTeam filters a set of default edges down to those whose endpoints
// are BOTH members of the given team. Used on workspace create so a custom
// core_team only inherits the default edges relevant to the agents it actually
// includes (no dangling edge to an agent the operator left out). A nil/empty team
// yields no edges.
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
		}
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
func workspaceTeamSet(ws storedWorkspace) map[string]bool {
	team := make(map[string]bool, len(ws.CoreTeam)+2*len(ws.Delegation))
	for _, id := range ws.CoreTeam {
		if id = strings.TrimSpace(id); id != "" {
			team[id] = true
		}
	}
	for _, e := range ws.Delegation {
		if f := strings.TrimSpace(e.FromAgent); f != "" {
			team[f] = true
		}
		if t := strings.TrimSpace(e.ToAgent); t != "" {
			team[t] = true
		}
	}
	return team
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
		if from == "" || to == "" {
			return nil, "delegation edge from_agent and to_agent must not be empty"
		}
		if from == to {
			return nil, "delegation edge cannot be a self-edge (from_agent == to_agent: " + from + ")"
		}
		if !team[from] {
			return nil, "delegation edge from_agent " + from + " is not a member of the workspace team"
		}
		if !team[to] {
			return nil, "delegation edge to_agent " + to + " is not a member of the workspace team"
		}

		var modes []string
		if e.Modes != nil {
			modes = make([]string, 0, len(*e.Modes))
			seenMode := make(map[string]bool, len(*e.Modes))
			for _, m := range *e.Modes {
				ms := string(m)
				switch config.DelegationMode(ms) {
				case config.DelegationModeAwait, config.DelegationModeBackground, config.DelegationModeTask:
				default:
					return nil, "delegation edge mode " + ms + " is invalid (valid: await, background, task)"
				}
				if seenMode[ms] {
					continue
				}
				seenMode[ms] = true
				modes = append(modes, ms)
			}
		}

		var depth *int
		if e.Depth != nil {
			d := *e.Depth
			if d < 0 {
				return nil, "delegation edge depth must be >= 0"
			}
			if d > depthCeiling {
				return nil, "delegation edge depth exceeds the maximum allowed depth"
			}
			depth = &d
		}

		edge := storedDelegationEdge{FromAgent: from, ToAgent: to, Modes: modes, Depth: depth}
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
