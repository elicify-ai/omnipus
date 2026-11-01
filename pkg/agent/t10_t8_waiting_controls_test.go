//go:build goolm && stdjson

package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r17 18.29's P3 is a mandatory contrasting control: an unmet dependency
// with no scheduled root must still expire at the ordinary seven-day bound.
// This is preserve-behavior, never manufactured RED. The plan engine, task
// store, dependency state and expiry writer are real; only time/model I/O differ.
func TestT10_InheritedT8GenuineStallExpiresAtExactIdleBound(t *testing.T) {
	f := t10T8(t)
	ps := plan.New(filepath.Join(t.TempDir(), "plans"))
	p := newPlanGateTestPlan(t, ps, plan.StateRunning)
	f.al.taskExecutor.SetPlanStore(ps)
	pe := NewPlanEngine(f.al, ps, f.al.taskStore, f.al.taskExecutor)
	pe.clock = f.clk
	t.Cleanup(pe.Stop) // expiry may publish a real asynchronous owner wake
	outside := newCompletionContractTask(t, f.al, "native-agent", "unmet manual dependency outside plan")
	inbox := task.StatusInbox
	_, err := f.al.taskStore.Update(outside.ID, task.Patch{Status: &inbox})
	require.NoError(t, err)
	member := newCompletionContractTask(t, f.al, "native-agent", "genuinely stalled member")
	next, deps := task.StatusNext, []string{outside.ID}
	waiting, err := f.al.taskStore.Update(member.ID, task.Patch{Status: &next, BlockedBy: &deps, PlanID: &p.ID})
	require.NoError(t, err)
	require.Equal(t, task.StatusBlocked, waiting.Status, "store derives blocked from the unmet dependency")
	// A genuine recorded activity at fixture start; no tick refreshes it.
	anchor := f.clk.Now().Truncate(time.Second)
	stamp := anchor.Format(time.RFC3339)
	_, err = ps.Update(p.ID, plan.Patch{LastActivityAt: &stamp})
	require.NoError(t, err)
	bound := anchor.Add(7 * 24 * time.Hour)
	f.clk.Advance(bound.Add(-time.Millisecond).Sub(f.clk.Now()))
	pe.idleExpirySweep()
	before, err := ps.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.StateRunning, before.State, "one millisecond before the effective idle bound")
	assert.Equal(t, stamp, before.LastActivityAt)
	f.clk.Advance(time.Millisecond)
	pe.idleExpirySweep()
	after, err := ps.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.StateFailed, after.State, "exact idle bound: exemption may not over-match ordinary stalls")
	assert.Equal(t, plan.FailedReasonIdleExpired, after.FailedReason)
	assert.Contains(t, after.HandoverText, "idle-expired")
	pe.Stop() // join its real owner notification before asserting/tearing down
	for _, id := range []string{outside.ID, member.ID} {
		runs, err := f.al.taskStore.ListRuns(id)
		require.NoError(t, err)
		assert.Empty(t, runs, "no real task execution during a genuine stall")
	}
	waiting, err = f.al.taskStore.Get(member.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusBlocked, waiting.Status)
	assert.Empty(t, waiting.SessionID)
	f.clk.Advance(24 * time.Hour)
	pe.idleExpirySweep()
	again, err := ps.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, after, again, "terminal expiry remains stable well beyond the bound")
}
