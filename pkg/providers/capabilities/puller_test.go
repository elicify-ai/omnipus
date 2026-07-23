package capabilities

// Puller tests — see catalog.go for the package doc and the package-level
// test inventory in catalog_test.go. The package godoc is intentionally
// defined once (catalog.go) to satisfy godoclint.
//
// Traces to: FR-025 (transport), SC-009 (pull failure non-fatal).
//
// Test inventory:
//
//	#1  TestGHReleasePuller_Pull_SuccessPath   — Releases API returns asset → Pull returns its bytes
//	#2  TestGHReleasePuller_Pull_RawFallback   — Releases API fails → raw fallback succeeds
//	#3  TestGHReleasePuller_Pull_ChecksumMatch  — sidecar sha256 matches → success
//	#4  TestGHReleasePuller_Pull_ChecksumMismatch — sidecar sha256 mismatches → ErrChecksumMismatch
//	#5  TestGHReleasePuller_Pull_NoSidecar      — no .sha256 file → success (release-asset trust)
//	#6  TestGHReleasePuller_Pull_AssetNotFound  — Releases API returns but asset missing → falls back
//	#7  TestGHReleasePuller_Pull_BothFail       — both fail → error returned
//	#8  TestGHReleasePuller_RawURL              — ref + asset path construction
//	#9  TestGHReleasePuller_NewDefaults         — fields are populated
//	#10 TestChecksumURLFor                       — derived URL is correct / empty on bad input

import (
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

// validCatalog is a minimal catalog JSON used by the puller tests. It does
// not need to be a real catalog — the puller only validates that it parses
// (it does not parse inside Pull — the Catalog does that on apply). The
// puller does verify the SHA-256 checksum against a sidecar.
func validCatalog() []byte {
	return []byte(`{"version":"test","models":[],"default_resize_budget":{"long_edge_px":1,"max_bytes":1}}`)
}

// sha256Of returns the hex SHA-256 of data — the format the sidecar file uses.
func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ── Test #1 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_SuccessPath is the happy path: the GitHub Releases
// API returns a release with the matching asset; Pull returns the asset bytes.
func TestGHReleasePuller_Pull_SuccessPath(t *testing.T) {
	body := validCatalog()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/elicify-ai/omnipus/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"tag_name": "v1.2.3",
			"assets": [
				{"name": "providers_capabilities.json", "browser_download_url": "%s/asset", "state": "uploaded"}
			]
		}`, srv.URL)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := &GHReleasePuller{
		Owner:      "elicify-ai",
		Repo:       "omnipus",
		Asset:      "providers_capabilities.json",
		Ref:        "main",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		RawBaseURL: srv.URL, // unreachable path; success uses release
		UserAgent:  "test",
	}
	got, err := p.Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── Test #2 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_RawFallback exercises the fallback path: the
// Releases API errors out (simulated by serving a 500), the raw URL returns
// the catalog, and Pull returns the raw bytes.
func TestGHReleasePuller_Pull_RawFallback(t *testing.T) {
	body := validCatalog()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/elicify-ai/omnipus/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	// raw URL: <RawBaseURL>/<Owner>/<Repo>/<Ref>/<Asset>
	mux.HandleFunc(
		"/elicify-ai/omnipus/main/providers_capabilities.json",
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		},
	)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := &GHReleasePuller{
		Owner:      "elicify-ai",
		Repo:       "omnipus",
		Asset:      "providers_capabilities.json",
		Ref:        "main",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		RawBaseURL: srv.URL,
		UserAgent:  "test",
	}
	got, err := p.Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── Test #3 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_ChecksumMatch verifies the success path when the
// sidecar SHA-256 matches the asset bytes.
func TestGHReleasePuller_Pull_ChecksumMatch(t *testing.T) {
	body := validCatalog()
	checksum := sha256Of(body)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/elicify-ai/omnipus/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{
			"tag_name": "v1.2.3",
			"assets": [{"name":"providers_capabilities.json","browser_download_url":"%s/asset","state":"uploaded"}]
		}`, srv.URL)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		// Standard sha256sum format: "<hex>  \n"
		_, _ = fmt.Fprintf(w, "%s  providers_capabilities.json\n", checksum)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := &GHReleasePuller{
		Owner:      "elicify-ai",
		Repo:       "omnipus",
		Asset:      "providers_capabilities.json",
		Ref:        "main",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		RawBaseURL: srv.URL,
		UserAgent:  "test",
	}
	got, err := p.Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── Test #4 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_ChecksumMismatch verifies that a sidecar SHA-256
