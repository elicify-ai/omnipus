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

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// # Why delegation edges are NOT stored in the workspace record
//
// A delegation edge is an AUTHORIZATION. Per ADR-037 the per-workspace edge
// list is the SOLE runtime authority for who-may-delegate-to-whom — the global
// AgentConfig.DelegationPolicy was deleted precisely so that this one gate
// could not be bypassed by a second, divergent source. That makes the edge list
// a security decision input, and a security decision input must not be writable
// by the principal the decision constrains.
//
// The workspace record ($OMNIPUS_HOME/workspaces/<id>.json) fails that test, by
// exactly the same chain that forced mounts out of it (mountstore.go):
//
//  1. The kernel policy grants $OMNIPUS_HOME read/write/execute as one coarse
//     rule (sandbox.DefaultPolicyForModel), minus the per-turn denied set.
//  2. `workspaces` sits in fspolicy.SecretEntriesPerTurn, whose own-tree
//     exception re-admits the WHOLE `workspaces` root whenever the turn's work
//     dir is a proper descendant of it — which is the shape of every re-rooted
//     workspace turn (<home>/workspaces/<id>/work). So during a workspace turn
//     the kernel grants write on <home>/workspaces/<id>.json.
//  3. `bash` runs as a sandboxed child under precisely that policy, and the
//     gateway process that hosts the in-process file tools is not itself
//     confined on macOS, so no kernel layer catches a write issued through the
//     app layer either.
//  4. ReadDelegation read the `delegation` array straight back out of that
//     record and handed it to the enforcement gate
//     (pkg/agent's buildDelegationDenyChecker).
//
// Net effect: a child could append {"from_agent":"me","to_agent":"anyone"} to
// its own workspace record and grant itself delegation to any agent on the
// workspace — self-authorizing past the only control that exists.
//
// Re-validating on load does NOT fix it, for the same reason it did not fix
// mounts: a hostile edge is SHAPE-LEGAL. "jim delegates to worker with mode
// direct" is exactly what a legitimate operator-created edge looks like, so
// re-running DelegationEdge.Validate over a planted entry passes it. The list
// itself has to be out of reach.
//
// # Why entities/ is out of reach on BOTH layers
//
// `entities` is in fspolicy.SecretEntriesAlways — the context-free half of the
// secret set, the part that needs no work dir to be meaningful. Consequences,
// neither of them incidental:
//
//   - Kernel layer: fspolicy.DeniedPathsFor emits every SecretEntriesAlways
//     entry unconditionally, for every turn, with no own-tree exception. So
//     <home>/entities is a kernel DENY under Landlock and Seatbelt alike, and
//     the coarse $OMNIPUS_HOME grant above is subtracted by it.
//   - App layer: fspolicy.buildCarveOuts is SecretPaths, a superset of
//     SecretEntriesAlways, so <home>/entities is a carve-out. IsCarveOut's
//     own-tree exception can never re-admit it, because that exception only
//     fires when the turn's WorkDir is a proper descendant of the carve-out
//     root — and no turn's work dir is ever inside entities/.
//
// That is the same protection entities/agents already relies on for the
// Constraint #6 tool-policy map (ADR-054 §4) and entities/mounts for write
// grants (ADR-061): a per-entity record that decides what an agent may do, kept
// where the agent cannot reach it.
//
// # No migration, deliberately
//
// Edges left in (or planted in) an old workspace record are IGNORED — never
// imported into this store. Importing them would read the edge list out of the
// attacker-writable record and launder it into the protected one, which is the
// precise thing this store exists to prevent. See the NOTE in workspace.go
// where the field used to be for what an existing install loses.

// delegationStoreRecord is the on-disk shape of a per-workspace delegation
// file.
//
// WorkspaceID is stored (and cross-checked on load) even though the filename
// already carries it: a file whose recorded id disagrees with its own name has
// been moved or hand-assembled, and an authorization is not something to serve
// from a record that cannot account for its own identity.
type delegationStoreRecord struct {
	WorkspaceID string           `json:"workspace_id"`
	Delegation  []DelegationEdge `json:"delegation,omitempty"`
}

// DelegationStoreDir returns $OMNIPUS_HOME/entities/delegation — the directory
// holding every workspace's delegation record. Exported so tests can assert its
// position relative to the secret set without re-deriving the layout by hand (a
// test that hardcodes the path would keep passing if the real one moved).
func DelegationStoreDir(home string) string {
	return filepath.Join(home, "entities", "delegation")
}

// DelegationStorePath returns the delegation-record path for workspace id, or
// an error for an id that fails safeID (the same gate every other id-addressed
// path in this package passes through — an unchecked id here would be a path
// traversal into, or out of, the store directory).
func DelegationStorePath(home, id string) (string, error) {
	if !safeID(id) {
		return "", fmt.Errorf("%w: %q", ErrInvalidWorkspaceID, id)
	}
	return filepath.Join(DelegationStoreDir(home), id+".json"), nil
}

