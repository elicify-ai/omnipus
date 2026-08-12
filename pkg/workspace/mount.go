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

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Mount is the canonical on-disk representation of a workspace mount — a
// named write-grant on a real local folder (spec FR-5, ADR-063 D4). Stored in
// the mount store under $OMNIPUS_HOME/entities/mounts/<workspaceID>.json and
// deliberately NOT on the workspace record, which a sandboxed child can write
// — see mountstore.go's leading comment for the full chain that made storing
// it there a self-service write grant. HostPath is realpath-resolved at CREATE time
// (FR-5.3) and is NEVER re-resolved afterward: the resolved form is what is
// persisted, and it never changes once written. Liveness ("does this still
// exist on THIS machine") is a separate, always-live computation
// (MountStatus) — never cached here and never used to silently rewrite
// HostPath (FR-8.5: a mount is never silently re-bound to a different path
// that happens to exist at the same name).
type Mount struct {
	// Name is a single path segment identifying this mount inside work/
	// (FR-5.2): non-empty, no separators, not ".."/containing "..", unique
	// within the workspace, and not colliding with an existing work/ entry.
	Name string `json:"name"`

	// HostPath is the realpath-resolved (symlink-free) absolute host
	// filesystem path this mount grants write access to (FR-5.3).
	HostPath string `json:"host_path"`
}

// Sentinel errors for mount operations. Each is wrapped with the offending
// value via %w so callers can both errors.Is-match the class and read the
// specific detail in the message.
var (
	// ErrInvalidMountName is returned by ValidateMountName/CreateMount for a
	// name that fails FR-5.2's shape rules.
	ErrInvalidMountName = errors.New("workspace: invalid mount name")

	// ErrMountNameCollision is returned when name already names another
	// mount on this workspace, or already exists as some other entry under
	// work/ (FR-5.2).
	ErrMountNameCollision = errors.New("workspace: mount name collision")

	// ErrMountRefused is returned when the resolved target IS or lies
	// INSIDE $OMNIPUS_HOME (FR-7.5, ADR-063 D6). This is the ONE refusal —
	// every other target warns and proceeds (FR-7.6).
	ErrMountRefused = errors.New("workspace: mount target refused")

	// ErrMountTargetInvalid is returned when the raw host path cannot be
	// resolved to an existing, real, on-disk directory. A mount grants
	// access to "a real local folder" (R-7) — there is nothing to grant
	// access to otherwise.
	ErrMountTargetInvalid = errors.New("workspace: mount target is not an existing directory")

	// ErrMountNotFound is returned by DeleteMount when name does not name
	// any mount on the workspace.
	ErrMountNotFound = errors.New("workspace: mount not found")
)

// loadWorkspaceRecord reads the FULL workspace record for id. This package's
// doc comment (default.go) states that pkg/gateway owns the authoritative
// CRUD + wire mapping; mount lifecycle is the one exception, matching the
// precedent pkg/sysagent/tools/workspace.go already set (its own
// readWorkspaceFromDisk/write helpers, duplicated rather than shared, atop
// the same workspace.Workspace struct).
//
// The mount lifecycle uses this for ONE thing only: to establish that the
// workspace exists (so an unknown id surfaces as os.ErrNotExist and the REST
// layer can map it to 404). It reads NO security-relevant field from the
// record and no longer writes it at all — the mount list itself lives in the
// mount store, out of a sandboxed child's reach (mountstore.go).
func loadWorkspaceRecord(home, id string) (Workspace, error) {
	if !safeID(id) {
		return Workspace{}, fmt.Errorf("%w: %q", ErrInvalidWorkspaceID, id)
	}
	path := filepath.Join(dirFor(home), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Workspace{}, fmt.Errorf("workspace: %q: %w", id, os.ErrNotExist)
		}
		return Workspace{}, fmt.Errorf("workspace: read %s: %w", id, err)
	}
	var w Workspace
	if err := json.Unmarshal(data, &w); err != nil {
		return Workspace{}, fmt.Errorf("workspace: parse %s: %w", id, err)
	}
	if w.Status == "" {
		w.Status = "active"
	}
	return w, nil
}

