// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// WP-C RED tests for the SSE no-thinking ban (spec §16 item 12, §6, FR-025,
// ADR-095 D2 Boundary 4: "the streamer must not implement the web add-on" —
// POST /api/v1/chat is a no-thinking surface by construction).
//
// Oracle: the SSE chat stream's event vocabulary is closed at
// {token, done, error} — a thinking event must never appear, and no thinking
// text may ride the stream body.
//
// RED status: ban/regression guard — expected GREEN in RED. The ban already
// holds today (sse.go's only event writers are token/done/error); the test
// pins non-implementation so WP-D's frontend work and any future "add
// thinking to SSE" idea cannot silently cross the boundary.
//
// Harness: drives the REAL SSEHandler with tokens flowing through the real
// bus.Streamer (GetStreamer), mirroring how the bus stream delegate reaches
// the handler in production.

const wpcSSEThinkingSentinel = "sk-live-abcd1234EFGH" // recognised credential shape

// TestSSEChatStream_NoThinkingEventEver drives the REAL SSE handler end to
// end: a POST that commits headers, tokens pushed through the real
// bus.Streamer (as the bus stream delegate reaches it in production), and a
// Finalize that closes the stream. It then asserts the closed event
// vocabulary and the absence of thinking text on the wire.
func TestSSEChatStream_NoThinkingEventEver(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	msgBus := bus.NewMessageBus()
	h := newSSEHandler(msgBus, nil, "http://localhost:3000", testConfig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"message":"wpc sse ban probe"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	// Dev mode requires "Bearer " prefix; token value is ignored when
	// OMNIPUS_BEARER_TOKEN is unset (sse_test.go's auth pattern).
	req.Header.Set("Authorization", "Bearer dev-token")

	w := newCaptureRecorder()

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		h.ServeHTTP(w, req)
	}()

	select {
	case <-w.headerWritten:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("SSE handler did not commit response headers within 2 seconds")
	}

	// Drive the stream the way the bus stream delegate would: snapshot the
	// handler's live session IDs under the lock, then resolve the streamer
	// OUTSIDE the lock (GetStreamer takes h.mu itself — calling it under our
	// own h.mu.Lock() self-deadlocks).
	var ids []string
	h.mu.Lock()
	for id := range h.sessions {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	require.Len(t, ids, 1, "instrument check: exactly one live SSE session expected")
	streamer, ok := h.GetStreamer(ctx, "webchat", ids[0], "")
	require.True(t, ok, "instrument check: the handler must expose a streamer for the live session — otherwise nothing flowed and the ban proves nothing")

	require.NoError(t, streamer.Update(ctx, "plain answer"))
	require.NoError(t, streamer.Update(ctx, " more"))
	require.NoError(t, streamer.Finalize(ctx, ""))

	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("SSE handler did not finish after Finalize")
	}

	// Closed event vocabulary: token/done/error are the ONLY event types this
	// surface may ever emit (sse.go's writeSSEEvent call sites). A thinking
	// event must never exist here (§16 item 12; ADR-095 D2 Boundary 4).
	eventTypes := map[string]bool{}
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "event: ") {
			eventTypes[strings.TrimSpace(strings.TrimPrefix(line, "event: "))] = true
		}
	}
	assert.True(t, eventTypes["token"],
		"instrument check: the stream really carried tokens — an empty body would make this ban vacuous")
	assert.Equal(t, map[string]bool{"token": true, "done": true}, eventTypes,
		"the SSE event vocabulary is closed at {token, done}: no thinking event may ever appear (§16 item 12, ADR-095 D2 Boundary 4)")

	// Wire-bytes ban: no thinking-text sentinel anywhere in the stream body,
	// for toggle-on or toggle-off logins alike (the surface has no login at
	// all — that is the point of the prohibition).
	assert.NotContains(t, w.Body.String(), wpcSSEThinkingSentinel,
		"no thinking text may ever ride the legacy SSE chat stream (FR-025)")
}