// loadDelegationStore reads workspace id's delegation record and returns the
// edges that survive validation.
//
// Failure modes and their (deliberately different) outcomes:
//
//   - invalid id, unreadable file, malformed JSON, or an id mismatch: ok=false.
//     Callers treat that as "no delegation" — an edge list that cannot be read
//     is never approximated, and ReadDelegation turns it into a hard error so
//     the runtime gate denies rather than guesses.
//   - file absent: ok=true with no edges. A workspace with no delegation has no
//     file; that is the normal state, not an error, and it means deny-by-default
//     at the gate (no edge can match).
//   - an individual edge failing ValidateShape, or repeating a (from,to) pair
//     already seen: that EDGE is dropped with a WARN, the rest are kept.
//     Dropping one bad edge fails closed for that edge without silently
//     disarming delegation an operator legitimately configured.
func loadDelegationStore(home, id string) ([]DelegationEdge, bool) {
	path, err := DelegationStorePath(home, id)
	if err != nil {
		logger.WarnCF("workspace", "delegation store: refusing to read a record for an unsafe workspace id", map[string]any{
			"workspace_id": id, "error": err.Error(),
		})
		return nil, false
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is built from a safeID-checked id under the store dir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, true
		}
		logger.WarnCF("workspace", "delegation store: unreadable record — treating this workspace as having no delegation edges", map[string]any{
			"workspace_id": id, "path": path, "error": err.Error(),
		})
		return nil, false
	}
	var rec delegationStoreRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		logger.WarnCF("workspace", "delegation store: malformed record — treating this workspace as having no delegation edges", map[string]any{
			"workspace_id": id, "path": path, "error": err.Error(),
		})
		return nil, false
	}
	if rec.WorkspaceID != "" && rec.WorkspaceID != id {
		logger.WarnCF("workspace", "delegation store: record's workspace_id disagrees with its filename — refusing to authorize from it", map[string]any{
			"workspace_id": id, "path": path, "recorded_workspace_id": rec.WorkspaceID,
		})
		return nil, false
	}

	out := make([]DelegationEdge, 0, len(rec.Delegation))
	seen := make(map[string]struct{}, len(rec.Delegation))
	for _, e := range rec.Delegation {
		if err := e.ValidateShape(); err != nil {
			logger.WarnCF("workspace", "delegation store: dropping an invalid delegation edge", map[string]any{
				"workspace_id": id, "path": path,
				"from_agent": e.FromAgent, "to_agent": e.ToAgent, "error": err.Error(),
			})
			continue
		}
		key := e.FromAgent + "\x00" + e.ToAgent
		if _, dup := seen[key]; dup {
			logger.WarnCF("workspace", "delegation store: dropping a duplicate delegation edge", map[string]any{
				"workspace_id": id, "path": path,
				"from_agent": e.FromAgent, "to_agent": e.ToAgent,
			})
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

// LoadDelegation returns the delegation edges recorded for workspace id.
//
// The source is the delegation store
// ($OMNIPUS_HOME/entities/delegation/<id>.json), NEVER the workspace record — a
// `delegation` array left in an old workspace record, or planted in one by a
// sandboxed child, is not read by anything and authorizes nothing (this file's
// leading comment explains why that distinction is the whole point of this
// store).
//
// ok is false only when the record exists but cannot be trusted (unsafe id,
// unreadable file, malformed JSON, id mismatch); a workspace that simply has no
// edges returns (nil, true). Individual edges failing ValidateShape are dropped
// with a WARN rather than trusted — see loadDelegationStore.
//
// This is the read used by the management surfaces (the Team tab's GET/PUT and
// the update_workspace tool). The RUNTIME enforcement gate uses ReadDelegation
// (delegation.go), which layers a workspace-existence check and converts
// ok=false into a hard error so the gate fails closed.
func LoadDelegation(home, id string) ([]DelegationEdge, bool) {
	return loadDelegationStore(home, id)
}

// SaveDelegation atomically persists edges for workspace id, under the same
// advisory-lock + temp-file-rename discipline every other record writer in this
// package uses (saveWorkspaceRecord, saveMountStore). An empty list removes the
// file rather than leaving an empty husk, so "no delegation" has exactly one
// on-disk representation.
//
// Every edge is re-checked with ValidateShape before anything is written: a
// writer can never persist an edge its own reader would have to drop, so the
// store's contents and the gate's view of them cannot diverge. A shape failure
// aborts the whole write — a partially-applied edge list is a different
// authorization than the one the caller asked for.
//
// Callers MUST already hold LockID(id) across the full load-modify-write, the
// same contract saveMountStore states. Whole-graph concerns (team membership,
// depth ceiling, acyclicity) stay with the callers that have the config context
// to judge them — this function is the storage layer, not the policy layer.
func SaveDelegation(home, id string, edges []DelegationEdge) error {
	path, err := DelegationStorePath(home, id)
	if err != nil {
		return err
	}
	for _, e := range edges {
		if verr := e.ValidateShape(); verr != nil {
			return fmt.Errorf("workspace: delegation store: refusing to persist an invalid edge: %w", verr)
		}
	}
	if len(edges) == 0 {
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return fmt.Errorf("workspace: delegation store: remove %s: %w", path, rmErr)
		}
		return nil
	}
	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return fmt.Errorf("workspace: delegation store: mkdir %s: %w", dir, mkErr)
	}
	data, err := json.MarshalIndent(delegationStoreRecord{WorkspaceID: id, Delegation: edges}, "", "  ")
	if err != nil {
		return fmt.Errorf("workspace: delegation store: marshal %s: %w", id, err)
	}
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// DeleteDelegationStore removes workspace id's entire delegation record. Called
// from the workspace-delete cascade so a deleted workspace leaves no orphaned
// authorization record behind — the store lives outside the workspace
// directory, so the cascade's os.RemoveAll never reaches it.
//
// Absent record is success: the cascade must be idempotent and must not fail a
// delete over a file that was already gone.
func DeleteDelegationStore(home, id string) error {
	unlock := LockID(id)
	defer unlock()

	path, err := DelegationStorePath(home, id)
	if err != nil {
		return err
	}
	if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return fmt.Errorf("workspace: delegation store: remove %s: %w", path, rmErr)
	}
	return nil
}
