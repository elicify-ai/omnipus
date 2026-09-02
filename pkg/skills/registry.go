package skills

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

const (
	defaultMaxConcurrentSearches = 2
)

// SearchResult represents a single result from a skill registry search.
type SearchResult struct {
	Score        float64 `json:"score"`
	Slug         string  `json:"slug"`
	DisplayName  string  `json:"display_name"`
	Summary      string  `json:"summary"`
	Version      string  `json:"version"`
	RegistryName string  `json:"registry_name"`
	// OwnerHandle is the publisher/owner handle on the registry, when available.
	OwnerHandle string `json:"owner_handle,omitempty"`
}

// SkillMeta holds metadata about a skill from a registry.
type SkillMeta struct {
	Slug             string `json:"slug"`
	DisplayName      string `json:"display_name"`
	Summary          string `json:"summary"`
	LatestVersion    string `json:"latest_version"`
	ExpectedHash     string `json:"expected_hash"` // SHA-256 hex of the skill ZIP (SEC-09)
	IsMalwareBlocked bool   `json:"is_malware_blocked"`
	IsSuspicious     bool   `json:"is_suspicious"`
	RegistryName     string `json:"registry_name"`
}

// InstallResult is returned by DownloadAndInstall to carry metadata
// back to the caller for moderation and user messaging.
type InstallResult struct {
	Version          string
	IsMalwareBlocked bool
	IsSuspicious     bool
	Summary          string
	// Verified is true when the downloaded archive was checked against the registry hash.
	Verified bool
}

// SkillRegistry is the interface that all skill registries must implement.
// Each registry represents a different source of skills (e.g., clawhub.ai)
type SkillRegistry interface {
	// Name returns the unique name of this registry (e.g., "clawhub").
	Name() string
	// Search searches the registry for skills matching the query.
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
	// GetSkillMeta retrieves metadata for a specific skill by slug.
	GetSkillMeta(ctx context.Context, slug string) (*SkillMeta, error)
	// DownloadAndInstall fetches metadata, resolves the version, downloads and
	// installs the skill to targetDir. Returns an InstallResult with metadata
	// for the caller to use for moderation and user messaging.
	DownloadAndInstall(ctx context.Context, slug, version, targetDir string) (*InstallResult, error)
}

// OwnerScopedRegistry is an optional capability a SkillRegistry may implement
// when a bare slug lookup can be ambiguous (the same slug published by more
// than one owner) and the registry's API supports narrowing the lookup to a
// specific owner to resolve it. Callers that receive an ambiguous-match error
// from DownloadAndInstall should check for this interface and retry via
// DownloadAndInstallForOwner once the caller has an owner handle to
// disambiguate with (e.g. from a search result's OwnerHandle field, or the
// registry's own error response).
type OwnerScopedRegistry interface {
	// DownloadAndInstallForOwner behaves like DownloadAndInstall but scopes
	// the slug lookup to skills published by ownerHandle.
	DownloadAndInstallForOwner(
		ctx context.Context, slug, ownerHandle, version, targetDir string,
	) (*InstallResult, error)
}

// GitHubRegistryConfig configures a single GitHub-hosted skill registry.
// A GitHub registry enables direct installation from a GitHub repository
// via owner/repo references. Search and metadata lookups return
// ErrGitHubSearchNotSupported because GitHub is not a searchable catalog;
// DownloadAndInstall is the primary operation.
//
// Auth tokens are resolved via the credential store (SEC-23): set TokenRef
// to the env-var name injected by credentials.InjectFromConfig and pass the
// resolved token in Token at runtime.
type GitHubRegistryConfig struct {
	// Enabled controls whether this GitHub registry is active.
	Enabled bool
	// Name is the unique display name for this registry (e.g., "github").
	// Defaults to "github" when empty.
	Name string
	// Token is the resolved GitHub personal access token.
	// Populate from os.Getenv(TokenRef) after credentials are injected.
	Token string
	// Proxy is an optional HTTP/HTTPS/SOCKS5 proxy URL.
	Proxy string
	// Workspace is the base directory for skill installation.
	// Required: DownloadAndInstall writes skills under <Workspace>/skills/<slug>/.
	Workspace string
}

