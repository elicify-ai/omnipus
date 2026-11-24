// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_audit_sandbox_health_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order row 17:
//
//   - TestHandleAuditLog_ReadsWriterDirectory (#1742): the audit viewer must
//     read the directory the audit writer writes. The writer derives its
//     directory from the config (filepath.Dir(cfg.AgentHomeBasePath()) +
//     "/system" — loop_construct.go, the spec's #1742 rider row), while
//     HandleAuditLog reads homePath + "/system/audit.jsonl". The two are the
//     same only while agents.defaults.home sits directly under the gateway
//     home; a customized agents home splits them and the viewer shows an
//     empty log while audit entries accumulate. RED today: with
//     agents.defaults.home customized, the planted writer-path entry is not
//     returned.
//
//   - TestSecurityHealth_FreshInstallSandbox (#853): a fresh install has NO
//     sandbox.mode in config.json; ResolvedMode() resolves empty to "off",
//     so Protection's health claims "Sandbox is disabled" even while
//     GET /security/sandbox-status reports the kernel sandbox installed and
//     enforcing. The finding must key on the RUNNING sandbox state for an
//     unset persisted mode, and must still fire for an explicit off
//     (positive control). RED today: the finding shows for the fresh install.
//
// Oracles: the spec's BDD scenarios "The audit viewer shows what the audit
// logger wrote" (#1742 rider) and "A fresh install does not claim the
// sandbox is off" (#853 rider).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

func TestHandleAuditLog_ReadsWriterDirectory(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Customized agents home: no longer <gateway home>/agents. Both the live
	// config (what a GREEN reader may consult) and the persisted config.json
	// carry it, so either GREEN wiring sees the same precondition.
	customRoot := t.TempDir()
	agentsHome := filepath.Join(customRoot, "custom-agents")
	require.NoError(t, os.MkdirAll(agentsHome, 0o700))
	api.agentLoop.GetConfig().Agents.Defaults.Home = agentsHome
	persistedRaw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(persistedRaw, &persisted))
	persisted["agents"] = map[string]any{"defaults": map[string]any{"home": agentsHome}, "list": []any{}}
	out, err := json.Marshal(persisted)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))

	// The audit writer's directory, derived the way the writer derives it:
	// Dir(AgentHomeBasePath()) + "/system". The fixture must genuinely
	// diverge from the viewer's current read path.
	writerDir := filepath.Dir(agentsHome) + string(os.PathSeparator) + "system"
	require.NotEqual(t, filepath.Join(api.homePath, "system"), writerDir,
		"fixture: the writer directory must diverge from the viewer's current read path")
	require.NoError(t, os.MkdirAll(writerDir, 0o700))
	entry := `{"event":"settings_test_event","detail":"written by the audit logger"}`
	require.NoError(t, os.WriteFile(filepath.Join(writerDir, "audit.jsonl"), []byte(entry+"\n"), 0o600))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-log", nil)
	w := httptest.NewRecorder()
	api.HandleAuditLog(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.Contains(t, w.Body.String(), "settings_test_event",
		"RED (#1742): the audit viewer read %s and showed an empty log while the audit writer "+
			"writes %s — a customized agents home hides every audit entry from the viewer",
		filepath.Join(api.homePath, "system", "audit.jsonl"), filepath.Join(writerDir, "audit.jsonl"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	entries, _ := body["entries"].([]any)
	require.NotEmpty(t, entries, "the viewer must return the writer's entries")
}

func TestSecurityHealth_FreshInstallSandbox(t *testing.T) {
	findTitle := func(issues []map[string]any, title string) bool {
		for _, i := range issues {
			if i["title"] == title {
				return true
			}
		}
		return false
	}

	t.Run("fresh install, empty sandbox.mode, kernel sandbox enforcing: no disabled finding", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		cfg := api.agentLoop.GetConfig()
		require.Empty(t, cfg.Sandbox.Mode, "fixture: a fresh install has no sandbox.mode persisted")
		withTurnPolicyBase(t, true) // the kernel sandbox is installed and enforcing
		require.True(t, sandbox.TurnPolicyBaseInstalled())

		issues := api.runDiagnosticChecks(cfg)
		require.False(t, findTitle(issues, "Sandbox is disabled"),
			"RED (#853): a fresh install with an empty sandbox.mode is told 'Sandbox is disabled' "+
				"while GET /security/sandbox-status reports the kernel sandbox active — "+
				"the finding must key on the running sandbox state, not on the empty persisted mode alone")
	})

	t.Run("positive control: an explicit off still reports the finding", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		cfg := api.agentLoop.GetConfig()
		cfg.Sandbox.Mode = "off"
		withTurnPolicyBase(t, true)
		issues := api.runDiagnosticChecks(cfg)
		require.True(t, findTitle(issues, "Sandbox is disabled"),
			"an explicitly disabled sandbox must keep the finding (positive control)")
	})
}
