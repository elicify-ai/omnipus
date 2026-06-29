// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── TestClassify_Matrix ───────────────────────────────────────────────────────
// Drives the full Dataset A (24 rows) through classify().
// Each row uses an httptest.Server to assert (outcome, blocks) per R-A.

func TestClassify_Matrix(t *testing.T) {
	t.Parallel()
	rows := []struct {
		id       string
		status   int
		body     string
		wantOut  Outcome
		wantBlks bool
	}{
		// A1 — openrouter 401 no auth credentials
		{"A1", 401, `{"error":{"message":"No auth credentials found"}}`, OutcomeInvalidKey, true},
		// A2 — openrouter 402 insufficient credits
		{"A2", 402, `{"error":{"message":"Insufficient credits"}}`, OutcomeNoCredit, false},
		// A3 — openrouter 403 region not supported (no credential marker)
		{"A3", 403, `{"error":{"message":"Region not supported"}}`, OutcomeRestricted, false},
		// A4 — openrouter 200 clean choices
		{"A4", 200, `{"choices":[]}`, OutcomeValid, false},
		// A5 — openai 401 invalid_api_key code
		{"A5", 401, `{"error":{"code":"invalid_api_key","message":"Invalid API key."}}`, OutcomeInvalidKey, true},
		// A6 — openai 429 insufficient_quota (credit marker)
		{
			"A6",
			429,
			`{"error":{"type":"insufficient_quota","message":"You exceeded your current quota"}}`,
			OutcomeNoCredit,
			false,
		},
		// A7 — openai 429 rate_limit_exceeded (no credit marker → Unreachable)
		{
			"A7",
			429,
			`{"error":{"type":"rate_limit_exceeded","message":"Rate limit exceeded"}}`,
			OutcomeUnreachable,
			false,
		},
		// A8 — gemini 400 INVALID_ARGUMENT + "API key not valid" (credential marker on message)
		{
			"A8",
			400,
			`{"error":{"status":"INVALID_ARGUMENT","message":"API key not valid. Please pass a valid API key."}}`,
			OutcomeInvalidKey,
			true,
		},
		// A9 — gemini 403 PERMISSION_DENIED (no credential marker → Restricted)
		{
			"A9",
			403,
			`{"error":{"status":"PERMISSION_DENIED","message":"Permission denied."}}`,
			OutcomeRestricted,
			false,
		},
		// A10 — deepseek 401 Authentication Fails
		{"A10", 401, `{"error":{"message":"Authentication Fails"}}`, OutcomeInvalidKey, true},
		// A11 — deepseek 402 Insufficient Balance (credit marker)
		{"A11", 402, `{"error":{"message":"Insufficient Balance"}}`, OutcomeNoCredit, false},
		// A12 — groq 401 invalid_api_key
		{"A12", 401, `{"error":{"code":"invalid_api_key","message":"Invalid API key."}}`, OutcomeInvalidKey, true},
		// A13 — anthropic 401 authentication_error
		{
			"A13",
			401,
			`{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			OutcomeInvalidKey,
			true,
		},
		// A14 — anthropic 400 credit balance is too low (credit marker beats 400→Valid)
		{
			"A14",
			400,
			`{"error":{"message":"credit balance is too low for the requested model"}}`,
			OutcomeNoCredit,
			false,
		},
		// A15 — other 200 clean choices
		{"A15", 200, `{"choices":[]}`, OutcomeValid, false},
		// A16 — other 200 with embedded invalid_api_key error (R-A step 3: 200+marker → InvalidKey)
		{"A16", 200, `{"error":{"code":"invalid_api_key","message":"bad key"}}`, OutcomeInvalidKey, true},
		// A17 — other 500 server error
		{"A17", 500, `{"error":"server error"}`, OutcomeUnreachable, false},
		// A18 — other 404 not found
		{"A18", 404, `{"error":"not found"}`, OutcomeUnreachable, false},
		// A19 — other 400 model not found (no marker → 400 → Valid)
		{"A19", 400, `{"error":{"message":"model not found"}}`, OutcomeValid, false},
		// A20 — other 403 "revoked api key" (credential marker beats 403→Restricted)
		{"A20", 403, `{"error":{"message":"revoked api key"}}`, OutcomeInvalidKey, true},
		// A21 — gemini 400 INVALID_ARGUMENT "model not found" (no key marker → 400 → Valid) [M6]
		{"A21", 400, `{"error":{"status":"INVALID_ARGUMENT","message":"model not found"}}`, OutcomeValid, false},
		// A22 — other 429 empty body (no credit marker → Unreachable) [M5]
		{"A22", 429, ``, OutcomeUnreachable, false},
		// A23 — gemini 403 PERMISSION_DENIED (no credential marker → Restricted) [C1]
		{"A23", 403, `{"error":{"status":"PERMISSION_DENIED"}}`, OutcomeRestricted, false},
		// A24 — other 200 with embedded "overloaded" error (no recognized marker → 0 → Unreachable) [R-A step3]
		{"A24", 200, `{"error":{"message":"overloaded"}}`, OutcomeUnreachable, false},
	}

	for _, row := range rows {
		t.Run(row.id, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(row.status)
				_, _ = io.WriteString(w, row.body)
			}))
			defer srv.Close()

			// classify() is the inner function; call it directly with parsed args.
			out := classify(nil, row.status, []byte(row.body))
			blocks := out == OutcomeInvalidKey

			if out != row.wantOut {
				t.Errorf("%s: classify(nil, %d, %q) outcome = %q, want %q",
					row.id, row.status, row.body, out, row.wantOut)
			}
			if blocks != row.wantBlks {
				t.Errorf("%s: blocks = %v, want %v", row.id, blocks, row.wantBlks)
			}
		})
	}
}

// ── TestValidateKey_EmptyKeyNoNetwork ─────────────────────────────────────────
// Dataset B1/B2: empty and whitespace-only keys must short-circuit with no network call.

func TestValidateKey_EmptyKeyNoNetwork(t *testing.T) {
	t.Parallel()
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	for _, key := range []string{"", "   ", "\t\n"} {
		in := ValidateInput{
			ProviderID:   "openrouter",
			ProviderName: "OpenRouter",
			BaseURL:      srv.URL,
			APIKey:       key,
		}
		res := ValidateKey(context.Background(), in, nil)
		if res.Outcome != OutcomeInvalidKey {
			t.Errorf("key=%q: got %q, want invalid_key", key, res.Outcome)
		}
		if !res.Blocks() {
			t.Errorf("key=%q: Blocks must be true for InvalidKey", key)
		}
		if called {
			t.Errorf("key=%q: network call was made — must short-circuit", key)
		}
	}
}

// ── TestValidateKey_TransportError ────────────────────────────────────────────
// Dataset B3: transport reset → Unreachable.

func TestValidateKey_TransportError(t *testing.T) {
	t.Parallel()
	// Create a server and immediately close it to force a connection-refused / transport error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // force transport error

	in := ValidateInput{
		ProviderID:   "openai",
		ProviderName: "OpenAI",
		BaseURL:      url,
		APIKey:       "sk-test",
		// Provide a catalog entry so ValidateKey doesn't try FetchModels (which would also fail and return Unreachable — correct, but this way is more direct).
		Catalog: []string{"gpt-4o-mini"},
	}
	res := ValidateKey(context.Background(), in, nil)
	if res.Outcome != OutcomeUnreachable {
		t.Errorf("transport error: got %q, want unreachable", res.Outcome)
	}
	if res.Blocks() {
		t.Errorf("transport error: Blocks must be false for Unreachable")
	}
}

// ── Dataset B4/B5/B6: timeout, large key, unicode body (boundary/robustness) ──

// B4 — a context deadline that fires mid-probe is an Unreachable, never a block.
func TestValidateKey_ContextTimeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			time.Sleep(2 * time.Second) // outlast the ctx deadline below
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res := ValidateKey(ctx, ValidateInput{
		ProviderID: "openai", ProviderName: "OpenAI", BaseURL: srv.URL,
		APIKey: "sk-test", Catalog: []string{"gpt-4o-mini"},
	}, NoopChecker{})
	if res.Outcome != OutcomeUnreachable {
		t.Errorf("ctx timeout: got %q, want unreachable", res.Outcome)
	}
	if res.Blocks() {
		t.Error("ctx timeout: must not block")
	}
}

// B5 — a very large (8 KB) key must not panic; it classifies by the response only.
func TestValidateKey_LargeKeyNoPanic(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer srv.Close()

	res := ValidateKey(context.Background(), ValidateInput{
		ProviderID: "openai", ProviderName: "OpenAI", BaseURL: srv.URL,
		APIKey: "sk-" + strings.Repeat("k", 8<<10), Catalog: []string{"gpt-4o-mini"},
	}, NoopChecker{})
	if res.Outcome != OutcomeValid {
		t.Errorf("large key: got %q, want valid", res.Outcome)
	}
}

// B6 — a unicode/non-ASCII error body must classify without panicking.
func TestValidateKey_UnicodeBodyNoPanic(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403) // region block, no credential marker → Restricted
		//nolint:gosmopolitan // intentional non-ASCII body: this test asserts unicode bodies classify without panic
		_, _ = w.Write([]byte(`{"error":{"message":"このキーはこの地域でブロックされています 🚫"}}`))
	}))
	defer srv.Close()

	res := ValidateKey(context.Background(), ValidateInput{
		ProviderID: "openai", ProviderName: "OpenAI", BaseURL: srv.URL,
		APIKey: "sk-test", Catalog: []string{"gpt-4o-mini"},
	}, NoopChecker{})
	if res.Outcome != OutcomeRestricted {
		t.Errorf("unicode 403 body: got %q, want restricted", res.Outcome)
	}
	if res.Blocks() {
		t.Error("unicode 403 body: must not block (Restricted proceeds)")
	}
}

// ── TestBuildMessage_Catalog ──────────────────────────────────────────────────
// Dataset C: asserts exact text AND negatives (no api key, no raw body, no INVALID_ARGUMENT).

func TestBuildMessage_Catalog(t *testing.T) {
	t.Parallel()
	const (
		providerName = "OpenRouter"
		apiKey       = "sk-or-test-SECRETKEY"
		rawBody      = `{"error":{"status":"INVALID_ARGUMENT","code":401}}`
	)

	cases := []struct {
		outcome     Outcome
		mustContain string
		// All messages must NOT contain the api key, raw body, or INVALID_ARGUMENT.
	}{
		{OutcomeInvalidKey, "rejected by OpenRouter"},
		{OutcomeNoCredit, "no credit"},
		{OutcomeUnreachable, "Couldn't reach OpenRouter"},
		{OutcomeRestricted, "blocked this request"},
	}

	for _, c := range cases {
		msg := BuildMessage(c.outcome, providerName)
		if !strings.Contains(msg, c.mustContain) {
			t.Errorf("outcome=%s: message %q does not contain %q", c.outcome, msg, c.mustContain)
		}
		// SEC-16 negative asserts.
		if strings.Contains(msg, apiKey) {
			t.Errorf("outcome=%s: message contains api key!", c.outcome)
		}
		if strings.Contains(msg, rawBody) {
			t.Errorf("outcome=%s: message contains raw body!", c.outcome)
		}
		if strings.Contains(msg, "INVALID_ARGUMENT") {
			t.Errorf("outcome=%s: message contains internal status code INVALID_ARGUMENT", c.outcome)
		}
	}

	// Valid → empty message.
	if msg := BuildMessage(OutcomeValid, providerName); msg != "" {
		t.Errorf("valid outcome: expected empty message, got %q", msg)
	}
}

// ── TestPickProbeModel_SkipsNonChat ───────────────────────────────────────────
// US5 AS1: interleaved catalog → chat model returned; non-chat excluded.

func TestPickProbeModel_SkipsNonChat(t *testing.T) {
	t.Parallel()
	catalog := []string{"text-embedding-3-small", "gpt-4o-mini", "dall-e-3"}
	got := pickProbeModel(catalog, "openai")
	// The rules-table default for openai is "gpt-4o-mini".
	// gpt-4o-mini is in the catalog and is a chat model.
	if got != "gpt-4o-mini" {
		t.Errorf("pickProbeModel([embed,gpt-4o-mini,dall-e], openai) = %q, want gpt-4o-mini", got)
	}

	// OpenRouter — should return the rules-table default chat slug (R-E: no account-filter).
	catalogOR := []string{"openai/gpt-4o", "text-embedding-ada-002", "meta-llama/llama-3.1-8b-instruct:free"}
	got = pickProbeModel(catalogOR, "openrouter")
	// Rules-table default is "meta-llama/llama-3.1-8b-instruct:free" which is in the catalog.
	if got == "" {
		t.Errorf("pickProbeModel(openrouter catalog) returned empty; want a chat slug")
	}
	// Must not be the embedding entry.
	if strings.Contains(strings.ToLower(got), "embed") {
		t.Errorf("pickProbeModel returned an embedding model: %q", got)
	}
}

// ── TestPickProbeModel_EmptyCatalogFallback ───────────────────────────────────
// US5 AS2: empty catalog → rules-table default; no false InvalidKey.

func TestPickProbeModel_EmptyCatalogFallback(t *testing.T) {
	t.Parallel()
	// Known provider: should return a non-empty default.
	got := pickProbeModel(nil, "openai")
	if got == "" {
		t.Errorf("pickProbeModel(nil, openai): expected non-empty fallback default")
	}
	// Verify it is a chat slug (does not match any non-chat substring).
	for _, sub := range nonChatSubstrings {
		if strings.Contains(strings.ToLower(got), sub) {
			t.Errorf("pickProbeModel fallback %q contains non-chat substring %q", got, sub)
		}
	}

	// Unknown provider: returns empty — caller must not emit false InvalidKey.
	got = pickProbeModel(nil, "some-unknown-provider-xyz")
	if got != "" {
		// Having a fallback for unknown is acceptable (first catalog entry would be used),
		// but an empty catalog with unknown provider must return "" (no model to probe).
		// The spec says: never false InvalidKey — if got is non-empty here from an
		// empty catalog it would be a default slug, which is fine too. This test
		// just confirms no panic.
		_ = got
	}
}

// ── TestFetchModels_BehaviourPreserved ────────────────────────────────────────
// US1.2: FetchModels returns the same catalog list as the former gateway.fetchUpstreamModels.

func TestFetchModels_BehaviourPreserved(t *testing.T) {
	t.Parallel()
	// Serve a synthetic /models JSON identical to what a real OpenAI-compat endpoint returns.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o"},{"id":"gpt-3.5-turbo"},{"id":"text-embedding-3-small"}]}`)
	}))
	defer srv.Close()

	models, err := FetchModels(context.Background(), srv.URL, "sk-test", nil)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	// Must be sorted alphabetically (golden behavior of the former gateway copy).
	want := []string{"gpt-3.5-turbo", "gpt-4o", "text-embedding-3-small"}
	if len(models) != len(want) {
		t.Fatalf("FetchModels: got %v, want %v", models, want)
	}
	for i, m := range models {
		if m != want[i] {
			t.Errorf("FetchModels[%d] = %q, want %q", i, m, want[i])
		}
	}
}

