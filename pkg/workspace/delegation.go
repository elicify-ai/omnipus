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
//   - Depth: nil/absent ⇒ inherit the global/per-turn depth cap. A non-nil
//     value is the per-edge onward-delegation cap (0 ⇒ no onward delegation).
type DelegationEdge struct {
	FromAgent string   `json:"from_agent"`
	ToAgent   string   `json:"to_agent"`
	Modes     []string `json:"modes,omitempty"`
	Depth     *int     `json:"depth,omitempty"`
}

// Canonical delegation mode names. These MUST stay in lock-step with
// config.DelegationMode{Await,Background,Task} (pkg/config). They are duplicated
// here as bare string literals — NOT imported from pkg/config — deliberately:
// pkg/workspace is dependency-free of pkg/config to avoid an import cycle
// (pkg/agent imports pkg/workspace, pkg/config is imported by both). A drift
// between these and the config constants would be caught by
// TestDelegationEdgeValidate_ModesMatchConfig in pkg/gateway (which CAN import
// both), so this is a single, test-pinned source of truth at the edge layer.
const (
	delegationModeAwait      = "await"
	delegationModeBackground = "background"
	delegationModeTask       = "task"
)

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
		switch m {
		case delegationModeAwait, delegationModeBackground, delegationModeTask:
		default:
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
// workspace: the per-agent config.DelegationPolicy is seed-only and is NOT
// consulted at runtime enforcement. Callers that fail to read the graph MUST
// fail closed (deny), never fall open.
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
