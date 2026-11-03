//go:build (linux || darwin) && !nogodmode

package tools

// r15 §6.2/§6.3, FR-012/015, S-2.5/S-2.6; CHECK-08.
// Native probe bypasses command-text guards by constructing paths itself.
// Both modes use the real ExecTool; no skill roots are supplied by the test.
// Irreversible backend state lives only in the serial inner subprocess.
import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillsKernel_ActualTurnChildrenOrdinaryGodAndEnclosingMount(t *testing.T) {
	if slices.Contains(os.Args, "--skills-native-probe") {
		skillsNativeChildProbe()
		return
	}
	if !slices.Contains(os.Args, "--skills-kernel-inner") {
		exe, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(exe, "-test.run=^TestSkillsKernel_ActualTurnChildrenOrdinaryGodAndEnclosingMount$", "-test.v", "--", "--skills-kernel-inner")
		cmd.Env = sandbox.ScrubGatewayEnv()
		out, err := cmd.CombinedOutput()
		t.Logf("isolated native-kernel receipt:\n%s", out)
		require.NoError(t, err, "actual native child matrix is RED; inner output above distinguishes missing backend from missing skill grants")
		return
	}
	ctx, home, agentHome := skillsTurnFixture(t, false)
	// S-2.5: every installed ordinary skill is loadable. An ungranted sibling
	// must not accidentally mask a buggy whole-registry grant optimization.
	require.NoError(t, os.RemoveAll(filepath.Join(home, "skills", "other")))
	const wsID = "skills-kernel"
	seedWorkspaceRecord(t, home, wsID)
	work, err := workspace.EnsureWorkDir(home, wsID)
	require.NoError(t, err)
	ctx = WithTurnWorkspaceDir(WithWorkspaceID(ctx, wsID), work)
	outside := filepath.Join(t.TempDir(), "god-control.txt")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "skills", ".staging"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "skills", ".staging", "private"), []byte("CONTROL-PRIVATE\n"), 0o600))
	dir := filepath.Join(home, "skills", "granted")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\nprintf 'SKILL_EXEC_OK\\n'\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".inside"), []byte("DOT_INSIDE_OK\n"), 0o644))
	t.Setenv("OMNIPUS_CHILD_SKILLS_HOME", home)
	t.Setenv("OMNIPUS_CHILD_SKILLS_WORK", work)
	t.Setenv("OMNIPUS_CHILD_SKILLS_OUTSIDE", outside)
	exe, err := os.Executable()
	require.NoError(t, err)
	available, reason := hostKernelSandboxOracle() // independent OS oracle, not product detection
	require.True(t, available, "BLOCKED: native kernel instrument unavailable: %s — r15 §6.2 requires kernel evidence, never a skip", reason)
	skillsInstallNativeBackend(t, home)
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen})
	command := "'" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestSkillsKernel_ActualTurnChildrenOrdinaryGodAndEnclosingMount$ -- --skills-native-probe"
	for _, mount := range []bool{false, true} {
		if mount {
			_, warning, mountErr := workspace.CreateMount(home, wsID, "enclosing", filepath.Dir(home))
			require.NoError(t, mountErr)
			require.NotEmpty(t, warning, "the actual persisted mount encloses Omnipus home")
			require.Contains(t, workspace.AllowedMountRoots(home, wsID), filepath.Dir(home))
		}
		for _, god := range []bool{false, true} {
			t.Run(fmt.Sprintf("mount-%t/god-%t", mount, god), func(t *testing.T) {
				t.Setenv("OMNIPUS_CHILD_SKILLS_GOD", fmt.Sprint(god))
				tool, err := NewExecToolWithDeps(agentHome, false, nil, ExecToolDeps{GodMode: god})
				require.NoError(t, err)
				res := tool.ExecuteAsync(ctx, map[string]any{"command": command, "timeout_seconds": float64(20)}, nil)
				require.NotNil(t, res)
				assert.False(t, res.IsError, "native tool dispatch must finish successfully: %s", res.ForLLM)
				for _, marker := range []string{"WORK_WRITE_OK", "SKILL_READ_OK", "DOT_INSIDE_OK", "SKILL_EXEC_OK", "DENIED=skill-write", "DENIED=registry-mkdir", "DENIED=control-read"} {
					assert.Contains(t, res.ForLLM, marker, "actual %s child, ordinary/God=%t, mount=%t", runtime.GOOS, god, mount)
				}
				if god {
					assert.Contains(t, res.ForLLM, "GOD_OUTSIDE_WRITE_OK", "positive control: God remains writable outside, without lifting the skills boundary")
				}
				assert.NotContains(t, res.ForLLM, "UNEXPECTED=")
			})
		}
	}
}

