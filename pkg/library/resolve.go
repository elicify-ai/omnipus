// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/pathsafe"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// HostRefKind classifies where an absolute host path lives relative to the
// workspace Libraries (chat-links spec, "Resolver Rules").
type HostRefKind uint8

const (
	// HostRefOutside is a path no Library can open: an agent's private home,
	// $OMNIPUS_HOME internals, /tmp, an undeclared symlink out of the work
	// tree, or anything that could not be resolved. It never gets a link.
	HostRefOutside HostRefKind = iota
	// HostRefWorkspace is a file in a workspace's own work tree.
	HostRefWorkspace
	// HostRefMount is a file inside a folder mounted into a workspace.
	HostRefMount
)

// HostRef is the resolver's answer: which workspace Library holds a host
// path, and the Library path that addresses it there (mount name first for a
// mounted folder). Rel is "" for the Library root itself, which callers treat
// as "no file to link to".
type HostRef struct {
	Kind        HostRefKind
	WorkspaceID string
	Rel         string
}

// InLibrary reports whether the path resolved into some workspace Library.
func (r HostRef) InLibrary() bool {
	return r.Kind == HostRefWorkspace || r.Kind == HostRefMount
}

// ResolveScope names which Libraries a host path may resolve into.
//
// Ordinary agents resolve against their turn workspace only (their file
// policy cannot reach another workspace anyway). An agent with cross-workspace
// setup authority (Omnipus, D-omnipus-links) also tries every other workspace,
// so a file it writes into W2 gets a W2 link — but its own agent home, which
// is its turn root, stays outside.
type ResolveScope struct {
	// Home is $OMNIPUS_HOME: the source of every workspace's work directory
	// and mount records.
	Home string
	// WorkspaceID is the turn's workspace. May be empty only with
	// AllWorkspaces.
	WorkspaceID string
	// TurnWorkDir is the turn's re-rooted work directory, when the loop set
	// one. It stands in for the turn workspace's work directory only when that
	// directory does not exist under Home. Ignored with AllWorkspaces, where
	// the turn root is the agent's own home.
	TurnWorkDir string
	// AllWorkspaces enables the cross-workspace fallback.
	AllWorkspaces bool
}

// LibraryDir returns the turn workspace's Library directory — the directory a
// workspace-relative Library path is joined onto — or "" when the scope names
// no turn workspace or it has no directory.
func (s ResolveScope) LibraryDir() string {
	for _, c := range resolveCandidates(s) {
		if c.workspaceID == s.WorkspaceID {
			return c.workDir
		}
	}
	return ""
}

// resolveCandidate is one workspace Library a path may belong to.
type resolveCandidate struct {
	workspaceID string
	workDir     string
}

// ResolveHostPath maps an absolute host path to the workspace Library that
// holds it (chat-links spec FR-009, "Resolver Rules").
//
// The real path is computed with EvalSymlinks on the deepest existing
// ancestor, so a file that does not exist yet (a write target) resolves
// through its existing parent. A symlink counts as inside only when it is a
// declared mount and its real target lies under that mount's host path; any
// other symlink whose real target leaves the work tree resolves outside,
// because the comparison is made on real paths. Declared mounts are checked
// after the work tree; a write through the in-tree mount entry
// (work/<mount>/c.md) and a write through the host path give the same answer.
//
// Relative paths, paths with a control character, and anything that fails to
// resolve are outside. Never an error: "outside" is the safe answer.
func ResolveHostPath(scope ResolveScope, hostPath string) HostRef {
	if hostPath == "" || !filepath.IsAbs(hostPath) {
		return HostRef{Kind: HostRefOutside}
	}
	if _, bad := pathsafe.FirstControlRune(hostPath); bad {
		return HostRef{Kind: HostRefOutside}
	}
	real, ok := evalDeepest(hostPath)
	if !ok {
		return HostRef{Kind: HostRefOutside}
	}
	for _, c := range resolveCandidates(scope) {
		if ref, found := resolveInCandidate(scope.Home, c, real); found {
			return ref
		}
	}
	return HostRef{Kind: HostRefOutside}
}

// resolveCandidates lists the Libraries to try, turn workspace first.
//
// The turn workspace's Library is its work directory under $OMNIPUS_HOME. The
// loop's TurnWorkDir stands in for it only when that directory does not exist
// under Home (an install or harness whose work tree lives elsewhere); when both
// exist and differ, the Library's own directory wins, so a turn rooted in an
// agent's private home can never make that home look like the workspace.
func resolveCandidates(scope ResolveScope) []resolveCandidate {
	var out []resolveCandidate
	if scope.WorkspaceID != "" {
		dir := ""
		if scope.Home != "" {
			if safe, err := workspace.SafeWorkDir(scope.Home, scope.WorkspaceID); err == nil {
				if info, serr := os.Stat(safe); serr == nil && info.IsDir() {
					dir = safe
				}
			}
		}
		if dir == "" && !scope.AllWorkspaces {
			dir = scope.TurnWorkDir
		}
		if dir != "" {
			out = append(out, resolveCandidate{workspaceID: scope.WorkspaceID, workDir: dir})
		}
	}
	if !scope.AllWorkspaces || scope.Home == "" {
		return out
	}
	ids, err := workspace.ListWorkspaceIDs(scope.Home)
	if err != nil {
		return out
	}
	for _, id := range ids {
		if id == scope.WorkspaceID {
			continue
		}
		dir, derr := workspace.SafeWorkDir(scope.Home, id)
		if derr != nil {
			continue
		}
		out = append(out, resolveCandidate{workspaceID: id, workDir: dir})
	}
	return out
}

