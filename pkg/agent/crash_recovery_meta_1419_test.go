//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Issue 1419 explicitly permits documented separation. Session.yaml::status
// documents coarse conversation metadata (active includes stopped); its
// lifecycle_state is the run-state authority. Test the persisted bytes plus
// real admission, not an invented stopped value in the old metadata enum.
func crash1419AssertSeparated(t *testing.T, h *crashHarness, id string, want session.LifecycleDisplayState) {
	t.Helper()
	path := filepath.Join(h.al.GetSessionStore().BaseDir(), id, "meta.json")
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(bytes, &metadata))
	require.Contains(t, []any{"active", "archived", "failed", "interrupted"}, metadata["status"], "raw metadata uses the documented conversation-status enum")
	assert.NotEqual(t, "failed", metadata["status"], "Stop, limit and crash interruption are not a generic failure")
	// A valid but deliberately uninformative coarse value must never be read as
	// a running execution. This checks the consumer independently of mirroring.
	active := session.StatusActive
	require.NoError(t, h.al.GetSessionStore().SetMeta(id, session.MetaPatch{Status: &active}))
	before := h.record(t, id)
	assert.Equal(t, want, session.LifecycleRecordToDisplay(before, h.boot.Current()), "coarse active does not override the canonical ended lifecycle")
	_, err = NewSteerLauncher(h.al).Dispatch(context.Background(), id, before.Generation)
	assert.Error(t, err, "coarse active cannot authorize a new execution without an explicit continuation")
	after := h.record(t, id)
	assert.Equal(t, before.Generation, after.Generation)
	assert.Equal(t, before.ExecutionID, after.ExecutionID, "refused dispatch does not stamp a new run")
}

func TestCrashRecovery_1419_StoppedHelperMetaDoesNotMeanRunning(t *testing.T) {
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 8)}
	h := crashNew(t, p)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	rec, owner := crashChild(t, h, p)
	_, err := h.al.StopSession(context.Background(), StopRequest{SessionID: rec.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"}})
	require.NoError(t, err)
	crashAwaitDone(t, owner.done)
	got := h.record(t, rec.SessionID)
	require.Equal(t, session.LifecycleStopped, got.State)
	crash1419AssertSeparated(t, h, rec.SessionID, session.LifecycleDisplayStopped)
}

func TestCrashRecovery_1419_ToolLimitMetaDoesNotMeanRunning(t *testing.T) {
	h, rec := crashCeiling(t)
	got := h.record(t, rec.SessionID)
	require.Equal(t, session.LifecycleState("interrupted"), got.State, "Q14 must first persist that the execution ended")
	require.Equal(t, "tool_limit", crashFields(t, got)["interruption_reason"])
	crash1419AssertSeparated(t, h, rec.SessionID, session.LifecycleDisplayInterrupted)
}

func TestCrashRecovery_1419_CrashInterruptedMetaDoesNotMeanRunning(t *testing.T) {
	f := crashFileCut(t, "append_file")
	require.NoError(t, os.RemoveAll(filepath.Join(f.snapshot, "sessions", f.record.SessionID, "file_effects")))
	p := &crashScript{}
	f.h.restore(t, f.snapshot, p)
	f.h.recover(t)
	got := f.h.record(t, f.record.SessionID)
	require.Equal(t, session.LifecycleState("interrupted"), got.State, "uncertain crash effect must durably end with a reconciliation hold")
	require.Equal(t, "reconciliation_required", crashFields(t, got)["interruption_reason"])
	crash1419AssertSeparated(t, f.h, f.record.SessionID, session.LifecycleDisplayInterrupted)
}

var _ providers.LLMProvider = (*crashScript)(nil)
