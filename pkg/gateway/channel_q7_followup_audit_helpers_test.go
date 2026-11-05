package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

const q7AuditApprover = "q7-audit-approver"

// Use an actual configured human bearer identity and the real authentication
// middleware. Never inject the approver into the request context or audit rows.
func q7AuditAuth(t *testing.T, f *wu6Fixture, sid string) func(string, string, string) *httptest.ResponseRecorder {
	t.Helper()
	var entropy [32]byte
	_, err := rand.Read(entropy[:])
	require.NoError(t, err)
	bearer := hex.EncodeToString(entropy[:])
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Gateway.DevModeBypass = false
		cfg.Gateway.Users = []config.UserConfig{{Username: q7AuditApprover}}
		if err := cfg.SetUserTokenHash(q7AuditApprover, bearer); err != nil {
			return err
		}
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	owner := q7AuditApprover
	require.NoError(t, f.al.GetSessionStore().SetMeta(sid, session.MetaPatch{Owner: &owner}))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+bearer)
		ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, f.al.GetConfig())
		rec := httptest.NewRecorder()
		f.mux.ServeHTTP(rec, req.WithContext(ctx))
		return rec
	}
	probe := httptest.NewRequest(http.MethodGet, "/api/v1/security/tool-policies", nil)
	probe.Header.Set("Authorization", "Bearer "+bearer)
	identity := checkBearerAuth(probe.Context(), httptest.NewRecorder(), probe, f.al.GetConfig())
	require.True(t, identity.Authenticated, "instrument: configured human bearer is actually authenticated")
	require.NotNil(t, identity.User)
	require.Equal(t, q7AuditApprover, identity.User.Username)
	require.Equal(t, http.StatusOK, request(http.MethodGet, probe.URL.Path, "").Code)
	return request
}

func q7AuditRead(t *testing.T, f *wu6Fixture, sid, toolName string, executed bool) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.f1277.home, "system", "audit.jsonl"))
	require.NoError(t, err)
	var rows []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var row map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row), "instrument: every captured audit line is valid JSON")
		rows = append(rows, row)
	}
	require.NoError(t, scanner.Err())
	if dir := os.Getenv("Q7_AUDIT_EVIDENCE_DIR"); dir != "" {
		// Optional evidence export only; assertions run identically without it.
		// The fixed fixture contains no real account or customer information.
		require.NoError(t, os.MkdirAll(dir, 0o700))
		name := strings.NewReplacer("/", "__", "\\", "__").Replace(t.Name())
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".jsonl"), raw, 0o600))
	}
	t.Logf("instrument: read %d real audit records", len(rows))
	// Boot's real startup record proves the reader sees this enabled logger.
	// An approved call also has to appear as an actual generic execution in
	// the SAME file. Denied/canceled calls correctly have no execution row.
	// No synthetic audit emitter is planted to make either control pass.
	seenStartup, seenExecution := false, false
	for _, row := range rows {
		seenStartup = seenStartup || row["event"] == "startup"
		seenExecution = seenExecution || (row["event"] == audit.EventToolCall && row["tool"] == toolName && row["session_id"] == sid)
	}
	require.True(t, seenStartup, "instrument: reader sees actual boot startup in the real audit file")
	if executed {
		require.True(t, seenExecution, "instrument: reader sees this actual tool execution and session in the same audit file")
	}
	return rows
}

func q7AuditEvent(t *testing.T, rows []map[string]any, event, approvalID string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, row := range rows {
		fields, _ := row["fields"].(map[string]any)
		if row["event"] == event && fields["approval_id"] == approvalID {
			found = append(found, fields)
		}
	}
	require.Len(t, found, 1, "R-S3: actual audit must contain one %s for this approval, not only a WS frame/tool result", event)
	return found[0]
}

func q7AuditIdentity(t *testing.T, fields map[string]any, entry *approvalEntry, hash string) {
	t.Helper()
	for key, want := range map[string]string{
		"approval_id": entry.ApprovalID, "tool_call_id": entry.ToolCallID,
		"session_id": entry.SessionID, "agent_id": entry.AgentID,
		"turn_id": entry.TurnID, "tool_name": entry.ToolName, "args_hash": hash,
	} {
		require.Equal(t, want, fields[key], "R-S3: audit identity/digest field %s", key)
	}
}

