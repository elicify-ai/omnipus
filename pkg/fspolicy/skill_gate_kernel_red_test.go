//go:build goolm && stdjson

package fspolicy_test

// Order 50 / CHECK F17: C-25, S-111, D14#12–13. These are real experiments,
// not callbacks reporting a verdict. A persisted workspace/mount and raw Deny
// feed tools.ResolveTurnFSPolicy; only its real result feeds kernel derivation.
// Irreversible Linux state is confined to a re-executed test subprocess.
// An ordinary file is the allow control; a protected fake master.key is the
// kernel-deny instrument control. No app guard mediates the native cat/helper.
// Unsupported native/platform paths are UNVERIFIED and require hands-on ST-28;
// no successful policy rendering or setup receipt counts as kernel acceptance.
// The app-only experiment uses real tools without an installed boot backend.
// GREEN, mutation and supported-platform certification are deferred to CHECK.

import (
	"bytes"
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
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const projectGateBody = "---\nname: notes\ndescription: Project notes.\n---\n\nPROJECT-GATE-INSTRUCTIONS-9Z\n"
const projectGateControl = "PROJECT-GATE-ORDINARY-9Z\n"

type projectGateFixture struct {
	ctx                                context.Context
	home, work, mount, skill, helper   string
	effect, ordinary, protectedControl string
}

func newProjectGateFixture(t *testing.T) projectGateFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home := filepath.Join(root, "home")
	mount := filepath.Join(root, "mounted-project") // Outside OMNIPUS_HOME.
	skill := filepath.Join(mount, ".claude", "skills", "notes")
	require.NoError(t, os.MkdirAll(skill, 0o700))
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv(config.EnvHome, home)
	mia := config.AgentConfig{ID: "mia", Tools: &config.AgentToolsCfg{}}
	mia.Tools.Builtin.Policies = map[string]config.ToolPolicy{"edit_skill": config.ToolPolicyDeny}
	require.NoError(t, agentstore.New(home).Create("mia", &mia))
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{mia}
	cfg.Sandbox.ToolPolicies["edit_skill"] = "deny"
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	// Literal new-spec input persists even before its Go fields exist. It is
	// consumed only by production code, never translated to a test verdict.
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	sb := document["sandbox"].(map[string]any)
	sb["skill_policies"] = map[string]string{"project:w1/notes": "deny"}
	sb["skill_trust"] = "allow" // Isolate permission from the independent trust check.
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: "w1", Name: "Project gate fixture", Status: "active", CoreTeam: []string{"mia"},
		CreatedAt: "2026-11-22T00:00:00Z", UpdatedAt: "2026-11-22T00:00:00Z",
	}))
	work, err := workspace.EnsureWorkDir(home, "w1")
	require.NoError(t, err)
	_, warning, err := workspace.CreateMount(home, "w1", "project", mount)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, []string{mount}, workspace.AllowedMountRoots(home, "w1"), "fixture: a real protected mount record, not an agent Home binding")
	f := projectGateFixture{home: home, work: work, mount: mount,
		skill: filepath.Join(skill, "SKILL.md"), helper: filepath.Join(skill, "helper.sh"),
		effect: filepath.Join(work, "helper-effect"), ordinary: filepath.Join(mount, "README.md"),
		protectedControl: filepath.Join(home, "master.key")}
	require.NoError(t, os.WriteFile(f.skill, []byte(projectGateBody), 0o600))
	require.NoError(t, os.WriteFile(f.ordinary, []byte(projectGateControl), 0o600))
	require.NoError(t, os.WriteFile(f.protectedControl, []byte("FAKE-PROTECTED-CONTROL-9Z\n"), 0o600))
	require.NoError(t, os.WriteFile(f.helper, []byte("#!/bin/sh\nprintf 'PROJECT-HELPER-RAN-9Z\\n' > "+projectGateQuote(f.effect)+"\nprintf 'PROJECT-HELPER-RAN-9Z\\n'\n"), 0o700))
	f.ctx = tools.WithAgentID(context.Background(), "mia")
	f.ctx = tools.WithWorkspaceID(f.ctx, "w1")
	f.ctx = tools.WithTurnWorkspaceDir(f.ctx, work)
	f.ctx = tools.WithTranscriptSessionID(f.ctx, "project-gate-session")
	f.ctx = tools.WithTurnID(f.ctx, "project-gate-turn-one")
	return f
}

func projectGateQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestProjectSkillPathGate_Kernel(t *testing.T) {
	if slices.Contains(os.Args, "--project-gate-native") {
		projectGateNativeExperiment(t)
		return
	}
	if slices.Contains(os.Args, "--project-gate-app-only") {
		projectGateAppOnlyExperiment(t)
		return
	}
	for _, mode := range []string{"native", "app-only"} {
		t.Run(mode, func(t *testing.T) {
			executable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProjectSkillPathGate_Kernel$", "-test.v", "--", "--project-gate-"+mode)
			cmd.Env = sandbox.ScrubGatewayEnv()
			out, err := cmd.CombinedOutput()
			t.Logf("isolated %s experiment:\n%s", mode, out)
			require.NoError(t, ctx.Err(), "a deadline is a fixture failure, never proof of refusal")
			require.NoError(t, err, "D14 real %s experiment failed; inspect the child assertions, not a returned boolean", mode)
		})
	}
}

func projectGateNativeExperiment(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Fatal("UNVERIFIED: this native experiment requires enforcing Linux; macOS and Windows require their independent ST-28 platform lane")
	}
	f := newProjectGateFixture(t)
	// Before restriction the OS really can read the fixture and run its helper.
	raw, err := os.ReadFile(f.skill)
	require.NoError(t, err)
	require.Equal(t, projectGateBody, string(raw))
	out, err := exec.Command("/bin/sh", f.helper).CombinedOutput()
	require.NoError(t, err, "fixture: helper is executable before confinement: %s", out)
	require.Equal(t, "PROJECT-HELPER-RAN-9Z\n", string(out))
	require.NoError(t, os.Remove(f.effect))
	policy, err := tools.ResolveTurnFSPolicy(f.ctx, f.work, true)
	require.NoError(t, err)
	require.Equal(t, []string{f.mount}, policy.AllowedRoots)
	kernel := sandbox.DeriveKernelPolicy(policy, sandbox.TurnPolicyInput{HomePath: f.home, Model: sandbox.FilesystemModelOpen})
	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		t.Fatalf("UNVERIFIED: enforcing Linux backend unavailable (%s); ST-28 must establish native enforcement", name)
	}
	runtime.LockOSThread() // No unlock: the restricted thread dies with this subprocess.
	require.NoError(t, backend.Apply(sandbox.DefaultPolicyForModel(sandbox.FilesystemModelOpen, f.home, nil, nil, nil, nil)), "UNVERIFIED: native boot Apply failed; ST-28 required")
	require.True(t, sandbox.DescribeBackend(backend).PolicyApplied, "fixture: real Apply occurred")
	cat, err := exec.LookPath("cat")
	require.NoError(t, err)
	run := func(argv ...string) sandbox.Result {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, runErr := sandbox.Run(ctx, argv, append(sandbox.ScrubGatewayEnv(), "LC_ALL=C"), sandbox.Limits{WorkspaceDir: f.work, KernelPolicy: &kernel})
		require.NoError(t, runErr, "native spawn itself must succeed: %v", argv)
		require.False(t, result.TimedOut, "a timeout is not a permission refusal")
		return result
	}
	control := run(cat, f.ordinary)
	require.Zero(t, control.ExitCode, "instrument: ordinary mount file remains readable: %s", control.Stderr)
	require.Equal(t, projectGateControl, string(control.Stdout))
	secret := run(cat, f.protectedControl)
	require.NotZero(t, secret.ExitCode, "instrument: existing protected-file deny must really enforce")
	require.Empty(t, secret.Stdout)
	require.Contains(t, string(secret.Stderr), "Permission denied")
	t.Logf("KERNEL_INSTRUMENT: backend=%s ordinary=readable protected=permission-denied", name)
	read := run(cat, f.skill) // No ExecTool/path-text check: only the actual kernel can deny.
	assert.NotZero(t, read.ExitCode, "D14#12: native cat must be denied, not leak the real project instructions")
	assert.Empty(t, read.Stdout, "D14#12: no bytes from gated SKILL.md")
	assert.Contains(t, string(read.Stderr), "Permission denied")
	helper := run("/bin/sh", f.helper)
	assert.NotZero(t, helper.ExitCode, "C-25: native shell must not read/execute the denied helper")
	assert.Empty(t, helper.Stdout)
	assert.Contains(t, string(helper.Stderr), "Permission denied")
	_, err = os.Stat(f.effect)
	assert.ErrorIs(t, err, os.ErrNotExist, "a denied helper produces no real effect")
}

