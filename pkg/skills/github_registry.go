package skills

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ErrGitHubSearchNotSupported is returned by GitHubRegistry.Search and
// GitHubRegistry.GetSkillMeta because GitHub is a VCS host, not a searchable
// skill catalog. Callers that fan out across multiple registries should treat
// this as a PartialSearchError contribution (the GitHub registry has nothing
// to contribute to catalog searches). DownloadAndInstall is the primary
// operation for GitHub-hosted skills.
var ErrGitHubSearchNotSupported = errors.New(
	"github registry: search and metadata lookup not supported — use DownloadAndInstall with an owner/repo reference",
)

// GitHubRegistry implements SkillRegistry for GitHub-hosted skills.
//
// It wraps a SkillInstaller configured for the given workspace and optional
// auth token. Registry authentication tokens are resolved via the credential
// store (SEC-23) before this struct is constructed: the caller resolves
// GitHubRegistryConfig.Token via os.Getenv(TokenRef) after
// credentials.InjectFromConfig runs.
//
// Fan-out behavior: Search and GetSkillMeta return ErrGitHubSearchNotSupported.
// RegistryManager.SearchAll treats this as a partial failure (PartialSearchError
// semantics) — ClawHub or other searchable registries still contribute results.
// DownloadAndInstall performs a full GitHub checkout into the workspace.
type GitHubRegistry struct {
	name      string
	installer *SkillInstaller
}

// NewGitHubRegistry creates a GitHubRegistry from GitHubRegistryConfig.
// Returns an error if the SkillInstaller cannot be initialized (e.g., bad
// proxy URL or empty workspace).
func NewGitHubRegistry(cfg GitHubRegistryConfig) (*GitHubRegistry, error) {
	if cfg.Workspace == "" {
		return nil, fmt.Errorf("github registry %q: workspace must not be empty", cfg.Name)
	}

	name := cfg.Name
	if name == "" {
		name = "github"
	}

	installer, err := NewSkillInstaller(cfg.Workspace, cfg.Token, cfg.Proxy)
	if err != nil {
		return nil, fmt.Errorf("github registry %q: failed to create installer: %w", name, err)
	}

	return &GitHubRegistry{
		name:      name,
		installer: installer,
	}, nil
}

// NewGitHubRegistryWithClient creates a GitHubRegistry using a pre-built
// HTTP client (e.g., security.SSRFChecker.SafeClient() for SSRF protection,
// SEC-24). The proxy field in cfg is ignored when client is non-nil.
func NewGitHubRegistryWithClient(cfg GitHubRegistryConfig, client *http.Client) (*GitHubRegistry, error) {
	if cfg.Workspace == "" {
		return nil, fmt.Errorf("github registry %q: workspace must not be empty", cfg.Name)
	}

	name := cfg.Name
	if name == "" {
		name = "github"
	}

	installer, err := NewSkillInstallerWithSSRF(cfg.Workspace, cfg.Token, cfg.Proxy, nil)
	if err != nil {
		return nil, fmt.Errorf("github registry %q: failed to create installer: %w", name, err)
	}
	if client != nil {
		installer.SetHTTPClient(client)
	}

	return &GitHubRegistry{
		name:      name,
		installer: installer,
	}, nil
}

// Name returns the registry's unique name.
func (g *GitHubRegistry) Name() string {
	return g.name
}

// Search is not supported for GitHub registries. GitHub is a VCS host, not a
// skill catalog. Returns ErrGitHubSearchNotSupported so that RegistryManager
// can apply partial-result semantics when fanning out across mixed registries.
func (g *GitHubRegistry) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	return nil, ErrGitHubSearchNotSupported
}

// GetSkillMeta is not supported for GitHub registries. Returns
// ErrGitHubSearchNotSupported. Use DownloadAndInstall to fetch a skill by
// its "owner/repo" or "owner/repo/path" reference.
func (g *GitHubRegistry) GetSkillMeta(_ context.Context, _ string) (*SkillMeta, error) {
	return nil, ErrGitHubSearchNotSupported
}

// DownloadAndInstall installs a skill from GitHub into the configured workspace.
//
// The slug parameter is interpreted as a GitHub reference in any of the
// forms accepted by SkillInstaller.InstallFromGitHub:
//   - "owner/repo"
//   - "owner/repo/path/to/skill"
//   - "https://github.com/owner/repo/tree/ref/path"
//
// The version parameter is ignored (GitHub skills are pinned by ref in the
// slug). targetDir is also ignored; the install destination is derived from
// the workspace and the repository/path name, following SkillInstaller's
// conventions (<workspace>/skills/<skillName>/).
//
// Returns an InstallResult with Verified=false (no registry-side hash to
// check) and the slug as the Version field.
//
// MODERATION: GitHub is a raw VCS host with no moderation pipeline. Unlike the
// ClawHub path — where IsMalwareBlocked/IsSuspicious come from registry-side
// moderation metadata — there is no scanner here to give the checked-out tree a
// clean bill of health. We therefore must NOT report IsSuspicious=false (which a
// caller would read as "scanned and found clean"). Instead the result reflects
// "unscanned": IsSuspicious=true flags the install for caller attention/review,
// and Summary states the content was not moderated. IsMalwareBlocked stays false
// because nothing actively blocked the install (it is not a known-malicious slug).
func (g *GitHubRegistry) DownloadAndInstall(
	ctx context.Context,
	slug, _ /* version */, _ /* targetDir */ string,
) (*InstallResult, error) {
	if slug == "" {
		return nil, fmt.Errorf("github registry %q: slug (owner/repo reference) must not be empty", g.name)
	}
	if err := g.installer.InstallFromGitHub(ctx, slug); err != nil {
		return nil, fmt.Errorf("github registry %q: install from %q failed: %w", g.name, slug, err)
	}
	return unscannedGitHubResult(slug), nil
}

// unscannedGitHubResult builds the InstallResult for a successful GitHub install.
// GitHub has no moderation pipeline, so the result must reflect "unscanned" rather
// than a clean bill of health: IsSuspicious=true flags it for caller review, while
// IsMalwareBlocked stays false (nothing actively blocked the install).
func unscannedGitHubResult(slug string) *InstallResult {
	return &InstallResult{
		Version:          slug,
		IsMalwareBlocked: false,
		IsSuspicious:     true,
		Summary: fmt.Sprintf(
			"Installed from GitHub: %s (unmoderated source — content not scanned, review before trusting)",
			slug,
		),
		Verified: false,
	}
}