// MarketplaceConfig is a unified entry in the skill-marketplace list (FR-10.1).
// Each entry describes one marketplace registry (ClawHub, GitHub, or a future
// "omnipus" registry). Type selects which registry implementation backs the
// entry; the remaining fields are interpreted per Type.
//
// Tokens are the RESOLVED credential values (not refs) — callers resolve
// credential refs via the credential store (SEC-23) before constructing a
// MarketplaceConfig. This mirrors the resolved-token contract on ClawHubConfig
// and GitHubRegistryConfig.
type MarketplaceConfig struct {
	// Name is the unique display name for this marketplace (e.g. "clawhub",
	// "github", a future "omnipus"). Defaults to Type when empty.
	Name string `json:"name"`
	// Type selects the registry implementation: "clawhub" | "github"
	// (future: "omnipus").
	Type string `json:"type"`
	// Enabled controls whether this marketplace is active.
	Enabled bool `json:"enabled"`

	// ClawHub-specific fields (used when Type == "clawhub").
	BaseURL         string `json:"base_url,omitempty"`
	AuthToken       string `json:"auth_token,omitempty"` // resolved token
	SearchPath      string `json:"search_path,omitempty"`
	SkillsPath      string `json:"skills_path,omitempty"`
	DownloadPath    string `json:"download_path,omitempty"`
	Timeout         int    `json:"timeout,omitempty"`
	MaxZipSize      int    `json:"max_zip_size,omitempty"`
	MaxResponseSize int    `json:"max_response_size,omitempty"`
	// HTTPClient overrides the default ClawHub HTTP client. When non-nil it is
	// used as-is. Not serialized — injected at runtime (e.g. SSRF-safe client).
	HTTPClient *http.Client `json:"-"`

	// GitHub-specific fields (used when Type == "github").
	Token     string `json:"token,omitempty"` // resolved token
	Proxy     string `json:"proxy,omitempty"`
	Workspace string `json:"workspace,omitempty"` // base dir for skill install
}

// RegistryConfig holds configuration for all skill marketplaces.
// This is the input to NewRegistryManagerFromConfig.
//
// Marketplaces is a unified list (FR-10.1): each entry is one configured
// marketplace registry (ClawHub, GitHub, or a future "omnipus" registry). The
// RegistryManager fans out Search across all enabled entries; GitHub entries
// participate in fan-out but return ErrGitHubSearchNotSupported (partial-result
// semantics apply).
type RegistryConfig struct {
	Marketplaces          []MarketplaceConfig
	MaxConcurrentSearches int
}

// ClawHubConfig configures the ClawHub registry.
type ClawHubConfig struct {
	Enabled         bool
	BaseURL         string
	AuthToken       string
	SearchPath      string // e.g. "/api/v1/search"
	SkillsPath      string // e.g. "/api/v1/skills"
	DownloadPath    string // e.g. "/api/v1/download"
	Timeout         int    // seconds, 0 = default (30s)
	MaxZipSize      int    // bytes, 0 = default (50MB)
	MaxResponseSize int    // bytes, 0 = default (2MB)

	// HTTPClient overrides the default HTTP client. When non-nil it is used as-is
	// (the Timeout field is ignored). Callers should pass
	// security.SSRFChecker.SafeClient() to enforce SSRF protection (W-2).
	// This field is not serialized from JSON — it must be injected at runtime.
	HTTPClient *http.Client `json:"-"`
}

// clawHubConfigFromMarketplace adapts a MarketplaceConfig (Type=="clawhub") into
// the ClawHubConfig consumed by NewClawHubRegistry.
func clawHubConfigFromMarketplace(m MarketplaceConfig) ClawHubConfig {
	return ClawHubConfig{
		Enabled:         m.Enabled,
		BaseURL:         m.BaseURL,
		AuthToken:       m.AuthToken,
		SearchPath:      m.SearchPath,
		SkillsPath:      m.SkillsPath,
		DownloadPath:    m.DownloadPath,
		Timeout:         m.Timeout,
		MaxZipSize:      m.MaxZipSize,
		MaxResponseSize: m.MaxResponseSize,
		HTTPClient:      m.HTTPClient,
	}
}

// gitHubConfigFromMarketplace adapts a MarketplaceConfig (Type=="github") into
// the GitHubRegistryConfig consumed by NewGitHubRegistry.
func gitHubConfigFromMarketplace(m MarketplaceConfig) GitHubRegistryConfig {
	name := m.Name
	if name == "" {
		name = "github"
	}
	return GitHubRegistryConfig{
		Enabled:   m.Enabled,
		Name:      name,
		Token:     m.Token,
		Proxy:     m.Proxy,
		Workspace: m.Workspace,
	}
}

// RegistryManager coordinates multiple skill registries.
// It fans out search requests and routes installs to the correct registry.
type RegistryManager struct {
	registries    []SkillRegistry
	maxConcurrent int
	mu            sync.RWMutex
}

// NewRegistryManager creates an empty RegistryManager.
func NewRegistryManager() *RegistryManager {
	return &RegistryManager{
		registries:    make([]SkillRegistry, 0),
		maxConcurrent: defaultMaxConcurrentSearches,
	}
}

