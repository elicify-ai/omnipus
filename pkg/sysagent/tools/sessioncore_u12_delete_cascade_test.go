// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U12 (cleanup-first agent deletion), sysagent slice.
// Spec: docs/internal/specs/session-core-spec.md FR-037, C-DELETE,
// BDD-11.3/11.5, DEL-08.
//
// FR-037/C-DELETE: "delete owned chats/memory and required cleanup first, record
// last. Failure leaves visible agent with honest partly-deleted result; same
// Delete idempotently retries."
//
// Oracle: when the FINAL record removal fails, the owned-data cleanup (sessions/
// tasks) must ALREADY have run — because cleanup precedes the record removal.
// Today the entity record is deleted FIRST (agentDeleteToolExecute.deleteAndCascade),
// so a record-delete failure aborts before any cleanup and the session survives.
// The existing TestAgentDelete_StoreDeleteFailure_NoDestructiveCascade pins that
// old (now-inverted) order; this test pins the FR-037 order.

package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// FR-037/C-DELETE/BDD-11.3: cleanup-first, record-last. When the record delete
// (the last step) fails, owned data has already been cleaned.
func TestSessionCoreU12_DeleteCleansOwnedDataBeforeRemovingRecord(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	store := agentstore.New(home)
	if err := store.Create("victim", &config.AgentConfig{ID: "victim", Name: "Victim"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	// One solely-owned session under victim.
	sessStore, storeErr := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if storeErr != nil {
		t.Fatalf("test setup: open session store: %v", storeErr)
	}
	meta, metaErr := sessStore.NewSession(session.SessionTypeChat, "webchat", "victim")
	if metaErr != nil {
		t.Fatalf("test setup: create session: %v", metaErr)
	}
	sessionID := meta.ID
	if err := sessStore.Close(); err != nil {
		t.Fatalf("test setup: close session store: %v", err)
	}

	// Force the FINAL record removal to fail: replace the entity JSON file with
	// a non-empty directory so os.Remove fails with ENOTEMPTY regardless of uid
	// (the same technique the existing entity-first test uses).
	revision := currentAgentRevision(t, deps, "victim")
	entityPath := filepath.Join(home, "entities", "agents", "victim.json")
	if err := os.Remove(entityPath); err != nil {
		t.Fatalf("test setup: remove entity file: %v", err)
	}
	if err := os.MkdirAll(entityPath, 0o700); err != nil {
		t.Fatalf("test setup: mkdir in place of entity file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entityPath, "blocker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("test setup: seed blocker file: %v", err)
	}

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":       "victim",
		"confirm":  true,
		"revision": revision,
	})
	if !result.IsError {
		t.Fatalf("expected an error when the final record removal fails, got success: %s", result.ForLLM)
	}

	// FR-037 cleanup-first: the solely-owned session must ALREADY be removed,
	// because cleanup ran before the (failed) record removal. Today it survives.
	sessionDir := filepath.Join(home, "sessions", sessionID)
	if _, err := os.Stat(sessionDir); err == nil {
		t.Fatalf(
			"FR-037/C-DELETE: owned session %s must be cleaned BEFORE the record removal; it survived a failed record delete — the cascade is still entity-first",
			sessionID,
		)
	}

	// And the agent record must remain VISIBLE (partly-deleted), so the same
	// Delete can retry after restart. The entity path is present (as the blocker
	// dir); assert the store still lists it.
	if _, err := store.Get("victim"); err != nil {
		t.Fatalf("FR-037: a partly-deleted agent must remain visible for retry; Get(victim) = %v", err)
	}
}