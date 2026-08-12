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

// # Why mounts are NOT stored in the workspace record
//
// A mount is a WRITE GRANT. The list of mounts is therefore a security
// decision input, and a security decision input must not be writable by the
// principal the decision constrains.
//
// The workspace record ($OMNIPUS_HOME/workspaces/<id>.json) fails that test.
// The chain, verified end to end before this store existed:
//
//  1. The kernel policy grants $OMNIPUS_HOME read/write/execute as one coarse
//     rule (sandbox.DefaultPolicyForModel), minus the per-turn denied set.
//  2. That denied set (fspolicy.DeniedPathsFor) re-admits the `workspaces`
//     root whenever the turn's work dir is a proper descendant of it — which
//     is exactly the shape of every re-rooted workspace turn
//     (<home>/workspaces/<id>/work). So during a workspace turn the kernel
//     grants write on <home>/workspaces/<id>.json.
//  3. `bash` runs as a sandboxed child under precisely that policy, and the
//     gateway process that hosts the in-process file tools is not itself
//     confined on macOS, so no kernel layer catches a write issued through
//     the app layer either.
//  4. AllowedMountRoots read HostPath back out of that record verbatim and
//     fed it into FSPolicy.AllowedRoots, whose write branch allows any write
//     underneath it.
//
// Net effect: a child appended {"name":"x","host_path":"/"} to its own
// workspace record and every subsequent write went anywhere on the disk. That
// defeats the one property ADR-060 keeps — writes stay default-deny.
//
// Re-validating on load does NOT fix it. host_path "/" is a LEGITIMATE,
// operator-reachable value (FR-7.6 warn-and-allow), so re-running
// CheckMountTarget over a hostile entry passes it. The list itself has to be
// out of reach.
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
//   - App layer: fspolicy.buildCarveOuts is SecretPaths, which is a superset
//     of SecretEntriesAlways, so <home>/entities is a carve-out. IsCarveOut's
//     own-tree exception can never re-admit it, because that exception only
//     fires when the turn's WorkDir is a proper descendant of the carve-out
//     root — and no turn's work dir is ever inside entities/.
//
// That is the same protection entities/agents already relies on for the
// Constraint #6 tool-policy map (ADR-054 §4), which is the closest existing
// analogue: a per-entity record that decides what an agent may do, kept where
// the agent cannot reach it.
//
// TestMountStorePathIsInTheDeniedSet (pkg/tools) asserts the containment
// relationship against fspolicy.SecretPathsAlways directly, so this reasoning
// is enforced rather than merely written down.

// mountStoreRecord is the on-disk shape of a per-workspace mount file.
//
// WorkspaceID is stored (and cross-checked on load) even though the filename
// already carries it: a file whose recorded id disagrees with its own name has
// been moved or hand-assembled, and a write grant is not something to serve
// from a record that cannot account for its own identity.
type mountStoreRecord struct {
	WorkspaceID string  `json:"workspace_id"`
	Mounts      []Mount `json:"mounts,omitempty"`
}

// MountStoreDir returns $OMNIPUS_HOME/entities/mounts — the directory holding
// every workspace's mount record. Exported so tests can assert its position
// relative to the secret set without re-deriving the layout by hand (a test
// that hardcodes the path would keep passing if the real one moved).
func MountStoreDir(home string) string {
	return filepath.Join(home, "entities", "mounts")
}

// MountStorePath returns the mount-record path for workspace id, or an error
// for an id that fails safeID (the same gate every other id-addressed path in
// this package passes through — an unchecked id here would be a path traversal
// into, or out of, the store directory).
func MountStorePath(home, id string) (string, error) {
	if !safeID(id) {
		return "", fmt.Errorf("%w: %q", ErrInvalidWorkspaceID, id)
	}
	return filepath.Join(MountStoreDir(home), id+".json"), nil
}

// Validate enforces the shape invariants a Mount must satisfy before it is
// allowed to contribute a write grant. Defence in depth: CreateMount already
// validates everything here at create time, so a record that fails this check
// was corrupted, truncated, hand-edited, or restored from a foreign install.
//
// This is deliberately a SHAPE check and not a re-run of CheckMountTarget. Re-
// running the target classification would prove nothing about a hostile entry:
// host_path "/" passes it by design (FR-7.6 warn-and-allow). What this catches
// is an entry that could never have come out of CreateMount at all:
//
//   - a name that is not a single safe path segment (it names a location under
//     work/, so a separator or ".." in it is a traversal);
//   - a host path that is empty, relative, or not in cleaned form. CreateMount
//     always stores filepath.Clean(EvalSymlinks(...)), which is absolute and
//     lexically clean by construction, so anything else did not come from it.
//
// A mount that fails this is DROPPED by LoadMounts, never repaired and never
// trusted — fail closed.
func (m Mount) Validate() error {
	if err := ValidateMountName(m.Name); err != nil {
		return err
	}
	if m.HostPath == "" {
		return fmt.Errorf("%w: mount %q has an empty host_path", ErrMountTargetInvalid, m.Name)
	}
	if !filepath.IsAbs(m.HostPath) {
		return fmt.Errorf("%w: mount %q host_path %q is not absolute", ErrMountTargetInvalid, m.Name, m.HostPath)
	}
	if filepath.Clean(m.HostPath) != m.HostPath {
		return fmt.Errorf("%w: mount %q host_path %q is not in cleaned form", ErrMountTargetInvalid, m.Name, m.HostPath)
	}
	return nil
}