func projectGateAppOnlyExperiment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Fatal("UNVERIFIED: the Unix shell fixture does not certify Windows; ST-28 must drive its actual app-only file/helper paths")
	}
	f := newProjectGateFixture(t)
	require.False(t, sandbox.TurnPolicyBaseInstalled(), "fixture: this isolated app-only process installed no kernel policy")
	logPath := filepath.Join(t.TempDir(), "app-only.jsonl")
	require.NoError(t, logger.EnableFileLogging(logPath))
	t.Cleanup(logger.DisableFileLogging)
	logger.WarnCF("project-gate-test", "log capture instrument", map[string]any{"instrument": "PROJECT-GATE-LOG-9Z"})
	tool, err := tools.NewExecTool(f.work, true)
	require.NoError(t, err)
	control := tool.Execute(f.ctx, map[string]any{"command": "cat " + projectGateQuote(f.ordinary), "timeout_seconds": float64(5)})
	require.False(t, control.IsError, "instrument: ordinary app-only shell read works: %s", control.ForLLM)
	require.Contains(t, control.ForLLM, strings.TrimSpace(projectGateControl))
	for _, turn := range []string{"project-gate-turn-one", "project-gate-turn-two"} {
		ctx := tools.WithTurnID(f.ctx, turn)
		for repeat := 0; repeat < 2; repeat++ {
			read := tools.NewReadFileTool(f.work, true, 1<<20).Execute(ctx, map[string]any{"path": f.skill})
			assert.True(t, read.IsError, "C-25: real file tool refuses project Deny: %s", read.ForLLM)
			assert.Contains(t, read.ForLLM, "Deny", "refusal names the setting")
			assert.NotContains(t, read.ForLLM, "PROJECT-GATE-INSTRUCTIONS-9Z")
			for _, command := range []string{"cat " + projectGateQuote(f.skill), "/bin/sh " + projectGateQuote(f.helper)} {
				result := tool.Execute(ctx, map[string]any{"command": command, "timeout_seconds": float64(5)})
				assert.True(t, result.IsError, "D14#13: visible operand is refused by the real app layer: %s", result.ForLLM)
				assert.Contains(t, result.ForLLM, "Deny", "the refusal must identify permission, not an unrelated shell error")
				assert.NotContains(t, result.ForLLM, "PROJECT-GATE-INSTRUCTIONS-9Z")
				assert.NotContains(t, result.ForLLM, "PROJECT-HELPER-RAN-9Z")
			}
		}
		_, err := os.Stat(f.effect)
		assert.ErrorIs(t, err, os.ErrNotExist, "a refused helper never writes its effect")
		raw, err := os.ReadFile(logPath)
		require.NoError(t, err)
		warnings, instrument := 0, 0
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
			var entry map[string]any
			require.NoError(t, json.Unmarshal(line, &entry))
			if entry["instrument"] == "PROJECT-GATE-LOG-9Z" && entry["level"] == "warn" {
				instrument++
			}
			if entry["level"] == "warn" && strings.Contains(string(line), "app-level") && strings.Contains(string(line), filepath.Dir(f.skill)) {
				warnings++
			}
		}
		require.Equal(t, 1, instrument, "the logger check must demonstrably see a warning")
		want := 1
		if turn == "project-gate-turn-two" {
			want = 2
		}
		assert.Equal(t, want, warnings, fmt.Sprintf("C-25: exactly one app-level-only warning per turn naming the gated folder; %s", turn))
	}
}
