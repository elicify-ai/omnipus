// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLegacyLifecycleRecordWithTimeLimitLoads is issue #1306's
// backward-compatibility guarantee for persisted sessions: a lifecycle record
// written before the delegation time limit was removed carries
// steered_by.limits.timeout_seconds and the active-time budget fields. It must
// still load (the steering edge intact), and the next persist must drop the
// retired keys.
func TestLegacyLifecycleRecordWithTimeLimitLoads(t *testing.T) {
	dir := t.TempDir()
	store := NewLifecycleStore(dir)
	rec := &LifecycleRecord{
		SessionID: "legacy-child", Generation: 1, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker",
		SteeredBy: &SteeredBy{
			SteeringSessionID: "parent-1", RootSessionID: "parent-1",
			Authorization: Authorization{Mode: AuthorizationModeDirect, RemainingDepth: 2},
		},
	}
	if err := store.Persist(rec); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// Rewrite the record line exactly as an older release persisted it.
	path := filepath.Join(dir, "legacy-child.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var line map[string]any
	if uerr := json.Unmarshal([]byte(lines[len(lines)-1]), &line); uerr != nil {
		t.Fatalf("parse record line: %v", uerr)
	}
	steeredBy, ok := line["steered_by"].(map[string]any)
	if !ok {
		t.Fatalf("SETUP: record line has no steered_by object: %s", lines[len(lines)-1])
	}
	steeredBy["limits"] = map[string]any{"timeout_seconds": 900}
	line["active_budget_anchor"] = "2026-10-01T10:00:00Z"
	line["stopped_for_seconds"] = 42
	legacy, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	lines[len(lines)-1] = string(legacy)
	if werr := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}

	reopened := NewLifecycleStore(dir)
	loaded, err := reopened.Load("legacy-child")
	if err != nil {
		t.Fatalf("Load(legacy record with retired time-limit keys) = %v; it must load", err)
	}
	if loaded.SteeredBy == nil || loaded.SteeredBy.SteeringSessionID != "parent-1" || loaded.SteeredBy.Authorization.RemainingDepth != 2 {
		t.Fatalf("steering edge lost while ignoring the retired keys: %+v", loaded.SteeredBy)
	}

	if merr := reopened.Mutate("legacy-child", func(r *LifecycleRecord) error {
		r.State = LifecycleCompleted
		return nil
	}); merr != nil {
		t.Fatalf("Mutate(legacy record): %v", merr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	afterLines := strings.Split(strings.TrimSpace(string(after)), "\n")
	last := afterLines[len(afterLines)-1]
	for _, retired := range []string{"\"limits\"", "active_budget_anchor", "stopped_for_seconds"} {
		if strings.Contains(last, retired) {
			t.Errorf("re-persisted record still carries retired key %s: %s", retired, last)
		}
	}
}
