package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// InstallSkillTool allows the LLM agent to install skills from registries.
// It shares the same RegistryManager that FindSkillsTool uses,
// so all registries configured in config are available for installation.
type InstallSkillTool struct {
	BaseTool
	registryMgr *skills.RegistryManager
	// globalSkillsDir is the fixed, install-wide skills directory
	// ($OMNIPUS_HOME/skills) — NOT a per-agent/per-turn workspace path.
	// ADR-046 FR-009: install_skill targets this GLOBAL registry
	// unconditionally (every agent's own SkillsLoader searches it too, via
	// pkg/agent.globalSkillsDir — see pkg/skills/loader.go's SkillRoots),
	// so a skill installed by one agent is discoverable by every other
	// agent. This is NOT routed through ResolvePath: ResolvePath resolves a
	// caller-supplied path relative to the turn's effective WORKING
	// directory, whereas this is always the same fixed global directory
	// regardless of which agent or turn is calling.
	globalSkillsDir string
	mu              sync.Mutex
}

// NewInstallSkillTool creates a new InstallSkillTool.
// registryMgr is the shared registry manager (same instance as FindSkillsTool).
// globalSkillsDir is the fixed, install-wide skills directory
// ($OMNIPUS_HOME/skills, see pkg/agent.globalSkillsDir); skills install to
// {globalSkillsDir}/{slug}/.
func NewInstallSkillTool(registryMgr *skills.RegistryManager, globalSkillsDir string) *InstallSkillTool {
	// Boot-time sweep: remove any staging leftovers from a previous install
	// that never completed (crash, OOM-kill, forced restart) so they don't
	// accumulate forever. This constructor runs once per agent at process
	// startup (registerSharedTools, pkg/agent/loop.go) and again only on a
	// full config/registry reload — never on the per-turn hot path — so the
	// extra ReadDir here (typically of an empty or nonexistent directory) is
	// negligible. Best-effort: a sweep failure must never block tool
	// construction or gateway boot.
	if globalSkillsDir != "" {
		sweepStaleStaging(globalSkillsDir)
	}
	return &InstallSkillTool{
		registryMgr:     registryMgr,
		globalSkillsDir: globalSkillsDir,
		mu:              sync.Mutex{},
	}
}

// stagingDirName is the dedicated, non-scanned subdirectory of the global
// skills directory that install_skill stages downloads into. It lives one
// level below skillsDir (not skillsDir itself) for two reasons: (1)
// pkg/skills/loader.go's ListSkills scans skillsDir's direct children for a
// SKILL.md and, prior to this fix, had no dot-prefix filter — a staging
// directory created directly inside skillsDir (even dot-prefixed) was one
// missing check away from becoming a phantom entry in every agent's skill
// list; keeping all staging under one always-dot-prefixed, SKILL.md-less
// directory removes that surface entirely, and pkg/skills/loader.go now also
// skips any dot-prefixed entry defensively. (2) it stays on the SAME
// filesystem as skillsDir, so the final os.Rename into targetDir remains an
// atomic, cheap rename rather than a cross-filesystem copy.
const stagingDirName = ".staging"

// sweepStaleStaging removes any leftover contents of skillsDir/.staging left
// behind by an install_skill run that was interrupted before it could clean
// up after itself (process crash, OOM-kill, forced restart — the defer in
// Execute only runs on a normal return). It is best-effort: skillsDir may not
// exist yet on a fresh install, and any error here is logged, never
// propagated, since a failed sweep must not block tool construction or
// gateway boot. A future install_skill run will retry the sweep on its own
// next construction, and stale entries are otherwise harmless (never
// surfaced by ListSkills, per the dot-prefix skip in pkg/skills/loader.go).
func sweepStaleStaging(skillsDir string) {
	stagingRoot := filepath.Join(skillsDir, stagingDirName)
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.WarnCF("tool", "install_skill: failed to read staging directory during startup sweep",
				map[string]any{
					"tool":  "install_skill",
					"dir":   stagingRoot,
					"error": err.Error(),
				})
		}
		return
	}
	for _, entry := range entries {
		stalePath := filepath.Join(stagingRoot, entry.Name())
		if err := os.RemoveAll(stalePath); err != nil {
			logger.WarnCF("tool", "install_skill: failed to remove stale staging entry during startup sweep",
				map[string]any{
					"tool":  "install_skill",
					"path":  stalePath,
					"error": err.Error(),
				})
			continue
		}
		logger.InfoCF("tool", "install_skill: removed stale staging entry left by an interrupted install",
			map[string]any{
				"tool": "install_skill",
				"path": stalePath,
			})
	}
}

