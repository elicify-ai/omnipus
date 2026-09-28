// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestCheckBearerAuth_401BodyIsContractErrorResponse pins Hard Constraint #8
// for every rejection branch of checkBearerAuth: contracts/openapi.yaml's
// 401Unauthorized response is application/json with the ErrorResponse
// schema, so the body must decode as gen.ErrorResponse (no unknown fields,
// non-empty error). Before the fix every branch answered with http.Error's
// text/plain "unauthorized: …" line, and a client parsing the documented
// JSON (retention.spec.ts:398, `await listResp.json()`) threw
// `Unexpected token 'u', "unauthoriz"... is not valid JSON`.
func TestCheckBearerAuth_401BodyIsContractErrorResponse(t *testing.T) {
	withUser := &config.Config{Gateway: config.GatewayConfig{
		Users: []config.UserConfig{{
			Username: "someone",
			Tokens: []config.TokenEntry{{
				Hash: config.BcryptHash("$2a$10$notarealbcrypthashvalueXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"),
			}},
		}},
	}}
	noAccounts := &config.Config{}

	cases := []struct {
		name     string
		cfg      *config.Config
		envToken string
		bearer   string // "" = no Authorization header
	}{
		{name: "no bearer and no session cookie", cfg: withUser},
		{name: "bearer matches no configured account", cfg: withUser, bearer: "omnipus_" + strings.Repeat("a", 64)},
		{name: "no accounts, no env token, no bypass", cfg: noAccounts, bearer: "anything"},
		{name: "no accounts, env token mismatch", cfg: noAccounts, envToken: "the-real-env-token", bearer: "wrong-env-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMNIPUS_BEARER_TOKEN", tc.envToken)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			if tc.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}

			got := checkBearerAuth(context.Background(), w, r, tc.cfg)

			require.False(t, got.Authenticated, "setup: this branch must reject")
			require.Equal(t, http.StatusUnauthorized, w.Code)
			assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "application/json"),
				"401 Content-Type = %q, want application/json (contract 401Unauthorized)", w.Header().Get("Content-Type"))
			var body gen.ErrorResponse
			dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
			dec.DisallowUnknownFields()
			require.NoError(t, dec.Decode(&body),
				"401 body %q must decode as the contract's ErrorResponse", w.Body.String())
			assert.NotEmpty(t, body.Error, "ErrorResponse.error is required and must say why")
		})
	}
}
