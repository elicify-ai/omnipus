// Omnipus — System Agent Skill Authoring Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validSkill(name string) string {
	return "---\nname: " + name + "\ndescription: A valid authored skill with a long enough description to pass.\n---\n\n# " + name + "\n\nDoes the thing.\n"
}

func newAuthoringDeps(t *testing.T, globalDir, builtinDir string) *systools.Deps {
	t.Helper()
	deps, _ := newTestDeps(t)
	deps.SkillWriter = skills.NewSkillWriter(globalDir)
	deps.SkillsLoader = skills.NewSkillsLoader("", globalDir, builtinDir)
	return deps
}

// This helper invokes the supported real reader; it never manufactures a
// revision or injects the loop's approval marker. Only the policy edge is fake.
func observeSkillForMutation(t *testing.T, deps *systools.Deps, ctx context.Context, slug string) context.Context {
	t.Helper()
	if deps.ResolveToolPolicy == nil {
		deps.ResolveToolPolicy = func(_, _ string) (string, bool) { return "allow", true }
	}
	result := systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": slug, "fields": []any{"instructions"}})
	wu3Success(t, result)
	return ctx
}

// Only the model and the human decision are fakes. File tools, composition,
// dispatch, persisted enclosing mount, change pipeline and history are real.
func runSkillAuthoringLoop(t *testing.T, home string, provider *testutil.ScenarioProvider, approver agent.PolicyApprover) {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxToolIterations = 20
	cfg.Sandbox.AutoApprove = false
	policies := map[string]config.ToolPolicy{}
	for _, name := range []string{"write_file", "read_file", "ToolSearch", "get_skill"} {
		cfg.Sandbox.ToolPolicies[name] = "allow"
		policies[name] = config.ToolPolicyAllow
	}
	cfg.Sandbox.ToolPolicies["edit_skill"] = "ask"
	policies["edit_skill"] = config.ToolPolicyAsk
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia", Home: filepath.Join(home, "agents", "mia"), Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: policies}}}}
	require.NoError(t, agentstore.New(home).Create("mia", &cfg.Agents.List[0]))
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	// NewAgentLoop does not seed workspace membership; persist the real fixture.
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "authoring", Name: "Authoring", Status: "active", CoreTeam: []string{"mia"}}))
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	loop, err := agent.NewAgentLoop(cfg, mb, provider)
	require.NoError(t, err)
	t.Cleanup(loop.Close)
	wsID, ok := workspace.FindForAgent(home, "mia")
	require.True(t, ok, "real workspace membership control")
	_, _, err = workspace.CreateMount(home, wsID, "enclosing", filepath.Dir(home))
	require.NoError(t, err)
	loop.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: func() *config.Config { return cfg }, SkillWriter: skills.NewSkillWriter(filepath.Join(home, "skills")), SkillsLoader: skills.NewSkillsLoader("", filepath.Join(home, "skills"), ""), ResolveToolPolicy: loop.ResolveRegisteredToolPolicy})
	policy, current := loop.ResolveRegisteredToolPolicy("mia", "edit_skill")
	require.True(t, current, "real gateway policy callback resolves the live actor")
	require.Equal(t, "ask", policy, "reader and writer share actual editor authority")
	loop.SetToolApprover(approver)
	final, err := loop.ProcessDirectAs(context.Background(), "mia", "perform the scripted skill changes", t.Name())
	require.NoError(t, err)
	require.Equal(t, "done", final)
}

func authoringCall(t *testing.T, p *testutil.ScenarioProvider, name string, args map[string]any) {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	// WithToolCall reuses <name>-0; consecutive writes need distinct IDs
	// so both actual results remain independently observable.
	p.WithToolCalls([]providers.ToolCall{{ID: name + "-" + strconv.Itoa(p.Remaining()), Function: &providers.FunctionCall{Name: name, Arguments: string(raw)}}})
}

func authoringResult(t *testing.T, p *testutil.ScenarioProvider, id string) string {
	t.Helper()
	for _, m := range p.LastMessages() {
		if m.Role == "tool" && m.ToolCallID == id {
			return m.Content
		}
	}
	t.Fatalf("missing actual tool result %s", id)
	return ""
}

