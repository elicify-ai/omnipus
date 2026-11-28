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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// recordingRegistrar records every registered route pattern (the sweep's
// "registered route list") while still serving handlers on the real mux.
type recordingRegistrar struct {
	mu     sync.Mutex
	mux    *http.ServeMux
	routes []string
}

func (r *recordingRegistrar) RegisterHTTPHandler(pattern string, h http.Handler) {
	r.mu.Lock()
	r.routes = append(r.routes, pattern)
	r.mu.Unlock()
	if r.mux != nil {
		r.mux.Handle(pattern, h)
	}
}

func (r *recordingRegistrar) registered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.routes...)
}

const pgPersonRefusal = "this action requires a signed-in person"

// Programs and agents share the /api/v1 server base (contracts/openapi.yaml::servers).
const (
	pgSaveRoute     = "/api/v1/programs/github/credential"
	pgListRoute     = "/api/v1/programs"
	pgSettingsRoute = "/api/v1/programs/settings"
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
	store    *credentials.Store // the fixture's real unlocked credential store
	// restartCalls counts HandleGatewayRestart's injected restart action —
	// the sweep probes the real restart route through this process-edge seam
	// (restAPI.restarter, production falls back to re-exec) instead of
	// skipping the route.
	restartCalls *atomic.Int32
	restartDone  chan struct{}
}

// restartRequests reports how many times the sweep's restart-route probes
// reached the (injected) restart action.
func (f *pgGateFixture) restartRequests() int32 {
	if f.restartCalls == nil {
		return 0
	}
	return f.restartCalls.Load()
}

func pgPersonAPI(t *testing.T) *pgGateFixture {
	t.Helper()
	f, _ := pgNewGateway(t, false)
	return f
}

