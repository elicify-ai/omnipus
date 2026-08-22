// Omnipus — ADR-067 D7: workspace scoping for the knowledge tools.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// FR-052/FR-053 and US-9 (a P0) make workspace membership the trust boundary
// for retrieval: an agent in workspace A must not be able to read a knowledge
// base mounted only into workspace B, and asking for one must produce an EMPTY
// RESULT rather than a permission error (MV-12 — a 403 confirms the collection
// exists, which is itself the disclosure).
//
// Every knowledge tool therefore resolves what it may address through THIS
// file and nowhere else. The resolution path is ADR-067 D7's, literally:
//
//	agent → workspace → workspace.AllowedMountRoots(home, workspaceID) → KBs
//	within those roots
//
// Two properties are load-bearing and easy to lose in a later refactor:
//
//  1. The GRANT half comes from workspace.AllowedMountRoots and nothing else.
//     That function is the security-reviewed accessor: it reads the mount
//     STORE (which the granted principal cannot write) rather than the
//     workspace record, and every entry it returns has passed Mount.Validate.
//     mountLabels below reads the same store a second time, but ONLY to put a
//     human name on a root that is already granted — it can never add one.
//
//  2. An unknown or empty workspace id yields an EMPTY scope, never a wide
//     one. A tool call that arrives without a workspace on its context (an
//     agent not on any workspace team) can address no collection at all. The
//     failure direction matters: the alternative — treating "no workspace" as
//     "no restriction" — is exactly the unscoped search US-9 forbids, and it
//     would look like a working feature.
// ---------------------------------------------------------------------------

const (
	// ScopeMaxDepth bounds how far below an allowed root a knowledge base is
	// looked for. D7 says "KBs WITHIN those roots", so the root itself is not
	// the only candidate — an operator who mounts ~/Documents expects the vault
	// at ~/Documents/Notes to be found. Some bound is required regardless: a
	// broad mount can contain a hundred thousand directories, and enumeration
	// runs on the agent's tool-call path.
	//
	// The number is an implementation bound, NOT a spec value — the spec sets
	// no depth. It is stated as a named constant so it is visible and testable
	// rather than buried in a loop.
	ScopeMaxDepth = 4

	// ScopeMaxDirs bounds the total number of directories examined per scope
	// resolution, across all roots. Reaching it sets Scope.Truncated, which the
	// tools report — an incomplete enumeration presented as a complete one is
	// the dishonesty US-6 exists to prevent.
	ScopeMaxDirs = 4096

	// WorkTreeOrigin labels a collection found inside the workspace's own work
	// tree rather than under a mount. D11 creates knowledge bases there
	// ("workspace-first, movable"), so a scope that covered only mounts would
	// make every Omnipus-created knowledge base unreachable by its own tools.
	WorkTreeOrigin = "workspace"
)

// ScopedCollection is one knowledge base an agent in a given workspace may
// address.
type ScopedCollection struct {
	// Name is the operator-facing display name — the marker's name when there
	// is one (FR-024), the folder's own name otherwise. It is what an agent
	// passes as the "collection" argument.
	Name string
	// Root is the collection root's RESOLVED REAL PATH, which is the identity
	// the index is keyed on (D3/FR-031).
	Root string
	// Origin names where the grant came from: the mount's name, or
	// WorkTreeOrigin for a collection inside the workspace's own work tree. It
	// is a second address an agent may use, since a mount's name is what the
	// operator sees in the UI.
	Origin string
}

// Scope is the complete set of knowledge bases one workspace can address,
// plus the roots that granted them.
//
// The zero Scope grants NOTHING. That is deliberate: every failure path in
// ResolveScope returns it, so a bug that skips a step can only ever narrow
// access, never widen it.
type Scope struct {
	workspaceID string
	roots       []string
	collections []ScopedCollection
	truncated   bool
}

// WorkspaceID is the workspace this scope was resolved for, "" for the zero
// Scope.
func (s Scope) WorkspaceID() string { return s.workspaceID }

// Roots returns the allowed roots, real-path-resolved and deduplicated.
func (s Scope) Roots() []string { return append([]string(nil), s.roots...) }

// Collections returns every addressable knowledge base, sorted by name then
// root so two calls on an unchanged filesystem agree (FR-046).
func (s Scope) Collections() []ScopedCollection {
	return append([]ScopedCollection(nil), s.collections...)
}

