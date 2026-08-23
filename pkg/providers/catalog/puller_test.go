package catalog

// Puller tests — the GitHub Release transport retargeted at the assembly
// repository (FR-007), the mandatory sidecar (FR-032), the 16 MB size cap
// (FR-009, A-18) and the degraded raw fallback (US-3.AC8).
//
// Traces to: FR-007, FR-009, FR-032, US-2.AC1, US-3.AC8, E1, E11, E14.
//
// Test inventory:
//
//	T8   TestGHReleasePuller_Pull_RetargetedAsset          — owner/repo/asset are the assembly repo's; sidecar hex == SHA-256 of the bytes
//	T9b  TestGHReleasePuller_Pull_NoSidecar_Rejected       — no sidecar in the release asset list → ErrChecksumMismatch; raw path 404 sidecar → same
//	T12  TestRefresh_TooLarge_Rejected                     — cap + 1 → ErrTooLarge (never checksum); exactly cap → accepted
//	T13  TestRefresh_RawFallback_Degraded                  — release 403, raw serves asset + sidecar → bytes returned, LastPullDegraded true with the release error
//
// Carried over from pkg/providers/capabilities (paths retargeted, assertions unchanged):
//
//	#1  TestGHReleasePuller_Pull_SuccessPath
//	#2  TestGHReleasePuller_Pull_RawFallback
//	#3  TestGHReleasePuller_Pull_ChecksumMatch
//	#4  TestGHReleasePuller_Pull_ChecksumMismatch
//	#6  TestGHReleasePuller_Pull_AssetNotFound
//	#7  TestGHReleasePuller_Pull_BothFail
//	#8  TestGHReleasePuller_RawURL
//	#9  TestGHReleasePuller_NewDefaults
//	#11 TestGHReleasePuller_Pull_RawFallback_RecordsDegraded
//	#12 TestGHReleasePuller_Pull_SuccessPath_RecordsNotDegraded
//	#13 TestGHReleasePuller_Pull_BothFail_LeavesPriorDegradedState
//
// Retired: #5 TestGHReleasePuller_Pull_NoSidecar (asserted success on a
// missing sidecar — rewritten as T9b, F-02); #10 TestChecksumURLFor (the
// release path now reads the sidecar from the asset list, so no URL is
// derived).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	releasePath    = "/repos/elicify-ai/omnipus-provider-catalog/releases/latest"
	rawAssetPath   = "/elicify-ai/omnipus-provider-catalog/main/providers_catalog.json"
	rawSidecarPath = rawAssetPath + ".sha256"
)

// validCatalog is a minimal body used by the puller tests. The puller does
// not parse it (ParseDocument runs in the refresh transaction); it only
// verifies the sidecar SHA-256 and the size cap.
func validCatalog() []byte {
	return []byte(`{"schema_version":"2.0.0","version":"v2026.8.22","providers":[]}`)
}

// sha256Of returns the hex SHA-256 of data — the format the sidecar uses.
func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// releaseJSON renders a Releases API document whose asset list names the
// catalog asset and, when withSidecar, its .sha256 sidecar — both pointing
// back at the stub server.
func releaseJSON(srvURL string, withSidecar bool) string {
	assets := fmt.Sprintf(`{"name":%q,"browser_download_url":"%s/asset","state":"uploaded"}`, CatalogAsset, srvURL)
	if withSidecar {
		assets += fmt.Sprintf(`,{"name":%q,"browser_download_url":"%s/asset.sha256","state":"uploaded"}`, CatalogAsset+".sha256", srvURL)
	}
	return `{"tag_name":"v2026.8.22","assets":[` + assets + `]}`
}

// testPuller points a production-defaulted puller at the stub server.
func testPuller(srv *httptest.Server) *GHReleasePuller {
	p := NewGHReleasePuller()
	p.HTTPClient = srv.Client()
	p.BaseURL = srv.URL
	p.RawBaseURL = srv.URL
	p.UserAgent = "test"
	return p
}

// ── T8 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_RetargetedAsset asserts the release layout of
// US-2.AC1: the puller asks the assembly repository's latest release for
// exactly `providers_catalog.json` and its `.sha256` sidecar (both located
// from the release's own asset list), and the sidecar hex equals the
// SHA-256 of the returned bytes.
func TestGHReleasePuller_Pull_RetargetedAsset(t *testing.T) {
	body := validCatalog()
	var srv *httptest.Server
	var hits []string
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		_, _ = io.WriteString(w, releaseJSON(srv.URL, true))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		_, _ = fmt.Fprintf(w, "%s  providers_catalog.json\n", sha256Of(body))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := testPuller(srv)
	got, err := p.Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, sha256Of(got), sha256Of(body))
	assert.Equal(t, []string{releasePath, "/asset", "/asset.sha256"}, hits,
		"release API → asset → sidecar, all from the assembly repo's release")
	degraded, _ := p.LastPullDegraded()
	assert.False(t, degraded)

	// The pinned identity is a code constant, not configuration.
	assert.Equal(t, "elicify-ai", CatalogOwner)
	assert.Equal(t, "omnipus-provider-catalog", CatalogRepo)
	assert.Equal(t, "providers_catalog.json", CatalogAsset)
}