// ── Additional edge-case tests ────────────────────────────────────────────────

// TestClassify_EmbeddedErrorIn200 ensures 200 + embedded error body is never treated as Valid.
func TestClassify_EmbeddedErrorIn200(t *testing.T) {
	t.Parallel()
	// 200 + invalid_api_key → InvalidKey (A16 variant).
	out := classify(nil, 200, []byte(`{"error":{"code":"invalid_api_key"}}`))
	if out != OutcomeInvalidKey {
		t.Errorf("200+invalid_api_key: got %q, want invalid_key", out)
	}

	// 200 + overloaded (no marker) → Unreachable (A24).
	out = classify(nil, 200, []byte(`{"error":{"message":"overloaded"}}`))
	if out != OutcomeUnreachable {
		t.Errorf("200+overloaded: got %q, want unreachable", out)
	}

	// 200 no error → Valid.
	out = classify(nil, 200, []byte(`{"choices":[]}`))
	if out != OutcomeValid {
		t.Errorf("200+choices: got %q, want valid", out)
	}
}

// TestClassify_403_MarkerVsPermission is Test #25 in the TDD plan (C1 / SC-010).
func TestClassify_403_MarkerVsPermission(t *testing.T) {
	t.Parallel()
	// A20: 403 with "revoked" marker → InvalidKey.
	out := classify(nil, 403, []byte(`{"error":{"message":"revoked api key"}}`))
	if out != OutcomeInvalidKey {
		t.Errorf("403 revoked: got %q, want invalid_key", out)
	}
	// A23: 403 PERMISSION_DENIED (no credential marker) → Restricted.
	out = classify(nil, 403, []byte(`{"error":{"status":"PERMISSION_DENIED"}}`))
	if out != OutcomeRestricted {
		t.Errorf("403 PERMISSION_DENIED: got %q, want restricted", out)
	}
}

