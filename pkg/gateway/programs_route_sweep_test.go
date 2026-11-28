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
// Route patterns come from the REAL production registrar. Writer discovery
// is NAMED only, not independent reachability (R09's loud boundary rows).
// The named inventory includes:
// /api/v1/credentials, /api/v1/credentials/{key}, secret-request submit and
// Programs. Each required method needs a row; a planted special-payload
// writer tests method accounting, not all-route completeness. A probe whose handler does not answer within 5 s fails
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
// credential save, the check, the credential disconnect — never collapsed to one
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
			return "/api/v1/programs", "", false
		case http.MethodPost:
			// the custom-program row (the create schema, S-113 "their schemas")
			return "/api/v1/programs", `{"display_name":"Sweep","program_words":["sweepw"],` +
				`"token_variables":["SWEEP_TOKEN"],"network_sites":["api.sweep.invalid"]}`, true
		default:
			return generic("/api/v1/programs")
		}
	case subtree && strings.Contains(pattern, "programs") && !strings.Contains(pattern, "/channels/"):
		// the {id} entry subtree: each leaf by method (CHECK R09's collapse fix)
		switch method {
		case http.MethodGet:
			return "/api/v1/programs/github", "", false
		case http.MethodPut:
			// the credential save row (ProgramCredentialRequest)
			return "/api/v1/programs/github/credential", `{"value":"tok_evil_999999"}`, true
		case http.MethodPost:
			// the check row
			return "/api/v1/programs/github/check", `{}`, true
		default: // DELETE: the disconnect row
			return "/api/v1/programs/github/credential", "", true
		}
	case strings.Contains(pattern, "/channels/") && strings.Contains(pattern, "programs"):
		switch method {
		case http.MethodGet:
			return "/api/v1/channels/telegram/programs", "", false
		case http.MethodPut:
			// the channel setting row (its boolean + revision schema)
			return "/api/v1/channels/telegram/programs", `{"allowed":true,"revision":1}`, true
		default:
			return generic("/api/v1/channels/telegram/programs")
		}
	case strings.Contains(pattern, "programs"): // settings and any other Programs route
		switch method {
		case http.MethodGet:
			return "/api/v1/programs/settings", "", false
		case http.MethodPut:
			return "/api/v1/programs/settings", `{"web_chat_allowed":true,"revision":1}`, true
		default:
			return generic("/api/v1/programs/settings")
		}
	default:
		return generic(base)
	}
}

// pgRequiredSweepMethods is the NAMED inventory only. It is not independent
// route-to-writer reachability; the missing inventory/schema boundary is loud
// in TestProgramCredential_RouteSweep_MissingBoundaries (R09).
func pgRequiredSweepMethods(pattern string) []string {
	switch {
	case strings.HasPrefix(pattern, "/api/v1/credentials"):
		if strings.HasSuffix(pattern, "/") {
			return []string{http.MethodPost, http.MethodDelete}
		}
		// Code is POST, spec names PUT: neither is silently exempted.
		return []string{http.MethodPost, http.MethodPut}
	case strings.Contains(pattern, "secret-requests"):
		return []string{http.MethodPost}
	case strings.Contains(pattern, "programs"):
		if strings.Contains(pattern, "/channels/") || strings.Contains(pattern, "settings") {
			return []string{http.MethodPut}
		}
		if strings.HasSuffix(pattern, "/") {
			return []string{http.MethodPut, http.MethodPost, http.MethodDelete}
		}
		return []string{http.MethodPost}
	}
	return nil
}

// pgMissingSweepRows requires each method, not ANY method on the pattern.
// reached keys are pattern|method, obtained only from a joined synchronous
// probe. A method row still needs valid concrete schema/path reconciliation;
// it is not proof of every writer path. Missing card rows stay fatal separately.
func pgMissingSweepRows(routes []string, handled map[string]bool, excused map[string]string, reached map[string]bool) []string {
	var missing []string
	for _, p := range routes {
		methods := map[string]bool{}
		for _, m := range pgRequiredSweepMethods(p) {
			methods[m] = true
		}
		for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			if reached[p+"|"+m] {
				methods[m] = true
			}
		}
		for m := range methods {
			key := p + "|" + m
			if !handled[key] && excused[p] == "" {
				missing = append(missing, key)
			}
		}
	}
	sort.Strings(missing)
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
// timed-out probe cancels REQUEST WORK, then attempts a bounded handler join.
// An uncooperative handler is reported handlerDone=false; that is a fatal
// fixture-isolation failure, never a successful state verdict (R10).
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

// pgSweepProbeBound is how long one sweep probe may take before S-113 calls it
// hung. 5 s passed on a fast runner but a race-instrumented full-package run on
// a loaded CI runner (about 4-5 s per probe overall) missed it once on
// POST /auth/logout, a handler that did not hang. A genuinely hung handler
// still fails, and is still joined before any store read.
const pgSweepProbeBound = 20 * time.Second

// pgSweepServe fires one sweep probe with a hard deadline (pgSweepProbeBound). A probe
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
	w, out := pgServeProbe(h, req, pgSweepProbeBound)
	if out.timedOut {
		t.Errorf("sweep probe %s %s did not answer within %s (S-113) — the sweep fails and joins the handler before any store read", method, path, pgSweepProbeBound)
	}
	if !out.handlerDone {
		t.Fatalf("sweep probe %s %s handler still running 15s after the probe — the store verdict cannot be taken", method, path)
	}
	if method == http.MethodPost && path == "/api/v1/gateway/restart" && w.Code == http.StatusAccepted {
		// Register before later sweep assertions can fail. Handler completion
		// alone is not asynchronous restart completion.
		t.Cleanup(func() { pgAwaitRestart(t, f) })
	}
	return w
}

