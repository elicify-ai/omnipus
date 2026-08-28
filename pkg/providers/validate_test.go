// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
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
		remoteBase   = "https://openrouter.ai/api/v1"
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
		msg := BuildMessage(c.outcome, providerName, remoteBase)
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
	if msg := BuildMessage(OutcomeValid, providerName, remoteBase); msg != "" {
		t.Errorf("valid outcome: expected empty message, got %q", msg)
	}
}

// ── TestBuildMessage_Unreachable_LoopbackVsRemote ──────────────────────────────
// Pins the two "unreachable" messages separately: a loopback base URL (Ollama on
// localhost with no server running) must NOT tell the user to check their
// internet connection — it must say the local server isn't running and name it.
// A remote base URL keeps the original network-connectivity advice.
func TestBuildMessage_Unreachable_LoopbackVsRemote(t *testing.T) {
	t.Parallel()

	t.Run("loopback base URL names the local server, not the internet", func(t *testing.T) {
		t.Parallel()
		for _, base := range []string{
			"http://localhost:11434/v1",
			"http://127.0.0.1:11434/v1",
			"http://[::1]:11434/v1",
		} {
			msg := BuildMessage(OutcomeUnreachable, "Ollama", base)
			const want = "Couldn't reach Ollama — the local server doesn't seem to be running. Start Ollama, then try again. Continuing for now; the key will be used as entered."
			if msg != want {
				t.Errorf("base=%q:\n got  %q\n want %q", base, msg, want)
			}
			if strings.Contains(msg, "internet connection") {
				t.Errorf("base=%q: loopback message must not blame the internet connection, got %q", base, msg)
			}
			if strings.Contains(msg, "to check the key") {
				t.Errorf("base=%q: loopback message must not frame this as a key check, got %q", base, msg)
			}
		}
	})

	t.Run("remote base URL keeps the network-connectivity advice", func(t *testing.T) {
		t.Parallel()
		msg := BuildMessage(OutcomeUnreachable, "OpenRouter", "https://openrouter.ai/api/v1")
		const want = "Couldn't reach OpenRouter to check the key — check your internet connection. Continuing for now; the key will be used as entered."
		if msg != want {
			t.Errorf("got  %q\nwant %q", msg, want)
		}
	})

	t.Run("unparsable base URL falls back to the remote advice", func(t *testing.T) {
		t.Parallel()
		// A control character is invalid in a URL and makes url.Parse fail —
		// isLoopbackBaseURL must fail safe (non-loopback) rather than panic
		// or misclassify.
		msg := BuildMessage(OutcomeUnreachable, "CustomProvider", "http://\x7f/v1")
		if strings.Contains(msg, "the local server doesn't seem to be running") {
			t.Errorf("unparsable base URL must not be classified as loopback, got %q", msg)
		}
		if !strings.Contains(msg, "check your internet connection") {
			t.Errorf("unparsable base URL must fall back to network-connectivity advice, got %q", msg)
		}
	})
}

// ── T26 TestPickProbeModel_FromCatalog ───────────────────────────────────────
// FR-022 / A-20: the probe model is the provider's first ACTIVE,
// TOOL-CALLING, TEXT model in DOCUMENT ORDER — read from the catalog, not
// from a hand-typed slug table. The retired table is what this replaces: a
// stale slug there produced a 404 that classified as *Unreachable*, so a
// perfectly good key was reported as "provider unreachable".

func TestPickProbeModel_FromCatalog(t *testing.T) {
	withProbeCatalog(t)

	t.Run("first active tool-calling text model in document order", func(t *testing.T) {
		got := catalogProbeModels("probeshop")
		want := []string{"chat-b", "chat-c", "chat-d"}
		if len(got) != len(want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("candidates = %v, want %v", got, want)
			}
		}
		if got[0] != "chat-b" {
			t.Errorf("first candidate = %q; the document's first row (chat-a) is retired "+
				"and must be skipped", got[0])
		}
	})

	t.Run("bounded to three attempts", func(t *testing.T) {
		if got := catalogProbeModels("probeshop"); len(got) > maxProbeAttempts {
			t.Errorf("candidates = %d, want at most %d", len(got), maxProbeAttempts)
		}
	})

	t.Run("a non-catalog provider yields no candidates", func(t *testing.T) {
		if got := catalogProbeModels("nope"); got != nil {
			t.Errorf("catalogProbeModels(nope) = %v, want nil so the caller falls back to the live list", got)
		}
	})

	t.Run("DefaultProbeModel exposes the first candidate", func(t *testing.T) {
		if got := DefaultProbeModel("probeshop"); got != "chat-b" {
			t.Errorf("DefaultProbeModel = %q, want chat-b", got)
		}
		if got := DefaultProbeModel("nope"); got != "" {
			t.Errorf("DefaultProbeModel(nope) = %q, want empty", got)
		}
	})
}

