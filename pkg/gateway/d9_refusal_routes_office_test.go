//go:build darwin || linux

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// T-103 / §3.4 D9, FR-053 (B-68): every settings route that saves through the
// shared config writer must answer a §3.2 program-folder refusal with 400 and
// the exact sentence, not the generic 500. One row per route, 20 rows in
// total. The overlap is written DIRECTLY into config.json after the fixture
// booted, so the published configuration lacks it — exactly the state
// `writeRefusableConfigJSONLocked` refuses. Each row uses a valid target, so
// the route reaches its save (a 404 would answer first and prove nothing).
//
// Expected per row: 400, body key `error` byte-equal to the §3.2 sentence,
// config.json byte-identical. A second case per site pins the other half of
// D9: a write failure that is NOT a refusal keeps today's 500 (the helper
// answers refusals only), driven here by a read-only config directory.
//
// Route 7 (configure) runs both D9 sub-cases: with no credential stored the
// refusal is a 400 (row "07_configure"); with a credential already stored the
// answer keeps the coded 500 `credential_saved_not_saved`, whose text gains
// the §3.2 sentence appended after the existing one (subtest below).
const d9TelegramConfig = `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"telegram":{"enabled":false}}}`

type d9Outcome struct {
	status     int
	body       []byte
	configPath string
	before     []byte
}

// d9SeedOverlap registers a trusted LibreOffice under the fixture home and
// writes an overlapping tools.allow_write_paths entry directly into
// config.json. Returns the exact §3.2 refusal sentence this state produces
// and the seeded file's bytes (the byte-identical oracle's baseline).
func d9SeedOverlap(t *testing.T, api *restAPI) (string, []byte) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "office")
	officepreview.TrustedOffice(t, api.homePath, root)
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	entry := officeAllowancePattern(resolved)
	path := api.configPath()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk), "instrument: the fixture config must be valid JSON")
	require.NoError(t, officeAllowWriteMutate(disk, entry))
	seeded, err := json.Marshal(disk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, seeded, 0o600))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(before), entry, "instrument: the overlap really sits on disk")
	return officepreview.ConfigEntryRefusal("tools.allow_write_paths", entry, "LibreOffice", resolved), before
}

// d9BreakConfigWrite makes the config write fail with a plain write error —
// not a refusal. It points the API's home at a copied, read-only directory:
// every read (config.json, workspaces, agent records) still resolves, and the
// writer cannot create its temp file. The credential store keeps its original
// path, so routes that store a secret before saving still reach the config
// write.
func d9BreakConfigWrite(t *testing.T, api *restAPI) {
	t.Helper()
	broke := filepath.Join(t.TempDir(), "broke")
	d9CopyDir(t, api.homePath, broke)
	require.NoError(t, os.Chmod(broke, 0o500))
	previous := api.homePath
	api.homePath = broke
	t.Cleanup(func() {
		_ = os.Chmod(broke, 0o700)
		api.homePath = previous
	})
}

// d9CopyDir copies a small fixture home recursively, preserving file modes.
func d9CopyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dst, 0o700))
	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			d9CopyDir(t, s, d)
			continue
		}
		info, err := entry.Info()
		require.NoError(t, err)
		raw, err := os.ReadFile(s)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(d, raw, info.Mode().Perm()))
	}
}

// d9ConfigureTelegram stores a token for a fresh telegram channel through the
// real configure route, so the channel is complete and enable/disable reach
// their save.
func d9ConfigureTelegram(t *testing.T, api *restAPI) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
		strings.NewReader(`{"token":"secret-1"}`))
	r.Header.Set("Content-Type", "application/json")
	api.configureChannel(w, r, "telegram")
	require.Equal(t, http.StatusOK, w.Code, "instrument: telegram must configure cleanly; body=%s", w.Body.String())
}

// d9RouteRow is one §3.4 D9 settings route. The table is outside the test
// function so that function stays within the size budget. Rows, assertions
// and subtest names are unchanged.
type d9RouteRow struct {
	name  string
	setup func(t *testing.T, refusalCase bool) (d9Outcome, string)
}

