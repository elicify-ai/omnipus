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
// of that link is still caught (the kernel confines by the real path), and
// the app-level file tools resolve symlinks before their own confinement
// check (pkg/tools/filesystem.go, resolvepath.go), so a link that escapes
// the workspace cannot be used to write outside it through this process.
// What remains is that the link itself stays in the workspace, and a human
// or another program that trusts the workspace tree could follow it.
//
// This sweep looks at what the command DID rather than what it said: after a
// foreground bash run it walks the turn's roots (the working directory and
// every mounted folder), finds symlinks whose target resolves outside every
// root and whose inode change time falls inside the command's window, and
// REPORTS them — in the tool result, so the agent is told to undo its own
// creation, and in the audit log, so an operator can review them.
//
// It never removes anything. An earlier version of this file deleted every
// such link, and the Codex review of 2026-09-14 (finding #2) showed why that
// is wrong: a change time inside the command's window (which starts two
// seconds BEFORE the command, to survive coarse filesystem clocks) does not
// identify the link's creator. A person who creates a link in a mounted
// vault moments before, or while, an agent runs `git status` would have had
// their link deleted by a tool that never touched it. Nothing available to
// this process — not ctime, not mtime, not a before/after snapshot during a
// command that may run for minutes alongside a human — attributes a link to
// this command with certainty, so the honest action for an unattributable
// finding is to name it, not to destroy it. Write-time enforcement (kernel
// sandbox, app-level real-path confinement) is what actually stops writes
// through such a link; this sweep is the visibility layer on top of it.
//
// The change-time window is kept only as the FILTER for what is worth
// reporting after this particular command: a link that predates the command
// by more than the slack is not this command's doing and would otherwise be
// re-reported after every later command. ctime is used on Unix because
// `touch -h` cannot backdate it; on Windows the link's mtime stands in.
//
// The walk never follows a symlink, and is bounded in entries and time so a
// large vault degrades to a partial sweep (reported as such) rather than a
// slow bash tool.

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
	// escapeSweepSlack is subtracted from the command start time before the
	// "appeared during this command" comparison, so coarse filesystem
	// timestamp resolution (one second on some filesystems) cannot hide a
	// link created in the same second the command started. It is exactly why
	// a link inside the window cannot be attributed to the command: the
	// window deliberately reaches before the command began.
	escapeSweepSlack = 2 * time.Second
)

// escapeSweepMaxEntries bounds the number of directory entries one sweep
// will inspect across all roots before giving up and reporting a partial
// result, and escapeSweepBudget bounds the wall-clock time one sweep may
// spend. Package-level VARS (not consts) solely so tests can shrink them and
// force a deterministic partial sweep; production never writes them.
var (
	escapeSweepMaxEntries = 20000
	escapeSweepBudget     = 400 * time.Millisecond
)

// escapedSymlink records one reported link: the link's own path and the
// target it pointed at (as written, not resolved — that is what the agent
// asked for).
type escapedSymlink struct {
	Link   string
	Target string
}

// escapeSweepResult is what sweepEscapingSymlinks reports.
type escapeSweepResult struct {
	// Found lists every escaping symlink whose change time falls inside the
	// command's window. None of them has been touched.
	Found []escapedSymlink
	// Partial is true when the entry cap or the time budget stopped the walk
	// before every root was fully inspected.
	Partial bool
	// Swept lists the roots the walk fully inspected, and Skipped lists the
	// roots it never reached at all; PartialRoot is the one it was inside
	// when the budget stopped it (inspected only in part). Claude review
	// 2026-09-14 cut-list: on the production call path roots are
	// [workspace, mount1, mount2, …] in order, so a budget exhausted inside
	// the workspace leaves every MOUNT uninspected — the old banner said
	// only "workspace too large", which a reader could mistake for "the
	// mounts were checked". These fields exist so the notice and the WARN
	// log can say exactly what was covered and what was not.
	Swept       []string
	PartialRoot string
	Skipped     []string
}