func (t *InstallSkillTool) Name() string {
	return "install_skill"
}

func (t *InstallSkillTool) Description() string {
	return "Install a skill from a registry by slug. Downloads and extracts the skill into the global skills directory, where it becomes available to every agent. Use find_skills first to discover available skills. " +
		"force=true replaces an already-installed skill of the same slug: the replacement is downloaded to a staging area and swapped in only once it fully succeeds, so an ordinary failure (unknown registry, slug, or version; " +
		"network error; a skill flagged malicious and refused) leaves the existing install untouched. A skill flagged as malicious is refused and removed rather than installed."
}

func (t *InstallSkillTool) Scope() ToolScope       { return ScopeGeneral }
func (t *InstallSkillTool) Category() ToolCategory { return CategorySkills }

func (t *InstallSkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slug": map[string]any{
				"type":        "string",
				"description": "The unique slug of the skill to install (e.g., 'github', 'docker-compose')",
			},
			"version": map[string]any{
				"type":        "string",
				"description": "Specific version to install (optional, defaults to latest)",
			},
			"registry": map[string]any{
				"type":        "string",
				"description": "Registry to install from (required, e.g., 'clawhub')",
			},
			"force": map[string]any{
				"type":        "boolean",
				"description": "Force reinstall if skill already exists (default false)",
			},
		},
		"required": []string{"slug", "registry"},
	}
}

