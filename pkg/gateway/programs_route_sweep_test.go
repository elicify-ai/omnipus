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
// Route accounting (CHECK F03): every registered route is probed — including
// the restart routes. POST /api/v1/gateway/restart runs through the injected
// restAPI.restarter seam (production falls back to the real re-exec), so the
// sweep counts the restart instead of re-exec'ing the test binary and asserts
// the route was reached exactly once.
//
// Instrument: the routes come from a recording registrar over the REAL
// production registration (registerAdditionalEndpoints), never a hand list —
// a route that reaches a credential-store mutator (I-3's inventory:
// /api/v1/credentials, /api/v1/credentials/{key}, the secret-request submit,
// the Programs routes) without a payload row fails the test, and the
// completeness checker itself is proven with a planted missing row (F04
// negative control). A probe whose handler does not answer within 5 s fails
// the sweep and the handler goroutine is joined before any store read (F05).
// The test runs once in local (core) and once in platform (hosted) edition
// mode.
//
// Program-owned Given (CHECK F06): the fixture commits R through I-3's one
// writer (Store.CommitProgramCredential) and then VERIFIES the committed
// state — the value, generation 1 and the receipt referencing R. At RED the
// store methods are no-op stubs, so the verification fails loudly with a
// BLOCKED fatal naming the missing implementation; the sweep never runs
// against a target that is not actually program-owned.
//
// RED expectation: the BLOCKED fatal above fires first. (The pre-repair RED,
// on the pre-fix sweep, showed the store-corruption verdict: the generic
// credentials POST overwrote R because no program-owned guard existed.)

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
)

// pgCommitProgramReference prepares the sweep's target: the real store where
// reference R holds tok_valid_12345 committed as program-owned through I-3's
// one writer (S-113's Given: "a real temp store where GitHub is connected
// with tok_valid_12345 under reference R, generation 1"). The committed state
// is verified before any route is probed (CHECK F06): the value, the
// generation metadata and the receipt referencing R must all be readable
// back. At RED CommitProgramCredential is a no-op stub, so this fatal-BLOCKs
// naming it — never a skip.
func pgCommitProgramReference(t *testing.T, f *pgGateFixture) *credentials.Store {
	t.Helper()
	const receiptID = "req-s113"
	_, err := f.store.CommitProgramCredential("R", "tok_valid_12345",
		&credentials.CardReceipt{RequestID: receiptID, SessionID: "sess-s113",
			References: []string{"R"}, SubmittedAt: time.Now()})
	if err != nil {
		t.Fatalf("BLOCKED: Store.CommitProgramCredential refused the commit — required by invariant I-3/S-113's Given (error: %v)", err)
	}
	// What GREEN must have written (the stub's own documented contract: the
	// value, the optional CardReceipt and the program metadata with a new
	// generation, in one s.transact write; spec FR-043: a per-entry
	// generation, 1 on first connect).
	value, gerr := f.store.Get("R")
	if gerr != nil || value != "tok_valid_12345" {
		t.Fatalf("BLOCKED: the program-owned value is not in the store after CommitProgramCredential (get error: %v, value read back: %q) — the RED stub writes nothing; required by invariant I-3/S-113's Given", gerr, value)
	}
	gen1, ok := f.store.ProgramEntryGeneration("R")
	if !ok || gen1 != 1 {
		t.Fatalf("BLOCKED: Store.ProgramEntryGeneration(\"R\") returned (%d, %v) after the commit — no program metadata persisted (the RED stub always reports absent); required by FR-043/S-113's Given (generation 1)", gen1, ok)
	}
	receipt, found, rerr := f.store.CardReceipt(receiptID)
	if rerr != nil || !found {
		t.Fatalf("BLOCKED: the commit receipt %q is not readable back (found: %v, error: %v) — required by I-3's one-writer contract (the receipt is published in the SAME write as its values)", receiptID, found, rerr)
	}
	if len(receipt.References) != 1 || receipt.References[0] != "R" {
		t.Fatalf("BLOCKED: the commit receipt does not reference R (references: %v) — the sweep's target must be the committed program credential", receipt.References)
	}
	programstore.RequireOwned(t, f.store, "R", "tok_valid_12345", receiptID, "sess-s113")
	return f.store
}