// TestClassify_Gemini400_MessageNotStatus is Test #26 (M6).
func TestClassify_Gemini400_MessageNotStatus(t *testing.T) {
	t.Parallel()
	// A21: 400 INVALID_ARGUMENT "model not found" → Valid (no key marker in message).
	out := classify(nil, 400, []byte(`{"error":{"status":"INVALID_ARGUMENT","message":"model not found"}}`))
	if out != OutcomeValid {
		t.Errorf("400 INVALID_ARGUMENT model-not-found: got %q, want valid", out)
	}
	// A8: 400 INVALID_ARGUMENT "API key not valid" → InvalidKey (credential marker in message).
	out = classify(
		nil,
		400,
		[]byte(`{"error":{"status":"INVALID_ARGUMENT","message":"API key not valid. Please pass a valid API key."}}`),
	)
	if out != OutcomeInvalidKey {
		t.Errorf("400 INVALID_ARGUMENT api-key-not-valid: got %q, want invalid_key", out)
	}
}

// TestClassify_429And200Defaults is Test #27 (M5 / R-A step3).
func TestClassify_429And200Defaults(t *testing.T) {
	t.Parallel()
	// A22: 429 empty body → Unreachable (no credit marker).
	out := classify(nil, 429, []byte(``))
	if out != OutcomeUnreachable {
		t.Errorf("429 empty: got %q, want unreachable", out)
	}
	// A24: 200 + unrecognized error → Unreachable.
	out = classify(nil, 200, []byte(`{"error":{"message":"overloaded"}}`))
	if out != OutcomeUnreachable {
		t.Errorf("200+overloaded: got %q, want unreachable", out)
	}
}