// that does NOT match the asset bytes is treated as a hard failure.
// ErrChecksumMismatch is returned via errors.Is (the documented contract).
func TestGHReleasePuller_Pull_ChecksumMismatch(t *testing.T) {
	body := validCatalog()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/elicify-ai/omnipus/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{
			"tag_name": "v1.2.3",
			"assets": [{"name":"providers_capabilities.json","browser_download_url":"%s/asset","state":"uploaded"}]
		}`, srv.URL)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		// Deliberately wrong checksum.
		_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  providers_capabilities.json\n")
	})
	// The raw fallback also returns a mismatching sidecar — the puller should
	// NOT fall back silently when the release path fails on checksum.
	mux.HandleFunc(
		"/elicify-ai/omnipus/main/providers_capabilities.json",
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		},
	)
	mux.HandleFunc(
		"/elicify-ai/omnipus/main/providers_capabilities.json.sha256",
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  providers_capabilities.json\n")
		},
	)
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := &GHReleasePuller{
		Owner:      "elicify-ai",
		Repo:       "omnipus",
		Asset:      "providers_capabilities.json",
		Ref:        "main",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		RawBaseURL: srv.URL,
		UserAgent:  "test",
	}
	_, err := p.Pull(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrChecksumMismatch), "expected ErrChecksumMismatch, got %v", err)
}

// ── Test #5 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_NoSidecar verifies that an absent .sha256 sidecar
// is treated as a soft skip (success) — not every catalog release ships a
// checksum file, and the release-asset download itself is signed by GitHub.
func TestGHReleasePuller_Pull_NoSidecar(t *testing.T) {
	body := validCatalog()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/elicify-ai/omnipus/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{
			"tag_name": "v1.2.3",
			"assets": [{"name":"providers_capabilities.json","browser_download_url":"%s/asset","state":"uploaded"}]
		}`, srv.URL)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	// .sha256 deliberately returns 404 (no handler).
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := &GHReleasePuller{
		Owner:      "elicify-ai",
		Repo:       "omnipus",
		Asset:      "providers_capabilities.json",
		Ref:        "main",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		RawBaseURL: srv.URL,
		UserAgent:  "test",
	}
	got, err := p.Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── Test #6 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_AssetNotFound verifies that a release object
// without the expected asset triggers the raw-fallback path. The fallback
// returns the asset; Pull returns the raw bytes.
func TestGHReleasePuller_Pull_AssetNotFound(t *testing.T) {
	body := validCatalog()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/elicify-ai/omnipus/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{
			"tag_name": "v1.2.3",
			"assets": [{"name":"some-other-asset.txt","browser_download_url":"https://example.test/other","state":"uploaded"}]
		}`)
	})
	mux.HandleFunc(
		"/elicify-ai/omnipus/main/providers_capabilities.json",
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		},
	)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := &GHReleasePuller{
		Owner:      "elicify-ai",
		Repo:       "omnipus",
		Asset:      "providers_capabilities.json",
		Ref:        "main",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		RawBaseURL: srv.URL,
		UserAgent:  "test",
	}
	got, err := p.Pull(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// ── Test #7 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_Pull_BothFail verifies that when both the Releases API
// and the raw fallback fail, Pull returns an error (the Catalog caller
// treats this as non-fatal — last-known-good is retained).
func TestGHReleasePuller_Pull_BothFail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/elicify-ai/omnipus/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal", http.StatusInternalServerError)
	})
	mux.HandleFunc(
		"/elicify-ai/omnipus/main/providers_capabilities.json",
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		},
	)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := &GHReleasePuller{
		Owner:      "elicify-ai",
		Repo:       "omnipus",
		Asset:      "providers_capabilities.json",
		Ref:        "main",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		RawBaseURL: srv.URL,
		UserAgent:  "test",
	}
	_, err := p.Pull(context.Background())
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrChecksumMismatch), "both-fail is a transport error, NOT a checksum error")
}

// ── Test #8 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_RawURL verifies the raw-URL construction is exactly
// <RawBaseURL>/<Owner>/<Repo>/<Ref>/<Asset>. Trailing slashes on
// RawBaseURL are trimmed.
func TestGHReleasePuller_RawURL(t *testing.T) {
	p := &GHReleasePuller{
		Owner:      "elicify-ai",
		Repo:       "omnipus",
		Ref:        "main",
		RawBaseURL: "https://raw.githubusercontent.com/",
		Asset:      "providers_capabilities.json",
	}
	assert.Equal(t,
		"https://raw.githubusercontent.com/elicify-ai/omnipus/main/providers_capabilities.json",
		p.rawURL(p.Ref, p.Asset))
}

// ── Test #9 ──────────────────────────────────────────────────────────────────

// TestGHReleasePuller_NewDefaults verifies the constructor populates the
// production-safe defaults (api.github.com / raw.githubusercontent.com,
// 30-second timeout, "omnipus-capabilities/1.0" UA, ref "main").
func TestGHReleasePuller_NewDefaults(t *testing.T) {
	p := NewGHReleasePuller("elicify-ai", "omnipus", "providers_capabilities.json")
	assert.Equal(t, "elicify-ai", p.Owner)
	assert.Equal(t, "omnipus", p.Repo)
	assert.Equal(t, "providers_capabilities.json", p.Asset)
	assert.Equal(t, "main", p.Ref)
	assert.Equal(t, "https://api.github.com", p.BaseURL)
	assert.Equal(t, "https://raw.githubusercontent.com", p.RawBaseURL)
	assert.Equal(t, "omnipus-capabilities/1.0", p.UserAgent)
	require.NotNil(t, p.HTTPClient)
	assert.Equal(t, 30*time.Second, p.HTTPClient.Timeout)
}

// ── Test #10 ─────────────────────────────────────────────────────────────────

// TestChecksumURLFor verifies the sidecar URL derivation:
//   - https://example.test/path/asset.json → https://example.test/path/asset.json.sha256
//   - empty → ""
//   - trailing slash → ""
func TestChecksumURLFor(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"normal asset URL", "https://example.test/path/asset.json", "https://example.test/path/asset.json.sha256"},
		{
			"github release URL",
			"https://github.com/o/r/releases/download/v1/asset.json",
			"https://github.com/o/r/releases/download/v1/asset.json.sha256",
		},
		{
			"raw URL",
			"https://raw.githubusercontent.com/o/r/main/asset.json",
			"https://raw.githubusercontent.com/o/r/main/asset.json.sha256",
		},
		{"empty input", "", ""},
		{"trailing slash", "https://example.test/path/", ""},
		{"no slash", "asset.json", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, checksumURLFor(tc.in))
		})
	}
}
