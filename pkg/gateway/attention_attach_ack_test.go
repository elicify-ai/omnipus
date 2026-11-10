// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Oracle: architect U11 Q2 (ack seam) — only an acknowledging attach (ack true
// AND a number) of a main writes the seen mark, to max(seen, min(n, outcome));
// ack alone, a number alone, a plain attach and a stale number write nothing
// (BDD-13.2/13.3). Real: handleAttachSessionWithAck against the real store.
func TestSessionCoreU11_AttachAckWritesSeenMarkOnlyWhenAcknowledging(t *testing.T) {
	handler, store, mainID := u11WSFixture(t)
	u11AppendGoalOutcome(t, store, mainID, "goal-ack-1", generated.GoalOutcomeEndingMet)
	meta, err := store.GetMeta(mainID)
	require.NoError(t, err)
	bound := meta.Attention.OutcomeOrder
	require.Positive(t, bound, "fixture: the outcome raised the saved order")

	attach := func(ack *attachAck) {
		wc := &wsConn{sendCh: make(chan []byte, 2048), doneCh: make(chan struct{})}
		handler.handleAttachSessionWithAck(context.Background(), "chat-ack", mainID, nil, ack, wc)
	}
	seen := func() int64 {
		m, gerr := store.GetMeta(mainID)
		require.NoError(t, gerr)
		return m.Attention.SeenOrder
	}

	attach(nil)
	require.Zero(t, seen(), "a plain attach writes nothing")
	attach(&attachAck{Ack: true})
	require.Zero(t, seen(), "ack_attention without a number writes nothing")
	attach(&attachAck{Bound: &bound})
	require.Zero(t, seen(), "a number without ack_attention writes nothing")

	// A second outcome is saved after the client was shown `bound`.
	u11AppendGoalOutcome(t, store, mainID, "goal-ack-2", generated.GoalOutcomeEndingRoundsExhausted)
	attach(&attachAck{Ack: true, Bound: &bound})
	require.Equal(t, bound, seen(), "the ack advances the mark to the number the client saw")
	m, err := store.GetMeta(mainID)
	require.NoError(t, err)
	require.Greater(t, m.Attention.OutcomeOrder, m.Attention.SeenOrder, "the later outcome stays unseen")

	stale := bound - 1
	attach(&attachAck{Ack: true, Bound: &stale})
	require.Equal(t, bound, seen(), "a stale number cannot lower the mark")
}
