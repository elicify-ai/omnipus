package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/policy"
	"github.com/dapicom-ai/omnipus/pkg/skills"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

const skillsSearchMaxResults = 20

// buildRegistryManager creates a RegistryManager from the application config.
// Used by install, update, and search commands.
func buildRegistryManager(cfg *config.Config) *skills.RegistryManager {
	ch := cfg.Tools.Skills.Registries.ClawHub
	return skills.NewRegistryManagerFromConfig(skills.RegistryConfig{
		MaxConcurrentSearches: cfg.Tools.Skills.MaxConcurrentSearches,
		ClawHub: skills.ClawHubConfig{
			Enabled:         ch.Enabled,
			BaseURL:         ch.BaseURL,
			AuthToken:       os.Getenv(ch.AuthTokenRef),
			SearchPath:      ch.SearchPath,
			SkillsPath:      ch.SkillsPath,
			DownloadPath:    ch.DownloadPath,
			Timeout:         ch.Timeout,
			MaxZipSize:      ch.MaxZipSize,
			MaxResponseSize: ch.MaxResponseSize,
		},
	})
}

func skillsListCmd(loader *skills.SkillsLoader) {
	allSkills := loader.ListSkills()

	if len(allSkills) == 0 {
		fmt.Println("No skills installed.")
		return
	}

	fmt.Println("\nInstalled Skills:")
	fmt.Println("------------------")
	for _, skill := range allSkills {
		fmt.Printf("  ✓ %s (%s)\n", skill.Name, skill.Source)
		if skill.Description != "" {
			fmt.Printf("    %s\n", skill.Description)
		}
	}
}

func skillsInstallCmd(installer *skills.SkillInstaller, repo string) error {
	fmt.Printf("Installing skill from %s...\n", repo)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := installer.InstallFromGitHub(ctx, repo); err != nil {
		return fmt.Errorf("failed to install skill: %w", err)
	}

	fmt.Printf("\u2713 Skill '%s' installed successfully!\n", filepath.Base(repo))

	return nil
}

// skillTrustFromConfig returns the effective SkillTrustPolicy from the sandbox config.
func skillTrustFromConfig(cfg *config.Config) policy.SkillTrustPolicy {
	switch cfg.Sandbox.SkillTrust {
	case config.SkillTrustBlockUnverified:
		return policy.SkillTrustBlockUnverified
	case config.SkillTrustAllowAll:
		return policy.SkillTrustAllowAll
	default:
		return policy.SkillTrustWarnUnverified
	}
}

// enforceSkillTrust checks result.Verified against the configured trust policy.
// It removes targetDir on block, returns an error if policy blocks the install,
// and prints a warning if policy is warn_unverified and the skill is unverified.
func enforceSkillTrust(trust policy.SkillTrustPolicy, verified bool, slug, targetDir string) error {
	if verified || trust == policy.SkillTrustAllowAll {
		return nil
	}
	if trust == policy.SkillTrustBlockUnverified {
		if rmErr := os.RemoveAll(targetDir); rmErr != nil {
			fmt.Printf("✗ Failed to remove partial install: %v\n", rmErr)
		}
		return fmt.Errorf(
			"✗ skill '%s' could not be hash-verified and trust policy is block_unverified — install aborted",
			slug,
		)
	}
	// warn_unverified
	fmt.Printf(
		"⚠️  Warning: skill '%s' hash could not be verified (no hash in registry manifest). Install proceeded per warn_unverified policy.\n",
		slug,
	)
	return nil
}

