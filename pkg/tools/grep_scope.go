// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// grep's `path` dispatch and per-visited-file gates for #920
// (docs/internal/specs/read-boundary-consistency-spec.md; ADR-081 D4
// amendment of 2026-09-26). grep.go keeps the tool surface, the root
// assembly for the default search and the mount shorthand, and the
// rendering; this file holds what #920 added:
//
//   - the FR-001 dispatch: NUL pre-check, lexical mount-name shorthand, then
//     ONE uniform ResolvePath(FSOpList) for every other `path` — the same
//     single read decision read_file and list_directory use;
//   - the new absolute root type (ADR-081 D4 design steps 1-4) with its
//     FR-010 existence check, root_lost carrier and test-only seam;
//   - grepGateFS, the per-visited-file gates every grep root carries on top
//     of carveOutFS: the ADR-072 D10.3 registry skills gate, the agent
//     metadata guard, and founder decision D13 (the Linux kernel
//     pseudo-folders /proc, /sys and /dev are never walked; device files,
//     pipes and sockets are never opened).
package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// grepScopeStatOpenHook is FR-010's test-only seam, after the
// task.go::taskGoalEndedHook pattern: invoked in resolveScopedRoot after
// container.Stat(subPath) succeeded and before the root (or, for a regular
// file, its parent) is opened. Production never sets it — the pointer
// stays nil and the cost is one atomic load and a nil check per scoped
// root. A test sets it (setGrepScopeStatOpenHook) to remove the directory
// once, synchronously, so the "existed at the check, gone at the open"
// branch is driven deterministically, with no sleep and no goroutine.
var grepScopeStatOpenHook atomic.Pointer[func(subPath string)]

// setGrepScopeStatOpenHook installs fn (nil clears it) and returns a func
// that restores the previous hook.
func setGrepScopeStatOpenHook(fn func(subPath string)) (restore func()) {
	var next *func(string)
	if fn != nil {
		next = &fn
	}
	prev := grepScopeStatOpenHook.Swap(next)
	return func() { grepScopeStatOpenHook.Store(prev) }
}

func runGrepScopeStatOpenHook(subPath string) {
	if hook := grepScopeStatOpenHook.Load(); hook != nil {
		(*hook)(subPath)
	}
}

// grepAbsolutePostOpenHook is the same seam pattern for the post-open
// window — fires AFTER absoluteGrepRoot's os.OpenRoot(parent) (and the
// mount-anchored os.OpenRoot(mount.HostPath)) returns and BEFORE the
// SameFile check the A2 fix adds. A test sets it to swap the location
// AFTER the open has bound its file descriptor, so the bound fd points
// to the pre-swap inode while os.Stat(anchor) follows the post-swap
// symlink — the two stats differ and the SameFile check catches the
// discrepancy as root_lost. Production: nil, same one-atomic-load cost.
var grepAbsolutePostOpenHook atomic.Pointer[func(anchor string)]

// setGrepAbsolutePostOpenHook installs fn (nil clears it) and returns a
// func that restores the previous hook.
func setGrepAbsolutePostOpenHook(fn func(anchor string)) (restore func()) {
	var next *func(string)
	if fn != nil {
		next = &fn
	}
	prev := grepAbsolutePostOpenHook.Swap(next)
	return func() { grepAbsolutePostOpenHook.Store(prev) }
}

func runGrepAbsolutePostOpenHook(anchor string) {
	if hook := grepAbsolutePostOpenHook.Load(); hook != nil {
		(*hook)(anchor)
	}
}

// grepPreOpenRootHook drives a swap after capturing the admitted root's
// identity but immediately before the raw os.OpenRoot binds its descriptor.
var grepPreOpenRootHook atomic.Pointer[func(hostPath string)]

func setGrepPreOpenRootHook(fn func(hostPath string)) (restore func()) {
	var next *func(string)
	if fn != nil {
		next = &fn
	}
	prev := grepPreOpenRootHook.Swap(next)
	return func() { grepPreOpenRootHook.Store(prev) }
}

func runGrepPreOpenRootHook(hostPath string) {
	if hook := grepPreOpenRootHook.Load(); hook != nil {
		(*hook)(hostPath)
	}
}

// grepPreOpenIdentity walks the admitted realpath from its volume root,
// refusing symlink components. Each component is checked against the
// directory actually opened through the preceding os.Root: a swap between
// Lstat and OpenRoot cannot silently change the identity we capture. The
// returned identity is taken BEFORE the caller's raw host-path os.OpenRoot.
func grepPreOpenIdentity(canonical string) (fs.FileInfo, error) {
	if !filepath.IsAbs(canonical) || filepath.Clean(canonical) != canonical {
		return nil, fmt.Errorf("search root %q is not a clean absolute realpath: %w", canonical, fs.ErrPermission)
	}
	volume := filepath.VolumeName(canonical)
	base := volume + string(os.PathSeparator)
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if canonical == base {
		return root.Stat(".")
	}
	rel, err := filepath.Rel(base, canonical)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("search root %q cannot be traversed from its volume root: %w", canonical, fs.ErrPermission)
	}
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		before, statErr := root.Lstat(component)
		if statErr != nil {
			return nil, statErr
		}
		if !before.IsDir() || before.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("search root %q has a non-directory or symlink component %q: %w", canonical, component, fs.ErrPermission)
		}
		next, openErr := root.OpenRoot(component)
		if openErr != nil {
			return nil, openErr
		}
		after, checkErr := next.Stat(".")
		if checkErr != nil || !os.SameFile(before, after) {
			next.Close()
			if checkErr != nil {
				return nil, checkErr
			}
			return nil, fmt.Errorf("search root %q changed while capturing its identity: %w", canonical, fs.ErrPermission)
		}
		root.Close()
		root = next
	}
	return root.Stat(".")
}