// saveWorkspaceRecord atomically persists w to
// $home/workspaces/<w.ID>.json — the same temp-file+rename+flock pattern
// pkg/gateway's writeWorkspaceFile uses, so a mount write is exactly as
// crash-safe as any other workspace write.
// SaveRecord atomically writes w to $OMNIPUS_HOME/workspaces/<id>.json under
// the same advisory lock every other reader/writer of that file takes.
//
// Exported because pkg/gateway had a byte-for-byte duplicate of this
// (writeWorkspaceFile) — same path, same marshalling, same flock, same atomic
// write. Two implementations of one persistence rule is how the two file-access
// layers drifted in the first place, so gateway now delegates here. The type is
// already canonical (gateway's storedWorkspace is a type ALIAS for
// workspace.Workspace), so this converges the write path to match.
func SaveRecord(home string, w Workspace) error { return saveWorkspaceRecord(home, w) }

func saveWorkspaceRecord(home string, w Workspace) error {
	dir := dirFor(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return fmt.Errorf("workspace: marshal %s: %w", w.ID, err)
	}
	path := filepath.Join(dir, w.ID+".json")
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// ValidateMountName enforces FR-5.2's shape rule on name alone (no
// uniqueness/collision check — those need workspace context, see
// checkMountNameAvailable). A single path segment: non-empty, contains no
// "/" or "\", and is neither ".." nor a string containing "..". "." is also
// refused even though the spec text doesn't name it explicitly, because a
// mount named "." would collide with work/ itself, not an entry inside it.
func ValidateMountName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: empty", ErrInvalidMountName)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidMountName, name)
	}
	if name == "." {
		return fmt.Errorf("%w: %q names the work directory itself", ErrInvalidMountName, name)
	}
	if name == ".." || strings.Contains(name, "..") {
		return fmt.Errorf("%w: %q contains \"..\"", ErrInvalidMountName, name)
	}
	return nil
}

// checkMountNameAvailable enforces the two collision rules FR-5.2 names:
// name must be unique among the workspace's existing mounts, and must not
// already exist as some other entry under work/ (a real file/dir/symlink an
// agent or the operator put there before the mount was requested).
func checkMountNameAvailable(existing []Mount, workDir, name string) error {
	for _, m := range existing {
		if m.Name == name {
			return fmt.Errorf("%w: %q already names a mount on this workspace", ErrMountNameCollision, name)
		}
	}
	p := filepath.Join(workDir, name)
	if _, err := os.Lstat(p); err == nil {
		return fmt.Errorf("%w: %q already exists in work/", ErrMountNameCollision, name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("workspace: check work/ collision for %q: %w", name, err)
	}
	return nil
}

// resolveExistingDir resolves path to an absolute, realpath'd (symlink-free)
// location, requiring that it currently exists and is a directory.
// ErrMountTargetInvalid wraps both "doesn't exist" and "is not a directory"
// so a caller can distinguish "target problem" from every other error class
// (ErrMountRefused, ErrMountNameCollision) with one errors.Is check.
func resolveExistingDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: empty path", ErrMountTargetInvalid)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("workspace: resolve absolute path %q: %w", path, err)
	}
	if !filepath.IsAbs(path) {
		// filepath.Abs already refuses nothing for a relative input (it
		// joins against the process cwd), but a mount target MUST be
		// spelled as an absolute path by the caller (FR-5.3) — silently
		// resolving "foo" against the gateway process's own cwd would
		// mount whatever happens to be there, which is never what an
		// operator typing a bare relative string meant.
		return "", fmt.Errorf("%w: %q is not absolute", ErrMountTargetInvalid, path)
	}
	fi, statErr := os.Stat(abs)
	if statErr != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrMountTargetInvalid, abs, statErr)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%w: %q is not a directory", ErrMountTargetInvalid, abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("workspace: resolve realpath of %q: %w", abs, err)
	}
	return filepath.Clean(resolved), nil
}

