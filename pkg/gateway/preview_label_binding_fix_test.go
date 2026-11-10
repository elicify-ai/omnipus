// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — capability-binding tests for ADR-094 Mode 1 labels
// (review finding F1, fix A; spec docs/internal/specs/adr-094-preview-isolation-spec.md
// FR-008 and S-2.4: "the label MUST map to the same registry entry as the
// Mode 2 token").
//
// Test plan (elicify-test-writing):
//
//   - Behaviour under test: a Mode 1 label serves ONLY the registration whose
//     token it was derived from. A static label never reaches a dev server
//     registered by the same agent, even while both registrations are live.
//   - Oracle: FR-008/S-2.4 — label URL and token URL name one registration, so
//     each returns exactly that registration's bytes. Expected bytes are the
//     fixture's own distinct contents, never observed output.
//   - Unit boundary: the REAL production chain around a real mux with the
//     preview endpoints, a REAL static registry (agent.ServedSubdirs), a REAL
//     dev registry (sandbox.DevServerRegistry) and a real httptest upstream
//     standing in for the dev server.
//   - Cases: concurrent static + dev for one agent (each label its own bytes),
//     token/label parity for both registrations, revocation (static rotation,
//     dev unregister), static expiry with a live dev server. Dev-registration
//     expiry of LookupByLabel is pinned in
//     pkg/sandbox/dev_servers_label_expiry_test.go (the gateway package cannot
//     backdate a dev entry).
//   - Mutation: restoring the by-agent substitution in
//     preview_host_dispatch.go::servePreviewByLabel turns the static-label
//     rows red (the upstream is reached and the dev bytes are returned).

package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

const (
	lbAgent      = "lb-binding-agent"
	lbStaticBody = "<!doctype html><p>STATIC-REGISTRATION-S</p>"
	lbStatic2    = "<!doctype html><p>STATIC-REGISTRATION-S2</p>"
	lbDevBody    = "DEV-REGISTRATION-D"
)

type lbHarness struct {
	api      *restAPI
	srv      *httptest.Server
	port     string
	devToken string
	devHits  atomic.Int32
}

func lbNewHarness(t *testing.T) *lbHarness {
	t.Helper()
	_ = os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	api, _ := newPreviewRouteTestAPI(t)
	h := &lbHarness{api: api}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h.devHits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, lbDevBody)
	}))
	t.Cleanup(upstream.Close)

	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	api.devServers = reg
	devEntry, err := reg.Register(lbAgent, upstreamPort(t, upstream.URL), 0, "npm run dev", 10)
	require.NoError(t, err)
	h.devToken = devEntry.Token

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})
	h.srv = httptest.NewServer(buildProductionMiddlewareChain(api, mainMux))
	t.Cleanup(h.srv.Close)
	h.port = fmt.Sprintf("%d", upstreamPort(t, h.srv.URL))
	api.agentLoop.GetConfig().Gateway.PublicURL = "http://localhost:" + h.port
	api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(true)
	return h
}

// lbRegisterStatic registers a static directory holding index.html=body for
// the harness agent, with the given lifetime, and returns its token.
func (h *lbHarness) lbRegisterStatic(t *testing.T, body string, d time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0o644))
	tok, _, err := h.api.servedSubdirs.Register(lbAgent, dir, d)
	require.NoError(t, err)
	return tok
}

// lbGetLabel issues a Mode 1 GET under the label Host of token.
func (h *lbHarness) lbGetLabel(t *testing.T, token, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+path, nil)
	require.NoError(t, err)
	req.Host = middleware.PreviewLabelForToken(token) + ".localhost:" + h.port
	return lbDo(t, h.srv.Client(), req)
}

// lbGetToken issues a Mode 2 GET under /preview/{agent}/{token}/.
func (h *lbHarness) lbGetToken(t *testing.T, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/preview/"+lbAgent+"/"+token+"/", nil)
	require.NoError(t, err)
	return lbDo(t, h.srv.Client(), req)
}

func lbDo(t *testing.T, c *http.Client, req *http.Request) (int, string) {
	t.Helper()
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(b)
}

