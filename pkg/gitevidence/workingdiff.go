// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	godiff "github.com/go-git/go-git/v5/utils/diff"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// secretWithheldPatch replaces the rendered patch text of a path whose
// content matched this Repo's secret guard (MIN-5). The CHANGE is still
// reported — path, kind, and the counters — so the Judge is never told
// "nothing happened here"; only the bytes are withheld.
const secretWithheldPatch = "(patch withheld — this path's content matched the sensitive-value scan (MIN-5); " +
	"the change itself is reported, its content is not)"

// DiffWorkingTree returns the write-set-scoped diff evidence between the
// commit at fromHash (or an empty tree when fromHash == "", meaning "since
// this workspace's evidence repo began") and the repo's CURRENT WORKING
// TREE ON DISK — including any changes never committed at all.
//
// Operator correction (fix GX-E, 2026-09): a commit is an audit/progress
// artifact, never the evidence path — THE WORKING TREE IS GROUND TRUTH. The
// prior design (Diff/AttemptDiff, both commit-to-commit) could report
// "(no workspace diff available)" purely because nothing had been
// committed yet, even when the worker's files were sitting right there on
// disk. DiffWorkingTree never has that failure mode: it walks the live
// filesystem directly, so it reports real evidence whether or not ANYTHING
// has ever been committed — including from an entirely unborn HEAD.
//
// writeSet scopes which paths are considered (nil/empty = every path
// present in fromHash's tree OR on disk under r.dir, minus .git and any
// symlink — a workspace mount is never read through, mirroring Commit's own
// ADR-063 D4 exclusion). ToHash is always the literal marker
// "(working tree)" — there is no commit to name the live filesystem state.
//
// This is read-only: it never stages, commits, or otherwise mutates the
// repository. Concurrent with a Commit call is safe (Commit's own mutex is
// not required here — a diff racing a real commit at worst sees a
// slightly-stale filesystem snapshot, the same staleness any `git diff`
// invoked mid-write would show).
//
// SECRET DISCIPLINE (MIN-5, review finding 6): this function reads raw
// working-tree bytes and its output is rendered straight into the Judge's
// prompt and sent to an external model on every round — a strictly WIDER
// exposure than Commit's (which only writes to a local repo). It therefore
// carries the SAME two controls Commit has, and for the same reason:
//
//  1. FAIL-CLOSED on an unguarded Repo. A Repo opened with neither
//     WithSecretScanner nor WithRedactor is refused outright, exactly like
//     Commit's MAJOR-3/MIN-5 guard. Before this, the judge call site opened
//     the repo with NO options at all, so an agent that wrote an API key
//     into work/.env got it excluded from the boundary commit (Commit's
//     guard) and then classified here as a plain "insert" and rendered into
//     the prompt verbatim. A caller that legitimately wants a no-op scan
//     opts in visibly with WithRedactor(identity), as Commit's doc comment
//     already describes.
//  2. PER-PATH EXCLUSION of matching content. A path whose committed-side
//     or working-tree-side content matches the guard keeps its entry in
//     Files (path, kind, and both counters) but its Patch is replaced with
//     secretWithheldPatch. The change stays visible to the Judge; the bytes
//     do not. Withholding the whole FILE instead would tell the Judge
//     "nothing changed here", which is the fail-open direction.
func (r *Repo) DiffWorkingTree(fromHash string, writeSet []string) (*DiffEvidence, error) {
	if r.scanner == nil && r.redact == nil {
		return nil, fmt.Errorf(
			"gitevidence: working-tree diff refused: no secret scanner or redactor configured (MIN-5 fail-closed) " +
				"— pass WithSecretScanner or WithRedactor to Open",
		)
	}

	// tainted maps a path to the guard's human-readable, non-secret-leaking
	// detail label, for every path whose content matched on EITHER side.
	tainted := make(map[string]string)
	fromContents := make(map[string]string)
	displayFrom := fromHash
	if fromHash == "" {
		displayFrom = "(repo start — no prior commit)"
	} else {
		fromCommit, err := r.repo.CommitObject(plumbing.NewHash(fromHash))
		if err != nil {
			return nil, fmt.Errorf("gitevidence: load from-commit %s: %w", fromHash, err)
		}
		fromTree, err := fromCommit.Tree()
		if err != nil {
			return nil, fmt.Errorf("gitevidence: from-tree of %s: %w", fromHash, err)
		}
		iter := fromTree.Files()
		defer iter.Close()
		if err := iter.ForEach(func(f *object.File) error {
			if !pathMatches(f.Name, writeSet) {
				return nil
			}
			content, cerr := f.Contents()
			if cerr != nil {
				return fmt.Errorf("gitevidence: read committed content of %s: %w", f.Name, cerr)
			}
			if hit, detail := r.fileHasSecret(f.Name, []byte(content)); hit {
				tainted[f.Name] = detail
			}
			fromContents[f.Name] = content
			return nil
		}); err != nil {
			return nil, err
		}
	}

	toContents := make(map[string]string)
	walkErr := filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(r.dir, p)
		if relErr != nil {
			return fmt.Errorf("gitevidence: relativize %s: %w", p, relErr)
		}
		relSlash := filepath.ToSlash(rel)
		if !pathMatches(relSlash, writeSet) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			if os.IsNotExist(infoErr) {
				return nil // removed between the WalkDir readdir and this Info call
			}
			return fmt.Errorf("gitevidence: stat %s: %w", relSlash, infoErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Never read through a workspace-mount symlink's target — same
			// exclusion Commit applies (ADR-063 D4).
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				return nil // same benign race as above
			}
			return fmt.Errorf("gitevidence: read working-tree file %s: %w", relSlash, readErr)
		}
		if hit, detail := r.fileHasSecret(relSlash, data); hit {
			tainted[relSlash] = detail
		}
		toContents[relSlash] = string(data)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("gitevidence: walk working tree %s: %w", r.dir, walkErr)
	}

	allPaths := make(map[string]bool, len(fromContents)+len(toContents))
	for p := range fromContents {
		allPaths[p] = true
	}
	for p := range toContents {
		allPaths[p] = true
	}
	paths := make([]string, 0, len(allPaths))
	for p := range allPaths {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	ev := &DiffEvidence{FromHash: displayFrom, ToHash: "(working tree, includes uncommitted changes)"}
	for _, p := range paths {
		fromC, hadFrom := fromContents[p]
		toC, hadTo := toContents[p]
		if hadFrom && hadTo && fromC == toC {
			continue // unchanged
		}
		ev.Total++
		var kind string
		switch {
		case !hadFrom && hadTo:
			kind = "insert"
		case hadFrom && !hadTo:
			kind = "delete"
		default:
			kind = "modify"
		}
		// A tainted path is never line-diffed at all — the rendered patch is
		// exactly where the secret bytes would end up, so it is not produced
		// in the first place rather than produced and then discarded.
		patch := secretWithheldPatch
		if detail, hit := tainted[p]; hit {
			logger.WarnCF("gitevidence",
				"withholding working-tree diff patch from evidence: sensitive value detected (MIN-5)",
				map[string]any{"dir": r.dir, "path": p, "kind": kind, "detail": detail})
		} else {
			patch = renderWorkingTreePatch(fromC, toC)
		}
		ev.Files = append(ev.Files, FileDiff{
			Path:  p,
			Kind:  kind,
			Patch: patch,
		})
	}
	ev.Matched = len(ev.Files)
	return ev, nil
}

