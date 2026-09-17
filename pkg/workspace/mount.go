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
	// Lock the record's sidecar, never the record this write renames over (see
	// fileutil.SidecarLockPath). pkg/sysagent/tools' writeEntity writes the same
	// file under the same sidecar lock.
	return fileutil.WithFlock(fileutil.SidecarLockPath(path), func() error {
		return writeFileAtomicFn(path, data, 0o600)
	})
}

// writeFileAtomicFn is fileutil.WriteFileAtomic, held in a package variable so
// a test can pause a record write inside its lock (store_lock_test.go). The
// workspace record, mount store and delegation store writers all go through
// it. Production code never reassigns it.
var writeFileAtomicFn = fileutil.WriteFileAtomic

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
		return "", fmt.Errorf("%w: %q: %w", ErrMountTargetInvalid, abs, statErr)
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
// IsBroadMountTarget is the exported form, for callers that must answer "is
// this grant broad?" about an ALREADY-STORED mount rather than about one being
// created.
//
// Breadth is deliberately NOT persisted on Mount: it is a property of the path,
// and recomputing it keeps one definition of "broad" instead of a stored copy
// that goes stale the moment this list changes. The Library needs it to mark
// an existing mount as a broad grant — without this, the warning would appear
// only in the create response, i.e. exactly once, in the moment the operator is
// least likely to revisit it.
func IsBroadMountTarget(resolved string) bool { return isBroadMountTarget(resolved) }

func isBroadMountTarget(resolved string) bool {
	if resolved == string(filepath.Separator) {
		return true
	}
	// Own home, in BOTH spellings (cleaned $HOME and its realpath — C9): the
	// warning classification must agree with isSystemMountTarget's own-home
	// exemption, or a symlink-spelled home would be exempted from the refusal
	// yet never warned, i.e. a quiet broad grant, which FR-7.4 forbids.
	if isOwnHomeMountTarget(resolved) {
		return true
	}
	if isSystemMountTarget(resolved) {
		return true
	}
	if _, listed := broadMountRoots[resolved]; listed {
		return true
	}
	// Compare on real paths too: on macOS /tmp and /var are symlinks into
	// /private, so a target the operator typed as /tmp arrives here as
	// /private/tmp and must still be judged as the broad location it is
	// (UAT 2026-09-13 D-117: /tmp mounted with no warning at all).
	for root := range broadMountRoots {
		if realPath, err := filepath.EvalSymlinks(root); err == nil && realPath == resolved {
			return true
		}
	}
	// Somebody's home directory: a direct child of /Users or /home.
	if parent := filepath.Dir(resolved); parent == "/Users" || parent == "/home" {
		return true
	}
	return false
}

// broadMountRoots are locations that are LEGITIMATE to mount but so wide that
// the operator must be warned (FR-7.4 warn-and-allow): every file under them
// becomes writable by every agent on the workspace.
var broadMountRoots = map[string]struct{}{
	"/tmp": {}, "/private/tmp": {}, "/var/tmp": {}, "/private/var/tmp": {},
	"/opt": {}, "/Users": {}, "/home": {}, "/Volumes": {}, "/Applications": {},
	"/mnt": {}, "/media": {}, "/srv": {},
}

