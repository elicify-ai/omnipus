package gateway

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/stretchr/testify/require"
)

func (h *historyUS16Harness) awaitTurnEnd(t *testing.T, turnID string) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-h.events.C:
			require.True(t, ok, "real event subscription closed before engine completion")
			h.observedEvents = append(h.observedEvents, event)
			if event.Kind != agent.EventKindTurnEnd || event.Meta.AgentID != "mia" {
				continue
			}
			payload, valid := event.Payload.(agent.TurnEndPayload)
			require.True(t, valid)
			require.Equal(t, h.sid, payload.SessionID)
			require.Equal(t, turnID, event.Meta.TurnID)
			return
		case <-deadline.C:
			t.Fatal("US1.6 actual Stop did not end its real engine turn; harness cancellation cannot prove this")
		}
	}
}