// sweepEscapingSymlinks walks roots (without following symlinks) and reports
// every symlink changed at or after since (minus escapeSweepSlack) whose
// resolved target lies outside every entry of allowed. It removes nothing.
// roots must be real (symlink-resolved) directories; allowed is the set of
// trees a target may legitimately point into and normally equals roots.
func sweepEscapingSymlinks(roots, allowed []string, since time.Time) escapeSweepResult {
	var res escapeSweepResult
	deadline := time.Now().Add(escapeSweepBudget)
	since = since.Add(-escapeSweepSlack)
	entries := 0
	stop := fmt.Errorf("sweep budget exhausted")

	seen := make(map[string]bool, len(roots))
	queue := make([]string, 0, len(roots))
	for _, root := range roots {
		root = filepath.Clean(root)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		queue = append(queue, root)
	}
	for i, root := range queue {
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
			res.Found = append(res.Found, escapedSymlink{Link: p, Target: target})
			return nil
		})
		if err != nil {
			res.Partial = true
			res.PartialRoot = root
			res.Skipped = append(res.Skipped, queue[i+1:]...)
			break
		}
		res.Swept = append(res.Swept, root)
	}
	sort.Slice(res.Found, func(i, j int) bool { return res.Found[i].Link < res.Found[j].Link })
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
	// resolveRealpathUnderWorkDir (not a raw filepath.EvalSymlinks) is this
	// package's sanctioned resolver — the same one ResolvePath routes
	// through — per FR-034. It also resolves the nearest EXISTING ancestor
	// when the target leaf does not exist yet, which strengthens this check:
	// a link whose target path runs through a symlinked ancestor that leads
	// out of the roots is caught even before anything creates the leaf.
	if resolved, err := resolveRealpathUnderWorkDir(abs, ""); err == nil {
		if _, ok := matchedAllowedRoot(resolved, allowed); !ok {
			return true
		}
	}
	return false
}

// escapeSweepNotice renders a sweep result for the tool result text. Empty
// when nothing was found and the sweep was complete.
//
// A PARTIAL sweep is reported honestly (Claude review 2026-09-14 cut-list):
// the notice names the root the budget died inside, the roots fully
// inspected, and — most important for a reader deciding how much to trust a
// clean result — the roots never inspected at all, mounts included. The old
// text ("workspace too large to inspect within its budget") let a reader
// believe the mounts had been checked when the walk had never reached them.
func escapeSweepNotice(res escapeSweepResult) string {
	if len(res.Found) == 0 && !res.Partial {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[SAFETY GUARD")
	if len(res.Found) > 0 {
		fmt.Fprintf(&b, ": found %d symlink(s) that appeared during this command and point outside the workspace and its mounts", len(res.Found))
		for _, r := range res.Found {
			fmt.Fprintf(&b, "\n  %s -> %s", r.Link, r.Target)
		}
		b.WriteString("\nThey were NOT removed: a link cannot be attributed to this command with certainty (a person may have created it), so the sweep reports instead of deleting. " +
			"Writes through such a link are still confined by the real path they reach. " +
			"If this command created any of them, remove them yourself now — symlinks that escape the workspace are refused as a way of working outside it. " +
			"Use request_mount to work in a folder outside the workspace. This finding has been recorded in the audit log for the operator.")
	}
	if res.Partial {
		if len(res.Found) > 0 {
			b.WriteString("\n")
		} else {
			b.WriteString(": ")
		}
		fmt.Fprintf(&b, "the post-command symlink sweep stopped early (entry/time budget exhausted while walking %s); the check was PARTIAL.", res.PartialRoot)
		if len(res.Swept) > 0 {
			fmt.Fprintf(&b, " Fully inspected: %s.", strings.Join(res.Swept, ", "))
		}
		if len(res.Skipped) > 0 {
			fmt.Fprintf(&b, " NOT inspected at all: %s — symlinks under those trees (mounted folders among them) were not looked for, so a clean result above says nothing about them.", strings.Join(res.Skipped, ", "))
		} else {
			b.WriteString(" Every other root was fully inspected.")
		}
	}
	b.WriteString("]")
	return b.String()
}
