package omnpkg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RecoveryOps is the store edge start-up recovery works through (FR-062,
// Rollback contract revision 7 "Start-up recovery"). The gateway fills it with
// the agent store, the shared workspace join/leave and the audit log; the
// engine itself only reads and writes the staging journals.
type RecoveryOps interface {
	// Published reports whether entities/agents/<agentID>.json exists.
	Published(agentID string) (bool, error)
	// Leave removes agentID from workspace wsID's team and every edge
	// involving it. A missing workspace is not a failure (sign-off C-7).
	Leave(wsID, agentID string) error
	// Join is the idempotent JoinWorkspaceTeam (team entry plus self-row).
	Join(wsID, agentID string) error
	// DiscardPending deletes <agentID>.pending and agents/<agentID>/.
	DiscardPending(agentID string) error
	// UnpublishAndDiscard deletes a published record and its folder. Only
	// called when a committed import cannot be re-joined (FR-062).
	UnpublishAndDiscard(agentID string) error
	// PendingIDs lists the agent IDs that have an entities/agents/*.pending.
	PendingIDs() ([]string, error)
	// Audit writes one audit entry for a recovered import. A failure is the
	// caller's to log; it never changes the recovery outcome.
	Audit(outcome, agentID, stagingID, message string)
}

// Recovery audit messages (FR-062: one audit entry per recovered import).
const (
	RecoveryFinishedMessage   = "import completed at start-up"
	RecoveryRolledBackMessage = "import rolled back at start-up"
)

// RecoveryReport is what one RecoverImports run did. Failed holds the staging
// IDs whose recovery step failed (kept for the next start, FR-044); Errors
// holds the step errors in the order they happened.
type RecoveryReport struct {
	Finished       []string
	RolledBack     []string
	Failed         []string
	OrphansRemoved []string
	Errors         []error
}

// RecoverImports finishes or rolls back every unfinished import under
// stagingBase ($OMNIPUS_HOME/backups/.staging), deciding from the disk and not
// from the journal state, then removes orphan pending records no unfinished
// journal names (FR-062). It never stops at a failed step: the failure is
// recorded, the journal stays unfinished (the staging keeps its slot and the
// next upload warns, FR-044) and the next start tries again. It is idempotent:
// finished journals (done, rolled_back) are left as they are.
//
// A recovered staging keeps only its journal, in its terminal state; the next
// upload's Sweep deletes the folder (a finished journal is deleted there).
func RecoverImports(ctx context.Context, stagingBase string, ops RecoveryOps) RecoveryReport {
	var rep RecoveryReport
	keep := map[string]bool{} // agent IDs whose recovery failed: their pending record stays
	entries, err := os.ReadDir(stagingBase)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		rep.Errors = append(rep.Errors, fmt.Errorf("omnpkg: recovery: list staging: %w", err))
	}
	for _, de := range entries {
		if ctx.Err() != nil {
			rep.Errors = append(rep.Errors, ctx.Err())
			return rep
		}
		if !de.IsDir() {
			continue
		}
		dir := filepath.Join(stagingBase, de.Name())
		j, jerr := ReadJournal(dir)
		if jerr != nil {
			if !errors.Is(jerr, os.ErrNotExist) {
				rep.Failed = append(rep.Failed, de.Name())
				rep.Errors = append(rep.Errors, jerr)
			}
			continue
		}
		if j.State.Terminal() {
			continue
		}
		recoverOne(dir, de.Name(), j, ops, &rep, keep)
	}
	removeOrphanPending(ops, keep, &rep)
	return rep
}

// recoverOne applies the start-up recovery table to one unfinished journal.
func recoverOne(dir, stagingID string, j JournalFile, ops RecoveryOps, rep *RecoveryReport, keep map[string]bool) {
	fail := func(err error) {
		rep.Failed = append(rep.Failed, stagingID)
		rep.Errors = append(rep.Errors, fmt.Errorf("omnpkg: recovery of staging %s: %w", stagingID, err))
		if j.AgentID != "" {
			keep[j.AgentID] = true
		}
		if j.State != JournalRollbackIncomplete && j.State != JournalCommitted {
			j.State = JournalRollbackIncomplete
			if werr := WriteJournal(dir, j); werr != nil {
				rep.Errors = append(rep.Errors, werr)
			}
		}
	}
	if j.AgentID == "" {
		fail(errors.New("journal names no agent"))
		return
	}
	published, err := ops.Published(j.AgentID)
	if err != nil {
		fail(err)
		return
	}
	if published {
		joinErr := errors.New("journal names no workspace")
		if j.WorkspaceID != "" {
			joinErr = ops.Join(j.WorkspaceID, j.AgentID)
		}
		if joinErr == nil {
			finishJournal(dir, stagingID, j, JournalDone, RecoveryFinishedMessage, ops, rep)
			rep.Finished = append(rep.Finished, stagingID)
			return
		}
		// FR-062: the re-join failed, so the published record is rolled back
		// too (nothing has loaded it yet; no success answer was ever sent).
		if err := rollbackRecovered(j, ops, ops.UnpublishAndDiscard); err != nil {
			fail(errors.Join(joinErr, err))
			return
		}
	} else if err := rollbackRecovered(j, ops, ops.DiscardPending); err != nil {
		fail(err)
		return
	}
	finishJournal(dir, stagingID, j, JournalRolledBack, RecoveryRolledBackMessage, ops, rep)
	rep.RolledBack = append(rep.RolledBack, stagingID)
}

// rollbackRecovered runs the leave (when the journal names a workspace) and
// then the discard, always both (revision 7: a C1 failure does not stop C2).
func rollbackRecovered(j JournalFile, ops RecoveryOps, discard func(string) error) error {
	var leaveErr error
	if j.WorkspaceID != "" {
		leaveErr = ops.Leave(j.WorkspaceID, j.AgentID)
	}
	return errors.Join(leaveErr, discard(j.AgentID))
}

// finishJournal writes the terminal journal state, removes every other file
// in the staging folder and writes the recovery audit entry.
func finishJournal(dir, stagingID string, j JournalFile, state JournalState, message string, ops RecoveryOps, rep *RecoveryReport) {
	j.State = state
	if err := WriteJournal(dir, j); err != nil {
		rep.Errors = append(rep.Errors, err)
	}
	if files, err := os.ReadDir(dir); err == nil {
		for _, f := range files {
			if f.Name() == JournalName {
				continue
			}
			if rerr := os.RemoveAll(filepath.Join(dir, f.Name())); rerr != nil {
				rep.Errors = append(rep.Errors, fmt.Errorf("omnpkg: recovery: clean staging %s: %w", stagingID, rerr))
			}
		}
	}
	ops.Audit(string(state), j.AgentID, stagingID, message)
}

// removeOrphanPending discards every pending record that no kept journal
// names (FR-062: orphan *.pending, for example restored from a backup).
func removeOrphanPending(ops RecoveryOps, keep map[string]bool, rep *RecoveryReport) {
	ids, err := ops.PendingIDs()
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Errorf("omnpkg: recovery: list pending records: %w", err))
		return
	}
	sort.Strings(ids)
	for _, id := range ids {
		if keep[id] {
			continue
		}
		if err := ops.DiscardPending(id); err != nil {
			rep.Errors = append(rep.Errors, fmt.Errorf("omnpkg: recovery: discard orphan pending %s: %w", id, err))
			continue
		}
		rep.OrphansRemoved = append(rep.OrphansRemoved, id)
	}
}
