package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DESIGN Decision 1 and exact contract table: Interrupted is a frozen ended
// generation with one of two nonfatal causes, not an alias for Running/Failed.
// Decode the specified JSON so this RED pack compiles before new Go fields exist.
func crashInterruptedRecord(t *testing.T, state, reason string) *LifecycleRecord {
	t.Helper()
	raw := map[string]any{"session_id": "crash-session", "generation": 1, "state": state, "owner_scope_kind": "human", "workspace_id": "ws", "agent_id": "mia",
		"execution_id": map[string]any{"run_id": "ended-execution", "boot_seq": 1}}
	if reason != "" {
		raw["interruption_reason"] = reason
	}
	bytes, err := json.Marshal(raw)
	require.NoError(t, err)
	var rec LifecycleRecord
	require.NoError(t, json.Unmarshal(bytes, &rec))
	return &rec
}

func TestCrashRecovery_InterruptedLifecycleIsFrozenAndDisplayable(t *testing.T) {
	for _, reason := range []string{"tool_limit", "reconciliation_required"} {
		t.Run(reason, func(t *testing.T) {
			rec := crashInterruptedRecord(t, "interrupted", reason)
			assert.True(t, IsValidLifecycleState(rec.State), "Q14/Q15: interrupted is a real durable state")
			assert.True(t, rec.Terminal(), "ended generation is frozen, not automatically resumable")
			assert.Equal(t, LifecycleDisplayInterrupted, LifecycleRecordToDisplay(rec), "UI adapters must show Interrupted without Failed/Working/restart-only projection")
			s := newTestLifecycleStore(t)
			require.NoError(t, s.Persist(rec), "valid Interrupted/cause pair must persist")
			loaded, err := NewLifecycleStore(s.Dir()).Load(rec.SessionID)
			require.NoError(t, err)
			bytes, err := json.Marshal(loaded)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(bytes, &fields))
			assert.Equal(t, reason, fields["interruption_reason"])
			changed := *loaded
			changed.State = LifecycleRunning
			assert.ErrorIs(t, s.Persist(&changed), ErrLifecycleTerminalImmutable, "a new run cannot rewrite this frozen generation")
			assert.Equal(t, loaded, func() *LifecycleRecord {
				r, e := NewLifecycleStore(s.Dir()).Load(rec.SessionID)
				require.NoError(t, e)
				return r
			}(), "rejected mutation leaves the exact outcome intact")
		})
	}
}

func TestCrashRecovery_InterruptionReasonCannotLeakToOtherOutcomes(t *testing.T) {
	for _, state := range []string{"running", "queued", "completed", "failed", "stopped"} {
		t.Run(state, func(t *testing.T) {
			s := newTestLifecycleStore(t)
			original := crashInterruptedRecord(t, "running", "")
			require.NoError(t, s.Persist(original))
			path := filepath.Join(s.Dir(), original.SessionID+".jsonl")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			bad := crashInterruptedRecord(t, state, "tool_limit")
			if state == "failed" {
				bad.FailedReason = "independent genuine failure"
			}
			// The design requires rejection, but does not prescribe an error string
			// or sentinel. Assert refusal plus zero durable mutation, not invented copy.
			assert.Error(t, s.Persist(bad), "reason belongs only to interrupted; a new non-interrupted record must not silently accept it")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "invalid state/reason pair must append nothing")
		})
	}
}

func TestCrashRecovery_InterruptedRequiresValidReason(t *testing.T) {
	for _, reason := range []string{"", "restart", "invented"} {
		t.Run("reason_"+reason, func(t *testing.T) {
			s := newTestLifecycleStore(t)
			rec := crashInterruptedRecord(t, "interrupted", reason)
			assert.Error(t, s.Persist(rec), "new durable Interrupted outcome requires tool_limit or reconciliation_required; restart is only the pre-existing Session display projection")
			_, err := s.Load(rec.SessionID)
			assert.ErrorIs(t, err, ErrLifecycleNotFound, "refused record never lands")
		})
	}
}
