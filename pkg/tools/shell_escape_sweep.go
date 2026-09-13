// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Post-command symlink sweep for the bash tool (UAT 2026-09-13 D-14).
//
// The workspace path guard (guardCommand) reads the command TEXT. A path the
// command assembles at runtime — `python3 -c "os.symlink('/' + 'etc', 'x')"`
// — never appears in that text, so the guard cannot see it, and the UAT
// proved the point by planting `etclink -> /etc` inside a vault through
// exactly that shape. Where the platform has a kernel sandbox the WRITE side
// of that link is still caught (the kernel confines by the real path), but
// the link itself remains in the workspace as a standing escape hatch for
// every later tool call, and on a host with no kernel sandbox nothing
// catches it at all.
//
// This sweep looks at what the command DID rather than what it said: after a
// foreground bash run it walks the turn's roots (the working directory and
// every mounted folder), finds symlinks created by that command whose target
// resolves outside every root, removes them, reports each removal in the
// tool result, and writes a deny-decision audit entry. It never follows a
// symlink while walking, never touches a link older than the command, and is
// bounded in entries and time so a large vault degrades to a partial sweep
// (reported as such) rather than a slow bash tool.
//
// "Created by that command" is judged by the inode change time (ctime) on
// Unix, which `touch -h` cannot backdate, and by the link's mtime on Windows
// where ctime is not exposed the same way.

package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// escapeSweepMaxEntries bounds the number of directory entries one sweep
	// will inspect across all roots before giving up and reporting a partial
	// result.
	escapeSweepMaxEntries = 20000
	// escapeSweepBudget bounds the wall-clock time one sweep may spend.
	escapeSweepBudget = 400 * time.Millisecond
	// escapeSweepSlack is subtracted from the command start time before the
	// "created by this command" comparison, so coarse filesystem timestamp
	// resolution (one second on some filesystems) cannot hide a link created
	// in the same second the command started.
	escapeSweepSlack = 2 * time.Second
)

// escapedSymlink records one removed link: the link's own path and the target
// it pointed at (as written, not resolved — that is what the agent asked for).
type escapedSymlink struct {
	Link   string
	Target string
}

// escapeSweepResult is what sweepEscapingSymlinks reports.
type escapeSweepResult struct {
	Removed []escapedSymlink
	// Partial is true when the entry cap or the time budget stopped the walk
	// before every root was fully inspected.
	Partial bool
	// Errors counts links that escaped but could not be removed; each is
	// still listed in Removed so the report names it.
	Errors int
}

// sweepEscapingSymlinks walks roots (without following symlinks) and removes
// every symlink created at or after since whose resolved target lies outside
// every entry of allowed. roots must be real (symlink-resolved) directories;
// allowed is the set of trees a target may legitimately point into and
// normally equals roots.
func sweepEscapingSymlinks(roots, allowed []string, since time.Time) escapeSweepResult {
	var res escapeSweepResult
	deadline := time.Now().Add(escapeSweepBudget)
	since = since.Add(-escapeSweepSlack)
	entries := 0
	stop := fmt.Errorf("sweep budget exhausted")

	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		root = filepath.Clean(root)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				// An unreadable subtree is skipped, not fatal: the sweep is a
				// check on this command's own creations, and a directory the
				// agent cannot read is one it did not just write into.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			entries++
			if entries > escapeSweepMaxEntries || time.Now().After(deadline) {
				return stop
			}
			if d.Type()&fs.ModeSymlink == 0 {
				return nil
			}
			info, lerr := os.Lstat(p)
			if lerr != nil {
				return nil //nolint:nilerr // a link that vanished mid-walk is not this sweep's concern
			}
			if symlinkChangeTime(info).Before(since) {
				return nil
			}
			target, rerr := os.Readlink(p)
			if rerr != nil {
				return nil //nolint:nilerr // unreadable link: cannot judge it, cannot have been written by us
			}
			if !symlinkTargetEscapes(p, target, allowed) {
				return nil
			}
			rec := escapedSymlink{Link: p, Target: target}
			if remErr := os.Remove(p); remErr != nil {
				res.Errors++
			}
			res.Removed = append(res.Removed, rec)
			return nil
		})
		if err != nil {
			res.Partial = true
			break
		}
	}
	sort.Slice(res.Removed, func(i, j int) bool { return res.Removed[i].Link < res.Removed[j].Link })
	return res
}

// symlinkTargetEscapes reports whether the link at linkPath, pointing at
// target (as written), resolves outside every allowed root. A relative target
// is taken relative to the link's own directory, exactly as the kernel would.
// The lexical absolute form is judged first; if the target exists its real
// path is judged too, so a link that points at a path inside a root which is
// itself a symlink out of the root is still caught.
func symlinkTargetEscapes(linkPath, target string, allowed []string) bool {
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(filepath.Dir(linkPath), target)
	}
	abs = filepath.Clean(abs)
	if _, ok := matchedAllowedRoot(abs, allowed); !ok {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		if _, ok := matchedAllowedRoot(resolved, allowed); !ok {
			return true
		}
	}
	return false
}

// escapeSweepNotice renders a sweep result for the tool result text. Empty
// when nothing was removed and the sweep was complete.
func escapeSweepNotice(res escapeSweepResult) string {
	if len(res.Removed) == 0 && !res.Partial {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[SAFETY GUARD")
	if len(res.Removed) > 0 {
		fmt.Fprintf(&b, ": removed %d symlink(s) created by this command that pointed outside the workspace and its mounts", len(res.Removed))
		for _, r := range res.Removed {
			fmt.Fprintf(&b, "\n  %s -> %s", r.Link, r.Target)
		}
		b.WriteString("\nSymlinks that escape the workspace are refused: they would turn every later write inside the workspace into a write outside it. Use request_mount to work in a folder outside the workspace.")
		if res.Errors > 0 {
			fmt.Fprintf(&b, "\n%d of them could not be removed; report this to the operator.", res.Errors)
		}
	}
	if res.Partial {
		if len(res.Removed) > 0 {
			b.WriteString("\n")
		} else {
			b.WriteString(": ")
		}
		b.WriteString("the post-command symlink sweep stopped early (workspace too large to inspect within its budget); the check was partial.")
	}
	b.WriteString("]")
	return b.String()
}
