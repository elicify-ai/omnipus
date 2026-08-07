// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
)

// IntegrityStatus is the result of CheckIntegrity.
type IntegrityStatus struct {
	// OK is false when the check surfaces "evidence integrity lost"
	// (D17/G-15) — the Judge MUST fail closed on the diff channel when OK
	// is false.
	OK bool
	// HeadHash is the repo's current HEAD commit hash ("" if unborn).
	HeadHash string
	// LastKnownHash is the hash the engine passed in.
	LastKnownHash string
	// Reason is a human-readable explanation, always set when OK is false.
	Reason string
}

// CheckIntegrity detects whether the repository's HEAD has diverged from
// the engine's last-known commit out-of-band — i.e. NOT via this
// package's own serialized Commit calls (D17/G-15: "a diverged HEAD ...
// surfaces 'evidence integrity lost' and the Judge fails closed on that
// diff channel").
//
// Passing lastKnownHash == "" means "the engine has no prior commit on
// record" — any HEAD state (including none) is OK. Otherwise: if HEAD
// equals lastKnownHash, or lastKnownHash is an ancestor of HEAD (the
// ordinary, expected shape of the engine's own serialized boundary
// commits advancing history forward), integrity holds. If lastKnownHash is
// NOT an ancestor of HEAD — including the case where lastKnownHash no
// longer resolves to any commit at all — that is exactly what an
// out-of-band rewrite (amend/rebase/reset/force-push, the D17 caveat-3
// bypass the spike proved: a real `git` binary can rewrite a go-git-made
// repo with no in-process gate involved) looks like, and CheckIntegrity
// reports OK: false.
func (r *Repo) CheckIntegrity(lastKnownHash string) (*IntegrityStatus, error) {
	head, err := r.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			if lastKnownHash == "" {
				return &IntegrityStatus{OK: true}, nil
			}
			return &IntegrityStatus{
				OK:            false,
				LastKnownHash: lastKnownHash,
				Reason:        "evidence integrity lost: HEAD reference is missing but the engine has a last-known commit",
			}, nil
		}
		return nil, fmt.Errorf("gitevidence: head: %w", err)
	}
	currentHash := head.Hash().String()

	if lastKnownHash == "" || currentHash == lastKnownHash {
		return &IntegrityStatus{OK: true, HeadHash: currentHash, LastKnownHash: lastKnownHash}, nil
	}

	ancestor, err := r.isAncestor(lastKnownHash, currentHash)
	if err != nil {
		return nil, err
	}
	if ancestor {
		return &IntegrityStatus{OK: true, HeadHash: currentHash, LastKnownHash: lastKnownHash}, nil
	}
	return &IntegrityStatus{
		OK:            false,
		HeadHash:      currentHash,
		LastKnownHash: lastKnownHash,
		Reason:        "evidence integrity lost: HEAD diverged from the engine's last-known commit outside this package's serialized boundary commits",
	}, nil
}

// isAncestor reports whether the commit named by ancestorHash is a
// (possibly indirect) ancestor of the commit named by descendantHash. A
// missing ancestor object (already garbage-collected, or never really
// existed — e.g. a rewritten/dropped commit) is reported as "not an
// ancestor" rather than erroring, since from the caller's perspective both
// outcomes mean the same thing: the last-known commit cannot be vouched
// for anymore.
func (r *Repo) isAncestor(ancestorHash, descendantHash string) (bool, error) {
	ancestorCommit, err := r.repo.CommitObject(plumbing.NewHash(ancestorHash))
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("gitevidence: load last-known commit %s: %w", ancestorHash, err)
	}
	descendantCommit, err := r.repo.CommitObject(plumbing.NewHash(descendantHash))
	if err != nil {
		return false, fmt.Errorf("gitevidence: load HEAD commit %s: %w", descendantHash, err)
	}
	ok, err := ancestorCommit.IsAncestor(descendantCommit)
	if err != nil {
		return false, fmt.Errorf("gitevidence: ancestry check %s..%s: %w", ancestorHash, descendantHash, err)
	}
	return ok, nil
}
