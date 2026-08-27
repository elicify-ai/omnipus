// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/auth"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// blockingRefreshVendor returns an OAuth refresh endpoint that parks until
// the returned release func is called, so a test can act on the credential
// store while an exchange is genuinely mid-flight rather than hoping a sleep
// lands in the right window. entered is closed once the handler is running.
//
// The helper owns BOTH the parking channel and the server's shutdown, and
// registers them so the park is always released before the server is closed
// (t.Cleanup is LIFO, hence the registration order below). Without that, any
// t.Fatal on a failure path left the handler parked forever and
// httptest.Server.Close blocked on it — a failing test that hangs the whole
// package instead of reporting, which is exactly how a real regression here
// would first present itself.
func blockingRefreshVendor(
	t *testing.T, entered chan struct{}, onRelease func(), access, refresh string,
) (server *httptest.Server, release func()) {
	t.Helper()
	var enteredOnce, releaseOnce sync.Once
	gate := make(chan struct{})
	release = func() { releaseOnce.Do(func() { close(gate) }) }

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		<-gate
		if onRelease != nil {
			onRelease()
		}
		resp := map[string]any{
			"access_token":  access,
			"refresh_token": refresh,
			"expires_in":    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encoding vendor refresh response: %v", err)
		}
	}))
	t.Cleanup(server.Close) // registered first -> runs second
	t.Cleanup(release)      // registered second -> runs first
	return server, release
}

// TestStoreOAuthTokenSource_DeletedCredentialIsNotResurrected is the
// regression test for the revocation failure: sign-out deleted the stored
// credential, and an in-flight refresh exchange then wrote a brand-new
// access+refresh pair back into the entry the operator had just destroyed.
//
// The defect was a single missing distinction. readStoreOAuthCred reports a
// MISSING entry as (nil, nil) — "not signed in" is not an error to it — and
// the compare-and-swap only aborted when `latest != nil`. So a DELETED entry
// read as "nothing newer here" and execution fell straight through to the
// write. The operator saw the delete succeed, the UI say "not signed in" and
// the audit log record provider.signed_out; seconds later the grant was live
// again on their real vendor account with nothing anywhere surfacing it.
//
// BDD: Given a stored credential four minutes from expiry (inside the
// five-minute refresh lead), When a refresh exchange is in flight and the
// stored entry is deleted before the vendor responds, Then the refreshed
// tokens are discarded, the caller is told the provider needs sign-in, and
// the entry is still absent.
//
// The delete here goes straight to store.Delete rather than through
// DeleteStoreOAuthCred, deliberately: this exercises the compare-and-swap ON
// ITS OWN, which is the only defence available when the deleter is a second
// Omnipus process sharing $OMNIPUS_HOME or the provider-row delete in
// pkg/gateway/rest_providers_delete.go — neither of which can be reached by
// this process's refresh lock.
func TestStoreOAuthTokenSource_DeletedCredentialIsNotResurrected(t *testing.T) {
	resetOAuthTokenSourceState(t)

	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "compromised-access-token",
		RefreshToken: "compromised-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(4 * time.Minute),
	})

	entered := make(chan struct{})
	server, release := blockingRefreshVendor(t, entered, nil,
		"resurrected-access-token", "resurrected-refresh-token")

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store,
		auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c", Timeout: 10 * time.Second})

	type result struct {
		token string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		tok, _, err := ts()
		done <- result{token: tok, err: err}
	}()

	<-entered
	// The operator, believing the token is compromised, clicks Sign out.
	if err := store.Delete(entryName); err != nil {
		t.Fatalf("deleting the stored credential mid-exchange: %v", err)
	}
	release()

	res := <-done

	if _, getErr := store.Get(entryName); getErr == nil {
		t.Fatal("REVOKED CREDENTIAL RESURRECTED: the entry the operator deleted " +
			"exists again, rewritten by the exchange that was in flight during sign-out")
	} else {
		var notFound *credentials.NotFoundError
		if !errors.As(getErr, &notFound) {
			t.Fatalf("reading the deleted entry: want NotFoundError, got %v", getErr)
		}
	}
	if !errors.Is(res.err, ErrProviderNeedsSignIn) {
		t.Errorf("after revocation the token source must report needs-sign-in, got err=%v", res.err)
	}
	if res.token != "" {
		t.Errorf("token source handed out %q after the credential was revoked; want no token", res.token)
	}
}

