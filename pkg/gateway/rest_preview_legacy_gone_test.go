//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_preview_legacy_gone_test.go is a regression test for the ADR-044 preview-on-main-listener
// production bug: registerPreviewEndpoints (rest.go) registers ONLY
// middleware.PreviewPathPrefix ("/preview/") on the main mux. The legacy
// /serve/ and /dev/ back-compat prefixes were retired on the backend, but the
// SPA (src/lib/preview-url.ts's PREVIEW_PATH_REGEX) still recognizes and
// renders/HEAD-probes them for historical transcript replay
// (IframePreview.tsx's warmup poll). Because no handler was registered for
// those two prefixes, an unmatched /serve/... or /dev/... request fell
// through to the "/" SPA catch-all (embed.go's newSPAHandler), which answers
// ANY unknown path with 200 + index.html — so the warmup probe misread a
// dead legacy link as "ready" instead of erroring. See
// tests/e2e/web-serve-canonical.spec.ts's legacy_run_in_workspace_warmup_replay.
//
// The fix registers /serve/ and /dev/ on the SAME mux to a dedicated 404
// responder (handleLegacyPreviewRetired), which — because the mux dispatches
// by longest matching subtree prefix — outranks the "/" catch-all for any
// path under those two prefixes.
//
// This file proves, on a REAL mux built by the actual production
// registerPreviewEndpoints call (not a hand-rolled stand-in), that:
//   - GET/HEAD /serve/<agent>/<token>/... now returns a genuine 404, not the
//     200 SPA shell.
//   - GET/HEAD /dev/<agent>/<token>/... likewise.
//   - /preview/<agent>/<token>/... is completely unaffected — still reaches
//     HandlePreview and serves real content.
//   - An unrelated SPA path (not under any preview prefix) is unaffected —
//     still hits the "/" catch-all.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spaShellBody is the sentinel body the fake "/" catch-all in this test
// returns, standing in for embed.go's real index.html. Assertions check for
// its ABSENCE on /serve/ and /dev/ responses (proving they no longer fall
// through to the catch-all) and its PRESENCE on the genuine SPA-route case
// (proving the catch-all itself is untouched by the fix).
const spaShellBody = "<html>SPA-INDEX-SHELL</html>"

// newWiredLegacyPreviewRealMux builds a real *http.ServeMux populated by the
// ACTUAL production registerPreviewEndpoints call, plus a stand-in "/"
// catch-all (mirrors embed.go's newSPAHandler: 200 + shell body for any
// unmatched path), then serves it from a real httptest.Server. This gives
// equivalent longest-prefix dispatch semantics to what production uses
// (dispatch by longest matching subtree prefix, the same rule
// pkg/channels/dynamic_mux.go's dynamicServeMux applies) — but note this
// helper wraps a plain stdlib *http.ServeMux, NOT the production
// dynamicServeMux type itself. dynamicServeMux is never instantiated here,
// so a bug specific to that struct's own implementation (as opposed to the
// registration/dispatch-order behavior it's meant to provide) would not be
// caught by this test.
func newWiredLegacyPreviewRealMux(t *testing.T) (*restAPI, *httptest.Server) {
	t.Helper()
	api, _ := newPreviewRouteTestAPI(t)
	api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(true)

	mainMux := http.NewServeMux()
	api.registerPreviewEndpoints(&testMuxRegistrar{mux: mainMux})

	// Stand-in for embed.go's SPA catch-all: 200 + shell body for ANY
	// unmatched path, exactly like newSPAHandler falling back to index.html.
	mainMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(spaShellBody))
	})

	srv := httptest.NewServer(mainMux)
	t.Cleanup(srv.Close)
	return api, srv
}

