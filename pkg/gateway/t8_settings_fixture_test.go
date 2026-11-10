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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const t8SettingsRoute = "/api/v1/settings/third-party-agents"

type t8SettingsFixture struct {
	api      *restAPI
	handler  http.Handler
	cookies  []*http.Cookie
	token    string
	auditDir string
}

func t8SettingsAPI(t *testing.T) *t8SettingsFixture {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	api, _ := newTestRestAPIWithUser(t, "t8-person", "T8-Settings-Test-Password")
	t.Setenv(config.EnvHome, api.homePath)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	f := &t8SettingsFixture{api: api, handler: buildProductionMiddlewareChain(api, mux)}
	f.auditDir = attachTestAuditor(t, api)
	login := httptest.NewRecorder()
	api.HandleLogin(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"t8-person","password":"T8-Settings-Test-Password"}`)))
	require.Equal(t, http.StatusOK, login.Code, "real person login: %s", login.Body.String())
	f.cookies = login.Result().Cookies()
	var out map[string]any
	require.NoError(t, json.Unmarshal(login.Body.Bytes(), &out))
	f.token, _ = out["token"].(string)
	found := false
	for _, c := range f.cookies {
		if c.Name == middleware.SessionCookieName {
			found = true
		}
	}
	require.True(t, found, "login must mint a real person session, not an injected role")
	// Instrument: the same mux and real cookie must authenticate an existing route.
	control := f.call(t, http.MethodGet, "/api/v1/settings/memory", nil, "person")
	require.Equal(t, http.StatusOK, control.Code, "positive mux/auth control: %s", control.Body.String())
	return f
}
func (f *t8SettingsFixture) call(t *testing.T, method, path string, body any, actor string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	// Supply the real issued CSRF pair even for the unauthenticated probes,
	// so a 403 can only come from the tested authority boundary, not CSRF.
	for _, c := range f.cookies {
		if c.Name == middleware.SessionCookieName && actor != "person" {
			continue
		}
		req.AddCookie(c)
		if c.Name == middleware.CSRFCookieName || c.Name == middleware.CSRFCookieNameHTTP {
			req.Header.Set(middleware.CSRFHeaderName, c.Value)
		}
	}
	if actor == "token" {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}
func t8SettingsBody(cli, path, preset string) map[string]any {
	return map[string]any{"clis": map[string]any{cli: map[string]any{"path": path, "preset_args": preset}}}
}
func (f *t8SettingsFixture) disk(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(f.api.configPath())
	require.NoError(t, err)
	return b
}
func (f *t8SettingsFixture) view(t *testing.T) map[string]any {
	t.Helper()
	w := f.call(t, http.MethodGet, t8SettingsRoute, nil, "person")
	require.Equal(t, http.StatusOK, w.Code, "r17 §3.3 Settings GET: %s", w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}
func t8SettingsRows(t *testing.T, view map[string]any) map[string]any {
	t.Helper()
	rows, ok := view["clis"].(map[string]any)
	require.True(t, ok, "view carries clis")
	require.Len(t, rows, 3)
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		require.Contains(t, rows, name)
	}
	return rows
}
func t8SettingsRow(t *testing.T, rows map[string]any, cli string) map[string]any {
	t.Helper()
	row, ok := rows[cli].(map[string]any)
	require.True(t, ok)
	return row
}
func (f *t8SettingsFixture) expectRejected(t *testing.T, body any, cli, field, code, check, at, want string) {
	t.Helper()
	before := f.disk(t)
	w := f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
	require.Equal(t, http.StatusBadRequest, w.Code, "r17 §3.4 exact rejection, not a missing route: %s", w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, code, out["code"])
	assert.Equal(t, "clis."+cli+"."+field, out["field"])
	assert.Equal(t, want, out["error"])
	details, ok := out["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, cli, details["cli"])
	assert.Equal(t, check, details["check"])
	if at != "" {
		t8AssertNoProgramPath(t, w.Body.Bytes(), at)
	}
	assert.Equal(t, before, f.disk(t), "no valid row or invalid row is written on refusal")
}
func t8Program(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nprintf 'T8 fixture never intentionally launched'\n"), mode))
	require.NoError(t, os.Chmod(path, mode))
	return path
}
