// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — wave-2 PTA-5 pin (fix round 5): a Mode 1 static label
// host refuses non-GET/HEAD methods with 405.
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: pkg/gateway/preview_host_dispatch.go::
//     servePreviewByLabel answers 405 for a POST/PUT/DELETE dispatched to a
//     label Host backed by a STATIC registration.
//   - Specification source: the static-serving method restriction mirrored
//     from HandlePreview's Mode 2 static branch (rest_preview.go — "method
//     not allowed" 405 for non-GET/HEAD) as directed by the wave-2 PTA-5
//     dispatch; the Mode 1 static branch is the same property on the label
//     host. Status-value oracle (405, exactly), not merely "refused".
//   - Unit boundary: REAL chain via piRedNewGuardHarness + a REAL static
//     mint through the serve_web tool. The mint runs under a DEDICATED agent
//     id ("fix5-pin-agent") that has NO dev registration: servePreviewByLabel
//     proxies when the label's agent has a live dev server (the dev
//     preference), and only the static sub-branch carries the 405 — the
//     harness's own agent (piRedGuardAgent) is dev-registered, so the pin
//     must not mint under it.
//   - Case table:
//     GET /                → 200 with the served file body (positive control:
//     proves the 405 is method-conditional, not a
//     broken route)
//     POST /               → 405 (the pin)
//     PUT /                → 405 (the pin)
//     DELETE /             → 405 (the pin)
//   - Mutation (run in CHECK): delete the 405 branch in servePreviewByLabel →
//     the POST falls through to serveStaticFile, which does NO method check
//     and serves the file with 200 → every 405 row dies.
//   - Known gaps: HEAD is not driven (it is in the allowed set and shares
//     GET's branch); OPTIONS on a label host is not pinned here (the spec
//     leaves the Mode 1 preflight story to the ADR, not this dispatch).
package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// fix5PinAgent is the pin's dedicated static-mint agent id. It must NOT be
// piRedGuardAgent: that agent carries the harness's dev registration, and
// servePreviewByLabel's dev preference would proxy the request instead of
// reaching the static sub-branch whose 405 this pin asserts.
const fix5PinAgent = "fix5-pin-agent"

// fix5MintStaticLabel mints a Mode 1 STATIC label through the serve_web tool
// under fix5PinAgent (modeled on piRedMintGuardLabel, parameterized agent).
func fix5MintStaticLabel(t *testing.T, h *piRedGuardHarness) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "index.html"), []byte("x"), 0o644))
	tool := tools.NewWebServeTool(
		dir, fix5PinAgent,
		func() *config.Config { return h.api.agentLoop.GetConfig() },
		h.api.servedSubdirs, nil,
		tools.WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
		nil, nil, 60, 86400)
	result := tool.Execute(tools.WithAgentID(context.Background(), fix5PinAgent),
		map[string]any{"path": "."})
	require.False(t, result.IsError, "web_serve must succeed: %s", result.ForLLM)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed))
	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"fixture binding: no isolated_url mint — the Mode 1 surface must be live for this pin")
	labelURL, ok := isoRaw.(string)
	require.True(t, ok, "isolated_url must be a string")
	u := strings.TrimPrefix(labelURL, "http://")
	u = strings.TrimSuffix(u, "/")
	parts := strings.SplitN(u, ".", 2)
	require.Len(t, parts, 2, "isolated_url must be <label>.localhost[:port], got %q", labelURL)
	return parts[0]
}

// TestFix5Mode1LabelHost_MethodNotAllowedPin pins the Mode 1 static label
// host's method restriction: non-GET/HEAD → 405, exactly.
func TestFix5Mode1LabelHost_MethodNotAllowedPin(t *testing.T) {
	h := piRedNewGuardHarness(t)
	label := fix5MintStaticLabel(t, h)
	host := label + ".localhost:" + h.port

	t.Run("get_serves_control", func(t *testing.T) {
		resp := h.piRedGuardGet(t, host, "/", http.MethodGet, nil)
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"fixture binding: a GET on the static label host must serve — proves the 405s below are method-conditional")
		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		assert.Equal(t, "x", string(body),
			"fixture binding: the served body is the minted index.html content")
	})

	// The pin: every state-changing method is refused with exactly 405 on the
	// static label host.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run("label_host_"+strings.ToLower(method)+"_405", func(t *testing.T) {
			resp := h.piRedGuardGet(t, host, "/", method, nil)
			t.Cleanup(func() { _ = resp.Body.Close() })
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode,
				"PTA-5: a %s dispatched to a Mode 1 static label host must answer exactly 405 — "+
					"the static label branch serves reads only", method)
		})
	}
}
