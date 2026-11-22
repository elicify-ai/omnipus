// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: S-81 (dataset D-8 rows 1–4, 6, 8, 12–14) and S-134 — the
// person-session gate over every route that can start a program or change a
// program credential, plus the shared /agents/{id} route when the request
// carries the new `programs` field. D-8 rows 5 and 7 need gateways whose
// auth is NOT account-configured (development bypass, legacy env token), so
// they run in TestProgramsREST_BypassAndLegacyEnvRefused (same file pack).
// D-8 rows 10–11 need a pending program card and live in
// TestProgramCard_PersonGate.
//
// Oracle: dataset D-8, row by row. The person gate itself exists (T11 built
// middleware.RequirePersonSession and requireSessionCookieAuth with the
// pinned refusal text); what S1 adds is the wrapped Programs routes and the
// programs-conditional gate on the shared agent route. A 404 on a Programs
// path is reported BLOCKED with the spec reference — never a skip (the wu5
// pack's routeMissing pattern).
//
// Body-read is observed with a counting reader wrapping the request body;
// the fake `gh` records every start so refused rows can prove the program
// was not started. Expected values come from the spec table, never from the
// handlers.

package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

const pgPersonRefusal = "this action requires a signed-in person"

// The Programs routes use bare /programs paths (the spec's new-path table);
// the agent route is the existing /api/v1 one.
const (
	pgSaveRoute     = "/programs/github/credential"
	pgListRoute     = "/programs"
	pgSettingsRoute = "/programs/settings"
	pgAgentsRoute   = "/api/v1/agents/dev"
)

// pgGateFixture is this pack's gateway: a real agent loop with a seeded dev
// agent (ADR-054: roster in the live config, entity record in the agent
// store), a real person account with a real login (cookies + API token), the
// production middleware chain over the real route table, and a machine CLI
// credential for the negative probes.
type pgGateFixture struct {
	t        *testing.T
	api      *restAPI
	handler  http.Handler
	cookies  []*http.Cookie
	token    string // the person's own API bearer token from the real login
	cliToken string
	ghStarts string
}

