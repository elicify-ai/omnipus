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
// JSON string — `["direct","task"]` — exactly as the former []string did. Making
// it a named type (instead of a bare string) eliminates primitive obsession: the
// valid set is closed by Valid() and the constants below, so a reader can no
// longer typo a mode into existence or accept an arbitrary string.
//
// This is a DELIBERATELY COLLAPSED vocabulary relative to config.DelegationMode
// (pkg/config), which still has 3 values (Await/Background/Task) — that type
// describes the delegate TOOL's real runtime call parameter (does this
// particular dispatch block the caller or run in the background?), which is
// still a meaningful choice made per-call. The trust EDGE, by contrast, no
// longer gates that choice separately: an edge that authorizes "direct"
// delegation authorizes both the synchronous and background call patterns, so
// the edge-level vocabulary collapses to just direct|task. See
// EdgeModeCategory (pkg/agent/loop.go) for the mapping from the tool's 3-value
// parameter down to this type's 2-value category at the enforcement gate, and
// wireDelegationInjectors (pkg/agent/loop_env.go) for the inverse expansion
// back to 3 values when advertising available modes in the system prompt.
type DelegationMode string

const (
	ModeDirect DelegationMode = "direct"
	ModeTask   DelegationMode = "task"
)

// Valid reports whether m is one of the closed set of delegation modes. The
// per-edge validator and every mode-membership check route through this single
// authority, so an unknown mode can never be persisted or trusted at runtime.
func (m DelegationMode) Valid() bool {
	return m == ModeDirect || m == ModeTask
}

// legacyModeDirect is the set of pre-collapse mode strings that now map to
// ModeDirect. UnmarshalJSON uses this to transparently migrate edges persisted
// under the old 3-value ("await"/"background"/"task") vocabulary the first time
// they are read, without requiring a rewrite-to-disk migration pass.
var legacyModeDirect = map[string]bool{
	"await":      true,
	"background": true,
}

// DelegationEdge mirrors a single directed delegation edge stored in the
// per-workspace delegation store, entities/delegation/<id>.json (NOT in
// workspaces/<id>.json — see delegationstore.go for why an authorization
// cannot live in a record the constrained principal can write). It is the
// dependency-free read view of the gateway's
// storedDelegationEdge (same JSON tags), so non-gateway packages (pkg/agent)
// can read the per-workspace delegation graph WITHOUT importing pkg/gateway
// (which would create an import cycle).
//
// Semantics carried by an edge (the runtime authority — see pkg/agent):
//   - An edge FromAgent→ToAgent AUTHORIZES FromAgent to delegate to ToAgent in
//     this workspace. No edge ⇒ delegation is DENIED (deny-by-default).
//   - Modes: empty/absent ⇒ ALL delegation modes (direct|task) are allowed for
//     this edge. Non-empty ⇒ only the listed modes are allowed. Modes is
//     []DelegationMode (a string type), so json:"modes" still
//     marshals/unmarshals as a plain ["direct",...] string array — the on-disk
//     workspace JSON and the generated wire type (which stays []string) are
//     UNCHANGED by the typing. UnmarshalJSON below transparently migrates any
//     persisted edge still using the legacy 3-value ("await"/"background"/
//     "task") vocabulary: "await" and "background" both collapse to "direct"
//     (deduped, so an edge listing both legacy values yields a single "direct"
//     entry), "task" passes through unchanged. No rewrite-to-disk is forced —
//     the migration is transparent at every read, and the next explicit write
//     (PUT or update_workspace tool) persists the new 2-value form.
//   - Depth: nil/absent ⇒ inherit the global/per-turn depth cap. A non-nil
//     value is the per-edge onward-delegation cap. DEPTH INVARIANT (the single
//     authority, mirrored at the runtime gate in pkg/agent's
//     enforceEdgeModeAndDepth): depth <= 0 ⇒ this edge grants NO onward
//     delegation (the strictest bound — a negative value is never an "uncapped"
//     signal and must fail closed); depth > 0 ⇒ onward delegation is capped at
//     that chain depth.
//
// Deliberate mixed receivers below: UnmarshalJSON MUST be a pointer receiver
// (the json.Unmarshaler contract requires mutating the receiver in place),
// while Validate is deliberately a VALUE receiver so it can be called
// directly on composite-literal edges — a pattern used throughout the
// gateway handlers and tests (e.g. storedDelegationEdge{...}.Validate(team,
// ceiling)), which would stop compiling if Validate required an addressable
// value.
//
//nolint:recvcheck // see comment above
type DelegationEdge struct {
	FromAgent string           `json:"from_agent"`
	ToAgent   string           `json:"to_agent"`
	Modes     []DelegationMode `json:"modes,omitempty"`
	Depth     *int             `json:"depth,omitempty"`
}

