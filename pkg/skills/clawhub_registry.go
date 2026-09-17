package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/elicify-ai/omnipus/pkg/utils"
)

const (
	defaultClawHubTimeout  = 30 * time.Second
	defaultMaxZipSize      = 50 * 1024 * 1024 // 50 MB
	defaultMaxResponseSize = 2 * 1024 * 1024  // 2 MB
)

// ClawHubRegistry implements SkillRegistry for the ClawHub platform.
type ClawHubRegistry struct {
	baseURL         string
	authToken       string // Optional - for elevated rate limits
	searchPath      string // Search API
	skillsPath      string // For retrieving skill metadata
	downloadPath    string // For fetching ZIP files for download
	maxZipSize      int
	maxResponseSize int
	client          *http.Client
}

// NewClawHubRegistry creates a new ClawHub registry client from config.
func NewClawHubRegistry(cfg ClawHubConfig) *ClawHubRegistry {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://clawhub.ai"
	}
	searchPath := cfg.SearchPath
	if searchPath == "" {
		searchPath = "/api/v1/search"
	}
	skillsPath := cfg.SkillsPath
	if skillsPath == "" {
		skillsPath = "/api/v1/skills"
	}
	downloadPath := cfg.DownloadPath
	if downloadPath == "" {
		downloadPath = "/api/v1/download"
	}

	timeout := defaultClawHubTimeout
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}

	maxZip := defaultMaxZipSize
	if cfg.MaxZipSize > 0 {
		maxZip = cfg.MaxZipSize
	}

	maxResp := defaultMaxResponseSize
	if cfg.MaxResponseSize > 0 {
		maxResp = cfg.MaxResponseSize
	}

	// Use the caller-supplied HTTP client if provided (e.g., SSRF-safe client
	// from security.SSRFChecker.SafeClient — see ClawHubConfig.HTTPClient, W-2).
	// Otherwise build a minimal default client.
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        5,
				IdleConnTimeout:     30 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		}
	}

	return &ClawHubRegistry{
		baseURL:         baseURL,
		authToken:       cfg.AuthToken,
		searchPath:      searchPath,
		skillsPath:      skillsPath,
		downloadPath:    downloadPath,
		maxZipSize:      maxZip,
		maxResponseSize: maxResp,
		client:          client,
	}
}

func (c *ClawHubRegistry) Name() string {
	return "clawhub"
}

// --- Search ---

type clawhubSearchResponse struct {
	Results []clawhubSearchResult `json:"results"`
}

type clawhubSearchResult struct {
	Score       float64           `json:"score"`
	Slug        *string           `json:"slug"`
	DisplayName *string           `json:"displayName"`
	Summary     *string           `json:"summary"`
	Version     *string           `json:"version"`
	OwnerHandle *string           `json:"ownerHandle"`
	Owner       *clawhubOwnerInfo `json:"owner"`
}

// clawhubOwnerInfo carries the nested owner object some registry responses
// include alongside the flat ownerHandle field.
type clawhubOwnerInfo struct {
	Handle      *string `json:"handle"`
	DisplayName *string `json:"displayName"`
	Image       *string `json:"image"`
}

func (c *ClawHubRegistry) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	u, err := url.Parse(c.baseURL + c.searchPath)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	q := u.Query()
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	u.RawQuery = q.Encode()

	body, err := c.doGet(ctx, u.String())
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}

	var resp clawhubSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %w", err)
	}

	results := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		slug := utils.DerefStr(r.Slug, "")
		if slug == "" {
			continue
		}

		summary := utils.DerefStr(r.Summary, "")
		if summary == "" {
			continue
		}

		displayName := utils.DerefStr(r.DisplayName, "")
		if displayName == "" {
			displayName = slug
		}

		// Owner handle: prefer the flat ownerHandle, fall back to the nested
		// owner.handle object when present.
		ownerHandle := utils.DerefStr(r.OwnerHandle, "")
		if ownerHandle == "" && r.Owner != nil {
			ownerHandle = utils.DerefStr(r.Owner.Handle, "")
		}

		results = append(results, SearchResult{
			Score:        r.Score,
			Slug:         slug,
			DisplayName:  displayName,
			Summary:      summary,
			Version:      utils.DerefStr(r.Version, ""),
			RegistryName: c.Name(),
			OwnerHandle:  ownerHandle,
		})
	}

	return results, nil
}