// isWithinOrEqualPath reports whether candidate is root itself or lives
// strictly under it, guarded by a trailing separator so "/a/bc" does not
// match root "/a/b". Mirrors pkg/fspolicy's isWithinOrEqual exactly (that
// helper is unexported in a stdlib-only leaf this package must not import
// from just for this one function — see the Constraints note on
// pkg/fspolicy in the task brief), so both packages apply the identical
// containment rule.
func isWithinOrEqualPath(candidate, root string) bool {
	if candidate == root {
		return true
	}
	rootWithSep := root
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	return strings.HasPrefix(candidate, rootWithSep)
}

// isBroadMountTarget reports whether resolved is one of the small set of
// canonically "risky" locations FR-7.4 names on its own terms (home
// directory, filesystem root) — independent of whether $OMNIPUS_HOME
// happens to resolve underneath it. In the default install $OMNIPUS_HOME
// always lives under $HOME (~/.omnipus), so the FR-7.6 containment check in
// CheckMountTarget already warns for both; this exists so the SAME warning
// still fires for an install where $OMNIPUS_HOME is configured somewhere
// $HOME does not contain (e.g. OMNIPUS_HOME on a separate volume) — FR-7.4's
// "home directory, filesystem root" risk is about the TARGET's own breadth,
// not merely about incidentally containing this installation.
func isBroadMountTarget(resolved string) bool {
	if resolved == string(filepath.Separator) {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == resolved {
		return true
	}
	switch resolved {
	case "/etc", "/usr", "/var", "/System", "/Library", "/bin", "/sbin", "/opt", "/Users", "/home":
		return true
	}
	return false
}

// CheckMountTarget resolves rawHostPath to its realpath and classifies it
// against omnipusHome (spec FR-7.4-FR-7.7, ADR-063 D6, operator decision
// 2026-08-12 overruling ADR-063 D6's original wider-refusal text: "warn and
// allow applies to all but the omnipus directory").
//
//   - REFUSES (FR-7.5, non-nil error wrapping ErrMountRefused) only when the
//     REALPATH-RESOLVED target IS omnipusHome or lies INSIDE it — so a
//     symlink pointing at $OMNIPUS_HOME is refused too, which is the form
//     FR-7.5 calls out as the one nobody would reach for directly but must
//     still be covered.
//   - Otherwise WARNS AND ALLOWS (FR-7.6/FR-7.4): a non-empty warning string
//     is returned (nil error) when the target CONTAINS omnipusHome (mounting
//     $HOME or / when $OMNIPUS_HOME lives underneath — FR-7.6) or is
//     otherwise a canonically broad/risky location (FR-7.4,
//     isBroadMountTarget). Every other target returns ("", "") — allowed,
//     no warning.
//
// The FR-7.6 reasoning this function encodes: refusing a broad mount would
// take away something legitimate to guard against a danger the secret set
// (pkg/fspolicy, subtracted from every grant regardless of its source)
// already handles independently of any mount — see FR-7.7's regression
// coverage in mount_test.go, which proves that reasoning rather than
// assuming it.
func CheckMountTarget(rawHostPath, omnipusHome string) (resolved string, warning string, err error) {
	resolved, err = resolveExistingDir(rawHostPath)
	if err != nil {
		return "", "", err
	}
	resolvedHome, err := resolveExistingDir(omnipusHome)
	if err != nil {
		return "", "", fmt.Errorf("workspace: resolve $OMNIPUS_HOME %q: %w", omnipusHome, err)
	}

	if isWithinOrEqualPath(resolved, resolvedHome) {
		relation := "lies inside"
		if resolved == resolvedHome {
			relation = "IS"
		}
		return "", "", fmt.Errorf(
			"%w: %q %s the Omnipus data directory (%q) — mounting it would make config.json and master.key writable and let an agent disable its own sandbox (FR-7.5, ADR-063 D6)",
			ErrMountRefused, resolved, relation, resolvedHome,
		)
	}

	var reasons []string
	if isWithinOrEqualPath(resolvedHome, resolved) {
		reasons = append(reasons, fmt.Sprintf(
			"contains this Omnipus installation's own data directory (%s) — the installation's own secrets remain protected independently of this mount (its secret set is subtracted from every write grant regardless of where the grant came from), but every OTHER file under %s becomes writable by any agent on this workspace",
			resolvedHome, resolved,
		))
	} else if isBroadMountTarget(resolved) {
		reasons = append(reasons, fmt.Sprintf(
			"is a broad location (%s) — every file under it becomes writable by any agent on this workspace",
			resolved,
		))
	}
	if len(reasons) == 0 {
		return resolved, "", nil
	}
	return resolved, fmt.Sprintf("mounting %q %s", resolved, strings.Join(reasons, "; and ")), nil
}

// MountStatus reports whether m's target currently resolves on THIS machine
// (FR-8.2/FR-8.5): "ok" when m.HostPath still exists and is a directory,
// "broken" otherwise (deleted, unmounted drive, restored on a different
// machine, symlink loop, permission error — every failure mode collapses to
// "broken", never a crash and never a distinction the caller has to parse).
//
// This is computed LIVE, every call, from the filesystem — never cached,
// never persisted on the Mount record itself (FR-5's wire schema marks
// status readOnly/server-computed for exactly this reason). Critically, it
// NEVER changes m.HostPath: a broken mount is reported broken, not silently
// re-pointed at some other directory that happens to exist at the same
// leaf name (FR-8.5) or recreated as an empty one (FR-8.3) — this function
// only ever reads, it has no side effect on disk at all.
func MountStatus(m Mount) string {
	fi, err := os.Stat(m.HostPath)
	if err != nil || !fi.IsDir() {
		return "broken"
	}
	return "ok"
}

// LoadMounts returns the mounts recorded for workspace id.
//
// The source is the mount store ($OMNIPUS_HOME/entities/mounts/<id>.json),
// NEVER the workspace record — a `mounts` array left in an old workspace
// record, or planted in one by a sandboxed child, is not read by anything and
// grants nothing (mountstore.go's leading comment explains why that
// distinction is the whole point of this store).
//
// ok is false only when the record exists but cannot be trusted (unsafe id,
// unreadable file, malformed JSON, id mismatch); a workspace that simply has
// no mounts returns (nil, true). Individual entries failing Mount.Validate are
// dropped with a WARN rather than trusted — see loadMountStore.
func LoadMounts(home, id string) ([]Mount, bool) {
	return loadMountStore(home, id)
}

// AllowedMountRoots returns the resolved host_path of every mount belonging
// to workspace id, for population into fspolicy.FSPolicy.AllowedRoots
// (FR-6.1/FR-6.3, ADR-063 D4) by whichever call site constructs a turn's
// authored policy (pkg/tools.ResolveTurnFSPolicy, per that function's own
// doc comment anticipating this: "the moment FR-5/FR-6 populates
// AllowedRoots, without another edit to this file"). A mount grants WRITE
// and nothing else — reads are already open under ADR-062 regardless of
// this list.
//
// Deliberately NOT filtered by MountStatus: a currently-broken mount still
// contributes its (non-resolving) HostPath to the list. That is harmless —
// nothing exists there, so nothing becomes writable — and avoids a second,
// independently-drifting notion of "live" between this function and
// MountStatus/the UI's status badge.
//
// Returns nil (not an empty, non-nil slice) for a workspace with no mounts,
// an unreadable record, or an invalid id, matching FSPolicy.AllowedRoots'
// existing "nil means no grants" contract.
//
// SECURITY: this is a write-grant computation, so its input must be a source
// the granted principal cannot write. That source is the mount store, not the
// workspace record — see LoadMounts and mountstore.go's leading comment.
// Every entry it returns has passed Mount.Validate.
func AllowedMountRoots(home, id string) []string {
	mounts, ok := LoadMounts(home, id)
	if !ok || len(mounts) == 0 {
		return nil
	}
	roots := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if err := m.Validate(); err != nil {
			// Unreachable via loadMountStore, which already drops these.
			// Repeated here because this is the function whose OUTPUT
			// becomes a kernel and app-layer write grant: a future second
			// reader wired in above must not be able to bypass validation
			// by feeding this loop.
			logger.WarnCF("workspace", "mount store: refusing to grant from an invalid mount entry", map[string]any{
				"workspace_id": id, "name": m.Name, "host_path": m.HostPath, "error": err.Error(),
			})
			continue
		}
		roots = append(roots, m.HostPath)
	}
	if len(roots) == 0 {
		return nil
	}
	return roots
}