type skillDecisionRecorder struct {
	allow    bool
	requests []agent.PolicyApprovalReq
}

func (r *skillDecisionRecorder) RequestApproval(_ context.Context, req agent.PolicyApprovalReq) (bool, string, bool) {
	r.requests = append(r.requests, req)
	if r.allow {
		return true, "", false
	}
	return false, "user", false
}

// r15 §§5.5/5.11 replace the retired create/content-edit verbs: both writes
// go through the real loop and each resulting state is retained. Restoration
// proves the first state's bytes are recoverable, not merely a version count.
func TestSkillFileAuthoring_WritesAndVersions(t *testing.T) {
	home := filepath.Join(t.TempDir(), "omnipus")
	root := filepath.Join(home, "skills")
	path := filepath.Join(root, "my-skill", "SKILL.md")
	original := validSkill("my-skill")
	edited := original + "\nEdited.\n"
	p := testutil.NewScenario()
	authoringCall(t, p, "write_file", map[string]any{"path": path, "content": original})
	authoringCall(t, p, "write_file", map[string]any{"path": path, "content": edited, "overwrite": true})
	p.WithText("done")
	approver := &skillDecisionRecorder{allow: true}
	runSkillAuthoringLoop(t, home, p, approver)
	require.Len(t, approver.requests, 2, "one ordinary card for each composed skill write")
	assert.Contains(t, authoringResult(t, p, "write_file-0"), "saved as skill version ")
	assert.Contains(t, authoringResult(t, p, "write_file-1"), "saved as skill version ")
	assert.Equal(t, edited, string(wu3ReadFile(t, path)))
	obs, err := skills.ObserveSkill(root, "my-skill", skills.VersionQuery{Want: true, Limit: 50})
	require.NoError(t, err)
	require.Len(t, obs.Versions, 2, "create and edit each record resulting state (§5.11)")
	assert.Equal(t, "edit", obs.Versions[0].Kind)
	assert.Equal(t, "create", obs.Versions[1].Kind)
	deps := newAuthoringDeps(t, root, "")
	ctx := observeSkillForMutation(t, deps, readCtx("editor"), "my-skill")
	wu3Success(t, systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "restore", "skill": "my-skill", "version": obs.Versions[1].ID}))
	assert.Equal(t, original, string(wu3ReadFile(t, path)), "the entire creation snapshot remains recoverable")
}

// §5.4/§8.2 supersede implicit built-in overrides: explicit copy, no mutation
// or history for the original. The copy has the declared name transform.
func TestSkillEditTool_BuiltinReadOnlyRequiresExplicitCopy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	_, err := skills.SyncBuiltins(root, nil)
	require.NoError(t, err)
	original := wu3ReadFile(t, filepath.Join(root, "plan", "SKILL.md"))
	deps := newAuthoringDeps(t, root, "")
	ctx := observeSkillForMutation(t, deps, readCtx("editor"), "plan")
	result := systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete", "skill": "plan"})
	body := wu3Error(t, result, "BUILTIN_READ_ONLY")
	assert.Equal(t, "'plan' is a built-in skill. Built-in skills are read-only and are replaced with the shipped version on every Omnipus update, so this change was not saved. Copy it first: edit_skill action=copy skill=plan to=<new-name>, then edit the copy and grant it to the agents that should use it.", body["message"])
	wu3Success(t, systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "copy", "skill": "plan", "to": "my-plan"}))
	assert.Equal(t, strings.Replace(string(original), "name: plan", "name: my-plan", 1), string(wu3ReadFile(t, filepath.Join(root, "my-plan", "SKILL.md"))))
	assert.Equal(t, original, wu3ReadFile(t, filepath.Join(root, "plan", "SKILL.md")))
	assert.NoDirExists(t, filepath.Join(skills.SkillHistoryRoot(root), "plan"))
}

