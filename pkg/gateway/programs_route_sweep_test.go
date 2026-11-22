// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: S-113 (route sweep — only the gated paths change a program
// credential) and S-128 (the generic credentials API refuses program-owned
// keys). The caller is the person's OWN API bearer token (D-8's sweep
// caller); the payload table is the spec's sweep table: route-specific valid
// payloads where the spec names them, and the generic
// {"key":"R","value":"tok_evil_999999"} elsewhere, for GET, POST, PUT and
// DELETE.
//
// Instrument: the routes come from a recording registrar over the REAL
// production registration (registerAdditionalEndpoints), never a hand
// list — a route that reaches a credential-store mutator (I-3's inventory:
// /api/v1/credentials, /api/v1/credentials/{key}, the secret-request submit,
// the Programs routes) without a payload row fails the test. The test runs
// once in local (core) and once in platform (hosted) edition mode.
//
// RED expectation: the generic credentials route has no program-owned guard
// yet, so the sweep's POST/DELETE really overwrite and delete R — the store
// assertion fails with the corruption in hand. The Programs rows are
// recorded-BLOCKED (no /programs routes registered yet).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// pgCommitProgramReference prepares the sweep's target: a real temp store
// where reference R holds tok_valid_12345 and is committed as program-owned
// through I-3's one writer (S-113's Given).
func pgCommitProgramReference(t *testing.T, f *pgGateFixture) *credentials.Store {
	t.Helper()
	require.NoError(t, f.store.Set("R", "tok_valid_12345"), "instrument: the reference exists")
	_, err := f.store.CommitProgramCredential("R", "tok_valid_12345",
		&credentials.CardReceipt{RequestID: "req-s113", SessionID: "sess-s113",
			References: []string{"R"}, SubmittedAt: time.Now()})
	require.NoError(t, err, "instrument: the reference is committed as program-owned (I-3's one writer)")
	return f.store
}

// pgReauthTokenFor mints a fresh local re-auth consent token (S-113: "a
// valid local re-auth token minted before each call"). In the platform
// edition there is no local re-auth; it returns "" and the sweep proceeds —
// the sweep's assertion is about the store, whatever the gate answers.
func pgReauthTokenFor(t *testing.T, f *pgGateFixture) string {
	t.Helper()
	user := &f.api.agentLoop.GetConfig().Gateway.Users[0]
	rw := doReAuth(f.api, user, "pg-person-password")
	if rw.Code != http.StatusOK {
		return ""
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &out))
	token, _ := out["token"].(string)
	return token
}

// pgSweepPath maps a registered pattern to the concrete probe path for the
// sweep table's Programs rows (their schemas).
func pgSweepPath(pattern, method string) string {
	switch {
	case method == http.MethodGet:
		if strings.Contains(pattern, "/channels/") {
			return "/channels/telegram/programs"
		}
		return "/programs"
	case method == http.MethodPost && strings.Contains(pattern, "/check"):
		return "/programs/github/check"
	case method == http.MethodPost:
		return "/programs"
	case method == http.MethodDelete:
		return "/programs/github"
	case strings.Contains(pattern, "/credential"):
		return "/programs/github/credential"
	case strings.Contains(pattern, "/settings"):
		return "/programs/settings"
	case strings.Contains(pattern, "/channels/"):
		return "/channels/telegram/programs"
	default:
		return "/programs/github"
	}
}

// pgSweepBody is the route-specific valid payload for a Programs route and
// method (the S1 contract table's schemas).
func pgSweepBody(method, path string) string {
	switch {
	case path == "/programs/github/credential":
		return `{"value":"tok_evil_999999"}`
	case path == "/programs/github/check":
		return `{}`
	case path == "/programs/settings":
		return `{"web_chat_allowed":true,"revision":1}`
	case path == "/channels/telegram/programs":
		return `{"allowed":true,"revision":1}`
	case path == "/programs":
		return `{"display_name":"Sweep","program_words":["sweepw"],` +
			`"token_variables":["SWEEP_TOKEN"],"network_sites":["api.sweep.invalid"]}`
	case path == "/programs/github":
		return `{"revision":1,"display_name":"Sweep"}`
	default:
		if method == http.MethodGet || method == http.MethodDelete {
			return ""
		}
		return `{"key":"R","value":"tok_evil_999999"}`
	}
}

// pgKnownMutatorRoute reports whether a registered route can reach a
// credential-store mutator from I-3's inventory — such a route MUST have a
// payload row, so a forgotten path fails the sweep (S-113's last And-clause).
func pgKnownMutatorRoute(pattern string) bool {
	return strings.HasPrefix(pattern, "/api/v1/credentials") ||
		strings.Contains(pattern, "secret-requests") ||
		strings.Contains(pattern, "programs")
}

// pgSweepServe fires one sweep probe with a hard 5-second deadline: a probe
// whose handler does not answer is recorded and the sweep moves on (the
// sweep's verdict is about the store, not about any single route's latency).
func pgSweepServe(t *testing.T, f *pgGateFixture, method, path, body, bearer, reauth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if reauth != "" {
		req.Header.Set(reAuthHeader, reauth)
	}
	pgAttach(f, req, bearer, false, false)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.handler.ServeHTTP(w, req)
	}()
	select {
	case <-done:
		return w
	case <-time.After(5 * time.Second):
		t.Logf("sweep probe %s %s did not answer within 5s (recorded, sweep continues)", method, path)
		return w
	}
}

