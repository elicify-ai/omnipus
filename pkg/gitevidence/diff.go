// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

// FileDiff is one changed path within a DiffEvidence result.
type FileDiff struct {
	// Path is the "/"-separated repo-relative path.
	Path string
	// Kind is "insert", "delete", or "modify" (merkletrie.Action.String()).
	Kind string
	// Patch is the unified-diff text for this path. For a binary change
	// go-git emits a short opaque marker rather than the full blob
	// content (confirmed by the embedding spike: a media-scoped diff
	// returned 316 bytes, not the multi-megabyte blob) — DiffEvidence
	// never inflates Judge evidence with raw binary content.
	Patch string
}

// DiffEvidence is the per-attempt, write-set-scoped diff between two
// commits — the shape the plan Judge consumes as rung-1 evidence
// (G-3/G-12).
type DiffEvidence struct {
	FromHash string
	ToHash   string
	Files    []FileDiff
	// Matched is len(Files); Total is the number of changed paths between
	// the two commits BEFORE write-set scoping — Matched < Total is
	// expected and normal (it means real changes existed outside the
	// evidence request's scope, not that anything was lost).
	Matched int
	Total   int
}

// Diff returns the write-set-scoped diff evidence between two commits
// (FR-151/G-3/G-12). writeSet scopes which changed paths are included in
// Files — pass nil/empty for an unscoped diff (every changed path). Both
// hashes must name commits reachable in this Repo; use CheckIntegrity
// first when the caller's lastKnownHash might have been invalidated by an
// out-of-band history rewrite.
func (r *Repo) Diff(fromHash, toHash string, writeSet []string) (*DiffEvidence, error) {
	fromCommit, err := r.repo.CommitObject(plumbing.NewHash(fromHash))
	if err != nil {
		return nil, fmt.Errorf("gitevidence: load from-commit %s: %w", fromHash, err)
	}
	toCommit, err := r.repo.CommitObject(plumbing.NewHash(toHash))
	if err != nil {
		return nil, fmt.Errorf("gitevidence: load to-commit %s: %w", toHash, err)
	}
	fromTree, err := fromCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("gitevidence: from-tree of %s: %w", fromHash, err)
	}
	toTree, err := toCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("gitevidence: to-tree of %s: %w", toHash, err)
	}
	changes, err := fromTree.Diff(toTree)
	if err != nil {
		return nil, fmt.Errorf("gitevidence: tree diff %s..%s: %w", fromHash, toHash, err)
	}
	return r.diffEvidenceFromChanges(changes, fromHash, toHash, writeSet)
}

// AttemptDiff returns the write-set-scoped diff of the LATEST boundary commit
// against its parent — the per-attempt diff evidence the idle/plan Judge feeds
// the prose verifier (G-3/G-15: "the Judge sees the real diff, not a transcript
// window alone"). It resolves HEAD and walks one parent back, so callers that
// only know the workspace (not a stored from-hash) still get a real, scoped
// diff without reaching into go-git themselves.
//
// If HEAD is unborn (no commits yet) it returns an empty DiffEvidence —
// nothing has been committed, so there is no diff to feed; the Judge degrades
// to the transcript window + machine evidence. If HEAD is the root commit (no
// parent), it diffs the root tree against an empty tree so the first attempt's
// files still surface as insertions. writeSet scopes which changed paths are
// included (nil/empty = every changed path, matching Diff's convention).
func (r *Repo) AttemptDiff(writeSet []string) (*DiffEvidence, error) {
	head, err := r.Head()
	if err != nil {
		return nil, fmt.Errorf("gitevidence: attempt diff: head: %w", err)
	}
	if head == "" {
		return &DiffEvidence{}, nil // unborn — nothing committed yet
	}
	headCommit, err := r.repo.CommitObject(plumbing.NewHash(head))
	if err != nil {
		return nil, fmt.Errorf("gitevidence: attempt diff: load HEAD %s: %w", head, err)
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("gitevidence: attempt diff: HEAD tree: %w", err)
	}

	var fromTree *object.Tree
	var fromHash string
	if len(headCommit.ParentHashes) > 0 {
		fromHash = headCommit.ParentHashes[0].String()
		parentCommit, perr := r.repo.CommitObject(headCommit.ParentHashes[0])
		if perr != nil {
			return nil, fmt.Errorf("gitevidence: attempt diff: load parent %s: %w", fromHash, perr)
		}
		fromTree, err = parentCommit.Tree()
		if err != nil {
			return nil, fmt.Errorf("gitevidence: attempt diff: parent tree: %w", err)
		}
	} else {
		// Root commit (no parent): diff against an empty tree so every tracked
		// file surfaces as an insertion — the first attempt's full write-set.
		fromTree = &object.Tree{}
	}

	changes, err := fromTree.Diff(headTree)
	if err != nil {
		return nil, fmt.Errorf("gitevidence: attempt diff: tree diff: %w", err)
	}
	return r.diffEvidenceFromChanges(changes, fromHash, head, writeSet)
}

// diffEvidenceFromChanges is the shared change-set → DiffEvidence builder used
// by both Diff (two named commits) and AttemptDiff (HEAD vs parent). It scopes
// the merkletrie changes to writeSet and renders each kept change's unified
// patch. errors return non-nil (the caller wraps with its own hash context).
func (r *Repo) diffEvidenceFromChanges(
	changes object.Changes, fromHash, toHash string, writeSet []string,
) (*DiffEvidence, error) {
	ev := &DiffEvidence{FromHash: fromHash, ToHash: toHash, Total: len(changes)}
	for _, ch := range changes {
		name := ch.To.Name
		if name == "" {
			name = ch.From.Name
		}
		if !pathMatches(name, writeSet) {
			continue
		}
		action, actErr := ch.Action()
		if actErr != nil {
			return nil, fmt.Errorf("gitevidence: classify change for %s: %w", name, actErr)
		}
		patch, patchErr := ch.Patch()
		if patchErr != nil {
			return nil, fmt.Errorf("gitevidence: patch for %s: %w", name, patchErr)
		}
		ev.Files = append(ev.Files, FileDiff{
			Path:  name,
			Kind:  actionKind(action),
			Patch: patch.String(),
		})
	}
	ev.Matched = len(ev.Files)
	return ev, nil
}

func actionKind(a merkletrie.Action) string {
	switch a {
	case merkletrie.Insert:
		return "insert"
	case merkletrie.Delete:
		return "delete"
	case merkletrie.Modify:
		return "modify"
	default:
		return a.String()
	}
}