// pgNewGateway builds the person-account gateway. With recordRoutes it
// registers through a recording registrar and returns every registered route
// pattern (the S-113 sweep drives "every registered HTTP route"). In the
// platform edition there is no password login, so the person's API bearer is
// minted directly into the account's token set (SEC-1) instead.
func pgNewGateway(t *testing.T, recordRoutes bool) (*pgGateFixture, []string) {
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

	// The real encrypted store, unlocked under the fixture master key and
	// wired the way production wires it (submit continuations write card
	// secrets through it).
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	require.NoError(t, credentials.Unlock(store), "instrument: the store unlocks under the fixture master key")
	t.Cleanup(func() { store.Close() })
	al.WireSysagentDeps(&systools.Deps{Home: tmpDir, GetCfg: al.GetConfig, CredStore: store})

	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
	}
	// S-113's sweep probes the real restart route; the process edge is the
	// injectable restarter seam (rest_gateway_restart.go::HandleGatewayRestart
	// falls back to re-exec in production). The stub counts invocations and
	// never re-execs the test binary.
	restarts := &atomic.Int32{}
	restartDone := make(chan struct{})
	var restartSignalOnce sync.Once
	api.restarter = func() { defer restartSignalOnce.Do(func() { close(restartDone) }); restarts.Add(1) }
	mux := http.NewServeMux()
	var routes []string
	if recordRoutes {
		rec := &recordingRegistrar{mux: mux}
		api.registerAdditionalEndpoints(rec)
		routes = rec.registered()
	} else {
		api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	}
	f := &pgGateFixture{t: t, api: api, handler: buildProductionMiddlewareChain(api, mux), store: store, restartCalls: restarts, restartDone: restartDone}

	login := httptest.NewRecorder()
	api.HandleLogin(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"pg-person","password":"pg-person-password"}`)))
	if login.Code == http.StatusNotFound {
		// Platform edition: no password login exists. Mint the person's own
		// API bearer straight into the account's token set instead.
		f.token = pgMintUserToken(t, api)
	} else {
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
	}
	// Instrument: the same mux and real identity authenticate an existing
	// route (bearer works in both editions).
	control := httptest.NewRequest(http.MethodGet, "/api/v1/settings/memory", nil)
	pgAttach(f, control, f.token, false, false)
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
	bin := pgFakeProgramDir(t)
	name := "gh"
	script := "#!/bin/sh\necho started >> " + f.ghStarts + "\n"
	if runtime.GOOS == "windows" {
		name += ".cmd"
		script = "@echo started >> " + f.ghStarts + "\r\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A credential save (PUT /programs/{id}/credential) and a recheck start
	// the entry's account check on a background goroutine
	// (programs.Service.launchLocked); its finish step writes
	// <home>/programs/connections.json and the program.check_result audit
	// row into this fixture's temp home. Cleanups run last-registered-first,
	// so registering the join HERE, after every t.TempDir above, makes it run
	// before those temp dirs are removed (and before the store, bus and loop
	// close). Without it a check still finishing while the test ends races
	// the TempDir RemoveAll ("directory not empty", CI run 46109810043,
	// D1_row10 under -race).
	t.Cleanup(func() { api.programService().Wait() })
	return f, routes
}

// pgMintUserToken appends an ID-tagged bearer token to the person account's
// token set (the SEC-1 set login itself appends to) and returns the raw
// token. Generation mirrors generateUserToken + login's hashing: the bcrypt
// input is config.TokenSecret(raw), the ID prefix is non-secret routing data.
func pgMintUserToken(t *testing.T, api *restAPI) string {
	t.Helper()
	idBytes := make([]byte, 4)
	body := make([]byte, 32)
	_, err := rand.Read(idBytes)
	require.NoError(t, err)
	_, err = rand.Read(body)
	require.NoError(t, err)
	raw := "omnipus_" + hex.EncodeToString(idBytes) + "_" + hex.EncodeToString(body)
	hash, err := bcrypt.GenerateFromPassword([]byte(config.TokenSecret(raw)), bcrypt.DefaultCost)
	require.NoError(t, err)
	require.NoError(t, api.agentLoop.MutateConfig(func(c *config.Config) error {
		for i := range c.Gateway.Users {
			if c.Gateway.Users[i].Username == "pg-person" {
				c.Gateway.Users[i].Tokens = append(c.Gateway.Users[i].Tokens, config.TokenEntry{
					ID: config.TokenIDFromRaw(raw), Hash: config.BcryptHash(hash),
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
				})
			}
		}
		return config.SaveConfig(api.configPath(), c)
	}))
	return raw
}

func (f *pgGateFixture) ghStarted() int {
	b, err := os.ReadFile(f.ghStarts)
	if err != nil {
		// Only a not-yet-created marker reads as zero. Any other read error is
		// an instrument fault, not evidence nothing started (F18).
		if !errors.Is(err, os.ErrNotExist) {
			require.Failf(f.t, "instrument: cannot read the gh start marker",
				"path=%q error=%v — a zero here would hide real starts", f.ghStarts, err)
		}
		return 0
	}
	return strings.Count(string(b), "started")
}

// ghStartedAtLeast is the positive control for the start marker: it proves the
// marker file is readable and being appended to before a zero-start claim is
// trusted (CHECK F18: a marker that cannot be seen must fail loudly, not read
// as zero).
func (f *pgGateFixture) ghMarkerReadable() {
	f.t.Helper()
	if _, err := os.Stat(f.ghStarts); errors.Is(err, os.ErrNotExist) {
		return // no starts recorded yet — legitimate zero state
	} else if err != nil {
		require.Failf(f.t, "instrument: cannot stat the gh start marker",
			"path=%q error=%v", f.ghStarts, err)
	}
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
// route refuses a stale revision), with the caller identity attached, and
// returns the recorder plus the number of body bytes the server consumed.
// CHECK R04: the revision is injected FIRST (the fixture helper reads and
// re-marshals the body — counting that read measured request construction,
// never the server); the helper's finished request is then re-wrapped with a
// fresh counter, so the count is exactly the bytes the SERVER consumed.
func pgAgentPUT(t *testing.T, f *pgGateFixture, body string, bearer string, withSession, csrf bool) (*httptest.ResponseRecorder, int64) {
	t.Helper()
	req := revisionedAgentMutationRequest(t, f.api, pgAgentsRoute, strings.NewReader(body))
	var read int64
	raw, err := io.ReadAll(req.Body)
	require.NoError(t, err, "instrument: the prepared fixture body re-reads")
	req.Body = io.NopCloser(&pgCountingBody{r: bytes.NewReader(raw), n: &read})
	req.Header.Set("Content-Type", "application/json")
	pgAttach(f, req, bearer, withSession, csrf)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w, read
}

// TestProgramsREST_AgentBodyCounter_Instrument proves the shared-agent body
// counter on known handlers (CHECK R04): a KNOWN NO-READ refusal reports
// zero through the same wrapper — the instrument can see a server that never
// decoded — and a bounded oversized-body probe shows the counter reports the
// SERVER's bounded read, never the sender's payload size.
func TestProgramsREST_AgentBodyCounter_Instrument(t *testing.T) {
	t.Run("no_read_handler_counts_zero", func(t *testing.T) {
		noRead := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden) // refuses without touching the body
		})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/dev", nil)
		var read int64
		req.Body = io.NopCloser(&pgCountingBody{r: strings.NewReader(`{"description":"x"}`), n: &read})
		w := httptest.NewRecorder()
		noRead.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Equal(t, int64(0), read, "instrument: a refusal that never reads reports zero")
	})

	t.Run("bounded_oversized_body_probe_reports_the_server_read", func(t *testing.T) {
		const bound = 64
		bounded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			dec := json.NewDecoder(io.LimitReader(r.Body, bound))
			var m map[string]any
			_ = dec.Decode(&m) // the bounded decode the D-8 row 4 column describes
			w.WriteHeader(http.StatusOK)
		})
		big := strings.Repeat("a", 10000)
		payload := `{"description":"` + big + `"}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/dev", nil)
		var read int64
		req.Body = io.NopCloser(&pgCountingBody{r: strings.NewReader(payload), n: &read})
		w := httptest.NewRecorder()
		bounded.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		require.Greater(t, read, int64(0), "the bounded handler did read")
		require.LessOrEqual(t, read, int64(bound),
			"instrument: the counter reports the server's bounded read, never the fixture's payload size (%d bytes sent)", len(payload))
	})
}

