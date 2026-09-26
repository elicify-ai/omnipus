// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — RED tests for ADR-094 preview isolation, orders 17 and
// 19 (FR-029, FR-027, S-1.4, S-7.2; spec:
// docs/internal/specs/adr-094-preview-isolation-spec.md).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: a minted label follows its token's lifecycle —
//     same-directory static re-serve renews in place (same label), a
//     different-directory re-serve mints a new label and retires the old
//     (404), every token-revocation path (static Evict, dev
//     UnregisterByAgent, preview_enabled hot-flip) retires the label (404),
//     and the per-label rate limit throttles one label without touching
//     other labels or the main host.
//   - Specification source: S-1.4, FR-029, S-7.2, FR-027 verbatim.
//   - Unit boundary: real stores (h.api.servedSubdirs for static,
//     sandbox.NewDevServerRegistry for dev), real chain via
//     piRedNewPlantedHarness, real mints through the serve_web tool.
//   - RED shape: the label does not exist pre-GREEN — every row REDs at the
//     missing isolated_url mint (dev rows additionally at the tier3 Linux
//     gate on darwin). The rate-limit row additionally REDs at "no 429 ever
//     appears" once the mint lands. Isolation rows are pins post-mint.
//   - Known gaps (documented): rotation at maxTokenLifetime (a 24h
//     package-private const in pkg/agent/served_subdirs.go — no clock seam)
//     and janitor expiry (time-based) are not drivable without a clock seam;
//     they land with GREEN's store-side unit tests if the seam exists.
//   - Spec-vs-code discrepancy (reported, not resolved): FR-027's parenthetical
//     says the token-bucket "sits only on the /preview/ prefix" today;
//     rest.go::registerPreviewEndpoints registers HandlePreview bare — there
//     is no preview limiter anywhere in pkg/gateway today. The limit VALUE is
//     therefore GREEN's choice (mechanism freedom); this test hammers a fixed
//     modest burst and asserts only that a per-label limit EXISTS and is
//     label-isolated. If GREEN's limit exceeds the burst, the burst constant
//     is calibrated post-GREEN (fixture calibration, not a weakening).

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// piRedLifeMint mints a Mode 1 label for (agentID, dir) through the
// serve_web tool (static mode) and returns the label.
func piRedLifeMint(t *testing.T, h *piRedPlantedHarness, agentID, dir string) string {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "index.html"), []byte("x"), 0o644))
	tool := tools.NewWebServeTool(
		dir, agentID,
		func() *config.Config { return h.api.agentLoop.GetConfig() },
		h.api.servedSubdirs, nil,
		tools.WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
		nil, nil, 60, 86400)
	result := tool.Execute(tools.WithAgentID(context.Background(), agentID),
		map[string]any{"path": "."})
	require.False(t, result.IsError, "web_serve must succeed: %s", result.ForLLM)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed))
	isoRaw, has := parsed["isolated_url"]
	require.True(t, has,
		"RED (FR-001/FR-022): no isolated_url mint exists yet — the label lifecycle cannot "+
			"be exercised pre-GREEN")
	labelURL := isoRaw.(string)
	host := strings.TrimPrefix(labelURL, "http://")
	host = strings.TrimSuffix(host, "/")
	parts := strings.SplitN(host, ".", 2)
	require.Len(t, parts, 2, "isolated_url must be <label>.localhost[:port], got %q", labelURL)
	return parts[0]
}

// piRedLifeLabelGet issues a GET / under the given label Host without
// following redirects and returns the client-visible status.
func piRedLifeLabelGet(t *testing.T, h *piRedPlantedHarness, label string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/", nil)
	require.NoError(t, err)
	req.Host = label + ".localhost:" + h.port
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp.StatusCode
}

// ---------------------------------------------------------------------------
// order 17 — label lifecycle (S-1.4, FR-029)
// ---------------------------------------------------------------------------