// resolveInCandidate checks the work tree first, then the declared mounts.
//
// The work tree goes first because a broad mount (an operator may mount their
// whole home folder, with a warning) can contain $OMNIPUS_HOME itself; a mount
// match is also refused for any real path under $OMNIPUS_HOME, so an agent's
// private home or Omnipus's own files never become a mount link. A path that
// arrives through the in-tree mount entry (work/<mount>/c.md) evaluates to the
// mount's host path, which is not under the work tree, so it reaches the mount
// check and gets the same answer as the host path.
func resolveInCandidate(home string, c resolveCandidate, real string) (HostRef, bool) {
	if workReal, ok := evalDeepest(c.workDir); ok {
		if sub, under := relUnder(workReal, real); under {
			sub = foldRelToDisk(workReal, sub)
			if sub != "" && home != "" && shadowedByMount(home, c.workspaceID, sub) {
				// A real folder in work/ that carries a declared mount's name
				// cannot be addressed through the Library (its first segment
				// opens the mount), so a link would open the wrong file. The
				// declared mount wins; this path gets no link.
				return HostRef{Kind: HostRefOutside}, true
			}
			return HostRef{Kind: HostRefWorkspace, WorkspaceID: c.workspaceID, Rel: sub}, true
		}
	}
	if home == "" {
		return HostRef{}, false
	}
	if homeReal, ok := evalDeepest(home); ok {
		if _, inHome := relUnder(homeReal, real); inHome {
			return HostRef{}, false
		}
	}
	mounts, ok := workspace.LoadMounts(home, c.workspaceID)
	if !ok {
		return HostRef{}, false
	}
	for _, m := range mounts {
		if m.Name == "" || m.HostPath == "" {
			continue
		}
		mountReal, mok := evalDeepest(m.HostPath)
		if !mok {
			continue
		}
		sub, under := relUnder(mountReal, real)
		if !under {
			continue
		}
		rel := m.Name
		if sub != "" {
			rel = m.Name + "/" + foldRelToDisk(mountReal, sub)
		}
		return HostRef{Kind: HostRefMount, WorkspaceID: c.workspaceID, Rel: rel}, true
	}
	return HostRef{}, false
}

// shadowedByMount reports whether rel's first segment names a declared mount.
func shadowedByMount(home, workspaceID, rel string) bool {
	first, _, _ := strings.Cut(rel, "/")
	mounts, ok := workspace.LoadMounts(home, workspaceID)
	if !ok {
		return false
	}
	for _, m := range mounts {
		if m.Name == first {
			return true
		}
	}
	return false
}

// evalDeepest resolves p's real path through its deepest existing ancestor,
// re-attaching the missing tail. A component that exists but cannot be
// evaluated (a dangling symlink, an unreadable directory) makes the whole
// path unresolvable: following it lexically could classify a write through
// a dangling link as inside the work tree.
func evalDeepest(p string) (string, bool) {
	cur := filepath.Clean(p)
	var tail []string
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return real, true
		}
		if _, err := os.Lstat(cur); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// relUnder reports whether target lies at or under root, returning the
// slash-separated relative path ("" for root itself). On case-insensitive
// platforms (macOS, Windows) the comparison folds case, as the filesystem
// does.
func relUnder(root, target string) (string, bool) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		if caseInsensitiveFS() {
			return relUnderFold(root, target)
		}
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// relUnderFold is relUnder's case-folding fallback.
func relUnderFold(root, target string) (string, bool) {
	r := filepath.Clean(root)
	t := filepath.Clean(target)
	if strings.EqualFold(r, t) {
		return "", true
	}
	prefix := r
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if len(t) <= len(prefix) || !strings.EqualFold(t[:len(prefix)], prefix) {
		return "", false
	}
	return filepath.ToSlash(t[len(prefix):]), true
}

// caseInsensitiveFS reports whether this platform's default filesystems fold
// case. Linux filesystems normally do not.
func caseInsensitiveFS() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

// foldRelToDisk rewrites each existing segment of rel to its on-disk name
// when the given spelling only matches by case (the Library helper's folding,
// pathsafe.FindFold). Segments that do not exist yet are kept as given. On a
// case-sensitive platform the path is returned unchanged.
func foldRelToDisk(root, rel string) string {
	if rel == "" || !caseInsensitiveFS() {
		return rel
	}
	segs := strings.Split(rel, "/")
	dir := root
	for i, seg := range segs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			break
		}
		names := make([]string, len(entries))
		exact := false
		for j, e := range entries {
			names[j] = e.Name()
			if e.Name() == seg {
				exact = true
			}
		}
		if !exact {
			if match, found := pathsafe.FindFold(names, seg); found {
				segs[i] = match
			} else {
				break
			}
		}
		dir = filepath.Join(dir, segs[i])
	}
	return strings.Join(segs, "/")
}
