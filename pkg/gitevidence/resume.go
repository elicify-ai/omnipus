// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// LastCommitForTask returns the hash of the most recent boundary commit whose
// structured message names task=<taskID> — the marker CommitMeta stamps on
// every boundary commit (see formatCommitMessage). Because a boundary commit
// is write-set-scoped AND tagged with the task it was committed for, this is
// the authoritative "last checkpoint for this member" lookup the plan engine
// consumes for Play-from-commit (D13/G-12, FR-144).
//
// Returns ("", nil) when there is no such commit — an unborn HEAD (no commits
// at all) or no commit in HEAD's ancestry names this task (the member never
// reached a boundary commit: empty write-set, size-guard skip, or every path
// excluded for secrets). Callers MUST treat ("", nil) as "fresh attempt",
// never as an error — this is the FR-155 no-commit fallback. A non-nil error
// means the git layer itself was unreachable (e.g. a corrupt HEAD reference);
// the plan engine maps that to fresh-attempt too, but surfaces it for logging.
func (r *Repo) LastCommitForTask(taskID string) (string, error) {
	if taskID == "" {
		return "", nil
	}
	head, err := r.Head()
	if err != nil {
		return "", fmt.Errorf("gitevidence: last commit for task %q: head: %w", taskID, err)
	}
	if head == "" {
		return "", nil // unborn — nothing committed yet
	}
	iter, err := r.repo.Log(&git.LogOptions{From: plumbing.NewHash(head)})
	if err != nil {
		return "", fmt.Errorf("gitevidence: last commit for task %q: log: %w", taskID, err)
	}
	defer iter.Close()
	for {
		c, err := iter.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A mid-walk object error (e.g. a shallow clone boundary or a
			// missing object) — surface it; callers degrade to fresh-attempt.
			return "", fmt.Errorf("gitevidence: last commit for task %q: walk: %w", taskID, err)
		}
		if extractTaskID(c.Message) == taskID {
			return c.Hash.String(), nil
		}
	}
	return "", nil
}

// extractTaskID pulls the task=<id> value out of a boundary commit message.
// The message shape is fixed by formatCommitMessage
// ("[<boundary>] task=<id> attempt=<id> agent=<id>"), so a whitespace-split
// token scan is sufficient and avoids a regex dependency. task IDs are
// validated identifiers (no whitespace), so the token's payload is the whole
// id. Returns "" when no task= token is present (e.g. a commit authored
// outside the boundary-commit path).
func extractTaskID(message string) string {
	for _, tok := range strings.Fields(message) {
		const prefix = "task="
		if strings.HasPrefix(tok, prefix) {
			return strings.TrimPrefix(tok, prefix)
		}
	}
	return ""
}
