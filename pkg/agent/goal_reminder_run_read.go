package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// reminderTaskHistory resolves retained history by actual execution provenance,
// not the current task row (which may be deleted or name a later foreign run).
func (al *AgentLoop) reminderTaskHistory(scope *goalReminderScope, work *goalReminderWork, since time.Time) (bool, error) {
	ids := make(map[string]bool)
	for _, record := range scope.records {
		if scope.sessions[record.SessionID] && record.Origin != nil && record.Origin.TaskID != "" && !scope.scratchpads[record.Origin.TaskID] {
			ids[record.Origin.TaskID] = true
		}
	}
	for _, item := range scope.tasks {
		if item.SessionID != "" && scope.sessions[item.SessionID] {
			ids[item.ID] = true
		}
	}
	store := GetTaskStore(al)
	if store == nil && len(ids) != 0 {
		return false, errors.New("goal reminder: owned task history store is unavailable")
	}
	progress := false
	for id := range ids {
		runs, err := reminderTaskRuns(store, id)
		if err != nil {
			return false, fmt.Errorf("goal reminder: read owned task outcomes: %w", err)
		}
		for _, run := range runs {
			if !scope.sessions[run.SessionID] || run.EndedAt == nil {
				continue
			}
			at, err := parseReminderTime(*run.EndedAt)
			if err != nil {
				return false, err
			}
			progress = reminderEvent(work, "task:"+run.RunID+":"+string(run.Status), at, since) || progress
		}
	}
	return progress, nil
}

// reminderTaskRuns keeps the existing canonical folding/validation while
// refusing its documented warn-and-skip corruption posture for an assessment
// of no progress. It adds no history or persistence of its own.
func reminderTaskRuns(store *task.Store, taskID string) ([]task.TaskRun, error) {
	runs, err := store.ListRuns(taskID) // validates the ID before resolving paths
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(store.Dir(), taskID, "runs")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return runs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if err := validateReminderRunFile(filepath.Join(dir, entry.Name())); err != nil {
			return nil, fmt.Errorf("goal reminder: task history is partially unreadable: %w", err)
		}
	}
	return runs, nil
}

func validateReminderRunFile(path string) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	scan := bufio.NewScanner(file)
	// Real retained results are capped at 50,000 characters by CloseRun.
	// Leave room for JSON escaping/metadata without loading whole day files.
	scan.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		if len(line) == 0 {
			continue
		}
		var run task.TaskRun
		if err := json.Unmarshal(line, &run); err != nil {
			return err
		}
		if run.RunID == "" {
			return errors.New("run history has no execution identity")
		}
	}
	return scan.Err()
}
