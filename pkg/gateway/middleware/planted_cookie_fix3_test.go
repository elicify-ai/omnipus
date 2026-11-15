// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package middleware — focused fix-round test for the planted-cookie typed
// error's contract envelope (gate round fix3, finding A7). File name carries
// the fix3 marker per the round's dispatch brief.
package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestFix3PlantedCookieErrorResponseEnvelope pins Hard Constraint #8: the
// planted-cookie typed error is the generated ErrorResponse envelope —
// required `error` carrying the verbatim human message, `code` carrying the
// machine-readable planted_cookie_cleared the SPA toast chain keys on
// (src/lib/api/http.ts::request → raisePlantedCookieToast reads err.code,
// then falls back from body `message` to body `error` for the toast text).
// RED before the fix: writePlantedCookieCleared emits {"code","message"} —
// decoding into gen.ErrorResponse yields an empty Error.
func TestFix3PlantedCookieErrorResponseEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	writePlantedCookieCleared(rec)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"the planted-cookie typed error stays a 403")
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var env gen.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env),
		"the body must decode into the generated ErrorResponse envelope (Hard Constraint #8)")
	require.NotNil(t, env.Code,
		"the machine-readable code must survive the envelope switch")
	require.Equal(t, "planted_cookie_cleared", *env.Code,
		"the code is what the SPA toast chain keys on — it must not change")
	require.Equal(t, plantedCookieClearedMessage, env.Error,
		"the required `error` field carries the verbatim human message "+
			"(the SPA toast falls back from body `message` to body `error`)")
}