// pgAgentRecord reads the persisted dev record and decodes it to a
// field-agnostic JSON map — the before/after full-state oracle for the
// shared-route rows (CHECK F18: both-field/full-state compares, not one
// field).
func pgAgentRecord(t *testing.T, f *pgGateFixture) map[string]any {
	t.Helper()
	rec, err := agentstore.New(f.api.homePath).Get("dev")
	require.NoError(t, err, "instrument: the seeded dev record is readable")
	raw, merr := json.Marshal(rec)
	require.NoError(t, merr, "instrument: the record marshals")
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m), "instrument: the record decodes to a map")
	return m
}

func TestProgramsREST_PersonSessionOnly(t *testing.T) {
	saveBody := []byte(`{"value":"tok_evil_999999"}`)

	t.Run("D8_row1_browser_session_is_processed", func(t *testing.T) {
		f := pgPersonAPI(t)
		w, read := pgServe(t, f, http.MethodPut, pgSaveRoute, saveBody, "", true, true)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential", "S1 contract table / S-81 D-8 row 1")
		}
		require.Equal(t, http.StatusOK, w.Code, "the positive control is processed: %s", w.Body.String())
		require.Greater(t, read, int64(0), "a processed row reads the body")
	})

	t.Run("D8_row2_cli_token_refused_before_body", func(t *testing.T) {
		f := pgPersonAPI(t)
		// Own positive control, awaited through check completion before baseline.
		pgConnectGithub(t, f)
		canary := programstore.SeedCanary(t, f.store)
		before := programstore.Snapshot(t, f.store, canary)
		starts := f.ghStarted()
		w, read := pgServe(t, f, http.MethodPut, pgSaveRoute, saveBody, f.cliToken, false, true)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential", "S1 contract table / S-81 D-8 row 2")
		}
		require.Equal(t, http.StatusForbidden, w.Code, "a CLI token is not a person: %s", w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "the refusal is the person-session text")
		require.Equal(t, int64(0), read, "refused before the body")
		f.ghMarkerReadable()
		require.Greater(t, starts, 0,
			"instrument: this row's completed save started the fake gh — the start marker counts, so the zero delta below is a real zero")
		require.Equal(t, starts, f.ghStarted(), "the fake gh was not started by this refused row")
		require.Equal(t, before, programstore.Snapshot(t, f.store, canary))
	})

	t.Run("D8_row3_person_api_bearer_refused", func(t *testing.T) {
		f := pgPersonAPI(t)
		pgConnectGithub(t, f)
		canary := programstore.SeedCanary(t, f.store)
		before := programstore.Snapshot(t, f.store, canary)
		starts := f.ghStarted()
		// The person's OWN API bearer is still not a signed-in person
		// (requireSessionCookieAuth answers with the same refusal).
		w, read := pgServe(t, f, http.MethodPut, pgSaveRoute, saveBody, f.token, false, true)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential", "S1 contract table / S-81 D-8 row 3")
		}
		require.Equal(t, http.StatusForbidden, w.Code, "the person's own API bearer is refused: %s", w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
		require.Equal(t, int64(0), read, "refused before the body")
		require.Equal(t, starts, f.ghStarted(), "no check starts")
		require.Equal(t, before, programstore.Snapshot(t, f.store, canary))
	})

	t.Run("D8_row4_agent_tool_bearer_on_shared_agent_route_refused", func(t *testing.T) {
		f := pgPersonAPI(t)
		// D-8 row 4's caller is the agent/tool bearer class. The machine
		// credential slot holds exactly one entry, so the row mints a
		// SECOND, distinct, valid machine credential (row 2's value was
		// already refused): the refusal must follow the caller CLASS, never
		// the specific token value (CHECK F18). The shared route's body-read
		// column is "yes: bounded decode, no mutation" — the read is counted.
		agentToken := "omnipus_" + strings.Repeat("a", 64)
		require.NotEqual(t, agentToken, f.cliToken, "instrument: the row's credential is distinct from row 2's")
		require.NoError(t, f.api.agentLoop.MutateConfig(func(c *config.Config) error {
			c.Gateway.CLIToken = &config.TokenEntry{Hash: mustBcryptHash(t, agentToken)}
			return config.SaveConfig(f.api.configPath(), c)
		}), "instrument: the second machine credential is configured")
		require.NoError(t, f.api.agentLoop.GetConfig().Gateway.VerifyCLIToken(agentToken),
			"instrument: the row presents a valid machine credential")

		canary := programstore.SeedCanary(t, f.store)
		storeBefore := programstore.Snapshot(t, f.store, canary)
		before := pgAgentRecord(t, f)
		w, read := pgAgentPUT(t, f, `{"description":"x","programs":["github"]}`, agentToken, false, true)
		require.Equal(t, http.StatusForbidden, w.Code,
			"an agent/tool bearer carrying programs is refused on the shared route (FR-028): got %d %s",
			w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
		require.Greater(t, read, int64(0), "the shared route bounded-decodes the body (D-8 row 4)")
		require.Equal(t, before, pgAgentRecord(t, f),
			"unchanged — the full persisted record, not one field (CHECK F18)")
		require.Equal(t, storeBefore, programstore.Snapshot(t, f.store, canary))
	})

	t.Run("D8_row6_no_identity_401", func(t *testing.T) {
		f := pgPersonAPI(t)
		w, _ := pgServe(t, f, http.MethodGet, pgListRoute, nil, "", false, false)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "GET /programs", "S1 contract table / S-81 D-8 row 6")
		}
		require.Equal(t, http.StatusUnauthorized, w.Code,
			"no identity is withAuth's 401: %s", w.Body.String())
	})

	t.Run("D8_row8_browser_cookie_without_csrf_refused_by_csrf_middleware", func(t *testing.T) {
		f := pgPersonAPI(t)
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

		canary := programstore.SeedCanary(t, f.store)
		before := programstore.Snapshot(t, f.store, canary)
		w, read := pgServe(t, f, http.MethodPut, pgSettingsRoute,
			[]byte(`{"web_chat_allowed":true,"revision":1}`), "", true, false)
		require.Equal(t, withoutCSRF.Code, w.Code,
			"refused by the existing CSRF middleware with its own status: got %d %s", w.Code, w.Body.String())
		require.Equal(t, withoutCSRF.Body.String(), w.Body.String(),
			"refused with the CSRF middleware's own text")
		require.NotContains(t, w.Body.String(), pgPersonRefusal,
			"the refusal is the CSRF middleware's, not the person gate's")
		require.Zero(t, read, "CSRF refuses before body read")
		require.Equal(t, before, programstore.Snapshot(t, f.store, canary))
	})

	t.Run("D8_row12_person_bearer_on_shared_agent_route_refused_before_mutation", func(t *testing.T) {
		f := pgPersonAPI(t)
		// S-134 first clause: bounded decode, refusal BEFORE any mutation.
		canary := programstore.SeedCanary(t, f.store)
		storeBefore := programstore.Snapshot(t, f.store, canary)
		before := pgAgentRecord(t, f)
		w, read := pgAgentPUT(t, f, `{"description":"x","programs":["github"]}`, f.token, false, true)
		require.Equal(t, http.StatusForbidden, w.Code,
			"the person's own API bearer carrying programs is refused: got %d %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
		require.Greater(t, read, int64(0), "the shared route bounded-decodes the body")
		require.Equal(t, before, pgAgentRecord(t, f),
			"description and programs both unchanged — the full persisted record (CHECK F18)")
		require.Equal(t, storeBefore, programstore.Snapshot(t, f.store, canary))
	})

	t.Run("D8_row13_person_bearer_without_programs_still_works", func(t *testing.T) {
		f := pgPersonAPI(t)
		// S-134 second clause: regression — exactly as today. The description
		// change lands; nothing else in the record moves (in particular no
		// programs field appears).
		before := pgAgentRecord(t, f)
		w, read := pgAgentPUT(t, f, `{"description":"x"}`, f.token, false, true)
		require.Equal(t, http.StatusOK, w.Code, "processed as today: %s", w.Body.String())
		require.Greater(t, read, int64(0), "a processed row reads the body")
		after := pgAgentRecord(t, f)
		require.Equal(t, "x", after["description"], "the description change is applied")
		// The route's own concurrency bookkeeping (the revision and
		// updated_at bumps) and the requested description are the only
		// allowed differences; in particular no programs field may appear.
		delete(before, "description")
		delete(after, "description")
		delete(before, "revision")
		delete(after, "revision")
		delete(before, "updated_at")
		delete(after, "updated_at")
		require.Equal(t, before, after,
			"nothing outside the description (and the route's own bookkeeping) changed — no programs field appeared")
	})

	t.Run("D8_row14_program_owned_key_refused_by_store_guard", func(t *testing.T) {
		f := pgPersonAPI(t)
		// S-128 / D-8 row 14: the generic credentials route cannot overwrite
		// (or delete) a program-owned key. The reference is committed as
		// program-owned through I-3's one writer, then attacked through the
		// existing generic route with a real re-auth consent token. S-128's R
		// denotes GitHub's actual credential reference, not an unrelated name.
		const reference = "program.github"
		t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
		store := credentials.NewStore(filepath.Join(f.api.homePath, "credentials.json"))
		require.NoError(t, credentials.Unlock(store), "instrument: the store unlocks under the fixture master key")
		t.Cleanup(func() { store.Close() })
		_, err := store.CommitProgramCredential(reference, "tok_valid_12345",
			&credentials.CardReceipt{RequestID: "req-s128", SessionID: "sess-s128",
				References: []string{reference}, SubmittedAt: time.Now()})
		require.NoError(t, err, "instrument: the reference is committed as program-owned (I-3's one writer)")
		before := programstore.RequireOwned(t, store, reference, "tok_valid_12345", "req-s128", "sess-s128")

		user := &f.api.agentLoop.GetConfig().Gateway.Users[0]
		rw := doReAuth(f.api, user, "pg-person-password")
		require.Equal(t, http.StatusOK, rw.Code, "instrument: re-auth mints a consent token: %s", rw.Body.String())
		var reauth map[string]any
		require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &reauth))
		rtok, _ := reauth["token"].(string)
		require.NotEmpty(t, rtok, "instrument: the consent token is a string")

		setBody, err := json.Marshal(gen.CredentialSetRequest{Key: reference, Value: "tok_evil_999999"})
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
			reference+` belongs to the program GitHub. Change it in Settings → Programs.`,
			"the refusal is the spec message")

		delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/credentials/"+reference, nil)
		delReq.Header.Set(reAuthHeader, pgReauthTokenFor(t, f))
		pgAttach(f, delReq, "", true, true)
		dw := httptest.NewRecorder()
		f.handler.ServeHTTP(dw, delReq)
		require.Equal(t, http.StatusConflict, dw.Code,
			"DELETE of a program-owned key is refused with 409 too: got %d %s", dw.Code, dw.Body.String())

		got, gerr := store.Get(reference)
		require.NoError(t, gerr)
		require.Equal(t, "tok_valid_12345", got, "the stored value is unchanged")
		require.Equal(t, before, programstore.Snapshot(t, store, "req-s128"), "both generic attacks preserve value/kind/generation/metadata/receipt")
	})
}

