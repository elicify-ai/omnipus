package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/auth"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func newTestOAuthStore(t *testing.T) *credentials.Store {
	t.Helper()
	dir := t.TempDir()
	store := credentials.NewStore(filepath.Join(dir, "credentials.json"))
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	if err := store.UnlockWithKey(key); err != nil {
		t.Fatalf("UnlockWithKey: %v", err)
	}
	return store
}

func setStoredOAuthCred(t *testing.T, store *credentials.Store, entryName string, cred storeOAuthCred) {
	t.Helper()
	data, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.Set(entryName, string(data)); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func TestOAuthVendorID(t *testing.T) {
	if got := OAuthVendorID("openai-chatgpt"); got != "openai" {
		t.Errorf("OAuthVendorID(openai-chatgpt) = %q, want %q", got, "openai")
	}
	if got := OAuthVendorID("xai"); got != "xai" {
		t.Errorf("OAuthVendorID(xai) = %q, want %q", got, "xai")
	}
}

func TestStoreOAuthTokenSource_NotSignedIn(t *testing.T) {
	store := newTestOAuthStore(t)
	ts := NewStoreOAuthTokenSource("openai-chatgpt", store, auth.OAuthProviderConfig{Issuer: "http://unused"})

	_, _, err := ts()
	if err == nil {
		t.Fatal("expected an error when nothing is stored")
	}
	if !errors.Is(err, ErrProviderNeedsSignIn) {
		t.Errorf("expected errors.Is(err, ErrProviderNeedsSignIn), got %v", err)
	}
}

func TestStoreOAuthTokenSource_FreshTokenNoRefresh(t *testing.T) {
	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken: "fresh-access-token",
		AccountID:   "acc_1",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	})

	var refreshCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store, auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c"})
	token, accountID, err := ts()
	if err != nil {
		t.Fatalf("ts() error: %v", err)
	}
	if token != "fresh-access-token" {
		t.Errorf("token = %q, want fresh-access-token", token)
	}
	if accountID != "acc_1" {
		t.Errorf("accountID = %q, want acc_1", accountID)
	}
	if atomic.LoadInt32(&refreshCalls) != 0 {
		t.Errorf("refresh was called %d times, want 0 for a fresh token", refreshCalls)
	}
}

func TestStoreOAuthTokenSource_RefreshesNearExpiry(t *testing.T) {
	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Minute), // within the 5-minute window
	})

	var refreshCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
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

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store, auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c"})
	token, accountID, err := ts()
	if err != nil {
		t.Fatalf("ts() error: %v", err)
	}
	if token != "new-access-token" {
		t.Errorf("token = %q, want new-access-token", token)
	}
	if accountID != "acc_1" {
		t.Errorf("accountID = %q, want acc_1 (preserved)", accountID)
	}
	if atomic.LoadInt32(&refreshCalls) != 1 {
		t.Errorf("refresh was called %d times, want exactly 1", refreshCalls)
	}

	// The refreshed credential must be persisted so a subsequent call reuses it.
	persisted, err := readStoreOAuthCred(store, entryName)
	if err != nil {
		t.Fatalf("readStoreOAuthCred: %v", err)
	}
	if persisted == nil || persisted.AccessToken != "new-access-token" {
		t.Errorf("persisted credential = %+v, want AccessToken=new-access-token", persisted)
	}
	if persisted.RefreshToken != "new-refresh-token" {
		t.Errorf("persisted RefreshToken = %q, want new-refresh-token", persisted.RefreshToken)
	}
}

func TestStoreOAuthTokenSource_RefreshFailureIsNeedsProvider(t *testing.T) {
	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(-1 * time.Minute), // already expired
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
	}))
	defer server.Close()

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store, auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c"})
	_, _, err := ts()
	if err == nil {
		t.Fatal("expected an error when the refresh call fails")
	}
	if !errors.Is(err, ErrProviderNeedsSignIn) {
		t.Errorf("expected errors.Is(err, ErrProviderNeedsSignIn), got %v", err)
	}
}

func TestStoreOAuthTokenSource_NoRefreshTokenIsNeedsProvider(t *testing.T) {
	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken: "old-access-token",
		// No RefreshToken (e.g. an FR-047 import, which never carries one).
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	})

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store, auth.OAuthProviderConfig{Issuer: "http://unused"})
	_, _, err := ts()
	if !errors.Is(err, ErrProviderNeedsSignIn) {
		t.Errorf("expected errors.Is(err, ErrProviderNeedsSignIn), got %v", err)
	}
}

// TestStoreOAuthTokenSource_SingleFlight drives many concurrent callers at a
// token source whose stored credential needs refreshing, and asserts the
// vendor's refresh endpoint is hit exactly once — the mutex serializes
// callers, and every caller after the first re-reads the now-fresh
// persisted credential instead of refreshing again.
func TestStoreOAuthTokenSource_SingleFlight(t *testing.T) {
	store := newTestOAuthStore(t)
	entryName := credentials.OAuthEntryName(OAuthVendorID("openai-chatgpt"))
	setStoredOAuthCred(t, store, entryName, storeOAuthCred{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AccountID:    "acc_1",
		ExpiresAt:    time.Now().Add(1 * time.Minute),
	})

	var refreshCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		// Simulate network latency so concurrent callers genuinely overlap.
		time.Sleep(20 * time.Millisecond)
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

	ts := NewStoreOAuthTokenSource("openai-chatgpt", store, auth.OAuthProviderConfig{Issuer: server.URL, ClientID: "c"})

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, _, err := ts()
			tokens[i] = tok
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: unexpected error: %v", i, err)
		}
		if tokens[i] != "new-access-token" {
			t.Errorf("caller %d: token = %q, want new-access-token", i, tokens[i])
		}
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Errorf("vendor refresh endpoint called %d times, want exactly 1", got)
	}
}
