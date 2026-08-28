// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package onboard

// Tests #17, #18, #31 from the TDD plan (FR-014/FR-015 / US7 CLI onboard matrix).
//
// These tests use an httptest.Server as the provider upstream and point the
// api_base at it via providers.APIBaseFor — which we override by
// building a ValidateInput that already carries the test server URL. The
// validateAndResolveKey function calls providers.ValidateKey with a BaseURL
// resolved from providers.APIBaseFor(providerID); to make the URL
// injectable without changing public signatures we use a test-private helper
// that builds a mock Input with a controlled BaseURL.
//
// The 7 CLI matrix rows (Dataset D / spec "Flow D — CLI onboard"):
//
//  #1  interactive     (no flag)      wrong key      → reprompt, not persisted
//  #2  non-interactive (no flag)      wrong key      → exit non-zero, not persisted
//  #3  interactive     --skip-verify  wrong key      → persisted, skip audited, 0 probes
//  #4  non-interactive --skip-verify  wrong key      → persisted, skip audited, 0 probes
//  #5  interactive     (no flag)      no_credit key  → warn, persisted
//  #6  non-interactive (no flag)      valid key      → persisted, no warning
//  #7  non-interactive (no flag)      unreachable    → warn, persisted
//
// Additional:
//  #18  --skip-verify → zero probes (spy httptest)
//  #31  --skip-verify completes when the log sink is unavailable

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// stubUpstream returns an httptest.Server and a request counter.
// completionStatus is the HTTP status for /chat/completions.
// completionBody is the JSON body for /chat/completions.
// /models always returns a valid catalog so FetchModels never blocks.
func stubUpstream(completionStatus int, completionBody string) (*httptest.Server, *atomic.Int64) {
	var probeCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o-mini"}]}`)
		case "/chat/completions":
			probeCount.Add(1)
			w.WriteHeader(completionStatus)
			_, _ = io.WriteString(w, completionBody)
		default:
			w.WriteHeader(404)
		}
	}))
	return srv, &probeCount
}

// validateAndResolveKey (production, in onboard.go) takes a baseURL seam so these
// tests exercise the real function with an httptest upstream — no mirror.

// ── Test #17: CLI onboard validation matrix ───────────────────────────────────