// pgReauthTokenFor mints a fresh local re-auth consent token (S-113: "a valid
// local re-auth token minted before each call") and asserts the mint
// succeeded (CHECK F04: a silently-empty token would turn the re-auth probe
// into a no-credential probe). The mint endpoint works in both editions;
// only its enforcement is skipped in platform mode
// (rest_integrations_auth.go::requireReAuth).
func pgReauthTokenFor(t *testing.T, f *pgGateFixture) string {
	t.Helper()
	users := f.api.agentLoop.GetConfig().Gateway.Users
	require.NotEmpty(t, users, "instrument: the fixture has a user to re-authenticate")
	rw := doReAuth(f.api, &users[0], "pg-person-password")
	require.Equal(t, http.StatusOK, rw.Code, "the re-auth mint must succeed: %s", rw.Body.String())
	var out gen.ReAuthResponse
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &out), "the re-auth mint body parses")
	require.NotEmpty(t, out.Token, "the mint returned a consent token")
	return out.Token
}

// pgGenericBody is the sweep table's generic payload — the spec's sweep text
// probes every registered route without a route-specific row with
// {"key":"R","value":"tok_evil_999999"}, for GET, POST, PUT and DELETE
// alike. The spec's table names the generic credential writer "PUT
// /api/v1/credentials"; this tree registers it as POST (contracts/openapi.yaml).
// The sweep does NOT exempt the specified method: PUT /api/v1/credentials is
// probed with exactly the spec's payload, and the PUT-vs-POST discrepancy is
// the chief's contract reconciliation (test-disputes.md H, item 9).
func pgGenericBody() string { return `{"key":"R","value":"tok_evil_999999"}` }

// pgSweepTarget maps a registered pattern and method to the concrete probe
// (path, body) and whether the probe is a sweep-table ROW (a route-specific
// valid payload from the rev-8 contract, whose execution must be marked
// handled — never marked before the probe really fired). The Programs
// subtree pattern maps BY METHOD so its leaves are each exercised: the
// credential save, the check, the entry disconnect — never collapsed to one
// entry path (CHECK R09).
func pgSweepTarget(pattern, method string) (path, body string, isRow bool) {
	subtree := strings.HasSuffix(pattern, "/")
	base := strings.TrimSuffix(pattern, "/")
	generic := func(p string) (string, string, bool) {
		if subtree {
			p = base + "/sweep-probe"
		}
		return p, pgGenericBody(), false
	}
	switch {
	case pattern == "/api/v1/programs":
		switch method {
		case http.MethodGet:
			return "/programs", "", false
		case http.MethodPost:
			// the custom-program row (the create schema, S-113 "their schemas")
			return "/programs", `{"display_name":"Sweep","program_words":["sweepw"],` +
				`"token_variables":["SWEEP_TOKEN"],"network_sites":["api.sweep.invalid"]}`, true
		default:
			return generic("/programs")
		}
	case subtree && strings.Contains(pattern, "programs") && !strings.Contains(pattern, "/channels/"):
		// the {id} entry subtree: each leaf by method (CHECK R09's collapse fix)
		switch method {
		case http.MethodGet:
			return "/programs/github", "", false
		case http.MethodPut:
			// the credential save row (ProgramCredentialRequest)
			return "/programs/github/credential", `{"value":"tok_evil_999999"}`, true
		case http.MethodPost:
			// the check row
			return "/programs/github/check", `{}`, true
		default: // DELETE: the disconnect row
			return "/programs/github", "", true
		}
	case strings.Contains(pattern, "/channels/") && strings.Contains(pattern, "programs"):
		switch method {
		case http.MethodGet:
			return "/channels/telegram/programs", "", false
		case http.MethodPut:
			// the channel setting row (its boolean + revision schema)
			return "/channels/telegram/programs", `{"allowed":true,"revision":1}`, true
		default:
			return generic("/channels/telegram/programs")
		}
	case strings.Contains(pattern, "programs"): // settings and any other Programs route
		switch method {
		case http.MethodGet:
			return "/programs/settings", "", false
		case http.MethodPut:
			return "/programs/settings", `{"web_chat_allowed":true,"revision":1}`, true
		default:
			return generic("/programs/settings")
		}
	default:
		return generic(base)
	}
}