// TestChatProbeCandidates_LiveListFallback covers the custom/local path,
// where the catalog has no models and the upstream list is all there is.
func TestChatProbeCandidates_LiveListFallback(t *testing.T) {
	t.Parallel()
	got := chatProbeCandidates([]string{
		"text-embedding-3-small", "gpt-4o-mini", "dall-e-3", "chat-1", "chat-2", "chat-3",
	})
	if len(got) != maxProbeAttempts {
		t.Fatalf("candidates = %v, want %d entries", got, maxProbeAttempts)
	}
	if got[0] != "gpt-4o-mini" {
		t.Errorf("first candidate = %q, want gpt-4o-mini (the embedding entry must be skipped)", got[0])
	}
	for _, m := range got {
		for _, sub := range nonChatSubstrings {
			if strings.Contains(strings.ToLower(m), sub) {
				t.Errorf("candidate %q contains the non-chat marker %q", m, sub)
			}
		}
	}
}

// TestValidateKey_FallsThroughOnModelNotFound — F-25: a "that model does not
// exist" answer moves to the NEXT candidate; a credential answer does not.
// Bounded at three attempts so one key check can never become a burst.
func TestValidateKey_FallsThroughOnModelNotFound(t *testing.T) {
	withProbeCatalog(t)

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		seen = append(seen, req.Model)
		if req.Model == "chat-d" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"the model does not exist"}}`))
	}))
	defer srv.Close()

	res := ValidateKey(context.Background(), ValidateInput{
		ProviderID:   "probeshop",
		ProviderName: "ProbeShop",
		BaseURL:      srv.URL,
		APIKey:       "sk-test",
	}, NoopChecker{})

	if res.Outcome != OutcomeValid {
		t.Fatalf("outcome = %q (%s), want valid after falling through to a live model",
			res.Outcome, res.RawDetail)
	}
	want := []string{"chat-b", "chat-c", "chat-d"}
	if len(seen) != len(want) {
		t.Fatalf("probed %v, want exactly %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("probed %v, want %v", seen, want)
		}
	}
}

// TestValidateKey_NoFallThroughOnBadKey — an invalid-key answer is the ANSWER.
// Retrying it against two more models would triple the traffic and tell us
// nothing, and would report the same outcome anyway.
func TestValidateKey_NoFallThroughOnBadKey(t *testing.T) {
	withProbeCatalog(t)

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer srv.Close()

	res := ValidateKey(context.Background(), ValidateInput{
		ProviderID:   "probeshop",
		ProviderName: "ProbeShop",
		BaseURL:      srv.URL,
		APIKey:       "sk-bad",
	}, NoopChecker{})

	if res.Outcome != OutcomeInvalidKey {
		t.Fatalf("outcome = %q, want invalid_key", res.Outcome)
	}
	if calls != 1 {
		t.Errorf("upstream calls = %d, want exactly 1 — a credential answer is final", calls)
	}
}

// TestValidateKey_NoModelsPreFetchForCatalogProvider — FR-022: the probe path
// makes ZERO `GET /models` calls for a provider the catalog knows.
func TestValidateKey_NoModelsPreFetchForCatalogProvider(t *testing.T) {
	withProbeCatalog(t)

	var gets, posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
		} else {
			posts++
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer srv.Close()

	ValidateKey(context.Background(), ValidateInput{
		ProviderID:   "probeshop",
		ProviderName: "ProbeShop",
		BaseURL:      srv.URL,
		APIKey:       "sk-test",
	}, NoopChecker{})

	if gets != 0 {
		t.Errorf("GET requests = %d, want 0 — the catalog already lists this provider's models", gets)
	}
	if posts != 1 {
		t.Errorf("POST requests = %d, want exactly 1 completion probe", posts)
	}
}

// TestValidateKey_OutcomeClassificationUnchanged — the regression this task
// owes: the probe MODEL now comes from the catalog, and the outcome
// CLASSIFICATION must be byte-for-byte what it was. Each row is an upstream
// answer and the outcome `classify` has always produced for it.
func TestValidateKey_OutcomeClassificationUnchanged(t *testing.T) {
	withProbeCatalog(t)

	tests := []struct {
		name   string
		status int
		body   string
		want   Outcome
	}{
		{"200 completion", http.StatusOK, `{"choices":[{"message":{"content":"hi"}}]}`, OutcomeValid},
		{"401 credential marker", http.StatusUnauthorized, `{"error":{"message":"Incorrect API key provided"}}`, OutcomeInvalidKey},
		{"402 no credit", http.StatusPaymentRequired, `{"error":{"message":"insufficient balance"}}`, OutcomeNoCredit},
		{"403 permission", http.StatusForbidden, `{"error":{"message":"permission_denied for region"}}`, OutcomeRestricted},
		{"500 upstream", http.StatusInternalServerError, `{"error":{"message":"boom"}}`, OutcomeUnreachable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			res := ValidateKey(context.Background(), ValidateInput{
				ProviderID:   "probeshop",
				ProviderName: "ProbeShop",
				BaseURL:      srv.URL,
				APIKey:       "sk-test",
			}, NoopChecker{})
			if res.Outcome != tt.want {
				t.Errorf("outcome = %q, want %q (raw: %s)", res.Outcome, tt.want, res.RawDetail)
			}
			if res.Blocks() != (tt.want == OutcomeInvalidKey) {
				t.Errorf("Blocks() = %v for outcome %q", res.Blocks(), res.Outcome)
			}
		})
	}
}

// probeCatalogJSON is a minimal 2.0.0 document whose single provider exercises
// every branch of the probe-model rule: a retired row, a non-tool-calling row
// and an image-only row all sit AHEAD of the models that qualify.
const probeCatalogJSON = `{
  "schema_version": "2.0.0",
  "version": "v2026.8.22",
  "updated_at": "2026-08-22T06:00:00Z",
  "source": "test",
  "default_resize_limits": {"long_edge_px": 7680, "max_bytes": 10485760},
  "providers": [{
    "id": "probeshop",
    "name": "ProbeShop",
    "company": "ProbeShop",
    "api": "https://api.probeshop.example/v1",
    "protocol": "openai-compatible",
    "tier": "standard",
    "auth_methods": ["api_key"],
    "resize_limits": {"long_edge_px": 7680, "max_bytes": 10485760},
    "models": [
      {"id": "chat-a", "name": "A", "tool_call": true,  "context_window": 8192, "input_modalities": ["text"], "status": "retired"},
      {"id": "embed-1","name": "E", "tool_call": false, "context_window": 8192, "input_modalities": ["text"], "status": "active"},
      {"id": "chat-b", "name": "B", "tool_call": true,  "context_window": 8192, "input_modalities": ["text"], "status": "active"},
      {"id": "chat-c", "name": "C", "tool_call": true,  "context_window": 8192, "input_modalities": ["text","image"], "status": "active"},
      {"id": "chat-d", "name": "D", "tool_call": true,  "context_window": 8192, "input_modalities": ["text"], "status": "active"},
      {"id": "chat-e", "name": "F", "tool_call": true,  "context_window": 8192, "input_modalities": ["text"], "status": "active"}
    ]
  }]
}`

// withProbeCatalog installs probeCatalogJSON as the process catalog for one
// test. These tests never run in parallel: the catalog is process-wide.
func withProbeCatalog(t *testing.T) {
	t.Helper()
	c, err := catalog.NewCatalog([]byte(probeCatalogJSON))
	if err != nil {
		t.Fatalf("parse probe catalog: %v", err)
	}
	SetCatalog(c)
	t.Cleanup(func() { SetCatalog(nil) })
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
