// Issue #716 gate handoff — pins the documented decision that the LEGACY
// /api/v1/media/{uuid} route stays on OPTIONAL auth (rest.go's registration
// comment: #716 keeps it as the accepted secret-link, its 122-bit UUID
// randomness being the control; the module CLAUDE.md documents the same).
//
// characterization test: this pins current, deliberately-chosen behaviour —
// an unauthenticated request reaches the handler instead of answering 401.
// It is a tripwire against accidental over-tightening (the opposite regression
// of the one #716 fixed), not a claim that optional auth is the ideal.
//
// The request drives the production route table through the production
// middleware chain (serveRegistered), so a registration flip from
// withOptionalAuth to withAuth is exactly what it observes. The harness wires
// no media store, so the handler itself answers 503 "media store not
// available" — proof the request got past the auth wrapper and into
// HandleMedia.

package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMediaLegacyRoute_NoCredentials_ReachesHandler_Not401(t *testing.T) {
	api, _ := newTestRestAPI(t)

	// A well-formed opaque media id (the legacy route takes one path segment;
	// no store is wired, so reaching the handler yields the handler's own 503).
	rec := serveRegistered(t, api, http.MethodGet, "/api/v1/media/0123456789abcdef0123456789abcdef", nil, nil)

	require.NotEqual(t, http.StatusUnauthorized, rec.Code,
		"the legacy media route must stay optional-auth; an unauthenticated request must not 401 (issue #716 secret-link decision); body=%s",
		rec.Body.String())
	require.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"with no media store wired the request must reach HandleMedia itself; body=%s", rec.Body.String())
}