func pgAwaitRestart(t *testing.T, f *pgGateFixture) {
	t.Helper()
	select {
	case <-f.restartDone:
	case <-time.After(10 * time.Second):
		t.Error("accepted restart callback did not complete within 10s; no joined restart verdict (R10)")
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
			canary := programstore.SeedCanary(t, store)
			stateBefore := programstore.Snapshot(t, store, "req-s113", canary)
			var pendingCardIDs []string

			// A valid local re-auth token is minted before EACH mutator call
			// (S-113's Given); every mint is asserted (F04).
			pgReauthTokenFor(t, f)

			t.Logf("sweep: %d registered route patterns, edition %s", len(routes), edition)
			probes := 0
			handledMutator := map[string]bool{} // keyed pattern|method, set only AFTER the probe fired
			// No request-context-aware independent reachability inventory is
			// connected. A process-global hook cannot safely attribute async
			// writes to the current request. Do not present generic probes as
			// completeness; the dedicated missing-boundary tests remain red.
			reached := map[string]bool{}
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
					var isRow bool
					probeReauth := false
					switch {
					case strings.HasPrefix(pattern, "/api/v1/credentials") && (method == http.MethodPost || method == http.MethodPut) && !subtree:
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
					case strings.HasPrefix(pattern, "/api/v1/credentials") && method == http.MethodDelete && subtree:
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
						callReauth = pgReauthTokenFor(t, f)
					}
					pgSweepServe(t, f, method, path, body, f.token, callReauth)
					probes++
					// The row is covered only now — after its probe really
					// executed its route-specific method/path/payload.
					if isRow {
						handledMutator[pattern+"|"+method] = true
					}
				}
			}

			// The sweep-table completeness check (S-113's last And-clause):
			// Named method inventory only. Independent writer reachability
			// and valid concrete schema coverage remain separately BLOCKED.
			// Missing card rows fatal-BLOCK after the state verdict below.
			missing := pgMissingSweepRows(routes, handledMutator, blockedPayloadRows, reached)
			require.Empty(t, missing, "sweep table is missing payload rows for registered mutator routes: %v", missing)

			// Instrument proof (F04 negative control): the completeness
			// checker fires on a PLANTED route that has no row — a checker
			// that can never fire proves nothing.
			const plantedRow = "/api/v1/credentials/planted-sweep-row"
			planted := append(append([]string(nil), routes...), plantedRow)
			plantedReached := map[string]bool{plantedRow + "|" + http.MethodPost: true}
			require.Contains(t, pgMissingSweepRows(planted, handledMutator, blockedPayloadRows, plantedReached), plantedRow+"|"+http.MethodPost,
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
			pgAwaitRestart(t, f)
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

// The arbitrary-name registered writer requires its OWN payload. A generic
// key/value probe is rejected before Store.Set; a valid probe must reach it.
// This proves observed-method accounting only, not discovery of all writers.
func TestProgramCredential_RouteSweep_PlantedIndirectWriter(t *testing.T) {
	f, _ := pgNewGateway(t, true)
	const plant = "/api/v1/rename-me-sweep-writer-7f3a"
	wrapper := http.NewServeMux()
	write := func(ref, value string) error { return f.store.Set(ref, value) }
	wrapper.HandleFunc(http.MethodPost+" "+plant, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["plant_operation"] != "overwrite" || body["reference"] != "PLANT_RW" || body["value"] != "x" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := write(body["reference"], body["value"]); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	wrapper.Handle("/", f.handler)
	var engaged atomic.Bool
	credentials.TransactHook = func(string) { engaged.Store(true) }
	t.Cleanup(func() { credentials.TransactHook = nil })

	rejected := pgSweepServeOn(t, f, wrapper, http.MethodPost, plant, pgGenericBody(), f.token, "")
	require.Equal(t, http.StatusBadRequest, rejected.Code, "generic probe cannot exercise this route-specific writer")
	require.False(t, engaged.Load(), "rejected payload never reaches store transaction")
	w := pgSweepServeOn(t, f, wrapper, http.MethodPost, plant,
		`{"plant_operation":"overwrite","reference":"PLANT_RW","value":"x"}`, f.token, "")
	require.Equal(t, http.StatusOK, w.Code, "valid declared payload reaches indirect writer")
	value, err := f.store.Get("PLANT_RW")
	require.NoError(t, err)
	require.Equal(t, "x", value, "positive: the valid route really writes")
	if !engaged.Load() {
		t.Fatal("BLOCKED: Store.Set transaction instrument not implemented — required by S-113/R09; valid planted writer reached Store.Set but emitted no TransactHook stage")
	}
	reached := map[string]bool{plant + "|" + http.MethodPost: true}
	handled := map[string]bool{plant + "|" + http.MethodGet: true}
	require.Equal(t, []string{plant + "|" + http.MethodPost}, pgMissingSweepRows([]string{plant}, handled, nil, reached), "GET cannot cover POST")
	handled[plant+"|"+http.MethodPost] = true
	require.Empty(t, pgMissingSweepRows([]string{plant}, handled, nil, reached), "the attempted valid POST covers this observed method")
}
