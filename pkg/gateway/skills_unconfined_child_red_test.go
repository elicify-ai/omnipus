package gateway

// r15 §6.3/§9.4, FR-015/035/061; S-2.11/S-12.1. Runs on all
// supported platforms, including native Windows. A fresh inner process
// prevents clearing a turn-policy base from pretending to undo Landlock.
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillsNoKernel_ActualChildWriteThenLoadDetection(t *testing.T) {
	if slices.Contains(os.Args, "--skills-unconfined-probe") {
		home := os.Getenv("OMNIPUS_CHILD_SKILLS_UNCONFINED_HOME")
		if home == "" {
			fmt.Fprintln(os.Stderr, "missing fixture home")
			os.Exit(90)
		}
		if err := os.WriteFile(filepath.Join(home, "skills", "foo", "SKILL.md"), []byte(crashSkillMD("CHILD-OUTSIDE-CHANGE")), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(91)
		}
		fmt.Println("UNCONFINED_CHILD_WRITE_OK")
		os.Exit(0)
	}
	if !slices.Contains(os.Args, "--skills-unconfined-inner") {
		for _, mode := range []string{"off", "enforce", "permissive"} {
			t.Run(mode, func(t *testing.T) {
				exe, err := os.Executable()
				require.NoError(t, err)
				cmd := exec.Command(exe, "-test.run=^TestSkillsNoKernel_ActualChildWriteThenLoadDetection$", "-test.v", "--", "--skills-unconfined-inner")
				cmd.Env = append(sandbox.ScrubGatewayEnv(), "OMNIPUS_CHILD_SKILLS_UNCONFINED_MODE="+mode)
				out, err := cmd.CombinedOutput()
				t.Logf("isolated no-kernel child receipt (mode=%s):\n%s", mode, out)
				require.NoError(t, err, "no-kernel behavior must be demonstrated on the native OS; prerequisite failures are not child proof")
			})
		}
		return
	}
	mode := os.Getenv("OMNIPUS_CHILD_SKILLS_UNCONFINED_MODE")
	require.Contains(t, []string{"off", "enforce", "permissive"}, mode)
	home, cfg := bootFixture(t)
	dir := filepath.Join(home, "skills", "foo")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(crashSkillMD("DESCRIPTION-A")), 0o644))
	const generation = "11111111-2222-3333-4444-555555555555"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte(generation+"\n"), 0o644))
	// This row concerns an explicitly authorized child, not Auto's headless
	// approval refusal. Persist the real two-layer policy before actual boot;
	// do not inject a settled-approval marker or replace the bash tool.
	cfg.Sandbox.ToolPolicies["bash"] = string(config.ToolPolicyAllow)
	updated, err := agentstore.New(home).Update("agent-a", func(a *config.AgentConfig) error {
		a.Skills = []string{"foo"}
		if a.Tools == nil {
			a.Tools = &config.AgentToolsCfg{}
		}
		if a.Tools.Builtin.Policies == nil {
			a.Tools.Builtin.Policies = map[string]config.ToolPolicy{}
		}
		a.Tools.Builtin.Policies["bash"] = config.ToolPolicyAllow
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, generation, updated.SkillGrants["foo"], "real grant persistence binds the observed folder")
	cfg.Agents.List[0] = *updated
	cfg.Sandbox.Mode = config.SandboxMode(mode)
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	rc, inst, _ := crashBoot(t, home)
	require.Equal(t, string(config.ToolPolicyAllow), rc.agentLoop.ResolveApprovalToolPolicy(inst.ID, "bash"),
		"the real booted shell policy must authorize this child independently of kernel mode")
	applied, err := applySandbox(SandboxApplyOptions{Cfg: rc.cfg, HomePath: home, Backend: sandbox.NewFallbackBackend(), GetEnv: func(string) string { return "" }})
	require.NoError(t, err)
	require.Equal(t, sandbox.Mode(mode), applied.Mode, "actual off/fallback boot, not a caller-made unconfined policy")
	require.False(t, sandbox.TurnPolicyBaseInstalled(), "a genuine no-kernel mode installs no per-turn kernel overlay")
	meta, err := rc.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", inst.ID)
	require.NoError(t, err)
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), inst.ID), meta.ID)
	first := crashActualSkill(t, inst, ctx)
	require.False(t, first.IsError)
	require.Contains(t, first.ForLLM, `revision="`, "BLOCKED: actual initial load/detection not implemented — r15 §9.4")
	before := len(crashVersionRows(t, home))
	require.Positive(t, before, "the initial real load established a baseline, not fabricated history")
	exe, err := os.Executable()
	require.NoError(t, err)
	// Keep the ordinary executable-path guard enabled. Authorize execution by
	// placing the real native child inside the agent's existing working dir.
	workDir, err := agentWorkspacePath(rc.cfg, inst.ID, "", home)
	require.NoError(t, err)
	childPath := filepath.Join(workDir, filepath.Base(exe))
	nativeChild, err := os.ReadFile(exe)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(childPath, nativeChild, 0o700))
	t.Setenv("OMNIPUS_CHILD_SKILLS_UNCONFINED_HOME", home)
	quoted := "'" + strings.ReplaceAll(childPath, "'", "'\\''") + "'"
	if runtime.GOOS == "windows" {
		quoted = "& '" + strings.ReplaceAll(childPath, "'", "''") + "'"
	} // actual bash tool uses PowerShell on Windows
	command := quoted + " -test.run=^TestSkillsNoKernel_ActualChildWriteThenLoadDetection$ -- --skills-unconfined-probe"
	bash, ok := inst.Tools.Get("bash")
	require.True(t, ok, "actual startup registers the child execution tool")
	res := bash.Execute(ctx, map[string]any{"command": command, "timeout_seconds": float64(20)})
	require.False(t, res.IsError, "no-kernel child is allowed to execute: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "UNCONFINED_CHILD_WRITE_OK", "actual child wrote through the documented no-kernel boundary")
	body, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, crashSkillMD("CHILD-OUTSIDE-CHANGE"), string(body))
	next := crashActualSkill(t, inst, ctx)
	require.False(t, next.IsError)
	assert.Contains(t, next.ForLLM, "Body CHILD-OUTSIDE-CHANGE.")
	assert.Contains(t, next.ForLLM, "changed outside Omnipus")
	rows := crashVersionRows(t, home)
	require.Len(t, rows, before+1, "next actual Skill records exactly one outside change")
	var outside []map[string]any
	for _, row := range rows {
		if row["kind"] == "external_change" {
			outside = append(outside, row)
		}
	}
	require.Len(t, outside, 1)
	assert.Equal(t, "unknown", outside[0]["actor"], "§5.11/§9.4: outside script is never attributed as a managed edit")
}
