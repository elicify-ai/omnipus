package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CONTRACT-SHAPE-DECISION D / DESIGN Decision 1: the producer knows whether a
// lifecycle is newly recorded. Exercise exported Persist, never its validator.
func producerRelationRecord(state LifecycleState, reason InterruptionReason) *LifecycleRecord {
	return &LifecycleRecord{
		SessionID: "producer-relation", Generation: 1, State: state,
		OwnerScopeKind: OwnerScopeHuman, WorkspaceID: "ws", AgentID: "mia",
		ExecutionID:        &ExecutionIdentity{RunID: "producer-execution", BootSeq: 1},
		InterruptionReason: reason,
	}
}

func TestCrashRecovery_ProducerPersistRejectsInterruptedWithoutReason(t *testing.T) {
	for _, validReason := range []InterruptionReason{InterruptionToolLimit, InterruptionReconciliationRequired} {
		t.Run(string(validReason), func(t *testing.T) {
			control := NewLifecycleStore(t.TempDir())
			valid := producerRelationRecord(LifecycleInterrupted, validReason)
			require.NoError(t, control.Persist(valid), "instrument: the otherwise-identical Interrupted record with a valid reason is writable")
			reloaded, err := NewLifecycleStore(control.Dir()).Load(valid.SessionID)
			require.NoError(t, err)
			require.Equal(t, validReason, reloaded.InterruptionReason)

			store := NewLifecycleStore(t.TempDir())
			prior := producerRelationRecord(LifecycleRunning, "")
			require.NoError(t, store.Persist(prior))
			path := filepath.Join(store.Dir(), prior.SessionID+".jsonl")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			invalid := *prior
			invalid.State = LifecycleInterrupted
			// The tail is Running, so terminal immutability cannot fake rejection.
			err = store.Persist(&invalid)
			assert.Error(t, err, "newly recorded Interrupted without a reason must fail the public persist path")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "rejection appends no invalid wire-producing record")
			actual, err := NewLifecycleStore(store.Dir()).Load(prior.SessionID)
			require.NoError(t, err)
			assert.Equal(t, prior, actual, "the valid prior lifecycle remains authoritative")
		})
	}
}

func TestCrashRecovery_ProducerPersistRejectsRunningWithInterruptionReason(t *testing.T) {
	for _, invalidReason := range []InterruptionReason{InterruptionToolLimit, InterruptionReconciliationRequired} {
		t.Run(string(invalidReason), func(t *testing.T) {
			store := NewLifecycleStore(t.TempDir())
			prior := producerRelationRecord(LifecycleRunning, "")
			require.NoError(t, store.Persist(prior), "instrument: Running is valid when no interruption reason is supplied")
			path := filepath.Join(store.Dir(), prior.SessionID+".jsonl")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			invalid := *prior
			invalid.InterruptionReason = invalidReason
			err = store.Persist(&invalid)
			assert.Error(t, err, "Running with an interruption reason must fail the public persist path")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "the producer must not save or silently drop the forbidden reason")
			actual, err := NewLifecycleStore(store.Dir()).Load(prior.SessionID)
			require.NoError(t, err)
			assert.Equal(t, prior, actual)
		})
	}
}