// renderWorkingTreePatch renders a readable, line-prefixed ("+"/"-"/" ")
// patch between from and to using go-git's own line-oriented diff algorithm
// (utils/diff — the exact package object.Change.Patch() itself wraps
// internally, github.com/sergi/go-diff already an existing transitive
// dependency of this module, not a new one). This is NOT a strict
// git-apply-able unified diff (no hunk headers/line numbers) — it exists to
// give the prose Judge readable evidence text, not a patch to apply.
//
// Binary content (detected via a NUL byte, the same heuristic git itself
// uses) is never diffed line-by-line — a short opaque marker is returned
// instead, mirroring FileDiff.Patch's own documented binary-change
// contract.
func renderWorkingTreePatch(from, to string) string {
	if looksBinary(from) || looksBinary(to) {
		return "(binary content changed — patch omitted)"
	}
	if from == "" {
		return prefixLines("+", to)
	}
	if to == "" {
		return prefixLines("-", from)
	}
	diffs := godiff.DoWithTimeout(from, to, 5*time.Second)
	var sb strings.Builder
	for _, d := range diffs {
		prefix := " "
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			prefix = "+"
		case diffmatchpatch.DiffDelete:
			prefix = "-"
		}
		chunk := strings.TrimSuffix(d.Text, "\n")
		if chunk == "" {
			continue
		}
		for _, line := range strings.Split(chunk, "\n") {
			sb.WriteString(prefix)
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// prefixLines prefixes every line of s with prefix (used for a whole-file
// insert/delete, where there is nothing to line-diff against).
func prefixLines(prefix, s string) string {
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		sb.WriteString(prefix)
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String()
}

// looksBinary reports whether s looks like binary content (contains a NUL
// byte) — the same cheap heuristic git itself uses to decide whether a
// textual diff is meaningful.
func looksBinary(s string) bool {
	return strings.Contains(s, "\x00")
}
