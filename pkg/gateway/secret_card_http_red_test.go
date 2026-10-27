// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Secret-card HTTP contract (K5, §6.2, §6.7, BDD-6.15, BDD-6.16, FR-083).
// Requests go through the production route table (registerAdditionalEndpoints),
// not a hand-written handler. A stdlib "404 page not found" means the route
// is not registered yet — that is the missing endpoint, reported as BLOCKED
// with the spec reference, never a skip.
package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// secretSubmitPath is K5's submit route. The id is one the server has not minted.
const secretSubmitPath = "/api/v1/secret-requests/req-not-minted/submit"

func secretCardMux(t *testing.T, bypass bool) (*restAPI, *http.ServeMux) {
	t.Helper()
	withEdition(t, config.EditionCore)
	api := newTestRestAPIWithHomeDevModeBypass(t, bypass)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return api, mux
}

func secretCardDo(t *testing.T, api *restAPI, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = uniqueTestSourceIP()
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// secretRouteBlocked fails the test when the production mux has no secret-card
// route. The spec's status codes are unreachable until the route exists.
func secretRouteBlocked(t *testing.T, rec *httptest.ResponseRecorder, what, spec string) {
	t.Helper()
	body := rec.Body.String()
	if rec.Code == http.StatusNotFound && strings.Contains(body, "page not found") {
		t.Fatalf("BLOCKED: %s is not registered on the production mux — required by %s (got the stdlib 404 page, not a secret-card response)", what, spec)
	}
}

// TestSecretSubmit_Unauthenticated_Is401 is BDD-6.16's first row: no
// authentication → 401, and nothing about a secret value is written.
func TestSecretSubmit_Unauthenticated_Is401(t *testing.T) {
	api, mux := secretCardMux(t, false)
	rec := secretCardDo(t, api, mux, http.MethodPost, secretSubmitPath, `{"values":{"api_key":"abcd"}}`)
	secretRouteBlocked(t, rec, "POST /api/v1/secret-requests/{id}/submit", "K5, BDD-6.16, §6.2")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated submit: got %d %q, want 401 (BDD-6.16)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Fatalf("401 body = %q, want the unauthorized refusal (docs/troubleshooting.md)", rec.Body.String())
	}
}

// TestSecretGet_Unauthenticated_Is401 is the value-free state read (FR-079):
// an unauthenticated caller is 401, not a leaked state.
func TestSecretGet_Unauthenticated_Is401(t *testing.T) {
	api, mux := secretCardMux(t, false)
	rec := secretCardDo(t, api, mux, http.MethodGet, "/api/v1/secret-requests/req-not-minted", "")
	secretRouteBlocked(t, rec, "GET /api/v1/secret-requests/{id}", "K5, FR-079, §6.2")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated state read: got %d %q, want 401", rec.Code, rec.Body.String())
	}
}

// TestSecretSubmit_UnknownID_Is404 is BDD-6.16: an authenticated caller and
// an unknown request id → 404, value-free, no resume.
func TestSecretSubmit_UnknownID_Is404(t *testing.T) {
	api, mux := secretCardMux(t, true)
	const sentinel = "abcd-unknown-id-sentinel"
	rec := secretCardDo(t, api, mux, http.MethodPost, secretSubmitPath,
		`{"values":{"api_key":"`+sentinel+`"}}`)
	secretRouteBlocked(t, rec, "POST /api/v1/secret-requests/{id}/submit", "K5, BDD-6.16")
	body := rec.Body.String()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown request id: got %d %q, want 404 (BDD-6.16)", rec.Code, body)
	}
	if strings.Contains(body, sentinel) {
		t.Fatalf("404 body echoed the submitted value: %q", body)
	}
}