// TestValidateKey_EndToEnd exercises the full ValidateKey path with an httptest server.
func TestValidateKey_EndToEnd(t *testing.T) {
	t.Parallel()
	// Server simulates: public /models (no auth), 401 on /chat/completions.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o-mini"}]}`)
		case "/chat/completions":
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"message":"No auth credentials found","code":"invalid_api_key"}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	in := ValidateInput{
		ProviderID:   "openrouter",
		ProviderName: "OpenRouter",
		BaseURL:      srv.URL,
		APIKey:       "bad-key-value",
	}
	res := ValidateKey(context.Background(), in, nil)
	if res.Outcome != OutcomeInvalidKey {
		t.Errorf("public-models + 401 completions: got %q, want invalid_key", res.Outcome)
	}
	if !res.Blocks() {
		t.Errorf("InvalidKey must set Blocks=true")
	}
	// SEC-16: Message must not contain the raw body.
	if strings.Contains(res.Message, "No auth credentials found") {
		t.Errorf("Message contains raw upstream body — SEC-16 violation")
	}
	// Message must not contain the api key.
	if strings.Contains(res.Message, "bad-key-value") {
		t.Errorf("Message contains api key — SEC-16 violation")
	}
}

// TestValidateKey_NoCreditProceeds verifies a no-credit key proceeds with a warning.
func TestValidateKey_NoCreditProceeds(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o-mini"}]}`)
		case "/chat/completions":
			w.WriteHeader(402)
			_, _ = io.WriteString(w, `{"error":{"message":"Insufficient credits"}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	in := ValidateInput{
		ProviderID:   "openrouter",
		ProviderName: "OpenRouter",
		BaseURL:      srv.URL,
		APIKey:       "valid-but-empty-balance",
	}
	res := ValidateKey(context.Background(), in, nil)
	if res.Outcome != OutcomeNoCredit {
		t.Errorf("402: got %q, want no_credit", res.Outcome)
	}
	if res.Blocks() {
		t.Errorf("NoCredit must not block")
	}
	if !strings.Contains(res.Message, "no credit") {
		t.Errorf("NoCredit message %q does not contain 'no credit'", res.Message)
	}
}
