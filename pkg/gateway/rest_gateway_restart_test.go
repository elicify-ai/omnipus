//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestHandleGatewayRestart_HappyPath drives the handler directly with a stubbed
// restarter so the test process is never re-execed. It asserts the 202 response
// shape (status/restart_id/drain_seconds) the SPA polls on, and that the stub
// restarter is invoked after the drain.
func TestHandleGatewayRestart_HappyPath(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	var mu sync.Mutex
	called := false
	done := make(chan struct{})
	api.restarter = func() {
		mu.Lock()
		called = true
		mu.Unlock()
		close(done)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/restart", nil)
	api.HandleGatewayRestart(w, r)

	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())

	var resp gen.GatewayRestartResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), "decode: %s", w.Body.String())
	assert.Equal(t, gen.Restarting, resp.Status)
	assert.NotEmpty(t, resp.RestartId)
	assert.Equal(t, selfRestartDrainSeconds, resp.DrainSeconds)
	require.NotNil(t, resp.Message)
	assert.NotEmpty(t, *resp.Message)

	// The restarter runs asynchronously after the drain sleep. Wait for it.
	select {
	case <-done:
	case <-time.After(time.Duration(selfRestartDrainSeconds+5) * time.Second):
		t.Fatal("stub restarter was not invoked within the drain window")
	}
	mu.Lock()
	assert.True(t, called, "the restarter indirection must be invoked")
	mu.Unlock()
}

// TestHandleGatewayRestart_RejectsNonPost verifies non-POST methods are rejected
// 405 and never trigger a restart.
func TestHandleGatewayRestart_RejectsNonPost(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.restarter = func() { t.Fatal("restarter must not run for a non-POST method") }

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/v1/gateway/restart", nil)
		api.HandleGatewayRestart(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "method %s", method)
	}
	// Give any (erroneously) scheduled goroutine a moment — it must not fire.
	time.Sleep(50 * time.Millisecond)
}
