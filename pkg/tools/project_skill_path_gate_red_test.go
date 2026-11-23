//go:build goolm && stdjson

package tools

// Order 50 / CHECK F25. C-25, S-111, literal D14#1–11. Raw persisted
// global permissions and real mounts feed the SAME native turn authority used
// by read_file, grep, list_directory and bash. No pre-decided outcome/action
// adapter remains. The editor's allowed file result MUST contain every byte,
// including the sentinel; it is not a Skill load. The separate native-loop
// TestProjectSkillEditor_Native in pkg/gateway observes instructions, actual
// load results/audit and helper effects. Kernel/platform certification is
// UNVERIFIED here: D14#12–13 and hands-on ST-28 are separate experiments.
// GREEN, mutation and proof-of-failability defer to independent CHECK.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gateAskPendingText = "notes is a project skill this agent may not use yet. Approve it when it is loaded, or change its permission in Skills & Tools."
const d14Instructions = "---\nname: notes\ndescription: Project notes.\n---\n\nGATE-SENTINEL notes instructions.\n"
const d14Readme = "GATE-ORDINARY An ordinary file.\n"
const d14HelperEffect = "GATE-HELPER-RAN\n"

type d14NativeFixture struct {
	ctx                                                         context.Context
	home, agentHome, work, mount, skill, helper, effect, readme string
	key                                                         string
}

// The fixture's inputs are stored policies, not the resolver's desired result.
func seedGateFixture(t *testing.T, shelf, slug, permission, editSkill string, nested bool) d14NativeFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	f := d14NativeFixture{home: filepath.Join(root, "home"), mount: filepath.Join(root, "W1")}
	f.agentHome = filepath.Join(f.home, "agents", "mia")
	require.NoError(t, os.MkdirAll(f.agentHome, 0o700))
	require.NoError(t, os.MkdirAll(f.mount, 0o700))
	t.Setenv(config.EnvHome, f.home)
	t.Setenv(config.EnvConfig, filepath.Join(f.home, "config.json"))
	mia := config.AgentConfig{ID: "mia", Home: f.agentHome, Tools: &config.AgentToolsCfg{}}
	mia.Tools.Builtin.Policies = map[string]config.ToolPolicy{"edit_skill": config.ToolPolicy(editSkill)}
	require.NoError(t, agentstore.New(f.home).Create("mia", &mia))
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{mia}
	cfg.Sandbox.ToolPolicies["edit_skill"] = "allow"
	require.NoError(t, config.SaveConfig(filepath.Join(f.home, "config.json"), cfg))
	require.NoError(t, workspace.SaveRecord(f.home, workspace.Workspace{ID: "w1", Name: "D14 W1", Status: "active", CoreTeam: []string{"mia"}}))
	f.work, err = workspace.EnsureWorkDir(f.home, "w1")
	require.NoError(t, err)
	_, warning, err := workspace.CreateMount(f.home, "w1", "outer", f.mount)
	require.NoError(t, err)
	require.Empty(t, warning)
	projectRoot := f.mount
	if nested {
		projectRoot = filepath.Join(f.mount, "sub")
		require.NoError(t, os.MkdirAll(projectRoot, 0o700))
		_, warning, err = workspace.CreateMount(f.home, "w1", "inner", projectRoot)
		require.NoError(t, err)
		require.Empty(t, warning)
	}
	roots := workspace.AllowedMountRoots(f.home, "w1")
	require.Contains(t, roots, f.mount, "real persisted outer mount, outside OMNIPUS_HOME")
	if nested {
		require.Contains(t, roots, projectRoot, "a real second mount, not a nested-read action label")
	}
	dir := filepath.Join(projectRoot, shelf, "skills", slug)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	f.skill, f.helper = filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "helper.sh")
	body := d14Instructions
	if slug == "x" {
		body = strings.Replace(body, "name: notes", "name: x", 1)
	}
	require.NoError(t, os.WriteFile(f.skill, []byte(body), 0o600))
	f.effect, f.readme = filepath.Join(f.work, "helper-effect"), filepath.Join(f.mount, "README.md")
	require.NoError(t, os.WriteFile(f.readme, []byte(d14Readme), 0o600))
	helper := "#!/bin/sh\nprintf 'GATE-HELPER-RAN\\n' > " + d14Quote(f.effect) + "\nprintf 'GATE-HELPER-RAN\\n'\n"
	require.NoError(t, os.WriteFile(f.helper, []byte(helper), 0o700))
	f.key = "project:w1/" + slug
	writeD14Permission(t, f, permission)
	f.ctx = WithAgentID(context.Background(), "mia")
	f.ctx = WithWorkspaceID(f.ctx, "w1")
	f.ctx = WithTurnWorkspaceDir(f.ctx, f.work)
	f.ctx = WithTranscriptSessionID(f.ctx, "d14-native")
	f.ctx = WithTurnID(f.ctx, "d14-turn-one")
	policy, err := ResolveTurnFSPolicy(f.ctx, f.agentHome, true)
	require.NoError(t, err)
	require.True(t, slices.Contains(policy.AllowedRoots, f.mount), "native policy actually observes the persisted mount")
	return f
}