// grepOpenedRootMatches checks the bound descriptor against the identity
// captured before its raw host-path open. An unanswerable Stat is not a match.
func grepOpenedRootMatches(root *os.Root, expected fs.FileInfo) bool {
	actual, err := root.Stat(".")
	return err == nil && os.SameFile(actual, expected)
}

// grepGateFIFOSwapHook is the A3 test-only seam (Opus security-lead
// review): invoked in refuseNonRegular (the OLD Stat-then-check path)
// and just BEFORE the non-blocking, root-confined open in the live path.
// Production never sets it — the pointer stays nil and the cost is one
// atomic load per Open. Tests replace a regular file with a FIFO here
// to prove the open never blocks.
var grepGateFIFOSwapHook atomic.Pointer[func(name string)]

func setGrepGateFIFOSwapHook(fn func(name string)) (restore func()) {
	var next *func(string)
	if fn != nil {
		next = &fn
	}
	prev := grepGateFIFOSwapHook.Swap(next)
	return func() { grepGateFIFOSwapHook.Store(prev) }
}

func runGrepGateFIFOSwapHook(name string) {
	if hook := grepGateFIFOSwapHook.Load(); hook != nil {
		(*hook)(name)
	}
}

// grepGatePreReopenHook fires after the opened file's kind has been checked.
// A test swaps its on-disk name here; the verified descriptor, never a
// second open of that name, must be handed to the reader.
var grepGatePreReopenHook atomic.Pointer[func(name string)]

func setGrepGatePreReopenHook(fn func(name string)) (restore func()) {
	var next *func(string)
	if fn != nil {
		next = &fn
	}
	prev := grepGatePreReopenHook.Swap(next)
	return func() { grepGatePreReopenHook.Store(prev) }
}

func runGrepGatePreReopenHook(name string) {
	if hook := grepGatePreReopenHook.Load(); hook != nil {
		(*hook)(name)
	}
}

// grepPathRefusalError marks an error as a refusal of the `path` argument by
// the single read decision (or by grep's own NUL pre-check): Execute audits it
// as path.access_denied (FR-020) and returns a permission-denied result.
// Every other resolveGrepRoots error (not found, not a directory, cannot open
// the workspace) is a plain error and writes no denial row.
type grepPathRefusalError struct{ err error }

func (r *grepPathRefusalError) Error() string { return r.err.Error() }
func (r *grepPathRefusalError) Unwrap() error { return r.err }

// grepScopeLostError is resolveScopedRoot's "existed at the Stat, could not
// be opened right after" failure. The absolute root type turns it into an
// unreachable root (FR-010: truncated root_lost, never a hard error); the
// workspace-relative and mount scopes keep reporting it as the error they
// always did (its message is unchanged).
type grepScopeLostError struct {
	msg string
	err error
}

func (e *grepScopeLostError) Error() string { return e.msg }
func (e *grepScopeLostError) Unwrap() error { return e.err }

// grepRootSet is what one call searches: the roots handed to the engine,
// in walk order, and the realpath of each (the path.search_roots row's
// `roots`, MV-3). ancestorUnreadable is resolveScopedRoot's LoadAncestorIgnore
// count (finding L11), folded into Stats by Execute.
type grepRootSet struct {
	roots              []filegrep.Root
	real               []string
	ancestorUnreadable int
}

func (s *grepRootSet) add(root filegrep.Root, realPath string) {
	s.roots = append(s.roots, root)
	s.real = append(s.real, realPath)
}

