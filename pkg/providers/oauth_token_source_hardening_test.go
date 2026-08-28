package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/auth"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// resetOAuthTokenSourceState clears BOTH process-wide singletons the store
// OAuth token source keeps, so nothing one test leaves behind is observed by
// the next:
//
//   - oauthRefreshLocks, the keyed mutex map (a deliberate singleton — that
//     is the whole point of the single-flight fix), so one test's lock for an
//     entry name is not the next test's lock.
//   - registeredOAuthTokens, the fast-path registration memo, so a token this
//     test registers cannot suppress a registration the NEXT test asserts.
//     That is not hypothetical: several tests here refresh the same
//     OPENAI_OAUTH entry to the same fixture string "new-access-token", and
//     the memo — correctly, in production, where a vendor never reissues a
//     byte-identical token and the value is still in the store either way —
//     would treat the second one as already registered.
//
// Tests that care about either singleton's identity must reset it explicitly.
func resetOAuthTokenSourceState(t *testing.T) {
	t.Helper()
	reset := func() {
		oauthRefreshLocksMu.Lock()
		oauthRefreshLocks = make(map[string]*sync.Mutex)
		oauthRefreshLocksMu.Unlock()

		registeredOAuthTokensMu.Lock()
		registeredOAuthTokens = make(map[string]string)
		registeredOAuthTokensMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// TestStoreOAuthTokenSource_SingleFlightAcrossIndependentSources is the
// regression test for the defect that the single-flight mutex was declared
// INSIDE NewStoreOAuthTokenSource, making it per-closure rather than
// per-vendor.
//
// A fresh closure is constructed on every CreateProviderFromConfig (each agent
// instance, each loop rebuild, the voice transcriber) and on every
// GET /providers/{id}/sign-in/status poll, so the real system routinely has
// several closures over ONE stored credential. Each held its own mutex, so
// they could present the SAME refresh token to the vendor concurrently —
// and OpenAI rotates refresh tokens, so all but the first exchange fail
// invalid_grant and last-write-wins decides what lands on disk.
//
// The oracle is the vendor's own call count: exactly one exchange, no matter
// how many independently-constructed sources ask at once.
func TestStoreOAuthTokenSource_SingleFlightAcrossIndependentSources(t *testing.T) {
	resetOAuthTokenSourceState(t)

	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Minute), // inside the 5-minute window
	})

	var refreshCalls int32
	var presentedRefreshTokens sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		presented := r.FormValue("refresh_token")
		if _, seen := presentedRefreshTokens.LoadOrStore(presented, true); seen {
			// This is the invalid_grant scenario reproduced exactly: a
			// rotated refresh token presented twice.
			t.Errorf("refresh token %q was presented to the vendor more than once", presented)
		}
		// Wide enough that overlapping callers genuinely overlap.
		time.Sleep(50 * time.Millisecond)
		resp := map[string]any{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"expires_in":    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	oauthCfg := auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c", Timeout: 5 * time.Second}

	const n = 8
	var wg sync.WaitGroup
	tokens := make([]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each goroutine builds its OWN token source, which is what the
			// per-closure mutex could never coordinate.
			ts := NewStoreOAuthTokenSource("openai-chatgpt", store, oauthCfg)
			<-start
			tokens[i], _, errs[i] = ts()
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Errorf("caller %d: unexpected error: %v", i, errs[i])
		}
		if tokens[i] != "new-access-token" {
			t.Errorf("caller %d: token = %q, want new-access-token", i, tokens[i])
		}
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Errorf("vendor refresh endpoint called %d times across %d independently-constructed token sources, want exactly 1", got, n)
	}

	persisted, err := readStoreOAuthCred(store, entryName)
	if err != nil {
		t.Fatalf("readStoreOAuthCred: %v", err)
	}
	if persisted == nil || persisted.RefreshToken != "new-refresh-token" {
		t.Errorf("persisted credential = %+v, want the rotated RefreshToken", persisted)
	}
}

