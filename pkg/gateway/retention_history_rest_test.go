package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fix-round-3's explicit HTTP policy: index-state unavailability is a per-chat
// warning and the real manual sweep returns200 with its success audit. Actual
// filesystem failure still returns500 and must not emit a success audit.
// These call the REAL registered handler implementation with an authenticated
// admin context; route-registration/authentication middleware are out of scope.
func TestRetentionHistoryGateway_ManualSweepPreservesHTTPAndAuditPolicy(t *testing.T) {
	for _, reason := range []string{"catchup_limit", "index_inconsistent", "index_missing", "storage_error", "healthy"} {
		t.Run(reason, func(t *testing.T) {
			logs := retentionGatewayCapture(t)
			api, home := newTestRestAPIWithAuditLog(t)
			put := retentionPUT(t, api, `{"session_days":7,"disabled":false}`)
			require.Equal(t, http.StatusOK, put.Code, "real config PUT sets the seven-day retention window")
			controls := retentionGatewayAudit(t, home, "storage.retention")
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
