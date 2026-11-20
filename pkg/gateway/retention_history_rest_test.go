package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRetentionHistoryAPI is newTestRestAPIWithAuditLog with the retention
// window seeded in the RUNNING config (the sweep reads the config the loop
// holds — pkg/gateway/retention_goroutine.go::executeRetentionSweepTick) and
// in config.json. The old seeding went through PUT /security/retention, which
// the Settings redesign removes (FR-047, C-3); the legacy disabled switch is
// config-file only (FR-048), so the window is seeded here rather than written
// through any endpoint.
func newRetentionHistoryAPI(t *testing.T, sessionDays int) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o700))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}
	cfg.Storage.Retention = config.OmnipusRetentionConfig{SessionDays: sessionDays}

	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"sandbox":{"audit_log":true},"storage":{"retention":{"session_days":` + strconv.Itoa(sessionDays) + `}}}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}
	return api, tmpDir
}

// Fix-round-3's explicit HTTP policy: index-state unavailability is a per-chat
// warning and the real manual sweep returns200 with its success audit. Actual
// filesystem failure still returns500 and must not emit a success audit.
// These call the REAL registered handler implementation with an authenticated
// admin context; route-registration/authentication middleware are out of scope.
func TestRetentionHistoryGateway_ManualSweepPreservesHTTPAndAuditPolicy(t *testing.T) {
	for _, reason := range []string{"catchup_limit", "index_inconsistent", "index_missing", "storage_error", "healthy"} {
		t.Run(reason, func(t *testing.T) {
			logs := retentionGatewayCapture(t)
			api, home := newRetentionHistoryAPI(t, 7) // the seven-day window the sweep audit must report
			// Positive control: a REAL handler-written security_setting_change,
			// proving the audit scanner below sees real handler writes. The old
			// control rode PUT /security/retention, which FR-047 removes; the
			// audit-log toggle writes the same record kind under its own
			// resource ("sandbox.audit_log"), so it is the control now.
			put := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(`{"enabled":true}`))
			put.Header.Set("Content-Type", "application/json")
			put = withAdminRole(put)
			putW := httptest.NewRecorder()
			api.HandleSandboxAuditLog(putW, put)
			require.Equal(t, http.StatusOK, putW.Code, "real handler PUT arms the positive control; body=%s", putW.Body)
			controls := retentionGatewayAudit(t, home, "sandbox.audit_log")
			require.Len(t, controls, 1, "positive control: audit scanner can see a REAL handler-written security change")
			require.Equal(t, "admin", controls[0].Actor)
			require.Empty(t, retentionGatewayAudit(t, home, "storage.retention.sweep"))

			f := retentionGatewaySeed(t, api.agentLoop.GetSessionStore())
			f.breakIndex(t, reason)
			index := retentionGatewayFiles(t, f.indexDir())
			canonical := retentionGatewayCanonical(t, f)
			require.Empty(t, retentionGatewayEvents(t, logs, retentionGatewayUnavailableLog), "no earlier failure check may hide a silent manual sweep")

			for repeat := 1; repeat <= 2; repeat++ {
				agedSession := retentionGatewayAgedSession(t, f.store)
				response := retentionSweepPOST(t, api) // no fake store/sweep/error; real admin POST handler
				require.NoDirExists(t, agedSession, "the real manual sweep still removes other aged sessions")
				if reason == "storage_error" {
					require.Equal(t, http.StatusInternalServerError, response.Code, "genuine disk error remains an HTTP failure")
					var body generated.ErrorResponse
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
					require.Contains(t, body.Error, "sweep failed")
					require.NotContains(t, response.Body.String(), f.store.BaseDir(), "private storage paths stay server-side")
					require.NotContains(t, response.Body.String(), retentionGatewayPrivateBody)
					require.Empty(t, retentionGatewayAudit(t, home, "storage.retention.sweep"), "failed manual sweep must never emit a success audit")
					failures := retentionGatewayEvents(t, logs, "rest: on-demand retention sweep failed")
					require.Len(t, failures, repeat)
					for _, failure := range failures {
						require.Equal(t, "ERROR", failure["level"])
					}
				} else {
					assert.Equal(t, http.StatusOK, response.Code, "one unavailable derived index must NOT make the manual sweep fail")
					var body generated.RetentionSweepResult
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
					assert.Equal(t, generated.RetentionSweepResult{Removed: 1}, body)
					audits := retentionGatewayAudit(t, home, "storage.retention.sweep")
					assert.Len(t, audits, repeat, "exactly ONE real success audit per successful sweep")
					for _, record := range audits {
						require.Equal(t, audit.EventSecuritySettingChange, record.Event)
						require.Equal(t, "admin", record.Actor)
						require.Equal(t, "storage.retention.sweep", record.Resource)
						require.Equal(t, map[string]any{"days": json.Number("7")}, record.OldValue)
						require.Equal(t, map[string]any{"removed": json.Number("1")}, record.NewValue)
					}
					assert.Empty(t, retentionGatewayEvents(t, logs, "rest: on-demand retention sweep failed"))
				}
				retentionGatewayRequireP3(t, logs, f, reason)
				require.Equal(t, index, retentionGatewayFiles(t, f.indexDir()), "manual retention must NOT heal/recreate the index")
				require.Equal(t, canonical, retentionGatewayCanonical(t, f))
			}
		})
	}
}

func retentionGatewayAudit(t *testing.T, home, resource string) []audit.SecurityChangeRecord {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(home, "system", "*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "the real audit sink must exist; an empty scan is not evidence")
	var matching []audit.SecurityChangeRecord
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.UseNumber()
		for {
			var record audit.SecurityChangeRecord
			err := decoder.Decode(&record)
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err, "malformed audit records must fail the check, not disappear from it")
			if record.Event == audit.EventSecuritySettingChange && record.Resource == resource {
				matching = append(matching, record)
			}
		}
	}
	return matching
}