// pgKnownMutatorRoute is I-3's NAMED inventory — the routes the spec itself
// lists as credential-store mutators (the credentials API, the secret-request
// submit, the Programs routes). It is the base half of the completeness net
// only: the other half is OBSERVED writer reachability (pgSweepReached, the
// transaction instrument), so an arbitrarily named route that delegates to a
// store mutator is required to have a row too (CHECK R09). The restart
// routes change no credential state and are in neither half; they are still
// probed (F03), just without a payload row.
func pgKnownMutatorRoute(pattern string) bool {
	return strings.HasPrefix(pattern, "/api/v1/credentials") ||
		strings.Contains(pattern, "secret-requests") ||
		strings.Contains(pattern, "programs")
}

// pgRowHandled reports whether a route has ANY method's row marked — a row
// is marked only after its probe really fired with its route-specific
// payload (CHECK R09: coverage is the executed method/path/payload, never
// the pattern name).
func pgRowHandled(handled map[string]bool, pattern string) bool {
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		if handled[pattern+"|"+m] {
			return true
		}
	}
	return false
}

// pgMissingSweepRows returns the registered routes that need a payload row
// but have none: a route needs one when it is in I-3's named inventory OR
// the sweep's transaction instrument observed it reaching the store's
// transaction (the observed net catches arbitrary-name indirect writers).
// Rows in excused are acknowledged separately (fatal BLOCKED after the
// verdict) and do not double-report; a route that only READS through a
// transaction is excused with that reason at GREEN, loudly (CHECK R09).
func pgMissingSweepRows(routes []string, handled map[string]bool, excused map[string]string, reached map[string]bool) []string {
	var missing []string
	for _, p := range routes {
		if (pgKnownMutatorRoute(p) || reached[p]) && !pgRowHandled(handled, p) && excused[p] == "" {
			missing = append(missing, p)
		}
	}
	return missing
}

// pgProbeOutcome is one probe's result for the caller's verdicts: the
// deadline fired before the handler answered, and whether the handler
// goroutine was joined (after its request work was cancelled) before the
// probe returned (CHECK R10).
type pgProbeOutcome struct {
	timedOut    bool
	handlerDone bool
}

// pgServeProbe fires one probe with a hard deadline on the given handler. It
// never fails the test — the outcome returns to the caller (the sweep turns
// a timeout into t.Errorf; the planted instrument asserts detection). A
// timed-out probe has its REQUEST WORK CANCELLED first and its handler
// goroutine JOINED before returning, so a still-live probe can neither keep
// modifying state nor outlive the fixture (CHECK F05 + CHECK R10).
func pgServeProbe(h http.Handler, prepared *http.Request, deadline time.Duration) (*httptest.ResponseRecorder, *pgProbeOutcome) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // cancels any still-running probe work when we return
	prepared.Header.Set("Content-Type", "application/json")
	req := prepared.WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(w, req)
	}()
	out := &pgProbeOutcome{}
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case <-done:
		out.handlerDone = true
		return w, out
	case <-timer.C:
		out.timedOut = true
		cancel() // cancel the probe's request work before joining it
		select {
		case <-done:
			out.handlerDone = true
		case <-time.After(10 * time.Second):
			out.handlerDone = false
		}
		return w, out
	}
}

// pgSweepServe fires one sweep probe with a hard 5-second deadline. A probe
// whose handler does not answer FAILS the sweep — a hanging credential-route
// handler is itself a defect, not a latency note — and the handler goroutine
// is joined (request work cancelled first) before returning, so the final
// store reads never race a live handler (CHECK F05; CHECK R10 adds the
// cancellation). If the goroutine cannot be joined even after the grace
// period the test fatals: nothing may be asserted about a store a handler
// may still be writing.
func pgSweepServe(t *testing.T, f *pgGateFixture, method, path, body, bearer, reauth string) *httptest.ResponseRecorder {
	t.Helper()
	return pgSweepServeOn(t, f, f.handler, method, path, body, bearer, reauth)
}