func TestSkillCopyTool_TraversalRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	validSkillFixture(t, root, "source", "A copy source")
	deps := newAuthoringDeps(t, root, "")
	deps.ResolveToolPolicy = func(_, _ string) (string, bool) { return "allow", true }
	result := systools.NewSkillEditTool(deps).Execute(readCtx("editor"), map[string]any{"action": "copy", "skill": "source", "to": "../escape"})
	wu3Error(t, result, "INVALID_INPUT")
	assert.NoDirExists(t, filepath.Join(filepath.Dir(root), "escape"))
	assert.Equal(t, "---\nname: source\ndescription: A copy source\n---\n\nBody of source.\n", string(wu3ReadFile(t, filepath.Join(root, "source", "SKILL.md"))))
}

// §7.2 rejects an oversized creation before persistence. This uses the real
// pipeline, not the removed tool or a test-owned validation wrapper.
func TestSkillAuthoringPipeline_OversizeRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	content := validSkill("big") + strings.Repeat("A", skills.MaxSkillMarkdownBytes+1)
	out, err := skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindCreate, Slug: "big", Content: []byte(content), Expect: skills.Expectation{Absent: true}})
	require.ErrorIs(t, err, skills.ErrSaveCheckFailed)
	assert.Equal(t, skills.PersistenceNone, out.PersistenceStatus)
	assert.NoDirExists(t, filepath.Join(root, "big"))
}

func TestSkillCopyTool_NilDeps_ReturnsNotAvailable(t *testing.T) {
	result := systools.NewSkillEditTool(nil).Execute(context.Background(), map[string]any{"action": "copy", "skill": "source", "to": "own-copy"})
	wu3Error(t, result, "NOT_AVAILABLE")
}

// This replaces the old test-owned conditional dispatch with actual loop
// enforcement. Both create and edit must stay byte-identical after Deny; the
// approved counterparts must write, proving a blanket refusal cannot pass.
func TestSkillAuthoring_ConsentFlow_DenyBlocksExecute(t *testing.T) {
	for _, operation := range []string{"create", "edit"} {
		for _, allow := range []bool{false, true} {
			t.Run(operation+"/"+map[bool]string{false: "deny", true: "allow"}[allow], func(t *testing.T) {
				home := filepath.Join(t.TempDir(), "omnipus")
				root := filepath.Join(home, "skills")
				path := filepath.Join(root, "consent", "SKILL.md")
				original := validSkill("consent")
				changed := original + "\nChanged after approval.\n"
				p := testutil.NewScenario()
				if operation == "edit" {
					// No separator blank line: the projected body is unambiguous;
					// this consent test does not invent a whitespace-trimming rule.
					original = "---\nname: consent\ndescription: A consent fixture\n---\nBody of consent.\n"
					require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
					require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
					authoringCall(t, p, "ToolSearch", map[string]any{"names": []any{"get_skill"}})
					authoringCall(t, p, "get_skill", map[string]any{"id": "consent", "fields": []any{"instructions"}})
				}
				args := map[string]any{"path": path, "content": changed}
				if operation == "edit" {
					args["overwrite"] = true
				}
				authoringCall(t, p, "write_file", args)
				p.WithText("done")
				recorder := &skillDecisionRecorder{allow: allow}
				runSkillAuthoringLoop(t, home, p, recorder)
				if operation == "edit" {
					var read map[string]any
					require.NoError(t, json.Unmarshal([]byte(authoringResult(t, p, "get_skill-1")), &read))
					require.NotContains(t, read, "error", "edit fixture must first observe the real skill")
					require.Equal(t, "consent", read["id"], "§5.2 identity key identifies the actual observed skill")
					require.Equal(t, "Body of consent.\n", read["instructions"], "§5.2 returns exact fixture body without frontmatter")
				}
				require.Len(t, recorder.requests, 1, "exactly one ordinary approval, no second ritual")
				assert.Equal(t, "write_file", recorder.requests[0].ToolName)
				assert.Equal(t, args, recorder.requests[0].Args)
				if allow {
					assert.Equal(t, changed, string(wu3ReadFile(t, path)))
				} else if operation == "edit" {
					assert.Equal(t, original, string(wu3ReadFile(t, path)))
				} else {
					assert.NoFileExists(t, path)
				}
			})
		}
	}
}
