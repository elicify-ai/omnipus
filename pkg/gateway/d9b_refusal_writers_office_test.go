//go:build darwin || linux

package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/stretchr/testify/require"
)

// T-104 / §3.4 D9b, FR-053 (B-68, founder Q26 = A): every other config
// writer that hides the refusal reason answers the §3.2 program-folder
// refusal with 400 and the exact sentence. One row per D9b route (rows 21 to
// 34, fourteen), same seeding rule as T-103: the overlapping
// tools.allow_write_paths entry is written DIRECTLY into config.json after
// the fixture booted, so the published configuration lacks it.
//
// Helper rows (24 to 30 and 28): 400, error byte-equal to the §3.2 text,
// config.json byte-identical. The rows with their own rules carry their own
// assertions:
//
//	21 login      — right password: 400 with the text, no session cookie, no
//	                token stored; wrong password: still 401 (subtest).
//	22 logout     — 400 with the text (the refused sign-out leaves the
//	                server-side session valid; §3.3, unchanged).
//	23 change-pw  — valid current password: 400; wrong current: still 401.
//	31            — refused before commit: 400 and the provider survives.
//	32            — nothing lowered: 400; agents lowered and all restored:
//	                400 with every agent back at its old value (subtest).
//	33            — 400 "failed to configure signed-in provider: " + text.
//	34            — 400 ChannelOutboundRejection passing the generated shape,
//	                code program_access_refused, the fixed sentence.
//	30            — the workspace still exists.
//
// The cascade row proves FR-053's writers-with-no-answer rule: the
// removeMailboxesForWorkspace log line holds the refusal text.
const d9bSignInPrefix = "failed to configure signed-in provider: "

// d9bOutboundFixedText is D9b row 34's own fixed sentence: the
// ChannelOutboundRejection error field cannot carry the quoted entry (safe
// echo only), so the answer is this generic refusal instead of the §3.2
// sentence.
const d9bOutboundFixedText = "config error: config.json holds a write entry that would give agents write access to a document preview program folder; nothing was saved. Saving any other setting shows the entry."

// d9bCaptureLogs installs a buffer as the default slog sink and restores the
// previous one at cleanup; logsafe* writes reach slog.Default().
func d9bCaptureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

// d9bCascadeRefusal decodes the refusal attribute on the cascade log record.
// JSON decoding, not the text handler: the §3.2 sentence always contains
// quotes, and a text handler escapes those, so a raw search can never match.
// The compared value is the decoded string, byte-equal to the §3.2 text.
func d9bCascadeRefusal(t *testing.T, raw string) string {
	t.Helper()
	var seen []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var rec map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		var msg string
		if rawMsg, ok := rec["msg"]; ok {
			_ = json.Unmarshal(rawMsg, &msg)
		}
		if !strings.Contains(msg, "cascade mailboxes") {
			continue
		}
		seen = append(seen, msg)
		rawRefusal, ok := rec["refusal"]
		if !ok {
			continue
		}
		var decoded string
		require.NoError(t, json.Unmarshal(rawRefusal, &decoded),
			"FR-053 cascade: the refusal attribute must be a JSON string")
		return decoded
	}
	t.Fatalf("FR-053 cascade: removeMailboxesForWorkspace must log the §3.2 text in a refusal attribute; cascade records seen: %v", seen)
	return ""
}

// d9bBindMailbox drives the real PUT mailbox route so the (agent, workspace)
// pair is bound in memory and on disk — what the delete cascade identifies.
func d9bBindMailbox(t *testing.T, api *restAPI, agentID, workspaceID string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agentID+"/mailboxes/"+workspaceID,
		strings.NewReader(`{"enabled":true,"imap_host":"imap.x.com","smtp_host":"smtp.x.com","username":"me@x.com","password":"app-pass-123"}`))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, agentID, workspaceID)
	require.Equal(t, http.StatusOK, w.Code, "instrument: the mailbox must bind cleanly; body=%s", w.Body.String())
}

// d9bWriterRow is one §3.4 D9b writer. The table is outside the test
// function so that function stays within the size budget. Rows, assertions
// and subtest names are unchanged.
type d9bWriterRow struct {
	name  string
	setup func(t *testing.T) (d9Outcome, string)
}

