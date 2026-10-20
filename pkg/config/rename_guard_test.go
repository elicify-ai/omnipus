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
		walkErr := walkRenameGuardSourceTree(base, func(path string, directory bool, err error) error {
			if err != nil {
				return err
			}
			if directory {
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
//
// Adding a new entry is a deliberate, reviewed exception: confirm which type is
// genuinely involved before silencing a failure this way.
//
// COLLISION NOTE: because keys are content, two identical lines in the same file
// collapse to one entry — intended for repeated idioms (e.g. the seven
// state.Workspace field reads in pkg/sysagent/tools/workspace.go). A reviewed
// entry therefore whitelists that exact line wherever it appears in that file.
var allowedWorkspaceIdentifierSites = map[string]map[string]bool{
	// L1 #1274 fixtures: these ReadState results are workspace.State (whose
	// Workspace field is the live workspace record), never AgentConfig.
	// Reviewed ci1-fs chief ruling 2026-10-14: exact content-anchored entries;
	// the scanner and all membership/delegation assertions stay unchanged.
	// l11274AssertCreated: durable membership readback.
	"pkg/agent/l1_1274_create_test.go": {
		`if !reflect.DeepEqual(state.Workspace.CoreTeam, []string{"omnipus", id}) {`:                                              true,
		`t.Errorf("create must join exactly the requesting workspace: team=%v, want [omnipus %s]", state.Workspace.CoreTeam, id)`: true,
	},
	// l11274DelegatingContext and l11274SeedWorker: fixture team records.
	"pkg/agent/l1_1274_delegate_test.go": {
		`state.Workspace.CoreTeam = append(state.Workspace.CoreTeam, "jim")`: true,
		`state.Workspace.CoreTeam = append(state.Workspace.CoreTeam, id)`:    true,
		`data, err := json.Marshal(state.Workspace)`:                         true,
	},
	// TestL11274NativeMainAndSubagentAcceptAndPersistSoul: membership readback.
	"pkg/agent/l1_1274_fix1_soul_test.go": {
		// WU-3: the created agent's id is a generated UUID read from the result.
		`if err != nil || !reflect.DeepEqual(state.Workspace.CoreTeam, []string{"omnipus", id}) {`:  true,
		`t.Errorf("native create membership readback = %v, err=%v", state.Workspace.CoreTeam, err)`: true,
	},
	// ADR-090 workspace.State.Workspace — the multi-agent workspace record.
	"pkg/gateway/rest_workspace_delegation.go": {
		"ws := state.Workspace": true,
	},
	"pkg/agent/routing_drops.go": {
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
	// Train 1 #1363: ReadState returns workspace.State; this is the default
	// multi-agent workspace record, not an agent's home directory.
	"pkg/gateway/train1_default_workspace_delete_test.go": {
		`require.True(t, before.Workspace.IsDefault)`: true,
	},
	"pkg/sysagent/tools/workspace.go": {
		"\"core_team\":   state.Workspace.CoreTeam,":    true,
		"\"description\": state.Workspace.Description,": true,
		"\"id\":          state.Workspace.ID,":          true,
		"\"name\":        state.Workspace.Name,":        true,
		"\"pin_order\":   state.Workspace.PinOrder,":    true,
		"\"pinned\":      state.Workspace.Pinned,":      true,
		"\"status\":      state.Workspace.Status,":      true,
		// Train 1 #1363: CheckRevisionLocked returns workspace.State;
		// default-workspace protection reads that record, not AgentConfig.
		`if refusal := workspacepkg.RefuseDefaultDeletion(state.Workspace.IsDefault); refusal != nil {`: true,
		// WU-3 (FR-032, #1057): update_workspace's staged read-back and the
		// canonical workspace view read workspace.State.Workspace.
		`resp := workspaceView(state.Workspace, state.Delegation, state.Revision, computeWorkspaceTaskCount(home, id))`:                                                                    true,
		`members := workspacepkg.TeamSet(state.Workspace.CoreTeam, nil)`:                                                                                                                   true,
		`u := &workspaceUpdate{t: t, args: args, id: id, state: state, w: state.Workspace,`:                                                                                                true,
		`oldTeam: append([]string(nil), state.Workspace.CoreTeam...)}`:                                                                                                                     true,
		`changed := workspaceChangedFields(u.state.Workspace, u.w)`:                                                                                                                        true,
		`resp := workspaceView(u.state.Workspace, u.state.Delegation, u.state.Revision, computeWorkspaceTaskCount(home, u.id))`:                                                            true,
		`return workspaceMutationError(u.id, "save_delegation", "partial", "", workspaceChangedFields(u.state.Workspace, u.w), map[string]any{"exists": true, "state_read_failed": true})`: true,
		`return workspaceMutationError(u.id, "save_delegation", "partial", actual.Revision, workspaceChangedFields(u.state.Workspace, actual.Workspace), workspaceActualState(actual))`:    true,
		`return tools.NewToolResult(successJSON(workspaceView(state.Workspace, state.Delegation, state.Revision,`:                                                                          true,
	},
	// WU-3 RED real-registry tests: workspace.ReadState returns workspace.State.
	"pkg/gateway/wu3_real_registry_test.go": {
		`assert.Equal(t, []string{id}, target.Workspace.CoreTeam)`:                                                                                       true,
		`assert.Equal(t, []string{"omnipus"}, anchor.Workspace.CoreTeam)`:                                                                                true,
		`assert.Equal(t, "real WU3 turn write", after.Workspace.Description, "actual registered Execute must have committed, not returned model prose")`: true,
	},
	// WU-3 create_agent workspace_id test: ReadState returns workspace.State.
	// WU-3 team-admission test (b39cfe2fe): workspace.ReadState returns
	// workspace.State; after.Workspace is that state's record, not AgentConfig.
	"pkg/sysagent/tools/wu3_fix_team_admission_test.go": {
		`assert.Empty(t, after.Workspace.CoreTeam)`: true,
	},
	"pkg/sysagent/tools/wu3dev_create_agent_target_test.go": {
		`require.Contains(t, sales.Workspace.CoreTeam, id, "the agent joins the target team")`:          true,
		`require.NotContains(t, other.Workspace.CoreTeam, id, "the caller's workspace is not touched")`: true,
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
