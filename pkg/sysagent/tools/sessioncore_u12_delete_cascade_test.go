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
// Oracle: when the FINAL record-removal step (store.DeleteState) fails, the
// owned-data cleanup (sessions/tasks) must ALREADY have run — because cleanup
// precedes the record removal — the result must be the honest PARTLY-DELETED
// result (partly_deleted=true, error_stage="record_delete", sessions_deleted=1),
// and the record must stay VISIBLE so the same Delete retries.
//
// The record-removal failure is injected by breakAgentRecordDelete (see
// agent_delete_recordfailure_helper_test.go for the mechanism and why the
// RED-D pack's original injection — replacing the entity file with a
// non-empty directory — is defective).

package systools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// FR-037/C-DELETE/BDD-11.3: cleanup-first, record-last. When the record delete
// (the last step) fails, owned data has already been cleaned, the result is the
// honest partly-deleted one, and the record stays visible for retry.
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

	// Read the revision BEFORE breaking the record delete: currentAgentRevision
	// goes through store.ReadState, which takes the very lock the rig breaks.
	revision := currentAgentRevision(t, deps, "victim")
	breakAgentRecordDelete(t, home, "victim")

	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":       "victim",
		"confirm":  true,
		"revision": revision,
	})
	if !result.IsError {
		t.Fatalf("expected an error when the final record removal fails, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "SAVE_FAILED" {
		t.Errorf("error code = %v, want SAVE_FAILED", errBlock["code"])
	}

	// The failure must be the honest PARTLY-DELETED result, not a bare error:
	// the record delete is the failed stage, and the cascade counts are reported.
	if pd, _ := m["partly_deleted"].(bool); !pd {
		t.Errorf("partly_deleted = %v, want true (the record delete is the failed step)", m["partly_deleted"])
	}
	if stage, _ := m["error_stage"].(string); stage != "record_delete" {
		t.Errorf("error_stage = %v, want %q (only the final record delete may fail here)", m["error_stage"], "record_delete")
	}

	// FR-037 cleanup-first: the solely-owned session must ALREADY be removed,
	// because cleanup ran before the (failed) record removal.
	if n, _ := m["sessions_deleted"].(float64); n != 1 {
		t.Errorf("sessions_deleted = %v, want 1 (cleanup must run before the record delete)", m["sessions_deleted"])
	}
	sessionDir := filepath.Join(home, "sessions", sessionID)
	if _, err := os.Stat(sessionDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(
			"FR-037/C-DELETE: owned session %s must be cleaned BEFORE the record removal; it survived a failed record delete (err=%v)",
			sessionID, err,
		)
	}

	// And the agent record must remain VISIBLE (partly-deleted), so the same
	// Delete can retry.
	if _, err := store.Get("victim"); err != nil {
		t.Errorf("FR-037: a partly-deleted agent must remain visible for retry; Get(victim) = %v", err)
	}
}
