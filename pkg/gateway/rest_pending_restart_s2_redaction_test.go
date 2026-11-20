// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_pending_restart_s2_redaction_test.go — Settings redesign #1519, part 8,
// RED.
//
// Security finding S2 proof (sec report 2026-11-20, "failed-apply readback
// exposes unredacted sensitive values"). The report's instrument gap:
// TestSettingsWrites_RefreshFailureReported "uses nonsecret log-level/context
// values" and TestPendingRestart_ApplyFailedEntries exercises the locked-store
// seam "but not secret redaction". This proof covers the reported disclosure
// scenario itself: a failed refresh with a protected legacy MCP header and an
// inert dotted member, alongside a normal nonsecret setting.
//
// Oracle (spec + report, never the fix code):
//   - C-2 addition / FR-062: apply_failed entries carry persisted_value read
//     from config.json at GET time, and applied_value.
//   - C-6 / FR-102: the failed-refresh write records its keys before answering.
//   - Report S2: an inert literal dotted member naming a legacy header must not
//     turn the pending readback into a secret disclosure — "Normal config
//     readback masks that same value", so the readback must be no less safe.
//     The report's QA ask names exactly this fixture shape plus "a normal
//     nonsecret setting" as the control.
//
// RED today (landing): the apply_failed recording of FR-102/FR-062 is not
// implemented yet, so the positive control fails first — the readback surface
// this proof needs does not exist on landing. On the backend branch the
// recording exists and the assertion becomes the live S2 discriminator: the
// secret must not appear anywhere in the response.
//
// Expected against work/settings-1519-be (fix ee658b0fd7): passes.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

const s2LegacyHeaderSecret = "Bearer sk-s2-legacy-header-secret"

// s2LegacyHeaderDotPath is the real nested path of the legacy header in the
// marshalled config (Config.Tools is json:"tools"; ToolsConfig.MCP is
// json:"mcp"): tools.mcp.servers.<name>.headers.<Header>.
const s2LegacyHeaderDotPath = "tools.mcp.servers.probe.headers.Authorization"

// s2SeedLegacyMCPHeader plants a supported legacy plaintext MCP authorization
// header in BOTH the live config and config.json — the shape old installs
// carry and LoadConfig keeps loading (the report's S2 evidence).
func s2SeedLegacyMCPHeader(t *testing.T, api *restAPI) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"probe": {
			Type: "http",
			URL:  "https://probe.invalid/sse",
			Headers: map[string]string{
				"Authorization": s2LegacyHeaderSecret,
			},
		},
	}
	persistedRaw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(persistedRaw, &doc))
	doc["tools"] = map[string]any{
		"mcp": map[string]any{
			"servers": map[string]any{
				"probe": map[string]any{
					"type": "http",
					"url":  "https://probe.invalid/sse",
					"headers": map[string]any{
						"Authorization": s2LegacyHeaderSecret,
					},
				},
			},
		},
	}
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))
}

func TestPendingRestartReadback_DoesNotDiscloseLegacyHeaderSecret(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	s2SeedLegacyMCPHeader(t, api)

	// The save: the inert literal dotted member naming the protected header
	// (null value — the report: the MCP-header guards reject literal strings,
	// not null) plus a normal nonsecret setting as the control.
	locked := credentials.NewStore(strings.Join([]string{t.TempDir(), "locked-credentials.json"}, string(os.PathSeparator)))
	previous := api.credStore
	api.credStore = locked
	w := putConfigBody(api, `{"tools.mcp.servers.probe.headers.Authorization": null, "gateway":{"log_level":"debug"}}`)
	api.credStore = previous

	// Precondition: the live refresh failed after config.json was written
	// (locked-store seam, the C-6 pattern) — the saved-but-not-applied state
	// the pending readback describes.
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"fixture: the refresh must fail after the write for the apply_failed state to exist; body=%s", w.Body.String())

	// The readback the banner and the saved-state check use.
	pw := callPendingRestart(t, api)
	require.Equal(t, http.StatusOK, pw.Code, "body=%s", pw.Body.String())
	body := pw.Body.String()

	// Positive control (the report's "normal nonsecret setting"): the
	// apply_failed readback must actually work — the ordinary key is listed
	// with its real stored value. Without this control a readback that
	// recorded nothing at all would pass the secret assertion vacuously.
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(pw.Body.Bytes(), &entries), "body=%s", body)
	logLevelListed := false
	for _, e := range entries {
		if e["key"] == "gateway.log_level" && e["reason"] == "apply_failed" {
			logLevelListed = true
			require.Equal(t, "debug", e["persisted_value"],
				"the nonsecret control must still report its real stored value; entry=%v", e)
		}
	}
	require.True(t, logLevelListed,
		"RED on landing (the S2 proof needs the C-6/FR-102 readback): GET /config/pending-restart lists no "+
			"apply_failed entry for gateway.log_level — the failed-apply recording the report's readback scenario "+
			"rides on (FR-102/FR-062) is not implemented yet, so the secret-disclosure surface cannot be exercised")

	// The finding: the legacy header secret must not surface anywhere in the
	// readback — not in persisted_value, not in applied_value, under any entry.
	require.NotContains(t, body, s2LegacyHeaderSecret,
		"S2: the pending-restart readback disclosed the protected legacy MCP header secret — the inert literal "+
			"dotted member was resolved against the real nested config and returned in persisted_value/applied_value, "+
			"which the normal config readback masks")
	for _, e := range entries {
		if e["key"] == s2LegacyHeaderDotPath {
			persisted, _ := e["persisted_value"].(string)
			applied, _ := e["applied_value"].(string)
			require.NotContains(t, persisted, s2LegacyHeaderSecret,
				"S2: the literal-name entry's persisted_value carries the secret; entry=%v", e)
			require.NotContains(t, applied, s2LegacyHeaderSecret,
				"S2: the literal-name entry's applied_value carries the secret; entry=%v", e)
		}
	}
}