// TestSecretSubmit_OversizedBody_Is413 is §6.2 / dataset D2 row 9: a body
// over 65,536 bytes is 413. The body is not a secret.
func TestSecretSubmit_OversizedBody_Is413(t *testing.T) {
	api, mux := secretCardMux(t, true)
	// 65,537 bytes is max+1 (§6.2). Content is padding, not a credential.
	body := strings.Repeat("x", 65537)
	rec := secretCardDo(t, api, mux, http.MethodPost, secretSubmitPath, body)
	secretRouteBlocked(t, rec, "POST /api/v1/secret-requests/{id}/submit", "§6.2 body limit, BDD-6.16")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body of %d bytes: got %d %q, want 413", len(body), rec.Code, rec.Body.String())
	}
}

// TestSecretSubmit_NoReAuth_RawRouteStillReAuth is spec test 69 / BDD-6.15 /
// FR-083. The card submit must not demand password re-entry. The raw
// credential route must still demand it.
func TestSecretSubmit_NoReAuth_RawRouteStillReAuth(t *testing.T) {
	withEdition(t, config.EditionCore)
	api := newCredVaultReAuthTestAPI(t)

	raw := httptest.NewRequest(http.MethodPost, "/api/v1/credentials",
		strings.NewReader(`{"key":"TEST_KEY","value":"hunter2"}`))
	raw.Header.Set("Content-Type", "application/json")
	raw = withReAuthAdminNoToken(raw)
	rawRec := httptest.NewRecorder()
	api.HandleCredentials(rawRec, raw)
	if rawRec.Code != http.StatusForbidden || !strings.Contains(strings.ToLower(rawRec.Body.String()), "re-typing your password") {
		t.Fatalf("raw POST /api/v1/credentials without re-auth: got %d %q, want 403 naming password re-entry (FR-083, BDD-6.15)",
			rawRec.Code, rawRec.Body.String())
	}

	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	// Authenticated (user on the context, same identity the raw route saw)
	// and deliberately no re-auth token.
	card := httptest.NewRequest(http.MethodPost, secretSubmitPath, strings.NewReader(`{"values":{"api_key":"abcd"}}`))
	card.Header.Set("Content-Type", "application/json")
	card = withReAuthAdminNoToken(card)
	card = card.WithContext(context.WithValue(card.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig()))
	cardRec := httptest.NewRecorder()
	mux.ServeHTTP(cardRec, card)
	secretRouteBlocked(t, cardRec, "POST /api/v1/secret-requests/{id}/submit", "FR-083, BDD-6.15, K5")
	cardBody := cardRec.Body.String()
	if cardRec.Code == http.StatusForbidden && strings.Contains(strings.ToLower(cardBody), "re-typing your password") {
		t.Fatalf("secret-card submit demanded password re-entry (%q) — FR-083 forbids that; raw routes keep it", cardBody)
	}
}

// TestSecretSubmit_ValuesAndCancel_NotBoth is §6.2 / dataset D2 row 10:
// a body that sets both values and cancel is 400 with a value-free message.
// The id is unknown, and §6.7 checks that the request exists before payload
// validity — so 404 (unknown id) is also a correct refusal that writes
// nothing. 200 is never correct. A router 404 is BLOCKED, not this case.
func TestSecretSubmit_ValuesAndCancel_NotBoth(t *testing.T) {
	api, mux := secretCardMux(t, true)
	const sentinel = "both-values-and-cancel"
	rec := secretCardDo(t, api, mux, http.MethodPost, secretSubmitPath,
		`{"values":{"api_key":"`+sentinel+`"},"cancel":true}`)
	secretRouteBlocked(t, rec, "POST /api/v1/secret-requests/{id}/submit", "§6.2 exclusivity, dataset D2 row 10")
	body, _ := io.ReadAll(rec.Result().Body)
	text := string(body)
	switch rec.Code {
	case http.StatusBadRequest, http.StatusNotFound:
		if strings.Contains(text, sentinel) {
			t.Fatalf("exclusivity refusal echoed the value: %q", text)
		}
	default:
		t.Fatalf("values and cancel together: got %d %q, want 400 (payload) or 404 (unknown id is checked first, §6.7)", rec.Code, text)
	}
}