// Truncated reports that enumeration hit ScopeMaxDirs, so Collections may be
// incomplete. Tools MUST surface this rather than presenting a partial list as
// the whole (US-6).
func (s Scope) Truncated() bool { return s.truncated }

// Names returns the addressable collection names, for an "available
// collections" hint. It lists only what this workspace may already see, so it
// discloses nothing across the boundary.
func (s Scope) Names() []string {
	out := make([]string, 0, len(s.collections))
	for _, c := range s.collections {
		out = append(out, c.Name)
	}
	return out
}

// Contains reports whether an already-real absolute path is inside one of the
// allowed roots. It is the containment predicate for any future write path;
// retrieval uses Select, which is stricter still (it matches only enumerated
// collections).
func (s Scope) Contains(candidate string) bool {
	if candidate == "" {
		return false
	}
	clean := filepath.Clean(candidate)
	for _, r := range s.roots {
		if clean == r || strings.HasPrefix(clean, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Select resolves the "collection" argument of a tool call to exactly one
// knowledge base.
//
// ref may be a display name, a mount name (Origin), or the collection's own
// absolute path. Matching is case-insensitive for names and exact for paths.
//
// An empty ref selects the only collection in scope when there is exactly one,
// and nothing when there are none or several — an agent addressing a
// multi-collection workspace must say which one.
//
// The ONLY candidates are this scope's own collections, which is what makes
// out-of-scope addressing impossible rather than merely refused: a reference
// naming another workspace's knowledge base matches nothing here and comes
// back (zero, false), indistinguishable from a name that does not exist
// anywhere (FR-053, MV-12, US-9 AS-2).
func (s Scope) Select(ref string) (ScopedCollection, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		if len(s.collections) == 1 {
			return s.collections[0], true
		}
		return ScopedCollection{}, false
	}

	lower := strings.ToLower(ref)
	// Names first, then origins, then paths. Collections are already sorted,
	// so "the first match" is deterministic when two collections share a name
	// (FR-046 — a query must not depend on directory-read order).
	for _, c := range s.collections {
		if strings.ToLower(c.Name) == lower {
			return c, true
		}
	}
	for _, c := range s.collections {
		if strings.ToLower(c.Origin) == lower {
			return c, true
		}
	}
	if filepath.IsAbs(ref) {
		want := filepath.Clean(ref)
		if resolved, err := filepath.EvalSymlinks(want); err == nil {
			want = filepath.Clean(resolved)
		}
		for _, c := range s.collections {
			if c.Root == want {
				return c, true
			}
		}
	}
	return ScopedCollection{}, false
}

// ResolveScope computes what an agent in workspaceID may address.
//
// It never returns an error. Every failure — an empty home, an unknown
// workspace, an unreadable mount store, a mount whose target has gone away —
// resolves to a NARROWER scope, because the alternative (surfacing a typed
// error from a security boundary) tempts callers into treating an error as a
// reason to fall back to something wider.
func ResolveScope(home, workspaceID string) Scope {
	home = strings.TrimSpace(home)
	workspaceID = strings.TrimSpace(workspaceID)
	if home == "" || workspaceID == "" {
		return Scope{}
	}

	s := Scope{workspaceID: workspaceID}

	// The workspace's own work tree (D11). SafeWorkDir refuses an unsafe id,
	// so an id that could traverse never reaches the filesystem here.
	labels := map[string]string{}
	var raw []string
	if workDir, err := workspace.SafeWorkDir(home, workspaceID); err == nil {
		raw = append(raw, workDir)
		labels[filepath.Clean(workDir)] = WorkTreeOrigin
	}

	// The mounts. AllowedMountRoots is the grant; mountLabels only names what
	// it granted (see this file's header).
	mountRoots := workspace.AllowedMountRoots(home, workspaceID)
	raw = append(raw, mountRoots...)
	for hostPath, name := range mountLabels(home, workspaceID) {
		labels[hostPath] = name
	}

	seen := make(map[string]struct{}, len(raw))
	budget := ScopeMaxDirs
	for _, r := range raw {
		resolvedRoot, err := realPath(r)
		if err != nil {
			// A broken mount contributes nothing and is not an error here:
			// D13 surfaces broken mounts on the operator's surface, not
			// through an agent's search call.
			continue
		}
		if _, dup := seen[resolvedRoot]; dup {
			continue
		}
		seen[resolvedRoot] = struct{}{}
		s.roots = append(s.roots, resolvedRoot)

		origin := labels[resolvedRoot]
		if origin == "" {
			origin = labels[filepath.Clean(r)]
		}
		if origin == "" {
			origin = filepath.Base(resolvedRoot)
		}
		found, truncated := discoverCollections(resolvedRoot, origin, &budget)
		s.collections = append(s.collections, found...)
		if truncated {
			s.truncated = true
		}
	}

	// Deduplicate collections by real path: one host folder mounted twice into
	// the same workspace is ONE collection (FR-026/FR-031), not two.
	byRoot := make(map[string]struct{}, len(s.collections))
	deduped := s.collections[:0]
	for _, c := range s.collections {
		if _, dup := byRoot[c.Root]; dup {
			continue
		}
		byRoot[c.Root] = struct{}{}
		deduped = append(deduped, c)
	}
	s.collections = deduped

	sort.Slice(s.collections, func(i, j int) bool {
		if s.collections[i].Name != s.collections[j].Name {
			return s.collections[i].Name < s.collections[j].Name
		}
		return s.collections[i].Root < s.collections[j].Root
	})
	return s
}

// mountLabels maps a mount's resolved host path to its operator-visible name.
//
// It is presentation only. Nothing it returns can become a root — the roots
// come from workspace.AllowedMountRoots, and a label with no matching root is
// simply never used.
func mountLabels(home, workspaceID string) map[string]string {
	mounts, ok := workspace.LoadMounts(home, workspaceID)
	if !ok || len(mounts) == 0 {
		return nil
	}
	out := make(map[string]string, len(mounts))
	for _, m := range mounts {
		if resolved, err := realPath(m.HostPath); err == nil {
			out[resolved] = m.Name
		}
		out[filepath.Clean(m.HostPath)] = m.Name
	}
	return out
}

// discoverCollections finds every knowledge base at or below root, bounded by
// ScopeMaxDepth and the shared directory budget.
//
// Symbolic links are never followed (FR-044). That is not only the walk rule
// this package applies everywhere — it is what keeps the workspace work tree
// from re-entering its own mounts through the work/<name> symlinks
// workspace.CreateMount materialises, and what stops a symlink planted inside
// a mounted folder from pulling another workspace's collection into scope.
//
// A directory that IS a knowledge base is recorded and NOT descended into: a
// collection is exactly one folder (FR-026), so a nested marker below one is
// not a second collection.
func discoverCollections(root, origin string, budget *int) ([]ScopedCollection, bool) {
	type queued struct {
		path  string
		depth int
	}
	var (
		out       []ScopedCollection
		truncated bool
		stack     = []queued{{path: root, depth: 0}}
	)

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if *budget <= 0 {
			truncated = true
			break
		}
		*budget--

		entries, err := os.ReadDir(cur.path)
		if err != nil {
			// Unreadable directory: nothing to add. The operator-facing
			// surfaces report unreadable paths (FR-112); a retrieval scope
			// resolution is not the place to fail an agent's whole call.
			continue
		}

		isKB := false
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// A marker must be a real directory, never a symlink to one —
			// same rule DetectUsing applies, restated here because this walk
			// reads its own entries.
			if e.Name() == MarkerDirName || e.Name() == ObsidianMarkerDirName {
				if info, statErr := os.Lstat(filepath.Join(cur.path, e.Name())); statErr == nil && info.IsDir() {
					isKB = true
				}
			}
		}
		if isKB {
			name := filepath.Base(cur.path)
			if c, openErr := OpenCollection(cur.path); openErr == nil {
				name = c.DisplayName()
			}
			out = append(out, ScopedCollection{Name: name, Root: filepath.Clean(cur.path), Origin: origin})
			continue // FR-026: one collection is one folder; do not descend.
		}

		if cur.depth >= ScopeMaxDepth {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, skip := scanSkippedDirNames[e.Name()]; skip {
				continue
			}
			child := filepath.Join(cur.path, e.Name())
			info, statErr := os.Lstat(child)
			if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
				continue // FR-044: skipped, never followed.
			}
			stack = append(stack, queued{path: child, depth: cur.depth + 1})
		}
	}
	return out, truncated
}