func d9SettingsRouteRowsEarly() []d9RouteRow {
	return []d9RouteRow{
		{"01_audit_log", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(`{"enabled":true}`))
			r.Header.Set("Content-Type", "application/json")
			api.HandleSandboxAuditLog(w, r.WithContext(adminCtx()))
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"02_channel_routing", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			addAgentsToAPI(t, api, []config.AgentConfig{{ID: "mia"}, {ID: "ray"}})
			writeTestWorkspaceJSON(t, api, "sales", "active", []string{"mia", "ray"}, false)
			seedChannelInstance(t, api, "whatsapp.eu")
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := setChannelRoutingReq(t, api, "whatsapp.eu", `{"workspace_id":"sales","default_agent_id":"ray"}`)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"03_channel_create", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := createChannelInstanceReq(t, api, `{"type":"whatsapp","slug":"eu"}`)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"04_channel_delete", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			f, _, _ := orderingWriterFixture(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, f.api)
			} else {
				d9BreakConfigWrite(t, f.api)
			}
			// Driven through the handler directly, like
			// channel_ordering_restart_test.go: the mux path is bypass-gated
			// (503 before the save) in this fixture.
			w := httptest.NewRecorder()
			f.api.deleteChannelInstance(w, "whatsapp")
			return d9Outcome{w.Code, w.Body.Bytes(), f.api.configPath(), before}, refusal
		}},
		{"05_channel_enable", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newChannelTestAPI(t, d9TelegramConfig)
			d9ConfigureTelegram(t, api)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := httptest.NewRecorder()
			api.setChannelEnabled(w, "telegram", true)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"06_channel_disable", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newChannelTestAPI(t, d9TelegramConfig)
			d9ConfigureTelegram(t, api)
			w := httptest.NewRecorder()
			api.setChannelEnabled(w, "telegram", true)
			require.Equal(t, http.StatusOK, w.Code, "instrument: enable must succeed before disable; body=%s", w.Body.String())
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w = httptest.NewRecorder()
			api.setChannelEnabled(w, "telegram", false)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"07_configure", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			// D9 route 7, sub-case (a): no credential stored by the request
			// ({}), so the refusal is a plain 400.
			api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{"whatsapp":{"enabled":false}}}`)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/whatsapp/configure", strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			api.configureChannel(w, r, "whatsapp")
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"08_rotate_token", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			require.NotNil(t, api.agentLoop, "instrument: the fixture wires the agent loop")
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/config/gateway/rotate-token", nil)
			api.rotateGatewayToken(w, withAdminRole(r))
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"09_god_mode", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			api.allowGodMode = true
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":true}`))
			r.Header.Set("Content-Type", "application/json")
			api.HandleGodMode(w, withReAuthAdmin(t, api, r))
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"10_mailbox_set", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newMailboxTestAPI(t, nil)
			seedWorkspaceFile(t, api.homePath, "ws_my")
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my",
				strings.NewReader(`{"enabled":true,"imap_host":"imap.x.com","smtp_host":"smtp.x.com","username":"me@x.com","password":"app-pass-123"}`))
			r.Header.Set("Content-Type", "application/json")
			api.setAgentMailbox(w, r, "mia", "ws_my")
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"11_mailbox_delete", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
				"mia": {"ws_my": {
					Enabled: true, WorkspaceID: "ws_my", IMAPHost: "i", SMTPHost: "s",
					Username: "u", PasswordRef: "mailbox_mia_ws_my_password",
				}},
			})
			require.NoError(t, api.credStore.Set("mailbox_mia_ws_my_password", "secret"))
			require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
				m["mailboxes"] = map[string]any{
					"mia": map[string]any{"ws_my": map[string]any{"enabled": true, "workspace_id": "ws_my"}},
				}
				return nil
			}))
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := httptest.NewRecorder()
			api.deleteAgentMailbox(w, "mia", "ws_my")
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
	}
}

