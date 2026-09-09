package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// GitHubContent represents a file or directory in GitHub API response
type GitHubContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"` // "file" or "dir"
	DownloadURL string `json:"download_url"`
	URL         string `json:"url"` // API URL for subdirectories
}

// GitHubRef represents a parsed GitHub reference
type GitHubRef struct {
	Owner    string // Repository owner
	RepoName string // Repository name
	Ref      string // Git reference (branch, tag, or commit)
	SubPath  string // Path within the repository
}

type SkillInstaller struct {
	workspace   string
	client      *http.Client
	githubToken string
	proxy       string
}

// NewSkillInstaller creates a new skill installer.
// proxy is an optional HTTP/HTTPS/SOCKS5 proxy URL for downloading skills.
// To enforce SSRF protection (SEC-24), use NewSkillInstallerWithSSRF instead.
func NewSkillInstaller(workspace, githubToken, proxy string) (*SkillInstaller, error) {
	return NewSkillInstallerWithSSRF(workspace, githubToken, proxy, nil)
}

// NewSkillInstallerWithSSRF creates a new skill installer with SSRF protection.
//
// When ssrf is non-nil, all outbound HTTP requests (GitHub API calls, raw
// content downloads) are routed through ssrf.SafeClient(), which blocks
// connections to private/internal IP ranges, cloud metadata endpoints, and
// non-http(s) schemes (SEC-24). Pass nil to use the default proxy-aware client
// without SSRF enforcement.
func NewSkillInstallerWithSSRF(
	workspace, githubToken, proxy string,
	ssrf *security.SSRFChecker,
) (*SkillInstaller, error) {
	var client *http.Client
	if ssrf != nil {
		client = ssrf.SafeClient()
	} else {
		var err error
		client, err = utils.CreateHTTPClient(proxy, 15*time.Second)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client: %w", err)
		}
	}

	return &SkillInstaller{
		workspace:   workspace,
		client:      client,
		githubToken: githubToken,
		proxy:       proxy,
	}, nil
}

// SetHTTPClient replaces the HTTP client used for GitHub downloads.
// Use security.SSRFChecker.SafeClient() to enforce SSRF protection (SEC-24).
func (si *SkillInstaller) SetHTTPClient(client *http.Client) {
	si.client = client
}

// parseGitHubRef parses a GitHub reference.
// Supports: "owner/repo", "owner/repo/path", or full URL like "https://github.com/owner/repo/tree/ref/path"
func parseGitHubRef(repo string) (GitHubRef, error) {
	repo = strings.TrimSpace(repo)

	// Handle full URL
	if strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "https://") {
		u, err := url.Parse(repo)
		if err != nil {
			return GitHubRef{}, fmt.Errorf("invalid URL: %w", err)
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 {
			return GitHubRef{}, fmt.Errorf("invalid GitHub URL")
		}
		ref := GitHubRef{
			Owner:    parts[0],
			RepoName: parts[1],
			Ref:      "main",
		}
		// Look for /tree/ or /blob/ in the path
		for i := 2; i < len(parts); i++ {
			if parts[i] == "tree" || parts[i] == "blob" {
				if i+1 < len(parts) {
					ref.Ref = parts[i+1]
					ref.SubPath = strings.Join(parts[i+2:], "/")
				}
				break
			}
		}
		return ref, nil
	}

	// Handle shorthand format
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) < 2 {
		return GitHubRef{}, fmt.Errorf("invalid format %q: expected 'owner/repo'", repo)
	}
	ref := GitHubRef{
		Owner:    parts[0],
		RepoName: parts[1],
		Ref:      "main",
	}
	if len(parts) > 2 {
		ref.SubPath = strings.Join(parts[2:], "/")
	}
	return ref, nil
}