// CreateMount validates name and rawHostPath, and — on success — persists a
// new mount on workspace id and materializes it as a symlink work/<name> ->
// resolved host path (FR-5.4). Returns the created Mount, an advisory
// warning string (FR-7.4/FR-7.6 — empty when the target warranted none),
// and an error for any validation/refusal failure.
//
// Ordering matters for FR-8.5's "never silently re-bound" guarantee and for
// leaving no partial state behind on any error path:
//  1. Validate name shape (no I/O).
//  2. Acquire the per-workspace lock and load the current mount store (so a
//     concurrent create against the same workspace can never race past the
//     collision checks below — mirrors the LockID contract every other
//     load-modify-write site in this codebase follows). The workspace record
//     is read too, but only to establish that the workspace exists.
//  3. Check name collisions (existing mounts, existing work/ entries).
//  4. Resolve and classify the target (CheckMountTarget) — refuses before
//     anything is materialized.
//  5. Ensure work/ exists (also auto-inits the evidence git layer).
//  6. Create the symlink.
//  7. Persist the record. On a persist failure, the just-created symlink is
//     removed — an unrecorded symlink on disk would be worse than no mount
//     at all (an agent could write through it with nothing in the UI/API
//     ever showing it exists).
func CreateMount(home, id, name, rawHostPath string) (Mount, string, error) {
	if err := ValidateMountName(name); err != nil {
		return Mount{}, "", err
	}

	unlock := LockID(id)
	defer unlock()

	// Existence check only — no field of this record feeds any decision below.
	if _, err := loadWorkspaceRecord(home, id); err != nil {
		return Mount{}, "", fmt.Errorf("workspace: create mount: load workspace %s: %w", id, err)
	}

	existing, ok := loadMountStore(home, id)
	if !ok {
		// The store exists but could not be parsed. Appending to it would
		// silently discard whatever the operator had recorded, so refuse
		// rather than write over an unreadable grant list.
		return Mount{}, "", fmt.Errorf("workspace: create mount: mount store for %s is unreadable or malformed", id)
	}

	workDir, err := SafeWorkDir(home, id)
	if err != nil {
		return Mount{}, "", fmt.Errorf("workspace: create mount: %w", err)
	}
	if nameErr := checkMountNameAvailable(existing, workDir, name); nameErr != nil {
		return Mount{}, "", nameErr
	}

	resolved, warning, err := CheckMountTarget(rawHostPath, home)
	if err != nil {
		return Mount{}, "", err
	}

	if _, err := EnsureWorkDir(home, id); err != nil {
		return Mount{}, "", fmt.Errorf("workspace: create mount: ensure work dir: %w", err)
	}
	linkPath := filepath.Join(workDir, name)
	if err := os.Symlink(resolved, linkPath); err != nil {
		return Mount{}, "", fmt.Errorf("workspace: create mount: symlink %s -> %s: %w", linkPath, resolved, err)
	}

	m := Mount{Name: name, HostPath: resolved}
	// Defence in depth: the record about to be persisted must satisfy the same
	// invariant LoadMounts enforces on the way back in, so a create can never
	// write an entry its own reader would have to drop.
	if err := m.Validate(); err != nil {
		if rmErr := os.Remove(linkPath); rmErr != nil {
			logger.WarnCF("workspace", "create mount: failed to roll back symlink after refusing to persist an invalid record", map[string]any{
				"workspace_id": id, "name": name, "link": linkPath, "rollback_error": rmErr.Error(),
			})
		}
		return Mount{}, "", fmt.Errorf("workspace: create mount: refusing to persist an invalid record: %w", err)
	}
	if err := saveMountStore(home, id, append(append([]Mount{}, existing...), m)); err != nil {
		if rmErr := os.Remove(linkPath); rmErr != nil {
			logger.WarnCF("workspace", "create mount: failed to roll back symlink after a persist failure — a mount now exists on disk with no record of it", map[string]any{
				"workspace_id": id, "name": name, "link": linkPath, "persist_error": err.Error(), "rollback_error": rmErr.Error(),
			})
		}
		return Mount{}, "", fmt.Errorf("workspace: create mount: persist: %w", err)
	}

	if warning != "" {
		logger.WarnCF("workspace", "mount created against a risky target; proceeding per operator decision (warn-and-allow, FR-7.6)", map[string]any{
			"workspace_id": id, "name": name, "host_path": resolved, "warning": warning,
		})
	}
	return m, warning, nil
}