func TestProgramCredential_RouteSweep(t *testing.T) {
	for _, edition := range []string{config.EditionCore, config.EditionHosted} {
		t.Run(edition, func(t *testing.T) {
			withEdition(t, edition)
			f, routes := pgNewGateway(t, true)
			store := pgCommitProgramReference(t, f)

			// The store's decrypted value and the config's programs section
			// are the sweep's baseline; both must survive every call.
			before, err := store.Get("R")
			require.NoError(t, err)
			require.Equal(t, "tok_valid_12345", before, "instrument: the baseline value is in place")
			cfgBefore := pgProgramsSection(t, f)

			reauth := pgReauthTokenFor(t, f)

			t.Logf("sweep: %d registered route patterns, edition %s", len(routes), edition)
			probes := 0
			handledMutator := map[string]bool{}
			for _, pattern := range routes {
				if strings.Contains(pattern, "restart") {
					// Instrument guard: POST /api/v1/gateway/restart re-execs
					// the process — in the test binary that restarts this very
					// test forever. Not in I-3's mutator inventory, so the
					// sweep table needs no row for it; the skip is recorded.
					t.Logf("sweep: skipping %q — the route re-execs the process (recorded, sweep continues)", pattern)
					continue
				}
				subtree := strings.HasSuffix(pattern, "/")
				base := strings.TrimSuffix(pattern, "/")
				for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
					var path, body string
					probeReauth := false
					switch {
					case strings.HasPrefix(pattern, "/api/v1/credentials") && method == http.MethodPost:
						// Sweep table: set with the program-owned key.
						handledMutator[pattern] = true
						path, body, probeReauth = "/api/v1/credentials", `{"key":"R","value":"tok_evil_999999"}`, true
					case strings.HasPrefix(pattern, "/api/v1/credentials") && method == http.MethodDelete:
						// Sweep table: delete the program-owned key.
						handledMutator[pattern] = true
						path, probeReauth = "/api/v1/credentials/R", true
					case strings.HasPrefix(pattern, "/api/v1/credentials"):
						continue // GET/PUT carry no mutator on this route
					case strings.Contains(pattern, "secret-requests") && method == http.MethodPost:
						// Submit with a pending program card where one can be
						// raised (S-112 covers the card rows in depth; at RED
						// no program card exists, and the sweep records it).
						handledMutator[pattern] = true // the payload row exists
						cardID, _, cardErr := pgRaiseProgramCard(t, f, "dev", map[string]any{"program": "github"})
						if cardErr != nil {
							t.Logf("BLOCKED (recorded, sweep continues): the program card cannot be raised — required by S-113's submit payload row (tool said: %v)", cardErr)
							continue
						}
						path = "/api/v1/secret-requests/" + cardID + "/submit"
						body = pgSweepBody(http.MethodPost, "")
					case strings.Contains(pattern, "programs"):
						// The Programs routes and the channel setting: their
						// schemas (at RED no such route is registered).
						handledMutator[pattern] = true
						path = pgSweepPath(pattern, method)
						body = pgSweepBody(method, path)
					default:
						// Every other registered route: GET, POST, PUT and
						// DELETE with the generic payload.
						path = base
						if subtree {
							path = base + "/sweep-probe"
						}
						if method == http.MethodGet || method == http.MethodDelete {
							body = ""
						} else {
							body = `{"key":"R","value":"tok_evil_999999"}`
						}
					}
					if path == "" {
						continue
					}
					callReauth := ""
					if probeReauth && reauth != "" {
						// A fresh consent token per call (S-113's Given).
						reauth = pgReauthTokenFor(t, f)
						callReauth = reauth
					}
					pgSweepServe(t, f, method, path, body, f.token, callReauth)
					probes++
				}
			}

			// The sweep-table completeness check (S-113's last And-clause):
			// a registered route that can reach a credential-store mutator
			// (I-3's inventory: the generic credentials routes, the
			// secret-request submit, the Programs routes) without a payload
			// row fails the sweep.
			for _, pattern := range routes {
				if pgKnownMutatorRoute(pattern) && !handledMutator[pattern] {
					t.Errorf("sweep table is missing a payload row for registered mutator route %q", pattern)
				}
			}

			require.Greater(t, probes, 0, "instrument: the sweep actually fired requests")

			// The sweep's verdict (S-113 Then): the decrypted store still
			// holds tok_valid_12345 under R, and the fake program started 0
			// times.
			after, aerr := store.Get("R")
			require.NoError(t, aerr, "the reference survives the sweep")
			require.Equal(t, "tok_valid_12345", after,
				"S-113: no route may change a program credential — the sweep must leave R untouched")
			require.Equal(t, 0, f.ghStarted(), "the fake program started 0 times")
			require.Equal(t, cfgBefore, pgProgramsSection(t, f), "program metadata unchanged")
		})
	}
}

// pgProgramsSection reads the config's programs section (any) — the sweep's
// program-metadata baseline. A missing section reads as nil.
func pgProgramsSection(t *testing.T, f *pgGateFixture) any {
	t.Helper()
	raw, err := os.ReadFile(f.api.configPath())
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg), "config.json parses")
	return cfg["programs"]
}