// TestLegacyPrefixes_Returns404NotSPAShell is the direct regression test: a
// GET to a /serve/<agent>/<token>/ or /dev/<agent>/<token>/ path (the shape
// the SPA's legacy transcript replay renders and HEAD-probes) must 404,
// never fall through to the 200 SPA shell. Table-driven over both retired
// prefixes, referencing the same legacyServePathPrefix/legacyDevPathPrefix
// constants rest.go's registerPreviewEndpoints registers, so a prefix rename
// fails here at the point of truth instead of silently going stale. Mirrors
// the loop shape of TestLegacyPreviewPrefixes_HEADAlso404s below.
func TestLegacyPrefixes_Returns404NotSPAShell(t *testing.T) {
	_, srv := newWiredLegacyPreviewRealMux(t)

	for _, prefix := range []string{legacyServePathPrefix, legacyDevPathPrefix} {
		path := prefix + "agent/token/"
		t.Run(strings.Trim(prefix, "/"), func(t *testing.T) {
			resp, err := http.Get(srv.URL + path)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			assert.Equal(t, http.StatusNotFound, resp.StatusCode,
				"GET %s must 404 — a retired legacy preview link must never resolve "+
					"to the SPA catch-all's 200", path)
			body := readBody(t, resp)
			assert.NotContains(t, body, spaShellBody,
				"body must NOT be the SPA shell — that would mean the request fell through to the '/' catch-all")
			assert.Contains(t, body, "retired",
				"defense-in-depth: the 404 body should carry handleLegacyPreviewRetired's "+
					"retired-prefix message, not just any 404")
		})
	}
}

// TestLegacyPreviewPrefixes_HEADAlso404s exercises the EXACT request method
// the SPA's warmup probe actually uses (IframePreview.tsx: fetch(target,
// {method:'HEAD'})). probeOnce only clears the warmup state to 'ready' when
// the status is in [200,400) — this proves that check now correctly fails
// for a dead legacy link on both prefixes, instead of seeing a false 200.
func TestLegacyPreviewPrefixes_HEADAlso404s(t *testing.T) {
	_, srv := newWiredLegacyPreviewRealMux(t)

	for _, path := range []string{"/serve/agent/token/", "/dev/agent/token/"} {
		req, err := http.NewRequest(http.MethodHead, srv.URL+path, nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode,
			"HEAD %s must 404 — this is the exact method/status check IframePreview.tsx's "+
				"warmup probeOnce uses to decide 'ready' vs. 'keep polling'", path)
	}
}

// TestPreviewPrefix_UnaffectedByLegacyPrefixFix is the differentiation
// control: on the SAME wired mux as the tests above, the CANONICAL /preview/
// prefix is completely unaffected — it still reaches HandlePreview and
// serves real registered content, proving the fix is scoped to /serve/ and
// /dev/ only.
func TestPreviewPrefix_UnaffectedByLegacyPrefixFix(t *testing.T) {
	api, srv := newWiredLegacyPreviewRealMux(t)

	workDir := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(workDir, "index.html"),
			[]byte("<h1>legacy-gone-test canonical preview</h1>"),
			0o644,
		),
	)

	// newPreviewRouteTestAPI wires a real ServedSubdirs onto the restAPI, but
	// this helper only returns the *restAPI — reach servedSubdirs via the
	// api handle returned above.
	token, _, err := api.servedSubdirs.Register("legacy-gone-agent", workDir, time.Hour)
	require.NoError(t, err)

	resp, err := http.Get(srv.URL + "/preview/legacy-gone-agent/" + token + "/index.html")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"canonical /preview/ must be completely unaffected by the /serve//dev/ 404 fix")
	assert.Contains(t, readBody(t, resp), "legacy-gone-test canonical preview")
}

// TestSPACatchAll_UnaffectedByLegacyPrefixFix is the second differentiation
// control: an unrelated path that is NOT under /preview/, /serve/, or /dev/
// still falls through to the "/" catch-all exactly as before — proving the
// fix did not broaden into a general change to catch-all/SPA routing
// (explicitly out of scope; embed.go is untouched).
func TestSPACatchAll_UnaffectedByLegacyPrefixFix(t *testing.T) {
	_, srv := newWiredLegacyPreviewRealMux(t)

	resp, err := http.Get(srv.URL + "/agents/some-real-spa-route")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"a genuine SPA client-side route (not under /preview//serve//dev/) must still hit the '/' "+
			"catch-all unchanged")
	assert.Contains(t, readBody(t, resp), spaShellBody,
		"body must be the SPA shell — proving the catch-all itself was not touched by this fix")
}
