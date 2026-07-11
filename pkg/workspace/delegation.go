// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DelegationMode is the typed name of a single delegation mode on an edge. It is
// a string type (NOT a struct), so on the wire and on disk it marshals as a plain
// JSON string — `["await","task"]` — exactly as the former []string did. Making it
// a named type (instead of a bare string) eliminates primitive obsession: the
// valid set is closed by Valid() and the constants below, so a reader can no
// longer typo a mode into existence or accept an arbitrary string.
//
// These constants MUST stay in lock-step with config.DelegationMode{Await,
// Background,Task} (pkg/config). They are duplicated here as bare string literals
// — NOT imported from pkg/config — deliberately: pkg/workspace is dependency-free
// of pkg/config to avoid an import cycle (pkg/agent imports pkg/workspace, and
// pkg/config is imported by both). A drift between these and the config constants
// is caught by TestDelegationEdgeValidate_ModesMatchConfig in pkg/gateway (which
// CAN import both), so this is a single, test-pinned source of truth at the edge
// layer.
type DelegationMode string

const (
	ModeAwait      DelegationMode = "await"
	ModeBackground DelegationMode = "background"
	ModeTask       DelegationMode = "task"
)

// Valid reports whether m is one of the closed set of delegation modes. The
// per-edge validator and every mode-membership check route through this single
// authority, so an unknown mode can never be persisted or trusted at runtime.
func (m DelegationMode) Valid() bool {
	return m == ModeAwait || m == ModeBackground || m == ModeTask
}

// DelegationEdge mirrors a single directed delegation edge stored in
// workspaces/<id>.json. It is the dependency-free read view of the gateway's
// storedDelegationEdge (same JSON tags), so non-gateway packages (pkg/agent)
// can read the per-workspace delegation graph WITHOUT importing pkg/gateway
// (which would create an import cycle).
//
// Semantics carried by an edge (the runtime authority — see pkg/agent):
//   - An edge FromAgent→ToAgent AUTHORIZES FromAgent to delegate to ToAgent in
//     this workspace. No edge ⇒ delegation is DENIED (deny-by-default).
//   - Modes: empty/absent ⇒ ALL delegation modes (await|background|task) are
//     allowed for this edge. Non-empty ⇒ only the listed modes are allowed.
//     Modes is []DelegationMode (a string type), so json:"modes" still
//     marshals/unmarshals as a plain ["await",...] string array — the on-disk
//     workspace JSON and the generated wire type (which stays []string) are
//     UNCHANGED by the typing.
//   - Depth: nil/absent ⇒ inherit the global/per-turn depth cap. A non-nil
//     value is the per-edge onward-delegation cap. DEPTH INVARIANT (the single
//     authority, mirrored at the runtime gate in pkg/agent's
//     enforceEdgeModeAndDepth): depth <= 0 ⇒ this edge grants NO onward
//     delegation (the strictest bound — a negative value is never an "uncapped"
//     signal and must fail closed); depth > 0 ⇒ onward delegation is capped at
//     that chain depth.
type DelegationEdge struct {
	FromAgent string           `json:"from_agent"`
	ToAgent   string           `json:"to_agent"`
	Modes     []DelegationMode `json:"modes,omitempty"`
	Depth     *int             `json:"depth,omitempty"`
}

// Validate enforces the per-edge invariants for a single delegation edge. It is
// the SHARED authority for edge well-formedness across every write path (the
// gateway PUT handler AND the update_workspace tool), so no second writer can
// persist an illegal edge that is then trusted at runtime. The whole-graph
// acyclicity check is NOT part of this method — that remains a graph-level
// concern owned by the gateway (detectDelegationCycle), because it depends on
// the full edge set, not one edge.
//
// Invariants enforced (fail-closed: any violation is a hard error):
//   - from_agent and to_agent are both non-empty (after trimming)
//   - from_agent != to_agent (no self-edge)
//   - both endpoints are members of team (the workspace team set — core_team ∪
//     existing-edge endpoints). A nil team treats EVERY endpoint as off-team
//     (deny-by-default): callers MUST pass the real team set.
//   - every mode (when Modes is non-empty) ∈ {await, background, task}
//   - Depth, when non-nil, is >= 0 and <= ceiling
//
// The returned error messages are the canonical wire messages surfaced verbatim
// as the gateway's 400 body — callers that present them to the API MUST NOT
// rewrap them, or existing wire-contract tests break.
//
// NOTE: Validate trims endpoints for the membership/empty checks but does NOT
// mutate the receiver. A caller that wants the trimmed/normalised form (as
// buildWorkspaceDelegationEdges does) should trim explicitly before storing.
func (e DelegationEdge) Validate(team map[string]bool, ceiling int) error {
	from := strings.TrimSpace(e.FromAgent)
	to := strings.TrimSpace(e.ToAgent)
	if from == "" || to == "" {
		return errors.New("delegation edge from_agent and to_agent must not be empty")
	}
	if from == to {
		return fmt.Errorf("delegation edge cannot be a self-edge (from_agent == to_agent: %s)", from)
	}
	if !team[from] {
		return fmt.Errorf("delegation edge from_agent %s is not a member of the workspace team", from)
	}
	if !team[to] {
		return fmt.Errorf("delegation edge to_agent %s is not a member of the workspace team", to)
	}
	for _, m := range e.Modes {
		if !m.Valid() {
			return fmt.Errorf("delegation edge mode %s is invalid (valid: await, background, task)", m)
		}
	}
	if e.Depth != nil {
		if *e.Depth < 0 {
			return errors.New("delegation edge depth must be >= 0")
		}
		if *e.Depth > ceiling {
			return errors.New("delegation edge depth exceeds the maximum allowed depth")
		}
	}
	return nil
}

