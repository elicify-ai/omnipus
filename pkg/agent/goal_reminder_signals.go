package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// collectGoalReminderProgress observes the current window while work.mu is held.
// Busy is separate from progress: unchanged running presence never resets the
// streak, and unknown sources never become an unproductive assessment.
func (al *AgentLoop) collectGoalReminderProgress(sessionID string, rec *goal.Goal, work *goalReminderWork, since time.Time) (goalReminderProgress, bool, error) {
	var progress goalReminderProgress
	if since.IsZero() {
		since = goalReminderInitialBoundary(rec)
		if since.IsZero() {
			return progress, false, errors.New("goal reminder: no persisted activity boundary")
		}
	}
	scope, err := al.reminderScope(sessionID)
	if err != nil {
		return progress, false, err
	}
	busy, processProgress := al.reminderBackgroundProcesses(scope, work, since)
	busy = busy || al.goalAdjudicationInFlight(sessionID)
	progress.background = processProgress
	jobsBusy, jobsProgress, err := al.reminderOwnedJobs(sessionID, scope, work, since)
	if err != nil {
		return progress, false, err
	}
	busy, progress.background = busy || jobsBusy, progress.background || jobsProgress
	progress.tool = !since.IsZero() && work.toolActivityAt.After(since)
	progress.evidence, err = al.reminderEvidence(rec, scope, work, since)
	if err != nil {
		return progress, false, err
	}
	progress.descendant, err = al.reminderDescendantOutput(sessionID, scope, work, since)
	if err != nil {
		return progress, false, err
	}
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return progress, false, errors.New("goal reminder: session store is unavailable")
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		return progress, false, fmt.Errorf("goal reminder: read workspace association: %w", err)
	}
	if meta == nil {
		return progress, false, errors.New("goal reminder: workspace association is unavailable")
	}
	progress.files, err = al.reminderScopedDiff(sessionID, meta.WorkspaceID)
	if err != nil {
		return progress, false, err
	}
	if goalReminderMadeProgress(progress) {
		work.observedProgress = true
	}
	// Return fresh activity separately from the retained outcome latch. A busy
	// reset may consume this window without manufacturing new activity later.
	return progress, busy, nil
}

// goalReminderInitialBoundary bootstraps only a missing RAM watermark from
// existing goal clocks. It never treats earlier goals' old work as new activity.
func goalReminderInitialBoundary(rec *goal.Goal) time.Time {
	at := effectiveGoalActivity(rec)
	if rec != nil && rec.StartedAt != nil && rec.StartedAt.After(at) {
		at = *rec.StartedAt
	}
	return at
}

func (al *AgentLoop) reminderEvidence(rec *goal.Goal, scope *goalReminderScope, work *goalReminderWork, since time.Time) (bool, error) {
	taskIDs := make(map[string]bool)
	if rec.OwnerKind == generated.GoalOwnerKindTask {
		taskIDs[rec.OwnerID] = true
	}
	for _, record := range scope.records {
		if scope.sessions[record.SessionID] && record.Origin != nil && record.Origin.TaskID != "" {
			taskIDs[record.Origin.TaskID] = true
		}
	}
	progress := false
	for taskID := range taskIDs {
		records, err := al.evidenceStore().List(taskID)
		if err != nil {
			return false, fmt.Errorf("goal reminder: read check evidence: %w", err)
		}
		entries, err := os.ReadDir(filepath.Join(config.OmnipusHomeDir(), "tasks_evidence", taskID))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("goal reminder: inspect check evidence: %w", err)
		}
		files := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				files++
			}
		}
		if files != len(records) {
			return false, errors.New("goal reminder: check evidence is partially unreadable")
		}
		if len(records) == 0 {
			continue
		}
		// Read every retained run before any record can latch progress.
		// Task id alone does not say which execution wrote the check.
		store := GetTaskStore(al)
		if store == nil {
			return false, errors.New("goal reminder: check evidence run store is unavailable")
		}
		runs, err := reminderTaskRuns(store, taskID)
		if err != nil {
			return false, fmt.Errorf("goal reminder: read check execution ownership: %w", err)
		}
		for _, record := range records {
			if record.ID == "" || record.TaskID != taskID || record.PolicyDenied || record.TimedOut || record.ExitCode < 0 {
				continue
			}
			at, err := parseReminderTime(record.RecordedAt)
			if err != nil {
				return false, err
			}
			owned, err := reminderEvidenceOwned(scope, taskID, runs, at)
			if err != nil {
				return false, fmt.Errorf("goal reminder: resolve check execution window: %w", err)
			}
			if !owned {
				continue
			}
			progress = reminderEvent(work, "evidence:"+record.ID, at, since) || progress
		}
	}
	return progress, nil
}

func (al *AgentLoop) reminderDescendantOutput(root string, scope *goalReminderScope, work *goalReminderWork, since time.Time) (bool, error) {
	progress := false
	for id := range scope.sessions {
		if id == root {
			continue // root text/claim-only replies are deliberately not progress
		}
		store := al.ResolveSessionStore(id)
		if store == nil {
			return false, fmt.Errorf("goal reminder: descendant store for %q is unavailable", id)
		}
		entries, err := store.ReadTranscript(id)
		if err != nil {
			return false, fmt.Errorf("goal reminder: read descendant activity: %w", err)
		}
		for _, entry := range entries {
			if entry.Role == "assistant" && strings.TrimSpace(entry.Content) != "" {
				progress = reminderEvent(work, "child-output:"+id+":"+entry.ID, entry.Timestamp, since) || progress
			}
		}
	}
	for _, record := range scope.records {
		if record.SessionID == root || !scope.sessions[record.SessionID] || record.FinalDelivery == nil || record.FinalDelivery.CommitID == "" {
			continue
		}
		progress = reminderEvent(work, "child-final:"+record.FinalDelivery.CommitID, record.LastActivityAt, since) || progress
	}
	return progress, nil
}

// reminderScopedDiff reuses the existing guarded HEAD-range boundary. Unlike
// the legacy Boolean helper, failed reads preserve the last usable boundary.
func (al *AgentLoop) reminderScopedDiff(sessionID, workspaceID string) (bool, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return false, nil
	}
	dir, err := workspace.SafeWorkDir(config.OmnipusHomeDir(), workspaceID)
	if err != nil {
		return false, err
	}
	repo, err := gitevidence.Open(dir, gitevidence.WithSecretScanner(al.verifierEvidenceScanner()))
	if errors.Is(err, gitevidence.ErrNestedRepo) {
		return false, nil // existing supported no-evidence-layer posture
	}
	if err != nil {
		return false, fmt.Errorf("goal reminder: open scoped evidence: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return false, fmt.Errorf("goal reminder: read scoped boundary: %w", err)
	}
	s := goalTriggers()
	s.mu.Lock()
	boundary, known := s.diffBoundaryHash[sessionID]
	s.mu.Unlock()
	changed := false
	if known && boundary != head && head != "" {
		var diff *gitevidence.DiffEvidence
		if boundary == "" {
			// We observed this repo while unborn. The new boundary is
			// necessarily inside this window, including a root commit's
			// real inserted files; it is not an old workspace-wide diff.
			diff, err = repo.AttemptDiff(nil)
		} else {
			diff, err = repo.Diff(boundary, head, nil)
		}
		if err != nil {
			return false, fmt.Errorf("goal reminder: read scoped diff: %w", err)
		}
		changed = diff != nil && len(diff.Files) != 0
	}
	s.mu.Lock()
	s.diffBoundaryHash[sessionID] = head
	s.mu.Unlock()
	return changed, nil
}