// Save-outcome event names and nesting were not specified by S3. Assert the
// required relationship without inventing either: a separate audit record must
// carry this approval/call/session and the ACTUAL resulting revision (the
// already-defined FR-019 revision alternative), or the stale-refusal reason.
func q7AuditOutcome(t *testing.T, rows []map[string]any, entry *approvalEntry, revision string, stale bool) {
	t.Helper()
	matched := false
	for _, row := range rows {
		event, _ := row["event"].(string)
		if event == audit.EventToolPolicyAskRequested || event == audit.EventToolPolicyAskGranted || event == audit.EventToolPolicyAskDenied {
			continue // Consent alone is never proof of persistence or rejection.
		}
		if !q7AuditContainsField(row, "approval_id", entry.ApprovalID) ||
			!q7AuditContainsField(row, "tool_call_id", entry.ToolCallID) ||
			!q7AuditContainsField(row, "session_id", entry.SessionID) {
			continue
		}
		if stale {
			matched = matched || q7AuditContainsValue(row, "stale_revision") || q7AuditContainsValue(row, "CONFLICT")
		} else {
			matched = matched || q7AuditContainsValue(row, revision)
		}
	}
	require.True(t, matched, "R-S3: a distinct correlated outcome must prove resulting revision or post-approval stale refusal, not merely approval")
}

func q7AuditContainsField(value any, key, want string) bool {
	switch x := value.(type) {
	case map[string]any:
		if x[key] == want {
			return true
		}
		for _, child := range x {
			if q7AuditContainsField(child, key, want) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if q7AuditContainsField(child, key, want) {
				return true
			}
		}
	}
	return false
}

func q7AuditSavedOutbound(t *testing.T, f *wu6Fixture, request func(string, string, string) *httptest.ResponseRecorder) string {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(f.provider.lastToolResult()), &result))
	require.Equal(t, "saved", result["status"], "instrument: approve-once reached the actual writer")
	rec := request(http.MethodGet, "/api/v1/channels/telegram/outbound", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var read gen.ChannelOutboundRead
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &read))
	require.NotEmpty(t, read.Revision)
	require.Equal(t, "agent_may_start", string(read.Outbound.Policy))
	require.Equal(t, []string{"telegram:987654321"}, read.Outbound.Targets)
	live := f.al.GetConfig().Channels["telegram"].Outbound
	require.NotNil(t, live)
	require.Equal(t, "agent_may_start", live.Policy)
	require.Equal(t, []string{"telegram:987654321"}, []string(live.Targets))
	disk := readOutboundConfigChannelsFile(t, f)
	instance, ok := disk["telegram"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{"policy": "agent_may_start", "targets": []any{"telegram:987654321"}}, instance["outbound"])
	return read.Revision
}

func q7AuditPrivateArguments(t *testing.T, rows []map[string]any, toolName string) {
	t.Helper()
	requestedPreviews := 0
	for _, row := range rows {
		// R-S3 / CHECK F3: inspect EVERY audit row, not only tool_call. This
		// includes args_preview at any nesting depth and future event names;
		// the real request event must not leak an array of private targets.
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		require.NotContains(t, string(raw), `"987654321"`, "R-S3: raw target payload is not a general tool-call argument (event=%v)", row["event"])
		require.NotContains(t, string(raw), `"telegram:987654321"`, "R-S3: canonical target payload is also private in the general argument log (event=%v)", row["event"])
		fields, _ := row["fields"].(map[string]any)
		if row["event"] == audit.EventToolPolicyAskRequested && fields["tool_name"] == toolName {
			require.Contains(t, fields, "args_preview", "instrument: the real request's argument preview is actually inspected")
			requestedPreviews++
		}
		if row["event"] == audit.EventToolCall && row["tool"] == toolName {
			details, _ := row["details"].(map[string]any)
			arguments, _ := details["args"].(map[string]any)
			require.NotContains(t, arguments, "targets", "R-S3: do not widen the general identifying-argument allowlist")
		}
	}
	require.Positive(t, requestedPreviews, "instrument: privacy assertions saw a tool.policy.ask.requested preview, not an empty log")
}

func q7AuditContainsValue(value any, want string) bool {
	switch x := value.(type) {
	case string:
		return x == want
	case map[string]any:
		for _, child := range x {
			if q7AuditContainsValue(child, want) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if q7AuditContainsValue(child, want) {
				return true
			}
		}
	}
	return false
}