func (si *SkillInstaller) InstallFromGitHub(ctx context.Context, repo string) error {
	ref, err := parseGitHubRef(repo)
	if err != nil {
		return err
	}

	skillName := ref.RepoName
	if ref.SubPath != "" {
		skillName = filepath.Base(ref.SubPath)
	}
	skillDirectory := filepath.Join(si.workspace, "skills", skillName)

	if _, err := os.Stat(skillDirectory); err == nil {
		return fmt.Errorf("skill '%s' already exists", skillName)
	}

	// Build GitHub API URL
	apiPath := path.Join(ref.Owner, ref.RepoName, "contents")
	if ref.SubPath != "" {
		apiPath = path.Join(apiPath, ref.SubPath)
	}
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s?ref=%s", apiPath, ref.Ref)

	if err := si.getGithubDirAllFiles(ctx, apiURL, skillDirectory, true); err != nil {
		// Fallback to raw download
		return si.downloadRaw(ctx, ref.Owner, ref.RepoName, ref.Ref, ref.SubPath, skillDirectory)
	}

	// Bundle gate (FR-10.2): if the installed directory carries a bundle
	// manifest (omnipus-plugin.json), it is an installable bundle, not a bare
	// skill — validate the manifest against the required shape and reject a
	// malformed bundle cleanly before it is used. A directory without a
	// manifest is a plain SKILL.md skill and keeps the legacy contract.
	if _, statErr := os.Stat(filepath.Join(skillDirectory, BundleManifestFilename)); statErr == nil {
		if _, mErr := LoadBundleManifest(skillDirectory); mErr != nil {
			// Remove the partially-installed bundle so a bad manifest never
			// leaves a half-installed directory behind.
			if rmErr := os.RemoveAll(skillDirectory); rmErr != nil {
				return fmt.Errorf("%w (cleanup failed: %w)", mErr, rmErr)
			}
			return mErr
		}
		return nil
	}

	if _, err := os.Stat(filepath.Join(skillDirectory, "SKILL.md")); err != nil {
		return fmt.Errorf("SKILL.md not found in repository")
	}
	return nil
}

