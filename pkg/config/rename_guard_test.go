// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestNoAgentConfigWorkspaceIdentifier is the CI grep-guard for ADR-046
// FR-001/FR-002 (docs/internal/specs/unified-filesystem-workspace-spec.md
// US-1): the per-agent directory concept was renamed from "workspace" to
// "agent home" via identifier-scoped gopls renames —
// AgentInstance.Workspace, AgentConfig.Workspace, AgentDefaults.Workspace,
// AgentModelConfig.Workspace all became .Home; resolveAgentWorkspace /
// ResolveAgentWorkspace became resolveAgentHome / ResolveAgentHome;
// datamodel.AgentWorkspacePath / InitAgentWorkspace became AgentHomePath /
// InitAgentHome; Config.WorkspacePath became Config.AgentHomeBasePath.
//
// This test fails the build if that agent-config ".Workspace" spelling (as
// either a selector, e.g. `agentCfg.Workspace`, or a composite-literal key,
// e.g. `AgentConfig{Workspace: ...}`) is ever reintroduced anywhere under
// pkg/ or cmd/ — the FR-001 regression this guard exists to catch.
//
// It deliberately does NOT flag:
//
//   - pkg/workspace/ (skipped entirely) and any `workspace.Workspace` /
//     `workspacepkg.Workspace` / `gen.Workspace`-qualified reference — the
//     UNRELATED, still-live pkg/workspace.Workspace multi-agent Workspace
//     feature (CoreTeam, REST-CRUD, delegation graph). The two concepts
//     share an English word by historical accident, not by design.
//
//   - pkg/api/generated/ — machine-generated from contracts/*.yaml; never
//     hand-edited, and regenerating it is outside the scope of a pure Go
//     identifier rename (no wire-shape/schema change is involved here).
//
//   - env:"..."/json:"..." struct-tag strings — the persisted wire/env
//     format is deliberately frozen (existing config.json files must keep
//     parsing), so e.g. Home string with tag json:"workspace,omitempty" is
//     correct and must never be flagged.
//     NOTE ON FRAGILITY: the allowlist pins absolute LINE NUMBERS, so ANY edit
//     that adds or removes lines above an entry in the same file shifts it and
//     makes this guard report the same, unchanged code as a fresh violation.
//     That is exactly what happened when dead exports were removed from
//     openclaw_config.go: all seven entries moved by a uniform -22. If this test
//     fails with a uniform offset across every entry in one file, the code did
//     not regress — re-point the entries. A content-based anchor would be more
//     robust, but line numbers keep the allowlist reviewable at a glance.
//
//   - a documented, reviewed file:line allowlist (allowedWorkspaceIdentifierLines
//     below) for the two unrelated types that happen to share the field
//     name "Workspace" by coincidence, plus one comment heading — see the
//     allowlist's own doc comment for exactly why each entry is there.
func TestNoAgentConfigWorkspaceIdentifier(t *testing.T) {
	root := repoRootForRenameGuard(t)

	selectorRe := regexp.MustCompile(`\.Workspace\b`)
	literalKeyRe := regexp.MustCompile(`\bWorkspace\s*:`)
	qualifiedRefs := []string{"workspace.Workspace", "workspacepkg.Workspace", "gen.Workspace"}

	skipDirs := map[string]bool{
		filepath.Join(root, "pkg", "workspace"):        true,
		filepath.Join(root, "pkg", "api", "generated"): true,
	}

	var violations []string

	for _, base := range []string{filepath.Join(root, "pkg"), filepath.Join(root, "cmd")} {
		walkErr := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if skipDirs[path] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			// This guard's own doc comments and regexes legitimately spell out
			// the ".Workspace" pattern it exists to catch — self-scanning would
			// always fail. Skip this one file, and only this file.
			if filepath.Base(path) == "rename_guard_test.go" {
				return nil
			}

			relPath, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return fmt.Errorf("rel path for %s: %w", path, relErr)
			}
			relPath = filepath.ToSlash(relPath)

			f, openErr := os.Open(path)
			if openErr != nil {
				return fmt.Errorf("open %s: %w", path, openErr)
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			lineNum := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Text()
				if !strings.Contains(line, "Workspace") {
					continue
				}
				if strings.Contains(line, `json:"`) || strings.Contains(line, `env:"`) {
					continue
				}

				stripped := line
				for _, q := range qualifiedRefs {
					stripped = strings.ReplaceAll(stripped, q, "")
				}
				if !selectorRe.MatchString(stripped) && !literalKeyRe.MatchString(stripped) {
					continue
				}

				key := fmt.Sprintf("%s:%d", relPath, lineNum)
				if allowedWorkspaceIdentifierLines[key] {
					continue
				}

				violations = append(violations, fmt.Sprintf("%s: %s", key, strings.TrimSpace(line)))
			}
			if scanErr := scanner.Err(); scanErr != nil {
				return fmt.Errorf("scan %s: %w", path, scanErr)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", base, walkErr)
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf(
			"reintroduced agent-config .Workspace usage (FR-001/FR-002 — rename "+
				"the identifier to .Home, or add a reviewed file:line entry to "+
				"allowedWorkspaceIdentifierLines in this file with a documented "+
				"reason if it genuinely belongs to an unrelated type):\n%s",
			strings.Join(violations, "\n"),
		)
	}
}

