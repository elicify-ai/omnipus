// Omnipus — System Agent Tool Tests (shared delete-failure rig)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// agent_delete_recordfailure_helper_test.go — the shared rig the session-core
// U12 delete tests use to force a REAL failure of delete_agent's FINAL
// record-removal step (agentstore.Store.DeleteState) while keeping the agent
// entity record READABLE, so the call reaches the post-cascade FR-037
// partly-deleted path.
//
// Spec: docs/internal/specs/session-core-spec.md FR-037, C-DELETE, DEL-08.
//
// Injection: replace the per-entity sidecar LOCK file (entities/agents/<id>.lock)
// with a non-empty directory. store.Get (called from validateAndLoad) and
// DeleteState's own readStateFiles both read <id>.json with a bare os.ReadFile
// (entity.Store.load takes no lock), so the record stays readable and
// validateAndLoad passes; DeleteState's WithLock then opens the lock path with
// O_RDWR|O_CREATE and fails EISDIR — so the record delete fails AFTER the
// cascade has run.
//
// EISDIR is uid-independent AND portable: it fails for root exactly as for an
// unprivileged user, on every OS (the ci-omnipus worker runs the Go suite AS
// ROOT). Two rejected alternatives — both observed to be unusable:
//
//   - Replacing the entity file <id>.json ITSELF with a non-empty directory.
//     CONFIRMED DEFECTIVE by direct observation: validateAndLoad's store.Get
//     (FR-037: required before any write) does os.ReadFile(entityPath) and fails
//     EISDIR ("is a directory") BEFORE the cascade, so a test using it never
//     reaches the code path it claims to pin — it fails/passes for the wrong
//     reason.
//   - chmod-ing the parent directory (or the file) read-only. The ci-omnipus
//     worker runs the suite AS ROOT, where CAP_DAC_OVERRIDE makes permission bits
//     block nothing — the remove would succeed and the test would falsely fail.
//     Same finding recorded in pkg/gateway/rest_plan_stop_immutable_linux_test.go
//     and pkg/agent/goal_triggers_immutable_linux_test.go.

package systools_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// breakAgentRecordDelete forces delete_agent's final record-removal step to fail
// (see the file header for the mechanism and the two rejected alternatives).
//
// id's revision MUST be read BEFORE calling this: the revision read goes through
// store.ReadState, which takes the very lock this rig breaks.
func breakAgentRecordDelete(t *testing.T, home, id string) {
	t.Helper()
	lockPath := filepath.Join(home, "entities", "agents", id+".lock")
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("break record delete: remove lock file: %v", err)
	}
	if err := os.MkdirAll(lockPath, 0o700); err != nil {
		t.Fatalf("break record delete: mkdir in place of lock file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockPath, "blocker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("break record delete: seed blocker file: %v", err)
	}
}