// systemMountRoots are operating-system-owned trees that no agent workspace
// legitimately lives in. Mounting the root of one — or a DIRECT child of it
// (Claude review 2026-09-14 C9: /etc/ssh, /usr/bin, /var/root,
// /Library/LaunchDaemons were previously never refused at any spelling) — is
// refused outright (UAT 2026-09-13 D-117): writing there is never "a folder
// the agent works in", it is a machine-wide change, and a refusal here is
// the one case the founder's 2026-08-12 warn-and-allow ruling did not
// contemplate (that ruling addressed $HOME and /, which remain
// warn-and-allow). Anything DEEPER than a direct child is a user path and is
// not refused by this list — that is what keeps macOS's per-user temp tree
// (/private/var/folders/…) mountable, and it is why the deliberately broad
// tmp family (/tmp, /private/tmp, /var/tmp — all direct children of system
// roots) is carved out below (isBroadMountLocation) rather than re-refused
// here.
var systemMountRoots = map[string]struct{}{
	"/etc": {}, "/private/etc": {}, "/usr": {}, "/bin": {}, "/sbin": {}, "/lib": {}, "/lib64": {},
	"/var": {}, "/private/var": {}, "/private": {}, "/System": {}, "/Library": {},
	"/dev": {}, "/proc": {}, "/sys": {}, "/boot": {}, "/root": {}, "/cores": {},
	`C:\Windows`: {}, `C:\Program Files`: {}, `C:\Program Files (x86)`: {},
}

// isOwnHomeMountTarget reports whether resolved is the CURRENT process's own
// home directory, comparing both spellings that matter: the cleaned value of
// $HOME and that path's realpath (on macOS a home spelled /var/root resolves
// to /private/var/root — both forms are the user's own home). It is the
// CI go-test #2 fix: on the root-running worker $HOME IS /root, a system
// root, and before this exemption that made the user's own home arrive as a
// D-117 refusal instead of the broad-target warning the founder's
// 2026-08-12 ruling grants it. The comparison is deliberately NOT
// case-folded: an exemption may miss a case variant of the home (fail
// closed, the refusal stands) but must never fire on anything the process
// did not literally configure as its home.
func isOwnHomeMountTarget(resolved string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	if filepath.Clean(home) == resolved {
		return true
	}
	if realHome, rerr := resolveExistingDir(home); rerr == nil && realHome == resolved {
		return true
	}
	return false
}

// foldedSameOrDirectChild reports whether child is parent itself or exactly
// one path segment below it, compared WITHOUT regard to letter case.
//
// Case folding is unconditional, mirroring pkg/sandbox's pathCoversFold
// (exec_paths.go) and for the same two reasons its doc comment gives: case
// sensitivity is a property of the MOUNT, not of the OS (case-insensitive
// APFS accepted /PRIVATE/etc, /system and /USR past the old exact-match
// refusal — verified empirically in the Claude review), and the two failure
// directions are not symmetric — a false MATCH here costs one mount target
// the operator can see and re-scope, while a false MISS silently re-opens a
// system tree. Both sides are lowercased in full before the segment test
// (never sliced by byte length) so a multi-byte directory name cannot be
// split mid-rune.
func foldedSameOrDirectChild(parent, child string) bool {
	sep := string(filepath.Separator)
	p := strings.ToLower(strings.TrimSuffix(filepath.Clean(parent), sep))
	c := strings.ToLower(strings.TrimSuffix(filepath.Clean(child), sep))
	if p == "" {
		// Clean+Trim collapses the filesystem root to "": "/" covers everything.
		return true
	}
	if c == p {
		return true
	}
	if !strings.HasPrefix(c, p+sep) {
		return false
	}
	rest := c[len(p)+1:]
	return rest != "" && !strings.Contains(rest, sep)
}

// pathComparisonForms returns every form a path must be compared in: the
// cleaned declared form plus its symlink-resolved form when it differs —
// the same additive pair pkg/sandbox's comparisonForms keeps, so the macOS
// /etc -> /private/etc pair is judged identically whichever spelling
// arrives. A form that cannot be resolved (does not exist on this host) is
// simply absent, never an error.
func pathComparisonForms(p string) []string {
	clean := filepath.Clean(p)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil || resolved == clean {
		return []string{clean}
	}
	return []string{clean, resolved}
}

// isBroadMountLocation reports whether resolved IS one of the deliberately
// warn-and-allow broad locations (folded, real paths included). It exists as
// a CARVE-OUT from the system-root refusal: the tmp family (/tmp ->
// /private/tmp, /var/tmp -> /private/var/tmp) consists of direct children of
// system roots that D-117 and the founder's ruling explicitly keep as
// warn-and-allow, so the depth rule must not re-refuse them.
func isBroadMountLocation(resolved string) bool {
	for root := range broadMountRoots {
		for _, form := range pathComparisonForms(root) {
			if foldedSameOrDirectChild(form, resolved) {
				return true
			}
		}
	}
	return false
}

