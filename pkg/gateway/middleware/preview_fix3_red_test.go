// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package middleware_test — RED regression, #798 fix round 3, finding A7
// (architect gate finding 7): the planted-cookie error is not the contract
// envelope. Hard Constraint #8 (contract-first wire formats).
//
// TEST PLAN (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: the planted-cookie guard's typed 4xx response
//     body validates against the GENERATED ErrorResponse type — the human
//     message lives in the REQUIRED `error` field and the machine code in
//     `code`. Hard Constraint #8: bytes crossing the gateway/SPA boundary
//     must be runtime-validatable against the contract.
//
//   - Specification source (oracle — never the implementation):
//     docs/internal/specs/adr-094-preview-isolation-spec.md line 381 (FR-015
//     typed error): "the gateway's standard JSON error envelope with a
//     machine-readable code (planted_cookie_cleared) and the human message
//     \"Omnipus cleared cookies set by a preview — please retry\" (Q4)".
//     "Standard envelope" is defined by contracts/components/schemas/
//     ErrorResponse.yaml: `error` (REQUIRED, human text) + `code` (optional
//     machine code) — Go type pkg/api/generated
//     .openapi_types.gen.go::ErrorResponse.
//
//   - Unit boundary: the REAL PlantedCookieGuard middleware wrapped around
//     a plain inner handler over a real httptest.Server. No mocks; the
//     guard is self-contained (detection on the raw Cookie header).
//
//   - Case table:
//     | case                                          | request                                   | expected                                    | source          |
//     |-----------------------------------------------|-------------------------------------------|---------------------------------------------|-----------------|
//     | RED: state-changing body is the contract type | POST, duplicate omnipus-session cookie    | 403; body unmarshals into ErrorResponse     | spec line 381   |
//     |                                               |                                           | with error==message, code==planted_cookie_cleared |          |
//     | pin: GET duplicate is not the typed 403       | GET, duplicate omnipus-session cookie     | 200 from inner; >=1 Set-Cookie clear line   | DS-5 row 1      |
//     | pin: single occurrence never intercepts       | POST, one omnipus-session cookie          | 200 from inner; zero Set-Cookie lines       | DS-5 row 6      |
//
//   - What would break this (CHECK mutations): drop `error` from the body
//     (survives the old {"code","message"} shape — the RED state); swap the
//     code constant; drop the clear set on the GET path; intercept
//     single-occurrence requests.
//
//   - Known gaps: envelope VALIDITY (type-correct bytes) is asserted here;
//     the SPA toast/Retry surfacing (orders 27/28) is frontend scope.

package middleware_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

func TestFix3PlantedCookieError_IsGeneratedEnvelope(t *testing.T) {
	// Message and code are verbatim spec oracle values (line 381 / Q4) —
	// written from the spec, never copied from the handler.
	const (
		wantCode    = "planted_cookie_cleared"
		wantMessage = "Omnipus cleared cookies set by a preview — please retry"
	)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("next-ran"))
	})
	srv := httptest.NewServer(middleware.PlantedCookieGuard()(inner))
	defer srv.Close()

	client := srv.Client()

	t.Run("state-changing duplicate gets the contract envelope", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/agents",
			nil)
		require.NoError(t, err)
		req.Header.Set("Cookie", "omnipus-session=real-session-value; omnipus-session=planted-by-preview")
		req.Header.Set("X-Csrf-Token", "irrelevant-to-this-guard") // canonical MIME form (canonicalheader); the guard keys on Cookie, not this header

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		require.Equal(t, http.StatusForbidden, resp.StatusCode,
			"a duplicated reserved cookie on a state-changing request must be refused with 403")

		// The bytes must be the generated contract type: required `error`
		// carries the human message, `code` the machine code.
		var env generated.ErrorResponse
		bodyBytes, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "reading the 403 body must not fail")
		require.NoError(t, json.Unmarshal(bodyBytes, &env),
			"the 403 body must be valid JSON for the generated ErrorResponse type")

		assert.Equal(t, wantMessage, env.Error,
			"the human message must live in the REQUIRED error field (spec line 381)")
		require.NotNil(t, env.Code, "the machine-readable code field must be present")
		assert.Equal(t, wantCode, *env.Code)
	})

	t.Run("GET duplicate is not the typed 403 but still gets the clear set", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
		require.NoError(t, err)
		req.Header.Set("Cookie", "omnipus-session=real-session-value; omnipus-session=planted-by-preview")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"FR-015: a GET with a planted duplicate must fall through (the credential read fails, not a typed 403)")
		assert.Equal(t, "next-ran", string(body))
		assert.NotEmpty(t, resp.Header.Values("Set-Cookie"),
			"the FR-015 clear set must still be stamped on the GET fall-through")
	})

	t.Run("single occurrence is never intercepted", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/agents", nil)
		require.NoError(t, err)
		req.Header.Set("Cookie", "omnipus-session=only-one-occurrence")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"DS-5 row 6: one occurrence of a reserved name is not a planted duplicate")
		assert.Empty(t, resp.Header.Values("Set-Cookie"))
	})
}