// TestDeleteStoreOAuthCred_WaitsForInFlightRefresh pins the ordering half of
// the same finding. handleProviderSignOut used to call store.Delete with no
// synchronisation at all against the refresh path, so the delete and the
// refresh's write overlapped and which one landed last was decided by the
// vendor's response time. DeleteStoreOAuthCred takes the same per-vendor
// refresh lock the exchange holds, which turns an overlap into an order:
// the delete waits for the exchange, then removes what it wrote.
//
// BDD: Given a refresh exchange in flight, When sign-out deletes the stored
// credential, Then the delete does not complete until the exchange has
// completed, and the entry is absent afterwards.
//
// The oracle is sequence, not duration: "delete-returned" must never be
// recorded before "exchange-completed".
func TestDeleteStoreOAuthCred_WaitsForInFlightRefresh(t *testing.T) {
	resetOAuthTokenSourceState(t)

	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(4 * time.Minute),
	})

	var orderMu sync.Mutex
	var order []string
	record := func(step string) {
		orderMu.Lock()
		defer orderMu.Unlock()
		order = append(order, step)
	}
	snapshot := func() []string {
		orderMu.Lock()
		defer orderMu.Unlock()
		return append([]string(nil), order...)
	}

	entered := make(chan struct{})
	server, release := blockingRefreshVendor(t, entered,
		func() { record("exchange-completed") },
		"refreshed-access-token", "refreshed-refresh-token")

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store,
		auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c", Timeout: 10 * time.Second})

	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		if _, _, err := ts(); err != nil {
			t.Errorf("the refresh itself must succeed here: %v", err)
		}
	}()

	<-entered

	deleteDone := make(chan error, 1)
	go func() {
		err := DeleteStoreOAuthCred("openai-chatgpt", store)
		record("delete-returned")
		deleteDone <- err
	}()

	// Long enough that an unsynchronised delete — which is a local file
	// write taking microseconds — would have finished many times over.
	select {
	case <-deleteDone:
		t.Fatalf("sign-out completed while the vendor exchange was still in flight (steps: %v); "+
			"the delete is not ordered against the write that follows the exchange", snapshot())
	case <-time.After(200 * time.Millisecond):
	}

	release()
	if err := <-deleteDone; err != nil {
		t.Fatalf("DeleteStoreOAuthCred: %v", err)
	}
	<-refreshDone

	steps := snapshot()
	if len(steps) != 2 || steps[0] != "exchange-completed" || steps[1] != "delete-returned" {
		t.Errorf("steps = %v, want [exchange-completed delete-returned]", steps)
	}
	if _, getErr := store.Get(entryName); getErr == nil {
		t.Fatal("the entry survived sign-out: the refresh's write landed after the delete")
	}
}

