// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_audit_logger_binding_test.go — Settings redesign #1519, part 8, RED.
//
// Security finding S3 proof (sec report 2026-11-20, "the audit viewer can
// display one file and verify another"). The report's instrument gap: the
// earlier test "plants a log file at the expected path without proving that
// the writer/verifier uses it". This proof creates a REAL logger (the one the
// loop constructs at boot, audit on by default), emits a REAL event, changes
// the live agents home, and checks both the displayed records and the
// integrity verdict against that same logger file.
//
// Oracle (spec + report, never the fix code):
//   - FR-103 (#1742): "HandleAuditLog MUST read the audit files from the
//     directory the gateway's audit writer uses"; BDD "The audit viewer shows
//     what the audit logger wrote" ("the entry is listed").
//   - Report S3 QA ask: "create a real logger, emit a real event, change the
//     live home, and verify both displayed records and integrity status" —
//     and the finding's harm: entries can vanish, or stale entries can be
//     shown as chain-valid "based on verification of A, not B".
//
// Binding is proven by tampering: after the intact read answers valid with the
// emitted event listed, tampering THE LOGGER'S file must flip the same read to
// broken. A viewer that displayed one file while verifying another cannot pass.
//
// RED today (landing): HandleAuditLog reads homePath + "/system" while the
// logger writes Dir(agents home) + "/system" — with a customized agents home
// the viewer's file does not exist, so the emitted event is missing and the
// chain reads "unknown".
//
// Expected against work/settings-1519-be (fix 1fe8afb29e): passes.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const s3ViewerMarker = "s3-viewer-binding-proof"

func TestAuditViewer_BindsRecordsAndChainToActiveLogger(t *testing.T) {
	withEdition(t, config.EditionCore)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("S3-Viewer-Binding-Passphrase-1"), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "s3-person", string(hash))

	// A hand-built Config carries no defaults seed, so audit logging is turned
	// on EXPLICITLY here — the state a default-on install boots with
	// (initializeAudit then constructs the real logger from the agents home).
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	cfg.Sandbox.AuditLog = true
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
	}
	home := tmpDir

	logger := api.agentLoop.AuditLogger()
	require.NotNil(t, logger,
		"fixture: sandbox.audit_log is on — the loop must carry a real audit logger")

	// The audit writer's directory, derived the way the writer derives it
	// (the spec's #1742 row): Dir(AgentHomeBasePath()) + "/system".
	writerDir := filepath.Join(filepath.Dir(api.agentLoop.GetConfig().AgentHomeBasePath()), "system")
	writerFile := filepath.Join(writerDir, "audit.jsonl")
	require.NotEqual(t, filepath.Join(home, "system"), writerDir,
		"fixture: the writer directory must diverge from the viewer's current read path")

	// A real event through the real logger, confirmed on the writer's file —
	// the precondition "the logger really wrote this record" (the report's
	// instrument gap was a planted file no writer used).
	audit.Emit(context.Background(), logger, audit.EventStartup, audit.SeverityInfo,
		map[string]any{"marker": s3ViewerMarker, "detail": "emitted by the S3 binding proof"})
	written, err := os.ReadFile(writerFile)
	require.NoError(t, err, "the logger's file must be readable at %s", writerFile)
	require.Contains(t, string(written), s3ViewerMarker,
		"fixture: the emitted event must be on the writer's file before the viewer is asked for it")

	// The live agents home changes after boot (the finding's scenario); the
	// logger keeps writing its boot directory. Persist it the way the #1742
	// fixture does, so any wiring that consults config.json sees the same
	// precondition.
	movedHome := filepath.Join(t.TempDir(), "custom-agents-after-boot")
	require.NoError(t, os.MkdirAll(movedHome, 0o700))
	api.agentLoop.GetConfig().Agents.Defaults.Home = movedHome
	persistedRaw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(persistedRaw, &doc))
	doc["agents"] = map[string]any{"defaults": map[string]any{"home": movedHome}, "list": []any{}}
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))

	// The viewer, through the real registered route.
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	handler := buildProductionMiddlewareChain(api, mux)
	login := httptest.NewRecorder()
	api.HandleLogin(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"s3-person","password":"S3-Viewer-Binding-Passphrase-1"}`)))
	require.Equal(t, http.StatusOK, login.Code, "real person login: %s", login.Body.String())
	cookies := login.Result().Cookies()

	readViewer := func(t *testing.T) (string, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-log", nil)
		var csrf *http.Cookie
		for _, c := range cookies {
			switch c.Name {
			case middleware.CSRFCookieName, middleware.CSRFCookieNameHTTP:
				csrf = c
			case middleware.SessionCookieName:
				req.AddCookie(c)
			}
		}
		require.NotNil(t, csrf, "fixture: the login response must seed a CSRF cookie")
		req.AddCookie(csrf)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var bodyMap map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bodyMap), "body=%s", w.Body.String())
		status, _ := bodyMap["chain_status"].(string)
		return w.Body.String(), status
	}

	// Read 1, intact: the emitted event is listed and the chain verdict
	// covers the same file the records came from.
	body1, chain1 := readViewer(t)
	require.Contains(t, body1, s3ViewerMarker,
		"RED (S3/FR-103): the audit viewer shows an empty log after the live agents home changed — it reads "+
			"its homePath-derived file while the real audit logger writes %s; a permitted config update made "+
			"every audit entry disappear from the viewer", writerFile)
	require.Equal(t, "valid", chain1,
		"the integrity verdict must cover the displayed file (the logger's), not \"unknown\"; got chain_status=%q",
		chain1)

	// Read 2, tampered: flipping THE LOGGER'S file must flip the same read.
	f, err := os.OpenFile(writerFile, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"event":"s3-tamper-probe","detail":"appended outside the chain"}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	body2, chain2 := readViewer(t)
	require.Equal(t, "broken", chain2,
		"S3 binding: tampering the logger's file must flip the viewer's verdict — displayed records and "+
			"chain_status must come from the SAME source file (body still readable: %s)", body2)
	require.Contains(t, body2, s3ViewerMarker,
		"the emitted record stays listed on the tampered read")
}