// TestStoreOAuthTokenSource_KeyedByVendorEntryNotProviderID pins that the
// single-flight key is the stored vendor entry, not the provider id: an
// OpenAI-family row shares one stored credential (OAuthVendorID), so two
// sources built under different provider ids that resolve to the same entry
// must still share one lock.
func TestStoreOAuthTokenSource_KeyedByVendorEntryNotProviderID(t *testing.T) {
	resetOAuthTokenSourceState(t)
	a := oauthRefreshLock(credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt")))
	b := oauthRefreshLock(credentials.OAuthEntryName(OAuthVendorID("openai")))
	if a != b {
		t.Error("openai-chatgpt and openai share one stored credential but got different refresh locks")
	}
	c := oauthRefreshLock(credentials.OAuthEntryName(OAuthVendorID("xai")))
	if a == c {
		t.Error("a different vendor must not share the openai refresh lock — one stalled vendor would block the other")
	}
}

// TestStoreOAuthTokenSource_RegistersRefreshedTokensAsSensitive is the
// regression test for ADR-068 FR-046's agent-path gap: a mid-turn refresh
// mints a NEW access+refresh pair and persists it, but nothing re-registered
// it with the sensitive-value scrubber. The scrubber therefore kept protecting
// the OLD, superseded token while the live one travelled unprotected until the
// next boot, sign-in, or sign-in-status poll.
func TestStoreOAuthTokenSource_RegistersRefreshedTokensAsSensitive(t *testing.T) {
	resetOAuthTokenSourceState(t)

	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Minute),
	})

	var mu sync.Mutex
	var registered []string
	SetSensitiveValueRegistrar(func(values ...string) {
		mu.Lock()
		defer mu.Unlock()
		registered = append(registered, values...)
	})
	t.Cleanup(func() { SetSensitiveValueRegistrar(nil) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"expires_in":    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store,
		auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c", Timeout: 5 * time.Second})
	if _, _, err := ts(); err != nil {
		t.Fatalf("ts() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !contains(registered, "new-access-token") {
		t.Errorf("the refreshed ACCESS token was never registered as sensitive; registrar saw %v", registered)
	}
	if !contains(registered, "new-refresh-token") {
		t.Errorf("the rotated REFRESH token was never registered as sensitive; registrar saw %v", registered)
	}
}

// TestSensitiveValueRegistrar_DropsEmptyValues: a vendor response that omits a
// rotated refresh token must not register "" as a secret — a zero-length
// needle would match everywhere.
func TestSensitiveValueRegistrar_DropsEmptyValues(t *testing.T) {
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

	registerSensitiveValues("tok", "")
	registerSensitiveValues("", "")

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("registrar called %d times, want 1 — an all-empty call must not reach it", calls)
	}
	for _, v := range registered {
		if v == "" {
			t.Error("an empty string was registered as a sensitive value")
		}
	}
}

// TestSensitiveValueRegistrar_DefaultsToNoOp: the refresh path must not
// nil-check a security control it cannot provide, and a build that never
// installs a registrar (a CLI path, a test) must not panic.
func TestSensitiveValueRegistrar_DefaultsToNoOp(t *testing.T) {
	SetSensitiveValueRegistrar(nil)
	registerSensitiveValues("tok") // must not panic
}

// TestStoreOAuthTokenSource_DoesNotClobberNewerCredential is the lost-update
// half of the concurrency finding. The refresh path is a read-modify-write
// with no compare-and-swap: a sign-in handler (which writes this same entry
// without taking the refresh lock) or a second Omnipus process could install a
// brand-new credential while our exchange was in flight, and the unconditional
// write would overwrite it with our older exchange's result — leaving a dead
// refresh token on disk and a spurious "needs sign-in".
//
// The vendor handler here performs that concurrent write, so by the time the
// exchange returns the store already holds something newer.
func TestStoreOAuthTokenSource_DoesNotClobberNewerCredential(t *testing.T) {
	resetOAuthTokenSourceState(t)

	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Minute),
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stand in for a sign-in completing while our exchange is in flight.
		// Written inline rather than via setStoredOAuthCred because this runs
		// on the server's goroutine, where t.Fatalf is not legal.
		newer, marshalErr := json.Marshal(storeOAuthCred{
			AccessToken:  "signin-access-token",
			RefreshToken: "signin-refresh-token",
			AccountID:    "acc_1",
			ExpiresAt:    time.Now().Add(2 * time.Hour),
		})
		if marshalErr != nil {
			t.Errorf("marshal: %v", marshalErr)
			return
		}
		if setErr := store.Set(entryName, string(newer)); setErr != nil {
			t.Errorf("concurrent sign-in write: %v", setErr)
			return
		}
		resp := map[string]any{
			"access_token":  "refreshed-access-token",
			"refresh_token": "refreshed-refresh-token",
			"expires_in":    3600,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	var mu sync.Mutex
	var registered []string
	SetSensitiveValueRegistrar(func(values ...string) {
		mu.Lock()
		defer mu.Unlock()
		registered = append(registered, values...)
	})
	t.Cleanup(func() { SetSensitiveValueRegistrar(nil) })

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store,
		auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c", Timeout: 5 * time.Second})
	token, _, err := ts()
	if err != nil {
		t.Fatalf("ts() error: %v", err)
	}

	// The credential we yield to the LLM transport must be known to the
	// scrubber whichever party minted it — a second Omnipus process sharing
	// this store never registered it here.
	mu.Lock()
	if !contains(registered, "signin-access-token") || !contains(registered, "signin-refresh-token") {
		t.Errorf("the newer credential we returned was not registered as sensitive; registrar saw %v", registered)
	}
	mu.Unlock()

	if token != "signin-access-token" {
		t.Errorf("returned token = %q, want the newer signin-access-token", token)
	}
	persisted, err := readStoreOAuthCred(store, entryName)
	if err != nil {
		t.Fatalf("readStoreOAuthCred: %v", err)
	}
	if persisted == nil {
		t.Fatal("credential vanished from the store")
	}
	if persisted.AccessToken != "signin-access-token" || persisted.RefreshToken != "signin-refresh-token" {
		t.Errorf("stored credential = %+v — the newer credential was clobbered by the in-flight refresh", persisted)
	}
}

// TestStoreOAuthTokenSource_HungVendorReleasesTheLock: the refresh mutex is
// held ACROSS the vendor exchange, so an unbounded exchange wedged the token
// source permanently — every later caller (an agent turn) blocked forever.
// With the exchange bounded, a hung vendor costs each caller one bounded
// stall and nothing more.
func TestStoreOAuthTokenSource_HungVendorReleasesTheLock(t *testing.T) {
	resetOAuthTokenSourceState(t)

	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Minute),
	})

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	oauthCfg := auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c", Timeout: 250 * time.Millisecond}

	const n = 3
	const ceiling = 15 * time.Second
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			ts := NewStoreOAuthTokenSource("openai-chatgpt", store, oauthCfg)
			_, _, err := ts()
			done <- err
		}()
	}

	deadline := time.After(ceiling)
	for i := 0; i < n; i++ {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("expected an error from a vendor that never responds")
			}
			if !errors.Is(err, ErrProviderNeedsSignIn) {
				t.Errorf("error %v does not classify as needs-sign-in", err)
			}
		case <-deadline:
			t.Fatalf("only %d of %d callers returned within %v — the refresh lock is held by a hung exchange", i, n, ceiling)
		}
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