// UnmarshalJSON is the transparent legacy-mode migration point for
// DelegationEdge. Every reader that decodes a DelegationEdge from JSON — the
// gateway's PUT handler, ReadDelegation below, and the update_workspace
// sysagent tool — routes through this single method (they all unmarshal into
// this same struct, directly or via an embedding/aliasing type), so fixing the
// migration here covers all three read paths with no other code changes.
// (ReadDelegation now sources its edges from the delegation store rather than
// the workspace record; the decode still lands in this same struct, so the
// migration point is unchanged.)
//
// It decodes into a shadow struct with Modes as raw []string, remaps any
// legacy "await"/"background" value to "direct" (via legacyModeDirect),
// dedupes (preserving first-seen order so migration is stable and
// deterministic), and populates the typed Modes []DelegationMode field. All
// other fields pass through unchanged. Decoding an already-migrated edge
// (Modes already direct/task) is a no-op pass-through — idempotent.
func (e *DelegationEdge) UnmarshalJSON(data []byte) error {
	var shadow struct {
		FromAgent string   `json:"from_agent"`
		ToAgent   string   `json:"to_agent"`
		Modes     []string `json:"modes,omitempty"`
		Depth     *int     `json:"depth,omitempty"`
	}
	if err := json.Unmarshal(data, &shadow); err != nil {
		return fmt.Errorf("workspace: unmarshal delegation edge: %w", err)
	}
	e.FromAgent = shadow.FromAgent
	e.ToAgent = shadow.ToAgent
	e.Depth = shadow.Depth
	if shadow.Modes == nil {
		e.Modes = nil
		return nil
	}
	seen := make(map[DelegationMode]bool, len(shadow.Modes))
	migrated := make([]DelegationMode, 0, len(shadow.Modes))
	for _, raw := range shadow.Modes {
		mode := DelegationMode(raw)
		if legacyModeDirect[raw] {
			mode = ModeDirect
		}
		if seen[mode] {
			continue
		}
		seen[mode] = true
		migrated = append(migrated, mode)
	}
	e.Modes = migrated
	return nil
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
//   - every mode (when Modes is non-empty) ∈ {direct, task}
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
			return fmt.Errorf("delegation edge mode %s is invalid (valid: direct, task)", m)
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

// workspaceExistenceProbe is the minimal subset of the on-disk workspace JSON
// that ReadDelegation parses. It carries NO delegation field, deliberately: the
// only thing the workspace record is still consulted for at delegation time is
// "does this workspace exist and is its record intact". The edges themselves
// come from the delegation store (delegationstore.go), which is where a
// sandboxed child cannot reach them.
//
// Do NOT add a Delegation field here. Parsing the record's own `delegation`
// array — even just to compare, log, or merge it — re-opens the self-
// authorization hole this split exists to close.
type workspaceExistenceProbe struct {
	ID string `json:"id"`
}

// ReadDelegation reads and returns the delegation edges governing workspaceID.
//
// This is the SOLE runtime authority for who-may-delegate-to-whom in a
// workspace (ADR-037) — there is no separate global per-agent delegation
// policy at all; coreagent's seeded trust graph is consulted only to
// bootstrap a fresh workspace's initial edges, never at enforcement time.
// Callers that fail to read the graph MUST fail closed (deny), never fall open.
//
// SOURCE (the whole point): the edges come from the delegation STORE,
// $OMNIPUS_HOME/entities/delegation/<id>.json, NEVER from
// workspaces/<id>.json. The workspace record is writable by the sandboxed
// child the delegation decision constrains, so an edge list read from it would
// let that child authorize itself — see delegationstore.go's leading comment
// and the NOTE in workspace.go where the `delegation` field used to be. A
// `delegation` array still present in (or planted into) the workspace record is
// read by nothing and authorizes nothing.
//
// The workspace record is still read, for one purpose only: a delegation check
// that cannot locate or parse its governing workspace MUST fail closed. That is
// an existence/integrity probe (workspaceExistenceProbe), not an edge source.
//
// Returns:
//   - (edges, nil)        when the workspace record exists and parses and the
//     store is readable. A workspace with no delegation store file yields a nil
//     slice with a nil error: "workspace exists but has no edges" ⇒
//     deny-by-default at the caller (no matching edge can be found).
//   - (nil, ErrInvalidWorkspaceID-wrapped err) when the id is unsafe (traversal).
//   - (nil, wrapped err)  when the workspace record is missing, unreadable or
//     malformed, OR when the delegation store record exists but cannot be
//     trusted (unreadable, malformed, id mismatch). The caller MUST treat this
//     as a hard error and DENY — an unreadable graph is a closed graph.
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
	var probe workspaceExistenceProbe
	if jerr := json.Unmarshal(data, &probe); jerr != nil {
		return nil, fmt.Errorf("workspace: parse delegation %q: %w", workspaceID, jerr)
	}
	edges, ok := loadDelegationStore(home, workspaceID)
	if !ok {
		// loadDelegationStore already logged the specific reason (unreadable,
		// malformed, or a record whose workspace_id disagrees with its
		// filename). An edge list that cannot be trusted is never approximated.
		return nil, fmt.Errorf(
			"workspace: read delegation %q: delegation store record is unreadable or untrusted", workspaceID)
	}
	return edges, nil
}