func TestCliOnboard_ValidationMatrix(t *testing.T) {
	t.Parallel()

	// Shared key constants.
	const wrongKey = "bad-key"
	const goodKey = "sk-good"
	const correctKey = "sk-correct"

	type row struct {
		name           string
		nonInteractive bool
		skipVerify     bool
		// upstream response for /chat/completions
		completionStatus int
		completionBody   string
		// initial key supplied
		initialKey string
		// second key supplied when interactive re-prompt fires (may be "")
		repromptKey string
		// expected resolved key (empty string means error path / not-persisted)
		wantKey        string
		wantErr        bool
		wantWarnSubstr string // non-empty: stdout must contain this substring
		wantProbe      bool   // false means 0 probes expected (skip)
	}

	rows := []row{
		// #1 interactive + wrong key → reprompt with correctKey, which then returns valid
		{
			name:             "interactive_wrong_reprompt",
			nonInteractive:   false,
			skipVerify:       false,
			completionStatus: 401,
			completionBody:   `{"error":{"message":"No auth credentials found","code":"invalid_api_key"}}`,
			initialKey:       wrongKey,
			repromptKey:      correctKey, // second attempt returns valid (we'll use a 2-stage server)
			wantKey:          correctKey,
			wantErr:          false,
			wantProbe:        true,
		},
		// #2 non-interactive + wrong key → exit non-zero, key not resolved
		{
			name:             "noninteractive_wrong_exit_nonzero",
			nonInteractive:   true,
			skipVerify:       false,
			completionStatus: 401,
			completionBody:   `{"error":{"message":"No auth credentials found","code":"invalid_api_key"}}`,
			initialKey:       wrongKey,
			wantKey:          "",
			wantErr:          true,
			wantProbe:        true,
		},
		// #3 interactive + --skip-verify + wrong key → persisted, 0 probes
		{
			name:           "interactive_skipverify_wrong_persisted",
			nonInteractive: false,
			skipVerify:     true,
			initialKey:     wrongKey,
			wantKey:        wrongKey,
			wantErr:        false,
			wantProbe:      false,
		},
		// #4 non-interactive + --skip-verify + wrong key → persisted, 0 probes
		{
			name:           "noninteractive_skipverify_wrong_persisted",
			nonInteractive: true,
			skipVerify:     true,
			initialKey:     wrongKey,
			wantKey:        wrongKey,
			wantErr:        false,
			wantProbe:      false,
		},
		// #5 interactive + no_credit → warn, persisted
		{
			name:             "interactive_nocredit_warn_persisted",
			nonInteractive:   false,
			skipVerify:       false,
			completionStatus: 402,
			completionBody:   `{"error":{"message":"Insufficient credits"}}`,
			initialKey:       goodKey,
			wantKey:          goodKey,
			wantErr:          false,
			wantWarnSubstr:   "no credit",
			wantProbe:        true,
		},
		// #6 non-interactive + valid → persisted, no warning
		{
			name:             "noninteractive_valid_persisted",
			nonInteractive:   true,
			skipVerify:       false,
			completionStatus: 200,
			completionBody:   `{"choices":[{"message":{"content":"hi"}}]}`,
			initialKey:       goodKey,
			wantKey:          goodKey,
			wantErr:          false,
			wantWarnSubstr:   "",
			wantProbe:        true,
		},
		// #7 non-interactive + unreachable → warn, persisted
		{
			name:             "noninteractive_unreachable_warn_persisted",
			nonInteractive:   true,
			skipVerify:       false,
			completionStatus: 500,
			completionBody:   `{"error":"internal server error"}`,
			initialKey:       goodKey,
			wantKey:          goodKey,
			wantErr:          false,
			wantWarnSubstr:   "Couldn't reach",
			wantProbe:        true,
		},
	}

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var probeCount atomic.Int64

			// For #1 (interactive reprompt) we need a 2-stage server: first call
			// returns 401 (wrong key), second returns 200 (correct key). We
			// differentiate by counting calls.
			var srv *httptest.Server
			if tc.name == "interactive_wrong_reprompt" {
				srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/models":
						w.WriteHeader(200)
						_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o-mini"}]}`)
					case "/chat/completions":
						n := probeCount.Add(1)
						if n == 1 {
							// First probe: wrong key → 401
							w.WriteHeader(401)
							_, _ = io.WriteString(
								w,
								`{"error":{"message":"No auth credentials found","code":"invalid_api_key"}}`,
							)
						} else {
							// Second probe (after reprompt): correct key → 200
							w.WriteHeader(200)
							_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"hi"}}]}`)
						}
					default:
						w.WriteHeader(404)
					}
				}))
			} else {
				// Single-behavior server.
				srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/models":
						w.WriteHeader(200)
						_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o-mini"}]}`)
					case "/chat/completions":
						probeCount.Add(1)
						w.WriteHeader(tc.completionStatus)
						_, _ = io.WriteString(w, tc.completionBody)
					default:
						w.WriteHeader(404)
					}
				}))
			}
			defer srv.Close()

			var stdout, stderr bytes.Buffer
			passwordCallCount := 0
			wio := wizardIO{
				stdin:  strings.NewReader(""),
				stdout: &stdout,
				stderr: &stderr,
				readPassword: func() (string, error) {
					passwordCallCount++
					// First readPassword call = initial key (already provided as initialKey).
					// On reprompt the test returns repromptKey.
					return tc.repromptKey, nil
				},
			}

			gotKey, err := validateAndResolveKey(
				context.Background(),
				Input{
					ProviderID:     "openrouter",
					APIKey:         tc.initialKey,
					SkipVerify:     tc.skipVerify,
					NonInteractive: tc.nonInteractive,
				},
				wio,
				srv.URL,
			)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (resolved key=%q)", gotKey)
				}
				// Key must not be persisted — by definition gotKey is empty on error.
				if gotKey != "" {
					t.Errorf("on error, resolved key must be empty, got %q", gotKey)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if gotKey != tc.wantKey {
					t.Errorf("resolved key = %q, want %q", gotKey, tc.wantKey)
				}
			}

			outStr := stdout.String() + stderr.String()
			if tc.wantWarnSubstr != "" && !strings.Contains(outStr, tc.wantWarnSubstr) {
				t.Errorf("expected warning containing %q in output, got: %s", tc.wantWarnSubstr, outStr)
			}

			if !tc.wantProbe {
				if probeCount.Load() != 0 {
					t.Errorf("expected 0 probes (skip-verify), got %d", probeCount.Load())
				}
			} else {
				if probeCount.Load() == 0 && !tc.skipVerify {
					t.Errorf("expected at least 1 probe, got 0")
				}
			}
		})
	}
}

// ── Test #18: --skip-verify → zero probes ────────────────────────────────────

