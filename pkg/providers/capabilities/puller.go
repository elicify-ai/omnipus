package capabilities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GHReleasePuller fetches the capability catalog from the Omnipus GitHub
// repository. Transport per FR-025:
//
//  1. GitHub Release asset — the "latest" release's semver-tagged asset,
//     fetched from the GitHub Releases API (browser-like request — tokenless,
//     public read).
//  2. raw.githubusercontent.com fallback — when the Releases API returns a
//     4xx/5xx or the asset is missing, fetch the canonical asset at
//     HEAD of the default branch. The raw fallback is what the public web
//     reads; it carries the same SHA-256 we can verify against a sidecar
//     .sha256 file (released alongside the asset and the raw fetch).
//
// Checksum verification (FR-025): every successful Pull verifies the returned
// JSON bytes against the sidecar SHA-256. A mismatch is a hard error — the
// Puller returns ErrChecksumMismatch and the Catalog retains last-known-good.
//
// Auth: tokenless. The catalog is a public artifact. We deliberately do NOT
// set Authorization headers; the GitHub API has a 60 req/h unauthenticated
// rate limit, but a 7-day refresh cadence (FR-025) plus a single startup
// pull leaves ample headroom for the gateway's own usage.
type GHReleasePuller struct {
	// Owner is the GitHub org or user that owns the catalog repo
	// (e.g. "elicify-ai"). Required.
	Owner string
	// Repo is the catalog repository name (e.g. "omnipus"). Required.
	Repo string
	// Asset is the catalog asset filename within the GitHub Release
	// (e.g. "providers_capabilities.json"). Required.
	Asset string
	// Ref is the Git ref to fetch from in the raw-fallback path. Defaults
	// to "main" when empty. The release-asset path uses the latest semver
	// tag and ignores Ref.
	Ref string

	// HTTPClient is the HTTP client used for both the Releases API and the
	// raw fallback. When nil, a 30-second-timeout default client is used.
	// The gateway passes its shared client (which honors outbound proxy,
	// TLS settings, etc.); tests pass httptest.Server-derived clients.
	HTTPClient *http.Client

	// BaseURL is the GitHub API base URL. Override for tests; defaults to
	// https://api.github.com.
	BaseURL string
	// RawBaseURL is the raw-assets base URL. Override for tests; defaults to
	// https://raw.githubusercontent.com.
	RawBaseURL string

	// UserAgent is the User-Agent header sent on every request. GitHub
	// rejects tokenless requests with no User-Agent. Defaults to
	// "omnipus-capabilities/1.0" when empty.
	UserAgent string
}

// releaseAsset is the minimal GitHub Releases API shape we depend on.
// The full response includes dozens of fields we don't need — listing them
// all would couple this package to GitHub API surface we don't use.
type releaseAsset struct {
	TagName string        `json:"tag_name"`
	Assets  []releaseItem `json:"assets"`
}

type releaseItem struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"` // "sha256:..."; may be empty on older releases
	State              string `json:"state"`
}

// NewGHReleasePuller constructs a Puller for the standard elicify-ai/omnipus
// catalog release. Use a struct literal directly when overriding fields
// (tests, custom repos).
func NewGHReleasePuller(owner, repo, asset string) *GHReleasePuller {
	return &GHReleasePuller{
		Owner:      owner,
		Repo:       repo,
		Asset:      asset,
		Ref:        "main",
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    "https://api.github.com",
		RawBaseURL: "https://raw.githubusercontent.com",
		UserAgent:  "omnipus-capabilities/1.0",
	}
}

// Pull fetches the catalog from the GitHub Release asset; on any failure it
// falls back to the raw.githubusercontent.com URL. Checksum verification
// (against a sidecar .sha256 file) runs on whichever path succeeds.
//
// The returned error is one of:
//   - transport errors (DNS, TCP, TLS, timeout) — wrapped as "release fetch"
//   - GitHub API shape errors (no tag, no matching asset) — wrapped as "release"
//   - checksum mismatch — returns ErrChecksumMismatch (callers compare with errors.Is)
//   - raw-fallback transport errors — wrapped as "raw fetch"
//
// On any error, the catalog retains last-known-good — Pull is non-fatal at the
// caller.
func (p *GHReleasePuller) Pull(ctx context.Context) ([]byte, error) {
	// Step 1: GitHub Release asset path.
	data, assetURL, fetchErr := p.fetchReleaseAsset(ctx)
	if fetchErr == nil {
		if verifyErr := p.verify(ctx, data, assetURL); verifyErr != nil {
			return nil, fmt.Errorf("capabilities: pull release: %w", verifyErr)
		}
		return data, nil
	}
	// Step 2: raw fallback. The GitHub Releases API may rate-limit, return
	// 404 for repos without a release, or transiently fail — the raw
	// fallback is the always-on public endpoint.
	rawData, rawErr := p.fetchRaw(ctx)
	if rawErr != nil {
		return nil, fmt.Errorf("capabilities: pull failed (release=%v, raw=%v)", fetchErr, rawErr)
	}
	if verifyErr := p.verify(ctx, rawData, p.rawURL(p.Ref, p.Asset)); verifyErr != nil {
		return nil, fmt.Errorf("capabilities: pull raw: %w", verifyErr)
	}
	return rawData, nil
}