// allowedWorkspaceIdentifierLines documents every file:line where a literal
// "Workspace" selector or composite-literal key legitimately survives the
// ADR-046 "agent home" rename. Every entry here belongs to one of two
// types that are NOT one of the four renamed agent-config types
// (config.AgentConfig, config.AgentDefaults, config.AgentModelConfig,
// agent.AgentInstance) — they merely share the field name "Workspace" by
// historical coincidence:
//
//   - pkg/skills: GitHubRegistryConfig.Workspace and the MarketplaceEntry
//     .Workspace it's built from (pkg/skills/registry.go's own field, task
//     explicitly out of scope for this rename) — the skills-marketplace
//     install directory, unrelated to any agent's own home directory.
//   - pkg/migrate/sources/openclaw/openclaw_config.go (+ its _test.go): the
//     read side is OpenClawAgentDefaults.Workspace, mirroring OpenClaw's
//     FROZEN third-party JSON schema (renaming breaks parsing real openclaw
//     configs); the local OmnipusConfig/AgentDefaults/AgentConfig types
//     declared later in the SAME file are a package-private staging format
//     used only to shuttle data between the two schemas before
//     ToStandardConfig() writes the REAL config.AgentConfig/AgentDefaults —
//     which correctly uses .Home (openclaw_config.go:889,916 — NOT
//     allowlisted, and must never be added here).
//   - (retired) pkg/sandbox/sandbox.go's comment heading ("// Workspace: full
//     RWX ...", a prose label), not a struct field or composite literal.
//   - pkg/agent/loop.go's comment explaining why a second, competing
//     remove_skill registration was deleted (commit 81f7ef26): the prose
//     names "agent.Workspace" only as the root the DELETED code used to be
//     constructed against, pre-ADR-046 — historical context in a comment,
//     not a live selector or composite-literal key.
//
// Adding a new entry is a deliberate, reviewed exception: confirm which type
// is genuinely involved before silencing a failure this way.
var allowedWorkspaceIdentifierLines = map[string]bool{
	"pkg/skills/github_registry.go:41": true,
	"pkg/skills/github_registry.go:50": true,
	"pkg/skills/github_registry.go:65": true,
	"pkg/skills/github_registry.go:74": true,
	"pkg/skills/registry.go:208":       true,
	"pkg/skills/config_bridge.go:53":   true,
	"pkg/skills/config_bridge.go:54":   true,
	"pkg/agent/loop.go:1965":           true,

	"pkg/skills/github_registry_test.go:24":  true,
	"pkg/skills/github_registry_test.go:35":  true,
	"pkg/skills/github_registry_test.go:46":  true,
	"pkg/skills/github_registry_test.go:58":  true,
	"pkg/skills/github_registry_test.go:69":  true,
	"pkg/skills/github_registry_test.go:78":  true,
	"pkg/skills/github_registry_test.go:98":  true,
	"pkg/skills/github_registry_test.go:134": true,
	"pkg/skills/github_registry_test.go:179": true,
	"pkg/skills/github_registry_test.go:204": true,
	"pkg/skills/github_registry_test.go:205": true,
	"pkg/skills/github_registry_test.go:221": true,
	"pkg/skills/github_registry_test.go:236": true,
	"pkg/skills/github_registry_test.go:238": true,
	"pkg/skills/github_registry_test.go:321": true,

	"pkg/migrate/sources/openclaw/openclaw_config.go:374": true,
	"pkg/migrate/sources/openclaw/openclaw_config.go:377": true,
	"pkg/migrate/sources/openclaw/openclaw_config.go:416": true,
	"pkg/migrate/sources/openclaw/openclaw_config.go:863": true,
	"pkg/migrate/sources/openclaw/openclaw_config.go:864": true,
	"pkg/migrate/sources/openclaw/openclaw_config.go:890": true,
	"pkg/migrate/sources/openclaw/openclaw_config.go:925": true,

	"pkg/migrate/sources/openclaw/openclaw_config_test.go:250": true,
	"pkg/migrate/sources/openclaw/openclaw_config_test.go:251": true,
	"pkg/migrate/sources/openclaw/openclaw_config_test.go:618": true,
}

// repoRootForRenameGuard resolves the repository root from this test file's
// own path (pkg/config/rename_guard_test.go is two directories below root),
// so the guard works regardless of the caller's current working directory —
// matching the pattern in pkg/channels/webhook_signature_test.go.
func repoRootForRenameGuard(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolved repo root %q does not contain go.mod: %v", root, err)
	}
	return root
}
