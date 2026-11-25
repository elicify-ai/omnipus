// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skillperm

// Skills & Tools C-19 "The notice" (ruling spec-gaps-1): the agents the
// upgrade has not finished for, read from the ledger on every request.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// UnfinishedAgent is one agent the upgrade has not finished for.
type UnfinishedAgent struct {
	AgentID string
	Reason  string // ReasonWriteFailed or ReasonUnreadable
}

// UnfinishedAgents lists the agents of home's upgrade ledger whose record
// write failed (write_failed) or whose record the snapshot skipped as
// unreadable (unreadable). An agent is listed only while its record file
// still exists; deleting the record clears it. No ledger lists nothing.
func UnfinishedAgents(home string) ([]UnfinishedAgent, error) {
	entities := filepath.Join(home, "entities")
	led, ok, err := readLedger(entities)
	if err != nil || !ok {
		return nil, err
	}
	var out []UnfinishedAgent
	failed := make([]string, 0, len(led.Failures))
	for id := range led.Failures {
		failed = append(failed, id)
	}
	sort.Strings(failed)
	for _, id := range failed {
		exists, err := agentRecordExists(entities, id)
		if err != nil {
			return nil, err
		}
		if exists {
			out = append(out, UnfinishedAgent{AgentID: id, Reason: ReasonWriteFailed})
		}
	}
	skipped := append([]string(nil), led.Skipped...)
	sort.Strings(skipped)
	for _, id := range skipped {
		if _, listed := led.Failures[id]; listed {
			continue
		}
		exists, err := agentRecordExists(entities, id)
		if err != nil {
			return nil, err
		}
		if exists {
			out = append(out, UnfinishedAgent{AgentID: id, Reason: ReasonUnreadable})
		}
	}
	return out, nil
}

// agentRecordExists reports whether the agent's record file is still there.
func agentRecordExists(entities, id string) (bool, error) {
	_, err := os.Stat(filepath.Join(entities, "agents", id+".json"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("skillperm: check agent record %s: %w", id, err)
}