// pgSweepServeOn fires one sweep probe at an explicit handler (the planted
// arbitrary-name writer rides a wrapper mux over the production table).
func pgSweepServeOn(t *testing.T, f *pgGateFixture, h http.Handler, method, path, body, bearer, reauth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if reauth != "" {
		req.Header.Set(reAuthHeader, reauth)
	}
	pgAttach(f, req, bearer, false, false)
	w, out := pgServeProbe(h, req, 5*time.Second)
	if out.timedOut {
		t.Errorf("sweep probe %s %s did not answer within 5s (S-113) — the sweep fails and joins the handler before any store read", method, path)
	}
	if !out.handlerDone {
		t.Fatalf("sweep probe %s %s handler still running 15s after the probe — the store verdict cannot be taken", method, path)
	}
	return w
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
			canary := programstore.SeedCanary(t, store)
			stateBefore := programstore.Snapshot(t, store, "req-s113", canary)
			var pendingCardIDs []string

			// A valid local re-auth token is minted before EACH mutator call
			// (S-113's Given); every mint is asserted (F04).
			reauth := pgReauthTokenFor(t, f)

			t.Logf("sweep: %d registered route patterns, edition %s", len(routes), edition)
			probes := 0
			handledMutator := map[string]bool{} // keyed pattern|method, set only AFTER the probe fired
			// The OBSERVED reachability net (CHECK R09): the transaction
			// instrument attributes every store-transaction engagement to the
			// route being probed, so a route the named inventory missed but
			// that demonstrably reaches the store still needs a payload row.
			reached := map[string]bool{}
			var currentRoute string
			credentials.TransactHook = func(string) {
				if currentRoute != "" {
					reached[currentRoute] = true
				}
			}
			t.Cleanup(func() { credentials.TransactHook = nil })
			// blockedPayloadRows records mutator routes whose payload row
			// could not be established (at RED the program card cannot be
			// raised). They are excused from the completeness verdict and
			// fatal-BLOCK after the sweep's own verdict (CHECK F04: never
			// marked handled before the row really exists, never a silent
			// skip).
			blockedPayloadRows := map[string]string{}
			for _, pattern := range routes {
				subtree := strings.HasSuffix(pattern, "/")
				for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
					var path, body string
					isRow := false
					probeReauth := false
					switch {
					case strings.HasPrefix(pattern, "/api/v1/credentials") && method == http.MethodPost && !subtree:
						// Sweep table: set with the program-owned key.
						path, body, probeReauth = "/api/v1/credentials", pgGenericBody(), true
						isRow = true
					case strings.HasPrefix(pattern, "/api/v1/credentials") && method == http.MethodPost && subtree:
						// The subtree pattern covers /credentials/rotate — a
						// registered route with its own payload row
						// (re-encrypt-only in I-3's classification table;
						// values unchanged are asserted by the final verdict).
						path, body, probeReauth = "/api/v1/credentials/rotate", `{}`, true
						isRow = true
					case strings.HasPrefix(pattern, "/api/v1/credentials") && method == http.MethodDelete && !subtree:
						// Sweep table: delete the program-owned key.
						path, body, probeReauth = "/api/v1/credentials/R", "", true
						isRow = true
					case strings.Contains(pattern, "secret-requests") && method == http.MethodPost:
						// Submit with a pending program card where one can be
						// raised (S-112 covers the card rows in depth). The
						// row exists only when the card really exists — never
						// marked handled before the raise succeeds (F04). The
						// body uses the card's DECLARED field key and the
						// generated Values shape, never a guessed one.
						cardID, sid, cardErr := pgRaiseProgramCard(t, f, "dev", map[string]any{"program": "github"})
						if cardErr != nil {
							blockedPayloadRows[pattern] = fmt.Sprintf("the program card cannot be raised — required by S-113's submit payload row (tool said: %v)", cardErr)
							continue
						}
						pendingCardIDs = append(pendingCardIDs, cardID)
						fieldKey := pgCardFieldKey(t, f, "pg-person", sid, cardID)
						submitBody, merr := json.Marshal(gen.SecretSubmitRequest{
							Values: &map[string]string{fieldKey: "tok_evil_999999"},
						})
						require.NoError(t, merr, "the submit body marshals")
						path = "/api/v1/secret-requests/" + cardID + "/submit"
						body = string(submitBody)
						probeReauth = true
						isRow = true
					default:
						// Everything else — the credentials GET/PUT probes are
						// NOT exempted (CHECK R09: the spec's generic-credential
						// writer is "PUT /api/v1/credentials", so PUT is probed
						// with exactly that payload; the PUT-vs-POST discrepancy
						// is the chief's reconciliation, disputes H item 9).
						// The Programs routes map BY METHOD (their schemas);
						// the restart routes ride the fixture's injected
						// restarter seam (F03).
						path, body, isRow = pgSweepTarget(pattern, method)
					}
					if path == "" {
						continue
					}
					callReauth := ""
					if probeReauth {
						// A fresh consent token per call (S-113's Given), the
						// mint asserted successful (F04).
						reauth = pgReauthTokenFor(t, f)
						callReauth = reauth
					}
					currentRoute = pattern
					pgSweepServe(t, f, method, path, body, f.token, callReauth)
					currentRoute = ""
					probes++
					// The row is covered only now — after its probe really
					// executed its route-specific method/path/payload.
					if isRow {
						handledMutator[pattern+"|"+method] = true
					}
				}
			}

			// The sweep-table completeness check (S-113's last And-clause):
			// a registered route that can reach a credential-store mutator —
			// by I-3's named inventory OR by observed store-transaction
			// engagement — without a payload row fails the sweep. Blocked
			// rows are excused here and fatal-BLOCK after the verdict below.
			missing := pgMissingSweepRows(routes, handledMutator, blockedPayloadRows, reached)
			require.Empty(t, missing, "sweep table is missing payload rows for registered mutator routes: %v", missing)

			// Instrument proof (F04 negative control): the completeness
			// checker fires on a PLANTED route that has no row — a checker
			// that can never fire proves nothing.
			const plantedRow = "/api/v1/credentials/planted-sweep-row"
			planted := append(append([]string(nil), routes...), plantedRow)
			plantedReached := map[string]bool{plantedRow: true}
			require.Contains(t, pgMissingSweepRows(planted, handledMutator, blockedPayloadRows, plantedReached), plantedRow,
				"instrument: the completeness check catches a planted missing payload row")

			require.Greater(t, probes, 0, "instrument: the sweep actually fired requests")

			// F03 route accounting (CHECK R10): the restart route was probed
			// through the process-edge seam and the injected restart action
			// ran exactly once. The seam is ASYNCHRONOUS —
			// HandleGatewayRestart acknowledges, returns, and a goroutine
			// invokes the restart action after its drain delay — so the sweep
			// AWAITS the callback through the seam before accounting, never
			// samples it once. Zero after the await means the sweep never
			// reached the real route.
			awaitDeadline := time.Now().Add(10 * time.Second)
			for f.restartRequests() == 0 {
				if time.Now().After(awaitDeadline) {
					t.Fatalf("POST /api/v1/gateway/restart never reached the restart action within 10s of the sweep — the asynchronous restart callback was not awaited/joined (CHECK R10)")
				}
				time.Sleep(50 * time.Millisecond)
			}
			require.Equal(t, int32(1), f.restartRequests(),
				"POST /api/v1/gateway/restart reached the restart action exactly once")

			// The sweep's verdict (S-113 Then): the decrypted store still
			// holds tok_valid_12345 under R, and the fake program started 0
			// times.
			after, aerr := store.Get("R")
			require.NoError(t, aerr, "the reference survives the sweep")
			require.Equal(t, "tok_valid_12345", after,
				"S-113: no route may change a program credential — the sweep must leave R untouched")
			require.Equal(t, 0, f.ghStarted(), "the fake program started 0 times")
			require.Equal(t, cfgBefore, pgProgramsSection(t, f), "program metadata unchanged")
			require.Equal(t, stateBefore, programstore.Snapshot(t, store, "req-s113", canary), "sweep preserves values/kinds/selections/generations/metadata/bound receipt")
			for _, id := range pendingCardIDs {
				_, found, err := store.CardReceipt(id)
				require.NoError(t, err)
				require.False(t, found, "forbidden submit cannot publish receipt %s", id)
			}

			// Blocked payload rows are loud, never silent (F04): after the
			// sweep's own verdict, an unestablishable row fails the test with
			// its reason.
			if len(blockedPayloadRows) > 0 {
				names := make([]string, 0, len(blockedPayloadRows))
				for p, reason := range blockedPayloadRows {
					names = append(names, p+": "+reason)
				}
				sort.Strings(names)
				t.Fatalf("BLOCKED: %d sweep payload row(s) could not be established — required by S-113's payload table:\n%s",
					len(blockedPayloadRows), strings.Join(names, "\n"))
			}
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