// isSystemMountTarget reports whether resolved is a system tree root or a
// direct child of one, folded case-insensitively and judged on real paths
// as well as typed ones. Two exemptions run BEFORE the list, both fail-safe
// in the "refuse" direction they narrow:
//
//   - the current process's own home is never a system target (it gets the
//     broad-target warning instead — CI go-test #2);
//   - a deliberately broad location (the tmp family) is never re-refused by
//     the depth rule (D-117 keeps /tmp warn-and-allow).
//
// Deeper-than-direct-child paths are NOT refused by this function, on
// purpose: see systemMountRoots' doc comment.
func isSystemMountTarget(resolved string) bool {
	if isOwnHomeMountTarget(resolved) {
		return false
	}
	if isBroadMountLocation(resolved) {
		return false
	}
	for root := range systemMountRoots {
		for _, form := range pathComparisonForms(root) {
			if foldedSameOrDirectChild(form, resolved) {
				return true
			}
		}
	}
	return false
}

// IsSystemMountTarget is the exported form of isSystemMountTarget, for the
// host-folder picker, so it greys out the same directories the create path
// refuses.
func IsSystemMountTarget(resolved string) bool { return isSystemMountTarget(resolved) }

// CheckMountTarget resolves rawHostPath to its realpath and classifies it
// against omnipusHome (spec FR-7.4-FR-7.7, ADR-063 D6, operator decision
// 2026-08-12 overruling ADR-063 D6's original wider-refusal text: "warn and
// allow applies to all but the omnipus directory").
//
//   - REFUSES (FR-7.5, non-nil error wrapping ErrMountRefused) when the
//     REALPATH-RESOLVED target IS omnipusHome or lies INSIDE it — so a
//     symlink pointing at $OMNIPUS_HOME is refused too, which is the form
//     FR-7.5 calls out as the one nobody would reach for directly but must
//     still be covered — and (UAT 2026-09-13 D-117) when the target IS the
//     root of an operating-system tree (systemMountRoots: /etc, /usr,
//     /System, …). / and $HOME stay warn-and-allow per the 2026-08-12 ruling.
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
	if isSystemMountTarget(resolved) {
		return "", "", fmt.Errorf(
			"%w: %q is an operating-system directory — no agent workspace lives there, and writing into it changes the whole machine. Mount a specific folder inside it if you really need one (UAT D-117)",
			ErrMountRefused, resolved,
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
//
// This two-value shape cannot tell "no mounts recorded" apart from "some
// mounts were recorded but dropped" — both return ok=true. Most callers only
// ever act on the surviving mounts, for which that distinction is
// irrelevant. A caller whose own correctness answer depends on having opened
// EVERY recorded mount (e.g. a "did this search cover the whole workspace?"
// claim) needs LoadMountsWithDropStatus instead.
func LoadMounts(home, id string) ([]Mount, bool) {
	mounts, ok, _ := loadMountStore(home, id)
	return mounts, ok
}

// LoadMountsWithDropStatus is LoadMounts plus the one bit LoadMounts cannot
// express: droppedInvalid is true when ok=true but at least one recorded
// mount entry was silently dropped by loadMountStore (it failed
// Mount.Validate, or repeated an earlier entry's name). Added for I4
// (2026-09 code review): a caller that reports "I searched everything" needs
// to know when that is false even though ok itself is true. See
// loadMountStore's own doc comment for the full failure-mode table.
func LoadMountsWithDropStatus(home, id string) (mounts []Mount, ok bool, droppedInvalid bool) {
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

	existing, ok, _ := loadMountStore(home, id)
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

	mounts, ok, _ := loadMountStore(home, id)
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