// grepRootRealpath is the realpath of a root's host folder for the audit
// row; a folder that cannot be resolved right now (a dead mount, a root
// lost after admission) is listed by its cleaned spelling — MV-3 lists a
// lost root too.
func grepRootRealpath(p string) string {
	if resolved, err := resolveRealpathUnderWorkDir(p, ""); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// grepShorthandCandidate reports whether scope may be read as the mount-name
// shorthand (FR-002): a relative path with no volume, not rooted, and with
// no ".." segment. It returns the slash-normalised, path.Clean'd spelling
// the shorthand test and the mount branch use. On Windows `\` counts as a
// separator (Ambiguity Warning 4); on Linux and macOS it is a file-name
// character (FR-009), so it is left alone.
func grepShorthandCandidate(scope string) (string, bool) {
	if filepath.IsAbs(scope) || filepath.VolumeName(scope) != "" {
		return "", false
	}
	slashed := scope
	if runtime.GOOS == "windows" {
		slashed = strings.ReplaceAll(scope, `\`, "/")
	}
	if strings.HasPrefix(slashed, "/") {
		// Rooted without a volume (`\x` on Windows): not workspace-relative.
		return "", false
	}
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			return "", false
		}
	}
	return normalizeGrepScope(slashed), true
}

// resolveGrepRoots resolves the fs.FS roots one call searches. With no scope
// that is the calling agent's own effective working directory (policy.WorkDir
// — its fixed home, or the per-turn Workspace re-root when it is a CoreTeam
// member) plus every mount on that SAME workspace (D5). A scope naming a
// mount by its name searches that mount; any other scope is admitted or
// refused by ResolvePath exactly as list_directory would judge it — FR-001's
// dispatch, in this order: (1) refused if it carries a NUL byte; (2) the
// mount-name shorthand when it is a relative, ".."-free path whose first
// segment names a mount (lexical, no I/O); (3) otherwise decided by
// ResolvePath(FSOpList) — relative, absolute and ".." paths alike — exactly
// as list_directory decides it. There is no lexical first attempt with a
// fall-through (spec FR-001, round-1 MIN-002). It never consults an argument
// named workspace_id — there is no such argument.
//
// # Resolving which workspace's mounts apply
//
// Mirrors ResolveTurnFSPolicy's own mount resolution (turn-carried
// ToolWorkspaceID, else workspace.FindForAgentPreferring) rather than
// calling through it, for the same reason ListMountsTool
// (pkg/tools/list_mounts.go) duplicates it locally: policy.AllowedRoots
// already carries the resolved host paths, but discards their NAMES, and a
// filegrep.Root needs a Name to prefix every reported hit with (matching how
// the Library addresses a mounted entry: "<mount>/<rel>"). Re-deriving the
// same workspace id the same way — rather than reusing policy.AllowedRoots
// directly — keeps this in lock-step with every other mount-consuming tool;
// see ResolveTurnFSPolicy's own doc comment for why the two resolutions must
// never disagree.
//
// The entries themselves come from workspace.LoadMounts, which is the
// VALIDATING reader: loadMountStore runs Mount.Validate over every entry and
// drops the failures with a WARN before returning any of them, so a
// hand-edited or partially-migrated record cannot contribute a search root
// here any more than it can contribute a write grant through
// workspace.AllowedMountRoots (itself LoadMounts plus a repeat of that same
// check). AllowedMountRoots is not used here only because it discards the
// mount NAMES a filegrep.Root needs. That the two readers return the same set
// is asserted in grep_mountvalidation_test.go, not assumed.
//
// # scope
//
// "" searches the whole workspace: the root plus every mount, each its own
// filegrep.Root. A non-empty scope opens ONE further-confined os.Root via
// (*os.Root).OpenRoot — pruning the walk itself, not just filtering after
// the fact — and returns a single Root whose Name is the resolved prefix:
// workspace-relative inside the workspace, the mount name for the
// shorthand, and the absolute forward-slash location anywhere else (#920
// FR-004). A relative, ".."-free scope's first path segment is matched
// against the workspace's mount names FIRST (an exact segment match, never a
// prefix of a longer name); anything else goes through ResolvePath.
//
// # A broken mount is never silently dropped
//
// A mount whose host folder cannot be opened right now (unplugged drive,
// renamed folder) still contributes a Root — backed by unreachableRootFS,
// which fails filegrep.Search's own fs.Stat(root.FS, ".") check and turns
// into Result.Truncated + filegrep.ReasonRootLost (FR-021) through that
// single existing code path, rather than a quiet reduction in coverage or a
// second, tool-local notion of "root lost".
//
// # The secret set is subtracted from every root
//
// An os.Root confines the walk to one host directory; it says nothing about
// WHICH files inside it an agent may see. Every root handed to the engine is
// therefore wrapped in carveOutFS (and, on top of it, grepGateFS — the
// skills-registry gate, the metadata guard and D13, this file), which
// applies fspolicy.IsCarveOut — the
// same predicate ResolvePath consults before it looks at a mount at all — to
// every entry the engine can list, name-match, or open. Without it a mount on
// an ANCESTOR of $OMNIPUS_HOME (warn-and-allow per workspace.CheckMountTarget,
// which hard-refuses only a target inside $OMNIPUS_HOME) makes credentials.json,
// master.key, cli.token, the config backups and system/audit.jsonl greppable —
// every one of them refused to read_file on the same turn, and every one of
// them covered by the warning CheckMountTarget prints when the operator creates
// that mount ("the installation's own secrets remain protected independently of
// this mount").
//
// grepRootSet.ancestorUnreadable is the total LoadAncestorIgnore "unreadable"
// count summed across every root this resolves (finding L11) — zero whenever
// scope=="", since that path never preloads an ancestor chain (the walk root
// already is the search scope). See resolveScopedRoot's doc comment for why
// this cannot be folded into filegrep.Stats until after filegrep.Search
// runs; Execute performs that fold.
func (t *GrepTool) resolveGrepRoots(ctx context.Context, policy fspolicy.FSPolicy, scope string) (grepRootSet, func(), error) {
	var opened []*os.Root
	closeAll := func() { closeGrepRoots(opened) }
	mounts := grepMounts(ctx)

	if scope == "" {
		set, err := defaultGrepRoots(policy, mounts, &opened)
		return set, closeAll, err
	}
	if strings.IndexByte(scope, 0) != -1 {
		return grepRootSet{}, closeAll, &grepPathRefusalError{
			err: fmt.Errorf("%w: `path` contains an embedded NUL byte", ErrPathInvalid),
		}
	}
	if lex, ok := grepShorthandCandidate(scope); ok {
		if lex == "" {
			// "." and "./" mean the whole workspace, as "" does.
			set, err := defaultGrepRoots(policy, mounts, &opened)
			return set, closeAll, err
		}
		if idx, rest, matched := splitGrepScopeMount(lex, mounts); matched {
			set, err := t.mountScopeRoot(mounts[idx], rest, policy, &opened)
			return set, closeAll, err
		}
	}
	set, err := t.resolvedScopeRoot(ctx, policy, scope, &opened)
	return set, closeAll, err
}

// resolvedScopeRoot is FR-001 step 3-4: the single read decision, then the
// root type the resolved location calls for.
func (t *GrepTool) resolvedScopeRoot(ctx context.Context, policy fspolicy.FSPolicy, scope string, opened *[]*os.Root) (grepRootSet, error) {
	handle, err := ResolvePath(ctx, policy, t.Name(), "", FSOpList, scope)
	if err != nil {
		return grepRootSet{}, &grepPathRefusalError{err: err}
	}
	// ADR-081 D4 design step 2: grep needs the resolved location, not the
	// handle's own I/O, so the handle is closed at once (FR-032).
	realAbs, realErr := handle.RealPath()
	if closeErr := handle.Close(); closeErr != nil && realErr == nil {
		realErr = closeErr
	}
	if realErr != nil {
		return grepRootSet{}, fmt.Errorf("path %s could not be resolved: %w", scope, realErr)
	}
	if isKernelPseudoPath(realAbs) {
		return grepRootSet{}, fmt.Errorf(
			"path %s is inside a Linux kernel pseudo-folder (/proc, /sys or /dev), which grep never searches; "+
				"use read_file to read a named file there", scope)
	}

	realWorkDir, wdErr := resolveRealpathUnderWorkDir(policy.WorkDir, "")
	var root filegrep.Root
	var n int
	if wdErr == nil && isWithinWorkspace(realAbs, realWorkDir) {
		root, n, err = t.workspaceScopeRoot(policy, realWorkDir, realAbs, opened)
	} else {
		// Security A1: absoluteGrepRoot consults the workspace's mounts so
		// it can anchor os.OpenRoot at the mount's HostPath when realAbs is
		// under a mount — the same anchor read_file uses via
		// newMountRootHandle.
		root, n, err = t.absoluteGrepRoot(scope, realAbs, grepMounts(ctx), policy, opened)
	}
	if err != nil {
		return grepRootSet{}, err
	}
	var set grepRootSet
	set.add(root, realAbs)
	set.ancestorUnreadable = n
	return set, nil
}

// workspaceScopeRoot opens a location that resolved inside the workspace as
// the workspace-scoped root it always was (FR-001 step 4): workspace-relative
// match paths, ancestor ignore layers preloaded up to the workspace root.
func (t *GrepTool) workspaceScopeRoot(policy fspolicy.FSPolicy, realWorkDir, realAbs string, opened *[]*os.Root) (filegrep.Root, int, error) {
	expected, err := grepPreOpenIdentity(realWorkDir)
	if err != nil {
		return filegrep.Root{}, 0, fmt.Errorf("cannot verify your workspace root before opening it: %w", err)
	}
	runGrepPreOpenRootHook(policy.WorkDir)
	wr, err := os.OpenRoot(policy.WorkDir)
	if err != nil {
		return filegrep.Root{}, 0, fmt.Errorf("cannot open your workspace root: %w", err)
	}
	*opened = append(*opened, wr)
	if !grepOpenedRootMatches(wr, expected) {
		return filegrep.Root{}, 0, fmt.Errorf("your workspace root changed before it could be opened")
	}
	rel, err := filepath.Rel(realWorkDir, realAbs)
	if err != nil {
		return filegrep.Root{}, 0, fmt.Errorf("path could not be made relative to your workspace: %w", err)
	}
	subPath := filepath.ToSlash(rel)
	if subPath == "." {
		// The workspace folder itself, named by some other spelling: the
		// workspace root alone (a mount is not inside the workspace folder).
		return filegrep.Root{Name: "", FS: guardGrepRoot(policy.WorkDir, wr.FS(), wr, policy)}, 0, nil
	}
	return t.resolveScopedRoot(wr, policy.WorkDir, subPath, "", "your workspace", policy, opened)
}

// absoluteGrepRoot is ADR-081 D4's new root type for a location outside the
// workspace (design steps 3-4): a fresh os.OpenRoot at the realpath's parent
// as container, then resolveScopedRoot for the directory-vs-file dispatch,
// with absolute forward-slash match paths (FR-004).
//
// FR-010's boundary: the os.Stat below is the one existence check — a
// location missing there is an error naming the path. Once it existed, a
// container or root that cannot be opened is carried as an unreachable root
// (truncated root_lost naming it), never a hard error and never a silent
// zero.
//
// Residual (SL-4, stated in ADR-081): realAbs is advisory; an ancestor
// swapped for a symlink between ResolvePath and os.OpenRoot is the same
// class existing mount roots carry. carveOutFS re-judges every entry by
// string against the secret set.
//
// FR-010's seam, fired between the existence check above and the os.OpenRoot
// below, drives the "swap between resolve and open" race for the absolute
// root type (resolveScopedRoot already fires the same seam later, after its
// own existence check — the two together let a single test hook cover both
// windows with a `sync.Once`). Production: the pointer is nil; the cost is
// one atomic load and a nil check per scoped absolute root.
//
// Security fix A1 (Opus security-lead review): when realAbs is inside one
// of the workspace's mounts, anchor os.OpenRoot at the mount's HostPath
// instead of at parent. os.OpenRoot FOLLOWS symlinks in the directory
// name (per os.OpenRoot's own doc), so opening at parent would let a
// folder swapped for an outside symlink between ResolvePath's admission
// and this open escape the mount's intended boundary — the same TOCTOU
// class resolvepath.go's package doc claims to close for write/serve,
// and the same class newMountRootHandle already closes for read_file
// (TestResolvePath_ReadConfinedMountAncestorSwap, S-4.6). Anchoring at
// the mount root + delegating to resolveScopedRoot applies os.Root's
// "symbolic links may not reference a location outside the root"
// protection to the resolved path; a swapped-in outside symlink is
// caught as a "path escapes from parent" open error and surfaced as a
// lost root (FR-021, truncated root_lost), never as silent coverage.
func (t *GrepTool) absoluteGrepRoot(rawPath, realAbs string, mounts []workspace.Mount, policy fspolicy.FSPolicy, opened *[]*os.Root) (filegrep.Root, int, error) {
	if _, err := os.Stat(realAbs); err != nil {
		return filegrep.Root{}, 0, grepAbsoluteStatError(rawPath, realAbs, err)
	}
	// (No pre-open seam fire here. The existing grepScopeStatOpenHook
	// inside resolveScopedRoot fires for any TOCTOU that affects the
	// walk's own stat — the absoluteGrepRoot-level stat is best-effort
	// and an additional fire there caused TestReadBoundary_AbsoluteRootLost
	// to regress: the test removes the anchor between Stat and Open, so
	// firing the lost hook in absoluteGrepRoot surfaces "anc does not
	// exist" before resolveScopedRoot's own Stat-fires-hook window can
	// convert it to a lost root.)
	name := grepAbsoluteName(realAbs)
	lost := func(err error) filegrep.Root {
		return filegrep.Root{Name: name, FS: unreachableRootFS{err: err}}
	}

	parent := filepath.Dir(realAbs)
	if parent == realAbs {
		// A volume root (`/`, `C:\`) has no parent: open it directly, the
		// shape of resolveGrepRoots' mount branch with no sub-path (ADR-081 D4).
		expected, err := grepPreOpenIdentity(realAbs)
		if err != nil {
			return lost(err), 0, nil
		}
		runGrepPreOpenRootHook(realAbs)
		root, err := os.OpenRoot(realAbs)
		if err != nil {
			return lost(err), 0, nil
		}
		*opened = append(*opened, root)
		if !grepOpenedRootMatches(root, expected) {
			return lost(fmt.Errorf("absolute root %q changed before opening", realAbs)), 0, nil
		}
		return filegrep.Root{Name: name, FS: guardGrepRoot(realAbs, root.FS(), root, policy)}, 0, nil
	}
	// Security A1: a path under a mount anchors at the mount root, not at
	// the immediate parent. The containment check reuses isWithinWorkspace
	// (filesystem.go) — the same predicate ResolvePath's own workspace
	// branch uses for WorkDir — so the two cases cannot drift.
	for _, m := range mounts {
		if !isWithinWorkspace(realAbs, m.HostPath) {
			continue
		}
		expected, identityErr := grepPreOpenIdentity(m.HostPath)
		if identityErr != nil {
			return lost(identityErr), 0, nil
		}
		runGrepPreOpenRootHook(m.HostPath)
		mr, mErr := os.OpenRoot(m.HostPath)
		if mErr != nil {
			return lost(mErr), 0, nil
		}
		*opened = append(*opened, mr)
		if !grepOpenedRootMatches(mr, expected) {
			return lost(fmt.Errorf("absolute root %q changed before opening", m.HostPath)), 0, nil
		}
		// A2 seam (post-open): a test installs a hook here to swap the
		// mount root AFTER the open has bound its fd, driving the
		// post-open SameFile race the A2 fix catches.
		runGrepAbsolutePostOpenHook(m.HostPath)
		// Security A2: the bound root's "." must be the same directory
		// the opened path (mount root here) still resolves to. A swap
		// landing between the open above and this check leaves the fd
		// bound at the pre-swap inode while os.Stat(mountRoot) follows
		// the swap — the two stats differ via os.SameFile, and the
		// mismatch surfaces as a lost root (FR-021, truncated
		// root_lost), the same carrier the pre-existing stat-then-open
		// race uses.
		if same, sErr := sameFileCheck(mr, m.HostPath); sErr == nil && !same {
			return lost(fmt.Errorf("absolute root %q was lost after admission (post-open)", m.HostPath)), 0, nil
		}
		rel, relErr := filepath.Rel(m.HostPath, realAbs)
		if relErr != nil {
			return filegrep.Root{}, 0, fmt.Errorf("path %s could not be made relative to its mount %q: %w", rawPath, m.Name, relErr)
		}
		// namePrefix is the absolute host root (e.g., "<MNT>"), not the
		// mount name ("docs-mount") — the spec contract for row 4
		// (DS-1 absolute in a mount) is "<MNT>/src/b.md" (absolute) as
		// the match path, while the mount-name SHORTHAND path (row 3)
		// uses "docs-mount/src/b.md". The two are distinguished by how
		// the user invoked the scope, not by which mount holds the
		// location; the engine joins namePrefix + "/" + match-rel.
		r, n, rErr := t.resolveScopedRoot(mr, m.HostPath, filepath.ToSlash(rel), grepAbsoluteName(m.HostPath), fmt.Sprintf("mount %q", m.Name), policy, opened)
		if rErr != nil {
			var lostErr *grepScopeLostError
			if errors.As(rErr, &lostErr) {
				return lost(lostErr.err), 0, nil
			}
			return filegrep.Root{}, 0, rErr
		}
		return r, n, nil
	}
	expected, err := grepPreOpenIdentity(parent)
	if err != nil {
		return lost(err), 0, nil
	}
	runGrepPreOpenRootHook(parent)
	container, err := os.OpenRoot(parent)
	if err != nil {
		return lost(err), 0, nil
	}
	*opened = append(*opened, container)
	if !grepOpenedRootMatches(container, expected) {
		return lost(fmt.Errorf("absolute root %q changed before opening", parent)), 0, nil
	}
	// A2 seam (post-open): same shape, fires after the unanchored
	// os.OpenRoot(parent) too — a swap of parent (the opened path)
	// drives the same race.
	runGrepAbsolutePostOpenHook(parent)
	// Security A2 (unanchored): same SameFile check, anchor is parent
	// (the opened path) — not realAbs, which is a sub-path the bound
	// fd legitimately doesn't name (parent and realAbs are different
	// directories, not the same).
	if same, sErr := sameFileCheck(container, parent); sErr == nil && !same {
		return lost(fmt.Errorf("absolute root %q was lost after admission (post-open)", parent)), 0, nil
	}
	label := fmt.Sprintf("folder %q", grepAbsoluteName(parent))
	root, n, err := t.resolveScopedRoot(container, parent, filepath.Base(realAbs), grepAbsoluteName(parent), label, policy, opened)
	if err != nil {
		var lostErr *grepScopeLostError
		if errors.As(err, &lostErr) {
			return lost(lostErr.err), 0, nil
		}
		return filegrep.Root{}, 0, err
	}
	return root, n, nil
}

// grepAbsoluteName is a host path's forward-slash form used as a Root name
// (FR-004). A trailing separator is dropped except on the Unix root, whose
// name is "/" (the engine's own "name/rel" join then yields "//rel", which
// Execute collapses — see fixGrepRootSlash).
func grepAbsoluteName(hostPath string) string {
	name := filepath.ToSlash(hostPath)
	if len(name) > 1 {
		name = strings.TrimSuffix(name, "/")
	}
	return name
}

// joinGrepName joins a Root name prefix and a slash sub-path without
// doubling a separator the prefix already ends with.
func joinGrepName(prefix, sub string) string {
	if prefix == "" {
		return sub
	}
	if strings.HasSuffix(prefix, "/") {
		return prefix + sub
	}
	return prefix + "/" + sub
}

// fixGrepRootSlash collapses the "//" the engine produces for hits under the
// Unix root "/" (Root.Name "/" joined with "/" + rel). On Windows a leading
// "//" is a UNC path and is left alone.
func fixGrepRootSlash(res *filegrep.Result) {
	if runtime.GOOS == "windows" {
		return
	}
	for i := range res.Hits {
		if strings.HasPrefix(res.Hits[i].Path, "//") {
			res.Hits[i].Path = res.Hits[i].Path[1:]
		}
	}
	if strings.HasPrefix(res.TruncatedRoot, "//") {
		res.TruncatedRoot = res.TruncatedRoot[1:]
	}
}

// grepAbsoluteStatError is FR-010's "does not exist or cannot be opened"
// error for an absolute root, naming the path as the agent wrote it (and the
// resolved location when that differs).
func grepAbsoluteStatError(rawPath, realAbs string, err error) error {
	where := rawPath
	if filepath.Clean(rawPath) != realAbs {
		where = fmt.Sprintf("%s (resolved to %s)", rawPath, realAbs)
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("path %s does not exist", where)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("path %s exists but could not be read (permission denied): %w", where, err)
	default:
		return fmt.Errorf("path %s could not be opened: %w", where, err)
	}
}

// grepMounts resolves the calling turn's workspace mounts — see
// resolveGrepRoots' doc comment ("Resolving which workspace's mounts apply").
func grepMounts(ctx context.Context) []workspace.Mount {
	home := config.OmnipusHomeDir()
	mountWorkspaceID := ToolWorkspaceID(ctx)
	if mountWorkspaceID == "" {
		if wsID, found := workspace.FindForAgentPreferring(home, ToolAgentID(ctx), ""); found {
			mountWorkspaceID = wsID
		}
	}
	if mountWorkspaceID == "" {
		return nil
	}
	if loaded, ok := workspace.LoadMounts(home, mountWorkspaceID); ok {
		return loaded
	}
	return nil
}

// isKernelPseudoPath reports whether abs is, or lies under, one of the Linux
// kernel pseudo-folders /proc, /sys, /dev (founder decision D13). They hold
// no user content, and some of their reads never return — a stuck read
// would pin one of the two shared walk slots past the deadline, which cannot
// interrupt a blocked read. Linux only: the decision names the Linux kernel
// folders, and on macOS /dev holds only device files, which grepGateFS never
// opens anyway.
func isKernelPseudoPath(abs string) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	clean := filepath.Clean(abs)
	for _, p := range [...]string{"/proc", "/sys", "/dev"} {
		if clean == p || strings.HasPrefix(clean, p+"/") {
			return true
		}
	}
	return false
}

// guardGrepRoot wraps one grep root in every gate: carveOutFS (the secret
// set, gate 1 of ADR-081 D4) and grepGateFS (gates 2 and 3 plus D13). Used
// for every grep root type — workspace, mount, scoped and absolute — so no
// root is a second, more permissive code path (ADR-081 D4 design step 4).
// The Library search bar keeps GuardCarveOuts alone (FR-028).
func guardGrepRoot(hostAbs string, fsys fs.FS, bound *os.Root, policy fspolicy.FSPolicy) fs.FS {
	guarded := guardCarveOuts(hostAbs, fsys, policy)
	carve, ok := guarded.(carveOutFS)
	if !ok {
		return guarded // unreachableRootFS: nothing to walk, nothing to gate
	}
	return grepGateFS{fsys: carve, raw: carve.fsys, bound: bound, root: carve.root, policy: policy}
}

// grepGateFS withholds, by name and content, what read_file would refuse and
// grep must never walk (FR-006, FR-007, D13):
//
//   - registry skill instruction files (SKILL.md, AGENT.md, AGENTS.md under
//     $OMNIPUS_HOME/skills) — ADR-072 D10.3 with the single-spelling
//     primitives (ADR-081 MIN-003): leaf test first, containment test only
//     for those three names; project-shelf files stay searchable;
//   - agent metadata files (metadataFileMatch: SOUL.md, HEARTBEAT.md,
//     AGENT.md, legacy MEMORY.md under agents/<id>/, case-insensitive);
//   - the Linux kernel pseudo-folders /proc, /sys, /dev (D13);
//   - and it never opens anything that is not a directory or a regular file
//     (D13: device files, pipes, sockets) — the engine content-scans only
//     regular directory entries, but it opens each directory's .gitignore
//     and .ignore by name, and a pipe there would block the walk.
//
// Candidates are the realpath anchor (carveOutFS.root) joined with the
// walk-relative name; the walk never follows a symlink, so the as-written
// and resolved spellings coincide for everything whose content is read.
type grepGateFS struct {
	fsys fs.FS // carveOutFS, used for Stat and ReadDir
	// raw identifies the optional singleEntryFS restriction beneath
	// carveOutFS; a direct Open must preserve its one-file boundary.
	raw fs.FS
	// bound is the already-admitted os.Root. Opening a name through this
	// descriptor preserves confinement when the host root moves later.
	bound  *os.Root
	root   string // absolute, symlink-resolved host path fsys is anchored at
	policy fspolicy.FSPolicy
}

func (g grepGateFS) abs(name string) string {
	if name == "." || name == "" {
		return g.root
	}
	return filepath.Join(g.root, filepath.FromSlash(name))
}

// withheld is the per-entry gate decision.
func (g grepGateFS) withheld(name string) bool {
	abs := g.abs(name)
	if isKernelPseudoPath(abs) {
		return true
	}
	if _, _, ok := metadataFileMatch(abs); ok {
		return true
	}
	if isSkillInstructionFileLeaf(abs) {
		if shelf, ok := classifySkillsGateCandidate(abs, g.policy); ok && shelf == skillShelfRegistry {
			return true
		}
	}
	return false
}

func (g grepGateFS) refusal(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrPermission}
}

func (g grepGateFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name != "." && g.withheld(name) {
		return nil, g.refusal("open", name)
	}
	// Bypassing carveOutFS.Open must not bypass its secret-set decision.
	if fspolicy.IsCarveOut(g.abs(name), g.policy) {
		return nil, g.refusal("open", name)
	}
	// singleEntryFS.Open exposes only its chosen file and the two ignore
	// files. Preserve that restriction before using the bound root directly.
	if single, ok := g.raw.(singleEntryFS); ok && name != "." && name != single.name && !isGrepIgnoreFileName(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	bound := g.bound
	if bound == nil {
		// A standalone gate can be constructed around an fs.FS without an
		// existing os.Root. Bind and verify its host root before opening the
		// entry; production roots arrive pre-bound from guardGrepRoot.
		canonical, err := filepath.EvalSymlinks(g.root)
		if err != nil {
			return nil, err
		}
		expected, err := grepPreOpenIdentity(canonical)
		if err != nil {
			return nil, err
		}
		bound, err = os.OpenRoot(g.root)
		if err != nil {
			return nil, err
		}
		defer bound.Close()
		if !grepOpenedRootMatches(bound, expected) {
			return nil, g.refusal("open", name)
		}
	}
	f, err := openRegularGrepEntry(bound, name)
	if err != nil {
		return nil, err
	}
	// A swap now changes the name on disk, not the verified descriptor.
	runGrepGatePreReopenHook(name)
	return f, nil
}

func (g grepGateFS) Stat(name string) (fs.FileInfo, error) {
	if name != "." && g.withheld(name) {
		return nil, g.refusal("stat", name)
	}
	return fs.Stat(g.fsys, name)
}

func (g grepGateFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." && name != "" && g.withheld(name) {
		return nil, g.refusal("readdir", name)
	}
	entries, err := fs.ReadDir(g.fsys, name)
	if err != nil {
		return nil, err
	}
	kept := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		child := e.Name()
		if name != "." && name != "" {
			child = name + "/" + e.Name()
		}
		if g.withheld(child) {
			continue
		}
		kept = append(kept, e)
	}
	return kept, nil
}

// refuseNonRegular refuses to open anything that is neither a directory nor
// a regular file (D13): opening a FIFO for reading blocks until a writer
// appears, and a device read may never return. fs.ErrNotExist is left to the
// Open that follows, which reports the absence truthfully — unchanged.
//
// Any OTHER Stat error (F1/S3, 8-reviewer gate: silent-failure-hunter and
// security-lead) means this function cannot establish what kind of entry
// `name` is at all — a permission error on an ancestor, a transient fs.FS-
// specific I/O failure, anything not-ENOENT. The pre-fix code treated "Stat
// failed" as "nothing to refuse" and let the entry through to Open — a
// fail-OPEN gate. Fail closed instead: refuse it, exactly as an unreadable
// kind would be refused, with the original Stat error folded into the
// message so the refusal is diagnosable, and log it once at warn level
// (component "tool", the walk-relative name only — never file content) via
// the repo's structured logger.WarnCF, the same log-safe shape
// guardCarveOuts already uses for its own refusal (grep.go).
//
// Retained because TestRefuseNonRegular_NotExistLeftToOpen calls it directly.
// Live grep reads instead use openRegularGrepEntry: one root-confined,
// non-blocking open whose own descriptor is checked and returned.
func refuseNonRegular(fsys fs.FS, name string) error {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		logger.WarnCF("tool", "grep: could not determine an entry's kind before opening it — refusing it (fail closed)", map[string]any{
			"path": name, "error": err.Error(),
		})
		return &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("entry kind could not be determined, refusing to open it: %w: %w", fs.ErrPermission, err)}
	}
	// A3 seam: in the OLD code, the kind check uses the snapshot info
	// above, so a FIFO swap here passes the check and the next Open
	// blocks. The A3 fix moves the check to the opened file, where
	// the swap is visible without any blocking Open.
	runGrepGateFIFOSwapHook(name)
	if info.IsDir() || info.Mode().IsRegular() {
		return nil
	}
	return &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("%s is not searched: %w", describeFileKind(info.Mode()), fs.ErrPermission)}
}

// openRegularGrepEntry performs the ONE content open, through the admitted
// os.Root with O_NONBLOCK on Unix. It checks the opened descriptor's kind
// and returns that SAME descriptor, never reopening an unverified name.
// Directories remain openable for fs.FS traversal; FIFOs and devices do not.
func openRegularGrepEntry(bound *os.Root, name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	runGrepGateFIFOSwapHook(name)
	f, err := bound.OpenFile(name, regularReadOpenFlags(), 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		logger.WarnCF("tool", "grep: could not open an entry to determine its kind — refusing it (fail closed)", map[string]any{
			"path": name, "error": err.Error(),
		})
		return nil, &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("entry kind could not be determined, refusing to open it: %w: %w", fs.ErrPermission, err)}
	}
	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("could not stat opened file: %w", statErr)}
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("%s is not searched: %w", describeFileKind(info.Mode()), fs.ErrPermission)}
	}
	return f, nil
}

// regularOnlyFS reads ancestor .gitignore/.ignore files through the same
// already-bound os.Root as the scoped search, without a host-path reopen.
type regularOnlyFS struct {
	bound *os.Root
}

func (r regularOnlyFS) Open(name string) (fs.File, error) {
	return openRegularGrepEntry(r.bound, name)
}

// sameFileCheck reports whether the *os.Root bound at root still names the
// same directory anchor currently resolves to. anchor is expected to be
// already realpath-resolved by the caller (ResolvePath returns it that
// way); on a swap landing between os.OpenRoot and this check, anchor
// now follows the symlink and resolves to a different inode. os.SameFile
// compares inode (and dev) pairs; a mismatch surfaces a stale fd to the
// caller as a lost root (FR-021). Returns (true, nil) when the two paths
// agree, (false, nil) on mismatch, and (false, err) when either stat
// itself failed — the latter is treated as "check did not run" by the
// callers, never as a green (a missing check must not silently pass).
func sameFileCheck(root *os.Root, anchor string) (bool, error) {
	rootInfo, err := root.Stat(".")
	if err != nil {
		return false, err
	}
	anchorInfo, err := os.Stat(anchor)
	if err != nil {
		return false, err
	}
	return os.SameFile(rootInfo, anchorInfo), nil
}