func writeD14Permission(t *testing.T, f d14NativeFixture, permission string) {
	t.Helper()
	path := filepath.Join(f.home, "config.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	sb := document["sandbox"].(map[string]any)
	sb["skill_policies"] = map[string]string{f.key: permission}
	sb["skill_trust"] = "allow" // Isolate raw permission; project files remain unverified.
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}

// read_file's documented plain-text carrier is a metadata header then a blank
// line and unmodified bytes. Strip only that header, never the sentinel/content.
func assertD14Read(t *testing.T, result *ToolResult, literal string) {
	t.Helper()
	require.False(t, result.IsError, "actual file read must succeed: %s", result.ForLLM)
	_, body, ok := strings.Cut(result.ForLLM, "\n\n")
	require.True(t, ok, "actual read_file header/body carrier")
	assert.Equal(t, literal, body, "all permitted bytes, not just OK or absence of the instruction sentinel")
}

func assertD14Denied(t *testing.T, result *ToolResult, tool, exactReason string) {
	t.Helper()
	assert.True(t, result.IsError, "native action must be refused, not return content: %s", result.ForLLM)
	var refusal gen.PermissionDenied
	if err := json.Unmarshal([]byte(result.ForLLM), &refusal); err != nil {
		t.Errorf("native refusal must use the actual PermissionDenied carrier, got %q: %v", result.ForLLM, err)
		return
	}
	assert.Equal(t, "permission_denied", refusal.Error)
	assert.Equal(t, tool, refusal.Tool)
	assert.True(t, refusal.Permanent)
	if exactReason != "" {
		assert.Contains(t, []string{refusal.Reason, refusal.Message}, exactReason, "C-25 exact Ask sentence")
	} else {
		assert.Contains(t, refusal.Reason+"\n"+refusal.Message, "Deny", "D14 Deny refusal names the setting")
	}
	assert.NotContains(t, result.ForLLM, "GATE-SENTINEL")
}

func d14Quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func assertD14HelperRefused(t *testing.T, f d14NativeFixture) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("UNVERIFIED: this Unix helper fixture does not certify Windows; independent ST-28 lane required")
	}
	// Check the instrument first: these exact helper bytes are executable and
	// create the exact effect before the app guard is asked to refuse them.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/bin/sh", f.helper).CombinedOutput()
	require.NoError(t, ctx.Err(), "fixture deadline is not a permission result")
	require.NoError(t, err, "fixture helper is executable: %s", output)
	require.Equal(t, d14HelperEffect, string(output))
	data, err := os.ReadFile(f.effect)
	require.NoError(t, err)
	require.Equal(t, d14HelperEffect, string(data))
	require.NoError(t, os.Remove(f.effect))
	tool, err := NewExecTool(f.agentHome, true)
	require.NoError(t, err)
	result := tool.Execute(f.ctx, map[string]any{"command": "/bin/sh " + d14Quote(f.helper), "timeout_seconds": float64(5)})
	assert.True(t, result.IsError, "C-25: actual helper execution is denied: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "Deny", "not an unrelated shell/fixture error")
	assert.NotContains(t, result.ForLLM, strings.TrimSpace(d14HelperEffect))
	_, err = os.Stat(f.effect)
	assert.ErrorIs(t, err, os.ErrNotExist, "a denied native helper creates no real effect")
}