// ── T9b ──────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_NoSidecar_Rejected rewrites the former
// Pull_NoSidecar (which asserted success): with signing not adopted the
// checksum is the only integrity check, so a release whose asset list has
// the catalog but no `.sha256` is rejected with ErrChecksumMismatch — and
// the raw path behaves the same when the sidecar URL 404s. Neither case
// falls through to the other transport.
func TestGHReleasePuller_Pull_NoSidecar_Rejected(t *testing.T) {
	t.Run("release path: asset list lacks the sidecar", func(t *testing.T) {
		body := validCatalog()
		var srv *httptest.Server
		rawHit := false
		mux := http.NewServeMux()
		mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, releaseJSON(srv.URL, false))
		})
		mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		})
		mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
			rawHit = true
			_, _ = w.Write(body)
		})
		mux.HandleFunc(rawSidecarPath, func(w http.ResponseWriter, r *http.Request) {
			rawHit = true
			_, _ = io.WriteString(w, sha256Of(body))
		})
		srv = httptest.NewServer(mux)
		defer srv.Close()

		got, err := testPuller(srv).Pull(context.Background())
		require.Error(t, err)
		assert.Nil(t, got)
		assert.True(t, errors.Is(err, ErrChecksumMismatch), "missing sidecar is a checksum rejection, got %v", err)
		assert.False(t, rawHit, "a release without a sidecar must not silently fall back to raw")
	})

	t.Run("raw path: sidecar 404", func(t *testing.T) {
		body := validCatalog()
		mux := http.NewServeMux()
		mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		})
		mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		})
		// rawSidecarPath deliberately unhandled → 404.
		srv := httptest.NewServer(mux)
		defer srv.Close()

		got, err := testPuller(srv).Pull(context.Background())
		require.Error(t, err)
		assert.Nil(t, got)
		assert.True(t, errors.Is(err, ErrChecksumMismatch), "raw path without a sidecar is a checksum rejection, got %v", err)
	})
}

// ── T12 (puller half) ────────────────────────────────────────────────────────

// TestRefresh_TooLarge_Rejected is the puller half of T12 (E1, E14): a body
// of maxCatalogAssetBytes + 1 is rejected with ErrTooLarge — never
// ErrChecksumMismatch, even though the truncated bytes cannot match the
// sidecar — on both transports; a body of exactly maxCatalogAssetBytes with
// a correct sidecar is accepted. The refresh-transaction half (WARN
// reason=too_large, version unchanged) lands with T067-04.
func TestRefresh_TooLarge_Rejected(t *testing.T) {
	require.Equal(t, 16<<20, maxCatalogAssetBytes, "A-18 pins the cap at 16 MB")

	exactly := bytes.Repeat([]byte("x"), maxCatalogAssetBytes)
	oversize := bytes.Repeat([]byte("x"), maxCatalogAssetBytes+1)

	serve := func(t *testing.T, body []byte, releaseUp bool) *GHReleasePuller {
		t.Helper()
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
			if !releaseUp {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, releaseJSON(srv.URL, true))
		})
		sidecar := func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "%s  providers_catalog.json\n", sha256Of(body))
		}
		asset := func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }
		mux.HandleFunc("/asset", asset)
		mux.HandleFunc("/asset.sha256", sidecar)
		mux.HandleFunc(rawAssetPath, asset)
		mux.HandleFunc(rawSidecarPath, sidecar)
		srv = httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return testPuller(srv)
	}

	t.Run("release path: cap + 1 → too_large, never checksum", func(t *testing.T) {
		got, err := serve(t, oversize, true).Pull(context.Background())
		require.Error(t, err)
		assert.Nil(t, got)
		assert.True(t, errors.Is(err, ErrTooLarge), "got %v", err)
		assert.False(t, errors.Is(err, ErrChecksumMismatch), "an oversize body must be classified by size, not checksum")
	})
	t.Run("release path: exactly cap → accepted", func(t *testing.T) {
		got, err := serve(t, exactly, true).Pull(context.Background())
		require.NoError(t, err)
		assert.Len(t, got, maxCatalogAssetBytes)
	})
	t.Run("raw path: cap + 1 → too_large, never checksum", func(t *testing.T) {
		got, err := serve(t, oversize, false).Pull(context.Background())
		require.Error(t, err)
		assert.Nil(t, got)
		assert.True(t, errors.Is(err, ErrTooLarge), "got %v", err)
		assert.False(t, errors.Is(err, ErrChecksumMismatch))
	})
	t.Run("raw path: exactly cap → accepted", func(t *testing.T) {
		got, err := serve(t, exactly, false).Pull(context.Background())
		require.NoError(t, err)
		assert.Len(t, got, maxCatalogAssetBytes)
	})
}