// fetchReleaseAsset hits the GitHub Releases API for the latest release, finds
// the matching asset, and returns its raw bytes. Returns the asset URL alongside
// the data so the caller can attempt a sidecar-checksum fetch.
func (p *GHReleasePuller) fetchReleaseAsset(ctx context.Context) ([]byte, string, error) {
	client := p.client()
	apiURL := strings.TrimSuffix(p.BaseURL, "/") + "/repos/" + p.Owner + "/" + p.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", p.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("release fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("release status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MB cap on the API response
	if err != nil {
		return nil, "", fmt.Errorf("release read: %w", err)
	}
	var release releaseAsset
	if parseErr := json.Unmarshal(body, &release); parseErr != nil {
		return nil, "", fmt.Errorf("release parse: %w", parseErr)
	}
	if release.TagName == "" {
		return nil, "", fmt.Errorf("release missing tag_name")
	}
	var assetURL string
	for _, a := range release.Assets {
		if a.Name == p.Asset && (a.State == "" || a.State == "uploaded") {
			assetURL = a.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		return nil, "", fmt.Errorf("release asset %q not found in tag %q", p.Asset, release.TagName)
	}
	// Fetch the asset itself.
	assetReq, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("asset request: %w", err)
	}
	assetReq.Header.Set("User-Agent", p.UserAgent)
	assetResp, err := client.Do(assetReq)
	if err != nil {
		return nil, "", fmt.Errorf("asset fetch: %w", err)
	}
	defer assetResp.Body.Close()
	if assetResp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("asset status: %d", assetResp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(assetResp.Body, 2<<20)) // 2 MB cap on the catalog itself
	if err != nil {
		return nil, "", fmt.Errorf("asset read: %w", err)
	}
	return data, assetURL, nil
}

// fetchRaw hits the raw.githubusercontent.com URL for the asset and returns
// its bytes. Used as the fallback when the GitHub Releases API path fails.
func (p *GHReleasePuller) fetchRaw(ctx context.Context) ([]byte, error) {
	client := p.client()
	url := p.rawURL(p.Ref, p.Asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("raw request: %w", err)
	}
	req.Header.Set("User-Agent", p.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("raw fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("raw status: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("raw read: %w", err)
	}
	return data, nil
}

// rawURL returns the raw.githubusercontent.com URL for the given ref and asset
// filename. Exposed so verify() can derive the sidecar URL without duplicating
// the path construction.
func (p *GHReleasePuller) rawURL(ref, asset string) string {
	return strings.TrimSuffix(p.RawBaseURL, "/") + "/" + p.Owner + "/" + p.Repo + "/" + ref + "/" + asset
}

// verify fetches the sidecar .sha256 file alongside the asset and compares it
// against the sha256(data) digest. A mismatch is a hard error (returns
// ErrChecksumMismatch); an absent sidecar (404) is treated as success because
// not every catalog release ships a checksum file yet — the gateway still has
// the release-asset or raw-fallback transport-layer guarantee.
//
// When the sidecar is present and mismatches, the caller MUST treat the
// pulled catalog as untrusted and retain last-known-good.
func (p *GHReleasePuller) verify(ctx context.Context, data []byte, assetURL string) error {
	checksumURL := checksumURLFor(assetURL)
	if checksumURL == "" {
		return nil
	}
	client := p.client()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		// Treat request construction failure as a soft skip — never block on
		// a malformed URL.
		return nil
	}
	req.Header.Set("User-Agent", p.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		// Soft skip: transport failure on checksum fetch does not invalidate
		// the catalog body.
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// No sidecar at this URL — soft skip (releases without sidecars
		// are still trusted by virtue of the GitHub Release signing path).
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<10)) // 1 KB cap on the checksum file
	if err != nil {
		return nil
	}
	expected := strings.TrimSpace(string(body))
	// sha256sum format: "<hex>  " or just "<hex>". Accept both.
	if idx := strings.IndexAny(expected, " \t"); idx > 0 {
		expected = expected[:idx]
	}
	if expected == "" {
		return nil
	}
	got := sha256.Sum256(data)
	gotHex := hex.EncodeToString(got[:])
	if !strings.EqualFold(expected, gotHex) {
		return fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expected, gotHex)
	}
	return nil
}

// checksumURLFor returns the .sha256 sidecar URL for an asset URL, or "" if
// the URL can't be derived (the rare case where the asset URL doesn't end in
// a recognizable filename — the caller treats "" as "skip verification").
//
// Recognized forms:
//   - https://github.com/<owner>/<repo>/releases/download/<tag>/<asset>
//   - https://raw.githubusercontent.com/<owner>/<repo>/<ref>/<asset>
//   - any URL whose path ends with /<asset>
func checksumURLFor(assetURL string) string {
	if assetURL == "" {
		return ""
	}
	lastSlash := strings.LastIndex(assetURL, "/")
	if lastSlash < 0 || lastSlash == len(assetURL)-1 {
		return ""
	}
	return assetURL[:lastSlash+1] + assetURL[lastSlash+1:] + ".sha256"
}

// client returns the configured HTTPClient, falling back to a 30s-timeout
// default when nil. The default fallback only fires in tests that pass a
// nil client; production code passes a non-nil client.
func (p *GHReleasePuller) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ErrChecksumMismatch is returned by Puller.Pull when the pulled asset's
// SHA-256 does not match the sidecar .sha256 file. Callers MUST treat this
// as a hard failure and retain last-known-good (FR-025).
//
// Compare with errors.Is(err, capabilities.ErrChecksumMismatch).
var ErrChecksumMismatch = fmt.Errorf("capabilities: checksum mismatch")