// TestPreviewLabelBinding_ConcurrentStaticAndDev: one agent holds a live
// static registration S (token T1) and a live dev registration D (token T2)
// at the same time. Each label serves exactly its own registration.
func TestPreviewLabelBinding_ConcurrentStaticAndDev(t *testing.T) {
	h := lbNewHarness(t)
	staticTok := h.lbRegisterStatic(t, lbStaticBody, time.Hour)
	require.NotEqual(t, staticTok, h.devToken, "fixture: the two registrations carry distinct tokens")
	require.NotEqual(t, middleware.PreviewLabelForToken(staticTok), middleware.PreviewLabelForToken(h.devToken),
		"fixture: the two registrations carry distinct labels")

	t.Run("static_label_serves_static_bytes_only", func(t *testing.T) {
		h.devHits.Store(0)
		status, body := h.lbGetLabel(t, staticTok, "/")
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, lbStaticBody, body,
			"FR-008: the static label must serve the static registration, not the agent's dev server")
		assert.Equal(t, int32(0), h.devHits.Load(),
			"FR-008: a static label must never reach the dev upstream")
	})

	t.Run("static_label_subpath_never_reaches_dev", func(t *testing.T) {
		h.devHits.Store(0)
		status, body := h.lbGetLabel(t, staticTok, "/app.js")
		assert.Equal(t, http.StatusNotFound, status,
			"a path absent from the static directory is a 404 of the static registration")
		assert.NotContains(t, body, lbDevBody)
		assert.Equal(t, int32(0), h.devHits.Load(),
			"FR-008: a static label must never reach the dev upstream")
	})

	t.Run("dev_label_serves_dev_bytes", func(t *testing.T) {
		h.devHits.Store(0)
		status, body := h.lbGetLabel(t, h.devToken, "/")
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, lbDevBody, body)
		assert.Equal(t, int32(1), h.devHits.Load())
	})
}

// TestPreviewLabelBinding_TokenLabelParity: S-2.4 — the token URL and the
// label URL of one registration return the same bytes, for both kinds.
func TestPreviewLabelBinding_TokenLabelParity(t *testing.T) {
	h := lbNewHarness(t)
	staticTok := h.lbRegisterStatic(t, lbStaticBody, time.Hour)

	for _, tc := range []struct {
		name, token, want string
	}{
		{"static", staticTok, lbStaticBody},
		{"dev", h.devToken, lbDevBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokStatus, tokBody := h.lbGetToken(t, tc.token)
			lblStatus, lblBody := h.lbGetLabel(t, tc.token, "/")
			require.Equal(t, http.StatusOK, tokStatus)
			require.Equal(t, http.StatusOK, lblStatus)
			assert.Equal(t, tc.want, tokBody, "the token URL serves its registration")
			assert.Equal(t, tc.want, lblBody,
				"S-2.4: the label URL serves the SAME registration as the token URL")
		})
	}
}

// TestPreviewLabelBinding_Revocation: revoking one registration retires its
// label without redirecting it to the other registration, and leaves the
// other registration's label intact.
func TestPreviewLabelBinding_Revocation(t *testing.T) {
	h := lbNewHarness(t)
	oldStatic := h.lbRegisterStatic(t, lbStaticBody, time.Hour)

	// A different directory rotates the static token (served_subdirs.Register).
	newStatic := h.lbRegisterStatic(t, lbStatic2, time.Hour)
	require.NotEqual(t, oldStatic, newStatic, "fixture: a different directory rotates the token")

	t.Run("rotated_static_label_404_not_dev", func(t *testing.T) {
		h.devHits.Store(0)
		status, body := h.lbGetLabel(t, oldStatic, "/")
		assert.Equal(t, http.StatusNotFound, status, "a revoked static label is gone")
		assert.NotContains(t, body, lbDevBody)
		assert.Equal(t, int32(0), h.devHits.Load())
	})

	t.Run("new_static_label_serves_new_static", func(t *testing.T) {
		h.devHits.Store(0)
		status, body := h.lbGetLabel(t, newStatic, "/")
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, lbStatic2, body)
		assert.Equal(t, int32(0), h.devHits.Load())
	})

	t.Run("dev_unregister_retires_dev_label_only", func(t *testing.T) {
		require.True(t, h.api.devServers.Unregister(h.devToken))
		status, _ := h.lbGetLabel(t, h.devToken, "/")
		assert.Equal(t, http.StatusNotFound, status, "an unregistered dev label is gone")
		status, body := h.lbGetLabel(t, newStatic, "/")
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, lbStatic2, body, "the static label is unaffected by the dev revocation")
	})
}

// TestPreviewLabelBinding_StaticExpiry: an expired static registration's
// label is a 404 even while the agent's dev server is live (pin: it must not
// fall through to the dev server).
func TestPreviewLabelBinding_StaticExpiry(t *testing.T) {
	h := lbNewHarness(t)
	staticTok := h.lbRegisterStatic(t, lbStaticBody, 100*time.Millisecond)
	time.Sleep(300 * time.Millisecond)

	h.devHits.Store(0)
	status, body := h.lbGetLabel(t, staticTok, "/")
	assert.Equal(t, http.StatusNotFound, status, "an expired static label is gone")
	assert.NotContains(t, body, lbDevBody)
	assert.Equal(t, int32(0), h.devHits.Load(), "an expired static label never reaches the dev upstream")
}