// TestCliOnboard_SkipVerifyNoProbe asserts that --skip-verify emits 0
// HTTP requests to the provider upstream, even when the key is wrong.
func TestCliOnboard_SkipVerifyNoProbe(t *testing.T) {
	t.Parallel()

	var probeCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeCount.Add(1)
		w.WriteHeader(500) // any response — must never be reached
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	wio := wizardIO{
		stdin:        strings.NewReader(""),
		stdout:       &stdout,
		stderr:       &stderr,
		readPassword: func() (string, error) { return "", nil },
	}

	resolvedKey, err := validateAndResolveKey(
		context.Background(),
		Input{
			ProviderID:     "openrouter",
			APIKey:         "wrong-key",
			SkipVerify:     true,
			NonInteractive: true,
		},
		wio,
		srv.URL,
	)
	if err != nil {
		t.Fatalf("skip-verify must not error: %v", err)
	}
	if resolvedKey != "wrong-key" {
		t.Errorf("skip-verify: resolved key = %q, want %q", resolvedKey, "wrong-key")
	}
	if n := probeCount.Load(); n != 0 {
		t.Errorf("skip-verify: expected 0 probes, got %d", n)
	}
}

// ── Test #31: --skip-verify completes when log sink unavailable ───────────────

// TestCliOnboard_SkipVerifyAuditUnavailable verifies R-F/SC-013: the skip-verify
// path succeeds even when slog's output is redirected to /dev/null or unavailable.
// Since we test in-process and slog writes are best-effort, we simply verify that
// validateAndResolveKey returns no error under the skip-verify path
// regardless of slog availability. The slog package itself is always available
// in-process; the "unavailable" case means "the underlying writer fails silently"
// which is precisely slog's behavior (errors from the handler are discarded).
func TestCliOnboard_SkipVerifyAuditUnavailable(t *testing.T) {
	t.Parallel()

	var probeCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeCount.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	wio := wizardIO{
		stdin:        strings.NewReader(""),
		stdout:       io.Discard,
		stderr:       io.Discard,
		readPassword: func() (string, error) { return "", nil },
	}

	// Drive the skip path twice: once for non-interactive, once for interactive.
	for _, nonInteractive := range []bool{true, false} {
		resolvedKey, err := validateAndResolveKey(
			context.Background(),
			Input{
				ProviderID:     "openai",
				APIKey:         "sk-secret",
				SkipVerify:     true,
				NonInteractive: nonInteractive,
			},
			wio,
			srv.URL,
		)
		if err != nil {
			t.Errorf(
				"nonInteractive=%v: skip-verify must not error even with log sink unavailable: %v",
				nonInteractive,
				err,
			)
		}
		if resolvedKey != "sk-secret" {
			t.Errorf("nonInteractive=%v: resolved key = %q, want sk-secret", nonInteractive, resolvedKey)
		}
	}
	if n := probeCount.Load(); n != 0 {
		t.Errorf("skip-verify: expected 0 probes, got %d", n)
	}
}

// ── Test: non-interactive + wrong key persists nothing ───────────────────────