// The shared route keeps its existing 1 MiB read bound (readAgentWriteBody).
// These are real mux requests, separate from the synthetic counter controls.
func TestProgramsREST_SharedRouteControls(t *testing.T) {
	t.Run("browser_programs_positive", func(t *testing.T) {
		f := pgPersonAPI(t)
		pgGithubPresetPresent(t, f)
		w, read := pgAgentPUT(t, f, `{"programs":["github"]}`, "", true, true)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Positive(t, read)
		require.Equal(t, []any{"github"}, pgDevPrograms(t, f))
	})
	t.Run("oversized_shared_route_read_is_bounded", func(t *testing.T) {
		f := pgPersonAPI(t)
		canary := programstore.SeedCanary(t, f.store)
		before := programstore.Snapshot(t, f.store, canary)
		agentBefore := pgAgentRecord(t, f)
		const bound = 1 << 20 // existing route bound, not the program-save cap
		payload := `{"programs":["github"],"description":"` + strings.Repeat("a", 2*bound) + `"}`
		w, read := pgAgentPUT(t, f, payload, f.token, false, true)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
		require.Positive(t, read)
		require.LessOrEqual(t, read, int64(bound+1), "one overflow byte, not an unbounded decode")
		require.Equal(t, agentBefore, pgAgentRecord(t, f))
		require.Equal(t, before, programstore.Snapshot(t, f.store, canary))
	})
}