// TeamSet computes the workspace team-membership set against which a delegation
// edge's endpoints are validated: the union of the workspace core_team and the
// endpoints of the workspace's EXISTING (already-stored) delegation edges, each
// trimmed of surrounding whitespace and with empties dropped. It is the SINGLE
// canonical derivation of that set, shared by every write path (the gateway PUT
// handler's workspaceTeamSet wrapper AND the update_workspace tool), so the team
// argument fed to DelegationEdge.Validate has exactly one definition and the two
// call sites can no longer diverge (they previously differed on whitespace
// trimming).
//
// Semantics: an edge write may rewire edges AMONG team members but may NOT
// silently introduce a brand-new agent that is neither in core_team nor already
// an endpoint — that would expand the team as a side effect of an edge write,
// which the WorkspaceDelegationEdge schema forbids. A nil/empty inputs yield an
// empty (non-nil) set ⇒ deny-by-default at the validator (every endpoint is
// off-team).
func TeamSet(coreTeam []string, edges []DelegationEdge) map[string]bool {
	team := make(map[string]bool, len(coreTeam)+2*len(edges))
	for _, id := range coreTeam {
		if id = strings.TrimSpace(id); id != "" {
			team[id] = true
		}
	}
	for _, e := range edges {
		if f := strings.TrimSpace(e.FromAgent); f != "" {
			team[f] = true
		}
		if t := strings.TrimSpace(e.ToAgent); t != "" {
			team[t] = true
		}
	}
	return team
}

// delegationRecord is the minimal subset of the on-disk workspace JSON that
// ReadDelegation parses — just the delegation edge list. It deliberately does
// NOT mirror the full storedWorkspace struct (the gateway owns that).
type delegationRecord struct {
	Delegation []DelegationEdge `json:"delegation,omitempty"`
}

// ReadDelegation reads and returns the delegation edges from
// workspaces/<workspaceID>.json under home.
//
// This is the SOLE runtime authority for who-may-delegate-to-whom in a
// workspace (ADR-037) — there is no separate global per-agent delegation
// policy at all; coreagent's seeded trust graph is consulted only to
// bootstrap a fresh workspace's initial edges, never at enforcement time.
// Callers that fail to read the graph MUST fail closed (deny), never fall open.
//
// Returns:
//   - (edges, nil)        when the workspace file exists and parses. An empty
//     (or absent) Delegation list yields an empty, non-nil-or-nil slice with a
//     nil error: "workspace exists but has no edges" ⇒ deny-by-default at the
//     caller (no matching edge can be found).
//   - ("", ErrInvalidWorkspaceID-wrapped err) when the id is unsafe (traversal).
//   - (nil, wrapped err)  when the file is missing or unreadable, or its JSON is
//     malformed. The caller MUST treat this as a hard error and DENY — an
//     unreadable graph is a closed graph.
func ReadDelegation(home, workspaceID string) ([]DelegationEdge, error) {
	if !safeID(workspaceID) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidWorkspaceID, workspaceID)
	}
	path := filepath.Join(dirFor(home), workspaceID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		// A missing workspace is a hard error here (unlike ReadInstructions,
		// where an absent file is a benign empty state): a delegation check that
		// cannot locate its governing workspace MUST fail closed at the caller.
		return nil, fmt.Errorf("workspace: read delegation %q: %w", workspaceID, err)
	}
	var rec delegationRecord
	if jerr := json.Unmarshal(data, &rec); jerr != nil {
		return nil, fmt.Errorf("workspace: parse delegation %q: %w", workspaceID, jerr)
	}
	return rec.Delegation, nil
}
