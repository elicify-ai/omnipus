package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The assembly repository and its release layout (FR-007, A-5). Pinned in
// code: the puller has no owner/repo/asset/ref configuration (F-28).
const (
	// CatalogOwner is the GitHub org that owns the assembly repository.
	CatalogOwner = "elicify-ai"
	// CatalogRepo is the assembly repository that publishes the daily
	// providers_catalog.json release.
	CatalogRepo = "omnipus-provider-catalog"
	// CatalogAsset is the release asset filename; its sidecar is
	// CatalogAsset + ".sha256".
	CatalogAsset = "providers_catalog.json"
	// catalogSidecar is the mandatory checksum sidecar (FR-032).
	catalogSidecar = CatalogAsset + ".sha256"
	// catalogRef is the branch the raw fallback reads at HEAD. Not
	// configurable (FR-032, F-28).
	catalogRef = "main"

	// maxCatalogAssetBytes is the largest document the puller accepts
	// (FR-009, A-18). The body is read with a limit of cap + 1 byte so an
	// oversize body is classified ErrTooLarge before the checksum is ever
	// computed — never misreported as a checksum mismatch (E14).
	maxCatalogAssetBytes = 16 << 20
	// maxReleaseAPIBytes caps the GitHub Releases API response (F-05).
	maxReleaseAPIBytes = 4 << 20
	// maxSidecarBytes caps the .sha256 sidecar read.
	maxSidecarBytes = 1 << 10
)

// ErrChecksumMismatch is returned by Pull when the pulled asset's SHA-256
// does not match its sidecar, or when the sidecar is absent on the
// transport that served the asset (FR-032). The refresh transaction logs
// reason=checksum and retains the current document.
//
// Compare with errors.Is(err, catalog.ErrChecksumMismatch).
var ErrChecksumMismatch = errors.New("catalog: checksum mismatch")

// ErrTooLarge is returned by Pull when the asset body exceeds
// maxCatalogAssetBytes on the transport that served it (FR-009, E1). The
// refresh transaction logs reason=too_large and retains the current
// document.
var ErrTooLarge = errors.New("catalog: asset too large")

// GHReleasePuller fetches providers_catalog.json from the assembly
// repository's GitHub Release. Transport order (FR-007, unchanged from the
// capabilities-era puller):
//
//  1. GitHub Release asset — the "latest" release of
//     elicify-ai/omnipus-provider-catalog, asset and sidecar both located
//     from the release's own asset list (tokenless public read).
//  2. raw.githubusercontent.com fallback — when the Releases API fails or
//     the asset is missing from the release, asset and sidecar are read at
//     HEAD of `main`. On that path both come from the same branch, so the
//     checksum proves transport integrity only (accepted risk, ADR §2); the
//     fallback is reported through LastPullDegraded so the refresh
//     transaction can surface it.
//
// Every successful Pull has verified the bytes against the sidecar. A
// missing sidecar, a mismatch, or a body over maxCatalogAssetBytes is a
// hard error on whichever transport served the asset — the puller never
// retries the other transport after one of those, so a tampered or broken
// release cannot be laundered through the raw path.
//
// Auth: tokenless. The GitHub API has a 60 req/h unauthenticated rate
// limit; a 24 h refresh cadence plus one startup pull leaves ample headroom.
type GHReleasePuller struct {
	// HTTPClient is used for the Releases API, the asset, the sidecar and
	// the raw fallback. When nil, a 30-second-timeout default is used.
	HTTPClient *http.Client

	// BaseURL is the GitHub API base URL. Override for tests; defaults to
	// https://api.github.com.
	BaseURL string
	// RawBaseURL is the raw-assets base URL. Override for tests; defaults
	// to https://raw.githubusercontent.com.
	RawBaseURL string

	// UserAgent is sent on every request (GitHub rejects tokenless
	// requests without one). Defaults to "omnipus-catalog/2.0".
	UserAgent string

	// transportMu guards lastDegraded/lastReleaseErr. Pull must be safe
	// for concurrent callers.
	transportMu    sync.Mutex
	lastDegraded   bool
	lastReleaseErr error
}