// pgStandaloneAPI builds a gateway over the real route table with no
// configured accounts, for the D-8 callers that only exist without one
// (development bypass, legacy env token). It also puts the start-marker fake
// gh first on PATH and returns the start counter, so a "nothing started"
// claim is countable (CHECK F18): without a fake on PATH the claim would be
// vacuous — nothing COULD have started.
func pgStandaloneAPI(t *testing.T, bypass bool) (http.Handler, *restAPI, func() int) {
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

	// The start-marker fake gh (same instrument as pgNewGateway's fixture).
	marker := filepath.Join(t.TempDir(), "gh-starts")
	bin := pgFakeProgramDir(t)
	name := "gh"
	script := "#!/bin/sh\necho started >> " + marker + "\n"
	if runtime.GOOS == "windows" {
		name += ".cmd"
		script = "@echo started >> " + marker + "\r\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	starts := func() int {
		b, err := os.ReadFile(marker)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return 0
			}
			t.Fatalf("instrument: cannot read the gh start marker %q: %v", marker, err)
		}
		return strings.Count(string(b), "started")
	}
	return buildProductionMiddlewareChain(api, mux), api, starts
}

// TestProgramsREST_BypassAndLegacyEnvRefused covers D-8 rows 5 and 7 (S-81):
// the development bypass identity and the legacy OMNIPUS_BEARER_TOKEN
// fallback are not persons either. Both rows expect the person-session 403
// from the wrapped Programs routes.
func TestProgramsREST_BypassAndLegacyEnvRefused(t *testing.T) {
	t.Run("D8_row5_development_bypass_refused", func(t *testing.T) {
		handler, _, starts := pgStandaloneAPI(t, true)
		attachCSRF := func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: middleware.CSRFCookieNameHTTP, Value: "pg-bypass-csrf"})
			r.Header.Set(middleware.CSRFHeaderName, "pg-bypass-csrf")
		}
		control := httptest.NewRequest(http.MethodPost, "/api/v1/settings/memory", nil)
		attachCSRF(control)
		cw := httptest.NewRecorder()
		handler.ServeHTTP(cw, control)
		require.Equal(t, http.StatusMethodNotAllowed, cw.Code, "CSRF-matched bypass reaches existing GET-only handler: %s", cw.Body.String())
		if path, err := exec.LookPath("gh"); err != nil || path == "" {
			t.Fatalf("instrument: the fake gh is not on PATH (%v) — the nothing-started claim below would be vacuous", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/programs/github/check", nil)
		attachCSRF(req)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/check", "S1 contract table / S-81 D-8 row 5")
		}
		require.Equal(t, http.StatusForbidden, w.Code,
			"bypass mode is refused before anything starts: %s", w.Body.String())
		require.Contains(t, w.Body.String(), pgPersonRefusal, "same refusal text")
		require.Equal(t, 0, starts(),
			"D-8 row 5: nothing started (the fake gh is on PATH, so a start would have been counted)")
	})

	t.Run("D8_row7_legacy_env_token_refused", func(t *testing.T) {
		handler, _, _ := pgStandaloneAPI(t, false)
		t.Setenv("OMNIPUS_BEARER_TOKEN", "legacy-machine-secret")
		req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/telegram/programs",
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
