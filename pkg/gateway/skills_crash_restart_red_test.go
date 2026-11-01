package gateway

// r15 §7.6/§9.3/§9.4, FR-022/033/034/035/036; CHECK-06.
// Actual process exit at a verified publication boundary (not panic/recover),
// actual gateway restart, first menu, session state, detection and audit bytes.
// Other matrix stages are not implied by these three named crash boundaries.
import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const skillsCrashExit = 73

func crashSkillMD(description string) string {
	return "---\nname: foo\ndescription: " + description + "\n---\nBody " + description + ".\n"
}

func crashBoot(t *testing.T, home string, transport ...providers.LLMProvider) (*runContextWithOptions, *agent.AgentInstance, *systools.Deps) {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("0", 64)) // synthetic fixture only
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no HTTP server; optional provisioning receives cancellation, not a readiness claim
	rc := &runContextWithOptions{ctx: ctx, homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	if rc.credStore != nil {
		t.Cleanup(rc.credStore.Close)
	}
	require.NoError(t, err, "real credential/config/roster startup")
	require.False(t, stop)
	rc.provider = &restMockProvider{} // only model transport; no paid model request
	require.LessOrEqual(t, len(transport), 1, "one model-transport edge per actual boot")
	if len(transport) == 1 {
		require.NotNil(t, transport[0])
		rc.provider = transport[0]
	}
	err, stop = rc.initializeAgentLoop()
	if rc.agentLoop != nil {
		t.Cleanup(rc.agentLoop.Close)
	}
	if rc.liveLimits != nil {
		t.Cleanup(rc.liveLimits.Close)
		t.Cleanup(func() { agent.SetLiveWindowLookup(nil) })
	}
	require.NoError(t, err)
	require.False(t, stop)
	inst, ok := rc.agentLoop.GetRegistry().GetAgent("agent-a")
	require.True(t, ok)
	require.NotNil(t, rc.agentLoop.GetSessionStore())
	root := filepath.Join(home, "skills")
	deps := &systools.Deps{Home: home, GetCfg: rc.agentLoop.GetConfig,
		SkillsLoader: skills.NewSkillsLoader("", root, ""), SkillWriter: skills.NewSkillWriter(root),
		DeleteSessionStore: func() systools.AgentSessionStore { return rc.agentLoop.GetSessionStore() },
		ResolveToolPolicy:  func(id, _ string) (string, bool) { return "allow", id == "agent-a" },
	}
	return rc, inst, deps
}

func crashActualSkill(t *testing.T, inst *agent.AgentInstance, ctx context.Context) *tools.ToolResult {
	t.Helper()
	tool, ok := inst.Tools.Get("Skill")
	require.True(t, ok, "actual startup registers Skill")
	return tool.Execute(ctx, map[string]any{"name": "foo"})
}

func crashVersionRows(t *testing.T, home string) []map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(home, "system", "skill-history", "foo", "versions", "*.json"))
	require.NoError(t, err)
	rows := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var row map[string]any
		require.NoError(t, json.Unmarshal(raw, &row))
		rows = append(rows, row)
	}
	return rows
}

