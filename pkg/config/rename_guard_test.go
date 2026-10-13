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
//     NOTE ON ROBUSTNESS (item 9): the allowlist is keyed by file AND the exact
//     trimmed source line, NOT by absolute line numbers. It used to pin line
//     numbers, so any edit above an entry re-reported unchanged, legitimate code
//     as a fresh violation (the reason this guard was red at 67345b1d7 with no
//     real regression). A moved line now costs nothing; a genuine new
//     agent-config ".Workspace" use still fails, because it cannot match any
//     reviewed line.
//
//   - a documented, reviewed content-anchored allowlist
//     (allowedWorkspaceIdentifierSites below) for the unrelated types that
//     happen to share the field name "Workspace" by coincidence — see the
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

				// Content-anchored allowlist: keyed by (file, exact trimmed
				// source line), so an edit that shifts line numbers no longer
				// re-reports unchanged, legitimate code as a fresh violation.
				if allowedWorkspaceIdentifierSites[relPath][strings.TrimSpace(line)] {
					continue
				}

				key := fmt.Sprintf("%s:%d", relPath, lineNum)
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
				"the identifier to .Home, or add a reviewed entry to "+
				"allowedWorkspaceIdentifierSites in this file (keyed by file AND the "+
				"exact trimmed source line) with a documented reason if it genuinely "+
				"belongs to an unrelated type):\n%s",
			strings.Join(violations, "\n"),
		)
	}
}