// TestProgramCredential_SweepProbe_Instrument proves the probe deadline
// machinery on a PLANTED delayed handler (CHECK R10): the timeout is
// detected, the timed-out probe's request work is CANCELLED before the join,
// and the handler goroutine is JOINED before the probe returns — the exact
// path the sweep's 5 s deadline takes, exercised in milliseconds instead of
// seconds.
func TestProgramCredential_SweepProbe_Instrument(t *testing.T) {
	var cancelled atomic.Bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler answers only after its 300 ms delay — past the probe's
		// 100 ms deadline — unless its request work is cancelled first.
		select {
		case <-r.Context().Done():
			cancelled.Store(true)
		case <-time.After(300 * time.Millisecond):
		}
		w.WriteHeader(http.StatusOK)
	})
	w, out := pgServeProbe(h, httptest.NewRequest(http.MethodPost, "/planted-slow-probe", nil), 100*time.Millisecond)
	require.True(t, out.timedOut, "instrument: a delayed handler is detected at the deadline")
	require.True(t, out.handlerDone,
		"instrument: the delayed handler's goroutine is joined before the probe returns (request work cancelled first)")
	require.True(t, cancelled.Load(),
		"instrument: the timed-out probe's request work was cancelled before the join")
	require.Equal(t, http.StatusOK, w.Code, "instrument: the planted handler answered after the cancellation")
}

