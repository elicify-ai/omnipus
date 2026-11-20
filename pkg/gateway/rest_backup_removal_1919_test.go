// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_backup_removal_1919_test.go — RED tests for issue #1919 (founder
// decision P-Q3b B, 2026-11-19): Backup and Restore is removed from
// Settings, Data. This file pins the server half of the new truth: once the
// feature is removed, every backup route (list, create, restore, any other)
// answers exactly the way the router answers a path no handler claims —
// the unknown-route answer, 404 or 405 — in the LOCAL edition, the only
// edition that ever registered these routes.
//
// Oracle anchor: the spec says "as the router does for an unknown route",
// so the expected value is not hard-coded — a never-registered control path
// is probed at run time and every removed route must match it, with the
// control itself pinned to 404/405. The registration mux is built through
// registerAdditionalEndpoints (the same function production wiring calls)
// using the auth_mode_test.go harness; CSRF/snapshot middleware wraps the
// mux at boot, not at registration, and is out of scope here exactly as in
// routes_admin_test.go.
package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestBackupRoutesRemoved_1919_AnswerLikeUnknownRoutes(t *testing.T) {
	withEdition(t, config.EditionCore) // local edition: the only mode that ever registered these routes
	api := newAuthModeTestAPI(t)       // harness clears OMNIPUS_BEARER_TOKEN and configures no users
	mux := authModeRealMux(t, api)

	// Control: a path nothing registers. The unknown-route answer is read
	// from the router itself, per the spec wording, and pinned to 404/405
	// so "equal to the control" can never quietly mean "equal to 200".
	control := probeMuxStatus_1919(t, mux, http.MethodGet, "/api/v1/no-such-route-1919")
	require.Truef(t, control == http.StatusNotFound || control == http.StatusMethodNotAllowed,
		"control path /api/v1/no-such-route-1919 must answer like an unknown route (404 or 405), got %d — the unknown-route oracle is stale, fix the control before trusting this test", control)

	removed := []struct{ method, path string }{
		// The three removed routes, their canonical methods (issue #1919:
		// "backups list/create/restore").
		{http.MethodGet, "/api/v1/backups"},  // list
		{http.MethodPost, "/api/v1/backup"},  // create
		{http.MethodPost, "/api/v1/restore"}, // restore
		// Wrong-method probes: "any other" backup route must equally stop
		// answering. The registration mux has no method-aware patterns, so
		// today each of these reaches the handler chain (auth challenge);
		// after removal none of them may answer differently from the control.
		{http.MethodGet, "/api/v1/backup"},
		{http.MethodGet, "/api/v1/restore"},
		{http.MethodPost, "/api/v1/backups"},
		{http.MethodPut, "/api/v1/backups"},
		{http.MethodDelete, "/api/v1/backups"},
		{http.MethodPatch, "/api/v1/restore"},
	}
	for _, tc := range removed {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			got := probeMuxStatus_1919(t, mux, tc.method, tc.path)
			require.Equalf(t, control, got,
				"%s %s must answer exactly like an unknown route (%d) once Backup and Restore is removed (issue #1919); got %d — the route still exists and answered",
				tc.method, tc.path, control, got)
		})
	}
}

// probeMuxStatus_1919 sends a bare, unauthenticated request through the
// registered mux and returns the status code. Unauthenticated on purpose: a
// registered route answers its auth challenge (401) before anything else, an
// unknown route answers 404 from the mux — so any status other than the
// control's proves a handler still claims the path.
func probeMuxStatus_1919(t *testing.T, mux *http.ServeMux, method, path string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if body := bodyForMethod_1919(method); body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w.Code
}

func bodyForMethod_1919(method string) string {
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		return "{}"
	}
	return ""
}