// --- GetSkillMeta ---

type clawhubSkillResponse struct {
	Slug          string                 `json:"slug"`
	DisplayName   string                 `json:"displayName"`
	Summary       string                 `json:"summary"`
	LatestVersion *clawhubVersionInfo    `json:"latestVersion"`
	Moderation    *clawhubModerationInfo `json:"moderation"`
}

type clawhubVersionInfo struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"` // hex-encoded SHA-256 of the ZIP archive (SEC-09)
}

type clawhubModerationInfo struct {
	IsMalwareBlocked bool `json:"isMalwareBlocked"`
	IsSuspicious     bool `json:"isSuspicious"`
}

// GetSkillMeta retrieves metadata for slug. When the same slug is published
// by more than one owner on the registry, the request is ambiguous; use
// getSkillMeta with a non-empty ownerHandle (via DownloadAndInstallForOwner)
// to disambiguate.
func (c *ClawHubRegistry) GetSkillMeta(ctx context.Context, slug string) (*SkillMeta, error) {
	return c.getSkillMeta(ctx, slug, "")
}

// getSkillMeta is the ownerHandle-aware implementation behind both
// GetSkillMeta and the owner-scoped install path. ownerHandle == "" means
// "unscoped", matching GetSkillMeta's public behavior exactly.
func (c *ClawHubRegistry) getSkillMeta(ctx context.Context, slug, ownerHandle string) (*SkillMeta, error) {
	if err := utils.ValidateSkillIdentifier(slug); err != nil {
		return nil, fmt.Errorf("invalid slug %q: error: %s", slug, err.Error())
	}
	if ownerHandle != "" {
		if err := utils.ValidateSkillIdentifier(ownerHandle); err != nil {
			return nil, fmt.Errorf("invalid ownerHandle %q: error: %s", ownerHandle, err.Error())
		}
	}

	parsedURL, err := url.Parse(c.baseURL + c.skillsPath + "/" + url.PathEscape(slug))
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if ownerHandle != "" {
		q := parsedURL.Query()
		q.Set("ownerHandle", ownerHandle)
		parsedURL.RawQuery = q.Encode()
	}
	u := parsedURL.String()

	body, err := c.doGet(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("skill metadata request failed: %w", err)
	}

	var resp clawhubSkillResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse skill metadata: %w", err)
	}

	meta := &SkillMeta{
		Slug:         resp.Slug,
		DisplayName:  resp.DisplayName,
		Summary:      resp.Summary,
		RegistryName: c.Name(),
	}

	if resp.LatestVersion != nil {
		meta.LatestVersion = resp.LatestVersion.Version
		meta.ExpectedHash = resp.LatestVersion.SHA256
	}
	if resp.Moderation != nil {
		meta.IsMalwareBlocked = resp.Moderation.IsMalwareBlocked
		meta.IsSuspicious = resp.Moderation.IsSuspicious
	}

	return meta, nil
}

// --- DownloadAndInstall ---

// DownloadAndInstall fetches metadata (with fallback), resolves version,
// downloads the skill ZIP, and extracts it to targetDir.
// Returns an InstallResult for the caller to use for moderation decisions.
//
// If the registry reports slug as ambiguous (published by more than one
// owner), retry via DownloadAndInstallForOwner with the disambiguating
// owner handle rather than retrying this method — a bare slug lookup will
// hit the same ambiguity again.
func (c *ClawHubRegistry) DownloadAndInstall(
	ctx context.Context,
	slug, version, targetDir string,
) (*InstallResult, error) {
	return c.downloadAndInstall(ctx, slug, "", version, targetDir)
}

