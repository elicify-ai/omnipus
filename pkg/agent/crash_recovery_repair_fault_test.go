//go:build goolm && stdjson

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Fault only the durable filesystem append boundary; retain the real store's
// archive reads, snapshot, address mapping and projection. Recovery, assembly,
// validation and the HTTP provider adapter are never replaced.
type crashRecoveryWriteFault struct {
	*session.UnifiedStore
	attempts int
	failure  error
}

func (s *crashRecoveryWriteFault) AddMessage(string, string, string) error {
	s.attempts++
	return s.failure
}
func (s *crashRecoveryWriteFault) AppendModelMessage(context.Context, string, session.ModelAppend) (session.ModelSlot, session.WindowView, error) {
	s.attempts++
	return session.ModelSlot{}, session.WindowView{}, s.failure
}

func TestCrashRecovery_RepairWriteFailureBlocksProviderInsteadOfHidingBatch(t *testing.T) {
	h := cwR1New(t, 100000)
	h.append(t, providers.Message{Role: "user", Content: "retain A; repair only B"}, orphanACGroup("batch", "repair-a", "repair-b"), orphanACResult("repair-a", "known successful A"))
	before := orphanACArchiveBytes(t, h)
	fault := &crashRecoveryWriteFault{UnifiedStore: h.store, failure: errors.New("fixture: recovery append storage unavailable")}
	cleaned := RecoverOrphanedToolCalls(fault, h.key, nil)
	assert.Greater(t, fault.attempts, 0, "instrument: the actual recovery attempted a durable append and received the fault")
	h.agent.Sessions = fault
	ts := h.turn("")
	assembled := h.al.assembleMessages(context.Background(), ts, cleaned, "", nil, nil)
	recorder, provider := cwR1OpenAI(t, 0)
	flow := cwR1Flow(h, ts, assembled, provider)
	flow.rq.prepareCallMessages()
	_, err := flow.rq.ri.rf.rt.callProviderOnce(flow.rq.ri.rf.callMessages, nil)
	assert.Error(t, err, "an uncommitted repair cannot be sanitized into a valid provider request")
	require.Len(t, recorder.requests(t), 0, "provider sees zero requests after failed repair/proof persistence")
	assert.Equal(t, before, orphanACArchiveBytes(t, h), "failed correction neither rewrites known A nor invents B")
}

func TestCrashRecovery_ConcurrentChangeDuringHashNeedsReadBarrier(t *testing.T) {
	crashRequireFileProof(t)
	t.Fatal("BLOCKED: deterministic mid-hash/post-read-stat observation seam is not exposed by the file reconciler — required by DESIGN Q15 unstable/concurrent readback control; competing unresolved writers and changed anchor/symlink controls are separately executable")
}