// TestStoreOAuthTokenSource_FastPathRegistersTokenMintedElsewhere covers the
// scrubber gap on the FAST path — the branch that returns a stored,
// still-fresh token without refreshing.
//
// Two Omnipus processes share $OMNIPUS_HOME. P2 refreshes and writes token B.
// P1's next call reads B, finds it fresh, and returned it straight to the LLM
// transport having never handed it to the scrubber: P1 registers at boot and
// after every refresh IT performs, and B is neither. If B is ever echoed back
// — a provider error quoting the Authorization header, a debug dump — it
// lands in P1's log verbatim instead of [FILTERED] (ADR-068 FR-046).
//
// The test also pins the COST control that makes registering here
// affordable. The installed registrar is expensive (a full ResolveBundle plus
// a decrypting scan of the store) and this path runs on every LLM call, so
// registration is memoized per entry: an unchanged token must not re-enter
// the registrar. Both directions are asserted — a new token always reaches
// it, an unchanged one never does.
func TestStoreOAuthTokenSource_FastPathRegistersTokenMintedElsewhere(t *testing.T) {
	resetOAuthTokenSourceState(t)

	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	// Written by the OTHER process: fresh, so this process takes the fast
	// path and never refreshes.
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "p2-minted-access-token",
		RefreshToken: "p2-minted-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	})

	var mu sync.Mutex
	var registered []string
	calls := 0
	SetSensitiveValueRegistrar(func(values ...string) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		registered = append(registered, values...)
	})
	t.Cleanup(func() { SetSensitiveValueRegistrar(nil) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the vendor must not be called: the stored token is an hour from expiry")
		http.Error(w, "unexpected refresh", http.StatusInternalServerError)
	}))
	defer server.Close()

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store,
		auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c", Timeout: 5 * time.Second})

	token, _, err := ts()
	if err != nil {
		t.Fatalf("ts() error: %v", err)
	}
	if token != "p2-minted-access-token" {
		t.Fatalf("token = %q, want the stored p2-minted-access-token", token)
	}

	mu.Lock()
	if !contains(registered, "p2-minted-access-token") {
		t.Errorf("a token minted by another process was handed to the transport "+
			"without ever being registered as sensitive; registrar saw %v", registered)
	}
	if !contains(registered, "p2-minted-refresh-token") {
		t.Errorf("the refresh token stored alongside it was never registered; registrar saw %v", registered)
	}
	mu.Unlock()

	// Cost control: the same token on every later call must not re-enter the
	// expensive registrar.
	for i := 0; i < 5; i++ {
		if _, _, err := ts(); err != nil {
			t.Fatalf("ts() call %d: %v", i+2, err)
		}
	}
	mu.Lock()
	if calls != 1 {
		t.Errorf("registrar called %d times for one unchanged token, want 1 — "+
			"the fast path must not pay a full config walk and store decrypt per LLM call", calls)
	}
	mu.Unlock()

	// The other process rotates again. The new token must reach the
	// registrar — the memo suppresses repeats, never a genuine change.
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "p2-rotated-access-token",
		RefreshToken: "p2-rotated-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	})
	if _, _, err := ts(); err != nil {
		t.Fatalf("ts() after rotation: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("registrar called %d times, want 2 — a genuinely new token must not be suppressed", calls)
	}
	if !contains(registered, "p2-rotated-access-token") {
		t.Errorf("the rotated token was never registered; registrar saw %v", registered)
	}
}

// TestDeleteStoreOAuthCred_ForgetsTheRegistrationMemo: sign-out must clear the
// memo, or a re-sign-in that lands on a byte-identical access token would be
// suppressed and travel unscrubbed.
func TestDeleteStoreOAuthCred_ForgetsTheRegistrationMemo(t *testing.T) {
	resetOAuthTokenSourceState(t)

	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	cred := storeOAuthCred{
		AccessToken:  "same-access-token",
		RefreshToken: "same-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	setStoredOAuthCred(t, store, entryName, cred)

	var mu sync.Mutex
	calls := 0
	SetSensitiveValueRegistrar(func(values ...string) {
		mu.Lock()
		defer mu.Unlock()
		calls++
	})
	t.Cleanup(func() { SetSensitiveValueRegistrar(nil) })

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store,
		auth.OAuthProviderConfig{Issuer: "http://unused", ClientID: "c"})
	if _, _, err := ts(); err != nil {
		t.Fatalf("first ts(): %v", err)
	}

	if err := DeleteStoreOAuthCred("openai-chatgpt", store); err != nil {
		t.Fatalf("DeleteStoreOAuthCred: %v", err)
	}
	setStoredOAuthCred(t, store, entryName, cred) // signed in again, same token
	if _, _, err := ts(); err != nil {
		t.Fatalf("ts() after re-sign-in: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("registrar called %d times, want 2 — sign-out must drop the memo so "+
			"a re-stored token is registered again rather than silently suppressed", calls)
	}
}