func crashInner(t *testing.T, home, operation string) {
	t.Helper()
	rc, inst, deps := crashBoot(t, home)
	meta, err := rc.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", inst.ID)
	require.NoError(t, err)
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), inst.ID), meta.ID)
	first := crashActualSkill(t, inst, ctx)
	require.False(t, first.IsError)
	require.Contains(t, first.ForLLM, `revision="`, "BLOCKED: actual activation must establish revision/seen — r15 §7.3")
	control := systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "control.md"})
	require.False(t, control.IsError, "positive control: actual Skill established shared session seen: %s", control.ForLLM)
	rows := crashVersionRows(t, home)
	require.NotEmpty(t, rows, "real activation/mutation must commit history")
	root, live := filepath.Join(home, "skills"), filepath.Join(home, "skills", "foo", "SKILL.md")
	rev, err := skills.ContentRevision(filepath.Dir(live))
	require.NoError(t, err, "BLOCKED: ContentRevision required by §7.3")
	var matching []string
	for _, row := range rows {
		if row["revision"] == rev {
			id, ok := row["id"].(string)
			require.True(t, ok)
			matching = append(matching, id)
		}
	}
	require.Len(t, matching, 1, "restore target is the actual committed A observation after the control, not a guessed or lexically newest id")
	vA := matching[0]
	_, err = skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindEdit, Slug: "foo", Path: "SKILL.md", Content: []byte(crashSkillMD("DESCRIPTION-B")), Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "agent", AgentID: inst.ID, SessionID: meta.ID, Tool: "write_file"}})
	require.NoError(t, err)
	healthy := crashActualSkill(t, inst, ctx)
	require.False(t, healthy.IsError)
	require.Contains(t, inst.ContextBuilder.BuildSystemPromptWithCache(), "DESCRIPTION-B", "warm the real old menu before the interrupted change")
	rev, err = skills.ContentRevision(filepath.Dir(live))
	require.NoError(t, err)
	receipt := map[string]any{"session": meta.ID, "versions_before": len(crashVersionRows(t, home)), "operation": operation}
	raw, err := json.Marshal(receipt)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "crash-receipt.json"), raw, 0o600))
	wantLive := crashSkillMD("DESCRIPTION-C")
	if operation == "restore" {
		wantLive = crashSkillMD("DESCRIPTION-A")
	}
	old := skills.FaultManifestWrite
	defer func() { skills.FaultManifestWrite = old }() // deliberately NOT run by os.Exit
	skills.FaultManifestWrite = func(path string) error {
		if filepath.Dir(path) != filepath.Join(home, "system", "skill-history", "foo", "versions") {
			return nil
		}
		body, readErr := os.ReadFile(live)
		published := readErr == nil && string(body) == wantLive
		if operation == "delete" {
			_, swapErr := os.Stat(filepath.Join(root, ".swap", "foo", "SKILL.md"))
			published = os.IsNotExist(readErr) && swapErr == nil
		}
		if published {
			os.Exit(skillsCrashExit)
		} // exact verified stage, process death, no rollback defers
		return nil
	}
	if operation == "edit" {
		_, err = skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindEdit, Slug: "foo", Path: "SKILL.md", Content: []byte(wantLive), Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "agent", AgentID: inst.ID, SessionID: meta.ID, Tool: "write_file"}})
	} else {
		args := map[string]any{"action": operation, "skill": "foo"}
		if operation == "restore" {
			args["version"] = vA
		}
		res := systools.NewSkillEditTool(deps).Execute(ctx, args)
		t.Fatalf("BLOCKED: verified post-publication manifest boundary not reached — r15 §7.6/%s; actual=%s", operation, res.ForLLM)
	}
	t.Fatalf("BLOCKED: verified post-publication manifest boundary not reached — r15 §7.6/%s; err=%v", operation, err)
}

func TestSkillsCrash_ActualRestartMenuSeenDetectionAndAudit(t *testing.T) {
	if slices.Contains(os.Args, "--skills-crash-inner") {
		crashInner(t, os.Getenv("OMNIPUS_CHILD_SKILLS_CRASH_HOME"), os.Getenv("OMNIPUS_CHILD_SKILLS_CRASH_OPERATION"))
		return
	}
	for _, operation := range []string{"edit", "restore", "delete"} {
		t.Run(operation, func(t *testing.T) {
			home, cfg := bootFixture(t)
			dir := filepath.Join(home, "skills", "foo")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(crashSkillMD("DESCRIPTION-A")), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("KEEP UNTIL FRESH READ\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "control.md"), []byte("seen positive control\n"), 0o644))
			const generation = "11111111-2222-3333-4444-555555555555"
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte(generation+"\n"), 0o644))
			_, err := agentstore.New(home).Update("agent-a", func(a *config.AgentConfig) error { a.Skills = []string{"foo"}; return nil })
			require.NoError(t, err)
			path := filepath.Join(home, "entities", "agents", "agent-a.json")
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var agentRow map[string]any
			require.NoError(t, json.Unmarshal(raw, &agentRow))
			agentRow["skill_grants"] = map[string]string{"foo": generation} // explicit construction-time binding fixture
			raw, err = json.Marshal(agentRow)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0o600))
			require.NoError(t, json.Unmarshal(raw, &cfg.Agents.List[0]))
			require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
			exe, err := os.Executable()
			require.NoError(t, err)
			cmd := exec.Command(exe, "-test.run=^TestSkillsCrash_ActualRestartMenuSeenDetectionAndAudit$", "-test.v", "--", "--skills-crash-inner")
			cmd.Env = append(sandbox.ScrubGatewayEnv(), "OMNIPUS_CHILD_SKILLS_CRASH_HOME="+home, "OMNIPUS_CHILD_SKILLS_CRASH_OPERATION="+operation)
			output, err := cmd.CombinedOutput()
			t.Logf("actual crash receipt:\n%s", output)
			var exited *exec.ExitError
			require.ErrorAs(t, err, &exited)
			require.Equal(t, skillsCrashExit, exited.ExitCode(), "BLOCKED before verified crash stage is not crash proof; child output above")
			crashVerifyRestart(t, home, operation)
		})
	}
}