// ValidateShape checks the invariants of a single edge that hold with NO
// workspace context — non-empty endpoints, no self-edge, known modes, and a
// non-negative depth.
//
// It exists because the delegation STORE (delegationstore.go) validates edges
// at load and save time, where the team roster and the depth ceiling are not
// available: the roster lives on the workspace record and the ceiling in
// config, and reading either from inside the store would make a security-
// critical loader depend on two more mutable inputs.
//
// It is deliberately a SUBSET of Validate, never a replacement. Validate still
// runs where the roster is known and is the one that enforces "both endpoints
// are on this team" — the check that actually stops an agent delegating to
// someone outside the workspace. Shape validation only stops a corrupt or
// hand-edited store injecting a structurally impossible edge; it is a
// data-integrity guard, not the authorization boundary.
//
// The depth CEILING is intentionally not checked here for the same reason: an
// edge whose depth exceeds a ceiling that has since been lowered is stale
// configuration to be re-evaluated against the live ceiling by Validate, not
// corrupt data to be silently dropped at load.
func (e DelegationEdge) ValidateShape() error {
	from := strings.TrimSpace(e.FromAgent)
	to := strings.TrimSpace(e.ToAgent)
	if from == "" || to == "" {
		return errors.New("delegation edge from_agent and to_agent must not be empty")
	}
	if from == to {
		return fmt.Errorf("delegation edge cannot be a self-edge (from_agent == to_agent: %s)", from)
	}
	for _, m := range e.Modes {
		if !m.Valid() {
			return fmt.Errorf("delegation edge mode %s is invalid (valid: direct, task)", m)
		}
	}
	if e.Depth != nil && *e.Depth < 0 {
		return errors.New("delegation edge depth must be >= 0")
	}
	return nil
}