func TestProjectSkillPathGate(t *testing.T) {
	t.Run("instrument allowed project folder grep actually sees skill bytes and helper", func(t *testing.T) {
		f := seedGateFixture(t, ".omnipus", "notes", "allow", "deny", false)
		result := NewGrepTool(f.agentHome, true).Execute(f.ctx, map[string]any{"path": filepath.Dir(f.skill), "pattern": "GATE-"})
		require.False(t, result.IsError, "real scoped grep can see this project folder: %s", result.ForLLM)
		require.Contains(t, result.ForLLM, "GATE-SENTINEL notes instructions.")
		require.Contains(t, result.ForLLM, "helper.sh")
	})
	rows := []struct {
		row                                              int
		name, shelf, slug, permission, editSkill, action string
		nested                                           bool
	}{
		{1, "Deny read names setting", ".claude", "notes", "deny", "deny", "read-denied", false},
		{2, "Ask read exact instruction", ".claude", "notes", "ask", "deny", "read-ask", false},
		{3, "Allow read exact bytes", ".claude", "notes", "allow", "deny", "read-allowed", false},
		{4, "grep excludes denied content but sees README", ".omnipus", "notes", "deny", "deny", "grep", false},
		{5, "listing hides notes but keeps ordinary sibling", ".omnipus", "notes", "deny", "deny", "list", false},
		{6, "helper actual effect denied", ".omnipus", "notes", "deny", "deny", "helper", false},
		{7, "real symlink alias same canonical identity", ".omnipus", "notes", "deny", "deny", "alias", false},
		{8, "workdir own-tree does not reopen Deny", ".omnipus", "notes", "deny", "deny", "workdir", false},
		{9, "actual nested mount reached through outer", ".claude", "x", "deny", "deny", "nested", true},
		{10, "editor reads sentinel but cannot run helper", ".claude", "notes", "deny", "allow", "editor", false},
		{11, "raw permission write next turn", ".claude", "notes", "deny", "deny", "next-turn", false},
	}
	require.Len(t, rows, 11)
	for _, row := range rows {
		t.Run(fmt.Sprintf("D14#%02d %s", row.row, row.name), func(t *testing.T) {
			f := seedGateFixture(t, row.shelf, row.slug, row.permission, row.editSkill, row.nested)
			reader := NewReadFileTool(f.agentHome, true, 1<<20)
			// S-111 exact-byte allow control executes in EVERY row before a failure.
			assertD14Read(t, reader.Execute(f.ctx, map[string]any{"path": f.readme}), d14Readme)
			switch row.action {
			case "read-allowed", "editor":
				assertD14Read(t, reader.Execute(f.ctx, map[string]any{"path": f.skill}), d14Instructions)
				if row.action == "editor" {
					assertD14HelperRefused(t, f)
				}
			case "read-denied", "nested":
				assertD14Denied(t, reader.Execute(f.ctx, map[string]any{"path": f.skill}), "read_file", "")
			case "read-ask":
				assertD14Denied(t, reader.Execute(f.ctx, map[string]any{"path": f.skill}), "read_file", gateAskPendingText)
			case "grep":
				result := NewGrepTool(f.agentHome, true).Execute(f.ctx, map[string]any{"path": f.mount, "pattern": "GATE-"})
				require.False(t, result.IsError, "search actually runs: %s", result.ForLLM)
				assert.Contains(t, result.ForLLM, strings.TrimSpace(d14Readme), "positive hit from the ordinary file")
				assert.NotContains(t, result.ForLLM, "GATE-SENTINEL")
				assert.NotContains(t, result.ForLLM, "helper.sh", "no filename or content from the denied folder")
				// The broad W1 walk prunes dot folders even without a skill gate.
				// Probe the exact same scoped path the independent Allow instrument
				// can read, so ordinary dot pruning cannot manufacture this green.
				scoped := NewGrepTool(f.agentHome, true).Execute(f.ctx, map[string]any{"path": filepath.Dir(f.skill), "pattern": "GATE-"})
				if scoped.IsError {
					assert.Contains(t, scoped.ForLLM, "Deny", "only the setting's refusal, not an unrelated search error")
				}
				assert.NotContains(t, scoped.ForLLM, "GATE-SENTINEL", "same instrument sees literal bytes under Allow, never under Deny")
				assert.NotContains(t, scoped.ForLLM, "helper.sh")
			case "list":
				parent := filepath.Dir(filepath.Dir(f.skill))
				require.NoError(t, os.MkdirAll(filepath.Join(parent, "ordinary-folder"), 0o700))
				result := NewListDirTool(f.agentHome, true).Execute(f.ctx, map[string]any{"path": parent})
				require.False(t, result.IsError, "list the parent, not an unrelated refusal: %s", result.ForLLM)
				assert.Contains(t, result.ForLLM, "ordinary-folder")
				assert.NotContains(t, result.ForLLM, "notes")
			case "helper":
				assertD14HelperRefused(t, f)
			case "alias":
				alias := filepath.Join(f.work, "innocuous-alias.md")
				require.NoError(t, os.Symlink(f.skill, alias))
				real, err := filepath.EvalSymlinks(alias)
				require.NoError(t, err)
				require.Equal(t, f.skill, real)
				assertD14Denied(t, reader.Execute(f.ctx, map[string]any{"path": alias}), "read_file", "")
			case "workdir":
				ctx := WithTurnWorkspaceDir(f.ctx, f.mount)
				policy, err := ResolveTurnFSPolicy(ctx, f.agentHome, true)
				require.NoError(t, err)
				require.Equal(t, f.mount, policy.WorkDir)
				assertD14Denied(t, reader.Execute(ctx, map[string]any{"path": f.skill}), "read_file", "")
			case "next-turn":
				policy, err := ResolveTurnFSPolicy(f.ctx, f.agentHome, true)
				require.NoError(t, err)
				assertD14Denied(t, reader.Execute(f.ctx, map[string]any{"path": f.skill}), "read_file", "")
				writeD14Permission(t, f, "allow")
				old, oldErr := ResolvePath(f.ctx, policy, "read_file", "d14-old-turn", FSOpRead, f.skill)
				assert.Error(t, oldErr, "the existing turn policy is not widened by the later write")
				if old != nil {
					require.NoError(t, old.Close())
				}
				next := WithTurnID(f.ctx, "d14-turn-two")
				assertD14Read(t, reader.Execute(next, map[string]any{"path": f.skill}), d14Instructions)
			}
		})
	}
}
