// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// session-core FR-009 / DEL-03 keeps ONE waiting-item bound (MaxQueueSize) for a
// session worker's inbox. The bound is enforced on enqueue, not by
// preallocating a MaxQueueSize-slot channel for every worker: an idle worker
// must not cost MaxQueueSize message slots (TestCompactionBoundsMemory, 10 MB).

func newInboxTestWorker(t *testing.T) *sessionWorker {
	t.Helper()
	al, _ := newConcurrentTestAgentLoop(t)
	t.Cleanup(func() { al.Close() })
	const scope = "agent:default:session:inbox-bound-test"
	w := newSessionWorker(scope, al, func() {})
	al.sessionWorkers.Store(scope, w)
	t.Cleanup(func() { al.sessionWorkers.Delete(scope) })
	return w
}

func TestSessionWorker_Inbox_DoesNotPreallocateTheBound(t *testing.T) {
	w := newInboxTestWorker(t)
	require.Less(t, cap(w.inbox), MaxQueueSize/4,
		"the inbox channel must stay small; the MaxQueueSize bound is counted on enqueue, not allocated up front")
}

func TestSessionWorker_Inbox_BoundIsExactlyMaxQueueSize(t *testing.T) {
	w := newInboxTestWorker(t)
	for i := 0; i < MaxQueueSize; i++ {
		require.True(t, w.enqueue(bus.InboundMessage{Channel: "system", ChatID: "webchat:c", Content: fmt.Sprintf("m%d", i)}),
			"message %d of %d must be accepted", i+1, MaxQueueSize)
	}
	// One past the bound: a system message is reported as not delivered (the
	// unchanged overflow behaviour), and nothing already queued is displaced.
	require.False(t, w.enqueue(bus.InboundMessage{Channel: "system", ChatID: "webchat:c", Content: "overflow"}),
		"message MaxQueueSize+1 must be refused")
	require.Equal(t, MaxQueueSize, w.waitingInboxCount())
}

func TestSessionWorker_Inbox_FIFOAcrossTheSmallBuffer(t *testing.T) {
	w := newInboxTestWorker(t)
	const n = 60 // well past the channel buffer, so the overflow tier is used
	for i := 0; i < n; i++ {
		require.True(t, w.enqueue(bus.InboundMessage{Channel: "system", ChatID: "webchat:c", Content: fmt.Sprintf("m%d", i)}))
	}
	for i := 0; i < n; i++ {
		msg := <-w.inbox
		w.refillInbox()
		require.Equal(t, fmt.Sprintf("m%d", i), msg.Content, "messages must come out in the order they went in")
	}
	require.Equal(t, 0, w.waitingInboxCount())
}