func d9bWriterRows() []d9bWriterRow {
	return []d9bWriterRow{
		{"21_login", func(t *testing.T) (d9Outcome, string) {
			api, _ := newTestRestAPIWithUser(t, "d9buser", "Pass1111")
			refusal, before := d9SeedOverlap(t, api)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
				strings.NewReader(`{"username":"d9buser","password":"Pass1111"}`))
			r.Header.Set("Content-Type", "application/json")
			api.HandleLogin(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"22_logout", func(t *testing.T) (d9Outcome, string) {
			api, _ := newTestRestAPIWithUser(t, "d9bout", "Pass1111")
			// A real login first: it writes the session hash (the write the
			// logout will later refuse is the same browser-session branch).
			wl := httptest.NewRecorder()
			rl := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
				strings.NewReader(`{"username":"d9bout","password":"Pass1111"}`))
			rl.Header.Set("Content-Type", "application/json")
			api.HandleLogin(wl, rl)
			require.Equal(t, http.StatusOK, wl.Code, "instrument: the seed login must succeed; body=%s", wl.Body.String())
			cookies := parseCookiesFromRecorder(wl)
			session, ok := cookies[middleware.SessionCookieName]
			require.True(t, ok, "instrument: the seed login set the session cookie")
			refusal, before := d9SeedOverlap(t, api)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
			r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: session.Value})
			r = injectUser(r, "d9bout")
			api.HandleLogout(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"23_change_password", func(t *testing.T) (d9Outcome, string) {
			api, _ := newTestRestAPIWithUser(t, "d9bcp", "OldPass123")
			refusal, before := d9SeedOverlap(t, api)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password",
				strings.NewReader(`{"current_password":"OldPass123","new_password":"NewPass456"}`))
			r.Header.Set("Content-Type", "application/json")
			r = injectUser(r, "d9bcp")
			api.HandleChangePassword(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"24_context_settings", func(t *testing.T) (d9Outcome, string) {
			api, _ := newContextSettingsAPI(t)
			refusal, before := d9SeedOverlap(t, api)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context",
				strings.NewReader(`{"mcp_result_cap":40000}`))
			r.Header.Set("Content-Type", "application/json")
			api.HandleContextSettings(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"25_memory_settings", func(t *testing.T) (d9Outcome, string) {
			t.Setenv("OMNIPUS_BEARER_TOKEN", "")
			tmpDir := t.TempDir()
			t.Setenv("OMNIPUS_HOME", tmpDir)
			cfgJSON := `{"version":1,"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096}}}`
			require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(cfgJSON), 0o600))
			cfg := &config.Config{
				Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
				Agents: config.AgentsConfig{
					Defaults: config.AgentDefaults{Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
					List:     []config.AgentConfig{{ID: "test-agent", Name: "Test Agent", Type: config.AgentTypeCustom}},
				},
			}
			seedAgentEntities(t, tmpDir, cfg.Agents.List)
			al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
			api := &restAPI{agentLoop: al, homePath: tmpDir}
			refusal, before := d9SeedOverlap(t, api)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/memory",
				strings.NewReader(`{"auto_recap_enabled":true,"idle_timeout_minutes":45,"session_days":60}`))
			r.Header.Set("Content-Type", "application/json")
			api.HandleMemorySettings(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"26_third_party_settings", func(t *testing.T) (d9Outcome, string) {
			f := t8SettingsAPI(t)
			refusal, before := d9SeedOverlap(t, f.api)
			body := t8SettingsBody("codex", "/bin/sh", "--model a")
			w := f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
			return d9Outcome{w.Code, w.Body.Bytes(), f.api.configPath(), before}, refusal
		}},
		{"27_default_model", func(t *testing.T) (d9Outcome, string) {
			rows := []*config.ModelConfig{
				{Name: "before", Provider: "provider-a", Model: "model-before", APIBase: "https://a.example/v1", Protocol: "openai-compatible"},
				{Name: "after", Provider: "provider-b", Model: "model-after", APIBase: "https://b.example/v1", Protocol: "openai-compatible"},
			}
			api, _, _ := newDefaultModelAPI(t,
				config.DefaultModel{Provider: "provider-a", Model: "model-before"}, rows, &recordingLLMProvider{})
			refusal, before := d9SeedOverlap(t, api)
			w := doDefaultModel(api, http.MethodPut, `{"provider":"provider-b","model":"model-after"}`)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"28_integrations_save", func(t *testing.T) (d9Outcome, string) {
			api, user, cfg := newRolesTestAPI(t)
			web := map[string]any{}
			web["tavily"] = map[string]any{"enabled": true, "api_key_ref": "TAVILY_API_KEY"}
			web["brave"] = map[string]any{"enabled": true, "api_key_ref": "BRAVE_API_KEY"}
			writeRolesWebConfig(t, api, web)
			storeWebSearchKeys(t, api, "TAVILY_API_KEY", "BRAVE_API_KEY")
			wireRolesReload(t, cfg, api, nil, nil)
			refusal, before := d9SeedOverlap(t, api)
			w := putRoles(t, api, user, "brave", `{"kind":"search","fallback":true}`)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"29_onboarding_complete", func(t *testing.T) (d9Outcome, string) {
			withEdition(t, config.EditionHosted)
			api, _ := newAuthMethodOnboardingAPI(t)
			upstream := startFakeProviderUpstream(t)
			refusal, before := d9SeedOverlap(t, api)
			body := withProviderEndpoint(
				`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-pin-key","model":"gpt-4o"},`+
					`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`,
				upstream,
			)
			w := postOnboardingComplete(api, body)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"30_workspace_delete", func(t *testing.T) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			addAgentsToAPI(t, api, []config.AgentConfig{{ID: "mia"}, {ID: "ray"}})
			writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
			seedChannelInstance(t, api, "whatsapp.eu")
			bound := setChannelRoutingReq(t, api, "whatsapp.eu", `{"workspace_id":"sales","default_agent_id":"ray"}`)
			require.Equal(t, http.StatusOK, bound.Code,
				"instrument: the channel is bound to the workspace, so the unbind write runs; body=%s", bound.Body.String())
			refusal, before := d9SeedOverlap(t, api)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, "sales"), nil)
			r.URL.Path = "/api/v1/workspaces/sales"
			api.HandleWorkspaces(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"31_provider_delete", func(t *testing.T) (d9Outcome, string) {
			cfg := providerDeleteBaseConfig(
				config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
				[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
			)
			api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
			require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test-openrouter-secret"))
			refusal, before := d9SeedOverlap(t, api)
			w := doProviderDelete(t, api, "openrouter", "", nil, true)
			require.Contains(t, diskProviderIDs(t, tmpDir), "openrouter",
				"D9b 31: the refused delete commits nothing — the provider is still there")
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"32_performance", func(t *testing.T) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := d9SeedOverlap(t, api)
			w := mtiPutPerf(t, api, `{"max_parallel_agents":4}`)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"33_sign_in_poll", func(t *testing.T) (d9Outcome, string) {
			api := newProbeAPI(t)
			state := "signed_in"
			vendor := httptest.NewServer(deviceCodeVendorMux(t, &state))
			t.Cleanup(vendor.Close)
			withDeviceCodeVendor(t, vendor)
			// newProbeAPI pins hosted mode: both requests need the signed-in
			// operator, even before onboarding completes (release fbaad673f).
			const operator = "operator@example.com"
			startW := doJSONWithUser(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in", nil, operator)
			require.Equal(t, http.StatusOK, startW.Code, startW.Body.String())
			var startResp gen.SignInStartResponse
			require.NoError(t, json.Unmarshal(startW.Body.Bytes(), &startResp))
			refusal, before := d9SeedOverlap(t, api)
			w := doJSONWithUser(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/poll",
				gen.SignInPollRequest{DeviceAuthId: startResp.DeviceAuthId}, operator)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, d9bSignInPrefix + refusal
		}},
		{"34_channel_outbound", func(t *testing.T) (d9Outcome, string) {
			f, _ := t14Fixture(t)
			_, before := d9SeedOverlap(t, f.api)
			revision := t14Read(t, f, "discord").Revision
			require.NotEmpty(t, revision, "instrument: the fixture's outbound revision is readable")
			w := t14Put(t, f, "discord", "reply_only", []string{"222"}, revision)
			// §3.4 D9b row 34: code program_access_refused and the fixed sentence,
			// not the §3.2 text. That text cannot fit this body's safe-echo set.
			return d9Outcome{w.Code, w.Body.Bytes(), f.api.configPath(), before}, d9bOutboundFixedText
		}},
	}
}

func TestD9bWritersRefuseProgramFolderOverlap(t *testing.T) {
	rows := d9bWriterRows()
	require.Len(t, rows, 14, "§3.4 D9b: one row per route — the table must cover all 14 (rows 21 to 34)")

	for _, row := range rows {
		t.Run(row.name+"/refusal", func(t *testing.T) {
			out, expected := row.setup(t)
			require.NotEmpty(t, expected, "instrument: the row seeded the overlap")
			require.Equal(t, http.StatusBadRequest, out.status,
				"T-104 §3.4 D9b: a refused save answers 400 with the refusal text, not the generic failure; body=%s", out.body)
			var errBody struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(out.body, &errBody), "body=%s", out.body)
			require.Equal(t, expected, errBody.Error,
				"T-104: the answer must carry the expected refusal sentence byte-equal")
			after, err := os.ReadFile(out.configPath)
			require.NoError(t, err)
			require.NotNil(t, out.before, "instrument: the row captured the pre-save config bytes")
			require.Equal(t, out.before, after, "T-104: a refused save writes no config byte")
		})
	}

	// Row 21's own rule, second half: a WRONG password still answers on the
	// 401 path — the refusal must sit behind the credentials check, so only a
	// person with valid credentials ever sees the text.
	t.Run("21_login/wrong_password_still_401", func(t *testing.T) {
		api, _ := newTestRestAPIWithUser(t, "d9buser", "Pass1111")
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"d9buser","password":"wrong"}`))
		r.Header.Set("Content-Type", "application/json")
		api.HandleLogin(w, r)
		require.Equal(t, http.StatusUnauthorized, w.Code,
			"D9b 21: a wrong password answers 401 before any refusal; body=%s", w.Body.String())
		require.NotContains(t, w.Body.String(), "agents can never get write access",
			"the 401 path must not carry the refusal text")
	})

	// Row 21's first half, the session side: no session cookie is set and no
	// token is stored when the login's config write is refused.
	t.Run("21_login/no_session_token_or_cookie", func(t *testing.T) {
		api, tmpDir := newTestRestAPIWithUser(t, "d9buser", "Pass1111")
		_, before := d9SeedOverlap(t, api)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"d9buser","password":"Pass1111"}`))
		r.Header.Set("Content-Type", "application/json")
		api.HandleLogin(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		require.NotContains(t, parseCookiesFromRecorder(w), middleware.SessionCookieName,
			"D9b 21: a refused login sets no session cookie")
		users := loadDiskUsers(t, tmpDir)
		require.Len(t, users, 1)
		require.Empty(t, users[0]["session_token_hash"],
			"D9b 21: a refused login stores no session token on disk")
		after, err := os.ReadFile(api.configPath())
		require.NoError(t, err)
		require.Equal(t, before, after, "the refused login writes no config byte")
	})

	// Row 23's second half: a wrong CURRENT password still answers 401.
	t.Run("23_change_password/wrong_current_still_401", func(t *testing.T) {
		api, _ := newTestRestAPIWithUser(t, "d9bcp", "OldPass123")
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password",
			strings.NewReader(`{"current_password":"nope","new_password":"NewPass456"}`))
		r.Header.Set("Content-Type", "application/json")
		r = injectUser(r, "d9bcp")
		api.HandleChangePassword(w, r)
		require.Equal(t, http.StatusUnauthorized, w.Code,
			"D9b 23: a wrong current password answers 401 before any refusal; body=%s", w.Body.String())
		require.NotContains(t, w.Body.String(), "agents can never get write access",
			"the 401 path must not carry the refusal text")
	})

	// Row 32's own rule, second half: when agents were already lowered, the
	// rollback runs first and every agent must come back at its old value,
	// then the refusal is answered 400.
	t.Run("32_performance/agents_lowered_then_restored_400", func(t *testing.T) {
		api, _, _ := newMTIRollbackAPI(t) // global 300, agent-a own 250, agent-b own 280
		refusal, before := d9SeedOverlap(t, api)
		w := mtiPutPerf(t, api, mtiRollbackBody)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"D9b 32: agents lowered and all restored answers 400 with the §3.2 text; body=%s", w.Body.String())
		var errBody struct {
			Error string `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errBody), "body=%s", w.Body.String())
		require.Equal(t, refusal, errBody.Error, "the exact §3.2 sentence")
		require.EqualValues(t, 250, mtiStoredOwn(t, api, "agent-a"), "D9b 32: agent-a back at its old value")
		require.EqualValues(t, 280, mtiStoredOwn(t, api, "agent-b"), "D9b 32: agent-b back at its old value")
		require.EqualValues(t, 300, mtiDiskGlobal(t, api), "the global is untouched")
		after, err := os.ReadFile(api.configPath())
		require.NoError(t, err)
		require.Equal(t, before, after, "the refused save writes no config byte")
	})

	// Row 34's own rule: the body must pass the generated
	// ChannelOutboundRejection shape (no unknown fields), carry the new code
	// and the fixed sentence — status alone proves nothing here.
	t.Run("34_channel_outbound/body_passes_generated_schema", func(t *testing.T) {
		f, _ := t14Fixture(t)
		_, before := d9SeedOverlap(t, f.api)
		revision := t14Read(t, f, "discord").Revision
		w := t14Put(t, f, "discord", "reply_only", []string{"222"}, revision)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"D9b 34: the refused outbound change answers 400; body=%s", w.Body.String())
		decoder := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
		decoder.DisallowUnknownFields()
		var rejection gen.ChannelOutboundRejection
		require.NoError(t, decoder.Decode(&rejection),
			"D9b 34: the body must be exactly a ChannelOutboundRejection (generated schema); body=%s", w.Body.String())
		require.Equal(t, "program_access_refused", string(rejection.Code),
			"D9b 34: the rejection carries the program_access_refused code added to the contract")
		require.Equal(t, d9bOutboundFixedText, rejection.Error,
			"D9b 34: the fixed sentence, byte-equal")
		after, err := os.ReadFile(f.api.configPath())
		require.NoError(t, err)
		require.Equal(t, before, after, "the refused save writes no config byte")
	})

	// Row 30's own rule: the refused unbind aborts the delete, so the
	// workspace is NOT deleted — its record file still exists.
	t.Run("30_workspace_delete/workspace_still_exists", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		addAgentsToAPI(t, api, []config.AgentConfig{{ID: "mia"}, {ID: "ray"}})
		writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
		seedChannelInstance(t, api, "whatsapp.eu")
		bound := setChannelRoutingReq(t, api, "whatsapp.eu", `{"workspace_id":"sales","default_agent_id":"ray"}`)
		require.Equal(t, http.StatusOK, bound.Code, "instrument: bind first; body=%s", bound.Body.String())
		d9SeedOverlap(t, api)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, "sales"), nil)
		r.URL.Path = "/api/v1/workspaces/sales"
		api.HandleWorkspaces(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		require.FileExists(t, filepath.Join(api.homePath, "workspaces", "sales.json"),
			"D9b 30: the refused delete aborts — the workspace record still exists")
	})

	// FR-053's writers-with-no-answer rule: removeMailboxesForWorkspace logs
	// the refusal text (it holds no credential); the delete's response does
	// not change. The workspace has a bound mailbox but no channels, so the
	// unbind is a no-op, the delete proceeds, and the cascade's own write is
	// the one that refuses.
	t.Run("cascade_removeMailboxesForWorkspace_logs_refusal_text", func(t *testing.T) {
		logs := d9bCaptureLogs(t)
		api := newTestRestAPIWithHome(t)
		id := createWorkspaceViaAPI(t, api, "D9BCascade", "")
		addAgentsToAPI(t, api, []config.AgentConfig{{ID: "mia"}})
		seedWorkspaceFile(t, api.homePath, id)
		d9bBindMailbox(t, api, "mia", id)
		refusal, _ := d9SeedOverlap(t, api)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, id), nil)
		r.URL.Path = "/api/v1/workspaces/" + id
		api.HandleWorkspaces(w, r)
		require.Equal(t, http.StatusOK, w.Code,
			"FR-053 cascade: the delete's answer does not change on a best-effort cascade failure; body=%s", w.Body.String())
		require.Equal(t, refusal, d9bCascadeRefusal(t, logs.String()),
			"FR-053 cascade: the decoded refusal attribute is the §3.2 text, byte-equal")
		after, err := os.ReadFile(api.configPath())
		require.NoError(t, err)
		require.Contains(t, string(after), "mailboxes",
			"the refused cascade wrote no mailboxes removal (the entry survives on disk)")
	})
}