// DeleteMount removes the named mount's symlink and its record from
// workspace id. The operator's real folder at the mount's HostPath is NEVER
// touched (FR-8.6) — only a single filesystem link entry (os.Remove, never
// RemoveAll, never following the link) and the workspace's own record of it
// are removed.
//
// Defense in depth: if the path where the symlink should be is NOT actually
// a symlink (e.g. hand-edited on disk, or a directory an operator created
// there directly), DeleteMount refuses to touch it rather than risk
// deleting a real directory tree that happens to share the mount's name.
func DeleteMount(home, id, name string) error {
	unlock := LockID(id)
	defer unlock()

	// Existence check only — kept so an unknown workspace id still surfaces as
	// os.ErrNotExist for the REST layer's 404 mapping, rather than collapsing
	// into the "no such mount" case.
	if _, err := loadWorkspaceRecord(home, id); err != nil {
		return fmt.Errorf("workspace: delete mount: load workspace %s: %w", id, err)
	}

	mounts, ok := loadMountStore(home, id)
	if !ok {
		return fmt.Errorf("workspace: delete mount: mount store for %s is unreadable or malformed", id)
	}

	idx := -1
	for i, m := range mounts {
		if m.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("%w: %q", ErrMountNotFound, name)
	}

	workDir, err := SafeWorkDir(home, id)
	if err != nil {
		return fmt.Errorf("workspace: delete mount: %w", err)
	}
	linkPath := filepath.Join(workDir, name)
	if fi, statErr := os.Lstat(linkPath); statErr == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("workspace: delete mount: %q is not a symlink, refusing to remove it (possible manual on-disk tampering)", linkPath)
		}
		if rmErr := os.Remove(linkPath); rmErr != nil {
			return fmt.Errorf("workspace: delete mount: remove symlink %s: %w", linkPath, rmErr)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("workspace: delete mount: stat symlink %s: %w", linkPath, statErr)
	}

	remaining := append(append([]Mount{}, mounts[:idx]...), mounts[idx+1:]...)
	if err := saveMountStore(home, id, remaining); err != nil {
		return fmt.Errorf("workspace: delete mount: persist: %w", err)
	}
	return nil
}