func d9SettingsRouteRowsLate() []d9RouteRow {
	return []d9RouteRow{
		{"12_mcp_create", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			mcpURL := "https://127.0.0.1:1/sse"
			transport := gen.McpServerCreateTransportSse
			bodyBytes, err := json.Marshal(gen.McpServerCreate{Name: "my-sse-server", Transport: transport, Url: &mcpURL})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
			r.Header.Set("Content-Type", "application/json")
			api.HandleMCPServers(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"13_mcp_delete", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api, _ := newTestRestAPIWithHomeAndCredStore(t)
			created := createMCPServerForD9(t, api, "to-delete")
			require.NotEmpty(t, created, "instrument: the seed server was created")
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-servers/"+created, nil)
			api.HandleMCPServers(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"14_mcp_patch", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api, _ := newTestRestAPIWithHomeAndCredStore(t)
			created := createMCPServerForD9(t, api, "patch-env-srv")
			require.NotEmpty(t, created, "instrument: the seed server was created")
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			env := map[string]string{"NEW_KEY": "new-plaintext-value"}
			bodyBytes, err := json.Marshal(gen.McpServerUpdate{Env: &env})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPatch, "/api/v1/mcp-servers/patch-env-srv", bytes.NewReader(bodyBytes))
			r.Header.Set("Content-Type", "application/json")
			api.HandleMCPServers(w, r)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"15_prompt_guard", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := promptGuardPUT(t, api, "low")
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"16_provider_put", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/models"):
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
				case strings.HasSuffix(r.URL.Path, "/chat/completions"):
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"x","choices":[]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(upstream.Close)
			api := newProviderValidationTestAPI(t, "my-proxy", upstream.URL)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := doPutProvider(t, api, "my-proxy", `{"api_key":"sk-test"}`)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"17_rate_limits", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := putRateLimits(t, api, `{"max_agent_llm_calls_per_hour":100,"max_agent_tool_calls_per_minute":30}`)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"18_retention", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := retentionPUT(t, api, `{"session_days": 30, "disabled": false}`)
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"19_session_scope", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := sessionScopePUT(t, api, "main")
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
		{"20_skill_trust", func(t *testing.T, refusalCase bool) (d9Outcome, string) {
			api := newTestRestAPIWithHome(t)
			refusal, before := "", []byte(nil)
			if refusalCase {
				refusal, before = d9SeedOverlap(t, api)
			} else {
				d9BreakConfigWrite(t, api)
			}
			w := skillTrustPUT(t, api, "block_unverified")
			return d9Outcome{w.Code, w.Body.Bytes(), api.configPath(), before}, refusal
		}},
	}
}

func TestD9SettingsRoutesRefuseProgramFolderOverlap(t *testing.T) {
	rows := append(d9SettingsRouteRowsEarly(), d9SettingsRouteRowsLate()...)
	require.Len(t, rows, 20, "§3.4 D9: one row per route — the table must cover all 20")

	for _, row := range rows {
		t.Run(row.name+"/refusal_answers_400", func(t *testing.T) {
			out, refusal := row.setup(t, true)
			require.NotEmpty(t, refusal, "instrument: the row seeded the overlap and built the refusal sentence")
			require.Equal(t, http.StatusBadRequest, out.status,
				"T-103 §3.4 D9: a refused save answers 400 with the §3.2 text, not the generic failure; body=%s", out.body)
			var errBody struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(out.body, &errBody), "body=%s", out.body)
			require.Equal(t, refusal, errBody.Error,
				"T-103: the refusal text must be byte-equal to the §3.2 sentence, not a summary of it")
			after, err := os.ReadFile(out.configPath)
			require.NoError(t, err)
			require.NotNil(t, out.before, "instrument: the row captured the pre-save config bytes")
			require.Equal(t, out.before, after, "T-103: a refused save writes no config byte")
		})
		t.Run(row.name+"/non_refusal_keeps_500", func(t *testing.T) {
			out, _ := row.setup(t, false)
			require.Equal(t, http.StatusInternalServerError, out.status,
				"T-103 §3.4 D9: a write failure that is not a refusal keeps today's 500; body=%s", out.body)
			var errBody struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(out.body, &errBody), "body=%s", out.body)
			require.NotContains(t, errBody.Error, "agents can never get write access",
				"a non-refusal failure must not borrow the §3.2 sentence")
		})
	}

	// Route 7, sub-case (b): with a credential already stored by this
	// request, the answer keeps the coded 500 `credential_saved_not_saved`;
	// its text is the existing sentence, one space, then the §3.2 sentence.
	t.Run("07_configure/credential_stored_keeps_coded_500", func(t *testing.T) {
		api := newChannelTestAPI(t, d9TelegramConfig)
		refusal, _ := d9SeedOverlap(t, api)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/configure",
			strings.NewReader(`{"token":"secret-1"}`))
		r.Header.Set("Content-Type", "application/json")
		api.configureChannel(w, r, "telegram")
		require.Equal(t, http.StatusInternalServerError, w.Code,
			"D9 route 7b: with a stored credential the refusal keeps the coded 500; body=%s", w.Body.String())
		var body struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body=%s", w.Body.String())
		require.Equal(t, "credential_saved_not_saved", body.Code,
			"D9 route 7b: the code keeps its classification")
		require.Equal(t, configureCredentialSavedNotText+" "+refusal, body.Error,
			"D9 route 7b: the existing sentence, one space, then the exact §3.2 sentence")
	})
}

// createMCPServerForD9 seeds one stdio MCP server through the real POST route
// and returns its id.
func createMCPServerForD9(t *testing.T, api *restAPI, name string) string {
	t.Helper()
	command := "/bin/true"
	bodyBytes, err := json.Marshal(gen.McpServerCreate{Name: name, Transport: gen.McpServerCreateTransportStdio, Command: &command})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp-servers", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMCPServers(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "instrument: the seed server must create cleanly; body=%s", w.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	return created.ID
}