// skillsInstallFromRegistry installs a skill from a named registry (e.g. clawhub).
func skillsInstallFromRegistry(cfg *config.Config, registryName, slug string) error {
	err := utils.ValidateSkillIdentifier(registryName)
	if err != nil {
		return fmt.Errorf("✗  invalid registry name: %w", err)
	}

	err = utils.ValidateSkillIdentifier(slug)
	if err != nil {
		return fmt.Errorf("✗  invalid slug: %w", err)
	}

	fmt.Printf("Installing skill '%s' from %s registry...\n", slug, registryName)

	registryMgr := buildRegistryManager(cfg)

	registry := registryMgr.GetRegistry(registryName)
	if registry == nil {
		return fmt.Errorf("✗  registry '%s' not found or not enabled. check your config.json.", registryName)
	}

	workspace := cfg.WorkspacePath()
	targetDir := filepath.Join(workspace, "skills", slug)

	if _, err = os.Stat(targetDir); err == nil {
		return fmt.Errorf("\u2717 skill '%s' already installed at %s", slug, targetDir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err = os.MkdirAll(filepath.Join(workspace, "skills"), 0o755); err != nil {
		return fmt.Errorf("\u2717 failed to create skills directory: %v", err)
	}

	result, err := registry.DownloadAndInstall(ctx, slug, "", targetDir)
	if err != nil {
		rmErr := os.RemoveAll(targetDir)
		if rmErr != nil {
			fmt.Printf("\u2717 Failed to remove partial install: %v\n", rmErr)
		}
		return fmt.Errorf("✗ failed to install skill: %w", err)
	}

	if result.IsMalwareBlocked {
		rmErr := os.RemoveAll(targetDir)
		if rmErr != nil {
			fmt.Printf("\u2717 Failed to remove partial install: %v\n", rmErr)
		}
		return fmt.Errorf("\u2717 Skill '%s' is flagged as malicious and cannot be installed.\n", slug)
	}

	// Enforce skill trust policy (SEC-09).
	if err := enforceSkillTrust(skillTrustFromConfig(cfg), result.Verified, slug, targetDir); err != nil {
		return err
	}

	if result.IsSuspicious {
		fmt.Printf("\u26a0\ufe0f  Warning: skill '%s' is flagged as suspicious.\n", slug)
	}

	fmt.Printf("\u2713 Skill '%s' v%s installed successfully!\n", slug, result.Version)
	if result.Summary != "" {
		fmt.Printf("  %s\n", result.Summary)
	}

	return nil
}

// skillsUpdateCmd re-installs a skill from its registry at the latest available version.
// It delegates to the ClawHub registry using the existing config.
func skillsUpdateCmd(installer *skills.SkillInstaller, skillName string) error {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	err = utils.ValidateSkillIdentifier(skillName)
	if err != nil {
		return fmt.Errorf("✗  invalid skill name: %w", err)
	}

	// Determine which registry the skill came from via its .skill-origin.json.
	originPath := filepath.Join(cfg.WorkspacePath(), "skills", skillName, ".skill-origin.json")
	registryName, err := readSkillOriginRegistry(originPath)
	if err != nil {
		fmt.Printf("⚠️  Warning: could not read skill origin file (%v) — falling back to 'clawhub' registry.\n", err)
		registryName = "clawhub"
	}

	fmt.Printf("Updating skill '%s' from %s...\n", skillName, registryName)

	registryMgr := buildRegistryManager(cfg)

	registry := registryMgr.GetRegistry(registryName)
	if registry == nil {
		return fmt.Errorf("✗  registry '%s' not found or not enabled. check your config.json.", registryName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Remove old version before reinstalling.
	if unErr := installer.Uninstall(skillName); unErr != nil {
		return fmt.Errorf("✗  failed to remove old version: %w", unErr)
	}

	workspace := cfg.WorkspacePath()
	targetDir := filepath.Join(workspace, "skills", skillName)

	if mkErr := os.MkdirAll(filepath.Join(workspace, "skills"), 0o755); mkErr != nil {
		return fmt.Errorf("✗  failed to create skills directory: %w", mkErr)
	}

	result, err := registry.DownloadAndInstall(ctx, skillName, "", targetDir)
	if err != nil {
		_ = os.RemoveAll(targetDir)
		return fmt.Errorf("✗  failed to install update: %w", err)
	}

	if result.IsMalwareBlocked {
		_ = os.RemoveAll(targetDir)
		return fmt.Errorf("✗  Skill '%s' is flagged as malicious and cannot be installed.", skillName)
	}

	// Enforce skill trust policy (SEC-09).
	if trustErr := enforceSkillTrust(
		skillTrustFromConfig(cfg),
		result.Verified,
		skillName,
		targetDir,
	); trustErr != nil {
		return trustErr
	}

	if result.IsSuspicious {
		fmt.Printf("⚠️  Warning: skill '%s' is flagged as suspicious.\n", skillName)
	}

	fmt.Printf("✓ Skill '%s' updated to v%s successfully!\n", skillName, result.Version)
	return nil
}

// readSkillOriginRegistry reads the registry name from a .skill-origin.json file.
func readSkillOriginRegistry(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var origin struct {
		Registry string `json:"registry"`
	}
	if err := json.Unmarshal(data, &origin); err != nil || origin.Registry == "" {
		return "", fmt.Errorf("no registry in origin file")
	}
	return origin.Registry, nil
}

func skillsRemoveCmd(installer *skills.SkillInstaller, skillName string) {
	fmt.Printf("Removing skill '%s'...\n", skillName)

	if err := installer.Uninstall(skillName); err != nil {
		fmt.Printf("✗ Failed to remove skill: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Skill '%s' removed successfully!\n", skillName)
}

func skillsInstallBuiltinCmd(workspace string) {
	builtinSkillsDir := "./omnipus/skills"
	workspaceSkillsDir := filepath.Join(workspace, "skills")

	fmt.Printf("Copying builtin skills to workspace...\n")

	skillsToInstall := []string{
		"weather",
		"news",
		"stock",
		"calculator",
	}

	var failed, installed int
	for _, skillName := range skillsToInstall {
		builtinPath := filepath.Join(builtinSkillsDir, skillName)
		workspacePath := filepath.Join(workspaceSkillsDir, skillName)

		if _, err := os.Stat(builtinPath); err != nil {
			fmt.Printf("⊘ Builtin skill '%s' not found: %v\n", skillName, err)
			failed++
			continue
		}

		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			fmt.Printf("✗ Failed to create directory for %s: %v\n", skillName, err)
			failed++
			continue
		}

		if err := copyDirectory(builtinPath, workspacePath); err != nil {
			fmt.Printf("✗ Failed to copy %s: %v\n", skillName, err)
			failed++
			continue
		}
		installed++
	}

	fmt.Println()
	if failed == 0 {
		fmt.Printf("✓ All %d builtin skills installed successfully!\n", installed)
	} else {
		fmt.Printf("⚠️  %d skill(s) installed, %d failed. Check the errors above.\n", installed, failed)
	}
	fmt.Println("Now you can use them in your workspace.")
}

func skillsListBuiltinCmd() {
	cfg, err := internal.LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}
	builtinSkillsDir := filepath.Join(filepath.Dir(cfg.WorkspacePath()), "omnipus", "skills")

	fmt.Println("\nAvailable Builtin Skills:")
	fmt.Println("-----------------------")

	entries, err := os.ReadDir(builtinSkillsDir)
	if err != nil {
		fmt.Printf("Error reading builtin skills: %v\n", err)
		return
	}

	if len(entries) == 0 {
		fmt.Println("No builtin skills available.")
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			skillName := entry.Name()
			skillFile := filepath.Join(builtinSkillsDir, skillName, "SKILL.md")

			description := "No description"
			if _, err := os.Stat(skillFile); err == nil {
				data, err := os.ReadFile(skillFile)
				if err == nil {
					content := string(data)
					if idx := strings.Index(content, "\n"); idx > 0 {
						firstLine := content[:idx]
						if strings.Contains(firstLine, "description:") {
							descLine := strings.Index(content[idx:], "\n")
							if descLine > 0 {
								description = strings.TrimSpace(content[idx+descLine : idx+descLine])
							}
						}
					}
				}
			}
			status := "✓"
			fmt.Printf("  %s  %s\n", status, entry.Name())
			if description != "" {
				fmt.Printf("     %s\n", description)
			}
		}
	}
}

func skillsSearchCmd(query string) {
	fmt.Println("Searching for available skills...")

	cfg, err := internal.LoadConfig()
	if err != nil {
		fmt.Printf("✗ Failed to load config: %v\n", err)
		return
	}

	registryMgr := buildRegistryManager(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := registryMgr.SearchAll(ctx, query, skillsSearchMaxResults)
	var partialErr *skills.PartialSearchError
	switch {
	case err == nil:
		// full success
	case errors.As(err, &partialErr):
		fmt.Printf("⚠️  Warning: results may be incomplete — one or more registries failed: %v\n\n", partialErr.Cause)
	default:
		fmt.Printf("✗ Failed to fetch skills list: %v\n", err)
		return
	}

	if len(results) == 0 {
		fmt.Println("No skills available.")
		return
	}

	fmt.Printf("\nAvailable Skills (%d):\n", len(results))
	fmt.Println("--------------------")
	for _, result := range results {
		fmt.Printf("  📦 %s\n", result.DisplayName)
		fmt.Printf("     %s\n", result.Summary)
		fmt.Printf("     Slug: %s\n", result.Slug)
		fmt.Printf("     Registry: %s\n", result.RegistryName)
		if result.Version != "" {
			fmt.Printf("     Version: %s\n", result.Version)
		}
		fmt.Println()
	}
}

func skillsShowCmd(loader *skills.SkillsLoader, skillName string) {
	content, ok := loader.LoadSkill(skillName)
	if !ok {
		fmt.Printf("✗ Skill '%s' not found\n", skillName)
		return
	}

	fmt.Printf("\n📦 Skill: %s\n", skillName)
	fmt.Println("----------------------")
	fmt.Println(content)
}

func copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}
