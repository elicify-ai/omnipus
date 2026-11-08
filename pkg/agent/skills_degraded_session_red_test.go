package agent

// r15 §9.5/§7.3, FR-022/064/065, S-5.20; CHECK-14.
// Real registered Skill and its loop's real session store. The reader/writer
// adapters share that exact store/root; their allow-policy answer is the only
// injected edge. This does not claim gateway registration of those adapters.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sessionSkillFixture(t *testing.T) (string, *AgentLoop, *AgentInstance, *systools.Deps, context.Context) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	root := filepath.Join(home, "skills")
	dir := menuSkill(t, root, "foo", "session fixture", menuG1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "control.md"), []byte("CONTROL\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("KEEP UNTIL FRESH READ\n"), 0o644))
	a := &config.AgentConfig{ID: "menu-agent", Name: "Menu Agent", Home: filepath.Join(home, "agents", "menu-agent"), Skills: []string{"foo"}}
	// Persist the same construction-time grant fixture; bind-at-persist itself
	// is independently exercised by the agentstore tests.
	require.NoError(t, agentstore.New(home).Create("menu-agent", a))
	p := filepath.Join(home, "entities", "agents", "menu-agent.json")
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	var row map[string]any
	require.NoError(t, json.Unmarshal(raw, &row))
	row["skill_grants"] = map[string]string{"foo": menuG1}
	raw, err = json.Marshal(row)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, raw, 0o600))
	al, inst := sd30LoopFromDurableRow(t, home)
	require.NotNil(t, al.GetSessionStore())
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", inst.ID)
	require.NoError(t, err)
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), inst.ID), meta.ID)
	deps := &systools.Deps{
		Home: home, GetCfg: al.GetConfig,
		SkillsLoader:       skills.NewSkillsLoader("", root, ""),
		SkillWriter:        skills.NewSkillWriter(root),
		DeleteSessionStore: func() systools.AgentSessionStore { return al.GetSessionStore() },
		ResolveToolPolicy:  func(id, _ string) (string, bool) { return "allow", id == inst.ID },
	}
	return dir, al, inst, deps, ctx
}

func registeredSessionSkill(t *testing.T, inst *AgentInstance, ctx context.Context) *tools.ToolResult {
	t.Helper()
	tool, ok := inst.Tools.Get("Skill")
	require.True(t, ok, "real AgentLoop must register Skill")
	return tool.Execute(ctx, map[string]any{"name": "foo"})
}

func TestSkillDegradedActivation_ClearsSeenAndMarksLocal(t *testing.T) {
	dir, al, inst, deps, ctx := sessionSkillFixture(t)
	edit := systools.NewSkillEditTool(deps)
	first := registeredSessionSkill(t, inst, ctx)
	require.False(t, first.IsError, "healthy actual activation succeeds: %s", first.ForLLM)
	require.Contains(t, first.ForLLM, `revision="`, "BLOCKED: healthy activation/seen binding not implemented — r15 §5.7/§7.3")
	control := edit.Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "control.md"})
	require.False(t, control.IsError, "positive control: Skill established real seen in the shared store: %s", control.ForLLM)
	require.NoFileExists(t, filepath.Join(dir, "control.md"))
	healthy := registeredSessionSkill(t, inst, ctx)
	require.False(t, healthy.IsError)
	rev, err := skills.ContentRevision(dir)
	require.NoError(t, err, "BLOCKED: ContentRevision required by r15 §7.3")
	before, err := filepath.Glob(filepath.Join(skills.SkillHistoryRoot(filepath.Dir(dir)), "foo", "versions", "*.json"))
	require.NoError(t, err)
	huge := filepath.Join(dir, "huge.bin")
	require.NoError(t, os.WriteFile(huge, []byte(strings.Repeat("H", 10485761)), 0o644)) // §7.2 per-file limit + 1
	degraded := registeredSessionSkill(t, inst, ctx)
	require.False(t, degraded.IsError, "bounded instructions survive an uncheckpointable helper: %s", degraded.ForLLM)
	assert.Contains(t, degraded.ForLLM, "Body.")
	assert.Contains(t, degraded.ForLLM, "last change: unknown (changed outside Omnipus)")
	assert.NotContains(t, degraded.ForLLM, `revision="`, "aborted capture certifies no revision")
	assert.NotContains(t, degraded.ForLLM, `version="`, "aborted capture records no version")
	after, err := filepath.Glob(filepath.Join(skills.SkillHistoryRoot(filepath.Dir(dir)), "foo", "versions", "*.json"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "degraded activation cannot publish a manifest")
	other, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", inst.ID)
	require.NoError(t, err)
	otherCtx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), inst.ID), other.ID)
	flags := systools.NewGetSkillTool(deps).Execute(otherCtx, map[string]any{"id": "foo", "fields": []any{"has_local_changes"}})
	require.False(t, flags.IsError, "actual shaped reader returns degraded local-change state: %s", flags.ForLLM)
	var projection map[string]any
	require.NoError(t, json.Unmarshal([]byte(flags.ForLLM), &projection))
	assert.Equal(t, true, projection["has_local_changes"])
	// A different reader session cannot accidentally refresh the tested seen.
	require.NoError(t, os.Remove(huge))
	recovered, err := skills.ContentRevision(dir)
	require.NoError(t, err)
	require.Equal(t, rev, recovered, "equal-state recovery: a stale-revision refusal cannot substitute for cleared seen")
	refused := edit.Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"})
	require.True(t, refused.IsError, "degraded activation cleared the earlier seen record")
	assert.Contains(t, refused.ForLLM, "SKILL_NOT_READ")
	assert.Contains(t, refused.ForLLM, "read skill 'foo' first (Skill, get_skill, read_file or list_directory), then retry")
	assert.FileExists(t, filepath.Join(dir, "notes.md"))
	fresh := registeredSessionSkill(t, inst, ctx)
	require.False(t, fresh.IsError)
	retry := edit.Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"})
	require.False(t, retry.IsError, "only a supported fresh read restores mutation readiness: %s", retry.ForLLM)
	assert.NoFileExists(t, filepath.Join(dir, "notes.md"))
}