// ── T13 (puller half) ────────────────────────────────────────────────────────

// TestRefresh_RawFallback_Degraded is the puller half of T13 (US-3.AC8,
// DS-4 row 4): the release API returns 403, the raw URL of the pinned
// `main` ref serves asset + sidecar, Pull returns the bytes, and the
// fallback is reported as degraded carrying the release-path error. The
// refresh-transaction half (`Degraded()` on the catalog) lands with
// T067-04.
func TestRefresh_RawFallback_Degraded(t *testing.T) {
	body := validCatalog()
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc(rawSidecarPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sha256Of(body)+"\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := testPuller(srv)
	got, err := p.Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
	degraded, releaseErr := p.LastPullDegraded()
	assert.True(t, degraded)
	require.Error(t, releaseErr)
	assert.Contains(t, releaseErr.Error(), "release status: 403")
}

// ── #1 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_SuccessPath is the happy path: the Releases API
// returns a release with the matching asset and sidecar; Pull returns the
// asset bytes.
func TestGHReleasePuller_Pull_SuccessPath(t *testing.T) {
	body := validCatalog()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, releaseJSON(srv.URL, true))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sha256Of(body))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	got, err := testPuller(srv).Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── #2 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_RawFallback exercises the fallback path: the
// Releases API errors out (simulated by serving a 429), the raw URL returns
// the catalog and its sidecar, and Pull returns the raw bytes.
func TestGHReleasePuller_Pull_RawFallback(t *testing.T) {
	body := validCatalog()
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc(rawSidecarPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sha256Of(body))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := testPuller(srv).Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── #3 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_ChecksumMatch verifies the success path when the
// sidecar is in sha256sum's two-column format and matches the asset bytes.
func TestGHReleasePuller_Pull_ChecksumMatch(t *testing.T) {
	body := validCatalog()
	checksum := sha256Of(body)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, releaseJSON(srv.URL, true))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		// Standard sha256sum format: "<hex>  <name>\n"
		_, _ = fmt.Fprintf(w, "%s  providers_catalog.json\n", checksum)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	got, err := testPuller(srv).Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── #4 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_ChecksumMismatch verifies that a sidecar SHA-256
// that does NOT match the asset bytes is treated as a hard failure.
// ErrChecksumMismatch is returned via errors.Is (the documented contract).
func TestGHReleasePuller_Pull_ChecksumMismatch(t *testing.T) {
	body := validCatalog()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, releaseJSON(srv.URL, true))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		// Deliberately wrong checksum.
		_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  providers_catalog.json\n")
	})
	// The raw fallback also returns a mismatching sidecar — the puller should
	// NOT fall back silently when the release path fails on checksum.
	mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc(rawSidecarPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  providers_catalog.json\n")
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	_, err := testPuller(srv).Pull(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrChecksumMismatch), "expected ErrChecksumMismatch, got %v", err)
}

