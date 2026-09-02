// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestRestAPIWithAuditLog creates a restAPI where the agent loop has a real
// audit logger wired (Sandbox.AuditLog = true). The workspace is placed at
// tmpDir/workspace so that the agent loop calculates homePath = tmpDir and
// writes audit entries to tmpDir/system/.
func newTestRestAPIWithAuditLog(t *testing.T) (*restAPI, string) {
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
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
	}

	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"sandbox":{"audit_log":true}}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
	}
	return api, tmpDir
}

// adminCtx returns a context with an authenticated user set — used for PUT
// tests. Under the single-user model there is no separate role to inject;
// the handlers under test here are gated purely by RequireNotBypass at
// route-registration time (not exercised by these direct-handler-call
// tests) and read the actor identity from ctxkey.UserContextKey for audit
// attribution.
func adminCtx() context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.UserContextKey{}, &config.UserConfig{Username: "admin"})
	return ctx
}

// TestHandleSandboxAuditLog_PUTPersists verifies that PUT {enabled:true}
// writes sandbox.audit_log=true to config.json on disk.
func TestHandleSandboxAuditLog_PUTPersists(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := strings.NewReader(`{"enabled":true}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	sandbox, _ := m["sandbox"].(map[string]any)
	require.NotNil(t, sandbox, "sandbox key must exist in config.json after PUT")
	assert.Equal(t, true, sandbox["audit_log"], "audit_log must be persisted as true")
}

// TestHandleSandboxAuditLog_ResponseShape verifies the response JSON contains
// saved, requires_restart:true, and applied_enabled with correct types.
func TestHandleSandboxAuditLog_ResponseShape(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := strings.NewReader(`{"enabled":true}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	saved, hasSaved := resp["saved"]
	assert.True(t, hasSaved, "response must have 'saved' field")
	assert.Equal(t, true, saved, "'saved' must be true")

	rr, hasRR := resp["requires_restart"]
	assert.True(t, hasRR, "response must have 'requires_restart' field")
	assert.Equal(t, true, rr, "'requires_restart' must be true")

	_, hasAE := resp["applied_enabled"]
	assert.True(t, hasAE, "response must have 'applied_enabled' field")
}

// TestHandleSandboxAuditLog_MethodNotAllowed verifies that POST and DELETE
// receive 405 Method Not Allowed.
func TestHandleSandboxAuditLog_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			r := httptest.NewRequest(method, "/api/v1/security/audit-log", nil)
			w := httptest.NewRecorder()
			api.HandleSandboxAuditLog(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// TestHandleSandboxAuditLog_InvalidBody verifies that missing the 'enabled'
// field or sending malformed JSON yields 400 Bad Request.
func TestHandleSandboxAuditLog_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	t.Run("missing enabled field", func(t *testing.T) {
		// Regression: gen.AuditLogToggleRequest.Enabled is bool (not *bool), so an
		// absent "enabled" key would decode as false and silently disable audit
		// logging. The pre-check in HandleSandboxAuditLog now rejects {} with 400
		// before decoding, preventing the silent-disable regression.
		r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(`{}`))
		r = r.WithContext(adminCtx())
		w := httptest.NewRecorder()
		api.HandleSandboxAuditLog(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "enabled")
	})

	t.Run("malformed JSON", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(`{not json}`))
		r = r.WithContext(adminCtx())
		w := httptest.NewRecorder()
		api.HandleSandboxAuditLog(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// TestHandleSandboxAuditLog_EmitsAuditEntry verifies that when audit logging is
// already enabled (precondition: sandbox.audit_log=true), a PUT {enabled:false}
// emits a security_setting_change JSONL record with the correct resource and
// actor fields.
func TestHandleSandboxAuditLog_EmitsAuditEntry(t *testing.T) {
	api, tmpDir := newTestRestAPIWithAuditLog(t)

	body := strings.NewReader(`{"enabled":false}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	systemDir := filepath.Join(tmpDir, "system")
	entries, err := os.ReadDir(systemDir)
	require.NoError(t, err, "system dir must exist after audit emit")
	require.NotEmpty(t, entries, "at least one audit file must exist")

	var found bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(systemDir, entry.Name()))
		require.NoError(t, err)
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				continue
			}
			if record["event"] != "security_setting_change" {
				continue
			}
			assert.Equal(t, "sandbox.audit_log", record["resource"], "resource must match")
			assert.Equal(t, "admin", record["actor"], "actor must be the admin username")
			found = true
		}
		require.NoError(t, scanner.Err())
	}

	assert.True(t, found, "security_setting_change record must appear in audit JSONL")
}

// TestAuditLog_RecordRetrievableAlongsideDottedLegacyEvent pins the READ
// surface FR-028 (browser-agent-capability-spec §10 order 23, US-18/AC4)
// actually rests on today, and it pins it as it really behaves — not as the
// spec's prose hopes.
//
// FR-028 says a metadata-only `browser_snapshot` audit event is "readable via
// GET /api/v1/audit-log and $OMNIPUS_HOME/system/audit.jsonl today, and via
// Settings → Security → Audit Log once #667 lands". This test covers the
// first half and DELIBERATELY DOES NOT claim the second.
//
// What issue #667 actually is, verified in this worktree rather than assumed:
//   - contracts/components/schemas/AuditEntry.yaml:17 pins `event` to
//     `^[a-z_]+$`, and the generated client schema carries that verbatim
//     (src/lib/api/generated/schemas.ts::AuditEntry, `z.string().regex(/^[a-z_]+$/)`).
//   - AuditLogResponse wraps it in `z.array(AuditEntry)`, so ONE non-matching
//     `event` fails the WHOLE array, and src/lib/api.ts::fetchAuditLog runs
//     that schema over the response — the viewer blanks entirely.
//   - Dotted event names are emitted from 19 non-test production sites today
//     (`pkg/tools/memory.go`'s `memory.remember`, `pkg/gateway/rest_workspaces.go`'s
//     `workspace.create`, `pkg/gateway/rest.go`'s `agent.delete`, …), PLUS seven
//     dotted named constants in `pkg/audit/audit.go:53-106` — `boot.abort`,
//     `channel.pairing`, `cli.validate`, `executor.smoke_test`,
//     `onboarding.admin_created`, `onboarding.refused`, `skill.call`.
//   - Two of those make the blank effectively certain rather than likely:
//     `onboarding.admin_created` is written once per install at admin
//     registration (`pkg/gateway/rest_onboarding.go:835`), and `skill.call` is
//     written on every `Skill` tool call (`pkg/audit/skill_call.go:155`,
//     ADR-072 D3.1). So on any real install the Audit Log screen is ALREADY
//     blank, for reasons that have nothing to do with this spec — FR-028's
//     "operator-inspectable" mitigation does not reach an operator today.
//
// The failure is therefore CLIENT-side, in zod. The Go handler
// (rest_settings.go::HandleAuditLog) passes each line through as an opaque
// json.RawMessage and validates nothing, so a dotted legacy record neither
// drops nor corrupts a well-named one sitting next to it. That is the
// property this test fixes: `browser_snapshot` stays retrievable from the
// endpoint even in a log that contains the very record that blanks the SPA.
//
// The last two assertions are the honest half. They state, at the contract's
// own regex, that `browser_snapshot` is innocent of #667 (it matches) and
// that the co-resident `channel.pairing` is guilty of it (it does not) — so
// nobody later reads this test's green as evidence that the Audit Log SCREEN
// shows a snapshot. It does not, and will not until #667 lands.
func TestAuditLog_RecordRetrievableAlongsideDottedLegacyEvent(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	systemDir := filepath.Join(api.homePath, "system")
	require.NoError(t, os.MkdirAll(systemDir, 0o700))

	// Line 1: a dotted LEGACY event — the exact constant at
	// pkg/audit/audit.go::EventChannelPairing. This is the #667 trigger.
	// Line 2: the metadata-only browser_snapshot record FR-028 specifies —
	// origin, node count, byte count, values-emitted flag, truncated flag,
	// and deliberately NOT the captured values themselves.
	lines := []string{
		`{"timestamp":"2026-09-02T10:00:00Z","event":"channel.pairing","decision":"allow","details":{"channel":"whatsapp_native"}}`,
		`{"timestamp":"2026-09-02T10:00:01Z","event":"browser_snapshot","decision":"allow","agent_id":"ray",` +
			`"details":{"page_origin":"https://example.com","node_count":412,"output_bytes":18234,` +
			`"value_nodes_emitted":7,"truncated":false}}`,
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(systemDir, "audit.jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"),
		0o600,
	))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-log", nil)
	w := httptest.NewRecorder()
	api.HandleAuditLog(w, r)
	require.Equal(t, http.StatusOK, w.Code, "endpoint must not fail on a dotted legacy event name")

	var resp struct {
		Entries     []map[string]any `json:"entries"`
		ChainStatus string           `json:"chain_status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Entries, 2, "both records must survive the read")

	byEvent := map[string]map[string]any{}
	for _, e := range resp.Entries {
		name, _ := e["event"].(string)
		byEvent[name] = e
	}

	// (1) The dotted legacy record is returned, not silently dropped. If the
	// handler ever started filtering by name this assertion would red, and
	// the next one would keep passing — which is why both are here.
	require.Contains(t, byEvent, "channel.pairing",
		"the handler must pass a dotted legacy record through untouched, not filter it")

	// (2) The browser_snapshot record is still retrievable ALONGSIDE it.
	snap, ok := byEvent["browser_snapshot"]
	require.True(t, ok, "browser_snapshot must be retrievable from GET /api/v1/audit-log")

	details, ok := snap["details"].(map[string]any)
	require.True(t, ok, "browser_snapshot record must carry a details object")
	assert.Equal(t, "https://example.com", details["page_origin"])
	assert.Equal(t, float64(412), details["node_count"])
	assert.Equal(t, float64(18234), details["output_bytes"])
	assert.Equal(t, false, details["truncated"])
	assert.Equal(t, "ray", snap["agent_id"])

	// (3) FIXED oracle, not a conditional one: the record is metadata-only.
	// The whole serialized entry must contain no accessible-name or field-value
	// text from the page. Asserted over the raw bytes so a value smuggled into
	// any field, at any nesting depth, reds.
	rawSnap, err := json.Marshal(snap)
	require.NoError(t, err)
	for _, forbidden := range []string{"role=", "textbox", "password", "4111"} {
		assert.NotContains(t, string(rawSnap), forbidden,
			"the browser_snapshot audit record must carry metadata only, never captured node text")
	}

	// (4) The #667 boundary, stated at the contract's own regex
	// (contracts/components/schemas/AuditEntry.yaml:17). browser_snapshot's
	// underscore name is NOT what blanks the viewer; the co-resident dotted
	// legacy name is. This test's green is evidence about the ENDPOINT only.
	contractEventPattern := regexp.MustCompile(`^[a-z_]+$`)
	assert.True(t, contractEventPattern.MatchString("browser_snapshot"),
		"browser_snapshot must satisfy AuditEntry's ^[a-z_]+$ so it is never the cause of #667")
	assert.False(t, contractEventPattern.MatchString("channel.pairing"),
		"channel.pairing violates AuditEntry's ^[a-z_]+$ — this is #667, and it is why "+
			"Settings → Security → Audit Log is blank on a real install regardless of this spec")
}
