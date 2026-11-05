package agent

// CHECK1 CK2 (skills spec r15 FR-053, S-10.1/E-4/S-3.5): the session skill menu
// is frozen for a session, but a COMMITTED skill change (the skills generation
// moves) must show in the next turn's menu. Real loop, real session cache, real
// skills pipeline; only the model is scripted.
//
// Failability: before commit 9c6c750e7 nothing read skills.Generation(), so the
// frozen menu (and the system-prompt cache) kept the old description and kept a
// deleted skill listed; the "committed change" and "deletion" steps below fail
// there. The outside-change control passes both before and after the fix.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ck2Skill(slug, description string) []byte {
	return []byte("---\nname: " + slug + "\ndescription: " + description + "\n---\n\nBody of " + slug + ".\n")
}

func ck2Create(t *testing.T, root, slug, description string) string {
	t.Helper()
	out, err := skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindCreate, Slug: slug, Content: ck2Skill(slug, description),
		Expect: skills.Expectation{Absent: true},
		Actor:  skills.ChangeActor{Kind: "person", Tool: "rest-create"},
	})
	require.NoError(t, err)
	return out.Revision
}

// ck2SystemPrompt is the system message of the model's i-th request.
func ck2SystemPrompt(t *testing.T, provider *testutil.ScenarioProvider, i int) string {
	t.Helper()
	requests := provider.AllRequests()
	require.Greater(t, len(requests), i, "the model must have received request %d", i)
	require.NotEmpty(t, requests[i])
	require.Equal(t, "system", requests[i][0].Role)
	return requests[i][0].Content
}

// ck2Loop builds a real loop whose agent "mia" is granted the given skills.
func ck2Loop(t *testing.T, home string, granted []string, provider *testutil.ScenarioProvider) *AgentLoop {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.List = []config.AgentConfig{{
		ID: "mia", Name: "Mia", Home: filepath.Join(home, "agents", "mia"), Skills: granted,
	}}
	store := agentstore.New(home)
	require.NoError(t, store.Create("mia", &cfg.Agents.List[0]))
	bound, err := store.Get("mia") // the persisted record carries the identity-bound grants
	require.NoError(t, err)
	cfg.Agents.List[0] = *bound
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	return al
}

func TestSessionSkillMenu_RefreshesAfterCommittedSkillChange(t *testing.T) {
	home := filepath.Join(t.TempDir(), "omnipus")
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	root := filepath.Join(home, "skills")

	alphaRev := ck2Create(t, root, "menu-alpha", "ALPHA-DESCRIPTION-ONE")
	ck2Create(t, root, "menu-beta", "BETA-DESCRIPTION-ONE")

	provider := testutil.NewScenario().WithText("one").WithText("two").WithText("three")
	al := ck2Loop(t, home, []string{"menu-alpha", "menu-beta"}, provider)

	const session = "ck2-session"
	turn := func(text string) {
		t.Helper()
		_, perr := al.ProcessDirectAs(context.Background(), "mia", text, session)
		require.NoError(t, perr)
	}

	turn("first")
	first := ck2SystemPrompt(t, provider, 0)
	require.Contains(t, first, "ALPHA-DESCRIPTION-ONE", "positive control: the menu advertises the granted skill")
	require.Contains(t, first, "menu-beta", "positive control: both granted skills are listed")

	// Committed change: the description changes through the pipeline.
	_, err := skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindEdit, Slug: "menu-alpha", Path: "SKILL.md",
		Content: ck2Skill("menu-alpha", "ALPHA-DESCRIPTION-TWO"),
		Expect:  skills.Expectation{Revision: alphaRev},
		Actor:   skills.ChangeActor{Kind: "person", Tool: "rest-edit"},
	})
	require.NoError(t, err)
	turn("second")
	second := ck2SystemPrompt(t, provider, 1)
	assert.Contains(t, second, "ALPHA-DESCRIPTION-TWO", "FR-053: the next turn's menu reflects the committed change")
	assert.NotContains(t, second, "ALPHA-DESCRIPTION-ONE", "the old description is gone from the menu")

	// Committed deletion: the menu drops the skill on the next turn.
	betaRev, err := skills.ContentRevision(filepath.Join(root, "menu-beta"))
	require.NoError(t, err)
	_, err = skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindDelete, Slug: "menu-beta", Expect: skills.Expectation{Revision: betaRev},
		Actor: skills.ChangeActor{Kind: "person", Tool: "rest-delete"},
	})
	require.NoError(t, err)
	turn("third")
	third := ck2SystemPrompt(t, provider, 2)
	assert.NotContains(t, third, "menu-beta", "FR-053: the menu drops a deleted skill next turn")
	assert.Contains(t, third, "ALPHA-DESCRIPTION-TWO", "the surviving skill is still advertised")
}

// An outside change (no committed skill change, no generation bump) leaves the
// session's frozen menu exactly as it was (FR-009 prefix stability).
func TestSessionSkillMenu_OutsideDiskChangeDoesNotRefreeze(t *testing.T) {
	home := filepath.Join(t.TempDir(), "omnipus")
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	root := filepath.Join(home, "skills")
	ck2Create(t, root, "menu-alpha", "ALPHA-DESCRIPTION-ONE")

	provider := testutil.NewScenario().WithText("one").WithText("two")
	al := ck2Loop(t, home, []string{"menu-alpha"}, provider)

	_, err := al.ProcessDirectAs(context.Background(), "mia", "first", "ck2-outside")
	require.NoError(t, err)
	require.Contains(t, ck2SystemPrompt(t, provider, 0), "ALPHA-DESCRIPTION-ONE")

	require.NoError(t, os.WriteFile(filepath.Join(root, "menu-alpha", "SKILL.md"), ck2Skill("menu-alpha", "ALPHA-OUTSIDE-EDIT"), 0o644))
	_, err = al.ProcessDirectAs(context.Background(), "mia", "second", "ck2-outside")
	require.NoError(t, err)
	second := ck2SystemPrompt(t, provider, 1)
	assert.Contains(t, second, "ALPHA-DESCRIPTION-ONE", "an outside edit leaves the frozen menu as it was")
	assert.NotContains(t, second, "ALPHA-OUTSIDE-EDIT")
}