func skillsNativeChildProbe() {
	home, work := os.Getenv("OMNIPUS_CHILD_SKILLS_HOME"), os.Getenv("OMNIPUS_CHILD_SKILLS_WORK")
	if home == "" || work == "" {
		fmt.Println("UNEXPECTED=missing-fixture-env")
		os.Exit(91)
	}
	if err := os.WriteFile(filepath.Join(work, "probe.txt"), []byte("WORK_WRITE_OK\n"), 0o600); err != nil {
		fmt.Printf("UNEXPECTED=workspace-write:%v\n", err)
	} else {
		fmt.Println("WORK_WRITE_OK")
	}
	dir := filepath.Join(home, "skills", "granted")
	if b, err := os.ReadFile(filepath.Join(dir, "helper.md")); err == nil && string(b) == "MARKER-granted\n" {
		fmt.Println("SKILL_READ_OK")
	} else {
		fmt.Printf("UNEXPECTED=skill-read:%v\n", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".inside")); err == nil && string(b) == "DOT_INSIDE_OK\n" {
		fmt.Print(string(b))
	} else {
		fmt.Printf("UNEXPECTED=dot-read:%v\n", err)
	}
	if b, err := exec.Command(filepath.Join(dir, "run.sh")).CombinedOutput(); err == nil && string(b) == "SKILL_EXEC_OK\n" {
		fmt.Print(string(b))
	} else {
		fmt.Printf("UNEXPECTED=skill-execute:%v:%s\n", err, b)
	}
	if os.Getenv("OMNIPUS_CHILD_SKILLS_GOD") == "true" {
		if err := os.WriteFile(os.Getenv("OMNIPUS_CHILD_SKILLS_OUTSIDE"), []byte("god control"), 0o600); err != nil {
			fmt.Printf("UNEXPECTED=god-outside-write:%v\n", err)
		} else {
			fmt.Println("GOD_OUTSIDE_WRITE_OK")
		}
	}
	f, writeErr := os.OpenFile(filepath.Join(dir, "helper.md"), os.O_WRONLY|os.O_APPEND, 0)
	if f != nil {
		_ = f.Close()
	}
	for _, probe := range []struct {
		name string
		err  error
	}{
		{"skill-write", writeErr},
		{"registry-mkdir", os.Mkdir(filepath.Join(home, "skills", "new"), 0o755)},
		{"control-read", func() error { _, err := os.ReadFile(filepath.Join(home, "skills", ".staging", "private")); return err }()},
	} {
		if errors.Is(probe.err, os.ErrPermission) {
			fmt.Println("DENIED=" + probe.name)
		} else {
			fmt.Printf("UNEXPECTED=%s:%v\n", probe.name, probe.err)
		}
	}
	os.Exit(0)
}

func TestSkillsKernel_RealOrdinaryTurnRulesAreReadExecuteOnly(t *testing.T) {
	ctx, home, agentHome := skillsTurnFixture(t, false)
	old, present := sandbox.RegisteredTurnPolicyBase()
	t.Cleanup(func() {
		if present {
			sandbox.RegisterTurnPolicyBase(&old)
		} else {
			sandbox.RegisterTurnPolicyBase(nil)
		}
	})
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen})
	tool, err := NewExecToolWithDeps(agentHome, false, nil, ExecToolDeps{})
	require.NoError(t, err)
	policy, err := tool.turnKernelPolicy(ctx, agentHome, environmentsetup.RuntimeEnv{}, documentEnvLayer{}, nil)
	require.NoError(t, err)
	require.NotNil(t, policy)
	root, dir := filepath.Join(home, "skills"), filepath.Join(home, "skills", "granted")
	assert.Contains(t, policy.WriteDeniedPaths, root, "§6.2 explicit subtree write deny must reach the actual turn")
	assert.Contains(t, policy.AllowedBranches, sandbox.PathRule{Path: dir, Access: sandbox.AccessRead | sandbox.AccessExecute}, "actual turn wiring, not caller-provided allowances")
	rules, err := sandbox.ExpandRulesExcluding(policy.FilesystemRules, policy.DeniedPaths, policy.DeniedNodes, policy.DeniedPathPrefixes, policy.AllowedBranches)
	require.NoError(t, err)
	for _, rule := range rules {
		assert.NotEqual(t, root, rule.Path, "final Linux expansion grants no wholesale registry root")
		assert.NotEqual(t, home, rule.Path, "final Linux expansion grants no wholesale home root")
		if rule.Path == root || strings.HasPrefix(rule.Path, root+string(filepath.Separator)) {
			assert.Zero(t, rule.Access&sandbox.AccessWrite, "final skill branch has no write bit: %+v", rule)
		}
	}
}