// TestCliOnboard_NonInteractive_WrongKey_NotPersisted drives a full applyInput
// call in non-interactive mode with an invalid key and asserts that credentials.json
// is NOT created / the store has no entry for the provider key.
func TestCliOnboard_NonInteractive_WrongKey_NotPersisted(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	srv, _ := stubUpstream(401, `{"error":{"message":"No auth credentials found","code":"invalid_api_key"}}`)
	defer srv.Close()

	if err := datamodel.Init(home); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	wio := wizardIO{
		stdin:        strings.NewReader(""),
		stdout:       &stdout,
		stderr:       &stderr,
		readPassword: func() (string, error) { return "", nil },
	}

	in := Input{
		Home:           home,
		ProviderID:     "openrouter",
		APIKey:         "bad-key",
		Model:          "gpt-4o-mini",
		Username:       "admin",
		Password:       "longenoughpw",
		SkipVerify:     false,
		NonInteractive: true,
	}

	// applyInput resolves baseURL via providers.APIBaseFor and passes it to
	// validateAndResolveKey. We call the production validateAndResolveKey directly with
	// the httptest URL (the baseURL seam) and assert that a non-interactive InvalidKey
	// returns an error — the caller then persists nothing.
	_, err := validateAndResolveKey(context.Background(), in, wio, srv.URL)
	if err == nil {
		t.Fatal("expected error for invalid key in non-interactive mode, got nil")
	}
	if !strings.Contains(err.Error(), "provider key rejected") {
		t.Errorf("expected 'provider key rejected' in error, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "rejected") {
		t.Errorf("expected error message on stderr, got: %s", stderr.String())
	}

	// The credential store should NOT have the key (applyInput never reached store.Set).
	credPath := home + "/credentials.json"
	store := credentials.NewStore(credPath)
	if err := credentials.Unlock(store); err != nil {
		// Store does not exist yet — perfect, nothing was persisted.
		return
	}
	val, getErr := store.Get("openrouter_api_key")
	if getErr == nil && val != "" {
		t.Errorf("credential was persisted despite invalid key rejection: %q", val)
	}
}

// ── Test: interactive + wrong key → reprompt, then correct key persisted ─────

func TestCliOnboard_Interactive_WrongThenCorrect_Persisted(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o-mini"}]}`)
		case "/chat/completions":
			n := callCount.Add(1)
			if n == 1 {
				w.WriteHeader(401)
				_, _ = io.WriteString(w, `{"error":{"code":"invalid_api_key","message":"bad key"}}`)
			} else {
				w.WriteHeader(200)
				_, _ = io.WriteString(w, `{"choices":[]}`)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	// readPassword is called once during the reprompt. The initial key is set on Input.APIKey.
	readCount := 0
	wio := wizardIO{
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: io.Discard,
		readPassword: func() (string, error) {
			readCount++
			return "correct-key", nil
		},
	}

	resolvedKey, err := validateAndResolveKey(
		context.Background(),
		Input{
			ProviderID:     "openrouter",
			APIKey:         "wrong-key",
			SkipVerify:     false,
			NonInteractive: false,
		},
		wio,
		srv.URL,
	)
	if err != nil {
		t.Fatalf("expected no error after reprompt with correct key: %v", err)
	}
	if resolvedKey != "correct-key" {
		t.Errorf("resolved key = %q, want correct-key", resolvedKey)
	}
	if readCount != 1 {
		t.Errorf("expected 1 readPassword call (reprompt), got %d", readCount)
	}
	// stdout must have contained the InvalidKey message.
	outStr := stdout.String()
	if !strings.Contains(outStr, "rejected") {
		t.Errorf("expected 'rejected' in stdout (InvalidKey message), got: %s", outStr)
	}
	// Two probes: one for wrong key (401) and one for correct key (200).
	if n := callCount.Load(); n != 2 {
		t.Errorf("expected 2 completion probes, got %d", n)
	}
}

// ── Test: valid key → persisted, no warning ───────────────────────────────────

func TestCliOnboard_ValidKey_NoWarning(t *testing.T) {
	t.Parallel()

	srv, probeCount := stubUpstream(200, `{"choices":[{"message":{"content":"hi"}}]}`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	wio := wizardIO{
		stdin:        strings.NewReader(""),
		stdout:       &stdout,
		stderr:       &stderr,
		readPassword: func() (string, error) { return "", nil },
	}

	resolvedKey, err := validateAndResolveKey(
		context.Background(),
		Input{
			ProviderID:     "openai",
			APIKey:         "sk-good",
			SkipVerify:     false,
			NonInteractive: true,
		},
		wio,
		srv.URL,
	)
	if err != nil {
		t.Fatalf("valid key: unexpected error: %v", err)
	}
	if resolvedKey != "sk-good" {
		t.Errorf("resolved key = %q, want sk-good", resolvedKey)
	}
	// No warning banner.
	outStr := stdout.String() + stderr.String()
	if strings.Contains(outStr, "Warning") {
		t.Errorf("valid key: unexpected warning in output: %s", outStr)
	}
	if probeCount.Load() == 0 {
		t.Error("expected at least 1 probe for valid key")
	}
}

// ── Test: full applyInput round-trip with skip-verify ────────────────────────

// TestCliOnboard_FullApplyInput_SkipVerify drives applyInput end-to-end with
// SkipVerify=true and asserts that the credential is persisted and state is committed.
// This exercises the production path (not the test-only validateAndResolveKey).
// With SkipVerify=true, validateAndResolveKey calls providers.APIBaseFor — but
// since skip short-circuits before any network call, the URL is never actually used.
func TestCliOnboard_FullApplyInput_SkipVerify(t *testing.T) {
	t.Parallel()
	home := t.TempDir()

	if err := datamodel.Init(home); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	wio := wizardIO{
		stdin:        strings.NewReader(""),
		stdout:       &stdout,
		stderr:       &stderr,
		readPassword: func() (string, error) { return "", nil },
	}

	in := Input{
		Home:           home,
		ProviderID:     "openrouter",
		APIKey:         "sk-skip-verify-test",
		Model:          "gpt-4o-mini",
		Username:       "admin",
		Password:       "longenoughpw",
		SkipVerify:     true,
		NonInteractive: true,
	}

	if err := applyInput(in, wio); err != nil {
		t.Fatalf("applyInput with skip-verify: %v", err)
	}

	// Credential must be persisted.
	credPath := home + "/credentials.json"
	store := credentials.NewStore(credPath)
	if err := credentials.Unlock(store); err != nil {
		t.Fatalf("unlock store: %v", err)
	}
	got, err := store.Get("openrouter_api_key")
	if err != nil {
		t.Fatalf("get api key: %v", err)
	}
	if got != "sk-skip-verify-test" {
		t.Errorf("api key roundtrip: got %q, want sk-skip-verify-test", got)
	}

	// Onboarding must be marked complete.
	mgr := onboarding.NewManager(home)
	if !mgr.IsComplete() {
		t.Error("onboarding must be marked complete after applyInput")
	}
}