// downloadDir recursively downloads a directory from GitHub API
// isRoot: true if this is the skill root directory (only download SKILL.md at root)
func (si *SkillInstaller) getGithubDirAllFiles(ctx context.Context, apiURL, localDir string, isRoot bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return err
	}
	if si.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+si.githubToken)
	}

	resp, err := utils.DoRequestWithRetry(si.client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var items []GitHubContent
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return err
	}

	for _, item := range items {
		// Guard against path traversal via malicious filenames from GitHub API.
		if !filepath.IsLocal(item.Name) {
			return fmt.Errorf("unsafe filename in repository: %q", item.Name)
		}
		localPath := filepath.Join(localDir, item.Name)

		switch item.Type {
		case "file":
			if !shouldDownload(item.Name, isRoot) {
				continue
			}
			if err := si.downloadFile(ctx, item.DownloadURL, localPath); err != nil {
				return fmt.Errorf("download %s: %w", item.Name, err)
			}
		case "dir":
			if !isSkillDirectory(item.Name) {
				continue
			}
			if err := si.getGithubDirAllFiles(ctx, item.URL, localPath, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// downloadRaw is a fallback that downloads just SKILL.md from raw.githubusercontent.com
func (si *SkillInstaller) downloadRaw(ctx context.Context, owner, repo, ref, subPath, localDir string) error {
	urlPath := path.Join(owner, repo, ref)
	if subPath != "" {
		urlPath = path.Join(urlPath, subPath)
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/SKILL.md", urlPath)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Use chunked download to temporary file, retrying on transient
	// GitHub rate-limit/5xx failures (same root cause DownloadToFileWithRetry
	// was built to address for the primary getGithubDirAllFiles path above).
	tmpPath, err := utils.DownloadToFileWithRetry(ctx, si.client, req, 0)
	if err != nil {
		return fmt.Errorf("failed to fetch skill: %w", err)
	}
	defer os.Remove(tmpPath)

	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return fmt.Errorf("failed to create skill directory: %w", err)
	}

	localPath := filepath.Join(localDir, "SKILL.md")

	// Atomic move from temp to final location.
	if err := os.Rename(tmpPath, localPath); err != nil {
		return fmt.Errorf("failed to write skill file: %w", err)
	}

	return os.Chmod(localPath, 0o600)
}

func (si *SkillInstaller) downloadFile(ctx context.Context, url, localPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	// Use chunked download to temporary file, retrying on transient
	// GitHub rate-limit/5xx failures, then move atomically to target.
	tmpPath, err := utils.DownloadToFileWithRetry(ctx, si.client, req, 0)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}

	// Atomic move from temp to final location.
	if err := os.Rename(tmpPath, localPath); err != nil {
		return fmt.Errorf("failed to move downloaded file: %w", err)
	}

	return os.Chmod(localPath, 0o600)
}

// shouldDownload determines if a file should be downloaded
// root: true if we're at the skill root directory
func shouldDownload(name string, root bool) bool {
	if root {
		return name == "SKILL.md"
	}
	return true
}

// isSkillDir checks if a directory is a standard skill resource directory
func isSkillDirectory(name string) bool {
	switch name {
	case "scripts", "references", "assets", "templates", "docs":
		return true
	}
	return false
}

// Uninstall removes an installed skill directory from <workspace>/skills.
//
// skillName may be given either as a bare skill id ("pdf") or in the
// namespaced form the installer accepts ("owner/repo/pdf", "pdf/") — the last
// non-empty path segment is the skill id.
//
// SECURITY: the segment split below strips separators but NOT "." or "..", so
// before this guard existed a caller passing ".." produced
// filepath.Join(workspace, "skills", "..") — which Clean collapses to the
// workspace root — and os.RemoveAll then deleted the operator's entire
// workspace. "." wiped every installed skill. The extracted id is therefore
// resolved through the SAME path-confinement guard the authoring layer uses
// (SkillWriter.resolveSkillDir: namePattern + filepath.Rel confinement, FR-9.2
// / M-6), so the only thing this function can ever remove is one well-formed
// skill directory sitting directly under <workspace>/skills.
//
// Callers get defence at two layers, not one: the remove_skill tool rejects
// path separators and ".." up front (validateID) and the REST handler rejects
// them via validateEntityID, but neither of those rejects "." — this guard is
// what makes the destructive operation itself safe regardless of caller.
func (si *SkillInstaller) Uninstall(skillName string) error {
	// An absolute path is never a skill id. Without this, the segment split
	// below quietly reinterprets "/etc/passwd" as the skill "passwd": confined
	// and therefore harmless, but reported to the caller as NOT_FOUND — an
	// attack presented as a typo, in the tool result and in the audit trail
	// alike. Refuse it as what it is.
	trimmed := strings.TrimSpace(skillName)
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, `\`) {
		return fmt.Errorf("refusing to uninstall %q: %w", skillName, ErrPathConfinement)
	}

	parts := strings.Split(skillName, "/")
	var finalSkillName string
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			finalSkillName = parts[i]
			break
		}
	}
	if finalSkillName == "" {
		finalSkillName = skillName
	}

	// Resolve + confine. The error deliberately does NOT contain "not found":
	// callers (remove_skill's isNotFound, the REST delete handler) map that
	// substring to a 404/NOT_FOUND, and a refused traversal is a rejection, not
	// a missing skill.
	skillDir, err := NewSkillWriter(filepath.Join(si.workspace, "skills")).resolveSkillDir(finalSkillName)
	if err != nil {
		return fmt.Errorf("refusing to uninstall %q: %w", skillName, err)
	}

	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		// NOTE: skillName here is always expected to be the skill's stable ID
		// (the on-disk directory slug) — the SAME identifier SkillsLoader.ListSkills
		// and list_skills report as "id" (SkillInfo.ID, pkg/skills/loader.go).
		// SkillInfo also carries a separate, free-form Name (the SKILL.md
		// frontmatter display name, e.g. "Daily Briefing") that intentionally is
		// NOT accepted here: every "which skills are installed" surface
		// (ListSkills, the <skills> context block, list_skills) already
		// surfaces id as the primary, addressable identifier and documents
		// that name is for display only (SkillRemoveTool's own Description()
		// tells the caller to pass "the skill id as reported by list_skills").
		// A prior version of this function tried to paper over a caller
		// passing the display name instead by scanning for a frontmatter-name
		// match — that was a workaround for an ambiguity the ID/Name split
		// above already resolved at the source, so it was removed rather than
		// carried forward as unused complexity.
		return fmt.Errorf("skill '%s' not found (processed as '%s')", skillName, finalSkillName)
	}

	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("failed to remove skill '%s': %w", finalSkillName, err)
	}

	return nil
}