// releaseAsset is the minimal GitHub Releases API shape we depend on.
type releaseAsset struct {
	TagName string        `json:"tag_name"`
	Assets  []releaseItem `json:"assets"`
}

type releaseItem struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	State              string `json:"state"`
}

// NewGHReleasePuller constructs the production puller for the assembly
// repository's release. Tests override the URL/client fields on the
// returned value.
func NewGHReleasePuller() *GHReleasePuller {
	return &GHReleasePuller{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    "https://api.github.com",
		RawBaseURL: "https://raw.githubusercontent.com",
		UserAgent:  "omnipus-catalog/2.0",
	}
}

// Pull fetches the catalog from the GitHub Release; when the release path
// is unavailable (API error, no release, asset not in the release) it
// falls back to the raw URL of `main`. The returned bytes have been
// verified against the sidecar on whichever transport served them.
//
// The returned error is one of:
//   - ErrTooLarge — body over maxCatalogAssetBytes (errors.Is)
//   - ErrChecksumMismatch — sidecar absent or mismatching (errors.Is)
//   - a transport/shape error naming both paths' failures
//
// On any error the caller retains last-known-good — Pull is non-fatal.
func (p *GHReleasePuller) Pull(ctx context.Context) ([]byte, error) {
	data, fetchErr := p.fetchRelease(ctx)
	if fetchErr == nil {
		p.recordTransport(false, nil)
		return data, nil
	}
	if errors.Is(fetchErr, ErrTooLarge) || errors.Is(fetchErr, ErrChecksumMismatch) {
		// The release served the asset and it failed acceptance: reject,
		// do not launder through the raw path.
		return nil, fmt.Errorf("catalog: pull release: %w", fetchErr)
	}
	rawData, rawErr := p.fetchRaw(ctx)
	if rawErr != nil {
		if errors.Is(rawErr, ErrTooLarge) || errors.Is(rawErr, ErrChecksumMismatch) {
			return nil, fmt.Errorf("catalog: pull raw (release=%v): %w", fetchErr, rawErr)
		}
		return nil, fmt.Errorf("catalog: pull failed (release=%w, raw=%w)", fetchErr, rawErr)
	}
	// Release path failed, raw succeeded: record the degraded provenance
	// so the refresh transaction can WARN and mark the document degraded
	// instead of treating this as an indistinguishable clean success.
	p.recordTransport(true, fetchErr)
	return rawData, nil
}

// recordTransport stores the outcome of the most recently completed
// successful Pull's transport selection so LastPullDegraded can report it.
func (p *GHReleasePuller) recordTransport(degraded bool, releaseErr error) {
	p.transportMu.Lock()
	defer p.transportMu.Unlock()
	p.lastDegraded = degraded
	p.lastReleaseErr = releaseErr
}

// LastPullDegraded reports whether the most recently completed successful
// Pull fell back to the raw transport after the GitHub Release path
// failed. releaseErr is the release-path failure that triggered the
// fallback (nil when the last Pull used the release path, or when Pull has
// never succeeded). A wholly failed Pull leaves the prior state untouched.
func (p *GHReleasePuller) LastPullDegraded() (degraded bool, releaseErr error) {
	p.transportMu.Lock()
	defer p.transportMu.Unlock()
	return p.lastDegraded, p.lastReleaseErr
}

