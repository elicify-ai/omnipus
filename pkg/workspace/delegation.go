// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