// DownloadAndInstallForOwner implements OwnerScopedRegistry: it behaves like
// DownloadAndInstall but scopes both the metadata lookup and the download to
// skills published by ownerHandle, resolving an otherwise-ambiguous slug.
func (c *ClawHubRegistry) DownloadAndInstallForOwner(
	ctx context.Context,
	slug, ownerHandle, version, targetDir string,
) (*InstallResult, error) {
	if err := utils.ValidateSkillIdentifier(ownerHandle); err != nil {
		return nil, fmt.Errorf("invalid ownerHandle %q: error: %s", ownerHandle, err.Error())
	}
	return c.downloadAndInstall(ctx, slug, ownerHandle, version, targetDir)
}

func (c *ClawHubRegistry) downloadAndInstall(
	ctx context.Context,
	slug, ownerHandle, version, targetDir string,
) (*InstallResult, error) {
	if err := utils.ValidateSkillIdentifier(slug); err != nil {
		return nil, fmt.Errorf("invalid slug %q: error: %s", slug, err.Error())
	}

	// Step 1: Fetch metadata (with fallback).
	// NOTE: when metadata is unavailable, hash verification is skipped and
	// result.Verified stays false. Callers MUST enforce config.SkillTrustLevel
	// (wired via pkg/gateway/rest_skill_trust.go) to decide whether to block
	// or warn on unverified installs (SEC-09).
	result := &InstallResult{}
	meta, err := c.getSkillMeta(ctx, slug, ownerHandle)
	if err != nil {
		slog.Warn("skill metadata fetch failed — hash verification will be skipped",
			"slug", slug,
			"error", err,
		)
		meta = nil
	}

	if meta != nil {
		result.IsMalwareBlocked = meta.IsMalwareBlocked
		result.IsSuspicious = meta.IsSuspicious
		result.Summary = meta.Summary
	}

	// Step 2: Resolve version.
	installVersion := version
	if installVersion == "" && meta != nil {
		installVersion = meta.LatestVersion
	}
	if installVersion == "" {
		installVersion = "latest"
	}
	result.Version = installVersion

	// Step 3: Download ZIP to temp file (streams in ~32KB chunks).
	u, err := url.Parse(c.baseURL + c.downloadPath)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	q := u.Query()
	q.Set("slug", slug)
	if ownerHandle != "" {
		q.Set("ownerHandle", ownerHandle)
	}
	if installVersion != "latest" {
		q.Set("version", installVersion)
	}
	u.RawQuery = q.Encode()

	req, err := c.newGetRequest(ctx, u.String(), "application/zip")
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}

	tmpPath, err := utils.DownloadToFileWithRetry(ctx, c.client, req, int64(c.maxZipSize))
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tmpPath)

	// Step 4: Verify SHA-256 hash if the registry provided one (SEC-09).
	if meta != nil && meta.ExpectedHash != "" {
		actual, hashErr := computeFileSHA256(tmpPath)
		if hashErr != nil {
			return nil, fmt.Errorf("hash computation failed: %w", hashErr)
		}
		if actual != meta.ExpectedHash {
			return nil, fmt.Errorf(
				"hash verification failed: expected %s got %s — skill may have been tampered with",
				meta.ExpectedHash, actual,
			)
		}
		result.Verified = true
	}

	// Step 5: Extract from file on disk.
	if err := utils.ExtractZipFile(tmpPath, targetDir); err != nil {
		return nil, err
	}

	return result, nil
}

// computeFileSHA256 returns the lowercase hex SHA-256 digest of the file at path.
func computeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// --- HTTP helper ---

func (c *ClawHubRegistry) doGet(ctx context.Context, urlStr string) ([]byte, error) {
	req, err := c.newGetRequest(ctx, urlStr, "application/json")
	if err != nil {
		return nil, err
	}

	resp, err := utils.DoRequestWithRetry(c.client, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Limit response body read to prevent memory issues.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(c.maxResponseSize)))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func (c *ClawHubRegistry) newGetRequest(ctx context.Context, urlStr, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	return req, nil
}