// loadMountStore reads workspace id's mount record and returns the entries
// that survive validation.
//
// Failure modes and their (deliberately different) outcomes:
//
//   - invalid id, unreadable file, malformed JSON, or an id mismatch: ok=false.
//     Callers treat that as "no grants" — a mount list that cannot be read is
//     never approximated.
//   - file absent: ok=true with no entries. A workspace with no mounts has no
//     file; that is the normal state, not an error.
//   - an individual entry failing Mount.Validate, or repeating a name already
//     seen: that ENTRY is dropped with a WARN, the rest are kept. Dropping one
//     bad entry fails closed for that entry without silently disarming mounts
//     an operator legitimately created.
func loadMountStore(home, id string) ([]Mount, bool) {
	path, err := MountStorePath(home, id)
	if err != nil {
		logger.WarnCF("workspace", "mount store: refusing to read a record for an unsafe workspace id", map[string]any{
			"workspace_id": id, "error": err.Error(),
		})
		return nil, false
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is built from a safeID-checked id under the store dir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, true
		}
		logger.WarnCF("workspace", "mount store: unreadable record — treating this workspace as having no mounts", map[string]any{
			"workspace_id": id, "path": path, "error": err.Error(),
		})
		return nil, false
	}
	var rec mountStoreRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		logger.WarnCF("workspace", "mount store: malformed record — treating this workspace as having no mounts", map[string]any{
			"workspace_id": id, "path": path, "error": err.Error(),
		})
		return nil, false
	}
	if rec.WorkspaceID != "" && rec.WorkspaceID != id {
		logger.WarnCF("workspace", "mount store: record's workspace_id disagrees with its filename — refusing to grant from it", map[string]any{
			"workspace_id": id, "path": path, "recorded_workspace_id": rec.WorkspaceID,
		})
		return nil, false
	}

	out := make([]Mount, 0, len(rec.Mounts))
	seen := make(map[string]struct{}, len(rec.Mounts))
	for _, m := range rec.Mounts {
		if err := m.Validate(); err != nil {
			logger.WarnCF("workspace", "mount store: dropping an invalid mount entry", map[string]any{
				"workspace_id": id, "path": path, "name": m.Name, "host_path": m.HostPath, "error": err.Error(),
			})
			continue
		}
		if _, dup := seen[m.Name]; dup {
			logger.WarnCF("workspace", "mount store: dropping a duplicate mount name", map[string]any{
				"workspace_id": id, "path": path, "name": m.Name,
			})
			continue
		}
		seen[m.Name] = struct{}{}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

// saveMountStore atomically persists mounts for workspace id, under the same
// advisory-lock + temp-file-rename discipline every other record writer in this
// package uses (saveWorkspaceRecord). An empty list removes the file rather
// than leaving an empty husk, so "no mounts" has exactly one on-disk
// representation.
//
// Callers MUST already hold LockID(id) across the full load-modify-write.
func saveMountStore(home, id string, mounts []Mount) error {
	path, err := MountStorePath(home, id)
	if err != nil {
		return err
	}
	if len(mounts) == 0 {
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return fmt.Errorf("workspace: mount store: remove %s: %w", path, rmErr)
		}
		return nil
	}
	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return fmt.Errorf("workspace: mount store: mkdir %s: %w", dir, mkErr)
	}
	data, err := json.MarshalIndent(mountStoreRecord{WorkspaceID: id, Mounts: mounts}, "", "  ")
	if err != nil {
		return fmt.Errorf("workspace: mount store: marshal %s: %w", id, err)
	}
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// DeleteMountStore removes workspace id's entire mount record. Called from the
// workspace-delete cascade so a deleted workspace leaves no orphaned grant
// record behind. The operator's real folders are never touched — this removes
// only the record of the grants, exactly as DeleteMount does for one of them
// (FR-8.6).
//
// Absent record is success: the cascade must be idempotent and must not fail a
// delete over a file that was already gone.
func DeleteMountStore(home, id string) error {
	unlock := LockID(id)
	defer unlock()

	path, err := MountStorePath(home, id)
	if err != nil {
		return err
	}
	if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return fmt.Errorf("workspace: mount store: remove %s: %w", path, rmErr)
	}
	return nil
}