// TestProgramCredential_RouteSweep_PlantedIndirectWriter is CHECK R09's
// plant: a TRULY REGISTERED route with an arbitrary name outside every name
// predicate, whose handler delegates DIRECTLY to the credential store's
// mutator (the failure scenario's indirect writer). The sweep's observed
// reachability net must catch it: the transaction instrument shows the probe
// really wrote through the store, and the completeness check requires a
// payload row for the route despite its name — and stops requiring one once
// its row is attempted. At RED the store mutator is a no-op stub and fires
// no transaction, so the plant fatal-BLOCKs naming the instrument.
func TestProgramCredential_RouteSweep_PlantedIndirectWriter(t *testing.T) {
	f, routes := pgNewGateway(t, true)

	// The plant: an arbitrary name (no credentials/secret-requests/programs
	// substring) on a wrapper mux over the production table; its handler is
	// an indirect store writer whose route-specific payload is its own
	// declared schema.
	const plant = "/api/v1/rename-me-sweep-writer-7f3a"
	wrapper := http.NewServeMux()
	wrapper.HandleFunc(http.MethodPost+" "+plant, func(w http.ResponseWriter, r *http.Request) {
		if err := f.store.Set("PLANT_RW", "x"); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	wrapper.Handle("/", f.handler)

	reached := map[string]bool{}
	credentials.TransactHook = func(string) { reached[plant] = true }
	t.Cleanup(func() { credentials.TransactHook = nil })

	w := pgSweepServeOn(t, f, wrapper, http.MethodPost, plant,
		`{"reference":"R","value":"tok_evil_999999"}`, f.token, "")
	require.Equal(t, http.StatusOK, w.Code,
		"the planted writer's probe succeeded through the wrapper mux: %s", w.Body.String())
	if !reached[plant] {
		t.Fatalf("BLOCKED: the transaction instrument did not observe the planted writer — " +
			"the store mutator ran no s.transact stage (Store.Set), so writer reachability " +
			"cannot be observed; S-113's completeness net needs the transaction instrument " +
			"(credentials.TransactHook firing inside s.transact) to catch arbitrary-name writers")
	}

	// Unit: with the observed engagement and no row, the out-of-predicate
	// route is REQUIRED to have a payload row — the name predicate alone
	// would have called it covered.
	handled := map[string]bool{}
	withPlant := append(append([]string(nil), routes...), plant)
	require.Contains(t, pgMissingSweepRows(withPlant, handled, map[string]string{}, reached), plant,
		"the arbitrary-name indirect writer needs a payload row despite its name (CHECK R09)")

	// Negative: with the row ATTEMPTED (marked only after a probe, as the
	// sweep marks rows), the route is no longer missing.
	handled[plant+"|"+http.MethodPost] = true
	require.NotContains(t, pgMissingSweepRows(withPlant, handled, map[string]string{}, reached), plant,
		"an attempted row covers the planted writer")
}