func (t *InstallSkillTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// Install lock to prevent concurrent directory operations.
	// Ideally this should be done at a `slug` level, currently, its at the
	// (single, global) skills-directory level.
	t.mu.Lock()
	defer t.mu.Unlock()

	// Validate slug
	slug, _ := args["slug"].(string)
	if err := utils.ValidateSkillIdentifier(slug); err != nil {
		return ErrorResult(fmt.Sprintf("invalid slug %q: error: %s", slug, err.Error()))
	}

	// Validate registry
	registryName, _ := args["registry"].(string)
	if err := utils.ValidateSkillIdentifier(registryName); err != nil {
		return ErrorResult(fmt.Sprintf("invalid registry %q: error: %s", registryName, err.Error()))
	}

	version, _ := args["version"].(string)
	force, _ := args["force"].(bool)

	// Check if already installed. skillsDir is the fixed GLOBAL skills
	// directory (ADR-046 FR-009) — NOT a per-agent workspace path — so an
	// install from one agent is discoverable by every other agent.
	skillsDir := t.globalSkillsDir
	targetDir := filepath.Join(skillsDir, slug)

	alreadyInstalled := false
	if _, err := os.Stat(targetDir); err == nil {
		alreadyInstalled = true
	}
	if alreadyInstalled && !force {
		return ErrorResult(
			fmt.Sprintf("skill %q already installed at %s. Use force=true to reinstall.", slug, targetDir),
		)
	}

	// Resolve which registry to use BEFORE touching anything on disk: a
	// typo'd registry name (or any other resolution failure) must fail
	// closed with an existing install left exactly as it was, not after
	// that install has already been deleted.
	registry := t.registryMgr.GetRegistry(registryName)
	if registry == nil {
		return ErrorResult(fmt.Sprintf("registry %q not found", registryName))
	}

	// Ensure skills directory exists.
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create skills directory: %v", err))
	}

	// Download to a staging directory and only swap it into place once
	// everything below has succeeded. This is what makes force=true safe:
	// an ordinary failure (bad version, network error, moderation block)
	// never touches the existing install, because the existing install is
	// only ever removed immediately before the verified replacement is
	// renamed into its place.
	//
	// The staging directory lives under skillsDir/.staging — a dedicated,
	// non-scanned subdirectory — rather than directly inside the live global
	// skills directory. pkg/skills/loader.go's ListSkills iterates every
	// direct subdirectory of skillsDir and accepts any containing a
	// SKILL.md; a staging directory created directly inside skillsDir was
	// visible to a concurrent list_skills call or a system-prompt build
	// between extraction and the rename below, and — if the process died in
	// that window — was never cleaned up, leaving a permanent phantom skill
	// in every agent's skill list. .staging itself has no SKILL.md at
	// skillsDir's top level, so the scan never descends into it; it stays on
	// the same filesystem as skillsDir, so the final os.Rename below remains
	// an atomic, cheap rename.
	stagingRoot := filepath.Join(skillsDir, stagingDirName)
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create the staging directory for %q: %v", slug, err))
	}
	stageDir, err := os.MkdirTemp(stagingRoot, slug+".install-")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create a staging directory for %q: %v", slug, err))
	}
	// If we return before the rename below, stageDir was never moved and
	// this cleans it up; if the rename succeeded, stageDir no longer
	// exists and this is a harmless no-op.
	defer os.RemoveAll(stageDir)

	// Download and install (handles metadata, version resolution, extraction).
	result, err := registry.DownloadAndInstall(ctx, slug, version, stageDir)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to install %q: %v", slug, err))
	}

	// Moderation: block malware. The existing install (if any) is untouched.
	if result.IsMalwareBlocked {
		return ErrorResult(fmt.Sprintf("skill %q is flagged as malicious and cannot be installed", slug))
	}

	// Write origin metadata into the staged copy before it becomes the real one.
	if err := writeOriginMeta(stageDir, registry.Name(), slug, result.Version); err != nil {
		logger.ErrorCF("tool", "Failed to write origin metadata",
			map[string]any{
				"tool":     "install_skill",
				"error":    err.Error(),
				"target":   stageDir,
				"registry": registry.Name(),
				"slug":     slug,
				"version":  result.Version,
			})
		// Non-fatal: skill is installed, metadata just won't appear in audits.
	}

	// Everything above succeeded: only now do we touch the previous install
	// (if any), and only to swap in the verified replacement.
	if alreadyInstalled {
		if err := os.RemoveAll(targetDir); err != nil {
			return ErrorResult(fmt.Sprintf(
				"downloaded %q successfully but failed to remove the previous install at %s: %v",
				slug, targetDir, err,
			))
		}
	}
	if err := os.Rename(stageDir, targetDir); err != nil {
		return ErrorResult(fmt.Sprintf(
			"downloaded %q successfully but failed to move it into place at %s: %v",
			slug, targetDir, err,
		))
	}

	// Build result with moderation warning if suspicious.
	var output string
	if result.IsSuspicious {
		output = fmt.Sprintf("⚠️ Warning: skill %q is flagged as suspicious (may contain risky patterns).\n\n", slug)
	}
	output += fmt.Sprintf("Successfully installed skill %q v%s from %s registry.\nLocation: %s\n",
		slug, result.Version, registry.Name(), targetDir)

	if result.Summary != "" {
		output += fmt.Sprintf("Description: %s\n", result.Summary)
	}
	output += "\nThe skill is now available and can be loaded in the current session."

	return SilentResult(output)
}

// originMeta tracks which registry a skill was installed from.
type originMeta struct {
	Version          int    `json:"version"`
	Registry         string `json:"registry"`
	Slug             string `json:"slug"`
	InstalledVersion string `json:"installed_version"`
	InstalledAt      int64  `json:"installed_at"`
}

func writeOriginMeta(targetDir, registryName, slug, version string) error {
	meta := originMeta{
		Version:          1,
		Registry:         registryName,
		Slug:             slug,
		InstalledVersion: version,
		InstalledAt:      time.Now().UnixMilli(),
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	return fileutil.WriteFileAtomic(filepath.Join(targetDir, ".skill-origin.json"), data, 0o600)
}