func pgPersonAPI(t *testing.T) *pgGateFixture {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	const password = "pg-person-password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, "pg-person", string(hash))

	devAgent := config.AgentConfig{
		ID: "dev", Name: "Dev", Type: config.AgentTypeCustom,
		Tools: coreagent.NewCustomAgentToolsCfg(),
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{devAgent},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	devForStore := devAgent
	require.NoError(t, agentstore.New(tmpDir).Create("dev", &devForStore),
		"instrument: the dev entity record persists (ADR-054)")

	api := &restAPI{agentLoop: al, homePath: tmpDir, allowedOrigin: "http://localhost:3000"}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	f := &pgGateFixture{t: t, api: api, handler: buildProductionMiddlewareChain(api, mux)}

	login := httptest.NewRecorder()
	api.HandleLogin(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"pg-person","password":"pg-person-password"}`)))
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
	// Instrument: the same mux and real cookie authenticate an existing route.
	control := httptest.NewRequest(http.MethodGet, "/api/v1/settings/memory", nil)
	for _, c := range f.cookies {
		control.AddCookie(c)
	}
	cw := httptest.NewRecorder()
	f.handler.ServeHTTP(cw, control)
	require.Equal(t, http.StatusOK, cw.Code, "positive mux/auth control: %s", cw.Body.String())

	// The machine credential the negative probes present.
	f.cliToken = "omnipus_" + strings.Repeat("c", 64)
	require.NoError(t, api.agentLoop.MutateConfig(func(c *config.Config) error {
		c.Gateway.CLIToken = &config.TokenEntry{Hash: mustBcryptHash(t, f.cliToken)}
		return config.SaveConfig(api.configPath(), c)
	}))
	require.NoError(t, api.agentLoop.GetConfig().Gateway.VerifyCLIToken(f.cliToken),
		"instrument: the negative probe presents a valid configured CLI token")

	// The fake gh records every start (D-8 row 2); FR-005 resolves the check
	// word on the gateway PATH, so the fake sits first on PATH.
	f.ghStarts = filepath.Join(t.TempDir(), "gh-starts")
	bin := t.TempDir()
	name := "gh"
	script := "#!/bin/sh\necho started >> " + f.ghStarts + "\n"
	if runtime.GOOS == "windows" {
		name += ".cmd"
		script = "@echo started >> " + f.ghStarts + "\r\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f *pgGateFixture) ghStarted() int {
	b, err := os.ReadFile(f.ghStarts)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "started")
}

// pgCountingBody counts how many bytes the SERVER consumed from the request
// body — the D-8 "body read" column's instrument.
type pgCountingBody struct {
	r io.Reader
	n *int64
}

func (b *pgCountingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	*b.n += int64(n)
	return n, err
}

// pgAttach applies the caller identity to a prepared request: bearer carries
// exactly that token; withSession attaches the real login cookies (and, with
// csrf, the matching CSRF header).
func pgAttach(f *pgGateFixture, req *http.Request, bearer string, withSession, csrf bool) {
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range f.cookies {
		if c.Name == middleware.SessionCookieName && !withSession {
			continue
		}
		req.AddCookie(c)
		if csrf && (c.Name == middleware.CSRFCookieName || c.Name == middleware.CSRFCookieNameHTTP) {
			req.Header.Set(middleware.CSRFHeaderName, c.Value)
		}
	}
}

// pgServe issues one request through the fixture's production middleware
// chain and real route table, returning the recorder and the number of body
// bytes the server consumed.
func pgServe(t *testing.T, f *pgGateFixture, method, path string, body []byte,
	bearer string, withSession, csrf bool,
) (*httptest.ResponseRecorder, int64) {
	t.Helper()
	var read int64
	var rdr io.Reader
	if body != nil {
		rdr = &pgCountingBody{r: bytes.NewReader(body), n: &read}
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	pgAttach(f, req, bearer, withSession, csrf)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w, read
}

// pgAgentPUT prepares a revisioned PUT /api/v1/agents/dev (the shared agent
// route refuses a stale revision), with the caller identity attached.
func pgAgentPUT(t *testing.T, f *pgGateFixture, body string, bearer string, withSession, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := revisionedAgentMutationRequest(t, f.api, pgAgentsRoute, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	pgAttach(f, req, bearer, withSession, csrf)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

// pgAgentDescription reads the persisted description of the seeded dev agent
// from the agent store (the record the shared route persists through).
func pgAgentDescription(t *testing.T, f *pgGateFixture) string {
	t.Helper()
	rec, err := agentstore.New(f.api.homePath).Get("dev")
	require.NoError(t, err, "instrument: the seeded dev record is readable")
	return rec.Description
}

func TestProgramsREST_PersonSessionOnly(t *testing.T) {
	f := pgPersonAPI(t)
	loginToken := f.token
	saveBody := []byte(`{"value":"tok_evil_999999"}`)

	t.Run("D8_row1_browser_session_is_processed", func(t *testing.T) {
		w, read := pgServe(t, f, http.MethodPut, pgSaveRoute, saveBody, "", true, true)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential", "S1 contract table / S-81 D-8 row 1")
		}
		require.Equal(t, http.StatusOK, w.Code, "the positive control is processed: %s", w.Body.String())
		require.Greater(t, read, int64(0), "a processed row reads the body")
	})

	t.Run("D8_row2_cli_token_refused_before_body", func(t *testing.T) {
		w, read := pgServe(t, f, http.MethodPut, pgSaveRoute, saveBody, f.cliToken, false, true)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential", "S1 contract table / S-81 D-8 row 2")
		}
		require.Equal(t, http.StatusForbidden, w.Code, "a CLI token is not a person: %s", w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "the refusal is the person-session text")
		require.Equal(t, int64(0), read, "refused before the body")
		require.Equal(t, 0, f.ghStarted(), "the fake gh was not started")
	})

	t.Run("D8_row3_person_api_bearer_refused", func(t *testing.T) {
		// The person's OWN API bearer is still not a signed-in person
		// (requireSessionCookieAuth answers with the same refusal).
		w, read := pgServe(t, f, http.MethodPut, pgSaveRoute, saveBody, loginToken, false, true)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential", "S1 contract table / S-81 D-8 row 3")
		}
		require.Equal(t, http.StatusForbidden, w.Code, "the person's own API bearer is refused: %s", w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
		require.Equal(t, int64(0), read, "refused before the body")
	})

	t.Run("D8_row4_cli_token_on_shared_agent_route_refused", func(t *testing.T) {
		before := pgAgentDescription(t, f)
		w := pgAgentPUT(t, f, `{"description":"x","programs":["github"]}`, f.cliToken, false, true)
		require.Equal(t, http.StatusForbidden, w.Code,
			"an agent/tool bearer carrying programs is refused on the shared route (FR-028): got %d %s",
			w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
		require.Equal(t, before, pgAgentDescription(t, f), "no mutation")
	})

	t.Run("D8_row6_no_identity_401", func(t *testing.T) {
		w, _ := pgServe(t, f, http.MethodGet, pgListRoute, nil, "", false, false)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "GET /programs", "S1 contract table / S-81 D-8 row 6")
		}
		require.Equal(t, http.StatusUnauthorized, w.Code,
			"no identity is withAuth's 401: %s", w.Body.String())
	})

	t.Run("D8_row8_browser_cookie_without_csrf_refused_by_csrf_middleware", func(t *testing.T) {
		// Instrument controls on an EXISTING write route: the same chain
		// accepts the CSRF-bearing write and refuses the one without the
		// header, so the row's refusal can only be the CSRF middleware's.
		ctlBody := []byte(`{"clis":{"claude-code":{"path":"","preset_args":""},` +
			`"codex":{"path":"","preset_args":""},"opencode":{"path":"","preset_args":""}}}`)
		withCSRF, _ := pgServe(t, f, http.MethodPut, "/api/v1/settings/third-party-agents", ctlBody, "", true, true)
		require.Equal(t, http.StatusOK, withCSRF.Code,
			"instrument: the CSRF-bearing control write is processed: %s", withCSRF.Body.String())
		withoutCSRF, _ := pgServe(t, f, http.MethodPut, "/api/v1/settings/third-party-agents", ctlBody, "", true, false)
		require.NotEqual(t, http.StatusOK, withoutCSRF.Code,
			"instrument: the control write without the CSRF header is refused")

		w, _ := pgServe(t, f, http.MethodPut, pgSettingsRoute,
			[]byte(`{"web_chat_allowed":true,"revision":1}`), "", true, false)
		require.Equal(t, withoutCSRF.Code, w.Code,
			"refused by the existing CSRF middleware with its own status: got %d %s", w.Code, w.Body.String())
		require.Equal(t, withoutCSRF.Body.String(), w.Body.String(),
			"refused with the CSRF middleware's own text")
		require.NotContains(t, w.Body.String(), pgPersonRefusal,
			"the refusal is the CSRF middleware's, not the person gate's")
	})

	t.Run("D8_row12_person_bearer_on_shared_agent_route_refused_before_mutation", func(t *testing.T) {
		// S-134 first clause: bounded decode, refusal BEFORE any mutation.
		before := pgAgentDescription(t, f)
		w := pgAgentPUT(t, f, `{"description":"x","programs":["github"]}`, loginToken, false, true)
		require.Equal(t, http.StatusForbidden, w.Code,
			"the person's own API bearer carrying programs is refused: got %d %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
		require.Equal(t, before, pgAgentDescription(t, f),
			"description and programs both unchanged (no mutation)")
	})

	t.Run("D8_row13_person_bearer_without_programs_still_works", func(t *testing.T) {
		// S-134 second clause: regression — exactly as today.
		w := pgAgentPUT(t, f, `{"description":"x"}`, loginToken, false, true)
		require.Equal(t, http.StatusOK, w.Code, "processed as today: %s", w.Body.String())
		require.Equal(t, "x", pgAgentDescription(t, f), "the description change is applied")
	})

	t.Run("D8_row14_program_owned_key_refused_by_store_guard", func(t *testing.T) {
		// S-128 / D-8 row 14: the generic credentials route cannot overwrite
		// (or delete) a program-owned key. The reference is committed as
		// program-owned through I-3's one writer, then attacked through the
		// existing generic route with a real re-auth consent token.
		t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
		store := credentials.NewStore(filepath.Join(f.api.homePath, "credentials.json"))
		require.NoError(t, credentials.Unlock(store), "instrument: the store unlocks under the fixture master key")
		t.Cleanup(func() { store.Close() })
		require.NoError(t, store.Set("R", "tok_valid_12345"), "instrument: the reference exists")
		_, err := store.CommitProgramCredential("R", "tok_valid_12345",
			&credentials.CardReceipt{RequestID: "req-s128", SessionID: "sess-s128",
				References: []string{"R"}, SubmittedAt: time.Now()})
		require.NoError(t, err, "instrument: the reference is committed as program-owned (I-3's one writer)")

		user := &f.api.agentLoop.GetConfig().Gateway.Users[0]
		rw := doReAuth(f.api, user, "pg-person-password")
		require.Equal(t, http.StatusOK, rw.Code, "instrument: re-auth mints a consent token: %s", rw.Body.String())
		var reauth map[string]any
		require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &reauth))
		rtok, _ := reauth["token"].(string)
		require.NotEmpty(t, rtok, "instrument: the consent token is a string")

		setBody, err := json.Marshal(gen.CredentialSetRequest{Key: "R", Value: "tok_evil_999999"})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/credentials", bytes.NewReader(setBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(reAuthHeader, rtok)
		pgAttach(f, req, "", true, true)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusConflict, w.Code,
			"a program-owned key is refused with 409 (S-128): got %d %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(),
			`R belongs to the program GitHub. Change it in Settings → Programs.`,
			"the refusal is the spec message")

		delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/credentials/R", nil)
		delReq.Header.Set(reAuthHeader, rtok)
		pgAttach(f, delReq, "", true, true)
		dw := httptest.NewRecorder()
		f.handler.ServeHTTP(dw, delReq)
		require.Equal(t, http.StatusConflict, dw.Code,
			"DELETE of a program-owned key is refused with 409 too: got %d %s", dw.Code, dw.Body.String())

		got, gerr := store.Get("R")
		require.NoError(t, gerr)
		require.Equal(t, "tok_valid_12345", got, "the stored value is unchanged")
	})
}

// pgStandaloneAPI builds a gateway over the real route table with no
// configured accounts, for the D-8 callers that only exist without one
// (development bypass, legacy env token).
func pgStandaloneAPI(t *testing.T, bypass bool) (http.Handler, *restAPI) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmp := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: bypass},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(tmp, "workspace"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096, MaxToolIterations: 8,
			},
			List: []config.AgentConfig{{ID: "dev", Name: "Dev", Type: config.AgentTypeCustom}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: config.OmnipusHomeDir(), allowedOrigin: "http://localhost:3000"}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return buildProductionMiddlewareChain(api, mux), api
}

// TestProgramsREST_BypassAndLegacyEnvRefused covers D-8 rows 5 and 7 (S-81):
// the development bypass identity and the legacy OMNIPUS_BEARER_TOKEN
// fallback are not persons either. Both rows expect the person-session 403
// from the wrapped Programs routes.
func TestProgramsREST_BypassAndLegacyEnvRefused(t *testing.T) {
	t.Run("D8_row5_development_bypass_refused", func(t *testing.T) {
		handler, _ := pgStandaloneAPI(t, true)
		req := httptest.NewRequest(http.MethodPost, "/programs/github/check", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/check", "S1 contract table / S-81 D-8 row 5")
		}
		require.Equal(t, http.StatusForbidden, w.Code,
			"bypass mode is refused before anything starts: %s", w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
	})

	t.Run("D8_row7_legacy_env_token_refused", func(t *testing.T) {
		handler, _ := pgStandaloneAPI(t, false)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "legacy-machine-secret")
		req := httptest.NewRequest(http.MethodPut, "/channels/telegram/programs",
			strings.NewReader(`{"allowed":true,"revision":1}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer legacy-machine-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /channels/telegram/programs", "S1 contract table / S-81 D-8 row 7")
		}
		require.Equal(t, http.StatusForbidden, w.Code,
			"the legacy env token is not a person: %s", w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
	})
}