func crashVerifyRestart(t *testing.T, home, operation string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "crash-receipt.json"))
	require.NoError(t, err)
	var receipt map[string]any
	require.NoError(t, json.Unmarshal(raw, &receipt))
	rc, inst, deps := crashBoot(t, home) // actual boot must recover BEFORE constructing inst
	want := "DESCRIPTION-C"
	if operation == "restore" {
		want = "DESCRIPTION-A"
	}
	if operation == "delete" {
		want = "DESCRIPTION-B"
	} // uncommitted deletion recovers its only live copy
	body, err := os.ReadFile(filepath.Join(home, "skills", "foo", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, crashSkillMD(want), string(body))
	assert.NoDirExists(t, filepath.Join(home, "skills", ".swap", "foo"), "actual startup disposed/recovered the interrupted swap")
	assert.Contains(t, inst.ContextBuilder.BuildSystemPromptWithCache(), want, "first real menu reflects recovered live state")
	sessionID, sessionOK := receipt["session"].(string)
	require.True(t, sessionOK, "actual crash receipt includes its string session id")
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), inst.ID), sessionID)
	edit := systools.NewSkillEditTool(deps)
	unread := edit.Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"})
	assert.True(t, unread.IsError)
	assert.Contains(t, unread.ForLLM, "SKILL_NOT_READ", "restart cannot retain the dead process's seen record")
	fresh := crashActualSkill(t, inst, ctx)
	require.False(t, fresh.IsError)
	rows := crashVersionRows(t, home)
	versionsBefore, countOK := receipt["versions_before"].(float64)
	require.True(t, countOK, "actual crash receipt includes its numeric history count")
	before := int(versionsBefore)
	if operation != "delete" {
		require.Len(t, rows, before+1, "next actual load records exactly one external_change, not a phantom committed mutation")
		var detections []map[string]any
		for _, row := range rows {
			if row["kind"] == "external_change" {
				detections = append(detections, row)
			}
		}
		require.Len(t, detections, 1)
		liveRevision, err := skills.ContentRevision(filepath.Join(home, "skills", "foo"))
		require.NoError(t, err)
		assert.Equal(t, liveRevision, detections[0]["revision"], "detection certifies the recovered live observation")
	} else {
		assert.Len(t, rows, before, "recovery restored the committed predecessor; no invented delete version")
	}
	retry := edit.Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"})
	require.False(t, retry.IsError, "fresh explicit read is a real recovery path: %s", retry.ForLLM)
	assert.NoFileExists(t, filepath.Join(home, "skills", "foo", "notes.md"))
	require.NoError(t, rc.agentLoop.AuditLogger().Close())
	foundWrite, foundDetection := false, false
	for _, row := range bootAuditRows(t, filepath.Join(home, "system")) {
		if row.Event == "skill.write" && row.Details["slug"] == "foo" {
			if row.Details["kind"] == "delete_file" && row.Details["outcome"] == "committed" {
				foundWrite = true
			}
			if row.Details["kind"] == "external_change" {
				foundDetection = true
			}
		}
	}
	assert.True(t, foundWrite, "positive control: normal post-restart write reached the real audit file")
	if operation != "delete" {
		assert.True(t, foundDetection, "post-crash actual load emitted external_change audit")
	}
}