// TestPreviewLabelLifecycle: every S-1.4 lifecycle clause. All rows RED at
// the missing mint pre-GREEN; the 404-on-revocation rows additionally carry
// their post-GREEN assertion.
func TestPreviewLabelLifecycle(t *testing.T) {
	h := piRedNewPlantedHarness(t)

	t.Run("renew_in_place_same_dir", func(t *testing.T) {
		dir := t.TempDir()
		first := piRedLifeMint(t, h, "pi-red-life-agent", dir)
		second := piRedLifeMint(t, h, "pi-red-life-agent", dir)
		assert.Equal(t, first, second,
			"RED (FR-029, S-1.4): a same-directory static re-serve renews in place — same "+
				"token, same label; no isolated_url exists yet to compare")
	})

	t.Run("replace_different_dir_new_label_old_retires", func(t *testing.T) {
		first := piRedLifeMint(t, h, "pi-red-life-agent-b", t.TempDir())
		second := piRedLifeMint(t, h, "pi-red-life-agent-b", t.TempDir())
		require.NotEqual(t, first, second,
			"FR-029: a different-directory re-serve replaces the token — new label")
		assert.Equal(t, http.StatusNotFound, piRedLifeLabelGet(t, h, first),
			"S-1.4: the replaced label 404s (registry miss)")
	})

	t.Run("evict_static_label_404", func(t *testing.T) {
		agent := "pi-red-life-agent-c"
		label := piRedLifeMint(t, h, agent, "")
		h.api.servedSubdirs.Evict(agent)
		assert.Equal(t, http.StatusNotFound, piRedLifeLabelGet(t, h, label),
			"S-1.4: after ServedSubdirs.Evict the label 404s")
	})

	t.Run("hot_flip_off_label_404", func(t *testing.T) {
		agent := "pi-red-life-agent-d"
		label := piRedLifeMint(t, h, agent, "")
		h.api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(false)
		t.Cleanup(func() {
			h.api.agentLoop.GetConfig().Gateway.PreviewEnabled = boolPtr(true)
		})
		assert.Equal(t, http.StatusNotFound, piRedLifeLabelGet(t, h, label),
			"S-1.4: with preview_enabled flipped off the label host 404s (hot, no restart)")
	})

	t.Run("dev_reserve_mints_new_label_each_time", func(t *testing.T) {
		// FR-029 / round-2 MIN-005: the dev store does NOT renew — a dev
		// re-serve mints a new token, hence a new label and origin. On darwin
		// the tier3 Linux gate answers before any mint (green-able on Linux
		// CI); on Linux pre-GREEN the RED here is the missing isolated_url.
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "index.html"), []byte("x"), 0o644))
		reg := newDevRegistryForLife(t)
		devMint := func(port int32) string {
			tool := tools.NewWebServeTool(
				dir, "pi-red-life-agent-dev",
				func() *config.Config { return h.api.agentLoop.GetConfig() },
				h.api.servedSubdirs, reg,
				tools.WebServeDevConfig{PortRange: [2]int32{18000, 18999}, MaxConcurrent: 2},
				nil, nil, 60, 86400)
			result := tool.Execute(tools.WithAgentID(context.Background(), "pi-red-life-agent-dev"),
				map[string]any{
					"command": fmt.Sprintf("python3 -m http.server %d", port),
					"port":    port,
				})
			require.False(t, result.IsError,
				"RED (dev path): on darwin the tier3 Linux gate answers before any mint — "+
					"green-able on Linux CI; on Linux pre-GREEN this is the missing mint. Got: %s",
				result.ForLLM)
			var parsed map[string]any
			require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &parsed))
			isoRaw, has := parsed["isolated_url"]
			require.True(t, has,
				"RED (FR-029 dev variant): the dev mint carries no isolated_url")
			return isoRaw.(string)
		}
		first := devMint(18044)
		second := devMint(18045)
		assert.NotEqual(t, first, second,
			"FR-029 (round-2 MIN-005): a dev re-serve does NOT renew — each mint is a new "+
				"token, label and origin")
	})
}

// newDevRegistryForLife creates a dev registry with cleanup for the
// lifecycle file's dev rows.
func newDevRegistryForLife(t *testing.T) *sandbox.DevServerRegistry {
	t.Helper()
	reg := sandbox.NewDevServerRegistry()
	t.Cleanup(reg.Close)
	return reg
}

// ---------------------------------------------------------------------------
// order 19 — per-label rate limit (S-7.2, FR-027)
// ---------------------------------------------------------------------------

// piRedPerLabelBurst is a modest burst any preview-class limiter must
// throttle. The exact limit is GREEN's choice (mechanism freedom — there is
// no preview limiter on /preview/ today to copy a value from; see the file
// header's spec-vs-code note); if GREEN's limit exceeds this burst, the
// constant is calibrated post-GREEN (fixture calibration, not a weakening).
const piRedPerLabelBurst = 100

// TestPreviewPerLabelRateLimit pins S-7.2: one label throttled; other labels
// and the main host unaffected. RED today at the missing mint (no label host
// exists to throttle); post-GREEN the burst must produce a 429 on the
// hammered label only.
func TestPreviewPerLabelRateLimit(t *testing.T) {
	h := piRedNewPlantedHarness(t)

	labelA := piRedLifeMint(t, h, "pi-red-rate-agent-a", "")
	labelB := piRedLifeMint(t, h, "pi-red-rate-agent-b", "")

	t.Run("hammered_label_throttled", func(t *testing.T) {
		saw429 := false
		for i := 0; i < piRedPerLabelBurst; i++ {
			if piRedLifeLabelGet(t, h, labelA) == http.StatusTooManyRequests {
				saw429 = true
				break
			}
		}
		require.True(t, saw429,
			"RED (FR-027, S-7.2): a burst of %d requests to one label host produced no 429 — "+
				"no per-label preview rate limiter exists yet", piRedPerLabelBurst)
	})

	t.Run("other_label_unaffected", func(t *testing.T) {
		assert.NotEqual(t, http.StatusTooManyRequests, piRedLifeLabelGet(t, h, labelB),
			"S-7.2 (pin): label A's throttle must not touch label B")
	})

	t.Run("main_host_unaffected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/agents", nil)
		require.NoError(t, err)
		req.Header.Set("Cookie", "omnipus-session="+h.sessionTok)
		resp, err := h.srv.Client().Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		assert.NotEqual(t, http.StatusTooManyRequests, resp.StatusCode,
			"S-7.2 (pin): label A's throttle must not touch the main host")
	})
}