// allowedWorkspaceIdentifierSites is the CONTENT-anchored allowlist: every
// reviewed place a literal "Workspace" selector or composite-literal key
// legitimately survives the ADR-046 "agent home" rename, keyed by the file and
// the EXACT trimmed source line.
//
// Why content, not file:line (item 9): the previous allowlist pinned absolute
// line numbers, so any edit above an entry shifted it and re-reported the same,
// unchanged, legitimate code as a fresh violation — the reason this guard was
// red at 67345b1d7 while containing no real regression. That churned the
// allowlist on nearly every merge. Keyed by content, a line move costs nothing
// and a genuine new agent-config ".Workspace" use still fails: it cannot match
// any reviewed line.
//
// Every entry belongs to a type that is NOT one of the four renamed agent-config
// types (config.AgentConfig, config.AgentDefaults, config.AgentModelConfig,
// agent.AgentInstance) — the types merely share the field name "Workspace" by
// historical coincidence:
//
//   - workspace.State.Workspace (ADR-090): the multi-agent workspace record
//     paired with its delegation graph and revision, read through
//     ReadState/CheckRevisionLocked — not an agent home.
//   - pkg/skills: GitHubRegistryConfig.Workspace and the MarketplaceEntry
//     .Workspace it's built from (the skills-marketplace install directory,
//     unrelated to any agent's own home directory).
//   - pkg/migrate/sources/openclaw: OpenClawAgentDefaults.Workspace (the
//     FROZEN third-party JSON schema — renaming breaks parsing real openclaw
//     configs) and the package-private staging types that shuttle data into the
//     REAL config.AgentConfig/AgentDefaults .Home field.
//
// Adding a new entry is a deliberate, reviewed exception: confirm which type is
// genuinely involved before silencing a failure this way.
//
// COLLISION NOTE: because keys are content, two identical lines in the same file
// collapse to one entry — intended for repeated idioms (e.g. the seven
// state.Workspace field reads in pkg/sysagent/tools/workspace.go). A reviewed
// entry therefore whitelists that exact line wherever it appears in that file.
var allowedWorkspaceIdentifierSites = map[string]map[string]bool{
	// ADR-090 workspace.State.Workspace — the multi-agent workspace record.
	"pkg/gateway/rest_workspace_delegation.go": {
		"ws := state.Workspace": true,
	},
	"pkg/gateway/rest_workspace_wire_snapshot_test.go": {
		"reread := workspaceToWire(api, snapshot.Workspace, 0)":                                               true,
		"wire := workspaceToWireFrom(api.homePath, snapshot.Workspace, 0, &snapshot, api.mainSessionAddress)": true,
	},
	"pkg/gateway/rest_workspaces.go": {
		"existingTeam := workspace.TeamSet(rw.state.Workspace.CoreTeam, nil)":                                                               true,
		"jsonOK(w, workspaceToWireFrom(a.homePath, state.Workspace, countTasksForWorkspace(a.homePath, id), &state, a.mainSessionAddress))": true,
		"rd.ws = state.Workspace": true,
		"return &workspace.State{Workspace: rw.ws, Delegation: edges, Revision: revision}": true,
		"rw.ws = state.Workspace": true,
	},
	"pkg/sysagent/tools/workspace.go": {
		"\"core_team\":   state.Workspace.CoreTeam,":    true,
		"\"description\": state.Workspace.Description,": true,
		"\"id\":          state.Workspace.ID,":          true,
		"\"name\":        state.Workspace.Name,":        true,
		"\"pin_order\":   state.Workspace.PinOrder,":    true,
		"\"pinned\":      state.Workspace.Pinned,":      true,
		"\"status\":      state.Workspace.Status,":      true,
		"return workspaceMutationError(id, \"save_delegation\", \"partial\", \"\", workspaceChangedFields(state.Workspace, w), map[string]any{\"exists\": true, \"state_read_failed\": true})": true,
		"return workspaceMutationError(id, \"save_delegation\", \"partial\", actual.Revision, workspaceChangedFields(state.Workspace, actual.Workspace), workspaceActualState(actual))":        true,
		"w := state.Workspace": true,
	},
	"pkg/sysagent/tools/ava_configuration_context_test.go": {
		"require.Equal(t, \"Coordination for the launch\", state.Workspace.Description)": true,
		"require.Equal(t, \"Coordination for the launch\", state.Workspace.Description,": true,
		"require.Equal(t, \"Launch Room West\", state.Workspace.Name)":                   true,
		"require.Equal(t, \"Launch Room\", state.Workspace.Name)":                        true,
		"require.Equal(t, \"active\", state.Workspace.Status)":                           true,
		"require.Equal(t, []string{\"field-analyst\"}, state.Workspace.CoreTeam)":        true,
		"require.Equal(t, true, state.Workspace.Pinned)":                                 true,
	},
	"pkg/sysagent/tools/workspace_outcomes_test.go": {
		"if after.Workspace.Name != \"After\" || !reflect.DeepEqual(after.Delegation, oldEdges) {": true,
	},
	// DEL-23 task Owner attribution: st is a workspace.State (workspace.ReadState).
	"pkg/tools/task.go": {
		"owner = st.Workspace.Owner": true,
	},

	// pkg/skills: GitHubRegistryConfig.Workspace / MarketplaceEntry.Workspace.
	"pkg/skills/github_registry.go": {
		"if cfg.Workspace == \"\" {": true,
		"installer, err := NewSkillInstaller(cfg.Workspace, cfg.Token, cfg.Proxy)":              true,
		"installer, err := NewSkillInstallerWithSSRF(cfg.Workspace, cfg.Token, cfg.Proxy, nil)": true,
	},
	"pkg/skills/registry.go": {
		"Workspace: m.Workspace,": true,
	},
	"pkg/skills/config_bridge.go": {
		"entry.Workspace = githubWorkspace":                                      true,
		"if m.Type == config.MarketplaceTypeGitHub && entry.Workspace == \"\" {": true,
	},
	"pkg/skills/config_bridge_test.go": {
		"assert.Equal(t, \"/injected-ws\", gh.Workspace, \"empty github Workspace must be injected\")": true,
	},
	"pkg/skills/github_registry_test.go": {
		"GitHubRegistryConfig{Enabled: true, Workspace: ws},": true,
		"Workspace: ws,": true,
		"_, err := NewGitHubRegistry(GitHubRegistryConfig{Enabled: true, Workspace: \"\"})": true,
		"reg, err := NewGitHubRegistry(GitHubRegistryConfig{Enabled: true, Workspace: ws})": true,
		"{Name: \"github\", Type: \"github\", Enabled: false, Workspace: ws},":              true,
		"{Name: \"github-bad\", Type: \"github\", Enabled: true, Workspace: \"\"},":         true,
		"{Name: \"github-corp\", Type: \"github\", Enabled: true, Workspace: ws2},":         true,
		"{Name: \"github-good\", Type: \"github\", Enabled: true, Workspace: t.TempDir()},": true,
		"{Name: \"github-public\", Type: \"github\", Enabled: true, Workspace: ws1},":       true,
	},

	// pkg/migrate/sources/openclaw: frozen OpenClaw schema + local staging types.
	"pkg/migrate/sources/openclaw/openclaw_config.go": {
		"Home:    a.Workspace,": true,
		"agentCfg.Workspace = rewriteWorkspacePath(*entry.Workspace)":                            true,
		"cfg.Agents.Defaults.Home = c.Agents.Defaults.Workspace":                                 true,
		"cfg.Agents.Defaults.Workspace = c.GetDefaultWorkspace()":                                true,
		"if c.Agents == nil || c.Agents.Defaults == nil || c.Agents.Defaults.Workspace == nil {": true,
		"if entry.Workspace != nil {":                                                            true,
		"return rewriteWorkspacePath(*c.Agents.Defaults.Workspace)":                              true,
	},
	"pkg/migrate/sources/openclaw/openclaw_config_test.go": {
		"Workspace: \"~/.omnipus/workspace\",":                                                                    true,
		"if omnipusCfg.Agents.Defaults.Workspace != \"~/.omnipus/workspace\" {":                                   true,
		"t.Errorf(\"expected workspace '~/.omnipus/workspace', got '%s'\", omnipusCfg.Agents.Defaults.Workspace)": true,
	},
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
