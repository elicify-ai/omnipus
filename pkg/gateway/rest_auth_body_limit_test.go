//go:build !cgo

// B1.1 backend half: regression test for the 1 MiB body cap on
// withOptionalAuth-wrapped routes. An anonymous client cannot pin the
// gateway with an unbounded POST body.

package gateway

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWithOptionalAuth_BodyLimit verifies that withOptionalAuth caps request
// bodies at 1 MiB. A 2 MiB POST must produce a *http.MaxBytesError when the
// downstream handler reads r.Body.
func TestWithOptionalAuth_BodyLimit(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	const overLimit = (1 << 20) + 1024 // 1 MiB + 1 KiB
	body := bytes.Repeat([]byte("a"), overLimit)

	var readErr error
	stub := func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		_ = r.Body.Close()
	}
	wrapped := api.withOptionalAuth(stub)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/state", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/octet-stream")
	wrapped(w, r)

	require.Error(t, readErr, "ReadAll must fail when the body exceeds the 1 MiB cap")
	var maxBytesErr *http.MaxBytesError
	require.True(t, errors.As(readErr, &maxBytesErr),
		"err must be *http.MaxBytesError, got %T: %v", readErr, readErr)
	require.Equal(t, int64(1<<20), maxBytesErr.Limit, "limit must be 1 MiB")
}

// TestWithOptionalAuth_UnderLimit confirms a body smaller than 1 MiB is
// allowed through unchanged — the cap is enforced strictly above the limit.
func TestWithOptionalAuth_UnderLimit(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	const underLimit = (1 << 20) - 1024 // 1 MiB - 1 KiB
	body := bytes.Repeat([]byte("b"), underLimit)

	var got []byte
	var readErr error
	stub := func(w http.ResponseWriter, r *http.Request) {
		got, readErr = io.ReadAll(r.Body)
		_ = r.Body.Close()
	}
	wrapped := api.withOptionalAuth(stub)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/state", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/octet-stream")
	wrapped(w, r)

	require.NoError(t, readErr)
	require.Equal(t, underLimit, len(got))
}

// TestWithAuth_BodyLimit_AppliesEvenWithValidAuth (T2.11) verifies that the
// 1 MiB body cap on the withAuth middleware path fires even when a valid
// Bearer token is supplied. This closes the coverage gap flagged by the
// PR-test-analyzer: the existing test only exercised withOptionalAuth; the
// withAuth path has its own MaxBytesReader call (rest.go ~L226) that was
// not previously tested with an over-limit authenticated body.
//
// BDD: Given a valid Bearer token,
//
//	When a POST body > 1 MiB is sent to a withAuth-protected endpoint,
//	Then the downstream handler receives a *http.MaxBytesError when reading r.Body.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 7 (Rank-7)
func TestWithAuth_BodyLimit_AppliesEvenWithValidAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	const overLimit = (1 << 20) + 1024 // 1 MiB + 1 KiB
	body := bytes.Repeat([]byte("z"), overLimit)

	// Use the env-based bearer token so checkBearerAuth passes.
	const testToken = "test-bearer-for-withauth-body-limit"
	t.Setenv("OMNIPUS_BEARER_TOKEN", testToken)

	var readErr error
	innerCalled := false
	stub := func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		_, readErr = io.ReadAll(r.Body)
		_ = r.Body.Close()
	}

	// Wrap with withAuth (not withOptionalAuth) to exercise the withAuth path.
	wrapped := api.withAuth(stub)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Authorization", "Bearer "+testToken)
	wrapped(w, r)

	// The inner handler must be reached (auth passed).
	require.True(t, innerCalled,
		"inner handler must be called — withAuth with valid token must pass through")

	// The body read must fail with MaxBytesError.
	require.Error(t, readErr,
		"T2.11: ReadAll must fail when body > 1 MiB even on withAuth path")
	var maxBytesErr *http.MaxBytesError
	require.True(t, errors.As(readErr, &maxBytesErr),
		"T2.11: err must be *http.MaxBytesError, got %T: %v", readErr, readErr)
	require.Equal(t, int64(1<<20), maxBytesErr.Limit,
		"T2.11: withAuth body cap must be 1 MiB")
}

// TestWithOptionalAuth_BodyLimit_AppliesEvenWithValidAuth (T2.10) verifies
// that the 1 MiB body cap fires even when a valid Bearer token is supplied.
// Previously only anonymous paths were tested; this closes the coverage gap
// for authenticated callers with oversized payloads.
func TestWithOptionalAuth_BodyLimit_AppliesEvenWithValidAuth(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	const overLimit = (1 << 20) + 1024 // 1 MiB + 1 KiB
	body := bytes.Repeat([]byte("c"), overLimit)

	// Set a valid env-based bearer token so withOptionalAuth takes the
	// "authenticated" code path.
	const testToken = "test-bearer-token-body-limit-check"
	t.Setenv("OMNIPUS_BEARER_TOKEN", testToken)

	var readErr error
	stub := func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		_ = r.Body.Close()
	}
	wrapped := api.withOptionalAuth(stub)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/state", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Authorization", "Bearer "+testToken)
	wrapped(w, r)

	require.Error(t, readErr,
		"body cap must fire even with a valid Bearer token (T2.10)")
	var maxBytesErr *http.MaxBytesError
	require.True(t, errors.As(readErr, &maxBytesErr),
		"err must be *http.MaxBytesError, got %T: %v", readErr, readErr)
	require.Equal(t, int64(1<<20), maxBytesErr.Limit,
		"limit must be 1 MiB regardless of auth state")
}
