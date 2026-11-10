package gateway

import (
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// The streaming done frame is emitted inside runAgentLoop; room cost reports
// follow its return. Ordinary-root lifecycle settlement is the later, durable
// boundary (execution_disposition.go::settleOrdinaryRoot). Wait for that boundary,
// never for an allocation value chosen to make the monetary assertion pass.
func (h *wu15RoomHarness) waitMainSettled(id string) {
	h.t.Helper()
	sid := h.main(id)
	scope := "agent:" + id + ":session:" + sid
	lifecycle := h.al.GetSessionLifecycleStore()
	require.NotNil(h.t, lifecycle)
	var lastErr error
	var state session.LifecycleState
	require.Eventually(h.t, func() bool {
		record, err := lifecycle.Load(sid)
		lastErr = err
		if err != nil {
			return false
		}
		state = record.State
		return record.Terminal() && h.al.GetActiveTurnBySession(scope) == nil
	}, 30*time.Second, 10*time.Millisecond, "SETUP: the producing main turn must durably settle before its final cost is read")
	require.NoError(h.t, lastErr)
	require.Equal(h.t, session.LifecycleCompleted, state, "SETUP: the measured producing turn completed successfully")
}