// NewRegistryManagerFromConfig builds a RegistryManager from config,
// instantiating only the enabled marketplaces.
//
// The Marketplaces list is iterated in order. Each enabled entry is
// instantiated by Type: "clawhub" → ClawHubRegistry, "github" → GitHubRegistry.
// If a GitHub entry fails to initialize (e.g., bad workspace path or proxy
// URL), it is logged at Warn level and skipped — the manager still starts with
// the remaining entries. Unknown types are logged and skipped.
func NewRegistryManagerFromConfig(cfg RegistryConfig) *RegistryManager {
	rm := NewRegistryManager()
	if cfg.MaxConcurrentSearches > 0 {
		rm.maxConcurrent = cfg.MaxConcurrentSearches
	}
	for _, m := range cfg.Marketplaces {
		if !m.Enabled {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.Type
		}
		switch m.Type {
		case config.MarketplaceTypeClawHub:
			rm.AddRegistry(NewClawHubRegistry(clawHubConfigFromMarketplace(m)))
		case config.MarketplaceTypeGitHub:
			ghCfg := gitHubConfigFromMarketplace(m)
			reg, err := NewGitHubRegistry(ghCfg)
			if err != nil {
				slog.Warn("skills: failed to initialize GitHub marketplace — skipping",
					"name", ghCfg.Name,
					"error", err,
				)
				continue
			}
			rm.AddRegistry(reg)
		default:
			slog.Warn("skills: unknown marketplace type — skipping",
				"name", name,
				"type", m.Type,
			)
		}
	}
	return rm
}

// AddRegistry adds a registry to the manager.
func (rm *RegistryManager) AddRegistry(r SkillRegistry) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.registries = append(rm.registries, r)
}

// GetRegistry returns a registry by name, or nil if not found.
func (rm *RegistryManager) GetRegistry(name string) SkillRegistry {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	for _, r := range rm.registries {
		if r.Name() == name {
			return r
		}
	}
	return nil
}

// SearchAll fans out the query to all registries concurrently
// and merges results sorted by score descending.
func (rm *RegistryManager) SearchAll(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	rm.mu.RLock()
	regs := make([]SkillRegistry, len(rm.registries))
	copy(regs, rm.registries)
	rm.mu.RUnlock()

	if len(regs) == 0 {
		return nil, fmt.Errorf("no registries configured")
	}

	type regResult struct {
		results []SearchResult
		err     error
	}

	// Semaphore: limit concurrency.
	sem := make(chan struct{}, rm.maxConcurrent)
	resultsCh := make(chan regResult, len(regs))

	var wg sync.WaitGroup
	for _, reg := range regs {
		wg.Add(1)
		go func(r SkillRegistry) {
			defer wg.Done()

			// Acquire semaphore slot.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				resultsCh <- regResult{err: ctx.Err()}
				return
			}

			searchCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
			defer cancel()

			results, err := r.Search(searchCtx, query, limit)
			if err != nil {
				slog.Warn("registry search failed", "registry", r.Name(), "error", err)
				resultsCh <- regResult{err: err}
				return
			}
			resultsCh <- regResult{results: results}
		}(reg)
	}

	// Close results channel after all goroutines complete.
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var merged []SearchResult
	var lastErr error

	var anyRegistrySucceeded bool
	for rr := range resultsCh {
		if rr.err != nil {
			lastErr = rr.err
			continue
		}
		anyRegistrySucceeded = true
		merged = append(merged, rr.results...)
	}

	// If all registries failed, return the last error.
	if !anyRegistrySucceeded && lastErr != nil {
		return nil, fmt.Errorf("all registries failed: %w", lastErr)
	}

	// If some (but not all) registries failed, log a warning and return a
	// PartialSearchError so callers can surface the caveat to users.
	var retErr error
	if lastErr != nil && anyRegistrySucceeded {
		slog.Warn("search returned partial results: one or more registries failed",
			"error", lastErr,
		)
		retErr = &PartialSearchError{Cause: lastErr}
	}

	// Sort by score descending.
	sortByScoreDesc(merged)

	// Clamp to limit.
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}

	return merged, retErr
}

// PartialSearchError is returned by SearchAll when at least one registry
// succeeded but others failed. Results are still usable; the caller should
// display a notice that results may be incomplete.
type PartialSearchError struct {
	Cause error
}

func (e *PartialSearchError) Error() string {
	return fmt.Sprintf("partial results: one or more registries failed (%v)", e.Cause)
}

func (e *PartialSearchError) Unwrap() error { return e.Cause }

// sortByScoreDesc sorts SearchResults by Score in descending order (insertion sort — small slices).
func sortByScoreDesc(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		key := results[i]
		j := i - 1
		for j >= 0 && results[j].Score < key.Score {
			results[j+1] = results[j]
			j--
		}
		results[j+1] = key
	}
}
