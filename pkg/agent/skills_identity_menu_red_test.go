package agent

// r15 SD-30, S-6.12/S-6.18/S-10.1; CHECK-12. The actual instance and
// ContextBuilder survive each mutation. No test calls InvalidateCache or
// replaces a resolver: the next prompt/resolution is the product observation.
import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const menuG1 = "11111111-2222-3333-4444-555555555555"
const menuG2 = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func menuSkill(t *testing.T, root, slug, description, generation string) string {
	t.Helper()
	dir := filepath.Join(root, slug)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+slug+"\ndescription: "+description+"\n---\nBody.\n"), 0o644))
	if generation != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte(generation+"\n"), 0o644))
	}
	return dir
}
func menuInstance(t *testing.T, home string, names []string, bindings map[string]string) *AgentInstance {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	a := config.AgentConfig{ID: "menu-agent", Name: "Menu Agent", Home: filepath.Join(home, "agents", "menu-agent"), Skills: names}
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(raw, &record))
	record["skill_grants"] = bindings
	raw, err = json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &a))
	cfg.Agents.List = []config.AgentConfig{a}
	inst := NewAgentInstance(&a, &cfg.Agents.Defaults, cfg, &mockProvider{})
	require.NoError(t, inst.initErr)
	require.NotNil(t, inst.ContextBuilder)
	t.Cleanup(func() { require.NoError(t, inst.Close()) })
	return inst
}

func TestSD30_ExistingInstanceRejectsRecreatedGeneration(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "skills")
	dir := menuSkill(t, root, "foo", "ORIGINAL-G1", menuG1)
	inst := menuInstance(t, home, []string{"FoO"}, map[string]string{"foo": menuG1})
	cb := inst.ContextBuilder
	_, ok := cb.ResolveSkillFullForWorkspace("", "foo")
	require.True(t, ok, "sensitivity control: original matching binding is loadable")
	require.Contains(t, cb.BuildSystemPromptWithCache(), "ORIGINAL-G1")
	// Outside replacement is an actual remove + creation of a different stamped
	// folder. No holder update/rebuild is requested; the old instance stays live.
	require.NoError(t, os.RemoveAll(dir))
	menuSkill(t, root, "foo", "RECREATED-G2", menuG2)
	_, ok = cb.ResolveSkillFullForWorkspace("", "foo")
	assert.False(t, ok, "G1-bound preexisting instance must not resolve G2 at next activation")
	assert.NotContains(t, cb.BuildSystemPromptWithCache(), "RECREATED-G2", "stale grant is absent from the next-turn menu")
	assert.Same(t, cb, inst.ContextBuilder, "no rebuild hid stale-instance behavior")
	// Reconstruction from the same binding snapshot is a separate control;
	// durable bind-at-persist is driven by the agentstore tests, not claimed here.
	reconstructed := menuInstance(t, home, []string{"foo"}, map[string]string{"foo": menuG1})
	_, ok = reconstructed.ContextBuilder.ResolveSkillFullForWorkspace("", "foo")
	assert.False(t, ok, "constructing another instance from a G1 binding cannot authorize G2")
	// Both halves of authorization: even the same generation cannot grant a
	// name not in the construction-time allowlist.
	menuSkill(t, root, "not-granted", "NOT-GRANTED", menuG1)
	_, ok = cb.ResolveSkillFullForWorkspace("", "not-granted")
	assert.False(t, ok)
}

func TestSD30_UnstampedLoadIsReadOnlyAndUnboundIsInert(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "skills")
	dir := menuSkill(t, root, "foo", "UNSTAMPED", "")
	inst := menuInstance(t, home, []string{"foo"}, nil)
	_, ok := inst.ContextBuilder.ResolveSkillFullForWorkspace("", "foo")
	assert.False(t, ok, "unstamped and unbound matches no grant")
	assert.NotContains(t, inst.ContextBuilder.BuildSystemPromptWithCache(), "UNSTAMPED")
	_, err := os.Stat(filepath.Join(dir, ".omnipus-identity"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "loader must never stamp; grant persistence owns external stamping")
}

func TestSkillMenu_ManagedEditVisibleOnNextTurnSameInstance(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "skills")
	dir := menuSkill(t, root, "foo", "DESCRIPTION-BEFORE", menuG1)
	inst := menuInstance(t, home, []string{"foo"}, map[string]string{"foo": menuG1})
	cb := inst.ContextBuilder
	before := cb.BuildSystemPromptWithCacheForWorkspace("")
	require.Contains(t, before, "DESCRIPTION-BEFORE")
	rev, err := skills.ContentRevision(dir)
	require.NoError(t, err, "BLOCKED: managed content revision not implemented — r15 S-10.1")
	out, err := skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindEdit, Slug: "foo", Path: "SKILL.md", Content: []byte("---\nname: foo\ndescription: DESCRIPTION-AFTER\n---\nBody.\n"), Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "agent", AgentID: inst.ID, SessionID: "menu-session", Tool: "write_file"}})
	require.NoError(t, err)
	assert.Equal(t, skills.PersistenceComplete, out.PersistenceStatus)
	after := cb.BuildSystemPromptWithCacheForWorkspace("")
	assert.Contains(t, after, "DESCRIPTION-AFTER")
	assert.NotContains(t, after, "DESCRIPTION-BEFORE")
	assert.Same(t, cb, inst.ContextBuilder, "the existing next-turn cache must refresh without a restart")
}