// ── #6 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_AssetNotFound verifies that a release object
// without the expected asset triggers the raw-fallback path. The fallback
// returns the asset and sidecar; Pull returns the raw bytes.
func TestGHReleasePuller_Pull_AssetNotFound(t *testing.T) {
	body := validCatalog()
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"tag_name": "v2026.8.22",
			"assets": [{"name":"some-other-asset.txt","browser_download_url":"https://example.test/other","state":"uploaded"}]
		}`)
	})
	mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc(rawSidecarPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sha256Of(body))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := testPuller(srv).Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── #7 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_BothFail verifies that when both the Releases API
// and the raw fallback fail, Pull returns an error (the refresh transaction
// treats this as non-fatal — last-known-good is retained).
func TestGHReleasePuller_Pull_BothFail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal", http.StatusInternalServerError)
	})
	mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := testPuller(srv).Pull(context.Background())
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrChecksumMismatch), "both-fail is a transport error, NOT a checksum error")
	assert.False(t, errors.Is(err, ErrTooLarge))
}

// ── #8 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_RawURL verifies the raw-URL construction is exactly
// <RawBaseURL>/<owner>/<repo>/main/<asset>. Trailing slashes on RawBaseURL
// are trimmed; the ref is the pinned `main` (F-28, no override).
func TestGHReleasePuller_RawURL(t *testing.T) {
	p := &GHReleasePuller{RawBaseURL: "https://raw.githubusercontent.com/"}
	assert.Equal(t,
		"https://raw.githubusercontent.com/elicify-ai/omnipus-provider-catalog/main/providers_catalog.json",
		p.rawURL(CatalogAsset))
	assert.Equal(t,
		"https://raw.githubusercontent.com/elicify-ai/omnipus-provider-catalog/main/providers_catalog.json.sha256",
		p.rawURL(CatalogAsset+".sha256"))
}

// ── #9 ───────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_NewDefaults verifies the constructor populates the
// production-safe defaults (api.github.com / raw.githubusercontent.com,
// 30-second timeout, "omnipus-catalog/2.0" UA).
func TestGHReleasePuller_NewDefaults(t *testing.T) {
	p := NewGHReleasePuller()
	assert.Equal(t, "https://api.github.com", p.BaseURL)
	assert.Equal(t, "https://raw.githubusercontent.com", p.RawBaseURL)
	assert.Equal(t, "omnipus-catalog/2.0", p.UserAgent)
	require.NotNil(t, p.HTTPClient)
	assert.Equal(t, 30*time.Second, p.HTTPClient.Timeout)
}

// ── #11 ──────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_RawFallback_RecordsDegraded is the regression
// test for the review finding that Pull() used to discard the release error
// once the raw fallback succeeded: after a release-fails/raw-succeeds Pull,
// LastPullDegraded reports degraded=true and returns the original
// release-path error.
func TestGHReleasePuller_Pull_RawFallback_RecordsDegraded(t *testing.T) {
	body := validCatalog()
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc(rawSidecarPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sha256Of(body))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := testPuller(srv)

	// Before any Pull, the zero value must report not-degraded (never a
	// stale/uninitialized "degraded" claim).
	degraded, releaseErr := p.LastPullDegraded()
	assert.False(t, degraded, "no Pull has run yet")
	assert.NoError(t, releaseErr)

	got, err := p.Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)

	degraded, releaseErr = p.LastPullDegraded()
	assert.True(t, degraded, "release failed + raw succeeded must be recorded as degraded")
	require.Error(t, releaseErr, "the release-path failure must be preserved, not discarded")
	assert.Contains(t, releaseErr.Error(), "release status: 429")
}

// ── #12 ──────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_SuccessPath_RecordsNotDegraded asserts the
// converse: when the GitHub Release path itself succeeds, LastPullDegraded
// reports degraded=false with a nil releaseErr.
func TestGHReleasePuller_Pull_SuccessPath_RecordsNotDegraded(t *testing.T) {
	body := validCatalog()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, releaseJSON(srv.URL, true))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sha256Of(body))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := testPuller(srv)
	_, err := p.Pull(context.Background())
	require.NoError(t, err)

	degraded, releaseErr := p.LastPullDegraded()
	assert.False(t, degraded, "release path succeeded — must not be reported degraded")
	assert.NoError(t, releaseErr)
}

// ── #13 ──────────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_BothFail_LeavesPriorDegradedState asserts that a
// Pull call which fails entirely (both transports fail) does NOT overwrite
// the previously recorded transport state — recordTransport is only called
// on the two successful return paths, so a wholly failed Pull leaves
// LastPullDegraded describing the data actually in use.
func TestGHReleasePuller_Pull_BothFail_LeavesPriorDegradedState(t *testing.T) {
	body := validCatalog()
	releaseUp := true
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc(releasePath, func(w http.ResponseWriter, r *http.Request) {
		if !releaseUp {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, releaseJSON(srv.URL, true))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sha256Of(body))
	})
	mux.HandleFunc(rawAssetPath, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := testPuller(srv)

	// First Pull succeeds cleanly via the release path.
	_, err := p.Pull(context.Background())
	require.NoError(t, err)
	degraded, _ := p.LastPullDegraded()
	require.False(t, degraded, "precondition: first pull used the release path")

	// Second Pull: both transports fail.
	releaseUp = false
	_, err = p.Pull(context.Background())
	require.Error(t, err, "both transports failing must return an error")

	degradedAfter, releaseErrAfter := p.LastPullDegraded()
	assert.False(t, degradedAfter, "a wholly-failed Pull must not overwrite the prior (not-degraded) state")
	assert.NoError(t, releaseErrAfter)
}