// fetchRelease hits the Releases API for the latest release, locates the
// asset AND its sidecar in the release's asset list, downloads both, and
// verifies. A release that lists the asset without the sidecar is
// rejected with ErrChecksumMismatch (FR-032); a release that does not
// list the asset at all is a shape error (the caller falls back to raw).
func (p *GHReleasePuller) fetchRelease(ctx context.Context) ([]byte, error) {
	apiURL := strings.TrimSuffix(p.BaseURL, "/") + "/repos/" + CatalogOwner + "/" + CatalogRepo + "/releases/latest"
	body, err := p.get(ctx, apiURL, "application/vnd.github+json", maxReleaseAPIBytes)
	if err != nil {
		return nil, fmt.Errorf("release %w", err)
	}
	var release releaseAsset
	if parseErr := json.Unmarshal(body, &release); parseErr != nil {
		return nil, fmt.Errorf("release parse: %w", parseErr)
	}
	if release.TagName == "" {
		return nil, errors.New("release missing tag_name")
	}
	assetURL, sidecarURL := "", ""
	for _, a := range release.Assets {
		if a.State != "" && a.State != "uploaded" {
			continue
		}
		switch a.Name {
		case CatalogAsset:
			assetURL = a.BrowserDownloadURL
		case catalogSidecar:
			sidecarURL = a.BrowserDownloadURL
		}
	}
	if assetURL == "" {
		return nil, fmt.Errorf("release asset %q not found in tag %q", CatalogAsset, release.TagName)
	}
	if sidecarURL == "" {
		return nil, fmt.Errorf("%w: release %q lists %q without its %q sidecar",
			ErrChecksumMismatch, release.TagName, CatalogAsset, catalogSidecar)
	}
	data, err := p.fetchAsset(ctx, assetURL)
	if err != nil {
		return nil, fmt.Errorf("asset %w", err)
	}
	if err := p.verify(ctx, data, sidecarURL); err != nil {
		return nil, err
	}
	return data, nil
}

// fetchRaw reads the asset and its sidecar at HEAD of the pinned branch.
// A 404 on the sidecar is ErrChecksumMismatch (FR-032), not a skip.
func (p *GHReleasePuller) fetchRaw(ctx context.Context) ([]byte, error) {
	data, err := p.fetchAsset(ctx, p.rawURL(CatalogAsset))
	if err != nil {
		return nil, fmt.Errorf("raw %w", err)
	}
	if err := p.verify(ctx, data, p.rawURL(catalogSidecar)); err != nil {
		return nil, err
	}
	return data, nil
}

// fetchAsset downloads an asset body, enforcing maxCatalogAssetBytes by
// reading cap + 1 bytes: a body that fills the extra byte is ErrTooLarge
// before any checksum is computed.
func (p *GHReleasePuller) fetchAsset(ctx context.Context, url string) ([]byte, error) {
	data, err := p.get(ctx, url, "", maxCatalogAssetBytes+1)
	if err != nil {
		return nil, err
	}
	if len(data) > maxCatalogAssetBytes {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrTooLarge, maxCatalogAssetBytes)
	}
	return data, nil
}

// rawURL returns <RawBaseURL>/<owner>/<repo>/main/<name>.
func (p *GHReleasePuller) rawURL(name string) string {
	return strings.TrimSuffix(p.RawBaseURL, "/") + "/" + CatalogOwner + "/" + CatalogRepo + "/" + catalogRef + "/" + name
}

// verify fetches the sidecar at sidecarURL and compares its hex digest
// against sha256(data). Any failure to obtain a usable sidecar — 404, other
// non-200, empty body — is ErrChecksumMismatch: with signing not adopted
// the checksum is the only integrity check (FR-032).
func (p *GHReleasePuller) verify(ctx context.Context, data []byte, sidecarURL string) error {
	body, err := p.get(ctx, sidecarURL, "", maxSidecarBytes)
	if err != nil {
		return fmt.Errorf("%w: sidecar %v", ErrChecksumMismatch, err)
	}
	expected := strings.TrimSpace(string(body))
	// sha256sum format: "<hex>  <name>" or just "<hex>". Accept both.
	if idx := strings.IndexAny(expected, " \t"); idx > 0 {
		expected = expected[:idx]
	}
	if expected == "" {
		return fmt.Errorf("%w: sidecar is empty", ErrChecksumMismatch)
	}
	got := sha256.Sum256(data)
	gotHex := hex.EncodeToString(got[:])
	if !strings.EqualFold(expected, gotHex) {
		return fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expected, gotHex)
	}
	return nil
}

// get performs one GET with the puller's User-Agent, requiring a 200 and
// reading at most limit bytes. Errors are prefixed so callers can wrap
// them with the transport name ("release fetch: …", "release status: 403").
func (p *GHReleasePuller) get(ctx context.Context, url, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", p.UserAgent)
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return body, nil
}

// client returns the configured HTTPClient, falling back to a 30 s default.
func (p *GHReleasePuller) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}
